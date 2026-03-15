package cmd

import (
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var upcloudCredCmd = &cobra.Command{
	Use:     "upcloud",
	Aliases: []string{"uc"},
	Short:   "Manage UpCloud credentials",
	Long:    "Commands to list and create UpCloud API credentials and SSH key credentials.",
}

var upcloudCredListCmd = &cobra.Command{
	Use:   "list",
	Short: "List UpCloud API credentials",
	Run: func(cmd *cobra.Command, args []string) {
		creds, err := apiClient.ListUpcloudCredentials()
		if err != nil {
			fmt.Printf("Error listing UpCloud credentials: %v\n", err)
			return
		}

		if len(creds) == 0 {
			fmt.Println("No UpCloud credentials found.")
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

var upcloudCredCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an UpCloud API credential",
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")

		prompt := promptui.Prompt{
			Label: "UpCloud API Token",
			Mask:  '*',
			Validate: func(input string) error {
				if len(input) == 0 {
					return fmt.Errorf("token cannot be empty")
				}
				return nil
			},
		}
		apiTokenValue, err := prompt.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Prompt cancelled.\n")
			os.Exit(1)
		}

		result, err := apiClient.CreateUpcloudCredential(client.CreateUpcloudCredentialRequest{
			Name:     name,
			APIToken: apiTokenValue,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating UpCloud credential: %v\n", err)
			os.Exit(1)
		}

		if !result.Success {
			fmt.Fprintln(os.Stderr, "Failed to create UpCloud credential:")
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "  - %s: %s\n", e.Key, e.Message)
			}
			os.Exit(1)
		}

		fmt.Printf("UpCloud credential '%s' created successfully!\n", name)
	},
}

var upcloudSSHKeyCmd = &cobra.Command{
	Use:     "ssh-key",
	Aliases: []string{"ssh-keys", "ssh"},
	Short:   "Manage SSH key credentials",
}

var upcloudSSHKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH key credentials",
	Run: func(cmd *cobra.Command, args []string) {
		creds, err := apiClient.ListUpcloudSSHKeyCredentials()
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

var upcloudSSHKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an SSH key credential",
	Long: `Create an SSH key credential. Either provide a public key or generate a new keypair.

Examples:
  ankra credentials upcloud ssh-key create --name my-key --generate
  ankra credentials upcloud ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."`,
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

		result, err := apiClient.CreateUpcloudSSHKeyCredential(req)
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

func init() {
	upcloudCredCreateCmd.Flags().String("name", "", "Credential name (required)")
	_ = upcloudCredCreateCmd.MarkFlagRequired("name")

	upcloudSSHKeyCreateCmd.Flags().String("name", "", "Credential name (required)")
	upcloudSSHKeyCreateCmd.Flags().String("public-key", "", "SSH public key")
	upcloudSSHKeyCreateCmd.Flags().Bool("generate", false, "Generate a new SSH keypair")
	_ = upcloudSSHKeyCreateCmd.MarkFlagRequired("name")

	upcloudSSHKeyCmd.AddCommand(upcloudSSHKeyListCmd)
	upcloudSSHKeyCmd.AddCommand(upcloudSSHKeyCreateCmd)

	upcloudCredCmd.AddCommand(upcloudCredListCmd)
	upcloudCredCmd.AddCommand(upcloudCredCreateCmd)
	upcloudCredCmd.AddCommand(upcloudSSHKeyCmd)

	credentialsCmd.AddCommand(upcloudCredCmd)
}
