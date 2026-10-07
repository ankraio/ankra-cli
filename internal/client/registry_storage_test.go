package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

// Lane parity: each method hits the exact bearer route, method and status
// the platform serves, and decodes the contract's shapes.
func TestRegistryStorageLaneParity(t *testing.T) {
	type call struct {
		method string
		path   string
		body   string
	}
	var calls []call
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		bodyBytes, _ := io.ReadAll(request.Body)
		calls = append(calls, call{request.Method, request.URL.Path, string(bodyBytes)})
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer token on %s %s", request.Method, request.URL.Path)
		}
		retention := map[string]any{
			"organisation_id": "org-1", "default": map[string]any{"keep_days": 30, "keep_latest": 3},
			"platform_default": map[string]any{"keep_days": 30, "keep_latest": 3}, "source": "default",
			"updated_by": nil, "updated_at": nil,
			"rules": []any{map[string]any{"id": "r1", "repository_pattern": "ankra-ci/**", "application_id": nil,
				"keep_days": 7, "keep_latest": 2, "updated_by": "a@b.c", "updated_at": "2026-10-01T00:00:00Z"}},
			"recently_pulled_days": 0, "runs_daily_at": "14:37 UTC", "space_freed_at": "weekly, Sunday 03:00 UTC",
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/registry-storage":
			jsonResponse(t, writer, http.StatusOK, map[string]any{
				"organisation_id": "org-1", "limit_bytes": 53687091200, "default_limit_bytes": 53687091200,
				"source": "default", "reason": nil, "updated_by": nil, "updated_at": nil,
				"used_bytes": nil, "usage_status": "unknown",
				"projects": []any{map[string]any{"name": "default", "project": "org-1", "limit_bytes": 53687091200,
					"used_bytes": nil, "status": "unknown"}},
			})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/registry-storage/repositories":
			jsonResponse(t, writer, http.StatusOK, map[string]any{
				"status": "complete", "sizes_are_approximate": true,
				"repositories": []any{map[string]any{"name": "ankra-ci/api", "project": "default", "artifact_count": 42,
					"size_bytes": 123, "last_pushed_at": "", "truncated": true, "unreadable": false, "retention_rule_id": "r1"},
					map[string]any{"name": "web/site", "project": "default", "artifact_count": 3, "size_bytes": 0,
						"last_pushed_at": "", "truncated": false, "unreadable": true, "retention_rule_id": nil}},
			})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/registry-storage/retention":
			jsonResponse(t, writer, http.StatusOK, retention)
		case request.Method == http.MethodPut && request.URL.Path == "/api/v1/org/registry-storage/retention":
			jsonResponse(t, writer, http.StatusOK, map[string]any{"retention": retention, "harbor_applied": false,
				"harbor_apply_error": "registry unreachable"})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/org/registry-storage/retention/run":
			jsonResponse(t, writer, http.StatusAccepted, map[string]any{
				"projects":       []any{map[string]any{"project": "org-1", "started": true, "reason": nil}},
				"space_freed_at": "weekly, Sunday 03:00 UTC",
			})
		default:
			t.Errorf("unexpected %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusTeapot)
		}
	})
	ctx := context.Background()

	storage, getError := client.GetRegistryStorage(ctx)
	if getError != nil {
		t.Fatalf("get storage: %v", getError)
	}
	if storage.UsedBytes != nil || storage.UsageStatus != RegistryStorageUsageUnknown || storage.LimitBytes != 50*1073741824 ||
		len(storage.Projects) != 1 || storage.Projects[0].UsedBytes != nil {
		t.Fatalf("an unknown usage must decode as nil, never 0: %+v", storage)
	}

	repositories, listError := client.ListRegistryStorageRepositories(ctx)
	if listError != nil || len(repositories.Repositories) != 2 || !repositories.Repositories[0].Truncated ||
		repositories.Repositories[0].Unreadable || !repositories.Repositories[1].Unreadable ||
		repositories.Repositories[0].RetentionRuleID == nil || *repositories.Repositories[0].RetentionRuleID != "r1" ||
		!repositories.SizesAreApproximate {
		t.Fatalf("repositories: %+v %v", repositories, listError)
	}

	retention, retentionError := client.GetRegistryRetention(ctx)
	if retentionError != nil || retention.Default.KeepDays != 30 || len(retention.Rules) != 1 ||
		retention.Rules[0].RepositoryPattern != "ankra-ci/**" || retention.RunsDailyAt != "14:37 UTC" {
		t.Fatalf("retention: %+v %v", retention, retentionError)
	}

	result, updateError := client.UpdateRegistryRetention(ctx, RegistryRetentionUpdate{DefaultSet: true})
	if updateError != nil || result.HarborApplied || result.HarborApplyError == nil || result.Retention.Default.KeepLatest != 3 {
		t.Fatalf("update: %+v %v", result, updateError)
	}

	run, runError := client.RunRegistryRetention(ctx)
	if runError != nil || len(run.Projects) != 1 || !run.Projects[0].Started || run.SpaceFreedAt == "" {
		t.Fatalf("run: %+v %v", run, runError)
	}

	if len(calls) != 5 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[3].body != `{"default":null}` {
		t.Fatalf("a reset sends default null and nothing else, got %s", calls[3].body)
	}
}

// The PUT body is tri-state per part: absent leaves it unchanged, null
// resets the default, an object sets it; rules absent are unchanged and
// [] removes every rule.
func TestRegistryRetentionUpdateBodyIsTriState(t *testing.T) {
	applicationID := "app-1"
	cases := []struct {
		name   string
		update RegistryRetentionUpdate
		want   string
	}{
		{"default only", RegistryRetentionUpdate{DefaultSet: true, Default: &RegistryRetentionPolicy{KeepDays: 14, KeepLatest: 2}},
			`{"default":{"keep_days":14,"keep_latest":2}}`},
		{"default reset", RegistryRetentionUpdate{DefaultSet: true}, `{"default":null}`},
		{"rules emptied", RegistryRetentionUpdate{RulesSet: true}, `{"rules":[]}`},
		{"rules emptied by an empty slice", RegistryRetentionUpdate{RulesSet: true, Rules: []RegistryRetentionRuleInput{}}, `{"rules":[]}`},
		{"rules only", RegistryRetentionUpdate{RulesSet: true, Rules: []RegistryRetentionRuleInput{
			{RepositoryPattern: "ankra-ci/**", KeepDays: 7, KeepLatest: 2},
			{RepositoryPattern: "web/*", ApplicationID: &applicationID, KeepDays: 0, KeepLatest: 5},
		}}, `{"rules":[{"repository_pattern":"ankra-ci/**","keep_days":7,"keep_latest":2},` +
			`{"repository_pattern":"web/*","application_id":"app-1","keep_days":0,"keep_latest":5}]}`},
		{"nothing set", RegistryRetentionUpdate{}, `{}`},
	}
	for _, testCase := range cases {
		encoded, marshalError := json.Marshal(testCase.update)
		if marshalError != nil {
			t.Fatalf("%s: %v", testCase.name, marshalError)
		}
		if string(encoded) != testCase.want {
			t.Errorf("%s: body = %s, want %s", testCase.name, encoded, testCase.want)
		}
	}
}

// An update that sets nothing is refused before any request: the platform
// would answer 422, and nothing must be sent by mistake.
func TestRegistryRetentionUpdateRefusesAnEmptyWrite(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Errorf("no request expected, got %s %s", request.Method, request.URL.Path)
	})
	if _, updateError := client.UpdateRegistryRetention(context.Background(), RegistryRetentionUpdate{}); updateError == nil {
		t.Fatal("an empty update must be refused")
	}
}

// The platform's refusal sentence rides verbatim on a 422.
func TestRegistryRetentionUpdateRelaysTheRefusal(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Duplicate repository pattern: web/*"})
	})
	_, updateError := client.UpdateRegistryRetention(context.Background(), RegistryRetentionUpdate{RulesSet: true})
	var unexpected *UnexpectedResponseError
	if !errors.As(updateError, &unexpected) || unexpected.StatusCode != http.StatusUnprocessableEntity ||
		updateError.Error() != "Duplicate repository pattern: web/*" {
		t.Fatalf("error = %v", updateError)
	}
}

// A storage request rides the limit-request lane with kind
// registry_storage and the size in GiB.
func TestSubmitLimitRequestRegistryStorage(t *testing.T) {
	var body map[string]any
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/billing/limit-request" {
			t.Errorf("unexpected %s %s", request.Method, request.URL.Path)
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		jsonResponse(t, writer, http.StatusOK, map[string]any{"id": "lr-1", "limit_kind": "registry_storage",
			"requested_value": 100, "justification": "more images", "status": "pending"})
	})
	request, submitError := client.SubmitLimitRequest(RegistryStorageLimitKind, 100, "more images")
	if submitError != nil || request.Status != "pending" || request.LimitKind != RegistryStorageLimitKind {
		t.Fatalf("submit: %+v %v", request, submitError)
	}
	if body["limit_kind"] != "registry_storage" || body["requested_value"] != float64(100) || body["justification"] != "more images" {
		t.Fatalf("body = %v", body)
	}
}
