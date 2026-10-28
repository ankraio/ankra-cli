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

// azureClientSecretEnv lets automation pass the service principal secret
// without an interactive prompt (expand it from a secret manager into the
// process environment; never put it on the command line).
const azureClientSecretEnv = "AZURE_CLIENT_SECRET"

var azureCredCmd = &cobra.Command{
	Use:     "azure",
	Aliases: []string{"az"},
	Short:   "Manage Azure credentials",
	Long:    "Commands to list and create Azure service principal credentials used for Azure Kubernetes Service (AKS).",
}

var azureCredListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Azure credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := apiClient.ListAzureCredentials()
		if err != nil {
			return fmt.Errorf("listing Azure credentials: %w", err)
		}

		if creds == nil {
			creds = []client.AzureCredentialListItem{}
		}
		if handled, err := renderStructured(cmd, creds); err != nil {
			return err
		} else if handled {
			return nil
		}

		if len(creds) == 0 {
			fmt.Println("No Azure credentials found.")
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

var azureCredCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an Azure service principal credential",
	Long: `Store an Azure service principal for AKS. The client secret is read from
the ` + azureClientSecretEnv + ` environment variable when set, and prompted
for (masked) otherwise - it is never accepted as a flag.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		subscriptionID, _ := cmd.Flags().GetString("subscription-id")
		tenantID, _ := cmd.Flags().GetString("tenant-id")
		clientID, _ := cmd.Flags().GetString("client-id")

		clientSecret := os.Getenv(azureClientSecretEnv)
		if clientSecret == "" {
			prompt := promptui.Prompt{
				Label: "Azure client secret",
				Mask:  '*',
				Validate: func(input string) error {
					if len(input) == 0 {
						return fmt.Errorf("client secret cannot be empty")
					}
					return nil
				},
			}
			secretValue, err := prompt.Run()
			if err != nil {
				return errors.New("prompt cancelled")
			}
			clientSecret = secretValue
		}

		result, err := apiClient.CreateAzureCredential(client.CreateAzureCredentialRequest{
			Name:           name,
			SubscriptionID: subscriptionID,
			TenantID:       tenantID,
			ClientID:       clientID,
			ClientSecret:   clientSecret,
		})
		if err != nil {
			return fmt.Errorf("creating Azure credential: %w", err)
		}

		if !result.Success {
			msg := "failed to create Azure credential:"
			for _, e := range result.Errors {
				msg += fmt.Sprintf("\n  - %s: %s", e.Key, e.Message)
			}
			return errors.New(msg)
		}

		fmt.Printf("Azure credential '%s' created successfully!\n", name)
		return nil
	},
}

func init() {
	azureCredCreateCmd.Flags().String("name", "", "Credential name")
	azureCredCreateCmd.Flags().String("subscription-id", "", "Azure subscription ID the service principal manages")
	azureCredCreateCmd.Flags().String("tenant-id", "", "Microsoft Entra tenant ID")
	azureCredCreateCmd.Flags().String("client-id", "", "Service principal application (client) ID")
	_ = azureCredCreateCmd.MarkFlagRequired("name")
	_ = azureCredCreateCmd.MarkFlagRequired("subscription-id")
	_ = azureCredCreateCmd.MarkFlagRequired("tenant-id")
	_ = azureCredCreateCmd.MarkFlagRequired("client-id")

	azureCredCmd.AddCommand(azureCredListCmd, azureCredCreateCmd)
	credentialsCmd.AddCommand(azureCredCmd)
	registerStructuredOutputFlags(azureCredListCmd)
}
