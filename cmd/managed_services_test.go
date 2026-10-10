package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The managed-services commands run here against an in-memory platform
// served over HTTP, through the real client, so each test sees exactly the
// requests a real platform would: which route, which body, which digest.

const (
	fakeServiceClusterID    = "c1a2b3c4-0000-4000-8000-000000000001"
	fakeServiceApplication  = "0a000000-0000-4000-8000-000000000002"
	fakeServiceInstanceID   = "c9b96bb8-0000-4000-8000-000000000003"
	fakeServiceConsumerID   = "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11"
	fakeServicePackageID    = "a5e62beb-cbe7-4340-9986-635e09cf086e"
	fakeServiceReviewID     = "0b6f2a8e-3a1c-4b8e-9a51-2c7d4f1e9a20"
	fakeServiceRetirementID = "7d3c1f0a-5b2e-4c9d-8e1f-0a2b3c4d5e6f"
	fakeServiceOrganisation = "3b7dccca-0788-4470-9910-19478ae345ae"
	// fakeServiceSecretValue is a credential value the fake platform slips
	// into its answers where none belongs. It must never reach any output.
	fakeServiceSecretValue = "hunter2-plaintext-credential"
)

var (
	fakeServiceReviewDigest     = "sha256:" + strings.Repeat("a", 64)
	fakeServiceRetirementDigest = "sha256:" + strings.Repeat("b", 64)
)

type fakeServiceRequest struct {
	method       string
	path         string
	query        string
	body         string
	organisation string
}

type fakeServicesPlatform struct {
	t      *testing.T
	server *httptest.Server
	mutex  sync.Mutex
	calls  []fakeServiceRequest

	// policyMissing makes the policy read answer the platform's 409.
	policyMissing bool
	// pendingPages are the pages the pending-retirement listing answers, in
	// cursor order; nil answers one empty page.
	pendingPages [][]map[string]any
	// retirementStates are the successive states a retirement read answers;
	// the last one repeats.
	retirementStates []string
	retirementReads  int
	// settledOutcome is the outcome a settled retirement carries.
	settledOutcome string
	// reviewChanged makes every setup confirmation answer the platform's
	// conflict, as for a review that expired or whose inputs changed.
	reviewChanged bool
	// consumersUnreadable makes the consumer listing answer a refusal.
	consumersUnreadable bool
	// packageSecrets are the secret inputs the package contract declares.
	packageSecrets []any
	// unbindInUse makes a binding removal answer that a service still uses it.
	unbindInUse bool
	// ignoreInstanceFilters answers the instance inventory unfiltered, as a
	// platform from before cluster#4053 that ignores the filter keys would.
	ignoreInstanceFilters bool
}

// fakeServiceOrphanConsumerID is a binding whose application was removed:
// it no longer reads by id, but can still be removed at its revision.
const fakeServiceOrphanConsumerID = "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1cd0"

var fakeServiceBindingRevisions = map[string]string{fakeServiceConsumerID: "4", fakeServiceOrphanConsumerID: "2"}

func newFakeServicesPlatform(t *testing.T) *fakeServicesPlatform {
	t.Helper()
	platform := &fakeServicesPlatform{t: t, settledOutcome: "retired"}
	platform.server = httptest.NewServer(http.HandlerFunc(platform.serve))
	t.Cleanup(platform.server.Close)
	previousInterval := serviceRetirementPollInterval
	serviceRetirementPollInterval = 0
	t.Cleanup(func() { serviceRetirementPollInterval = previousInterval })
	return platform
}

// client is a real API client aimed at the fake platform, built the way the
// CLI builds its own, so the --org override travels in its transport.
func (platform *fakeServicesPlatform) client() *client.Client {
	return client.New("test-token", platform.server.URL)
}

func (platform *fakeServicesPlatform) requests(method string, pathSuffix string) []fakeServiceRequest {
	platform.mutex.Lock()
	defer platform.mutex.Unlock()
	var matched []fakeServiceRequest
	for _, call := range platform.calls {
		if call.method == method && strings.HasSuffix(call.path, pathSuffix) {
			matched = append(matched, call)
		}
	}
	return matched
}

func fakeServiceJSON(t *testing.T, writer http.ResponseWriter, status int, body any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if encodeError := json.NewEncoder(writer).Encode(body); encodeError != nil {
		t.Errorf("encode: %v", encodeError)
	}
}

func fakeServiceInstance() map[string]any {
	return map[string]any{
		"id": fakeServiceInstanceID, "package_version_id": fakeServicePackageID, "cluster_id": fakeServiceClusterID,
		"cluster_name": "prod", "state": "serving",
		"admission_operation_id": "e0000000-0000-4000-8000-000000000009", "name": "orders-db",
		"region": "eu-north-1", "data_boundary": "eu", "generation": 2, "mode": "customer",
		"requested_destinations": []any{map[string]any{"application_id": fakeServiceApplication, "cluster_id": fakeServiceClusterID, "namespace": "orders"}},
		"consumers": []any{map[string]any{"id": fakeServiceConsumerID, "application_id": fakeServiceApplication,
			"cluster_id": fakeServiceClusterID, "namespace": "orders", "application_name": "storefront", "cluster_name": "prod",
			"environment": map[string]any{"id": "e1000000-0000-4000-8000-000000000020", "name": "production", "role": "production"}}},
		"deployment_operation_id": "e0000000-0000-4000-8000-000000000010", "deployment_state": "verification_pending",
		"readiness": "ready",
		"readiness_check": map[string]any{"observed_at": "2026-10-02T10:00:00Z", "expires_at": "2026-10-02T10:15:00Z",
			"reason": "canary logged in and ran SELECT 1", "last_passed_at": "2026-10-02T10:00:00Z"},
		"created_at": "2026-10-02T09:00:00Z", "released_at": nil, "evidence_updated_at": "2026-10-02T09:05:00Z",
		"connection": map[string]any{
			"namespace": "orders-db",
			"endpoints": []any{map[string]any{"name": "rw", "address": "postgresql-rw.orders-db.svc:5432"},
				map[string]any{"name": "ro", "address": "postgresql-ro.orders-db.svc:5432"}},
			// A value where the contract has none: the CLI must drop it.
			"secret":   map[string]any{"name": "postgresql-app", "keys": []any{"username", "password", "fqdn-uri"}, "password": fakeServiceSecretValue},
			"password": fakeServiceSecretValue,
		},
		"credentials": map[string]any{"password": fakeServiceSecretValue, "uri": "postgresql://app:" + fakeServiceSecretValue + "@x"},
		"health": map[string]any{"state": "serving", "observed_at": "2026-10-02T10:01:00Z", "expires_at": "2026-10-02T10:06:00Z",
			"reason": "primary ready, 1 of 1 instances ready"},
		"retirement": nil,
	}
}

func fakeServiceReview(confirmed bool) map[string]any {
	review := map[string]any{
		"id": fakeServiceReviewID, "package_version_id": fakeServicePackageID, "digest": fakeServiceReviewDigest,
		"created_at": "2026-10-02T09:00:00Z", "execution_id": nil, "confirmed_at": nil,
		"capacity": map[string]any{"state": "unknown", "reason": "The playground's quota could not be read; the service may not fit.",
			"plan_id": "trial", "quota": nil, "committed": nil, "transient": nil, "required": nil},
		"plan": map[string]any{
			"schema_version": 2, "name": "orders-db", "mode": "customer", "region": "eu-north-1", "data_boundary": "eu",
			"service_cluster_id": fakeServiceClusterID, "cluster_id": fakeServiceClusterID,
			"package_digest": "sha256:" + strings.Repeat("c", 64), "expires_at": "2026-10-02T09:10:00Z",
			"parameters":        map[string]any{"instances": 1, "storage_gib": 20, "memory_mib": 512, "cpu_millicores": 250},
			"secret_references": map[string]any{},
			"consumers": []any{map[string]any{"id": fakeServiceConsumerID, "application_id": fakeServiceApplication,
				"cluster_id": fakeServiceClusterID, "namespace": "orders", "binding_kind": "installation",
				"installation_id": "f0000000-0000-4000-8000-000000000011"}},
		},
	}
	if confirmed {
		review["execution_id"] = "e0000000-0000-4000-8000-000000000012"
		review["confirmed_at"] = "2026-10-02T09:01:00Z"
	}
	return review
}

func fakeServiceRetirement(state string) map[string]any {
	retirement := map[string]any{
		"id": fakeServiceRetirementID, "instance_id": fakeServiceInstanceID, "digest": fakeServiceRetirementDigest,
		"state": state, "created_at": "2026-10-02T11:00:00Z", "expires_at": "2026-10-02T11:10:00Z",
		"execution_id": nil, "confirmed_at": nil, "phase": nil, "settled_at": nil, "outcome": nil, "reason": nil,
		"volume_disposal": nil,
		"plan": map[string]any{
			"schema_version": 1, "instance_id": fakeServiceInstanceID, "organisation_id": fakeServiceOrganisation,
			"actor_id": "a0000000-0000-4000-8000-000000000013", "name": "orders-db", "cluster_id": fakeServiceClusterID,
			"generation": 2, "namespace": "orders-db",
			"namespace_contents": map[string]any{
				"observed_at": "2026-10-02T10:59:00Z", "complete": true, "incomplete_reason": nil,
				"items": []any{map[string]any{"resource_type": "deployments", "kind": "Deployment", "name": "debug-shell",
					"created_at": "2026-10-01T00:00:00Z", "owner": nil}},
				"volumes": []any{map[string]any{"claim_name": "postgresql-1", "claim_uid": "u1", "created_by_service": true,
					"storage_class": "local-path", "volume_name": "pvc-123", "reclaim_policy": "Retain"}},
			},
			"stack": map[string]any{"resource_id": "d0000000-0000-4000-8000-000000000014", "name": "orders-db", "state": "deployed",
				"members":       []any{map[string]any{"id": "d0000000-0000-4000-8000-000000000015", "kind": "addon", "name": "postgresql-orders-db", "namespace": "orders-db"}},
				"member_digest": "sha256:" + strings.Repeat("d", 64)},
			"nothing_deployed": false, "owned_draft_id": nil,
			"consumers_to_disconnect": []any{map[string]any{"id": fakeServiceConsumerID, "application_id": fakeServiceApplication,
				"cluster_id": fakeServiceClusterID, "namespace": "orders"}},
			"shared_kept": []any{map[string]any{"stack_name": "cloudnative-pg", "title": "CloudNativePG operator"}},
			"data": map[string]any{"namespace_deleted": true, "volumes_deleted": true, "secrets_deleted": true, "export_offered": false,
				"restore_points": "kept_per_vault_retention",
				"statement":      "The namespace orders-db, its volume claims and Secrets are deleted; no export is offered."},
			"expires_at": "2026-10-02T11:10:00Z",
		},
	}
	switch state {
	case "in_progress":
		retirement["execution_id"] = "e0000000-0000-4000-8000-000000000016"
		retirement["confirmed_at"] = "2026-10-02T11:01:00Z"
		retirement["phase"] = "dispose_data"
	case "settled":
		retirement["execution_id"] = "e0000000-0000-4000-8000-000000000016"
		retirement["confirmed_at"] = "2026-10-02T11:01:00Z"
		retirement["phase"] = "dispose_data"
		retirement["settled_at"] = "2026-10-02T11:02:00Z"
		retirement["volume_disposal"] = map[string]any{"observed_at": "2026-10-02T11:02:00Z", "complete": true,
			"retained_volumes": []any{"pvc-123"},
			"volumes": []any{map[string]any{"claim_name": "postgresql-1", "volume_name": "pvc-123", "state": "retained",
				"reclaim_policy": "Retain", "phase": "Released"}}}
	}
	return retirement
}

func (platform *fakeServicesPlatform) serve(writer http.ResponseWriter, request *http.Request) {
	t := platform.t
	bodyBytes, _ := io.ReadAll(request.Body)
	platform.mutex.Lock()
	platform.calls = append(platform.calls, fakeServiceRequest{request.Method, request.URL.Path, request.URL.RawQuery,
		string(bodyBytes), request.Header.Get("X-Ankra-Organisation-Id")})
	platform.mutex.Unlock()

	admission := "/api/v1/org/service-admission"
	instancePath := admission + "/instances/" + fakeServiceInstanceID
	retirementPath := instancePath + "/retirements/" + fakeServiceRetirementID
	path := request.URL.Path
	switch {
	case request.Method == http.MethodGet && path == "/api/v1/org/organisation":
		fakeServiceJSON(t, writer, http.StatusOK, []any{map[string]any{"organisation_id": fakeServiceOrganisation, "name": "Ankra AB"}})
	case request.Method == http.MethodGet && path == "/api/v1/org/service-packages":
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"next_cursor": nil, "items": []any{
			map[string]any{"id": fakeServicePackageID, "publisher_id": "a11b7a00-0000-4000-a000-000000000001", "name": "postgresql",
				"version": "1.0.0", "capability": "database", "digest": "sha256:" + strings.Repeat("c", 64), "first_party": true,
				"created_at": "2026-09-30T14:56:09Z"},
		}})
	case request.Method == http.MethodGet && path == "/api/v1/org/service-packages/"+fakeServicePackageID:
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"id": fakeServicePackageID, "publisher_id": "a11b7a00-0000-4000-a000-000000000001",
			"name": "postgresql", "version": "1.0.0", "capability": "database", "digest": "sha256:" + strings.Repeat("c", 64),
			"first_party": true, "created_at": "2026-09-30T14:56:09Z",
			"contract": map[string]any{"schema_version": 1, "name": "postgresql", "version": "1.0.0", "capability": "database",
				"publisher_id": "a11b7a00-0000-4000-a000-000000000001",
				"profiles": map[string]any{"customer": map[string]any{"id": "c8a9b817-9147-416c-9117-f1bee03c687f",
					"version_id": "4bd1b3a1-d800-4747-afa5-3e0667f17e2b", "digest": "sha256:" + strings.Repeat("e", 64)}},
				"parameters": []any{
					map[string]any{"name": "instances", "minimum": 1, "maximum": 3, "default": 1, "unit": "instances"},
					map[string]any{"name": "storage_gib", "minimum": 1, "maximum": 1000, "default": 10, "unit": "gib"},
				},
				"secrets": platform.declaredSecrets(), "outputs": []any{map[string]any{"name": "DATABASE_ENDPOINT", "kind": "endpoint"}},
				"lifecycle": map[string]any{"verify": "stack-profile-verify", "upgrade": "stack-profile-upgrade",
					"recovery": "stack-profile-recovery", "delete": "stack-profile-delete"}}})
	case path == admission+"/cluster-policies/"+fakeServiceClusterID:
		if platform.policyMissing && request.Method == http.MethodGet {
			fakeServiceJSON(t, writer, http.StatusConflict, map[string]any{"detail": servicePolicyRequiredDetail})
			return
		}
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"cluster_id": fakeServiceClusterID, "region": "eu-north-1",
			"data_boundary": "eu", "local_only": false, "revision": 3, "source": "organisation_declared", "updated_at": "2026-10-01T00:00:00Z"})
	case request.Method == http.MethodPost && path == admission+"/reviews":
		fakeServiceJSON(t, writer, http.StatusOK, fakeServiceReview(false))
	case request.Method == http.MethodGet && path == admission+"/reviews/"+fakeServiceReviewID:
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"id": fakeServiceReviewID, "package_version_id": fakeServicePackageID,
			"cluster_id": fakeServiceClusterID, "name": "orders-db", "region": "eu-north-1", "data_boundary": "eu",
			"digest": fakeServiceReviewDigest, "mode": "customer", "parameters": map[string]any{"instances": 1},
			"secret_inputs": []any{}, "destinations": []any{map[string]any{"application_id": fakeServiceApplication,
				"cluster_id": fakeServiceClusterID, "namespace": "orders"}},
			"state": "pending", "created_at": "2026-10-02T09:00:00Z", "expires_at": "2026-10-02T09:10:00Z",
			"execution_id": nil, "confirmed_at": nil})
	case request.Method == http.MethodPost && path == admission+"/reviews/"+fakeServiceReviewID+"/confirm":
		var body struct{ Digest string }
		_ = json.Unmarshal(bodyBytes, &body)
		if body.Digest != fakeServiceReviewDigest || platform.reviewChanged {
			fakeServiceJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Service review has changed"})
			return
		}
		fakeServiceJSON(t, writer, http.StatusOK, fakeServiceReview(true))
	case request.Method == http.MethodGet && path == admission+"/consumers":
		if platform.consumersUnreadable {
			fakeServiceJSON(t, writer, http.StatusForbidden, map[string]any{"detail": "Insufficient permission"})
			return
		}
		if request.URL.Query().Get("application_id") != fakeServiceApplication {
			t.Errorf("consumer listing for an unexpected application: %s", request.URL.RawQuery)
		}
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"next_cursor": nil, "items": []any{
			map[string]any{"id": fakeServiceConsumerID, "application_id": fakeServiceApplication, "cluster_id": fakeServiceClusterID,
				"namespace": "orders", "allow_planned": true, "local_only": false, "revision": 4, "updated_at": "2026-10-01T00:00:00Z"},
		}})
	case request.Method == http.MethodGet && path == admission+"/consumers/"+fakeServiceConsumerID:
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"id": fakeServiceConsumerID, "application_id": fakeServiceApplication,
			"organisation_id": fakeServiceOrganisation, "cluster_id": fakeServiceClusterID, "namespace": "orders",
			"region": "eu-north-1", "data_boundary": "eu", "policy_revision": 3, "binding_revision": 4,
			"binding_kind": "installation", "installation_id": "f0000000-0000-4000-8000-000000000011", "local_only": false})
	case request.Method == http.MethodGet && strings.HasPrefix(path, admission+"/consumers/"):
		fakeServiceJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service admission resource not found"})
	case request.Method == http.MethodDelete && strings.HasPrefix(path, admission+"/consumers/"):
		current, known := fakeServiceBindingRevisions[strings.TrimPrefix(path, admission+"/consumers/")]
		switch {
		case !known:
			fakeServiceJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service admission resource not found"})
		case platform.unbindInUse:
			fakeServiceJSON(t, writer, http.StatusConflict, map[string]any{"detail": "A service still uses this binding; retire the service before removing it"})
		case request.URL.Query().Get("expected_revision") != current:
			fakeServiceJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Service configuration changed; refresh and review again"})
		default:
			writer.WriteHeader(http.StatusNoContent)
		}
	case request.Method == http.MethodPost && path == admission+"/consumers":
		var body client.ServiceConsumerRequest
		_ = json.Unmarshal(bodyBytes, &body)
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"id": "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c99",
			"application_id": body.ApplicationID, "organisation_id": fakeServiceOrganisation, "cluster_id": body.ClusterID,
			"namespace": body.Namespace, "region": "eu-north-1", "data_boundary": "eu", "policy_revision": 3, "binding_revision": 1,
			"binding_kind": "planned", "installation_id": nil, "local_only": body.LocalOnly})
	case request.Method == http.MethodGet && path == admission+"/instances":
		// The first page is empty but carries a cursor: rows the caller may
		// not read used the scan budget. Stopping there would miss the
		// service.
		if request.URL.Query().Get("after") == "" {
			fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"items": []any{}, "next_cursor": "00000000-0000-4000-8000-0000000000ff"})
			return
		}
		// The platform's own filters (cluster#4053): the one service serves
		// fakeServiceApplication and runs on fakeServiceClusterID.
		items := []any{fakeServiceInstance()}
		query := request.URL.Query()
		if !platform.ignoreInstanceFilters &&
			((query.Has("application_id") && query.Get("application_id") != fakeServiceApplication) ||
				(query.Has("cluster_id") && query.Get("cluster_id") != fakeServiceClusterID)) {
			items = []any{}
		}
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
	case request.Method == http.MethodGet && path == "/api/v1/org/applications":
		// The application name lookup: a case-insensitive substring search.
		var result []any
		if search := strings.ToLower(request.URL.Query().Get("search")); strings.Contains("storefront", search) {
			result = append(result, map[string]any{"id": fakeServiceApplication, "name": "storefront"})
		}
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"result": result,
			"pagination": map[string]any{"total_pages": 1, "page": 1, "page_size": 100, "total_count": len(result)}})
	case request.Method == http.MethodGet && path == instancePath:
		fakeServiceJSON(t, writer, http.StatusOK, fakeServiceInstance())
	case request.Method == http.MethodPost && path == instancePath+"/retirements":
		var body client.ServiceRetirementRequest
		_ = json.Unmarshal(bodyBytes, &body)
		if !body.AcknowledgeDataLoss {
			fakeServiceJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Acknowledge the data loss"})
			return
		}
		fakeServiceJSON(t, writer, http.StatusOK, fakeServiceRetirement("pending"))
	case request.Method == http.MethodGet && path == instancePath+"/retirements":
		platform.serveRetirementPage(writer, request)
	case request.Method == http.MethodGet && path == retirementPath:
		state := "pending"
		if len(platform.retirementStates) > 0 {
			index := min(platform.retirementReads, len(platform.retirementStates)-1)
			state = platform.retirementStates[index]
		}
		platform.retirementReads++
		retirement := fakeServiceRetirement(state)
		if state == "settled" {
			retirement["outcome"] = platform.settledOutcome
			if platform.settledOutcome != "retired" {
				retirement["reason"] = "another workload deploys into orders-db"
			}
		}
		fakeServiceJSON(t, writer, http.StatusOK, retirement)
	case request.Method == http.MethodPost && path == retirementPath+"/confirm":
		var body struct{ Digest string }
		_ = json.Unmarshal(bodyBytes, &body)
		if body.Digest != fakeServiceRetirementDigest {
			fakeServiceJSON(t, writer, http.StatusConflict, map[string]any{"detail": "Service retirement has changed"})
			return
		}
		fakeServiceJSON(t, writer, http.StatusOK, fakeServiceRetirement("in_progress"))
	default:
		// Display-name lookups (the cluster listing) are best effort; an
		// unknown route answers a bare 404 like an unregistered route.
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (platform *fakeServicesPlatform) declaredSecrets() []any {
	if platform.packageSecrets == nil {
		return []any{}
	}
	return platform.packageSecrets
}

func (platform *fakeServicesPlatform) serveRetirementPage(writer http.ResponseWriter, request *http.Request) {
	pages := platform.pendingPages
	if len(pages) == 0 {
		pages = [][]map[string]any{{}}
	}
	index := 0
	if after := request.URL.Query().Get("after"); after != "" {
		number, parseError := strconv.Atoi(strings.TrimPrefix(after, "cursor-"))
		if parseError != nil || number < 1 || number >= len(pages) {
			fakeServiceJSON(platform.t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Invalid service review page"})
			return
		}
		index = number
	}
	items := make([]any, 0, len(pages[index]))
	for _, item := range pages[index] {
		items = append(items, item)
	}
	var next any
	if index+1 < len(pages) {
		next = fmt.Sprintf("cursor-%d", index+1)
	}
	fakeServiceJSON(platform.t, writer, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// runServicesCommand runs `ankra services <arguments>` against the fake
// platform and returns stdout and stderr separately, so a test can prove
// stdout carries nothing but the document under -o json.
func runServicesCommand(t *testing.T, platform *fakeServicesPlatform, input string, arguments ...string) (string, string, error) {
	t.Helper()
	previousClient := apiClient
	apiClient = platform.client()
	t.Cleanup(func() { apiClient = previousClient })
	servicesCommand := newServicesCommand()
	var stdout, stderr bytes.Buffer
	servicesCommand.SetOut(&stdout)
	servicesCommand.SetErr(&stderr)
	servicesCommand.SetIn(strings.NewReader(input))
	servicesCommand.SetArgs(arguments)
	runError := servicesCommand.Execute()
	return stdout.String(), stderr.String(), runError
}

func TestServicesCommandsRegistered(t *testing.T) {
	expected := map[string][]string{
		"":            {"list", "get", "setup", "delete", "packages", "policy", "consumers", "reviews", "retirements"},
		"packages":    {"list", "get"},
		"policy":      {"get", "set"},
		"consumers":   {"list", "get", "bind", "unbind"},
		"reviews":     {"list", "get", "confirm"},
		"retirements": {"list", "get", "confirm"},
	}
	servicesCommand := newServicesCommand()
	for parent, children := range expected {
		group := servicesCommand
		if parent != "" {
			found, _, findError := servicesCommand.Find([]string{parent})
			if findError != nil || found.Name() != parent {
				t.Fatalf("services %s is not registered", parent)
			}
			group = found
		}
		for _, child := range children {
			found, _, findError := group.Find([]string{child})
			if findError != nil || found.Name() != child {
				t.Errorf("services %s %s is not registered", parent, child)
			}
		}
	}
	registered, _, findError := rootCmd.Find([]string{"services"})
	if findError != nil || registered.Name() != "services" {
		t.Fatal("ankra services is not registered on the root command")
	}
}

// Setup prepares a review, shows the plan, and confirms it with exactly the
// digest the prepare answered. Region and data boundary come from the
// cluster's placement policy when not given.
func TestServicesSetupConfirmsTheReviewedDigest(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID,
		"--consumer", fakeServiceConsumerID, "--param", "storage_gib=20", "--yes")
	if runError != nil {
		t.Fatalf("setup: %v", runError)
	}
	prepares := platform.requests(http.MethodPost, "/service-admission/reviews")
	if len(prepares) != 1 {
		t.Fatalf("expected one prepare, got %d", len(prepares))
	}
	var prepared map[string]any
	if decodeError := json.Unmarshal([]byte(prepares[0].body), &prepared); decodeError != nil {
		t.Fatal(decodeError)
	}
	want := map[string]any{"package_version_id": fakeServicePackageID, "name": "orders-db", "mode": "customer",
		"region": "eu-north-1", "data_boundary": "eu", "cluster_id": fakeServiceClusterID}
	for key, value := range want {
		if prepared[key] != value {
			t.Errorf("prepare %s = %v, want %v", key, prepared[key], value)
		}
	}
	if consumers, _ := prepared["consumer_ids"].([]any); len(consumers) != 1 || consumers[0] != fakeServiceConsumerID {
		t.Errorf("prepare consumer_ids = %v", prepared["consumer_ids"])
	}
	if parameters, _ := prepared["parameters"].(map[string]any); parameters["storage_gib"] != float64(20) {
		t.Errorf("prepare parameters = %v", prepared["parameters"])
	}
	if secretReferences, isObject := prepared["secret_references"].(map[string]any); !isObject || len(secretReferences) != 0 {
		t.Errorf("secret_references must be an empty object, got %v", prepared["secret_references"])
	}

	confirms := platform.requests(http.MethodPost, "/reviews/"+fakeServiceReviewID+"/confirm")
	if len(confirms) != 1 {
		t.Fatalf("expected one confirm, got %d", len(confirms))
	}
	if confirms[0].body != `{"digest":"`+fakeServiceReviewDigest+`"}` {
		t.Errorf("confirm must carry exactly the reviewed digest, got %s", confirms[0].body)
	}
	for _, expected := range []string{"Setup review " + fakeServiceReviewID, "postgresql 1.0.0", "storage_gib=20",
		fakeServiceReviewDigest, "is being set up", "e0000000-0000-4000-8000-000000000012", "ankra services get orders-db"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("setup output lacks %q:\n%s", expected, stdout)
		}
	}
}

// Nothing confirms on its own: without --yes the prompt decides, and a
// script that answers nothing (stdin at EOF) is declined with exit 4.
func TestServicesSetupNeverConfirmsWithoutAYes(t *testing.T) {
	for _, input := range []string{"", "n\n", "maybe\n"} {
		platform := newFakeServicesPlatform(t)
		_, stderr, runError := runServicesCommand(t, platform, input,
			"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID)
		if !errors.Is(runError, errCancelled) || exitCodeFor(runError) != exitCancelled {
			t.Fatalf("input %q: expected the cancelled exit, got %v", input, runError)
		}
		if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
			t.Fatalf("input %q: a declined setup confirmed: %+v", input, confirms)
		}
		if !strings.Contains(stderr, "ankra services reviews confirm "+fakeServiceReviewID+" --digest "+fakeServiceReviewDigest) {
			t.Errorf("a declined setup must say how to confirm the open review later:\n%s", stderr)
		}
	}

	platform := newFakeServicesPlatform(t)
	if _, _, runError := runServicesCommand(t, platform, "y\n",
		"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID); runError != nil {
		t.Fatalf("a yes at the prompt must confirm: %v", runError)
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 1 {
		t.Fatalf("expected one confirm after a yes, got %d", len(confirms))
	}
}

func TestServicesSetupReviewOnlyConfirmsNothing(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db", "--package", fakeServicePackageID, "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID, "--review-only")
	if runError != nil {
		t.Fatal(runError)
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
		t.Fatalf("--review-only confirmed: %+v", confirms)
	}
	if !strings.Contains(stdout, "Nothing was set up") ||
		!strings.Contains(stdout, "ankra services reviews confirm "+fakeServiceReviewID+" --digest "+fakeServiceReviewDigest) {
		t.Errorf("--review-only must print the confirm command:\n%s", stdout)
	}
}

// Under -o json stdout is the confirmed review and nothing else; the plan
// and the hints go to stderr.
func TestServicesSetupStructuredOutputStaysParseable(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, stderr, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID, "--yes", "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	var confirmed client.ServiceReview
	if decodeError := json.Unmarshal([]byte(stdout), &confirmed); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	if confirmed.ExecutionID == nil || confirmed.Digest != fakeServiceReviewDigest || confirmed.Plan["name"] != "orders-db" {
		t.Errorf("unexpected confirmed review: %+v", confirmed)
	}
	if confirmed.Capacity == nil || confirmed.Capacity.State != "unknown" {
		t.Errorf("-o json must carry the capacity verdict, got %+v", confirmed.Capacity)
	}
	if !strings.Contains(stderr, "Setup review "+fakeServiceReviewID) {
		t.Errorf("the plan must still be shown, on stderr:\n%s", stderr)
	}
}

// Contract checks refuse before a review is stored, and a cluster without a
// placement policy is named as such.
func TestServicesSetupRefusesBeforePreparing(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		policy    bool
		exitCode  int
		message   string
	}{
		{"no consumer", []string{"--package", "postgresql"}, true, exitUsage, "at least one --consumer"},
		{"bad name", []string{"Orders_DB", "--package", "postgresql", "--consumer", fakeServiceConsumerID}, true, exitUsage, "lower-case"},
		{"unknown parameter", []string{"--package", "postgresql", "--consumer", fakeServiceConsumerID, "--param", "replicas=2"}, true, exitUsage, "no parameter"},
		{"out of bounds", []string{"--package", "postgresql", "--consumer", fakeServiceConsumerID, "--param", "instances=9"}, true, exitUsage, "outside 1-3"},
		{"mode not offered", []string{"--package", "postgresql", "--consumer", fakeServiceConsumerID, "--mode", "existing"}, true, exitUsage, "does not run in existing mode"},
		{"hosted", []string{"--package", "postgresql", "--consumer", fakeServiceConsumerID, "--mode", "hosted"}, true, exitUsage, "hosted"},
		{"secret value", []string{"--package", "postgresql", "--consumer", fakeServiceConsumerID, "--secret-reference", "password=s3cret"}, true, exitUsage, "not a value"},
		{"undeclared secret input", []string{"--package", "postgresql", "--consumer", fakeServiceConsumerID,
			"--secret-reference", "password=9e000000-0000-4000-8000-000000000017"}, true, exitUsage, "no secret input"},
		{"unknown package", []string{"--package", "mysql", "--consumer", fakeServiceConsumerID}, true, exitNotFound, "no service package"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			platform := newFakeServicesPlatform(t)
			platform.policyMissing = !testCase.policy
			arguments := []string{"setup"}
			if !strings.HasPrefix(testCase.arguments[0], "--") {
				arguments = append(arguments, testCase.arguments...)
			} else {
				arguments = append(append(arguments, "orders-db"), testCase.arguments...)
			}
			arguments = append(arguments, "--cluster", fakeServiceClusterID, "--yes")
			_, _, runError := runServicesCommand(t, platform, "", arguments...)
			if runError == nil || exitCodeFor(runError) != testCase.exitCode || !strings.Contains(runError.Error(), testCase.message) {
				t.Fatalf("expected exit %d mentioning %q, got %v (exit %d)", testCase.exitCode, testCase.message, runError, exitCodeFor(runError))
			}
			if prepares := platform.requests(http.MethodPost, "/reviews"); len(prepares) != 0 {
				t.Errorf("a refused setup stored a review")
			}
		})
	}
}

// A placement policy is optional: setup on a cluster that declares none
// prepares the review in the platform's default location instead of refusing,
// and explicit --region/--data-boundary still win.
func TestServicesSetupWithoutAPolicyUsesTheDefaultLocation(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		flags            []string
		region, boundary string
	}{
		{"default", nil, servicesUndeclaredLocation, servicesUndeclaredLocation},
		{"explicit", []string{"--region", "eu-west-1", "--data-boundary", "eu"}, "eu-west-1", "eu"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			platform := newFakeServicesPlatform(t)
			platform.policyMissing = true
			arguments := append([]string{"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID,
				"--consumer", fakeServiceConsumerID, "--yes"}, testCase.flags...)
			if _, _, runError := runServicesCommand(t, platform, "", arguments...); runError != nil {
				t.Fatalf("setup on a cluster with no policy: %v", runError)
			}
			prepares := platform.requests(http.MethodPost, "/service-admission/reviews")
			if len(prepares) != 1 {
				t.Fatalf("expected one prepare, got %d", len(prepares))
			}
			var prepared map[string]any
			if decodeError := json.Unmarshal([]byte(prepares[0].body), &prepared); decodeError != nil {
				t.Fatal(decodeError)
			}
			if prepared["region"] != testCase.region || prepared["data_boundary"] != testCase.boundary {
				t.Errorf("prepare placed it in %v/%v, want %s/%s", prepared["region"], prepared["data_boundary"], testCase.region, testCase.boundary)
			}
		})
	}
}

// reviews confirm shows the stored review and confirms its digest; a digest
// that is not the review's is refused without confirming.
func TestServicesReviewsConfirm(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "", "reviews", "confirm", fakeServiceReviewID, "--digest", "sha256:"+strings.Repeat("f", 64), "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "not the plan you reviewed") {
		t.Fatalf("expected a digest mismatch refusal, got %v", runError)
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
		t.Fatalf("a mismatched digest was confirmed")
	}

	stdout, _, runError := runServicesCommand(t, platform, "y\n", "reviews", "confirm", fakeServiceReviewID, "--digest", fakeServiceReviewDigest)
	if runError != nil {
		t.Fatal(runError)
	}
	confirms := platform.requests(http.MethodPost, "/confirm")
	if len(confirms) != 1 || !strings.Contains(confirms[0].body, fakeServiceReviewDigest) {
		t.Fatalf("expected one confirm with the reviewed digest, got %+v", confirms)
	}
	if !strings.Contains(stdout, "is being set up") {
		t.Errorf("unexpected output:\n%s", stdout)
	}
}

// get follows the inventory's cursor past an empty first page to find the
// service by name, and shows health, readiness and connection - with the
// Secret's name and keys, never a credential value, in any output format.
func TestServicesGetShowsConnectionButNeverSecretValues(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, stderr, runError := runServicesCommand(t, platform, "", "get", "orders-db")
	if runError != nil {
		t.Fatal(runError)
	}
	for _, expected := range []string{"verification_pending", "Health:      serving", "Readiness:   ready - canary logged in",
		"postgresql-rw.orders-db.svc:5432", "Secret:    postgresql-app (keys: username, password, fqdn-uri)",
		"ankra cluster get secrets postgresql-app -n orders-db", fakeServiceConsumerID} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("get output lacks %q:\n%s", expected, stdout)
		}
	}
	if strings.Contains(stdout+stderr, fakeServiceSecretValue) {
		t.Fatalf("a credential value reached the output:\n%s%s", stdout, stderr)
	}
	// The fake serves no cluster listing: the ids shown for want of names
	// are said to be that, once, on stderr.
	if strings.Count(stderr, "Note: cluster names could not be read") != 1 {
		t.Errorf("a failed cluster name lookup must be noted once on stderr:\n%s", stderr)
	}
	if pages := platform.requests(http.MethodGet, "/service-admission/instances"); len(pages) != 2 {
		t.Errorf("the name lookup must follow next_cursor past the empty first page, read %d pages", len(pages))
	}

	for _, format := range []string{"json", "yaml"} {
		stdout, stderr, runError = runServicesCommand(t, platform, "", "get", fakeServiceInstanceID, "-o", format)
		if runError != nil {
			t.Fatal(runError)
		}
		if strings.Contains(stdout+stderr, fakeServiceSecretValue) {
			t.Fatalf("-o %s: a credential value reached the output:\n%s", format, stdout)
		}
		if !strings.Contains(stdout, "postgresql-app") {
			t.Errorf("-o %s must still carry the Secret's name:\n%s", format, stdout)
		}
	}

	stdout, stderr, runError = runServicesCommand(t, platform, "", "list", "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	if strings.Contains(stdout+stderr, fakeServiceSecretValue) {
		t.Fatalf("list: a credential value reached the output:\n%s", stdout)
	}
	var listed []client.ServiceInstance
	if decodeError := json.Unmarshal([]byte(stdout), &listed); decodeError != nil || len(listed) != 1 || listed[0].Name != "orders-db" {
		t.Fatalf("list -o json: %v %+v", decodeError, listed)
	}
}

// list --application resolves a name the way every per-application command
// does and hands the platform the id on every page of the walk: a filter on
// the first page only would let the rest of the walk list the whole
// organisation. The table says which of the application's environments each
// service serves, and names the cluster from the instance read without a
// cluster listing.
func TestServicesListByApplicationFiltersOnThePlatform(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, stderr, runError := runServicesCommand(t, platform, "", "list", "--application", "storefront")
	if runError != nil {
		t.Fatalf("services list --application: %v\n%s", runError, stderr)
	}
	pages := platform.requests(http.MethodGet, "/service-admission/instances")
	if len(pages) != 2 {
		t.Fatalf("the walk must follow next_cursor past the empty first page, read %d pages", len(pages))
	}
	for _, page := range pages {
		query, _ := url.ParseQuery(page.query)
		if query.Get("application_id") != fakeServiceApplication {
			t.Errorf("every page must carry the application filter, got %q", page.query)
		}
	}
	output := stripANSICodes(stdout)
	if header := strings.SplitN(output, "\n", 3)[1]; !strings.Contains(header, "USED BY") {
		t.Errorf("--application adds a Used by column: %q", header)
	}
	row := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "orders-db") {
			row = line
		}
	}
	for _, expected := range []string{"prod", "production", fakeServiceInstanceID} {
		if !strings.Contains(row, expected) {
			t.Errorf("the row should contain %q: %q", expected, row)
		}
	}
	if strings.Contains(stderr, "cluster names could not be read") {
		t.Errorf("the instance read names its cluster; no cluster listing is needed:\n%s", stderr)
	}
}

// A service consumer whose application is not deployed in its namespace yet
// has no environment: Used by names the namespace instead.
func TestServiceUsedByNamesTheNamespaceWithoutAnEnvironment(t *testing.T) {
	instance := client.ServiceInstance{Consumers: []client.ServiceInstanceConsumer{
		{ApplicationID: fakeServiceApplication, Namespace: "orders",
			Environment: &client.ServiceConsumerEnvironment{Name: "production", Role: "production"}},
		{ApplicationID: fakeServiceApplication, Namespace: "orders-staging"},
		{ApplicationID: "0a000000-0000-4000-8000-0000000000ff", Namespace: "other",
			Environment: &client.ServiceConsumerEnvironment{Name: "elsewhere"}},
	}}
	if got := serviceUsedBy(instance, fakeServiceApplication); got != "production, namespace orders-staging" {
		t.Errorf("serviceUsedBy = %q", got)
	}
	if got := serviceUsedBy(client.ServiceInstance{}, fakeServiceApplication); got != "-" {
		t.Errorf("no consumer of the application reads -, got %q", got)
	}
}

// An application no service serves answers an empty listing, which says so
// and where to add one, and exits 0. -o json is an empty list.
func TestServicesListByApplicationSaysWhenNoneServeIt(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	const otherApplication = "0a000000-0000-4000-8000-0000000000ff"
	stdout, _, runError := runServicesCommand(t, platform, "", "list", "--application", otherApplication)
	if runError != nil {
		t.Fatalf("services list --application: %v", runError)
	}
	if !strings.Contains(stdout, "No managed services serve application "+otherApplication) ||
		!strings.Contains(stdout, "ankra services setup") {
		t.Errorf("an empty listing should say so and where to add one:\n%s", stdout)
	}
	stdout, _, runError = runServicesCommand(t, platform, "", "list", "--application", otherApplication, "-o", "json")
	if runError != nil {
		t.Fatalf("services list --application -o json: %v", runError)
	}
	if strings.TrimSpace(stdout) != "[]" {
		t.Errorf("-o json of an empty listing is [], got %q", stdout)
	}
}

// A platform that ignores the application filter answers every service;
// the command keeps only those whose plan serves the application, rather
// than listing the whole organisation as if it did.
func TestServicesListByApplicationChecksTheFilterItself(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.ignoreInstanceFilters = true
	const otherApplication = "0a000000-0000-4000-8000-0000000000ff"
	stdout, _, runError := runServicesCommand(t, platform, "", "list", "--application", otherApplication)
	if runError != nil {
		t.Fatalf("services list --application: %v", runError)
	}
	if strings.Contains(stdout, "orders-db") || !strings.Contains(stdout, "No managed services serve application "+otherApplication) {
		t.Errorf("a service that does not serve the application must not be listed:\n%s", stdout)
	}
	stdout, _, runError = runServicesCommand(t, platform, "", "list", "--application", fakeServiceApplication)
	if runError != nil || !strings.Contains(stdout, "orders-db") {
		t.Errorf("the service that serves the application stays listed: %v\n%s", runError, stdout)
	}
}

// An application reference that names nothing is refused before the
// inventory is read: an unknown name exits 3, an empty one exits 2.
func TestServicesListByApplicationRefusesAnUnknownApplication(t *testing.T) {
	cases := []struct {
		reference string
		wantExit  int
		wantText  string
	}{
		{"checkout", exitNotFound, `no application named "checkout"`},
		{"  ", exitUsage, "an application id or name is required"},
	}
	for _, testCase := range cases {
		platform := newFakeServicesPlatform(t)
		_, _, runError := runServicesCommand(t, platform, "", "list", "--application", testCase.reference)
		if runError == nil {
			t.Fatalf("--application %q: expected an error", testCase.reference)
		}
		if got := exitCodeFor(runError); got != testCase.wantExit || !strings.Contains(runError.Error(), testCase.wantText) {
			t.Errorf("--application %q: exit %d %q, want exit %d with %q", testCase.reference, got, runError.Error(), testCase.wantExit, testCase.wantText)
		}
		if pages := platform.requests(http.MethodGet, "/service-admission/instances"); len(pages) != 0 {
			t.Errorf("--application %q: the inventory must not be read", testCase.reference)
		}
	}
}

// --cluster travels to the platform as its cluster filter, on every page.
func TestServicesListByClusterFiltersOnThePlatform(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	if _, _, runError := runServicesCommand(t, platform, "", "list", "--cluster", fakeServiceClusterID); runError != nil {
		t.Fatalf("services list --cluster: %v", runError)
	}
	pages := platform.requests(http.MethodGet, "/service-admission/instances")
	if len(pages) != 2 {
		t.Fatalf("expected the two-page walk, read %d pages", len(pages))
	}
	for _, page := range pages {
		query, _ := url.ParseQuery(page.query)
		if query.Get("cluster_id") != fakeServiceClusterID || query.Has("application_id") {
			t.Errorf("every page must carry the cluster filter and nothing else, got %q", page.query)
		}
	}
}

// Retiring deletes data: without --acknowledge-data-loss neither delete nor
// retirements confirm prepares or confirms anything.
func TestServicesRetirementRequiresTheAcknowledgement(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	for _, arguments := range [][]string{
		{"delete", "orders-db", "--yes"},
		{"retirements", "confirm", "orders-db", fakeServiceRetirementID, "--digest", fakeServiceRetirementDigest, "--yes"},
	} {
		_, _, runError := runServicesCommand(t, platform, "y\n", arguments...)
		if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--acknowledge-data-loss") {
			t.Fatalf("%v: expected a usage refusal naming --acknowledge-data-loss, got %v", arguments, runError)
		}
	}
	_, _, runError := runServicesCommand(t, platform, "", "delete", "orders-db", "--acknowledge-data-loss", "--yes", "--wait", "--timeout", "0s")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--timeout") {
		t.Fatalf("a --wait that cannot wait must be a usage error, got %v", runError)
	}
	if prepares := platform.requests(http.MethodPost, "/retirements"); len(prepares) != 0 {
		t.Errorf("an unacknowledged retirement was prepared")
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
		t.Errorf("an unacknowledged retirement was confirmed")
	}
}

// delete prepares the retirement for the generation and consumers it read,
// with the acknowledgement, shows the plan, and confirms its digest.
func TestServicesDeletePreparesAndConfirmsTheRetirement(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "", "delete", "orders-db", "--acknowledge-data-loss", "--yes")
	if runError != nil {
		t.Fatal(runError)
	}
	prepares := platform.requests(http.MethodPost, "/retirements")
	if len(prepares) != 1 {
		t.Fatalf("expected one prepare, got %d", len(prepares))
	}
	if prepares[0].body != `{"expected_generation":2,"disconnect_consumer_ids":["`+fakeServiceConsumerID+`"],"acknowledge_data_loss":true}` {
		t.Errorf("unexpected prepare body %s", prepares[0].body)
	}
	confirms := platform.requests(http.MethodPost, "/retirements/"+fakeServiceRetirementID+"/confirm")
	if len(confirms) != 1 || confirms[0].body != `{"digest":"`+fakeServiceRetirementDigest+`"}` {
		t.Fatalf("expected one confirm with the reviewed digest, got %+v", confirms)
	}
	for _, expected := range []string{"Stack orders-db", "addon postgresql-orders-db", "Namespace orders-db, with everything in it",
		"deployments/debug-shell", "postgresql-1  created by the service, storage class local-path, volume pvc-123, reclaim Retain",
		"cloudnative-pg (CloudNativePG operator)", "no export is offered", fakeServiceRetirementDigest, "is being retired"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("delete output lacks %q:\n%s", expected, stdout)
		}
	}
}

func TestServicesDeleteDeclinedRemovesNothing(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, stderr, runError := runServicesCommand(t, platform, "n\n", "delete", "orders-db", "--acknowledge-data-loss")
	if !errors.Is(runError, errCancelled) {
		t.Fatalf("expected cancelled, got %v", runError)
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
		t.Fatalf("a declined retirement was confirmed")
	}
	if !strings.Contains(stderr, "ankra services retirements confirm "+fakeServiceInstanceID+" "+fakeServiceRetirementID) {
		t.Errorf("a declined delete must say how to confirm the open review later:\n%s", stderr)
	}
}

// A retirement the caller prepared earlier whose answer was lost is found by
// walking the pending listing to its end - past an empty page that still
// carries a cursor - and resumed instead of preparing another.
func TestServicesDeleteResumesAPendingRetirementAcrossPages(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.pendingPages = [][]map[string]any{{}, {fakeServiceRetirement("pending")}}
	stdout, _, runError := runServicesCommand(t, platform, "", "delete", "orders-db", "--acknowledge-data-loss", "--yes")
	if runError != nil {
		t.Fatal(runError)
	}
	listings := platform.requests(http.MethodGet, "/retirements")
	if len(listings) != 2 || !strings.Contains(listings[0].query, "state=pending") || !strings.Contains(listings[1].query, "after=cursor-1") {
		t.Fatalf("expected the pending listing to follow next_cursor, got %+v", listings)
	}
	if prepares := platform.requests(http.MethodPost, "/retirements"); len(prepares) != 0 {
		t.Fatalf("a pending retirement was found, yet another was prepared")
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 1 {
		t.Fatalf("expected the resumed retirement to be confirmed once, got %d", len(confirms))
	}
	if !strings.Contains(stdout, "Resuming the retirement review you prepared") {
		t.Errorf("a resumed review must say so:\n%s", stdout)
	}

	platform = newFakeServicesPlatform(t)
	platform.pendingPages = [][]map[string]any{{}, {fakeServiceRetirement("pending")}}
	if _, _, runError = runServicesCommand(t, platform, "", "delete", "orders-db", "--acknowledge-data-loss", "--new-review", "--review-only"); runError != nil {
		t.Fatal(runError)
	}
	if prepares := platform.requests(http.MethodPost, "/retirements"); len(prepares) != 1 {
		t.Fatalf("--new-review must prepare a fresh retirement, got %d prepares", len(prepares))
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
		t.Fatalf("--review-only confirmed a retirement")
	}
}

func TestServicesRetirementsListFollowsTheCursor(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	second := fakeServiceRetirement("pending")
	second["id"] = "7d3c1f0a-5b2e-4c9d-8e1f-0a2b3c4d5e70"
	platform.pendingPages = [][]map[string]any{{fakeServiceRetirement("pending")}, {}, {second}}
	stdout, _, runError := runServicesCommand(t, platform, "", "retirements", "list", "orders-db", "--state", "pending", "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	var listed []client.ServiceRetirement
	if decodeError := json.Unmarshal([]byte(stdout), &listed); decodeError != nil {
		t.Fatalf("%v\n%s", decodeError, stdout)
	}
	if len(listed) != 2 || listed[1].ID != "7d3c1f0a-5b2e-4c9d-8e1f-0a2b3c4d5e70" {
		t.Fatalf("expected both pages' retirements, got %+v", listed)
	}
	if listings := platform.requests(http.MethodGet, "/retirements"); len(listings) != 3 {
		t.Errorf("expected three pages read, got %d", len(listings))
	}
}

// --wait follows a confirmed retirement until it settles and names every
// volume still holding the service's data.
func TestServicesDeleteWaitsForTheRetirementToSettle(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.retirementStates = []string{"in_progress", "in_progress", "settled"}
	stdout, stderr, runError := runServicesCommand(t, platform, "", "delete", "orders-db", "--acknowledge-data-loss", "--yes", "--wait")
	if runError != nil {
		t.Fatal(runError)
	}
	if !strings.Contains(stdout, "settled") || !strings.Contains(stdout, "STILL HOLDING DATA: pvc-123") {
		t.Errorf("a settled retirement must name the retained volume:\n%s", stdout)
	}
	if !strings.Contains(stderr, "in_progress (dispose_data)") {
		t.Errorf("--wait must report progress on stderr:\n%s", stderr)
	}

	platform = newFakeServicesPlatform(t)
	platform.retirementStates = []string{"settled"}
	platform.settledOutcome = "disposal_unknown"
	_, _, runError = runServicesCommand(t, platform, "", "delete", "orders-db", "--acknowledge-data-loss", "--yes", "--wait")
	if runError == nil || !strings.Contains(runError.Error(), "disposal_unknown") || exitCodeFor(runError) != exitError {
		t.Fatalf("a retirement that did not settle retired must fail, got %v", runError)
	}
}

// retirements confirm needs the acknowledgement too, refuses a digest that is
// not the review's, and otherwise confirms the stored digest.
func TestServicesRetirementsConfirm(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "", "retirements", "confirm", "orders-db", fakeServiceRetirementID,
		"--acknowledge-data-loss", "--digest", "sha256:"+strings.Repeat("0", 64), "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "not the plan you reviewed") {
		t.Fatalf("expected a digest mismatch refusal, got %v", runError)
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
		t.Fatal("a mismatched digest was confirmed")
	}
	stdout, _, runError := runServicesCommand(t, platform, "y\n", "retirements", "confirm", "orders-db", fakeServiceRetirementID,
		"--acknowledge-data-loss", "--digest", fakeServiceRetirementDigest)
	if runError != nil {
		t.Fatal(runError)
	}
	confirms := platform.requests(http.MethodPost, "/confirm")
	if len(confirms) != 1 || !strings.Contains(confirms[0].body, fakeServiceRetirementDigest) {
		t.Fatalf("expected one confirm with the reviewed digest, got %+v", confirms)
	}
	if !strings.Contains(stdout, "is being retired") {
		t.Errorf("unexpected output:\n%s", stdout)
	}
}

func TestServicesPolicySetNeedsTheRevisionToReplace(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "", "policy", "set", "--cluster", fakeServiceClusterID,
		"--region", "eu-west-1", "--data-boundary", "eu")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--revision 3") {
		t.Fatalf("replacing a policy without --revision must be refused naming it, got %v", runError)
	}
	if puts := platform.requests(http.MethodPut, "/cluster-policies/"+fakeServiceClusterID); len(puts) != 0 {
		t.Fatal("a policy was replaced without its revision")
	}
	if _, _, runError = runServicesCommand(t, platform, "", "policy", "set", "--cluster", fakeServiceClusterID,
		"--region", "eu-west-1", "--data-boundary", "eu", "--revision", "3"); runError != nil {
		t.Fatal(runError)
	}
	puts := platform.requests(http.MethodPut, "/cluster-policies/"+fakeServiceClusterID)
	if len(puts) != 1 || puts[0].body != `{"expected_revision":3,"region":"eu-west-1","data_boundary":"eu","local_only":false}` {
		t.Fatalf("unexpected policy write %+v", puts)
	}

	platform = newFakeServicesPlatform(t)
	platform.policyMissing = true
	_, _, runError = runServicesCommand(t, platform, "", "policy", "get", "--cluster", fakeServiceClusterID)
	if runError == nil || exitCodeFor(runError) != exitNotFound {
		t.Fatalf("a missing policy reads as not found, got %v", runError)
	}
	if _, _, runError = runServicesCommand(t, platform, "", "policy", "set", "--cluster", fakeServiceClusterID,
		"--region", "eu-north-1", "--data-boundary", "eu"); runError != nil {
		t.Fatalf("creating a policy needs no revision: %v", runError)
	}
}

// The global --org flag scopes these commands like every other: the
// override header rides every managed-services request.
func TestServicesHonourTheOrgFlag(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	withTempHome(t)
	setMockClient(t, platform.client())
	servicesCommand, _, findError := rootCmd.Find([]string{"services", "packages", "list"})
	if findError != nil {
		t.Fatal(findError)
	}
	t.Cleanup(func() {
		resetServicesFlags(rootCmd)
		resetServicesFlags(servicesCommand)
	})
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stdout)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"services", "packages", "list", "--org", fakeServiceOrganisation, "-o", "json"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
		rootCmd.SetArgs(nil)
	})
	if runError := rootCmd.Execute(); runError != nil {
		t.Fatalf("%v\n%s", runError, stdout.String())
	}
	catalogue := platform.requests(http.MethodGet, "/api/v1/org/service-packages")
	if len(catalogue) == 0 {
		t.Fatal("the catalogue was not read")
	}
	for _, request := range catalogue {
		if request.organisation != fakeServiceOrganisation {
			t.Errorf("expected X-Ankra-Organisation-Id %s, got %q", fakeServiceOrganisation, request.organisation)
		}
	}
}

func resetServicesFlags(command *cobra.Command) {
	command.Flags().VisitAll(resetFlagToDefault)
	command.PersistentFlags().VisitAll(resetFlagToDefault)
}

func TestCollectServicePagesStopsOnARepeatedCursor(t *testing.T) {
	cursor := "same"
	calls := 0
	_, collectError := collectServicePages(func(string) ([]int, *string, error) {
		calls++
		return []int{calls}, &cursor, nil
	})
	if collectError == nil || !strings.Contains(collectError.Error(), "twice") || calls != 2 {
		t.Fatalf("expected a repeated cursor to stop the walk after two pages, got %v after %d", collectError, calls)
	}
}

func TestResolveServiceInstanceRefusesAnAmbiguousName(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	previousClient := apiClient
	twin := fakeServiceInstance()
	twin["id"] = "c9b96bb8-0000-4000-8000-0000000000aa"
	twin["cluster_id"] = "c1a2b3c4-0000-4000-8000-0000000000bb"
	apiClient = &twinInstancesClient{Client: platform.client(), items: []map[string]any{fakeServiceInstance(), twin}}
	t.Cleanup(func() { apiClient = previousClient })
	command := newServicesGetCommand()
	_, resolveError := resolveServiceInstance(command, "orders-db")
	if resolveError == nil || exitCodeFor(resolveError) != exitUsage || !strings.Contains(resolveError.Error(), "--cluster") {
		t.Fatalf("expected an ambiguity refusal naming --cluster, got %v", resolveError)
	}
}

// twinInstancesClient answers the inventory with a fixed page.
type twinInstancesClient struct {
	*client.Client
	items []map[string]any
}

func (twins *twinInstancesClient) ListServiceInstances(_ context.Context, _ client.ServiceInstanceListOptions) (*client.ServiceInstancePage, error) {
	encoded, _ := json.Marshal(map[string]any{"items": twins.items, "next_cursor": nil})
	var page client.ServiceInstancePage
	return &page, json.Unmarshal(encoded, &page)
}

// A confirmation the platform refuses as a conflict (the review expired, or
// what it was resolved from changed) says that nothing was set up and how to
// review the current plan.
func TestServicesSetupExplainsAConfirmConflict(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.reviewChanged = true
	_, _, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID, "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "Service review has changed") ||
		!strings.Contains(runError.Error(), "nothing was set up") || !strings.Contains(runError.Error(), "ankra services setup orders-db") {
		t.Fatalf("expected the platform's conflict with the way forward, got %v", runError)
	}
	_, _, runError = runServicesCommand(t, platform, "", "reviews", "confirm", fakeServiceReviewID, "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "nothing was set up") {
		t.Fatalf("reviews confirm must explain the conflict too, got %v", runError)
	}
}

// bind names an existing binding of the same namespace instead of letting
// the platform answer a bare conflict, and creates a new one otherwise.
func TestServicesConsumersBindNamesAnExistingBinding(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "", "consumers", "bind",
		"--application", fakeServiceApplication, "--cluster", fakeServiceClusterID, "--namespace", "orders")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), fakeServiceConsumerID) ||
		!strings.Contains(runError.Error(), "--revision 4") {
		t.Fatalf("expected a refusal naming the existing binding and its revision, got %v", runError)
	}
	if binds := platform.requests(http.MethodPost, "/service-admission/consumers"); len(binds) != 0 {
		t.Fatal("an existing binding was bound again")
	}

	stdout, _, runError := runServicesCommand(t, platform, "", "consumers", "bind",
		"--application", fakeServiceApplication, "--cluster", fakeServiceClusterID, "--namespace", "billing", "--allow-planned")
	if runError != nil {
		t.Fatal(runError)
	}
	binds := platform.requests(http.MethodPost, "/service-admission/consumers")
	if len(binds) != 1 || binds[0].body != `{"application_id":"`+fakeServiceApplication+`","cluster_id":"`+fakeServiceClusterID+
		`","namespace":"billing","allow_planned":true,"local_only":false,"expected_revision":0}` {
		t.Fatalf("unexpected bind requests %+v", binds)
	}
	if !strings.Contains(stdout, "--consumer 5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c99") {
		t.Errorf("bind must print the consumer id setup takes:\n%s", stdout)
	}

	if _, _, runError = runServicesCommand(t, platform, "", "consumers", "bind",
		"--application", fakeServiceApplication, "--cluster", fakeServiceClusterID, "--namespace", "orders", "--revision", "4", "--local-only"); runError != nil {
		t.Fatalf("changing an existing binding with its revision must go through: %v", runError)
	}
	// The change names only --local-only: the binding's allow_planned (true)
	// is kept rather than reset to the flag's default.
	binds = platform.requests(http.MethodPost, "/service-admission/consumers")
	if len(binds) != 2 || binds[1].body != `{"application_id":"`+fakeServiceApplication+`","cluster_id":"`+fakeServiceClusterID+
		`","namespace":"orders","allow_planned":true,"local_only":true,"expected_revision":4}` {
		t.Fatalf("a change must keep settings it does not name, got %+v", binds)
	}
}

// --wait waits only on a running retirement: one that is not running is
// reported at once, and one still running when the budget ends exits with
// the wait code.
func TestServicesRetirementWaitStopsOnStatesItCannotWaitOut(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.retirementStates = []string{"expired"}
	_, _, runError := runServicesCommand(t, platform, "", "delete", "orders-db", "--acknowledge-data-loss", "--yes", "--wait")
	if runError == nil || exitCodeFor(runError) != exitError || !strings.Contains(runError.Error(), "not running") {
		t.Fatalf("a retirement that is not running must be reported at once, got %v", runError)
	}
	if platform.retirementReads != 1 {
		t.Errorf("expected one read, got %d", platform.retirementReads)
	}

	platform = newFakeServicesPlatform(t)
	platform.retirementStates = []string{"in_progress"}
	_, _, runError = runServicesCommand(t, platform, "", "delete", "orders-db", "--acknowledge-data-loss", "--yes", "--wait", "--timeout", "30ms")
	if runError == nil || exitCodeFor(runError) != exitWaitTimeout || !strings.Contains(runError.Error(), "still in_progress") {
		t.Fatalf("a retirement still running at the deadline must exit with the wait code, got %v", runError)
	}
}

// retirements confirm confirms only a review the platform says is pending.
func TestServicesRetirementsConfirmOnlyConfirmsAPendingReview(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.retirementStates = []string{"withdrawn"}
	_, _, runError := runServicesCommand(t, platform, "", "retirements", "confirm", "orders-db", fakeServiceRetirementID,
		"--acknowledge-data-loss", "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "only a pending retirement review can be confirmed") {
		t.Fatalf("expected a refusal for a state that is not pending, got %v", runError)
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
		t.Fatal("a retirement that is not pending was confirmed")
	}
}

// A binding listing that cannot be read does not block the bind, but says
// that the duplicate check was skipped.
func TestServicesConsumersBindNotesASkippedDuplicateCheck(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.consumersUnreadable = true
	_, stderr, runError := runServicesCommand(t, platform, "", "consumers", "bind",
		"--application", fakeServiceApplication, "--cluster", fakeServiceClusterID, "--namespace", "orders")
	if runError != nil {
		t.Fatal(runError)
	}
	if binds := platform.requests(http.MethodPost, "/service-admission/consumers"); len(binds) != 1 {
		t.Fatalf("the bind must still be sent, got %d", len(binds))
	}
	if !strings.Contains(stderr, "is already bound was not checked") {
		t.Errorf("a skipped duplicate check must be said on stderr:\n%s", stderr)
	}
}

// An id-shaped reference the platform does not know is searched as a name
// too (service names may look like ids), and the answer names both.
func TestServicesGetUnknownIDSaysIDOrName(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "", "get", "abcdef12-0000-4000-8000-000000000099")
	if runError == nil || exitCodeFor(runError) != exitNotFound || !strings.Contains(runError.Error(), "no service with the id or name") {
		t.Fatalf("expected a not-found naming id or name, got %v", runError)
	}
	if reads := platform.requests(http.MethodGet, "/instances/abcdef12-0000-4000-8000-000000000099"); len(reads) != 1 {
		t.Errorf("the id must be read as an id first, got %d reads", len(reads))
	}
}

// Every secret input the chosen mode uses must be supplied, and only those.
func TestServicesSetupChecksRequiredSecretInputs(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.packageSecrets = []any{
		map[string]any{"name": "admin_password", "modes": []any{"customer"}},
		map[string]any{"name": "hosted_token", "modes": []any{"hosted"}},
	}
	base := []string{"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID, "--yes"}
	_, _, runError := runServicesCommand(t, platform, "", base...)
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--secret-reference admin_password=") {
		t.Fatalf("a missing required secret input must be refused naming it, got %v", runError)
	}
	_, _, runError = runServicesCommand(t, platform, "", append(base,
		"--secret-reference", "admin_password=9e000000-0000-4000-8000-000000000017",
		"--secret-reference", "hosted_token=9e000000-0000-4000-8000-000000000018")...)
	if runError == nil || !strings.Contains(runError.Error(), "not used in customer mode") {
		t.Fatalf("a secret input the mode does not use must be refused, got %v", runError)
	}
	if prepares := platform.requests(http.MethodPost, "/reviews"); len(prepares) != 0 {
		t.Fatal("a refused setup stored a review")
	}
	if _, _, runError = runServicesCommand(t, platform, "", append(base,
		"--secret-reference", "admin_password=9e000000-0000-4000-8000-000000000017")...); runError != nil {
		t.Fatalf("a setup that supplies the mode's secret inputs must go through: %v", runError)
	}
	prepares := platform.requests(http.MethodPost, "/reviews")
	if len(prepares) != 1 || !strings.Contains(prepares[0].body, `"secret_references":{"admin_password":"9e000000-0000-4000-8000-000000000017"}`) {
		t.Fatalf("unexpected prepare %+v", prepares)
	}
}

// A change to a binding whose current settings cannot be read is refused
// unless both settings are named, since one left out would be reset.
func TestServicesConsumersBindRefusesABlindChange(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.consumersUnreadable = true
	_, _, runError := runServicesCommand(t, platform, "", "consumers", "bind",
		"--application", fakeServiceApplication, "--cluster", fakeServiceClusterID, "--namespace", "orders", "--revision", "4", "--local-only")
	if runError == nil || !strings.Contains(runError.Error(), "pass both --allow-planned") {
		t.Fatalf("expected a refusal asking for both settings, got %v", runError)
	}
	if binds := platform.requests(http.MethodPost, "/service-admission/consumers"); len(binds) != 0 {
		t.Fatal("a blind change was sent")
	}
	if _, _, runError = runServicesCommand(t, platform, "", "consumers", "bind",
		"--application", fakeServiceApplication, "--cluster", fakeServiceClusterID, "--namespace", "orders", "--revision", "4",
		"--local-only", "--allow-planned=false"); runError != nil {
		t.Fatalf("a change naming both settings must go through: %v", runError)
	}
}

// --cluster holds for a service named by id as it does for a name.
func TestServicesGetByIDHonoursCluster(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "", "get", fakeServiceInstanceID, "--cluster", "c1a2b3c4-0000-4000-8000-0000000000bb")
	if runError == nil || exitCodeFor(runError) != exitNotFound || !strings.Contains(runError.Error(), "runs on cluster "+fakeServiceClusterID) {
		t.Fatalf("a service on another cluster must not be returned, got %v", runError)
	}
	if _, _, runError = runServicesCommand(t, platform, "", "get", fakeServiceInstanceID, "--cluster", fakeServiceClusterID); runError != nil {
		t.Fatalf("the matching cluster must resolve: %v", runError)
	}
}

// unbind reads the binding's current revision, asks first, and removes the
// binding at that revision; a decline removes nothing.
func TestServicesConsumersUnbindRemovesAtTheCurrentRevision(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "n\n", "consumers", "unbind", fakeServiceConsumerID)
	if !errors.Is(runError, errCancelled) {
		t.Fatalf("expected a declined unbind to cancel, got %v", runError)
	}
	if deletes := platform.requests(http.MethodDelete, "/consumers/"+fakeServiceConsumerID); len(deletes) != 0 {
		t.Fatal("a declined unbind removed the binding")
	}

	stdout, stderr, runError := runServicesCommand(t, platform, "y\n", "consumers", "unbind", fakeServiceConsumerID)
	if runError != nil {
		t.Fatal(runError)
	}
	deletes := platform.requests(http.MethodDelete, "/consumers/"+fakeServiceConsumerID)
	if len(deletes) != 1 || deletes[0].query != "expected_revision=4" {
		t.Fatalf("expected one removal at the read revision 4, got %+v", deletes)
	}
	if !strings.Contains(stderr, "namespace orders") || !strings.Contains(stderr, "revision 4") {
		t.Errorf("the prompt must say what is removed and at which revision:\n%s", stderr)
	}
	if !strings.Contains(stdout, "Consumer binding "+fakeServiceConsumerID+" removed.") {
		t.Errorf("unexpected output:\n%s", stdout)
	}

	stdout, _, runError = runServicesCommand(t, platform, "", "consumers", "unbind", fakeServiceConsumerID, "--yes", "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	var removed map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &removed); decodeError != nil || removed["removed"] != true || removed["revision"] != float64(4) {
		t.Fatalf("-o json must be the removal record: %v %s", decodeError, stdout)
	}
}

// A binding whose application was removed no longer reads by id: without
// --revision the command says how to find it; with --revision it is removed.
func TestServicesConsumersUnbindAnOrphanedBinding(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "", "consumers", "unbind", fakeServiceOrphanConsumerID, "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "--revision") ||
		!strings.Contains(runError.Error(), "ankra services consumers list --application") {
		t.Fatalf("an unreadable binding must point at --revision and the listing, got %v", runError)
	}
	if deletes := platform.requests(http.MethodDelete, "/consumers/"); len(deletes) != 0 {
		t.Fatal("a binding was removed at a guessed revision")
	}
	if _, _, runError = runServicesCommand(t, platform, "", "consumers", "unbind", fakeServiceOrphanConsumerID, "--revision", "2", "--yes"); runError != nil {
		t.Fatalf("an orphaned binding must be removable at its revision: %v", runError)
	}
	deletes := platform.requests(http.MethodDelete, "/consumers/"+fakeServiceOrphanConsumerID)
	if len(deletes) != 1 || deletes[0].query != "expected_revision=2" {
		t.Fatalf("unexpected removals %+v", deletes)
	}
}

// The platform's refusals reach the user verbatim with the way forward: a
// binding a service uses names that service; a stale revision says how to
// read the current one.
func TestServicesConsumersUnbindRelaysRefusals(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.unbindInUse = true
	_, _, runError := runServicesCommand(t, platform, "", "consumers", "unbind", fakeServiceConsumerID, "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "A service still uses this binding; retire the service before removing it") ||
		!strings.Contains(runError.Error(), "orders-db ("+fakeServiceInstanceID+")") ||
		!strings.Contains(runError.Error(), "ankra services delete") {
		t.Fatalf("expected the in-use refusal verbatim, naming the service, got %v", runError)
	}

	platform = newFakeServicesPlatform(t)
	_, _, runError = runServicesCommand(t, platform, "", "consumers", "unbind", fakeServiceConsumerID, "--revision", "3", "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "Service configuration changed") ||
		!strings.Contains(runError.Error(), "without --revision") {
		t.Fatalf("expected the stale-revision refusal with how to read the current one, got %v", runError)
	}
	for _, arguments := range [][]string{{"not-an-id"}, {fakeServiceConsumerID, "--revision", "0"}} {
		_, _, runError = runServicesCommand(t, platform, "", append([]string{"consumers", "unbind"}, append(arguments, "--yes")...)...)
		if runError == nil || exitCodeFor(runError) != exitUsage {
			t.Errorf("%v: expected a usage refusal, got %v", arguments, runError)
		}
	}
}

// endlessClusterPages answers every cluster page full, as an organisation
// with more clusters than the name lookup reads would.
type endlessClusterPages struct{ baseMock }

func (m *endlessClusterPages) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	clusters := make([]client.ClusterListItem, pageSize)
	for index := range clusters {
		clusters[index] = client.ClusterListItem{ID: fmt.Sprintf("cluster-%d-%d", page, index), Name: fmt.Sprintf("name-%d-%d", page, index)}
	}
	return &client.ClusterListResponse{Result: clusters}, nil
}

// A cluster lookup that stops at its page cap says so on stderr, so an id
// shown for want of a name is not read as a cluster that is gone; a lookup
// that read every cluster says nothing.
func TestServiceNamesSaysWhenTheClusterLookupStopsAtItsCap(t *testing.T) {
	previousClient := apiClient
	t.Cleanup(func() { apiClient = previousClient })

	apiClient = &endlessClusterPages{}
	command := &cobra.Command{}
	command.SetContext(context.Background())
	var notes bytes.Buffer
	command.SetErr(&notes)
	names := newServiceNames(command)
	if shown := names.cluster("beyond-the-cap"); shown != "beyond-the-cap" {
		t.Fatalf("an unread cluster shown as %q, expected its id", shown)
	}
	if shown := names.cluster("cluster-3-7"); shown != "name-3-7" {
		t.Fatalf("a read cluster shown as %q", shown)
	}
	if !strings.Contains(notes.String(), "only the first 2000 clusters were read for names") || strings.Count(notes.String(), "Note:") != 1 {
		t.Fatalf("notes %q", notes.String())
	}

	apiClient = &clusterListMock{clusters: []client.ClusterListItem{{ID: "only", Name: "the-only-one"}}}
	notes.Reset()
	complete := newServiceNames(command)
	if shown := complete.cluster("only"); shown != "the-only-one" || notes.Len() != 0 {
		t.Fatalf("a complete lookup: %q, notes %q", shown, notes.String())
	}
}

// The review summary shows the capacity verdict for every state the
// platform answers a prepare with, and says nothing when the platform sends
// none (ankra-t5jf5.34.7.10.1).
func TestServicesSetupShowsTheCapacityVerdict(t *testing.T) {
	review := client.ServiceReview{ID: fakeServiceReviewID, Digest: fakeServiceReviewDigest,
		Plan: fakeServiceReview(false)["plan"].(map[string]any)}
	for _, testCase := range []struct {
		capacity *client.ServiceCapacity
		want     string
	}{
		{&client.ServiceCapacity{State: "fits", Reason: "The service fits the playground's 3 CPU quota."},
			"Capacity:       fits - The service fits the playground's 3 CPU quota."},
		{&client.ServiceCapacity{State: "unknown", Reason: "The quota could not be read."},
			"Capacity:       unknown (the install may still hit the cluster's quota) - The quota could not be read."},
		{&client.ServiceCapacity{State: "unchecked", Reason: "The platform sets no quota for this cluster."},
			"Capacity:       unchecked - The platform sets no quota for this cluster."},
	} {
		review.Capacity = testCase.capacity
		var out bytes.Buffer
		printServiceReviewPlan(&out, &serviceNames{clustersLoaded: true, clusters: map[string]string{}}, "postgresql 1.1.0", review)
		if !strings.Contains(out.String(), testCase.want) {
			t.Errorf("%s: summary lacks %q:\n%s", testCase.capacity.State, testCase.want, out.String())
		}
	}
	review.Capacity = nil
	var out bytes.Buffer
	printServiceReviewPlan(&out, &serviceNames{clustersLoaded: true, clusters: map[string]string{}}, "postgresql 1.1.0", review)
	if strings.Contains(out.String(), "Capacity:") {
		t.Errorf("a platform that sends no verdict must not be shown one:\n%s", out.String())
	}
}
