package cmd

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
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

	t.Run("multi-document inline manifest", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":     "two-docs",
			"manifest": "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: a\n---\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: b",
		}
		if _, err := buildManifest(mm, ""); err != nil {
			t.Fatalf("unexpected error for multi-document manifest: %v", err)
		}
	})

	t.Run("invalid inline YAML", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":     "broken",
			"manifest": "apiVersion: v1\n  kind: Namespace\n bad: : :",
		}
		_, err := buildManifest(mm, "")
		if err == nil {
			t.Fatal("expected error for invalid inline YAML")
		}
		if !strings.Contains(err.Error(), "not valid YAML") {
			t.Errorf("expected YAML validation error, got: %v", err)
		}
	})

	t.Run("invalid from_file YAML names the file", func(t *testing.T) {
		tmpDir := t.TempDir()
		manifestPath := filepath.Join(tmpDir, "broken.yaml")
		if err := os.WriteFile(manifestPath, []byte("key: value\n\tbad-tab-indent: x"), 0644); err != nil {
			t.Fatal(err)
		}
		mm := map[string]interface{}{
			"name":      "broken-file",
			"from_file": "broken.yaml",
		}
		_, err := buildManifest(mm, tmpDir)
		if err == nil {
			t.Fatal("expected error for invalid from_file YAML")
		}
		if !strings.Contains(err.Error(), "broken.yaml") || !strings.Contains(err.Error(), "not valid YAML") {
			t.Errorf("expected error naming the file and YAML problem, got: %v", err)
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

	t.Run("invalid inline values YAML", func(t *testing.T) {
		am := map[string]interface{}{
			"name":          "bad-values",
			"chart_name":    "test",
			"chart_version": "1.0.0",
			"configuration": map[string]interface{}{
				"values": "replicaCount: 3\n  badIndent: : :",
			},
		}
		_, err := buildAddon(am, "")
		if err == nil {
			t.Fatal("expected error for invalid inline values YAML")
		}
		if !strings.Contains(err.Error(), "not valid YAML") {
			t.Errorf("expected YAML validation error, got: %v", err)
		}
	})

	t.Run("invalid values file YAML names the file", func(t *testing.T) {
		tmpDir := t.TempDir()
		valuesPath := filepath.Join(tmpDir, "values", "bad.yaml")
		if err := os.MkdirAll(filepath.Dir(valuesPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(valuesPath, []byte("a: b\n\tc: d"), 0644); err != nil {
			t.Fatal(err)
		}
		am := map[string]interface{}{
			"name":          "bad-values-file",
			"chart_name":    "test",
			"chart_version": "1.0.0",
			"configuration": map[string]interface{}{
				"from_file": "values/bad.yaml",
			},
		}
		_, err := buildAddon(am, tmpDir)
		if err == nil {
			t.Fatal("expected error for invalid values file YAML")
		}
		if !strings.Contains(err.Error(), "bad.yaml") || !strings.Contains(err.Error(), "not valid YAML") {
			t.Errorf("expected error naming the file and YAML problem, got: %v", err)
		}
	})

	t.Run("encrypted_paths round-trip from inline values", func(t *testing.T) {
		am := map[string]interface{}{
			"name":          "sealed",
			"chart_name":    "test",
			"chart_version": "1.0.0",
			"configuration": map[string]interface{}{
				"values":          "password: ENC[...]",
				"encrypted_paths": []interface{}{"password", "apiKey"},
			},
		}
		a, err := buildAddon(am, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg, ok := a.Configuration.(client.AddonStandaloneConfiguration)
		if !ok {
			t.Fatalf("configuration type = %T, want AddonStandaloneConfiguration", a.Configuration)
		}
		if len(cfg.EncryptedPaths) != 2 || cfg.EncryptedPaths[0] != "password" || cfg.EncryptedPaths[1] != "apiKey" {
			t.Errorf("encrypted_paths = %v, want [password apiKey]", cfg.EncryptedPaths)
		}
	})

	t.Run("encrypted_paths round-trip from from_file", func(t *testing.T) {
		tmpDir := t.TempDir()
		valuesPath := filepath.Join(tmpDir, "values", "sealed.yaml")
		if err := os.MkdirAll(filepath.Dir(valuesPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(valuesPath, []byte("password: ENC[...]"), 0644); err != nil {
			t.Fatal(err)
		}
		am := map[string]interface{}{
			"name":          "sealed-file",
			"chart_name":    "test",
			"chart_version": "1.0.0",
			"configuration": map[string]interface{}{
				"from_file":       "values/sealed.yaml",
				"encrypted_paths": []interface{}{"password"},
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
		if len(cfg.EncryptedPaths) != 1 || cfg.EncryptedPaths[0] != "password" {
			t.Errorf("encrypted_paths = %v, want [password]", cfg.EncryptedPaths)
		}
	})

	t.Run("encrypted_paths with nothing to decrypt errors", func(t *testing.T) {
		am := map[string]interface{}{
			"name":          "sealed-empty",
			"chart_name":    "test",
			"chart_version": "1.0.0",
			"configuration": map[string]interface{}{
				"encrypted_paths": []interface{}{"password"},
			},
		}
		_, err := buildAddon(am, "")
		if err == nil {
			t.Fatal("expected error for encrypted_paths with no configuration")
		}
		if !strings.Contains(err.Error(), "encrypted_paths") {
			t.Errorf("expected encrypted_paths error, got: %v", err)
		}
	})

	t.Run("missing chart_version errors", func(t *testing.T) {
		am := map[string]interface{}{
			"name":       "no-version",
			"chart_name": "test",
		}
		_, err := buildAddon(am, "")
		if err == nil {
			t.Fatal("expected error for missing chart_version")
		}
		if !strings.Contains(err.Error(), "chart_version is required") {
			t.Errorf("expected chart_version-required error, got: %v", err)
		}
	})

	t.Run("missing chart_name errors", func(t *testing.T) {
		am := map[string]interface{}{
			"name":          "no-chart",
			"chart_version": "1.0.0",
		}
		_, err := buildAddon(am, "")
		if err == nil {
			t.Fatal("expected error for missing chart_name")
		}
		if !strings.Contains(err.Error(), "chart_name is required") {
			t.Errorf("expected chart_name-required error, got: %v", err)
		}
	})

	t.Run("unquoted float chart_version errors with quote hint", func(t *testing.T) {
		am := map[string]interface{}{
			"name":          "float-version",
			"chart_name":    "test",
			"chart_version": 1.20, // YAML parses unquoted 1.20 as float64(1.2)
		}
		_, err := buildAddon(am, "")
		if err == nil {
			t.Fatal("expected error for unquoted float chart_version")
		}
		if !strings.Contains(err.Error(), "quoted string") {
			t.Errorf("expected quote-hint error, got: %v", err)
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
		if s.DeployWave != nil {
			t.Errorf("deploy wave = %v, want nil for a stack without deploy_wave", *s.DeployWave)
		}
	})

	t.Run("deploy_wave is parsed", func(t *testing.T) {
		sm := map[string]interface{}{
			"name":        "waved",
			"deploy_wave": 2,
		}
		s, err := buildStack(sm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.DeployWave == nil || *s.DeployWave != 2 {
			t.Errorf("deploy wave = %v, want 2", s.DeployWave)
		}
	})

	t.Run("deploy_wave rejects negatives and fractions", func(t *testing.T) {
		if _, err := buildStack(map[string]interface{}{"name": "w", "deploy_wave": -1}, ""); err == nil {
			t.Error("expected error for a negative deploy_wave")
		}
		if _, err := buildStack(map[string]interface{}{"name": "w", "deploy_wave": 1.5}, ""); err == nil {
			t.Error("expected error for a fractional deploy_wave")
		}
		if _, err := buildStack(map[string]interface{}{"name": "w", "deploy_wave": "first"}, ""); err == nil {
			t.Error("expected error for a non-numeric deploy_wave")
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

	t.Run("description_from_file is read", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "desc.txt"), []byte("a longer description"), 0644); err != nil {
			t.Fatal(err)
		}
		sm := map[string]interface{}{
			"name":                  "documented",
			"description_from_file": "desc.txt",
		}
		s, err := buildStack(sm, tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Description != "a longer description" {
			t.Errorf("description = %q, want %q", s.Description, "a longer description")
		}
	})

	t.Run("missing description_from_file is validated even with inline description", func(t *testing.T) {
		sm := map[string]interface{}{
			"name":                  "documented",
			"description":           "inline wins",
			"description_from_file": "does-not-exist.txt",
		}
		_, err := buildStack(sm, t.TempDir())
		if err == nil {
			t.Fatal("expected error for missing description_from_file")
		}
		if !strings.Contains(err.Error(), "description_from_file") {
			t.Errorf("expected description_from_file error, got: %v", err)
		}
	})

	t.Run("inline description wins over file content", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "desc.txt"), []byte("from file"), 0644); err != nil {
			t.Fatal(err)
		}
		sm := map[string]interface{}{
			"name":                  "documented",
			"description":           "inline wins",
			"description_from_file": "desc.txt",
		}
		s, err := buildStack(sm, tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Description != "inline wins" {
			t.Errorf("description = %q, want %q", s.Description, "inline wins")
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

func manifestWithParents(name string, parents ...client.Parent) client.Manifest {
	return client.Manifest{Name: name, Parents: parents}
}

func addonWithParents(name string, parents ...client.Parent) client.Addon {
	return client.Addon{Name: name, Parents: parents}
}

func TestValidateResourceGraph(t *testing.T) {
	manifestParent := func(name string) client.Parent {
		return client.Parent{Name: name, Kind: client.AnkraResourceKind("manifest")}
	}
	addonParent := func(name string) client.Parent {
		return client.Parent{Name: name, Kind: client.AnkraResourceKind("addon")}
	}

	t.Run("valid acyclic graph with cross-stack parent", func(t *testing.T) {
		request := client.CreateImportClusterRequest{
			Spec: client.CreateResourceSpec{
				Stacks: []client.Stack{
					{
						Name:      "base",
						Manifests: []client.Manifest{manifestWithParents("namespace")},
					},
					{
						Name:   "apps",
						Addons: []client.Addon{addonWithParents("cert-manager", manifestParent("namespace"))},
					},
				},
			},
		}
		if err := validateResourceGraph(request); err != nil {
			t.Errorf("expected valid graph, got error: %v", err)
		}
	})

	t.Run("parent does not exist", func(t *testing.T) {
		request := client.CreateImportClusterRequest{
			Spec: client.CreateResourceSpec{
				Stacks: []client.Stack{
					{
						Name:   "apps",
						Addons: []client.Addon{addonWithParents("cert-manager", manifestParent("missing-namespace"))},
					},
				},
			},
		}
		err := validateResourceGraph(request)
		if err == nil {
			t.Fatal("expected error for missing parent")
		}
		if !strings.Contains(err.Error(), "missing-namespace") {
			t.Errorf("expected error to name the missing parent, got: %v", err)
		}
	})

	t.Run("invalid parent kind", func(t *testing.T) {
		request := client.CreateImportClusterRequest{
			Spec: client.CreateResourceSpec{
				Stacks: []client.Stack{
					{
						Name:   "apps",
						Addons: []client.Addon{addonWithParents("cert-manager", client.Parent{Name: "x", Kind: client.AnkraResourceKind("stack")})},
					},
				},
			},
		}
		err := validateResourceGraph(request)
		if err == nil {
			t.Fatal("expected error for invalid parent kind")
		}
		if !strings.Contains(err.Error(), "invalid kind") {
			t.Errorf("expected invalid-kind error, got: %v", err)
		}
	})

	t.Run("dependency cycle", func(t *testing.T) {
		request := client.CreateImportClusterRequest{
			Spec: client.CreateResourceSpec{
				Stacks: []client.Stack{
					{
						Name: "apps",
						Addons: []client.Addon{
							addonWithParents("a", addonParent("b")),
							addonWithParents("b", addonParent("a")),
						},
					},
				},
			},
		}
		err := validateResourceGraph(request)
		if err == nil {
			t.Fatal("expected error for dependency cycle")
		}
		if !strings.Contains(err.Error(), "cycle") {
			t.Errorf("expected cycle error, got: %v", err)
		}
	})

	t.Run("self dependency is a cycle", func(t *testing.T) {
		request := client.CreateImportClusterRequest{
			Spec: client.CreateResourceSpec{
				Stacks: []client.Stack{
					{
						Name:   "apps",
						Addons: []client.Addon{addonWithParents("a", addonParent("a"))},
					},
				},
			},
		}
		if err := validateResourceGraph(request); err == nil {
			t.Fatal("expected error for self-referential dependency")
		}
	})

	t.Run("duplicate resource name across stacks is rejected", func(t *testing.T) {
		request := client.CreateImportClusterRequest{
			Spec: client.CreateResourceSpec{
				Stacks: []client.Stack{
					{
						Name:      "base",
						Manifests: []client.Manifest{manifestWithParents("namespace")},
					},
					{
						Name:      "apps",
						Manifests: []client.Manifest{manifestWithParents("namespace")},
					},
				},
			},
		}
		err := validateResourceGraph(request)
		if err == nil {
			t.Fatal("expected error for duplicate resource name")
		}
		if !strings.Contains(err.Error(), "more than once") {
			t.Errorf("expected duplicate-name error, got: %v", err)
		}
	})

	t.Run("same name different kind is allowed", func(t *testing.T) {
		request := client.CreateImportClusterRequest{
			Spec: client.CreateResourceSpec{
				Stacks: []client.Stack{
					{
						Name:      "base",
						Manifests: []client.Manifest{manifestWithParents("shared")},
						Addons:    []client.Addon{addonWithParents("shared")},
					},
				},
			},
		}
		if err := validateResourceGraph(request); err != nil {
			t.Errorf("expected manifest and addon to share a name, got error: %v", err)
		}
	})

	t.Run("no parents is valid", func(t *testing.T) {
		request := client.CreateImportClusterRequest{
			Spec: client.CreateResourceSpec{
				Stacks: []client.Stack{
					{
						Name:      "base",
						Manifests: []client.Manifest{manifestWithParents("namespace")},
						Addons:    []client.Addon{addonWithParents("cert-manager")},
					},
				},
			},
		}
		if err := validateResourceGraph(request); err != nil {
			t.Errorf("expected valid graph, got error: %v", err)
		}
	})
}
