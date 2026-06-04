package cmd

import (
	"testing"
)

func TestParseLabelsFlag(t *testing.T) {
	t.Run("empty clears labels", func(t *testing.T) {
		labels, err := parseLabelsFlag("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(labels) != 0 {
			t.Fatalf("expected empty map, got %v", labels)
		}
	})

	t.Run("parses multiple pairs and trims whitespace", func(t *testing.T) {
		labels, err := parseLabelsFlag(" workload=realtime , tier=gold ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if labels["workload"] != "realtime" || labels["tier"] != "gold" {
			t.Fatalf("unexpected labels: %v", labels)
		}
	})

	t.Run("allows empty value", func(t *testing.T) {
		labels, err := parseLabelsFlag("drain=")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if value, ok := labels["drain"]; !ok || value != "" {
			t.Fatalf("expected drain to map to empty string, got %v", labels)
		}
	})

	t.Run("rejects pair without key", func(t *testing.T) {
		if _, err := parseLabelsFlag("=oops"); err == nil {
			t.Fatal("expected error for missing key")
		}
	})

	t.Run("rejects pair without equals", func(t *testing.T) {
		if _, err := parseLabelsFlag("novalue"); err == nil {
			t.Fatal("expected error for missing '='")
		}
	})
}

func TestParseTaintsFlag(t *testing.T) {
	t.Run("empty clears taints", func(t *testing.T) {
		taints, err := parseTaintsFlag("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(taints) != 0 {
			t.Fatalf("expected empty slice, got %v", taints)
		}
	})

	t.Run("parses key=value:Effect", func(t *testing.T) {
		taints, err := parseTaintsFlag("dedicated=realtime:NoExecute")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(taints) != 1 || taints[0].Key != "dedicated" || taints[0].Value != "realtime" || taints[0].Effect != "NoExecute" {
			t.Fatalf("unexpected taint: %+v", taints)
		}
	})

	t.Run("defaults effect to NoSchedule", func(t *testing.T) {
		taints, err := parseTaintsFlag("key=value")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if taints[0].Effect != "NoSchedule" {
			t.Fatalf("expected default NoSchedule, got %q", taints[0].Effect)
		}
	})

	t.Run("allows key-only with effect", func(t *testing.T) {
		taints, err := parseTaintsFlag("special:NoSchedule")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if taints[0].Key != "special" || taints[0].Value != "" || taints[0].Effect != "NoSchedule" {
			t.Fatalf("unexpected taint: %+v", taints)
		}
	})

	t.Run("rejects taint without key", func(t *testing.T) {
		if _, err := parseTaintsFlag("=value:NoSchedule"); err == nil {
			t.Fatal("expected error for missing key")
		}
	})
}
