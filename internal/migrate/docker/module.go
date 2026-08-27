package docker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ankra/internal/migrate"
)

// Module is the built-in Docker module. It reads compose files, bare
// Dockerfiles, and the live daemon, and is also the reference for what an
// external module must do.
type Module struct{}

// New returns the Docker module.
func New() migrate.Module { return Module{} }

// Option names accepted through --option key=value.
const (
	OptionSource         = "source"          // compose | dockerfile | daemon (default: detected)
	OptionProfiles       = "profiles"        // comma-separated compose profiles to include
	OptionAllProfiles    = "all-profiles"    // include every compose profile
	OptionUseEnvironment = "use-environment" // let the process environment satisfy ${VAR}
	OptionProject        = "project"         // compose project label for the daemon source
	OptionContainers     = "containers"      // comma-separated container names/ids for the daemon source
	OptionAll            = "all"             // include stopped containers for the daemon source
	OptionName           = "name"            // workload name for the dockerfile source
	OptionVolumeSize     = "volume-size"     // PersistentVolumeClaim request, default 10Gi
	OptionStorageClass   = "storage-class"   // storageClassName for every claim
	OptionClusterIssuer  = "cluster-issuer"  // cert-manager ClusterIssuer for generated Ingresses
	OptionImagePrefix    = "image."          // image.<workload>=<ref> overrides one workload's image
	OptionIngressPrefix  = "ingress."        // ingress.<workload>=<host> exposes one workload
)

// Describe implements migrate.Module.
func (Module) Describe() migrate.Description {
	return migrate.Description{
		Name:         "docker",
		Version:      "1",
		Protocol:     migrate.ProtocolVersion,
		Summary:      "docker-compose files, Dockerfiles, and the running Docker daemon",
		FilePatterns: append(append([]string(nil), ComposeFileNames...), "Dockerfile"),
		Builtin:      true,
	}
}

// Detect implements migrate.Module. A compose file is the strongest signal
// because it describes a whole deployment; a lone Dockerfile only describes
// one image.
func (Module) Detect(_ context.Context, dir string) (migrate.Detection, error) {
	if path, ok := FindComposeFile(dir); ok {
		return migrate.Detection{Confidence: 1, Files: []string{relative(dir, path)}, Reason: "compose file present"}, nil
	}
	if path, ok := FindDockerfile(dir); ok {
		return migrate.Detection{Confidence: 0.6, Files: []string{relative(dir, path)}, Reason: "Dockerfile present, no compose file"}, nil
	}
	return migrate.Detection{Reason: "no compose file or Dockerfile"}, nil
}

// Convert implements migrate.Module.
func (Module) Convert(ctx context.Context, request migrate.ConvertRequest) (migrate.Result, error) {
	options := request.Options
	if options == nil {
		options = map[string]string{}
	}

	source := options[OptionSource]
	if source == "" {
		switch {
		case options[OptionProject] != "" || options[OptionContainers] != "":
			source = "daemon"
		default:
			if _, ok := FindComposeFile(request.Dir); ok {
				source = "compose"
			} else if _, ok := FindDockerfile(request.Dir); ok {
				source = "dockerfile"
			} else {
				return migrate.Result{}, fmt.Errorf("no compose file or Dockerfile in %s (use --option source=daemon to read running containers)", request.Dir)
			}
		}
	}

	var (
		project  Project
		warnings []string
		err      error
	)
	switch source {
	case "compose":
		path, ok := FindComposeFile(request.Dir)
		if !ok {
			return migrate.Result{}, fmt.Errorf("no compose file in %s", request.Dir)
		}
		project, warnings, err = LoadCompose(request.Dir, path, ComposeOptions{
			Profiles:       splitList(options[OptionProfiles]),
			AllProfiles:    isTrue(options[OptionAllProfiles]),
			UseEnvironment: isTrue(options[OptionUseEnvironment]),
		})
	case "dockerfile":
		path, ok := FindDockerfile(request.Dir)
		if !ok {
			return migrate.Result{}, fmt.Errorf("no Dockerfile in %s", request.Dir)
		}
		project, warnings, err = LoadDockerfile(request.Dir, path, DockerfileOptions{
			Name:  options[OptionName],
			Image: options[OptionImagePrefix+options[OptionName]],
		})
	case "daemon":
		project, warnings, err = LoadDaemon(ctx, DaemonOptions{
			Project:    options[OptionProject],
			Containers: splitList(options[OptionContainers]),
			All:        isTrue(options[OptionAll]),
		})
	default:
		return migrate.Result{}, fmt.Errorf("unknown source %q: expected compose, dockerfile, or daemon", source)
	}
	if err != nil {
		return migrate.Result{}, err
	}

	render := RenderOptions{
		ClusterName:   request.ClusterName,
		Namespace:     request.Namespace,
		VolumeSize:    options[OptionVolumeSize],
		StorageClass:  options[OptionStorageClass],
		ClusterIssuer: options[OptionClusterIssuer],
		Images:        prefixed(options, OptionImagePrefix),
		Ingress:       prefixed(options, OptionIngressPrefix),
	}
	result := Render(project, render)
	result.Warnings = uniqueSorted(append(warnings, result.Warnings...))
	return result, nil
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	out := values[:0]
	for i, value := range values {
		if i == 0 || value != values[i-1] {
			out = append(out, value)
		}
	}
	return out
}

func prefixed(options map[string]string, prefix string) map[string]string {
	out := map[string]string{}
	for key, value := range options {
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) {
			out[key[len(prefix):]] = value
		}
	}
	return out
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func isTrue(value string) bool {
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func relative(dir, path string) string {
	if strings.HasPrefix(path, dir) {
		return strings.TrimPrefix(strings.TrimPrefix(path, dir), "/")
	}
	return path
}
