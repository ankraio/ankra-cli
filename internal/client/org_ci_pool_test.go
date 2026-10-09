package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The CI pool lane parity table: each call's method, path and body, pinned on
// the request itself, because a typo here fails only against a real platform.
func TestOrganisationCIPoolLaneParity(t *testing.T) {
	weight := 150
	lanes := []struct {
		name       string
		wantMethod string
		wantPath   string
		wantBody   string
		status     int
		call       func(testClient *Client) error
	}{
		{
			name: "list reads the pool", wantMethod: http.MethodGet, wantPath: "/api/v1/org/ci-settings/pool",
			status: http.StatusOK,
			call: func(testClient *Client) error {
				_, getError := testClient.GetOrganisationCIPool(context.Background())
				return getError
			},
		},
		{
			name: "add with a weight sends it, and a 201 is success", wantMethod: http.MethodPut,
			wantPath: "/api/v1/org/ci-settings/pool/cluster-1", wantBody: `{"weight":150}`, status: http.StatusCreated,
			call: func(testClient *Client) error {
				_, setError := testClient.SetOrganisationCIPoolMember(context.Background(), "cluster-1", &weight)
				return setError
			},
		},
		{
			name: "add without a weight sends no body", wantMethod: http.MethodPut,
			wantPath: "/api/v1/org/ci-settings/pool/cluster-1", status: http.StatusOK,
			call: func(testClient *Client) error {
				_, setError := testClient.SetOrganisationCIPoolMember(context.Background(), "cluster-1", nil)
				return setError
			},
		},
		{
			name: "remove deletes the member", wantMethod: http.MethodDelete,
			wantPath: "/api/v1/org/ci-settings/pool/cluster-1", status: http.StatusOK,
			call: func(testClient *Client) error {
				_, removeError := testClient.RemoveOrganisationCIPoolMember(context.Background(), "cluster-1")
				return removeError
			},
		},
	}
	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			var seenMethod, seenPath, seenBody string
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				seenMethod, seenPath = request.Method, request.URL.Path
				body, _ := io.ReadAll(request.Body)
				seenBody = string(body)
				jsonResponse(t, writer, lane.status, OrganisationCIPool{IsPooled: true,
					Members: []OrganisationCIPoolMember{{ClusterID: "cluster-1", Weight: 150}}})
			})
			if callError := lane.call(testClient); callError != nil {
				t.Fatalf("call error = %v", callError)
			}
			if seenMethod != lane.wantMethod || seenPath != lane.wantPath {
				t.Errorf("request = %s %s, want %s %s", seenMethod, seenPath, lane.wantMethod, lane.wantPath)
			}
			if seenBody != lane.wantBody {
				t.Errorf("body = %q, want %q", seenBody, lane.wantBody)
			}
		})
	}
}

// A platform without the pool routes answers a bare 404, which is "not
// supported here", while the routes' own not-found carries a sentence that
// must reach the user verbatim.
func TestOrganisationCIPoolNotFoundShapes(t *testing.T) {
	bare := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte("404 page not found"))
	})
	if _, getError := bare.GetOrganisationCIPool(context.Background()); !errors.Is(getError, ErrCIPoolUnavailable) {
		t.Errorf("a bare 404 is an older platform, got %v", getError)
	}
	detailed := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusNotFound,
			map[string]string{"detail": "This cluster is not listed in the organisation's CI pool."})
	})
	_, removeError := detailed.RemoveOrganisationCIPoolMember(context.Background(), "cluster-1")
	var unexpected *UnexpectedResponseError
	if !errors.As(removeError, &unexpected) || unexpected.StatusCode != http.StatusNotFound ||
		unexpected.Error() != "This cluster is not listed in the organisation's CI pool." {
		t.Errorf("the route's own not-found is relayed verbatim, got %v", removeError)
	}
}

// Clearing a repository's CI cluster is a present key with a null value.
func TestSetPipelineRepositoryClusterSendsTheClusterOrAnExplicitNull(t *testing.T) {
	for _, testCase := range []struct {
		clusterID string
		wantBody  map[string]any
	}{
		{clusterID: "cluster-1", wantBody: map[string]any{"cluster_id": "cluster-1"}},
		{clusterID: "", wantBody: map[string]any{"cluster_id": nil}},
	} {
		var seenMethod, seenPath string
		var seenBody map[string]any
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			seenMethod, seenPath = request.Method, request.URL.Path
			_ = json.NewDecoder(request.Body).Decode(&seenBody)
			jsonResponse(t, writer, http.StatusOK, PipelineRepository{ID: "repo-1"})
		})
		if _, setError := testClient.SetPipelineRepositoryCluster(context.Background(), "repo-1",
			testCase.clusterID); setError != nil {
			t.Fatalf("set error = %v", setError)
		}
		if seenMethod != http.MethodPatch || seenPath != "/api/v1/org/pipelines/repositories/repo-1" {
			t.Errorf("request = %s %s", seenMethod, seenPath)
		}
		got, isPresent := seenBody["cluster_id"]
		if !isPresent || got != testCase.wantBody["cluster_id"] {
			t.Errorf("body = %v, want %v", seenBody, testCase.wantBody)
		}
	}
}

// The capacity read carries the pool's members from a platform that has
// pools, and leaves them out entirely from one that predates them.
func TestGetOrganisationCICapacityDecodesThePoolMembers(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"cluster_id":null,"cluster_name":null,"ci_worker_count":0,` +
			`"is_pooled":true,"pool_members":[{"cluster_id":"c-1","cluster_name":"ci-b","weight":200,` +
			`"is_primary":false,"is_listed":true,"ci_worker_count":8,"steps_in_flight":3,` +
			`"can_run_steps":true,"is_full":false}]}`))
	})
	capacity, getError := testClient.GetOrganisationCICapacity(context.Background())
	if getError != nil {
		t.Fatalf("get error = %v", getError)
	}
	if !capacity.IsPooled || len(capacity.PoolMembers) != 1 || capacity.PoolMembers[0].Weight != 200 ||
		capacity.PoolMembers[0].StepsInFlight != 3 || !capacity.PoolMembers[0].CanRunSteps {
		t.Errorf("capacity = %+v", capacity)
	}
	encoded, _ := json.Marshal(OrganisationCICapacity{})
	if strings.Contains(string(encoded), "pool_members") || strings.Contains(string(encoded), "is_pooled") {
		t.Errorf("an older platform's capacity must not grow pool keys: %s", encoded)
	}
}
