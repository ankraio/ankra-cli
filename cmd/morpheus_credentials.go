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

var morpheusCredCmd = &cobra.Command{
	Use:   "morpheus",
	Short: "Manage HPE Morpheus credentials",
	Long:  "Commands to list and create HPE Morpheus API credentials and SSH key credentials.",
}

var morpheusCredListCmd = &cobra.Command{
	Use:   "list",
	Short: "List HPE Morpheus API credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, listError := apiClient.ListMorpheusCredentials()
		if listError != nil {
			return fmt.Errorf("listing HPE Morpheus credentials: %w", listError)
		}

		if credentials == nil {
			credentials = []client.MorpheusCredentialListItem{}
		}
		if handled, renderError := renderStructured(cmd, credentials); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(credentials) == 0 {
			fmt.Println("No HPE Morpheus credentials found.")
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
	},
}

var morpheusCredCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an HPE Morpheus API credential",
	Long: `Create an HPE Morpheus API credential. The API access token is collected
via a masked prompt, never on the command line.

An optional SSH jumphost lets the platform reach a Morpheus API that is
not directly routable; pass --jumphost-host together with
--jumphost-private-key-file (port defaults to 22, username to root).

Examples:
  ankra credentials morpheus create --name my-morpheus --api-url https://morpheus.example
  ankra credentials morpheus create --name my-morpheus --api-url https://morpheus.example \
    --jumphost-host 203.0.113.10 --jumphost-private-key-file ~/.ssh/id_ed25519`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		apiURL, _ := cmd.Flags().GetString("api-url")
		tlsInsecure, _ := cmd.Flags().GetBool("tls-insecure")

		jumphost, jumphostError := credentialJumphostFromFlags(cmd)
		if jumphostError != nil {
			return jumphostError
		}

		prompt := promptui.Prompt{
			Label: "HPE Morpheus Access Token",
			Mask:  '*',
			Validate: func(input string) error {
				if len(input) == 0 {
					return fmt.Errorf("access token cannot be empty")
				}
				return nil
			},
		}
		accessTokenValue, promptError := prompt.Run()
		if promptError != nil {
			return errors.New("prompt cancelled")
		}

		result, createError := apiClient.CreateMorpheusCredential(client.CreateMorpheusCredentialRequest{
			Name:        name,
			APIURL:      apiURL,
			AccessToken: accessTokenValue,
			TLSInsecure: tlsInsecure,
			Jumphost:    jumphost,
		})
		if createError != nil {
			return fmt.Errorf("creating HPE Morpheus credential: %w", createError)
		}

		if !result.Success {
			message := "failed to create HPE Morpheus credential:"
			for _, resourceError := range result.Errors {
				message += fmt.Sprintf("\n  - %s: %s", resourceError.Key, resourceError.Message)
			}
			return errors.New(message)
		}

		fmt.Printf("HPE Morpheus credential '%s' created successfully!\n", name)
		return nil
	},
}

var morpheusSSHKeyCmd = &cobra.Command{
	Use:     "ssh-key",
	Aliases: []string{"ssh-keys", "ssh"},
	Short:   "Manage SSH key credentials",
}

var morpheusSSHKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH key credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, listError := apiClient.ListMorpheusSSHKeyCredentials()
		if listError != nil {
			return fmt.Errorf("listing SSH key credentials: %w", listError)
		}

		if credentials == nil {
			credentials = []client.MorpheusCredentialListItem{}
		}
		if handled, renderError := renderStructured(cmd, credentials); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(credentials) == 0 {
			fmt.Println("No SSH key credentials found.")
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
	},
}

var morpheusSSHKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an SSH key credential",
	Long: `Create an SSH key credential. Either provide a public key or generate a new keypair.

Examples:
  ankra credentials morpheus ssh-key create --name my-key --generate
  ankra credentials morpheus ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."`,
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

		result, createError := apiClient.CreateMorpheusSSHKeyCredential(createRequest)
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
	morpheusCredCreateCmd.Flags().String("name", "", "Credential name (required)")
	morpheusCredCreateCmd.Flags().String("api-url", "", "HPE Morpheus API URL, e.g. https://morpheus.example (required)")
	morpheusCredCreateCmd.Flags().Bool("tls-insecure", false, "Skip TLS certificate verification for the API URL")
	registerCredentialJumphostFlags(morpheusCredCreateCmd)
	_ = morpheusCredCreateCmd.MarkFlagRequired("name")
	_ = morpheusCredCreateCmd.MarkFlagRequired("api-url")

	morpheusSSHKeyCreateCmd.Flags().String("name", "", "Credential name (required)")
	morpheusSSHKeyCreateCmd.Flags().String("public-key", "", "SSH public key")
	morpheusSSHKeyCreateCmd.Flags().Bool("generate", false, "Generate a new SSH keypair")
	_ = morpheusSSHKeyCreateCmd.MarkFlagRequired("name")

	registerStructuredOutputFlags(morpheusCredListCmd, morpheusSSHKeyListCmd)

	morpheusSSHKeyCmd.AddCommand(morpheusSSHKeyListCmd)
	morpheusSSHKeyCmd.AddCommand(morpheusSSHKeyCreateCmd)

	morpheusCredCmd.AddCommand(morpheusCredListCmd)
	morpheusCredCmd.AddCommand(morpheusCredCreateCmd)
	morpheusCredCmd.AddCommand(morpheusSSHKeyCmd)

	credentialsCmd.AddCommand(morpheusCredCmd)
}
