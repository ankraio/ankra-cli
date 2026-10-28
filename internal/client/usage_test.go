package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The usage read goes to the bearer route with only the parameters given,
// carries the --org override, and keeps an unmeasured quantity null.
func TestGetOrganisationUsageSendsTheWindowAndKeepsNull(t *testing.T) {
	var path, query, organisation, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path, query = request.URL.Path, request.URL.RawQuery
		organisation, authorization = request.Header.Get(orgOverrideHeader), request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"from":"2026-09-01T00:00:00Z","to":"2026-10-01T00:00:00Z","granularity":"month","group_by":"meter",
			"lines":[{"meter":"service_instance_seconds","unit":"seconds","period_start":"2026-09-01T00:00:00Z",
			"period_end":"2026-10-01T00:00:00Z","cluster_id":null,"cluster_name":null,"application_id":null,"instance_id":null,
			"instance_name":null,"is_shared":false,"quantity":null,"coverage":"unknown","price_state":"included",
			"price_basis":"pilot","amount_minor":null,"currency":null}]}`))
	}))
	t.Cleanup(server.Close)
	client := New(testToken, server.URL)
	client.SetOrganisationOverride("3b7dccca-0788-4470-9910-19478ae345ae")
	report, err := client.GetOrganisationUsage(context.Background(), UsageOptions{From: "2026-09-01", To: "2026-10-01", GroupBy: "meter"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/v1/org/billing/usage" || query != "from=2026-09-01&group_by=meter&to=2026-10-01" {
		t.Errorf("request = %s?%s", path, query)
	}
	if organisation != "3b7dccca-0788-4470-9910-19478ae345ae" || authorization != "Bearer "+testToken {
		t.Errorf("the read must carry the bearer token and the organisation override, got %q / %q", authorization, organisation)
	}
	if len(report.Lines) != 1 || report.Lines[0].Quantity != nil || report.Lines[0].Coverage != UsageCoverageUnknown {
		t.Errorf("an unmeasured line must keep a null quantity, got %+v", report.Lines)
	}
}

// A 422 in the pydantic shape becomes a refusal per parameter, with the
// parameter named and pydantic's "Value error, " prefix dropped. A 422 in
// any other shape stays the client's usual error.
func TestGetOrganisationUsageReadsTheRefusal(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": []any{
			map[string]any{"type": "value_error", "loc": []any{"query", "from"}, "input": "2020-01-01",
				"msg": "Value error, from must be on or after 2025-09-03: usage is kept 400 days"},
		}})
	})
	_, err := client.GetOrganisationUsage(context.Background(), UsageOptions{From: "2020-01-01"})
	var refused *UsageQueryRefusedError
	if !errors.As(err, &refused) || len(refused.Refusals) != 1 {
		t.Fatalf("expected one refused parameter, got %v", err)
	}
	if refusal := refused.Refusals[0]; refusal.Parameter != "from" || refusal.Message != "from must be on or after 2025-09-03: usage is kept 400 days" {
		t.Errorf("refusal = %+v", refusal)
	}

	client = newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Organisation not found"})
	})
	_, err = client.GetOrganisationUsage(context.Background(), UsageOptions{})
	var unexpected *UnexpectedResponseError
	if errors.As(err, &refused) || !errors.As(err, &unexpected) || !strings.Contains(err.Error(), "Organisation not found") {
		t.Errorf("a 422 that names no parameter keeps the platform's detail, got %v", err)
	}
}

// A report without its lines field is not an empty report: nothing would
// tell "no usage" from "the platform answered something else".
func TestGetOrganisationUsageRefusesAReportWithoutLines(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, writer, http.StatusOK, map[string]any{"from": "2026-10-01T00:00:00Z", "to": "2026-10-08T00:00:00Z"})
	})
	if _, err := client.GetOrganisationUsage(context.Background(), UsageOptions{}); err == nil || !strings.Contains(err.Error(), "no lines field") {
		t.Errorf("expected the missing lines to be refused, got %v", err)
	}
}
