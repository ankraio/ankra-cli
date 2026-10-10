package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// `ankra workspace up|status|down`: the checkout's Ankra Workspace, the pod
// on the organisation's CI pool that `ankra exec` runs commands in. The
// repository is the checkout's origin remote, as a connected pipeline
// repository (ankra-b5c3as.7).

// workspacePollInterval is how often --wait reads a provisioning workspace;
// each read runs its preflight server-side. A variable so tests can shrink it.
var workspacePollInterval = 3 * time.Second

const (
	// defaultWorkspaceKind is the platform's kind when a caller names none.
	defaultWorkspaceKind = "default"
	// defaultWorkspaceWaitTimeout bounds --wait: the platform itself gives a
	// provisioning workspace 15 minutes.
	defaultWorkspaceWaitTimeout = 16 * time.Minute
)

// workspaceCheckout is the git worktree a workspace command runs in.
type workspaceCheckout struct {
	// Top is the worktree's root.
	Top string
	// Prefix is the working directory relative to Top, "" or ending in "/".
	Prefix string
	// Remote is the repository its origin names.
	Remote repositoryRemote
}

// gitStdout runs git in directory with the repository-binding variables
// removed (gitEnvironment) plus extraEnv, and answers its trimmed stdout.
// stderr is kept apart, so a warning never corrupts what is parsed.
func gitStdout(ctx context.Context, directory string, extraEnv []string, arguments ...string) (string, error) {
	output, runError := gitRaw(ctx, directory, extraEnv, nil, arguments...)
	return strings.TrimSpace(string(output)), runError
}

// gitRaw is gitStdout without trimming, with optional stdin.
func gitRaw(ctx context.Context, directory string, extraEnv []string, stdin io.Reader,
	arguments ...string) ([]byte, error) {
	gitCommand := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	gitCommand.Env = append(gitEnvironment(), extraEnv...)
	gitCommand.Stdin = stdin
	var stdout, stderr bytes.Buffer
	gitCommand.Stdout = &stdout
	gitCommand.Stderr = &stderr
	if runError := gitCommand.Run(); runError != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return stdout.Bytes(), fmt.Errorf("git %s: %w", firstArgument(arguments), runError)
		}
		return stdout.Bytes(), fmt.Errorf("git %s: %w: %s", firstArgument(arguments), runError, detail)
	}
	return stdout.Bytes(), nil
}

func firstArgument(arguments []string) string {
	if len(arguments) == 0 {
		return ""
	}
	return arguments[0]
}

// inspectWorkspaceCheckout reads the worktree around directory and the
// repository its origin remote names.
func inspectWorkspaceCheckout(ctx context.Context, directory string) (workspaceCheckout, error) {
	top, topError := gitStdout(ctx, directory, nil, "rev-parse", "--show-toplevel")
	if topError != nil || top == "" {
		return workspaceCheckout{}, errors.New("not inside a git worktree")
	}
	prefix, prefixError := gitStdout(ctx, directory, nil, "rev-parse", "--show-prefix")
	if prefixError != nil {
		return workspaceCheckout{}, fmt.Errorf("reading the worktree prefix: %w", prefixError)
	}
	originURL, originError := gitStdout(ctx, top, nil, "remote", "get-url", "origin")
	if originError != nil || originURL == "" {
		return workspaceCheckout{}, errors.New("the worktree has no origin remote")
	}
	remote, parseError := parseRepositoryRemote(originURL)
	if parseError != nil {
		return workspaceCheckout{}, fmt.Errorf("origin remote: %w", parseError)
	}
	return workspaceCheckout{Top: top, Prefix: prefix, Remote: remote}, nil
}

// workspaceUpRequest names the checkout's repository and kind for
// POST /org/workspaces.
func workspaceUpRequest(checkout workspaceCheckout, kind string) client.WorkspaceUpRequest {
	return client.WorkspaceUpRequest{
		Provider: checkout.Remote.Provider,
		Owner:    checkout.Remote.Owner,
		Name:     checkout.Remote.Name,
		Kind:     kind,
	}
}

// findPipelineRepository finds the connected pipeline repository a remote
// names, walking the provider's listing. found is false when it is not
// connected.
func findPipelineRepository(ctx context.Context, remote repositoryRemote) (*client.PipelineRepository, bool, error) {
	cursor := ""
	for page := 0; page < 50; page++ {
		listed, listError := apiClient.ListPipelineRepositories(ctx, client.ListPipelineRepositoriesOptions{
			Provider: remote.Provider,
			Cursor:   cursor,
			Limit:    100,
		})
		if listError != nil {
			return nil, false, listError
		}
		for index := range listed.Repositories {
			repository := listed.Repositories[index]
			if strings.EqualFold(repository.Provider, remote.Provider) &&
				strings.EqualFold(repository.Owner, remote.Owner) &&
				strings.EqualFold(repository.Name, remote.Name) {
				return &repository, true, nil
			}
		}
		if listed.NextCursor == nil || *listed.NextCursor == "" {
			return nil, false, nil
		}
		cursor = *listed.NextCursor
	}
	return nil, false, errors.New("the repository listing did not end; name fewer repositories or try again")
}

// workspaceAPIError maps a workspace route's refusal onto the CLI's exit
// codes for the up/status/down commands.
func workspaceAPIError(err error) error {
	var workspaceError *client.WorkspaceAPIError
	if errors.As(err, &workspaceError) {
		switch workspaceError.StatusCode {
		case 400, 422:
			return withExitCode(exitUsage, err)
		case 403:
			return withExitCode(exitForbidden, err)
		case 404:
			return withExitCode(exitNotFound, err)
		}
	}
	return err
}

// workspaceProgress remembers the last progress message a wait printed, so a
// message is printed once each time it changes rather than on every read.
type workspaceProgress struct {
	last string
}

// next answers a provisioning workspace's progress message when it differs
// from the last one answered; isNew is false when there is nothing new to
// print.
func (progress *workspaceProgress) next(workspace *client.Workspace) (string, bool) {
	if workspace == nil || workspace.Status != client.WorkspaceStatusProvisioning ||
		workspace.ProgressMessage == nil {
		return "", false
	}
	message := strings.TrimSpace(*workspace.ProgressMessage)
	if message == "" || message == progress.last {
		return "", false
	}
	progress.last = message
	return message, true
}

// waitForWorkspace reads the workspace until it is ready or failed, or the
// timeout passes, printing what it waits on whenever that changes. Each read
// of a provisioning workspace runs its preflight.
func waitForWorkspace(ctx context.Context, workspace *client.Workspace, timeout time.Duration,
	progress io.Writer) (*client.Workspace, error) {
	deadline := time.Now().Add(timeout)
	current := workspace
	announced := false
	reported := &workspaceProgress{}
	for current.Status == client.WorkspaceStatusProvisioning {
		if !announced && progress != nil {
			_, _ = fmt.Fprintf(progress, "Waiting for workspace %s (%s) to be ready...\n", current.ID, current.Kind)
			announced = true
		}
		if message, isNew := reported.next(current); isNew && progress != nil {
			_, _ = fmt.Fprintf(progress, "  %s\n", message)
		}
		if time.Now().After(deadline) {
			return current, withExitCode(exitWaitTimeout, fmt.Errorf(
				"workspace %s was still provisioning after %s", current.ID, timeout))
		}
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-time.After(workspacePollInterval):
		}
		read, readError := apiClient.GetWorkspace(ctx, current.ID)
		if readError != nil {
			var workspaceError *client.WorkspaceAPIError
			if errors.As(readError, &workspaceError) && workspaceError.IsRetryable() {
				continue
			}
			return current, readError
		}
		current = read
	}
	return current, nil
}

func newWorkspaceCommand() *cobra.Command {
	workspaceCommand := &cobra.Command{
		Use:   "workspace",
		Short: "Manage this checkout's Ankra Workspace, where 'ankra exec' runs commands",
		Long: `An Ankra Workspace is your long-lived pod on the organisation's CI pool,
built from the repository's workspace profile (or its pipeline's image), that
'ankra exec' runs heavy commands in instead of on this machine.

The repository is the checkout's origin remote (GitHub, GitLab or Bitbucket),
which must be connected to Ankra Pipelines. Each repository has one workspace
per kind (default "default"); 'ankra exec --kind K' uses the K one.

Needs the workspaces.use permission.`,
	}
	workspaceCommand.AddCommand(newWorkspaceUpCommand(), newWorkspaceStatusCommand(), newWorkspaceDownCommand())
	return workspaceCommand
}

func newWorkspaceUpCommand() *cobra.Command {
	upCommand := &cobra.Command{
		Use:   "up",
		Short: "Bring this checkout's workspace up (idempotent)",
		Long: `Bring this checkout's workspace of the given kind up, or answer the one that
is already live. A new workspace starts provisioning; --wait reads it until it
is ready (the platform runs its preflight on each read) or failed.

Exit codes: 0 when the workspace is up (ready, or provisioning without --wait),
1 when it failed, 5 when --wait timed out.`,
		Example: `  ankra workspace up
  ankra workspace up --kind playwright --wait`,
		Args: cobra.NoArgs,
		RunE: runWorkspaceUp,
	}
	upCommand.Flags().String("kind", defaultWorkspaceKind, "Workspace kind (the repository's workspace profile)")
	upCommand.Flags().Bool("wait", false, "Wait until the workspace is ready or failed")
	upCommand.Flags().Duration("timeout", defaultWorkspaceWaitTimeout, "How long --wait waits")
	registerStructuredOutputFlags(upCommand)
	return upCommand
}

func runWorkspaceUp(cmd *cobra.Command, _ []string) error {
	format, formatError := structuredFormatFromFlags(cmd)
	if formatError != nil {
		return formatError
	}
	kind, _ := cmd.Flags().GetString("kind")
	shouldWait, _ := cmd.Flags().GetBool("wait")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	checkout, checkoutError := inspectWorkspaceCheckout(ctx, ".")
	if checkoutError != nil {
		return withExitCode(exitUsage, checkoutError)
	}
	workspace, isCreated, upError := apiClient.UpWorkspace(ctx, workspaceUpRequest(checkout, kind))
	if upError != nil {
		return workspaceAPIError(upError)
	}
	if format == outputDefault {
		verb := "is up"
		if isCreated {
			verb = "was created"
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Workspace for %s (%s) %s.\n", checkout.Remote.FullName(), workspace.Kind, verb)
	}
	if shouldWait {
		waited, waitError := waitForWorkspace(ctx, workspace, timeout, cmd.ErrOrStderr())
		if waited != nil {
			workspace = waited
		}
		if waitError != nil {
			return waitError
		}
	}
	if written, renderError := renderStructured(cmd, workspace); written || renderError != nil {
		if renderError == nil && workspace.Status == client.WorkspaceStatusFailed {
			return workspaceFailedError(workspace)
		}
		return renderError
	}
	printWorkspace(cmd.OutOrStdout(), checkout.Remote.FullName(), workspace)
	if workspace.Status == client.WorkspaceStatusFailed {
		return workspaceFailedError(workspace)
	}
	return nil
}

func workspaceFailedError(workspace *client.Workspace) error {
	reason := "no reason recorded"
	if workspace.LastError != nil && strings.TrimSpace(*workspace.LastError) != "" {
		reason = strings.TrimSpace(*workspace.LastError)
	}
	return fmt.Errorf("workspace %s failed: %s", workspace.ID, reason)
}

func printWorkspace(out io.Writer, repository string, workspace *client.Workspace) {
	_, _ = fmt.Fprintf(out, "ID:          %s\n", workspace.ID)
	if repository != "" {
		_, _ = fmt.Fprintf(out, "Repository:  %s\n", repository)
	}
	_, _ = fmt.Fprintf(out, "Kind:        %s\n", workspace.Kind)
	_, _ = fmt.Fprintf(out, "Status:      %s\n", workspace.Status)
	if workspace.Image != nil && *workspace.Image != "" {
		source := ""
		if workspace.ImageSource != nil && *workspace.ImageSource != "" {
			source = " (" + *workspace.ImageSource + ")"
		}
		_, _ = fmt.Fprintf(out, "Image:       %s%s\n", *workspace.Image, source)
	}
	if workspace.Namespace != nil && workspace.PodName != nil {
		_, _ = fmt.Fprintf(out, "Pod:         %s/%s\n", *workspace.Namespace, *workspace.PodName)
	}
	if !workspace.LastUsedAt.IsZero() {
		_, _ = fmt.Fprintf(out, "Last used:   %s\n", workspace.LastUsedAt.Local().Format(time.RFC3339))
	}
	if workspace.ExpiresAt != nil {
		_, _ = fmt.Fprintf(out, "Expires:     %s (idle TTL %dh)\n", workspace.ExpiresAt.Local().Format(time.RFC3339),
			workspace.IdleTTLHours)
	}
	if workspace.Status == client.WorkspaceStatusProvisioning && workspace.ProgressMessage != nil &&
		strings.TrimSpace(*workspace.ProgressMessage) != "" {
		_, _ = fmt.Fprintf(out, "Waiting on:  %s\n", strings.TrimSpace(*workspace.ProgressMessage))
	}
	if workspace.LastError != nil && strings.TrimSpace(*workspace.LastError) != "" {
		_, _ = fmt.Fprintf(out, "Last error:  %s\n", strings.TrimSpace(*workspace.LastError))
	}
}

func newWorkspaceStatusCommand() *cobra.Command {
	statusCommand := &cobra.Command{
		Use:   "status",
		Short: "Show this checkout's workspaces",
		Long: `Show this checkout's live workspaces (every kind, or the --kind one). Reading a
provisioning workspace moves it on: the platform runs its preflight.

Exit codes: 0 when a workspace was found, 3 when the repository has none (or
is not connected to Ankra Pipelines).`,
		Example: `  ankra workspace status
  ankra workspace status --kind playwright -o json`,
		Args: cobra.NoArgs,
		RunE: runWorkspaceStatus,
	}
	statusCommand.Flags().String("kind", "", "Only the workspace of this kind")
	registerStructuredOutputFlags(statusCommand)
	return statusCommand
}

// checkoutWorkspaces lists the caller's live workspaces of the checkout's
// repository, optionally of one kind.
func checkoutWorkspaces(ctx context.Context, checkout workspaceCheckout, kind string) ([]client.Workspace, error) {
	repository, isFound, findError := findPipelineRepository(ctx, checkout.Remote)
	if findError != nil {
		return nil, findError
	}
	if !isFound {
		return nil, withExitCode(exitNotFound, fmt.Errorf(
			"%s (%s) is not connected to Ankra Pipelines in this organisation", checkout.Remote.FullName(),
			checkout.Remote.Provider))
	}
	listed, listError := apiClient.ListWorkspaces(ctx, client.ListWorkspacesOptions{
		Kind:         kind,
		RepositoryID: repository.ID,
	})
	if listError != nil {
		return nil, workspaceAPIError(listError)
	}
	live := make([]client.Workspace, 0, len(listed.Items))
	for _, workspace := range listed.Items {
		if workspace.Status != client.WorkspaceStatusDestroyed {
			live = append(live, workspace)
		}
	}
	return live, nil
}

func runWorkspaceStatus(cmd *cobra.Command, _ []string) error {
	if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
		return formatError
	}
	kind, _ := cmd.Flags().GetString("kind")
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	checkout, checkoutError := inspectWorkspaceCheckout(ctx, ".")
	if checkoutError != nil {
		return withExitCode(exitUsage, checkoutError)
	}
	workspaces, listError := checkoutWorkspaces(ctx, checkout, kind)
	if listError != nil {
		return listError
	}
	// A provisioning workspace is read once more: the read is what moves it
	// on, and what the caller wants to know is where it is now.
	for index := range workspaces {
		if workspaces[index].Status != client.WorkspaceStatusProvisioning {
			continue
		}
		if read, readError := apiClient.GetWorkspace(ctx, workspaces[index].ID); readError == nil {
			workspaces[index] = *read
		}
	}
	if written, renderError := renderStructured(cmd, workspaces); written || renderError != nil {
		if renderError == nil && len(workspaces) == 0 {
			return withExitCode(exitNotFound, errors.New("no live workspace for this checkout"))
		}
		return renderError
	}
	if len(workspaces) == 0 {
		hint := "ankra workspace up"
		if kind != "" {
			hint += " --kind " + kind
		}
		return withExitCode(exitNotFound, fmt.Errorf("no live workspace for %s; bring one up with '%s'",
			checkout.Remote.FullName(), hint))
	}
	for index := range workspaces {
		if index > 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout())
		}
		printWorkspace(cmd.OutOrStdout(), checkout.Remote.FullName(), &workspaces[index])
	}
	return nil
}

func newWorkspaceDownCommand() *cobra.Command {
	downCommand := &cobra.Command{
		Use:   "down",
		Short: "Tear this checkout's workspace down",
		Long: `Tear this checkout's workspace of the given kind down: its pod, its volume and
everything synced into it. The next 'ankra exec' or 'ankra workspace up'
brings a fresh one up (and sends the checkout's history again).

Asks for confirmation unless --yes is given; declining exits 4. Exits 3 when
there is no live workspace of that kind.`,
		Example: `  ankra workspace down
  ankra workspace down --kind playwright --yes`,
		Args: cobra.NoArgs,
		RunE: runWorkspaceDown,
	}
	downCommand.Flags().String("kind", defaultWorkspaceKind, "Workspace kind to tear down")
	downCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(downCommand)
	return downCommand
}

func runWorkspaceDown(cmd *cobra.Command, _ []string) error {
	if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
		return formatError
	}
	kind, _ := cmd.Flags().GetString("kind")
	isConfirmed, _ := cmd.Flags().GetBool("yes")
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	checkout, checkoutError := inspectWorkspaceCheckout(ctx, ".")
	if checkoutError != nil {
		return withExitCode(exitUsage, checkoutError)
	}
	workspaces, listError := checkoutWorkspaces(ctx, checkout, kind)
	if listError != nil {
		return listError
	}
	if len(workspaces) == 0 {
		return withExitCode(exitNotFound, fmt.Errorf("no live %s workspace for %s", kind, checkout.Remote.FullName()))
	}
	target := workspaces[0]
	if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), fmt.Sprintf(
		"Tear down the %s workspace %s for %s? Everything synced into it is lost. [y/N]: ",
		target.Kind, target.ID, checkout.Remote.FullName()), isConfirmed); confirmError != nil {
		return confirmError
	}
	torn, downError := apiClient.DeleteWorkspace(ctx, target.ID)
	if downError != nil {
		return workspaceAPIError(downError)
	}
	if written, renderError := renderStructured(cmd, torn); written || renderError != nil {
		return renderError
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Workspace %s (%s) is %s.\n", torn.ID, torn.Kind, torn.Status)
	return nil
}

func init() {
	rootCmd.AddCommand(newWorkspaceCommand())
}
