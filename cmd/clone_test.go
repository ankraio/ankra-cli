package cmd

import (
	"testing"
)

func TestIsURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"http url", "http://example.com/cluster.yaml", true},
		{"https url", "https://github.com/user/repo/raw/main/cluster.yaml", true},
		{"local file path", "./cluster.yaml", false},
		{"absolute file path", "/home/user/cluster.yaml", false},
		{"relative path", "configs/cluster.yaml", false},
		{"empty string", "", false},
		{"ftp url", "ftp://example.com/file", false},
		{"just a name", "cluster.yaml", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isURL(tt.input)
			if result != tt.expected {
				t.Errorf("isURL(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGenerateNewClusterName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"name with -cluster suffix", "prod-cluster", "prod-cloned-cluster"},
		{"name without -cluster", "my-app", "my-app-cloned"},
		{"name with cluster in middle", "my-cluster-v2", "my-cloned-cluster-v2"},
		{"simple name", "test", "test-cloned"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateNewClusterName(tt.input)
			if result != tt.expected {
				t.Errorf("generateNewClusterName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetStackNames(t *testing.T) {
	tests := []struct {
		name     string
		stacks   []StackConfig
		expected map[string]bool
	}{
		{
			name:     "empty slice",
			stacks:   []StackConfig{},
			expected: map[string]bool{},
		},
		{
			name: "single stack",
			stacks: []StackConfig{
				{Name: "monitoring"},
			},
			expected: map[string]bool{"monitoring": true},
		},
		{
			name: "multiple stacks",
			stacks: []StackConfig{
				{Name: "monitoring"},
				{Name: "networking"},
				{Name: "storage"},
			},
			expected: map[string]bool{"monitoring": true, "networking": true, "storage": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStackNames(tt.stacks)
			if len(result) != len(tt.expected) {
				t.Errorf("getStackNames() returned %d entries, want %d", len(result), len(tt.expected))
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("getStackNames() missing key %q", k)
				}
			}
		})
	}
}

func TestCheckStackConflicts(t *testing.T) {
	tests := []struct {
		name               string
		newStack           StackConfig
		existingStacks     []StackConfig
		expectManifests    int
		expectAddons       int
	}{
		{
			name: "no conflicts",
			newStack: StackConfig{
				Manifests: []ManifestConfig{{Name: "new-manifest"}},
				Addons:    []AddonConfig{{Name: "new-addon"}},
			},
			existingStacks: []StackConfig{
				{
					Manifests: []ManifestConfig{{Name: "existing-manifest"}},
					Addons:    []AddonConfig{{Name: "existing-addon"}},
				},
			},
			expectManifests: 0,
			expectAddons:    0,
		},
		{
			name: "manifest conflict",
			newStack: StackConfig{
				Manifests: []ManifestConfig{{Name: "shared-manifest"}},
			},
			existingStacks: []StackConfig{
				{
					Manifests: []ManifestConfig{{Name: "shared-manifest"}},
				},
			},
			expectManifests: 1,
			expectAddons:    0,
		},
		{
			name: "addon conflict",
			newStack: StackConfig{
				Addons: []AddonConfig{{Name: "cert-manager"}},
			},
			existingStacks: []StackConfig{
				{
					Addons: []AddonConfig{{Name: "cert-manager"}},
				},
			},
			expectManifests: 0,
			expectAddons:    1,
		},
		{
			name: "both conflicts",
			newStack: StackConfig{
				Manifests: []ManifestConfig{{Name: "ns"}},
				Addons:    []AddonConfig{{Name: "ingress"}},
			},
			existingStacks: []StackConfig{
				{
					Manifests: []ManifestConfig{{Name: "ns"}, {Name: "other"}},
					Addons:    []AddonConfig{{Name: "ingress"}},
				},
			},
			expectManifests: 1,
			expectAddons:    1,
		},
		{
			name: "no existing stacks",
			newStack: StackConfig{
				Manifests: []ManifestConfig{{Name: "any"}},
				Addons:    []AddonConfig{{Name: "any"}},
			},
			existingStacks:  []StackConfig{},
			expectManifests: 0,
			expectAddons:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifestConflicts, addonConflicts := checkStackConflicts(tt.newStack, tt.existingStacks)
			if len(manifestConflicts) != tt.expectManifests {
				t.Errorf("manifest conflicts = %d, want %d", len(manifestConflicts), tt.expectManifests)
			}
			if len(addonConflicts) != tt.expectAddons {
				t.Errorf("addon conflicts = %d, want %d", len(addonConflicts), tt.expectAddons)
			}
		})
	}
}
