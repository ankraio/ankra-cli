package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var clusterHelmCmd = &cobra.Command{
	Use:   "helm",
	Short: "Manage Helm releases in the cluster",
	Long:  "Commands to list and uninstall Helm releases running in the cluster.",
}

var clusterHelmReleasesCmd = &cobra.Command{
	Use:   "releases",
	Short: "List Helm releases in the cluster",
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		namespace, _ := cmd.Flags().GetString("namespace")
		allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
		outputFormat, _ := cmd.Flags().GetString("output")

		opts := &client.HelmReleasesOptions{AllNamespaces: true}
		if namespace != "" && !allNamespaces {
			opts = &client.HelmReleasesOptions{Namespace: namespace}
		}

		response, err := apiClient.ListHelmReleases(cluster.ID, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if outputFormat == "json" {
			jsonData, _ := json.MarshalIndent(response, "", "  ")
			fmt.Println(string(jsonData))
			return
		}

		allItems := []interface{}{}
		for _, resp := range response.ResourceResponses {
			allItems = append(allItems, resp.Items...)
		}

		if len(allItems) == 0 {
			fmt.Println("No Helm releases found.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Namespace", "Revision", "Status", "Chart", "App Version"})

		for _, item := range allItems {
			if release, ok := item.(map[string]interface{}); ok {
				status := getNestedString(release, "status")
				switch status {
				case "deployed":
					status = text.FgGreen.Sprint(status)
				case "failed":
					status = text.FgRed.Sprint(status)
				case "pending-install", "pending-upgrade":
					status = text.FgYellow.Sprint(status)
				}

				t.AppendRow(table.Row{
					getNestedString(release, "name"),
					getNestedString(release, "namespace"),
					getNestedString(release, "revision"),
					status,
					getNestedString(release, "chart"),
					getNestedString(release, "app_version"),
				})
			}
		}
		t.Render()
	},
}

var clusterHelmUninstallCmd = &cobra.Command{
	Use:   "uninstall <release_name>",
	Short: "Uninstall a Helm release from the cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		releaseName := args[0]
		namespace, _ := cmd.Flags().GetString("namespace")

		if namespace == "" {
			fmt.Fprintln(os.Stderr, "Error: --namespace (-n) is required for uninstall")
			os.Exit(1)
		}

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		result, err := apiClient.UninstallHelmRelease(cluster.ID, releaseName, namespace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Helm release '%s' uninstalled from namespace '%s'.\n", releaseName, namespace)
		if result.Message != nil && *result.Message != "" {
			fmt.Printf("  Message: %s\n", *result.Message)
		}
	},
}

func init() {
	clusterHelmReleasesCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace")
	clusterHelmReleasesCmd.Flags().BoolP("all-namespaces", "A", false, "List across all namespaces (default)")
	clusterHelmReleasesCmd.Flags().StringP("output", "o", "table", "Output format: table, json")

	clusterHelmUninstallCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required)")

	clusterHelmCmd.AddCommand(clusterHelmReleasesCmd)
	clusterHelmCmd.AddCommand(clusterHelmUninstallCmd)

	clusterCmd.AddCommand(clusterHelmCmd)
}
