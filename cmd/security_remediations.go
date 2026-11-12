package cmd

// `ankra security remediations ...`, `ankra security finding rebuild-plan|rebuild`
// and `ankra security code-dispositions ...` (ankra-hq41xb.23): the fix lane
// of the Security Center. A remediation run is one fix pull request on one
// application followed to a rescan that proves the fix; a rebuild opens the
// base-image bump pull requests alone; a code disposition records a reviewed
// Semgrep or Checkov finding the image gate should stop blocking on.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type securityRemediationRun struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Status          string   `json:"status"`
	StatusReason    *string  `json:"status_reason"`
	Title           string   `json:"title"`
	ApplicationName string   `json:"application_name"`
	Repository      string   `json:"repository"`
	Branch          string   `json:"branch"`
	PullRequestURL  *string  `json:"pull_request_url"`
	StartedBy       string   `json:"started_by"`
	Attempts        int      `json:"attempts"`
	MaxAttempts     int      `json:"max_attempts"`
	FindingIDs      []string `json:"finding_ids"`
	CreatedAt       string   `json:"created_at"`
}

type securityRebuildRefusal struct {
	ApplicationName *string `json:"application_name"`
	ImageRepository string  `json:"image_repository"`
	Repository      *string `json:"repository"`
	Reason          string  `json:"reason"`
}

func printSecurityRemediationRun(out io.Writer, run securityRemediationRun) {
	_, _ = fmt.Fprintf(out, "Remediation run: %s\n", run.ID)
	_, _ = fmt.Fprintf(out, "  Title:        %s\n", orDash(run.Title))
	_, _ = fmt.Fprintf(out, "  Kind:         %s\n", run.Kind)
	_, _ = fmt.Fprintf(out, "  Status:       %s\n", run.Status)
	if run.StatusReason != nil && *run.StatusReason != "" {
		_, _ = fmt.Fprintf(out, "  Reason:       %s\n", *run.StatusReason)
	}
	_, _ = fmt.Fprintf(out, "  Application:  %s\n", orDash(run.ApplicationName))
	_, _ = fmt.Fprintf(out, "  Repository:   %s (%s)\n", orDash(run.Repository), orDash(run.Branch))
	_, _ = fmt.Fprintf(out, "  Pull request: %s\n", valueOrDash(run.PullRequestURL))
	_, _ = fmt.Fprintf(out, "  Started by:   %s\n", orDash(run.StartedBy))
	_, _ = fmt.Fprintf(out, "  Attempts:     %d of %d\n", run.Attempts, run.MaxAttempts)
	_, _ = fmt.Fprintf(out, "  Findings:     %d\n", len(run.FindingIDs))
}

func printSecurityRebuildRefusals(out io.Writer, refusals []securityRebuildRefusal) {
	for _, refusal := range refusals {
		subject := refusal.ImageRepository
		if refusal.Repository != nil && *refusal.Repository != "" {
			subject = *refusal.Repository
		}
		_, _ = fmt.Fprintf(out, "Refused: %s: %s\n", orDash(subject), refusal.Reason)
	}
}

func securityRemediationStatusNote(status string) string {
	switch status {
	case "verified":
		return "Verified: every affected cluster was rescanned after the fix with no active occurrence."
	case "uncertain":
		return "Uncertain: the deadline passed without proof. This is not a clean result."
	case "needs_human":
		return "Needs a person: read the reason above and the pull request."
	}
	return ""
}

func newSecurityRemediationsCommand() *cobra.Command {
	remediationsCommand := &cobra.Command{
		Use:     "remediations",
		Aliases: []string{"remediation"},
		Short:   "Start and follow the fix pull requests of security findings",
		Long: `A remediation run is one fix pull request on one application (a finding's
image rebuild, a base-image fix or a dependency bump), the pipeline runs of
its commits and the rescan that proves the fix. A run reads "verified" only
once every affected cluster was rescanned after the fix with no active
occurrence; "uncertain" is a deadline that passed without proof and never
means clean.`,
	}

	listCommand := newOperationCommand(operationCommand{
		use:     "list",
		aliases: []string{"ls"},
		short:   "List remediation runs, newest first",
		long: `List the organisation's remediation runs newest first (at most 100). Without
--application only runs of one finding are listed; with it the application's
batched runs are listed too, and --finding then also matches a batched run
that fixes the finding.`,
		example: `  ankra security remediations list
  ankra security remediations list --finding 4f1c0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c -o json`,
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			findingID, _ := command.Flags().GetString("finding")
			applicationReference, _ := command.Flags().GetString("application")
			limit, _ := command.Flags().GetInt("limit")
			if limit < 0 || limit > 100 {
				return nil, withExitCode(exitUsage, fmt.Errorf("--limit is between 1 and 100, or 0 for the platform's default"))
			}
			options := client.SecurityRemediationsOptions{FindingID: strings.TrimSpace(findingID), Limit: limit}
			if strings.TrimSpace(applicationReference) != "" {
				applicationID, resolveError := resolveApplicationID(command.Context(), apiClient, strings.TrimSpace(applicationReference))
				if resolveError != nil {
					return nil, resolveError
				}
				options.ApplicationID = applicationID
			}
			return apiClient.ListSecurityRemediations(command.Context(), options)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var listing struct {
				Result []securityRemediationRun `json:"result"`
			}
			if decodeError := decodeDocument(document, &listing); decodeError != nil {
				return decodeError
			}
			if len(listing.Result) == 0 {
				_, _ = fmt.Fprintln(out, "No remediation runs. Start one with 'ankra security remediations start <finding-id>'.")
				return nil
			}
			rows := make([]table.Row, 0, len(listing.Result))
			for _, run := range listing.Result {
				rows = append(rows, table.Row{run.ID, run.Status, run.Kind, orDash(run.ApplicationName),
					valueOrDash(run.PullRequestURL), formatTimeAgo(run.CreatedAt)})
			}
			renderRoundedTable(out, table.Row{"ID", "Status", "Kind", "Application", "Pull request", "Created"}, rows)
			return nil
		},
	})
	listCommand.Flags().String("finding", "", "Only runs that fix this finding (its id)")
	listCommand.Flags().String("application", "", "Only this application's runs, batched ones included (name or id)")
	listCommand.Flags().Int("limit", 0, "How many runs to list (1 to 100; the platform's default when omitted)")
	registerSecurityProjectScopeFlag(listCommand)

	getCommand := newOperationCommand(operationCommand{
		use:       "get <run-id>",
		short:     "Show one remediation run with its receipt trail",
		example:   `  ankra security remediations get 0b6f1c1e-7c0e-4b7e-9a57-0d5f3c1a2b3c`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			runID, argumentError := requiredArgument(arguments, 0, "the run id")
			if argumentError != nil {
				return nil, argumentError
			}
			return apiClient.GetSecurityRemediation(command.Context(), runID)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var detail struct {
				Run    securityRemediationRun `json:"run"`
				Events []struct {
					Kind       string `json:"kind"`
					OccurredAt string `json:"occurred_at"`
				} `json:"events"`
			}
			if decodeError := decodeDocument(document, &detail); decodeError != nil {
				return decodeError
			}
			printSecurityRemediationRun(out, detail.Run)
			if note := securityRemediationStatusNote(detail.Run.Status); note != "" {
				_, _ = fmt.Fprintf(out, "\n%s\n", note)
			}
			if len(detail.Events) > 0 {
				_, _ = fmt.Fprintln(out, "\nReceipts:")
				for _, event := range detail.Events {
					_, _ = fmt.Fprintf(out, "  %s  %s\n", event.OccurredAt, event.Kind)
				}
			}
			return nil
		},
	})

	startCommand := newOperationCommand(operationCommand{
		use:   "start <finding-id>",
		short: "Open or reuse the fix pull requests of a finding and start a run for each",
		long: `Open or reuse the fix pull request of every application repository the finding
affects (the image rebuild lane), start or answer the live remediation run of
each, and send the pull request branch through Ankra Pipelines. Repeating the
command reuses the live runs; nothing is approved, waived or accepted.

You are the one asking, so the organisation's AI autonomy pause does not hold
this start; it holds every step Ankra later takes on its own for the run. It
asks first; --yes skips the question and is required when there is no terminal
to ask on. Read what it would do with 'ankra security finding rebuild-plan'.`,
		example:   `  ankra security remediations start 4f1c0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --yes`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			findingID, argumentError := requiredArgument(arguments, 0, "the finding id")
			if argumentError != nil {
				return nil, argumentError
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Starting remediation of finding %s opens or reuses a pull request in every affected application repository and runs its pipeline.",
				findingID)); confirmError != nil {
				return nil, confirmError
			}
			return apiClient.StartSecurityRemediations(command.Context(), findingID)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var started struct {
				Runs []struct {
					Created bool                   `json:"created"`
					Run     securityRemediationRun `json:"run"`
				} `json:"runs"`
				Refusals []securityRebuildRefusal `json:"refusals"`
			}
			if decodeError := decodeDocument(document, &started); decodeError != nil {
				return decodeError
			}
			for _, entry := range started.Runs {
				verb := "Reused"
				if entry.Created {
					verb = "Started"
				}
				_, _ = fmt.Fprintf(out, "%s run %s (%s) for %s: %s\n", verb, entry.Run.ID, entry.Run.Status,
					orDash(entry.Run.ApplicationName), valueOrDash(entry.Run.PullRequestURL))
			}
			printSecurityRebuildRefusals(out, started.Refusals)
			_, _ = fmt.Fprintln(out, "\nFollow a run with 'ankra security remediations get <run-id>'.")
			return nil
		},
	})
	startCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")

	cancelCommand := newOperationCommand(operationCommand{
		use:   "cancel <run-id>",
		short: "Stop a live remediation run",
		long: `Stop a live run. The pull request and any running pipeline are left for their
reviewers; a finished run is answered unchanged. It asks first; --yes skips
the question and is required when there is no terminal to ask on.`,
		example:   `  ankra security remediations cancel 0b6f1c1e-7c0e-4b7e-9a57-0d5f3c1a2b3c --yes`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			runID, argumentError := requiredArgument(arguments, 0, "the run id")
			if argumentError != nil {
				return nil, argumentError
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Cancelling remediation run %s stops Ankra following it. Its pull request stays open.", runID)); confirmError != nil {
				return nil, confirmError
			}
			return apiClient.CancelSecurityRemediation(command.Context(), runID)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var run securityRemediationRun
			if decodeError := decodeDocument(document, &run); decodeError != nil {
				return decodeError
			}
			printSecurityRemediationRun(out, run)
			return nil
		},
	})
	cancelCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")

	remediationsCommand.AddCommand(listCommand, getCommand, startCommand, cancelCommand)
	return remediationsCommand
}

func newSecurityFindingRebuildPlanCommand() *cobra.Command {
	return newOperationCommand(operationCommand{
		use:   "rebuild-plan <finding-id>",
		short: "Plan the base-image bump pull requests of a finding",
		long: `Plan the base-image bump pull requests for a finding whose occurrences are
application container images: per affected application repository, the
Dockerfile FROM lines that would move to the advisory's fixed version, the
branch the pull request would use and the open Ankra pull request already
there; per image the lane cannot act on, the reason. A read: nothing is opened.`,
		example:   `  ankra security finding rebuild-plan 4f1c0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			findingID, argumentError := requiredArgument(arguments, 0, "the finding id")
			if argumentError != nil {
				return nil, argumentError
			}
			return apiClient.GetSecurityFindingRebuildPlan(command.Context(), findingID)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var plan struct {
				Repositories []struct {
					ApplicationName        string  `json:"application_name"`
					Repository             string  `json:"repository"`
					Branch                 string  `json:"branch"`
					ExistingPullRequestURL *string `json:"existing_pull_request_url"`
					Dockerfiles            []struct {
						Path    string `json:"path"`
						Changes []struct {
							Line   int    `json:"line"`
							Before string `json:"before"`
							After  string `json:"after"`
						} `json:"changes"`
					} `json:"dockerfiles"`
				} `json:"repositories"`
				Refusals []securityRebuildRefusal `json:"refusals"`
			}
			if decodeError := decodeDocument(document, &plan); decodeError != nil {
				return decodeError
			}
			if len(plan.Repositories) == 0 && len(plan.Refusals) == 0 {
				_, _ = fmt.Fprintln(out, "Nothing to rebuild: no application image occurrence of this finding carries a fixed version.")
				return nil
			}
			for _, repository := range plan.Repositories {
				_, _ = fmt.Fprintf(out, "%s (%s), branch %s\n", repository.Repository, orDash(repository.ApplicationName), repository.Branch)
				if repository.ExistingPullRequestURL != nil && *repository.ExistingPullRequestURL != "" {
					_, _ = fmt.Fprintf(out, "  Open pull request: %s\n", *repository.ExistingPullRequestURL)
				}
				for _, dockerfile := range repository.Dockerfiles {
					for _, change := range dockerfile.Changes {
						_, _ = fmt.Fprintf(out, "  %s:%d\n    - %s\n    + %s\n", dockerfile.Path, change.Line, change.Before, change.After)
					}
				}
			}
			printSecurityRebuildRefusals(out, plan.Refusals)
			return nil
		},
	})
}

func newSecurityFindingRebuildCommand() *cobra.Command {
	rebuildCommand := newOperationCommand(operationCommand{
		use:   "rebuild <finding-id>",
		short: "Open the base-image bump pull requests of a finding",
		long: `Open one pull request per affected application repository bumping the
Dockerfile base image to the advisory's fixed version, through the
application's own GitHub credential. Every write goes to a new branch and a
pull request, never to the default branch; an open Ankra pull request for the
same finding and repository is reused. It opens the pull requests only: to
have Ankra follow them to a verified fix, use 'ankra security remediations
start'. It asks first; --yes skips the question and is required when there is
no terminal to ask on.`,
		example:   `  ankra security finding rebuild 4f1c0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --yes`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			findingID, argumentError := requiredArgument(arguments, 0, "the finding id")
			if argumentError != nil {
				return nil, argumentError
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Rebuilding finding %s opens a pull request in every affected application repository.", findingID)); confirmError != nil {
				return nil, confirmError
			}
			return apiClient.RebuildSecurityFinding(command.Context(), findingID)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var rebuild struct {
				PullRequests []struct {
					Repository     string `json:"repository"`
					PullRequestURL string `json:"pull_request_url"`
					Reused         bool   `json:"reused"`
				} `json:"pull_requests"`
				Refusals []securityRebuildRefusal `json:"refusals"`
			}
			if decodeError := decodeDocument(document, &rebuild); decodeError != nil {
				return decodeError
			}
			for _, pullRequest := range rebuild.PullRequests {
				verb := "Opened"
				if pullRequest.Reused {
					verb = "Reused"
				}
				_, _ = fmt.Fprintf(out, "%s %s for %s\n", verb, pullRequest.PullRequestURL, pullRequest.Repository)
			}
			printSecurityRebuildRefusals(out, rebuild.Refusals)
			return nil
		},
	})
	rebuildCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	return rebuildCommand
}

type securityCodeDisposition struct {
	ID          string  `json:"id"`
	Tool        string  `json:"tool"`
	RuleID      string  `json:"rule_id"`
	Path        string  `json:"path"`
	Repository  string  `json:"repository"`
	Disposition string  `json:"disposition"`
	Status      string  `json:"status"`
	Severity    string  `json:"finding_severity"`
	Reason      string  `json:"reason"`
	ExpiresAt   *string `json:"expires_at"`
}

func printSecurityCodeDisposition(out io.Writer, document json.RawMessage) error {
	var disposition securityCodeDisposition
	if decodeError := decodeDocument(document, &disposition); decodeError != nil {
		return decodeError
	}
	_, _ = fmt.Fprintf(out, "Code disposition: %s\n", disposition.ID)
	_, _ = fmt.Fprintf(out, "  Finding:     %s %s in %s (%s)\n", disposition.Tool, disposition.RuleID, disposition.Path, disposition.Severity)
	_, _ = fmt.Fprintf(out, "  Repository:  %s\n", disposition.Repository)
	_, _ = fmt.Fprintf(out, "  Disposition: %s (%s)\n", disposition.Disposition, disposition.Status)
	_, _ = fmt.Fprintf(out, "  Reason:      %s\n", orDash(disposition.Reason))
	_, _ = fmt.Fprintf(out, "  Expires:     %s\n", valueOrDash(disposition.ExpiresAt))
	return nil
}

// securityCodeDispositionRequestFromFlags builds the body create and preview
// share. A create needs a reason; a preview does not.
func securityCodeDispositionRequestFromFlags(command *cobra.Command, needsReason bool) (client.SecurityCodeDispositionRequest, error) {
	request := client.SecurityCodeDispositionRequest{}
	applicationReference, _ := command.Flags().GetString("application")
	repositoryID, _ := command.Flags().GetString("repository-id")
	applicationReference, repositoryID = strings.TrimSpace(applicationReference), strings.TrimSpace(repositoryID)
	if (applicationReference == "") == (repositoryID == "") {
		return request, withExitCode(exitUsage, fmt.Errorf("name the pipeline repository with --application or --repository-id, not both"))
	}
	for _, finding := range []struct {
		flagName string
		target   *string
	}{{"tool", &request.Tool}, {"rule", &request.RuleID}, {"path", &request.Path}} {
		value, flagError := requiredTextFlag(command, finding.flagName, "it names the finding, as the pipeline run reports it")
		if flagError != nil {
			return request, flagError
		}
		*finding.target = value
	}
	if request.Tool != "semgrep" && request.Tool != "checkov" {
		return request, withExitCode(exitUsage, fmt.Errorf("--tool must be semgrep or checkov, got %q", request.Tool))
	}
	disposition, _ := command.Flags().GetString("disposition")
	request.Disposition = strings.TrimSpace(disposition)
	if request.Disposition != "false_positive" && request.Disposition != "accepted_risk" {
		return request, withExitCode(exitUsage, fmt.Errorf("--disposition must be false_positive or accepted_risk, got %q", disposition))
	}
	reason, _ := command.Flags().GetString("reason")
	request.Reason = strings.TrimSpace(reason)
	if needsReason && request.Reason == "" {
		return request, withExitCode(exitUsage, fmt.Errorf("--reason is required: it is what an auditor reads"))
	}
	expiresAtRaw, _ := command.Flags().GetString("expires-at")
	expiresAt, expiresError := parseSecurityDeadline(expiresAtRaw)
	if expiresError != nil {
		return request, expiresError
	}
	if expiresAt != nil {
		formatted := expiresAt.Format(time.RFC3339)
		request.ExpiresAt = &formatted
	} else if request.Disposition == "accepted_risk" {
		return request, withExitCode(exitUsage, fmt.Errorf("--expires-at is required for accepted_risk: a review deadline at most a year out"))
	}
	if applicationReference != "" {
		applicationID, resolveError := resolveApplicationID(command.Context(), apiClient, applicationReference)
		if resolveError != nil {
			return request, resolveError
		}
		request.ApplicationID = applicationID
	}
	request.RepositoryID = repositoryID
	return request, nil
}

func registerSecurityCodeDispositionFlags(command *cobra.Command) {
	command.Flags().String("application", "", "The application whose pipeline repository holds the finding (name or id)")
	command.Flags().String("repository-id", "", "The pipeline repository's id, instead of --application")
	command.Flags().String("tool", "", "semgrep or checkov (required)")
	command.Flags().String("rule", "", "The rule id of the finding (required)")
	command.Flags().String("path", "", "The file the finding is in, relative to the repository root (required)")
	command.Flags().String("disposition", "", "false_positive or accepted_risk (required)")
	command.Flags().String("reason", "", "Why; recorded on the audit trail (required to create)")
	command.Flags().String("expires-at", "", "Review deadline as a date (2026-12-31) or RFC3339; required for accepted_risk")
}

func newSecurityCodeDispositionsCommand() *cobra.Command {
	dispositionsCommand := &cobra.Command{
		Use:     "code-dispositions",
		Aliases: []string{"code-disposition"},
		Short:   "Record reviewed Semgrep and Checkov findings the image gate should not block on",
		Long: `A code disposition records a reviewed Semgrep or Checkov finding of a
pipeline repository as a false positive or an accepted risk. The Ankra
Pipelines image gate then stops blocking on that rule in that file, and names
the disposition on every run it lets through. A CRITICAL finding cannot be
disposed of. Container vulnerability dispositions are 'ankra security
dispositions'.`,
	}

	listCommand := newOperationCommand(operationCommand{
		use:     "list",
		aliases: []string{"ls"},
		short:   "List code finding dispositions, newest first",
		example: `  ankra security code-dispositions list
  ankra security code-dispositions list --application shop --include-revoked -o json`,
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			applicationReference, _ := command.Flags().GetString("application")
			repositoryID, _ := command.Flags().GetString("repository-id")
			includeRevoked, _ := command.Flags().GetBool("include-revoked")
			limit, _ := command.Flags().GetInt("limit")
			if limit < 0 || limit > 200 {
				return nil, withExitCode(exitUsage, fmt.Errorf("--limit is between 1 and 200, or 0 for the platform's default"))
			}
			options := client.SecurityCodeDispositionsOptions{RepositoryID: strings.TrimSpace(repositoryID),
				IncludeRevoked: includeRevoked, Limit: limit}
			if strings.TrimSpace(applicationReference) != "" {
				applicationID, resolveError := resolveApplicationID(command.Context(), apiClient, strings.TrimSpace(applicationReference))
				if resolveError != nil {
					return nil, resolveError
				}
				options.ApplicationID = applicationID
			}
			return apiClient.ListSecurityCodeDispositions(command.Context(), options)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var listing struct {
				Items       []securityCodeDisposition `json:"items"`
				IsTruncated bool                      `json:"is_truncated"`
			}
			if decodeError := decodeDocument(document, &listing); decodeError != nil {
				return decodeError
			}
			if len(listing.Items) == 0 {
				_, _ = fmt.Fprintln(out, "No code finding dispositions.")
				return nil
			}
			rows := make([]table.Row, 0, len(listing.Items))
			for _, disposition := range listing.Items {
				rows = append(rows, table.Row{disposition.ID, disposition.Repository, disposition.Tool, disposition.RuleID,
					disposition.Path, disposition.Disposition, disposition.Status, valueOrDash(disposition.ExpiresAt)})
			}
			renderRoundedTable(out, table.Row{"ID", "Repository", "Tool", "Rule", "Path", "Disposition", "Status", "Expires"}, rows)
			if listing.IsTruncated {
				_, _ = fmt.Fprintln(out, "More dispositions exist than are listed; narrow with --application or raise --limit (at most 200).")
			}
			return nil
		},
	})
	listCommand.Flags().String("application", "", "Only this application's pipeline repository (name or id)")
	listCommand.Flags().String("repository-id", "", "Only this pipeline repository (its id)")
	listCommand.Flags().Bool("include-revoked", false, "List revoked dispositions too")
	listCommand.Flags().Int("limit", 0, "How many to list (at most 200; the platform's default when omitted)")
	registerSecurityProjectScopeFlag(listCommand)

	createCommand := newOperationCommand(operationCommand{
		use:   "create --application <application> --tool semgrep|checkov --rule <rule-id> --path <file> --disposition <kind> --reason <why>",
		short: "Record a code finding as a false positive or an accepted risk",
		long: `Record a reviewed Semgrep or Checkov finding as a false_positive or an
accepted_risk. An accepted risk needs --expires-at, at most a year out. A
CRITICAL finding is refused, as is a second active disposition of the same
finding. See what it would cover first with 'preview'. It asks first; --yes
skips the question and is required when there is no terminal to ask on.`,
		example: `  ankra security code-dispositions create --application shop --tool semgrep \
    --rule go.lang.security.audit.xss --path internal/web/render.go \
    --disposition false_positive --reason "Output is escaped by the template"`,
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			request, requestError := securityCodeDispositionRequestFromFlags(command, true)
			if requestError != nil {
				return nil, requestError
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Recording %s rule %s in %s as %s stops the image gate blocking on it.",
				request.Tool, request.RuleID, request.Path, request.Disposition)); confirmError != nil {
				return nil, confirmError
			}
			return apiClient.CreateSecurityCodeDisposition(command.Context(), request)
		},
		human: printSecurityCodeDisposition,
	})
	registerSecurityCodeDispositionFlags(createCommand)
	createCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")

	previewCommand := newOperationCommand(operationCommand{
		use:       "preview --application <application> --tool semgrep|checkov --rule <rule-id> --path <file> --disposition <kind>",
		short:     "Show the finding a code disposition would cover, writing nothing",
		example:   `  ankra security code-dispositions preview --application shop --tool checkov --rule CKV_K8S_8 --path deploy/app.yaml --disposition false_positive`,
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			request, requestError := securityCodeDispositionRequestFromFlags(command, false)
			if requestError != nil {
				return nil, requestError
			}
			return apiClient.PreviewSecurityCodeDisposition(command.Context(), request)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var preview struct {
				CanCreate    bool    `json:"can_create"`
				Refusal      *string `json:"refusal"`
				Effect       string  `json:"effect"`
				FindingTitle string  `json:"finding_title"`
				Severity     string  `json:"finding_severity"`
				Repository   string  `json:"repository"`
				Path         string  `json:"path"`
				FindingLine  *int    `json:"finding_line"`
			}
			if decodeError := decodeDocument(document, &preview); decodeError != nil {
				return decodeError
			}
			location := preview.Path
			if preview.FindingLine != nil {
				location = fmt.Sprintf("%s:%d", preview.Path, *preview.FindingLine)
			}
			_, _ = fmt.Fprintf(out, "Finding:    %s (%s)\n", orDash(preview.FindingTitle), orDash(preview.Severity))
			_, _ = fmt.Fprintf(out, "Where:      %s %s\n", preview.Repository, location)
			_, _ = fmt.Fprintf(out, "Effect:     %s\n", orDash(preview.Effect))
			_, _ = fmt.Fprintf(out, "Can create: %s\n", yesNo(preview.CanCreate))
			if preview.Refusal != nil && *preview.Refusal != "" {
				_, _ = fmt.Fprintf(out, "Refusal:    %s\n", *preview.Refusal)
			}
			return nil
		},
	})
	registerSecurityCodeDispositionFlags(previewCommand)

	revokeCommand := newOperationCommand(operationCommand{
		use:   "revoke <disposition-id> --reason <why>",
		short: "Revoke a code finding disposition",
		long: `Revoke a code finding disposition. The image gate blocks on the finding again
from its next run, and the record is kept for audit. It asks first; --yes
skips the question and is required when there is no terminal to ask on.`,
		example:   `  ankra security code-dispositions revoke 5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --reason "Fixed in the code" --yes`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			dispositionID, argumentError := requiredArgument(arguments, 0, "the disposition id")
			if argumentError != nil {
				return nil, argumentError
			}
			reason, reasonError := requiredTextFlag(command, "reason", "why the disposition no longer holds; recorded on the audit trail")
			if reasonError != nil {
				return nil, reasonError
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Revoking code disposition %s makes the image gate block on its finding again from the next pipeline run.",
				dispositionID)); confirmError != nil {
				return nil, confirmError
			}
			return apiClient.RevokeSecurityCodeDisposition(command.Context(), dispositionID, reason)
		},
		human: printSecurityCodeDisposition,
	})
	revokeCommand.Flags().String("reason", "", "Why; recorded on the audit trail (required)")
	revokeCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")

	dispositionsCommand.AddCommand(listCommand, createCommand, previewCommand, revokeCommand)
	return dispositionsCommand
}

func init() {
	securityCmd.AddCommand(newSecurityRemediationsCommand(), newSecurityCodeDispositionsCommand())
	securityFindingCmd.AddCommand(newSecurityFindingRebuildPlanCommand(), newSecurityFindingRebuildCommand())
}
