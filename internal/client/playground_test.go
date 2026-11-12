package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestCreatePlaygroundOrdersTheTrialWithAnEmptyBody(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/clusters/playground" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get(csrfHeaderName) == "" {
			t.Error("the order must carry the CSRF double-submit header")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "{}" {
			t.Errorf("trial order body = %s, want {}", body)
		}
		jsonResponse(t, w, http.StatusOK, CreatePlaygroundResult{ClusterID: "c-1", Success: true})
	})
	result, err := testClient.CreatePlayground("")
	if err != nil {
		t.Fatalf("CreatePlayground: %v", err)
	}
	if result.ClusterID != "c-1" || !result.Success {
		t.Fatalf("result = %+v", result)
	}
}

func TestCreatePlaygroundSendsThePlan(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var order createPlaygroundRequest
		if err := json.NewDecoder(r.Body).Decode(&order); err != nil || order.Plan != "medium" {
			t.Errorf("order = %+v (%v), want plan medium", order, err)
		}
		jsonResponse(t, w, http.StatusOK, CreatePlaygroundResult{ClusterID: "c-2", Success: true})
	})
	if _, err := testClient.CreatePlayground("medium"); err != nil {
		t.Fatalf("CreatePlayground: %v", err)
	}
}

// The 409 names the playground the organisation already holds, and the
// caller gets it back while the error still reads, and unwraps, as before.
func TestCreatePlaygroundConflictNamesTheExistingPlayground(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]string{
			"detail":     "This organisation already has a playground.",
			"cluster_id": "existing-1",
		})
	})
	_, err := testClient.CreatePlayground("")
	if err == nil {
		t.Fatal("expected the 409 to be an error")
	}
	if got := ExistingPlaygroundID(err); got != "existing-1" {
		t.Fatalf("ExistingPlaygroundID = %q, want existing-1", got)
	}
	if err.Error() != "This organisation already has a playground." {
		t.Fatalf("message = %q, want the server's detail", err.Error())
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusConflict {
		t.Fatalf("the 409 must still unwrap to *UnexpectedResponseError, got %T", err)
	}
}

func TestCreatePlaygroundCapacityIsAPlainRefusal(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusServiceUnavailable, map[string]string{"detail": "The playground host is at capacity."})
	})
	_, err := testClient.CreatePlayground("")
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want a 503 *UnexpectedResponseError", err)
	}
	if ExistingPlaygroundID(err) != "" {
		t.Fatal("a 503 names no existing playground")
	}
	if unexpected.Detail != "The playground host is at capacity." {
		t.Fatalf("detail = %q", unexpected.Detail)
	}
}

func TestCreatePlaygroundConflictWithoutAClusterIDStaysPlain(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]string{"detail": "already exists"})
	})
	_, err := testClient.CreatePlayground("")
	if ExistingPlaygroundID(err) != "" {
		t.Fatal("a 409 without cluster_id names nothing")
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusConflict {
		t.Fatalf("err = %v, want a 409 *UnexpectedResponseError", err)
	}
}
