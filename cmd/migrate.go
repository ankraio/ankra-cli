package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/migrate"
	"ankra/internal/migrate/docker"
)

// migrateCmd converts an existing deployment description into Ankra
// resources. Every subcommand works on local files and never calls the
// platform API, so none of them require a login.
var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Convert an existing deployment (docker-compose, Dockerfile, running containers) into Ankra cluster and stack definitions",
	Long: `Convert an existing deployment into an ImportCluster manifest plus the
Kubernetes manifests its stack refers to, ready for 'ankra cluster apply'.

Conversion is done by modules. The 'docker' module is built in and reads
docker-compose files, bare Dockerfiles, and the running Docker daemon. Anyone
can add a module for another format by putting an executable named
'ankra-module-<name>' on PATH or in ~/.ankra/modules - see
'ankra migrate modules --help' for the contract.

Nothing here talks to the platform: the commands read local files, write a
directory, and leave applying it to you.`,
	Annotations: map[string]string{annotationRequiresAuth: "false"},
}

// newMigrateRegistry builds the module registry. It is a variable so tests
// can substitute a registry that does not scan PATH.
var newMigrateRegistry = func() *migrate.Registry {
	return migrate.NewRegistry(docker.New())
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}

// selectMigrateModule picks the module by name, or by asking each one about
// the directory and taking the most confident answer.
func selectMigrateModule(cmd *cobra.Command, registry *migrate.Registry, dir, name string) (migrate.Module, error) {
	if name != "" {
		module, err := registry.Lookup(cmd.Context(), name)
		if err != nil {
			return nil, withExitCode(exitNotFound, err)
		}
		return module, nil
	}
	candidates, notes := registry.Detect(cmd.Context(), dir)
	for _, note := range notes {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), note)
	}
	if len(candidates) == 0 || candidates[0].Err != nil || candidates[0].Detection.Confidence == 0 {
		return nil, withExitCode(exitNotFound, fmt.Errorf("no module recognises %s (run `ankra migrate detect %s` to see why)", dir, dir))
	}
	return candidates[0].Module, nil
}

var migrateNamePattern = regexp.MustCompile(`[^a-z0-9-]+`)

// migrateResourceName makes a directory name usable as a cluster or namespace
// name.
func migrateResourceName(value string) string {
	value = migrateNamePattern.ReplaceAllString(strings.ToLower(value), "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "migrated"
	}
	return value
}
