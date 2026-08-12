package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMinimalImportCluster(t *testing.T) string {
	t.Helper()
	yamlPath := filepath.Join(t.TempDir(), "cluster.yaml")
	contents := `kind: ImportCluster
metadata:
  name: repoint-guard-cluster
spec:
  stacks: []
`
	if writeError := os.WriteFile(yamlPath, []byte(contents), 0o644); writeError != nil {
		t.Fatal(writeError)
	}
	return yamlPath
}

// TestApplyRepointFlagsDefaultToOff pins that an ordinary apply asks for
// neither acknowledgement. Every apply carries git_repository, so a flag that
// defaulted on would silently re-enable the destructive path the server guard
// exists to stop (ankra-po6d).
func TestApplyRepointFlagsDefaultToOff(t *testing.T) {
	for _, flagName := range []string{"allow-repoint", "allow-repoint-destroying-data"} {
		flag := clusterApplyCmd.Flags().Lookup(flagName)
		if flag == nil {
			t.Fatalf("--%s is not registered on cluster apply", flagName)
		}
		if flag.DefValue != "false" {
			t.Fatalf("--%s defaults to %q, want false", flagName, flag.DefValue)
		}
	}
}

// TestApplyRepointFlagsExplainThePrune pins that the help text says what the
// flag permits. "allow-repoint" on its own reads like a permission toggle
// rather than a delete.
func TestApplyRepointFlagsExplainThePrune(t *testing.T) {
	repointUsage := clusterApplyCmd.Flags().Lookup("allow-repoint").Usage
	if !strings.Contains(repointUsage, "pruned") {
		t.Fatalf("--allow-repoint usage does not mention pruning: %q", repointUsage)
	}
	dataUsage := clusterApplyCmd.Flags().Lookup("allow-repoint-destroying-data").Usage
	if !strings.Contains(dataUsage, "PersistentVolumeClaims") || !strings.Contains(dataUsage, "--allow-repoint") {
		t.Fatalf("--allow-repoint-destroying-data usage is incomplete: %q", dataUsage)
	}
}

// TestApplyDataFlagRequiresRepointFlag pins the dependency between the two.
// Accepting the data acknowledgement alone would let a caller believe they had
// confirmed the repoint when the server had not been told.
func TestApplyDataFlagRequiresRepointFlag(t *testing.T) {
	t.Cleanup(func() {
		_ = clusterApplyCmd.Flags().Set("allow-repoint", "false")
		_ = clusterApplyCmd.Flags().Set("allow-repoint-destroying-data", "false")
		_ = clusterApplyCmd.Flags().Set("file", "")
	})
	if setError := clusterApplyCmd.Flags().Set("allow-repoint-destroying-data", "true"); setError != nil {
		t.Fatalf("setting flag: %v", setError)
	}
	if setError := clusterApplyCmd.Flags().Set("file", writeMinimalImportCluster(t)); setError != nil {
		t.Fatalf("setting flag: %v", setError)
	}

	runError := runApply(clusterApplyCmd, nil)
	if runError == nil || !strings.Contains(runError.Error(), "requires --allow-repoint") {
		t.Fatalf("error = %v, want the dependency between the two flags to be reported", runError)
	}
}
