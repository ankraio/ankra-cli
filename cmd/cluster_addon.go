package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var clusterAddonsCmd = &cobra.Command{
	Use:   "addons",
	Short: "Manage addons for clusters",
	Long:  "Commands to list, manage settings, and uninstall addons.",
}

var clusterAddonsListCmd = &cobra.Command{
	Use:   "list [addon name]",
	Short: "List addons for the active cluster; or show details for a single addon",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		addons, err := client.ListClusterAddons(apiToken, baseURL, cluster.ID)
		if err != nil {
			fmt.Printf("Error listing addons: %v\n", err)
			return
		}
		if len(addons) == 0 {
			fmt.Println("No addons found for the active cluster.")
			return
		}

		if len(args) == 1 {
			name := strings.TrimSpace(args[0])
			var found *client.ClusterAddonListItem
			for i := range addons {
				if addons[i].Name == name {
					found = &addons[i]
					break
				}
			}
			if found == nil {
				fmt.Printf("Addon %q not found on the active cluster.\n", name)
				return
			}

			fmt.Println("Addon Details:")
			fmt.Printf("  ID:              %s\n", found.ID)
			fmt.Printf("  Name:            %s\n", found.Name)
			fmt.Printf("  Chart:           %s\n", found.ChartName)
			fmt.Printf("  Version:         %s\n", found.ChartVersion)
			fmt.Printf("  Repository:      %s\n", found.RepositoryURL)
			fmt.Printf("  Namespace:       %s\n", found.Namespace)
			fmt.Printf("  Through Ankra:   %t\n", found.ThroughAnkra)
			if found.Health != nil {
				fmt.Printf("  Health:          %s\n", *found.Health)
			}
			if found.State != nil {
				fmt.Printf("  State:           %s\n", *found.State)
			}
			fmt.Printf("  Created:         %s\n", formatTimeAgo(found.CreatedAt.Format(time.RFC3339)))
			fmt.Printf("  Updated:         %s\n", formatTimeAgo(found.UpdatedAt.Format(time.RFC3339)))
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{
			"Name", "Chart", "Version", "Namespace", "Health", "Ankra?", "Created At", "Updated At", "State",
		})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 20},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 15},
			{Number: 5, WidthMin: 10},
			{Number: 6, WidthMin: 8},
			{Number: 7, WidthMin: 15},
			{Number: 8, WidthMin: 15},
			{Number: 9, WidthMin: 10},
		})

		for _, a := range addons {
			health := ""
			if a.Health != nil {
				health = *a.Health
			}
			state := ""
			if a.State != nil {
				state = *a.State
			}
			t.AppendRow(table.Row{
				a.Name,
				a.ChartName,
				a.ChartVersion,
				a.Namespace,
				health,
				a.ThroughAnkra,
				formatTimeAgo(a.CreatedAt.Format(time.RFC3339)),
				formatTimeAgo(a.UpdatedAt.Format(time.RFC3339)),
				state,
			})
		}
		t.Render()
	},
}

var clusterAddonsAvailableCmd = &cobra.Command{
	Use:   "available",
	Short: "List addons available for installation",
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		addons, err := client.ListAvailableAddons(apiToken, baseURL, cluster.ID)
		if err != nil {
			fmt.Printf("Error listing available addons: %v\n", err)
			return
		}

		if len(addons) == 0 {
			fmt.Println("No addons available for installation.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Chart", "Version", "Category"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 20},
			{Number: 4, WidthMin: 10},
			{Number: 5, WidthMin: 15},
		})

		for _, a := range addons {
			category := ""
			if a.Category != nil {
				category = *a.Category
			}
			t.AppendRow(table.Row{
				a.ID,
				a.Name,
				a.ChartName,
				a.Version,
				category,
			})
		}
		t.Render()
	},
}

var clusterAddonsSettingsCmd = &cobra.Command{
	Use:   "settings <addon_name>",
	Short: "Get settings for an addon",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		addonName := args[0]

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		settings, err := client.GetAddonSettings(apiToken, baseURL, cluster.ID, addonName)
		if err != nil {
			fmt.Printf("Error getting addon settings: %v\n", err)
			return
		}

		fmt.Printf("Settings for addon '%s':\n\n", settings.AddonName)

		// Pretty print as JSON
		jsonData, err := json.MarshalIndent(settings.Settings, "", "  ")
		if err != nil {
			fmt.Printf("Error formatting settings: %v\n", err)
			return
		}
		fmt.Println(string(jsonData))
	},
}

var clusterAddonsUninstallCmd = &cobra.Command{
	Use:   "uninstall <addon_name>",
	Short: "Uninstall an addon from the cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		addonName := args[0]
		deletePermanently, _ := cmd.Flags().GetBool("delete")

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		// First find the addon to get its resource ID
		addon, err := client.GetAddonByName(apiToken, baseURL, cluster.ID, addonName)
		if err != nil {
			fmt.Printf("Error finding addon: %v\n", err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := client.UninstallAddon(ctx, apiToken, baseURL, cluster.ID, addon.ID, deletePermanently)
		if err != nil {
			fmt.Printf("Error uninstalling addon: %v\n", err)
			return
		}

		if result.Success {
			if deletePermanently {
				fmt.Printf("Addon '%s' uninstalled and deleted successfully!\n", addonName)
			} else {
				fmt.Printf("Addon '%s' uninstalled successfully!\n", addonName)
			}
		}
	},
}

func init() {
	clusterAddonsUninstallCmd.Flags().Bool("delete", false, "Also delete the addon permanently")

	clusterAddonsCmd.AddCommand(clusterAddonsListCmd)
	clusterAddonsCmd.AddCommand(clusterAddonsAvailableCmd)
	clusterAddonsCmd.AddCommand(clusterAddonsSettingsCmd)
	clusterAddonsCmd.AddCommand(clusterAddonsUninstallCmd)

	clusterCmd.AddCommand(clusterAddonsCmd)
}
