package cmd

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"ankra/internal/client"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

type SelectableItem struct {
	IsLoadMore bool
	Cluster    *client.ClusterListItem
	Label      string
}

var clusterSelectCmd = &cobra.Command{
	Use:   "select",
	Short: "Interactively select a cluster and save as active",
	Run: func(cmd *cobra.Command, args []string) {
		page := 1
		fetchedClusters := []client.ClusterListItem{}
		startCursorPosition := 0

		for {
			response, err := client.ListClusters(apiToken, baseURL, page, 25)
			if err != nil {
				fmt.Printf("Error listing clusters: %v\n", err)
				break
			}

			if len(response.Result) == 0 {
				fmt.Println("No clusters available.")
				break
			}

			prompt, selectableItems, updatedFetchedClusters := createListPromptUi(response, fetchedClusters, startCursorPosition)
			fetchedClusters = updatedFetchedClusters
			i, _, err := prompt.Run()
			if err != nil {
				fmt.Printf("Prompt failed: %v\n", err)
				break
			}
			selectedItem := selectableItems[i]
			if selectedItem.IsLoadMore {
				startCursorPosition = i
				page++
				continue
			} else {
				selectedCluster := *selectedItem.Cluster
				if err := saveSelectedCluster(selectedCluster); err != nil {
					fmt.Printf("Failed to save selection: %v\n", err)
					return
				} else {
					fmt.Printf("Selected cluster: %s is now active.\n", selectedCluster.Name)
					fmt.Println("Run 'ankra cluster --help' to see available commands for this cluster")
					return
				}
			}
		}
	},
}

var clusterClearCmd = &cobra.Command{
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

func init() {
	clusterCmd.AddCommand(clusterSelectCmd)
	clusterCmd.AddCommand(clusterClearCmd)
}

func createListPromptUi(response *client.ClusterListResponse, previousFetchedClusters []client.ClusterListItem, startCursorPosition int) (promptui.Select, []SelectableItem, []client.ClusterListItem) {
	templates := &promptui.SelectTemplates{
		Label:    "{{ . }}:",
		Active:   "\U0001F336 {{ .Label | cyan }} {{ if .Cluster }} {{.Cluster | stateColor}} {{.Cluster.ID | red }} {{ end }}",
		Inactive: "  {{ .Label | cyan }} {{ if .Cluster }} {{.Cluster | stateColor}} {{.Cluster.ID | red }} {{ end }}",
		Selected: "{{ if .Cluster }} \u2705 {{ .Label | cyan }} {{.Cluster | stateColor}} {{.Cluster.ID | red }} {{ end }} {{ if .IsLoadMore }} \u23F3 Loading next page... {{ end }}",
		FuncMap: template.FuncMap{
			"black":   promptui.Styler(promptui.FGBlack),
			"red":     promptui.Styler(promptui.FGRed),
			"green":   promptui.Styler(promptui.FGGreen),
			"yellow":  promptui.Styler(promptui.FGYellow),
			"blue":    promptui.Styler(promptui.FGBlue),
			"magenta": promptui.Styler(promptui.FGMagenta),
			"cyan":    promptui.Styler(promptui.FGCyan),
			"white":   promptui.Styler(promptui.FGWhite),
			"bold":    promptui.Styler(promptui.FGBold),
			"faint":   promptui.Styler(promptui.FGFaint),
			"stateColor": func(cluster client.ClusterListItem) string {
				if cluster.State == "online" {
					return promptui.Styler(promptui.FGGreen)(cluster.State)
				}
				return promptui.Styler(promptui.FGWhite)(cluster.State)
			},
		},
	}
	selectableItems := []SelectableItem{}

	fetchedClusters := append(previousFetchedClusters, response.Result...)

	for _, cluster := range fetchedClusters {
		currentCluster := cluster
		selectableItems = append(selectableItems, SelectableItem{IsLoadMore: false, Cluster: &currentCluster, Label: cluster.Name})
	}

	if response.Pagination.Page < response.Pagination.TotalPages {
		selectableItems = append(selectableItems, SelectableItem{IsLoadMore: true, Cluster: nil, Label: "→ Load Next Page"})
	}

	searcher := func(input string, index int) bool {
		cluster := fetchedClusters[index]
		return strings.Contains(strings.ToLower(cluster.Name), strings.ToLower(input))
	}

	prompt := promptui.Select{
		Label:     "Select Cluster",
		Items:     selectableItems,
		CursorPos: startCursorPosition,
		Templates: templates,
		Searcher:  searcher,
		Size:      response.Pagination.PageSize + 1,
	}

	return prompt, selectableItems, fetchedClusters
}
