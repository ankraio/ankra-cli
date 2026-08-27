package docker

import (
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/migrate"
)

func renderFixture(t *testing.T, options RenderOptions) migrate.Result {
	t.Helper()
	dir := writeComposeFixture(t)
	project, _, err := LoadCompose(dir, filepath.Join(dir, "compose.yaml"), ComposeOptions{Profiles: []string{"app"}})
	if err != nil {
		t.Fatal(err)
	}
	if options.ClusterName == "" {
		options.ClusterName = "office"
	}
	if options.Namespace == "" {
		options.Namespace = "office"
	}
	return Render(project, options)
}

func manifestByName(t *testing.T, result migrate.Result, name string) migrate.Manifest {
	t.Helper()
	for _, manifest := range result.Cluster.Spec.Stacks[0].Manifests {
		if manifest.Name == name {
			return manifest
		}
	}
	t.Fatalf("manifest %q not in stack", name)
	return migrate.Manifest{}
}

func parentNames(manifest migrate.Manifest) string {
	var names []string
	for _, parent := range manifest.Parents {
		names = append(names, parent.Name)
	}
	return strings.Join(names, ",")
}

func TestRenderProducesStackWithDependencyOrder(t *testing.T) {
	result := renderFixture(t, RenderOptions{})
	if err := migrate.Validate(result); err != nil {
		t.Fatalf("result failed validation: %v", err)
	}

	cluster := result.Cluster
	if cluster.APIVersion != migrate.APIVersion || cluster.Kind != migrate.KindImport || cluster.Metadata.Name != "office" {
		t.Errorf("cluster header = %+v", cluster.Metadata)
	}
	if len(cluster.Spec.Stacks) != 1 || cluster.Spec.Stacks[0].Name != "office" {
		t.Fatalf("stacks = %+v, want one named office", cluster.Spec.Stacks)
	}

	if got := parentNames(manifestByName(t, result, "app")); got != "namespace,migrate,postgres" {
		t.Errorf("app parents = %s, want namespace,migrate,postgres (depends_on became parents)", got)
	}
	if got := parentNames(manifestByName(t, result, "postgres")); got != "namespace,volumes" {
		t.Errorf("postgres parents = %s, want namespace,volumes (stateful waits for claims)", got)
	}
	if got := parentNames(manifestByName(t, result, "migrate")); got != "namespace,postgres" {
		t.Errorf("migrate parents = %s", got)
	}
	if got := parentNames(manifestByName(t, result, "namespace")); got != "" {
		t.Errorf("namespace should have no parents, got %s", got)
	}
}

func TestRenderWorkloadKinds(t *testing.T) {
	result := renderFixture(t, RenderOptions{})

	postgres := result.Files["manifests/postgres.yaml"]
	for _, fragment := range []string{
		"kind: Deployment", "type: Recreate", "enableServiceLinks: false",
		"kind: Secret", "POSTGRES_PASSWORD: s3cret",
		"kind: ConfigMap", "POSTGRES_DB: office",
		"name: postgres-files", "init.sql: |", "subPath: init.sql",
		"claimName: pgdata", "mountPath: /var/lib/postgresql/data",
		"readinessProbe:", "- pg_isready -U office", "periodSeconds: 5", "failureThreshold: 10",
		"memory: 2Gi", "cpu: \"2\"",
		"kind: Service", "targetPort: 5432",
	} {
		if !strings.Contains(postgres, fragment) {
			t.Errorf("postgres.yaml lacks %q\n%s", fragment, postgres)
		}
	}
	if strings.Contains(postgres, "POSTGRES_DB: office\n") && strings.Contains(strings.SplitN(postgres, "kind: Secret", 2)[1], "POSTGRES_DB") {
		t.Error("POSTGRES_DB is not a credential and must not land in the Secret")
	}

	migrateFile := result.Files["manifests/migrate.yaml"]
	if !strings.Contains(migrateFile, "kind: Job") || !strings.Contains(migrateFile, "restartPolicy: OnFailure") {
		t.Errorf("migrate (restart: no) should be a Job:\n%s", migrateFile)
	}
	if !strings.Contains(migrateFile, "image: office-migrate:latest") {
		t.Errorf("a build-only service gets the compose-style image name:\n%s", migrateFile)
	}

	worker := result.Files["manifests/worker.yaml"]
	if !strings.Contains(worker, "kind: Deployment") {
		t.Errorf("worker without a restart policy must stay a Deployment, never a silent one-shot Job:\n%s", worker)
	}
	if !strings.Contains(worker, "memory: 512Mi") || !strings.Contains(worker, "cpu: 500m") {
		t.Errorf("worker deploy limits missing:\n%s", worker)
	}

	app := result.Files["manifests/app.yaml"]
	if !strings.Contains(app, "image: example/app:1.0") {
		t.Errorf("app should use its declared image over the build:\n%s", app)
	}
	if !strings.Contains(app, "protocol: UDP") || !strings.Contains(app, "name: udp-53") {
		t.Errorf("udp port missing from app service:\n%s", app)
	}

	volumes := result.Files["manifests/volumes.yaml"]
	if !strings.Contains(volumes, "kind: PersistentVolumeClaim") || !strings.Contains(volumes, "name: pgdata") || !strings.Contains(volumes, "storage: 10Gi") {
		t.Errorf("volumes.yaml should hold a 10Gi pgdata claim:\n%s", volumes)
	}

	if !strings.Contains(result.Files["manifests/namespace.yaml"], "kind: Namespace") {
		t.Error("namespace manifest missing")
	}
}

func TestRenderWarnings(t *testing.T) {
	result := renderFixture(t, RenderOptions{})
	for _, fragment := range []string{
		"migrate: image is built locally",
		"--option image.migrate=",
		"postgres: 1 credential(s) were written to Secret postgres-secrets",
		"ankra cluster encrypt",
		"app: publishes port 8080 on the host",
		"--option ingress.app=",
		"postgres: MISSING has no value",
	} {
		if !hasWarning(result.Warnings, fragment) {
			t.Errorf("missing warning %q in:\n  %s", fragment, strings.Join(result.Warnings, "\n  "))
		}
	}
}

func TestRenderIngressAndOverrides(t *testing.T) {
	result := renderFixture(t, RenderOptions{
		Ingress:       map[string]string{"app": "example.com"},
		ClusterIssuer: "letsencrypt-prod",
		Images:        map[string]string{"migrate": "ghcr.io/example/office:abc123"},
		VolumeSize:    "25Gi",
		StorageClass:  "local-path",
	})
	app := result.Files["manifests/app.yaml"]
	for _, fragment := range []string{
		"kind: Ingress", "host: example.com", "number: 3000", "pathType: Prefix",
		"cert-manager.io/cluster-issuer: letsencrypt-prod", "secretName: app-tls",
	} {
		if !strings.Contains(app, fragment) {
			t.Errorf("app.yaml lacks %q\n%s", fragment, app)
		}
	}
	if hasWarning(result.Warnings, "app: publishes port 8080") {
		t.Error("the public-port hint should disappear once an ingress host is given")
	}
	if !strings.Contains(result.Files["manifests/migrate.yaml"], "image: ghcr.io/example/office:abc123") {
		t.Error("image override was not applied")
	}
	if hasWarning(result.Warnings, "migrate: image is built locally") {
		t.Error("the build warning should disappear once the image is overridden")
	}
	volumes := result.Files["manifests/volumes.yaml"]
	if !strings.Contains(volumes, "storage: 25Gi") || !strings.Contains(volumes, "storageClassName: local-path") {
		t.Errorf("volume options not applied:\n%s", volumes)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	first := renderFixture(t, RenderOptions{})
	second := renderFixture(t, RenderOptions{})
	for file := range first.Files {
		if first.Files[file] != second.Files[file] {
			t.Errorf("%s differs between runs", file)
		}
	}
	if strings.Join(first.Warnings, "\n") != strings.Join(second.Warnings, "\n") {
		t.Error("warnings differ between runs")
	}
}
