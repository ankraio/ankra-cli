package cmd

// `ankra slack`: connect the organisation to Slack from a terminal. The install
// itself is Slack's OAuth page, which a person has to approve in a browser, so
// the command prints where to go and waits for the platform to see it finish.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const slackDefaultConnectTimeout = 15 * time.Minute

// slackPollInterval paces the wait. A var so the tests do not wait.
var slackPollInterval = 3 * time.Second

var slackCmd = &cobra.Command{
	Use:   "slack",
	Short: "Connect the organisation to Slack",
}

var slackStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the Slack workspaces connected to the organisation",
	Args:  cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		status, statusError := apiClient.GetSlackStatus(command.Context())
		if statusError != nil {
			return statusError
		}
		if format != outputDefault {
			_, renderError := renderStructured(command, status)
			return renderError
		}
		out := command.OutOrStdout()
		if len(status.Workspaces) == 0 {
			_, _ = fmt.Fprintln(out, "No Slack workspace is connected. Connect one with 'ankra slack connect'.")
			return nil
		}
		for _, workspace := range status.Workspaces {
			_, _ = fmt.Fprintf(out, "%s, connected %s\n", slackWorkspaceName(workspace), workspace.InstalledAt.Format(time.RFC3339))
		}
		return nil
	},
}

var slackConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Print the Slack install URL and wait for the install to finish",
	Long: `Print the address that adds Ankra to a Slack workspace, and wait until the
platform sees the install finish. Open it in a browser and approve it there.
Slack does the rest, so this works from a machine with no browser too.

Starting an install needs an organisation administrator and a person's token, not
a service account's. To send alerts to the workspace afterwards, use
'ankra alerts routes create'.

Exit codes: 0 when connected, 5 when --timeout ran out first (the address may
still work, so run 'ankra slack status' to check), 7 without the role.`,
	Example: `  ankra slack connect
  ankra slack connect --no-wait
  ankra slack connect --timeout 30m -o json`,
	Args: cobra.NoArgs,
	RunE: runSlackConnect,
}

func init() {
	slackConnectCmd.Flags().Bool("no-wait", false, "Print the address and exit without waiting")
	slackConnectCmd.Flags().Duration("timeout", slackDefaultConnectTimeout, "How long to wait for the install to finish")
	registerStructuredOutputFlags(slackConnectCmd)
	registerStructuredOutputFlags(slackStatusCmd)
	slackCmd.AddCommand(slackConnectCmd, slackStatusCmd)
	rootCmd.AddCommand(slackCmd)
}

// slackConnectResult is the -o json document of `ankra slack connect`.
type slackConnectResult struct {
	InstallURL string                 `json:"install_url" yaml:"install_url"`
	Connected  bool                   `json:"connected" yaml:"connected"`
	Workspace  *client.SlackWorkspace `json:"workspace,omitempty" yaml:"workspace,omitempty"`
}

func runSlackConnect(command *cobra.Command, _ []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	timeout, _ := command.Flags().GetDuration("timeout")
	if timeout <= 0 {
		return withExitCode(exitUsage, errors.New("--timeout must be positive"))
	}
	skipWait, _ := command.Flags().GetBool("no-wait")

	// What exists before the install, so a workspace that was already connected
	// is not mistaken for this one finishing.
	before, statusError := apiClient.GetSlackStatus(command.Context())
	if statusError != nil {
		return slackRouteError(statusError)
	}
	install, installError := apiClient.GetSlackInstallURL(command.Context())
	if installError != nil {
		return slackRouteError(installError)
	}

	// The address goes to stderr with -o json, so stdout stays one document,
	// and a person waiting still sees it.
	progress := command.OutOrStdout()
	if format != outputDefault {
		progress = command.ErrOrStderr()
	}
	_, _ = fmt.Fprintf(progress, "Open this in a browser and approve it:\n  %s\n", install.InstallURL)

	result := slackConnectResult{InstallURL: install.InstallURL}
	if !skipWait {
		_, _ = fmt.Fprintln(progress, "Waiting for the install to finish...")
		workspace, waitError := waitForSlackInstall(command.Context(), before, timeout)
		if waitError != nil {
			return waitError
		}
		result.Connected = true
		result.Workspace = workspace
	}
	if format != outputDefault {
		_, renderError := renderStructured(command, result)
		return renderError
	}
	if result.Workspace != nil {
		_, _ = fmt.Fprintf(progress, "Connected %s. Send alerts to it with 'ankra alerts routes create'.\n", slackWorkspaceName(*result.Workspace))
	}
	return nil
}

// waitForSlackInstall polls until a workspace appears that was not connected
// before, or one already connected is installed again.
func waitForSlackInstall(requestContext context.Context, before *client.SlackStatus, timeout time.Duration) (*client.SlackWorkspace, error) {
	waitContext, cancel := context.WithTimeout(requestContext, timeout)
	defer cancel()
	seen := map[string]time.Time{}
	for _, workspace := range before.Workspaces {
		seen[workspace.TeamID] = workspace.InstalledAt
	}
	for {
		status, statusError := apiClient.GetSlackStatus(waitContext)
		if statusError == nil {
			for index := range status.Workspaces {
				workspace := status.Workspaces[index]
				installedAt, wasSeen := seen[workspace.TeamID]
				if !wasSeen || workspace.InstalledAt.After(installedAt) {
					return &workspace, nil
				}
			}
		} else if waitContext.Err() == nil && !slackPollErrorIsRetryable(statusError) {
			return nil, statusError
		}
		select {
		case <-waitContext.Done():
			return nil, withExitCode(exitWaitTimeout, fmt.Errorf(
				"the Slack install did not finish within %s; the address may still work, so check with 'ankra slack status'", timeout))
		case <-time.After(slackPollInterval):
		}
	}
}

// slackPollErrorIsRetryable keeps a passing network or platform failure from
// ending a wait a person is in the middle of. A refusal ends it.
func slackPollErrorIsRetryable(statusError error) bool {
	if errors.Is(statusError, client.ErrUnauthorized) {
		return false
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(statusError, &unexpected) {
		return unexpected.StatusCode >= 500
	}
	var denied *client.PermissionDeniedError
	return !errors.As(statusError, &denied)
}

// slackRouteError explains the answer of a platform without these routes, or
// one where Slack is not set up.
func slackRouteError(routeError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(routeError, &unexpected) && unexpected.StatusCode == 404 {
		return fmt.Errorf("this platform cannot start a Slack install from the CLI yet; connect Slack from the portal's integrations page: %w", routeError)
	}
	return routeError
}

func slackWorkspaceName(workspace client.SlackWorkspace) string {
	if workspace.TeamName != nil && strings.TrimSpace(*workspace.TeamName) != "" {
		return *workspace.TeamName
	}
	return workspace.TeamID
}
