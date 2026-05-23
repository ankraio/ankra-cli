package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var helmCmd = &cobra.Command{
	Use:   "helm",
	Short: "Manage Helm registries and credentials",
	Long:  "Commands to manage Helm chart registries and registry credentials.",
}

var helmRegistriesCmd = &cobra.Command{
	Use:   "registries",
	Short: "Manage Helm chart registries",
}

var helmRegistriesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Helm chart registries",
	RunE: func(cmd *cobra.Command, args []string) error {
		response, err := apiClient.ListHelmRegistries()
		if err != nil {
			return fmt.Errorf("listing registries: %w", err)
		}

		if len(response.Result) == 0 {
			fmt.Println("No Helm registries found.")
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Kind", "URL", "Charts", "Indexing", "Global", "Last Indexed"})

		for _, reg := range response.Result {
			lastIndexed := "-"
			if reg.LastIndexedAt != nil && *reg.LastIndexedAt != "" {
				lastIndexed = formatTimeAgo(*reg.LastIndexedAt)
			}
			t.AppendRow(table.Row{
				reg.Name,
				reg.Kind(),
				reg.URL,
				reg.ChartCount,
				reg.Indexing,
				reg.IsGlobal,
				lastIndexed,
			})
		}
		t.Render()
		return nil
	},
}

var helmRegistriesGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get details of a Helm chart registry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		registryName := args[0]

		response, err := apiClient.GetHelmRegistry(registryName)
		if err != nil {
			return fmt.Errorf("getting registry: %w", err)
		}

		fmt.Printf("Registry: %s\n", response.Registry.Name)
		fmt.Printf("  URL:           %s\n", response.Registry.URL)
		if response.Registry.CredentialName != nil && *response.Registry.CredentialName != "" {
			fmt.Printf("  Credential:    %s\n", *response.Registry.CredentialName)
		}
		fmt.Printf("  Indexing:      %t\n", response.Indexing)
		if response.LastIndexedAt != nil && *response.LastIndexedAt != "" {
			fmt.Printf("  Last Indexed:  %s\n", formatTimeAgo(*response.LastIndexedAt))
		}
		if response.NextSyncAt != nil && *response.NextSyncAt != "" {
			fmt.Printf("  Next Sync:     %s\n", formatTimeAgo(*response.NextSyncAt))
		}
		fmt.Printf("  Charts:        %d (showing %d on this page)\n",
			response.Pagination.TotalCount, len(response.Charts))
		if response.ResourceState != nil && *response.ResourceState != "" {
			fmt.Printf("  State:         %s\n", *response.ResourceState)
		}
		return nil
	},
}

var helmRegistriesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Helm chart registry from a spec file",
	Long: `Create a Helm chart registry by providing a JSON spec file.

Example:
  ankra helm registries create -f registry-spec.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, _ := cmd.Flags().GetString("file")

		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}

		var specJSON json.RawMessage
		if err := json.Unmarshal(fileData, &specJSON); err != nil {
			return fmt.Errorf("parsing JSON: %w", err)
		}

		result, err := apiClient.CreateHelmRegistry(client.CreateHelmRegistryRequest{Spec: specJSON})
		if err != nil {
			return fmt.Errorf("creating registry: %w", err)
		}

		if len(result.Errors) > 0 {
			fmt.Fprintln(os.Stderr, "Registry creation had errors:")
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "  - %s: %s\n", e.Key, e.Message)
			}
			return fmt.Errorf("registry creation reported %d error(s)", len(result.Errors))
		}

		fmt.Println("Helm registry created successfully!")
		return nil
	},
}

var helmRegistriesDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a Helm chart registry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		registryName := args[0]
		force, _ := cmd.Flags().GetBool("force")

		if !force {
			prompt := promptui.Prompt{
				Label:     fmt.Sprintf("Delete registry '%s'", registryName),
				IsConfirm: true,
			}
			if _, err := prompt.Run(); err != nil {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		if _, err := apiClient.DeleteHelmRegistry(registryName); err != nil {
			return fmt.Errorf("deleting registry: %w", err)
		}

		fmt.Printf("Registry '%s' deleted.\n", registryName)
		return nil
	},
}

var helmCredentialsCmd = &cobra.Command{
	Use:   "credentials",
	Short: "Manage Helm registry credentials",
}

var helmCredentialsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Helm registry credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		response, err := apiClient.ListHelmRegistryCredentials()
		if err != nil {
			return fmt.Errorf("listing credentials: %w", err)
		}

		if len(response.Credentials) == 0 {
			fmt.Println("No Helm registry credentials found.")
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Created"})

		for _, cred := range response.Credentials {
			t.AppendRow(table.Row{cred.ID, cred.Name, formatTimeAgo(cred.CreatedAt)})
		}
		t.Render()
		fmt.Printf("\nTotal: %d\n", response.TotalCount)
		return nil
	},
}

var helmCredentialsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Helm registry credential",
	Long: `Create a Helm registry credential with username and password.
The password will be prompted interactively.

Example:
  ankra helm credentials create --name my-cred --username user`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		username, _ := cmd.Flags().GetString("username")

		passwordPrompt := promptui.Prompt{
			Label: "Password",
			Mask:  '*',
			Validate: func(input string) error {
				if len(input) == 0 {
					return fmt.Errorf("password cannot be empty")
				}
				return nil
			},
		}
		password, err := passwordPrompt.Run()
		if err != nil {
			return fmt.Errorf("prompt cancelled: %w", err)
		}

		result, err := apiClient.CreateHelmRegistryCredential(client.CreateHelmCredentialRequest{
			Name:     name,
			Username: username,
			Password: password,
		})
		if err != nil {
			return fmt.Errorf("creating credential: %w", err)
		}

		if len(result.Errors) > 0 {
			fmt.Fprintln(os.Stderr, "Credential creation had errors:")
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "  - %s: %s\n", e.Key, e.Message)
			}
			return fmt.Errorf("credential creation reported %d error(s)", len(result.Errors))
		}

		fmt.Printf("Helm registry credential '%s' created successfully!\n", name)
		return nil
	},
}

var helmCredentialsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a Helm registry credential",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialName := args[0]
		force, _ := cmd.Flags().GetBool("force")

		if !force {
			prompt := promptui.Prompt{
				Label:     fmt.Sprintf("Delete credential '%s'", credentialName),
				IsConfirm: true,
			}
			if _, err := prompt.Run(); err != nil {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		if _, err := apiClient.DeleteHelmRegistryCredential(credentialName); err != nil {
			return fmt.Errorf("deleting credential: %w", err)
		}

		fmt.Printf("Credential '%s' deleted.\n", credentialName)
		return nil
	},
}

func init() {
	helmRegistriesCreateCmd.Flags().StringP("file", "f", "", "Path to registry spec JSON file (required)")
	_ = helmRegistriesCreateCmd.MarkFlagRequired("file")
	helmRegistriesDeleteCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")

	helmCredentialsCreateCmd.Flags().String("name", "", "Credential name (required)")
	helmCredentialsCreateCmd.Flags().String("username", "", "Registry username (required)")
	_ = helmCredentialsCreateCmd.MarkFlagRequired("name")
	_ = helmCredentialsCreateCmd.MarkFlagRequired("username")
	helmCredentialsDeleteCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")

	helmRegistriesCmd.AddCommand(helmRegistriesListCmd)
	helmRegistriesCmd.AddCommand(helmRegistriesGetCmd)
	helmRegistriesCmd.AddCommand(helmRegistriesCreateCmd)
	helmRegistriesCmd.AddCommand(helmRegistriesDeleteCmd)

	helmCredentialsCmd.AddCommand(helmCredentialsListCmd)
	helmCredentialsCmd.AddCommand(helmCredentialsCreateCmd)
	helmCredentialsCmd.AddCommand(helmCredentialsDeleteCmd)

	helmCmd.AddCommand(helmRegistriesCmd)
	helmCmd.AddCommand(helmCredentialsCmd)

	rootCmd.AddCommand(helmCmd)
}
