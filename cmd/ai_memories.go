package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var aiMemoriesCmd = &cobra.Command{
	Use:   "memories",
	Short: "List or clear what the assistant remembers about you",
	Long: `List or clear what the assistant remembers about you.

Ankra's chat stores short facts from your conversations and recalls them
into later ones, so it does not have to rediscover your clusters, choices and
preferences every time. These commands act on your own memories in the
current organisation only; nobody else's are listed or touched.`,
}

var aiMemoriesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the memories the assistant has stored for you, newest first",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		if limit < 1 || limit > client.AIMemoryListCap {
			return withExitCode(exitUsage, fmt.Errorf("--limit must be between 1 and %d", client.AIMemoryListCap))
		}
		memories, listError := apiClient.ListAIMemories(limit)
		if listError != nil {
			return aiMemoriesRouteError(listError)
		}
		if rendered, renderError := renderStructured(cmd, memories); rendered || renderError != nil {
			return renderError
		}
		renderAIMemories(cmd.OutOrStdout(), memories, limit)
		return nil
	},
}

var aiMemoriesClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Delete every memory the assistant has stored for you",
	Long: `Delete every memory the assistant has stored for you in the current
organisation. The assistant starts your next conversation with nothing it
learned before. Other members' memories are not affected.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		skipConfirm, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			"Delete every AI memory stored for you in this organisation? [y/N]: ", skipConfirm); confirmError != nil {
			return confirmError
		}
		deleted, clearError := apiClient.ClearAIMemories()
		if clearError != nil {
			return aiMemoriesRouteError(clearError)
		}
		if rendered, renderError := renderStructured(cmd, map[string]int{"deleted": deleted}); rendered || renderError != nil {
			return renderError
		}
		noun := "memories"
		if deleted == 1 {
			noun = "memory"
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted %d %s.\n", deleted, noun)
		return nil
	},
}

// aiMemoriesRouteError names the one 404 these routes can answer: the
// platform does not serve them to API tokens yet. Left alone it would exit 3,
// which reads as "you have no memories", the opposite of what it proves.
func aiMemoriesRouteError(routeError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(routeError, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
		return withExitCode(exitError, errors.New(
			"this platform does not serve AI memories to API tokens: /api/v1/org/ai-memory/memories "+
				"is not registered, so this platform predates the token route. Memories are managed in the portal"))
	}
	return routeError
}

func renderAIMemories(out io.Writer, memories []client.AIMemory, limit int) {
	if len(memories) == 0 {
		_, _ = fmt.Fprintln(out, "The assistant has no memories stored for you in this organisation.")
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.AppendHeader(table.Row{"CREATED", "CLUSTER", "MEMORY"})
	for _, memory := range memories {
		cluster := "-"
		if memory.ClusterID != nil {
			cluster = *memory.ClusterID
		}
		content := strings.Join(strings.Fields(memory.Content), " ")
		if runes := []rune(content); len(runes) > 100 {
			content = string(runes[:99]) + "…"
		}
		writer.AppendRow(table.Row{memory.CreatedAt.UTC().Format("2006-01-02 15:04"), cluster, content})
	}
	writer.Render()
	if len(memories) == limit {
		_, _ = fmt.Fprintf(out, "Showing the newest %d; there may be more (--limit up to %d).\n", limit, client.AIMemoryListCap)
	}
}

func init() {
	aiMemoriesListCmd.Flags().Int("limit", 50, fmt.Sprintf("How many memories to list (1-%d)", client.AIMemoryListCap))
	registerStructuredOutputFlags(aiMemoriesListCmd)
	aiMemoriesClearCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(aiMemoriesClearCmd)
	aiMemoriesCmd.AddCommand(aiMemoriesListCmd, aiMemoriesClearCmd)
	aiCmd.AddCommand(aiMemoriesCmd)
}
