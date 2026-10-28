package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const (
	decisionLadderID = "0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	decisionWaveID   = "1c8d5e2f-6a7b-4c9d-8e1f-2a3b4c5d6e7f"
	decisionWasteID  = "2d9e6f3a-7b8c-4d0e-9f2a-3b4c5d6e7f80"
)

// decisionProposalJSON builds one DecisionProposal body in the shape the
// cluster's openapi.json declares, every required member present.
func decisionProposalJSON(id, kind, status, plan, extra string) string {
	return `{"id":"` + id + `","area":"cost","kind":"` + kind + `","subject":{"cluster_id":"11111111-1111-4111-8111-111111111111"},` +
		`"summary":"Summary of ` + kind + `","plan":` + plan + `,"evidence":{},"status":"` + status + `","source":"surface",` +
		`"created_by":null,"decided_by":null,"decided_at":null,"decision_note":null,"operation_id":null,"receipt":null,` +
		`"dedupe_key":"` + kind + `:c1","executable":true,"expected_monthly_cents":null,"baseline_monthly_cents":null,` +
		`"baseline_window":null,"measured_monthly_cents":null,"measured_at":null,"measurement_status":"not_applicable",` +
		`"verify_until":null,"subject_cluster_id":"11111111-1111-4111-8111-111111111111","verification_status":"not_applicable",` +
		`"verification_days":[],"rollback_of":null,"parent_id":null,"created_at":"2026-09-20T08:00:00Z",` +
		`"updated_at":"2026-09-21T08:00:00Z","run_after":null` + extra + `}`
}

func TestListDecisions_SendsTheFiltersAndDecodesWavesAndUnsettled(t *testing.T) {
	wave := strings.Replace(decisionProposalJSON(decisionWaveID, "right_size", "running", `{"parameters":{"wave":1}}`, ""),
		`"parent_id":null`, `"parent_id":"`+decisionLadderID+`"`, 1)
	wave = strings.Replace(wave, `"operation_id":null`, `"operation_id":"99999999-9999-4999-8999-999999999999"`, 1)
	body := `{"proposals":[` + decisionProposalJSON(decisionLadderID, "right_size_ladder", "approved", `{"waves":3}`, "") + `,` + wave + `,` +
		decisionProposalJSON(decisionWasteID, "waste_cleanup", "approved", `{"consent_required":true,"parameters":{"waste_finding_id":"w1"}}`, "") +
		`],"unsettled_proposal_ids":["` + decisionWaveID + `"]}`
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/org/decisions" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("area") != "cost" || query.Get("status") != "approved" || query.Get("limit") != "50" || len(query) != 3 {
			t.Errorf("query = %v", query)
		}
		_, _ = w.Write([]byte(body))
	}
	listing, err := newTestClient(t, handler).ListDecisions(DecisionListFilter{Area: "cost", Status: "approved", Limit: 50})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if len(listing.Proposals) != 3 || len(listing.UnsettledProposalIDs) != 1 || listing.UnsettledProposalIDs[0] != decisionWaveID {
		t.Fatalf("listing did not decode: %+v", listing)
	}
	decodedWave := listing.Proposals[1]
	if decodedWave.ParentID == nil || *decodedWave.ParentID != decisionLadderID || decodedWave.OperationID == nil ||
		decodedWave.Status != "running" || decodedWave.ExpectedMonthlyCents != nil || decodedWave.Receipt != nil {
		t.Fatalf("a wave did not decode with its parent: %+v", decodedWave)
	}
	if listing.Proposals[2].Plan["consent_required"] != true {
		t.Fatalf("the plan did not decode: %+v", listing.Proposals[2].Plan)
	}
}

func TestListDecisions_SendsNoQueryForAnEmptyFilterAndDecodesEmpty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("an empty filter sends no query, got %q", r.URL.RawQuery)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{})
	}
	listing, err := newTestClient(t, handler).ListDecisions(DecisionListFilter{})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if listing.Proposals == nil || listing.UnsettledProposalIDs == nil {
		t.Fatalf("absent lists must decode as empty, not nil: %+v", listing)
	}
}

func TestGetDecision_DecodesOutcomeVerificationAndReceipt(t *testing.T) {
	extra := ""
	proposal := decisionProposalJSON(decisionWaveID, "right_size", "succeeded", `{"parameters":{"cluster_id":"c1"}}`, extra)
	for old, replacement := range map[string]string{
		`"expected_monthly_cents":null`:          `"expected_monthly_cents":12000`,
		`"baseline_monthly_cents":null`:          `"baseline_monthly_cents":200000`,
		`"baseline_window":null`:                 `"baseline_window":{"from":"2026-09-19T10:00:00Z","to":"2026-09-20T10:00:00Z"}`,
		`"measurement_status":"not_applicable"`:  `"measurement_status":"pending"`,
		`"verify_until":null`:                    `"verify_until":"2026-09-27T10:00:00Z"`,
		`"verification_status":"not_applicable"`: `"verification_status":"verifying"`,
		`"verification_days":[]`: `"verification_days":[{"day":1,"from":"2026-09-20T10:00:00Z","to":"2026-09-21T10:00:00Z",` +
			`"state":"clear","cpu_p95_share":0.41,"memory_p95_share":null,"hottest_node":"w-2","nodes":3,"reporting":2}]`,
		`"receipt":null`: `"receipt":{"executed_by":"u1","dispatched_at":"2026-09-20T10:00:00Z","operation_id":"99999999-9999-4999-8999-999999999999",` +
			`"completed_at":"2026-09-20T10:05:00Z","execution_status":"success","steps":[{"name":"resize","outcome":"succeeded","detail":"cpx51 to cpx41"}]}`,
	} {
		proposal = strings.Replace(proposal, old, replacement, 1)
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/org/decisions/"+decisionWaveID {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(proposal))
	}
	decoded, err := newTestClient(t, handler).GetDecision(decisionWaveID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if decoded.ExpectedMonthlyCents == nil || *decoded.ExpectedMonthlyCents != 12000 || decoded.MeasuredMonthlyCents != nil ||
		decoded.BaselineWindow == nil || decoded.BaselineWindow.To != "2026-09-20T10:00:00Z" || decoded.MeasurementStatus != "pending" {
		t.Fatalf("outcome did not decode (an unknown measurement stays nil): %+v", decoded)
	}
	if len(decoded.VerificationDays) != 1 || decoded.VerificationDays[0].MemoryP95Share != nil || *decoded.VerificationDays[0].CPUP95Share != 0.41 {
		t.Fatalf("verification did not decode: %+v", decoded.VerificationDays)
	}
	if decoded.Receipt == nil || len(decoded.Receipt.Steps) != 1 || decoded.Receipt.Steps[0].Outcome != "succeeded" ||
		decoded.Receipt.ExecutionStatus != "success" {
		t.Fatalf("receipt did not decode: %+v", decoded.Receipt)
	}
}

func TestGetDecisionActivity_DecodesEvents(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/decisions/"+decisionWaveID+"/activity" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"events":[{"id":"e2","proposal_id":"` + decisionWaveID + `","event_type":"approved","actor":"u1",` +
			`"note":"ok","detail":{},"created_at":"2026-09-20T09:00:00Z"},{"id":"e1","proposal_id":"` + decisionWaveID + `",` +
			`"event_type":"proposed","actor":null,"note":null,"detail":{"source":"surface"},"created_at":"2026-09-20T08:00:00Z"}]}`))
	}
	activity, err := newTestClient(t, handler).GetDecisionActivity(decisionWaveID)
	if err != nil {
		t.Fatalf("GetDecisionActivity: %v", err)
	}
	if len(activity.Events) != 2 || activity.Events[1].Actor != nil || activity.Events[1].Note != nil ||
		activity.Events[1].Detail["source"] != "surface" || *activity.Events[0].Note != "ok" {
		t.Fatalf("events did not decode: %+v", activity.Events)
	}
}

type capturedDecisionWrite struct {
	method, path, csrf string
	raw                []byte
}

func captureDecisionWrite(t *testing.T, status int, response string) (*Client, *capturedDecisionWrite) {
	t.Helper()
	captured := &capturedDecisionWrite{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		captured.method, captured.path, captured.csrf = r.Method, r.URL.Path, r.Header.Get("X-Ankra-CSRF")
		captured.raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}
	return newTestClient(t, handler), captured
}

func TestApproveAndSetAsideDecision_SendTheNoteOnlyWhenGiven(t *testing.T) {
	approved := decisionProposalJSON(decisionWasteID, "waste_cleanup", "approved", `{}`, "")
	testClient, captured := captureDecisionWrite(t, http.StatusOK, approved)
	if _, err := testClient.ApproveDecision(decisionWasteID, nil); err != nil {
		t.Fatalf("ApproveDecision: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/v1/org/decisions/"+decisionWasteID+"/approve" ||
		string(captured.raw) != "{}" || captured.csrf != "" {
		t.Fatalf("an approval with no note sends an empty object: %s %s %q (csrf %q)", captured.method, captured.path, captured.raw, captured.csrf)
	}
	note := "Checked with the team"
	testClient, captured = captureDecisionWrite(t, http.StatusOK, approved)
	if _, err := testClient.SetAsideDecision(decisionWasteID, &note); err != nil {
		t.Fatalf("SetAsideDecision: %v", err)
	}
	if captured.path != "/api/v1/org/decisions/"+decisionWasteID+"/set-aside" || string(captured.raw) != `{"note":"Checked with the team"}` {
		t.Fatalf("a set-aside sends its note: %s %q", captured.path, captured.raw)
	}
}

// Hold and release post the same note body as approve and set-aside, to their
// own routes: the note when one is given, an empty object otherwise.
func TestHoldAndReleaseDecision_SendTheNoteOnlyWhenGiven(t *testing.T) {
	held := decisionProposalJSON(decisionWasteID, "waste_cleanup", "held", `{}`, "")
	testClient, captured := captureDecisionWrite(t, http.StatusOK, held)
	if _, err := testClient.HoldDecision(decisionWasteID, nil); err != nil {
		t.Fatalf("HoldDecision: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/v1/org/decisions/"+decisionWasteID+"/hold" ||
		string(captured.raw) != "{}" || captured.csrf != "" {
		t.Fatalf("a hold with no note sends an empty object: %s %s %q (csrf %q)", captured.method, captured.path, captured.raw, captured.csrf)
	}
	note := "Launch is over"
	approved := decisionProposalJSON(decisionWasteID, "waste_cleanup", "approved", `{}`, "")
	testClient, captured = captureDecisionWrite(t, http.StatusOK, approved)
	if _, err := testClient.ReleaseDecision(decisionWasteID, &note); err != nil {
		t.Fatalf("ReleaseDecision: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/v1/org/decisions/"+decisionWasteID+"/release" ||
		string(captured.raw) != `{"note":"Launch is over"}` {
		t.Fatalf("a release sends its note: %s %s %q", captured.method, captured.path, captured.raw)
	}
}

// Consent is never assumed: execute sends a body only when a consent field
// was given, and each field only when it was.
func TestExecuteDecision_SendsConsentOnlyWhenGiven(t *testing.T) {
	running := strings.Replace(decisionProposalJSON(decisionWasteID, "waste_cleanup", "running", `{}`, ""),
		`"operation_id":null`, `"operation_id":"99999999-9999-4999-8999-999999999999"`, 1)
	testClient, captured := captureDecisionWrite(t, http.StatusOK, running)
	result, err := testClient.ExecuteDecision(decisionWasteID, DecisionExecuteOptions{})
	if err != nil {
		t.Fatalf("ExecuteDecision: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/v1/org/decisions/"+decisionWasteID+"/execute" || len(captured.raw) != 0 {
		t.Fatalf("a run with no consent sends no body: %s %s %q", captured.method, captured.path, captured.raw)
	}
	if result.Status != "running" || result.OperationID == nil {
		t.Fatalf("the running proposal did not decode: %+v", result)
	}

	days := 45
	testClient, captured = captureDecisionWrite(t, http.StatusOK, running)
	if _, err := testClient.ExecuteDecision(decisionWasteID, DecisionExecuteOptions{MinUnattachedDays: &days}); err != nil {
		t.Fatalf("ExecuteDecision: %v", err)
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(captured.raw, &body) != nil || len(body) != 1 || string(body["min_unattached_days"]) != "45" {
		t.Fatalf("only the given minimum age is sent, never a consent: %q", captured.raw)
	}

	testClient, captured = captureDecisionWrite(t, http.StatusOK, running)
	if _, err := testClient.ExecuteDecision(decisionWasteID, DecisionExecuteOptions{Consent: "no_snapshot_acknowledged"}); err != nil {
		t.Fatalf("ExecuteDecision: %v", err)
	}
	if string(captured.raw) != `{"consent":"no_snapshot_acknowledged"}` {
		t.Fatalf("a given consent is sent alone: %q", captured.raw)
	}
}

func TestExecuteDecision_RefusalsKeepTheirMeaning(t *testing.T) {
	testClient, _ := captureDecisionWrite(t, http.StatusUnprocessableEntity,
		`{"detail":"Written consent is required: no snapshot exists for what this change deletes, so it is final.","error_code":"consent_required"}`)
	_, err := testClient.ExecuteDecision(decisionWasteID, DecisionExecuteOptions{})
	var consent *DecisionConsentRequiredError
	if !errors.As(err, &consent) || !strings.HasPrefix(consent.Detail, "Written consent is required") {
		t.Fatalf("a consent refusal must be told apart by its error_code, got %v", err)
	}

	testClient, _ = captureDecisionWrite(t, http.StatusUnprocessableEntity, `{"detail":"The plan carries no waste_finding_id."}`)
	_, err = testClient.ExecuteDecision(decisionWasteID, DecisionExecuteOptions{})
	var unexpected *UnexpectedResponseError
	if errors.As(err, &consent) || !errors.As(err, &unexpected) || unexpected.StatusCode != 422 || unexpected.Detail != "The plan carries no waste_finding_id." {
		t.Fatalf("a plan refusal is not a consent refusal, got %v", err)
	}

	testClient, _ = captureDecisionWrite(t, http.StatusConflict, `{"detail":"Wave 2 of 3 is inside its seven-day verification; the next wave runs once it has passed."}`)
	_, err = testClient.ExecuteDecision(decisionLadderID, DecisionExecuteOptions{})
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusConflict || !strings.Contains(unexpected.Detail, "seven-day verification") {
		t.Fatalf("a ladder not ready keeps its 409 detail, got %v", err)
	}

	testClient, _ = captureDecisionWrite(t, http.StatusForbidden, `{"detail":"permission_denied","permission":"clusters.write","scope_type":"organisation"}`)
	_, err = testClient.ExecuteDecision(decisionWaveID, DecisionExecuteOptions{})
	var denied *PermissionDeniedError
	if !errors.As(err, &denied) || denied.Permission != "clusters.write" {
		t.Fatalf("a permission refusal names the permission, got %v", err)
	}

	testClient, _ = captureDecisionWrite(t, http.StatusUnauthorized, `{"detail":"Unknown token"}`)
	if _, err = testClient.ExecuteDecision(decisionWaveID, DecisionExecuteOptions{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a 401 is ErrUnauthorized, got %v", err)
	}

	testClient, _ = captureDecisionWrite(t, http.StatusNotFound, "")
	_, err = testClient.ExecuteDecision(decisionWaveID, DecisionExecuteOptions{})
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "" {
		t.Fatalf("a bare 404 keeps its status and no detail, got %v", err)
	}
}

func TestDecisions_ItemNotFoundCarriesItsDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"detail": "Decision proposal not found"})
	}
	_, err := newTestClient(t, handler).GetDecision(decisionWaveID)
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "Decision proposal not found" {
		t.Fatalf("error = %v", err)
	}
}
