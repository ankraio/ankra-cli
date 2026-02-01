package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var credentialsCmd = &cobra.Command{
	Use:     "credentials",
	Aliases: []string{"credential", "cred", "creds"},
	Short:   "Manage credentials",
	Long:    "Commands to list, view, validate, and delete credentials.",
}

var credentialsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all credentials",
	Run: func(cmd *cobra.Command, args []string) {
		provider, _ := cmd.Flags().GetString("provider")
		var providerPtr *string
		if provider != "" {
			providerPtr = &provider
		}

		creds, err := client.ListCredentials(apiToken, baseURL, providerPtr)
		if err != nil {
			fmt.Printf("Error listing credentials: %v\n", err)
			return
		}

		if len(creds) == 0 {
			fmt.Println("No credentials found.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Provider", "Clusters", "Created"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 8},
			{Number: 5, WidthMin: 15},
		})

		for _, cred := range creds {
			t.AppendRow(table.Row{
				cred.ID,
				cred.Name,
				cred.Provider,
				cred.ClusterCount,
				formatTimeAgo(cred.CreatedAt),
			})
		}
		t.Render()
	},
}

var credentialsValidateCmd = &cobra.Command{
	Use:   "validate <name>",
	Short: "Validate a credential name",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		result, err := client.ValidateCredentialName(apiToken, baseURL, name)
		if err != nil {
			fmt.Printf("Error validating credential name: %v\n", err)
			return
		}

		if result.Valid {
			fmt.Printf("Credential name '%s' is valid and available.\n", name)
		} else {
			msg := "unavailable"
			if result.Message != nil {
				msg = *result.Message
			}
			fmt.Printf("Credential name '%s' is invalid: %s\n", name, msg)
		}
	},
}

var credentialsDeleteCmd = &cobra.Command{
	Use:   "delete <credential_id>",
	Short: "Delete a credential",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		credentialID := args[0]

		// Get organisation ID from current org
		var orgID string
		local, err := loadSelectedOrganisation()
		if err == nil && local.OrganisationID != "" {
			orgID = local.OrganisationID
		} else {
			// Fall back to server's current org
			orgs, err := client.ListOrganisations(apiToken, baseURL)
			if err != nil {
				fmt.Printf("Error fetching organisation: %v\n", err)
				return
			}
			for _, org := range orgs {
				if org.UserCurrent {
					orgID = org.OrganisationID
					break
				}
			}
		}

		if orgID == "" {
			fmt.Println("No organisation selected. Use 'ankra org switch <org_id>' first.")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result, err := client.DeleteCredential(ctx, apiToken, baseURL, credentialID, orgID)
		if err != nil {
			fmt.Printf("Error deleting credential: %v\n", err)
			return
		}

		if result.Success {
			fmt.Println("Credential deleted successfully!")
		}
	},
}

var credentialsGetCmd = &cobra.Command{
	Use:   "get <credential_id>",
	Short: "Get details of a specific credential",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		credentialID := args[0]

		cred, err := client.GetCredential(apiToken, baseURL, credentialID)
		if err != nil {
			fmt.Printf("Error fetching credential: %v\n", err)
			return
		}

		fmt.Printf("Credential Details:\n")
		fmt.Printf("  ID:       %s\n", cred.ID)
		fmt.Printf("  Name:     %s\n", cred.Name)
		fmt.Printf("  Provider: %s\n", cred.Provider)
		if cred.Description != nil {
			fmt.Printf("  Description: %s\n", *cred.Description)
		}
		if cred.Owner != nil {
			fmt.Printf("  Owner:    %s\n", *cred.Owner)
		}
		if cred.Repository != nil {
			fmt.Printf("  Repository: %s\n", *cred.Repository)
		}
		fmt.Printf("  Created:  %s\n", formatTimeAgo(cred.CreatedAt))
	},
}

func init() {
	credentialsListCmd.Flags().String("provider", "", "Filter by provider (e.g., github)")

	credentialsCmd.AddCommand(credentialsListCmd)
	credentialsCmd.AddCommand(credentialsValidateCmd)
	credentialsCmd.AddCommand(credentialsDeleteCmd)
	credentialsCmd.AddCommand(credentialsGetCmd)

	rootCmd.AddCommand(credentialsCmd)
}
