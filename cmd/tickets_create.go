package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"
	"ankra/internal/hiddenunicode"

	"github.com/spf13/cobra"
)

var ticketsCreateCmd = &cobra.Command{
	Use:   "create --title <title>",
	Short: "File a ticket on the board, or a sub-ticket under one",
	Long: `File a ticket on the AI board. --kind defaults to request and --priority to
medium. With --parent the ticket is filed as a sub-ticket of an open ticket:
it is worked like any ticket and counts toward its parent's sub-tickets. A
sub-ticket takes no --cluster or --staffing of its own.

To put an existing ticket under a root whose work covers it, use
"ankra tickets nest" instead: a symptom gets no agent run of its own.`,
	Example: `  ankra tickets create --title "Rotate the registry robot token" --kind chore --priority high
  ankra tickets create --title "Ingress cert expires Friday" --kind incident --cluster prod-hel1 --label cert
  ankra tickets create --title "Renew the wildcard certificate" --parent T-42
  ankra tickets create --title "Track the vendor fix" --staffing manual`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		title, _ := cmd.Flags().GetString("title")
		body, _ := cmd.Flags().GetString("body")
		kind, _ := cmd.Flags().GetString("kind")
		priority, _ := cmd.Flags().GetString("priority")
		labels, _ := cmd.Flags().GetStringSlice("label")
		parentReference, _ := cmd.Flags().GetString("parent")
		clusterReference, _ := cmd.Flags().GetString("cluster")
		staffing, _ := cmd.Flags().GetString("staffing")

		title = strings.TrimSpace(title)
		if title == "" {
			return withExitCode(exitUsage, errors.New("--title is required"))
		}
		parentReference = strings.TrimSpace(parentReference)
		clusterReference = strings.TrimSpace(clusterReference)
		staffing = strings.TrimSpace(staffing)
		if parentReference != "" && (clusterReference != "" || staffing != "") {
			return withExitCode(exitUsage, errors.New(
				"--cluster and --staffing apply to a top-level ticket; a sub-ticket filed with --parent takes neither"))
		}
		// An AI agent reads the ticket, so it does not carry invisible runes
		// to it (ankra-4r75g.9).
		hidden := 0
		title, removed := hiddenunicode.Strip(title)
		hidden += removed
		request := client.TicketCreateRequest{Title: title}
		if trimmed := strings.TrimSpace(body); trimmed != "" {
			cleaned, bodyRemoved := hiddenunicode.Strip(trimmed)
			hidden += bodyRemoved
			request.Body = &cleaned
		}
		if hidden > 0 {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", hiddenunicode.Notice(hidden))
		}
		if trimmed := strings.TrimSpace(kind); trimmed != "" {
			request.Kind = &trimmed
		}
		if trimmed := strings.TrimSpace(priority); trimmed != "" {
			request.Priority = &trimmed
		}
		for _, label := range labels {
			if trimmed := strings.TrimSpace(label); trimmed != "" {
				request.Labels = append(request.Labels, trimmed)
			}
		}

		var created *client.Ticket
		var parent *client.Ticket
		if parentReference != "" {
			resolved, err := resolveTicketReference(parentReference)
			if err != nil {
				return err
			}
			parent = resolved
			created, err = apiClient.CreateSubTicket(parent.ID, request)
			if err != nil {
				return ticketWriteError(err)
			}
		} else {
			if clusterReference != "" {
				clusterID, err := resolveClusterArg(clusterReference)
				if err != nil {
					return err
				}
				request.ClusterID = &clusterID
			}
			if staffing != "" {
				request.Staffing = &staffing
			}
			var err error
			created, err = apiClient.CreateTicket(request)
			if err != nil {
				return ticketWriteError(err)
			}
		}
		if handled, renderError := renderStructured(cmd, created); handled || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Filed T-%d: %s (%s, %s).\n", created.TicketNumber, created.Title, created.Kind, created.Priority)
		if parent != nil {
			_, _ = fmt.Fprintf(out, "It is a sub-ticket of T-%d %q.\n", parent.TicketNumber, parent.Title)
		}
		return nil
	},
}

func init() {
	ticketsCreateCmd.Flags().String("title", "", "The ticket's title")
	ticketsCreateCmd.Flags().String("body", "", "The ticket's description (markdown)")
	ticketsCreateCmd.Flags().String("kind", "", "incident, insight, gap, automation, request (the default) or chore")
	ticketsCreateCmd.Flags().String("priority", "", "urgent, high, medium (the default) or low")
	ticketsCreateCmd.Flags().StringSlice("label", nil, "A label to put on the ticket (repeat or comma-separate for more)")
	ticketsCreateCmd.Flags().String("parent", "", "File it as a sub-ticket of this open ticket (42, T-42 or its UUID)")
	ticketsCreateCmd.Flags().String("cluster", "", "The cluster the ticket is about (name or id); top-level tickets only")
	ticketsCreateCmd.Flags().String("staffing", "",
		"auto (the default: the AI team may staff it) or manual (a person owns it); top-level tickets only")
	registerStructuredOutputFlags(ticketsCreateCmd)
	ticketsCmd.AddCommand(ticketsCreateCmd)
}
