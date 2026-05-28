package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

func TestPatchStackError_ErrorMethod(t *testing.T) {
	perr := &PatchStackError{StatusCode: 422, Body: []byte("bad")}
	if !strings.Contains(perr.Error(), "422") {
		t.Errorf("Error() should mention status code: %s", perr.Error())
	}
}
