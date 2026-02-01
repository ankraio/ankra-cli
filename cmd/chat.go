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
	Run: func(cmd *cobra.Command, args []string) {
		clusterName, _ := cmd.Flags().GetString("cluster")

		var clusterID *string
		if clusterName != "" {
			cluster, err := client.GetCluster(apiToken, baseURL, clusterName)
			if err != nil {
				fmt.Printf("Error finding cluster %s: %v\n", clusterName, err)
				return
			}
			clusterID = &cluster.ID
		} else {
			// Try to use selected cluster
			selected, err := loadSelectedCluster()
			if err == nil {
				clusterID = &selected.ID
			}
		}

		if len(args) > 0 {
			// One-shot mode
			message := args[0]
			runChatMessage(clusterID, message)
		} else {
			// Interactive mode
			runInteractiveChat(clusterID)
		}
	},
}

func runChatMessage(clusterID *string, message string) {
	req := client.ChatRequest{Message: message}
	events, err := client.StreamChat(apiToken, baseURL, clusterID, req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Print("\n")
	for event := range events {
		switch event.Type {
		case "content":
			fmt.Print(event.Content)
		case "error":
			fmt.Printf("\nError: %s\n", event.Error)
		case "done":
			fmt.Print("\n\n")
		default:
			if event.Content != "" {
				fmt.Print(event.Content)
			}
		}
	}
}

func runInteractiveChat(clusterID *string) {
	fmt.Println("Ankra AI Chat")
	fmt.Println("─────────────")
	if clusterID != nil {
		fmt.Println("Cluster context: active")
	} else {
		fmt.Println("Cluster context: none (use --cluster to set)")
	}
	fmt.Println("Type 'exit' or 'quit' to exit, 'clear' to clear history")
	fmt.Println()

	var history []client.ChatMessage
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print(text.FgCyan.Sprint("You: "))
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("Error reading input: %v\n", err)
			break
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit", "quit", "q":
			fmt.Println("Goodbye!")
			return
		case "clear":
			history = nil
			fmt.Println("Chat history cleared.")
			continue
		}

		// Add user message to history
		history = append(history, client.ChatMessage{Role: "user", Content: input})

		req := client.ChatRequest{
			Message: input,
			History: history,
		}

		events, err := client.StreamChat(apiToken, baseURL, clusterID, req)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Print(text.FgGreen.Sprint("\nAssistant: "))
		var response strings.Builder
		for event := range events {
			switch event.Type {
			case "content":
				fmt.Print(event.Content)
				response.WriteString(event.Content)
			case "error":
				fmt.Printf("\nError: %s\n", event.Error)
			case "done":
				// Add assistant response to history
				if response.Len() > 0 {
					history = append(history, client.ChatMessage{Role: "assistant", Content: response.String()})
				}
			default:
				if event.Content != "" {
					fmt.Print(event.Content)
					response.WriteString(event.Content)
				}
			}
		}
		fmt.Print("\n\n")
	}
}

var chatHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "List chat conversation history",
	Run: func(cmd *cobra.Command, args []string) {
		clusterName, _ := cmd.Flags().GetString("cluster")
		limit, _ := cmd.Flags().GetInt("limit")

		var clusterID *string
		if clusterName != "" {
			cluster, err := client.GetCluster(apiToken, baseURL, clusterName)
			if err != nil {
				fmt.Printf("Error finding cluster %s: %v\n", clusterName, err)
				return
			}
			clusterID = &cluster.ID
		}

		resp, err := client.ListChatHistory(apiToken, baseURL, clusterID, limit, 0)
		if err != nil {
			fmt.Printf("Error listing chat history: %v\n", err)
			return
		}

		if len(resp.Conversations) == 0 {
			fmt.Println("No chat conversations found.")
			return
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
	},
}

var chatShowCmd = &cobra.Command{
	Use:   "show <conversation_id>",
	Short: "Show a specific chat conversation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		conversationID := args[0]

		conv, err := client.GetChatConversation(apiToken, baseURL, conversationID)
		if err != nil {
			fmt.Printf("Error getting conversation: %v\n", err)
			return
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
	},
}

var chatDeleteCmd = &cobra.Command{
	Use:   "delete <conversation_id>",
	Short: "Delete a chat conversation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		conversationID := args[0]

		result, err := client.DeleteChatConversation(apiToken, baseURL, conversationID)
		if err != nil {
			fmt.Printf("Error deleting conversation: %v\n", err)
			return
		}

		if result.Success {
			fmt.Println("Conversation deleted successfully!")
		}
	},
}

var chatHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Get AI-analyzed cluster health",
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra select cluster' to pick one.")
			return
		}

		includeAI, _ := cmd.Flags().GetBool("ai")

		health, err := client.GetClusterHealth(apiToken, baseURL, cluster.ID, includeAI)
		if err != nil {
			fmt.Printf("Error getting cluster health: %v\n", err)
			return
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
	},
}

func init() {
	chatCmd.Flags().String("cluster", "", "Cluster name for context")

	chatHistoryCmd.Flags().String("cluster", "", "Filter by cluster")
	chatHistoryCmd.Flags().Int("limit", 20, "Maximum number of conversations to show")

	chatHealthCmd.Flags().Bool("ai", true, "Include AI analysis")

	chatCmd.AddCommand(chatHistoryCmd)
	chatCmd.AddCommand(chatShowCmd)
	chatCmd.AddCommand(chatDeleteCmd)
	chatCmd.AddCommand(chatHealthCmd)

	rootCmd.AddCommand(chatCmd)
}
