package client

import (
	"net/http"
	"strings"
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
		stated.EstimateFX == nil || stated.EstimateFX.RateFromUSD == nil || *stated.EstimateFX.RateFromUSD != 0.92 ||
		stated.EstimateFX.Rate != nil || stated.EstimateFX.Basis != nil || len(stated.Clusters) != 1 ||
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

// costReconciliationCoverageBody is the shape cluster#5739 added: a
// credential whose difference covers one of two clusters (hours share
// unknown, read for a scope so the invoice is withheld), one whose platform
// says no cluster was fully metered (an explicit null), and one counted under
// the first that carries no difference_coverage key at all, as a platform
// from before that change answers.
const costReconciliationCoverageBody = `{"month":"2026-10","month_closed":true,"period_hours":744,"generated_at":"",` +
	`"credentials":[` +
	`{"credential_id":"aaaaaaaa-0000-4000-8000-000000000001","provider":"ovh","state":"complete","difference_pct":0.17,` +
	`"difference_coverage":{"clusters_covered":1,"clusters_named":2,"matched_minor":9000,"estimate_minor":8985,` +
	`"invoice_share_pct":90.18,"hours_share_pct":null},"invoice_withheld":true,"same_invoice_as":null,` +
	`"clusters":[{"cluster_id":"cccccccc-0000-4000-8000-000000000001","cluster_name":"prod","line_count":2,"period_hours":744,"in_coverage":true},` +
	`{"cluster_id":"cccccccc-0000-4000-8000-000000000002","cluster_name":"batch","line_count":1,"period_hours":744,"in_coverage":false}]},` +
	`{"credential_id":"aaaaaaaa-0000-4000-8000-000000000002","provider":"upcloud","state":"complete","difference_pct":null,` +
	`"difference_coverage":null},` +
	`{"credential_id":"aaaaaaaa-0000-4000-8000-000000000003","provider":"ovh","state":"complete",` +
	`"same_invoice_as":"aaaaaaaa-0000-4000-8000-000000000001"}]}`

func TestGetCostReconciliation_TellsAnUnreportedCoverageFromNoClusterCovered(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(costReconciliationCoverageBody))
	}
	reconciliation, err := newTestClient(t, handler).GetCostReconciliation("2026-10")
	if err != nil || len(reconciliation.Credentials) != 3 {
		t.Fatalf("GetCostReconciliation = %+v, %v", reconciliation, err)
	}
	covered, uncovered, unreported := reconciliation.Credentials[0], reconciliation.Credentials[1], reconciliation.Credentials[2]
	coverage := covered.DifferenceCoverage
	if !covered.CoverageReported || coverage == nil || coverage.ClustersCovered != 1 || coverage.ClustersNamed != 2 ||
		coverage.MatchedMinor != 9000 || coverage.EstimateMinor != 8985 ||
		coverage.InvoiceSharePct == nil || *coverage.InvoiceSharePct != 90.18 || coverage.HoursSharePct != nil {
		t.Fatalf("a stated coverage did not decode, or an unknown hours share became a number: %+v", covered)
	}
	if !covered.InvoiceWithheld || covered.SameInvoiceAs != nil || len(covered.Clusters) != 2 ||
		!covered.Clusters[0].InCoverage || covered.Clusters[1].InCoverage {
		t.Fatalf("withheld, same_invoice_as or in_coverage did not decode: %+v", covered)
	}
	if !uncovered.CoverageReported || uncovered.DifferenceCoverage != nil {
		t.Fatalf("an explicit null coverage must read as reported with no cluster covered: %+v", uncovered)
	}
	if unreported.CoverageReported || unreported.DifferenceCoverage != nil {
		t.Fatalf("a credential with no difference_coverage key must read as not reported: %+v", unreported)
	}
	if covered.CredentialID == nil || unreported.SameInvoiceAs == nil || *unreported.SameInvoiceAs != *covered.CredentialID {
		t.Fatalf("same_invoice_as did not decode: %+v", unreported)
	}
	for _, credential := range reconciliation.Credentials {
		if credential.CredentialOutsideProjects {
			t.Fatalf("a platform that predates credential_outside_projects must read as naming every credential: %+v", credential)
		}
	}
}

// costReconciliationOutsideBody is a read for a reader confined to a
// project (ankra-bn86k4.64): the reader's own credential in full, and two
// credentials outside its projects that bill its clusters, each with its
// identity withheld (a null id and name, an empty document id, a null
// provider error) and the marker set.
const costReconciliationOutsideBody = `{"month":"2026-10","month_closed":true,"period_hours":744,"generated_at":"",` +
	`"credentials":[` +
	`{"credential_id":"aaaaaaaa-0000-4000-8000-000000000001","credential_outside_projects":false,"credential_name":"ovh-a",` +
	`"provider":"ovh","state":"complete","invoice_withheld":true,"matched_minor":4000,"currency":"eur"},` +
	`{"credential_id":null,"credential_outside_projects":true,"credential_name":null,"provider":"ovh","state":"failed",` +
	`"imports":[{"source":"api","source_document_id":"","state":"failed","reason":null,"line_count":0,"attempted_at":"2026-11-01T06:00:00Z"}],` +
	`"invoice_withheld":true,"matched_minor":1200,"currency":"eur","same_invoice_as":null,"difference_reason":null},` +
	`{"credential_id":null,"credential_outside_projects":true,"credential_name":null,"provider":"ovh","state":"complete",` +
	`"invoice_withheld":true,"matched_minor":1700,"currency":"eur"}]}`

func TestGetCostReconciliation_DecodesAWithheldCredentialAsNotNamed(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(costReconciliationOutsideBody))
	}
	reconciliation, err := newTestClient(t, handler).GetCostReconciliation("2026-10")
	if err != nil || len(reconciliation.Credentials) != 3 {
		t.Fatalf("GetCostReconciliation = %+v, %v", reconciliation, err)
	}
	named := reconciliation.Credentials[0]
	if named.CredentialOutsideProjects || named.CredentialID == nil || *named.CredentialID != "aaaaaaaa-0000-4000-8000-000000000001" {
		t.Fatalf("the reader's own credential must decode named and unmarked: %+v", named)
	}
	for index, withheld := range reconciliation.Credentials[1:] {
		if !withheld.CredentialOutsideProjects || withheld.CredentialID != nil || withheld.CredentialName != nil ||
			withheld.MatchedMinor == nil {
			t.Fatalf("withheld entry %d must decode marked, with no id or name and its amount kept: %+v", index, withheld)
		}
	}
	if document := reconciliation.Credentials[1].Imports[0]; document.SourceDocumentID != "" || document.Reason != nil {
		t.Fatalf("a withheld credential's import must carry no document id or provider error: %+v", document)
	}
}

// The conversion as cluster#6006 sends it: a GBP invoice compared with the
// EUR figure, so rate_from_usd is null and rate is the one applied, and an
// estimate at today's rate. The golden above is the older shape, which leaves
// rate and basis absent.
func TestGetCostReconciliation_DecodesTheRateAppliedAndItsBasis(t *testing.T) {
	body := strings.Replace(costReconciliationGolden,
		`"estimate_fx":{"from":"usd","to":"eur","rate_from_usd":0.92,"as_of":"2026-09-25T00:00:00Z"}`,
		`"estimate_fx":{"from":"eur","to":"gbp","rate":0.8612,"rate_from_usd":null,"as_of":"2026-08-29T00:00:00Z","basis":"captured"}`, 1)
	body = strings.Replace(body, `"estimate_fx":null`,
		`"estimate_fx":{"from":"usd","to":"eur","rate":0.93,"rate_from_usd":0.93,"as_of":"2026-11-06T06:00:00Z","basis":"today"}`, 1)
	handler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}
	reconciliation, err := newTestClient(t, handler).GetCostReconciliation("2026-08")
	if err != nil || len(reconciliation.Credentials) != 2 {
		t.Fatalf("GetCostReconciliation = %+v, %v", reconciliation, err)
	}
	fromEUR := reconciliation.Credentials[0].EstimateFX
	if fromEUR == nil || fromEUR.From != "eur" || fromEUR.To != "gbp" || fromEUR.Rate == nil || *fromEUR.Rate != 0.8612 ||
		fromEUR.RateFromUSD != nil || fromEUR.AsOf == nil || *fromEUR.AsOf != "2026-08-29T00:00:00Z" ||
		fromEUR.Basis == nil || *fromEUR.Basis != "captured" {
		t.Fatalf("the EUR figure's conversion did not decode, rate_from_usd null: %+v", fromEUR)
	}
	today := reconciliation.Credentials[1].EstimateFX
	if today == nil || today.From != "usd" || today.Rate == nil || *today.Rate != 0.93 || today.RateFromUSD == nil ||
		*today.RateFromUSD != 0.93 || today.Basis == nil || *today.Basis != "today" {
		t.Fatalf("the estimate at today's rate did not decode: %+v", today)
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
