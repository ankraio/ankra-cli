package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGetClusterIaC_OK(t *testing.T) {
	src := `kind: ImportCluster
metadata:
  name: demo
spec:
  stacks: []
`
	encoded := base64.StdEncoding.EncodeToString([]byte(src))
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.Contains(r.URL.Path, "/iac") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, IacResponse{YamlStringBase64: encoded})
	})
	yaml, err := testClient.GetClusterIaC(context.Background(), "cluster-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if yaml != src {
		t.Errorf("got %q, want %q", yaml, src)
	}
}

func TestGetClusterIaC_EmptyClusterReturnsSentinel(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{
			"detail": "Unable to generate infrastructure as code. Please ensure the cluster has resources configured.",
		})
	})
	_, err := testClient.GetClusterIaC(context.Background(), "cluster-id")
	if err != ErrClusterEmpty {
		t.Errorf("expected ErrClusterEmpty, got %v", err)
	}
}

func TestGetClusterIaC_RouteNotFoundIsNotEmpty(t *testing.T) {
	// A bare 404 (no body, or a "Not Found" detail) must NOT be mapped to
	// ErrClusterEmpty: that would mask "route doesn't exist on this backend"
	// or "cluster not found" with a confusing "no resources" message.
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"detail": "Not Found"})
	})
	_, err := testClient.GetClusterIaC(context.Background(), "cluster-id")
	if err == nil {
		t.Fatal("expected error for 404 Not Found")
	}
	if err == ErrClusterEmpty {
		t.Errorf("404 with detail 'Not Found' must not map to ErrClusterEmpty; got %v", err)
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("expected error to surface detail message; got %v", err)
	}
}

func TestGetClusterIaC_NotFoundWithEmptyBody(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := testClient.GetClusterIaC(context.Background(), "cluster-id")
	if err == nil {
		t.Fatal("expected error for 404 with empty body")
	}
	if err == ErrClusterEmpty {
		t.Errorf("404 with empty body must not map to ErrClusterEmpty; got %v", err)
	}
}

func TestGetClusterIaC_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := testClient.GetClusterIaC(context.Background(), "cluster-id")
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if err == ErrClusterEmpty {
		t.Error("expected non-sentinel error for 500")
	}
}

// TestSpecAgentsMdWireSemantics pins the tri-state JSON encoding of the
// AGENTS.md fields on AddonSpec and ManifestSpec: nil pointers are omitted
// entirely (backend preserves the stored file), a pointer to "" is sent as an
// explicit empty string (backend clears the file), and content passes
// through verbatim.
func TestSpecAgentsMdWireSemantics(t *testing.T) {
	t.Run("nil is omitted", func(t *testing.T) {
		b, err := json.Marshal(AddonSpec{Name: "x", ChartName: "c", ChartVersion: "1"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(b), "agents_md") {
			t.Errorf("nil agents_md fields must be omitted, got %s", string(b))
		}
	})

	t.Run("empty string is an explicit clear", func(t *testing.T) {
		empty := ""
		b, err := json.Marshal(ManifestSpec{Name: "m", AgentsMd: &empty})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var generic map[string]any
		if err := json.Unmarshal(b, &generic); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		v, present := generic["agents_md"]
		if !present {
			t.Fatalf("explicit empty agents_md must be serialized, got %s", string(b))
		}
		if s, _ := v.(string); s != "" {
			t.Errorf("agents_md = %q, want empty string", s)
		}
	})

	t.Run("content and pointer round-trip", func(t *testing.T) {
		content := "# learnings\nplain markdown"
		path := "stacks/demo/add-ons/x/AGENTS.md"
		b, err := json.Marshal(AddonSpec{Name: "x", ChartName: "c", ChartVersion: "1", AgentsMd: &content, AgentsMdFromFile: &path})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var back AddonSpec
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if back.AgentsMd == nil || *back.AgentsMd != content {
			t.Errorf("agents_md round-trip = %v, want %q", back.AgentsMd, content)
		}
		if back.AgentsMdFromFile == nil || *back.AgentsMdFromFile != path {
			t.Errorf("agents_md_from_file round-trip = %v, want %q", back.AgentsMdFromFile, path)
		}
	})
}

func TestPatchClusterStackPartial_OK(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req PatchStackRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("invalid request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !req.PartialStack {
			t.Errorf("expected partial_stack=true, got false; body:\n%s", string(body))
		}
		if len(req.Spec.Stacks) != 1 {
			t.Errorf("expected exactly one stack, got %d", len(req.Spec.Stacks))
		}
		stack := req.Spec.Stacks[0]
		nAddons := len(stack.Addons)
		nManifests := len(stack.Manifests)
		// Exactly one of addons / manifests must contain one entry.
		hasAddon := nAddons == 1 && nManifests == 0
		hasManifest := nManifests == 1 && nAddons == 0
		if !hasAddon && !hasManifest {
			t.Errorf("expected exactly one addon OR one manifest, got %d addons, %d manifests", nAddons, nManifests)
		}
		jsonResponse(t, w, http.StatusOK, PatchStackResult{
			StackName: "demo",
			CommitSHA: "deadbeef",
			CommitURL: "https://github.com/o/r/commit/deadbeef",
			JobCount:  1,
		})
	})

	req := PatchStackRequest{
		PartialStack: true,
		Spec: ResourceSpecSpec{
			Stacks: []StackSpec{{
				Name:      "demo",
				Manifests: []ManifestSpec{},
				Addons:    []AddonSpec{{Name: "x", ChartName: "x", ChartVersion: "1"}},
			}},
		},
	}
	res, err := testClient.PatchClusterStackPartial(context.Background(), "cluster-id", "demo", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StackName != "demo" {
		t.Errorf("stack_name = %q, want demo", res.StackName)
	}
	if res.CommitSHA != "deadbeef" {
		t.Errorf("commit_sha = %q, want deadbeef", res.CommitSHA)
	}
}

func TestPatchClusterStackPartial_TypedErrorOnNon2xx(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"Stack is pending deletion"}`))
	})
	req := PatchStackRequest{
		PartialStack: true,
		Spec:         ResourceSpecSpec{Stacks: []StackSpec{{Name: "demo"}}},
	}
	_, err := testClient.PatchClusterStackPartial(context.Background(), "cluster-id", "demo", req)
	if err == nil {
		t.Fatal("expected error for 409 status")
	}
	perr, ok := err.(*PatchStackError)
	if !ok {
		t.Fatalf("expected *PatchStackError, got %T: %v", err, err)
	}
	if perr.StatusCode != http.StatusConflict {
		t.Errorf("StatusCode = %d, want 409", perr.StatusCode)
	}
	if !strings.Contains(string(perr.Body), "pending deletion") {
		t.Errorf("body = %s", string(perr.Body))
	}
}

func TestGetClusterAddonValues_DecodesBase64(t *testing.T) {
	rawYAML := "image:\n  tag: 1.0.0\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(rawYAML))
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/addons/website/configuration") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"result": map[string]any{
				"cluster_addon_configuration": map[string]any{
					"values": encoded,
				},
			},
		})
	})
	got, err := testClient.GetClusterAddonValues(context.Background(), "cluster-id", "website")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != rawYAML {
		t.Errorf("got %q, want %q", got, rawYAML)
	}
}

func TestGetClusterAddonValues_EmptyValues(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"result": map[string]any{
				"cluster_addon_configuration": map[string]any{"values": ""},
			},
		})
	})
	got, err := testClient.GetClusterAddonValues(context.Background(), "cluster-id", "website")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestGetClusterManifestConfiguration_ReturnsBase64(t *testing.T) {
	rawManifest := "apiVersion: v1\nkind: ConfigMap\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(rawManifest))
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/manifests/cm/configuration") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"manifest": map[string]any{"manifest_base64": encoded},
		})
	})
	got, err := testClient.GetClusterManifestConfiguration(context.Background(), "cluster-id", "cm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != encoded {
		t.Errorf("got %q, want %q", got, encoded)
	}
}

func TestPatchClusterStackPartial_UnauthorizedCarriesErrUnauthorized(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"token expired"}`))
	})
	req := PatchStackRequest{
		PartialStack: true,
		Spec:         ResourceSpecSpec{Stacks: []StackSpec{{Name: "demo"}}},
	}
	_, err := testClient.PatchClusterStackPartial(context.Background(), "cluster-id", "demo", req)
	if err == nil {
		t.Fatal("expected error for 401 status")
	}
	perr, ok := err.(*PatchStackError)
	if !ok {
		t.Fatalf("expected *PatchStackError, got %T: %v", err, err)
	}
	if perr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", perr.StatusCode)
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("401 PatchStackError should wrap ErrUnauthorized, got %v", err)
	}
}

func TestPatchStackError_ErrorMethod(t *testing.T) {
	perr := &PatchStackError{StatusCode: 422, Body: []byte("bad")}
	if !strings.Contains(perr.Error(), "422") {
		t.Errorf("Error() should mention status code: %s", perr.Error())
	}
}
