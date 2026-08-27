package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var orgDnsCredentialsCmd = &cobra.Command{
	Use:     "credentials",
	Aliases: []string{"credential"},
	Short:   "DNS webhook credentials for zones you own",
	Long: `Store and list the organisation's DNS webhook credentials - the ones the
custom DNS zone lane serves your own zones with ('ankra org
custom-dns-zones' for every cluster in the organisation, 'ankra cluster
custom-dns-zones' for one cluster).

The webhook provider URL embeds the provider token, so it is written to the
platform's secret store on create and never returned by any read: 'list'
shows names and ids only. Re-creating a credential under the same name
re-points every cluster binding that names it, which is how a rotated token
rolls out.`,
}

var orgDnsCredentialsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's DNS credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, listError := apiClient.ListDNSCredentials()
		if listError != nil {
			return fmt.Errorf("listing dns credentials: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, credentials); rendered || renderError != nil {
			return renderError
		}
		if len(credentials) == 0 {
			fmt.Println("No DNS credentials stored.")
			return nil
		}
		credentialTable := table.NewWriter()
		credentialTable.SetOutputMirror(os.Stdout)
		credentialTable.SetStyle(table.StyleRounded)
		credentialTable.AppendHeader(table.Row{"ID", "Name", "Created"})
		for _, credential := range credentials {
			credentialTable.AppendRow(table.Row{
				credential.ID, credential.Name, formatTimeAgo(credential.CreatedAt)})
		}
		credentialTable.Render()
		return nil
	},
}

var (
	orgDnsCredentialName       string
	orgDnsCredentialWebhookURL string
)

var orgDnsCredentialsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Store a DNS webhook credential",
	Long: `Store a DNS webhook credential. The webhook provider URL is the external-dns
webhook endpoint including its token; it goes to the platform's secret store
and is never echoed back.

Creating a credential under a name that already exists replaces its stored
URL, which re-points every cluster binding that names it - the rotation path.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		response, createError := apiClient.CreateDNSCredential(
			orgDnsCredentialName, orgDnsCredentialWebhookURL)
		if createError != nil {
			return fmt.Errorf("creating the dns credential: %w", createError)
		}
		if !response.Success {
			message := "the platform refused the credential:"
			for _, resourceError := range response.Errors {
				message += fmt.Sprintf("\n  - %s: %s", resourceError.Key, resourceError.Message)
			}
			return errors.New(message)
		}
		if rendered, renderError := renderStructured(cmd, response); rendered || renderError != nil {
			return renderError
		}
		fmt.Printf("DNS credential %s stored (id %s). The webhook URL is in the platform's secret store and will not be shown again.\n",
			response.Name, response.ID)
		return nil
	},
}

func init() {
	orgDnsCredentialsCreateCmd.Flags().StringVar(&orgDnsCredentialName, "name", "", "Credential name, referenced by 'org custom-dns-zones add --credential' and 'cluster custom-dns-zones add --credential' (required)")
	orgDnsCredentialsCreateCmd.Flags().StringVar(&orgDnsCredentialWebhookURL, "webhook-provider-url", "", "external-dns webhook endpoint including its token (required)")
	_ = orgDnsCredentialsCreateCmd.MarkFlagRequired("name")
	_ = orgDnsCredentialsCreateCmd.MarkFlagRequired("webhook-provider-url")

	registerStructuredOutputFlags(orgDnsCredentialsListCmd, orgDnsCredentialsCreateCmd)

	orgDnsCredentialsCmd.AddCommand(orgDnsCredentialsListCmd)
	orgDnsCredentialsCmd.AddCommand(orgDnsCredentialsCreateCmd)
	orgDnsCmd.AddCommand(orgDnsCredentialsCmd)
}
