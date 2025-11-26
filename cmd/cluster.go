package cmd

import (
	"fmt"
	"os"
	"time"

	"ankra/internal/client"

	"github.com/dustin/go-humanize"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var getClustersCmd = &cobra.Command{
	Use:     "clusters [name]",
	Aliases: []string{"cluster"},
	Short:   "List clusters or get detailed information about a specific cluster by name",
	Args:    cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			response, err := client.ListClusters(apiToken, baseURL, 0, 0)
			if err != nil {
				fmt.Printf("Error listing clusters: %v\n", err)
				return
			}
			if len(response.Result) == 0 {
				fmt.Println("No clusters found.")
				return
			}
			t := table.NewWriter()
			t.SetOutputMirror(os.Stdout)
			t.SetStyle(table.StyleRounded)
			t.AppendHeader(table.Row{"Name", "Kube Version", "Nodes", "Control Planes", "State", "Kind", "Created"})
			t.SetColumnConfigs([]table.ColumnConfig{
				{Number: 1, WidthMin: 20},
				{Number: 2, WidthMin: 10},
				{Number: 3, WidthMin: 5},
				{Number: 4, WidthMin: 10},
				{Number: 5, WidthMin: 10},
				{Number: 6, WidthMin: 10},
				{Number: 7, WidthMin: 15},
			})
			for _, cluster := range response.Result {
				t.AppendRow(table.Row{
					cluster.Name,
					cluster.KubeVersion,
					cluster.Nodes,
					cluster.ControlPlanes,
					cluster.State,
					cluster.Kind,
					formatTimeAgo(cluster.CreatedAt),
				})
			}
			t.Render()
		} else {
			name := args[0]
			cluster, err := client.GetCluster(apiToken, baseURL, name)
			if err != nil {
				fmt.Printf("Error fetching cluster details for %s: %v\n", name, err)
				return
			}
			fmt.Printf("Cluster Details:\n")
			fmt.Printf("  ID: %s\n", cluster.ID)
			fmt.Printf("  Name: %s\n", cluster.Name)
			fmt.Printf("  Environment: %s\n", cluster.Environment)
			fmt.Printf("  Kube Version: %s\n", cluster.KubeVersion)
			fmt.Printf("  State: %s\n", cluster.State)
			fmt.Printf("  Status: %v\n", cluster.Status)
		}
	},
}

func formatTimeAgo(tStr string) string {
	t, err := time.Parse(time.RFC3339, tStr)
	if err != nil {
		return tStr
	}
	return humanize.Time(t)
}

var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Get a resource",
}

func init() {
	getCmd.AddCommand(getClustersCmd)
	getCmd.AddCommand(clearSelectionCmd)
}
