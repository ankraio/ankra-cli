package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"ankra/internal/skills"

	"github.com/spf13/cobra"
)

// skillsCmd installs the curated Ankra Agent Skills (SKILL.md files) into a
// Cursor/Claude Code skills directory. The skills are embedded in the binary,
// so installation works offline and is versioned with the CLI release. This is
// distinct from `ankra openclaw skill`, which generates a per-cluster skill.
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Install Ankra Agent Skills into your editor",
	Long: `Install the curated Ankra Agent Skills into a Cursor/Claude Code skills
directory. The skills teach your agent to follow Ankra's recommended practices
for the CLI, ImportCluster YAML, stacks/addons, GitOps, CI/CD, SOPS secrets,
Helm registries, observability, alerts, Terraform, and cloud clusters.

Skills are embedded in the CLI, so installation works offline.

  ankra skills list
  ankra skills install
  ankra skills install --editor claude-code
  ankra skills install --project .
  ankra skills install ankra-cli ankra-gitops`,
}

type skillsEditor struct {
	Name                   string
	ConfigurationDirectory string
}

type skillsTarget struct {
	Directory  string
	Scope      string
	EditorName string
}

type skillListEntry struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Installed   bool   `json:"installed" yaml:"installed"`
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
		target, err := skillsTargetForCommand(cmd)
		if err != nil {
			return err
		}
		list, err := skills.List(fsys)
		if err != nil {
			return err
		}
		entries := make([]skillListEntry, 0, len(list))
		for _, s := range list {
			entries = append(entries, skillListEntry{
				Name:        s.Name,
				Description: s.Description,
				Installed:   dirExists(filepath.Join(target.Directory, s.Name)),
			})
		}
		if rendered, err := renderStructured(cmd, entries); rendered || err != nil {
			return err
		}
		for _, entry := range entries {
			marker := ""
			if entry.Installed {
				marker = " [installed]"
			}
			fmt.Printf("%s%s\n  %s\n", entry.Name, marker, entry.Description)
		}
		fmt.Printf("\n%d skills available. Install with: ankra skills install\n", len(entries))
		return nil
	},
}

var skillsInstallCmd = &cobra.Command{
	Use:   "install [skill ...]",
	Short: "Install Ankra skills into your skills directory",
	Long: `Install all skills (default) or only the named ones.

By default skills install into ~/.cursor/skills/ (personal, available in every
project). Use --editor claude-code to install into ~/.claude/skills/ instead.
Use --project <DIR> to install into the selected editor's project skills
directory (--project . for the current directory).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fsys, err := skillsSourceFS(cmd)
		if err != nil {
			return err
		}
		target, err := skillsTargetForCommand(cmd)
		if err != nil {
			return err
		}
		force, _ := cmd.Flags().GetBool("force")

		if err := os.MkdirAll(target.Directory, 0o755); err != nil {
			return fmt.Errorf("could not create %s: %w", target.Directory, err)
		}

		installed, skipped, err := skills.Install(fsys, target.Directory, args, force)
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
			len(installed), target.Directory, target.Scope, len(skipped))
		fmt.Printf("Restart %s to load the skills.\n", target.EditorName)
		return nil
	},
}

var skillsUninstallCmd = &cobra.Command{
	Use:   "uninstall [skill ...]",
	Short: "Remove installed Ankra skills",
	Long:  "Remove the named skills, or all Ankra skills when none are given.",
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := skillsTargetForCommand(cmd)
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
		// Validate every name before removing anything, so one bad argument
		// (e.g. "." or "../x") cannot delete the skills directory or escape it.
		for _, name := range names {
			if err := validateSkillName(target.Directory, name); err != nil {
				return err
			}
		}
		removed := 0
		for _, name := range names {
			dest := filepath.Join(target.Directory, name)
			if dirExists(dest) {
				if err := os.RemoveAll(dest); err != nil {
					return fmt.Errorf("could not remove %s: %w", dest, err)
				}
				fmt.Printf("removed %s\n", name)
				removed++
			}
		}
		fmt.Printf("\nRemoved %d skill(s) from %s (%s).\n", removed, target.Directory, target.Scope)
		return nil
	},
}

// validateSkillName rejects skill arguments that would resolve outside dir
// when joined onto it: empty, ".", "..", and anything containing a path
// separator. Belt-and-braces, it also verifies the joined path stays strictly
// inside dir (compare copySkill in internal/skills).
func validateSkillName(dir, name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return withExitCode(exitUsage, fmt.Errorf("invalid skill name %q: must be a bare directory name", name))
	}
	rel, err := filepath.Rel(dir, filepath.Join(dir, name))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return withExitCode(exitUsage, fmt.Errorf("invalid skill name %q: resolves outside %s", name, dir))
	}
	return nil
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
func skillsTargetDir(cmd *cobra.Command) (string, string, error) {
	target, err := skillsTargetForCommand(cmd)
	if err != nil {
		return "", "", err
	}
	return target.Directory, target.Scope, nil
}

func skillsTargetForCommand(cmd *cobra.Command) (skillsTarget, error) {
	editor, err := skillsEditorForCommand(cmd)
	if err != nil {
		return skillsTarget{}, err
	}
	projectFlag := cmd.Flags().Lookup("project")
	if projectFlag != nil && projectFlag.Changed {
		dir := projectFlag.Value.String()
		if dir == "" {
			dir = "."
		}
		return skillsTarget{
			Directory:  filepath.Join(dir, editor.ConfigurationDirectory, "skills"),
			Scope:      "project",
			EditorName: editor.Name,
		}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return skillsTarget{}, fmt.Errorf("could not determine home directory: %w", err)
	}
	return skillsTarget{
		Directory:  filepath.Join(home, editor.ConfigurationDirectory, "skills"),
		Scope:      "personal",
		EditorName: editor.Name,
	}, nil
}

func skillsEditorForCommand(cmd *cobra.Command) (skillsEditor, error) {
	editor, _ := cmd.Flags().GetString("editor")
	switch strings.ToLower(strings.TrimSpace(editor)) {
	case "", "cursor":
		return skillsEditor{Name: "Cursor", ConfigurationDirectory: ".cursor"}, nil
	case "claude", "claude-code", "claudecode":
		return skillsEditor{Name: "Claude Code", ConfigurationDirectory: ".claude"}, nil
	default:
		return skillsEditor{}, fmt.Errorf("unsupported editor %q (expected cursor or claude-code)", editor)
	}
}

func init() {
	for _, c := range []*cobra.Command{skillsListCmd, skillsInstallCmd, skillsUninstallCmd} {
		c.Flags().Bool("personal", false, "install into the selected editor's personal skills directory (default)")
		c.Flags().String("project", "", "install into <DIR>/<editor config>/skills (use \".\" for the current directory)")
		c.Flags().String("editor", "cursor", "skills app to target: cursor or claude-code")
		c.Flags().String("source", "", "read skills from a local directory instead of the embedded copy")
	}
	skillsInstallCmd.Flags().Bool("force", false, "overwrite existing skills without prompting")
	registerStructuredOutputFlags(skillsListCmd)

	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsInstallCmd)
	skillsCmd.AddCommand(skillsUninstallCmd)

	setRequiresAuth(skillsCmd, false)
	rootCmd.AddCommand(skillsCmd)
}
