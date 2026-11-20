package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var clusterAgentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage cluster agents",
	Long:  "Commands to view agent status, get tokens, and upgrade agents.",
}

var clusterAgentStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get agent status for the selected cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		agent, err := apiClient.GetClusterAgent(cluster.ID)
		if err != nil {
			return fmt.Errorf("getting agent status: %w", err)
		}

		if rendered, err := renderStructured(cmd, agent); rendered || err != nil {
			return err
		}

		fmt.Printf("Agent Status for cluster '%s':\n\n", cluster.Name)

		version := "unknown"
		if agent.AgentVersion != nil {
			version = *agent.AgentVersion
		}
		fmt.Printf("  Version:    %s\n", version)

		if agent.CheckedInAt != nil {
			fmt.Printf("  Last Check-in: %s\n", formatTimeAgo(*agent.CheckedInAt))
		} else {
			fmt.Printf("  Last Check-in: %s\n", text.FgRed.Sprint("never"))
		}

		fmt.Printf("  Created:    %s\n", formatTimeAgo(agent.CreatedAt))

		switch {
		case agent.Upgrading:
			fmt.Printf("  Status:     %s\n", text.FgYellow.Sprint("upgrading"))
		case agentIsConnected(agent):
			fmt.Printf("  Status:     %s\n", text.FgGreen.Sprint("connected"))
		case agent.CheckedInAt != nil:
			fmt.Printf("  Status:     %s\n", text.FgRed.Sprint("not connected (stale check-in)"))
		default:
			fmt.Printf("  Status:     %s\n", text.FgRed.Sprint("not connected"))
		}

		if agent.DisableAutoUpgrade {
			fmt.Printf("  Auto-upgrade: %s\n", text.FgYellow.Sprint("disabled (the fleet rollout skips this agent)"))
		} else {
			fmt.Printf("  Auto-upgrade: %s\n", text.FgGreen.Sprint("enabled"))
		}

		if agent.SecurityUpgradeRequired != nil && *agent.SecurityUpgradeRequired {
			fmt.Printf("\n  Security upgrade: %s\n", text.FgYellow.Sprint("required"))
			fmt.Println("  This agent must be upgraded for a security update; it stops connecting once the platform retires its current credentials.")
			switch {
			case agent.Upgrading:
				fmt.Println("  An upgrade is running now.")
			case !agent.UpgradeAvailable:
				// The upgrade block below prints the command when an upgrade is
				// available; print it here only when that block does not.
				fmt.Println("  Run 'ankra cluster agent upgrade' to upgrade it now.")
			}
		}

		if agent.UpgradeAvailable {
			fmt.Printf("\n  Upgrade Available: %s\n", text.FgYellow.Sprint("Yes"))
			if agent.LatestAgentVersion != nil {
				fmt.Printf("  Latest Version:    %s\n", *agent.LatestAgentVersion)
			}
			fmt.Println("\n  Run 'ankra cluster agent upgrade' to upgrade the agent.")
		}
		return nil
	},
}

// agentCheckinRecencyFallback bounds how old a check-in may be and still
// count as connected when the platform does not report is_online. The
// platform flips clusters offline after 30s without a check-in; two minutes
// leaves headroom for clock skew without ever showing "connected" for an
// agent that is being rejected.
const agentCheckinRecencyFallback = 2 * time.Minute

// agentIsConnected prefers the platform's is_online verdict and falls back
// to a check-in recency test for older platform versions.
func agentIsConnected(agent *client.AgentInfo) bool {
	if agent.IsOnline != nil {
		return *agent.IsOnline
	}
	if agent.CheckedInAt == nil {
		return false
	}
	checkedInAt, parseError := time.Parse(time.RFC3339, *agent.CheckedInAt)
	if parseError != nil {
		return false
	}
	return time.Since(checkedInAt) <= agentCheckinRecencyFallback
}

var clusterAgentTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Get or generate agent token for the selected cluster",
	Long: `Print the agent token and install command for the selected cluster, or
generate a new one with --generate.

A new token replaces the token the running agent holds. When the cluster's
agent is connected, or checked in within the last 15 minutes, --generate says
so and asks first; with no terminal to ask on it needs --yes. When Ankra keeps
the previous token valid for a while, the output says until when; otherwise
the running agent is refused from its next check-in until it is redeployed
with the new install command.`,
	Example: `  ankra cluster agent token
  ankra cluster agent token --generate
  ankra cluster agent token --generate --yes -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		generate, _ := cmd.Flags().GetBool("generate")

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		if generate {
			if err := confirmAgentTokenRotation(cmd, cluster.ID, cluster.Name); err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			token, err := apiClient.GenerateAgentToken(ctx, cluster.ID)
			if err != nil {
				return fmt.Errorf("generating agent token: %w", err)
			}

			if rendered, err := renderStructured(cmd, token); rendered || err != nil {
				return err
			}

			fmt.Println("New agent token generated!")
			fmt.Println()
			printAgentToken(token)
			fmt.Println()
			fmt.Println(previousAgentTokenSentence(token))
			return nil
		}

		token, err := apiClient.GetAgentToken(cluster.ID)
		if err != nil {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "To generate a new token, run: ankra cluster agent token --generate")
			return fmt.Errorf("getting agent token: %w", err)
		}

		if rendered, err := renderStructured(cmd, token); rendered || err != nil {
			return err
		}

		fmt.Printf("Agent Token for cluster '%s':\n\n", cluster.Name)
		printAgentToken(token)
		return nil
	},
}

// agentCutOffCheckinWindow is how recently an agent that the platform no
// longer reports as connected must have checked in to still count as
// running when a delete or a token rotation would cut it off. An agent
// between check-ins, or one restarting, is not connected for a moment but is
// still installed and will keep calling with the credential it holds
// (support ticket 1331: the record was deleted 16 minutes after the agent's
// last check-in, and the agent was refused for 13.7 days).
const agentCutOffCheckinWindow = 15 * time.Minute

// runningAgentPresence describes an agent that is, or very recently was,
// connected, as the end of a sentence that starts "The Ankra agent in
// cluster X": "is connected (last check-in 20 seconds ago)" or "checked in 5
// minutes ago". It is empty when nothing says an agent is running.
func runningAgentPresence(agent *client.AgentInfo) string {
	if agent == nil {
		return ""
	}
	if agentIsConnected(agent) {
		if agent.CheckedInAt != nil {
			return fmt.Sprintf("is connected (last check-in %s)", formatTimeAgo(*agent.CheckedInAt))
		}
		return "is connected"
	}
	if agent.CheckedInAt == nil {
		return ""
	}
	checkedInAt, parseError := time.Parse(time.RFC3339, *agent.CheckedInAt)
	if parseError != nil || time.Since(checkedInAt) > agentCutOffCheckinWindow {
		return ""
	}
	return "checked in " + formatTimeAgo(*agent.CheckedInAt)
}

// readRunningAgentPresence reads the cluster's agent and describes it with
// runningAgentPresence. A read that fails (a cluster with no agent record,
// a platform without the route, a transient error) says nothing: the
// warning is advice, and the command behaves as it did before it existed.
func readRunningAgentPresence(clusterID string) string {
	agent, readError := apiClient.GetClusterAgent(clusterID)
	if readError != nil {
		return ""
	}
	return runningAgentPresence(agent)
}

// confirmAgentTokenRotation warns before a new agent token is generated for
// a cluster whose agent is running, and asks. The token the running agent
// holds is replaced, so unless the platform keeps it valid for a while the
// agent is refused from its next check-in. Everything goes to stderr so
// -o json|yaml stays parseable. --yes skips the question; a run with no
// terminal and no --yes is refused, with nothing generated.
func confirmAgentTokenRotation(cmd *cobra.Command, clusterID, clusterName string) error {
	presence := readRunningAgentPresence(clusterID)
	if presence == "" {
		return nil
	}
	errOut := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(errOut, "The Ankra agent in cluster '%s' %s.\n", clusterName, presence)
	_, _ = fmt.Fprintln(errOut, "A new token replaces the token that agent uses. Redeploy the agent with the new "+
		"install command straight after; until then Ankra may refuse the running agent and the cluster shows as offline.")
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	if !promptIsInteractive(cmd.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"generating a new agent token for cluster '%s' while its agent is running needs confirmation, "+
				"but there is no terminal to ask on; nothing was generated. Re-run with --yes to confirm", clusterName))
	}
	if promptError := confirmPrompt(cmd.InOrStdin(), errOut, "Generate a new agent token? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// previousAgentTokenSentence says what happens to the agent still running
// with the previous token: on a platform with a rotation grace window it
// keeps working until the agent with the new token connects or the window
// ends; otherwise it was cut off the moment the new token was generated.
func previousAgentTokenSentence(token *client.AgentToken) string {
	if token.PreviousTokenValidUntil == nil || strings.TrimSpace(*token.PreviousTokenValidUntil) == "" {
		return "The previous token is now invalid; redeploy the agent with the command above."
	}
	until := strings.TrimSpace(*token.PreviousTokenValidUntil)
	if _, parseError := time.Parse(time.RFC3339, until); parseError == nil {
		until = fmt.Sprintf("%s (%s)", until, formatTimeAgo(until))
	}
	return fmt.Sprintf("An agent still running with the previous token keeps working until the agent with this token "+
		"connects, or until %s at the latest; redeploy the agent with the command above before then.", until)
}

func printAgentToken(token *client.AgentToken) {
	if token.Token != "" {
		fmt.Printf("  Token: %s\n", token.Token)
	} else {
		fmt.Printf("  Token: %s\n", text.FgRed.Sprint("unavailable (unexpected API response)"))
	}
	if token.Command != "" {
		fmt.Println()
		fmt.Println("  Install or update the agent with:")
		fmt.Printf("    %s\n", token.Command)
	}
}

var clusterAgentUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the agent on the selected cluster",
	Long: `Upgrade the Ankra agent on the selected cluster now, instead of waiting for
the fleet rollout.

Without --version the platform installs the newer of the release the agent
runs and the latest stable release, so this never moves an agent backwards:
an agent already on a newer release (a release candidate) is re-rendered where
it is.

--version installs that exact published release. A version older than the one
the agent runs is refused unless --allow-downgrade is also given, so a
downgrade only ever happens when both flags say so. A version that is neither
published nor the running one is refused.`,
	Example: `  ankra cluster agent upgrade
  ankra cluster agent upgrade --version 2.1.1250
  ankra cluster agent upgrade --version 2.1.1247 --allow-downgrade`,
	RunE: func(cmd *cobra.Command, args []string) error {
		version, _ := cmd.Flags().GetString("version")
		allowDowngrade, _ := cmd.Flags().GetBool("allow-downgrade")
		upgradeRequest := client.AgentUpgradeRequest{Version: strings.TrimSpace(version), AllowDowngrade: allowDowngrade}
		if upgradeRequest.AllowDowngrade && upgradeRequest.Version == "" {
			return withExitCode(exitUsage, errors.New(
				"--allow-downgrade needs --version: name the release to downgrade to; without a version the upgrade never moves the agent backwards"))
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.UpgradeClusterAgent(ctx, cluster.ID, upgradeRequest)
		if err != nil {
			return agentUpgradeError(err)
		}

		if result.Success {
			out := cmd.OutOrStdout()
			if upgradeRequest.Version != "" {
				_, _ = fmt.Fprintf(out, "Agent update to %s initiated for cluster '%s'!\n", upgradeRequest.Version, cluster.Name)
			} else {
				_, _ = fmt.Fprintf(out, "Agent upgrade initiated for cluster '%s'!\n", cluster.Name)
			}
			_, _ = fmt.Fprintln(out, "The agent will automatically restart with the new version.")
			_, _ = fmt.Fprintln(out, "\nRun 'ankra cluster agent status' to check the upgrade progress.")
			return nil
		}
		return fmt.Errorf("agent upgrade request did not report success")
	},
}

// agentUpgradeError words a failed hand upgrade. The platform's refusal of a
// named version (an older release without --allow-downgrade, or a release it
// cannot install) is relayed as the platform's own sentence and exits as a
// usage error: the way out is a different invocation, not a retry.
func agentUpgradeError(upgradeError error) error {
	var refused *client.AgentUpgradeRefusedError
	if errors.As(upgradeError, &refused) {
		message := "upgrading agent: " + refused.Detail
		if refused.IsDowngradeRefusal() {
			message += "\nAdd --allow-downgrade to install the older release on purpose."
		}
		return withExitCode(exitUsage, errors.New(message))
	}
	return fmt.Errorf("upgrading agent: %w", upgradeError)
}

func init() {
	clusterAgentTokenCmd.Flags().Bool("generate", false,
		"Generate a new agent token (asks first when the cluster's agent is running)")
	clusterAgentTokenCmd.Flags().BoolP("yes", "y", false,
		"With --generate, skip the confirmation asked when the cluster's agent is running "+
			"(required then when stdin is not a terminal)")
	clusterAgentUpgradeCmd.Flags().String("version", "",
		"Exact agent release to install (default: the newer of the running release and the latest stable one)")
	clusterAgentUpgradeCmd.Flags().Bool("allow-downgrade", false,
		"Allow --version to be older than the release the agent runs (needs --version)")

	registerStructuredOutputFlags(clusterAgentStatusCmd, clusterAgentTokenCmd)

	clusterAgentCmd.AddCommand(clusterAgentStatusCmd)
	clusterAgentCmd.AddCommand(clusterAgentTokenCmd)
	clusterAgentCmd.AddCommand(clusterAgentUpgradeCmd)

	clusterCmd.AddCommand(clusterAgentCmd)
}
