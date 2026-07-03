package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"ankra/internal/client"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// isPromptCancellation reports whether a promptui error signals that the user
// aborted the picker (Ctrl+C, ESC/abort, or EOF) rather than a real failure.
// Callers map it to errCancelled so an aborted picker exits with exitCancelled.
func isPromptCancellation(err error) bool {
	return errors.Is(err, promptui.ErrInterrupt) ||
		errors.Is(err, promptui.ErrAbort) ||
		errors.Is(err, promptui.ErrEOF)
}

type SelectableItem struct {
	IsLoadMore bool
	Cluster    *client.ClusterListItem
	Label      string
}

var clusterSelectCmd = &cobra.Command{
	Use:   "select [cluster_name]",
	Short: "Select a cluster and save as active",
	Long: `Select a cluster and save it as the active cluster for subsequent commands.

If a cluster name is provided, it will be selected directly without prompting.
If no name is provided, an interactive picker is shown.

Examples:
  ankra cluster select
  ankra cluster select my-cluster`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return selectClusterByName(args[0])
		}
		return selectClusterInteractive()
	},
}

func selectClusterByName(name string) error {
	cluster, err := apiClient.GetCluster(name)
	if err != nil {
		return fmt.Errorf("finding cluster '%s': %w", name, err)
	}

	if err := saveSelectedCluster(cluster); err != nil {
		return fmt.Errorf("failed to save selection: %w", err)
	}

	fmt.Printf("Selected cluster: %s is now active.\n", cluster.Name)
	return nil
}

func selectClusterInteractive() error {
	page := 1
	fetchedClusters := []client.ClusterListItem{}
	startCursorPosition := 0

	for {
		response, err := apiClient.ListClusters(page, 25)
		if err != nil {
			return fmt.Errorf("listing clusters: %w", err)
		}

		if len(response.Result) == 0 {
			fmt.Println("No clusters available.")
			return nil
		}

		prompt, selectableItems, updatedFetchedClusters := createListPromptUi(response, fetchedClusters, startCursorPosition)
		fetchedClusters = updatedFetchedClusters
		i, _, err := prompt.Run()
		if err != nil {
			if isPromptCancellation(err) {
				return errCancelled
			}
			return fmt.Errorf("prompt failed: %w", err)
		}
		selectedItem := selectableItems[i]
		if selectedItem.IsLoadMore {
			startCursorPosition = i
			page++
			continue
		}
		selectedCluster := *selectedItem.Cluster
		if err := saveSelectedCluster(selectedCluster); err != nil {
			return fmt.Errorf("failed to save selection: %w", err)
		}
		fmt.Printf("Selected cluster: %s is now active.\n", selectedCluster.Name)
		fmt.Println("Run 'ankra cluster --help' to see available commands for this cluster")
		return nil
	}
}

var clusterClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear the active cluster selection",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := clearSelectedCluster(); err != nil {
			return fmt.Errorf("clearing selection: %w", err)
		}
		fmt.Println("Active cluster selection cleared.")
		return nil
	},
}

func selectedClusterFile() (string, error) {
	// Honor an explicit --config so a custom config fully isolates CLI state.
	// The active-cluster selection is otherwise keyed only by $HOME, so two
	// invocations pointed at different --config files (e.g. parallel automation)
	// would clobber each other's selection. Keep the selection next to the
	// config file instead.
	if cfgFile != "" {
		absConfig, err := filepath.Abs(cfgFile)
		if err != nil {
			absConfig = cfgFile
		}
		if err := os.MkdirAll(filepath.Dir(absConfig), 0700); err != nil {
			return "", err
		}
		// Append the suffix to the full config path so distinct config files get
		// distinct selection files (deriving a stem via filepath.Ext collapses
		// e.g. config.hetzner and config.ovh onto the same name).
		return absConfig + ".selected.json", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ankra")
	if err := os.MkdirAll(dir, 0700); err != nil {
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
	return os.WriteFile(path, data, 0600)
}

// activeClusterFlagName is the persistent --cluster override registered on
// clusterCmd. Every `ankra cluster ...` subcommand inherits it, so a single
// command can target a cluster without first running `ankra cluster select`.
const activeClusterFlagName = "cluster"

// resolveActiveCluster returns the cluster a command should operate on. An
// explicit --cluster <name|id> takes precedence over the cluster persisted by
// `ankra cluster select`.
func resolveActiveCluster(cmd *cobra.Command) (client.ClusterListItem, error) {
	if override := clusterFlagOverride(cmd); override != "" {
		return lookupClusterByNameOrID(override)
	}
	selected, err := loadSelectedCluster()
	if err != nil {
		return client.ClusterListItem{}, errNoClusterSelected{}
	}
	return selected, nil
}

// clusterFlagOverride returns the trimmed --cluster value when the command (or
// an inherited persistent flag) defines it and a value was supplied.
func clusterFlagOverride(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	flag := cmd.Flags().Lookup(activeClusterFlagName)
	if flag == nil {
		return ""
	}
	return strings.TrimSpace(flag.Value.String())
}

// lookupClusterByNameOrID resolves a cluster name or UUID to its full list
// item. A value shaped like a UUID is looked up by ID first, then falls back to
// a name lookup so a name that merely resembles a UUID still resolves.
func lookupClusterByNameOrID(nameOrID string) (client.ClusterListItem, error) {
	if isLikelyClusterID(nameOrID) {
		if cluster, err := apiClient.GetClusterByID(nameOrID); err == nil {
			return cluster, nil
		}
	}
	cluster, err := apiClient.GetCluster(nameOrID)
	if err != nil {
		return client.ClusterListItem{}, fmt.Errorf("cluster %q not found", nameOrID)
	}
	return cluster, nil
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
	clusterCmd.PersistentFlags().String(activeClusterFlagName, "", "Target cluster name or ID for this command, overriding `ankra cluster select`")
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
		if index < 0 || index >= len(selectableItems) {
			return false
		}
		item := selectableItems[index]
		if item.IsLoadMore {
			// Always show the synthetic "load more" row regardless of search
			// input so users can still page forward after typing a filter.
			return true
		}
		if item.Cluster == nil {
			return false
		}
		return strings.Contains(strings.ToLower(item.Cluster.Name), strings.ToLower(input))
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
