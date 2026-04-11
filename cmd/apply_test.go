package cmd

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"ankra/internal/client"
)

func TestParseParentList(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected int
	}{
		{
			name: "valid parent list",
			input: []interface{}{
				map[string]interface{}{"name": "cert-manager", "kind": "addon"},
				map[string]interface{}{"name": "namespace", "kind": "manifest"},
			},
			expected: 2,
		},
		{
			name:     "nil input",
			input:    nil,
			expected: 0,
		},
		{
			name:     "non-slice input",
			input:    "not-a-slice",
			expected: 0,
		},
		{
			name: "malformed entry skipped",
			input: []interface{}{
				"not-a-map",
				map[string]interface{}{"name": "valid", "kind": "addon"},
			},
			expected: 1,
		},
		{
			name: "missing name field skipped",
			input: []interface{}{
				map[string]interface{}{"kind": "addon"},
			},
			expected: 0,
		},
		{
			name: "missing kind field skipped",
			input: []interface{}{
				map[string]interface{}{"name": "test"},
			},
			expected: 0,
		},
		{
			name:     "empty slice",
			input:    []interface{}{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseParentList(tt.input)
			if len(result) != tt.expected {
				t.Errorf("parseParentList() returned %d parents, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestBuildManifest(t *testing.T) {
	t.Run("inline content", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":      "my-namespace",
			"manifest":  "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test",
			"namespace": "default",
		}
		m, err := buildManifest(mm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Name != "my-namespace" {
			t.Errorf("name = %q, want %q", m.Name, "my-namespace")
		}
		if m.Namespace != "default" {
			t.Errorf("namespace = %q, want %q", m.Namespace, "default")
		}
		decoded, _ := base64.StdEncoding.DecodeString(m.ManifestBase64)
		if string(decoded) != "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test" {
			t.Errorf("decoded manifest content mismatch")
		}
	})

	t.Run("from_file content", func(t *testing.T) {
		tmpDir := t.TempDir()
		manifestContent := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test"
		manifestPath := filepath.Join(tmpDir, "manifests", "cm.yaml")
		if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
			t.Fatal(err)
		}

		mm := map[string]interface{}{
			"name":      "config-map",
			"from_file": "manifests/cm.yaml",
		}
		m, err := buildManifest(mm, tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		decoded, _ := base64.StdEncoding.DecodeString(m.ManifestBase64)
		if string(decoded) != manifestContent {
			t.Errorf("decoded content = %q, want %q", string(decoded), manifestContent)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		mm := map[string]interface{}{
			"manifest": "content",
		}
		_, err := buildManifest(mm, "")
		if err == nil {
			t.Error("expected error for missing name")
		}
	})

	t.Run("missing both inline and from_file", func(t *testing.T) {
		mm := map[string]interface{}{
			"name": "no-content",
		}
		_, err := buildManifest(mm, "")
		if err == nil {
			t.Error("expected error for missing content")
		}
	})

	t.Run("encrypted_paths parsing", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":            "secret",
			"manifest":        "apiVersion: v1\nkind: Secret",
			"encrypted_paths": []interface{}{"data.password", "data.token"},
		}
		m, err := buildManifest(mm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(m.EncryptedPaths) != 2 {
			t.Errorf("encrypted_paths count = %d, want 2", len(m.EncryptedPaths))
		}
	})
}

func TestBuildAddon(t *testing.T) {
	t.Run("basic addon", func(t *testing.T) {
		am := map[string]interface{}{
			"name":          "cert-manager",
			"chart_name":    "cert-manager",
			"chart_version": "1.14.0",
			"namespace":     "cert-manager",
		}
		a, err := buildAddon(am, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Name != "cert-manager" {
			t.Errorf("name = %q, want %q", a.Name, "cert-manager")
		}
		if a.ChartVersion != "1.14.0" {
			t.Errorf("chart_version = %q, want %q", a.ChartVersion, "1.14.0")
		}
	})

	t.Run("missing name", func(t *testing.T) {
		am := map[string]interface{}{
			"chart_name": "test",
		}
		_, err := buildAddon(am, "")
		if err == nil {
			t.Error("expected error for missing name")
		}
	})

	t.Run("standalone config from_file", func(t *testing.T) {
		tmpDir := t.TempDir()
		valuesContent := "replicaCount: 3\nimage:\n  tag: latest"
		valuesPath := filepath.Join(tmpDir, "values", "cert-manager.yaml")
		if err := os.MkdirAll(filepath.Dir(valuesPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(valuesPath, []byte(valuesContent), 0644); err != nil {
			t.Fatal(err)
		}

		am := map[string]interface{}{
			"name":          "cert-manager",
			"chart_name":    "cert-manager",
			"chart_version": "1.14.0",
			"configuration": map[string]interface{}{
				"from_file": "values/cert-manager.yaml",
			},
		}
		a, err := buildAddon(am, tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg, ok := a.Configuration.(client.AddonStandaloneConfiguration)
		if !ok {
			t.Fatalf("configuration type = %T, want AddonStandaloneConfiguration", a.Configuration)
		}
		decoded, _ := base64.StdEncoding.DecodeString(cfg.ValuesBase64)
		if string(decoded) != valuesContent {
			t.Errorf("decoded values = %q, want %q", string(decoded), valuesContent)
		}
	})

	t.Run("registry fields", func(t *testing.T) {
		am := map[string]interface{}{
			"name":                     "custom-chart",
			"chart_name":               "my-chart",
			"chart_version":            "1.0.0",
			"registry_name":            "my-registry",
			"registry_url":             "oci://registry.example.com",
			"registry_credential_name": "reg-cred",
		}
		a, err := buildAddon(am, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.RegistryName != "my-registry" {
			t.Errorf("registry_name = %q, want %q", a.RegistryName, "my-registry")
		}
		if a.RegistryURL != "oci://registry.example.com" {
			t.Errorf("registry_url = %q, want %q", a.RegistryURL, "oci://registry.example.com")
		}
		if a.RegistryCredentialName != "reg-cred" {
			t.Errorf("registry_credential_name = %q, want %q", a.RegistryCredentialName, "reg-cred")
		}
	})

	t.Run("settings map", func(t *testing.T) {
		am := map[string]interface{}{
			"name":          "with-settings",
			"chart_name":    "test",
			"chart_version": "1.0.0",
			"settings": map[string]interface{}{
				"sync_policy": map[string]interface{}{
					"automated": true,
				},
			},
		}
		a, err := buildAddon(am, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Settings == nil {
			t.Error("expected settings to be populated")
		}
	})
}

func TestBuildStack(t *testing.T) {
	t.Run("stack with manifests and addons", func(t *testing.T) {
		tmpDir := t.TempDir()
		manifestContent := "apiVersion: v1\nkind: Namespace"
		manifestPath := filepath.Join(tmpDir, "ns.yaml")
		if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
			t.Fatal(err)
		}

		sm := map[string]interface{}{
			"name":        "monitoring",
			"description": "Monitoring stack",
			"manifests": []interface{}{
				map[string]interface{}{
					"name":      "namespace",
					"from_file": "ns.yaml",
				},
			},
			"addons": []interface{}{
				map[string]interface{}{
					"name":          "prometheus",
					"chart_name":    "kube-prometheus-stack",
					"chart_version": "56.0.0",
				},
			},
		}
		s, err := buildStack(sm, tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Name != "monitoring" {
			t.Errorf("name = %q, want %q", s.Name, "monitoring")
		}
		if len(s.Manifests) != 1 {
			t.Errorf("manifests count = %d, want 1", len(s.Manifests))
		}
		if len(s.Addons) != 1 {
			t.Errorf("addons count = %d, want 1", len(s.Addons))
		}
	})

	t.Run("missing stack name", func(t *testing.T) {
		sm := map[string]interface{}{
			"description": "no name",
		}
		_, err := buildStack(sm, "")
		if err == nil {
			t.Error("expected error for missing stack name")
		}
	})

	t.Run("empty stack", func(t *testing.T) {
		sm := map[string]interface{}{
			"name": "empty",
		}
		s, err := buildStack(sm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Name != "empty" {
			t.Errorf("name = %q, want %q", s.Name, "empty")
		}
	})

	t.Run("invalid manifest entry", func(t *testing.T) {
		sm := map[string]interface{}{
			"name": "bad-manifest",
			"manifests": []interface{}{
				"not-a-map",
			},
		}
		_, err := buildStack(sm, "")
		if err == nil {
			t.Error("expected error for invalid manifest entry")
		}
	})
}

func TestBuildImportRequest(t *testing.T) {
	t.Run("valid import cluster yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlContent := `kind: ImportCluster
metadata:
  name: test-cluster
  description: A test cluster
spec:
  stacks:
    - name: monitoring
      manifests: []
      addons: []
`
		yamlPath := filepath.Join(tmpDir, "cluster.yaml")
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		req, err := buildImportRequest(yamlPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Name != "test-cluster" {
			t.Errorf("name = %q, want %q", req.Name, "test-cluster")
		}
		if req.Description != "A test cluster" {
			t.Errorf("description = %q, want %q", req.Description, "A test cluster")
		}
		if len(req.Spec.Stacks) != 1 {
			t.Errorf("stacks count = %d, want 1", len(req.Spec.Stacks))
		}
	})

	t.Run("wrong kind", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlContent := `kind: Deployment
metadata:
  name: test
spec: {}`
		yamlPath := filepath.Join(tmpDir, "deploy.yaml")
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := buildImportRequest(yamlPath)
		if err == nil {
			t.Error("expected error for wrong kind")
		}
	})

	t.Run("missing metadata", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlContent := `kind: ImportCluster
spec:
  stacks: []`
		yamlPath := filepath.Join(tmpDir, "no-meta.yaml")
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := buildImportRequest(yamlPath)
		if err == nil {
			t.Error("expected error for missing metadata")
		}
	})

	t.Run("missing spec", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlContent := `kind: ImportCluster
metadata:
  name: test`
		yamlPath := filepath.Join(tmpDir, "no-spec.yaml")
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := buildImportRequest(yamlPath)
		if err == nil {
			t.Error("expected error for missing spec")
		}
	})

	t.Run("missing metadata name", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlContent := `kind: ImportCluster
metadata:
  description: no name
spec:
  stacks: []`
		yamlPath := filepath.Join(tmpDir, "no-name.yaml")
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := buildImportRequest(yamlPath)
		if err == nil {
			t.Error("expected error for missing metadata.name")
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := buildImportRequest("/nonexistent/path/cluster.yaml")
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
	})

	t.Run("git_repository parsing", func(t *testing.T) {
		tmpDir := t.TempDir()
		yamlContent := `kind: ImportCluster
metadata:
  name: git-cluster
spec:
  git_repository:
    provider: github
    credential_name: my-cred
    branch: main
    repository: org/repo
  stacks: []`
		yamlPath := filepath.Join(tmpDir, "git-cluster.yaml")
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		req, err := buildImportRequest(yamlPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Spec.GitRepository == nil {
			t.Fatal("expected git_repository to be set")
		}
		if req.Spec.GitRepository.Provider != "github" {
			t.Errorf("provider = %q, want %q", req.Spec.GitRepository.Provider, "github")
		}
		if req.Spec.GitRepository.Branch != "main" {
			t.Errorf("branch = %q, want %q", req.Spec.GitRepository.Branch, "main")
		}
	})
}
