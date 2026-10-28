package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// The environment and reporting level a cluster is created with. Every
// cluster create family accepts both on its body (the platform's provider and
// managed create decoders), so the flags are declared once here and every
// create and preflight command registers them. A blank flag sends nothing:
// the platform then labels the cluster development and pairs the reporting
// level with the environment itself, exactly as before the flags existed.
// `ankra cluster update` changes either value later.

// registerClusterClassificationFlags declares --environment and
// --criticality on each command.
func registerClusterClassificationFlags(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().String("environment", "",
			"Environment: production, staging, development, testing, local, or a name of your own (server default: development)")
		command.Flags().String("criticality", "",
			"Reporting level: production, staging, development or disposable (server default: the environment's own level)")
	}
}

// clusterClassificationFromFlags reads --environment and --criticality for a
// create body. A blank flag returns nil, so the field is omitted. Input the
// platform would refuse (an environment over its 50-byte limit, an unknown
// reporting level) is a usage error before anything is sent.
func clusterClassificationFromFlags(command *cobra.Command) (*string, *string, error) {
	environmentFlag, _ := command.Flags().GetString("environment")
	criticalityFlag, _ := command.Flags().GetString("criticality")

	var environment *string
	if trimmed := strings.TrimSpace(environmentFlag); trimmed != "" {
		if len(trimmed) > clusterEnvironmentNameLimit {
			return nil, nil, withExitCode(exitUsage, fmt.Errorf(
				"--environment must be at most %d bytes (a letter outside ASCII counts as two or more)",
				clusterEnvironmentNameLimit))
		}
		environment = &trimmed
	}

	var criticality *string
	if normalised := strings.ToLower(strings.TrimSpace(criticalityFlag)); normalised != "" {
		if !slices.Contains(clusterCriticalities, normalised) {
			return nil, nil, withExitCode(exitUsage, fmt.Errorf(
				"--criticality must be one of: %s", strings.Join(clusterCriticalities, ", ")))
		}
		criticality = &normalised
	}
	return environment, criticality, nil
}
