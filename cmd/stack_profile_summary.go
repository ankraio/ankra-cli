package cmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"ankra/internal/client"
	"github.com/spf13/cobra"
)

var summaryFactIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var stackProfileSummaryCmd = newStackProfileSummaryCommand()

// newStackProfileSummaryCommand exposes explicit generation and review operations.
func newStackProfileSummaryCommand() *cobra.Command {
	parent := &cobra.Command{Use: "summary", Short: "Read and review evidence-backed profile summaries", Long: `Read the reviewed summary of an exact profile version, inspect its selected evidence,
review an existing candidate, or withdraw an approval. Use status, generate and
policy for explicit generation controls. No operation changes a workload version.
The platform checks current
permissions and source visibility on every request.

Read the candidate and its source_revision, candidate_revision and summary_revision
before deciding. Supply those exact counters; do not fetch newer counters and retry
a stale decision automatically. After an uncertain write, read back before retrying.
Output is JSON by default; use -o yaml for YAML.`}
	for _, spec := range []struct {
		name, use, short, example string
		count                     int
	}{
		{"get", "get <profile-id|profile-name> <version>", "Read one version's approved summary", "ankra stack-profiles summary get opensearch 3", 2},
		{"evidence", "evidence <profile-id|profile-name> <version> <fact-id>", "Inspect evidence selected by the current approval", "ankra stack-profiles summary evidence opensearch 3 f0", 3},
		{"generation", "generation <generation-id>", "Inspect a private candidate and its revision counters", "ankra stack-profiles summary generation <generation-id>", 1},
		{"edit", "edit <generation-id>", "Select existing facts for an unapproved candidate", "ankra stack-profiles summary edit <generation-id> --candidate-revision 2 --outcome logging=f0,f1", 1},
		{"review", "review <generation-id>", "Approve or reject the exact candidate you reviewed", "ankra stack-profiles summary review <generation-id> --decision approve --source-revision 3 --candidate-revision 2 --summary-revision 0", 1},
		{"withdraw", "withdraw <profile-id|profile-name> <version>", "Hide a summary approval while preserving its history", "ankra stack-profiles summary withdraw opensearch 3 --summary-revision 1 --reason incorrect_claim", 2},
	} {
		command := &cobra.Command{Use: spec.use, Short: spec.short, Example: "  " + spec.example, Args: cobra.ExactArgs(spec.count), RunE: runStackProfileSummary}
		registerStructuredOutputFlags(command)
		switch spec.name {
		case "edit":
			command.Flags().Int("candidate-revision", 0, "Candidate revision you inspected (required)")
			command.Flags().StringArray("outcome", nil, "Select KIND=FACT_ID,FACT_ID; repeat for up to five groups")
			command.Flags().Bool("clear", false, "Explicitly clear the candidate's selections; does not withdraw an existing approval")
			_ = command.MarkFlagRequired("candidate-revision")
			command.MarkFlagsMutuallyExclusive("outcome", "clear")
		case "review":
			command.Long = "Approve or reject the exact candidate you inspected. Approval requires a human identity; service and board principals are refused. For a version, approval publishes its summary. Draft approvals remain private. Workload versions do not change."
			command.Flags().String("decision", "", "approve or reject (required)")
			command.Flags().Int("source-revision", 0, "Exact source revision you inspected (required)")
			command.Flags().Int("candidate-revision", 0, "Candidate revision you inspected (required)")
			command.Flags().Int("summary-revision", 0, "Approval pointer revision you inspected, including zero (required)")
			for _, name := range []string{"decision", "source-revision", "candidate-revision", "summary-revision"} {
				_ = command.MarkFlagRequired(name)
			}
		case "withdraw":
			command.Flags().Int("summary-revision", 0, "Approval pointer revision you inspected (required)")
			command.Flags().String("reason", "", "incorrect_claim, sensitive_content, outdated_policy or author_request (required)")
			command.Flags().Bool("yes", false, "Confirm hiding the approved summary")
			_ = command.MarkFlagRequired("summary-revision")
			_ = command.MarkFlagRequired("reason")
		}
		parent.AddCommand(command)
	}
	addSummaryControlCommands(parent)
	return parent
}

// runStackProfileSummary keeps JSON/YAML output free of confirmation text or hints.
func runStackProfileSummary(command *cobra.Command, args []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	var payload json.RawMessage
	var requestError error
	switch command.Name() {
	case "get", "evidence", "withdraw":
		version, versionError := parseRequiredProfileVersionArgument(args[1])
		if versionError != nil {
			return versionError
		}
		if command.Name() == "evidence" && !summaryFactIDPattern.MatchString(args[2]) {
			return summaryUsage("fact ID must be an alias returned by the summary")
		}
		withdrawal := client.ProfileSummaryWithdrawalRequest{}
		if command.Name() == "withdraw" {
			revision, revisionError := summaryRevision(command, "summary-revision", 0)
			if revisionError != nil {
				return revisionError
			}
			reason, _ := command.Flags().GetString("reason")
			switch reason {
			case "incorrect_claim", "sensitive_content", "outdated_policy", "author_request":
			default:
				return summaryUsage("choose a documented withdrawal reason")
			}
			withdrawal = client.ProfileSummaryWithdrawalRequest{ExpectedSummaryRevision: revision, Reason: reason}
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		switch command.Name() {
		case "get":
			payload, requestError = apiClient.GetProfileSummary(command.Context(), profileID, version)
		case "evidence":
			payload, requestError = apiClient.GetProfileSummaryEvidence(command.Context(), profileID, version, args[2])
		case "withdraw":
			yes, _ := command.Flags().GetBool("yes")
			if confirmationError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), fmt.Sprintf("Hide the approved summary for %s version %d? [y/N] ", profileID, version), yes); confirmationError != nil {
				return confirmationError
			}
			payload, requestError = apiClient.WithdrawProfileSummary(command.Context(), profileID, version, withdrawal)
		}
	default:
		generationID := strings.ToLower(args[0])
		if !stackProfileIDPattern.MatchString(generationID) || generationID == "00000000-0000-0000-0000-000000000000" {
			return summaryUsage("generation ID must be a nonzero UUID")
		}
		switch command.Name() {
		case "generation":
			payload, requestError = apiClient.GetProfileSummaryGeneration(command.Context(), generationID)
		case "edit":
			revision, revisionError := summaryRevision(command, "candidate-revision", 1)
			if revisionError != nil {
				return revisionError
			}
			selections, _ := command.Flags().GetStringArray("outcome")
			clear, _ := command.Flags().GetBool("clear")
			outcomes, selectionError := parseSummarySelections(selections, clear)
			if selectionError != nil {
				return selectionError
			}
			payload, requestError = apiClient.EditProfileSummaryCandidate(command.Context(), generationID, client.ProfileSummaryCandidateRequest{ExpectedCandidateRevision: revision, Outcomes: outcomes})
		case "review":
			source, sourceError := summaryRevision(command, "source-revision", 1)
			if sourceError != nil {
				return sourceError
			}
			candidate, candidateError := summaryRevision(command, "candidate-revision", 1)
			if candidateError != nil {
				return candidateError
			}
			summary, summaryError := summaryRevision(command, "summary-revision", 0)
			if summaryError != nil {
				return summaryError
			}
			decision, _ := command.Flags().GetString("decision")
			if decision != "approve" && decision != "reject" {
				return summaryUsage("decision must be approve or reject")
			}
			payload, requestError = apiClient.ReviewProfileSummary(command.Context(), generationID, client.ProfileSummaryReviewRequest{ExpectedSourceRevision: source, ExpectedCandidateRevision: candidate, ExpectedApprovedRevision: summary, Decision: decision})
		}
	}
	if requestError != nil {
		return fmt.Errorf("profile summary %s: %w", command.Name(), requestError)
	}
	var document map[string]json.RawMessage
	if decodeError := json.Unmarshal(payload, &document); decodeError != nil || len(document) == 0 {
		return fmt.Errorf("profile summary response was invalid; read back before retrying a decision")
	}
	return renderApplicationPayload(command, payload)
}

func summaryRevision(command *cobra.Command, name string, minimum int) (int, error) {
	value, _ := command.Flags().GetInt(name)
	if !command.Flags().Changed(name) || value < minimum {
		return 0, summaryUsage(fmt.Sprintf("--%s must be explicitly supplied and at least %d", name, minimum))
	}
	return value, nil
}

func summaryUsage(message string) error { return withExitCode(exitUsage, fmt.Errorf("%s", message)) }

// parseSummarySelections bounds selections before the request and distinguishes
// a deliberate empty candidate from omitted input. No source document is accepted.
func parseSummarySelections(selections []string, clear bool) ([]client.ProfileSummaryOutcome, error) {
	outcomes := make([]client.ProfileSummaryOutcome, 0, len(selections))
	if clear && len(selections) == 0 {
		return outcomes, nil
	}
	if clear || len(selections) == 0 || len(selections) > 5 {
		return nil, summaryUsage("supply one to five --outcome selections, or --clear")
	}
	kinds := map[string]bool{}
	facts := map[string]bool{}
	for _, selection := range selections {
		if len(selection) > 1100 {
			return nil, summaryUsage("outcome selection is too long")
		}
		kind, ids, found := strings.Cut(selection, "=")
		if !found {
			return nil, summaryUsage("outcome must use KIND=FACT_ID,FACT_ID")
		}
		switch kind {
		case "logging", "search", "certificates", "storage", "workloads":
		default:
			return nil, summaryUsage("unknown outcome kind")
		}
		if kinds[kind] {
			return nil, summaryUsage("select each outcome kind only once")
		}
		kinds[kind] = true
		factIDs := strings.Split(ids, ",")
		if len(factIDs) > 16 {
			return nil, summaryUsage("an outcome selects at most 16 facts")
		}
		for _, id := range factIDs {
			if !summaryFactIDPattern.MatchString(id) || facts[id] {
				return nil, summaryUsage("fact IDs must be valid, nonempty and unique across selections")
			}
			facts[id] = true
		}
		outcomes = append(outcomes, client.ProfileSummaryOutcome{Kind: kind, FactIDs: factIDs})
	}
	return outcomes, nil
}

func init() { stackProfilesCmd.AddCommand(stackProfileSummaryCmd) }
