package cmd

import (
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
		}
		if found.FromFile != "manifests/secret.yaml" {
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
          configuration_type: standalone
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
		}
		if found.Configuration == nil {
			t.Fatal("expected configuration to be present")
		}
		fromFile, ok := found.Configuration["from_file"].(string)
		if !ok || fromFile == "" {
			t.Error("expected from_file to be set in configuration")
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
		}
		if found.Configuration != nil && len(found.Configuration) > 0 {
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
