package cmd

import (
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var getStacksCmd = &cobra.Command{
	Use:   "stacks [stack name]",
	Short: "List stacks for the active cluster; or show details for a single stack",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra select cluster' to pick one.")
			return
		}

		stacks, err := client.ListClusterStacks(apiToken, baseURL, cluster.ID)
		if err != nil {
			fmt.Printf("Error listing stacks: %v\n", err)
			return
		}
		if len(stacks) == 0 {
			fmt.Println("No stacks found for the active cluster.")
			return
		}

		if len(args) == 1 {
			name := strings.TrimSpace(args[0])
			var found *client.ClusterStackListItem
			for i := range stacks {
				if strings.EqualFold(stacks[i].Name, name) {
					found = &stacks[i]
					break
				}
			}
			if found == nil {
				fmt.Printf("Stack %q not found on the active cluster.\n", name)
				return
			}

			fmt.Println("Stack Details:")
			fmt.Printf("  Name:        %s\n", found.Name)
			fmt.Printf("  Description: %s\n", found.Description)
			fmt.Printf("  State:       %s\n", found.State)
			fmt.Printf("  Manifests:   %d\n", len(found.Manifests))
			fmt.Printf("  Addons:      %d\n", len(found.Addons))

			if len(found.Manifests) > 0 {
				fmt.Println("\n  Manifests:")
				for _, manifest := range found.Manifests {
					stateIcon := "●"
					switch strings.ToLower(manifest.State) {
					case "up":
						stateIcon = "✓"
					case "updating":
						stateIcon = "⟳"
					case "failed":
						stateIcon = "✗"
					}

					kind := extractKindFromBase64(manifest.ManifestBase64)

					fmt.Printf("    %s %s\n", stateIcon, manifest.Name)
					fmt.Printf("      ├─ kind: %s\n", kind)
					fmt.Printf("      ├─ namespace: %s\n", manifest.Namespace)
					fmt.Printf("      ├─ state: %s\n", manifest.State)

					if len(manifest.Parents) > 0 {
						fmt.Printf("      └─ parents: ")
						for i, parent := range manifest.Parents {
							if i > 0 {
								fmt.Print(", ")
							}
							fmt.Printf("%s (%s)", parent.Name, parent.Kind)
						}
						fmt.Println()
					} else {
						fmt.Printf("      └─ parents: none\n")
					}
					fmt.Println()
				}
			}

			if len(found.Addons) > 0 {
				fmt.Println("  Addons:")
				for _, addon := range found.Addons {
					stateIcon := "●"
					switch strings.ToLower(addon.State) {
					case "up":
						stateIcon = "✓"
					case "updating":
						stateIcon = "⟳"
					case "failed":
						stateIcon = "✗"
					}

					fmt.Printf("    %s %s\n", stateIcon, addon.Name)
					fmt.Printf("      ├─ chart: %s:%s\n", addon.ChartName, addon.ChartVersion)
					fmt.Printf("      ├─ namespace: %s\n", addon.Namespace)
					fmt.Printf("      ├─ state: %s\n", addon.State)

					if len(addon.Parents) > 0 {
						fmt.Printf("      └─ parents: ")
						for i, parent := range addon.Parents {
							if i > 0 {
								fmt.Print(", ")
							}
							fmt.Printf("%s (%s)", parent.Name, parent.Kind)
						}
						fmt.Println()
					} else {
						fmt.Printf("      └─ parents: none\n")
					}
					fmt.Println()
				}
			}
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{
			"Name", "Description", "State", "Manifests", "Addons",
		})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 20},
			{Number: 2, WidthMin: 30},
			{Number: 3, WidthMin: 12},
			{Number: 4, WidthMin: 10},
			{Number: 5, WidthMin: 10},
		})

		for _, stack := range stacks {
			description := stack.Description
			if description == "" {
				description = "-"
			}

			state := stack.State
			switch strings.ToLower(state) {
			case "up":
				state = "✓ " + state
			case "updating":
				state = "⟳ " + state
			case "failed":
				state = "✗ " + state
			}

			t.AppendRow(table.Row{
				stack.Name,
				description,
				state,
				len(stack.Manifests),
				len(stack.Addons),
			})
		}
		t.Render()
	},
}

func init() {
	getCmd.AddCommand(getStacksCmd)
}
