package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The published manifest add-on commands. 'ankra application publish-addon'
// turns an application's manifest set into an entry in the organisation's
// add-on catalog and 'published-addon' reports what that produced; these are
// the operations on the catalog entry itself - inspect it, diff two published
// versions, install it onto a cluster, take it out of the catalog, or delete
// it outright.
//
// Unpublish and delete are not the same act. Unpublishing withdraws the entry
// from the catalog and leaves what is installed running; deleting undeploys
// every installation that came from it.

func newApplicationManifestAddonCommand() *cobra.Command {
	manifestAddonCommand := &cobra.Command{
		Use:     "manifest-addon",
		Aliases: []string{"manifest-addons"},
		Short:   "Inspect, install and withdraw add-ons published from an application's manifests",
		Long: `Inspect, install and withdraw add-ons published from an application's manifests.

Publish one with 'ankra application publish-addon <application-id>' and find its
id with 'ankra application published-addon <application-id>'. These commands
take that add-on id, not an application id.`,
	}
	manifestAddonCommand.AddCommand(
		newManifestAddonGetCommand(),
		newManifestAddonDiffCommand(),
		newManifestAddonInstallCommand(),
		newManifestAddonUnpublishCommand(),
		newManifestAddonDeleteCommand(),
	)
	return manifestAddonCommand
}

func newManifestAddonGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:     "get <addon-id>",
		Short:   "Show a published manifest add-on",
		Long:    "Show a published manifest add-on: its catalog entry, published versions, and what it declares.",
		Example: "  ankra application manifest-addon get 8f2c1b90-4d6e-4a11-9c33-2f7a5b0e91d4",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, getError := apiClient.GetManifestAddon(command.Context(), strings.TrimSpace(arguments[0]))
			if getError != nil {
				return getError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newManifestAddonDiffCommand() *cobra.Command {
	diffCommand := &cobra.Command{
		Use:   "diff <addon-id>",
		Short: "Compare two published versions of a manifest add-on",
		Long: `Compare two published versions of a manifest add-on.

--to names the version to compare; --from names what to compare it against,
defaulting to the version published before it. --path narrows the comparison to
the named manifests and may be repeated.`,
		Example: `  ankra application manifest-addon diff <addon-id> --to 1.4.0
  ankra application manifest-addon diff <addon-id> --to 1.4.0 --from 1.2.0 --path deployment.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			toVersion := strings.TrimSpace(mustFlagString(command, "to"))
			if toVersion == "" {
				return withExitCode(exitUsage,
					errors.New("--to is required: the published version to compare"))
			}
			fromVersion := strings.TrimSpace(mustFlagString(command, "from"))
			paths, _ := command.Flags().GetStringArray("path")
			payload, diffError := apiClient.DiffManifestAddon(command.Context(),
				strings.TrimSpace(arguments[0]), toVersion, fromVersion, paths)
			if diffError != nil {
				return diffError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	diffCommand.Flags().String("to", "", "Published version to compare (required)")
	diffCommand.Flags().String("from", "", "Version to compare against (defaults to the one published before --to)")
	diffCommand.Flags().StringArray("path", nil, "Limit the comparison to this manifest path (repeatable)")
	registerStructuredOutputFlags(diffCommand)
	return diffCommand
}

func newManifestAddonInstallCommand() *cobra.Command {
	installCommand := &cobra.Command{
		Use:   "install <addon-id>",
		Short: "Install a published manifest add-on onto a cluster",
		Long: `Install a published manifest add-on onto a cluster.

--cluster-id is required. --namespace and --version default from the add-on's
own descriptor. --input answers an input the published manifests declare and
may be repeated as --input key=value.`,
		Example: `  ankra application manifest-addon install <addon-id> --cluster-id <cluster-id>
  ankra application manifest-addon install <addon-id> --cluster-id <cluster-id> \
    --namespace commerce --version 1.4.0 --input replicas=3`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			clusterID := strings.TrimSpace(mustFlagString(command, "cluster-id"))
			if clusterID == "" {
				return withExitCode(exitUsage,
					errors.New("--cluster-id is required: the cluster to install the add-on onto"))
			}
			rawInputs, _ := command.Flags().GetStringArray("input")
			inputs, inputsError := parseManifestAddonInputs(rawInputs)
			if inputsError != nil {
				return inputsError
			}
			payload, installError := apiClient.InstallManifestAddon(command.Context(),
				strings.TrimSpace(arguments[0]), client.InstallManifestAddonRequest{
					ClusterID: clusterID,
					Namespace: strings.TrimSpace(mustFlagString(command, "namespace")),
					Version:   strings.TrimSpace(mustFlagString(command, "version")),
					Inputs:    inputs,
				})
			if installError != nil {
				return installError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	installCommand.Flags().String("cluster-id", "", "Cluster to install the add-on onto (required)")
	installCommand.Flags().String("namespace", "", "Namespace to install into (defaults to the add-on's own)")
	installCommand.Flags().String("version", "", "Published version to install (defaults to the newest)")
	installCommand.Flags().StringArray("input", nil, "Answer a declared input as key=value (repeatable)")
	registerStructuredOutputFlags(installCommand)
	return installCommand
}

// parseManifestAddonInputs turns repeated --input key=value flags into the
// inputs map. A value may contain '=' - only the first one separates - and a
// missing '=' is a usage error rather than a key silently answered with "".
func parseManifestAddonInputs(rawInputs []string) (map[string]string, error) {
	if len(rawInputs) == 0 {
		return nil, nil
	}
	inputs := make(map[string]string, len(rawInputs))
	for _, rawInput := range rawInputs {
		key, value, found := strings.Cut(rawInput, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, withExitCode(exitUsage,
				fmt.Errorf("--input %q is not key=value", rawInput))
		}
		inputs[key] = value
	}
	return inputs, nil
}

func newManifestAddonUnpublishCommand() *cobra.Command {
	unpublishCommand := &cobra.Command{
		Use:   "unpublish <addon-id>",
		Short: "Withdraw a manifest add-on from the catalog, leaving installations running",
		Long: `Withdraw a manifest add-on from the catalog, leaving installations running.

Nobody can install it again afterwards. What is already installed from it keeps
running - use 'delete' if you want those undeployed too.`,
		Example: "  ankra application manifest-addon unpublish <addon-id>",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			addonID := strings.TrimSpace(arguments[0])
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(
				command.InOrStdin(), command.OutOrStdout(),
				fmt.Sprintf("Withdraw add-on %q from the catalog? "+
					"Existing installations keep running, but nobody can install it again. [y/N]: ", addonID),
				yes,
			); confirmError != nil {
				return confirmError
			}
			payload, unpublishError := apiClient.UnpublishManifestAddon(command.Context(), addonID)
			if unpublishError != nil {
				return unpublishError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	unpublishCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(unpublishCommand)
	return unpublishCommand
}

func newManifestAddonDeleteCommand() *cobra.Command {
	deleteCommand := &cobra.Command{
		Use:   "delete <addon-id>",
		Short: "Delete a manifest add-on and undeploy every installation of it",
		Long: `Delete a manifest add-on and undeploy every installation of it.

This is the most consequential of the three and the least reversible: it does
not only withdraw the catalog entry, it undeploys the workloads installed from
it on every cluster. Use 'unpublish' to withdraw the entry and leave those
running.`,
		Example: "  ankra application manifest-addon delete <addon-id>",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			addonID := strings.TrimSpace(arguments[0])
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(
				command.InOrStdin(), command.OutOrStdout(),
				fmt.Sprintf("Delete add-on %q? This undeploys every installation that came from it "+
					"and cannot be undone! [y/N]: ", addonID),
				yes,
			); confirmError != nil {
				return confirmError
			}
			payload, deleteError := apiClient.DeleteManifestAddon(command.Context(), addonID)
			if deleteError != nil {
				return deleteError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	deleteCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(deleteCommand)
	return deleteCommand
}
