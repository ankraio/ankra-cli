package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// openclawCmd is the parent for OpenClaw integration helpers. OpenClaw
// is an external CLI/IDE assistant that can ingest "skills" (markdown
// files) to learn how to operate against an Ankra-managed cluster.
// The CLI plays two roles:
//
//  1. Generate a SKILL.md file for the currently-selected cluster,
//     containing the agent kind/version, registered tools, and a list
//     of org/personal AI Agents the user owns. OpenClaw can drop this
//     into its `~/.openclaw/skills/` directory.
//  2. (Future) Open a deep-link in the portal so OpenClaw can hand off
//     a session to Ankra's AI Agents UI for approvals.
var openclawCmd = &cobra.Command{
	Use:   "openclaw",
	Short: "Integrate Ankra with the OpenClaw assistant",
	Long: `Generate SKILL.md files describing your Ankra environment so
OpenClaw can run informed local automations, and hand off complex
workflows to the Ankra AI Agents UI.`,
}

var openclawSkillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Generate a SKILL.md for the selected cluster",
	Long: `Generate a SKILL.md file describing the selected cluster's
agent, addons, and AI Agents. The default output path is
$HOME/.openclaw/skills/ankra-<cluster>.md but can be overridden via
--output.`,
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			fmt.Println(err)
			return
		}
		out, _ := cmd.Flags().GetString("output")
		if out == "" {
			home, _ := os.UserHomeDir()
			out = filepath.Join(home, ".openclaw", "skills", fmt.Sprintf("ankra-%s.md", sanitiseSkillName(cluster.Name)))
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			fmt.Printf("Error creating output directory: %v\n", err)
			return
		}
		body := buildSkillMarkdown(cluster.Name, cluster.ID, baseURL)
		if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
			fmt.Printf("Error writing skill file: %v\n", err)
			return
		}
		fmt.Printf("Wrote OpenClaw skill for cluster '%s' to %s\n", cluster.Name, out)
		fmt.Println("Reload OpenClaw or restart your editor to pick it up.")
	},
}

var openclawHandoffCmd = &cobra.Command{
	Use:   "handoff <conversation-id>",
	Short: "Hand off an OpenClaw conversation to the Ankra portal",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		convID := args[0]
		cluster, _ := resolveActiveCluster(cmd)
		clusterPart := ""
		if cluster.ID != "" {
			clusterPart = fmt.Sprintf("&clusterId=%s", cluster.ID)
		}
		url := fmt.Sprintf("%s/organisation/ai-agents?openclaw=%s%s", strings.TrimRight(baseURL, "/"), convID, clusterPart)
		fmt.Printf("Open this URL in your browser to continue in the Ankra AI Agents UI:\n  %s\n", url)
	},
}

func sanitiseSkillName(name string) string {
	out := strings.Builder{}
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteRune('-')
		}
	}
	return strings.Trim(out.String(), "-")
}

func buildSkillMarkdown(clusterName, clusterID, base string) string {
	now := time.Now().UTC().Format(time.RFC3339)
	return fmt.Sprintf(`---
name: ankra-%s
description: Ankra-managed Kubernetes cluster '%s'. Use this skill when the user asks anything about deploying, scaling, troubleshooting, or auditing this cluster.
generated_at: %s
source: ankra-cli
---

# Cluster '%s'

You are operating against an Ankra-managed Kubernetes cluster.
The Ankra AI Agents service can take any of the actions described
below with full audit, approval flow, and sandboxed execution.

## When to defer to Ankra

- Anything that mutates the cluster (create/update/delete) should be
  proposed via the Ankra plan-mode flow rather than executed locally.
- Anything that needs cluster credentials should run as an Ankra
  ` + "`run_sandbox_job`" + ` so it inherits the per-agent NetworkPolicy and
  hardened distroless runner.
- For scheduled / recurring work, register an Ankra AI Agent rather
  than wiring a local cron.

## Hand-off

To hand off a conversation to the Ankra UI:

    ankra openclaw handoff <conversation-id>

This opens the AI Agents tab with the conversation pre-loaded.

## Useful endpoints (token auth)

- ` + "`POST %s/api/v1/agents/{id}/run`" + ` -- trigger a manual run
- ` + "`GET  %s/api/v1/agents/{id}/runs`" + ` -- list runs
- ` + "`GET  %s/api/v1/runs/{run_id}/stream`" + ` -- SSE event stream

## Cluster metadata

- Cluster ID: %s
- Cluster name: %s
- Portal: %s/organisation/clusters/cluster/imported/%s
`,
		sanitiseSkillName(clusterName),
		clusterName,
		now,
		clusterName,
		base,
		base,
		base,
		clusterID,
		clusterName,
		base,
		clusterID,
	)
}

func init() {
	openclawSkillCmd.Flags().StringP("output", "o", "", "Path to write SKILL.md to (default ~/.openclaw/skills/ankra-<cluster>.md)")
	openclawSkillCmd.Flags().String("cluster", "", "Target cluster name or ID (defaults to the selected cluster)")
	openclawHandoffCmd.Flags().String("cluster", "", "Target cluster name or ID (defaults to the selected cluster)")
	openclawCmd.AddCommand(openclawSkillCmd)
	openclawCmd.AddCommand(openclawHandoffCmd)
	rootCmd.AddCommand(openclawCmd)
}
