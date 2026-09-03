package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The owner-lifecycle stack-profile commands: create a profile from a
// deployed stack, edit its catalogue metadata, snapshot new versions, roll
// the current-version pointer, diff versions, list the fleet deployments,
// and delete it. Together with apply/export-iac/import they give the CLI
// the same control over a profile the portal has.

// parseRequiredProfileVersionArgument parses a positional version argument
// that must be present, accepting both 2 and v2.
func parseRequiredProfileVersionArgument(raw string) (int, error) {
	version, parseError := parseProfileVersionFlag(raw)
	if parseError != nil {
		return 0, withExitCode(exitUsage, parseError)
	}
	if version <= 0 {
		return 0, withExitCode(exitUsage,
			fmt.Errorf("invalid version %q: use a version number such as 1 or v1", raw))
	}
	return version, nil
}

// optionalString returns nil for an unset flag so the PATCH body leaves the
// stored value unchanged; a set-but-empty flag clears it server-side.
func optionalString(command *cobra.Command, flagName string) *string {
	if !command.Flags().Changed(flagName) {
		return nil
	}
	value, _ := command.Flags().GetString(flagName)
	return &value
}

var stackProfilesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a stack profile from a deployed cluster stack",
	Long: `Create a stack profile by snapshotting a deployed cluster stack as
version 1. The source stack's add-ons and manifests become the profile
contents; detected inputs become the profile's parameters.`,
	Example: `  ankra stack-profiles create --name postgres-ha --stack postgres --cluster staging \
    --category database --tag postgresql --tag ha --description "Production PostgreSQL"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		name, _ := cmd.Flags().GetString("name")
		if strings.TrimSpace(name) == "" {
			return withExitCode(exitUsage, errors.New("--name is required"))
		}
		stackName, _ := cmd.Flags().GetString("stack")
		if strings.TrimSpace(stackName) == "" {
			return withExitCode(exitUsage, errors.New("--stack is required: the source stack to snapshot"))
		}
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		clusterID, _, clusterError := resolveApplyTargetCluster(clusterFlag)
		if clusterError != nil {
			return clusterError
		}

		category, _ := cmd.Flags().GetString("category")
		tags, _ := cmd.Flags().GetStringArray("tag")
		includeAddonConfigurations, _ := cmd.Flags().GetBool("include-addon-configurations")
		request := client.CreateStackProfileFromStackRequest{
			Name:                       strings.TrimSpace(name),
			Description:                optionalString(cmd, "description"),
			LogoURL:                    optionalString(cmd, "logo-url"),
			Category:                   category,
			Tags:                       tags,
			Visibility:                 optionalString(cmd, "visibility"),
			SourceClusterID:            clusterID,
			StackName:                  strings.TrimSpace(stackName),
			IncludeAddonConfigurations: includeAddonConfigurations,
			Changelog:                  optionalString(cmd, "changelog"),
		}
		payload, createError := apiClient.CreateStackProfile(cmd.Context(), request)
		if createError != nil {
			return fmt.Errorf("creating stack profile: %w", createError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesUpdateCmd = &cobra.Command{
	Use:   "update [profile-id|profile-name]",
	Short: "Update a stack profile's catalogue metadata",
	Long: `Update how a stack profile appears in the catalogue: its name,
description, category, tags, logo URL, and visibility. Only the flags you
set change; pass an empty value to clear a field.`,
	Example: `  ankra stack-profiles update postgres-ha --description "Production PostgreSQL 17" --tag postgresql --tag cnpg
  ankra stack-profiles update postgres-ha --visibility public`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		request := client.UpdateStackProfileRequest{
			Name:        optionalString(cmd, "name"),
			Description: optionalString(cmd, "description"),
			LogoURL:     optionalString(cmd, "logo-url"),
			Category:    optionalString(cmd, "category"),
			Visibility:  optionalString(cmd, "visibility"),
		}
		if cmd.Flags().Changed("tag") {
			tags, _ := cmd.Flags().GetStringArray("tag")
			request.Tags = tags
		}
		if request.Name == nil && request.Description == nil && request.LogoURL == nil &&
			request.Category == nil && request.Visibility == nil && request.Tags == nil {
			return withExitCode(exitUsage, errors.New(
				"nothing to update: set at least one of --name, --description, --category, --tag, --logo-url, --visibility"))
		}
		payload, updateError := apiClient.UpdateStackProfile(cmd.Context(), profileID, request)
		if updateError != nil {
			return fmt.Errorf("updating stack profile: %w", updateError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesDeleteCmd = &cobra.Command{
	Use:   "delete [profile-id|profile-name]",
	Short: "Delete a stack profile",
	Long: `Delete a stack profile. Stacks already deployed from it keep running;
the profile stops appearing in the catalogue and can no longer be applied.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete stack profile %q? Deployed stacks keep running, "+
				"but the profile and its versions stop being available. [y/N]: ", args[0]),
			yes,
		); confirmError != nil {
			return confirmError
		}
		payload, deleteError := apiClient.DeleteStackProfile(cmd.Context(), profileID)
		if deleteError != nil {
			return fmt.Errorf("deleting stack profile: %w", deleteError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesSaveVersionCmd = &cobra.Command{
	Use:   "save-version [profile-id|profile-name]",
	Short: "Snapshot a source stack as the profile's next version",
	Long: `Save a new profile version by re-snapshotting a deployed stack. The new
version becomes the profile's current version.`,
	Example: `  ankra stack-profiles save-version postgres-ha --stack postgres --cluster staging \
    --changelog "Bump CloudNativePG to 1.30"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		stackName, _ := cmd.Flags().GetString("stack")
		if strings.TrimSpace(stackName) == "" {
			return withExitCode(exitUsage, errors.New("--stack is required: the source stack to snapshot"))
		}
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		clusterID, _, clusterError := resolveApplyTargetCluster(clusterFlag)
		if clusterError != nil {
			return clusterError
		}
		channel, _ := cmd.Flags().GetString("channel")
		includeAddonConfigurations, _ := cmd.Flags().GetBool("include-addon-configurations")
		request := client.SaveStackProfileVersionRequest{
			SourceClusterID:            clusterID,
			StackName:                  strings.TrimSpace(stackName),
			IncludeAddonConfigurations: includeAddonConfigurations,
			Channel:                    channel,
			Changelog:                  optionalString(cmd, "changelog"),
		}
		payload, saveError := apiClient.SaveStackProfileVersion(cmd.Context(), profileID, request)
		if saveError != nil {
			return fmt.Errorf("saving stack profile version: %w", saveError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesSetCurrentVersionCmd = &cobra.Command{
	Use:   "set-current-version [profile-id|profile-name] <version>",
	Short: "Point the profile's current version at a published version",
	Long: `Set which published version new deployments of this profile resolve to.
Rolling back is setting the pointer to an earlier version.`,
	Example: "  ankra stack-profiles set-current-version postgres-ha v3",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		version, versionError := parseRequiredProfileVersionArgument(args[1])
		if versionError != nil {
			return versionError
		}
		payload, setError := apiClient.SetStackProfileCurrentVersion(cmd.Context(), profileID, version)
		if setError != nil {
			return fmt.Errorf("setting current version: %w", setError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesVersionCmd = &cobra.Command{
	Use:   "version [profile-id|profile-name] <version>",
	Short: "Show one published version's record and parameters",
	Long: `Print the stored record of one published version: its spec, parameters and
channel, as JSON (default) or YAML.

Manifests and add-on values inside the record are base64-encoded. To read
them decoded, list them, or grep across them, use:

  ankra stack-profiles contents <profile> <version>`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		version, versionError := parseRequiredProfileVersionArgument(args[1])
		if versionError != nil {
			return versionError
		}
		payload, getError := apiClient.GetStackProfileVersion(cmd.Context(), profileID, version)
		if getError != nil {
			return fmt.Errorf("getting stack profile version: %w", getError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesDiffCmd = &cobra.Command{
	Use:   "diff [profile-id|profile-name]",
	Short: "Compare two published versions of a profile",
	Example: `  ankra stack-profiles diff postgres-ha --from v1 --to v2
  ankra stack-profiles diff postgres-ha --from 1 --to 3 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		fromRaw, _ := cmd.Flags().GetString("from")
		toRaw, _ := cmd.Flags().GetString("to")
		if fromRaw == "" || toRaw == "" {
			return withExitCode(exitUsage, errors.New("--from and --to are required, as 1 or v1"))
		}
		fromVersion, fromError := parseRequiredProfileVersionArgument(fromRaw)
		if fromError != nil {
			return fromError
		}
		toVersion, toError := parseRequiredProfileVersionArgument(toRaw)
		if toError != nil {
			return toError
		}
		payload, diffError := apiClient.DiffStackProfileVersions(cmd.Context(), profileID, fromVersion, toVersion)
		if diffError != nil {
			return fmt.Errorf("diffing stack profile versions: %w", diffError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesDeploymentsCmd = &cobra.Command{
	Use:   "deployments [profile-id|profile-name]",
	Short: "List the stacks deployed from a profile across the fleet",
	Long: `List every stack your organisation deployed from this profile: the target
cluster, the stack, the profile version it runs, and whether a newer
version is available.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, listError := apiClient.ListStackProfileInstantiations(cmd.Context(), profileID)
		if listError != nil {
			return fmt.Errorf("listing stack profile deployments: %w", listError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

func init() {
	stackProfilesCreateCmd.Flags().String("name", "", "Profile name (required)")
	stackProfilesCreateCmd.Flags().String("stack", "", "Source stack name to snapshot (required)")
	stackProfilesCreateCmd.Flags().String("cluster", "", "Source cluster name or ID (defaults to the selected cluster)")
	stackProfilesCreateCmd.Flags().String("description", "", "Profile description shown in the catalogue")
	stackProfilesCreateCmd.Flags().String("category", "general", "Profile category (e.g. 'database')")
	stackProfilesCreateCmd.Flags().StringArray("tag", nil, "Catalogue tag (repeatable)")
	stackProfilesCreateCmd.Flags().String("logo-url", "", "Logo image URL shown in the catalogue")
	stackProfilesCreateCmd.Flags().String("visibility", "", "Profile visibility: organisation (default) or public")
	stackProfilesCreateCmd.Flags().String("changelog", "", "Changelog note for version 1")
	stackProfilesCreateCmd.Flags().Bool("include-addon-configurations", true, "Carry the source add-ons' values into the profile")
	registerStructuredOutputFlags(stackProfilesCreateCmd)

	stackProfilesUpdateCmd.Flags().String("name", "", "New profile name")
	stackProfilesUpdateCmd.Flags().String("description", "", "New description (empty clears)")
	stackProfilesUpdateCmd.Flags().String("category", "", "New category")
	stackProfilesUpdateCmd.Flags().StringArray("tag", nil, "New tag set (repeatable; replaces the stored tags)")
	stackProfilesUpdateCmd.Flags().String("logo-url", "", "New logo image URL (empty clears)")
	stackProfilesUpdateCmd.Flags().String("visibility", "", "Profile visibility: organisation or public")
	registerStructuredOutputFlags(stackProfilesUpdateCmd)

	stackProfilesDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(stackProfilesDeleteCmd)

	stackProfilesSaveVersionCmd.Flags().String("stack", "", "Source stack name to snapshot (required)")
	stackProfilesSaveVersionCmd.Flags().String("cluster", "", "Source cluster name or ID (defaults to the selected cluster)")
	stackProfilesSaveVersionCmd.Flags().String("channel", "stable", "Release channel for the new version")
	stackProfilesSaveVersionCmd.Flags().String("changelog", "", "Changelog note for the new version")
	stackProfilesSaveVersionCmd.Flags().Bool("include-addon-configurations", true, "Carry the source add-ons' values into the version")
	registerStructuredOutputFlags(stackProfilesSaveVersionCmd)

	registerStructuredOutputFlags(stackProfilesSetCurrentVersionCmd)
	registerStructuredOutputFlags(stackProfilesVersionCmd)

	stackProfilesDiffCmd.Flags().String("from", "", "Version to compare from, as 1 or v1 (required)")
	stackProfilesDiffCmd.Flags().String("to", "", "Version to compare to, as 1 or v1 (required)")
	registerStructuredOutputFlags(stackProfilesDiffCmd)

	registerStructuredOutputFlags(stackProfilesDeploymentsCmd)

	stackProfilesCmd.AddCommand(
		stackProfilesCreateCmd,
		stackProfilesUpdateCmd,
		stackProfilesDeleteCmd,
		stackProfilesSaveVersionCmd,
		stackProfilesSetCurrentVersionCmd,
		stackProfilesVersionCmd,
		stackProfilesDiffCmd,
		stackProfilesDeploymentsCmd,
	)
}
