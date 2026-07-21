package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat [message]",
	Short: "AI-powered chat for troubleshooting and assistance",
	Long: `AI-powered chat for troubleshooting and assistance.

If a message is provided, sends a one-shot question.
If no message is provided, enters interactive chat mode.

Use --cluster to provide cluster context for better answers.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName, _ := cmd.Flags().GetString("cluster")
		mode, _ := cmd.Flags().GetString("mode")
		interactionMode, modeError := normalizeChatMode(mode)
		if modeError != nil {
			return modeError
		}

		var clusterID *string
		if clusterName != "" {
			cluster, err := apiClient.GetCluster(clusterName)
			if err != nil {
				return fmt.Errorf("finding cluster %s: %w", clusterName, err)
			}
			clusterID = &cluster.ID
		} else {
			selected, err := loadSelectedCluster()
			if err == nil {
				clusterID = &selected.ID
			}
		}

		if len(args) > 0 {
			return runChatMessage(clusterID, args[0], interactionMode)
		}
		return runInteractiveChat(clusterID, interactionMode)
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

func runChatMessage(clusterID *string, query string, interactionMode string) error {
	req := client.ChatRequest{Query: query, InteractionMode: interactionMode}
	events, err := apiClient.StreamChat(clusterID, req)
	if err != nil {
		return fmt.Errorf("chat: %w", err)
	}

	fmt.Print("\n")
	var hasStartedContent bool
	var hadStatus bool
	for event := range events {
		switch event.Type {
		case "content":
			// Data can be string content
			if str, ok := event.Data.(string); ok {
				if !hasStartedContent {
					if hadStatus {
						fmt.Print("\n") // New line after status before content
					}
					hasStartedContent = true
				}
				fmt.Print(str)
			} else if event.Content != "" {
				if !hasStartedContent {
					if hadStatus {
						fmt.Print("\n")
					}
					hasStartedContent = true
				}
				fmt.Print(event.Content)
			}
		case "status":
			// Show status
			if str, ok := event.Data.(string); ok {
				if hasStartedContent {
					fmt.Printf("\n\n[%s]\n\n", str)
				} else {
					fmt.Printf("[%s]", str)
					hadStatus = true
				}
			}
		case "error":
			fmt.Printf("\nError: %s\n", event.Error)
		case "done", "complete":
			fmt.Print("\n\n")
		default:
			// Ignore triage, context and other metadata events
		}
	}
	return nil
}

func runInteractiveChat(clusterID *string, interactionMode string) error {
	fmt.Println("Ankra AI Chat")
	fmt.Println("─────────────")
	if clusterID != nil {
		fmt.Println("Cluster context: active")
	} else {
		fmt.Println("Cluster context: none (use --cluster to set)")
	}
	switch interactionMode {
	case "ask":
		fmt.Println("Mode: ask (read-only + safe creations)")
	case "agentic":
		fmt.Println("Mode: agent (can act)")
	}
	fmt.Println("Type 'exit' or 'quit' to exit, 'clear' to clear history")
	fmt.Println()

	var history []client.ChatMessage
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print(text.FgCyan.Sprint("You: "))
		input, err := reader.ReadString('\n')
		if err != nil {
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
			fmt.Println("Chat history cleared.")
			continue
		}

		// Add user message to history
		history = append(history, client.ChatMessage{Role: "user", Content: input})

		req := client.ChatRequest{
			Query:               input,
			ConversationHistory: history,
			InteractionMode:     interactionMode,
		}

		events, err := apiClient.StreamChat(clusterID, req)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Print(text.FgGreen.Sprint("\nAssistant: "))
		var response strings.Builder
		var hasStartedContent bool
		var hadStatus bool
		for event := range events {
			switch event.Type {
			case "content":
				// Data can be string content
				if str, ok := event.Data.(string); ok {
					if !hasStartedContent {
						if hadStatus {
							fmt.Print("\n") // New line after status before content
						}
						hasStartedContent = true
					}
					fmt.Print(str)
					response.WriteString(str)
				} else if event.Content != "" {
					if !hasStartedContent {
						if hadStatus {
							fmt.Print("\n")
						}
						hasStartedContent = true
					}
					fmt.Print(event.Content)
					response.WriteString(event.Content)
				}
			case "status":
				// Show status, don't add to response
				if str, ok := event.Data.(string); ok {
					if hasStartedContent {
						fmt.Printf("\n\n[%s]\n\n", str)
					} else {
						fmt.Printf("[%s]", str)
						hadStatus = true
					}
				}
			case "error":
				fmt.Printf("\nError: %s\n", event.Error)
			case "done", "complete":
				// Add assistant response to history
				if response.Len() > 0 {
					history = append(history, client.ChatMessage{Role: "assistant", Content: response.String()})
				}
			default:
				// Ignore triage, context and other metadata events
			}
		}
		fmt.Print("\n\n")
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
			return fmt.Errorf("getting cluster health: %w", err)
		}

		if handled, err := renderStructured(cmd, health); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Cluster Health for '%s'\n", cluster.Name)
		fmt.Println("─────────────────────────────────────────")

		// Color code health status
		healthColor := text.FgGreen
		switch strings.ToLower(health.OverallHealth) {
		case "degraded", "warning":
			healthColor = text.FgYellow
		case "critical", "unhealthy":
			healthColor = text.FgRed
		}

		fmt.Printf("  Status: %s\n", healthColor.Sprint(health.OverallHealth))
		fmt.Printf("  Score:  %d/100\n", health.Score)
		fmt.Printf("  Last Updated: %s\n", formatTimeAgo(health.LastUpdated))

		if len(health.Issues) > 0 {
			fmt.Println("\n  Issues:")
			for _, issue := range health.Issues {
				fmt.Printf("    - %s\n", text.FgYellow.Sprint(issue))
			}
		}

		if len(health.Recommendations) > 0 {
			fmt.Println("\n  Recommendations:")
			for _, rec := range health.Recommendations {
				fmt.Printf("    - %s\n", rec)
			}
		}
		return nil
	},
}

func init() {
	chatCmd.Flags().String("cluster", "", "Cluster name for context")
	chatCmd.Flags().String("mode", "", "Safety mode: 'ask' (read-only + safe creations) or 'agent' (can act). Defaults to the server default.")

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
