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

var digitaloceanCredCmd = &cobra.Command{
	Use:     "digitalocean",
	Aliases: []string{"do"},
	Short:   "Manage DigitalOcean credentials",
	Long:    "Commands to list and create DigitalOcean API credentials and SSH key credentials.",
}

var digitaloceanCredListCmd = &cobra.Command{
	Use:   "list",
	Short: "List DigitalOcean API credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := apiClient.ListDigitaloceanCredentials()
		if err != nil {
			return fmt.Errorf("listing DigitalOcean credentials: %w", err)
		}

		if creds == nil {
			creds = []client.DigitaloceanCredentialListItem{}
		}
		if handled, err := renderStructured(cmd, creds); err != nil {
			return err
		} else if handled {
			return nil
		}

		if len(creds) == 0 {
			fmt.Println("No DigitalOcean credentials found.")
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
		return nil
	},
}

var digitaloceanCredCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an DigitalOcean API credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")

		prompt := promptui.Prompt{
			Label: "DigitalOcean API Token",
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
			return errors.New("prompt cancelled")
		}

		result, err := apiClient.CreateDigitaloceanCredential(client.CreateDigitaloceanCredentialRequest{
			Name:     name,
			APIToken: apiTokenValue,
		})
		if err != nil {
			return fmt.Errorf("creating DigitalOcean credential: %w", err)
		}

		if !result.Success {
			msg := "failed to create DigitalOcean credential:"
			for _, e := range result.Errors {
				msg += fmt.Sprintf("\n  - %s: %s", e.Key, e.Message)
			}
			return errors.New(msg)
		}

		fmt.Printf("DigitalOcean credential '%s' created successfully!\n", name)
		return nil
	},
}

var digitaloceanSSHKeyCmd = &cobra.Command{
	Use:     "ssh-key",
	Aliases: []string{"ssh-keys", "ssh"},
	Short:   "Manage SSH key credentials",
}

var digitaloceanSSHKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH key credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := apiClient.ListDigitaloceanSSHKeyCredentials()
		if err != nil {
			return fmt.Errorf("listing SSH key credentials: %w", err)
		}

		if creds == nil {
			creds = []client.DigitaloceanCredentialListItem{}
		}
		if handled, err := renderStructured(cmd, creds); err != nil {
			return err
		} else if handled {
			return nil
		}

		if len(creds) == 0 {
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
		return nil
	},
}

var digitaloceanSSHKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an SSH key credential",
	Long: `Create an SSH key credential. Either provide a public key or generate a new keypair.

Examples:
  ankra credentials digitalocean ssh-key create --name my-key --generate
  ankra credentials digitalocean ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		publicKey, _ := cmd.Flags().GetString("public-key")
		generate, _ := cmd.Flags().GetBool("generate")

		if !generate && publicKey == "" {
			return errors.New("either --public-key or --generate must be provided")
		}

		req := client.CreateSSHKeyCredentialRequest{
			Name:     name,
			Generate: generate,
		}
		if publicKey != "" {
			req.SSHPublicKey = &publicKey
		}

		result, err := apiClient.CreateDigitaloceanSSHKeyCredential(req)
		if err != nil {
			return fmt.Errorf("creating SSH key credential: %w", err)
		}

		if !result.Success {
			msg := "failed to create SSH key credential:"
			for _, e := range result.Errors {
				msg += fmt.Sprintf("\n  - %s: %s", e.Key, e.Message)
			}
			return errors.New(msg)
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
	digitaloceanCredCreateCmd.Flags().String("name", "", "Credential name (required)")
	_ = digitaloceanCredCreateCmd.MarkFlagRequired("name")

	digitaloceanSSHKeyCreateCmd.Flags().String("name", "", "Credential name (required)")
	digitaloceanSSHKeyCreateCmd.Flags().String("public-key", "", "SSH public key")
	digitaloceanSSHKeyCreateCmd.Flags().Bool("generate", false, "Generate a new SSH keypair")
	_ = digitaloceanSSHKeyCreateCmd.MarkFlagRequired("name")

	registerStructuredOutputFlags(digitaloceanCredListCmd, digitaloceanSSHKeyListCmd)

	digitaloceanSSHKeyCmd.AddCommand(digitaloceanSSHKeyListCmd)
	digitaloceanSSHKeyCmd.AddCommand(digitaloceanSSHKeyCreateCmd)

	digitaloceanCredCmd.AddCommand(digitaloceanCredListCmd)
	digitaloceanCredCmd.AddCommand(digitaloceanCredCreateCmd)
	digitaloceanCredCmd.AddCommand(digitaloceanSSHKeyCmd)

	credentialsCmd.AddCommand(digitaloceanCredCmd)
}
