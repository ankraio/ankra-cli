package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// Retiring a service: prepare a retirement review (the platform resolves
// what retiring removes and keeps and stores it with a digest; nothing on
// the cluster changes), show it, and confirm it by that digest. Retiring
// deletes the service's data, so every path to a confirmation also needs
// --acknowledge-data-loss, and a confirmation needs a yes at the prompt or
// --yes on top of it.

// serviceRetirementPollInterval is how often --wait reads a confirmed
// retirement. A variable so tests do not sleep.
var serviceRetirementPollInterval = 5 * time.Second

// serviceDataLossFlag is the explicit acknowledgement a retirement needs.
const serviceDataLossFlag = "acknowledge-data-loss"

// requireServiceDataLossAcknowledgement refuses a retirement prepare or
// confirm that did not acknowledge, in words, that the data is deleted. It
// also refuses a --wait that could never wait, before anything is prepared.
func requireServiceDataLossAcknowledgement(command *cobra.Command) error {
	if wait, _ := command.Flags().GetBool("wait"); wait {
		if timeout, _ := command.Flags().GetDuration("timeout"); timeout <= 0 {
			return withExitCode(exitUsage, fmt.Errorf("--timeout must be positive, got %s", timeout))
		}
	}
	acknowledged, _ := command.Flags().GetBool(serviceDataLossFlag)
	if acknowledged {
		return nil
	}
	return withExitCode(exitUsage, errors.New("retiring a service deletes its namespace, its volume claims and its Secrets, "+
		"generated credentials included, and no export is offered; restore points stay in their backup vault. "+
		"Re-run with --"+serviceDataLossFlag+" to review the retirement"))
}

// listPendingServiceRetirements walks the caller's pending retirements of
// the instance to the end of the listing, newest first. A prepare whose
// answer was lost is found here instead of spending another of the
// caller's open retirements on a new one.
func listPendingServiceRetirements(ctx context.Context, instanceID string) ([]client.ServiceRetirement, error) {
	return collectServicePages(func(after string) ([]client.ServiceRetirement, *string, error) {
		page, listError := apiClient.ListServiceRetirements(ctx, instanceID, client.ServiceRetirementListOptions{
			ServicePageOptions: client.ServicePageOptions{Limit: servicePageLimit, After: after},
			State:              "pending",
		})
		if listError != nil {
			return nil, nil, listError
		}
		return page.Items, page.NextCursor, nil
	})
}

// sameServiceConsumers reports whether a pending plan disconnects exactly the
// consumers the instance has now.
func sameServiceConsumers(plan []client.ServiceInstanceConsumer, current []client.ServiceInstanceConsumer) bool {
	if len(plan) != len(current) {
		return false
	}
	planned := make([]string, 0, len(plan))
	for _, consumer := range plan {
		planned = append(planned, consumer.ID)
	}
	slices.Sort(planned)
	for _, consumer := range current {
		if _, found := slices.BinarySearch(planned, consumer.ID); !found {
			return false
		}
	}
	return true
}

func printServiceRetirementPlan(out io.Writer, names *serviceNames, retirement client.ServiceRetirement) {
	plan := retirement.Plan
	_, _ = fmt.Fprintf(out, "Retirement review %s of service %q (generation %d)\n", retirement.ID, plan.Name, plan.Generation)
	_, _ = fmt.Fprintf(out, "  Cluster:   %s\n", names.clusterWithID(plan.ClusterID))
	state := retirement.State
	if retirement.State == "pending" || retirement.State == "expired" {
		state += " (window ends " + retirement.ExpiresAt + ")"
	}
	_, _ = fmt.Fprintf(out, "  State:     %s\n", state)

	if plan.NothingDeployed {
		_, _ = fmt.Fprintln(out, "  Removes:   nothing on the cluster - the service never deployed; its unfinished draft is discarded and the name released")
	} else {
		_, _ = fmt.Fprintln(out, "  Removes:")
		if plan.Stack != nil {
			_, _ = fmt.Fprintf(out, "    Stack %s (%s, %s), with its members:\n", plan.Stack.Name, plan.Stack.ResourceID, plan.Stack.State)
			for _, member := range plan.Stack.Members {
				_, _ = fmt.Fprintf(out, "      %s %s (namespace %s)\n", member.Kind, member.Name, valueOr(member.Namespace, "not knowable"))
			}
		}
		if plan.Namespace != nil {
			_, _ = fmt.Fprintf(out, "    Namespace %s, with everything in it\n", *plan.Namespace)
		}
	}
	printServiceNamespaceContents(out, plan.NamespaceContents)

	if len(plan.ConsumersToDisconnect) > 0 {
		_, _ = fmt.Fprintln(out, "  Disconnects (a record only; no credentials were delivered to them):")
		for _, consumer := range plan.ConsumersToDisconnect {
			_, _ = fmt.Fprintf(out, "    %s  application %s, cluster %s, namespace %s\n",
				consumer.ID, consumer.ApplicationID, names.cluster(consumer.ClusterID), consumer.Namespace)
		}
	}
	if len(plan.SharedKept) > 0 {
		_, _ = fmt.Fprintln(out, "  Keeps (shared, never removed by a retirement):")
		for _, shared := range plan.SharedKept {
			_, _ = fmt.Fprintf(out, "    %s (%s)\n", shared.StackName, shared.Title)
		}
	}
	if plan.Data.Statement != "" {
		_, _ = fmt.Fprintf(out, "  Data:      %s\n", plan.Data.Statement)
	}
	_, _ = fmt.Fprintf(out, "  Digest:    %s\n", retirement.Digest)
}

// printServiceNamespaceContents shows what else sits in the namespace and
// every volume claim with the reclaim policy that decides its data. An
// index that was never synced is said in words: a missing list is "not
// known", never "nothing".
func printServiceNamespaceContents(out io.Writer, contents *client.ServiceNamespaceContents) {
	if contents == nil {
		return
	}
	switch {
	case contents.Complete:
		_, _ = fmt.Fprintf(out, "  Namespace contents (as indexed %s, complete):\n", valueOr(contents.ObservedAt, "-"))
	case contents.ObservedAt == nil:
		_, _ = fmt.Fprintf(out, "  Namespace contents: NOT KNOWN - %s\n", valueOr(contents.IncompleteReason, "the cluster's index was never synced"))
	default:
		_, _ = fmt.Fprintf(out, "  Namespace contents (as indexed %s, INCOMPLETE: %s):\n",
			*contents.ObservedAt, valueOr(contents.IncompleteReason, "some lists may be short"))
	}
	if contents.Items != nil {
		if len(contents.Items) == 0 {
			_, _ = fmt.Fprintln(out, "    Objects the service did not create: none indexed")
		} else {
			_, _ = fmt.Fprintln(out, "    Objects the service did not create, deleted with the namespace:")
			for _, item := range contents.Items {
				owner := ""
				if item.Owner != nil {
					owner = fmt.Sprintf(" (owned by %s %s)", item.Owner.Kind, item.Owner.Name)
				}
				_, _ = fmt.Fprintf(out, "      %s/%s%s\n", item.ResourceType, item.Name, owner)
			}
		}
	}
	if contents.Volumes != nil {
		if len(contents.Volumes) == 0 {
			_, _ = fmt.Fprintln(out, "    Volume claims: none indexed")
		} else {
			_, _ = fmt.Fprintln(out, "    Volume claims (Delete erases the volume's data; Retain keeps it until someone removes the volume):")
			for _, volume := range contents.Volumes {
				origin := "created by the service"
				if !volume.CreatedByService {
					origin = "NOT created by the service"
				}
				_, _ = fmt.Fprintf(out, "      %s  %s, storage class %s, volume %s, reclaim %s\n", volume.ClaimName, origin,
					valueOr(volume.StorageClass, "-"), valueOr(volume.VolumeName, "unbound"), valueOr(volume.ReclaimPolicy, "unknown"))
			}
		}
	}
}

// printServiceRetirementProgress shows a confirmed retirement's progress and,
// once settled, its outcome and what became of every reviewed volume.
func printServiceRetirementProgress(out io.Writer, retirement client.ServiceRetirement) {
	if retirement.ExecutionID == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "  Execution: %s (confirmed %s)\n", *retirement.ExecutionID, valueOr(retirement.ConfirmedAt, "-"))
	if retirement.Phase != nil {
		_, _ = fmt.Fprintf(out, "  Phase:     %s\n", *retirement.Phase)
	}
	if retirement.Outcome != nil {
		_, _ = fmt.Fprintf(out, "  Outcome:   %s (settled %s)\n", *retirement.Outcome, valueOr(retirement.SettledAt, "-"))
	}
	if retirement.Reason != nil && *retirement.Reason != "" {
		_, _ = fmt.Fprintf(out, "  Reason:    %s\n", *retirement.Reason)
	}
	disposal := retirement.VolumeDisposal
	if disposal == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "  Volumes after the namespace was deleted (read %s):\n", disposal.ObservedAt)
	for _, volume := range disposal.Volumes {
		_, _ = fmt.Fprintf(out, "    %s  %s (volume %s, reclaim %s)\n", volume.ClaimName, volume.State,
			valueOr(volume.VolumeName, "-"), valueOr(volume.ReclaimPolicy, "unknown"))
	}
	if len(disposal.RetainedVolumes) > 0 {
		_, _ = fmt.Fprintf(out, "  STILL HOLDING DATA: %s - Ankra does not delete retained volumes; remove them on the cluster when the data is no longer needed\n",
			strings.Join(disposal.RetainedVolumes, ", "))
	}
	if !disposal.Complete {
		_, _ = fmt.Fprintln(out, "  Not every volume could be listed or read: a volume not named here may still hold data")
	}
}

// waitForServiceRetirement reads a confirmed retirement until it settles.
// A settled outcome other than retired left the service and its name in
// place, and is an error; running past the timeout exits with the wait code.
func waitForServiceRetirement(command *cobra.Command, instanceID string, retirementID string, timeout time.Duration) (*client.ServiceRetirement, error) {
	ctx := command.Context()
	deadline := time.Now().Add(timeout)
	progress := command.ErrOrStderr()
	lastState := ""
	for {
		retirement, getError := apiClient.GetServiceRetirement(ctx, instanceID, retirementID)
		if getError != nil {
			return nil, getError
		}
		state := retirement.State
		if retirement.Phase != nil {
			state += " (" + *retirement.Phase + ")"
		}
		if state != lastState {
			_, _ = fmt.Fprintf(progress, "Retirement %s: %s\n", retirementID, state)
			lastState = state
		}
		switch retirement.State {
		case "settled":
			return retirement, nil
		case "in_progress":
		default:
			// Only a running retirement is worth waiting on. Anything else
			// (an unconfirmed review, or a state this CLI does not know)
			// will not settle by waiting, so say what it is now.
			return retirement, fmt.Errorf("retirement %s is %s, not running; there is nothing to wait for", retirementID, state)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return retirement, withExitCode(exitWaitTimeout, fmt.Errorf(
				"retirement %s is still %s after %s; follow it with 'ankra services retirements get %s %s'",
				retirementID, state, timeout, instanceID, retirementID))
		}
		// The last sleep is cut to what is left of the budget, so the final
		// read happens at the deadline rather than being skipped.
		select {
		case <-ctx.Done():
			return retirement, ctx.Err()
		case <-time.After(min(serviceRetirementPollInterval, remaining)):
		}
	}
}

// confirmServiceRetirementAndReport is the shared tail of delete and
// retirements confirm: confirm by the reviewed digest, then report (and with
// --wait, follow) the retirement.
func confirmServiceRetirementAndReport(command *cobra.Command, instanceID string, retirement client.ServiceRetirement) error {
	ctx := command.Context()
	confirmed, confirmError := apiClient.ConfirmServiceRetirement(ctx, instanceID, retirement.ID, retirement.Digest)
	if confirmError != nil {
		var unexpected *client.UnexpectedResponseError
		if errors.As(confirmError, &unexpected) && unexpected.StatusCode == http.StatusConflict {
			return fmt.Errorf("%w - the service changed since this retirement was reviewed, or it expired; "+
				"run 'ankra services delete %s --%s --new-review' to review it again", confirmError, instanceID, serviceDataLossFlag)
		}
		return confirmError
	}
	wait, _ := command.Flags().GetBool("wait")
	if wait {
		timeout, _ := command.Flags().GetDuration("timeout")
		settled, waitError := waitForServiceRetirement(command, instanceID, retirement.ID, timeout)
		if settled != nil {
			confirmed = settled
		}
		if waitError != nil {
			return waitError
		}
	}
	if rendered, renderError := renderStructured(command, confirmed); rendered || renderError != nil {
		return renderError
	}
	out := command.OutOrStdout()
	if confirmed.State == "settled" {
		_, _ = fmt.Fprintf(out, "\nRetirement of service %q settled.\n", confirmed.Plan.Name)
	} else {
		_, _ = fmt.Fprintf(out, "\nService %q is being retired.\n", confirmed.Plan.Name)
	}
	printServiceRetirementProgress(out, *confirmed)
	if confirmed.State != "settled" {
		_, _ = fmt.Fprintf(out, "Follow it with 'ankra services retirements get %s %s' (or re-run with --wait).\n", instanceID, confirmed.ID)
		return nil
	}
	if confirmed.Outcome != nil && *confirmed.Outcome != "retired" {
		return fmt.Errorf("the retirement settled %s: the service and its name stay in place (%s); prepare a new retirement once the cause is resolved",
			*confirmed.Outcome, valueOr(confirmed.Reason, "no reason given"))
	}
	return nil
}

func registerServiceRetirementConfirmFlags(command *cobra.Command) {
	command.Flags().Bool(serviceDataLossFlag, false, "Acknowledge that retiring deletes the service's data (required)")
	command.Flags().BoolP("yes", "y", false, "Confirm the reviewed retirement without asking")
	command.Flags().Bool("wait", false, "Follow the retirement until it settles; exit non-zero unless it settled retired")
	command.Flags().Duration("timeout", 30*time.Minute, "How long --wait follows the retirement")
	registerStructuredOutputFlags(command)
}

func newServicesDeleteCommand() *cobra.Command {
	deleteCommand := &cobra.Command{
		Use:     "delete <service>",
		Aliases: []string{"retire", "rm"},
		Short:   "Retire a managed service: review what is removed, then confirm it",
		Long: `Retire a managed service, in two steps, deleting its data.

First a retirement review is prepared: the platform resolves what retiring
removes and keeps - the service's stack and its members, its namespace with
everything in it (objects it did not create are listed), every volume claim
with the reclaim policy that decides its data, the consumers it is
disconnected from and the shared operators it keeps - and stores that plan
with a digest for ten minutes. Preparing changes nothing on the cluster. The
plan is shown, and the service is retired only when you confirm it: at the
prompt, or with --yes.

Retiring deletes the namespace, its volume claims and its Secrets, generated
credentials included, and no export is offered; restore points stay in their
backup vault under its retention. A volume whose reclaim policy is Retain
keeps its data until someone removes it, and the settled retirement names it.
So delete always needs --acknowledge-data-loss, before anything is prepared.

A retirement review you prepared earlier that is still open (for example one
whose answer was lost) is resumed instead of preparing another; --new-review
prepares a fresh one. --review-only stops after the review and prints the
command that confirms it. --wait follows the retirement until it settles.`,
		Example: `  ankra services delete orders-db --acknowledge-data-loss
  ankra services delete orders-db --acknowledge-data-loss --review-only
  ankra services delete orders-db --cluster prod --acknowledge-data-loss --yes --wait`,
		Args: cobra.ExactArgs(1),
		RunE: runServicesDelete,
	}
	deleteCommand.Flags().String("cluster", "", "The cluster the service runs on, when its name is not unique (name or id)")
	deleteCommand.Flags().Bool("review-only", false, "Prepare and show the retirement review, but do not confirm it")
	deleteCommand.Flags().Bool("new-review", false, "Prepare a new retirement review even if one of yours is still open")
	registerServiceRetirementConfirmFlags(deleteCommand)
	return deleteCommand
}

func runServicesDelete(command *cobra.Command, arguments []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	if acknowledgementError := requireServiceDataLossAcknowledgement(command); acknowledgementError != nil {
		return acknowledgementError
	}
	ctx := command.Context()
	instance, resolveError := resolveServiceInstance(command, arguments[0])
	if resolveError != nil {
		return resolveError
	}
	if instance.ReleasedAt != nil {
		return fmt.Errorf("service %q was already retired at %s", instance.Name, *instance.ReleasedAt)
	}
	if instance.Retirement != nil && instance.Retirement.State == "in_progress" {
		return fmt.Errorf("service %q is already being retired (retirement %s, phase %s); follow it with 'ankra services retirements get %s %s'",
			instance.Name, instance.Retirement.ID, valueOr(instance.Retirement.Phase, "-"), instance.ID, instance.Retirement.ID)
	}

	human := serviceHumanWriter(command)
	var retirement *client.ServiceRetirement
	newReview, _ := command.Flags().GetBool("new-review")
	if !newReview {
		pending, pendingError := listPendingServiceRetirements(ctx, instance.ID)
		if pendingError != nil {
			return pendingError
		}
		for index := range pending {
			candidate := pending[index]
			if candidate.Plan.Generation == instance.Generation && sameServiceConsumers(candidate.Plan.ConsumersToDisconnect, instance.Consumers) {
				retirement = &candidate
				_, _ = fmt.Fprintf(human, "Resuming the retirement review you prepared at %s (open until %s; --new-review prepares another).\n\n",
					candidate.CreatedAt, candidate.ExpiresAt)
				break
			}
		}
	}
	if retirement == nil {
		consumerIDs := make([]string, 0, len(instance.Consumers))
		for _, consumer := range instance.Consumers {
			consumerIDs = append(consumerIDs, consumer.ID)
		}
		prepared, prepareError := apiClient.PrepareServiceRetirement(ctx, instance.ID, client.ServiceRetirementRequest{
			ExpectedGeneration:    instance.Generation,
			DisconnectConsumerIDs: consumerIDs,
			AcknowledgeDataLoss:   true,
		})
		if prepareError != nil {
			return prepareError
		}
		retirement = prepared
	}
	return reviewAndConfirmServiceRetirement(command, human, instance.ID, *retirement)
}

// reviewAndConfirmServiceRetirement shows a pending retirement review and
// confirms it after a yes (or --yes), or stops there with --review-only.
func reviewAndConfirmServiceRetirement(command *cobra.Command, human io.Writer, instanceID string, retirement client.ServiceRetirement) error {
	names := newServiceNames(command.Context())
	printServiceRetirementPlan(human, names, retirement)
	confirmHint := fmt.Sprintf("ankra services retirements confirm %s %s --digest %s --%s",
		instanceID, retirement.ID, retirement.Digest, serviceDataLossFlag)

	reviewOnly := false
	if command.Flags().Lookup("review-only") != nil {
		reviewOnly, _ = command.Flags().GetBool("review-only")
	}
	if reviewOnly {
		if rendered, renderError := renderStructured(command, retirement); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(human, "\nNothing was removed. Confirm this retirement within ten minutes with:\n  %s\n", confirmHint)
		return nil
	}
	yes, _ := command.Flags().GetBool("yes")
	prompt := fmt.Sprintf("\nRetire service %q and delete its data as reviewed above? [y/N]: ", retirement.Plan.Name)
	if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), prompt, yes); confirmError != nil {
		if errors.Is(confirmError, errCancelled) {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Nothing was removed. The review stays open for ten minutes; confirm it with:\n  %s\n", confirmHint)
		}
		return confirmError
	}
	return confirmServiceRetirementAndReport(command, instanceID, retirement)
}

func newServicesRetirementsCommand() *cobra.Command {
	retirementsCommand := &cobra.Command{
		Use:     "retirements",
		Aliases: []string{"retirement"},
		Short:   "List, read and confirm your retirements of a service",
		Long: `List, read and confirm your own retirements of a service.

'ankra services delete' prepares a retirement review and confirms it. A
review that was prepared with --review-only, declined at the prompt or whose
answer was lost stays pending for ten minutes: 'list --state pending' finds it
and 'confirm' confirms it instead of preparing another (each person may hold
10 open retirements). 'get' follows a confirmed retirement and, once it
settled, shows what became of every volume.`,
	}
	retirementsCommand.AddCommand(newServicesRetirementsListCommand())
	retirementsCommand.AddCommand(newServicesRetirementsGetCommand())
	retirementsCommand.AddCommand(newServicesRetirementsConfirmCommand())
	return retirementsCommand
}

var serviceRetirementStates = []string{"pending", "expired", "in_progress", "settled"}

func newServicesRetirementsListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list <service>",
		Aliases: []string{"ls"},
		Short:   "List your retirements of a service, newest first",
		Example: "  ankra services retirements list orders-db\n  ankra services retirements list orders-db --state pending",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			state, _ := command.Flags().GetString("state")
			state = strings.TrimSpace(state)
			if state != "" && !slices.Contains(serviceRetirementStates, state) {
				return withExitCode(exitUsage, fmt.Errorf("--state %q: expected one of %s", state, strings.Join(serviceRetirementStates, ", ")))
			}
			instance, resolveError := resolveServiceInstance(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			retirements, listError := collectServicePages(func(after string) ([]client.ServiceRetirement, *string, error) {
				page, pageError := apiClient.ListServiceRetirements(command.Context(), instance.ID, client.ServiceRetirementListOptions{
					ServicePageOptions: client.ServicePageOptions{Limit: servicePageLimit, After: after},
					State:              state,
				})
				if pageError != nil {
					return nil, nil, pageError
				}
				return page.Items, page.NextCursor, nil
			})
			if listError != nil {
				return listError
			}
			if rendered, renderError := renderStructured(command, retirements); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(retirements) == 0 {
				_, _ = fmt.Fprintf(out, "No retirements of service %q by you.\n", instance.Name)
				return nil
			}
			retirementTable := table.NewWriter()
			retirementTable.SetOutputMirror(out)
			retirementTable.SetStyle(table.StyleRounded)
			retirementTable.AppendHeader(table.Row{"ID", "State", "Phase", "Outcome", "Created", "Expires"})
			for _, retirement := range retirements {
				retirementTable.AppendRow(table.Row{retirement.ID, retirement.State, valueOr(retirement.Phase, "-"),
					valueOr(retirement.Outcome, "-"), retirement.CreatedAt, retirement.ExpiresAt})
			}
			retirementTable.Render()
			return nil
		},
	}
	listCommand.Flags().String("cluster", "", "The cluster the service runs on, when its name is not unique (name or id)")
	listCommand.Flags().String("state", "", "Only retirements in this state: pending, expired, in_progress or settled")
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newServicesRetirementsGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <service> <retirement-id>",
		Short: "Show one of your retirements: the plan, its progress and what became of the volumes",
		Example: "  ankra services retirements get orders-db 7d3c1f0a-5b2e-4c9d-8e1f-0a2b3c4d5e6f\n" +
			"  ankra services retirements get orders-db 7d3c1f0a-5b2e-4c9d-8e1f-0a2b3c4d5e6f -o json",
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			instance, resolveError := resolveServiceInstance(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			retirement, getError := apiClient.GetServiceRetirement(command.Context(), instance.ID, strings.TrimSpace(arguments[1]))
			if getError != nil {
				return getError
			}
			if rendered, renderError := renderStructured(command, retirement); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			printServiceRetirementPlan(out, newServiceNames(command.Context()), *retirement)
			printServiceRetirementProgress(out, *retirement)
			return nil
		},
	}
	getCommand.Flags().String("cluster", "", "The cluster the service runs on, when its name is not unique (name or id)")
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newServicesRetirementsConfirmCommand() *cobra.Command {
	confirmCommand := &cobra.Command{
		Use:   "confirm <service> <retirement-id>",
		Short: "Confirm one of your pending retirement reviews, which retires the service and deletes its data",
		Long: `Confirm one of your pending retirement reviews by its digest. This is the step
that retires the service and deletes its data, so it needs
--acknowledge-data-loss as well as a yes at the prompt (or --yes).

--digest names the digest you reviewed ('delete --review-only' prints it); a
review whose digest differs is refused, because it is not the plan you saw.
Without --digest the digest of the review shown is the one confirmed.`,
		Example: "  ankra services retirements confirm orders-db 7d3c1f0a-5b2e-4c9d-8e1f-0a2b3c4d5e6f --digest sha256:9f2c... --acknowledge-data-loss\n" +
			"  ankra services retirements confirm orders-db 7d3c1f0a-5b2e-4c9d-8e1f-0a2b3c4d5e6f --acknowledge-data-loss --yes --wait",
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			if acknowledgementError := requireServiceDataLossAcknowledgement(command); acknowledgementError != nil {
				return acknowledgementError
			}
			instance, resolveError := resolveServiceInstance(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			retirement, getError := apiClient.GetServiceRetirement(command.Context(), instance.ID, strings.TrimSpace(arguments[1]))
			if getError != nil {
				return getError
			}
			human := serviceHumanWriter(command)
			switch retirement.State {
			case "expired":
				printServiceRetirementPlan(human, newServiceNames(command.Context()), *retirement)
				return fmt.Errorf("retirement %s expired at %s and can no longer be confirmed: prepare a new one with "+
					"'ankra services delete %s --%s'", retirement.ID, retirement.ExpiresAt, instance.ID, serviceDataLossFlag)
			case "in_progress", "settled":
				if rendered, renderError := renderStructured(command, retirement); rendered || renderError != nil {
					return renderError
				}
				printServiceRetirementPlan(human, newServiceNames(command.Context()), *retirement)
				printServiceRetirementProgress(human, *retirement)
				_, _ = fmt.Fprintf(human, "\nRetirement %s was already confirmed; nothing more to do.\n", retirement.ID)
				return nil
			case "pending":
			default:
				// Only a review known to be pending is ever confirmed.
				return fmt.Errorf("retirement %s is %s; only a pending retirement review can be confirmed", retirement.ID, retirement.State)
			}
			digest, _ := command.Flags().GetString("digest")
			if digest = strings.TrimSpace(digest); digest != "" && digest != retirement.Digest {
				return fmt.Errorf("retirement %s has digest %s, not %s: it is not the plan you reviewed, so it was not confirmed",
					retirement.ID, retirement.Digest, digest)
			}
			return reviewAndConfirmServiceRetirement(command, human, instance.ID, *retirement)
		},
	}
	confirmCommand.Flags().String("cluster", "", "The cluster the service runs on, when its name is not unique (name or id)")
	confirmCommand.Flags().String("digest", "", "The digest you reviewed (sha256:...); refused if the review's differs")
	registerServiceRetirementConfirmFlags(confirmCommand)
	return confirmCommand
}
