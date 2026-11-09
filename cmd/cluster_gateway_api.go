package cmd

// `ankra cluster gateway-api [status|enable]` (ankra-j6fpgh.4.4): the CLI
// surface over GET/PUT /api/v1/clusters/{provider}/{cluster_id}/gateway-api,
// the day-2 twin of the --gateway-api create flag (registerGatewayAPIFlag).
//
// The GET is the platform's one preview of the enable, the same one the
// portal and MCP read, so `status` prints it as a plan: where the enable
// stands (the phase), where the Gateway API CRDs come from and whether the
// platform reads them as established, what each of the two networking members
// (Traefik, cert-manager) still needs in its values, and the platform's note
// saying who moves the enable on. `enable` prints the same plan, asks, and
// PUTs. The PUT is idempotent and resumable: it records the option, and
// writes the values only once the CRDs are established, so --wait follows the
// phase and repeats the PUT when the platform says the values are next. When
// the platform holds that write back (values_hold), nothing writes the values
// until a person clears the hold, so --wait stops there and names it.
//
// The cluster is resolved like the sibling `cluster logs-ship` verbs, through
// --cluster or the selected cluster. The route is mounted per provider, so
// the cluster's kind picks the path segment, as node groups do.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// gatewayAPIRequestTimeout bounds one read or write of the option.
const gatewayAPIRequestTimeout = 30 * time.Second

// gatewayAPIDefaultWaitTimeout is how long `enable --wait` follows the enable
// before giving up: the reconciler's republish, the CRD addon's install, the
// values commit and one rollout each of Traefik and cert-manager, each a few
// minutes on a healthy cluster.
const gatewayAPIDefaultWaitTimeout = 20 * time.Minute

// gatewayAPIPollInterval is the pause between two status reads while --wait
// follows the enable. A variable so tests can run the loop in milliseconds.
var gatewayAPIPollInterval = 10 * time.Second

// gatewayAPIClusterNotFoundDetail is the platform's own answer for a cluster
// outside the selected organisation; only this detail is read as "not found".
const gatewayAPIClusterNotFoundDetail = "Cluster not found"

// gatewayAPITarget is the cluster a verb acts on and the provider segment of
// its route.
type gatewayAPITarget struct {
	cluster  client.ClusterListItem
	provider string
}

func newClusterGatewayAPICommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "gateway-api",
		Short: "Show or turn on the Kubernetes Gateway API for the cluster",
		Long: `The Traefik that Ankra installs as a cluster's ingress can also serve the
Kubernetes Gateway API, next to Ingress: a GatewayClass named traefik, a shared
Gateway named traefik-gateway (HTTP on Traefik's web entry point, routes from
every namespace), a certificate from cert-manager, and HTTPRoute hostnames
published by external-dns. Every Ingress keeps working.

At create time the option is '--gateway-api' on the create command. This
command turns it on for a cluster that already exists, or shows where that
stands. The platform answers one plan for both: whether the option is recorded,
where the Gateway API CRDs come from and whether they are established, what the
Traefik and cert-manager values still need, and who moves the enable on next.

Run without a subcommand it shows the plan, like 'status'. There is no
'disable': to stop the shared Gateway later, set gateway.enabled: false in
Traefik's values; routes and CRDs stay.`,
		Example: `  ankra cluster gateway-api
  ankra cluster gateway-api status --cluster prod -o json
  ankra cluster gateway-api enable --cluster prod
  ankra cluster gateway-api enable --cluster prod --yes --wait`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterGatewayAPIStatus(command)
		},
	}
	registerStructuredOutputFlags(command)
	command.AddCommand(
		newClusterGatewayAPIStatusCommand(),
		newClusterGatewayAPIEnableCommand(),
	)
	return command
}

func newClusterGatewayAPIStatusCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Show where the Gateway API stands for the cluster",
		Long: `Show the platform's plan for the Gateway API on the cluster: whether the
option is recorded, where the Gateway API CRDs come from and whether the
platform reads them as established, what the Traefik and cert-manager values
still need (with any key that says the opposite of what the option needs), the
last apply of each, any hold on the values write and what it names, and the
note saying who moves the enable on next.

Reading Gateway API objects on the cluster afterwards takes the singular kind:
'ankra cluster get resources gatewayclass --group gateway.networking.k8s.io'.

-o json prints the platform's answer as it is.

Exit codes: 0 when the plan was read, whatever it says (including "not
available"); 2 for a cluster Ankra did not build; 3 when the cluster is not in
the selected organisation; 6 when the credentials are missing, expired or
rejected; 7 when your role cannot read the cluster's settings; 1 for any other
failure, including a platform without this route.`,
		Example: `  ankra cluster gateway-api status
  ankra cluster gateway-api status --cluster prod -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterGatewayAPIStatus(command)
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newClusterGatewayAPIEnableCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "enable",
		Short: "Turn the Gateway API on for the cluster",
		Long: `Turn the Gateway API on for a cluster Ankra built. The platform records the
option on the cluster, installs the Gateway API CRDs through a gateway-api-crds
addon in the networking stack (or uses the ones the cluster already carries),
and, once it reads them as established, writes the Gateway API settings into
the Traefik and cert-manager values through the ordinary stack write: one
GitOps commit on a GitOps-backed cluster, then Traefik and the cert-manager
controller each restart once to pick the settings up (a member whose values
already carry them is left alone). Values you wrote yourself are never
changed: a key that says the opposite of what the option needs is reported as
a conflict and nothing is written until you decide.

Because it changes what the cluster's ingress serves, enable prints the plan
and asks for confirmation first. --yes skips the question; without a terminal
to ask on (a script, CI) it is required.

The enable is idempotent and resumable. Without --wait it records the option
and returns at once, saying what is next; run it again later to write the
values once the CRDs are established, or check with 'status'. With --wait it
follows the phase, repeats the enable when the values are next, and returns
once the Gateway API is enabled. When the platform holds the values back (a
values hold: the cluster is offline, its GitOps sync is paused on open
conflicts, or a Traefik or cert-manager values file was edited in git and is
kept there until a person decides), nothing writes them until that is
resolved, so --wait stops at once, names the hold and exits 1 instead of
repeating the enable until --timeout. A passing platform fault while it
follows (a 5xx, a dropped connection, one request timing out) is tried again
on the next poll; five in a row end the follow. --timeout bounds --wait and is
refused without it. The verdict is the same with or without --wait.

Exit codes:
  0  the Gateway API is on, or (without --wait) the option is recorded and
     the enable is moving: waiting for the CRDs, the values next and not
     held back, or Traefik and cert-manager applying them
  1  the option is recorded but the platform needs a person: a conflict in
     the values, a values hold (the values are next but held back until a
     person resolves what the hold names), a failed CRD install or apply, a
     networking stack with no Ankra-installed Traefik and cert-manager to
     write into, or a values write whose GitOps push failed (the values
     reached the platform, the repository does not carry them yet). The
     reason goes to stderr. Also a refusal of the enable --wait repeats
     (the option stays recorded), any request that failed, five failed
     requests in a row during --wait (the enable keeps going), and a
     platform without this route
  2  no terminal to confirm on and no --yes, --timeout without --wait, or a
     cluster Ankra did not build
  3  the cluster is not in the selected organisation
  4  the confirmation was declined
  5  --wait ran out of --timeout; the enable keeps going, run it again to
     keep following it
  6  the credentials are missing, expired or rejected: run 'ankra login'
  7  your role lacks clusters.write
  8  the platform cannot take the option and nothing was changed: the
     cluster has no Ankra-managed ingress to serve it (the plan reads "not
     available"), or the platform refused this run's first enable, for
     example while the cluster's cloud provider stack is still being created`,
		Example: `  ankra cluster gateway-api enable
  ankra cluster gateway-api enable --cluster prod --yes
  ankra cluster gateway-api enable --cluster prod --yes --wait --timeout 30m`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterGatewayAPIEnable(command)
		},
	}
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	command.Flags().Bool("wait", false, "Follow the enable until the Gateway API is enabled, the platform needs a person, or --timeout runs out")
	command.Flags().Duration("timeout", gatewayAPIDefaultWaitTimeout, "How long --wait follows the enable before giving up (exit 5); refused without --wait")
	registerStructuredOutputFlags(command)
	return command
}

// resolveGatewayAPITarget resolves the cluster and the provider segment of
// its route. `ankra cluster select` stores a cluster as its id and name, so a
// selected cluster is looked up for its kind. A cluster Ankra did not build
// has no Ankra-managed ingress to serve the Gateway API, so its kind is a
// usage error here, before any request to the option's route.
func resolveGatewayAPITarget(command *cobra.Command) (gatewayAPITarget, error) {
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return gatewayAPITarget{}, resolveError
	}
	if cluster.Kind == "" {
		listed, lookupError := apiClient.GetClusterByID(cluster.ID)
		if lookupError != nil {
			return gatewayAPITarget{}, fmt.Errorf("looking up cluster '%s' (%s): %w", cluster.Name, cluster.ID, lookupError)
		}
		cluster.Kind = listed.Kind
	}
	switch cluster.Kind {
	case "hetzner", "ovh", "upcloud", "digitalocean", "scaleway", "ankracloud", "aws", "proxmox", "morpheus":
		return gatewayAPITarget{cluster: cluster, provider: cluster.Kind}, nil
	default:
		return gatewayAPITarget{}, unsupportedClusterKindError(
			fmt.Sprintf("cluster '%s'", cluster.Name), cluster.Kind, "the Gateway API option")
	}
}

// gatewayAPIContext bounds one request by its parent (the command's context,
// or the follow's deadline inside --wait), so a cancelled parent stops the
// request instead of waiting it out.
func gatewayAPIContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, gatewayAPIRequestTimeout)
}

func gatewayAPICommandContext(command *cobra.Command) context.Context {
	if parent := command.Context(); parent != nil {
		return parent
	}
	return context.Background()
}

func runClusterGatewayAPIStatus(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	target, targetError := resolveGatewayAPITarget(command)
	if targetError != nil {
		return targetError
	}

	ctx, cancel := gatewayAPIContext(gatewayAPICommandContext(command))
	defer cancel()
	status, getError := apiClient.GetClusterGatewayAPI(ctx, target.provider, target.cluster.ID)
	if getError != nil {
		return gatewayAPIRouteError(getError, target, false)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, status)
	}

	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Gateway API for cluster '%s' (%s):\n\n", target.cluster.Name, target.provider)
	printGatewayAPIStatus(out, status)
	printGatewayAPINextStep(out, status)
	return nil
}

func runClusterGatewayAPIEnable(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	wait, _ := command.Flags().GetBool("wait")
	timeout, _ := command.Flags().GetDuration("timeout")
	if command.Flags().Changed("timeout") {
		// A timeout without --wait would read as a bound the command never
		// applies, so it is refused before any request rather than ignored
		// (as `pipeline` does, pipelineWaitTimeoutFromFlags).
		if !wait {
			return withExitCode(exitUsage, errors.New("--timeout bounds --wait: pass it with --wait, or drop it"))
		}
		if timeout <= 0 {
			return withExitCode(exitUsage, fmt.Errorf("--timeout must be a positive duration, got %s", timeout))
		}
	}
	target, targetError := resolveGatewayAPITarget(command)
	if targetError != nil {
		return targetError
	}

	planContext, cancelPlan := gatewayAPIContext(gatewayAPICommandContext(command))
	plan, planError := apiClient.GetClusterGatewayAPI(planContext, target.provider, target.cluster.ID)
	cancelPlan()
	if planError != nil {
		return gatewayAPIRouteError(planError, target, false)
	}
	if !plan.Available {
		return withExitCode(exitBlocked, fmt.Errorf(
			"the Gateway API cannot be turned on for cluster '%s': %s", target.cluster.Name, plan.UnavailableReason))
	}
	if plan.Phase == client.GatewayAPIPhaseEnabled {
		// Nothing to record and nothing to write: say so instead of asking
		// for a confirmation that changes nothing.
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, plan)
		}
		out := command.OutOrStdout()
		_, _ = fmt.Fprintf(out, "The Gateway API is already on for cluster '%s' (%s); nothing to do.\n\n", target.cluster.Name, target.provider)
		printGatewayAPIStatus(out, plan)
		return nil
	}
	if confirmError := confirmGatewayAPIEnable(command, target, plan); confirmError != nil {
		return confirmError
	}

	enableContext, cancelEnable := gatewayAPIContext(gatewayAPICommandContext(command))
	result, enableError := apiClient.EnableClusterGatewayAPI(enableContext, target.provider, target.cluster.ID)
	cancelEnable()
	if enableError != nil {
		return gatewayAPIRouteError(enableError, target, true)
	}

	if !wait {
		// The verdict does not depend on --wait: the write's own answer can
		// already say a person must act, and a script reading exit 0 as
		// "turned on" would be wrong. The answer stays the one document on
		// stdout; the verdict travels on the exit code and stderr.
		verdict := gatewayAPIEnableVerdict(target, result)
		if format != outputDefault {
			if encodeError := encodeStructured(command.OutOrStdout(), format, result); encodeError != nil {
				return encodeError
			}
			return verdict
		}
		out := command.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Gateway API enable for cluster '%s' (%s):\n\n", target.cluster.Name, target.provider)
		printGatewayAPIEnableResult(out, result)
		printGatewayAPIStatus(out, &result.GatewayAPIStatus)
		if verdict == nil {
			printGatewayAPINextStep(out, &result.GatewayAPIStatus)
		}
		return verdict
	}

	final, waitError := followGatewayAPIEnable(command, target, result, timeout)
	if format != outputDefault {
		// One document on stdout: the standing the wait ended on. The
		// verdict travels on the exit code and stderr.
		if final != nil {
			if encodeError := encodeStructured(command.OutOrStdout(), format, final); encodeError != nil {
				return encodeError
			}
		}
		return waitError
	}
	out := command.OutOrStdout()
	if final != nil {
		_, _ = fmt.Fprintf(out, "Gateway API for cluster '%s' (%s):\n\n", target.cluster.Name, target.provider)
		printGatewayAPIStatus(out, final)
	}
	return waitError
}

// followGatewayAPIEnable polls the status until the phase is enabled, a
// phase a person has to act on, or the deadline. The platform writes the
// values only on an enable call once the CRDs are established, so a
// values_pending reading repeats the enable (once per poll) rather than
// waiting for a write nobody will make. A values_pending reading that names a
// values hold is the exception: the platform holds that write back until a
// person clears the hold, so a repeated enable writes nothing, and the follow
// stops on the hold before any further enable (gatewayAPIPhaseVerdict). A
// request that fails on a passing fault is tried again on the next poll
// (gatewayAPIFollowFaults); a refusal ends the follow at once. Progress goes
// to stderr so -o json stays one document; the standing the wait ended on is
// returned with the verdict.
func followGatewayAPIEnable(command *cobra.Command, target gatewayAPITarget,
	first *client.GatewayAPIEnableResult, timeout time.Duration) (*client.GatewayAPIStatus, error) {
	deadline, cancel := context.WithTimeout(gatewayAPICommandContext(command), timeout)
	defer cancel()
	errOut := command.ErrOrStderr()
	status := &first.GatewayAPIStatus
	// A values write whose GitOps push failed is a verdict on its own,
	// whatever phase the platform reads next: following it to enabled would
	// report a repository that does not carry what the cluster runs.
	if pushError := gatewayAPIPushVerdict(target, first); pushError != nil {
		return status, pushError
	}
	faults := gatewayAPIFollowFaults{target: target, errOut: errOut}
	lastReported := ""
	enabledThisPoll := false
	for {
		if status.Phase != lastReported {
			_, _ = fmt.Fprintf(errOut, "  %s\n", gatewayAPIPhaseLabel(status.Phase))
			lastReported = status.Phase
		}
		if status.Phase == client.GatewayAPIPhaseEnabled {
			_, _ = fmt.Fprintf(errOut, "\nThe Gateway API is on for cluster '%s'.\n\n", target.cluster.Name)
			return status, nil
		}
		// The phases a person has to act on, and a values hold, end the follow
		// with the same verdict the enable gives without --wait. The check runs
		// before the repeated enable below, so a held write is never repeated.
		if verdict := gatewayAPIPhaseVerdict(target, status); verdict != nil {
			_, _ = fmt.Fprintln(errOut)
			return status, verdict
		}
		if status.Phase == client.GatewayAPIPhaseValuesPending {
			if !enabledThisPoll {
				enabledThisPoll = true
				requestContext, cancelRequest := gatewayAPIContext(deadline)
				result, enableError := apiClient.EnableClusterGatewayAPI(requestContext, target.provider, target.cluster.ID)
				cancelRequest()
				if enableError == nil {
					faults.count = 0
					status = &result.GatewayAPIStatus
					if pushError := gatewayAPIPushVerdict(target, result); pushError != nil {
						_, _ = fmt.Fprintln(errOut)
						return status, pushError
					}
					continue
				}
				if deadline.Err() != nil {
					_, _ = fmt.Fprintln(errOut)
					return status, gatewayAPIFollowStopped(deadline, target, status, timeout)
				}
				// A passing fault leaves the values next: the reading after
				// the pause finds them so, and the enable is repeated then.
				if stopError := faults.record(enableError, true); stopError != nil {
					_, _ = fmt.Fprintln(errOut)
					return status, stopError
				}
			}
		}

		// Pause, then read the standing again until a reading comes back. A
		// passing fault is read again after the next pause; a refusal, the
		// fault budget or the deadline ends the follow.
		for {
			select {
			case <-deadline.Done():
				_, _ = fmt.Fprintln(errOut)
				return status, gatewayAPIFollowStopped(deadline, target, status, timeout)
			case <-time.After(gatewayAPIPollInterval):
			}
			requestContext, cancelRequest := gatewayAPIContext(deadline)
			next, getError := apiClient.GetClusterGatewayAPI(requestContext, target.provider, target.cluster.ID)
			cancelRequest()
			if getError == nil {
				faults.count = 0
				status = next
				break
			}
			if deadline.Err() != nil {
				_, _ = fmt.Fprintln(errOut)
				return status, gatewayAPIFollowStopped(deadline, target, status, timeout)
			}
			if stopError := faults.record(getError, false); stopError != nil {
				_, _ = fmt.Fprintln(errOut)
				return status, stopError
			}
		}
		enabledThisPoll = false
	}
}

// gatewayAPIFollowFaultBudget is how many requests in a row --wait lets fail
// on a passing fault before it stops following. The client already retries
// each read for a few seconds on a gateway-style answer; at the default poll
// interval this rides out a platform restart of about a minute.
const gatewayAPIFollowFaultBudget = 5

// gatewayAPIFollowFaults counts the requests in a row that failed on a
// passing fault while --wait follows the enable.
type gatewayAPIFollowFaults struct {
	target gatewayAPITarget
	errOut io.Writer
	count  int
}

// record decides what one failed request does to the follow. A refusal ends
// it with the route's own verdict (gatewayAPIRouteError), except a 409 on the
// repeated enable (gatewayAPIRepeatRefused). A passing fault is reported on
// stderr and tried again on the next poll, until the
// gatewayAPIFollowFaultBudget-th in a row, which ends it with exit 1 saying
// the enable keeps going on the platform and how to keep following it: the
// option is recorded by then, so a lost connection is not a failed enable.
// It runs only inside the follow, so a write here is always the enable the
// wait repeats once the first one has recorded the option.
func (faults *gatewayAPIFollowFaults) record(fault error, writing bool) error {
	if !gatewayAPIFaultIsPassing(fault) {
		var unexpected *client.UnexpectedResponseError
		if writing && errors.As(fault, &unexpected) && unexpected.StatusCode == http.StatusConflict {
			return gatewayAPIRepeatRefused(faults.target, unexpected)
		}
		return gatewayAPIRouteError(fault, faults.target, writing)
	}
	faults.count++
	if faults.count >= gatewayAPIFollowFaultBudget {
		return withExitCode(exitError, fmt.Errorf(
			"stopped following the Gateway API enable for cluster '%s' after %d failed requests in a row (last: %w). "+
				"The option is recorded and the enable keeps going on the platform; run 'ankra cluster gateway-api enable --wait' "+
				"again to keep following it, or check with 'ankra cluster gateway-api status'",
			faults.target.cluster.Name, faults.count, fault))
	}
	_, _ = fmt.Fprintf(faults.errOut, "  a request to the platform failed (%v); trying again on the next poll\n", fault)
	return nil
}

// gatewayAPIRepeatRefused is the verdict on a 409 refusal of the enable the
// wait repeats. The first enable's 409 exits 8 because nothing was changed
// (gatewayAPIRouteError), but by this point that enable recorded the option,
// so saying "nothing was changed" would understate where the cluster stands.
// The option stays recorded and the enable can be resumed, as with the
// phases the platform holds for a person, so this exits 1 with the
// platform's sentence and how to resume.
func gatewayAPIRepeatRefused(target gatewayAPITarget, refusal *client.UnexpectedResponseError) error {
	reason := refusal.Detail
	if reason == "" {
		reason = refusal.Error()
	}
	return withExitCode(exitError, fmt.Errorf(
		"the platform refused the repeated Gateway API enable for cluster '%s': %s. "+
			"The option stays recorded from the first enable; once that is resolved, "+
			"run 'ankra cluster gateway-api enable --wait' again to resume", target.cluster.Name, reason))
}

// gatewayAPIFaultIsPassing reports whether a failed request is worth another
// poll: the platform answered 5xx, 408 or 429, the connection failed, or the
// one request timed out. A refusal is the platform's answer and is not tried
// again: a rejected login, a role without the permission, a billing
// restriction, a 404, a 409 or any other 4xx. Nor is an interrupted command,
// nor an answer that arrived whole but does not decode: that is a broken
// contract, and retrying it would only spend the budget and then claim the
// enable keeps going. A body cut short by a dropped connection fails while
// it is read, not while it is decoded, so it stays a passing fault.
func gatewayAPIFaultIsPassing(fault error) bool {
	if fault == nil || errors.Is(fault, context.Canceled) || errors.Is(fault, client.ErrUnauthorized) {
		return false
	}
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(fault, &syntaxError) || errors.As(fault, &typeError) {
		return false
	}
	var denied *client.PermissionDeniedError
	var restricted *client.BillingRestrictedError
	if errors.As(fault, &denied) || errors.As(fault, &restricted) {
		return false
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(fault, &unexpected) {
		return unexpected.StatusCode >= http.StatusInternalServerError ||
			unexpected.StatusCode == http.StatusRequestTimeout ||
			unexpected.StatusCode == http.StatusTooManyRequests
	}
	// Anything else never reached a whole platform answer: a connection
	// refused, reset or timed out, or a body cut short while it was read.
	return true
}

// gatewayAPIEnableVerdict is the verdict of one enable call, the same with
// and without --wait: nil when the enable is done or moving on its own, exit
// 1 when a person must act. That is a values write whose GitOps push failed,
// the phases the platform holds for a person (a conflict in the values, a
// failed CRD install or apply, no Ankra-installed networking), and a values
// hold. The error carries the platform's own wording, so it reaches stderr
// with the verdict.
func gatewayAPIEnableVerdict(target gatewayAPITarget, result *client.GatewayAPIEnableResult) error {
	if pushError := gatewayAPIPushVerdict(target, result); pushError != nil {
		return pushError
	}
	return gatewayAPIPhaseVerdict(target, &result.GatewayAPIStatus)
}

// gatewayAPIPushVerdict is exit 1 when the values reached the platform but
// the GitOps push failed or was deferred: the cluster is moving on them while
// the repository does not carry them, which a caller reading exit 0 as
// "committed" would never learn. The failure text is the stack write's own.
func gatewayAPIPushVerdict(target gatewayAPITarget, result *client.GatewayAPIEnableResult) error {
	if result == nil || result.GitPushFailure == nil || *result.GitPushFailure == "" {
		return nil
	}
	return withExitCode(exitError, fmt.Errorf(
		"the Gateway API values for cluster '%s' reached the platform, but the GitOps push failed: %s",
		target.cluster.Name, *result.GitPushFailure))
}

// gatewayAPIPhaseVerdict is exit 1 for the phases the platform holds for a
// person and for a values hold (gatewayAPIValuesHeld); every other standing is
// either done or moving on its own.
func gatewayAPIPhaseVerdict(target gatewayAPITarget, status *client.GatewayAPIStatus) error {
	switch status.Phase {
	case client.GatewayAPIPhaseConflict, client.GatewayAPIPhaseCRDsFailed,
		client.GatewayAPIPhaseApplyFailed, client.GatewayAPIPhaseNoPlatformNetworking:
		return withExitCode(exitError, fmt.Errorf(
			"the Gateway API enable for cluster '%s' needs attention (%s): %s",
			target.cluster.Name, status.Phase, status.Note))
	}
	if gatewayAPIValuesHeld(status) {
		return gatewayAPIHoldVerdict(target, status)
	}
	return nil
}

// gatewayAPIValuesHeld reports whether the values the enable writes next are
// held back: the phase says the values are next and the platform names what
// holds the write (values_hold: an offline cluster, a GitOps sync paused on
// open conflicts, a Traefik or cert-manager values file edited in git and kept
// there for its owner). The platform does not write held values by itself,
// and an enable repeated while the hold stands writes nothing either, so only
// a person moves it on. A hold read in another phase is left to that phase:
// while the CRDs are pending the enable is still moving towards the values,
// and once the values are written there is nothing left to hold.
func gatewayAPIValuesHeld(status *client.GatewayAPIStatus) bool {
	return status.Phase == client.GatewayAPIPhaseValuesPending && strings.TrimSpace(status.ValuesHold) != ""
}

// gatewayAPIHoldVerdict is the verdict on a values hold. It exits 1 like the
// other states a person has to act on (a conflict in the values is the
// nearest): the option is recorded, so it is not exit 8's "nothing was
// changed", and nothing is moving, so it is not exit 5's "keeps going on the
// platform". It carries the platform's sentence for the hold, says what clears
// it and how to resume.
func gatewayAPIHoldVerdict(target gatewayAPITarget, status *client.GatewayAPIStatus) error {
	hold := strings.TrimRight(strings.TrimSpace(status.ValuesHold), ".")
	return withExitCode(exitError, fmt.Errorf(
		"the Gateway API enable for cluster '%s' needs attention (values hold): %s. "+
			"The Traefik and cert-manager values are next, but nothing on the platform writes them while the hold stands, "+
			"and repeating the enable writes nothing either. Clear the hold first (bring the cluster back online, resolve its "+
			"open GitOps conflicts, or settle the values file kept in git, whichever the hold names); the option stays recorded, "+
			"so then run 'ankra cluster gateway-api enable --wait' again to write the values", target.cluster.Name, hold))
}

// gatewayAPIFollowStopped is the verdict when the follow's deadline context
// ends. It is exit 5 only when --timeout ran out. When the command's own
// context was cancelled instead, it is exit 1 carrying the cancellation, so
// a stopped follow is never reported as an expired --timeout. The CLI's root
// runs commands under context.Background() today (Execute in root.go), so an
// interrupt ends the process before this sees it; the distinction is for
// anything that runs the command under a cancellable context.
func gatewayAPIFollowStopped(deadline context.Context, target gatewayAPITarget,
	status *client.GatewayAPIStatus, timeout time.Duration) error {
	if cancelled := deadline.Err(); errors.Is(cancelled, context.Canceled) {
		return withExitCode(exitError, fmt.Errorf(
			"stopped following the Gateway API enable for cluster '%s': %w. "+
				"The option is recorded and the enable keeps going on the platform; "+
				"run 'ankra cluster gateway-api enable --wait' again to keep following it",
			target.cluster.Name, cancelled))
	}
	return gatewayAPIWaitExpired(target, status, timeout)
}

// gatewayAPIWaitExpired is the exit-5 verdict: the enable is still moving on
// the platform, this command just stopped following it.
func gatewayAPIWaitExpired(target gatewayAPITarget, status *client.GatewayAPIStatus, timeout time.Duration) error {
	return withExitCode(exitWaitTimeout, fmt.Errorf(
		"the Gateway API is not enabled for cluster '%s' after %s (phase %s). %s "+
			"Run 'ankra cluster gateway-api enable --wait' again to keep following it, or check with 'ankra cluster gateway-api status'",
		target.cluster.Name, timeout, status.Phase, status.Note))
}

// confirmGatewayAPIEnable prints the plan and asks for it to be confirmed.
// --yes skips the question; without a terminal to ask on, --yes is required,
// because a script that never saw the plan must not change what a cluster's
// ingress serves by accident. Everything goes to stderr so -o json|yaml stays
// parseable.
func confirmGatewayAPIEnable(command *cobra.Command, target gatewayAPITarget, plan *client.GatewayAPIStatus) error {
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprintln(errOut, gatewayAPIEnableExplanation(target, plan))
	yes, _ := command.Flags().GetBool("yes")
	if yes {
		_, _ = fmt.Fprintln(errOut)
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"turning the Gateway API on changes what the ingress of cluster '%s' serves and needs confirmation, "+
				"but there is no terminal to ask on; re-run with --yes to confirm", target.cluster.Name))
	}
	if promptError := confirmPrompt(command.InOrStdin(), errOut,
		"\nTurn the Gateway API on for this cluster? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// gatewayAPIEnableExplanation is the plan `enable` prints before it asks:
// what the option gives, what this call does now on this cluster, what it
// changes in which values, what restarts, and how to stop it later.
func gatewayAPIEnableExplanation(target gatewayAPITarget, plan *client.GatewayAPIStatus) string {
	lines := []string{fmt.Sprintf(
		"Turning the Gateway API on for cluster '%s' (%s) makes the Traefik Ankra installed as its ingress also serve "+
			"the Kubernetes Gateway API, next to Ingress: a GatewayClass named traefik, a shared Gateway named traefik-gateway "+
			"(HTTP on Traefik's web entry point, routes from every namespace), a certificate from cert-manager, and HTTPRoute "+
			"hostnames published by external-dns. Every Ingress keeps working.", target.cluster.Name, target.provider)}

	var now []string
	if !plan.Enabled {
		now = append(now, "record the option on the cluster")
	}
	switch {
	case plan.CRDs.Source == "preinstalled" || (plan.CRDs.Source == "" && plan.CRDs.State == "present"):
		now = append(now, "use the Gateway API CRDs the cluster already carries (no CRD addon is installed)")
	case plan.CRDs.Established:
		now = append(now, "keep the gateway-api-crds addon the networking stack already carries")
	default:
		now = append(now, "install the Gateway API CRDs through a gateway-api-crds addon in the networking stack")
	}
	for _, member := range []client.GatewayAPIMember{plan.Traefik, plan.CertManager} {
		switch {
		case !member.Managed:
			now = append(now, fmt.Sprintf("leave %s alone (not managed by Ankra's networking stack)", member.Addon))
		case member.Values == client.GatewayAPIValuesConflict:
			now = append(now, fmt.Sprintf("hold the %s values: %d %s says the opposite of what the option needs (see below)",
				member.Addon, len(member.Conflicts), gatewayAPIKeyWord(len(member.Conflicts))))
		case member.Values == client.GatewayAPIValuesAlreadyOn:
			now = append(now, fmt.Sprintf("leave the %s values as they are (they carry the settings already)", member.Addon))
		case member.Values == client.GatewayAPIValuesAdd:
			now = append(now, fmt.Sprintf("write the Gateway API settings into the %s values once the CRDs are established", member.Addon))
		case member.Values == "":
			// The status prints this as "values not reported": what is not
			// known is not presented as a planned write.
			now = append(now, fmt.Sprintf("decide on the %s values once the CRDs are established "+
				"(the platform did not report what they need, so this plan cannot say whether they are written)", member.Addon))
		default:
			now = append(now, fmt.Sprintf("decide on the %s values once the CRDs are established "+
				"(the platform reads them as %q, which this CLI does not know)", member.Addon, member.Values))
		}
	}
	lines = append(lines, "", "This will:")
	for _, step := range now {
		lines = append(lines, "  - "+step)
	}
	lines = append(lines, "",
		"The values are written through the ordinary stack write (one GitOps commit on a GitOps-backed cluster); "+
			"Traefik and the cert-manager controller each restart once to pick the settings up (a member whose values "+
			"already carry them is left alone). Nothing you wrote into those values is changed or removed. "+
			"There is no 'disable': to stop the shared Gateway later, set gateway.enabled: false in Traefik's values; routes and CRDs stay.")
	if conflicts := gatewayAPIConflicts(plan); len(conflicts) > 0 {
		lines = append(lines, "", "Conflicts (nothing is written to these values until they are resolved):")
		for _, finding := range conflicts {
			lines = append(lines, fmt.Sprintf("  %s %s: %s", finding.Addon, finding.Key, finding.Detail))
		}
	}
	if plan.ValuesHold != "" {
		lines = append(lines, "", "Values hold: "+plan.ValuesHold+".")
	}
	return strings.Join(lines, "\n")
}

// gatewayAPIRouteError maps a refusal from the option's routes onto what the user
// can act on. A 403 on the write is a missing clusters.write and exits 7; a
// 404 carrying the platform's own detail is a cluster outside the selected
// organisation and exits 3; the router's own 404 (detail "Not Found") is a
// platform that predates the option, which is not "the cluster does not
// exist" and so exits 1; a 404 with no detail at all says neither, so it is
// reported as unknown rather than guessed at. A 409 on the write is the
// platform refusing the enable with nothing changed, so it exits 8 with the
// platform's sentence. Anything else keeps the platform's detail.
func gatewayAPIRouteError(apiError error, target gatewayAPITarget, writing bool) error {
	operation := fmt.Sprintf("reading the Gateway API standing of cluster '%s'", target.cluster.Name)
	if writing {
		operation = fmt.Sprintf("turning the Gateway API on for cluster '%s'", target.cluster.Name)
	}

	var denied *client.PermissionDeniedError
	var unexpected *client.UnexpectedResponseError
	hasStatus := errors.As(apiError, &unexpected)
	if errors.As(apiError, &denied) || (hasStatus && unexpected.StatusCode == http.StatusForbidden) {
		if writing {
			return withExitCode(exitForbidden, fmt.Errorf(
				"you need permission to change cluster settings (clusters.write) to turn the Gateway API on "+
					"for cluster '%s'. Ask an organisation admin for a role that has it", target.cluster.Name))
		}
		return withExitCode(exitForbidden, fmt.Errorf("%s: %w", operation, apiError))
	}
	if hasStatus && unexpected.StatusCode == http.StatusNotFound {
		switch unexpected.Detail {
		case "":
			return withExitCode(exitError, fmt.Errorf(
				"%s: the platform answered 404 without saying why, so it is not known whether the cluster is "+
					"outside this organisation or the platform does not offer the Gateway API option yet. "+
					"Check the selected organisation with 'ankra org current' and try again", operation))
		case routeAbsentDetail:
			return withExitCode(exitError, fmt.Errorf(
				"%s: this Ankra platform does not offer turning the Gateway API on after create yet (the route answered 404). "+
					"Check the selected organisation with 'ankra org current' and try again once the platform has it", operation))
		case gatewayAPIClusterNotFoundDetail:
			return withExitCode(exitNotFound, fmt.Errorf(
				"cluster '%s' (%s) not found in this organisation. Check the selected organisation with "+
					"'ankra org current', or pick the cluster again with 'ankra cluster select'", target.cluster.Name, target.cluster.ID))
		default:
			return withExitCode(exitError, fmt.Errorf("%s: %w", operation, apiError))
		}
	}
	if hasStatus && unexpected.StatusCode == http.StatusConflict && writing {
		reason := unexpected.Detail
		if reason == "" {
			reason = apiError.Error()
		}
		return withExitCode(exitBlocked, fmt.Errorf(
			"the platform did not turn the Gateway API on for cluster '%s' (nothing was changed): %s", target.cluster.Name, reason))
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}

// printGatewayAPIStatus prints the plan the platform answered: the phase in
// words, the CRD reading, each member's values and apply, the hold, the
// conflicts, and the platform's note.
func printGatewayAPIStatus(out io.Writer, status *client.GatewayAPIStatus) {
	if !status.Available {
		_, _ = fmt.Fprintf(out, "  Not available: %s\n", status.UnavailableReason)
		return
	}
	_, _ = fmt.Fprintf(out, "  State:        %s\n", gatewayAPIPhaseLabel(status.Phase))
	_, _ = fmt.Fprintf(out, "  CRDs:         %s\n", gatewayAPICRDsLabel(status.CRDs))
	_, _ = fmt.Fprintf(out, "  Traefik:      %s\n", gatewayAPIMemberLabel(status.Traefik))
	_, _ = fmt.Fprintf(out, "  cert-manager: %s\n", gatewayAPIMemberLabel(status.CertManager))
	if status.ValuesHold != "" {
		_, _ = fmt.Fprintf(out, "  Values hold:  %s\n", status.ValuesHold)
	}
	if conflicts := gatewayAPIConflicts(status); len(conflicts) > 0 {
		_, _ = fmt.Fprintln(out, "\n  Conflicts (nothing is written to these values until they are resolved):")
		for _, finding := range conflicts {
			_, _ = fmt.Fprintf(out, "    %s %s: %s\n", finding.Addon, finding.Key, finding.Detail)
		}
	}
	if status.Note != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", status.Note)
	}
}

// printGatewayAPIEnableResult says what this enable call changed.
func printGatewayAPIEnableResult(out io.Writer, result *client.GatewayAPIEnableResult) {
	_, _ = fmt.Fprintf(out, "  Recorded:       %s\n", gatewayAPIYesNo(result.Recorded, "this call turned the option on", "the option was already on"))
	_, _ = fmt.Fprintf(out, "  Values written: %s\n", gatewayAPIYesNo(result.ValuesWritten, "this call wrote the Traefik and cert-manager values", "not by this call"))
	if result.CommitSHA != nil && *result.CommitSHA != "" {
		_, _ = fmt.Fprintf(out, "  GitOps commit:  %s\n", *result.CommitSHA)
	}
	if result.OperationID != nil && *result.OperationID != "" {
		_, _ = fmt.Fprintf(out, "  Operation:      %s\n", *result.OperationID)
	}
	if result.GitPushFailure != nil && *result.GitPushFailure != "" {
		_, _ = fmt.Fprintf(out, "  GitOps push:    %s\n", *result.GitPushFailure)
	}
	_, _ = fmt.Fprintln(out)
}

// printGatewayAPINextStep says what the person runs next, when there is a
// next step for them: the phases that need a person are explained by the
// platform's note already. Held values are not offered --wait, which would
// stop on the hold at once.
func printGatewayAPINextStep(out io.Writer, status *client.GatewayAPIStatus) {
	if !status.Available {
		return
	}
	if gatewayAPIValuesHeld(status) {
		_, _ = fmt.Fprintln(out, "\nThe values are held back (see 'Values hold' above) and nothing writes them until that is resolved; "+
			"then run 'ankra cluster gateway-api enable --wait' to write them.")
		return
	}
	switch status.Phase {
	case client.GatewayAPIPhaseOff:
		_, _ = fmt.Fprintln(out, "\nTurn it on with 'ankra cluster gateway-api enable'.")
	case client.GatewayAPIPhaseCRDsPending, client.GatewayAPIPhaseValuesPending, client.GatewayAPIPhaseApplying:
		_, _ = fmt.Fprintln(out, "\nFollow it to enabled with 'ankra cluster gateway-api enable --wait' (the enable is idempotent), "+
			"or check later with 'ankra cluster gateway-api status'.")
	}
}

func gatewayAPIPhaseLabel(phase string) string {
	switch phase {
	case client.GatewayAPIPhaseOff:
		return "off (Traefik serves Ingress only)"
	case client.GatewayAPIPhaseNoPlatformNetworking:
		return "recorded, but the networking stack is not Ankra's: nothing to write"
	case client.GatewayAPIPhaseConflict:
		return "recorded, held by a conflict in the values"
	case client.GatewayAPIPhaseCRDsFailed:
		return "recorded, the CRD addon's last install failed"
	case client.GatewayAPIPhaseCRDsPending:
		return "recorded, waiting for the Gateway API CRDs"
	case client.GatewayAPIPhaseValuesPending:
		return "CRDs established, values still to be written"
	case client.GatewayAPIPhaseApplyFailed:
		return "values written, the latest Traefik or cert-manager apply failed"
	case client.GatewayAPIPhaseApplying:
		return "values written, Traefik or cert-manager is applying them"
	case client.GatewayAPIPhaseEnabled:
		return "enabled"
	case "":
		return "not reported"
	}
	return phase
}

// gatewayAPICRDsLabel renders the CRD reading on one line: where the CRDs
// come from, what the watch sees, the addon when there is one, and whether
// the platform reads the bundle as established.
func gatewayAPICRDsLabel(crds client.GatewayAPICRDs) string {
	var parts []string
	switch crds.Source {
	case "":
		parts = append(parts, "no source recorded yet")
	case "addon":
		parts = append(parts, "from the gateway-api-crds addon")
	case "preinstalled":
		parts = append(parts, "already on the cluster")
	default:
		parts = append(parts, "from "+crds.Source)
	}
	watch := "the watch sees " + gatewayAPIBlankAs(crds.State, "nothing reported")
	if total := len(crds.Observed) + len(crds.Missing); total > 0 {
		watch += fmt.Sprintf(" (%d of %d kinds)", len(crds.Observed), total)
	}
	parts = append(parts, watch)
	if crds.AddonState != nil {
		parts = append(parts, fmt.Sprintf("addon %s, apply %s", gatewayAPIBlankAs(*crds.AddonState, "unknown"), gatewayAPIBlankAs(crds.AddonApplied, "unknown")))
	}
	if crds.Established {
		parts = append(parts, "established")
	} else {
		parts = append(parts, "not established")
	}
	return strings.Join(parts, "; ")
}

// gatewayAPIMemberLabel renders one member on one line: what its values say
// about the option and its last apply.
func gatewayAPIMemberLabel(member client.GatewayAPIMember) string {
	if !member.Managed {
		return "not managed by Ankra's networking stack"
	}
	var values string
	switch member.Values {
	case client.GatewayAPIValuesAdd:
		values = "Gateway API settings still to be written"
	case client.GatewayAPIValuesAlreadyOn:
		values = "values carry the Gateway API settings"
	case client.GatewayAPIValuesConflict:
		values = fmt.Sprintf("conflict on %d %s, see below", len(member.Conflicts), gatewayAPIKeyWord(len(member.Conflicts)))
	case "":
		values = "values not reported"
	default:
		values = member.Values
	}
	applied := "apply " + gatewayAPIBlankAs(member.Applied, "unknown")
	if member.LastAppliedAt != nil && *member.LastAppliedAt != "" {
		applied += ", last " + formatTimeAgo(*member.LastAppliedAt)
	}
	return values + " (" + applied + ")"
}

// gatewayAPIConflicts lists both members' conflicts in one slice without
// aliasing either member's own.
func gatewayAPIConflicts(status *client.GatewayAPIStatus) []client.GatewayAPIFinding {
	conflicts := make([]client.GatewayAPIFinding, 0, len(status.Traefik.Conflicts)+len(status.CertManager.Conflicts))
	conflicts = append(conflicts, status.Traefik.Conflicts...)
	conflicts = append(conflicts, status.CertManager.Conflicts...)
	return conflicts
}

func gatewayAPIKeyWord(count int) string {
	if count == 1 {
		return "key"
	}
	return "keys"
}

func gatewayAPIYesNo(value bool, whenYes string, whenNo string) string {
	if value {
		return "yes (" + whenYes + ")"
	}
	return "no (" + whenNo + ")"
}

func gatewayAPIBlankAs(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func init() {
	clusterCmd.AddCommand(newClusterGatewayAPICommand())
}
