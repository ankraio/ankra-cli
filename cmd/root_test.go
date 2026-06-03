package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestCommandRequiresAuth(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	defaultChild := &cobra.Command{Use: "default"}
	authChild := &cobra.Command{Use: "auth"}
	openChild := &cobra.Command{Use: "open"}
	openGrand := &cobra.Command{Use: "open-grand"}
	setRequiresAuth(authChild, true)
	setRequiresAuth(openChild, false)
	root.AddCommand(defaultChild)
	root.AddCommand(authChild)
	openChild.AddCommand(openGrand)
	root.AddCommand(openChild)

	if !commandRequiresAuth(defaultChild) {
		t.Errorf("default command should require auth")
	}
	if !commandRequiresAuth(authChild) {
		t.Errorf("explicit auth command should require auth")
	}
	if commandRequiresAuth(openChild) {
		t.Errorf("explicit open command should not require auth")
	}
	if commandRequiresAuth(openGrand) {
		t.Errorf("child of open command should inherit open status")
	}

	helpCmd := &cobra.Command{Use: "help"}
	root.AddCommand(helpCmd)
	if commandRequiresAuth(helpCmd) {
		t.Errorf("help command should never require auth")
	}
}

func TestPersistentPreRunESkipsAuthFreeCommands(t *testing.T) {
	openCmd := &cobra.Command{Use: "open"}
	setRequiresAuth(openCmd, false)
	if err := persistentPreRunE(openCmd, nil); err != nil {
		t.Fatalf("expected no error for auth-free command, got %v", err)
	}
}

func TestCommandDryRunSkipsAuthOnlyForOfflineCommands(t *testing.T) {
	root := &cobra.Command{Use: "root"}

	offlineCmd := &cobra.Command{Use: "apply"}
	offlineCmd.Flags().Bool("dry-run", false, "")
	setDryRunOffline(offlineCmd)
	root.AddCommand(offlineCmd)

	onlineDryRunCmd := &cobra.Command{Use: "upgrade"}
	onlineDryRunCmd.Flags().Bool("dry-run", false, "")
	root.AddCommand(onlineDryRunCmd)

	noFlagCmd := &cobra.Command{Use: "info"}
	root.AddCommand(noFlagCmd)

	if commandDryRunSkipsAuth(offlineCmd) {
		t.Error("offline command without --dry-run set should still require auth")
	}
	if err := offlineCmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	if !commandDryRunSkipsAuth(offlineCmd) {
		t.Error("offline command with --dry-run should skip auth")
	}

	if err := onlineDryRunCmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	if commandDryRunSkipsAuth(onlineDryRunCmd) {
		t.Error("a --dry-run command that still calls the API must NOT skip auth")
	}

	if commandDryRunSkipsAuth(noFlagCmd) {
		t.Error("command without a --dry-run flag must not skip auth")
	}
}

func TestResolveCredentialsTokenFlagBeatsConfigAndEnv(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)

	t.Setenv("ANKRA_API_TOKEN", "env-token")
	writeAnkraConfig(t, map[string]string{
		"token":    "saved-token",
		"base-url": "https://saved.example.com",
	})

	cmd := buildTestCommandWithFlags(t, map[string]string{
		"token": "flag-token",
	})

	resolved, err := resolveCredentials(cmd)
	if err != nil {
		t.Fatalf("resolveCredentials returned error: %v", err)
	}
	if resolved.token != "flag-token" {
		t.Errorf("expected flag token, got %q", resolved.token)
	}
	// flag has no --base-url, so saved baseURL is used
	if resolved.baseURL != "https://saved.example.com" {
		t.Errorf("expected saved base URL, got %q", resolved.baseURL)
	}
}

func TestResolveCredentialsSavedTokenIgnoresEnvBaseURL(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)

	writeAnkraConfig(t, map[string]string{
		"token":    "saved-token",
		"base-url": "https://platform.ankra.app",
	})
	t.Setenv("ANKRA_BASE_URL", "https://attacker.example.com")

	cmd := buildTestCommandWithFlags(t, nil)
	resolved, err := resolveCredentials(cmd)
	if err != nil {
		t.Fatalf("resolveCredentials returned error: %v", err)
	}
	if resolved.token != "saved-token" {
		t.Errorf("expected saved token, got %q", resolved.token)
	}
	if resolved.baseURL != "https://platform.ankra.app" {
		t.Errorf("expected saved base URL to win over env, got %q", resolved.baseURL)
	}
}

func TestResolveCredentialsEnvBaseURLUsedWhenNoSavedToken(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)

	t.Setenv("ANKRA_API_TOKEN", "env-token")
	t.Setenv("ANKRA_BASE_URL", "https://env.example.com")

	cmd := buildTestCommandWithFlags(t, nil)
	resolved, err := resolveCredentials(cmd)
	if err != nil {
		t.Fatalf("resolveCredentials returned error: %v", err)
	}
	if resolved.token != "env-token" {
		t.Errorf("expected env token, got %q", resolved.token)
	}
	if resolved.baseURL != "https://env.example.com" {
		t.Errorf("expected env base URL, got %q", resolved.baseURL)
	}
}

func TestResolveCredentialsRejectsHTTPByDefault(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)

	t.Setenv("ANKRA_API_TOKEN", "env-token")
	t.Setenv("ANKRA_BASE_URL", "http://attacker.example.com")

	cmd := buildTestCommandWithFlags(t, nil)
	_, err := resolveCredentials(cmd)
	if err == nil {
		t.Fatalf("expected plaintext base URL to be rejected")
	}
	if !strings.Contains(err.Error(), "plaintext http") {
		t.Errorf("expected plaintext error, got %v", err)
	}
}

func TestResolveCredentialsAllowsLoopbackHTTP(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)

	t.Setenv("ANKRA_API_TOKEN", "env-token")
	t.Setenv("ANKRA_BASE_URL", "http://localhost:8080")

	cmd := buildTestCommandWithFlags(t, nil)
	resolved, err := resolveCredentials(cmd)
	if err != nil {
		t.Fatalf("loopback http should be allowed, got %v", err)
	}
	if resolved.baseURL != "http://localhost:8080" {
		t.Errorf("expected loopback URL, got %q", resolved.baseURL)
	}
}

func TestResolveCredentialsAllowsHTTPWithInsecureOverride(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)

	t.Setenv("ANKRA_API_TOKEN", "env-token")
	t.Setenv("ANKRA_BASE_URL", "http://internal.example.lan")
	t.Setenv(envAllowInsecureHTTP, "1")

	cmd := buildTestCommandWithFlags(t, nil)
	resolved, err := resolveCredentials(cmd)
	if err != nil {
		t.Fatalf("override should permit plaintext, got %v", err)
	}
	if resolved.baseURL != "http://internal.example.lan" {
		t.Errorf("expected override URL, got %q", resolved.baseURL)
	}
}

func TestResolveCredentialsReturnsErrorWhenNoToken(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)

	cmd := buildTestCommandWithFlags(t, nil)
	_, err := resolveCredentials(cmd)
	if err == nil {
		t.Fatalf("expected error when no credentials configured")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected not-logged-in error, got %v", err)
	}
}

// buildTestCommandWithFlags wires a fresh cobra root + child command with
// the same persistent flag names as the real rootCmd. Flag values listed in
// the map are marked Changed=true to simulate `--flag value` on the command
// line.
func buildTestCommandWithFlags(t *testing.T, flagValues map[string]string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "ankra"}
	root.PersistentFlags().String("token", "", "")
	root.PersistentFlags().String("base-url", "", "")
	for name, value := range flagValues {
		if err := root.PersistentFlags().Set(name, value); err != nil {
			t.Fatalf("set flag %s: %v", name, err)
		}
	}
	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)
	return child
}

func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	originalCfgFile := cfgFile
	cfgFile = ""
	t.Cleanup(func() { cfgFile = originalCfgFile })
	return tmp
}

func writeAnkraConfig(t *testing.T, values map[string]string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home: %v", err)
	}
	var builder strings.Builder
	for key, value := range values {
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(value)
		builder.WriteString("\n")
	}
	path := filepath.Join(home, ".ankra.yaml")
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func withFreshViperAndEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ANKRA_API_TOKEN", "")
	t.Setenv("ANKRA_BASE_URL", "")
	t.Setenv(envAllowInsecureHTTP, "")
	originalViper := viper.GetViper()
	t.Cleanup(func() {
		_ = originalViper
	})
	viper.Reset()
	viper.SetDefault("base-url", defaultBaseURL)
}
