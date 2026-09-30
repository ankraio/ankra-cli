package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/hiddenunicode"
)

// openclawCmd is the parent for OpenClaw integration helpers. OpenClaw
// is an external assistant that loads "skills": a directory holding a
// SKILL.md, discovered under ~/.openclaw/skills (among other roots). The CLI
// generates one such skill for the currently-selected cluster.
var openclawCmd = &cobra.Command{
	Use:   "openclaw",
	Short: "Integrate Ankra with the OpenClaw assistant",
	Long: `Generate an OpenClaw skill (a SKILL.md) describing the selected
Ankra cluster so OpenClaw can run informed local automations.`,
}

var openclawSkillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Generate an OpenClaw skill for the selected cluster",
	Long: `Generate a SKILL.md describing the selected cluster's agent,
addons, and AI Agents.

OpenClaw only loads a skill from a SKILL.md inside its own directory, so the
default output is $HOME/.openclaw/skills/ankra-<cluster>/SKILL.md (the
directory is created). --output writes to any other path instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		out, _ := cmd.Flags().GetString("output")
		if out == "" {
			home, homeErr := os.UserHomeDir()
			if homeErr != nil {
				return fmt.Errorf("determine the home directory (pass --output instead): %w", homeErr)
			}
			out, err = defaultOpenclawSkillPath(home, cluster.Name)
			if err != nil {
				return err
			}
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return fmt.Errorf("creating output directory: %w", err)
		}
		// The cluster name is server-authored and lands in a file an AI
		// assistant loads as instructions, so invisible runes are removed and
		// the value is flattened to one line: a newline would let it forge
		// frontmatter fields around itself. sanitiseSkillName only ever
		// covered the filename (ankra-4r75g.9).
		clusterName, nameHidden := hiddenunicode.Line(cluster.Name)
		body := buildSkillMarkdown(clusterName, cluster.ID, baseURL)
		if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
			return fmt.Errorf("writing skill file: %w", err)
		}
		if nameHidden > 0 {
			_, _ = fmt.Fprintf(os.Stderr, "%s\n", hiddenunicode.Notice(nameHidden))
		}
		fmt.Printf("Wrote OpenClaw skill for cluster '%s' to %s\n", clusterName, out)
		fmt.Println("Start a new OpenClaw session (or restart the gateway) to pick it up.")
		return nil
	},
}

// openclawHandoffCmd is deprecated: it used to print
// /organisation/ai-agents?openclaw=<id>, a route that does not exist, with a
// parameter nothing in the portal reads, and the generated skill promised the
// link opened the conversation pre-loaded (ankra-0iixk). No conversation can
// be handed over, so it now prints the AI Agents page and says so.
var openclawHandoffCmd = &cobra.Command{
	Use:   "handoff [conversation-id]",
	Short: "Print the Ankra AI Agents page URL (deprecated)",
	Long: `Print the URL of the Ankra AI Agents page.

No OpenClaw conversation is transferred: the portal has no way to import one,
and the conversation-id argument is accepted only so existing scripts keep
running. This command will be removed in v0.22.0.`,
	Deprecated: "it only prints the AI Agents page URL (no conversation is transferred) and will be removed in v0.22.0",
	Args:       cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		url := strings.TrimRight(baseURL, "/") + "/organisation/ai/agents"
		fmt.Printf("Ankra AI Agents: %s\n", url)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "No conversation is transferred; start or continue the work in the Ankra UI.")
		return nil
	},
}

// defaultOpenclawSkillPath is where `ankra openclaw skill` writes when no
// --output is given: <home>/.openclaw/skills/ankra-<cluster>/SKILL.md.
// OpenClaw discovers skills only as SKILL.md files inside a directory, so a
// flat ankra-<cluster>.md there was never loaded (ankra-0iixk).
//
// The directory shares ~/.openclaw/skills with `ankra skills install
// --client openclaw`, so a cluster whose skill name equals a bundled Ankra
// skill (a cluster named "cli" or "terraform") is refused rather than
// overwriting that skill.
func defaultOpenclawSkillPath(home, clusterName string) (string, error) {
	directory := "ankra-" + sanitiseSkillName(clusterName)
	if sanitiseSkillName(clusterName) == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("cluster name %q has no characters usable in a skill directory name; pass --output", clusterName))
	}
	bundled, err := bundledAnkraSkillNames()
	if err != nil {
		return "", err
	}
	if bundled[directory] {
		return "", withExitCode(exitUsage, fmt.Errorf(
			"the default skill directory %s would overwrite the bundled Ankra skill of the same name; pass --output",
			filepath.Join(home, ".openclaw", "skills", directory)))
	}
	return filepath.Join(home, ".openclaw", "skills", directory, "SKILL.md"), nil
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

// buildSkillMarkdown renders the SKILL.md body. Every interpolated value is
// server-authored and the result is loaded by an AI assistant as
// instructions, so the values are stripped of invisible Unicode and
// flattened to one line here as well as at the call site: a future caller
// must not be able to reintroduce the hole (ankra-4r75g.9).
func buildSkillMarkdown(clusterName, clusterID, base string) string {
	clusterName, _ = hiddenunicode.Line(clusterName)
	clusterID, _ = hiddenunicode.Line(clusterID)
	base, _ = hiddenunicode.Line(base)
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
  `+"`run_sandbox_job`"+` so it inherits the per-agent NetworkPolicy and
  hardened distroless runner.
- For scheduled / recurring work, register an Ankra AI Agent rather
  than wiring a local cron.

To look at runs or approve proposed actions in the browser, open the
Ankra AI Agents page: %s/organisation/ai/agents

## Useful endpoints (token auth)

- `+"`GET  %s/api/v1/org/ai-agent-runs`"+` -- list runs
- `+"`GET  %s/api/v1/org/ai-agent-runs/{run_id}/transcript`"+` -- read a run
- `+"`POST %s/api/v1/org/ai-agent-runs/{run_id}/cancel`"+` -- cancel a run
- `+"`GET  %s/api/v1/org/runs/{run_id}/stream`"+` -- SSE event stream of a data run (backup, restore, clone)

## Cluster metadata

- Cluster ID: %s
- Cluster name: %s
- Portal: %s/organisation/clusters/cluster/imported/%s/overview
`,
		sanitiseSkillName(clusterName),
		clusterName,
		now,
		clusterName,
		base,
		base,
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
	openclawSkillCmd.Flags().StringP("output", "o", "", "Path to write SKILL.md to (default ~/.openclaw/skills/ankra-<cluster>/SKILL.md)")
	openclawSkillCmd.Flags().String("cluster", "", "Target cluster name or ID (defaults to the selected cluster)")
	// Kept, unused, so scripts passing --cluster to the deprecated handoff
	// do not start failing before its removal.
	openclawHandoffCmd.Flags().String("cluster", "", "Ignored; kept for compatibility")
	openclawCmd.AddCommand(openclawSkillCmd)
	openclawCmd.AddCommand(openclawHandoffCmd)
	rootCmd.AddCommand(openclawCmd)
}
