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
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		onlySubscribed, _ := cmd.Flags().GetBool("subscribed")

		resp, err := apiClient.ListCharts(page, pageSize, onlySubscribed)
		if err != nil {
			return fmt.Errorf("listing charts: %w", err)
		}

		if rendered, err := renderStructured(cmd, resp); rendered || err != nil {
			return err
		}

		if len(resp.Charts) == 0 {
			fmt.Println("No charts found.")
			return nil
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
		return nil
	},
}

var chartsSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search for Helm charts",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		charts, err := apiClient.SearchCharts(query)
		if err != nil {
			return fmt.Errorf("searching charts: %w", err)
		}

		if charts == nil {
			charts = []client.ChartItem{}
		}
		if rendered, err := renderStructured(cmd, charts); rendered || err != nil {
			return err
		}

		if len(charts) == 0 {
			fmt.Printf("No charts found matching '%s'.\n", query)
			return nil
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
		return nil
	},
}

var chartsInfoCmd = &cobra.Command{
	Use:   "info <chart_name>",
	Short: "Get detailed information about a chart",
	Long: `Get detailed information about a Helm chart.

Requires either --repository flag or finding the chart in the catalog first.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		chartName := args[0]
		repositoryURL, _ := cmd.Flags().GetString("repository")

		// If no repository URL provided, try to find the chart first
		if repositoryURL == "" {
			charts, err := apiClient.SearchCharts(chartName)
			if err != nil {
				return fmt.Errorf("finding chart: %w", err)
			}

			// Find exact match
			for _, chart := range charts {
				if strings.EqualFold(chart.Name, chartName) {
					repositoryURL = chart.RepositoryURL
					break
				}
			}

			if repositoryURL == "" {
				return withExitCode(exitNotFound, fmt.Errorf("chart '%s' not found. Please specify --repository flag", chartName))
			}
		}

		details, err := apiClient.GetChartDetails(chartName, repositoryURL)
		if err != nil {
			return fmt.Errorf("getting chart details: %w", err)
		}

		if rendered, err := renderStructured(cmd, details); rendered || err != nil {
			return err
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
		return nil
	},
}

func init() {
	chartsListCmd.Flags().Int("page", 1, "Page number")
	chartsListCmd.Flags().Int("page-size", 25, "Number of items per page")
	chartsListCmd.Flags().Bool("subscribed", false, "Show only subscribed charts")

	chartsInfoCmd.Flags().String("repository", "", "Repository URL for the chart")

	registerStructuredOutputFlags(chartsListCmd, chartsSearchCmd, chartsInfoCmd)

	chartsCmd.AddCommand(chartsListCmd)
	chartsCmd.AddCommand(chartsSearchCmd)
	chartsCmd.AddCommand(chartsInfoCmd)

	rootCmd.AddCommand(chartsCmd)
}
