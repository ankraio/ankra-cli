package cmd

import (
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var ovhCredCmd = &cobra.Command{
	Use:   "ovh",
	Short: "Manage OVH credentials",
	Long:  "Commands to list and create OVH API credentials and SSH key credentials.",
}

var ovhCredListCmd = &cobra.Command{
	Use:   "list",
	Short: "List OVH API credentials",
	Run: func(cmd *cobra.Command, args []string) {
		creds, err := client.ListOvhCredentials(apiToken, baseURL)
		if err != nil {
			fmt.Printf("Error listing OVH credentials: %v\n", err)
			return
		}

		if len(creds) == 0 {
			fmt.Println("No OVH credentials found.")
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

var ovhCredCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an OVH API credential",
	Long: `Create an OVH API credential. You will be prompted for the required secrets.

Generate your OVH API credentials at https://api.ovh.com/createToken/ with
GET, POST, PUT, DELETE rights on /cloud/project/* and /cloud/project.

Examples:
  ankra credentials ovh create --name my-ovh-cred --project-id <project-id>`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		projectID, _ := cmd.Flags().GetString("project-id")

		appKeyPrompt := promptui.Prompt{
			Label: "OVH Application Key",
			Validate: func(input string) error {
				if len(input) == 0 {
					return fmt.Errorf("application key cannot be empty")
				}
				return nil
			},
		}
		applicationKey, err := appKeyPrompt.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Prompt cancelled.\n")
			os.Exit(1)
		}

		appSecretPrompt := promptui.Prompt{
			Label: "OVH Application Secret",
			Mask:  '*',
			Validate: func(input string) error {
				if len(input) == 0 {
					return fmt.Errorf("application secret cannot be empty")
				}
				return nil
			},
		}
		applicationSecret, err := appSecretPrompt.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Prompt cancelled.\n")
			os.Exit(1)
		}

		consumerKeyPrompt := promptui.Prompt{
			Label: "OVH Consumer Key",
			Mask:  '*',
			Validate: func(input string) error {
				if len(input) == 0 {
					return fmt.Errorf("consumer key cannot be empty")
				}
				return nil
			},
		}
		consumerKey, err := consumerKeyPrompt.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Prompt cancelled.\n")
			os.Exit(1)
		}

		result, err := client.CreateOvhCredential(apiToken, baseURL, client.CreateOvhCredentialRequest{
			Name:              name,
			ApplicationKey:    applicationKey,
			ApplicationSecret: applicationSecret,
			ConsumerKey:       consumerKey,
			ProjectID:         projectID,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating OVH credential: %v\n", err)
			os.Exit(1)
		}

		if !result.Success {
			fmt.Fprintln(os.Stderr, "Failed to create OVH credential:")
			for _, e := range result.Errors {
				fmt.Fprintf(os.Stderr, "  - %s: %s\n", e.Key, e.Message)
			}
			os.Exit(1)
		}

		fmt.Printf("OVH credential '%s' created successfully!\n", name)
	},
}

var ovhSSHKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH key credentials",
	Run: func(cmd *cobra.Command, args []string) {
		creds, err := client.ListOvhSSHKeyCredentials(apiToken, baseURL)
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

var ovhSSHKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an SSH key credential for OVH",
	Long: `Create an SSH key credential. Either provide a public key or generate a new keypair.

Examples:
  ankra credentials ovh ssh-key create --name my-key --generate
  ankra credentials ovh ssh-key create --name my-key --public-key "ssh-ed25519 AAAA..."`,
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

		result, err := client.CreateOvhSSHKeyCredential(apiToken, baseURL, req)
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

var ovhSSHKeyCmd = &cobra.Command{
	Use:     "ssh-key",
	Aliases: []string{"ssh-keys", "ssh"},
	Short:   "Manage SSH key credentials for OVH",
}

func init() {
	ovhCredCreateCmd.Flags().String("name", "", "Credential name (required)")
	ovhCredCreateCmd.Flags().String("project-id", "", "OVH Cloud project ID (required)")
	_ = ovhCredCreateCmd.MarkFlagRequired("name")
	_ = ovhCredCreateCmd.MarkFlagRequired("project-id")

	ovhSSHKeyCreateCmd.Flags().String("name", "", "Credential name (required)")
	ovhSSHKeyCreateCmd.Flags().String("public-key", "", "SSH public key")
	ovhSSHKeyCreateCmd.Flags().Bool("generate", false, "Generate a new SSH keypair")
	_ = ovhSSHKeyCreateCmd.MarkFlagRequired("name")

	ovhSSHKeyCmd.AddCommand(ovhSSHKeyListCmd)
	ovhSSHKeyCmd.AddCommand(ovhSSHKeyCreateCmd)

	ovhCredCmd.AddCommand(ovhCredListCmd)
	ovhCredCmd.AddCommand(ovhCredCreateCmd)
	ovhCredCmd.AddCommand(ovhSSHKeyCmd)

	credentialsCmd.AddCommand(ovhCredCmd)
}
