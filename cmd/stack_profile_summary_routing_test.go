package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestSummaryRoutingFlagsPreserveExplicitPreviewAndApproval(t *testing.T) {
	for _, upstream := range []string{"google-vertex/europe", ""} {
		routing := client.ProfileSummaryRouting{Ingress: "europe", Upstream: upstream}
		flags := []string{"--openrouter-ingress=europe", "--openrouter-upstream=" + upstream}
		m := &summaryControlMock{response: json.RawMessage(`{"route_state":"available"}`)}
		out, diagnostics, err := executeSummaryControl(t, m, "", append([]string{"policy", "get", "-o", "json"}, flags...)...)
		if err != nil || m.calls != 1 || m.routing == nil || *m.routing != routing || diagnostics != "" || !json.Valid([]byte(out)) || m.approval.AcceptDataUse {
			t.Fatalf("preview lost explicit route or wrote approval: %v %+v", err, m)
		}
		m = &summaryControlMock{response: json.RawMessage(`{"revision":1,"state":"approved"}`)}
		out, diagnostics, err = executeSummaryControl(t, m, "", append(summaryApprovalArgs(), flags...)...)
		if err != nil || m.calls != 1 || m.routing != nil || m.approval.OpenRouterRouting == nil || *m.approval.OpenRouterRouting != routing || m.approval.ExpectedConsentDigest != strings.Repeat("a", 64) || diagnostics != "" || !json.Valid([]byte(out)) {
			t.Fatalf("approval lost inspected routing or refreshed digest: %v %+v", err, m)
		}
	}
}

func TestSummaryRoutingRejectsInvalidFlagsWithoutRequest(t *testing.T) {
	for _, flags := range [][]string{
		{"--openrouter-ingress=europe"}, {"--openrouter-upstream=google-vertex/europe"},
		{"--openrouter-ingress=asia", "--openrouter-upstream="},
		{"--openrouter-ingress=europe", "--openrouter-upstream=Google"},
		{"--openrouter-ingress=europe", "--openrouter-upstream=provider/region/extra"},
		{"--openrouter-ingress=europe", "--openrouter-upstream=provider?fallback=true"},
		{"--openrouter-ingress=europe", "--openrouter-upstream=" + strings.Repeat("a", 129)},
	} {
		for _, args := range [][]string{{"policy", "get"}, summaryApprovalArgs()} {
			m := &summaryControlMock{}
			out, _, err := executeSummaryControl(t, m, "", append(args, flags...)...)
			if err == nil || m.calls != 0 || out != "" || exitCodeFor(err) != exitUsage {
				t.Fatalf("invalid flags reached API: %v calls=%d output=%s", err, m.calls, out)
			}
		}
	}
}
