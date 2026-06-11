package cmd

import (
	"encoding/base64"
	"fmt"
	"os"

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

func init() {
	stackProfilesListCmd.Flags().Int("page", 1, "Page number")
	stackProfilesListCmd.Flags().Int("page-size", 25, "Page size")
	stackProfilesListCmd.Flags().String("search", "", "Filter profiles by name")
	registerStructuredOutputFlags(stackProfilesListCmd)

	stackProfilesExportIacCmd.Flags().Int("version", 1, "Profile version to export")
	stackProfilesExportIacCmd.Flags().StringP("output", "o", "", "Write YAML to this file instead of stdout")

	stackProfilesImportCmd.Flags().String("name", "", "Profile name (defaults to metadata.name in the file)")
	stackProfilesImportCmd.Flags().String("category", "general", "Profile category")

	stackProfilesCmd.AddCommand(stackProfilesListCmd)
	stackProfilesCmd.AddCommand(stackProfilesExportIacCmd)
	stackProfilesCmd.AddCommand(stackProfilesImportCmd)

	rootCmd.AddCommand(stackProfilesCmd)
}
