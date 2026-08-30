package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// runDocker shells out to the docker CLI, against a remote daemon when host
// is set. Tests replace it.
var runDocker = func(ctx context.Context, host string, args ...string) ([]byte, error) {
	command := dockerCommand(ctx, host, args)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}

// DaemonOptions select which running containers to read.
type DaemonOptions struct {
	// Host is DOCKER_HOST for the read; empty means the local daemon.
	Host string
	// Project limits the read to one compose project, by label.
	Project string
	// Containers names specific containers; when set, Project is ignored.
	Containers []string
	// All includes stopped containers.
	All bool
}

// Labels compose stamps on the containers it starts.
const (
	labelComposeProject   = "com.docker.compose.project"
	labelComposeService   = "com.docker.compose.service"
	labelComposeDependsOn = "com.docker.compose.depends_on"
)

type inspectedContainer struct {
	Name   string `json:"Name"`
	Config struct {
		Image        string              `json:"Image"`
		Env          []string            `json:"Env"`
		Cmd          []string            `json:"Cmd"`
		Entrypoint   []string            `json:"Entrypoint"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		Labels       map[string]string   `json:"Labels"`
		Healthcheck  *struct {
			Test        []string `json:"Test"`
			Interval    int64    `json:"Interval"`
			Timeout     int64    `json:"Timeout"`
			Retries     int      `json:"Retries"`
			StartPeriod int64    `json:"StartPeriod"`
		} `json:"Healthcheck"`
	} `json:"Config"`
	HostConfig struct {
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
		Memory   int64 `json:"Memory"`
		NanoCPUs int64 `json:"NanoCpus"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
}

// LoadDaemon reads what is actually running from the Docker daemon. It is the
// source of truth when a compose file has drifted from the containers it once
// started, which is common on a long-lived host.
func LoadDaemon(ctx context.Context, options DaemonOptions) (Project, []string, error) {
	ids := options.Containers
	if len(ids) == 0 {
		args := []string{"ps", "-q"}
		if options.All {
			args = append(args, "-a")
		}
		if options.Project != "" {
			args = append(args, "--filter", "label="+labelComposeProject+"="+options.Project)
		}
		output, err := runDocker(ctx, options.Host, args...)
		if err != nil {
			return Project{}, nil, err
		}
		ids = strings.Fields(string(output))
	}
	if len(ids) == 0 {
		return Project{}, nil, fmt.Errorf("no containers found")
	}

	output, err := runDocker(ctx, options.Host, append([]string{"inspect"}, ids...)...)
	if err != nil {
		return Project{}, nil, err
	}
	var containers []inspectedContainer
	if err := json.Unmarshal(output, &containers); err != nil {
		return Project{}, nil, fmt.Errorf("reading docker inspect output: %w", err)
	}

	project := Project{Name: options.Project, Source: "docker daemon"}
	var warnings []string
	volumes := map[string]bool{}
	for _, container := range containers {
		workload, containerWarnings := daemonWorkload(container)
		if project.Name == "" {
			project.Name = container.Config.Labels[labelComposeProject]
		}
		for _, volume := range workload.Volumes {
			if volume.Named {
				volumes[volume.Name] = true
			}
		}
		project.Workloads = append(project.Workloads, workload)
		warnings = append(warnings, containerWarnings...)
	}
	if project.Name == "" {
		project.Name = "docker"
	}
	for name := range volumes {
		project.Volumes = append(project.Volumes, name)
	}
	sort.Strings(project.Volumes)
	sort.Slice(project.Workloads, func(i, j int) bool { return project.Workloads[i].Name < project.Workloads[j].Name })
	return project, warnings, nil
}

// Variables the runtime injects into every container; they say nothing about
// the application and would only pin runtime internals into a manifest.
var runtimeInjectedEnv = map[string]bool{"PATH": true, "HOSTNAME": true, "HOME": true}

func daemonWorkload(container inspectedContainer) (Workload, []string) {
	var warnings []string
	name := container.Config.Labels[labelComposeService]
	if name == "" {
		name = sanitiseName(strings.TrimPrefix(container.Name, "/"))
		warnings = append(warnings, fmt.Sprintf("%s: not started by compose, so its start order is unknown", name))
	}

	workload := Workload{
		Name:    name,
		Image:   container.Config.Image,
		Command: container.Config.Entrypoint,
		Args:    container.Config.Cmd,
		Restart: container.HostConfig.RestartPolicy.Name,
	}

	for _, entry := range container.Config.Env {
		key, value, _ := strings.Cut(entry, "=")
		if runtimeInjectedEnv[key] {
			continue
		}
		workload.Env = append(workload.Env, EnvVar{Name: key, Value: value, Secret: looksSecret(key, value)})
	}

	exposed := map[string]bool{}
	for spec, bindings := range container.HostConfig.PortBindings {
		port, ok := parsePortSpec(spec)
		if !ok {
			continue
		}
		exposed[spec] = true
		for _, binding := range bindings {
			port.Host, _ = strconv.Atoi(binding.HostPort)
			port.Public = isPublicHostIP(binding.HostIP)
			break
		}
		workload.Ports = append(workload.Ports, port)
	}
	for spec := range container.Config.ExposedPorts {
		if exposed[spec] {
			continue
		}
		if port, ok := parsePortSpec(spec); ok {
			workload.Ports = append(workload.Ports, port)
		}
	}
	sort.Slice(workload.Ports, func(i, j int) bool { return workload.Ports[i].Container < workload.Ports[j].Container })

	for _, mount := range container.Mounts {
		volume := Volume{Source: mount.Source, Target: mount.Destination, ReadOnly: !mount.RW}
		switch mount.Type {
		case "volume":
			volume.Named = true
			volume.Name = mount.Name
			workload.Stateful = true
		case "bind":
			classifyBind("", &volume)
			if volume.BindDir {
				warnings = append(warnings, fmt.Sprintf("%s: bind mount %s is a host directory; mount a PersistentVolumeClaim at %s and copy the data across", name, mount.Source, mount.Destination))
			}
		default:
			continue
		}
		workload.Volumes = append(workload.Volumes, volume)
	}

	if dependsOn := container.Config.Labels[labelComposeDependsOn]; dependsOn != "" {
		for _, entry := range strings.Split(dependsOn, ",") {
			dependency, _, _ := strings.Cut(entry, ":")
			if dependency != "" {
				workload.DependsOn = append(workload.DependsOn, dependency)
			}
		}
		sort.Strings(workload.DependsOn)
	}

	if health := container.Config.Healthcheck; health != nil && len(health.Test) > 0 && health.Test[0] != "NONE" {
		workload.Healthcheck = &Healthcheck{
			Test:        health.Test,
			IntervalSec: int(health.Interval / 1e9),
			TimeoutSec:  int(health.Timeout / 1e9),
			Retries:     health.Retries,
			StartSec:    int(health.StartPeriod / 1e9),
		}
	}

	if container.HostConfig.Memory > 0 {
		workload.Memory = strconv.FormatInt(container.HostConfig.Memory/(1<<20), 10) + "Mi"
	}
	if container.HostConfig.NanoCPUs > 0 {
		workload.CPU = normaliseCPU(strconv.FormatFloat(float64(container.HostConfig.NanoCPUs)/1e9, 'f', -1, 64))
	}

	return workload, warnings
}

func parsePortSpec(spec string) (Port, bool) {
	number, protocol, found := strings.Cut(spec, "/")
	if !found {
		protocol = "tcp"
	}
	container, err := strconv.Atoi(number)
	if err != nil || container == 0 {
		return Port{}, false
	}
	return Port{Container: container, Protocol: protocol}, true
}
