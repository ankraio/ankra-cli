package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var clusterAgentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage cluster agents",
	Long:  "Commands to view agent status, get tokens, and upgrade agents.",
}

var clusterAgentStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get agent status for the selected cluster",
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		agent, err := apiClient.GetClusterAgent(cluster.ID)
		if err != nil {
			fmt.Printf("Error getting agent status: %v\n", err)
			return
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

		if agent.Upgrading {
			fmt.Printf("  Status:     %s\n", text.FgYellow.Sprint("upgrading"))
		} else if agent.CheckedInAt != nil {
			fmt.Printf("  Status:     %s\n", text.FgGreen.Sprint("connected"))
		} else {
			fmt.Printf("  Status:     %s\n", text.FgRed.Sprint("not connected"))
		}

		if agent.UpgradeAvailable {
			fmt.Printf("\n  Upgrade Available: %s\n", text.FgYellow.Sprint("Yes"))
			if agent.LatestAgentVersion != nil {
				fmt.Printf("  Latest Version:    %s\n", *agent.LatestAgentVersion)
			}
			fmt.Println("\n  Run 'ankra cluster agent upgrade' to upgrade the agent.")
		}
	},
}

var clusterAgentTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Get or generate agent token for the selected cluster",
	Run: func(cmd *cobra.Command, args []string) {
		generate, _ := cmd.Flags().GetBool("generate")

		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		if generate {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			token, err := apiClient.GenerateAgentToken(ctx, cluster.ID)
			if err != nil {
				fmt.Printf("Error generating agent token: %v\n", err)
				return
			}

			fmt.Println("New agent token generated!")
			fmt.Println()
			fmt.Printf("Token (save this, it won't be shown again):\n")
			fmt.Printf("  %s\n", token.Token)
			fmt.Println()
			fmt.Printf("Expires: %s\n", formatTimeAgo(token.ExpiresAt))
		} else {
			token, err := apiClient.GetAgentToken(cluster.ID)
			if err != nil {
				fmt.Printf("Error getting agent token: %v\n", err)
				fmt.Println("\nTo generate a new token, run: ankra agent token --generate")
				return
			}

			fmt.Printf("Agent Token for cluster '%s':\n\n", cluster.Name)
			fmt.Printf("  Token:   %s\n", token.Token)
			fmt.Printf("  Expires: %s\n", formatTimeAgo(token.ExpiresAt))
		}
	},
}

var clusterAgentUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the agent on the selected cluster",
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.UpgradeClusterAgent(ctx, cluster.ID)
		if err != nil {
			fmt.Printf("Error upgrading agent: %v\n", err)
			return
		}

		if result.Success {
			fmt.Printf("Agent upgrade initiated for cluster '%s'!\n", cluster.Name)
			fmt.Println("The agent will automatically restart with the new version.")
			fmt.Println("\nRun 'ankra cluster agent status' to check the upgrade progress.")
		}
	},
}

func init() {
	clusterAgentTokenCmd.Flags().Bool("generate", false, "Generate a new agent token")

	clusterAgentCmd.AddCommand(clusterAgentStatusCmd)
	clusterAgentCmd.AddCommand(clusterAgentTokenCmd)
	clusterAgentCmd.AddCommand(clusterAgentUpgradeCmd)

	clusterCmd.AddCommand(clusterAgentCmd)
}
