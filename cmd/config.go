package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// CLISettings holds local, non-secret CLI preferences. It is stored separately
// from the credential config (~/.ankra.yaml) so toggling a preference never
// risks rewriting the saved token or base URL.
type CLISettings struct {
	BetaReleases bool `json:"beta_releases"`
}

func cliSettingsFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ankra")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

func loadCLISettings() (CLISettings, error) {
	var settings CLISettings
	path, err := cliSettingsFile()
	if err != nil {
		return settings, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return settings, nil
		}
		return settings, err
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, err
	}
	return settings, nil
}

func saveCLISettings(settings CLISettings) error {
	path, err := cliSettingsFile()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// betaReleasesEnabled reports whether the beta (pre-release) update channel is
// turned on. A missing or unreadable settings file means the default: off.
func betaReleasesEnabled() bool {
	settings, err := loadCLISettings()
	if err != nil {
		return false
	}
	return settings.BetaReleases
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage Ankra CLI settings",
	Long:  "View and change local Ankra CLI preferences, such as the update channel.",
}

var configBetaCmd = &cobra.Command{
	Use:   "beta",
	Short: "Manage the beta (pre-release) update channel",
	Long: `Control whether 'ankra upgrade' installs pre-release (release candidate)
versions.

When enabled, 'ankra upgrade' resolves the newest release including
pre-releases such as v0.3.0-rc.1. When disabled (the default), only stable
x.x.x releases are installed.`,
}

var configBetaEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable the beta update channel",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		settings, err := loadCLISettings()
		if err != nil {
			return fmt.Errorf("read settings: %w", err)
		}
		settings.BetaReleases = true
		if err := saveCLISettings(settings); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Beta channel enabled. `ankra upgrade` will now install pre-release versions.")
		return nil
	},
}

var configBetaDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable the beta update channel (use stable releases)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		settings, err := loadCLISettings()
		if err != nil {
			return fmt.Errorf("read settings: %w", err)
		}
		settings.BetaReleases = false
		if err := saveCLISettings(settings); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Beta channel disabled. `ankra upgrade` will install stable releases only.")
		return nil
	},
}

var configBetaStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether the beta update channel is enabled",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if betaReleasesEnabled() {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Beta channel: enabled (pre-release versions)")
		} else {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Beta channel: disabled (stable releases)")
		}
		return nil
	},
}

func init() {
	configBetaCmd.AddCommand(configBetaEnableCmd)
	configBetaCmd.AddCommand(configBetaDisableCmd)
	configBetaCmd.AddCommand(configBetaStatusCmd)
	configCmd.AddCommand(configBetaCmd)

	setRequiresAuth(configCmd, false)
	rootCmd.AddCommand(configCmd)
}
