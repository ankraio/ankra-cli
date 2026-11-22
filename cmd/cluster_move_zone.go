package cmd

// `ankra cluster move <cluster> --zone <zone>`: the zone move, the second
// destination kind of `cluster move`. The platform does the work (build the
// twin, seed it in standby, cut over, soak, retire); this file chooses the
// step from the flags, refuses combinations that cannot mean anything before
// anything is sent, confirms each step that changes something, and follows
// the move with --wait until it rests.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// zoneMoveWaitTimeout is the default --timeout. Seeding copies every carried
// volume across zones and provisioning builds a cluster, so the shared
// ten-minute default would report a healthy move as timed out.
const zoneMoveWaitTimeout = 3 * time.Hour

// zoneMoveRequestTimeout bounds each single request of a zone move.
const zoneMoveRequestTimeout = 120 * time.Second

// zoneMovePollInterval paces the --wait poll.
var zoneMovePollInterval = 10 * time.Second

// zoneMoveStep is the step of a zone move a run of the command takes.
type zoneMoveStep string

const (
	zoneMoveNoAction zoneMoveStep = ""
	zoneMoveStart    zoneMoveStep = "start"
	zoneMoveStatus   zoneMoveStep = "status"
	zoneMoveCutover  zoneMoveStep = "cutover"
	zoneMoveRollback zoneMoveStep = "rollback"
	zoneMoveRetire   zoneMoveStep = "retire"
)

// zoneMoveFlags are every flag that belongs to the zone move, none of which
// means anything beside --organisation.
var zoneMoveFlags = []string{
	"zone", "target-cluster", "target-name", "plan",
	"control-plane-count", "control-plane-plan", "worker-plan", "worker-count", "storage-tier", "soak-days", "vault",
	"status", "cutover", "rollback", "retire", "wait", "timeout",
}

// zoneMoveStartFlags only shape a move that is being started.
var zoneMoveStartFlags = []string{
	"target-name", "plan",
	"control-plane-count", "control-plane-plan", "worker-plan", "worker-count", "storage-tier", "soak-days", "vault",
}

// zoneMoveTargetShapeFlags shape a target cluster the move builds, so they
// do not apply when --target-cluster names one that already exists.
var zoneMoveTargetShapeFlags = []string{
	"target-name", "control-plane-count", "control-plane-plan", "worker-plan", "worker-count", "storage-tier",
}

func registerZoneMoveFlags(command *cobra.Command) {
	flags := command.Flags()
	flags.String("zone", "", "Destination Ankra Cloud zone: starts a zone move (with --plan, shows its plan)")
	flags.String("target-cluster", "", "Move into this existing, empty cluster (name or id) instead of building one")
	flags.String("target-name", "", "Name of the cluster the move builds (default: <cluster>-<zone>)")
	flags.Bool("plan", false, "Show what the zone move would build, carry and cut over, and write nothing")
	flags.Int("control-plane-count", 0, "Control plane nodes of the target (default: derived from the source)")
	flags.String("control-plane-plan", "", "Server plan of the target's control plane nodes (default: derived from the source)")
	flags.String("worker-plan", "", "Server plan of the target's workers (default: derived from the source)")
	flags.Int("worker-count", 0, "Workers of the target (default: derived from the source)")
	flags.String("storage-tier", "", "Storage tier of the target's volumes (default: the zone's Ankra Storage tier for a local-NVMe source)")
	flags.Int("soak-days", 0, "Days the move soaks after the cutover before the source may be retired (default: the platform's)")
	flags.String("vault", "", "Backup vault (name or id) the move copies data through (default: the stack's backup policy vault, "+
		"else the organisation's only ready vault)")
	flags.Bool("status", false, "Show the cluster's latest zone move and its steps")
	flags.Bool("cutover", false, "Cut the zone move over: maintenance page, final copy, traffic switch")
	flags.Bool("rollback", false, "Roll a soaking zone move back: traffic returns to the source")
	flags.Bool("retire", false, "Retire the source of a soaking zone move: deprovisions it, cannot be undone")
	registerAsyncWriteFlagsWithTimeout(command, zoneMoveWaitTimeout)
}

// zoneMoveActionFromFlags reads which step of a zone move the flags ask for,
// zoneMoveNoAction when none, and refuses combinations that ask for two or
// shape a move they do not start.
func zoneMoveActionFromFlags(cmd *cobra.Command) (zoneMoveStep, error) {
	flags := cmd.Flags()
	chosen := []string{}
	action := zoneMoveNoAction
	if flags.Changed("zone") || flags.Changed("target-cluster") {
		chosen = append(chosen, "--zone")
		action = zoneMoveStart
	}
	for _, candidate := range []zoneMoveStep{zoneMoveStatus, zoneMoveCutover, zoneMoveRollback, zoneMoveRetire} {
		if isSet, _ := flags.GetBool(string(candidate)); isSet {
			chosen = append(chosen, "--"+string(candidate))
			action = candidate
		}
	}
	if len(chosen) > 1 {
		return zoneMoveNoAction, withExitCode(exitUsage, fmt.Errorf(
			"%s cannot be combined: --zone starts a zone move, --status reads it, and --cutover, --rollback "+
				"and --retire each take one step of the move already started", strings.Join(chosen, " and ")))
	}
	if action != zoneMoveStart {
		for _, flagName := range zoneMoveStartFlags {
			if flags.Changed(flagName) {
				return zoneMoveNoAction, withExitCode(exitUsage, fmt.Errorf(
					"--%s only applies when starting a zone move with --zone", flagName))
			}
		}
	}
	if action == zoneMoveNoAction {
		for _, flagName := range []string{"wait", "timeout"} {
			if flags.Changed(flagName) {
				return zoneMoveNoAction, withExitCode(exitUsage, fmt.Errorf(
					"--%s only applies to a zone move (--zone, --status, --cutover, --rollback or --retire)", flagName))
			}
		}
		return zoneMoveNoAction, nil
	}
	if action == zoneMoveStart {
		if startError := validateZoneMoveStartFlags(cmd); startError != nil {
			return zoneMoveNoAction, startError
		}
	}
	return action, nil
}

func validateZoneMoveStartFlags(cmd *cobra.Command) error {
	flags := cmd.Flags()
	if flags.Changed("target-cluster") {
		for _, flagName := range zoneMoveTargetShapeFlags {
			if flags.Changed(flagName) {
				return withExitCode(exitUsage, fmt.Errorf(
					"--target-cluster moves into a cluster that already exists, so --%s, which shapes a cluster "+
						"the move builds, does not apply", flagName))
			}
		}
	} else if zone, _ := flags.GetString("zone"); strings.TrimSpace(zone) == "" {
		return withExitCode(exitUsage, errors.New("--zone needs a zone name, for example --zone se-sto1"))
	}
	if vault, _ := flags.GetString("vault"); flags.Changed("vault") && strings.TrimSpace(vault) == "" {
		return withExitCode(exitUsage, errors.New("--vault needs a backup vault name or id, for example --vault backups"))
	}
	if plan, _ := flags.GetBool("plan"); plan && flags.Changed("wait") {
		return withExitCode(exitUsage, errors.New("--wait does not apply to --plan: a plan writes nothing, so there is nothing to follow"))
	}
	minimums := map[string]int{"control-plane-count": 1, "worker-count": 0, "soak-days": 0}
	for _, flagName := range []string{"control-plane-count", "worker-count", "soak-days"} {
		if !flags.Changed(flagName) {
			continue
		}
		value, _ := flags.GetInt(flagName)
		if value < minimums[flagName] {
			return withExitCode(exitUsage, fmt.Errorf("--%s must be at least %d, got %d", flagName, minimums[flagName], value))
		}
	}
	return nil
}

// zoneMoveRequestFromFlags builds the start request. A count flag left
// alone stays nil, so the platform keeps the shape it derives.
func zoneMoveRequestFromFlags(cmd *cobra.Command, targetClusterID string) client.ZoneMoveRequest {
	flags := cmd.Flags()
	request := client.ZoneMoveRequest{TargetClusterID: targetClusterID}
	request.Zone, _ = flags.GetString("zone")
	request.Zone = strings.TrimSpace(request.Zone)
	request.TargetName, _ = flags.GetString("target-name")
	request.ControlPlanePlan, _ = flags.GetString("control-plane-plan")
	request.WorkerPlan, _ = flags.GetString("worker-plan")
	request.StorageTier, _ = flags.GetString("storage-tier")
	request.Vault, _ = flags.GetString("vault")
	request.Vault = strings.TrimSpace(request.Vault)
	request.ControlPlaneCount = changedIntFlag(cmd, "control-plane-count")
	request.WorkerCount = changedIntFlag(cmd, "worker-count")
	request.SoakDays = changedIntFlag(cmd, "soak-days")
	return request
}

// zoneMoveSource is the cluster a zone move carries.
type zoneMoveSource struct {
	ID   string
	Name string
}

func runZoneMove(cmd *cobra.Command, args []string, action zoneMoveStep) error {
	format, formatError := structuredFormatFromFlags(cmd)
	if formatError != nil {
		return formatError
	}
	sourceID, sourceName, _, resolveError := resolveClusterFromArgsWithKind(cmd, args)
	if resolveError != nil {
		return resolveError
	}
	source := zoneMoveSource{ID: sourceID, Name: sourceName}
	switch action {
	case zoneMoveStart:
		return startZoneMove(cmd, format, source)
	case zoneMoveStatus:
		return showZoneMoveStatus(cmd, format, source)
	case zoneMoveCutover:
		return advanceZoneMove(cmd, format, source, client.ZoneMoveActionCutover)
	case zoneMoveRollback:
		return advanceZoneMove(cmd, format, source, client.ZoneMoveActionRollback)
	case zoneMoveRetire:
		return advanceZoneMove(cmd, format, source, client.ZoneMoveActionRetire)
	}
	return withExitCode(exitUsage, fmt.Errorf("unknown zone move step %q", action))
}

func startZoneMove(cmd *cobra.Command, format outputFormat, source zoneMoveSource) error {
	yes, _ := cmd.Flags().GetBool("yes")
	planOnly, _ := cmd.Flags().GetBool("plan")
	wait, waitFlagError := asyncWriteWaitFlag(cmd)
	if waitFlagError != nil {
		return waitFlagError
	}

	targetClusterID, targetLabel := "", ""
	if targetReference, _ := cmd.Flags().GetString("target-cluster"); strings.TrimSpace(targetReference) != "" {
		resolvedID, resolvedName, _, targetError := resolveClusterFromArgsWithKind(cmd, []string{strings.TrimSpace(targetReference)})
		if targetError != nil {
			wrapped := fmt.Errorf("resolving --target-cluster: %w", targetError)
			if errors.Is(targetError, client.ErrClusterNotFound) {
				return withExitCode(exitNotFound, wrapped)
			}
			return wrapped
		}
		if resolvedID == source.ID {
			return withExitCode(exitUsage, fmt.Errorf("--target-cluster names %s itself: a zone move needs a different cluster to move into", source.Name))
		}
		targetClusterID, targetLabel = resolvedID, resolvedName
	}
	request := zoneMoveRequestFromFlags(cmd, targetClusterID)

	planRequest := request
	planRequest.PlanOnly = true
	if planOnly {
		plan, planError := requestZoneMovePlan(source, planRequest)
		if planError != nil {
			return planError
		}
		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, plan)
		}
		printZoneMovePlan(cmd.OutOrStdout(), source, plan, request.Vault)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nNothing was changed. Start the move with the same command without --plan.\n")
		return nil
	}

	destination := fmt.Sprintf("zone %q", request.Zone)
	if targetClusterID != "" {
		destination = fmt.Sprintf("the existing cluster %q", targetLabel)
	}
	if !yes {
		plan, planError := requestZoneMovePlan(source, planRequest)
		if planError != nil {
			return planError
		}
		printZoneMovePlan(cmd.ErrOrStderr(), source, plan, request.Vault)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr())
		if plan.Plan.Target.Name != "" && targetClusterID == "" {
			destination = fmt.Sprintf("zone %q as cluster %q", plan.Plan.Target.Zone, plan.Plan.Target.Name)
		}
	}
	if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
		fmt.Sprintf("Start moving cluster %q to %s? The carried stacks and data are copied into a target kept in "+
			"standby; the source keeps serving until you run --cutover. [y/N]: ", source.Name, destination),
		yes); confirmError != nil {
		return confirmError
	}

	requestContext, cancelRequest := context.WithTimeout(context.Background(), zoneMoveRequestTimeout)
	defer cancelRequest()
	move, startError := apiClient.StartZoneMove(requestContext, source.ID, request)
	if startError != nil {
		return zoneMoveError("starting the zone move", withZoneMoveVaultHint(startError, request.Vault))
	}
	return reportZoneMove(cmd, format, source, move, wait, client.ZoneMoveStatusReadyForCutover)
}

func requestZoneMovePlan(source zoneMoveSource, planRequest client.ZoneMoveRequest) (*client.ZoneMove, error) {
	requestContext, cancelRequest := context.WithTimeout(context.Background(), zoneMoveRequestTimeout)
	defer cancelRequest()
	plan, planError := apiClient.StartZoneMove(requestContext, source.ID, planRequest)
	if planError != nil {
		return nil, zoneMoveError("planning the zone move", withZoneMoveVaultHint(planError, planRequest.Vault))
	}
	return plan, nil
}

// zoneMoveVaultHint is what the CLI adds to the platform's vault refusal,
// which names the request field ("vault") rather than the flag. A vault the
// request named applies to every stack, so a refusal while one is named is
// a refusal of that vault.
func zoneMoveVaultHint(requestedVault string) string {
	if requestedVault == "" {
		return "Name the vault with --vault <name|id>."
	}
	return fmt.Sprintf("The vault named with --vault (%s) was refused; name a ready one with --vault <name|id>.", requestedVault)
}

// withZoneMoveVaultHint adds the --vault hint to a start the platform
// refused (422) because it cannot tell which backup vault carries a stack's
// data, keeping the refusal's status for the exit code.
func withZoneMoveVaultHint(requestError error, requestedVault string) error {
	var unexpected *client.UnexpectedResponseError
	if !errors.As(requestError, &unexpected) || unexpected.StatusCode != 422 ||
		!mentionsZoneMoveBackupVault(requestError.Error()) {
		return requestError
	}
	return fmt.Errorf("%w %s", requestError, zoneMoveVaultHint(requestedVault))
}

func mentionsZoneMoveBackupVault(text string) bool {
	return strings.Contains(strings.ToLower(text), "backup vault")
}

func showZoneMoveStatus(cmd *cobra.Command, format outputFormat, source zoneMoveSource) error {
	wait, waitFlagError := asyncWriteWaitFlag(cmd)
	if waitFlagError != nil {
		return waitFlagError
	}
	move, readError := readZoneMove(source)
	if readError != nil {
		return readError
	}
	return reportZoneMove(cmd, format, source, move, wait, "")
}

func advanceZoneMove(cmd *cobra.Command, format outputFormat, source zoneMoveSource, action client.ZoneMoveAction) error {
	yes, _ := cmd.Flags().GetBool("yes")
	wait, waitFlagError := asyncWriteWaitFlag(cmd)
	if waitFlagError != nil {
		return waitFlagError
	}
	current, readError := readZoneMove(source)
	if readError != nil {
		return readError
	}
	if current.Status != action.AllowedFrom() {
		return withExitCode(exitBlocked, fmt.Errorf(
			"the zone move of %s is %s, and --%s runs only from %s, so nothing was changed. %s",
			source.Name, current.Status, action, action.AllowedFrom(), zoneMoveNextStep(source, current)))
	}
	if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
		zoneMoveActionPrompt(source, current, action), yes); confirmError != nil {
		return confirmError
	}

	requestContext, cancelRequest := context.WithTimeout(context.Background(), zoneMoveRequestTimeout)
	defer cancelRequest()
	move, advanceError := apiClient.AdvanceZoneMove(requestContext, source.ID, action)
	if advanceError != nil {
		return zoneMoveError(fmt.Sprintf("the zone move %s", action), advanceError)
	}
	if move == nil {
		if move, readError = readZoneMove(source); readError != nil {
			return readError
		}
	}
	return reportZoneMove(cmd, format, source, move, wait, action.SettlesAt())
}

// zoneMoveActionPrompt says in one line what the step does.
func zoneMoveActionPrompt(source zoneMoveSource, move *client.ZoneMove, action client.ZoneMoveAction) string {
	target := zoneMoveTargetLabel(move)
	switch action {
	case client.ZoneMoveActionCutover:
		return fmt.Sprintf("Cut %q over to %s? The maintenance page goes on, the source is frozen, the final copy "+
			"is taken and traffic switches to the target. [y/N]: ", source.Name, target)
	case client.ZoneMoveActionRollback:
		return fmt.Sprintf("Roll the move of %q back? Traffic returns to the source; writes made on %s since the "+
			"cutover are not copied back. [y/N]: ", source.Name, target)
	default:
		return fmt.Sprintf("Retire %q? This deprovisions the source cluster and cannot be undone; %s keeps "+
			"serving. [y/N]: ", source.Name, target)
	}
}

func readZoneMove(source zoneMoveSource) (*client.ZoneMove, error) {
	requestContext, cancelRequest := context.WithTimeout(context.Background(), zoneMoveRequestTimeout)
	defer cancelRequest()
	move, readError := apiClient.GetZoneMove(requestContext, source.ID)
	if readError != nil {
		if client.IsZoneMoveNotFound(readError) {
			return nil, withExitCode(exitNotFound, fmt.Errorf(
				"%s has no zone move: see what one would do with 'ankra cluster move %s --zone <zone> --plan'",
				source.Name, source.Name))
		}
		return nil, zoneMoveError("reading the zone move", readError)
	}
	return move, nil
}

// zoneMoveError labels a failed request. A 409 from the start route means a
// move of this source is already under way, which is a preflight block.
func zoneMoveError(label string, requestError error) error {
	wrapped := fmt.Errorf("%s: %w", label, requestError)
	var unexpected *client.UnexpectedResponseError
	if errors.As(requestError, &unexpected) && unexpected.StatusCode == 409 {
		return withExitCode(exitBlocked, wrapped)
	}
	return wrapped
}

// reportZoneMove prints the move, following it first with --wait. expected
// is the resting status the step was heading for ("" for --status, where
// any resting status but failed is an answer rather than a failure).
func reportZoneMove(cmd *cobra.Command, format outputFormat, source zoneMoveSource,
	move *client.ZoneMove, wait bool, expected string) error {
	if wait {
		if format == outputDefault {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Following zone move %s of %s until it rests...\n", move.ID, source.Name)
		}
		waitContext, cancelWait, contextError := asyncWriteRequestContext(cmd)
		if contextError != nil {
			return contextError
		}
		defer cancelWait()
		settled, followError := followZoneMove(waitContext, source, move, cmd.ErrOrStderr())
		if settled != nil {
			move = settled
		}
		if followError != nil {
			if format != outputDefault {
				_ = encodeStructured(cmd.OutOrStdout(), format, move)
			}
			return asyncWriteError("waiting for the zone move", true, followError)
		}
	}
	if format != outputDefault {
		if encodeError := encodeStructured(cmd.OutOrStdout(), format, move); encodeError != nil {
			return encodeError
		}
	} else {
		printZoneMove(cmd.OutOrStdout(), source, move)
	}
	if !wait {
		return nil
	}
	return zoneMoveOutcomeError(source, move, expected)
}

// followZoneMove polls the move until it rests or the context ends. Status
// changes go to progress (stderr), so structured stdout stays parseable.
func followZoneMove(ctx context.Context, source zoneMoveSource, move *client.ZoneMove, progress io.Writer) (*client.ZoneMove, error) {
	lastReported := ""
	current := move
	for {
		state := current.Status
		if running := runningZoneMoveStep(current); running != "" {
			state += " (" + running + ")"
		}
		if state != lastReported {
			_, _ = fmt.Fprintf(progress, "Zone move %s: %s\n", current.ID, state)
			lastReported = state
		}
		if client.IsZoneMoveResting(current.Status) {
			return current, nil
		}
		timer := time.NewTimer(zoneMovePollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return current, ctx.Err()
		case <-timer.C:
		}
		next, readError := apiClient.GetZoneMove(ctx, source.ID)
		if readError != nil {
			if ctx.Err() != nil {
				return current, ctx.Err()
			}
			return current, fmt.Errorf("reading the zone move: %w", readError)
		}
		current = next
	}
}

func runningZoneMoveStep(move *client.ZoneMove) string {
	for _, step := range move.Steps {
		if step.Status == client.ZoneMoveStepRunning {
			return step.Name
		}
	}
	return ""
}

// zoneMoveOutcomeError turns a followed move that rested somewhere other
// than where the step was heading into a non-zero exit.
func zoneMoveOutcomeError(source zoneMoveSource, move *client.ZoneMove, expected string) error {
	if move.Status == client.ZoneMoveStatusFailed {
		return fmt.Errorf("the zone move of %s failed%s", source.Name, zoneMoveFailureDetail(move))
	}
	if expected != "" && move.Status != expected {
		return fmt.Errorf("the zone move of %s rested at %s, not %s", source.Name, move.Status, expected)
	}
	return nil
}

func zoneMoveFailureDetail(move *client.ZoneMove) string {
	for _, step := range move.Steps {
		if step.Status != client.ZoneMoveStepFailed {
			continue
		}
		if step.Message != nil && *step.Message != "" {
			return fmt.Sprintf(" at step %s: %s", step.Name, *step.Message)
		}
		return " at step " + step.Name
	}
	return ""
}

func zoneMoveTargetLabel(move *client.ZoneMove) string {
	name := move.Plan.Target.Name
	switch {
	case name != "":
		return fmt.Sprintf("the target %q", name)
	case move.TargetClusterID != nil && *move.TargetClusterID != "":
		return "the target " + *move.TargetClusterID
	}
	return "the target"
}

// zoneMoveNextStep is the one sentence that says what happens next.
func zoneMoveNextStep(source zoneMoveSource, move *client.ZoneMove) string {
	follow := fmt.Sprintf("Follow it with 'ankra cluster move %s --status --wait'.", source.Name)
	switch move.Status {
	case client.ZoneMoveStatusPlanned, client.ZoneMoveStatusProvisioning, client.ZoneMoveStatusSeeding:
		return follow + " It stops at ready_for_cutover and waits for you; traffic does not move until then."
	case client.ZoneMoveStatusReadyForCutover:
		return fmt.Sprintf("The target is seeded and in standby. Switch traffic when you are ready with "+
			"'ankra cluster move %s --cutover'.", source.Name)
	case client.ZoneMoveStatusFreezing, client.ZoneMoveStatusFinalCopy, client.ZoneMoveStatusCuttingOver:
		return "The cutover is under way: the maintenance page is on while the final copy is taken. " + follow
	case client.ZoneMoveStatusSoaking:
		soak := ""
		if move.SoakUntil != nil && *move.SoakUntil != "" {
			soak = " until " + *move.SoakUntil
		}
		return fmt.Sprintf("Traffic is on the target, soaking%s. Roll back with 'ankra cluster move %[2]s --rollback', "+
			"or retire the source with 'ankra cluster move %[2]s --retire'.", soak, source.Name)
	case client.ZoneMoveStatusRollingBack, client.ZoneMoveStatusRetiring:
		return follow
	case client.ZoneMoveStatusRolledBack:
		return "Traffic is back on the source. Writes made on the target after the cutover were not copied back."
	case client.ZoneMoveStatusRetired:
		return "The source has been deprovisioned. The move is complete."
	case client.ZoneMoveStatusFailed:
		return "The move failed" + zoneMoveFailureDetail(move) + ". The steps above say where."
	}
	return follow
}

func printZoneMove(out io.Writer, source zoneMoveSource, move *client.ZoneMove) {
	_, _ = fmt.Fprintf(out, "Zone move %s\n", move.ID)
	_, _ = fmt.Fprintf(out, "  Source:  %s (%s)\n", source.Name, source.ID)
	target := move.Plan.Target.Name
	if move.TargetClusterID != nil && *move.TargetClusterID != "" {
		if target != "" {
			target += " (" + *move.TargetClusterID + ")"
		} else {
			target = *move.TargetClusterID
		}
	} else if target != "" {
		target += " (not created yet)"
	}
	if target != "" {
		_, _ = fmt.Fprintf(out, "  Target:  %s\n", target)
	}
	_, _ = fmt.Fprintf(out, "  Zone:    %s\n", move.Zone)
	_, _ = fmt.Fprintf(out, "  Status:  %s\n", move.Status)
	if move.SoakUntil != nil && *move.SoakUntil != "" {
		_, _ = fmt.Fprintf(out, "  Soak until: %s\n", *move.SoakUntil)
	}
	if len(move.Steps) > 0 {
		_, _ = fmt.Fprintln(out)
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Step", "Status", "Started", "Finished", "Message"})
		for _, step := range move.Steps {
			writer.AppendRow(table.Row{step.Name, step.Status, optionalText(step.StartedAt),
				optionalText(step.FinishedAt), optionalText(step.Message)})
		}
		writer.Render()
	}
	_, _ = fmt.Fprintf(out, "\n%s\n", zoneMoveNextStep(source, move))
}

// printZoneMovePlan renders the plan: what is built, what is carried with
// its size, what is left behind, and how each hostname moves.
func printZoneMovePlan(out io.Writer, source zoneMoveSource, move *client.ZoneMove, requestedVault string) {
	plan := move.Plan
	target := plan.Target
	zone := target.Zone
	if zone == "" {
		zone = move.Zone
	}
	_, _ = fmt.Fprintf(out, "Zone move plan: %s -> zone %s\n", source.Name, zone)

	_, _ = fmt.Fprintf(out, "\nTarget cluster: %s\n", target.Name)
	if target.NetworkMode != "" {
		_, _ = fmt.Fprintf(out, "  Network:        %s\n", target.NetworkMode)
	}
	if target.StorageTier != "" {
		_, _ = fmt.Fprintf(out, "  Storage tier:   %s\n", target.StorageTier)
	}
	if target.ControlPlane.Count > 0 || target.ControlPlane.Plan != "" {
		_, _ = fmt.Fprintf(out, "  Control plane:  %d x %s\n", target.ControlPlane.Count, target.ControlPlane.Plan)
	}
	if target.BastionPlan != "" {
		_, _ = fmt.Fprintf(out, "  Bastion:        %s\n", target.BastionPlan)
	}
	for _, group := range target.NodeGroups {
		_, _ = fmt.Fprintf(out, "  Node group %s: %d x %s\n", group.Name, group.Count, group.Plan)
	}

	carried, skipped := []string{}, []string{}
	for _, stack := range plan.Stacks {
		if stack.Carried {
			carried = append(carried, stack.Name+describeZoneMoveStackVault(stack, plan))
			continue
		}
		reason := "not carried"
		if stack.Reason != nil && *stack.Reason != "" {
			reason = *stack.Reason
		}
		skipped = append(skipped, fmt.Sprintf("%s: %s", stack.Name, reason))
	}
	_, _ = fmt.Fprintf(out, "\nStacks carried (%d):\n", len(carried))
	if len(carried) == 0 {
		_, _ = fmt.Fprintln(out, "  none")
	}
	for _, name := range carried {
		_, _ = fmt.Fprintf(out, "  - %s\n", name)
	}
	if len(skipped) > 0 {
		_, _ = fmt.Fprintf(out, "\nStacks not carried (%d):\n", len(skipped))
		for _, line := range skipped {
			_, _ = fmt.Fprintf(out, "  - %s\n", line)
		}
	}

	var totalBytes int64
	for _, volume := range plan.Data {
		totalBytes += volume.RequestedBytes
	}
	if len(plan.Data) == 0 {
		_, _ = fmt.Fprintf(out, "\nData volumes copied: none\n")
	} else {
		_, _ = fmt.Fprintf(out, "\nData volumes copied (%d, %s requested):\n", len(plan.Data), formatByteSize(totalBytes))
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Kind", "Namespace", "Name", "Size"})
		for _, volume := range plan.Data {
			writer.AppendRow(table.Row{volume.Kind, volume.Namespace, volume.Name, formatByteSize(volume.RequestedBytes)})
		}
		writer.Render()
	}

	if len(plan.NotCarried) > 0 {
		_, _ = fmt.Fprintf(out, "\nNot carried (%d):\n", len(plan.NotCarried))
		for _, omission := range plan.NotCarried {
			_, _ = fmt.Fprintf(out, "  - %s: %s\n", omission.What, omission.Reason)
		}
	}

	if len(plan.Warnings) > 0 {
		_, _ = fmt.Fprintf(out, "\nWarnings (%d):\n", len(plan.Warnings))
		mentionsVault := false
		for _, warning := range plan.Warnings {
			_, _ = fmt.Fprintf(out, "  - %s\n", warning)
			mentionsVault = mentionsVault || mentionsZoneMoveBackupVault(warning)
		}
		if mentionsVault {
			_, _ = fmt.Fprintf(out, "  %s\n", zoneMoveVaultHint(requestedVault))
		}
	}

	if len(plan.DNS) == 0 {
		_, _ = fmt.Fprintf(out, "\nHostnames cut over: none\n")
		return
	}
	_, _ = fmt.Fprintf(out, "\nHostnames cut over (%d):\n", len(plan.DNS))
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Hostname", "Cut over by", "Source origin", "Target origin"})
	for _, hostname := range plan.DNS {
		targetOrigin := "created by the move"
		if hostname.TargetOrigin != nil && *hostname.TargetOrigin != "" {
			targetOrigin = *hostname.TargetOrigin
		}
		writer.AppendRow(table.Row{hostname.Hostname, describeZoneMoveDNSMechanism(hostname.Mechanism),
			optionalText(hostname.SourceOrigin), targetOrigin})
	}
	writer.Render()
}

// describeZoneMoveStackVault is the suffix a carried stack's line gets: the
// vault its data copies write through and what chose it, "" for a stack the
// move copies no data of. A stack with data and no vault says why rather
// than reading like a stack without data: its data is not carried (the
// platform could not choose a vault), or the platform did not say.
func describeZoneMoveStackVault(stack client.ZoneMoveStack, plan client.ZoneMovePlan) string {
	vault := stack.Vault
	if vault == nil {
		if !zoneMoveStackHasData(stack.Name, plan.Data) {
			return ""
		}
		dataOmission := fmt.Sprintf("the data of stack '%s'", stack.Name)
		for _, omission := range plan.NotCarried {
			if omission.What == dataOmission {
				return " (data not carried: no backup vault, see Not carried)"
			}
		}
		return " (data vault not reported by the platform)"
	}
	name := vault.Name
	if name == "" {
		name = vault.ID
	}
	if name == "" {
		return ""
	}
	basis := vault.Basis
	switch vault.Basis {
	case client.ZoneMoveVaultBasisRequest:
		basis = "named with --vault"
	case client.ZoneMoveVaultBasisStackPolicy:
		basis = "the stack's backup policy"
	case client.ZoneMoveVaultBasisOnlyReady:
		basis = "the organisation's only ready vault"
	}
	if basis == "" {
		return fmt.Sprintf(" (data through vault %s)", name)
	}
	return fmt.Sprintf(" (data through vault %s: %s)", name, basis)
}

func zoneMoveStackHasData(stackName string, data []client.ZoneMoveDataVolume) bool {
	for _, volume := range data {
		if volume.Stack == stackName {
			return true
		}
	}
	return false
}

func describeZoneMoveDNSMechanism(mechanism string) string {
	if mechanism == client.ZoneMoveDNSMechanismAnkraCloudProxied {
		return "Ankra Cloud edge: proxied record repointed to the target load balancer"
	}
	if mechanism == "" {
		return "-"
	}
	return mechanism
}
