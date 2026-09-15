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
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		stacks, err := apiClient.ListClusterStacks(cluster.ID)
		if err != nil {
			return fmt.Errorf("listing stacks: %w", err)
		}
		if len(args) == 0 {
			if stacks == nil {
				stacks = []client.ClusterStackListItem{}
			}
			if rendered, err := renderStructured(cmd, stacks); rendered || err != nil {
				return err
			}
		}
		if len(stacks) == 0 {
			fmt.Println("No stacks found for the active cluster.")
			return nil
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
				return withExitCode(exitNotFound, fmt.Errorf("stack %q not found on the active cluster", name))
			}
			if rendered, err := renderStructured(cmd, found); rendered || err != nil {
				return err
			}

			fmt.Println("Stack Details:")
			fmt.Printf("  Name:         %s\n", found.Name)
			fmt.Printf("  Description:  %s\n", found.Description)
			fmt.Printf("  State:        %s\n", found.State)
			fmt.Printf("  Deploy wave:  %s\n", formatDeployWave(found.DeployWave))
			fmt.Printf("  Manifests:    %d\n", len(found.Manifests))
			fmt.Printf("  Addons:       %d\n", len(found.Addons))
			fmt.Printf("  Applications: %d\n", len(found.Applications))

			if len(found.Manifests) > 0 {
				fmt.Println("\n  Manifests:")
				for _, manifest := range found.Manifests {
					kind := extractKindFromBase64(manifest.ManifestBase64)

					fmt.Printf("    %s %s\n", stackMemberStateIcon(manifest.State), manifest.Name)
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
					fmt.Printf("    %s %s\n", stackMemberStateIcon(addon.State), addon.Name)
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

			if len(found.Applications) > 0 {
				fmt.Println("  Applications:")
				for _, application := range found.Applications {
					fmt.Printf("    %s %s\n", stackMemberStateIcon(application.State), application.Name)
					fmt.Printf("      ├─ application: %s\n", formatApplicationReference(application))
					fmt.Printf("      ├─ namespace: %s\n", application.Namespace)
					fmt.Printf("      ├─ state: %s\n", application.State)

					if len(application.Parents) > 0 {
						fmt.Printf("      └─ parents: ")
						for i, parent := range application.Parents {
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
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{
			"Name", "Description", "State", "Wave", "Manifests", "Addons", "Applications",
		})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 20},
			{Number: 2, WidthMin: 30},
			{Number: 3, WidthMin: 12},
			{Number: 4, WidthMin: 6},
			{Number: 5, WidthMin: 10},
			{Number: 6, WidthMin: 10},
			{Number: 7, WidthMin: 12},
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
				formatDeployWave(stack.DeployWave),
				len(stack.Manifests),
				len(stack.Addons),
				len(stack.Applications),
			})
		}
		t.Render()
		return nil
	},
}

// stackMemberStateIcon renders the leading glyph a stack member gets in the
// detail view, shared by manifests, addons and applications.
func stackMemberStateIcon(state string) string {
	switch strings.ToLower(state) {
	case "up":
		return "✓"
	case "updating":
		return "⟳"
	case "failed":
		return "✗"
	}
	return "●"
}

// formatApplicationReference renders the Ankra application a stack member is
// deployed from, with its version when the platform reports one.
func formatApplicationReference(application client.StackApplication) string {
	identifier := application.PlatformApplicationID
	if identifier == "" {
		identifier = "-"
	}
	if application.PlatformApplicationVersion == "" {
		return identifier
	}
	return identifier + ":" + application.PlatformApplicationVersion
}

// formatDeployWave renders a stack's deploy wave for tables and detail
// views ("-" when the stack does not participate in wave ordering).
func formatDeployWave(wave *int) string {
	if wave == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *wave)
}

var clusterStacksDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a stack",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		yes, _ := cmd.Flags().GetBool("yes")

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete stack %q from cluster %q? This removes every addon and manifest in the stack! [y/N]: ", stackName, cluster.Name),
			yes,
		); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.DeleteStack(ctx, cluster.ID, stackName)
		if err != nil {
			return fmt.Errorf("deleting stack: %w", err)
		}

		if result.Success {
			fmt.Printf("Stack '%s' deleted successfully!\n", stackName)
			return nil
		}
		return fmt.Errorf("delete request did not report success")
	},
}

var clusterStacksRenameCmd = &cobra.Command{
	Use:   "rename <old_name> <new_name>",
	Short: "Rename a stack",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		oldName := args[0]
		newName := args[1]

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.RenameStack(ctx, cluster.ID, oldName, newName)
		if err != nil {
			return fmt.Errorf("renaming stack: %w", err)
		}

		if result.Success {
			fmt.Printf("Stack '%s' renamed to '%s' successfully!\n", oldName, newName)
			printGitPushDeferral(cmd.OutOrStdout(), result.GitPushDeferred, result.Message)
			return nil
		}
		return fmt.Errorf("rename request did not report success")
	},
}

var clusterStacksHistoryCmd = &cobra.Command{
	Use:   "history <name>",
	Short: "Show history of changes for a stack",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		history, err := apiClient.GetStackHistory(cluster.ID, stackName)
		if err != nil {
			return fmt.Errorf("getting stack history: %w", err)
		}

		if rendered, err := renderStructured(cmd, history); rendered || err != nil {
			return err
		}

		if len(history.History) == 0 {
			fmt.Printf("No history found for stack '%s'.\n", stackName)
			return nil
		}

		fmt.Printf("History for stack '%s':\n\n", stackName)

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Resource", "Type", "Change Type", "Created At", "Created By"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 20},
			{Number: 2, WidthMin: 10},
			{Number: 3, WidthMin: 12},
			{Number: 4, WidthMin: 15},
			{Number: 5, WidthMin: 20},
		})

		for _, item := range history.History {
			for _, entry := range item.VersionHistory {
				changeType := "-"
				if entry.ChangeType != nil {
					changeType = *entry.ChangeType
				}
				createdBy := "-"
				switch {
				case entry.UserName != nil && *entry.UserName != "":
					createdBy = *entry.UserName
				case entry.ExternalUser != nil && *entry.ExternalUser != "":
					createdBy = *entry.ExternalUser
				case entry.UserID != "":
					createdBy = entry.UserID
				}
				t.AppendRow(table.Row{
					item.ResourceName,
					item.ResourceType,
					changeType,
					formatOptionalTimeAgo(entry.CreatedAt),
					createdBy,
				})
			}
		}
		t.Render()
		return nil
	},
}

// resolveClusterID resolves a cluster name or ID to a cluster ID.
//
// If the input already looks like a UUID, it is returned as-is so
// callers can pass either form. Otherwise the cluster list is paged
// through until a matching name is found, instead of relying on a
// single page that may silently truncate results.
//
// A name typed exactly as the cluster carries it wins immediately. Only when
// no exact match exists does the case-insensitive fallback decide, and then
// the whole listing is read first: matching with EqualFold and returning the
// first hit picked an arbitrary one of two clusters whose names differ only by
// case, and sent the command - `deprovision` included - to whichever the
// listing happened to order first. Ambiguity is now an error naming both.
func resolveClusterID(nameOrID string) (string, error) {
	// isLikelyClusterID, not a len/dash count: "36 characters with four
	// dashes" also describes plenty of real cluster names, and every one of
	// them was forwarded to the API as an id and answered with an opaque 404
	// instead of being looked up as the name it is.
	if isLikelyClusterID(nameOrID) {
		return nameOrID, nil
	}

	const pageSize = 100
	const maxPages = 50
	var caseInsensitiveMatches []client.ClusterListItem
	listingTruncated := false
	for page := 1; page <= maxPages; page++ {
		response, err := apiClient.ListClusters(page, pageSize)
		if err != nil {
			return "", fmt.Errorf("listing clusters: %w", err)
		}
		for _, cluster := range response.Result {
			if cluster.Name == nameOrID {
				return cluster.ID, nil
			}
			if strings.EqualFold(cluster.Name, nameOrID) {
				caseInsensitiveMatches = append(caseInsensitiveMatches, cluster)
			}
		}
		if response.Pagination.TotalPages <= page || len(response.Result) == 0 {
			break
		}
		if page == maxPages {
			listingTruncated = true
		}
	}

	switch len(caseInsensitiveMatches) {
	case 0:
		if listingTruncated {
			// NOT exitNotFound: a truncated listing is not a verified absence.
			// The cluster may well exist further down, so this must not tell a
			// script "no such cluster" - which for an idempotent teardown reads
			// as "already gone".
			return "", fmt.Errorf("cluster %q was not among the first %d clusters and the listing has more; pass the cluster id instead",
				nameOrID, pageSize*maxPages)
		}
		// exitNotFound keeps the name path and the id path telling scripts the
		// same thing, the rule application_resolve.go already states: an id
		// that does not exist reaches the API and comes back 404, which
		// exitCodeFor maps to exitNotFound, so a name that does not resolve
		// must not exit with the generic failure code instead. This PR makes
		// the two spellings interchangeable on 103 commands, and they would
		// otherwise have disagreed on the one thing scripts branch on - so
		// `ankra cluster hetzner deprovision "$C" || [ $? -eq 3 ]` stayed
		// idempotent with an id and stopped being idempotent with a name.
		return "", withExitCode(exitNotFound, fmt.Errorf("cluster %q not found", nameOrID))
	case 1:
		return caseInsensitiveMatches[0].ID, nil
	default:
		candidates := make([]string, 0, len(caseInsensitiveMatches))
		for _, cluster := range caseInsensitiveMatches {
			candidates = append(candidates, fmt.Sprintf("%s (%s)", cluster.Name, cluster.ID))
		}
		// An ambiguous name is a bad argument, not a missing cluster: the
		// invocation has to change before it can succeed.
		return "", withExitCode(exitUsage, fmt.Errorf("cluster %q is ambiguous - %d clusters differ from it only by case: %s; pass the cluster id instead",
			nameOrID, len(caseInsensitiveMatches), strings.Join(candidates, ", ")))
	}
}

func init() {
	clusterStacksDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(clusterStacksListCmd, clusterStacksHistoryCmd)

	clusterStacksCmd.AddCommand(clusterStacksListCmd)
	clusterStacksCmd.AddCommand(clusterStacksDeleteCmd)
	clusterStacksCmd.AddCommand(clusterStacksRenameCmd)
	clusterStacksCmd.AddCommand(clusterStacksHistoryCmd)

	clusterCmd.AddCommand(clusterStacksCmd)
}
