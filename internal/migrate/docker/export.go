package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"ankra/internal/migrate"
)

// Option names the export verb accepts through --option key=value.
const (
	OptionDockerHost      = "docker-host" // DOCKER_HOST for the dump; ssh://user@host reaches a remote daemon
	OptionContainerPrefix = "container."  // container.<workload>=<name|id> dumps from a specific container
	OptionDatabasesPrefix = "databases."  // databases.<workload>=a,b dumps only those databases
)

// dockerExecutor runs the docker CLI. Output is for short answers; Stream is
// for dumps, which are written straight to disk and never buffered whole.
type dockerExecutor interface {
	Output(ctx context.Context, args ...string) ([]byte, error)
	Stream(ctx context.Context, stdout io.Writer, args ...string) error
}

// newDockerExecutor builds the executor for one export. Tests replace it.
var newDockerExecutor = func(host string) dockerExecutor { return dockerCLI{host: host} }

// dockerCommand builds a docker invocation, against a remote daemon when
// host is set. Reaching another machine through DOCKER_HOST=ssh://... is how
// docker itself does it, so no ssh handling lives here.
func dockerCommand(ctx context.Context, host string, args []string) *exec.Cmd {
	command := exec.CommandContext(ctx, "docker", args...)
	if host != "" {
		command.Env = append(os.Environ(), "DOCKER_HOST="+host)
	}
	return command
}

// dockerCLI is the executor that runs the real docker binary.
type dockerCLI struct{ host string }

func (c dockerCLI) Output(ctx context.Context, args ...string) ([]byte, error) {
	return runDocker(ctx, c.host, args...)
}

func (c dockerCLI) Stream(ctx context.Context, stdout io.Writer, args ...string) error {
	command := dockerCommand(ctx, c.host, args)
	var stderr bytes.Buffer
	command.Stdout = stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Export implements migrate.DataExporter. It finds every database service in
// the source, locates its running container, and dumps each database it
// serves through the docker CLI - which is the one tool guaranteed to be
// present wherever a compose stack runs, and works unchanged against a remote
// host through DOCKER_HOST.
func (Module) Export(ctx context.Context, request migrate.ExportRequest) (migrate.Export, error) {
	options := request.Options
	if options == nil {
		options = map[string]string{}
	}
	progress := request.Progress
	if progress == nil {
		progress = io.Discard
	}

	project, err := exportProject(ctx, request.Dir, options)
	if err != nil {
		return migrate.Export{}, err
	}
	namespace := request.Namespace
	if namespace == "" {
		namespace = sanitiseName(project.Name)
	}
	composeProject := options[OptionProject]
	if composeProject == "" {
		composeProject = project.Name
	}
	docker := newDockerExecutor(options[OptionDockerHost])

	var export migrate.Export
	var otherImages []string
	for _, workload := range project.SortedWorkloads() {
		engine, ok := DatabaseEngine(workload.Image)
		if !ok {
			if workload.Image != "" {
				otherImages = append(otherImages, workload.Name+"="+workload.Image)
			}
			continue
		}
		container, containerWarnings, err := findContainer(ctx, docker, composeProject, workload.Name, options[OptionContainerPrefix+workload.Name])
		if err != nil {
			return migrate.Export{}, err
		}
		export.Warnings = append(export.Warnings, containerWarnings...)

		job := dumpJob{
			docker:    docker,
			container: container,
			workload:  workload,
			namespace: namespace,
			outputDir: request.OutputDir,
			only:      splitList(options[OptionDatabasesPrefix+workload.Name]),
			progress:  progress,
		}
		var database migrate.DatabaseExport
		switch engine {
		case migrate.EnginePostgres:
			database, err = dumpPostgres(ctx, job)
		case migrate.EngineMySQL:
			database, err = dumpMySQL(ctx, job)
		}
		if err != nil {
			return migrate.Export{}, err
		}
		export.Databases = append(export.Databases, database)
	}

	if len(export.Databases) == 0 {
		return migrate.Export{}, fmt.Errorf("no database service recognised in %s (images: %s); the export knows PostgreSQL and MySQL/MariaDB images",
			project.Name, strings.Join(otherImages, ", "))
	}
	export.Warnings = append(export.Warnings, "each dump is a point-in-time snapshot of a live server; rehearse with the source running, then stop its writers and export once more right before the final cutover")
	export.Warnings = uniqueSorted(export.Warnings)
	return export, nil
}

// exportProject reads the source an export works from. Unlike convert, a
// project option does not switch to the daemon source: the compose file in
// the directory stays the description, and the option names the compose
// project the containers run under, which is what the dump has to find.
func exportProject(ctx context.Context, dir string, options map[string]string) (Project, error) {
	source := options[OptionSource]
	if source == "" {
		source = "daemon"
		if _, ok := FindComposeFile(dir); ok {
			source = "compose"
		}
	}
	loadOptions := make(map[string]string, len(options)+1)
	for key, value := range options {
		loadOptions[key] = value
	}
	loadOptions[OptionSource] = source
	project, _, err := loadProject(ctx, dir, loadOptions)
	return project, err
}

// findContainer resolves a compose service to the container that runs it,
// by the labels compose stamps, unless the user named one.
func findContainer(ctx context.Context, docker dockerExecutor, composeProject, service, explicit string) (string, []string, error) {
	if explicit != "" {
		return explicit, nil, nil
	}
	output, err := docker.Output(ctx, "ps", "-q",
		"--filter", "label="+labelComposeProject+"="+composeProject,
		"--filter", "label="+labelComposeService+"="+service)
	if err != nil {
		return "", nil, err
	}
	ids := strings.Fields(string(output))
	if len(ids) == 0 {
		return "", nil, fmt.Errorf("no running container for service %s in compose project %s (pass --option %s=<name> if the stack runs under another project name, or --option %s%s=<container> to name the container)",
			service, composeProject, OptionProject, OptionContainerPrefix, service)
	}
	var warnings []string
	if len(ids) > 1 {
		warnings = append(warnings, fmt.Sprintf("%s: %d containers run this service; dumped from %s", service, len(ids), ids[0]))
	}
	return ids[0], warnings, nil
}

// dumpJob is everything one database server's dump needs.
type dumpJob struct {
	docker    dockerExecutor
	container string
	workload  Workload
	namespace string
	outputDir string
	// only restricts the dump to these databases; empty means every one the
	// server reports.
	only     []string
	progress io.Writer
}

// inContainer builds a `docker exec` that runs program through the
// container's own shell, so a password the image needs for local connections
// comes from the container's environment and never crosses the host as a
// command-line argument.
func (job dumpJob) inContainer(passwordVariable, passwordKey, program string, args ...string) []string {
	script := passwordVariable + `="$` + passwordKey + `" exec ` + program + ` "$@"`
	return append([]string{"exec", job.container, "sh", "-c", script, "sh"}, args...)
}

// restoreTarget describes where convert put this server in the cluster.
func (job dumpJob) restoreTarget(engine, username, passwordKey string) migrate.RestoreTarget {
	target := migrate.RestoreTarget{
		Namespace: job.namespace,
		Host:      job.workload.Name,
		Port:      DefaultDatabasePort(engine),
		Username:  username,
	}
	if entry, ok := lookupEnv(job.workload, passwordKey); ok && entry.Secret {
		target.PasswordSecret = job.workload.Name + "-secrets"
		target.PasswordKey = passwordKey
	}
	return target
}

var postgresCustomMagic = []byte("PGDMP")

func dumpPostgres(ctx context.Context, job dumpJob) (migrate.DatabaseExport, error) {
	const passwordKey = "POSTGRES_PASSWORD"
	username := "postgres"
	if entry, ok := lookupEnv(job.workload, "POSTGRES_USER"); ok && entry.Value != "" {
		username = entry.Value
	}
	psql := func(query string) []string {
		return job.inContainer("PGPASSWORD", passwordKey, "psql", "-U", username, "-d", "postgres", "-tAc", query)
	}

	version, err := job.docker.Output(ctx, psql("SHOW server_version")...)
	if err != nil {
		return migrate.DatabaseExport{}, fmt.Errorf("%s: reading the server version: %w", job.workload.Name, err)
	}
	databases := job.only
	if len(databases) == 0 {
		output, err := job.docker.Output(ctx, psql("SELECT datname FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres' ORDER BY datname")...)
		if err != nil {
			return migrate.DatabaseExport{}, fmt.Errorf("%s: listing databases: %w", job.workload.Name, err)
		}
		databases = nonEmptyLines(string(output))
	}
	if len(databases) == 0 {
		return migrate.DatabaseExport{}, fmt.Errorf("%s: the server holds no database besides postgres", job.workload.Name)
	}

	export := migrate.DatabaseExport{
		Workload:      job.workload.Name,
		Engine:        migrate.EnginePostgres,
		Image:         job.workload.Image,
		ServerVersion: strings.TrimSpace(string(version)),
		Target:        job.restoreTarget(migrate.EnginePostgres, username, passwordKey),
	}

	_, _ = fmt.Fprintf(job.progress, "%s: dumping roles and globals\n", job.workload.Name)
	globals, err := job.writeArtifact(ctx, "globals.sql", job.inContainer("PGPASSWORD", passwordKey, "pg_dumpall", "-U", username, "--globals-only"), nil)
	if err != nil {
		return migrate.DatabaseExport{}, err
	}
	export.Artifacts = append(export.Artifacts, migrate.Artifact{Path: globals, Kind: migrate.ArtifactKindGlobals, Format: migrate.ArtifactFormatSQL})

	for _, database := range databases {
		_, _ = fmt.Fprintf(job.progress, "%s: dumping database %s\n", job.workload.Name, database)
		dump, err := job.writeArtifact(ctx, sanitiseName(database)+".dump", job.inContainer("PGPASSWORD", passwordKey, "pg_dump", "-U", username, "-Fc", "-d", database), postgresCustomMagic)
		if err != nil {
			return migrate.DatabaseExport{}, err
		}
		export.Artifacts = append(export.Artifacts, migrate.Artifact{Path: dump, Kind: migrate.ArtifactKindDatabase, Format: migrate.ArtifactFormatPostgresCustom, Database: database})
	}
	return export, nil
}

// Schemas every MySQL/MariaDB server carries; they are not the application's
// data and a restore must not overwrite them.
var mysqlSystemSchemas = map[string]bool{"information_schema": true, "performance_schema": true, "mysql": true, "sys": true}

func dumpMySQL(ctx context.Context, job dumpJob) (migrate.DatabaseExport, error) {
	passwordKey := "MYSQL_ROOT_PASSWORD"
	if _, ok := lookupEnv(job.workload, "MARIADB_ROOT_PASSWORD"); ok {
		passwordKey = "MARIADB_ROOT_PASSWORD"
	}
	const username = "root"
	client := func(query string) []string {
		return job.inContainer("MYSQL_PWD", passwordKey, "mysql", "-u"+username, "-N", "-e", query)
	}

	version, err := job.docker.Output(ctx, client("SELECT VERSION()")...)
	if err != nil {
		return migrate.DatabaseExport{}, fmt.Errorf("%s: reading the server version: %w", job.workload.Name, err)
	}
	databases := job.only
	if len(databases) == 0 {
		output, err := job.docker.Output(ctx, client("SHOW DATABASES")...)
		if err != nil {
			return migrate.DatabaseExport{}, fmt.Errorf("%s: listing databases: %w", job.workload.Name, err)
		}
		for _, database := range nonEmptyLines(string(output)) {
			if !mysqlSystemSchemas[database] {
				databases = append(databases, database)
			}
		}
	}
	if len(databases) == 0 {
		return migrate.DatabaseExport{}, fmt.Errorf("%s: the server holds no database besides the system schemas", job.workload.Name)
	}

	export := migrate.DatabaseExport{
		Workload:      job.workload.Name,
		Engine:        migrate.EngineMySQL,
		Image:         job.workload.Image,
		ServerVersion: strings.TrimSpace(string(version)),
		Target:        job.restoreTarget(migrate.EngineMySQL, username, passwordKey),
	}
	for _, database := range databases {
		_, _ = fmt.Fprintf(job.progress, "%s: dumping database %s\n", job.workload.Name, database)
		// --databases makes the dump create and select its schema, so a
		// restore is one `mysql < file` with nothing prepared by hand.
		dump, err := job.writeArtifact(ctx, sanitiseName(database)+".sql",
			job.inContainer("MYSQL_PWD", passwordKey, "mysqldump", "-u"+username, "--single-transaction", "--routines", "--triggers", "--events", "--databases", database), nil)
		if err != nil {
			return migrate.DatabaseExport{}, err
		}
		export.Artifacts = append(export.Artifacts, migrate.Artifact{Path: dump, Kind: migrate.ArtifactKindDatabase, Format: migrate.ArtifactFormatSQL, Database: database})
	}
	return export, nil
}

// writeArtifact streams one docker command into <outputDir>/<workload>/<file>
// and checks the result looks like a dump: an empty file, or a custom-format
// archive without its magic bytes, is a failed dump even when the command
// did not say so.
func (job dumpJob) writeArtifact(ctx context.Context, fileName string, args []string, magic []byte) (string, error) {
	relative := path.Join(sanitiseName(job.workload.Name), fileName)
	absolute := filepath.Join(job.outputDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return "", err
	}
	file, err := os.Create(absolute)
	if err != nil {
		return "", err
	}
	streamErr := job.docker.Stream(ctx, file, args...)
	closeErr := file.Close()
	if streamErr != nil {
		return "", fmt.Errorf("%s: %w", job.workload.Name, streamErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := checkDump(absolute, magic); err != nil {
		return "", fmt.Errorf("%s: %s: %w", job.workload.Name, relative, err)
	}
	return relative, nil
}

func checkDump(absolute string, magic []byte) error {
	file, err := os.Open(absolute)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, 16)
	read, err := file.Read(header)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if read == 0 {
		return errors.New("the dump is empty")
	}
	if len(magic) > 0 && !bytes.HasPrefix(header[:read], magic) {
		return fmt.Errorf("the dump does not start with the %s archive header", string(magic))
	}
	return nil
}

func lookupEnv(workload Workload, name string) (EnvVar, bool) {
	for _, entry := range workload.Env {
		if entry.Name == name {
			return entry, true
		}
	}
	return EnvVar{}, false
}

func nonEmptyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
