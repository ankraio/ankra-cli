package cmd

import (
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var hetznerCredCmd = &cobra.Command{
	Use:     "hetzner",
	Aliases: []string{"hz"},
	Short:   "Manage Hetzner credentials",
	Long:    "Commands to list and create Hetzner API credentials and SSH key credentials.",
}

var hetznerCredListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Hetzner API credentials",
	Run: func(cmd *cobra.Command, args []string) {
		creds, err := client.ListHetznerCredentials(apiToken, baseURL)
		if err != nil {
			fmt.Printf("Error listing Hetzner credentials: %v\n", err)
			return
		}

		if len(creds) == 0 {
			fmt.Println("No Hetzner credentials found.")
			return
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

		for _, cred := range creds {
			available := "yes"
			if !cred.Available {
				available = "no"
			}
			t.AppendRow(table.Row{
				cred.ID,
				cred.Name,
				available,
				formatTimeAgo(cred.CreatedAt),
			})
		}
		t.Render()
	},
}

var hetznerCredCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Hetzner API credential",
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		apiTokenValue, _ := cmd.Flags().GetString("api-token")

		result, err := client.CreateHetznerCredential(apiToken, baseURL, client.CreateHetznerCredentialRequest{
			Name:     name,
			APIToken: apiTokenValue,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating Hetzner credential: %v\n", err)
			os.Exit(1)
		}

		if !result.Success {
			fmt.Fprintln(os.Stderr, "Failed to create Hetzner credential:")
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "  - %s: %s\n", e.Key, e.Message)
			}
			os.Exit(1)
		}

		fmt.Printf("Hetzner credential '%s' created successfully!\n", name)
	},
}

var sshKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH key credentials",
	Run: func(cmd *cobra.Command, args []string) {
		creds, err := client.ListSSHKeyCredentials(apiToken, baseURL)
		if err != nil {
			fmt.Printf("Error listing SSH key credentials: %v\n", err)
			return
		}

		if len(creds) == 0 {
			fmt.Println("No SSH key credentials found.")
			return
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

		for _, cred := range creds {
			available := "yes"
			if !cred.Available {
				available = "no"
			}
			t.AppendRow(table.Row{
				cred.ID,
				cred.Name,
				available,
				formatTimeAgo(cred.CreatedAt),
			})
		}
		t.Render()
	},
}

var sshKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an SSH key credential",
	Long: `Create an SSH key credential. Either provide a public key or generate a new keypair.

Examples:
  ankra credentials hetzner ssh-key create --name my-key --generate
  ankra credentials hetzner ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		publicKey, _ := cmd.Flags().GetString("public-key")
		generate, _ := cmd.Flags().GetBool("generate")

		if !generate && publicKey == "" {
			fmt.Fprintln(os.Stderr, "Either --public-key or --generate must be provided.")
			os.Exit(1)
		}

		req := client.CreateSSHKeyCredentialRequest{
			Name:     name,
			Generate: generate,
		}
		if publicKey != "" {
			req.SSHPublicKey = &publicKey
		}

		result, err := client.CreateSSHKeyCredential(apiToken, baseURL, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating SSH key credential: %v\n", err)
			os.Exit(1)
		}

		if !result.Success {
			fmt.Fprintln(os.Stderr, "Failed to create SSH key credential:")
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "  - %s: %s\n", e.Key, e.Message)
			}
			os.Exit(1)
		}

		fmt.Printf("SSH key credential '%s' created successfully!\n", name)

		if result.PrivateKey != nil {
			fmt.Println("\nGenerated private key (save this, it will not be shown again):")
			fmt.Println(*result.PrivateKey)
		}
	},
}

var sshKeyCmd = &cobra.Command{
	Use:     "ssh-key",
	Aliases: []string{"ssh-keys", "ssh"},
	Short:   "Manage SSH key credentials",
}

func init() {
	hetznerCredCreateCmd.Flags().String("name", "", "Credential name (required)")
	hetznerCredCreateCmd.Flags().String("api-token", "", "Hetzner API token (required)")
	_ = hetznerCredCreateCmd.MarkFlagRequired("name")
	_ = hetznerCredCreateCmd.MarkFlagRequired("api-token")

	sshKeyCreateCmd.Flags().String("name", "", "Credential name (required)")
	sshKeyCreateCmd.Flags().String("public-key", "", "SSH public key")
	sshKeyCreateCmd.Flags().Bool("generate", false, "Generate a new SSH keypair")
	_ = sshKeyCreateCmd.MarkFlagRequired("name")

	sshKeyCmd.AddCommand(sshKeyListCmd)
	sshKeyCmd.AddCommand(sshKeyCreateCmd)

	hetznerCredCmd.AddCommand(hetznerCredListCmd)
	hetznerCredCmd.AddCommand(hetznerCredCreateCmd)
	hetznerCredCmd.AddCommand(sshKeyCmd)

	credentialsCmd.AddCommand(hetznerCredCmd)
}
