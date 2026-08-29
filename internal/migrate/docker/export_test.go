package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ankra/internal/migrate"
)

const exportTestCompose = `services:
  app:
    image: ghcr.io/org/app:1.0
    depends_on: [postgres, mysql]
  postgres:
    image: pgvector/pgvector:0.8.1-pg17
    environment:
      POSTGRES_USER: office
      POSTGRES_PASSWORD: secret
    volumes: ['pgdata:/var/lib/postgresql/data']
  mysql:
    image: mariadb:11
    environment:
      MARIADB_ROOT_PASSWORD: root-secret
volumes:
  pgdata:
`

// cannedAnswer pairs a fragment of a docker command line with what the
// fake daemon answers. First match wins, so fragments are distinctive.
type cannedAnswer struct {
	fragment string
	answer   string
}

// fakeDocker stands in for the docker CLI: it records every call and
// answers from a script.
type fakeDocker struct {
	t       *testing.T
	host    string
	calls   []string
	answers []cannedAnswer
	// failOn makes Stream fail for a command containing the fragment.
	failOn string
}

func (f *fakeDocker) answer(args []string) (string, bool) {
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, joined)
	for _, canned := range f.answers {
		if strings.Contains(joined, canned.fragment) {
			return canned.answer, true
		}
	}
	return "", false
}

func (f *fakeDocker) Output(_ context.Context, args ...string) ([]byte, error) {
	answer, ok := f.answer(args)
	if !ok {
		f.t.Fatalf("unexpected docker call %q", strings.Join(args, " "))
	}
	return []byte(answer), nil
}

func (f *fakeDocker) Stream(_ context.Context, stdout io.Writer, args ...string) error {
	answer, ok := f.answer(args)
	if !ok {
		f.t.Fatalf("unexpected docker call %q", strings.Join(args, " "))
	}
	if f.failOn != "" && strings.Contains(strings.Join(args, " "), f.failOn) {
		return errors.New("exit status 1: connection refused")
	}
	_, err := io.WriteString(stdout, answer)
	return err
}

// testModule is the built-in module under test; a named value keeps the
// composite literal out of if-statement conditions.
var testModule = Module{}

func defaultAnswers() []cannedAnswer {
	return []cannedAnswer{
		{"label=com.docker.compose.service=postgres", "pg1\n"},
		{"label=com.docker.compose.service=mysql", "my1\n"},
		{"SHOW server_version", "17.2\n"},
		{"SELECT datname", "office\npdns\n"},
		{"pg_dumpall", "--\n-- PostgreSQL database cluster dump\nCREATE ROLE office;\n"},
		{"pg_dump ", "PGDMP\x01\x11\x00fake archive"},
		{"SELECT VERSION()", "11.4.2-MariaDB\n"},
		{"SHOW DATABASES", "information_schema\nmysql\nperformance_schema\nshop\nsys\n"},
		{"mysqldump", "-- MariaDB dump\nCREATE DATABASE shop;\n"},
	}
}

func installFakeDocker(t *testing.T, fake *fakeDocker) {
	t.Helper()
	original := newDockerExecutor
	newDockerExecutor = func(host string) dockerExecutor {
		fake.host = host
		return fake
	}
	t.Cleanup(func() { newDockerExecutor = original })
}

func writeExportFixture(t *testing.T, compose string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "shop")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func callsContaining(calls []string, fragment string) []string {
	var out []string
	for _, call := range calls {
		if strings.Contains(call, fragment) {
			out = append(out, call)
		}
	}
	return out
}

func TestExportDumpsEveryDatabaseServer(t *testing.T) {
	fake := &fakeDocker{t: t, answers: defaultAnswers()}
	installFakeDocker(t, fake)
	dir := writeExportFixture(t, exportTestCompose)
	out := t.TempDir()
	var progress bytes.Buffer

	export, err := testModule.Export(context.Background(), migrate.ExportRequest{
		Dir: dir, OutputDir: out, Progress: &progress,
		Options: map[string]string{OptionDockerHost: "ssh://root@203.0.113.7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.host != "ssh://root@203.0.113.7" {
		t.Errorf("docker-host option must reach the executor, got %q", fake.host)
	}
	if len(export.Databases) != 2 || export.Databases[0].Workload != "mysql" || export.Databases[1].Workload != "postgres" {
		t.Fatalf("databases = %+v, want mysql then postgres (sorted)", export.Databases)
	}

	// Containers are found by the compose labels of the project the
	// directory names.
	if got := callsContaining(fake.calls, "ps -q"); len(got) != 2 || got[0] != "ps -q --filter label=com.docker.compose.project=shop --filter label=com.docker.compose.service=mysql" {
		t.Errorf("container lookups = %v", got)
	}

	postgres := export.Databases[1]
	if postgres.Engine != migrate.EnginePostgres || postgres.ServerVersion != "17.2" || postgres.Image != "pgvector/pgvector:0.8.1-pg17" {
		t.Errorf("postgres export = %+v", postgres)
	}
	wantTarget := migrate.RestoreTarget{Namespace: "shop", Host: "postgres", Port: 5432, Username: "office", PasswordSecret: "postgres-secrets", PasswordKey: "POSTGRES_PASSWORD"}
	if postgres.Target != wantTarget {
		t.Errorf("postgres target = %+v, want %+v", postgres.Target, wantTarget)
	}
	if len(postgres.Artifacts) != 3 || postgres.Artifacts[0].Kind != migrate.ArtifactKindGlobals || postgres.Artifacts[1].Database != "office" || postgres.Artifacts[2].Database != "pdns" || postgres.Artifacts[2].Format != migrate.ArtifactFormatPostgresCustom {
		t.Errorf("postgres artifacts = %+v", postgres.Artifacts)
	}
	// Every command runs inside the container through its own shell, with
	// the password taken from the container's environment, so it never
	// appears on the host's command line.
	dumps := callsContaining(fake.calls, "pg_dump ")
	if len(dumps) != 2 || !strings.Contains(dumps[0], `exec pg1 sh -c PGPASSWORD="$POSTGRES_PASSWORD" exec pg_dump "$@" sh -U office -Fc -d office`) {
		t.Errorf("pg_dump calls = %v", dumps)
	}
	for _, call := range fake.calls {
		if strings.Contains(call, "secret") {
			t.Errorf("a password crossed the host command line: %q", call)
		}
	}
	dump, err := os.ReadFile(filepath.Join(out, "postgres", "pdns.dump"))
	if err != nil || !bytes.HasPrefix(dump, []byte("PGDMP")) {
		t.Errorf("pdns.dump = %q, %v", dump, err)
	}

	mysql := export.Databases[0]
	wantTarget = migrate.RestoreTarget{Namespace: "shop", Host: "mysql", Port: 3306, Username: "root", PasswordSecret: "mysql-secrets", PasswordKey: "MARIADB_ROOT_PASSWORD"}
	if mysql.Target != wantTarget || mysql.ServerVersion != "11.4.2-MariaDB" {
		t.Errorf("mysql export = %+v", mysql)
	}
	if len(mysql.Artifacts) != 1 || mysql.Artifacts[0].Database != "shop" || mysql.Artifacts[0].Path != "mysql/shop.sql" {
		t.Errorf("system schemas must be skipped, got %+v", mysql.Artifacts)
	}
	if got := callsContaining(fake.calls, "mysqldump"); len(got) != 1 || !strings.Contains(got[0], `MYSQL_PWD="$MARIADB_ROOT_PASSWORD"`) || !strings.HasSuffix(got[0], "--databases shop") {
		t.Errorf("mysqldump call = %v", got)
	}

	if !strings.Contains(progress.String(), "postgres: dumping database office") || !strings.Contains(progress.String(), "mysql: dumping database shop") {
		t.Errorf("progress = %q", progress.String())
	}
	if !hasWarning(export.Warnings, "point-in-time snapshot") {
		t.Errorf("the live-dump warning is missing: %v", export.Warnings)
	}

	manifest, err := migrate.FinaliseExport(out, "docker", dir, export, time.Now())
	if err != nil {
		t.Fatalf("the export must finalise as written: %v", err)
	}
	if manifest.Databases[1].Artifacts[1].SizeBytes == 0 {
		t.Error("artifact sizes must be measured")
	}
}

func TestExportOptionsNarrowTheDump(t *testing.T) {
	fake := &fakeDocker{t: t, answers: defaultAnswers()}
	installFakeDocker(t, fake)
	dir := writeExportFixture(t, exportTestCompose)

	export, err := testModule.Export(context.Background(), migrate.ExportRequest{
		Dir: dir, OutputDir: t.TempDir(), Namespace: "avura",
		Options: map[string]string{
			OptionProject:                      "aura-office",
			OptionContainerPrefix + "postgres": "postgres-main",
			OptionDatabasesPrefix + "postgres": "office",
			OptionContainerPrefix + "mysql":    "my-main",
			OptionDatabasesPrefix + "mysql":    "shop",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := callsContaining(fake.calls, "ps -q"); len(got) != 0 {
		t.Errorf("naming the container must skip the lookup, got %v", got)
	}
	if got := callsContaining(fake.calls, "SELECT datname"); len(got) != 0 {
		t.Errorf("naming the databases must skip the listing, got %v", got)
	}
	postgres := export.Databases[1]
	if len(postgres.Artifacts) != 2 || postgres.Artifacts[1].Database != "office" || postgres.Target.Namespace != "avura" {
		t.Errorf("postgres export = %+v", postgres)
	}
	if got := callsContaining(fake.calls, "exec postgres-main"); len(got) != 3 {
		t.Errorf("every postgres command must target the named container, got %v", got)
	}
}

func TestExportProjectOptionFindsContainers(t *testing.T) {
	fake := &fakeDocker{t: t, answers: defaultAnswers()}
	installFakeDocker(t, fake)
	dir := writeExportFixture(t, exportTestCompose)
	if _, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: dir, OutputDir: t.TempDir(), Options: map[string]string{OptionProject: "aura-office"}}); err != nil {
		t.Fatal(err)
	}
	if got := callsContaining(fake.calls, "ps -q"); len(got) != 2 || !strings.Contains(got[0], "label=com.docker.compose.project=aura-office") {
		t.Errorf("the project option must drive the label filter, got %v", got)
	}
}

func TestExportErrors(t *testing.T) {
	t.Run("no running container", func(t *testing.T) {
		fake := &fakeDocker{t: t, answers: []cannedAnswer{{"ps -q", "\n"}}}
		installFakeDocker(t, fake)
		_, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, exportTestCompose), OutputDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "no running container for service mysql") || !strings.Contains(err.Error(), "--option project=") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("garbled archive", func(t *testing.T) {
		answers := defaultAnswers()
		answers[5] = cannedAnswer{"pg_dump ", "pg_dump: error: connection failed\n"}
		fake := &fakeDocker{t: t, answers: answers}
		installFakeDocker(t, fake)
		_, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, exportTestCompose), OutputDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "archive header") {
			t.Errorf("a custom-format dump without its magic must be refused, got %v", err)
		}
	})
	t.Run("empty dump", func(t *testing.T) {
		answers := defaultAnswers()
		answers[8] = cannedAnswer{"mysqldump", ""}
		fake := &fakeDocker{t: t, answers: answers}
		installFakeDocker(t, fake)
		_, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, exportTestCompose), OutputDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "the dump is empty") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("dump command fails", func(t *testing.T) {
		fake := &fakeDocker{t: t, answers: defaultAnswers(), failOn: "pg_dumpall"}
		installFakeDocker(t, fake)
		_, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, exportTestCompose), OutputDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "postgres:") || !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("the failing command's reason must surface, got %v", err)
		}
	})
	t.Run("no database service", func(t *testing.T) {
		fake := &fakeDocker{t: t}
		installFakeDocker(t, fake)
		_, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, "services:\n  web:\n    image: nginx:1.27\n"), OutputDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "no database service recognised") || !strings.Contains(err.Error(), "web=nginx:1.27") {
			t.Errorf("err = %v", err)
		}
		if len(fake.calls) != 0 {
			t.Errorf("nothing to dump must mean no docker calls, got %v", fake.calls)
		}
	})
	t.Run("server without databases", func(t *testing.T) {
		answers := defaultAnswers()
		answers[3] = cannedAnswer{"SELECT datname", "\n"}
		fake := &fakeDocker{t: t, answers: answers}
		installFakeDocker(t, fake)
		_, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, exportTestCompose), OutputDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "no database besides postgres") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestExportIsAdvertised(t *testing.T) {
	if _, ok := migrate.ExporterFor(New()); !ok {
		t.Error("the docker module must advertise and implement export")
	}
}

func TestExportPostgresDumpsRolesWithoutPasswords(t *testing.T) {
	fake := &fakeDocker{t: t, answers: defaultAnswers()}
	installFakeDocker(t, fake)
	export, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, exportTestCompose), OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got := callsContaining(fake.calls, "pg_dumpall"); len(got) != 1 || !strings.Contains(got[0], "--globals-only --no-role-passwords") {
		t.Errorf("the globals dump must leave role passwords out, or the restore re-sets the cluster's own user, got %v", got)
	}
	if !hasWarning(export.Warnings, "roles are restored without their passwords") || !hasWarning(export.Warnings, "office keeps the password") {
		t.Errorf("the user must be told which roles need a password afterwards: %v", export.Warnings)
	}
}

func TestExportPostgresMaintenanceDatabase(t *testing.T) {
	withPostgres := func(answers []cannedAnswer) []cannedAnswer {
		answers[3] = cannedAnswer{"SELECT datname", "office\npostgres\n"}
		return answers
	}
	dumped := func(export migrate.Export) []string {
		var names []string
		for _, artifact := range export.Databases[1].Artifacts {
			if artifact.Kind == migrate.ArtifactKindDatabase {
				names = append(names, artifact.Database)
			}
		}
		return names
	}

	t.Run("skipped with a hint when the application lives elsewhere", func(t *testing.T) {
		fake := &fakeDocker{t: t, answers: withPostgres(defaultAnswers())}
		installFakeDocker(t, fake)
		export, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, exportTestCompose), OutputDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if got := dumped(export); len(got) != 1 || got[0] != "office" {
			t.Errorf("the maintenance database is not the application's, got %v", got)
		}
		if !hasWarning(export.Warnings, "--option databases.postgres=postgres") {
			t.Errorf("skipping postgres must say how to include it: %v", export.Warnings)
		}
	})
	t.Run("dumped when it is the application database", func(t *testing.T) {
		compose := strings.Replace(exportTestCompose, "      POSTGRES_USER: office\n", "", 1)
		fake := &fakeDocker{t: t, answers: withPostgres(defaultAnswers())}
		installFakeDocker(t, fake)
		export, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, compose), OutputDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if got := dumped(export); len(got) != 2 || got[1] != "postgres" {
			t.Errorf("without POSTGRES_USER and POSTGRES_DB the image keeps the data in postgres; it must be dumped, got %v", got)
		}
		if hasWarning(export.Warnings, "maintenance database postgres was not dumped") {
			t.Errorf("no hint when postgres was dumped: %v", export.Warnings)
		}
	})
	t.Run("dumped when POSTGRES_DB names it", func(t *testing.T) {
		compose := strings.Replace(exportTestCompose, "      POSTGRES_USER: office\n", "      POSTGRES_USER: office\n      POSTGRES_DB: postgres\n", 1)
		fake := &fakeDocker{t: t, answers: withPostgres(defaultAnswers())}
		installFakeDocker(t, fake)
		export, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, compose), OutputDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if got := dumped(export); len(got) != 2 || got[1] != "postgres" {
			t.Errorf("POSTGRES_DB=postgres makes it the application database, got %v", got)
		}
	})
}

func TestExportWritesTheDumpsForTheCurrentUserOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	fake := &fakeDocker{t: t, answers: defaultAnswers()}
	installFakeDocker(t, fake)
	out := t.TempDir()
	export, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, exportTestCompose), OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	for _, database := range export.Databases {
		directory, statError := os.Stat(filepath.Join(out, database.Workload))
		if statError != nil {
			t.Fatal(statError)
		}
		if directory.Mode().Perm() != 0o700 {
			t.Errorf("%s: directory mode = %o, want 700", database.Workload, directory.Mode().Perm())
		}
		for _, artifact := range database.Artifacts {
			file, statError := os.Stat(filepath.Join(out, filepath.FromSlash(artifact.Path)))
			if statError != nil {
				t.Fatal(statError)
			}
			if file.Mode().Perm() != 0o600 {
				t.Errorf("%s: mode = %o, want 600", artifact.Path, file.Mode().Perm())
			}
		}
	}
	if !hasWarning(export.Warnings, "delete the export directory once the restore has succeeded") {
		t.Errorf("the user must be told the export is their data on disk: %v", export.Warnings)
	}
}

func TestExportKeepsDumpsApartWhenNamesSanitiseAlike(t *testing.T) {
	answers := defaultAnswers()
	answers[3] = cannedAnswer{"SELECT datname", "my_db\nmy-db\n"}
	fake := &fakeDocker{t: t, answers: answers}
	installFakeDocker(t, fake)
	export, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: writeExportFixture(t, exportTestCompose), OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	postgres := export.Databases[1]
	if len(postgres.Artifacts) != 3 || postgres.Artifacts[1].Path != "postgres/my-db.dump" || postgres.Artifacts[2].Path != "postgres/my-db-2.dump" {
		t.Errorf("two databases whose names sanitise alike must not overwrite each other, got %+v", postgres.Artifacts)
	}
	if postgres.Artifacts[1].Database != "my_db" || postgres.Artifacts[2].Database != "my-db" {
		t.Errorf("the real database names must survive the renaming, got %+v", postgres.Artifacts)
	}
}

func TestExportNormalisesTheComposeProjectName(t *testing.T) {
	fake := &fakeDocker{t: t, answers: defaultAnswers()}
	installFakeDocker(t, fake)
	dir := filepath.Join(t.TempDir(), "Aura Office")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(exportTestCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := testModule.Export(context.Background(), migrate.ExportRequest{Dir: dir, OutputDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if got := callsContaining(fake.calls, "ps -q"); len(got) != 2 || !strings.Contains(got[0], "label=com.docker.compose.project=auraoffice") {
		t.Errorf("containers are labelled with compose's normalised project name, got %v", got)
	}
	for name, want := range map[string]string{"Aura Office": "auraoffice", "_shop.v2": "shopv2", "--Shop": "shop", "shop": "shop"} {
		if got := composeProjectName(name); got != want {
			t.Errorf("composeProjectName(%q) = %q, want %q", name, got, want)
		}
	}
}
