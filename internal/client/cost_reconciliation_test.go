package client

import (
	"net/http"
	"testing"
)

// costReconciliationGolden is a GET /api/v1/org/cloud-cost/reconciliation
// body in the shape the cluster's openapi.json declares
// (ReconciliationResponse): a credential with a stated difference, one never
// imported (every amount null) and one whose imports carry no lists.
const costReconciliationGolden = `{"month":"2026-08","month_closed":true,"period_hours":744,"generated_at":"2026-09-26T08:00:00Z",` +
	`"credentials":[` +
	`{"credential_id":"aaaaaaaa-0000-4000-8000-000000000001","credential_name":"ovh-prod","provider":"ovh","state":"complete",` +
	`"imports":[{"source":"api","source_document_id":"usage-2026-08","state":"complete","lines_state":"complete","reason":null,` +
	`"currency":"eur","line_count":5,"attempted_at":"2026-09-03T06:00:00Z","succeeded_at":"2026-09-03T06:00:00Z"}],` +
	`"currency":"eur","currencies_mixed":false,"invoice_minor":9800,"tax_minor":1805,"credit_minor":-500,"matched_minor":9500,` +
	`"unmatched_minor":300,"estimate_minor":9200,"estimate_complete":true,` +
	`"estimate_fx":{"from":"usd","to":"eur","rate_from_usd":0.92,"as_of":"2026-09-25T00:00:00Z"},` +
	`"difference_pct":3.26,"difference_reason":null,"unplaced_priced_minor":300,"difference_caveat":"1 billed line(s) ...",` +
	`"clusters":[{"cluster_id":"cccccccc-0000-4000-8000-000000000001","cluster_name":"prod-eu","invoice_minor":9500,"line_count":2,` +
	`"estimate_minor":9200,"metered_hours":744,"lifetime_hours":744,"period_hours":744}],` +
	`"unmatched_lines":[{"line_key":"address","external_id":null,"external_id_kind":null,"category":"network",` +
	`"description":"address","amount_minor":300,"currency":"eur"}],"unmatched_line_count":1,"unmatched_truncated":false},` +
	`{"credential_id":"aaaaaaaa-0000-4000-8000-000000000002","credential_name":null,"provider":"upcloud","state":"absent",` +
	`"imports":[],"currency":null,"currencies_mixed":false,"invoice_minor":null,"tax_minor":null,"credit_minor":null,` +
	`"matched_minor":null,"unmatched_minor":null,"estimate_minor":null,"estimate_complete":null,"estimate_fx":null,` +
	`"difference_pct":null,"difference_reason":"No billing document has been imported for this month, so what the provider billed is unknown, not zero.",` +
	`"unplaced_priced_minor":null,"difference_caveat":null,"clusters":null,"unmatched_lines":null,` +
	`"unmatched_line_count":0,"unmatched_truncated":false}]}`

func TestGetCostReconciliation_DecodesNullsAsUnknownAndSendsTheMonth(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/org/cloud-cost/reconciliation" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("month"); got != "2026-08" {
			t.Errorf("month = %q, want 2026-08", got)
		}
		if header := r.Header.Get("X-Ankra-CSRF"); header != "" {
			t.Errorf("a bearer read must not send a CSRF header, got %q", header)
		}
		_, _ = w.Write([]byte(costReconciliationGolden))
	}
	reconciliation, err := newTestClient(t, handler).GetCostReconciliation("2026-08")
	if err != nil {
		t.Fatalf("GetCostReconciliation: %v", err)
	}
	if reconciliation.Month != "2026-08" || !reconciliation.MonthClosed || reconciliation.PeriodHours != 744 ||
		len(reconciliation.Credentials) != 2 {
		t.Fatalf("reconciliation did not decode: %+v", reconciliation)
	}
	stated := reconciliation.Credentials[0]
	if stated.DifferencePct == nil || *stated.DifferencePct != 3.26 || stated.InvoiceMinor == nil || *stated.InvoiceMinor != 9800 ||
		stated.EstimateFX == nil || stated.EstimateFX.RateFromUSD != 0.92 || len(stated.Clusters) != 1 ||
		stated.Clusters[0].LifetimeHours == nil || *stated.Clusters[0].LifetimeHours != 744 ||
		len(stated.UnmatchedLines) != 1 || stated.UnmatchedLines[0].Currency != "eur" ||
		stated.Imports[0].LinesState == nil || *stated.Imports[0].LinesState != "complete" {
		t.Fatalf("stated credential did not decode: %+v", stated)
	}
	absent := reconciliation.Credentials[1]
	if absent.InvoiceMinor != nil || absent.MatchedMinor != nil || absent.Currency != nil || absent.CredentialName != nil ||
		absent.DifferenceReason == nil || absent.Clusters == nil || absent.UnmatchedLines == nil || absent.Imports == nil {
		t.Fatalf("a never-imported credential must decode as unknown with empty lists: %+v", absent)
	}
}

func TestGetCostReconciliation_OmitsTheMonthToLeaveItToThePlatform(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want none", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"month":"2026-08","month_closed":true,"period_hours":744,"generated_at":"","credentials":null}`))
	}
	reconciliation, err := newTestClient(t, handler).GetCostReconciliation("")
	if err != nil || reconciliation.Credentials == nil || len(reconciliation.Credentials) != 0 {
		t.Fatalf("GetCostReconciliation(\"\") = %+v, %v; want an empty list", reconciliation, err)
	}
}
