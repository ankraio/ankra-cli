package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The regression scenario behind these tests (bead ankra-qh3z): cluster.yaml
// files rewritten by `cluster encrypt -f` and `cluster clone` used to be
// round-tripped through the ImportClusterConfig structs, silently deleting
// stack deploy_wave, spec.prometheus_metrics, comments, and ordering from a
// GitOps source of truth.

const preservationClusterYAML = `apiVersion: v1
kind: ImportCluster
metadata:
  name: prod
spec:
  # prometheus scraping for this cluster
  prometheus_metrics:
    endpoint: https://prom.example.com
    flavor: victoriametrics
  stacks:
    - name: core
      deploy_wave: 2 # after networking
      manifests:
        - name: db-secret
          from_file: manifests/secret.yaml
      addons:
        - name: grafana
          chart_name: grafana
          chart_version: "7.0.0"
          configuration:
            from_file: values/grafana.yaml
`

func assertContainsAll(t *testing.T, output string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(output, want) {
			t.Errorf("output is missing %q\n---\n%s", want, output)
		}
	}
}

func setCloneFlags(t *testing.T, clean, force, copyMissing bool, stacks []string) {
	t.Helper()
	originalClean, originalForce, originalCopyMissing, originalStack := cleanFlag, forceFlag, copyMissingFlag, stackFlag
	cleanFlag, forceFlag, copyMissingFlag, stackFlag = clean, force, copyMissing, stacks
	t.Cleanup(func() {
		cleanFlag, forceFlag, copyMissingFlag, stackFlag = originalClean, originalForce, originalCopyMissing, originalStack
	})
}

const cloneSourceClusterYAML = `apiVersion: v1
kind: ImportCluster
metadata:
  name: source-cluster
spec:
  git_repository:
    provider: github
    credential_name: gh
    branch: main
    future_field: keep-me
  stacks:
    - name: monitoring
      deploy_wave: 3 # deploy last
      manifests:
        - name: grafana-ns
          from_file: manifests/ns.yaml
`

func writeCloneSource(t *testing.T) string {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "manifests", "ns.yaml"), []byte("kind: Namespace"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(srcDir, "cluster.yaml")
	if err := os.WriteFile(srcPath, []byte(cloneSourceClusterYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcPath
}

func TestRunClone_ExistingTargetPreservesUnmodeledFields(t *testing.T) {
	srcPath := writeCloneSource(t)

	dstDir := t.TempDir()
	dstPath := filepath.Join(dstDir, "cluster.yaml")
	targetYAML := `apiVersion: v1
kind: ImportCluster
metadata:
  name: target-cluster
spec:
  # keep prometheus wired
  prometheus_metrics:
    endpoint: https://prom.target.example
  stacks:
    - name: networking
      deploy_wave: 1
      manifests:
        - name: ingress
          from_file: manifests/ingress.yaml
`
	if err := os.WriteFile(dstPath, []byte(targetYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	setCloneFlags(t, false, false, false, []string{})

	if err := runClone(nil, []string{srcPath, dstPath}); err != nil {
		t.Fatalf("runClone failed: %v", err)
	}

	written, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsAll(t, string(written), []string{
		"# keep prometheus wired",
		"prometheus_metrics:",
		"endpoint: https://prom.target.example",
		"deploy_wave: 1",
		"deploy_wave: 3",
		"# deploy last",
	})

	var result ImportClusterConfig
	if err := yaml.Unmarshal(written, &result); err != nil {
		t.Fatalf("parse written target: %v", err)
	}
	if len(result.Spec.Stacks) != 2 {
		t.Fatalf("stacks = %d, want 2 (networking + monitoring)", len(result.Spec.Stacks))
	}
}

func TestRunClone_ForceReplacePreservesUnmodeledFields(t *testing.T) {
	srcPath := writeCloneSource(t)

	dstDir := t.TempDir()
	dstPath := filepath.Join(dstDir, "cluster.yaml")
	targetYAML := `apiVersion: v1
kind: ImportCluster
metadata:
  name: target-cluster
spec:
  prometheus_metrics:
    endpoint: https://prom.target.example
  stacks:
    - name: monitoring
      deploy_wave: 1
      manifests:
        - name: old-ns
          from_file: manifests/old.yaml
`
	if err := os.WriteFile(dstPath, []byte(targetYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	setCloneFlags(t, false, true, false, []string{})

	if err := runClone(nil, []string{srcPath, dstPath}); err != nil {
		t.Fatalf("runClone failed: %v", err)
	}

	written, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsAll(t, string(written), []string{
		"prometheus_metrics:",
		"deploy_wave: 3",
		"# deploy last",
	})

	var result ImportClusterConfig
	if err := yaml.Unmarshal(written, &result); err != nil {
		t.Fatalf("parse written target: %v", err)
	}
	if len(result.Spec.Stacks) != 1 {
		t.Fatalf("stacks = %d, want 1 (replaced monitoring)", len(result.Spec.Stacks))
	}
	if name := result.Spec.Stacks[0].Manifests[0].Name; name != "grafana-ns" {
		t.Errorf("replaced stack manifest = %q, want %q", name, "grafana-ns")
	}
}

func TestRunClone_NewTargetCarriesUnmodeledFields(t *testing.T) {
	srcPath := writeCloneSource(t)

	dstDir := t.TempDir()
	dstPath := filepath.Join(dstDir, "new-cluster.yaml")

	setCloneFlags(t, false, false, false, []string{})

	if err := runClone(nil, []string{srcPath, dstPath}); err != nil {
		t.Fatalf("runClone failed: %v", err)
	}

	written, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsAll(t, string(written), []string{
		"kind: ImportCluster",
		"future_field: keep-me",
		"deploy_wave: 3",
		"# deploy last",
	})

	var result ImportClusterConfig
	if err := yaml.Unmarshal(written, &result); err != nil {
		t.Fatalf("parse written target: %v", err)
	}
	if result.Metadata.Name != "source-cloned-cluster" {
		t.Errorf("metadata.name = %q, want %q", result.Metadata.Name, "source-cloned-cluster")
	}
	if len(result.Spec.Stacks) != 1 {
		t.Fatalf("stacks = %d, want 1", len(result.Spec.Stacks))
	}
}

type encryptFileMock struct {
	baseMock
	encrypted string
}

func (m encryptFileMock) EncryptYAML(yamlContent string, encryptedPaths []string) (string, error) {
	return m.encrypted, nil
}

func TestRunEncryptManifestFile_PreservesUnmodeledFields(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(clusterPath, []byte(preservationClusterYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifests", "secret.yaml")
	if err := os.WriteFile(manifestPath, []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\ndata:\n  password: aHVudGVyMg==\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	encryptedManifest := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\ndata:\n  password: ENC[AES256_GCM,data:abc,tag:def]\nsops:\n  mac: ENC[AES256_GCM,data:mac]\n"
	previousClient := apiClient
	apiClient = encryptFileMock{encrypted: encryptedManifest}
	t.Cleanup(func() { apiClient = previousClient })

	previousFile := encryptClusterFile
	encryptClusterFile = clusterPath
	t.Cleanup(func() { encryptClusterFile = previousFile })

	if err := runEncryptManifestFile(nil, "db-secret", []string{"password"}); err != nil {
		t.Fatalf("runEncryptManifestFile failed: %v", err)
	}

	manifestContent, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(manifestContent) != encryptedManifest {
		t.Errorf("manifest file = %q, want the encrypted content", string(manifestContent))
	}

	written, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	assertContainsAll(t, string(written), []string{
		"prometheus_metrics:",
		"endpoint: https://prom.example.com",
		"deploy_wave: 2",
		"# prometheus scraping for this cluster",
		"# after networking",
	})

	var result ImportClusterConfig
	if err := yaml.Unmarshal(written, &result); err != nil {
		t.Fatalf("parse written cluster file: %v", err)
	}
	paths := result.Spec.Stacks[0].Manifests[0].EncryptedPaths
	if len(paths) != 1 || paths[0] != "password" {
		t.Errorf("encrypted_paths = %v, want [password]", paths)
	}
}
