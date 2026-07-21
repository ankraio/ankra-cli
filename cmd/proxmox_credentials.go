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

var proxmoxCredCmd = &cobra.Command{
	Use:     "proxmox",
	Aliases: []string{"pve"},
	Short:   "Manage Proxmox VE credentials",
	Long:    "Commands to list and create Proxmox VE API credentials and SSH key credentials.",
}

var proxmoxCredListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Proxmox VE API credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, listError := apiClient.ListProxmoxCredentials()
		if listError != nil {
			return fmt.Errorf("listing Proxmox VE credentials: %w", listError)
		}

		if credentials == nil {
			credentials = []client.ProxmoxCredentialListItem{}
		}
		if handled, renderError := renderStructured(cmd, credentials); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(credentials) == 0 {
			fmt.Println("No Proxmox VE credentials found.")
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

var proxmoxCredCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Proxmox VE API credential",
	Long: `Create a Proxmox VE API credential. The API token secret is collected
via a masked prompt, never on the command line.

An optional SSH jumphost lets the platform reach a Proxmox VE API that is
not directly routable; pass --jumphost-host together with
--jumphost-private-key-file (port defaults to 22, username to root).

Examples:
  ankra credentials proxmox create --name my-lab --api-url https://pve.example:8006 --token-id root@pam!ankra
  ankra credentials proxmox create --name my-lab --api-url https://pve.example:8006 --token-id root@pam!ankra \
    --jumphost-host 203.0.113.10 --jumphost-private-key-file ~/.ssh/id_ed25519`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		apiURL, _ := cmd.Flags().GetString("api-url")
		tokenID, _ := cmd.Flags().GetString("token-id")
		tlsInsecure, _ := cmd.Flags().GetBool("tls-insecure")

		jumphost, jumphostError := credentialJumphostFromFlags(cmd)
		if jumphostError != nil {
			return jumphostError
		}

		prompt := promptui.Prompt{
			Label: "Proxmox VE API Token Secret",
			Mask:  '*',
			Validate: func(input string) error {
				if len(input) == 0 {
					return fmt.Errorf("token secret cannot be empty")
				}
				return nil
			},
		}
		tokenSecretValue, promptError := prompt.Run()
		if promptError != nil {
			return errors.New("prompt cancelled")
		}

		result, createError := apiClient.CreateProxmoxCredential(client.CreateProxmoxCredentialRequest{
			Name:        name,
			APIURL:      apiURL,
			TokenID:     tokenID,
			TokenSecret: tokenSecretValue,
			TLSInsecure: tlsInsecure,
			Jumphost:    jumphost,
		})
		if createError != nil {
			return fmt.Errorf("creating Proxmox VE credential: %w", createError)
		}

		if !result.Success {
			message := "failed to create Proxmox VE credential:"
			for _, resourceError := range result.Errors {
				message += fmt.Sprintf("\n  - %s: %s", resourceError.Key, resourceError.Message)
			}
			return errors.New(message)
		}

		fmt.Printf("Proxmox VE credential '%s' created successfully!\n", name)
		return nil
	},
}

var proxmoxSSHKeyCmd = &cobra.Command{
	Use:     "ssh-key",
	Aliases: []string{"ssh-keys", "ssh"},
	Short:   "Manage SSH key credentials",
}

var proxmoxSSHKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH key credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, listError := apiClient.ListProxmoxSSHKeyCredentials()
		if listError != nil {
			return fmt.Errorf("listing SSH key credentials: %w", listError)
		}

		if credentials == nil {
			credentials = []client.ProxmoxCredentialListItem{}
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

var proxmoxSSHKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an SSH key credential",
	Long: `Create an SSH key credential. Either provide a public key or generate a new keypair.

Examples:
  ankra credentials proxmox ssh-key create --name my-key --generate
  ankra credentials proxmox ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."`,
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

		result, createError := apiClient.CreateProxmoxSSHKeyCredential(createRequest)
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

// registerCredentialJumphostFlags adds the shared optional jumphost flags used
// by the Proxmox VE and HPE Morpheus credential create commands.
func registerCredentialJumphostFlags(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().String("jumphost-host", "", "SSH jumphost address used to reach the API")
		command.Flags().Int("jumphost-port", 22, "SSH jumphost port")
		command.Flags().String("jumphost-username", "root", "SSH jumphost username")
		command.Flags().String("jumphost-private-key-file", "", "Path to the SSH private key for the jumphost")
	}
}

// credentialJumphostFromFlags builds the optional jumphost payload from the
// shared jumphost flags. It returns nil when no jumphost was requested; a
// jumphost requires both --jumphost-host and --jumphost-private-key-file.
func credentialJumphostFromFlags(cmd *cobra.Command) (*client.CredentialJumphost, error) {
	host, _ := cmd.Flags().GetString("jumphost-host")
	privateKeyFile, _ := cmd.Flags().GetString("jumphost-private-key-file")
	if host == "" && privateKeyFile == "" {
		return nil, nil
	}
	if host == "" {
		return nil, withExitCode(exitUsage, errors.New("--jumphost-private-key-file requires --jumphost-host"))
	}
	if privateKeyFile == "" {
		return nil, withExitCode(exitUsage, errors.New("--jumphost-host requires --jumphost-private-key-file"))
	}

	privateKey, readError := os.ReadFile(privateKeyFile)
	if readError != nil {
		if os.IsNotExist(readError) {
			return nil, withExitCode(exitNotFound, fmt.Errorf("jumphost private key file %q does not exist", privateKeyFile))
		}
		return nil, fmt.Errorf("reading jumphost private key file %q: %w", privateKeyFile, readError)
	}

	port, _ := cmd.Flags().GetInt("jumphost-port")
	username, _ := cmd.Flags().GetString("jumphost-username")
	return &client.CredentialJumphost{
		Host:       host,
		Port:       port,
		Username:   username,
		PrivateKey: string(privateKey),
	}, nil
}

func init() {
	proxmoxCredCreateCmd.Flags().String("name", "", "Credential name (required)")
	proxmoxCredCreateCmd.Flags().String("api-url", "", "Proxmox VE API URL, e.g. https://pve.example:8006 (required)")
	proxmoxCredCreateCmd.Flags().String("token-id", "", "Proxmox VE API token ID, e.g. root@pam!ankra (required)")
	proxmoxCredCreateCmd.Flags().Bool("tls-insecure", false, "Skip TLS certificate verification for the API URL")
	registerCredentialJumphostFlags(proxmoxCredCreateCmd)
	_ = proxmoxCredCreateCmd.MarkFlagRequired("name")
	_ = proxmoxCredCreateCmd.MarkFlagRequired("api-url")
	_ = proxmoxCredCreateCmd.MarkFlagRequired("token-id")

	proxmoxSSHKeyCreateCmd.Flags().String("name", "", "Credential name (required)")
	proxmoxSSHKeyCreateCmd.Flags().String("public-key", "", "SSH public key")
	proxmoxSSHKeyCreateCmd.Flags().Bool("generate", false, "Generate a new SSH keypair")
	_ = proxmoxSSHKeyCreateCmd.MarkFlagRequired("name")

	registerStructuredOutputFlags(proxmoxCredListCmd, proxmoxSSHKeyListCmd)

	proxmoxSSHKeyCmd.AddCommand(proxmoxSSHKeyListCmd)
	proxmoxSSHKeyCmd.AddCommand(proxmoxSSHKeyCreateCmd)

	proxmoxCredCmd.AddCommand(proxmoxCredListCmd)
	proxmoxCredCmd.AddCommand(proxmoxCredCreateCmd)
	proxmoxCredCmd.AddCommand(proxmoxSSHKeyCmd)

	credentialsCmd.AddCommand(proxmoxCredCmd)
}
