package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/manifoldco/promptui"
	"os"
	"path/filepath"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var selectClusterCmd = &cobra.Command{
	Use:     "cluster",
	Aliases: []string{"clusters"},
	Short:   "Interactively select a cluster and save as active",
	Run: func(cmd *cobra.Command, args []string) {
		clusters, err := client.ListClusters(apiToken, baseURL)
		if err != nil {
			fmt.Printf("Error listing clusters: %v\n", err)
			return
		}
		if len(clusters) == 0 {
			fmt.Println("No clusters available.")
			return
		}
		templates := &promptui.SelectTemplates{
			Label:    "{{ . }}:",
			Active:   "\U0001F336 {{ .Name | cyan }} ({{ .ID | red }})",
			Inactive: "  {{ .Name | cyan }} ({{ .ID | red }})",
			Selected: "\U0001F336 {{ .Name | red }}",
		}
		searcher := func(input string, index int) bool {
			cluster := clusters[index]
			return strings.Contains(strings.ToLower(cluster.Name), strings.ToLower(input))
		}
		prompt := promptui.Select{
			Label:     "Select Cluster",
			Items:     clusters,
			Templates: templates,
			Searcher:  searcher,
		}
		i, _, err := prompt.Run()
		if err != nil {
			fmt.Printf("Prompt failed: %v\n", err)
			return
		}
		selectedCluster := clusters[i]
		if err := saveSelectedCluster(selectedCluster); err != nil {
			fmt.Printf("Failed to save selection: %v\n", err)
			return
		}
		fmt.Printf("Selected cluster: %s (ID: %s) is now active.\n", selectedCluster.Name, selectedCluster.ID)
		fmt.Println("You can now run 'ankra get operations' or 'ankra get addons'.")
	},
}

var clearSelectionCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear the active cluster selection",
	Run: func(cmd *cobra.Command, args []string) {
		if err := clearSelectedCluster(); err != nil {
			fmt.Printf("Error clearing selection: %v\n", err)
			return
		}
		fmt.Println("Active cluster selection cleared.")
	},
}

func selectedClusterFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ankra")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "selected.json"), nil
}

func saveSelectedCluster(cluster client.ClusterListItem) error {
	path, err := selectedClusterFile()
	if err != nil {
		return err
	}
	data, err := json.Marshal(cluster)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func loadSelectedCluster() (client.ClusterListItem, error) {
	var cluster client.ClusterListItem
	path, err := selectedClusterFile()
	if err != nil {
		return cluster, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cluster, err
	}
	err = json.Unmarshal(data, &cluster)
	return cluster, err
}

func clearSelectedCluster() error {
	path, err := selectedClusterFile()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

var selectCmd = &cobra.Command{
	Use:   "select",
	Short: "Interactively select a resource",
}

func init() {
	selectCmd.AddCommand(selectClusterCmd)
	selectCmd.AddCommand(clearSelectionCmd)
}
