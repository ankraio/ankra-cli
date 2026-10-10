package cmd

// `ankra exec`: run a heavy command (go test, golangci-lint run, pnpm
// typecheck, vitest run, a Playwright suite) for this git worktree in its
// Ankra Workspace instead of on this machine, streaming the output back live
// and exiting with the remote command's exit code. The native client of the
// Workspaces API (ankra-b5c3as.6), and a Go port of claude-tools
// remote-exec/bin/ankra-exec whose contract the routing shims rely on:
//
//   - 196 and $ANKRA_EXEC_NOTRUN_FILE when the command did not run remotely
//     (no workspace possible, the API unreachable after retries, refused
//     before it started): the shims key their local fallback on the file, so
//     a remote command that itself exits 196 is passed through, not re-run.
//   - otherwise the remote command's own exit code, decided by whether it
//     started, never by the code (196 and 197 pass through too).
//   - 130 after Ctrl-C, having asked the workspace to stop the command.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const (
	// execExitNotRun says the command did not run remotely.
	execExitNotRun = 196
	// execExitNeedsFull is a run that never started because the workspace
	// lacks history the thin bundle builds on: resend it whole, once.
	execExitNeedsFull = 197
	// execExitInterrupted is Ctrl-C.
	execExitInterrupted = 130
	// execLargeBundleBytes is the size above which the first sync is announced.
	execLargeBundleBytes = 50_000_000
	// execMaxEnvValueBytes is the platform's bound on one forwarded value.
	execMaxEnvValueBytes = 4096
	// execDefaultRetries is how many times a run that could not be started
	// is retried (ANKRA_EXEC_RETRIES): the API restarts in about that.
	execDefaultRetries = 3
	// execStreamFailureBudget is how many consecutive stream connections may
	// fail without delivering anything before the run's state is read
	// instead.
	execStreamFailureBudget = 6
	// execReconnectBudget bounds consecutive server-asked reconnects that
	// deliver nothing.
	execReconnectBudget = 30
	// execDefaultWaitTimeout bounds the wait for a provisioning workspace
	// (ANKRA_EXEC_WAIT_TIMEOUT).
	execDefaultWaitTimeout = 15 * time.Minute
	// execCheckPositiveTTL and execCheckNegativeTTL are how long --check
	// trusts its last answer: the guards call it on every classified command.
	execCheckPositiveTTL = time.Hour
	execCheckNegativeTTL = 10 * time.Minute
)

// execForwardEnv is the bash client's FORWARD_ENV: the environment a remote
// command sees from this machine, plus the names in ANKRA_EXEC_FORWARD_ENV.
var execForwardEnv = []string{
	"E2E_TARGET", "BASE_URL", "PLAYWRIGHT_BASE_URL", "GOFLAGS", "CGO_ENABLED", "GOEXPERIMENT", "GODEBUG",
	"GOARCH", "GOOS", "GOMAXPROCS", "GORACE", "PARITY_DATABASE_URL", "QUEUE_PARITY_DATABASE_URL",
	"NODE_OPTIONS", "NODE_ENV", "TZ", "VITEST_MAX_THREADS", "VITEST_MIN_THREADS", "VITEST_POOL",
	"VITEST_SHARD_COUNT", "ANKRA_SKIP_GO_CHECK",
}

// execSleep waits between retries; tests replace it.
var execSleep = func(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// execNow is the clock; tests replace it.
var execNow = time.Now

var execCmd = &cobra.Command{
	Use:   "exec [flags] [--] <command> [args...]",
	Short: "Run a heavy command for this worktree in its Ankra Workspace, streaming the output back",
	Long: `Run a command (go test, golangci-lint run, pnpm typecheck, vitest run, a
Playwright suite) for this git worktree in your Ankra Workspace instead of on
this machine. Output streams back live and the exit code is the remote
command's own.

The worktree is sent as it is - tracked files plus untracked files that are
not ignored - as a snapshot commit built in a temporary index (the branch, the
index and the working tree are untouched). Only what the workspace lacks is
uploaded: which commits it holds is recorded under refs/ankra-exec/synced/.
The command runs in the same relative directory, with only an allowlisted
environment forwarded (GOFLAGS, NODE_OPTIONS, TZ, ... and the names in
ANKRA_EXEC_FORWARD_ENV).

The workspace is this checkout's (origin remote, a repository connected to
Ankra Pipelines) of the --kind given, brought up on first use.

  --apply       the command rewrites files (gofmt -w, go generate): its changes
                come back as a patch applied to this worktree. A patch that no
                longer applies is kept under .git/ankra-exec-unapplied-<run>.patch.
  --fetch DIR   copy DIR back from the workspace after the run, replacing the
                local one, whatever the exit code (test-results, reports).
  --check       exit 0 when this worktree can use a workspace (cheap, cached).

Exit codes: the remote command's own when it ran; 196 when it did not run
remotely (and $ANKRA_EXEC_NOTRUN_FILE is created when set, which is what the
routing shims fall back on); 130 after Ctrl-C, which stops the remote command;
2 for a usage error. Each run appends a line to ~/.claude/logs/ankra-exec.log
(ANKRA_EXEC_LOG).`,
	Example: `  ankra exec -- go test ./internal/...
  ankra exec pnpm typecheck
  ankra exec --apply -- gofmt -w ./cmd
  ankra exec --kind playwright --fetch test-results -- pnpm test:e2e
  ankra exec --check && echo routable`,
	Annotations: map[string]string{
		// Credentials are resolved inside: a missing login is "not run" (196),
		// which the shims fall back on, never the auth exit code.
		annotationRequiresAuth: "false",
	},
	RunE: runExec,
}

func init() {
	execCmd.Flags().Bool("apply", false, "Apply the changes the command makes in the workspace to this worktree")
	execCmd.Flags().StringArray("fetch", nil, "Copy this directory back from the workspace after the run (repeatable)")
	execCmd.Flags().String("kind", "", "Workspace kind (default: the repository's default workspace)")
	execCmd.Flags().Bool("check", false, "Exit 0 when this worktree can use a workspace, 1 otherwise")
	// Everything from the first non-flag argument on is the command:
	// `ankra exec go test -run X ./...` needs no --.
	execCmd.Flags().SetInterspersed(false)
	rootCmd.AddCommand(execCmd)
}

// errExecSilent carries an exit code whose message was already printed.
var errExecSilent = errors.New("")

func runExec(cmd *cobra.Command, args []string) error {
	isApplying, _ := cmd.Flags().GetBool("apply")
	fetchDirectories, _ := cmd.Flags().GetStringArray("fetch")
	kind, _ := cmd.Flags().GetString("kind")
	isCheckOnly, _ := cmd.Flags().GetBool("check")
	if len(args) == 0 && !isCheckOnly {
		return withExitCode(exitUsage, errors.New(
			"name the command to run: ankra exec [--apply] [--fetch DIR]... [--kind K] [--] <command> [args...]"))
	}
	if len(args) > 0 && args[0] == "" {
		return withExitCode(exitUsage, errors.New("the command must not be empty"))
	}
	cmd.SilenceErrors = true

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	runner := &execRunner{
		cmd:              cmd,
		stdout:           cmd.OutOrStdout(),
		stderr:           cmd.ErrOrStderr(),
		kind:             kind,
		argv:             args,
		isApplying:       isApplying,
		fetchDirectories: fetchDirectories,
		startedAt:        execNow(),
	}
	var code int
	if isCheckOnly {
		code = runner.check(ctx)
	} else {
		code = runner.run(ctx)
	}
	if code == 0 {
		return nil
	}
	return withExitCode(code, errExecSilent)
}

// execRunner is one `ankra exec` invocation.
type execRunner struct {
	cmd              *cobra.Command
	stdout           io.Writer
	stderr           io.Writer
	kind             string
	argv             []string
	isApplying       bool
	fetchDirectories []string
	startedAt        time.Time

	// isEmbedded is a run made for a routing shim inside this process
	// (ankra dev shim): "not run" is then answered to the caller through
	// didNotRun and notRunReason instead of the marker file and a message,
	// since the caller decides what to say before it runs the tool here.
	isEmbedded   bool
	didNotRun    bool
	notRunReason string

	checkout     workspaceCheckout
	workspace    *client.Workspace
	snapshot     execSnapshot
	env          map[string]string
	temporaryDir string
	bundleBytes  int64
}

func (runner *execRunner) say(format string, arguments ...any) {
	_, _ = fmt.Fprintf(runner.stderr, "ankra exec: "+format+"\n", arguments...)
}

// notRun says why the command did not run remotely, creates the shims'
// marker file and answers 196.
func (runner *execRunner) notRun(reason string) int {
	runner.didNotRun = true
	runner.notRunReason = reason
	if runner.isEmbedded {
		return execExitNotRun
	}
	if markerPath := os.Getenv("ANKRA_EXEC_NOTRUN_FILE"); markerPath != "" {
		if file, createError := os.Create(markerPath); createError == nil {
			_ = file.Close()
		}
	}
	runner.say("%s", reason)
	return execExitNotRun
}

// ensureClient resolves credentials the way every command does, which the
// auth annotation skipped so a missing login is "not run".
func ensureExecClient(cmd *cobra.Command) error {
	if apiClient == nil {
		resolved, resolveError := resolveCredentials(cmd)
		if resolveError != nil {
			return resolveError
		}
		apiToken = resolved.token
		baseURL = resolved.baseURL
		apiClient = newAPIClient()
	}
	orgFlag, _ := flagValue(cmd.Root().PersistentFlags().Lookup("org"))
	if strings.TrimSpace(orgFlag) == "" && strings.TrimSpace(os.Getenv(envAnkraOrg)) == "" {
		// The bash client's config named the organisation as ANKRA_EXEC_ORG.
		if execOrg := strings.TrimSpace(os.Getenv("ANKRA_EXEC_ORG")); execOrg != "" {
			orgID, orgError := resolveOrgFlagToID(execOrg)
			if orgError != nil {
				return orgError
			}
			apiClient.SetOrganisationOverride(orgID)
			return nil
		}
	}
	return applyOrganisationOverride(cmd)
}

// isTransientWorkspaceError reports whether a failed call is worth retrying:
// a dropped connection or a passing platform state, never a refusal.
func isTransientWorkspaceError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var workspaceError *client.WorkspaceAPIError
	if errors.As(err, &workspaceError) {
		return workspaceError.IsRetryable()
	}
	var permissionDenied *client.PermissionDeniedError
	if errors.Is(err, client.ErrUnauthorized) || errors.As(err, &permissionDenied) {
		return false
	}
	// Anything else never reached a platform answer: a connection refused,
	// reset or timed out.
	return true
}

// execRetries is ANKRA_EXEC_RETRIES, default execDefaultRetries.
func execRetries() int {
	if raw := strings.TrimSpace(os.Getenv("ANKRA_EXEC_RETRIES")); raw != "" {
		if parsed, parseError := strconv.Atoi(raw); parseError == nil && parsed >= 0 {
			return parsed
		}
	}
	return execDefaultRetries
}

// execWaitTimeout is ANKRA_EXEC_WAIT_TIMEOUT (a Go duration), default
// execDefaultWaitTimeout.
func execWaitTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("ANKRA_EXEC_WAIT_TIMEOUT")); raw != "" {
		if parsed, parseError := time.ParseDuration(raw); parseError == nil && parsed > 0 {
			return parsed
		}
	}
	return execDefaultWaitTimeout
}

// forwardedEnv collects the allowlisted variables that are set here.
func (runner *execRunner) forwardedEnv() map[string]string {
	names := append([]string{}, execForwardEnv...)
	names = append(names, strings.Fields(os.Getenv("ANKRA_EXEC_FORWARD_ENV"))...)
	environment := map[string]string{}
	for _, name := range names {
		value, isSet := os.LookupEnv(name)
		if !isSet {
			continue
		}
		if len(value) > execMaxEnvValueBytes || strings.ContainsRune(value, 0) {
			runner.say("not forwarding %s: its value is longer than %d bytes or carries a NUL", name,
				execMaxEnvValueBytes)
			continue
		}
		environment[name] = value
	}
	if len(environment) == 0 {
		return nil
	}
	return environment
}

// upWorkspace brings the checkout's workspace up and waits until it is
// ready. reason is set when it never became usable.
func (runner *execRunner) upWorkspace(ctx context.Context) (*client.Workspace, string) {
	request := workspaceUpRequest(runner.checkout, runner.kind)
	retries := execRetries()
	var workspace *client.Workspace
	for attempt := 0; ; attempt++ {
		upped, _, upError := apiClient.UpWorkspace(ctx, request)
		if upError == nil {
			workspace = upped
			break
		}
		if ctx.Err() != nil {
			return nil, ""
		}
		if !isTransientWorkspaceError(upError) || attempt >= retries {
			return nil, "no workspace for " + runner.checkout.Remote.FullName() + ": " + upError.Error()
		}
		delay := time.Duration(attempt+1) * 2 * time.Second
		runner.say("could not bring the workspace up (%v); retrying in %s", upError, delay)
		if !execSleep(ctx, delay) {
			return nil, ""
		}
	}
	if workspace.Status == client.WorkspaceStatusProvisioning {
		kindLabel := workspace.Kind
		runner.say("bringing up the %s workspace for %s (first use takes a minute or two)", kindLabel,
			runner.checkout.Remote.FullName())
		deadline := execNow().Add(execWaitTimeout())
		failures := 0
		reported := &workspaceProgress{}
		for workspace.Status == client.WorkspaceStatusProvisioning {
			if message, isNew := reported.next(workspace); isNew {
				runner.say("still provisioning: %s", message)
			}
			if execNow().After(deadline) {
				return nil, fmt.Sprintf("the workspace %s was still provisioning after %s", workspace.ID, execWaitTimeout())
			}
			if !execSleep(ctx, workspacePollInterval) {
				return nil, ""
			}
			read, readError := apiClient.GetWorkspace(ctx, workspace.ID)
			if readError != nil {
				if ctx.Err() != nil {
					return nil, ""
				}
				failures++
				if !isTransientWorkspaceError(readError) || failures > retries+3 {
					return nil, "could not read the workspace: " + readError.Error()
				}
				continue
			}
			failures = 0
			workspace = read
		}
	}
	switch workspace.Status {
	case client.WorkspaceStatusReady:
		return workspace, ""
	case client.WorkspaceStatusFailed:
		reason := "no reason recorded"
		if workspace.LastError != nil && strings.TrimSpace(*workspace.LastError) != "" {
			reason = strings.TrimSpace(*workspace.LastError)
		}
		return nil, fmt.Sprintf("the workspace %s failed: %s (see 'ankra workspace status'; "+
			"'ankra workspace down' and retry replaces it)", workspace.ID, reason)
	default:
		return nil, fmt.Sprintf("the workspace %s is %s", workspace.ID, workspace.Status)
	}
}

// execAttempt is how one attempt to run the command ended.
type execAttempt struct {
	// isStarted says the command started: ExitCode is then its answer.
	isStarted bool
	ExitCode  int
	RunID     string
	// isInterrupted is Ctrl-C.
	isInterrupted bool
	// isTransient says it is worth another attempt; reason says why it
	// failed otherwise.
	isTransient bool
	// needsWorkspace says the workspace went away or is not ready: bring it
	// up again before the next attempt.
	needsWorkspace bool
	reason         string
}

func (runner *execRunner) run(parent context.Context) int {
	ctx, stopSignals := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	checkout, checkoutError := inspectWorkspaceCheckout(ctx, ".")
	if checkoutError != nil {
		return runner.notRun(checkoutError.Error())
	}
	runner.checkout = checkout
	head, headError := gitStdout(ctx, checkout.Top, nil, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	if headError != nil || head == "" {
		return runner.notRun("the worktree has no commit yet")
	}
	if clientError := ensureExecClient(runner.cmd); clientError != nil {
		return runner.finishNotRun("no Ankra credentials: " + clientError.Error())
	}

	temporaryDir, temporaryError := os.MkdirTemp("", "ankra-exec.")
	if temporaryError != nil {
		return runner.finishNotRun("could not create a temporary directory: " + temporaryError.Error())
	}
	runner.temporaryDir = temporaryDir
	defer func() { _ = os.RemoveAll(temporaryDir) }()

	snapshot, snapshotError := takeExecSnapshot(ctx, checkout, head, temporaryDir)
	if snapshotError != nil {
		return runner.finishNotRun(snapshotError.Error())
	}
	runner.snapshot = snapshot
	runner.env = runner.forwardedEnv()

	workspace, reason := runner.upWorkspace(ctx)
	if workspace == nil {
		if ctx.Err() != nil {
			return execExitInterrupted
		}
		return runner.finishNotRun(reason)
	}
	runner.workspace = workspace

	// The retry budget is the run's total, as in the bash client: a command
	// that keeps failing to start falls back after ANKRA_EXEC_RETRIES tries,
	// however far each one got.
	retries := execRetries()
	isThin := true
	hasResentFull := false
	attemptNumber := 0
	for {
		markerPrefix := execMarkerPrefix(runner.workspace.ID)
		if isThin {
			if markerError := reconcileShallowMarkers(ctx, runner.snapshot, markerPrefix); markerError != nil {
				return runner.finishNotRun("could not read the sync markers: " + markerError.Error())
			}
		}
		attempt := runner.attempt(ctx, markerPrefix, isThin)
		switch {
		case attempt.isInterrupted:
			return execExitInterrupted
		case attempt.isStarted:
			return runner.finishRan(ctx, attempt, markerPrefix)
		case attempt.ExitCode == execExitNeedsFull && !hasResentFull:
			// The workspace lacks history the thin bundle builds on.
			hasResentFull = true
			isThin = false
			if dropError := dropExecMarkers(ctx, runner.snapshot.Top, markerPrefix); dropError != nil {
				return runner.finishNotRun("could not reset the sync markers: " + dropError.Error())
			}
			continue
		case !attempt.isTransient:
			return runner.finishNotRun(attempt.reason)
		}
		attemptNumber++
		if attemptNumber > retries {
			return runner.finishNotRun(attempt.reason)
		}
		delay := time.Duration(attemptNumber) * 2 * time.Second
		runner.say("%s; retrying in %s", attempt.reason, delay)
		if !execSleep(ctx, delay) {
			return execExitInterrupted
		}
		if attempt.needsWorkspace {
			upped, upReason := runner.upWorkspace(ctx)
			if upped == nil {
				if ctx.Err() != nil {
					return execExitInterrupted
				}
				return runner.finishNotRun(upReason)
			}
			runner.workspace = upped
		}
	}
}

// attempt uploads the bundle, starts the run and follows it to its end.
func (runner *execRunner) attempt(ctx context.Context, markerPrefix string, isThin bool) execAttempt {
	bundle, bundleError := makeExecBundle(ctx, runner.snapshot, markerPrefix, isThin,
		filepath.Join(runner.temporaryDir, "bundle"))
	if bundleError != nil {
		if ctx.Err() != nil {
			return execAttempt{isInterrupted: true}
		}
		return execAttempt{reason: "could not bundle the worktree: " + bundleError.Error()}
	}
	runner.bundleBytes = bundle.SizeBytes
	workspaceID := runner.workspace.ID
	request := client.WorkspaceRunRequest{
		AgentID:     runner.snapshot.Agent,
		Argv:        runner.argv,
		CwdPrefix:   runner.snapshot.Prefix,
		Env:         runner.env,
		SnapshotSHA: runner.snapshot.Snapshot,
		HeadSHA:     runner.snapshot.Head,
		BaseRef:     runner.snapshot.BaseRef,
		BaseSHA:     runner.snapshot.BaseSHA,
		Shallow:     runner.snapshot.Shallow,
		Apply:       runner.isApplying,
	}
	if runner.snapshot.BaseSHA == "" {
		request.BaseRef = ""
	}
	if bundle.SizeBytes > 0 {
		if bundle.SizeBytes > execLargeBundleBytes {
			runner.say("sending %d MiB of history to the workspace (first sync for this workspace)",
				bundle.SizeBytes/(1<<20))
		}
		upload, uploadError := apiClient.CreateWorkspaceBundle(ctx, workspaceID, bundle.SHA256, bundle.SizeBytes)
		if uploadError != nil {
			return runner.failedBeforeStart(ctx, "could not register the bundle", uploadError)
		}
		if !upload.Exists {
			file, openError := os.Open(bundle.Path)
			if openError != nil {
				return execAttempt{reason: "could not read the bundle: " + openError.Error()}
			}
			putError := apiClient.UploadPresigned(ctx, upload.UploadURL, file, bundle.SizeBytes)
			_ = file.Close()
			if putError != nil {
				if ctx.Err() != nil {
					return execAttempt{isInterrupted: true}
				}
				return execAttempt{isTransient: true, reason: "could not upload the bundle: " + putError.Error()}
			}
		}
		request.BundleObjectKey = upload.ObjectKey
	}
	started, startError := apiClient.StartWorkspaceRun(ctx, workspaceID, request)
	if startError != nil {
		return runner.failedBeforeStart(ctx, "the workspace did not start the command", startError)
	}
	return runner.follow(ctx, started.RunID)
}

// failedBeforeStart classifies a failure that happened before any run
// existed.
func (runner *execRunner) failedBeforeStart(ctx context.Context, what string, err error) execAttempt {
	if ctx.Err() != nil {
		return execAttempt{isInterrupted: true}
	}
	attempt := execAttempt{reason: what + ": " + err.Error(), isTransient: isTransientWorkspaceError(err)}
	var workspaceError *client.WorkspaceAPIError
	if errors.As(err, &workspaceError) {
		switch {
		case workspaceError.StatusCode == http.StatusNotFound:
			// The workspace was reaped or torn down since it was brought up.
			attempt.isTransient = true
			attempt.needsWorkspace = true
		case workspaceError.Code == "WORKSPACE_NOT_READY":
			attempt.needsWorkspace = true
		}
	}
	return attempt
}

// follow streams a run's output from where it got to, reconnecting whenever
// the connection drops or the platform asks, until the run's exit arrives.
func (runner *execRunner) follow(ctx context.Context, runID string) execAttempt {
	workspaceID := runner.workspace.ID
	var offsets client.WorkspaceRunOffsets
	hasOutput := false
	failures := 0
	idleReconnects := 0
	lastProblem := ""
	for {
		if ctx.Err() != nil {
			return runner.interrupt(runID)
		}
		events, streamError := apiClient.StreamWorkspaceRun(ctx, workspaceID, runID, offsets)
		progressed := false
		askedToReconnect := false
		if streamError != nil {
			if ctx.Err() != nil {
				return runner.interrupt(runID)
			}
			lastProblem = streamError.Error()
			var workspaceError *client.WorkspaceAPIError
			if errors.As(streamError, &workspaceError) && !workspaceError.IsRetryable() {
				// The stream itself was refused (the run or the workspace is
				// gone): the run's state is the answer.
				return runner.settle(ctx, runID, hasOutput, lastProblem)
			}
		} else {
			for event := range events {
				switch event.Type {
				case client.WorkspaceRunEventStdout:
					if _, writeError := runner.stdout.Write(event.Data); writeError != nil {
						runner.say("writing the output: %v", writeError)
					}
					offsets.Stdout += int64(len(event.Data))
					hasOutput, progressed = true, true
				case client.WorkspaceRunEventStderr:
					if _, writeError := runner.stderr.Write(event.Data); writeError != nil {
						runner.say("writing the output: %v", writeError)
					}
					offsets.Stderr += int64(len(event.Data))
					hasOutput, progressed = true, true
				case client.WorkspaceRunEventExit:
					return execAttempt{isStarted: event.Started, ExitCode: event.ExitCode, RunID: runID,
						isTransient: !event.Started && event.ExitCode != execExitNeedsFull,
						reason:      notStartedReason(event.ExitCode)}
				case client.WorkspaceRunEventReconnect:
					askedToReconnect = true
				case client.WorkspaceRunEventError:
					lastProblem = event.Message
					if !event.IsDrop {
						// The platform says the run can no longer be followed
						// (its files are gone): its recorded state is the answer.
						return runner.settle(ctx, runID, hasOutput, lastProblem)
					}
				}
			}
		}
		if ctx.Err() != nil {
			return runner.interrupt(runID)
		}
		if progressed {
			failures, idleReconnects = 0, 0
		}
		if askedToReconnect {
			idleReconnects++
			if idleReconnects <= execReconnectBudget {
				continue
			}
			idleReconnects = 0
		}
		failures++
		if failures > execStreamFailureBudget {
			return runner.settle(ctx, runID, hasOutput, lastProblem)
		}
		delay := time.Duration(1<<min(failures-1, 4)) * 500 * time.Millisecond
		if hasOutput {
			runner.say("the output stream dropped (%s); the command keeps running, re-attaching (attempt %d)",
				lastProblem, failures)
		}
		if !execSleep(ctx, delay) {
			return runner.interrupt(runID)
		}
	}
}

// notStartedReason explains a run that ended before its command started.
func notStartedReason(code int) string {
	switch code {
	case execExitNeedsFull:
		return "the workspace lacks history the bundle builds on"
	case 75:
		return "the workspace could not download the bundle"
	default:
		return fmt.Sprintf("the workspace did not start the command (exit %d)", code)
	}
}

// settle answers a run whose stream could not be followed to its end, from
// the run's recorded state.
func (runner *execRunner) settle(ctx context.Context, runID string, hasOutput bool, problem string) execAttempt {
	run, readError := apiClient.GetWorkspaceRun(ctx, runner.workspace.ID, runID)
	if ctx.Err() != nil {
		return runner.interrupt(runID)
	}
	if readError == nil && run.IsTerminal() && run.ExitCode != nil {
		if run.IsStarted() {
			runner.say("lost the output stream (%s); run %s ended with exit %d, some output may be missing",
				problem, runID, *run.ExitCode)
			return execAttempt{isStarted: true, ExitCode: *run.ExitCode, RunID: runID}
		}
		if run.Started != nil {
			code := *run.ExitCode
			return execAttempt{ExitCode: code, RunID: runID, isTransient: code != execExitNeedsFull,
				reason: notStartedReason(code)}
		}
	}
	if readError == nil && run.IsTerminal() && run.IsStarted() {
		runner.say("lost the output stream (%s); run %s ended (%s) without a recorded exit code, "+
			"some output may be missing", problem, runID, run.State)
		return execAttempt{isStarted: true, ExitCode: 1, RunID: runID}
	}
	if readError == nil && run.IsStarted() {
		hasOutput = true
	}
	if !hasOutput {
		// Nothing proves the command started, so it may run elsewhere - once
		// the workspace is told not to start it after all.
		cancelContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if cancelError := apiClient.CancelWorkspaceRun(cancelContext, runner.workspace.ID, runID); cancelError != nil {
			runner.say("could not tell the workspace to drop run %s (%v); if it starts after all, "+
				"it runs there as well as here", runID, cancelError)
		}
		cancel()
		return execAttempt{reason: "lost the workspace before the command started: " + problem}
	}
	runner.say("lost the output stream of run %s (%s); it may still be running in the workspace", runID, problem)
	return execAttempt{isStarted: true, ExitCode: 1, RunID: runID}
}

// interrupt asks the workspace to stop the run and answers Ctrl-C.
func (runner *execRunner) interrupt(runID string) execAttempt {
	runner.say("interrupted; stopping the remote command")
	cancelContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if cancelError := apiClient.CancelWorkspaceRun(cancelContext, runner.workspace.ID, runID); cancelError != nil {
		runner.say("could not stop run %s: %v", runID, cancelError)
	}
	return execAttempt{isInterrupted: true, RunID: runID}
}

// finishRan records the sync, brings the run's results back and answers its
// exit code.
func (runner *execRunner) finishRan(ctx context.Context, attempt execAttempt, markerPrefix string) int {
	code := attempt.ExitCode
	if recordError := recordExecSynced(ctx, runner.snapshot, markerPrefix, execNow()); recordError != nil {
		runner.say("could not record what the workspace holds (the next run sends more): %v", recordError)
	}
	if runner.isApplying {
		if !runner.applyPatch(ctx, attempt.RunID) {
			code = 1
		}
	}
	runner.fetchBack(ctx, attempt.RunID)
	runner.logRun(code, "remote")
	return code
}

func (runner *execRunner) finishNotRun(reason string) int {
	runner.logRun(execExitNotRun, "not-run")
	return runner.notRun(reason)
}

// logRun appends the run's line to the phase 0 log, in the bash client's
// tab-separated format.
func (runner *execRunner) logRun(code int, mode string) {
	logPath := os.Getenv("ANKRA_EXEC_LOG")
	if logPath == "" {
		home, homeError := os.UserHomeDir()
		if homeError != nil {
			return
		}
		logPath = filepath.Join(home, ".claude", "logs", "ankra-exec.log")
	}
	line := formatExecLogLine(execNow(), runner.checkout.Remote.FullName(), runner.workspaceLabel(), code,
		mode+"/api", runner.bundleBytes, execNow().Sub(runner.startedAt), runner.argv)
	if mkdirError := os.MkdirAll(filepath.Dir(logPath), 0o755); mkdirError != nil {
		return
	}
	file, openError := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if openError != nil {
		return
	}
	_, _ = file.WriteString(line)
	_ = file.Close()
}

func (runner *execRunner) workspaceLabel() string {
	if runner.workspace == nil {
		return ""
	}
	return runner.workspace.ID
}

// formatExecLogLine renders one log line:
// <utc time>\trepo=\tpod=\trc=\tmode=\tbundle_bytes=\twall_ms=\tcmd=<argv, 200 chars>.
func formatExecLogLine(now time.Time, repository string, workspace string, code int, mode string,
	bundleBytes int64, wall time.Duration, argv []string) string {
	var command strings.Builder
	for _, argument := range argv {
		command.WriteString(argument)
		command.WriteByte(' ')
	}
	commandText := command.String()
	if utf8.RuneCountInString(commandText) > 200 {
		commandText = string([]rune(commandText)[:200])
	}
	commandText = strings.NewReplacer("\t", " ", "\n", " ").Replace(commandText)
	return fmt.Sprintf("%s\trepo=%s\tpod=%s\trc=%d\tmode=%s\tbundle_bytes=%d\twall_ms=%d\tcmd=%s\n",
		now.UTC().Format("2006-01-02T15:04:05Z"), repository, workspace, code, mode, bundleBytes,
		wall.Milliseconds(), commandText)
}
