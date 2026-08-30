package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
