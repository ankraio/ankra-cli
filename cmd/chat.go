package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// chatStatusText renders a status frame's progress line. Newer backends
// send an object ({intent, mechanism, ...}); older ones sent a plain
// string. Empty means nothing worth showing.
func chatStatusText(data any) string {
	switch typed := data.(type) {
	case string:
		return typed
	case map[string]any:
		intent, _ := typed["intent"].(string)
		mechanism, _ := typed["mechanism"].(string)
		switch {
		case intent != "" && mechanism != "":
			return intent + " · " + mechanism
		case intent != "":
			return intent
		case mechanism != "":
			return mechanism
		}
	}
	return ""
}

// chatErrorMessage never returns empty: an error frame without a readable
// message still has to fail the command with something actionable.
func chatErrorMessage(event client.ChatStreamEvent) string {
	if message := event.ErrorMessage(); message != "" {
		return message
	}
	return "the server reported an error without details"
}

var chatCmd = &cobra.Command{
	Use:   "chat [message]",
	Short: "AI-powered chat for troubleshooting and assistance",
	Long: `AI-powered chat for troubleshooting and assistance.

If a message is provided, sends a one-shot question and prints the
conversation id so a follow-up can continue it with --conversation.
If no message is provided, enters interactive chat mode.

Use --cluster to provide cluster context for better answers; without it the
persisted 'ankra cluster select' applies, and with neither the chat reads
across the whole organisation.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName, _ := cmd.Flags().GetString("cluster")
		mode, _ := cmd.Flags().GetString("mode")
		conversationID, _ := cmd.Flags().GetString("conversation")
		interactionMode, modeError := normalizeChatMode(mode)
		if modeError != nil {
			return modeError
		}
		if conversationID != "" {
			if err := validateConversationID(conversationID); err != nil {
				return err
			}
			conversationID = strings.TrimSpace(conversationID)
		}

		var scope chatScope
		if clusterName != "" {
			cluster, err := apiClient.GetCluster(clusterName)
			if err != nil {
				return fmt.Errorf("finding cluster %s: %w", clusterName, err)
			}
			scope.clusterID = &cluster.ID
		} else if selected, err := loadSelectedCluster(); err == nil {
			scope.clusterID = &selected.ID
			scope.fromSelection = true
			scope.selectedName = selected.Name
		}

		if len(args) > 0 {
			return runChatMessage(scope, conversationID, args[0], interactionMode)
		}
		return runInteractiveChat(cmd.InOrStdin(), scope, conversationID, interactionMode)
	},
}

// normalizeChatMode validates the --mode flag: empty leaves the server
// default; "ask" and "agent" map to the interaction_mode wire values.
func normalizeChatMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		return "", nil
	case "ask":
		return "ask", nil
	case "agent", "agentic":
		return "agentic", nil
	default:
		return "", fmt.Errorf("invalid --mode %q: use 'ask' (read-only + safe creations) or 'agent' (can act)", mode)
	}
}

// staleSelectionNotice tells the user the persisted cluster selection points
// at a cluster the backend no longer knows, and how to clear it.
func staleSelectionNotice(scope chatScope) string {
	return fmt.Sprintf("note: the selected cluster %q no longer exists; chatting without cluster context. "+
		"Run 'ankra cluster clear' to drop the selection, or pass --cluster.", scope.selectedName)
}

// openChatTurn starts the turn in the given scope, degrading a stale
// persisted selection to the global lane once with a notice. It returns the
// scope the turn actually ran in so callers keep it for the next turn. The
// sessions lane reports the stale cluster as ErrClusterNotFound; the
// deprecated per-cluster stream answers a bare 404, so both shapes count.
func openChatTurn(scope chatScope, conversationID string, req client.ChatRequest,
	interactionMode string, errOut io.Writer) (<-chan client.ChatStreamEvent, bool, chatScope, error) {
	events, onSessions, err := startChatTurn(conversationID, scope.clusterID, req, interactionMode)
	if err != nil && scope.fromSelection && isNotFoundResponse(err) {
		_, _ = fmt.Fprintln(errOut, staleSelectionNotice(scope))
		scope = chatScope{}
		events, onSessions, err = startChatTurn(conversationID, nil, req, interactionMode)
	}
	return events, onSessions, scope, err
}

func runChatMessage(scope chatScope, conversationID string, query string, interactionMode string) error {
	startedConversation := conversationID == ""
	if startedConversation {
		generated, err := newChatUUID()
		if err != nil {
			return err
		}
		conversationID = generated
	}
	req := client.ChatRequest{Query: query, InteractionMode: interactionMode}
	events, onSessions, _, err := openChatTurn(scope, conversationID, req, interactionMode, os.Stderr)
	if err != nil {
		return fmt.Errorf("chat: %w", err)
	}

	fmt.Print("\n")
	outcome := renderChatTurn(events, os.Stdout, os.Stderr, false)
	fmt.Print("\n\n")
	if onSessions && startedConversation && outcome.errorMessage == "" {
		requestConversationTitle(conversationID)
	}
	if onSessions {
		// The id goes to stderr so a piped answer stays clean.
		if startedConversation {
			fmt.Fprintf(os.Stderr, "conversation %s (continue with: ankra chat --conversation %s \"...\")\n",
				conversationID, conversationID)
		} else {
			fmt.Fprintf(os.Stderr, "conversation %s\n", conversationID)
		}
	} else if !startedConversation {
		legacyLaneNotice(os.Stderr, conversationID, true)
	}
	if outcome.errorMessage != "" {
		return errors.New(outcome.errorMessage)
	}
	return nil
}

func runInteractiveChat(stdin io.Reader, scope chatScope, conversationID string, interactionMode string) error {
	continued := conversationID != ""
	// A continued conversation is not this session's to name; one started
	// here is named after its first completed turn, like the portal does.
	titled := continued
	if conversationID == "" {
		generated, err := newChatUUID()
		if err != nil {
			return err
		}
		conversationID = generated
	}
	fmt.Println("Ankra AI Chat")
	fmt.Println("─────────────")
	switch {
	case scope.clusterID != nil && scope.selectedName != "":
		fmt.Printf("Cluster context: %s (selected)\n", scope.selectedName)
	case scope.clusterID != nil:
		fmt.Println("Cluster context: active")
	default:
		fmt.Println("Cluster context: none (use --cluster to set)")
	}
	switch interactionMode {
	case "ask":
		fmt.Println("Mode: ask (read-only + safe creations)")
	case "agentic":
		fmt.Println("Mode: agent (can act)")
	}
	fmt.Printf("Conversation: %s\n", conversationID)
	fmt.Println("Type 'exit' or 'quit' to exit, 'clear' to start a new conversation")
	fmt.Println()

	// The legacy lane still needs the history the server does not keep;
	// the sessions lane ignores it.
	var history []client.ChatMessage
	legacyNoticed := false
	reader := bufio.NewReader(stdin)

	for {
		fmt.Print(text.FgCyan.Sprint("You: "))
		input, err := reader.ReadString('\n')
		if err != nil {
			// Ctrl-D ends the session like 'exit', not like a failure.
			if errors.Is(err, io.EOF) {
				fmt.Println("\nGoodbye!")
				return nil
			}
			return fmt.Errorf("reading input: %w", err)
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit", "quit", "q":
			fmt.Println("Goodbye!")
			return nil
		case "clear":
			history = nil
			generated, genErr := newChatUUID()
			if genErr != nil {
				return genErr
			}
			conversationID = generated
			titled = false
			fmt.Printf("Started a new conversation: %s\n", conversationID)
			continue
		}

		history = append(history, client.ChatMessage{Role: "user", Content: input})
		req := client.ChatRequest{
			Query:               input,
			ConversationHistory: history,
			InteractionMode:     interactionMode,
		}

		events, onSessions, nextScope, err := openChatTurn(scope, conversationID, req, interactionMode, os.Stderr)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		scope = nextScope
		if !onSessions && !legacyNoticed {
			legacyNoticed = true
			legacyLaneNotice(os.Stderr, conversationID, continued)
		}

		fmt.Print(text.FgGreen.Sprint("\nAssistant: "))
		outcome := renderChatTurn(events, os.Stdout, os.Stderr, true)
		if outcome.response != "" {
			history = append(history, client.ChatMessage{Role: "assistant", Content: outcome.response})
		}
		if onSessions && !titled && outcome.errorMessage == "" {
			requestConversationTitle(conversationID)
			titled = true
		}
		fmt.Print("\n\n")
		resolvePendingProposals(reader, outcome.proposals)
	}
}

var chatHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "List chat conversation history",
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName, _ := cmd.Flags().GetString("cluster")
		limit, _ := cmd.Flags().GetInt("limit")

		var clusterID *string
		if clusterName != "" {
			cluster, err := apiClient.GetCluster(clusterName)
			if err != nil {
				return fmt.Errorf("finding cluster %s: %w", clusterName, err)
			}
			clusterID = &cluster.ID
		}

		resp, err := apiClient.ListChatHistory(clusterID, limit, 0)
		if err != nil {
			return fmt.Errorf("listing chat history: %w", err)
		}

		if handled, err := renderStructured(cmd, resp); err != nil {
			return err
		} else if handled {
			return nil
		}

		if len(resp.Conversations) == 0 {
			fmt.Println("No chat conversations found.")
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Title", "Created", "Updated"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 30},
			{Number: 3, WidthMin: 15},
			{Number: 4, WidthMin: 15},
		})

		for _, conv := range resp.Conversations {
			title := ""
			if conv.Title != nil {
				title = *conv.Title
			}
			if len(title) > 30 {
				title = title[:27] + "..."
			}
			t.AppendRow(table.Row{
				conv.ID,
				title,
				formatTimeAgo(conv.CreatedAt),
				formatTimeAgo(conv.UpdatedAt),
			})
		}
		t.Render()
		return nil
	},
}

var chatShowCmd = &cobra.Command{
	Use:   "show <conversation_id>",
	Short: "Show a specific chat conversation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conversationID := args[0]
		if err := validateConversationLookupID(conversationID); err != nil {
			return err
		}

		conv, err := apiClient.GetChatConversation(conversationID)
		if err != nil {
			return fmt.Errorf("getting conversation: %w", err)
		}

		if handled, err := renderStructured(cmd, conv); err != nil {
			return err
		} else if handled {
			return nil
		}

		if conv.Title != nil {
			fmt.Printf("Conversation: %s\n", *conv.Title)
		} else {
			fmt.Printf("Conversation: %s\n", conv.ID)
		}
		fmt.Printf("Created: %s\n", formatTimeAgo(conv.CreatedAt))
		fmt.Println()

		for _, msg := range conv.Messages {
			if msg.Role == "user" {
				fmt.Printf("%s: %s\n\n", text.FgCyan.Sprint("You"), msg.Content)
			} else {
				fmt.Printf("%s: %s\n\n", text.FgGreen.Sprint("Assistant"), msg.Content)
			}
		}
		return nil
	},
}

var chatDeleteCmd = &cobra.Command{
	Use:   "delete <conversation_id>",
	Short: "Delete a chat conversation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conversationID := args[0]
		if err := validateConversationLookupID(conversationID); err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete conversation %q? [y/N]: ", conversationID),
			yes); err != nil {
			return err
		}

		result, err := apiClient.DeleteChatConversation(conversationID)
		if err != nil {
			return fmt.Errorf("deleting conversation: %w", err)
		}

		if result.Success {
			fmt.Println("Conversation deleted successfully!")
		}
		return nil
	},
}

var chatHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Get AI-analyzed cluster health",
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		includeAI, _ := cmd.Flags().GetBool("ai")

		health, err := apiClient.GetClusterHealth(cluster.ID, includeAI)
		if err != nil {
			if clusterFlagOverride(cmd) == "" && isNotFoundResponse(err) {
				return fmt.Errorf("the selected cluster %q no longer exists; run 'ankra cluster clear' to drop the selection, or pass --cluster", cluster.Name)
			}
			return fmt.Errorf("getting cluster health: %w", err)
		}

		if handled, err := renderStructured(cmd, health); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Cluster Health for '%s'\n", cluster.Name)
		fmt.Println("─────────────────────────────────────────")

		report := health.HealthReport

		// Color code health status
		healthColor := text.FgGreen
		switch strings.ToLower(report.Status) {
		case "degraded", "warning":
			healthColor = text.FgYellow
		case "critical", "unhealthy":
			healthColor = text.FgRed
		}

		fmt.Printf("  Status: %s\n", healthColor.Sprint(report.Status))
		fmt.Printf("  Score:  %d/100\n", report.Score)
		fmt.Printf("  Last Updated: %s\n", formatTimeAgo(report.EvaluatedAt))
		if health.Summary != "" {
			fmt.Printf("  Summary: %s\n", health.Summary)
		}

		if len(report.Issues) > 0 {
			fmt.Println("\n  Issues:")
			for _, issue := range report.Issues {
				fmt.Printf("    - [%s] %s\n", issue.Severity, text.FgYellow.Sprint(issue.Title))
				for _, action := range issue.SuggestedActions {
					fmt.Printf("        · %s\n", action)
				}
			}
		}

		if len(health.AIInsights) > 0 {
			fmt.Println("\n  AI Insights:")
			for _, insight := range health.AIInsights {
				fmt.Printf("    - [%s] %s\n", insight.Severity, insight.Title)
				if insight.RootCauseAnalysis != "" {
					fmt.Printf("        Root cause: %s\n", insight.RootCauseAnalysis)
				}
			}
		}
		return nil
	},
}

func init() {
	chatCmd.Flags().String("cluster", "", "Cluster name for context")
	chatCmd.Flags().String("mode", "", "Safety mode: 'ask' (read-only + safe creations) or 'agent' (can act). Defaults to the server default.")
	chatCmd.Flags().String("conversation", "", "Continue an existing conversation (id from 'ankra chat history' or a previous answer)")

	chatHistoryCmd.Flags().String("cluster", "", "Filter by cluster")
	chatHistoryCmd.Flags().Int("limit", 20, "Maximum number of conversations to show")

	chatDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	chatHealthCmd.Flags().Bool("ai", true, "Include AI analysis")
	chatHealthCmd.Flags().String("cluster", "", "Target cluster name or ID (defaults to the selected cluster)")

	registerStructuredOutputFlags(chatHistoryCmd, chatShowCmd, chatHealthCmd)

	chatCmd.AddCommand(chatHistoryCmd)
	chatCmd.AddCommand(chatShowCmd)
	chatCmd.AddCommand(chatDeleteCmd)
	chatCmd.AddCommand(chatHealthCmd)

	rootCmd.AddCommand(chatCmd)
}

// validateConversationLookupID refuses a conversation id the history
// endpoints would answer 422 for, with the hint the raw status lacks.
func validateConversationLookupID(conversationID string) error {
	if looksLikeUUID(strings.TrimSpace(conversationID)) {
		return nil
	}
	return fmt.Errorf("invalid conversation id %q: pass the id shown by 'ankra chat history'", conversationID)
}

// isNotFoundResponse reports a 404 from the API, whichever error shape the
// client wrapped it in.
func isNotFoundResponse(err error) bool {
	if errors.Is(err, client.ErrClusterNotFound) {
		return true
	}
	var unexpected *client.UnexpectedResponseError
	return errors.As(err, &unexpected) && unexpected.StatusCode == http.StatusNotFound
}
