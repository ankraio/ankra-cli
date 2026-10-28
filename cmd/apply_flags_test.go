package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// ankra-cbktk (PLA-834): the manifests[] 'force' and 'auto_remediate' keys
// were never read, so a file carrying force: true applied with no force key
// in the payload and the platform stored false over the flag a GitOps PR
// had just set. The first fix sent the flags as plain booleans, false when
// the file did not mention them - but the platform (cluster#2843) inherits
// the stored flag when the key is absent from the request and takes the
// sent value, true OR false, when it is present, so a routine apply of a
// file that never spelled out `force` cleared a `force: true` someone had
// set through Git or the portal (ankra-mp2tr). The ruling: omitted in the
// file is omitted on the wire, and an explicit false in the file still
// clears.
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
		if m.Force == nil || !*m.Force || m.AutoRemediate == nil || !*m.AutoRemediate {
			t.Fatalf("force = %v, auto_remediate = %v, want both pointers to true", m.Force, m.AutoRemediate)
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

	t.Run("absent flags are omitted on the wire", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":     "plain",
			"manifest": "apiVersion: v1\nkind: Namespace",
		}
		m, err := buildManifest(mm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Force != nil || m.AutoRemediate != nil {
			t.Fatalf("force = %v, auto_remediate = %v, want both nil for a file without the keys", m.Force, m.AutoRemediate)
		}
		payload, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// The platform inherits the stored flag when the key is absent
		// (cluster#2843), so a file that does not mention the flag must not
		// put it on the wire at all: `"force":false` here would clear a
		// force: true set through Git or the portal on every routine apply
		// (ankra-mp2tr).
		for _, unwanted := range []string{`"force"`, `"auto_remediate"`} {
			if strings.Contains(string(payload), unwanted) {
				t.Errorf("payload %s carries %s for a file that never mentioned it", payload, unwanted)
			}
		}
	})

	t.Run("an explicit false is sent as false", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":           "cleared",
			"manifest":       "apiVersion: v1\nkind: Namespace",
			"force":          false,
			"auto_remediate": false,
		}
		m, err := buildManifest(mm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Force == nil || *m.Force || m.AutoRemediate == nil || *m.AutoRemediate {
			t.Fatalf("force = %v, auto_remediate = %v, want both pointers to false", m.Force, m.AutoRemediate)
		}
		payload, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// An explicit false is the one way a file clears a stored flag, so
		// omitempty must not swallow it: the platform only honours a clear
		// it can see.
		for _, want := range []string{`"force":false`, `"auto_remediate":false`} {
			if !strings.Contains(string(payload), want) {
				t.Errorf("payload %s lacks %s", payload, want)
			}
		}
	})

	t.Run("explicit null reads as absent", func(t *testing.T) {
		mm := map[string]interface{}{
			"name":     "nulled",
			"manifest": "apiVersion: v1\nkind: Namespace",
			"force":    nil,
		}
		m, err := buildManifest(mm, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 'force:' with no value is not a decision either way; the platform
		// treats a sent null like an absent key, and so does the CLI.
		if m.Force != nil {
			t.Fatalf("force = %v, want nil for 'force:' with no value", *m.Force)
		}
		payload, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(payload), `"force"`) {
			t.Errorf("payload %s carries force for 'force:' with no value", payload)
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
		// "true" and true print alike; the message has to say it was a string.
		if !strings.Contains(err.Error(), "of type string") {
			t.Errorf("error %q does not say the value was a string", err.Error())
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
		if !strings.Contains(err.Error(), "of type int") {
			t.Errorf("error %q does not say the value was an int", err.Error())
		}
	})
}
