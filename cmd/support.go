package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const supportRequestTimeout = 120 * time.Second

var supportCmd = &cobra.Command{
	Use:   "support",
	Short: "Create and track Ankra support requests",
	Long: `Create and track support requests for your organisation.

Every request is reviewed by Ankra AI before it reaches the team, which may
enrich it or flag low-quality submissions. Use --force on create to submit a
flagged request anyway.

  ankra support create --subject "Nodes NotReady" --description "..." --cluster prod
  ankra support list
  ankra support get <ticket-id>
  ankra support comment <ticket-id> --message "Any update?"
  ankra support close <ticket-id>`,
}

var supportCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a support request",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		subject, _ := cmd.Flags().GetString("subject")
		description, _ := cmd.Flags().GetString("description")
		category, _ := cmd.Flags().GetString("category")
		severity, _ := cmd.Flags().GetString("severity")
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		source, _ := cmd.Flags().GetString("source")
		force, _ := cmd.Flags().GetBool("force")
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		if subject == "" || description == "" {
			return errors.New("--subject and --description are required")
		}

		request := client.CreateSupportTicketRequest{
			Subject:      subject,
			Description:  description,
			Category:     category,
			Source:       source,
			Acknowledged: force,
		}
		if severity != "" {
			request.Severity = &severity
		}
		if clusterFlag != "" {
			clusterID, resolveErr := resolveClusterID(clusterFlag)
			if resolveErr != nil {
				return resolveErr
			}
			request.ClusterID = &clusterID
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), supportRequestTimeout)
		defer cancel()

		ticket, err := apiClient.CreateSupportTicket(ctx, request)
		if err != nil {
			if errors.Is(err, client.ErrSupportReviewRequired) {
				return fmt.Errorf("%w", err)
			}
			return fmt.Errorf("create support ticket: %w", err)
		}
		return renderTicket(cmd.OutOrStdout(), ticket, out)
	},
}

var supportListCmd = &cobra.Command{
	Use:   "list",
	Short: "List support requests",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		status, _ := cmd.Flags().GetStringSlice("status")
		query, _ := cmd.Flags().GetString("query")
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		list, err := apiClient.ListSupportTickets(ctx, client.ListSupportTicketsOptions{
			Page:     page,
			PageSize: pageSize,
			Status:   status,
			Query:    query,
		})
		if err != nil {
			return fmt.Errorf("list support tickets: %w", err)
		}
		return renderTicketList(cmd.OutOrStdout(), list, out)
	},
}

var supportGetCmd = &cobra.Command{
	Use:   "get <ticket-id>",
	Short: "Show a support request and its replies",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		ticket, err := apiClient.GetSupportTicket(ctx, args[0])
		if err != nil {
			if errors.Is(err, client.ErrSupportTicketNotFound) {
				return fmt.Errorf("support ticket %q not found", args[0])
			}
			return fmt.Errorf("get support ticket: %w", err)
		}
		return renderTicket(cmd.OutOrStdout(), ticket, out)
	},
}

var supportCommentCmd = &cobra.Command{
	Use:   "comment <ticket-id>",
	Short: "Add a reply to a support request",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		message, _ := cmd.Flags().GetString("message")
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		if message == "" {
			return errors.New("--message is required")
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		ticket, err := apiClient.CommentSupportTicket(ctx, args[0], message)
		if err != nil {
			if errors.Is(err, client.ErrSupportTicketNotFound) {
				return fmt.Errorf("support ticket %q not found", args[0])
			}
			return fmt.Errorf("comment on support ticket: %w", err)
		}
		return renderTicket(cmd.OutOrStdout(), ticket, out)
	},
}

var supportAttachCmd = &cobra.Command{
	Use:   "attach <ticket-id> <file>...",
	Short: "Attach screenshots or images to a support request",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ticketID := args[0]
		files := args[1:]
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), supportRequestTimeout)
		defer cancel()

		var ticket *client.SupportTicket
		for _, file := range files {
			ticket, err = apiClient.UploadSupportAttachment(ctx, ticketID, file)
			if err != nil {
				if errors.Is(err, client.ErrSupportTicketNotFound) {
					return fmt.Errorf("support ticket %q not found", ticketID)
				}
				return fmt.Errorf("attach %q: %w", file, err)
			}
		}
		return renderTicket(cmd.OutOrStdout(), ticket, out)
	},
}

var supportCloseCmd = &cobra.Command{
	Use:   "close <ticket-id>",
	Short: "Close a support request",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Close support ticket %q? [y/N]: ", args[0]),
			yes,
		); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		if _, err := apiClient.CloseSupportTicket(ctx, args[0]); err != nil {
			if errors.Is(err, client.ErrSupportTicketNotFound) {
				return fmt.Errorf("support ticket %q not found", args[0])
			}
			return fmt.Errorf("close support ticket: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Support ticket %q closed.\n", args[0])
		return nil
	},
}

func renderTicketList(out io.Writer, list *client.SupportTicketListResponse, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(list)
	}
	if list == nil || len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No support requests.")
		return nil
	}
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"ID", "SUBJECT", "STATUS", "SEVERITY", "TRACKED", "CREATED"})
	for _, ticket := range list.Result {
		t.AppendRow(table.Row{
			ticket.ID,
			truncateForDisplay(ticket.Subject, 50),
			ticket.Status,
			derefString(ticket.Severity),
			boolToYesNo(ticket.IsTrackedByTeam),
			ticket.CreatedAt.Format(time.RFC3339),
		})
	}
	t.Render()
	return nil
}

func renderTicket(out io.Writer, ticket *client.SupportTicket, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(ticket)
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(ticket)
	}
	if ticket == nil {
		_, _ = fmt.Fprintln(out, "No ticket.")
		return nil
	}
	_, _ = fmt.Fprintf(out,
		"ID:       %s\nSubject:  %s\nStatus:   %s\nCategory: %s\nSeverity: %s\nTracked:  %s\nCreated:  %s\n\n%s\n",
		ticket.ID, ticket.Subject, ticket.Status, ticket.Category, derefString(ticket.Severity),
		boolToYesNo(ticket.IsTrackedByTeam), ticket.CreatedAt.Format(time.RFC3339), ticket.Description,
	)
	if ticket.IsTrackedByTeam {
		_, _ = fmt.Fprintln(out, "\nThis issue is being tracked by the Ankra team.")
	}
	if len(ticket.Comments) > 0 {
		_, _ = fmt.Fprintln(out, "\nReplies:")
		for _, comment := range ticket.Comments {
			_, _ = fmt.Fprintf(out, "  [%s] %s: %s\n",
				comment.CreatedAt.Format(time.RFC3339), comment.AuthorLabel, comment.Body)
		}
	}
	return nil
}

func derefString(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

func boolToYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func init() {
	supportCreateCmd.Flags().String("subject", "", "Short summary of the issue (required)")
	supportCreateCmd.Flags().String("description", "", "Detailed description of the issue (required)")
	supportCreateCmd.Flags().String("category", "technical", "Category: technical, account, billing, bug, feature_request, other")
	supportCreateCmd.Flags().String("severity", "", "Optional severity: low, medium, high, critical")
	supportCreateCmd.Flags().String("cluster", "", "Optional related cluster (name or ID)")
	supportCreateCmd.Flags().String("source", "cli", "Origin of the request: cli or agent")
	supportCreateCmd.Flags().Bool("force", false, "Submit even if the AI review flags the request")
	supportCreateCmd.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")

	supportListCmd.Flags().StringSlice("status", nil, "Filter by status (repeatable)")
	supportListCmd.Flags().StringP("query", "q", "", "Filter by subject text")
	supportListCmd.Flags().Int("page", 1, "Page number")
	supportListCmd.Flags().Int("page-size", 25, "Page size")
	supportListCmd.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")

	supportGetCmd.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")

	supportCommentCmd.Flags().StringP("message", "m", "", "Reply message (required)")
	supportCommentCmd.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")

	supportAttachCmd.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")

	supportCloseCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	supportCmd.AddCommand(supportCreateCmd)
	supportCmd.AddCommand(supportListCmd)
	supportCmd.AddCommand(supportGetCmd)
	supportCmd.AddCommand(supportCommentCmd)
	supportCmd.AddCommand(supportAttachCmd)
	supportCmd.AddCommand(supportCloseCmd)
	rootCmd.AddCommand(supportCmd)
}
