package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

const (
	alreadyExistsTestClusterID = "0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	alreadyExistsTestDetail    = "Cluster with this name already exists. Please choose a different name."
)

// importBodyFrom reads the import request's JSON body as the platform would.
func importBodyFrom(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	raw, readError := io.ReadAll(request.Body)
	if readError != nil {
		t.Fatalf("reading the request body: %v", readError)
	}
	var body map[string]any
	if decodeError := json.Unmarshal(raw, &body); decodeError != nil {
		t.Fatalf("the request body is not JSON: %v\n%s", decodeError, raw)
	}
	return body
}

// An apply never carries the mark: the platform's create-or-update is what
// `ankra cluster apply` relies on, and a body that always sent the key would
// turn every re-apply into a refusal.
func TestApplyClusterSendsCreateOnlyOnlyWhenAsked(t *testing.T) {
	cases := map[string]struct {
		request     CreateImportClusterRequest
		wantPresent bool
	}{
		"an apply":         {request: CreateImportClusterRequest{Name: "production"}, wantPresent: false},
		"a create only":    {request: CreateImportClusterRequest{Name: "production", CreateOnly: true}, wantPresent: true},
		"explicitly false": {request: CreateImportClusterRequest{Name: "production", CreateOnly: false}, wantPresent: false},
	}
	for name, testCase := range cases {
		t.Run(name, func(subtest *testing.T) {
			var body map[string]any
			testClient := newTestClient(subtest, func(responseWriter http.ResponseWriter, request *http.Request) {
				body = importBodyFrom(subtest, request)
				jsonResponse(subtest, responseWriter, http.StatusOK, map[string]any{
					"name": "production", "cluster_id": alreadyExistsTestClusterID, "import_command": "helm upgrade", "errors": []any{},
				})
			})
			if _, _, applyError := testClient.ApplyCluster(context.Background(), testCase.request, true); applyError != nil {
				subtest.Fatalf("apply: %v", applyError)
			}
			value, present := body["create_only"]
			if present != testCase.wantPresent {
				subtest.Fatalf("create_only present = %v, want %v (body %v)", present, testCase.wantPresent, body)
			}
			if present && value != true {
				subtest.Fatalf("create_only = %v, want true", value)
			}
		})
	}
}

// The platform's refusal of a taken name is a type a caller can match, and
// it carries the existing cluster so the caller can point at it.
func TestApplyClusterTypesTheAlreadyExistsRefusal(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		jsonResponse(t, responseWriter, http.StatusConflict, map[string]any{
			"detail": alreadyExistsTestDetail, "error_code": "CLUSTER_ALREADY_EXISTS",
			"cluster_id": alreadyExistsTestClusterID, "name": "production",
		})
	})

	response, submitted, applyError := testClient.ApplyCluster(context.Background(),
		CreateImportClusterRequest{Name: "production", CreateOnly: true}, true)
	if response != nil || submitted {
		t.Fatalf("a refused write answered response=%+v submitted=%v", response, submitted)
	}
	var alreadyExists *ClusterAlreadyExistsError
	if !errors.As(applyError, &alreadyExists) {
		t.Fatalf("error = %T (%v), want *ClusterAlreadyExistsError", applyError, applyError)
	}
	if alreadyExists.ClusterID != alreadyExistsTestClusterID || alreadyExists.Name != "production" {
		t.Fatalf("refusal = %+v, want the existing cluster's id and name", alreadyExists)
	}
	if alreadyExists.Error() != alreadyExistsTestDetail {
		t.Fatalf("message = %q, want the platform's sentence", alreadyExists.Error())
	}
}

// A caller that reads the status and detail of a refused write, as the apply
// path does, sees this refusal the way it sees every other 409.
func TestClusterAlreadyExistsRefusalIsStillAConflictWithADetail(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		jsonResponse(t, responseWriter, http.StatusConflict, map[string]any{
			"detail": alreadyExistsTestDetail, "error_code": "CLUSTER_ALREADY_EXISTS",
			"cluster_id": alreadyExistsTestClusterID, "name": "production",
		})
	})

	_, _, applyError := testClient.ApplyCluster(context.Background(), CreateImportClusterRequest{Name: "production"}, true)
	var unexpected *UnexpectedResponseError
	if !errors.As(applyError, &unexpected) {
		t.Fatalf("error = %T (%v), want it to unwrap to *UnexpectedResponseError", applyError, applyError)
	}
	if unexpected.StatusCode != http.StatusConflict || unexpected.Detail != alreadyExistsTestDetail {
		t.Fatalf("unwrapped = status %d detail %q, want 409 and the platform's sentence", unexpected.StatusCode, unexpected.Detail)
	}
}

// The import answers other 409s. None of them is this refusal: only the code
// says a cluster already holds the name.
func TestOtherAnswersAreNotTheAlreadyExistsRefusal(t *testing.T) {
	cases := map[string]struct {
		status int
		body   map[string]any
	}{
		"a 409 with another code": {status: http.StatusConflict,
			body: map[string]any{"detail": "An apply of this cluster is already running.", "error_code": "APPLY_IN_PROGRESS"}},
		"a 409 with no code": {status: http.StatusConflict,
			body: map[string]any{"detail": alreadyExistsTestDetail}},
		"the code on another status": {status: http.StatusBadRequest,
			body: map[string]any{"detail": alreadyExistsTestDetail, "error_code": "CLUSTER_ALREADY_EXISTS"}},
	}
	for name, testCase := range cases {
		t.Run(name, func(subtest *testing.T) {
			testClient := newTestClient(subtest, func(responseWriter http.ResponseWriter, request *http.Request) {
				jsonResponse(subtest, responseWriter, testCase.status, testCase.body)
			})
			_, _, applyError := testClient.ApplyCluster(context.Background(), CreateImportClusterRequest{Name: "production"}, true)
			if applyError == nil {
				subtest.Fatal("a refused write must be an error")
			}
			var alreadyExists *ClusterAlreadyExistsError
			if errors.As(applyError, &alreadyExists) {
				subtest.Fatalf("error %v was read as the already-exists refusal", applyError)
			}
		})
	}
}

// A refusal that names no cluster and gives no sentence still says what
// happened, and leaves the id empty instead of inventing one.
func TestClusterAlreadyExistsRefusalWithoutDetails(t *testing.T) {
	refusal := clusterAlreadyExistsFromBody(http.StatusConflict, []byte(`{"error_code":"CLUSTER_ALREADY_EXISTS"}`))
	if refusal == nil {
		t.Fatal("the code alone is the refusal")
	}
	if refusal.ClusterID != "" || refusal.Name != "" {
		t.Fatalf("refusal = %+v, want no cluster named", refusal)
	}
	if refusal.Error() == "" {
		t.Fatal("a refusal with no detail must still have a message")
	}
}
