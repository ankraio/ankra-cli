package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

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
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, _ := cmd.Flags().GetString("provider")
		var providerPtr *string
		if provider != "" {
			providerPtr = &provider
		}

		creds, err := apiClient.ListCredentials(providerPtr)
		if err != nil {
			return fmt.Errorf("listing credentials: %w", err)
		}

		if len(creds) == 0 {
			fmt.Println("No credentials found.")
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Provider", "State", "Available", "Repos", "Last Synced", "Created"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 10},
			{Number: 5, WidthMin: 9},
			{Number: 6, WidthMin: 6},
			{Number: 7, WidthMin: 15},
			{Number: 8, WidthMin: 15},
		})

		for _, cred := range creds {
			state := "-"
			if cred.State != nil && *cred.State != "" {
				state = *cred.State
			}
			repoCount := "-"
			if cred.RepositoryCount != nil {
				repoCount = fmt.Sprintf("%d", *cred.RepositoryCount)
			}
			lastSynced := "-"
			if cred.LastSyncedAt != nil && *cred.LastSyncedAt != "" {
				lastSynced = formatTimeAgo(*cred.LastSyncedAt)
			}
			t.AppendRow(table.Row{
				cred.ID,
				cred.Name,
				cred.Provider,
				state,
				cred.Available,
				repoCount,
				lastSynced,
				formatTimeAgo(cred.CreatedAt),
			})
		}
		t.Render()
		return nil
	},
}

var credentialsValidateCmd = &cobra.Command{
	Use:   "validate <name>",
	Short: "Validate a credential name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		result, err := apiClient.ValidateCredentialName(name)
		if err != nil {
			return fmt.Errorf("validating credential name: %w", err)
		}

		if result.Valid {
			fmt.Printf("Credential name '%s' is valid and available.\n", name)
			return nil
		}
		msg := "unavailable"
		if result.Message != nil {
			msg = *result.Message
		}
		return fmt.Errorf("credential name %q is invalid: %s", name, msg)
	},
}

var credentialsDeleteCmd = &cobra.Command{
	Use:   "delete <credential_id>",
	Short: "Delete a credential",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID := args[0]

		var orgID string
		local, err := loadSelectedOrganisation()
		if err == nil && local.OrganisationID != "" {
			orgID = local.OrganisationID
		} else {
			orgs, err := apiClient.ListOrganisations()
			if err != nil {
				return fmt.Errorf("fetching organisation: %w", err)
			}
			for _, org := range orgs {
				if org.UserCurrent {
					orgID = org.OrganisationID
					break
				}
			}
		}

		if orgID == "" {
			return fmt.Errorf("no organisation selected: run `ankra org switch <org_id>` first")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result, err := apiClient.DeleteCredential(ctx, credentialID, orgID)
		if err != nil {
			return fmt.Errorf("deleting credential: %w", err)
		}

		if result.Success {
			fmt.Println("Credential deleted successfully!")
			return nil
		}
		return fmt.Errorf("delete request did not report success")
	},
}

var credentialsGetCmd = &cobra.Command{
	Use:   "get <credential_id|name>",
	Short: "Get details of a specific credential",
	Long:  "Get details of a credential by its ID or by its name (as shown in `ankra credentials list`).",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, err := resolveCredentialID(args[0])
		if err != nil {
			return err
		}

		cred, err := apiClient.GetCredential(credentialID)
		if err != nil {
			return fmt.Errorf("fetching credential: %w", err)
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
		return nil
	},
}

// resolveCredentialID accepts either a credential ID (UUID) or a credential
// name. A UUID is returned unchanged; a name is resolved to its ID by matching
// against the credential list, since the backend's get-credential endpoint
// only accepts a UUID path parameter.
func resolveCredentialID(idOrName string) (string, error) {
	if looksLikeUUID(idOrName) {
		return idOrName, nil
	}

	creds, err := apiClient.ListCredentials(nil)
	if err != nil {
		return "", fmt.Errorf("looking up credential %q: %w", idOrName, err)
	}

	var matchedIDs []string
	for _, cred := range creds {
		if cred.Name == idOrName {
			matchedIDs = append(matchedIDs, cred.ID)
		}
	}

	switch len(matchedIDs) {
	case 1:
		return matchedIDs[0], nil
	case 0:
		return "", fmt.Errorf("credential %q not found; run `ankra credentials list` to see available credentials", idOrName)
	default:
		return "", fmt.Errorf("multiple credentials are named %q; pass the credential ID instead", idOrName)
	}
}

// looksLikeUUID reports whether s has the canonical 8-4-4-4-12 hexadecimal
// UUID shape, so a value can be treated as an ID rather than a name without
// pulling in a UUID dependency.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for index, char := range s {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			isHex := (char >= '0' && char <= '9') ||
				(char >= 'a' && char <= 'f') ||
				(char >= 'A' && char <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

func init() {
	credentialsListCmd.Flags().String("provider", "", "Filter by provider (e.g., github)")

	credentialsCmd.AddCommand(credentialsListCmd)
	credentialsCmd.AddCommand(credentialsValidateCmd)
	credentialsCmd.AddCommand(credentialsDeleteCmd)
	credentialsCmd.AddCommand(credentialsGetCmd)

	rootCmd.AddCommand(credentialsCmd)
}
