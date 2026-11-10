package cmd

// `ankra security evidence destinations|deliveries ...` (ankra-hq41xb.15):
// the CLI surface over /api/v1/org/security/evidence/destinations and
// /deliveries, which send the organisation's security evidence (a framework
// report and the SBOM image inventory) to a Vanta document, a Wazuh relay or
// a signed webhook.
//
// A destination's credential is never a flag: it comes from stdin
// (--secret-stdin) or a masked prompt, and it is never printed, including
// in an error. The platform never returns one.

import (
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var securityEvidenceCmd = &cobra.Command{
	Use:   "evidence",
	Short: "Send security evidence to Vanta, a Wazuh relay or a signed webhook",
	Long: `Security evidence is a bounded snapshot of what the Security Center knows: a
compliance framework's report when one is asked for, and the SBOM image
inventory with known-exploited vulnerabilities first.

A destination is where evidence goes: a Vanta document, a Wazuh relay or a
webhook that receives signed requests. A delivery is one snapshot sent to one
destination. For Vanta, the upload and the submission are separate steps: a
delivery reads "uploaded" until 'deliveries submit' submits the document.`,
}

var securityEvidenceDestinationsCmd = &cobra.Command{
	Use:     "destinations",
	Aliases: []string{"destination"},
	Short:   "Manage where security evidence is sent",
}

var securityEvidenceDeliveriesCmd = &cobra.Command{
	Use:     "deliveries",
	Aliases: []string{"delivery"},
	Short:   "Send security evidence and follow its deliveries",
}

func evidenceDestinationTarget(destination client.SecurityEvidenceDestination) string {
	if destination.Config.URL != "" {
		return destination.Config.URL
	}
	if destination.Config.DocumentID != "" {
		return "document " + destination.Config.DocumentID
	}
	return "-"
}

func evidenceDestinationState(destination client.SecurityEvidenceDestination) string {
	switch {
	case destination.RevokedAt != nil:
		return "revoked"
	case destination.Enabled:
		return "enabled"
	default:
		return "disabled"
	}
}

func printEvidenceDestination(out io.Writer, destination client.SecurityEvidenceDestination) {
	credential := "not held"
	if destination.CredentialConfigured {
		credential = "held by the platform (never shown)"
	}
	_, _ = fmt.Fprintf(out, "Destination: %s (%s)\n", destination.Name, destination.ID)
	_, _ = fmt.Fprintf(out, "  Kind:       %s\n", destination.Kind)
	_, _ = fmt.Fprintf(out, "  Target:     %s\n", evidenceDestinationTarget(destination))
	_, _ = fmt.Fprintf(out, "  State:      %s\n", evidenceDestinationState(destination))
	_, _ = fmt.Fprintf(out, "  Credential: %s\n", credential)
}

var securityEvidenceDestinationsListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the organisation's evidence destinations",
	Long:    `List the organisation's evidence destinations, revoked ones included. Credentials are never returned.`,
	Example: `  ankra security evidence destinations list
  ankra security evidence destinations list -o json`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		destinations, listError := apiClient.ListSecurityEvidenceDestinations(command.Context())
		if listError != nil {
			return fmt.Errorf("listing evidence destinations: %w", listError)
		}
		if rendered, renderError := renderStructured(command, destinations); rendered || renderError != nil {
			return renderError
		}
		out := command.OutOrStdout()
		if len(destinations) == 0 {
			_, _ = fmt.Fprintln(out, "No evidence destinations. Create one with 'ankra security evidence destinations create'.")
			return nil
		}
		rows := make([]table.Row, 0, len(destinations))
		for _, destination := range destinations {
			rows = append(rows, table.Row{destination.ID, destination.Name, destination.Kind,
				evidenceDestinationTarget(destination), evidenceDestinationState(destination)})
		}
		renderRoundedTable(out, table.Row{"ID", "Name", "Kind", "Target", "State"}, rows)
		return nil
	},
}

var securityEvidenceDestinationsCreateCmd = &cobra.Command{
	Use:   "create --name <name> --kind vanta|wazuh|webhook",
	Short: "Create an evidence destination",
	Long: `Create an evidence destination. What it needs depends on --kind:

  webhook  --url (https). The secret is the signing secret requests are signed with.
  wazuh    --url (https) of the relay. The secret is the relay's bearer token.
  vanta    --document-id, --client-id and an optional --api-base-url (https).
           The secret is the client secret of the Vanta application.

The secret is never taken as an argument. Pipe it on stdin with --secret-stdin,
or run in a terminal to be asked for it with the input hidden. The platform
stores it in its secret store and never returns it.`,
	Example: `  cat signing-secret.txt | ankra security evidence destinations create --name siem-webhook --kind webhook \
    --url https://siem.example.com/ankra --secret-stdin
  ankra security evidence destinations create --name vanta --kind vanta --document-id <id> --client-id <id>`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		name, nameError := requiredTextFlag(command, "name", "the destination's name")
		if nameError != nil {
			return nameError
		}
		kind, _ := command.Flags().GetString("kind")
		destinationURL, _ := command.Flags().GetString("url")
		documentID, _ := command.Flags().GetString("document-id")
		apiBaseURL, _ := command.Flags().GetString("api-base-url")
		clientID, _ := command.Flags().GetString("client-id")
		request := client.CreateSecurityEvidenceDestinationRequest{Name: name, Kind: strings.TrimSpace(kind)}
		secretLabel, secretKey := "", ""
		switch request.Kind {
		case "webhook", "wazuh":
			if strings.TrimSpace(destinationURL) == "" {
				return withExitCode(exitUsage, fmt.Errorf("--url is required for a %s destination (https)", request.Kind))
			}
			if documentID != "" || apiBaseURL != "" || clientID != "" {
				return withExitCode(exitUsage, fmt.Errorf("--document-id, --api-base-url and --client-id belong to a vanta destination, not %s", request.Kind))
			}
			request.Config.URL = strings.TrimSpace(destinationURL)
			secretLabel, secretKey = "signing secret", "signing_secret"
			if request.Kind == "wazuh" {
				secretLabel, secretKey = "bearer token", "bearer_token"
			}
		case "vanta":
			if strings.TrimSpace(documentID) == "" || strings.TrimSpace(clientID) == "" {
				return withExitCode(exitUsage, fmt.Errorf("--document-id and --client-id are required for a vanta destination"))
			}
			if destinationURL != "" {
				return withExitCode(exitUsage, fmt.Errorf("--url belongs to a webhook or wazuh destination; a vanta destination takes --api-base-url"))
			}
			request.Config.DocumentID = strings.TrimSpace(documentID)
			request.Config.APIBaseURL = strings.TrimSpace(apiBaseURL)
			secretLabel, secretKey = "client secret", "client_secret"
		default:
			return withExitCode(exitUsage, fmt.Errorf("--kind must be vanta, wazuh or webhook, got %q", kind))
		}
		secret, secretError := readSecretFromStdinOrPrompt(command, "secret-stdin", secretLabel)
		if secretError != nil {
			return secretError
		}
		request.Credential = map[string]string{secretKey: secret}
		if request.Kind == "vanta" {
			request.Credential["client_id"] = strings.TrimSpace(clientID)
		}
		destination, createError := apiClient.CreateSecurityEvidenceDestination(command.Context(), request)
		if createError != nil {
			return redactSecret(fmt.Errorf("creating evidence destination %q: %w", name, createError), secret)
		}
		if rendered, renderError := renderStructured(command, destination); rendered || renderError != nil {
			return renderError
		}
		printEvidenceDestination(command.OutOrStdout(), *destination)
		return nil
	},
}

var securityEvidenceDestinationsRevokeCmd = &cobra.Command{
	Use:   "revoke <destination-id>",
	Short: "Revoke an evidence destination and delete its credential",
	Long: `Revoke a destination: the platform disables it, deletes its credential and
cancels its deliveries that have not been delivered. The destination stays in
the list as revoked, for the history, and cannot be enabled again. It asks
first; --yes skips the question and is required when there is no terminal to
ask on.`,
	Example: `  ankra security evidence destinations revoke 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --yes`,
	Args:    cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		destinationID := strings.TrimSpace(arguments[0])
		if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
			"Revoking evidence destination %s deletes its credential and cancels its undelivered deliveries. It cannot be undone.",
			destinationID)); confirmError != nil {
			return confirmError
		}
		destination, revokeError := apiClient.RevokeSecurityEvidenceDestination(command.Context(), destinationID)
		if revokeError != nil {
			return fmt.Errorf("revoking evidence destination %s: %w", destinationID, revokeError)
		}
		if rendered, renderError := renderStructured(command, destination); rendered || renderError != nil {
			return renderError
		}
		printEvidenceDestination(command.OutOrStdout(), *destination)
		return nil
	},
}

func printEvidenceDelivery(out io.Writer, delivery client.SecurityEvidenceDelivery) {
	_, _ = fmt.Fprintf(out, "Delivery: %s\n", delivery.ID)
	_, _ = fmt.Fprintf(out, "  Destination: %s\n", delivery.DestinationID)
	_, _ = fmt.Fprintf(out, "  Status:      %s\n", delivery.Status)
	_, _ = fmt.Fprintf(out, "  Period:      %s\n", delivery.Period)
	_, _ = fmt.Fprintf(out, "  Framework:   %s\n", optionalText(delivery.Framework))
	_, _ = fmt.Fprintf(out, "  Attempts:    %d\n", delivery.Attempts)
	if !delivery.Complete {
		_, _ = fmt.Fprintln(out, "  Snapshot:    incomplete (the snapshot was bounded and does not hold everything)")
	}
	if delivery.LastError != nil && *delivery.LastError != "" {
		_, _ = fmt.Fprintf(out, "  Last error:  %s\n", *delivery.LastError)
	}
	switch delivery.Status {
	case "uploaded":
		_, _ = fmt.Fprintf(out, "\nThe document is a draft in Vanta. Submit it with 'ankra security evidence deliveries submit %s'.\n", delivery.ID)
	case "delivered":
		_, _ = fmt.Fprintln(out, "\nDelivered means the destination accepted the transport, not that it decoded or indexed the evidence.")
	}
}

var securityEvidenceDeliveriesListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List evidence deliveries, newest first",
	Long: `List the organisation's evidence deliveries newest first (at most 100), for
every destination or for one with --destination.`,
	Example: `  ankra security evidence deliveries list
  ankra security evidence deliveries list --destination 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --limit 20`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		destinationID, _ := command.Flags().GetString("destination")
		limit, _ := command.Flags().GetInt("limit")
		if limit < 0 || limit > 100 {
			return withExitCode(exitUsage, fmt.Errorf("--limit is between 1 and 100"))
		}
		deliveries, listError := apiClient.ListSecurityEvidenceDeliveries(command.Context(), strings.TrimSpace(destinationID), limit)
		if listError != nil {
			return fmt.Errorf("listing evidence deliveries: %w", listError)
		}
		if rendered, renderError := renderStructured(command, deliveries); rendered || renderError != nil {
			return renderError
		}
		out := command.OutOrStdout()
		if len(deliveries) == 0 {
			_, _ = fmt.Fprintln(out, "No evidence deliveries. Send one with 'ankra security evidence deliveries send'.")
			return nil
		}
		rows := make([]table.Row, 0, len(deliveries))
		for _, delivery := range deliveries {
			rows = append(rows, table.Row{delivery.ID, delivery.DestinationID, delivery.Status, delivery.Period,
				optionalText(delivery.Framework), delivery.Attempts, formatTimeAgo(delivery.CreatedAt)})
		}
		renderRoundedTable(out, table.Row{"ID", "Destination", "Status", "Period", "Framework", "Attempts", "Created"}, rows)
		return nil
	},
}

var securityEvidenceDeliveriesSendCmd = &cobra.Command{
	Use:   "send --destination <destination-id>",
	Short: "Capture one evidence snapshot and send it to a destination",
	Long: `Capture one bounded evidence snapshot and queue it for the destination, due now:
the SBOM image inventory, known-exploited vulnerabilities first (at most 2,000
images), and with --framework that framework's report. --month YYYY-MM sends a
recorded month instead of current observations. A disabled framework or a
month that was not recorded is refused.

The command returns once the delivery is queued; follow it with
'ankra security evidence deliveries list'.`,
	Example: `  ankra security evidence deliveries send --destination 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c
  ankra security evidence deliveries send --destination <id> --framework iso27001 --month 2026-10`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		destinationID, destinationError := requiredTextFlag(command, "destination", "the destination's id ('ankra security evidence destinations list')")
		if destinationError != nil {
			return destinationError
		}
		request := client.SendSecurityEvidenceRequest{DestinationID: destinationID}
		if framework, _ := command.Flags().GetString("framework"); strings.TrimSpace(framework) != "" {
			trimmed := strings.TrimSpace(framework)
			request.Framework = &trimmed
		}
		if month, _ := command.Flags().GetString("month"); strings.TrimSpace(month) != "" {
			trimmed := strings.TrimSpace(month)
			request.Month = &trimmed
		}
		delivery, sendError := apiClient.SendSecurityEvidence(command.Context(), request)
		if sendError != nil {
			return fmt.Errorf("sending security evidence: %w", sendError)
		}
		if rendered, renderError := renderStructured(command, delivery); rendered || renderError != nil {
			return renderError
		}
		printEvidenceDelivery(command.OutOrStdout(), *delivery)
		return nil
	},
}

var securityEvidenceDeliveriesSubmitCmd = &cobra.Command{
	Use:   "submit <delivery-id>",
	Short: "Submit the Vanta document an uploaded delivery put there",
	Long: `Submit the Vanta evidence document an uploaded delivery put there. Upload and
submission are separate: a delivery to Vanta reads "uploaded", a draft, until
it is submitted. A refused or uncertain submission leaves the delivery uploaded
and says why.`,
	Example: `  ankra security evidence deliveries submit 0b6f1c1e-7c0e-4b7e-9a57-0d5f3c1a2b3c`,
	Args:    cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		deliveryID := strings.TrimSpace(arguments[0])
		delivery, submitError := apiClient.SubmitSecurityEvidenceDelivery(command.Context(), deliveryID)
		if submitError != nil {
			return fmt.Errorf("submitting evidence delivery %s: %w", deliveryID, submitError)
		}
		if rendered, renderError := renderStructured(command, delivery); rendered || renderError != nil {
			return renderError
		}
		printEvidenceDelivery(command.OutOrStdout(), *delivery)
		if delivery.Status != "submitted" {
			return withExitCode(exitError, fmt.Errorf(
				"delivery %s was not submitted: it is still %s; see its last error above", deliveryID, delivery.Status))
		}
		return nil
	},
}

func init() {
	registerStructuredOutputFlags(securityEvidenceDestinationsListCmd, securityEvidenceDestinationsCreateCmd,
		securityEvidenceDestinationsRevokeCmd, securityEvidenceDeliveriesListCmd, securityEvidenceDeliveriesSendCmd,
		securityEvidenceDeliveriesSubmitCmd)

	securityEvidenceDestinationsCreateCmd.Flags().String("name", "", "Destination name (required)")
	securityEvidenceDestinationsCreateCmd.Flags().String("kind", "", "vanta, wazuh or webhook (required)")
	securityEvidenceDestinationsCreateCmd.Flags().String("url", "", "The https URL of a webhook or Wazuh relay")
	securityEvidenceDestinationsCreateCmd.Flags().String("document-id", "", "The Vanta document evidence is uploaded to")
	securityEvidenceDestinationsCreateCmd.Flags().String("api-base-url", "", "A Vanta API base URL other than the default (https)")
	securityEvidenceDestinationsCreateCmd.Flags().String("client-id", "", "The client id of the Vanta application")
	securityEvidenceDestinationsCreateCmd.Flags().Bool("secret-stdin", false, "Read the signing secret, bearer token or client secret from stdin")
	securityEvidenceDestinationsRevokeCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")

	securityEvidenceDeliveriesListCmd.Flags().String("destination", "", "Only this destination's deliveries (its id)")
	securityEvidenceDeliveriesListCmd.Flags().Int("limit", 0, "How many deliveries to list (1 to 100; the platform's default when omitted)")
	securityEvidenceDeliveriesSendCmd.Flags().String("destination", "", "The destination's id (required)")
	securityEvidenceDeliveriesSendCmd.Flags().String("framework", "", "Add this compliance framework's report")
	securityEvidenceDeliveriesSendCmd.Flags().String("month", "", "Send a recorded month (YYYY-MM) instead of current observations")

	securityEvidenceDestinationsCmd.AddCommand(securityEvidenceDestinationsListCmd, securityEvidenceDestinationsCreateCmd, securityEvidenceDestinationsRevokeCmd)
	securityEvidenceDeliveriesCmd.AddCommand(securityEvidenceDeliveriesListCmd, securityEvidenceDeliveriesSendCmd, securityEvidenceDeliveriesSubmitCmd)
	securityEvidenceCmd.AddCommand(securityEvidenceDestinationsCmd, securityEvidenceDeliveriesCmd)
	securityCmd.AddCommand(securityEvidenceCmd)
}
