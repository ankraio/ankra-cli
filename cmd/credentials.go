package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
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

var credentialSortFields = []sortField[client.Credential]{
	{"id", func(a, b client.Credential) int { return compareFold(a.ID, b.ID) }},
	{"name", func(a, b client.Credential) int { return compareFold(a.Name, b.Name) }},
	{"provider", func(a, b client.Credential) int { return compareFold(a.Provider, b.Provider) }},
	{"state", func(a, b client.Credential) int { return compareFoldPtr(a.State, b.State) }},
	{"available", func(a, b client.Credential) int { return compareBools(a.Available, b.Available) }},
	{"repos", func(a, b client.Credential) int { return compareIntPtrs(a.RepositoryCount, b.RepositoryCount) }},
	{"last-synced", func(a, b client.Credential) int { return compareTimeStringPtrs(a.LastSyncedAt, b.LastSyncedAt) }},
	{"created", func(a, b client.Credential) int { return compareTimeStrings(a.CreatedAt, b.CreatedAt) }},
}

var credentialsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		sortCreds, err := resolveSort(cmd, credentialSortFields)
		if err != nil {
			return err
		}
		provider, _ := cmd.Flags().GetString("provider")
		var providerPtr *string
		if provider != "" {
			providerPtr = &provider
		}

		creds, err := apiClient.ListCredentials(providerPtr)
		if err != nil {
			return fmt.Errorf("listing credentials: %w", err)
		}

		if creds == nil {
			creds = []client.Credential{}
		}
		if providerPtr == nil {
			creds = ensureGitCredentialsListed(cmd, creds)
		}
		sortCreds(creds)
		if rendered, err := renderStructured(cmd, creds); rendered || err != nil {
			return err
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

		hasGithubCredential := false
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
			provider := cred.Provider
			if strings.EqualFold(cred.Provider, "github") {
				hasGithubCredential = true
				// An installation id means the credential is backed by a
				// GitHub App installation rather than a stored token.
				if cred.InstallationID != nil {
					provider = cred.Provider + " (App)"
				}
			}
			t.AppendRow(table.Row{
				cred.ID,
				cred.Name,
				provider,
				state,
				cred.Available,
				repoCount,
				lastSynced,
				formatTimeAgo(cred.CreatedAt),
			})
		}
		t.Render()
		if hasGithubCredential {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"\nSee which repositories a GitHub credential can reach with 'ankra credentials repositories <name>'.")
		}
		return nil
	},
}

// ensureGitCredentialsListed guards the unfiltered listing against a backend
// that omits git credentials from it: the org's GitHub connection is the
// first prerequisite of `application add`, and it must be discoverable from
// `credentials list` without knowing to pass --provider github. The
// provider-filtered read is the exact one the application add flow already
// resolves its credential from, so whatever add can see, list shows too. It
// runs unconditionally rather than only when the unfiltered list has no
// github rows at all - a backend that omits only some of them (the
// App-installation-backed ones, say) would otherwise keep the missing ones
// invisible - and the merge deduplicates by id, so a backend that already
// includes everything is unchanged.
func ensureGitCredentialsListed(cmd *cobra.Command, credentials []client.Credential) []client.Credential {
	githubProvider := "github"
	githubCredentials, listError := apiClient.ListCredentials(&githubProvider)
	if listError != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: could not list GitHub credentials separately: %v\n", listError)
		return credentials
	}
	listed := make(map[string]bool, len(credentials))
	for _, credential := range credentials {
		listed[credential.ID] = true
	}
	for _, credential := range githubCredentials {
		if !listed[credential.ID] {
			credentials = append(credentials, credential)
		}
	}
	return credentials
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
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete credential %q? Clusters using it will fail to reconcile! [y/N]: ", credentialID),
			yes,
		); err != nil {
			return err
		}

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

		if rendered, err := renderStructured(cmd, cred); rendered || err != nil {
			return err
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

// renderRepositoryList prints one titled block of repository full names, or
// says the set is empty. Kept separate from the unknown case below because an
// empty set is an answer and an unread listing is not.
func renderRepositoryList(title string, repositories []string, emptyNote string) {
	if len(repositories) == 0 {
		fmt.Printf("  %s: %s\n", title, emptyNote)
		return
	}
	fmt.Printf("  %s (%d):\n", title, len(repositories))
	for _, repository := range repositories {
		fmt.Printf("    %s\n", repository)
	}
}

var credentialsRepositoriesCmd = &cobra.Command{
	Use:   "repositories <credential_id|name>",
	Short: "Show which repositories a credential can actually reach",
	Long: "Read the repositories a GitHub credential's installation can reach right now, " +
		"against the repositories Ankra needs from it, and report where they disagree.\n\n" +
		"The accessible list is read live from the provider rather than from the cached " +
		"count shown in `ankra credentials list`, because that count is only refreshed " +
		"while the credential reports healthy - so a credential that has stopped being " +
		"able to read its repository keeps reporting the count it had before it broke.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, resolveError := resolveCredentialID(args[0])
		if resolveError != nil {
			return resolveError
		}

		coverage, coverageError := apiClient.GetCredentialRepositories(credentialID)
		if coverageError != nil {
			return fmt.Errorf("reading credential repositories: %w", coverageError)
		}

		if rendered, err := renderStructured(cmd, coverage); rendered || err != nil {
			return err
		}

		fmt.Printf("Credential: %s (%s)\n", coverage.Credential.Name, coverage.Credential.Provider)
		if coverage.Credential.AccountLogin != nil && *coverage.Credential.AccountLogin != "" {
			fmt.Printf("  Account:  %s\n", *coverage.Credential.AccountLogin)
		}
		fmt.Printf("  Coverage: %s\n", coverage.Coverage)
		if coverage.CoverageMessage != "" {
			fmt.Printf("  %s\n", coverage.CoverageMessage)
		}
		fmt.Println()

		// A nil accessible list is the provider listing failing, which is a
		// different fact from an installation that reaches nothing. Printing
		// "none" for both would recreate the confusion this command exists to
		// end, so the unread case says so and names the error.
		if coverage.AccessibleRepositories == nil {
			fmt.Printf("  Reachable now: could not be read\n")
			if coverage.AccessibleRepositoriesError != nil && *coverage.AccessibleRepositoriesError != "" {
				fmt.Printf("    %s\n", *coverage.AccessibleRepositoriesError)
			}
		} else {
			renderRepositoryList("Reachable now", *coverage.AccessibleRepositories,
				"none - the installation reaches no repository at all")
			if !coverage.AccessibleRepositoriesComplete {
				fmt.Printf("    (listing truncated; more repositories exist than were read)\n")
			}
		}

		renderRepositoryList("Required by Ankra", coverage.RequiredRepositories,
			"none - nothing is configured to use this credential yet")
		if !coverage.RequiredRepositoriesComplete {
			fmt.Printf("    (listing truncated; more repositories require this credential)\n")
		}

		if len(coverage.UnreachableRepositories) > 0 {
			renderRepositoryList("Required but NOT reachable", coverage.UnreachableRepositories, "")
		}
		if len(coverage.UnverifiedRepositories) > 0 {
			renderRepositoryList("Required but unverified", coverage.UnverifiedRepositories, "")
		}
		return nil
	},
}

func init() {
	credentialsListCmd.Flags().String("provider", "", "Filter by provider (e.g., github)")
	credentialsDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(credentialsListCmd, credentialsGetCmd, credentialsRepositoriesCmd)
	registerSortFlags(credentialsListCmd, credentialSortFields)

	credentialsCmd.AddCommand(credentialsListCmd)
	credentialsCmd.AddCommand(credentialsValidateCmd)
	credentialsCmd.AddCommand(credentialsDeleteCmd)
	credentialsCmd.AddCommand(credentialsGetCmd)
	credentialsCmd.AddCommand(credentialsRepositoriesCmd)

	rootCmd.AddCommand(credentialsCmd)
}
