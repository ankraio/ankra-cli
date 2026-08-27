package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testInspect = `[
  {
    "Name": "/aura-office-app-1",
    "Config": {
      "Image": "aura-office-app",
      "Env": ["PATH=/usr/bin", "HOSTNAME=abc", "NODE_ENV=production", "DB_PASSWORD=hunter2"],
      "Cmd": ["pnpm", "start"],
      "Entrypoint": ["./docker/entrypoint.sh"],
      "ExposedPorts": {"3000/tcp": {}, "9229/tcp": {}},
      "Labels": {
        "com.docker.compose.project": "aura-office",
        "com.docker.compose.service": "app",
        "com.docker.compose.depends_on": "postgres:service_healthy:false,redis:service_healthy:false"
      },
      "Healthcheck": {"Test": ["CMD", "curl", "-f", "http://127.0.0.1:3000/api/health"], "Interval": 15000000000, "Timeout": 5000000000, "Retries": 5, "StartPeriod": 60000000000}
    },
    "HostConfig": {
      "PortBindings": {"3000/tcp": [{"HostIp": "0.0.0.0", "HostPort": "8080"}]},
      "RestartPolicy": {"Name": "unless-stopped"},
      "Memory": 2147483648,
      "NanoCpus": 500000000
    },
    "Mounts": [
      {"Type": "volume", "Name": "aura-office_pgdata", "Source": "/var/lib/docker/volumes/x/_data", "Destination": "/data", "RW": true},
      {"Type": "bind", "Source": "BINDFILE", "Destination": "/etc/app/config.yaml", "RW": false}
    ]
  },
  {
    "Name": "/redis-1",
    "Config": {"Image": "redis:7", "Env": [], "Cmd": ["redis-server"], "Labels": {}},
    "HostConfig": {"PortBindings": {}, "RestartPolicy": {"Name": "always"}},
    "Mounts": []
  }
]`

func TestLoadDaemon(t *testing.T) {
	bindFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(bindFile, []byte("key: value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls []string
	original := runDocker
	runDocker = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "ps":
			return []byte("abc123\ndef456\n"), nil
		case "inspect":
			return []byte(strings.ReplaceAll(testInspect, "BINDFILE", bindFile)), nil
		}
		t.Fatalf("unexpected docker call %v", args)
		return nil, nil
	}
	defer func() { runDocker = original }()

	project, warnings, err := LoadDaemon(context.Background(), DaemonOptions{Project: "aura-office"})
	if err != nil {
		t.Fatal(err)
	}

	if calls[0] != "ps -q --filter label=com.docker.compose.project=aura-office" {
		t.Errorf("ps call = %q", calls[0])
	}
	if calls[1] != "inspect abc123 def456" {
		t.Errorf("inspect call = %q", calls[1])
	}
	if project.Name != "aura-office" || project.Source != "docker daemon" {
		t.Errorf("project = %+v", project)
	}
	if got := strings.Join(workloadNames(project), ","); got != "app,redis-1" {
		t.Errorf("workloads = %s", got)
	}

	app := findWorkload(t, project, "app")
	if app.Image != "aura-office-app" || strings.Join(app.Command, " ") != "./docker/entrypoint.sh" || strings.Join(app.Args, " ") != "pnpm start" {
		t.Errorf("app image/command/args = %q %v %v", app.Image, app.Command, app.Args)
	}
	if _, ok := envValue(app, "PATH"); ok {
		t.Error("runtime-injected PATH must be dropped")
	}
	if password, ok := envValue(app, "DB_PASSWORD"); !ok || !password.Secret {
		t.Errorf("DB_PASSWORD = %+v, want present and secret", password)
	}
	if len(app.Ports) != 2 || app.Ports[0].Container != 3000 || app.Ports[0].Host != 8080 || !app.Ports[0].Public || app.Ports[1].Container != 9229 || app.Ports[1].Public {
		t.Errorf("app ports = %+v, want published 3000->8080 and unpublished 9229", app.Ports)
	}
	if strings.Join(app.DependsOn, ",") != "postgres,redis" {
		t.Errorf("depends_on from label = %v", app.DependsOn)
	}
	if app.Healthcheck == nil || app.Healthcheck.IntervalSec != 15 || app.Healthcheck.StartSec != 60 || app.Healthcheck.Test[0] != "CMD" {
		t.Errorf("healthcheck = %+v", app.Healthcheck)
	}
	if app.Memory != "2048Mi" || app.CPU != "500m" || app.Restart != "unless-stopped" {
		t.Errorf("resources/restart = %s %s %s", app.Memory, app.CPU, app.Restart)
	}
	if !app.Stateful || len(app.Volumes) != 2 {
		t.Fatalf("volumes = %+v", app.Volumes)
	}
	if !app.Volumes[0].Named || app.Volumes[0].Name != "aura-office_pgdata" {
		t.Errorf("volume mount = %+v", app.Volumes[0])
	}
	if !app.Volumes[1].BindFile || app.Volumes[1].HostFiles["config.yaml"] != "key: value\n" || !app.Volumes[1].ReadOnly {
		t.Errorf("bind file mount = %+v", app.Volumes[1])
	}
	if got := strings.Join(project.Volumes, ","); got != "aura-office_pgdata" {
		t.Errorf("project volumes = %s", got)
	}

	if !hasWarning(warnings, "redis-1: not started by compose") {
		t.Errorf("expected a start-order warning for the unlabelled container, got %v", warnings)
	}
}

func TestLoadDaemonNoContainers(t *testing.T) {
	original := runDocker
	runDocker = func(_ context.Context, _ ...string) ([]byte, error) { return []byte("\n"), nil }
	defer func() { runDocker = original }()
	if _, _, err := LoadDaemon(context.Background(), DaemonOptions{}); err == nil {
		t.Error("no containers should be an error, not an empty conversion")
	}
}

func TestLoadDaemonExplicitContainersSkipPs(t *testing.T) {
	original := runDocker
	runDocker = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "ps" {
			t.Fatal("ps must not run when containers are named explicitly")
		}
		return []byte(strings.ReplaceAll(testInspect, "BINDFILE", "/nonexistent")), nil
	}
	defer func() { runDocker = original }()
	project, warnings, err := LoadDaemon(context.Background(), DaemonOptions{Containers: []string{"abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Workloads) != 2 {
		t.Errorf("workloads = %v", workloadNames(project))
	}
	if !hasWarning(warnings, "app: bind mount /nonexistent is a host directory") {
		t.Errorf("an unreadable bind source should warn as a directory, got %v", warnings)
	}
}
