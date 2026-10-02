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
}

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
		"admission_operation_id": "e0000000-0000-4000-8000-000000000009", "name": "orders-db",
		"region": "eu-north-1", "data_boundary": "eu", "generation": 2, "mode": "customer",
		"requested_destinations": []any{map[string]any{"application_id": fakeServiceApplication, "cluster_id": fakeServiceClusterID, "namespace": "orders"}},
		"consumers": []any{map[string]any{"id": fakeServiceConsumerID, "application_id": fakeServiceApplication,
			"cluster_id": fakeServiceClusterID, "namespace": "orders"}},
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
				"secrets": []any{}, "outputs": []any{map[string]any{"name": "DATABASE_ENDPOINT", "kind": "endpoint"}},
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
		if request.URL.Query().Get("application_id") != fakeServiceApplication {
			t.Errorf("consumer listing for an unexpected application: %s", request.URL.RawQuery)
		}
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"next_cursor": nil, "items": []any{
			map[string]any{"id": fakeServiceConsumerID, "application_id": fakeServiceApplication, "cluster_id": fakeServiceClusterID,
				"namespace": "orders", "allow_planned": false, "local_only": false, "revision": 4, "updated_at": "2026-10-01T00:00:00Z"},
		}})
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
		fakeServiceJSON(t, writer, http.StatusOK, map[string]any{"items": []any{fakeServiceInstance()}, "next_cursor": nil})
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
		"consumers":   {"list", "get", "bind"},
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
		{"no policy", []string{"--package", "postgresql", "--consumer", fakeServiceConsumerID}, false, exitError, "policy set"},
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

func (twins *twinInstancesClient) ListServiceInstances(_ context.Context, _ client.ServicePageOptions) (*client.ServiceInstancePage, error) {
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
}
