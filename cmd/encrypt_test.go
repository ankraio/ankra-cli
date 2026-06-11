package cmd

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEncryptManifestResolution(t *testing.T) {
	clusterYAML := `kind: ImportCluster
metadata:
  name: test-cluster
spec:
  stacks:
    - name: monitoring
      manifests:
        - name: db-secret
          from_file: manifests/secret.yaml
        - name: namespace
          from_file: manifests/ns.yaml
      addons:
        - name: prometheus
          chart_name: kube-prometheus-stack
          chart_version: "56.0.0"
    - name: networking
      manifests:
        - name: ingress-config
          from_file: manifests/ingress.yaml`

	var cluster ImportClusterConfig
	if err := yaml.Unmarshal([]byte(clusterYAML), &cluster); err != nil {
		t.Fatalf("failed to parse cluster YAML: %v", err)
	}

	if cluster.Kind != "ImportCluster" {
		t.Errorf("kind = %q, want %q", cluster.Kind, "ImportCluster")
	}

	t.Run("find manifest in first stack", func(t *testing.T) {
		var found *ManifestConfig
		for stackIdx := range cluster.Spec.Stacks {
			for manifestIdx := range cluster.Spec.Stacks[stackIdx].Manifests {
				if cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx].Name == "db-secret" {
					found = &cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx]
					break
				}
			}
			if found != nil {
				break
			}
		}
		if found == nil {
			t.Fatal("expected to find manifest 'db-secret'")
		} else if found.FromFile != "manifests/secret.yaml" {
			t.Errorf("from_file = %q, want %q", found.FromFile, "manifests/secret.yaml")
		}
	})

	t.Run("find manifest in second stack", func(t *testing.T) {
		var found *ManifestConfig
		for stackIdx := range cluster.Spec.Stacks {
			for manifestIdx := range cluster.Spec.Stacks[stackIdx].Manifests {
				if cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx].Name == "ingress-config" {
					found = &cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx]
					break
				}
			}
			if found != nil {
				break
			}
		}
		if found == nil {
			t.Fatal("expected to find manifest 'ingress-config'")
		}
	})

	t.Run("manifest not found", func(t *testing.T) {
		var found *ManifestConfig
		for stackIdx := range cluster.Spec.Stacks {
			for manifestIdx := range cluster.Spec.Stacks[stackIdx].Manifests {
				if cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx].Name == "nonexistent" {
					found = &cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx]
					break
				}
			}
		}
		if found != nil {
			t.Error("expected manifest to not be found")
		}
	})
}

func TestEncryptAddonResolution(t *testing.T) {
	clusterYAML := `kind: ImportCluster
metadata:
  name: test-cluster
spec:
  stacks:
    - name: monitoring
      addons:
        - name: grafana
          chart_name: grafana
          chart_version: "7.0.0"
          configuration:
            from_file: values/grafana.yaml
        - name: prometheus
          chart_name: kube-prometheus-stack
          chart_version: "56.0.0"`

	var cluster ImportClusterConfig
	if err := yaml.Unmarshal([]byte(clusterYAML), &cluster); err != nil {
		t.Fatalf("failed to parse cluster YAML: %v", err)
	}

	t.Run("find addon with from_file", func(t *testing.T) {
		var found *AddonConfig
		for stackIdx := range cluster.Spec.Stacks {
			for addonIdx := range cluster.Spec.Stacks[stackIdx].Addons {
				if cluster.Spec.Stacks[stackIdx].Addons[addonIdx].Name == "grafana" {
					found = &cluster.Spec.Stacks[stackIdx].Addons[addonIdx]
					break
				}
			}
			if found != nil {
				break
			}
		}
		if found == nil {
			t.Fatal("expected to find addon 'grafana'")
		} else {
			if found.Configuration == nil {
				t.Fatal("expected configuration to be present")
			}
			fromFile, ok := found.Configuration["from_file"].(string)
			if !ok || fromFile == "" {
				t.Error("expected from_file to be set in configuration")
			}
		}
	})

	t.Run("addon without configuration", func(t *testing.T) {
		var found *AddonConfig
		for stackIdx := range cluster.Spec.Stacks {
			for addonIdx := range cluster.Spec.Stacks[stackIdx].Addons {
				if cluster.Spec.Stacks[stackIdx].Addons[addonIdx].Name == "prometheus" {
					found = &cluster.Spec.Stacks[stackIdx].Addons[addonIdx]
					break
				}
			}
		}
		if found == nil {
			t.Fatal("expected to find addon 'prometheus'")
		} else if len(found.Configuration) > 0 {
			fromFile, ok := found.Configuration["from_file"].(string)
			if ok && fromFile != "" {
				t.Error("expected prometheus to not have from_file")
			}
		}
	})
}

func TestContainsString(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		str      string
		expected bool
	}{
		{"found", []string{"a", "b", "c"}, "b", true},
		{"not found", []string{"a", "b", "c"}, "d", false},
		{"empty slice", []string{}, "a", false},
		{"nil slice", nil, "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsString(tt.slice, tt.str)
			if result != tt.expected {
				t.Errorf("containsString(%v, %q) = %v, want %v", tt.slice, tt.str, result, tt.expected)
			}
		})
	}
}

func TestNormalizeEncryptKey(t *testing.T) {
	tests := []struct {
		name      string
		rawKey    string
		expected  string
		expectErr bool
	}{
		{"plain key", "password", "password", false},
		{"dotted path uses last segment", "data.password", "password", false},
		{"deeply dotted path", "spec.template.secret.apiKey", "apiKey", false},
		{"surrounding whitespace trimmed", "  password  ", "password", false},
		{"empty key", "", "", true},
		{"whitespace-only key", "   ", "", true},
		{"trailing dot", "data.", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := normalizeEncryptKey(tt.rawKey)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("normalizeEncryptKey(%q) expected error, got %q", tt.rawKey, result)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeEncryptKey(%q) unexpected error: %v", tt.rawKey, err)
			}
			if result != tt.expected {
				t.Errorf("normalizeEncryptKey(%q) = %q, want %q", tt.rawKey, result, tt.expected)
			}
		})
	}
}

func TestVerifyKeyEncrypted(t *testing.T) {
	encryptedSecret := `apiVersion: v1
kind: Secret
data:
  username: YWRtaW4=
  password: ENC[AES256_GCM,data:abc,iv:def,tag:ghi,type:str]
sops:
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(password)$
`

	plaintextWithSopsMetadata := `apiVersion: v1
kind: Secret
data:
  username: YWRtaW4=
  password: aHVudGVyMg==
sops:
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(data.password)$
`

	t.Run("encrypted value passes", func(t *testing.T) {
		if err := verifyKeyEncrypted(encryptedSecret, "password"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("plaintext value under sops metadata fails", func(t *testing.T) {
		err := verifyKeyEncrypted(plaintextWithSopsMetadata, "password")
		if err == nil {
			t.Fatal("expected error for plaintext value")
		}
		if !strings.Contains(err.Error(), "still plaintext") {
			t.Errorf("expected plaintext error, got: %v", err)
		}
	})

	t.Run("missing key fails", func(t *testing.T) {
		err := verifyKeyEncrypted(encryptedSecret, "token")
		if err == nil {
			t.Fatal("expected error for missing key")
		}
		if !strings.Contains(err.Error(), "SOPS encrypted nothing") {
			t.Errorf("expected missing-key error, got: %v", err)
		}
	})

	t.Run("sops metadata key does not count as a match", func(t *testing.T) {
		if err := verifyKeyEncrypted(encryptedSecret, "mac"); err == nil {
			t.Error("expected error: 'mac' only exists inside the sops metadata block")
		}
	})

	t.Run("nested mapping under matched key must be fully encrypted", func(t *testing.T) {
		content := `credentials:
  username: ENC[AES256_GCM,data:abc]
  password: plain
`
		if err := verifyKeyEncrypted(content, "credentials"); err == nil {
			t.Error("expected error for partially encrypted subtree")
		}
	})

	t.Run("multi-document YAML finds the key in any document", func(t *testing.T) {
		content := `kind: Namespace
metadata:
  name: web
---
kind: Secret
data:
  password: ENC[AES256_GCM,data:abc]
`
		if err := verifyKeyEncrypted(content, "password"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("leading fingerprint comment is tolerated", func(t *testing.T) {
		content := "# ankra_content_fingerprint: abc123\ndata:\n  password: ENC[AES256_GCM,data:abc]\n"
		if err := verifyKeyEncrypted(content, "password"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestGetEncryptedPathsFromConfig(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		result := getEncryptedPathsFromConfig(nil)
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("config without encrypted_paths", func(t *testing.T) {
		config := map[string]interface{}{
			"from_file": "values.yaml",
		}
		result := getEncryptedPathsFromConfig(config)
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("config with string slice encrypted_paths", func(t *testing.T) {
		config := map[string]interface{}{
			"encrypted_paths": []string{"data.password", "data.token"},
		}
		result := getEncryptedPathsFromConfig(config)
		if len(result) != 2 {
			t.Errorf("expected 2 paths, got %d", len(result))
		}
	})

	t.Run("config with interface slice encrypted_paths", func(t *testing.T) {
		config := map[string]interface{}{
			"encrypted_paths": []interface{}{"data.password", "data.token"},
		}
		result := getEncryptedPathsFromConfig(config)
		if len(result) != 2 {
			t.Errorf("expected 2 paths, got %d", len(result))
		}
	})
}
