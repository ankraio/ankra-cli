package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCloneStacks_CleanFlag(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	dstDir := filepath.Join(tmpDir, "dst")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatal(err)
	}

	existing := &ImportClusterConfig{
		Spec: ClusterSpec{
			Stacks: []StackConfig{
				{Name: "monitoring"},
				{Name: "networking"},
			},
		},
	}
	target := &ImportClusterConfig{
		Spec: ClusterSpec{
			Stacks: []StackConfig{
				{Name: "old-stack"},
			},
		},
	}

	originalClean := cleanFlag
	cleanFlag = true
	t.Cleanup(func() { cleanFlag = originalClean })

	originalForce := forceFlag
	forceFlag = false
	t.Cleanup(func() { forceFlag = originalForce })

	originalStack := stackFlag
	stackFlag = []string{}
	t.Cleanup(func() { stackFlag = originalStack })

	if err := cloneStacks(existing, target, srcDir, dstDir, false); err != nil {
		t.Fatalf("cloneStacks failed: %v", err)
	}

	if len(target.Spec.Stacks) != 2 {
		t.Errorf("expected 2 stacks after clean clone, got %d", len(target.Spec.Stacks))
	}
}

func TestCloneStacks_SkipConflictingNames(t *testing.T) {
	tmpDir := t.TempDir()

	existing := &ImportClusterConfig{
		Spec: ClusterSpec{
			Stacks: []StackConfig{
				{Name: "monitoring"},
				{Name: "new-stack"},
			},
		},
	}
	target := &ImportClusterConfig{
		Spec: ClusterSpec{
			Stacks: []StackConfig{
				{Name: "monitoring"},
			},
		},
	}

	originalClean := cleanFlag
	cleanFlag = false
	t.Cleanup(func() { cleanFlag = originalClean })

	originalForce := forceFlag
	forceFlag = false
	t.Cleanup(func() { forceFlag = originalForce })

	originalStack := stackFlag
	stackFlag = []string{}
	t.Cleanup(func() { stackFlag = originalStack })

	originalCopyMissing := copyMissingFlag
	copyMissingFlag = false
	t.Cleanup(func() { copyMissingFlag = originalCopyMissing })

	if err := cloneStacks(existing, target, tmpDir, tmpDir, false); err != nil {
		t.Fatalf("cloneStacks failed: %v", err)
	}

	if len(target.Spec.Stacks) != 2 {
		t.Errorf("expected 2 stacks (1 original + 1 new), got %d", len(target.Spec.Stacks))
	}
}

func TestCloneStacks_ForceOverride(t *testing.T) {
	tmpDir := t.TempDir()

	existing := &ImportClusterConfig{
		Spec: ClusterSpec{
			Stacks: []StackConfig{
				{Name: "monitoring", Description: "updated"},
			},
		},
	}
	target := &ImportClusterConfig{
		Spec: ClusterSpec{
			Stacks: []StackConfig{
				{Name: "monitoring", Description: "original"},
			},
		},
	}

	originalClean := cleanFlag
	cleanFlag = false
	t.Cleanup(func() { cleanFlag = originalClean })

	originalForce := forceFlag
	forceFlag = true
	t.Cleanup(func() { forceFlag = originalForce })

	originalStack := stackFlag
	stackFlag = []string{}
	t.Cleanup(func() { stackFlag = originalStack })

	if err := cloneStacks(existing, target, tmpDir, tmpDir, false); err != nil {
		t.Fatalf("cloneStacks failed: %v", err)
	}

	if len(target.Spec.Stacks) != 1 {
		t.Errorf("expected 1 stack after force override, got %d", len(target.Spec.Stacks))
	}
	if target.Spec.Stacks[0].Description != "updated" {
		t.Errorf("expected description 'updated', got %q", target.Spec.Stacks[0].Description)
	}
}

func TestCloneStacks_StackFilter(t *testing.T) {
	tmpDir := t.TempDir()

	existing := &ImportClusterConfig{
		Spec: ClusterSpec{
			Stacks: []StackConfig{
				{Name: "monitoring"},
				{Name: "networking"},
				{Name: "storage"},
			},
		},
	}
	target := &ImportClusterConfig{
		Spec: ClusterSpec{
			Stacks: []StackConfig{},
		},
	}

	originalClean := cleanFlag
	cleanFlag = false
	t.Cleanup(func() { cleanFlag = originalClean })

	originalForce := forceFlag
	forceFlag = false
	t.Cleanup(func() { forceFlag = originalForce })

	originalStack := stackFlag
	stackFlag = []string{"monitoring", "storage"}
	t.Cleanup(func() { stackFlag = originalStack })

	if err := cloneStacks(existing, target, tmpDir, tmpDir, false); err != nil {
		t.Fatalf("cloneStacks failed: %v", err)
	}

	if len(target.Spec.Stacks) != 2 {
		t.Errorf("expected 2 stacks after filtering, got %d", len(target.Spec.Stacks))
	}
}

func TestCopyStackFiles_LocalFiles(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	manifestDir := filepath.Join(srcDir, "manifests")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "ns.yaml"), []byte("kind: Namespace"), 0644); err != nil {
		t.Fatal(err)
	}

	stack := StackConfig{
		Manifests: []ManifestConfig{
			{Name: "namespace", FromFile: "manifests/ns.yaml"},
		},
	}

	originalForce := forceFlag
	forceFlag = false
	t.Cleanup(func() { forceFlag = originalForce })

	if err := copyStackFiles(stack, srcDir, dstDir, false, false); err != nil {
		t.Fatalf("copyStackFiles failed: %v", err)
	}

	copiedPath := filepath.Join(dstDir, "manifests", "ns.yaml")
	content, err := os.ReadFile(copiedPath)
	if err != nil {
		t.Fatalf("expected file to be copied: %v", err)
	}
	if string(content) != "kind: Namespace" {
		t.Errorf("copied content = %q, want %q", string(content), "kind: Namespace")
	}
}

func TestWriteClusterFile_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "cluster.yaml")

	original := &ImportClusterConfig{
		APIVersion: "v1",
		Kind:       "ImportCluster",
		Metadata: ClusterMetadata{
			Name:        "test-cluster",
			Description: "A test cluster",
		},
		Spec: ClusterSpec{
			Stacks: []StackConfig{
				{
					Name: "monitoring",
					Manifests: []ManifestConfig{
						{Name: "namespace", FromFile: "manifests/ns.yaml"},
					},
					Addons: []AddonConfig{
						{Name: "prometheus", ChartName: "kube-prometheus-stack", ChartVersion: "56.0.0"},
					},
				},
			},
		},
	}

	if err := writeClusterFile(outputPath, original); err != nil {
		t.Fatalf("writeClusterFile failed: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	var roundTripped ImportClusterConfig
	if err := yaml.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("failed to parse written YAML: %v", err)
	}

	if roundTripped.Kind != "ImportCluster" {
		t.Errorf("kind = %q, want %q", roundTripped.Kind, "ImportCluster")
	}
	if roundTripped.Metadata.Name != "test-cluster" {
		t.Errorf("name = %q, want %q", roundTripped.Metadata.Name, "test-cluster")
	}
	if len(roundTripped.Spec.Stacks) != 1 {
		t.Errorf("stacks count = %d, want 1", len(roundTripped.Spec.Stacks))
	}
	if len(roundTripped.Spec.Stacks[0].Manifests) != 1 {
		t.Errorf("manifests count = %d, want 1", len(roundTripped.Spec.Stacks[0].Manifests))
	}
	if len(roundTripped.Spec.Stacks[0].Addons) != 1 {
		t.Errorf("addons count = %d, want 1", len(roundTripped.Spec.Stacks[0].Addons))
	}
}
