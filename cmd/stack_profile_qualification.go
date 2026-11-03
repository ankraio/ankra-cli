package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"ankra/internal/client"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var qualificationPollInterval = 2 * time.Second

func newProfileRuntimeQualificationCommand() *cobra.Command {
	parent := &cobra.Command{Use: "runtime-qualification", Short: "Inspect the profile analysis runtime with synthetic probes", Long: `Run fixed synthetic checks on the organisation's configured staging cluster and
approved candidate image. Requires ai.manage and cluster read access; start and
cancel also require clusters.operate. No profile content or model request is sent.
A completed report can contain violated and unverified checks. These operations
do not issue an admission receipt, enable generation or change consent.`}
	for _, name := range []string{"start", "get", "cancel"} {
		command := &cobra.Command{Use: name, Args: cobra.NoArgs, RunE: runProfileRuntimeQualification, Long: parent.Long}
		if name != "start" {
			command.Use += " <operation-id>"
			command.Args = cobra.ExactArgs(1)
		}
		switch name {
		case "start":
			command.Short = "Start or recover one synthetic qualification using a request ID"
			command.Flags().String("request-id", "", "Saved nonzero UUID; reuse after an uncertain start (default: generate and print before sending)")
			command.Long += "\n\nThe request ID is printed to stderr before sending. Save it. If the response is\nuncertain, repeat start with the SAME --request-id and organisation; never use a\nnew ID to recover an existing request. A replay returns the recorded operation."
			command.Example = "  ankra stack-profiles summary runtime-qualification start --request-id <saved-uuid> --wait -o json"
		case "get":
			command.Short = "Read an operation and its individual observations"
			command.Example = "  ankra stack-profiles summary runtime-qualification get <operation-id> --wait -o json"
		case "cancel":
			command.Short = "Request cancellation without claiming cleanup was verified"
			command.Flags().Bool("yes", false, "Confirm cancelling this qualification")
			command.Example = "  ankra stack-profiles summary runtime-qualification cancel <operation-id> --yes"
		}
		if name != "cancel" {
			command.Flags().Bool("wait", false, "Wait for a terminal operation; completion does not mean all checks passed")
			command.Flags().Duration("timeout", 10*time.Minute, "Bound --wait, including requests (positive duration; expiry exits 5)")
			command.Long += "\n\n--wait exits 0 when a report is collected, even if observations are violated or\nunverified; inspect report.checks before deciding what it proves. Failed, cancelled\nor interrupted operations exit 1. Timeout exits 5. Interrupting the wait does not\ncancel the remote operation; use cancel explicitly. Without --wait, exit 0 means\nthe operation was read or accepted. JSON/YAML stdout contains one response only."
		}
		registerStructuredOutputFlags(command)
		parent.AddCommand(command)
	}
	return parent
}

func runProfileRuntimeQualification(command *cobra.Command, args []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	waiting, _ := command.Flags().GetBool("wait")
	timeout, _ := command.Flags().GetDuration("timeout")
	if command.Name() != "cancel" && (timeout <= 0 || (command.Flags().Changed("timeout") && !waiting)) {
		return summaryUsage("--timeout must be positive and supplied only with --wait")
	}
	var id string
	var identityError error
	if command.Name() == "start" {
		id, _ = command.Flags().GetString("request-id")
		if id == "" && !command.Flags().Changed("request-id") {
			generated, generateError := uuid.NewRandom()
			if generateError != nil {
				return fmt.Errorf("create qualification request ID: %w", generateError)
			}
			id = generated.String()
		}
	} else {
		id = args[0]
	}
	id, identityError = summaryUUID(id)
	if identityError != nil {
		return identityError
	}
	if command.Name() == "cancel" {
		yes, _ := command.Flags().GetBool("yes")
		if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), "Cancel this runtime qualification? [y/N] ", yes); confirmError != nil {
			return confirmError
		}
	}
	ctx := command.Context()
	if waiting {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, timeout)
		defer stop()
	}
	var view *client.ProfileRuntimeQualification
	var requestError error
	switch command.Name() {
	case "start":
		// A failed stderr write means the recovery identity was not delivered: do not submit.
		if _, writeError := fmt.Fprintf(command.ErrOrStderr(), "Request ID: %s\nRecover in the same organisation: ankra stack-profiles summary runtime-qualification start --request-id %s\n", id, id); writeError != nil {
			return writeError
		}
		view, requestError = apiClient.StartProfileRuntimeQualification(ctx, id)
	case "get":
		view, requestError = apiClient.GetProfileRuntimeQualification(ctx, id)
	case "cancel":
		view, requestError = apiClient.CancelProfileRuntimeQualification(ctx, id)
	}
	if requestError != nil {
		return qualificationRequestError(command, ctx, waiting, requestError)
	}
	if validationError := validateQualificationView(view); validationError != nil {
		return validationError
	}
	if (command.Name() == "start" && view.RequestID != id) || (command.Name() != "start" && view.OperationID != id) {
		return fmt.Errorf("runtime qualification response identity did not match the request")
	}
	if waiting {
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "Operation: %s\nResume: ankra stack-profiles summary runtime-qualification get %s --wait\n", view.OperationID, view.OperationID)
		view, requestError = waitProfileQualification(ctx, view)
	}
	// On a wait error preserve the last observed response, not an invented terminal state.
	if requestError != nil {
		_, _ = fmt.Fprintln(command.ErrOrStderr(), "Output is the last observed response; the remote operation may have changed since.")
	}
	if renderError := renderProfileQualification(command, view); renderError != nil {
		return renderError
	}
	if requestError != nil {
		return qualificationRequestError(command, ctx, waiting, requestError)
	}
	if waiting && view.State != "completed" {
		return fmt.Errorf("runtime qualification ended in state %s (%s); no completed report was collected", view.State, view.Outcome)
	}
	return nil
}

func qualificationRequestError(command *cobra.Command, ctx context.Context, waiting bool, cause error) error {
	if waiting && command.Context().Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return withExitCode(exitWaitTimeout, fmt.Errorf("qualification wait expired; remote outcome may still be pending; use the saved request or operation ID to recover: %w", cause))
	}
	return fmt.Errorf("runtime qualification %s could not be confirmed; use the saved identity to read back before retrying: %w", command.Name(), cause)
}

func waitProfileQualification(ctx context.Context, view *client.ProfileRuntimeQualification) (*client.ProfileRuntimeQualification, error) {
	for view.State == "running" || view.State == "pending" || view.State == "cancelling" {
		if sleepError := sleepInterrupted(ctx, qualificationPollInterval); sleepError != nil {
			return view, sleepError
		}
		next, readError := apiClient.GetProfileRuntimeQualification(ctx, view.OperationID)
		if readError != nil {
			return view, readError
		}
		if validationError := validateQualificationView(next); validationError != nil {
			return view, validationError
		}
		if next.OperationID != view.OperationID || next.RequestID != view.RequestID {
			return view, fmt.Errorf("qualification identity changed while waiting")
		}
		view = next
	}
	return view, nil
}

func validateQualificationView(view *client.ProfileRuntimeQualification) error {
	if view == nil {
		return fmt.Errorf("runtime qualification response was empty")
	}
	if _, identityError := summaryUUID(view.OperationID); identityError != nil {
		return fmt.Errorf("invalid qualification operation identity")
	}
	if _, identityError := summaryUUID(view.RequestID); identityError != nil {
		return fmt.Errorf("invalid qualification request identity")
	}
	switch view.State {
	case "pending", "running", "cancelling", "completed", "failed", "timeout", "cancelled":
	default:
		return fmt.Errorf("unrecognised runtime qualification state %q; update the CLI before interpreting it", view.State)
	}
	if view.State == "completed" && (view.Outcome != "completed" || view.Report == nil || len(view.Report.Checks) == 0) {
		return fmt.Errorf("completed runtime qualification has no valid report")
	}
	if view.Report != nil {
		if view.Report.Version != "profile_runtime_qualification_v1" || view.Report.Policy != "profile_analysis_stdin_v1" || view.Report.Image == "" || view.Report.Image != view.Image {
			return fmt.Errorf("runtime qualification report version, policy or image is not supported or does not match the operation")
		}
		for name, observation := range view.Report.Checks {
			switch observation {
			case "satisfied", "violated", "unverified":
			default:
				return fmt.Errorf("unrecognised qualification observation for %q", name)
			}
		}
	}
	return nil
}

func renderProfileQualification(command *cobra.Command, view *client.ProfileRuntimeQualification) error {
	if rendered, renderError := renderStructured(command, view); rendered || renderError != nil {
		return renderError
	}
	out := command.OutOrStdout()
	_, writeError := fmt.Fprintf(out, "Operation: %s\nRequest: %s\nState: %s\nOutcome: %s\nCluster: %s\nAgent: %s (%s)\nImage: %s\nCleanup: %s\nUpdated: %s\n", view.OperationID, view.RequestID, view.State, view.Outcome, view.ClusterID, view.AgentID, view.AgentVersion, view.Image, view.CleanupState, view.UpdatedAt)
	if writeError != nil {
		return writeError
	}
	retention := "unverified"
	if view.SubjectRetentionObserved {
		retention = "actual subjects checked; account and backup retention remain separate"
	}
	if _, writeError = fmt.Fprintf(out, "Subject retention: %s\n", retention); writeError != nil {
		return writeError
	}
	if view.Report == nil {
		_, writeError = fmt.Fprintln(out, "No report collected.")
		return writeError
	}
	if _, writeError = fmt.Fprintf(out, "Report: %s\nPolicy: %s\n", view.Report.Version, view.Report.Policy); writeError != nil {
		return writeError
	}
	names := make([]string, 0, len(view.Report.Checks))
	for name := range view.Report.Checks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, writeError = fmt.Fprintf(out, "  %s: %s\n", name, view.Report.Checks[name]); writeError != nil {
			return writeError
		}
	}
	_, writeError = fmt.Fprintln(out, "Report collection does not establish that every check passed or enable generation.")
	return writeError
}
