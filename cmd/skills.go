package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"ankra/internal/skills"

	"github.com/spf13/cobra"
)

// skillsCmd installs the curated Ankra Agent Skills (SKILL.md files) into a
// Cursor/Claude skills directory. The skills are embedded in the binary, so
// installation works offline and is versioned with the CLI release. This is
// distinct from `ankra openclaw skill`, which generates a per-cluster skill.
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Install Ankra Agent Skills into your editor",
	Long: `Install the curated Ankra Agent Skills into a Cursor/Claude skills
directory. The skills teach your agent to follow Ankra's recommended practices
for the CLI, ImportCluster YAML, stacks/addons, GitOps, CI/CD, SOPS secrets,
Helm registries, observability, alerts, Terraform, and cloud clusters.

Skills are embedded in the CLI, so installation works offline.

  ankra skills list
  ankra skills install
  ankra skills install --project .
  ankra skills install ankra-cli ankra-gitops`,
}

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the available Ankra skills",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		fsys, err := skillsSourceFS(cmd)
		if err != nil {
			return err
		}
		target, _, err := skillsTargetDir(cmd)
		if err != nil {
			return err
		}
		list, err := skills.List(fsys)
		if err != nil {
			return err
		}
		for _, s := range list {
			marker := ""
			if dirExists(filepath.Join(target, s.Name)) {
				marker = " [installed]"
			}
			fmt.Printf("%s%s\n  %s\n", s.Name, marker, s.Description)
		}
		fmt.Printf("\n%d skills available. Install with: ankra skills install\n", len(list))
		return nil
	},
}

var skillsInstallCmd = &cobra.Command{
	Use:   "install [skill ...]",
	Short: "Install Ankra skills into your skills directory",
	Long: `Install all skills (default) or only the named ones.

By default skills install into ~/.cursor/skills/ (personal, available in every
project). Use --project <DIR> to install into <DIR>/.cursor/skills/ instead
(--project . for the current directory).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fsys, err := skillsSourceFS(cmd)
		if err != nil {
			return err
		}
		target, scope, err := skillsTargetDir(cmd)
		if err != nil {
			return err
		}
		force, _ := cmd.Flags().GetBool("force")

		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("could not create %s: %w", target, err)
		}

		installed, skipped, err := skills.Install(fsys, target, args, force)
		if err != nil {
			return err
		}

		for _, name := range installed {
			fmt.Printf("installed %s\n", name)
		}
		for _, name := range skipped {
			fmt.Printf("skipped   %s (already exists; use --force to overwrite)\n", name)
		}
		fmt.Printf("\nInstalled %d skill(s) to %s (%s, skipped %d).\n",
			len(installed), target, scope, len(skipped))
		fmt.Println("Restart Cursor (or your editor) to load the skills.")
		return nil
	},
}

var skillsUninstallCmd = &cobra.Command{
	Use:   "uninstall [skill ...]",
	Short: "Remove installed Ankra skills",
	Long:  "Remove the named skills, or all Ankra skills when none are given.",
	RunE: func(cmd *cobra.Command, args []string) error {
		target, scope, err := skillsTargetDir(cmd)
		if err != nil {
			return err
		}
		names := args
		if len(names) == 0 {
			fsys, err := skillsSourceFS(cmd)
			if err != nil {
				return err
			}
			names, err = skills.Names(fsys)
			if err != nil {
				return err
			}
		}
		removed := 0
		for _, name := range names {
			dest := filepath.Join(target, name)
			if dirExists(dest) {
				if err := os.RemoveAll(dest); err != nil {
					return fmt.Errorf("could not remove %s: %w", dest, err)
				}
				fmt.Printf("removed %s\n", name)
				removed++
			}
		}
		fmt.Printf("\nRemoved %d skill(s) from %s (%s).\n", removed, target, scope)
		return nil
	},
}

// skillsSourceFS returns the skills filesystem: a local directory when
// --source is given (offline, for testing/power users), otherwise the copy
// embedded in the binary.
func skillsSourceFS(cmd *cobra.Command) (fs.FS, error) {
	source, _ := cmd.Flags().GetString("source")
	if source != "" {
		return skills.SourceFS(source)
	}
	return skills.EmbeddedFS()
}

// skillsTargetDir resolves the install directory and a human-readable scope.
// --project [DIR] installs into <DIR>/.cursor/skills (default DIR "."),
// otherwise skills install into ~/.cursor/skills.
func skillsTargetDir(cmd *cobra.Command) (string, string, error) {
	projectFlag := cmd.Flags().Lookup("project")
	if projectFlag != nil && projectFlag.Changed {
		dir := projectFlag.Value.String()
		if dir == "" {
			dir = "."
		}
		return filepath.Join(dir, ".cursor", "skills"), "project", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("could not determine home directory: %w", err)
	}
	return filepath.Join(home, ".cursor", "skills"), "personal", nil
}

func init() {
	for _, c := range []*cobra.Command{skillsListCmd, skillsInstallCmd, skillsUninstallCmd} {
		c.Flags().Bool("personal", false, "install into ~/.cursor/skills (default)")
		c.Flags().String("project", "", "install into <DIR>/.cursor/skills (use \".\" for the current directory)")
		c.Flags().String("source", "", "read skills from a local directory instead of the embedded copy")
	}
	skillsInstallCmd.Flags().Bool("force", false, "overwrite existing skills without prompting")

	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsInstallCmd)
	skillsCmd.AddCommand(skillsUninstallCmd)

	setRequiresAuth(skillsCmd, false)
	rootCmd.AddCommand(skillsCmd)
}
