package docker

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/migrate"
)

func TestRenderGivesDatabasesAServiceWithoutPublishedPorts(t *testing.T) {
	dir := t.TempDir()
	compose := `services:
  db:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: secret
  cache:
    image: redis:7
  app:
    image: ghcr.io/org/app:1.0
`
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	project, _, err := LoadCompose(dir, path, ComposeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := Render(project, RenderOptions{ClusterName: "shop", Namespace: "shop"})

	db := result.Files["manifests/db.yaml"]
	if !strings.Contains(db, "kind: Service") || !strings.Contains(db, "port: 5432") || !strings.Contains(db, "containerPort: 5432") {
		t.Errorf("a database with no published port must still get a Service on its default port:\n%s", db)
	}
	for _, name := range []string{"cache", "app"} {
		if strings.Contains(result.Files["manifests/"+name+".yaml"], "kind: Service") {
			t.Errorf("%s exposes nothing and must not get a Service", name)
		}
	}
}

func renderComposeFixture(t *testing.T, compose string) migrate.Result {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	project, _, err := LoadCompose(dir, path, ComposeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return Render(project, RenderOptions{ClusterName: "notes", Namespace: "notes"})
}

func TestRenderMountsDatabaseDataDirectoriesBelowTheVolumeRoot(t *testing.T) {
	cases := []struct {
		name        string
		service     string
		mountPath   string
		wantSubPath bool
	}{
		{
			name: "postgres",
			service: `    image: postgres:17
    environment:
      POSTGRES_PASSWORD: secret
    volumes: ['store:/var/lib/postgresql/data']
`,
			mountPath:   "/var/lib/postgresql/data",
			wantSubPath: true,
		},
		{
			name: "mysql",
			service: `    image: mysql:8.4
    environment:
      MYSQL_ROOT_PASSWORD: secret
    volumes: ['store:/var/lib/mysql']
`,
			mountPath:   "/var/lib/mysql",
			wantSubPath: true,
		},
		{
			name: "mariadb",
			service: `    image: mariadb:11
    volumes: ['store:/var/lib/mysql']
`,
			mountPath:   "/var/lib/mysql",
			wantSubPath: true,
		},
		{
			name: "postgres with PGDATA at the mount root",
			service: `    image: postgres:17
    environment:
      PGDATA: /var/lib/postgresql/data/
    volumes: ['store:/var/lib/postgresql/data']
`,
			mountPath:   "/var/lib/postgresql/data",
			wantSubPath: true,
		},
		{
			name: "postgres with PGDATA already below the mount",
			service: `    image: postgres:17
    environment:
      PGDATA: /var/lib/postgresql/data/pgdata
    volumes: ['store:/var/lib/postgresql/data']
`,
			mountPath:   "/var/lib/postgresql/data",
			wantSubPath: false,
		},
		{
			// The source moved its data directory and mounted the volume
			// straight onto it, so the volume root is the data directory
			// again and carries lost+found again.
			name: "postgres with PGDATA mounted at the volume root",
			service: `    image: postgres:17
    environment:
      PGDATA: /var/lib/postgresql/data/pgdata
    volumes: ['store:/var/lib/postgresql/data/pgdata']
`,
			mountPath:   "/var/lib/postgresql/data/pgdata",
			wantSubPath: true,
		},
		{
			name: "mariadb with --datadir already below the mount",
			service: `    image: mariadb:11
    command: ['--datadir=/var/lib/mysql/data']
    volumes: ['store:/var/lib/mysql']
`,
			mountPath:   "/var/lib/mysql",
			wantSubPath: false,
		},
		{
			name: "a database volume that is not the data directory",
			service: `    image: postgres:17
    volumes: ['store:/backups']
`,
			mountPath:   "/backups",
			wantSubPath: false,
		},
		{
			name: "a workload that is not a database",
			service: `    image: nginx:1.27
    volumes: ['store:/usr/share/nginx/html']
`,
			mountPath:   "/usr/share/nginx/html",
			wantSubPath: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := renderComposeFixture(t, "services:\n  db:\n"+testCase.service+"volumes:\n  store:\n")
			db := result.Files["manifests/db.yaml"]
			if !strings.Contains(db, "mountPath: "+testCase.mountPath) {
				t.Errorf("the container path the image expects must not move:\n%s", db)
			}
			if got := strings.Contains(db, "subPath: "+databaseVolumeSubPath); got != testCase.wantSubPath {
				t.Errorf("subPath %s present = %v, want %v:\n%s", databaseVolumeSubPath, got, testCase.wantSubPath, db)
			}
			warning := "db: claim store is mounted at " + testCase.mountPath + " with subPath " + databaseVolumeSubPath
			if got := hasWarning(result.Warnings, warning); got != testCase.wantSubPath {
				t.Errorf("warning %q present = %v, want %v, warnings:\n  %s", warning, got, testCase.wantSubPath, strings.Join(result.Warnings, "\n  "))
			}
		})
	}
}

// updateGolden rewrites the golden manifests from the current render; the
// rendered YAML is a contract with the cluster, so a change to it is reviewed
// as a diff rather than accepted silently.
var updateGolden = flag.Bool("update-golden", false, "rewrite the golden manifests under testdata from this run")

func TestRenderDatabaseDeploymentMatchesGolden(t *testing.T) {
	result := renderComposeFixture(t, `name: notes
services:
  db:
    image: postgres:17.2
    environment:
      POSTGRES_USER: notes
      POSTGRES_PASSWORD: secret
    volumes:
      - db-data:/var/lib/postgresql/data
volumes:
  db-data:
`)
	golden := filepath.Join("testdata", "database-deployment.yaml")
	rendered := result.Files["manifests/db.yaml"]
	if *updateGolden {
		if writeError := os.WriteFile(golden, []byte(rendered), 0o644); writeError != nil {
			t.Fatal(writeError)
		}
	}
	expected, readError := os.ReadFile(golden)
	if readError != nil {
		t.Fatal(readError)
	}
	if rendered != string(expected) {
		t.Errorf("manifests/db.yaml no longer matches %s; review the difference and rerun with -update-golden when it is intended\n--- want ---\n%s\n--- got ---\n%s", golden, expected, rendered)
	}
}
