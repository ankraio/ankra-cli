package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// SelectedOrganisation holds the currently selected organisation
type SelectedOrganisation struct {
	OrganisationID string  `json:"organisation_id"`
	Name           *string `json:"name"`
	Role           *string `json:"role"`
}

var orgCmd = &cobra.Command{
	Use:     "org",
	Aliases: []string{"organisation", "organization"},
	Short:   "Manage organisations",
	Long:    "Commands to list, switch, create, and manage organisations.",
}

var orgListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all organisations you belong to",
	Run: func(cmd *cobra.Command, args []string) {
		orgs, err := apiClient.ListOrganisations()
		if err != nil {
			fmt.Printf("Error listing organisations: %v\n", err)
			return
		}

		if len(orgs) == 0 {
			fmt.Println("No organisations found.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Role", "Status", "Current"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 10},
			{Number: 5, WidthMin: 8},
		})

		for _, org := range orgs {
			name := ""
			if org.Name != nil {
				name = *org.Name
			}
			role := ""
			if org.Role != nil {
				role = *org.Role
			}
			status := ""
			if org.Status != nil {
				status = *org.Status
			}
			current := ""
			if org.UserCurrent {
				current = text.FgGreen.Sprint("✓")
			}

			t.AppendRow(table.Row{
				org.OrganisationID,
				name,
				role,
				status,
				current,
			})
		}
		t.Render()
	},
}

var orgSwitchCmd = &cobra.Command{
	Use:   "switch <org_id>",
	Short: "Switch to a different organisation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		orgID := args[0]

		orgs, err := apiClient.ListOrganisations()
		if err != nil {
			fmt.Printf("Error listing organisations: %v\n", err)
			return
		}

		var targetOrg *client.OrganisationSummary
		for i := range orgs {
			if orgs[i].OrganisationID == orgID {
				targetOrg = &orgs[i]
				break
			}
		}

		if targetOrg == nil {
			fmt.Printf("Organisation %s not found or you don't have access.\n", orgID)
			return
		}

		resp, err := apiClient.SwitchOrganisation(orgID)
		if err != nil {
			fmt.Printf("Error switching organisation: %v\n", err)
			return
		}

		// Save locally
		selected := SelectedOrganisation{
			OrganisationID: targetOrg.OrganisationID,
			Name:           targetOrg.Name,
			Role:           targetOrg.Role,
		}
		if err := saveSelectedOrganisation(selected); err != nil {
			fmt.Printf("Warning: switched server-side but failed to save locally: %v\n", err)
		}

		name := ""
		if targetOrg.Name != nil {
			name = *targetOrg.Name
		}
		fmt.Printf("Switched to organisation: %s (%s)\n", name, orgID)
		if resp.Message != "" {
			fmt.Printf("Message: %s\n", resp.Message)
		}
	},
}

var orgCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the currently selected organisation",
	Run: func(cmd *cobra.Command, args []string) {
		org, source, err := resolveTargetOrganisation(cmd)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}

		name := ""
		if org.Name != nil {
			name = *org.Name
		}
		role := ""
		if org.Role != nil {
			role = *org.Role
		}
		fmt.Printf("Current organisation (%s):\n", source)
		fmt.Printf("  ID:   %s\n", org.OrganisationID)
		fmt.Printf("  Name: %s\n", name)
		fmt.Printf("  Role: %s\n", role)
	},
}

var orgCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new organisation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		country, _ := cmd.Flags().GetString("country")

		var countryPtr *string
		if country != "" {
			countryPtr = &country
		}

		resp, err := apiClient.CreateOrganisation(name, countryPtr)
		if err != nil {
			fmt.Printf("Error creating organisation: %v\n", err)
			return
		}

		fmt.Printf("Organisation created successfully!\n")
		fmt.Printf("  ID:      %s\n", resp.OrganisationID)
		fmt.Printf("  Message: %s\n", resp.Message)
		fmt.Println("\nTo switch to this organisation, run:")
		fmt.Printf("  ankra org switch %s\n", resp.OrganisationID)
	},
}

var orgMembersCmd = &cobra.Command{
	Use:   "members [org_id]",
	Short: "List members of an organisation",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var orgID string

		if len(args) > 0 {
			orgID = args[0]
		} else {
			resolved, err := resolveTargetOrganisationID(cmd)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				return
			}
			orgID = resolved
		}

		if orgID == "" {
			fmt.Println("No organisation specified. Use 'ankra org members <org_id>' or select an organisation first.")
			return
		}

		org, err := apiClient.GetOrganisation(orgID)
		if err != nil {
			fmt.Printf("Error fetching organisation: %v\n", err)
			return
		}

		name := ""
		if org.Name != nil {
			name = *org.Name
		}
		fmt.Printf("Members of %s (%s):\n\n", name, org.OrganisationID)

		if len(org.Members) == 0 {
			fmt.Println("No members found.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Email", "Name", "Role", "Joined"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 30},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 15},
		})

		for _, member := range org.Members {
			memberName := ""
			if member.Name != nil {
				memberName = *member.Name
			}
			t.AppendRow(table.Row{
				member.Email,
				memberName,
				member.Role,
				formatTimeAgo(member.JoinedAt),
			})
		}
		t.Render()
	},
}

func selectedOrganisationFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ankra")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "organisation.json"), nil
}

func saveSelectedOrganisation(org SelectedOrganisation) error {
	path, err := selectedOrganisationFile()
	if err != nil {
		return err
	}
	data, err := json.Marshal(org)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func loadSelectedOrganisation() (SelectedOrganisation, error) {
	var org SelectedOrganisation
	path, err := selectedOrganisationFile()
	if err != nil {
		return org, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return org, err
	}
	err = json.Unmarshal(data, &org)
	return org, err
}

func resolveOrganisationID() (string, error) {
	local, err := loadSelectedOrganisation()
	if err == nil && local.OrganisationID != "" {
		return local.OrganisationID, nil
	}

	orgs, err := apiClient.ListOrganisations()
	if err != nil {
		return "", fmt.Errorf("fetching organisations: %w", err)
	}
	for _, org := range orgs {
		if org.UserCurrent {
			return org.OrganisationID, nil
		}
	}
	return "", fmt.Errorf("no organisation selected")
}

// organisationOverrideValue returns the explicit organisation requested via
// the global `--org` flag, falling back to the ANKRA_ORG environment variable.
// An empty string means no override was supplied.
func organisationOverrideValue(cmd *cobra.Command) string {
	if flag := cmd.Root().PersistentFlags().Lookup("org"); flag != nil {
		if value := strings.TrimSpace(flag.Value.String()); value != "" {
			return value
		}
	}
	return strings.TrimSpace(os.Getenv(envAnkraOrg))
}

// resolveTargetOrganisation resolves which organisation a command should act
// on and the source of that decision. Precedence is: the explicit `--org`
// flag / ANKRA_ORG override, then the saved local selection (but only when it
// still matches an organisation the user belongs to, so a stale selection is
// ignored rather than sent to the API), then the server-side current
// organisation.
func resolveTargetOrganisation(cmd *cobra.Command) (client.OrganisationSummary, string, error) {
	orgs, err := apiClient.ListOrganisations()
	if err != nil {
		return client.OrganisationSummary{}, "", fmt.Errorf("fetching organisations: %w", err)
	}

	if override := organisationOverrideValue(cmd); override != "" {
		orgID, resolveErr := resolveOrgFlagToID(override)
		if resolveErr != nil {
			return client.OrganisationSummary{}, "", resolveErr
		}
		for _, org := range orgs {
			if org.OrganisationID == orgID {
				return org, "override", nil
			}
		}
		return client.OrganisationSummary{}, "", fmt.Errorf("organisation %q not found", override)
	}

	if local, loadErr := loadSelectedOrganisation(); loadErr == nil && local.OrganisationID != "" {
		for _, org := range orgs {
			if org.OrganisationID == local.OrganisationID {
				return org, "local", nil
			}
		}
	}

	for _, org := range orgs {
		if org.UserCurrent {
			return org, "server", nil
		}
	}
	return client.OrganisationSummary{}, "", fmt.Errorf("no organisation selected: run `ankra org switch <org_id>`")
}

func resolveTargetOrganisationID(cmd *cobra.Command) (string, error) {
	org, _, err := resolveTargetOrganisation(cmd)
	if err != nil {
		return "", err
	}
	return org.OrganisationID, nil
}

// resolveOrgFlagToID maps the global `--org` value (an organisation name or
// ID) to an organisation ID, validating that the authenticated user belongs
// to it. An exact ID match wins; otherwise a case-insensitive name match is
// used. Ambiguous or unknown names return an actionable error listing the
// organisations available to the user.
func resolveOrgFlagToID(value string) (string, error) {
	orgs, err := apiClient.ListOrganisations()
	if err != nil {
		return "", fmt.Errorf("resolving --org %q: %w", value, err)
	}

	for _, org := range orgs {
		if org.OrganisationID == value {
			return org.OrganisationID, nil
		}
	}

	var matches []client.OrganisationSummary
	for _, org := range orgs {
		if org.Name != nil && strings.EqualFold(strings.TrimSpace(*org.Name), value) {
			matches = append(matches, org)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].OrganisationID, nil
	case 0:
		return "", fmt.Errorf("organisation %q not found. %s", value, availableOrganisationsHint(orgs))
	default:
		return "", fmt.Errorf("multiple organisations are named %q; pass the organisation ID instead", value)
	}
}

func availableOrganisationsHint(orgs []client.OrganisationSummary) string {
	if len(orgs) == 0 {
		return "You do not belong to any organisations."
	}
	var names []string
	for _, org := range orgs {
		if org.Name != nil && *org.Name != "" {
			names = append(names, fmt.Sprintf("%s (%s)", *org.Name, org.OrganisationID))
		} else {
			names = append(names, org.OrganisationID)
		}
	}
	return "Available organisations: " + strings.Join(names, ", ")
}

var orgInviteCmd = &cobra.Command{
	Use:   "invite <email>",
	Short: "Invite a user to the current organisation",
	Long: `Invite a user by email to the current organisation.

Valid roles: member (default), admin, read-only

Examples:
  ankra org invite user@example.com
  ankra org invite user@example.com --role admin`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		email := args[0]
		role, _ := cmd.Flags().GetString("role")

		orgID, err := resolveOrganisationID()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			fmt.Println("Select an organisation first with 'ankra org switch <org_id>'")
			return
		}

		result, err := apiClient.InviteUserToOrganisation(client.InviteUserRequest{
			OrganisationID: orgID,
			InviteeEmail:   email,
			Role:           role,
		})
		if err != nil {
			fmt.Printf("Error inviting user: %v\n", err)
			return
		}

		if result.Success {
			fmt.Printf("Invitation sent to %s (role: %s)\n", email, role)
		}
		if result.Message != "" {
			fmt.Printf("Message: %s\n", result.Message)
		}
	},
}

var orgRemoveCmd = &cobra.Command{
	Use:   "remove <user_id>",
	Short: "Remove a user from the current organisation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		userID := args[0]
		force, _ := cmd.Flags().GetBool("force")

		orgID, err := resolveOrganisationID()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			fmt.Println("Select an organisation first with 'ankra org switch <org_id>'")
			return
		}

		if !force {
			prompt := promptui.Prompt{
				Label:     fmt.Sprintf("Remove user %s from the organisation", userID),
				IsConfirm: true,
			}
			_, err := prompt.Run()
			if err != nil {
				fmt.Println("Cancelled.")
				return
			}
		}

		result, err := apiClient.RemoveUserFromOrganisation(client.RemoveUserRequest{
			UserID:         userID,
			OrganisationID: orgID,
		})
		if err != nil {
			fmt.Printf("Error removing user: %v\n", err)
			return
		}

		if result.Success {
			fmt.Printf("User %s removed from the organisation.\n", userID)
		}
		if result.Message != "" {
			fmt.Printf("Message: %s\n", result.Message)
		}
	},
}

func init() {
	orgCreateCmd.Flags().String("country", "", "Country code for the organisation")

	orgInviteCmd.Flags().String("role", "member", "Role for the invited user (member, admin, read-only)")
	orgRemoveCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")

	orgCmd.AddCommand(orgListCmd)
	orgCmd.AddCommand(orgSwitchCmd)
	orgCmd.AddCommand(orgCurrentCmd)
	orgCmd.AddCommand(orgCreateCmd)
	orgCmd.AddCommand(orgMembersCmd)
	orgCmd.AddCommand(orgInviteCmd)
	orgCmd.AddCommand(orgRemoveCmd)

	rootCmd.AddCommand(orgCmd)
}
