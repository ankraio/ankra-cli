package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
	"github.com/google/uuid"
)

const summaryRequestKey = "01937e50-4980-7000-8000-000000000001"

type summaryControlMock struct {
	baseMock
	calls     int
	source    string
	revision  int
	admission client.ProfileSummaryAdmissionRequest
	approval  client.ProfileSummaryPolicyApprovalRequest
	revoke    client.ProfileSummaryPolicyRevocationRequest
	response  json.RawMessage
	failure   error
}

func (m *summaryControlMock) result() (json.RawMessage, error) {
	m.calls++
	return m.response, m.failure
}
func (m *summaryControlMock) GetProfileSummaryStatus(_ context.Context, id string, revision int) (json.RawMessage, error) {
	m.source = id
	m.revision = revision
	return m.result()
}
func (m *summaryControlMock) GetDraftSummaryStatus(_ context.Context, id string, revision int) (json.RawMessage, error) {
	m.source = id
	m.revision = revision
	return m.result()
}
func (m *summaryControlMock) GenerateProfileSummary(_ context.Context, id string, revision int, request client.ProfileSummaryAdmissionRequest) (json.RawMessage, error) {
	m.source = id
	m.revision = revision
	m.admission = request
	return m.result()
}
func (m *summaryControlMock) GenerateDraftSummary(_ context.Context, id string, request client.ProfileSummaryAdmissionRequest) (json.RawMessage, error) {
	m.source = id
	m.admission = request
	return m.result()
}
func (m *summaryControlMock) CancelProfileSummaryGeneration(_ context.Context, id string) (json.RawMessage, error) {
	m.source = id
	return m.result()
}
func (m *summaryControlMock) GetProfileSummaryPolicy(context.Context) (json.RawMessage, error) {
	return m.result()
}
func (m *summaryControlMock) ApproveProfileSummaryPolicy(_ context.Context, r client.ProfileSummaryPolicyApprovalRequest) (json.RawMessage, error) {
	m.approval = r
	return m.result()
}
func (m *summaryControlMock) RevokeProfileSummaryPolicy(_ context.Context, r client.ProfileSummaryPolicyRevocationRequest) (json.RawMessage, error) {
	m.revoke = r
	return m.result()
}

func executeSummaryControl(t *testing.T, m *summaryControlMock, input string, args ...string) (string, string, error) {
	t.Helper()
	setMockClient(t, m)
	c := newStackProfileSummaryCommand()
	var out, diagnostics bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&diagnostics)
	c.SetIn(strings.NewReader(input))
	c.SetArgs(args)
	c.SilenceErrors = true
	c.SilenceUsage = true
	err := c.Execute()
	return out.String(), diagnostics.String(), err
}

func TestSummaryControlsRejectInvalidAdmissionBeforeAnyRequest(t *testing.T) {
	valid := []string{"generate", summaryTestID, "3", "--source-revision", "3", "--summary-revision", "0", "--request-key", summaryRequestKey}
	tests := [][]string{
		{"draft-status", summaryTestID}, {"draft-status", summaryTestID, "--source-revision", "0"},
		{"draft-status", "bad", "--source-revision", "1"}, {"status", summaryTestID, "2147483648"},
		{"cancel", "00000000-0000-0000-0000-000000000000", "--yes"},
		{"generate-draft", summaryTestID, "--source-revision", "1", "--summary-revision", "0"},
	}
	for _, change := range []struct {
		index int
		value string
	}{{4, "2"}, {4, "2147483648"}, {6, "-1"}, {6, "2147483648"}, {8, summaryTestID}, {8, "01937e50-4980-7000-0000-000000000001"}, {8, strings.ToUpper(summaryRequestKey)}, {8, "bad"}} {
		args := append([]string(nil), valid...)
		args[change.index] = change.value
		tests = append(tests, args)
	}
	// Omitted zero must not silently become an initial pointer expectation.
	tests = append(tests, append(append([]string(nil), valid[:5]...), valid[7:]...))
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			m := &summaryControlMock{}
			out, _, err := executeSummaryControl(t, m, "", args...)
			if err == nil || m.calls != 0 || out != "" {
				t.Fatalf("invalid admission reached API: %v calls=%d output=%s", err, m.calls, out)
			}
		})
	}
}

func TestSummaryAdmissionRetriesPreserveIdentityAndExpectations(t *testing.T) {
	for _, draft := range []bool{false, true} {
		args := []string{"generate", summaryTestID, "3"}
		if draft {
			args = []string{"generate-draft", summaryTestID}
		}
		args = append(args, "--source-revision", "3", "--summary-revision", "0", "--request-key", summaryRequestKey)
		m := &summaryControlMock{failure: errors.New("outcome unknown")}
		out, _, err := executeSummaryControl(t, m, "", args...)
		if err == nil || m.calls != 1 || out != "" {
			t.Fatalf("uncertain write was hidden/retried: %v %+v", err, m)
		}
		original := m.admission
		m.failure = nil
		m.response = json.RawMessage(`{"id":"job","state":"queued"}`)
		out, _, err = executeSummaryControl(t, m, "", args...)
		if err != nil || m.calls != 2 || m.admission != original || original.IdempotencyKey != summaryRequestKey || original.ExpectedSourceRevision != 3 || original.ExpectedSummaryRevision != 0 || !json.Valid([]byte(out)) {
			t.Fatalf("retry changed identity or expectations: %v %+v", err, m)
		}
	}
}

func TestSummaryControlStatusAndCancellation(t *testing.T) {
	m := &summaryControlMock{response: json.RawMessage(`{"state":"absent"}`)}
	out, _, err := executeSummaryControl(t, m, "", "draft-status", summaryTestID, "--source-revision", "7", "-o", "yaml")
	if err != nil || m.revision != 7 || m.source != summaryTestID || !strings.Contains(out, "state: absent") {
		t.Fatalf("wrong draft projection: %v %s %+v", err, out, m)
	}
	m.calls = 0
	_, _, err = executeSummaryControl(t, m, "n\n", "cancel", summaryTestID)
	if exitCodeFor(err) != exitCancelled || m.calls != 0 {
		t.Fatalf("declined cancellation reached API: %v %+v", err, m)
	}
	_, _, err = executeSummaryControl(t, m, "", "cancel", summaryTestID, "--yes")
	if err != nil || m.calls != 1 {
		t.Fatalf("explicit cancellation failed: %v %+v", err, m)
	}
}

func TestSummaryRequestKeyIsOfflineAndUnique(t *testing.T) {
	m := &summaryControlMock{}
	seen := map[string]bool{}
	for range 2 {
		output, diagnostics, err := executeSummaryControl(t, m, "", "request-key")
		var result map[string]string
		decode := json.Unmarshal([]byte(output), &result)
		id, parse := uuid.Parse(result["idempotency_key"])
		if err != nil || decode != nil || parse != nil || id.Version() != 7 || seen[id.String()] || m.calls != 0 || diagnostics != "" {
			t.Fatalf("bad offline key: %s %v", output, err)
		}
		seen[id.String()] = true
	}
	c, _, _ := newStackProfileSummaryCommand().Find([]string{"request-key"})
	if commandRequiresAuth(c) {
		t.Fatal("offline key requires authentication")
	}
}

func summaryApprovalArgs() []string {
	return []string{"policy", "approve", "--policy-revision", "0", "--consent-digest", strings.Repeat("a", 64), "--accept-data-use", "--confirm-provider-terms", "--content-retention-days", "7", "--metadata-retention-days", "30", "--daily-tokens", "100000", "--request-tokens", "1000", "--daily-usd", "1.000001", "--request-usd", "0.000001"}
}
func TestSummaryPolicyExplicitLimitsAndAcknowledgements(t *testing.T) {
	m := &summaryControlMock{response: json.RawMessage(`{"revision":1,"state":"approved"}`)}
	output, diagnostics, err := executeSummaryControl(t, m, "", summaryApprovalArgs()...)
	if err != nil || m.calls != 1 || diagnostics != "" || !json.Valid([]byte(output)) || m.approval.ExpectedRevision != 0 || !m.approval.AcceptDataUse || !m.approval.ConfirmProviderTerms || m.approval.Limits.DailyCostMicrousd != 1000001 || m.approval.Limits.RequestCostMicrousd != 1 {
		t.Fatalf("approval lost exact consent: %v %+v", err, m)
	}
	for _, replacement := range []struct{ flag, value string }{
		{"--daily-usd", "1e2"}, {"--daily-usd", "1000000.000001"}, {"--request-usd", "1000.000001"}, {"--request-usd", "2"},
		{"--metadata-retention-days", "6"}, {"--content-retention-days", "91"}, {"--request-tokens", "100001"},
		{"--policy-revision", "9223372036854775807"}, {"--consent-digest", "bad"},
	} {
		args := summaryApprovalArgs()
		for i := range args {
			if args[i] == replacement.flag {
				args[i+1] = replacement.value
			}
		}
		m := &summaryControlMock{}
		_, _, err := executeSummaryControl(t, m, "", args...)
		if err == nil || m.calls != 0 {
			t.Fatalf("invalid %s admitted: %v %+v", replacement.flag, err, m)
		}
	}
	for _, flag := range []string{"--accept-data-use", "--confirm-provider-terms"} {
		for _, omit := range []bool{false, true} {
			args := summaryApprovalArgs()
			for i := range args {
				if args[i] == flag {
					if omit {
						args = append(args[:i], args[i+1:]...)
					} else {
						args[i] = flag + "=false"
					}
					break
				}
			}
			m := &summaryControlMock{}
			_, _, err := executeSummaryControl(t, m, "", args...)
			if err == nil || m.calls != 0 {
				t.Fatalf("missing acknowledgement accepted: %v", err)
			}
		}
	}
}

func TestSummaryPolicyRevocationAndMalformedResponses(t *testing.T) {
	m := &summaryControlMock{response: json.RawMessage(`{"revision":3,"state":"revoked"}`)}
	_, _, err := executeSummaryControl(t, m, "n\n", "policy", "revoke", "--policy-revision", "2")
	if exitCodeFor(err) != exitCancelled || m.calls != 0 {
		t.Fatalf("declined revocation reached API: %v", err)
	}
	_, _, err = executeSummaryControl(t, m, "", "policy", "revoke", "--policy-revision", "2", "--yes")
	if err != nil || m.calls != 1 || m.revoke.ExpectedRevision != 2 {
		t.Fatalf("revocation lost revision: %v %+v", err, m)
	}
	for _, payload := range []string{"null", "[]", "{}", "{broken"} {
		m := &summaryControlMock{response: json.RawMessage(payload)}
		out, _, err := executeSummaryControl(t, m, "", "policy", "get")
		if err == nil || out != "" || m.calls != 1 {
			t.Fatalf("malformed response rendered: %v %s", err, out)
		}
	}
}

func TestSummaryUSDParsingHasNoRoundingOrOverflow(t *testing.T) {
	for raw, want := range map[string]int64{"0.000001": 1, "1000000": 1000000000000, "1.234567": 1234567, "0.01": 10000} {
		got, err := summaryUSDMicros(raw, 1000000000000)
		if err != nil || got != want {
			t.Fatalf("%s = %d %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "0", "0.0000001", "1e3", "NaN", "-1", "+1", ".1", "1.", " 1", "1,00", "99999999999999", "1000000.000001"} {
		if _, err := summaryUSDMicros(raw, 1000000000000); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestSummaryAdmissionAcceptsTheServerRevisionBoundary(t *testing.T) {
	m := &summaryControlMock{response: json.RawMessage(`{"state":"queued"}`)}
	_, _, err := executeSummaryControl(t, m, "", "generate", summaryTestID, "2147483647", "--source-revision", "2147483647", "--summary-revision", "2147483647", "--request-key", summaryRequestKey)
	if err != nil || m.calls != 1 || m.revision != 2147483647 || m.admission.ExpectedSourceRevision != 2147483647 || m.admission.ExpectedSummaryRevision != 2147483647 {
		t.Fatalf("server boundary refused or changed: %v %+v", err, m)
	}
}
