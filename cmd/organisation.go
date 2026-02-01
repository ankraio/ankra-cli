package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
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
		orgs, err := client.ListOrganisations(apiToken, baseURL)
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

		// First, verify the org exists and user has access
		orgs, err := client.ListOrganisations(apiToken, baseURL)
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

		// Switch on the server
		resp, err := client.SwitchOrganisation(apiToken, baseURL, orgID)
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
		// First check local selection
		local, err := loadSelectedOrganisation()
		if err == nil && local.OrganisationID != "" {
			name := ""
			if local.Name != nil {
				name = *local.Name
			}
			role := ""
			if local.Role != nil {
				role = *local.Role
			}
			fmt.Printf("Current organisation (local):\n")
			fmt.Printf("  ID:   %s\n", local.OrganisationID)
			fmt.Printf("  Name: %s\n", name)
			fmt.Printf("  Role: %s\n", role)
			return
		}

		// Fall back to checking server for current org
		orgs, err := client.ListOrganisations(apiToken, baseURL)
		if err != nil {
			fmt.Printf("Error fetching organisations: %v\n", err)
			return
		}

		for _, org := range orgs {
			if org.UserCurrent {
				name := ""
				if org.Name != nil {
					name = *org.Name
				}
				role := ""
				if org.Role != nil {
					role = *org.Role
				}
				fmt.Printf("Current organisation (server):\n")
				fmt.Printf("  ID:   %s\n", org.OrganisationID)
				fmt.Printf("  Name: %s\n", name)
				fmt.Printf("  Role: %s\n", role)
				return
			}
		}

		fmt.Println("No organisation currently selected.")
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

		resp, err := client.CreateOrganisation(apiToken, baseURL, name, countryPtr)
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
			// Try to get from local selection or current org
			local, err := loadSelectedOrganisation()
			if err == nil && local.OrganisationID != "" {
				orgID = local.OrganisationID
			} else {
				// Fall back to server's current org
				orgs, err := client.ListOrganisations(apiToken, baseURL)
				if err != nil {
					fmt.Printf("Error fetching organisations: %v\n", err)
					return
				}
				for _, org := range orgs {
					if org.UserCurrent {
						orgID = org.OrganisationID
						break
					}
				}
			}
		}

		if orgID == "" {
			fmt.Println("No organisation specified. Use 'ankra org members <org_id>' or select an organisation first.")
			return
		}

		org, err := client.GetOrganisation(apiToken, baseURL, orgID)
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

func init() {
	orgCreateCmd.Flags().String("country", "", "Country code for the organisation")

	orgCmd.AddCommand(orgListCmd)
	orgCmd.AddCommand(orgSwitchCmd)
	orgCmd.AddCommand(orgCurrentCmd)
	orgCmd.AddCommand(orgCreateCmd)
	orgCmd.AddCommand(orgMembersCmd)

	rootCmd.AddCommand(orgCmd)
}
