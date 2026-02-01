package cmd

import (
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var chartsCmd = &cobra.Command{
	Use:   "charts",
	Short: "Browse Helm charts",
	Long:  "Commands to list, search, and get information about Helm charts.",
}

var chartsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available Helm charts",
	Run: func(cmd *cobra.Command, args []string) {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		onlySubscribed, _ := cmd.Flags().GetBool("subscribed")

		resp, err := client.ListCharts(apiToken, baseURL, page, pageSize, onlySubscribed)
		if err != nil {
			fmt.Printf("Error listing charts: %v\n", err)
			return
		}

		if len(resp.Charts) == 0 {
			fmt.Println("No charts found.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Version", "Repository", "Description"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 25},
			{Number: 2, WidthMin: 10},
			{Number: 3, WidthMin: 20},
			{Number: 4, WidthMax: 50},
		})

		for _, chart := range resp.Charts {
			description := chart.Description
			if len(description) > 50 {
				description = description[:47] + "..."
			}
			t.AppendRow(table.Row{
				chart.Name,
				chart.Version,
				chart.RepositoryName,
				description,
			})
		}
		t.Render()

		fmt.Printf("\nPage %d of %d (page size: %d)\n",
			resp.Pagination.Page, resp.Pagination.TotalPages, resp.Pagination.PageSize)
	},
}

var chartsSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search for Helm charts",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		query := args[0]

		charts, err := client.SearchCharts(apiToken, baseURL, query)
		if err != nil {
			fmt.Printf("Error searching charts: %v\n", err)
			return
		}

		if len(charts) == 0 {
			fmt.Printf("No charts found matching '%s'.\n", query)
			return
		}

		fmt.Printf("Charts matching '%s':\n\n", query)

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Version", "Repository", "Description"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 25},
			{Number: 2, WidthMin: 10},
			{Number: 3, WidthMin: 20},
			{Number: 4, WidthMax: 50},
		})

		for _, chart := range charts {
			description := chart.Description
			if len(description) > 50 {
				description = description[:47] + "..."
			}
			t.AppendRow(table.Row{
				chart.Name,
				chart.Version,
				chart.RepositoryName,
				description,
			})
		}
		t.Render()
	},
}

var chartsInfoCmd = &cobra.Command{
	Use:   "info <chart_name>",
	Short: "Get detailed information about a chart",
	Long: `Get detailed information about a Helm chart.

Requires either --repository flag or finding the chart in the catalog first.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		chartName := args[0]
		repositoryURL, _ := cmd.Flags().GetString("repository")

		// If no repository URL provided, try to find the chart first
		if repositoryURL == "" {
			charts, err := client.SearchCharts(apiToken, baseURL, chartName)
			if err != nil {
				fmt.Printf("Error finding chart: %v\n", err)
				return
			}

			// Find exact match
			for _, chart := range charts {
				if strings.EqualFold(chart.Name, chartName) {
					repositoryURL = chart.RepositoryURL
					break
				}
			}

			if repositoryURL == "" {
				fmt.Printf("Chart '%s' not found. Please specify --repository flag.\n", chartName)
				return
			}
		}

		details, err := client.GetChartDetails(apiToken, baseURL, chartName, repositoryURL)
		if err != nil {
			fmt.Printf("Error getting chart details: %v\n", err)
			return
		}

		fmt.Printf("Chart: %s\n\n", details.Name)
		fmt.Printf("  Repository: %s (%s)\n", details.RepositoryName, details.RepositoryURL)
		if details.Icon != "" {
			fmt.Printf("  Icon: %s\n", details.Icon)
		}

		if len(details.Versions) > 0 {
			fmt.Printf("\n  Available Versions (%d):\n", len(details.Versions))
			// Show up to 10 versions
			maxVersions := 10
			if len(details.Versions) < maxVersions {
				maxVersions = len(details.Versions)
			}
			for i := 0; i < maxVersions; i++ {
				fmt.Printf("    - %s\n", details.Versions[i])
			}
			if len(details.Versions) > 10 {
				fmt.Printf("    ... and %d more\n", len(details.Versions)-10)
			}
		}

		if len(details.Profiles) > 0 {
			fmt.Printf("\n  Available Profiles:\n")
			for _, profile := range details.Profiles {
				fmt.Printf("    - %s", profile.Name)
				if profile.Description != nil && *profile.Description != "" {
					fmt.Printf(": %s", *profile.Description)
				}
				fmt.Println()
			}
		}
	},
}

func init() {
	chartsListCmd.Flags().Int("page", 1, "Page number")
	chartsListCmd.Flags().Int("page-size", 25, "Number of items per page")
	chartsListCmd.Flags().Bool("subscribed", false, "Show only subscribed charts")

	chartsInfoCmd.Flags().String("repository", "", "Repository URL for the chart")

	chartsCmd.AddCommand(chartsListCmd)
	chartsCmd.AddCommand(chartsSearchCmd)
	chartsCmd.AddCommand(chartsInfoCmd)

	rootCmd.AddCommand(chartsCmd)
}
