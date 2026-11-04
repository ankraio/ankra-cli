package cmd

// `ankra cluster stacks redeploy` (bead ankra-bvzzq0).
//
// Re-running a stack's members, and forcing a manifest whose apply lost a
// server-side apply conflict, was reachable from the portal's operation page
// and nowhere else, so an agent or a script that hit the conflict had no path
// to the fix a person has. This verb is that path, with the same permission
// floor. --force is the opt-in: it is never inferred from a failed step, and
// the platform stores nothing, so the next redeploy is a plain one again.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// stackNotFoundDetail is the platform's 404 sentence for a stack it does not
// hold, as opposed to a cluster it does not hold or a route it does not serve.
const stackNotFoundDetail = "Stack not found"

var clusterStacksRedeployCmd = &cobra.Command{
	Use:   "redeploy <stack name>",
	Short: "Re-run the deployment jobs of a stack's members, optionally forcing manifest conflicts",
	Long: "Re-run the deployment jobs of the members a redeploy of this stack picks on the active cluster. " +
		"The result lists the operations it started and every member it skipped, with the reason.\n\n" +
		"--force is for a manifest whose apply failed with an error reading conflict with \"<manager>\". " +
		"The manifests this redeploy re-runs apply with server-side apply force-conflicts, for this redeploy " +
		"only, and nothing is stored on the stack. The result names the manifests it forced; members it does not " +
		"re-run and add-ons are not forced, and an application member's apply forces conflicts either way. It " +
		"overwrites the conflicting fields on the live objects with the stack's values, and that cannot be undone. " +
		"before-first-apply is Kubernetes' name for fields written before Ankra's first server-side apply, not " +
		"another controller, though the value may still be one a person set. --force needs clusters.write on the " +
		"cluster on top of stacks.deploy, and asks for confirmation first: --yes answers it, and without a " +
		"terminal --yes is required.",
	Example: "  ankra cluster stacks redeploy web\n" +
		"  ankra cluster stacks redeploy web --force\n" +
		"  ankra cluster stacks redeploy web --force --yes\n" +
		"  ankra cluster stacks redeploy web -o json",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := strings.TrimSpace(args[0])
		if stackName == "" {
			return withExitCode(exitUsage, fmt.Errorf("a stack name is required"))
		}
		force, forceError := cmd.Flags().GetBool("force")
		if forceError != nil {
			return forceError
		}
		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		if force {
			if confirmError := confirmForcedRedeploy(cmd, stackName, cluster); confirmError != nil {
				return confirmError
			}
		}
		if format == outputDefault {
			verb := "Redeploying"
			if force {
				verb = "Force redeploying"
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s stack '%s' on cluster '%s'...\n", verb, stackName, cluster.Name)
		}
		// The command's own context, so Ctrl-C cancels the request rather than
		// leaving its outcome unknown.
		result, redeployError := apiClient.RedeployStack(cmd.Context(), cluster.ID, stackName, force)
		if redeployError != nil {
			return redeployStackError(redeployError, stackName, cluster)
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			return renderError
		}
		printStackRedeployResult(cmd.OutOrStdout(), result)
		return nil
	},
}

// redeployStackError says which of the 404s a redeploy can answer this is. The
// stack and the cluster are the only ones that mean the target does not exist:
// a route the platform does not serve (this verb's bearer route is newer than
// the platform may be), or a 404 that gives no reason, says nothing about the
// stack, and must not exit as if it were missing.
func redeployStackError(redeployError error, stackName string, cluster client.ClusterListItem) error {
	const operation = "redeploying the stack"
	var unexpected *client.UnexpectedResponseError
	if !errors.As(redeployError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		return fmt.Errorf("%s: %w", operation, redeployError)
	}
	switch unexpected.Detail {
	case stackNotFoundDetail:
		return withExitCode(exitNotFound, fmt.Errorf("stack %q not found on cluster '%s'", stackName, cluster.Name))
	case hostedLogsClusterNotFoundDetail:
		return withExitCode(exitNotFound, fmt.Errorf(
			"cluster '%s' (%s) not found in this organisation. Check the selected organisation with "+
				"'ankra org current', or pick the cluster again with 'ankra cluster select'", cluster.Name, cluster.ID))
	case routeAbsentDetail:
		return withExitCode(exitError, fmt.Errorf(
			"%s: this Ankra platform does not offer a stack redeploy to API tokens yet (the route answered 404). "+
				"Nothing was started", operation))
	case "":
		return withExitCode(exitError, fmt.Errorf(
			"%s: the platform answered 404 without saying why, so it is not known whether the stack is missing "+
				"or the platform does not offer this yet. Nothing was started", operation))
	default:
		return withExitCode(exitError, fmt.Errorf("%s: %w", operation, redeployError))
	}
}

// confirmForcedRedeploy says what a forced redeploy overwrites and asks for it
// to be confirmed, the CLI's counterpart of the portal's dialog and the chat's
// card: the overwrite cannot be undone, so --force alone is not the consent.
// --yes skips the question; without a terminal to ask on, --yes is required,
// because a script that never saw the consequence must not overwrite live
// fields by accident. Everything goes to stderr so -o json|yaml stays
// parseable.
func confirmForcedRedeploy(command *cobra.Command, stackName string, cluster client.ClusterListItem) error {
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprintf(errOut, "A forced redeploy of stack '%s' on cluster '%s' applies the manifests it re-runs with "+
		"server-side apply force-conflicts: the conflicting fields on the live objects take the stack's values, "+
		"overwriting what they hold now, and that cannot be undone. Members it does not re-run and add-ons are not "+
		"forced, an application member's apply forces conflicts either way, and nothing is stored on the stack.\n",
		stackName, cluster.Name)
	yes, _ := command.Flags().GetBool("yes")
	if yes {
		_, _ = fmt.Fprintln(errOut)
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"a forced redeploy of stack '%s' overwrites fields on the live objects and needs confirmation, but "+
				"there is no terminal to ask on; re-run with --yes to confirm. Nothing was started", stackName))
	}
	if promptError := confirmPrompt(command.InOrStdin(), errOut,
		"\nForce the redeploy? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// redeploySkipReasonText says why a member was skipped. A reason this version
// does not know is printed as the platform wrote it rather than mapped to the
// nearest friendly sentence: an unknown cause is unknown.
func redeploySkipReasonText(reason string) string {
	switch reason {
	case "overlap":
		return "an operation already in flight covers it"
	case "admission_window":
		return "held by the window after a force stop; the background reconciler redispatches it when the window ends"
	case "no_job_for_kind":
		return "no job to dispatch for its current state"
	case "manifest_deletes_held":
		return "its delete could not be planned safely this pass; the background reconciler dispatches it"
	default:
		return reason
	}
}

// printStackRedeployResult renders the outcome. A redeploy that started nothing
// is not a failure, but it must not read as one that ran. A forced one says what
// force reached, from forced_resource_ids rather than from what was asked or
// started: an add-on or a manifest being deleted never carries the flag, and the
// manifest that failed may not be among those re-run. force_conflicts only
// echoes the request. A platform that does not report the reach is unknown, not
// a force that reached nothing.
func printStackRedeployResult(out io.Writer, result *client.RedeployStackResult) {
	switch {
	case len(result.ForcedResourceIDs) > 0:
		_, _ = fmt.Fprintf(out, "\nForced redeploy started. The manifests listed under Forced apply with force-conflicts, for this redeploy only.\n")
	case len(result.Operations) > 0:
		_, _ = fmt.Fprintf(out, "\nRedeploy started.\n")
	case len(result.SkippedResources) > 0:
		_, _ = fmt.Fprintf(out, "\nNothing was started: every member this redeploy picked was skipped.\n")
	default:
		_, _ = fmt.Fprintf(out, "\nNothing was started: this redeploy picked no members.\n")
	}
	switch {
	case !result.ForceConflicts || len(result.ForcedResourceIDs) > 0:
	case len(result.Operations) == 0:
		_, _ = fmt.Fprintf(out, "Nothing was forced.\n")
	case result.ForcedResourceIDs == nil:
		_, _ = fmt.Fprintf(out, "The platform did not say which manifests were forced; check the operation before relying on it.\n")
	default:
		_, _ = fmt.Fprintf(out, "Nothing was forced: what it re-runs applies no manifest.\n")
	}
	_, _ = fmt.Fprintf(out, "  Stack:  %s\n", result.StackName)
	_, _ = fmt.Fprintf(out, "  Jobs:   %d\n", result.JobCount)
	if len(result.Operations) > 0 {
		_, _ = fmt.Fprintf(out, "  Operations:\n")
		for _, operation := range result.Operations {
			_, _ = fmt.Fprintf(out, "    - %s (%d jobs)\n", operation.OperationID, operation.JobCount)
		}
	}
	if len(result.ForcedResourceIDs) > 0 {
		_, _ = fmt.Fprintf(out, "  Forced:\n")
		for _, resourceID := range result.ForcedResourceIDs {
			_, _ = fmt.Fprintf(out, "    - %s\n", resourceID)
		}
	}
	if len(result.SkippedResources) > 0 {
		_, _ = fmt.Fprintf(out, "  Skipped:\n")
		for _, skipped := range result.SkippedResources {
			_, _ = fmt.Fprintf(out, "    - %s: %s\n", skipped.ResourceID, redeploySkipReasonText(skipped.Reason))
		}
	}
	if len(result.Operations) > 0 {
		_, _ = fmt.Fprintf(out, "\nFollow it with 'ankra cluster operations list %s'.\n", result.Operations[0].OperationID)
	}
}

func init() {
	clusterStacksRedeployCmd.Flags().Bool("force", false,
		"apply the manifests this redeploy re-runs with server-side apply force-conflicts, overwriting the conflicting live fields (needs clusters.write)")
	clusterStacksRedeployCmd.Flags().Bool("yes", false, "Skip the --force confirmation prompt")
	registerStructuredOutputFlags(clusterStacksRedeployCmd)
	clusterStacksCmd.AddCommand(clusterStacksRedeployCmd)
}
