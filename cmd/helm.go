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
	Run: func(cmd *cobra.Command, args []string) {
		response, err := apiClient.ListHelmRegistries()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing registries: %v\n", err)
			os.Exit(1)
		}

		if len(response.Result) == 0 {
			fmt.Println("No Helm registries found.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Type", "URL", "Status", "Created"})

		for _, reg := range response.Result {
			t.AppendRow(table.Row{reg.Name, reg.Type, reg.URL, reg.Status, formatTimeAgo(reg.CreatedAt)})
		}
		t.Render()
	},
}

var helmRegistriesGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get details of a Helm chart registry",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		registryName := args[0]

		response, err := apiClient.GetHelmRegistry(registryName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Registry: %s\n", response.Name)
		fmt.Printf("  Type:      %s\n", response.Type)
		fmt.Printf("  URL:       %s\n", response.URL)
		fmt.Printf("  Status:    %s\n", response.Status)
		fmt.Printf("  Created:   %s\n", formatTimeAgo(response.CreatedAt))
	},
}

var helmRegistriesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Helm chart registry from a spec file",
	Long: `Create a Helm chart registry by providing a JSON spec file.

Example:
  ankra helm registries create -f registry-spec.json`,
	Run: func(cmd *cobra.Command, args []string) {
		filePath, _ := cmd.Flags().GetString("file")

		fileData, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}

		var specJSON json.RawMessage
		if err := json.Unmarshal(fileData, &specJSON); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
			os.Exit(1)
		}

		result, err := apiClient.CreateHelmRegistry(client.CreateHelmRegistryRequest{Spec: specJSON})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating registry: %v\n", err)
			os.Exit(1)
		}

		if len(result.Errors) > 0 {
			fmt.Fprintln(os.Stderr, "Registry creation had errors:")
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "  - %s: %s\n", e.Key, e.Message)
			}
			os.Exit(1)
		}

		fmt.Println("Helm registry created successfully!")
	},
}

var helmRegistriesDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a Helm chart registry",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		registryName := args[0]
		force, _ := cmd.Flags().GetBool("force")

		if !force {
			prompt := promptui.Prompt{
				Label:     fmt.Sprintf("Delete registry '%s'", registryName),
				IsConfirm: true,
			}
			if _, err := prompt.Run(); err != nil {
				fmt.Println("Cancelled.")
				return
			}
		}

		_, err := apiClient.DeleteHelmRegistry(registryName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting registry: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Registry '%s' deleted.\n", registryName)
	},
}

var helmCredentialsCmd = &cobra.Command{
	Use:   "credentials",
	Short: "Manage Helm registry credentials",
}

var helmCredentialsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Helm registry credentials",
	Run: func(cmd *cobra.Command, args []string) {
		response, err := apiClient.ListHelmRegistryCredentials()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing credentials: %v\n", err)
			os.Exit(1)
		}

		if len(response.Result) == 0 {
			fmt.Println("No Helm registry credentials found.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Created"})

		for _, cred := range response.Result {
			t.AppendRow(table.Row{cred.Name, formatTimeAgo(cred.CreatedAt)})
		}
		t.Render()
	},
}

var helmCredentialsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Helm registry credential",
	Long: `Create a Helm registry credential with username and password.
The password will be prompted interactively.

Example:
  ankra helm credentials create --name my-cred --username user`,
	Run: func(cmd *cobra.Command, args []string) {
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
			fmt.Fprintln(os.Stderr, "Prompt cancelled.")
			os.Exit(1)
		}

		result, err := apiClient.CreateHelmRegistryCredential(client.CreateHelmCredentialRequest{
			Name:     name,
			Username: username,
			Password: password,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating credential: %v\n", err)
			os.Exit(1)
		}

		if len(result.Errors) > 0 {
			fmt.Fprintln(os.Stderr, "Credential creation had errors:")
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "  - %s: %s\n", e.Key, e.Message)
			}
			os.Exit(1)
		}

		fmt.Printf("Helm registry credential '%s' created successfully!\n", name)
	},
}

var helmCredentialsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a Helm registry credential",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		credentialName := args[0]
		force, _ := cmd.Flags().GetBool("force")

		if !force {
			prompt := promptui.Prompt{
				Label:     fmt.Sprintf("Delete credential '%s'", credentialName),
				IsConfirm: true,
			}
			if _, err := prompt.Run(); err != nil {
				fmt.Println("Cancelled.")
				return
			}
		}

		_, err := apiClient.DeleteHelmRegistryCredential(credentialName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting credential: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Credential '%s' deleted.\n", credentialName)
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
