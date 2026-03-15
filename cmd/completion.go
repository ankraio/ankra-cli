package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion",
	Short: "Generate or install shell completion scripts",
	Long: `Generate or install shell completion scripts for ankra.

To print a completion script to stdout:
  ankra completion bash
  ankra completion zsh
  ankra completion fish
  ankra completion powershell

To auto-detect your shell and install completions:
  ankra completion install

To install for a specific shell:
  ankra completion install --shell zsh`,
}

var completionBashCmd = &cobra.Command{
	Use:   "bash",
	Short: "Print bash completion script",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return rootCmd.GenBashCompletion(cmd.OutOrStdout())
	},
}

var completionZshCmd = &cobra.Command{
	Use:   "zsh",
	Short: "Print zsh completion script",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return rootCmd.GenZshCompletion(cmd.OutOrStdout())
	},
}

var completionFishCmd = &cobra.Command{
	Use:   "fish",
	Short: "Print fish completion script",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return rootCmd.GenFishCompletion(cmd.OutOrStdout(), true)
	},
}

var completionPowershellCmd = &cobra.Command{
	Use:   "powershell",
	Short: "Print powershell completion script",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return rootCmd.GenPowerShellCompletion(cmd.OutOrStdout())
	},
}

var completionInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install shell completions for the current user",
	Long: `Detect your shell and install completion scripts automatically.

The command writes the completion script to the standard location for your
shell and updates your shell profile to source it (if needed).

Use --shell to override auto-detection.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		shellFlag, _ := cmd.Flags().GetString("shell")
		detectedShell := resolveShell(shellFlag)
		if detectedShell == "" {
			return fmt.Errorf("could not detect shell from $SHELL; use --shell to specify one of: bash, zsh, fish, powershell")
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("could not determine home directory: %w", err)
		}

		switch detectedShell {
		case "bash":
			return installBashCompletion(home)
		case "zsh":
			return installZshCompletion(home)
		case "fish":
			return installFishCompletion(home)
		case "powershell":
			return installPowershellCompletion(home)
		default:
			return fmt.Errorf("unsupported shell %q; supported: bash, zsh, fish, powershell", detectedShell)
		}
	},
}

func init() {
	completionInstallCmd.Flags().String("shell", "", "shell type (bash, zsh, fish, powershell)")

	completionCmd.AddCommand(completionBashCmd)
	completionCmd.AddCommand(completionZshCmd)
	completionCmd.AddCommand(completionFishCmd)
	completionCmd.AddCommand(completionPowershellCmd)
	completionCmd.AddCommand(completionInstallCmd)

	rootCmd.AddCommand(completionCmd)
}

func resolveShell(flagValue string) string {
	if flagValue != "" {
		return normalizeShellName(flagValue)
	}
	shellEnv := os.Getenv("SHELL")
	if shellEnv == "" {
		return ""
	}
	return normalizeShellName(filepath.Base(shellEnv))
}

func normalizeShellName(name string) string {
	switch strings.ToLower(name) {
	case "bash":
		return "bash"
	case "zsh":
		return "zsh"
	case "fish":
		return "fish"
	case "powershell", "pwsh":
		return "powershell"
	default:
		return ""
	}
}

func installBashCompletion(home string) error {
	var scriptBuf bytes.Buffer
	if err := rootCmd.GenBashCompletion(&scriptBuf); err != nil {
		return fmt.Errorf("failed to generate bash completion: %w", err)
	}

	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome == "" {
		xdgDataHome = filepath.Join(home, ".local", "share")
	}
	bashCompletionDir := filepath.Join(xdgDataHome, "bash-completion", "completions")

	if dirExists(bashCompletionDir) {
		completionFile := filepath.Join(bashCompletionDir, "ankra")
		if err := writeFile(completionFile, scriptBuf.Bytes()); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Completion script written to %s\n", completionFile)
		fmt.Fprintln(os.Stderr, "bash-completion will load it automatically. Restart your shell to activate.")
		return nil
	}

	completionFile := filepath.Join(home, ".ankra-completion.bash")
	if err := writeFile(completionFile, scriptBuf.Bytes()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Completion script written to %s\n", completionFile)

	profilePath := filepath.Join(home, ".bashrc")
	sourceLine := fmt.Sprintf("source %q", completionFile)
	added, err := appendLineIfMissing(profilePath, sourceLine, "ankra-completion")
	if err != nil {
		return fmt.Errorf("failed to update %s: %w", profilePath, err)
	}
	if added {
		fmt.Fprintf(os.Stderr, "Added source line to %s\n", profilePath)
	} else {
		fmt.Fprintf(os.Stderr, "Source line already present in %s\n", profilePath)
	}
	fmt.Fprintf(os.Stderr, "Restart your shell or run: source %s\n", profilePath)
	return nil
}

func installZshCompletion(home string) error {
	var scriptBuf bytes.Buffer
	if err := rootCmd.GenZshCompletion(&scriptBuf); err != nil {
		return fmt.Errorf("failed to generate zsh completion: %w", err)
	}

	completionDir := filepath.Join(home, ".zsh", "completions")
	completionFile := filepath.Join(completionDir, "_ankra")
	if err := writeFile(completionFile, scriptBuf.Bytes()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Completion script written to %s\n", completionFile)

	profilePath := filepath.Join(home, ".zshrc")
	zshBlock := fmt.Sprintf("fpath=(%s $fpath)\nautoload -Uz compinit && compinit", completionDir)
	added, err := appendLineIfMissing(profilePath, zshBlock, ".zsh/completions")
	if err != nil {
		return fmt.Errorf("failed to update %s: %w", profilePath, err)
	}

	if added {
		fmt.Fprintf(os.Stderr, "Updated %s with fpath and compinit\n", profilePath)
	} else {
		fmt.Fprintf(os.Stderr, "Ankra completion already configured in %s\n", profilePath)
	}
	fmt.Fprintf(os.Stderr, "Restart your shell or run: source %s\n", profilePath)
	return nil
}

func installFishCompletion(home string) error {
	var scriptBuf bytes.Buffer
	if err := rootCmd.GenFishCompletion(&scriptBuf, true); err != nil {
		return fmt.Errorf("failed to generate fish completion: %w", err)
	}

	completionFile := filepath.Join(home, ".config", "fish", "completions", "ankra.fish")
	if err := writeFile(completionFile, scriptBuf.Bytes()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Completion script written to %s\n", completionFile)
	fmt.Fprintln(os.Stderr, "Fish will load it automatically. Restart your shell to activate.")
	return nil
}

func installPowershellCompletion(home string) error {
	var scriptBuf bytes.Buffer
	if err := rootCmd.GenPowerShellCompletion(&scriptBuf); err != nil {
		return fmt.Errorf("failed to generate powershell completion: %w", err)
	}

	configDir := filepath.Join(home, ".config", "ankra")
	completionFile := filepath.Join(configDir, "completion.ps1")
	if err := writeFile(completionFile, scriptBuf.Bytes()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Completion script written to %s\n", completionFile)

	profilePath := powershellProfilePath(home)
	sourceLine := fmt.Sprintf(". %q", completionFile)
	added, err := appendLineIfMissing(profilePath, sourceLine, "ankra/completion.ps1")
	if err != nil {
		return fmt.Errorf("failed to update %s: %w", profilePath, err)
	}
	if added {
		fmt.Fprintf(os.Stderr, "Added source line to %s\n", profilePath)
	} else {
		fmt.Fprintf(os.Stderr, "Source line already present in %s\n", profilePath)
	}
	fmt.Fprintln(os.Stderr, "Restart PowerShell to activate completions.")
	return nil
}

func powershellProfilePath(home string) string {
	if p := os.Getenv("PROFILE"); p != "" {
		return p
	}
	return filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1")
}

func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

func appendLineIfMissing(filePath, line, marker string) (bool, error) {
	if fileContainsMarker(filePath, marker) {
		return false, nil
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close file: %v\n", closeErr)
		}
	}()
	_, err = fmt.Fprintf(f, "\n%s\n", line)
	return err == nil, err
}

func fileContainsMarker(filePath, marker string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close file: %v\n", closeErr)
		}
	}()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), marker) {
			return true
		}
	}
	return false
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
