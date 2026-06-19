package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var stackProfilesCmd = &cobra.Command{
	Use:   "stack-profiles",
	Short: "Manage reusable stack profiles",
	Long:  "List, export, and import organisation-level stack profiles.",
}

var stackProfilesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stack profiles in the active organisation",
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		search, _ := cmd.Flags().GetString("search")

		response, err := apiClient.ListStackProfiles(page, pageSize, search)
		if err != nil {
			return fmt.Errorf("listing stack profiles: %w", err)
		}
		if rendered, err := renderStructured(cmd, response); rendered || err != nil {
			return err
		}
		if len(response.Result) == 0 {
			fmt.Println("No stack profiles found.")
			return nil
		}

		writer := table.NewWriter()
		writer.SetOutputMirror(os.Stdout)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Name", "Category", "Latest", "ID"})
		for _, profile := range response.Result {
			writer.AppendRow(table.Row{
				profile.Name,
				profile.Category,
				fmt.Sprintf("v%d", profile.LatestVersion),
				profile.ID,
			})
		}
		writer.Render()
		return nil
	},
}

var stackProfilesExportIacCmd = &cobra.Command{
	Use:   "export-iac [profile-id]",
	Short: "Export a profile version as ClusterInfrastructureAsCode YAML",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profileID := args[0]
		version, _ := cmd.Flags().GetInt("version")
		outputPath, _ := cmd.Flags().GetString("output")

		export, err := apiClient.ExportStackProfileIac(profileID, version)
		if err != nil {
			return fmt.Errorf("exporting profile: %w", err)
		}

		decoded, err := base64.StdEncoding.DecodeString(export.ContentBase64)
		if err != nil {
			return fmt.Errorf("decoding export: %w", err)
		}

		if outputPath == "" {
			fmt.Print(string(decoded))
			return nil
		}
		if writeError := os.WriteFile(outputPath, decoded, 0o600); writeError != nil {
			return fmt.Errorf("writing %s: %w", outputPath, writeError)
		}
		fmt.Printf("Exported profile v%d to %s\n", export.Version, outputPath)
		return nil
	},
}

var stackProfilesImportCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import a profile from a ClusterInfrastructureAsCode YAML file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath := args[0]
		name, _ := cmd.Flags().GetString("name")
		category, _ := cmd.Flags().GetString("category")

		content, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}

		var profileName *string
		if name != "" {
			profileName = &name
		}

		importRequest := client.ImportStackProfileRequest{
			Name:          profileName,
			Category:      category,
			ContentBase64: base64.StdEncoding.EncodeToString(content),
		}

		result, err := apiClient.ImportStackProfile(importRequest)
		if err != nil {
			return fmt.Errorf("importing profile: %w", err)
		}
		fmt.Printf("Imported profile %q (id=%s, latest v%d)\n",
			result.Profile.Name, result.Profile.ID, result.Profile.LatestVersion)
		return nil
	},
}

var stackProfilesGetCmd = &cobra.Command{
	Use:   "get [profile-id]",
	Short: "Show a stack profile's versions and parameters",
	Long:  "Describe a stack profile: its metadata, published versions, and the parameters you can bind when applying it.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profileID := args[0]
		versionFlag, _ := cmd.Flags().GetInt("version")

		detail, err := apiClient.GetStackProfile(profileID)
		if err != nil {
			return fmt.Errorf("getting stack profile: %w", err)
		}

		if rendered, err := renderStructured(cmd, detail); rendered || err != nil {
			return err
		}

		parameters, shownVersion, err := selectProfileParameters(detail, versionFlag, profileID)
		if err != nil {
			return err
		}

		fmt.Println("Stack Profile:")
		fmt.Printf("  Name:            %s\n", detail.Profile.Name)
		fmt.Printf("  ID:              %s\n", detail.Profile.ID)
		fmt.Printf("  Category:        %s\n", detail.Profile.Category)
		if detail.Profile.Description != nil && *detail.Profile.Description != "" {
			fmt.Printf("  Description:     %s\n", *detail.Profile.Description)
		}
		fmt.Printf("  Visibility:      %s\n", detail.Profile.Visibility)
		fmt.Printf("  Latest version:  v%d\n", detail.Profile.LatestVersion)
		fmt.Printf("  Current version: v%d\n", detail.Profile.CurrentVersion)

		if len(detail.Versions) > 0 {
			fmt.Println("\nVersions:")
			versionsTable := table.NewWriter()
			versionsTable.SetOutputMirror(os.Stdout)
			versionsTable.SetStyle(table.StyleRounded)
			versionsTable.AppendHeader(table.Row{"Version", "Channel", "Created", "Changelog"})
			for _, profileVersion := range detail.Versions {
				changelog := "-"
				if profileVersion.Changelog != nil && *profileVersion.Changelog != "" {
					changelog = *profileVersion.Changelog
				}
				versionsTable.AppendRow(table.Row{
					fmt.Sprintf("v%d", profileVersion.Version),
					profileVersion.Channel,
					profileVersion.CreatedAt,
					changelog,
				})
			}
			versionsTable.Render()
		}

		fmt.Printf("\nParameters (v%d):\n", shownVersion)
		if len(parameters) == 0 {
			fmt.Println("  (none)")
			return nil
		}
		parametersTable := table.NewWriter()
		parametersTable.SetOutputMirror(os.Stdout)
		parametersTable.SetStyle(table.StyleRounded)
		parametersTable.AppendHeader(table.Row{"Name", "Type", "Required", "Default", "Description"})
		for _, parameter := range parameters {
			required := "no"
			if parameter.Required {
				required = "yes"
			}
			defaultValue := "-"
			if parameter.Type != "secret" && parameter.Default != nil && *parameter.Default != "" {
				defaultValue = *parameter.Default
			}
			description := ""
			if parameter.Description != nil {
				description = *parameter.Description
			}
			parametersTable.AppendRow(table.Row{parameter.Name, parameter.Type, required, defaultValue, description})
		}
		parametersTable.Render()
		return nil
	},
}

var stackProfilesApplyCmd = &cobra.Command{
	Use:   "apply [profile-id]",
	Short: "Apply a stack profile to a cluster as a draft (optionally deploy)",
	Long: `Apply (instantiate) a stack profile onto a cluster.

By default this creates a reviewable stack DRAFT on the target cluster — nothing is
deployed until you review it in the Ankra dashboard or pass --deploy.

Bind profile parameters with --set name=value. For secret parameters prefer
--set-file name=path or --set-env name=ENV_VAR so the value never appears in your
shell history or process list. Use 'ankra stack-profiles get <profile-id>' to see
which parameters a profile expects.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profileID := args[0]
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		stackName, _ := cmd.Flags().GetString("stack-name")
		versionFlag, _ := cmd.Flags().GetInt("version")
		deploy, _ := cmd.Flags().GetBool("deploy")
		setValues, _ := cmd.Flags().GetStringArray("set")
		setFiles, _ := cmd.Flags().GetStringArray("set-file")
		setEnvs, _ := cmd.Flags().GetStringArray("set-env")

		clusterID, clusterLabel, err := resolveApplyTargetCluster(clusterFlag)
		if err != nil {
			return err
		}

		parameters, err := buildParameterBindings(setValues, setFiles, setEnvs)
		if err != nil {
			return err
		}

		request := client.InstantiateStackProfileRequest{
			ProfileID:    profileID,
			NewStackName: stackName,
			Parameters:   parameters,
			Deploy:       deploy,
		}
		if versionFlag > 0 {
			version := versionFlag
			request.Version = &version
		}

		format, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		if format == outputDefault {
			fmt.Printf("Applying stack profile to cluster '%s'...\n", clusterLabel)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()

		result, err := apiClient.InstantiateStackProfile(ctx, clusterID, request)
		if err != nil {
			return fmt.Errorf("applying stack profile: %w", err)
		}

		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, result)
		}

		fmt.Printf("\nStack profile applied successfully!\n")
		fmt.Printf("  Draft ID:    %s\n", result.DraftID)
		fmt.Printf("  Stack Name:  %s\n", result.StackName)
		fmt.Printf("  Version:     v%d\n", result.ProfileVersion)
		fmt.Printf("  Addons:      %d\n", result.AddonsCount)
		fmt.Printf("  Manifests:   %d\n", result.ManifestsCount)

		if len(result.Warnings) > 0 {
			fmt.Println("\nWarnings:")
			for _, warning := range result.Warnings {
				fmt.Printf("  - %s\n", warning)
			}
		}

		if result.Deployed {
			fmt.Printf("\nThe stack has been deployed. %d job(s) scheduled.\n", result.JobCount)
			if result.OperationID != nil {
				fmt.Printf("  Operation ID: %s\n", *result.OperationID)
			}
		} else {
			fmt.Printf("\nThe stack was created as a draft. Review and deploy it in the Ankra dashboard, or re-run with --deploy.\n")
		}
		return nil
	},
}

func resolveApplyTargetCluster(clusterFlag string) (string, string, error) {
	if clusterFlag != "" {
		clusterID, err := resolveClusterID(clusterFlag)
		if err != nil {
			return "", "", fmt.Errorf("resolving cluster: %w", err)
		}
		return clusterID, clusterFlag, nil
	}
	selected, err := loadSelectedCluster()
	if err != nil {
		return "", "", errNoClusterSelected{}
	}
	return selected.ID, selected.Name, nil
}

func buildParameterBindings(setValues, setFiles, setEnvs []string) ([]client.ParameterBinding, error) {
	bindings := []client.ParameterBinding{}

	for _, entry := range setValues {
		name, value, err := splitParameterAssignment(entry, "--set")
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, client.ParameterBinding{Name: name, Value: value})
	}

	for _, entry := range setFiles {
		name, path, err := splitParameterAssignment(entry, "--set-file")
		if err != nil {
			return nil, err
		}
		contents, readError := os.ReadFile(path)
		if readError != nil {
			return nil, fmt.Errorf("--set-file %q: %w", name, readError)
		}
		bindings = append(bindings, client.ParameterBinding{Name: name, Value: string(contents)})
	}

	for _, entry := range setEnvs {
		name, rawEnvName, err := splitParameterAssignment(entry, "--set-env")
		if err != nil {
			return nil, err
		}
		environmentVariable := strings.TrimSpace(rawEnvName)
		value, present := os.LookupEnv(environmentVariable)
		if !present {
			return nil, fmt.Errorf("--set-env %q: environment variable %q is not set", name, environmentVariable)
		}
		bindings = append(bindings, client.ParameterBinding{Name: name, Value: value})
	}

	return bindings, nil
}

func splitParameterAssignment(entry, flagName string) (string, string, error) {
	separatorIndex := strings.Index(entry, "=")
	if separatorIndex < 0 {
		return "", "", fmt.Errorf("invalid %s entry %q: expected name=value", flagName, entry)
	}
	name := strings.TrimSpace(entry[:separatorIndex])
	value := entry[separatorIndex+1:]
	if name == "" {
		return "", "", fmt.Errorf("invalid %s entry %q: empty parameter name", flagName, entry)
	}
	return name, value, nil
}

func selectProfileParameters(detail *client.StackProfileDetail, versionFlag int, profileID string) ([]client.StackProfileParameter, int, error) {
	if versionFlag <= 0 {
		if detail.CurrentVersionDetail != nil {
			return detail.CurrentVersionDetail.Parameters, detail.CurrentVersionDetail.Version, nil
		}
		if detail.LatestVersionDetail != nil {
			return detail.LatestVersionDetail.Parameters, detail.LatestVersionDetail.Version, nil
		}
		return nil, detail.Profile.CurrentVersion, nil
	}
	if detail.CurrentVersionDetail != nil && detail.CurrentVersionDetail.Version == versionFlag {
		return detail.CurrentVersionDetail.Parameters, versionFlag, nil
	}
	if detail.LatestVersionDetail != nil && detail.LatestVersionDetail.Version == versionFlag {
		return detail.LatestVersionDetail.Parameters, versionFlag, nil
	}
	export, err := apiClient.ExportStackProfileIac(profileID, versionFlag)
	if err != nil {
		return nil, 0, fmt.Errorf("loading version %d parameters: %w", versionFlag, err)
	}
	return export.Parameters, versionFlag, nil
}

func init() {
	stackProfilesListCmd.Flags().Int("page", 1, "Page number")
	stackProfilesListCmd.Flags().Int("page-size", 25, "Page size")
	stackProfilesListCmd.Flags().String("search", "", "Filter profiles by name")
	registerStructuredOutputFlags(stackProfilesListCmd)

	stackProfilesExportIacCmd.Flags().Int("version", 1, "Profile version to export")
	stackProfilesExportIacCmd.Flags().StringP("output", "o", "", "Write YAML to this file instead of stdout")

	stackProfilesImportCmd.Flags().String("name", "", "Profile name (defaults to metadata.name in the file)")
	stackProfilesImportCmd.Flags().String("category", "general", "Profile category")

	stackProfilesGetCmd.Flags().Int("version", 0, "Profile version to describe (defaults to the current version)")
	registerStructuredOutputFlags(stackProfilesGetCmd)

	stackProfilesApplyCmd.Flags().String("cluster", "", "Target cluster name or ID (defaults to the selected cluster)")
	stackProfilesApplyCmd.Flags().Int("version", 0, "Profile version to apply (defaults to the profile's current version)")
	stackProfilesApplyCmd.Flags().String("stack-name", "", "Name for the new stack (defaults to the profile's stack name)")
	stackProfilesApplyCmd.Flags().StringArray("set", nil, "Bind a parameter: name=value (repeatable; not for secrets)")
	stackProfilesApplyCmd.Flags().StringArray("set-file", nil, "Bind a parameter from a file: name=path (repeatable; secret-safe)")
	stackProfilesApplyCmd.Flags().StringArray("set-env", nil, "Bind a parameter from an environment variable: name=ENV_VAR (repeatable; secret-safe)")
	stackProfilesApplyCmd.Flags().Bool("deploy", false, "Deploy the stack immediately instead of leaving a draft for review")
	registerStructuredOutputFlags(stackProfilesApplyCmd)

	stackProfilesCmd.AddCommand(stackProfilesListCmd)
	stackProfilesCmd.AddCommand(stackProfilesExportIacCmd)
	stackProfilesCmd.AddCommand(stackProfilesImportCmd)
	stackProfilesCmd.AddCommand(stackProfilesGetCmd)
	stackProfilesCmd.AddCommand(stackProfilesApplyCmd)

	rootCmd.AddCommand(stackProfilesCmd)
}
