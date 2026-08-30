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
	"strconv"
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
func (module Module) Export(ctx context.Context, request migrate.ExportRequest) (migrate.Export, error) {
	progress := request.Progress
	if progress == nil {
		progress = io.Discard
	}
	source, err := module.discoverSource(ctx, request)
	if err != nil {
		return migrate.Export{}, err
	}

	var export migrate.Export
	export.Warnings = append(export.Warnings, source.warnings...)
	writtenFiles := map[string]bool{}
	for _, server := range source.servers {
		job := dumpJob{
			docker:       source.docker,
			server:       server,
			namespace:    source.namespace,
			outputDir:    request.OutputDir,
			only:         splitList(source.options[OptionDatabasesPrefix+server.workload.Name]),
			progress:     progress,
			writtenFiles: writtenFiles,
		}
		var database migrate.DatabaseExport
		var databaseWarnings []string
		switch server.engine {
		case migrate.EnginePostgres:
			database, databaseWarnings, err = dumpPostgres(ctx, job)
		case migrate.EngineMySQL:
			database, databaseWarnings, err = dumpMySQL(ctx, job)
		}
		if err != nil {
			return migrate.Export{}, err
		}
		export.Databases = append(export.Databases, database)
		export.Warnings = append(export.Warnings, databaseWarnings...)
	}

	export.Warnings = append(export.Warnings,
		"each dump is a point-in-time snapshot of a live server; rehearse with the source running, then stop its writers and export once more right before the final cutover",
		"the dumps hold the application's data and are written readable by you alone; delete the export directory once the restore has succeeded")
	export.Warnings = uniqueSorted(export.Warnings)
	return export, nil
}

// exportSource is the source as an export or a plan sees it: the compose
// project, the docker executor reaching its daemon, and every database
// service resolved to its running container.
type exportSource struct {
	project   Project
	options   map[string]string
	namespace string
	docker    dockerExecutor
	servers   []databaseServer
	// others are the non-database workloads, for what the export leaves
	// behind.
	others   []Workload
	warnings []string
}

// databaseServer is one database service resolved to its container. The
// environment is the container's own, read from the daemon: it is what the
// server was actually started with, which a compose file with an
// unresolved ${VAR} or an env_file cannot tell.
type databaseServer struct {
	workload    Workload
	engine      string
	container   string
	environment map[string]string
}

// discoverSource loads the project, finds the container behind every
// database service and reads its environment. Nothing is dumped.
func (Module) discoverSource(ctx context.Context, request migrate.ExportRequest) (exportSource, error) {
	options := request.Options
	if options == nil {
		options = map[string]string{}
	}
	project, err := exportProject(ctx, request.Dir, options)
	if err != nil {
		return exportSource{}, err
	}
	namespace := request.Namespace
	if namespace == "" {
		namespace = sanitiseName(project.Name)
	}
	composeProject := options[OptionProject]
	if composeProject == "" {
		composeProject = composeProjectName(project.Name)
	}
	source := exportSource{
		project:   project,
		options:   options,
		namespace: namespace,
		docker:    newDockerExecutor(options[OptionDockerHost]),
	}

	var otherImages []string
	for _, workload := range project.SortedWorkloads() {
		engine, ok := DatabaseEngine(workload.Image)
		if !ok {
			source.others = append(source.others, workload)
			if workload.Image != "" {
				otherImages = append(otherImages, workload.Name+"="+workload.Image)
			}
			continue
		}
		container, containerWarnings, err := findContainer(ctx, source.docker, composeProject, workload.Name, options[OptionContainerPrefix+workload.Name])
		if err != nil {
			return exportSource{}, err
		}
		source.warnings = append(source.warnings, containerWarnings...)
		environment, err := containerEnvironment(ctx, source.docker, container)
		if err != nil {
			return exportSource{}, fmt.Errorf("%s: reading the container's environment: %w", workload.Name, err)
		}
		source.servers = append(source.servers, databaseServer{
			workload: workload, engine: engine, container: container, environment: environment,
		})
	}
	if len(source.servers) == 0 {
		return exportSource{}, fmt.Errorf("no database service recognised in %s (images: %s); the export knows PostgreSQL and MySQL/MariaDB images",
			project.Name, strings.Join(otherImages, ", "))
	}
	return source, nil
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
		warnings = append(warnings, fmt.Sprintf("service %s runs %d containers; dumping from %s", service, len(ids), ids[0]))
	}
	return ids[0], warnings, nil
}

// containerEnvironment reads the environment a container was started with.
func containerEnvironment(ctx context.Context, docker dockerExecutor, container string) (map[string]string, error) {
	output, err := docker.Output(ctx, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", container)
	if err != nil {
		return nil, err
	}
	environment := map[string]string{}
	for _, line := range nonEmptyLines(string(output)) {
		name, value, _ := strings.Cut(line, "=")
		environment[name] = value
	}
	return environment, nil
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

// dumpJob is everything one database server's dump needs.
type dumpJob struct {
	docker    dockerExecutor
	server    databaseServer
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

// value is a setting of the server, from the container's environment first
// and the compose description second, so what the server was really started
// with wins over what the file says.
func (server databaseServer) value(name string) string {
	if value, ok := server.environment[name]; ok && value != "" {
		return value
	}
	if entry, ok := lookupEnv(server.workload, name); ok && !entry.Unresolved {
		return entry.Value
	}
	return ""
}

// has reports whether the server carries the variable at all, as a value or
// as the _FILE indirection the official images accept for secrets.
func (server databaseServer) has(name string) bool {
	if _, ok := server.environment[name]; ok {
		return true
	}
	if _, ok := server.environment[name+"_FILE"]; ok {
		return true
	}
	if _, ok := lookupEnv(server.workload, name); ok {
		return true
	}
	_, ok := lookupEnv(server.workload, name+"_FILE")
	return ok
}

// inContainer builds a `docker exec` that runs program through the
// container's own shell, so a password the image needs for local connections
// comes from the container's environment - or from the file its _FILE
// variant points at, as the official images allow - and never crosses the
// host as a command-line argument.
func (job dumpJob) inContainer(passwordVariable, passwordKey, program string, args ...string) []string {
	script := `value="$` + passwordKey + `"; if [ -z "$value" ] && [ -n "${` + passwordKey + `_FILE:-}" ]; then value="$(cat "$` + passwordKey + `_FILE")"; fi; ` +
		passwordVariable + `="$value" exec ` + program + ` "$@"`
	return append([]string{"exec", job.server.container, "sh", "-c", script, "sh"}, args...)
}

// restoreTarget describes where convert put this server in the cluster.
func (job dumpJob) restoreTarget(engine, username, passwordKey string) migrate.RestoreTarget {
	target := migrate.RestoreTarget{
		Namespace: job.namespace,
		Host:      job.server.workload.Name,
		Port:      DefaultDatabasePort(engine),
		Username:  username,
	}
	if entry, ok := lookupEnv(job.server.workload, passwordKey); ok && entry.Secret {
		target.PasswordSecret = job.server.workload.Name + "-secrets"
		target.PasswordKey = passwordKey
	}
	return target
}

var postgresCustomMagic = []byte("PGDMP")

// databaseSize is one database and its size as the server reports it.
type databaseSize struct {
	name      string
	sizeBytes int64
}

// parseDatabaseSizes reads "name<sep>bytes" lines; a line without a size is
// a database of unknown size, never a rejected one.
func parseDatabaseSizes(text string, separator string) []databaseSize {
	var databases []databaseSize
	for _, line := range nonEmptyLines(text) {
		name, size, _ := strings.Cut(line, separator)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		bytes, _ := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
		databases = append(databases, databaseSize{name: name, sizeBytes: bytes})
	}
	return databases
}

// postgresCredentials is the superuser the image was configured with; the
// official image creates it and, unless POSTGRES_DB says otherwise, a
// database of the same name for the application.
func postgresCredentials(server databaseServer) (username string, applicationDatabase string) {
	username = server.value("POSTGRES_USER")
	if username == "" {
		username = "postgres"
	}
	applicationDatabase = server.value("POSTGRES_DB")
	if applicationDatabase == "" {
		applicationDatabase = username
	}
	return username, applicationDatabase
}

func postgresClient(job dumpJob, username string) func(query string) []string {
	return func(query string) []string {
		return job.inContainer("PGPASSWORD", "POSTGRES_PASSWORD", "psql", "-U", username, "-d", "postgres", "-tAc", query)
	}
}

// listPostgresDatabases asks the server for every non-template database and
// its size; the maintenance database `postgres` is left out unless it is
// the application's, and the caller is told when that happens.
func listPostgresDatabases(ctx context.Context, docker dockerExecutor, psql func(string) []string, applicationDatabase string, workload string) ([]databaseSize, []string, error) {
	output, err := docker.Output(ctx, psql("SELECT datname, pg_database_size(datname) FROM pg_database WHERE NOT datistemplate ORDER BY datname")...)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: listing databases: %w", workload, err)
	}
	var databases []databaseSize
	var warnings []string
	for _, database := range parseDatabaseSizes(string(output), "|") {
		if database.name == "postgres" && applicationDatabase != "postgres" {
			warnings = append(warnings, fmt.Sprintf("%s: the maintenance database postgres was not dumped; pass --option %s%s=postgres if the application keeps data in it",
				workload, OptionDatabasesPrefix, workload))
			continue
		}
		databases = append(databases, database)
	}
	return databases, warnings, nil
}

// dumpPostgres dumps a PostgreSQL server: its roles (without their
// passwords - the restore connects as the target's own user, and a dump that
// re-set that password would lock the cluster out of its database) and every
// database that is not a template.
func dumpPostgres(ctx context.Context, job dumpJob) (migrate.DatabaseExport, []string, error) {
	const passwordKey = "POSTGRES_PASSWORD"
	workload := job.server.workload.Name
	username, applicationDatabase := postgresCredentials(job.server)
	psql := postgresClient(job, username)

	version, err := job.docker.Output(ctx, psql("SHOW server_version")...)
	if err != nil {
		return migrate.DatabaseExport{}, nil, fmt.Errorf("%s: reading the server version: %w", workload, err)
	}
	var warnings []string
	databases := job.only
	if len(databases) == 0 {
		listed, listWarnings, listError := listPostgresDatabases(ctx, job.docker, psql, applicationDatabase, workload)
		if listError != nil {
			return migrate.DatabaseExport{}, nil, listError
		}
		warnings = append(warnings, listWarnings...)
		for _, database := range listed {
			databases = append(databases, database.name)
		}
	}
	if len(databases) == 0 {
		return migrate.DatabaseExport{}, nil, fmt.Errorf("%s: the server holds no database besides postgres", workload)
	}

	export := migrate.DatabaseExport{
		Workload:      workload,
		Engine:        migrate.EnginePostgres,
		Image:         job.server.workload.Image,
		ServerVersion: strings.TrimSpace(string(version)),
		Target:        job.restoreTarget(migrate.EnginePostgres, username, passwordKey),
	}

	_, _ = fmt.Fprintf(job.progress, "%s: dumping roles and globals\n", workload)
	globals, err := job.writeArtifact(ctx, "globals.sql", job.inContainer("PGPASSWORD", passwordKey, "pg_dumpall", "-U", username, "--globals-only", "--no-role-passwords"), nil)
	if err != nil {
		return migrate.DatabaseExport{}, nil, err
	}
	export.Artifacts = append(export.Artifacts, migrate.Artifact{Path: globals, Kind: migrate.ArtifactKindGlobals, Format: migrate.ArtifactFormatSQL})
	warnings = append(warnings, fmt.Sprintf("%s: roles are restored without their passwords; %s keeps the password from the cluster's secret, any other role that logs in needs ALTER ROLE ... PASSWORD after the restore",
		workload, username))

	for _, database := range databases {
		_, _ = fmt.Fprintf(job.progress, "%s: dumping database %s\n", workload, database)
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

// mysqlCredentials picks the account the dump connects as: root when the
// image was given a root password, else the application user the image
// created, which can read its own database. An image started with a random
// root password and no application user leaves nothing to dump with.
func mysqlCredentials(server databaseServer) (username string, passwordKey string, warning string, err error) {
	for _, rootKey := range []string{"MYSQL_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD"} {
		if server.has(rootKey) {
			return "root", rootKey, "", nil
		}
	}
	for _, emptyKey := range []string{"MYSQL_ALLOW_EMPTY_PASSWORD", "MARIADB_ALLOW_EMPTY_ROOT_PASSWORD"} {
		if server.value(emptyKey) != "" {
			return "root", "MYSQL_ROOT_PASSWORD", "", nil
		}
	}
	for _, pair := range [][2]string{{"MYSQL_USER", "MYSQL_PASSWORD"}, {"MARIADB_USER", "MARIADB_PASSWORD"}} {
		if user := server.value(pair[0]); user != "" && server.has(pair[1]) {
			return user, pair[1], fmt.Sprintf("%s: no root password is configured, so the dump runs as %s and carries only the databases that user can read",
				server.workload.Name, user), nil
		}
	}
	return "", "", "", fmt.Errorf("%s: no account to dump with - the container has neither MYSQL_ROOT_PASSWORD nor MYSQL_USER/MYSQL_PASSWORD (a MYSQL_RANDOM_ROOT_PASSWORD server needs an application user)",
		server.workload.Name)
}

func mysqlClient(job dumpJob, username string, passwordKey string) func(query string) []string {
	return func(query string) []string {
		return job.inContainer("MYSQL_PWD", passwordKey, "mysql", "-u"+username, "-N", "-e", query)
	}
}

// listMySQLDatabases asks the server for every schema the account can see
// and its size, minus the server's own.
func listMySQLDatabases(ctx context.Context, docker dockerExecutor, client func(string) []string, workload string) ([]databaseSize, error) {
	output, err := docker.Output(ctx, client("SELECT s.schema_name, COALESCE(SUM(t.data_length + t.index_length), 0) FROM information_schema.schemata s LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name GROUP BY s.schema_name ORDER BY s.schema_name")...)
	if err != nil {
		return nil, fmt.Errorf("%s: listing databases: %w", workload, err)
	}
	var databases []databaseSize
	for _, database := range parseDatabaseSizes(string(output), "\t") {
		if !mysqlSystemSchemas[database.name] {
			databases = append(databases, database)
		}
	}
	return databases, nil
}

func dumpMySQL(ctx context.Context, job dumpJob) (migrate.DatabaseExport, []string, error) {
	workload := job.server.workload.Name
	username, passwordKey, credentialWarning, err := mysqlCredentials(job.server)
	if err != nil {
		return migrate.DatabaseExport{}, nil, err
	}
	var warnings []string
	if credentialWarning != "" {
		warnings = append(warnings, credentialWarning)
	}
	client := mysqlClient(job, username, passwordKey)

	version, err := job.docker.Output(ctx, client("SELECT VERSION()")...)
	if err != nil {
		return migrate.DatabaseExport{}, nil, fmt.Errorf("%s: reading the server version: %w", workload, err)
	}
	databases := job.only
	if len(databases) == 0 {
		listed, listError := listMySQLDatabases(ctx, job.docker, client, workload)
		if listError != nil {
			return migrate.DatabaseExport{}, nil, listError
		}
		for _, database := range listed {
			databases = append(databases, database.name)
		}
	}
	if len(databases) == 0 {
		return migrate.DatabaseExport{}, nil, fmt.Errorf("%s: the server holds no database besides the system schemas", workload)
	}

	export := migrate.DatabaseExport{
		Workload:      workload,
		Engine:        migrate.EngineMySQL,
		Image:         job.server.workload.Image,
		ServerVersion: strings.TrimSpace(string(version)),
		Target:        job.restoreTarget(migrate.EngineMySQL, username, passwordKey),
	}
	for _, database := range databases {
		_, _ = fmt.Fprintf(job.progress, "%s: dumping database %s\n", workload, database)
		// --databases makes the dump create and select its schema, so a
		// restore is one `mysql < file` with nothing prepared by hand.
		dump, err := job.writeArtifact(ctx, sanitiseName(database)+".sql",
			job.inContainer("MYSQL_PWD", passwordKey, "mysqldump", "-u"+username, "--single-transaction", "--routines", "--triggers", "--events", "--databases", database), nil)
		if err != nil {
			return migrate.DatabaseExport{}, nil, err
		}
		export.Artifacts = append(export.Artifacts, migrate.Artifact{Path: dump, Kind: migrate.ArtifactKindDatabase, Format: migrate.ArtifactFormatSQL, Database: database})
	}
	return export, warnings, nil
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
		return "", fmt.Errorf("%s: %w", job.server.workload.Name, streamErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := checkDump(absolute, magic); err != nil {
		return "", fmt.Errorf("%s: %s: %w", job.server.workload.Name, relative, err)
	}
	return relative, nil
}

// uniqueArtifactPath is <workload>/<file>, suffixed with a counter when an
// earlier dump of this export already took the name - two databases can
// sanitise to the same file name, and the second must not overwrite the
// first.
func (job dumpJob) uniqueArtifactPath(fileName string) string {
	directory := sanitiseName(job.server.workload.Name)
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
