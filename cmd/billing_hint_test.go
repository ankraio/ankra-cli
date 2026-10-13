package cmd

// Tests for the billing refusal on the AI commands (ankra-qkyvf.35): the
// platform's sentence is shown as written, one hint names where to top up,
// -o json carries the code, and the exit code is the generic refusal.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

const testBillingDetail = "Your prepaid credit is used up; new clusters and AI are paused. Your running clusters are unaffected. Add credit to continue."

// orgOverrideMock answers the --org override the hint prefers over the
// persisted selection.
type orgOverrideMock struct {
	baseMock
	organisationID string
}

func (m *orgOverrideMock) OrganisationOverride() string { return m.organisationID }

func setTestBaseURL(t *testing.T, value string) {
	t.Helper()
	previous := baseURL
	baseURL = value
	t.Cleanup(func() { baseURL = previous })
}

func TestExitCodeFor_BillingRefusalIsTheGenericRefusal(t *testing.T) {
	refusal := fmt.Errorf("chat: %w", &client.BillingRestrictedError{Detail: testBillingDetail})
	if got := exitCodeFor(refusal); got != exitError {
		t.Errorf("exitCodeFor(billing refusal) = %d, want %d: neither a re-login nor a role change resolves it", got, exitError)
	}
}

func TestBillingRestrictedHint_NamesTheBillingPageOfTheSelectedOrganisation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setMockClient(t, &orgOverrideMock{})
	setTestBaseURL(t, "https://platform.example.test/api/v1")
	if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: "org 1"}); err != nil {
		t.Fatalf("saveSelectedOrganisation: %v", err)
	}
	want := "Add credit in the portal under Billing: https://platform.example.test/organisation/org%201/billing"
	if got := billingRestrictedHint(); got != want {
		t.Errorf("billingRestrictedHint() = %q, want %q", got, want)
	}
}

func TestBillingRestrictedHint_PrefersTheOrgOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setMockClient(t, &orgOverrideMock{organisationID: "org-override"})
	setTestBaseURL(t, "https://platform.example.test")
	if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: "org-selected"}); err != nil {
		t.Fatalf("saveSelectedOrganisation: %v", err)
	}
	if got := billingRestrictedHint(); !strings.HasSuffix(got, "/organisation/org-override/billing") {
		t.Errorf("billingRestrictedHint() = %q, want the --org organisation's page", got)
	}
}

func TestBillingRestrictedHint_PlainWhenNoOrganisationIsKnown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setMockClient(t, &orgOverrideMock{})
	if got := billingRestrictedHint(); got != "Add credit in the portal under Billing." {
		t.Errorf("billingRestrictedHint() = %q, want the plain sentence without a link", got)
	}
}

func TestPrintBillingRestrictedHint_OnlyAfterTheRefusal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setMockClient(t, &orgOverrideMock{})
	out := &strings.Builder{}
	printBillingRestrictedHint(out, fmt.Errorf("chat: %w", &client.BillingRestrictedError{Detail: testBillingDetail}))
	if !strings.Contains(out.String(), "Add credit in the portal under Billing") {
		t.Errorf("hint = %q, want the top-up line after a wrapped refusal", out.String())
	}
	out.Reset()
	printBillingRestrictedHint(out, errors.New("chat: status 500"))
	printBillingRestrictedHint(out, &client.UnexpectedResponseError{StatusCode: 402})
	if out.String() != "" {
		t.Errorf("hint = %q, want nothing for errors that are not the billing refusal", out.String())
	}
}

func TestChat_BillingRefusalOnTheStreamIsTheSentenceWithoutADebugSuffix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mock := &chatStreamMock{events: []client.ChatStreamEvent{
		{Type: "error", Data: map[string]any{"error_code": "BILLING_RESTRICTED", "message": testBillingDetail}},
	}}
	_, err := runWithInput(t, mock, "", "chat", "hello")
	var restricted *client.BillingRestrictedError
	if !errors.As(err, &restricted) {
		t.Fatalf("err = %v (%T), want the typed billing refusal so the hint follows it", err, err)
	}
	if err.Error() != testBillingDetail {
		t.Errorf("err = %q, want the platform's sentence alone, with no \"(code: ...)\" suffix", err.Error())
	}
	if exitCodeFor(err) != exitError {
		t.Errorf("exit = %d, want %d", exitCodeFor(err), exitError)
	}
}

func TestChatInteractive_BillingRefusalPrintsTheSentenceAndTheHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mock := &chatStreamMock{events: []client.ChatStreamEvent{
		{Type: "error", Data: map[string]any{"error_code": "BILLING_RESTRICTED", "message": testBillingDetail}},
	}}
	var out string
	var err error
	stderr := captureStderr(t, func() {
		out, err = runWithInput(t, mock, "hello\nexit\n", "chat")
	})
	if err != nil {
		t.Fatalf("interactive chat ended with %v, want a clean exit after 'exit'", err)
	}
	combined := out + stderr
	if !strings.Contains(combined, "Error: "+testBillingDetail) {
		t.Errorf("output = %q, want the platform's sentence", combined)
	}
	if !strings.Contains(combined, "Add credit in the portal under Billing") {
		t.Errorf("output = %q, want the top-up hint after the sentence", combined)
	}
	if strings.Contains(combined, "code: BILLING_RESTRICTED") {
		t.Errorf("output = %q, want no debug suffix on a refusal written for the user", combined)
	}
}

func TestChatOneShotJSON_BillingRefusalBeforeTheTurnPrintsTheDocumentWithTheCode(t *testing.T) {
	mock := &chatSessionMock{createErrors: []error{&client.BillingRestrictedError{Detail: testBillingDetail}}}
	stdout, _, err := runChatStructured(t, mock, "chat", "-o", "json", "hello")
	var restricted *client.BillingRestrictedError
	if !errors.As(err, &restricted) || exitCodeFor(err) != exitError {
		t.Fatalf("err = %v (exit %d), want the typed refusal with the generic exit code", err, exitCodeFor(err))
	}
	document := decodeChatResult(t, stdout)
	if document["error_code"] != client.BillingRestrictedErrorCode || document["detail"] != testBillingDetail {
		t.Errorf("document = %v, want error_code BILLING_RESTRICTED and the platform's sentence in detail", document)
	}
	if document["error"] != testBillingDetail || document["answer"] != "" || document["continuable"] != false {
		t.Errorf("document = %v, want the refusal as its error, no answer and not continuable", document)
	}
	if _, hasTools := document["tool_calls"].([]any); !hasTools {
		t.Errorf("document = %v, want tool_calls as an empty list, not null", document)
	}
}

func TestChatOneShotJSON_BillingRefusalOnTheTurnCarriesTheCode(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{
		{Type: "error", Data: map[string]any{"error_code": "BILLING_RESTRICTED", "message": testBillingDetail}, Sequence: 2},
		{Type: "session_complete", Data: map[string]any{"status": "failed"}, Sequence: 3},
		{Type: "end", Data: map[string]any{"status": "failed"}, Done: true},
	}}}
	stdout, stderr, err := runChatStructured(t, mock, "chat", "-o", "json", "hello")
	var restricted *client.BillingRestrictedError
	if !errors.As(err, &restricted) {
		t.Fatalf("err = %v (%T), want the typed billing refusal", err, err)
	}
	document := decodeChatResult(t, stdout)
	if document["error_code"] != client.BillingRestrictedErrorCode || document["detail"] != testBillingDetail || document["error"] != testBillingDetail {
		t.Errorf("document = %v, want error_code, detail and error from the refusal frame", document)
	}
	if !strings.Contains(stderr, testBillingDetail) || !strings.Contains(stderr, "Add credit in the portal under Billing") {
		t.Errorf("stderr = %q, want the sentence and the top-up hint for a human reading the job log", stderr)
	}
	if len(mock.titleCalls) != 0 {
		t.Errorf("title calls = %v, want none for a refused turn", mock.titleCalls)
	}
}

func TestChatOneShotJSON_AnOrdinaryFailureCarriesNoCode(t *testing.T) {
	mock := &chatSessionMock{tails: [][]client.ChatStreamEvent{{
		{Type: "error", Data: map[string]any{"message": "You've sent too many messages this hour."}, Sequence: 2},
		endFrame(),
	}}}
	stdout, _, err := runChatStructured(t, mock, "chat", "-o", "json", "hello")
	if err == nil {
		t.Fatal("expected the failed turn to exit non-zero")
	}
	document := decodeChatResult(t, stdout)
	if _, present := document["error_code"]; present {
		t.Errorf("document = %v, want no error_code for a failure the platform gave no code", document)
	}
	if _, present := document["detail"]; present {
		t.Errorf("document = %v, want no detail member for a failure the platform gave no code", document)
	}
}
