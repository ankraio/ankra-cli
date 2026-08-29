package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writePluginScript(t *testing.T, directory string, fileName string, marker string) {
	t.Helper()
	script := "#!/bin/sh\necho \"" + marker + " args:$* version:${ANKRA_CLI_VERSION}\" >> \"$PLUGIN_LOG\"\nexit 7\n"
	if err := os.WriteFile(filepath.Join(directory, fileName), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func pluginTestEnvironment(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-ins")
	}
	binDir := t.TempDir()
	log := filepath.Join(t.TempDir(), "plugin.log")
	t.Setenv("PATH", binDir)
	t.Setenv("PLUGIN_LOG", log)
	t.Setenv("HOME", t.TempDir())
	return binDir, log
}

func TestDispatchExternalPluginRunsTheLongestMatch(t *testing.T) {
	binDir, log := pluginTestEnvironment(t)
	writePluginScript(t, binDir, "ankra-hello", "short")
	writePluginScript(t, binDir, "ankra-hello-world", "long")

	handled, exitCode := dispatchExternalPlugin([]string{"hello", "world", "extra", "--flag"})
	if !handled || exitCode != 7 {
		t.Fatalf("handled=%v exit=%d, want a plugin run exiting 7", handled, exitCode)
	}
	content, _ := os.ReadFile(log)
	if !strings.Contains(string(content), "long args:extra --flag") {
		t.Errorf("the longest name must win and consume its words, got %q", content)
	}
	if !strings.Contains(string(content), "version:"+version) {
		t.Errorf("the plugin must see ANKRA_CLI_VERSION, got %q", content)
	}
}

func TestDispatchExternalPluginNeverShadowsABuiltin(t *testing.T) {
	binDir, log := pluginTestEnvironment(t)
	writePluginScript(t, binDir, "ankra-migrate", "impostor")
	writePluginScript(t, binDir, "ankra-help", "helpImpostor")

	if handled, _ := dispatchExternalPlugin([]string{"migrate", "modules"}); handled {
		t.Error("a built-in command must never be dispatched to a plugin")
	}
	if handled, _ := dispatchExternalPlugin([]string{"help"}); handled {
		t.Error("cobra's own entry points must never be dispatched")
	}
	if handled, _ := dispatchExternalPlugin([]string{"--version"}); handled {
		t.Error("flags are not plugin names")
	}
	if handled, _ := dispatchExternalPlugin([]string{"no-such-thing"}); handled {
		t.Error("an unknown command without a plugin falls through to the normal error")
	}
	if content, _ := os.ReadFile(log); len(content) != 0 {
		t.Errorf("nothing may have run, got %q", content)
	}
}

func TestDispatchExternalPluginPrefersTheHomeDirectory(t *testing.T) {
	binDir, log := pluginTestEnvironment(t)
	writePluginScript(t, binDir, "ankra-hello", "from-path")
	homePlugins := filepath.Join(os.Getenv("HOME"), ".ankra", "plugins")
	if err := os.MkdirAll(homePlugins, 0o755); err != nil {
		t.Fatal(err)
	}
	writePluginScript(t, homePlugins, "ankra-hello", "from-home")

	if handled, exitCode := dispatchExternalPlugin([]string{"hello"}); !handled || exitCode != 7 {
		t.Fatalf("handled=%v exit=%d", handled, exitCode)
	}
	if content, _ := os.ReadFile(log); !strings.Contains(string(content), "from-home") {
		t.Errorf("~/.ankra/plugins wins over PATH, got %q", content)
	}
}

func TestPluginsListsWhatWouldRun(t *testing.T) {
	binDir, _ := pluginTestEnvironment(t)
	writePluginScript(t, binDir, "ankra-hello-world", "x")
	writePluginScript(t, binDir, "ankra-migrate", "impostor")
	writePluginScript(t, binDir, "ankra-module-procfile", "module")

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"plugins"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ankra hello world") || !strings.Contains(stdout.String(), filepath.Join(binDir, "ankra-hello-world")) {
		t.Errorf("the listing must show the command and its path:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "module-procfile") {
		t.Errorf("migrate modules are not plugins:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), `shadowed by the built-in command "migrate"`) {
		t.Errorf("a plugin behind a built-in must be called out:\n%s", stderr.String())
	}
}
