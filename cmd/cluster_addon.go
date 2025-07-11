package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var getAddonsCmd = &cobra.Command{
	Use:   "addons [addon name]",
	Short: "List addons for the active cluster; or show details for a single addon",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra select cluster' to pick one.")
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

func init() {
	getCmd.AddCommand(getAddonsCmd)
}
