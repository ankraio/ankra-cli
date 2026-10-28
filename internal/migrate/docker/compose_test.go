package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCompose = `name: office
services:
  postgres:
    image: pgvector/pgvector:0.8.1-pg17
    environment:
      POSTGRES_USER: ${POSTGRES_USER:?Set POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: office
      MISSING: ${NOT_SET}
    ports:
      - '127.0.0.1:5432:5432'
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./init.sql:/docker-entrypoint-initdb.d/init.sql:ro
    healthcheck:
      test: ['CMD-SHELL', 'pg_isready -U office']
      interval: 5s
      timeout: 5s
      retries: 10
    restart: unless-stopped
    mem_limit: 2g
    cpus: 2
  migrate:
    build: .
    profiles: ['app']
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      - DATABASE_URL=postgres://office:${POSTGRES_PASSWORD}@postgres:5432/office
      - APP_PORT=${APP_PORT:-3000}
    command: ['node', 'migrate.js']
    restart: 'no'
  app:
    build:
      context: .
      dockerfile: Dockerfile
    image: example/app:1.0
    profiles: ['app']
    depends_on:
      - postgres
      - migrate
    ports:
      - '8080:3000'
      - '53:53/udp'
      - target: 9000
        published: 9001
        host_ip: 0.0.0.0
    volumes:
      - ./data:/data
  mailpit:
    image: axllent/mailpit
    profiles: ['dev']
  worker:
    image: example/worker
    command: run
    deploy:
      resources:
        limits:
          memory: 512M
          cpus: '0.5'
volumes:
  pgdata:
`

func writeComposeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(testCompose), 0o644))
	must(os.WriteFile(filepath.Join(dir, ".env"), []byte("POSTGRES_USER=office\nPOSTGRES_PASSWORD=\"s3cret\"\n# comment\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "init.sql"), []byte("CREATE DATABASE pdns;\n"), 0o644))
	must(os.Mkdir(filepath.Join(dir, "data"), 0o755))
	return dir
}

func findWorkload(t *testing.T, project Project, name string) Workload {
	t.Helper()
	for _, workload := range project.Workloads {
		if workload.Name == name {
			return workload
		}
	}
	t.Fatalf("workload %q not found in %v", name, workloadNames(project))
	return Workload{}
}

func workloadNames(project Project) []string {
	var names []string
	for _, workload := range project.Workloads {
		names = append(names, workload.Name)
	}
	return names
}

func envValue(workload Workload, name string) (EnvVar, bool) {
	for _, entry := range workload.Env {
		if entry.Name == name {
			return entry, true
		}
	}
	return EnvVar{}, false
}

func hasWarning(warnings []string, fragment string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			return true
		}
	}
	return false
}

func TestLoadComposeReadsServicesProfilesAndEnv(t *testing.T) {
	dir := writeComposeFixture(t)
	project, warnings, err := LoadCompose(dir, filepath.Join(dir, "compose.yaml"), ComposeOptions{Profiles: []string{"app"}})
	if err != nil {
		t.Fatal(err)
	}

	if project.Name != "office" {
		t.Errorf("project name = %q, want office", project.Name)
	}
	if got := strings.Join(workloadNames(project), ","); got != "app,migrate,postgres,worker" {
		t.Errorf("workloads = %s, want app,migrate,postgres,worker (mailpit is profile dev)", got)
	}
	if !hasWarning(warnings, "mailpit: skipped") {
		t.Errorf("expected a warning that mailpit was skipped, got %v", warnings)
	}

	postgres := findWorkload(t, project, "postgres")
	if user, _ := envValue(postgres, "POSTGRES_USER"); user.Value != "office" || user.Secret {
		t.Errorf("POSTGRES_USER = %+v, want office from .env and not secret", user)
	}
	if password, _ := envValue(postgres, "POSTGRES_PASSWORD"); password.Value != "s3cret" || !password.Secret {
		t.Errorf("POSTGRES_PASSWORD = %+v, want s3cret (quotes stripped) and secret", password)
	}
	if missing, _ := envValue(postgres, "MISSING"); !missing.Unresolved {
		t.Errorf("MISSING should be flagged unresolved, got %+v", missing)
	}
	if len(postgres.Ports) != 1 || postgres.Ports[0].Container != 5432 || postgres.Ports[0].Public {
		t.Errorf("postgres ports = %+v, want one loopback-bound 5432", postgres.Ports)
	}
	if !postgres.Stateful || len(postgres.Volumes) != 2 {
		t.Fatalf("postgres volumes = %+v, want named pgdata + bind file", postgres.Volumes)
	}
	if !postgres.Volumes[0].Named || postgres.Volumes[0].Name != "pgdata" {
		t.Errorf("first volume = %+v, want named pgdata", postgres.Volumes[0])
	}
	if !postgres.Volumes[1].BindFile || postgres.Volumes[1].HostFiles["init.sql"] != "CREATE DATABASE pdns;\n" || !postgres.Volumes[1].ReadOnly {
		t.Errorf("second volume = %+v, want read-only bind file init.sql with content", postgres.Volumes[1])
	}
	if postgres.Healthcheck == nil || postgres.Healthcheck.IntervalSec != 5 || postgres.Healthcheck.Retries != 10 || postgres.Healthcheck.Test[0] != "CMD-SHELL" {
		t.Errorf("postgres healthcheck = %+v", postgres.Healthcheck)
	}
	if postgres.Memory != "2Gi" || postgres.CPU != "2" {
		t.Errorf("postgres resources = %s/%s, want 2Gi/2", postgres.Memory, postgres.CPU)
	}

	migrate := findWorkload(t, project, "migrate")
	if migrate.Build == nil || migrate.Build.Context != "." || migrate.Build.Dockerfile != "Dockerfile" {
		t.Errorf("migrate build = %+v", migrate.Build)
	}
	if strings.Join(migrate.DependsOn, ",") != "postgres" {
		t.Errorf("migrate depends_on = %v (map form)", migrate.DependsOn)
	}
	if url, _ := envValue(migrate, "DATABASE_URL"); !url.Secret || !strings.Contains(url.Value, "s3cret") {
		t.Errorf("DATABASE_URL should be a resolved secret (embedded credential), got %+v", url)
	}
	if port, _ := envValue(migrate, "APP_PORT"); port.Value != "3000" {
		t.Errorf("APP_PORT default = %q, want 3000", port.Value)
	}
	if strings.Join(migrate.Args, " ") != "node migrate.js" || migrate.Restart != "no" {
		t.Errorf("migrate args/restart = %v/%q", migrate.Args, migrate.Restart)
	}

	app := findWorkload(t, project, "app")
	if app.Image != "example/app:1.0" || app.Build == nil {
		t.Errorf("app should keep both image and build, got %q / %+v", app.Image, app.Build)
	}
	if strings.Join(app.DependsOn, ",") != "migrate,postgres" {
		t.Errorf("app depends_on = %v (list form, sorted)", app.DependsOn)
	}
	if len(app.Ports) != 3 {
		t.Fatalf("app ports = %+v, want three", app.Ports)
	}
	if p := app.Ports[0]; p.Container != 3000 || p.Host != 8080 || !p.Public || p.Protocol != "tcp" {
		t.Errorf("short port = %+v", p)
	}
	if p := app.Ports[1]; p.Container != 53 || p.Protocol != "udp" || !p.Public {
		t.Errorf("udp port = %+v", p)
	}
	if p := app.Ports[2]; p.Container != 9000 || p.Host != 9001 || !p.Public {
		t.Errorf("long-form port = %+v", p)
	}
	if len(app.Volumes) != 1 || !app.Volumes[0].BindDir {
		t.Errorf("app ./data should be a bind directory, got %+v", app.Volumes)
	}
	if !hasWarning(warnings, "app: bind mount ./data is a host directory") {
		t.Errorf("expected a bind-directory warning, got %v", warnings)
	}

	worker := findWorkload(t, project, "worker")
	if worker.Memory != "512Mi" || worker.CPU != "500m" {
		t.Errorf("worker deploy limits = %s/%s, want 512Mi/500m", worker.Memory, worker.CPU)
	}
	if strings.Join(worker.Args, " ") != "run" {
		t.Errorf("worker string command = %v", worker.Args)
	}
}

func TestLoadComposeAllProfiles(t *testing.T) {
	dir := writeComposeFixture(t)
	project, _, err := LoadCompose(dir, filepath.Join(dir, "compose.yaml"), ComposeOptions{AllProfiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Workloads) != 5 {
		t.Errorf("all-profiles should include mailpit, got %v", workloadNames(project))
	}
}

func TestInterpolate(t *testing.T) {
	lookup := func(name string) (string, bool) {
		switch name {
		case "SET":
			return "value", true
		case "EMPTY":
			return "", true
		}
		return "", false
	}
	cases := []struct {
		raw, want  string
		unresolved int
	}{
		{"$$literal", "$literal", 0},
		{"${SET}", "value", 0},
		{"$SET", "value", 0},
		{"${UNSET:-fallback}", "fallback", 0},
		{"${EMPTY:-fallback}", "fallback", 0},
		{"${EMPTY-fallback}", "", 0},
		{"${UNSET-fallback}", "fallback", 0},
		{"${UNSET:?required}", "", 1},
		{"${EMPTY?required}", "", 0},
		{"${SET:+alt}", "alt", 0},
		{"${UNSET}", "", 1},
		{"pre-${SET}-post", "pre-value-post", 0},
	}
	for _, testCase := range cases {
		got, unresolved := interpolate(testCase.raw, lookup)
		if got != testCase.want || len(unresolved) != testCase.unresolved {
			t.Errorf("interpolate(%q) = %q (%d unresolved), want %q (%d)", testCase.raw, got, len(unresolved), testCase.want, testCase.unresolved)
		}
	}
}

func TestNormaliseResources(t *testing.T) {
	memory := map[string]string{"2g": "2Gi", "512m": "512Mi", "512M": "512Mi", "1gb": "1Gi", "1073741824": "1024Mi", "": "", "junk": ""}
	for input, want := range memory {
		if got := normaliseMemory(input); got != want {
			t.Errorf("normaliseMemory(%q) = %q, want %q", input, got, want)
		}
	}
	cpu := map[string]string{"2": "2", "0.5": "500m", "1.5": "1500m", "": "", "junk": ""}
	for input, want := range cpu {
		if got := normaliseCPU(input); got != want {
			t.Errorf("normaliseCPU(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLooksSecret(t *testing.T) {
	secret := []string{"POSTGRES_PASSWORD", "BETTER_AUTH_SECRET", "GROQ_API_KEY", "MAIL_DKIM_PRIVATE_KEY", "STRIPE_WEBHOOK_SECRET", "OVH_CONSUMER_KEY", "API_TOKEN"}
	for _, name := range secret {
		if !looksSecret(name, "x") {
			t.Errorf("%s should look secret", name)
		}
	}
	plain := []string{"POSTGRES_DB", "APP_URL", "NODE_ENV", "MINIO_BUCKET_QUOTA", "BETTER_AUTH_URL", "AI_MODEL"}
	for _, name := range plain {
		if looksSecret(name, "x") {
			t.Errorf("%s should not look secret", name)
		}
	}
	if !looksSecret("DATABASE_URL", "postgres://user:pass@host:5432/db") {
		t.Error("a connection string with an embedded password should look secret")
	}
	if looksSecret("REDIS_URL", "redis://redis:6379") {
		t.Error("a connection string without credentials should not look secret")
	}
}
