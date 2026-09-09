package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// ankra-cbktk (PLA-834): the manifests[] 'force' and 'auto_remediate' keys
// were never read, so a file carrying force: true applied with no force key
// in the payload and the platform stored false over the flag a GitOps PR
// had just set. The flags are read as booleans and always sent, so the file
// is authoritative either way.
func TestBuildManifestApplyFlags(t *testing.T) {
	t.Run("force and auto_remediate pass through and are sent", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":           "strimzi-crd-kafkas",
			"manifest":       "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition",
			"force":          true,
			"auto_remediate": true,
		}
		m, err := buildManifest(mm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !m.Force || !m.AutoRemediate {
			t.Fatalf("force = %v, auto_remediate = %v, want both true", m.Force, m.AutoRemediate)
		}
		payload, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, want := range []string{`"force":true`, `"auto_remediate":true`} {
			if !strings.Contains(string(payload), want) {
				t.Errorf("payload %s lacks %s", payload, want)
			}
		}
	})

	t.Run("absent flags are false and still sent", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":     "plain",
			"manifest": "apiVersion: v1\nkind: Namespace",
		}
		m, err := buildManifest(mm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Force || m.AutoRemediate {
			t.Fatalf("force = %v, auto_remediate = %v, want both false for a file without the keys", m.Force, m.AutoRemediate)
		}
		payload, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// Declarative: the key is on the wire as false, never omitted, so an
		// apply of a file that dropped force: true clears it on the platform
		// the way the GitOps cluster file would.
		for _, want := range []string{`"force":false`, `"auto_remediate":false`} {
			if !strings.Contains(string(payload), want) {
				t.Errorf("payload %s lacks %s", payload, want)
			}
		}
	})

	t.Run("explicit null reads as false", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":     "nulled",
			"manifest": "apiVersion: v1\nkind: Namespace",
			"force":    nil,
		}
		m, err := buildManifest(mm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Force {
			t.Fatal("force = true, want false for 'force:' with no value")
		}
	})

	t.Run("a quoted force is rejected, not read as off", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":     "quoted",
			"manifest": "apiVersion: v1\nkind: Namespace",
			"force":    "true",
		}
		_, err := buildManifest(mm, "")
		if err == nil {
			t.Fatal("expected an error for force: \"true\"")
		}
		if !strings.Contains(err.Error(), "'force'") {
			t.Errorf("error %q does not name the 'force' key", err.Error())
		}
	})

	t.Run("a non-boolean auto_remediate is rejected", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":           "numeric",
			"manifest":       "apiVersion: v1\nkind: Namespace",
			"auto_remediate": 1,
		}
		_, err := buildManifest(mm, "")
		if err == nil {
			t.Fatal("expected an error for auto_remediate: 1")
		}
		if !strings.Contains(err.Error(), "'auto_remediate'") {
			t.Errorf("error %q does not name the 'auto_remediate' key", err.Error())
		}
	})
}
