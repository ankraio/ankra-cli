package client

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newAuditLogTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(testToken, server.URL)
}

func TestListAuditLogsSendsTheFiltersAndDecodesTheTrail(t *testing.T) {
	var path, query, authorization, organisation string
	auditClient := newAuditLogTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		path, query = request.URL.Path, request.URL.RawQuery
		authorization, organisation = request.Header.Get("Authorization"), request.Header.Get(orgOverrideHeader)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"entries":[{"created_at":"2026-11-08T11:00:00Z","user_email":"mark@ankra.ai",
			"user_id":null,"action_type":"delete","resource_type":"pod","resource_id":null,"ip_address":"10.0.0.1",
			"cluster_id":"c1","cluster_name":"prod","actor_type":"agent","target_kind":"Pod","target_name":"web-1",
			"outcome":"failed","details":{"reason":"denied"}}],"truncated":true}`))
	})
	auditClient.SetOrganisationOverride("3b7dccca-0788-4470-9910-19478ae345ae")
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.FixedZone("CET", 3600))
	trail, listError := auditClient.ListAuditLogs(context.Background(), AuditLogFilters{
		ActorType: "agent", Outcome: "failed", ClusterID: "c1", StartDate: &start,
	})
	if listError != nil {
		t.Fatal(listError)
	}
	if path != auditLogExportPath ||
		query != "actor_type=agent&cluster_id=c1&format=json&outcome=failed&start_date=2026-10-31T23%3A00%3A00Z" {
		t.Errorf("request = %s?%s", path, query)
	}
	if authorization != "Bearer "+testToken || organisation != "3b7dccca-0788-4470-9910-19478ae345ae" {
		t.Errorf("the read must carry the bearer token and the organisation override, got %q / %q", authorization, organisation)
	}
	if !trail.Truncated || len(trail.Entries) != 1 || trail.Entries[0].TargetName != "web-1" ||
		trail.Entries[0].Details["reason"] != "denied" {
		t.Errorf("trail = %+v", trail)
	}
}

func TestListAuditLogsKeepsAnEmptyTrailAnArray(t *testing.T) {
	auditClient := newAuditLogTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"entries":null,"truncated":false}`))
	})
	trail, listError := auditClient.ListAuditLogs(context.Background(), AuditLogFilters{})
	if listError != nil || trail.Entries == nil || len(trail.Entries) != 0 {
		t.Fatalf("trail = %+v (%v)", trail, listError)
	}
}

func TestAuditLogRefusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, refusal error)
	}{
		{"401 asks for a login", http.StatusUnauthorized, `{"detail":"Not authenticated"}`, func(t *testing.T, refusal error) {
			if !errors.Is(refusal, ErrUnauthorized) {
				t.Errorf("got %v", refusal)
			}
		}},
		{"403 names the platform's reason", http.StatusForbidden, `{"detail":"You do not have permission to read the audit log"}`,
			func(t *testing.T, refusal error) {
				var denied *PermissionDeniedError
				if !errors.As(refusal, &denied) || !strings.Contains(refusal.Error(), "read the audit log") {
					t.Errorf("got %v", refusal)
				}
			}},
		{"403 in the RBAC shape keeps the permission", http.StatusForbidden, `{"detail":"permission_denied","permission":"audit.read"}`,
			func(t *testing.T, refusal error) {
				var denied *PermissionDeniedError
				if !errors.As(refusal, &denied) || denied.Permission != "audit.read" {
					t.Errorf("got %v", refusal)
				}
			}},
		{"422 names the parameter", http.StatusUnprocessableEntity,
			`{"detail":[{"type":"uuid_parsing","loc":["query","user_id"],"msg":"Input should be a valid UUID"}]}`,
			func(t *testing.T, refusal error) {
				if refusal == nil || !strings.Contains(refusal.Error(), "user_id: Input should be a valid UUID") {
					t.Errorf("got %v", refusal)
				}
			}},
		{"500 is a failure", http.StatusInternalServerError, `oops`, func(t *testing.T, refusal error) {
			var unexpected *UnexpectedResponseError
			if !errors.As(refusal, &unexpected) || unexpected.StatusCode != http.StatusInternalServerError {
				t.Errorf("got %v", refusal)
			}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			auditClient := newAuditLogTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(testCase.status)
				_, _ = writer.Write([]byte(testCase.body))
			})
			_, listError := auditClient.ListAuditLogs(context.Background(), AuditLogFilters{})
			testCase.check(t, listError)
			var destination bytes.Buffer
			_, exportError := auditClient.ExportAuditLogs(context.Background(), AuditLogFilters{}, AuditLogFormatCSV, &destination)
			testCase.check(t, exportError)
			if destination.Len() != 0 {
				t.Errorf("a refusal writes nothing, got %q", destination.String())
			}
		})
	}
}

func TestExportAuditLogsStreamsCSVAndReadsTheTruncationHeader(t *testing.T) {
	var query string
	auditClient := newAuditLogTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		query = request.URL.RawQuery
		writer.Header().Set("Content-Type", "text/csv; charset=utf-8")
		writer.Header().Set("X-Ankra-Export-Truncated", "true")
		_, _ = writer.Write([]byte("created_at,user_email\n2026-11-08T11:00:00Z,mark@ankra.ai\n"))
	})
	var destination bytes.Buffer
	result, exportError := auditClient.ExportAuditLogs(context.Background(), AuditLogFilters{Search: "web"}, AuditLogFormatCSV, &destination)
	if exportError != nil {
		t.Fatal(exportError)
	}
	if query != "format=csv&search=web" {
		t.Errorf("query = %s", query)
	}
	if destination.String() != "created_at,user_email\n2026-11-08T11:00:00Z,mark@ankra.ai\n" {
		t.Errorf("body = %q", destination.String())
	}
	if !result.Truncated || result.Format != AuditLogFormatCSV || result.BytesWritten != int64(destination.Len()) {
		t.Errorf("result = %+v", result)
	}
}

func TestExportAuditLogsStreamsJSONVerbatimAndReadsItsTruncatedMember(t *testing.T) {
	document := "{\"entries\":[{\"created_at\":\"2026-11-08T11:00:00Z\"}],\"truncated\":true}\n"
	auditClient := newAuditLogTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document))
	})
	var destination bytes.Buffer
	result, exportError := auditClient.ExportAuditLogs(context.Background(), AuditLogFilters{}, AuditLogFormatJSON, &destination)
	if exportError != nil {
		t.Fatal(exportError)
	}
	if destination.String() != document || !result.Truncated || result.BytesWritten != int64(len(document)) {
		t.Errorf("body %q result %+v", destination.String(), result)
	}

	brokenClient := newAuditLogTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("<html>"))
	})
	if _, brokenError := brokenClient.ExportAuditLogs(context.Background(), AuditLogFilters{}, AuditLogFormatJSON, &bytes.Buffer{}); brokenError == nil {
		t.Fatal("a JSON export that is not JSON must fail")
	}
}

func TestAuditLogFiltersQueryOmitsEmptyMembers(t *testing.T) {
	end := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	query := AuditLogFilters{UserID: "u1", AgentTaskID: "a1", ActionType: "create", ResourceType: "token", EndDate: &end}.Query()
	if encoded := query.Encode(); encoded != "action_type=create&agent_task_id=a1&end_date=2026-11-08T00%3A00%3A00Z&resource_type=token&user_id=u1" {
		t.Errorf("query = %s", encoded)
	}
	if encoded := (AuditLogFilters{}).Query().Encode(); encoded != "" {
		t.Errorf("empty filters send nothing, got %s", encoded)
	}
}
