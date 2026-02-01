package cmd

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var clusterManifestsCmd = &cobra.Command{
	Use:   "manifests",
	Short: "Manage manifests for the cluster",
	Long:  "Commands to list and view manifests.",
}

var clusterManifestsListCmd = &cobra.Command{
	Use:   "list [manifest name]",
	Short: "List manifests for the active cluster; or show details for a single manifest",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		manifests, err := client.ListClusterManifests(apiToken, baseURL, cluster.ID)
		if err != nil {
			fmt.Printf("Error listing manifests: %v\n", err)
			return
		}
		if len(manifests) == 0 {
			fmt.Println("No manifests found for the active cluster.")
			return
		}

		if len(args) == 1 {
			name := strings.TrimSpace(args[0])
			var found *client.ClusterManifestListItem
			for i := range manifests {
				if strings.EqualFold(manifests[i].Name, name) {
					found = &manifests[i]
					break
				}
			}
			if found == nil {
				fmt.Printf("Manifest %q not found on the active cluster.\n", name)
				return
			}

			kind := extractKindFromBase64(found.ManifestBase64)

			fmt.Println("Manifest Details:")
			fmt.Printf("  Name:        %s\n", found.Name)
			fmt.Printf("  Kind:        %s\n", kind)
			fmt.Printf("  Namespace:   %s\n", found.Namespace)
			fmt.Printf("  State:       %s\n", found.State)

			if len(found.Parents) > 0 {
				fmt.Printf("  Parents:     ")
				for i, parent := range found.Parents {
					if i > 0 {
						fmt.Print(", ")
					}
					fmt.Printf("%s (%s)", parent.Name, parent.Kind)
				}
				fmt.Println()
			} else {
				fmt.Printf("  Parents:     none\n")
			}

			if found.ManifestBase64 != "" {
				fmt.Println("\n  Manifest Content:")
				decoded, err := base64.StdEncoding.DecodeString(found.ManifestBase64)
				if err != nil {
					fmt.Printf("    Error decoding manifest: %v\n", err)
				} else {
					lines := strings.Split(string(decoded), "\n")
					for _, line := range lines {
						fmt.Printf("    %s\n", line)
					}
				}
			}
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{
			"Name", "Kind", "Namespace", "State", "Parents",
		})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 25},
			{Number: 2, WidthMin: 15},
			{Number: 3, WidthMin: 15},
			{Number: 4, WidthMin: 12},
			{Number: 5, WidthMin: 20},
		})

		for _, m := range manifests {
			kind := extractKindFromBase64(m.ManifestBase64)

			state := m.State
			switch strings.ToLower(state) {
			case "up":
				state = "✓ " + state
			case "updating":
				state = "⟳ " + state
			case "failed":
				state = "✗ " + state
			}

			parents := "none"
			if len(m.Parents) > 0 {
				var parentList []string
				for _, parent := range m.Parents {
					parentList = append(parentList, fmt.Sprintf("%s (%s)", parent.Name, parent.Kind))
				}
				parents = strings.Join(parentList, ", ")
				if len(parents) > 30 {
					parents = parents[:27] + "..."
				}
			}

			t.AppendRow(table.Row{
				m.Name,
				kind,
				m.Namespace,
				state,
				parents,
			})
		}
		t.Render()
	},
}

func init() {
	clusterManifestsCmd.AddCommand(clusterManifestsListCmd)
	clusterCmd.AddCommand(clusterManifestsCmd)
}
