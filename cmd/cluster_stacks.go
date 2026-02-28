package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var clusterStacksCmd = &cobra.Command{
	Use:   "stacks",
	Short: "Manage stacks for clusters",
	Long:  "Commands to list, create, delete, rename, and view history of stacks.",
}

var clusterStacksListCmd = &cobra.Command{
	Use:   "list [stack name]",
	Short: "List stacks for the active cluster; or show details for a single stack",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
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
				state = text.FgGreen.Sprint("✓ " + state)
			case "failed":
				state = text.FgRed.Sprint("✗ " + state)
			default:
				state = text.FgYellow.Sprint("⟳ " + state)
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

var clusterStacksCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new stack",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		stackName := args[0]
		description, _ := cmd.Flags().GetString("description")

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := client.CreateStack(ctx, apiToken, baseURL, cluster.ID, stackName, description)
		if err != nil {
			fmt.Printf("Error creating stack: %v\n", err)
			return
		}

		if result.Success {
			fmt.Printf("Stack '%s' created successfully!\n", stackName)
		}
	},
}

var clusterStacksDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a stack",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		stackName := args[0]

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := client.DeleteStack(ctx, apiToken, baseURL, cluster.ID, stackName)
		if err != nil {
			fmt.Printf("Error deleting stack: %v\n", err)
			return
		}

		if result.Success {
			fmt.Printf("Stack '%s' deleted successfully!\n", stackName)
		}
	},
}

var clusterStacksRenameCmd = &cobra.Command{
	Use:   "rename <old_name> <new_name>",
	Short: "Rename a stack",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		oldName := args[0]
		newName := args[1]

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := client.RenameStack(ctx, apiToken, baseURL, cluster.ID, oldName, newName)
		if err != nil {
			fmt.Printf("Error renaming stack: %v\n", err)
			return
		}

		if result.Success {
			fmt.Printf("Stack '%s' renamed to '%s' successfully!\n", oldName, newName)
		}
	},
}

var clusterStacksHistoryCmd = &cobra.Command{
	Use:   "history <name>",
	Short: "Show history of changes for a stack",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		stackName := args[0]

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		history, err := client.GetStackHistory(apiToken, baseURL, cluster.ID, stackName)
		if err != nil {
			fmt.Printf("Error getting stack history: %v\n", err)
			return
		}

		if len(history.History) == 0 {
			fmt.Printf("No history found for stack '%s'.\n", stackName)
			return
		}

		fmt.Printf("History for stack '%s':\n\n", history.StackName)

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Version", "Change Type", "Created At", "Created By", "Description"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 8},
			{Number: 2, WidthMin: 12},
			{Number: 3, WidthMin: 15},
			{Number: 4, WidthMin: 20},
			{Number: 5, WidthMin: 30},
		})

		for _, entry := range history.History {
			createdBy := "-"
			if entry.CreatedBy != nil {
				createdBy = *entry.CreatedBy
			}
			description := "-"
			if entry.Description != nil {
				description = *entry.Description
			}
			t.AppendRow(table.Row{
				entry.Version,
				entry.ChangeType,
				formatTimeAgo(entry.CreatedAt),
				createdBy,
				description,
			})
		}
		t.Render()
	},
}

var clusterStacksCloneCmd = &cobra.Command{
	Use:   "clone <stack_name> --to <target_cluster>",
	Short: "Clone a stack to another cluster as a draft",
	Long: `Clone a stack from the current cluster to a target cluster.
The cloned stack will be created as a draft in the target cluster,
allowing you to review and modify it before deployment.

Encrypted values will be stripped during cloning for security reasons
and will need to be reconfigured in the target cluster.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		stackName := args[0]
		targetCluster, _ := cmd.Flags().GetString("to")
		newName, _ := cmd.Flags().GetString("name")
		includeConfig, _ := cmd.Flags().GetBool("include-config")

		if targetCluster == "" {
			fmt.Println("Error: --to flag is required. Specify the target cluster name or ID.")
			return
		}

		// Load source cluster (currently selected)
		sourceCluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select' to pick one.")
			return
		}

		// Resolve target cluster ID
		targetClusterID, err := resolveClusterID(targetCluster)
		if err != nil {
			fmt.Printf("Error resolving target cluster: %v\n", err)
			return
		}

		if sourceCluster.ID == targetClusterID {
			fmt.Println("Error: Cannot clone a stack to the same cluster.")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		req := client.CloneStackToClusterRequest{
			SourceClusterID:            sourceCluster.ID,
			StackName:                  stackName,
			NewStackName:               newName,
			IncludeAddonConfigurations: includeConfig,
		}

		fmt.Printf("Cloning stack '%s' to cluster '%s'...\n", stackName, targetCluster)

		result, err := client.CloneStackToCluster(ctx, apiToken, baseURL, targetClusterID, req)
		if err != nil {
			fmt.Printf("Error cloning stack: %v\n", err)
			return
		}

		fmt.Printf("\nStack cloned successfully!\n")
		fmt.Printf("  Draft ID:    %s\n", result.DraftID)
		fmt.Printf("  Stack Name:  %s\n", result.StackName)
		fmt.Printf("  Addons:      %d\n", result.AddonsCloned)
		fmt.Printf("  Manifests:   %d\n", result.ManifestsCloned)

		if len(result.Warnings) > 0 {
			fmt.Println("\nWarnings:")
			for _, warning := range result.Warnings {
				fmt.Printf("  - %s\n", warning)
			}
		}

		fmt.Printf("\nThe stack has been created as a draft. Open the Ankra dashboard to review and deploy.\n")
	},
}

// resolveClusterID resolves a cluster name or ID to a cluster ID
func resolveClusterID(nameOrID string) (string, error) {
	// First, try to load it as a direct ID (UUID format)
	// If it looks like a UUID, use it directly
	if len(nameOrID) == 36 && strings.Count(nameOrID, "-") == 4 {
		return nameOrID, nil
	}

	// Otherwise, try to find by name from the clusters list
	clustersResp, err := client.ListClusters(apiToken, baseURL, 1, 100)
	if err != nil {
		return "", fmt.Errorf("failed to list clusters: %w", err)
	}

	for _, cluster := range clustersResp.Result {
		if strings.EqualFold(cluster.Name, nameOrID) {
			return cluster.ID, nil
		}
	}

	return "", fmt.Errorf("cluster '%s' not found", nameOrID)
}

func init() {
	clusterStacksCreateCmd.Flags().String("description", "", "Description for the stack")

	// Clone command flags
	clusterStacksCloneCmd.Flags().StringP("to", "t", "", "Target cluster name or ID (required)")
	clusterStacksCloneCmd.Flags().StringP("name", "n", "", "New stack name (optional, defaults to original)")
	clusterStacksCloneCmd.Flags().Bool("include-config", true, "Include addon configurations")
	_ = clusterStacksCloneCmd.MarkFlagRequired("to")

	clusterStacksCmd.AddCommand(clusterStacksListCmd)
	clusterStacksCmd.AddCommand(clusterStacksCreateCmd)
	clusterStacksCmd.AddCommand(clusterStacksDeleteCmd)
	clusterStacksCmd.AddCommand(clusterStacksRenameCmd)
	clusterStacksCmd.AddCommand(clusterStacksHistoryCmd)
	clusterStacksCmd.AddCommand(clusterStacksCloneCmd)

	clusterCmd.AddCommand(clusterStacksCmd)
}
