package cmd

import (
	"errors"
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var scalewayCredCmd = &cobra.Command{
	Use:     "scaleway",
	Aliases: []string{"scw"},
	Short:   "Manage Scaleway credentials",
	Long:    "Commands to list and create Scaleway API credentials and SSH key credentials.",
}

func renderScalewayCredentials(cmd *cobra.Command, credentials []client.ScalewayCredentialListItem, emptyMessage string) error {
	if credentials == nil {
		credentials = []client.ScalewayCredentialListItem{}
	}
	if handled, renderError := renderStructured(cmd, credentials); renderError != nil {
		return renderError
	} else if handled {
		return nil
	}

	if len(credentials) == 0 {
		fmt.Println(emptyMessage)
		return nil
	}

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"ID", "Name", "Available", "Created"})
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, WidthMin: 36},
		{Number: 2, WidthMin: 20},
		{Number: 3, WidthMin: 10},
		{Number: 4, WidthMin: 15},
	})

	for _, credential := range credentials {
		available := "yes"
		if !credential.Available {
			available = "no"
		}
		t.AppendRow(table.Row{
			credential.ID,
			credential.Name,
			available,
			formatTimeAgo(credential.CreatedAt),
		})
	}
	t.Render()
	return nil
}

var scalewayCredListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Scaleway API credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, listError := apiClient.ListScalewayCredentials()
		if listError != nil {
			return fmt.Errorf("listing Scaleway credentials: %w", listError)
		}
		return renderScalewayCredentials(cmd, credentials, "No Scaleway credentials found.")
	},
}

var scalewayCredCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Scaleway API credential",
	Long: `Create a Scaleway API credential from a project-scoped IAM application key.
The secret key is collected via a masked prompt, never on the command line -
Scaleway shows it once, so store it in a secret manager.

The project id is required: it is not derivable from the key, and every
resource Ankra creates is scoped to that one project.

Examples:
  ankra credentials scaleway create --name scw-production --project-id 11111111-2222-3333-4444-555555555555 --access-key SCWXXXXXXXXXXXXXXXXX`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		projectID, _ := cmd.Flags().GetString("project-id")
		accessKey, _ := cmd.Flags().GetString("access-key")

		prompt := promptui.Prompt{
			Label: "Scaleway Secret Key",
			Mask:  '*',
			Validate: func(input string) error {
				if len(input) == 0 {
					return fmt.Errorf("secret key cannot be empty")
				}
				return nil
			},
		}
		secretKeyValue, promptError := prompt.Run()
		if promptError != nil {
			return errors.New("prompt cancelled")
		}

		result, createError := apiClient.CreateScalewayCredential(client.CreateScalewayCredentialRequest{
			Name:      name,
			AccessKey: accessKey,
			SecretKey: secretKeyValue,
			ProjectID: projectID,
		})
		if createError != nil {
			return fmt.Errorf("creating Scaleway credential: %w", createError)
		}

		if !result.Success {
			message := "failed to create Scaleway credential:"
			for _, resourceError := range result.Errors {
				message += fmt.Sprintf("\n  - %s: %s", resourceError.Key, resourceError.Message)
			}
			return errors.New(message)
		}

		fmt.Printf("Scaleway credential '%s' created successfully!\n", name)
		return nil
	},
}

var scalewaySSHKeyCmd = &cobra.Command{
	Use:     "ssh-key",
	Aliases: []string{"ssh-keys", "ssh"},
	Short:   "Manage SSH key credentials",
}

var scalewaySSHKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH key credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, listError := apiClient.ListScalewaySSHKeyCredentials()
		if listError != nil {
			return fmt.Errorf("listing SSH key credentials: %w", listError)
		}
		return renderScalewayCredentials(cmd, credentials, "No SSH key credentials found.")
	},
}

var scalewaySSHKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an SSH key credential",
	Long: `Create an SSH key credential. Either provide a public key or generate a new keypair.

Examples:
  ankra credentials scaleway ssh-key create --name my-key --generate
  ankra credentials scaleway ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		publicKey, _ := cmd.Flags().GetString("public-key")
		generate, _ := cmd.Flags().GetBool("generate")

		if !generate && publicKey == "" {
			return errors.New("either --public-key or --generate must be provided")
		}

		createRequest := client.CreateSSHKeyCredentialRequest{
			Name:     name,
			Generate: generate,
		}
		if publicKey != "" {
			createRequest.SSHPublicKey = &publicKey
		}

		result, createError := apiClient.CreateScalewaySSHKeyCredential(createRequest)
		if createError != nil {
			return fmt.Errorf("creating SSH key credential: %w", createError)
		}

		if !result.Success {
			message := "failed to create SSH key credential:"
			for _, resourceError := range result.Errors {
				message += fmt.Sprintf("\n  - %s: %s", resourceError.Key, resourceError.Message)
			}
			return errors.New(message)
		}

		fmt.Printf("SSH key credential '%s' created successfully!\n", name)

		if result.PrivateKey != nil {
			fmt.Println("\nGenerated private key (save this, it will not be shown again):")
			fmt.Println(*result.PrivateKey)
		}
		return nil
	},
}

func init() {
	scalewayCredCreateCmd.Flags().String("name", "", "Credential name (required)")
	scalewayCredCreateCmd.Flags().String("project-id", "", "Scaleway Project ID (required)")
	scalewayCredCreateCmd.Flags().String("access-key", "", "Scaleway API access key (required)")
	_ = scalewayCredCreateCmd.MarkFlagRequired("name")
	_ = scalewayCredCreateCmd.MarkFlagRequired("project-id")
	_ = scalewayCredCreateCmd.MarkFlagRequired("access-key")

	scalewaySSHKeyCreateCmd.Flags().String("name", "", "Credential name (required)")
	scalewaySSHKeyCreateCmd.Flags().String("public-key", "", "SSH public key")
	scalewaySSHKeyCreateCmd.Flags().Bool("generate", false, "Generate a new SSH keypair")
	_ = scalewaySSHKeyCreateCmd.MarkFlagRequired("name")

	registerStructuredOutputFlags(scalewayCredListCmd, scalewaySSHKeyListCmd)

	scalewaySSHKeyCmd.AddCommand(scalewaySSHKeyListCmd)
	scalewaySSHKeyCmd.AddCommand(scalewaySSHKeyCreateCmd)

	scalewayCredCmd.AddCommand(scalewayCredListCmd)
	scalewayCredCmd.AddCommand(scalewayCredCreateCmd)
	scalewayCredCmd.AddCommand(scalewaySSHKeyCmd)

	credentialsCmd.AddCommand(scalewayCredCmd)
}
