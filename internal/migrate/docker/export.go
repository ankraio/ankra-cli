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
		composeProject = composeProjectName(project.Name)
	}
	docker := newDockerExecutor(options[OptionDockerHost])
	writtenFiles := map[string]bool{}

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
			docker:       docker,
			container:    container,
			workload:     workload,
			namespace:    namespace,
			outputDir:    request.OutputDir,
			only:         splitList(options[OptionDatabasesPrefix+workload.Name]),
			progress:     progress,
			writtenFiles: writtenFiles,
		}
		var database migrate.DatabaseExport
		var databaseWarnings []string
		switch engine {
		case migrate.EnginePostgres:
			database, databaseWarnings, err = dumpPostgres(ctx, job)
		case migrate.EngineMySQL:
			database, err = dumpMySQL(ctx, job)
		}
		if err != nil {
			return migrate.Export{}, err
		}
		export.Databases = append(export.Databases, database)
		export.Warnings = append(export.Warnings, databaseWarnings...)
	}

	if len(export.Databases) == 0 {
		return migrate.Export{}, fmt.Errorf("no database service recognised in %s (images: %s); the export knows PostgreSQL and MySQL/MariaDB images",
			project.Name, strings.Join(otherImages, ", "))
	}
	export.Warnings = append(export.Warnings,
		"each dump is a point-in-time snapshot of a live server; rehearse with the source running, then stop its writers and export once more right before the final cutover",
		"the dumps hold the application's data and are written readable by you alone; delete the export directory once the restore has succeeded")
	export.Warnings = uniqueSorted(export.Warnings)
	return export, nil
}

// composeProjectName derives the project name compose labels containers
// with from a directory or `name:` value, the way compose itself does:
// lower-cased, every character outside [a-z0-9_-] dropped, and no leading
// '-' or '_'. Without this a source directory called "Aura Office" would be
// looked up as a project no container is labelled with.
func composeProjectName(name string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(name) {
		isLetterOrDigit := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
		if isLetterOrDigit || character == '_' || character == '-' {
			builder.WriteRune(character)
		}
	}
	return strings.TrimLeft(builder.String(), "-_")
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
	// writtenFiles is shared by every job of one export so two databases
	// whose names sanitise alike never overwrite each other.
	writtenFiles map[string]bool
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

// dumpPostgres dumps a PostgreSQL server: its roles (without their
// passwords - the restore connects as the target's own user, and a dump that
// re-set that password would lock the cluster out of its database) and every
// database that is not a template. The `postgres` database is the server's
// maintenance database and is left out, unless the image was told to keep
// the application's data there - which is what POSTGRES_DB, or its default
// of POSTGRES_USER, says.
func dumpPostgres(ctx context.Context, job dumpJob) (migrate.DatabaseExport, []string, error) {
	const passwordKey = "POSTGRES_PASSWORD"
	username := "postgres"
	if entry, ok := lookupEnv(job.workload, "POSTGRES_USER"); ok && entry.Value != "" {
		username = entry.Value
	}
	applicationDatabase := username
	if entry, ok := lookupEnv(job.workload, "POSTGRES_DB"); ok && entry.Value != "" {
		applicationDatabase = entry.Value
	}
	psql := func(query string) []string {
		return job.inContainer("PGPASSWORD", passwordKey, "psql", "-U", username, "-d", "postgres", "-tAc", query)
	}

	version, err := job.docker.Output(ctx, psql("SHOW server_version")...)
	if err != nil {
		return migrate.DatabaseExport{}, nil, fmt.Errorf("%s: reading the server version: %w", job.workload.Name, err)
	}
	var warnings []string
	databases := job.only
	if len(databases) == 0 {
		output, err := job.docker.Output(ctx, psql("SELECT datname FROM pg_database WHERE NOT datistemplate ORDER BY datname")...)
		if err != nil {
			return migrate.DatabaseExport{}, nil, fmt.Errorf("%s: listing databases: %w", job.workload.Name, err)
		}
		for _, database := range nonEmptyLines(string(output)) {
			if database == "postgres" && applicationDatabase != "postgres" {
				warnings = append(warnings, fmt.Sprintf("%s: the maintenance database postgres was not dumped; pass --option %s%s=postgres if the application keeps data in it",
					job.workload.Name, OptionDatabasesPrefix, job.workload.Name))
				continue
			}
			databases = append(databases, database)
		}
	}
	if len(databases) == 0 {
		return migrate.DatabaseExport{}, nil, fmt.Errorf("%s: the server holds no database besides postgres", job.workload.Name)
	}

	export := migrate.DatabaseExport{
		Workload:      job.workload.Name,
		Engine:        migrate.EnginePostgres,
		Image:         job.workload.Image,
		ServerVersion: strings.TrimSpace(string(version)),
		Target:        job.restoreTarget(migrate.EnginePostgres, username, passwordKey),
	}

	_, _ = fmt.Fprintf(job.progress, "%s: dumping roles and globals\n", job.workload.Name)
	globals, err := job.writeArtifact(ctx, "globals.sql", job.inContainer("PGPASSWORD", passwordKey, "pg_dumpall", "-U", username, "--globals-only", "--no-role-passwords"), nil)
	if err != nil {
		return migrate.DatabaseExport{}, nil, err
	}
	export.Artifacts = append(export.Artifacts, migrate.Artifact{Path: globals, Kind: migrate.ArtifactKindGlobals, Format: migrate.ArtifactFormatSQL})
	warnings = append(warnings, fmt.Sprintf("%s: roles are restored without their passwords; %s keeps the password from the cluster's secret, any other role that logs in needs ALTER ROLE ... PASSWORD after the restore",
		job.workload.Name, username))

	for _, database := range databases {
		_, _ = fmt.Fprintf(job.progress, "%s: dumping database %s\n", job.workload.Name, database)
		dump, err := job.writeArtifact(ctx, sanitiseName(database)+".dump", job.inContainer("PGPASSWORD", passwordKey, "pg_dump", "-U", username, "-Fc", "-d", database), postgresCustomMagic)
		if err != nil {
			return migrate.DatabaseExport{}, nil, err
		}
		export.Artifacts = append(export.Artifacts, migrate.Artifact{Path: dump, Kind: migrate.ArtifactKindDatabase, Format: migrate.ArtifactFormatPostgresCustom, Database: database})
	}
	return export, warnings, nil
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
// did not say so. The file is the application's data, so it is created
// readable by the current user only.
func (job dumpJob) writeArtifact(ctx context.Context, fileName string, args []string, magic []byte) (string, error) {
	relative := job.uniqueArtifactPath(fileName)
	absolute := filepath.Join(job.outputDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return "", err
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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

// uniqueArtifactPath is <workload>/<file>, suffixed with a counter when an
// earlier dump of this export already took the name - two databases can
// sanitise to the same file name, and the second must not overwrite the
// first.
func (job dumpJob) uniqueArtifactPath(fileName string) string {
	directory := sanitiseName(job.workload.Name)
	extension := path.Ext(fileName)
	stem := strings.TrimSuffix(fileName, extension)
	candidate := path.Join(directory, fileName)
	for suffix := 2; job.writtenFiles[candidate]; suffix++ {
		candidate = path.Join(directory, fmt.Sprintf("%s-%d%s", stem, suffix, extension))
	}
	if job.writtenFiles != nil {
		job.writtenFiles[candidate] = true
	}
	return candidate
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
