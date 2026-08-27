package docker

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const testDockerfile = `# syntax=docker/dockerfile:1
FROM node:22-bookworm-slim AS base
ENV PNPM_HOME=/pnpm
EXPOSE 3000

FROM base AS build
RUN echo building \
    && echo done

FROM base AS runtime
ENV NODE_ENV=production
EXPOSE 53/udp
VOLUME ["/data", "/var/log/app"]
HEALTHCHECK --interval=15s --timeout=5s --retries=5 --start-period=60s \
  CMD curl -f http://localhost:3000/health || exit 1
CMD ["node", "server.js"]
`

func TestLoadDockerfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My App")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte(testDockerfile), 0o644); err != nil {
		t.Fatal(err)
	}

	project, warnings, err := LoadDockerfile(dir, path, DockerfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(project.Workloads) != 1 {
		t.Fatalf("workloads = %d, want 1", len(project.Workloads))
	}
	workload := project.Workloads[0]

	if workload.Name != "my-app" {
		t.Errorf("name = %q, want my-app (sanitised directory name)", workload.Name)
	}
	if workload.Build == nil || workload.Build.Dockerfile != "Dockerfile" {
		t.Errorf("build = %+v", workload.Build)
	}
	if len(workload.Command) != 0 || len(workload.Args) != 0 {
		t.Errorf("CMD/ENTRYPOINT must stay in the image, got command=%v args=%v", workload.Command, workload.Args)
	}
	if len(workload.Env) != 0 {
		t.Errorf("ENV must stay in the image, got %v", workload.Env)
	}

	var ports []string
	for _, port := range workload.Ports {
		ports = append(ports, strings.Join([]string{itoa(port.Container), port.Protocol}, "/"))
	}
	if got := strings.Join(ports, ","); got != "3000/tcp,53/udp" {
		t.Errorf("ports = %s, want 3000/tcp (inherited from base) and 53/udp", got)
	}

	if !workload.Stateful || len(workload.Volumes) != 2 {
		t.Fatalf("volumes = %+v, want two named", workload.Volumes)
	}
	if workload.Volumes[0].Name != "data" || workload.Volumes[1].Name != "var-log-app" || !workload.Volumes[1].Named {
		t.Errorf("volumes = %+v", workload.Volumes)
	}
	if got := strings.Join(project.Volumes, ","); got != "data,var-log-app" {
		t.Errorf("project volumes = %s", got)
	}

	health := workload.Healthcheck
	if health == nil {
		t.Fatal("healthcheck missing")
	}
	if health.IntervalSec != 15 || health.TimeoutSec != 5 || health.Retries != 5 || health.StartSec != 60 {
		t.Errorf("healthcheck timings = %+v", health)
	}
	if len(health.Test) != 2 || health.Test[0] != "CMD-SHELL" || !strings.HasPrefix(health.Test[1], "curl -f") {
		t.Errorf("healthcheck test = %v", health.Test)
	}
}

func TestLoadDockerfileHealthcheckNone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte("FROM alpine\nHEALTHCHECK NONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project, _, err := LoadDockerfile(dir, path, DockerfileOptions{Name: "svc", Image: "example/svc:1"})
	if err != nil {
		t.Fatal(err)
	}
	if project.Workloads[0].Healthcheck != nil {
		t.Error("HEALTHCHECK NONE should yield no probe")
	}
	if project.Workloads[0].Image != "example/svc:1" || project.Workloads[0].Name != "svc" {
		t.Errorf("options not applied: %+v", project.Workloads[0])
	}
}

func TestLoadDockerfileWithoutFrom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte("# empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadDockerfile(dir, path, DockerfileOptions{}); err == nil {
		t.Error("a Dockerfile with no FROM should be an error")
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
