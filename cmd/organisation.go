package cmd

import (
	"encoding/json"
	"errors"
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
	Slug           *string `json:"slug,omitempty"`
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
	RunE: func(cmd *cobra.Command, args []string) error {
		orgs, err := apiClient.ListOrganisations()
		if err != nil {
			return fmt.Errorf("listing organisations: %w", err)
		}

		if orgs == nil {
			orgs = []client.OrganisationSummary{}
		}
		if rendered, err := renderStructured(cmd, orgs); rendered || err != nil {
			return err
		}

		if len(orgs) == 0 {
			fmt.Println("No organisations found.")
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Slug", "Role", "Status", "Current"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 20},
			{Number: 4, WidthMin: 10},
			{Number: 5, WidthMin: 10},
			{Number: 6, WidthMin: 8},
		})

		for _, org := range orgs {
			name := ""
			if org.Name != nil {
				name = *org.Name
			}
			slug := ""
			if org.Slug != nil {
				slug = *org.Slug
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
				slug,
				role,
				status,
				current,
			})
		}
		t.Render()
		return nil
	},
}

var orgSwitchCmd = &cobra.Command{
	Use:   "switch <organisation>",
	Short: "Switch to a different organisation by slug, name, or ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reference := args[0]

		orgs, err := apiClient.ListOrganisations()
		if err != nil {
			return fmt.Errorf("listing organisations: %w", err)
		}

		targetOrg, err := resolveOrganisationReference(orgs, reference)
		if err != nil {
			// A genuinely unknown organisation is a missing target (exit 3);
			// an ambiguous reference is a fixable invocation (exit 2). Both
			// keep their human-readable message.
			if errors.Is(err, errOrganisationNotFound) {
				return withExitCode(exitNotFound, err)
			}
			return withExitCode(exitUsage, err)
		}

		resp, err := apiClient.SwitchOrganisation(targetOrg.OrganisationID)
		if err != nil {
			return fmt.Errorf("switching organisation: %w", err)
		}

		// Save locally
		selected := SelectedOrganisation{
			OrganisationID: targetOrg.OrganisationID,
			Name:           targetOrg.Name,
			Slug:           targetOrg.Slug,
			Role:           targetOrg.Role,
		}
		if err := saveSelectedOrganisation(selected); err != nil {
			fmt.Printf("Warning: switched server-side but failed to save locally: %v\n", err)
		}

		name := ""
		if targetOrg.Name != nil {
			name = *targetOrg.Name
		}
		fmt.Printf("Switched to organisation: %s (%s)\n", name, targetOrg.OrganisationID)
		if resp.Message != "" {
			fmt.Printf("Message: %s\n", resp.Message)
		}
		return nil
	},
}

type currentOrganisationOutput struct {
	Organisation client.OrganisationSummary `json:"organisation" yaml:"organisation"`
	Source       string                     `json:"source" yaml:"source"`
}

var orgCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the currently selected organisation",
	RunE: func(cmd *cobra.Command, args []string) error {
		org, source, err := resolveTargetOrganisation(cmd)
		if err != nil {
			return err
		}

		if rendered, err := renderStructured(cmd, currentOrganisationOutput{Organisation: org, Source: source}); rendered || err != nil {
			return err
		}

		name := ""
		if org.Name != nil {
			name = *org.Name
		}
		slug := ""
		if org.Slug != nil {
			slug = *org.Slug
		}
		role := ""
		if org.Role != nil {
			role = *org.Role
		}
		fmt.Printf("Current organisation (%s):\n", source)
		fmt.Printf("  ID:   %s\n", org.OrganisationID)
		fmt.Printf("  Name: %s\n", name)
		if slug != "" {
			fmt.Printf("  Slug: %s\n", slug)
		}
		fmt.Printf("  Role: %s\n", role)
		return nil
	},
}

var orgCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new organisation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		country, _ := cmd.Flags().GetString("country")

		var countryPtr *string
		if country != "" {
			countryPtr = &country
		}

		resp, err := apiClient.CreateOrganisation(name, countryPtr)
		if err != nil {
			return fmt.Errorf("creating organisation: %w", err)
		}

		if rendered, err := renderStructured(cmd, resp); rendered || err != nil {
			return err
		}

		fmt.Printf("Organisation created successfully!\n")
		fmt.Printf("  ID:      %s\n", resp.OrganisationID)
		if resp.Slug != "" {
			fmt.Printf("  Slug:    %s\n", resp.Slug)
		}
		fmt.Printf("  Message: %s\n", resp.Message)
		fmt.Println("\nTo switch to this organisation, run:")
		switchReference := resp.OrganisationID
		if resp.Slug != "" {
			switchReference = resp.Slug
		}
		fmt.Printf("  ankra org switch %s\n", switchReference)
		return nil
	},
}

var orgMembersCmd = &cobra.Command{
	Use:   "members [org_id]",
	Short: "List members of an organisation",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var orgID string

		if len(args) > 0 {
			orgID = args[0]
		} else {
			resolved, err := resolveTargetOrganisationID(cmd)
			if err != nil {
				return err
			}
			orgID = resolved
		}

		if orgID == "" {
			return errors.New("no organisation specified. Use 'ankra org members <org_id>' or select an organisation first")
		}

		org, err := apiClient.GetOrganisation(orgID)
		if err != nil {
			return fmt.Errorf("fetching organisation: %w", err)
		}

		if rendered, err := renderStructured(cmd, org); rendered || err != nil {
			return err
		}

		name := ""
		if org.Name != nil {
			name = *org.Name
		}
		fmt.Printf("Members of %s (%s):\n\n", name, org.OrganisationID)

		if len(org.Members) == 0 {
			fmt.Println("No members found.")
			return nil
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
		return nil
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

// resolveOrganisationReference maps a user-supplied organisation reference (an
// ID, a slug, or a name) to the matching organisation the user belongs to.
// Resolution order is exact ID, then exact slug, then case-insensitive name.
// Slug and name comparisons are case-insensitive. Ambiguous or unknown
// references return an actionable error listing the organisations available to
// the user.
// errOrganisationNotFound marks the case where a reference matches no
// organisation the user belongs to, so `org switch` can exit exitNotFound(3)
// while an ambiguous reference (the org exists, but the name/slug is shared)
// stays a fixable usage error (exit 2).
var errOrganisationNotFound = errors.New("organisation not found")

func resolveOrganisationReference(orgs []client.OrganisationSummary, reference string) (*client.OrganisationSummary, error) {
	trimmedReference := strings.TrimSpace(reference)

	for index := range orgs {
		if orgs[index].OrganisationID == trimmedReference {
			return &orgs[index], nil
		}
	}

	var slugMatches []*client.OrganisationSummary
	for index := range orgs {
		if orgs[index].Slug != nil && strings.EqualFold(strings.TrimSpace(*orgs[index].Slug), trimmedReference) {
			slugMatches = append(slugMatches, &orgs[index])
		}
	}
	if len(slugMatches) == 1 {
		return slugMatches[0], nil
	}
	if len(slugMatches) > 1 {
		return nil, fmt.Errorf("multiple organisations have slug %q; pass the organisation ID instead", reference)
	}

	var nameMatches []*client.OrganisationSummary
	for index := range orgs {
		if orgs[index].Name != nil && strings.EqualFold(strings.TrimSpace(*orgs[index].Name), trimmedReference) {
			nameMatches = append(nameMatches, &orgs[index])
		}
	}
	switch len(nameMatches) {
	case 1:
		return nameMatches[0], nil
	case 0:
		return nil, fmt.Errorf("%w: %q. %s", errOrganisationNotFound, reference, availableOrganisationsHint(orgs))
	default:
		return nil, fmt.Errorf("multiple organisations are named %q; pass the organisation ID instead", reference)
	}
}

// resolveOrgFlagToID maps the global `--org` value (an organisation slug, name,
// or ID) to an organisation ID, validating that the authenticated user belongs
// to it.
func resolveOrgFlagToID(value string) (string, error) {
	orgs, err := apiClient.ListOrganisations()
	if err != nil {
		return "", fmt.Errorf("resolving --org %q: %w", value, err)
	}

	org, err := resolveOrganisationReference(orgs, value)
	if err != nil {
		return "", err
	}
	return org.OrganisationID, nil
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

Valid roles: owner, admin, operator, member (default), viewer, read-only.
owner/operator alias onto admin/member on the invite until the RBAC
assignments API ships; run 'ankra org roles' for the full list.

Examples:
  ankra org invite user@example.com
  ankra org invite user@example.com --role admin
  ankra org invite user@example.com --role viewer`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		email := args[0]
		role, _ := cmd.Flags().GetString("role")
		if !isValidAssignableRole(role) {
			return withExitCode(exitUsage, fmt.Errorf(
				"invalid role %q; valid roles: %s", role, strings.Join(assignableRoles, ", ")))
		}

		orgID, err := resolveOrganisationID()
		if err != nil {
			return fmt.Errorf("%w\nSelect an organisation first with 'ankra org switch <org_id>'", err)
		}

		result, err := apiClient.InviteUserToOrganisation(client.InviteUserRequest{
			OrganisationID: orgID,
			InviteeEmail:   email,
			Role:           toLegacyWireRole(role),
		})
		if err != nil {
			return fmt.Errorf("inviting user: %w", err)
		}

		if result.Success {
			fmt.Printf("Invitation sent to %s (role: %s)\n", email, role)
		}
		if result.Message != "" {
			fmt.Printf("Message: %s\n", result.Message)
		}
		return nil
	},
}

var orgRolesCmd = &cobra.Command{
	Use:   "roles",
	Short: "List the assignable organisation roles",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		for _, role := range assignableRoles {
			fmt.Printf("  %-10s %s\n", role, roleDescriptions[role])
		}
		return nil
	},
}

var orgRemoveCmd = &cobra.Command{
	Use:   "remove <user_id>",
	Short: "Remove a user from the current organisation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		userID := args[0]
		force, _ := cmd.Flags().GetBool("force")

		orgID, err := resolveOrganisationID()
		if err != nil {
			return fmt.Errorf("%w\nSelect an organisation first with 'ankra org switch <org_id>'", err)
		}

		if !force {
			prompt := promptui.Prompt{
				Label:     fmt.Sprintf("Remove user %s from the organisation", userID),
				IsConfirm: true,
			}
			if _, err := prompt.Run(); err != nil {
				return errCancelled
			}
		}

		result, err := apiClient.RemoveUserFromOrganisation(client.RemoveUserRequest{
			UserID:         userID,
			OrganisationID: orgID,
		})
		if err != nil {
			return fmt.Errorf("removing user: %w", err)
		}

		if result.Success {
			fmt.Printf("User %s removed from the organisation.\n", userID)
		}
		if result.Message != "" {
			fmt.Printf("Message: %s\n", result.Message)
		}
		return nil
	},
}

func init() {
	orgCreateCmd.Flags().String("country", "", "Country code for the organisation")

	orgInviteCmd.Flags().String("role", "member", "Role for the invited user (owner, admin, operator, member, viewer, read-only)")
	orgRemoveCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")

	registerStructuredOutputFlags(orgListCmd, orgCurrentCmd, orgCreateCmd, orgMembersCmd)

	orgCmd.AddCommand(orgListCmd)
	orgCmd.AddCommand(orgSwitchCmd)
	orgCmd.AddCommand(orgCurrentCmd)
	orgCmd.AddCommand(orgCreateCmd)
	orgCmd.AddCommand(orgMembersCmd)
	orgCmd.AddCommand(orgInviteCmd)
	orgCmd.AddCommand(orgRolesCmd)
	orgCmd.AddCommand(orgRemoveCmd)

	rootCmd.AddCommand(orgCmd)
}
