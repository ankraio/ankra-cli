package cmd

import (
	"context"
	"fmt"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		generate, _ := cmd.Flags().GetBool("generate")

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		if generate {
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
			fmt.Println("The previous token is now invalid; redeploy the agent with the command above.")
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
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.UpgradeClusterAgent(ctx, cluster.ID)
		if err != nil {
			return fmt.Errorf("upgrading agent: %w", err)
		}

		if result.Success {
			fmt.Printf("Agent upgrade initiated for cluster '%s'!\n", cluster.Name)
			fmt.Println("The agent will automatically restart with the new version.")
			fmt.Println("\nRun 'ankra cluster agent status' to check the upgrade progress.")
			return nil
		}
		return fmt.Errorf("agent upgrade request did not report success")
	},
}

func init() {
	clusterAgentTokenCmd.Flags().Bool("generate", false, "Generate a new agent token")

	registerStructuredOutputFlags(clusterAgentStatusCmd, clusterAgentTokenCmd)

	clusterAgentCmd.AddCommand(clusterAgentStatusCmd)
	clusterAgentCmd.AddCommand(clusterAgentTokenCmd)
	clusterAgentCmd.AddCommand(clusterAgentUpgradeCmd)

	clusterCmd.AddCommand(clusterAgentCmd)
}
