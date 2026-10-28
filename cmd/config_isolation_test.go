package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"ankra/internal/client"
)

func TestConfigExtSupported(t *testing.T) {
	supported := []string{"a.yaml", "a.yml", "a.json", "/path/to/config.YAML"}
	for _, path := range supported {
		if !configExtSupported(path) {
			t.Errorf("expected %q to be a supported config extension", path)
		}
	}
	unsupported := []string{"config", "config.hetzner", "config.ovh", "worker1", "/run/ankra/cfg"}
	for _, path := range unsupported {
		if configExtSupported(path) {
			t.Errorf("expected %q to be treated as an unsupported extension", path)
		}
	}
}

// An explicit --config file whose extension viper does not recognise must still
// be read (falling back to YAML) so the saved token/base-url are not silently
// dropped.
func TestReadSavedCredentialsHonoursConfigWithUnknownExtension(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.hetzner")
	contents := "token: saved-token\nbase-url: https://platform.ankra.dev\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	originalCfgFile := cfgFile
	cfgFile = path
	t.Cleanup(func() { cfgFile = originalCfgFile })

	token, baseURL := readSavedCredentials()
	if token != "saved-token" {
		t.Errorf("expected saved token from extension-less --config, got %q", token)
	}
	if baseURL != "https://platform.ankra.dev" {
		t.Errorf("expected saved base URL from extension-less --config, got %q", baseURL)
	}
}

func TestSelectedClusterFileDefaultsToHome(t *testing.T) {
	home := withTempHome(t)

	path, err := selectedClusterFile()
	if err != nil {
		t.Fatalf("selectedClusterFile: %v", err)
	}
	want := filepath.Join(home, ".ankra", "selected.json")
	if path != want {
		t.Errorf("expected default selection path %q, got %q", want, path)
	}
}

// A custom --config must fully isolate CLI state: the active-cluster selection
// lives next to the config file, not in the shared $HOME path.
func TestSelectedClusterFileHonoursConfig(t *testing.T) {
	withTempHome(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.hetzner")
	originalCfgFile := cfgFile
	cfgFile = path
	t.Cleanup(func() { cfgFile = originalCfgFile })

	got, err := selectedClusterFile()
	if err != nil {
		t.Fatalf("selectedClusterFile: %v", err)
	}
	want := path + ".selected.json"
	if got != want {
		t.Errorf("expected per-config selection path %q, got %q", want, got)
	}
}

// Two invocations pointed at different --config files must not clobber each
// other's selection -- the bug that broke parallel automation.
func TestSelectedClusterIsolationAcrossConfigs(t *testing.T) {
	withTempHome(t)

	dir := t.TempDir()
	configA := filepath.Join(dir, "config.hetzner")
	configB := filepath.Join(dir, "config.ovh")
	originalCfgFile := cfgFile
	t.Cleanup(func() { cfgFile = originalCfgFile })

	cfgFile = configA
	if err := saveSelectedCluster(client.ClusterListItem{ID: "id-a", Name: "cluster-a"}); err != nil {
		t.Fatalf("save A: %v", err)
	}
	cfgFile = configB
	if err := saveSelectedCluster(client.ClusterListItem{ID: "id-b", Name: "cluster-b"}); err != nil {
		t.Fatalf("save B: %v", err)
	}

	cfgFile = configA
	loadedA, err := loadSelectedCluster()
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	if loadedA.Name != "cluster-a" {
		t.Errorf("config A selection clobbered: got %q, want cluster-a", loadedA.Name)
	}

	cfgFile = configB
	loadedB, err := loadSelectedCluster()
	if err != nil {
		t.Fatalf("load B: %v", err)
	}
	if loadedB.Name != "cluster-b" {
		t.Errorf("config B selection clobbered: got %q, want cluster-b", loadedB.Name)
	}
}
