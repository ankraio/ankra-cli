package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

const testProposalID = "5d1c1a52-0000-4000-8000-000000000001"

type accessPolicyProposalMock struct {
	baseMock
	mutex     sync.Mutex
	policy    client.ClusterAccessPolicy
	requests  []client.ProposeClusterAccessPolicyRequest
	proposed  *client.ClusterAccessPolicyProposal
	refusal   error
	statuses  []string
	reads     int
	listed    []string
	listing   []client.ClusterAccessPolicyProposal
	listError error
	readError error
	// organisation is the organisation every response names; empty plays
	// a platform that predates the field.
	organisation client.ClusterAccessPolicyOrganisation
}

func (m *accessPolicyProposalMock) GetClusterAccessPolicy(ctx context.Context) (*client.ClusterAccessPolicyRead, error) {
	return &client.ClusterAccessPolicyRead{Policy: m.policy, ClusterAccessPolicyOrganisation: m.organisation}, nil
}

func (m *accessPolicyProposalMock) ProposeClusterAccessPolicy(ctx context.Context, request client.ProposeClusterAccessPolicyRequest) (*client.ClusterAccessPolicyProposal, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.requests = append(m.requests, request)
	if m.refusal != nil {
		return nil, m.refusal
	}
	proposed := client.ProposedClusterAccessPolicy{
		CreatorGrantRole:      m.policy.CreatorGrantRole,
		MaxGrantRole:          m.policy.MaxGrantRole,
		ElevatedMaxTTLSeconds: m.policy.ElevatedMaxTTLSeconds,
		RequireReasonFromRole: m.policy.RequireReasonFromRole,
	}
	if request.CreatorGrantRole != nil {
		proposed.CreatorGrantRole = *request.CreatorGrantRole
	}
	if request.MaxGrantRole != nil {
		proposed.MaxGrantRole = *request.MaxGrantRole
	}
	if request.ElevatedMaxTTLSeconds != nil {
		proposed.ElevatedMaxTTLSeconds = request.ElevatedMaxTTLSeconds
	}
	if request.ClearElevatedMaxTTL {
		proposed.ElevatedMaxTTLSeconds = nil
	}
	if request.RequireReasonFromRole != nil {
		proposed.RequireReasonFromRole = request.RequireReasonFromRole
	}
	if request.ClearRequireReasonFromRole {
		proposed.RequireReasonFromRole = nil
	}
	m.proposed = proposalFixture(client.ProposalStatusPending, m.policy, proposed)
	m.proposed.Reason = request.Reason
	m.proposed.ClusterAccessPolicyOrganisation = m.organisation
	copied := *m.proposed
	return &copied, nil
}

func (m *accessPolicyProposalMock) GetClusterAccessPolicyProposal(ctx context.Context, proposalID string) (*client.ClusterAccessPolicyProposal, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.readError != nil {
		return nil, m.readError
	}
	if m.proposed == nil || proposalID != m.proposed.ID {
		notFound := `{"detail":"Cluster access policy proposal not found"}`
		if m.organisation.OrganisationID != "" {
			encoded, _ := json.Marshal(m.organisation)
			notFound = `{"detail":"Cluster access policy proposal not found",` + strings.TrimPrefix(string(encoded), "{")
		}
		return nil, client.ClusterAccessPolicyProposalResponseError("read", 404, []byte(notFound))
	}
	copied := *m.proposed
	if m.reads < len(m.statuses) {
		copied.Status = m.statuses[m.reads]
	} else if len(m.statuses) > 0 {
		copied.Status = m.statuses[len(m.statuses)-1]
	}
	m.reads++
	if copied.Status == client.ProposalStatusApplied {
		decided := "2026-10-19T12:00:00Z"
		copied.DecidedAt = &decided
		copied.Current = client.ClusterAccessPolicy{
			CreatorGrantRole: copied.Proposed.CreatorGrantRole, MaxGrantRole: copied.Proposed.MaxGrantRole,
			ElevatedMaxTTLSeconds: copied.Proposed.ElevatedMaxTTLSeconds,
			RequireReasonFromRole: copied.Proposed.RequireReasonFromRole, IsConfigured: true,
		}
	}
	return &copied, nil
}

func (m *accessPolicyProposalMock) ListClusterAccessPolicyProposals(ctx context.Context, status string) (*client.ClusterAccessPolicyProposalList, error) {
	m.listed = append(m.listed, status)
	if m.listError != nil {
		return nil, m.listError
	}
	return &client.ClusterAccessPolicyProposalList{Proposals: m.listing, ClusterAccessPolicyOrganisation: m.organisation}, nil
}

func proposalFixture(status string, live client.ClusterAccessPolicy, proposed client.ProposedClusterAccessPolicy) *client.ClusterAccessPolicyProposal {
	tokenName := "laptop"
	return &client.ClusterAccessPolicyProposal{
		ID:                 testProposalID,
		Status:             status,
		Proposed:           proposed,
		Base:               live,
		Current:            live,
		BaseMatchesCurrent: true,
		Reason:             "PLA-878 no standing admin",
		ProposedBy:         client.ClusterAccessPolicyProposer{Principal: "ops@example.com", TokenName: &tokenName},
		CreatedAt:          "2026-10-19T10:00:00Z",
		ExpiresAt:          "2026-10-26T10:00:00.123456Z",
		ApplyURL:           "https://platform.ankra.app/next/organisation/roles?org=o-1&proposal=" + testProposalID,
	}
}

func defaultLivePolicy() client.ClusterAccessPolicy {
	return client.ClusterAccessPolicy{CreatorGrantRole: "cluster-admin", MaxGrantRole: "cluster-admin"}
}

// resetAccessPolicyFlags clears every flag of the access-policy commands,
// so one test's --wait or -o json does not leak into the next.
func resetAccessPolicyFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		for _, command := range []interface{ Flags() *pflag.FlagSet }{
			orgAccessPolicyGetCmd, orgAccessPolicySetCmd, orgAccessPolicyProposalsCmd, orgAccessPolicyProposalCmd,
		} {
			command.Flags().VisitAll(func(flag *pflag.Flag) {
				_ = flag.Value.Set(flag.DefValue)
				flag.Changed = false
			})
		}
	}
	reset()
	originalInterval := accessPolicyProposalPollInterval
	accessPolicyProposalPollInterval = time.Millisecond
	t.Cleanup(func() {
		reset()
		accessPolicyProposalPollInterval = originalInterval
	})
}

func TestOrgAccessPolicySetProposesOnlyWhatChangesAndPrintsTheApplyLink(t *testing.T) {
	resetAccessPolicyFlags(t)
	mock := &accessPolicyProposalMock{policy: defaultLivePolicy()}
	setMockClient(t, mock)

	output, err := executeCommand("org", "access-policy", "set", "--max-role", "edit", "--creator-role", "view",
		"--reason", "PLA-878 no standing admin")
	if err != nil {
		t.Fatalf("set: %v\n%s", err, output)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("expected one proposal, got %d", len(mock.requests))
	}
	request := mock.requests[0]
	if request.MaxGrantRole == nil || *request.MaxGrantRole != "edit" || request.CreatorGrantRole == nil ||
		*request.CreatorGrantRole != "view" || request.ElevatedMaxTTLSeconds != nil || request.ClearElevatedMaxTTL ||
		request.RequireReasonFromRole != nil || request.ClearRequireReasonFromRole {
		t.Fatalf("request = %+v", request)
	}
	for _, want := range []string{
		"Nothing has changed yet",
		testProposalID,
		"2026-10-26 10:00 UTC",
		"/next/organisation/roles?org=o-1&proposal=" + testProposalID,
		"An organisation owner or admin must open the link and apply it; the CLI cannot apply it.",
		`ops@example.com (token "laptop")`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in:\n%s", want, output)
		}
	}
	changedRows := map[string]bool{"Creator role": true, "Ceiling": true, "Elevated lifetime": false, "Reason required": false}
	seenRows := 0
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 2 {
			continue
		}
		for label, changed := range changedRows {
			if !strings.HasPrefix(strings.TrimSpace(line[1:]), label) {
				continue
			}
			seenRows++
			if marked := strings.HasPrefix(line, "*"); marked != changed {
				t.Errorf("row %q marked=%v, want %v: %q", label, marked, changed, line)
			}
		}
	}
	if seenRows != len(changedRows) {
		t.Errorf("expected %d comparison rows, saw %d in:\n%s", len(changedRows), seenRows, output)
	}
}

func TestOrgAccessPolicySetClearsAndParsesBounds(t *testing.T) {
	resetAccessPolicyFlags(t)
	mock := &accessPolicyProposalMock{policy: defaultLivePolicy()}
	setMockClient(t, mock)

	if output, err := executeCommand("org", "access-policy", "set", "--elevated-max-ttl", "7d",
		"--require-reason-from", "edit", "--reason", "audit"); err != nil {
		t.Fatalf("set: %v\n%s", err, output)
	}
	first := mock.requests[0]
	if first.ElevatedMaxTTLSeconds == nil || *first.ElevatedMaxTTLSeconds != 7*24*3600 ||
		first.RequireReasonFromRole == nil || *first.RequireReasonFromRole != "edit" {
		t.Fatalf("request = %+v", first)
	}

	resetAccessPolicyFlags(t)
	if output, err := executeCommand("org", "access-policy", "set", "--elevated-max-ttl", "none",
		"--require-reason-from", "none", "--reason", "audit"); err != nil {
		t.Fatalf("set: %v\n%s", err, output)
	}
	second := mock.requests[1]
	if !second.ClearElevatedMaxTTL || !second.ClearRequireReasonFromRole || second.MaxGrantRole != nil {
		t.Fatalf("request = %+v", second)
	}
}

func TestOrgAccessPolicySetRefusesBadInvocationsBeforeSending(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown creator role":   {"--creator-role", "root", "--reason", "x y z"},
		"none is no ceiling":     {"--max-role", "none", "--reason", "x y z"},
		"unknown reason role":    {"--require-reason-from", "owner", "--reason", "x y z"},
		"bad lifetime":           {"--elevated-max-ttl", "soon", "--reason", "x y z"},
		"nothing to propose":     {"--reason", "x y z"},
		"blank reason":           {"--max-role", "edit", "--reason", "   "},
		"missing reason":         {"--max-role", "edit"},
		"wait without a timeout": {"--max-role", "edit", "--reason", "x y z", "--wait", "--timeout", "0s"},
	} {
		t.Run(name, func(t *testing.T) {
			resetAccessPolicyFlags(t)
			mock := &accessPolicyProposalMock{policy: defaultLivePolicy()}
			setMockClient(t, mock)
			_, err := executeCommand(append([]string{"org", "access-policy", "set"}, args...)...)
			if err == nil || exitCodeFor(err) != exitUsage {
				t.Fatalf("expected a usage error, got %v (exit %d)", err, exitCodeFor(err))
			}
			if len(mock.requests) != 0 {
				t.Fatalf("nothing may be sent for an invalid invocation, sent %+v", mock.requests)
			}
		})
	}
}

func TestOrgAccessPolicySetRefusalsExitWithTheirClass(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		status   int
		body     string
		wantCode int
		wantText string
	}{
		{"unchanged", 422, `{"detail":[{"type":"value_error","loc":["body"],"msg":"Value error, the proposed policy is identical to the current policy"}]}`,
			exitUsage, "the proposed policy is identical to the current policy"},
		{"out of range", 422, `{"detail":[{"type":"greater_than_equal","loc":["body","elevated_max_ttl_seconds"],"msg":"Input should be greater than or equal to 60"}]}`,
			exitUsage, "elevated_max_ttl_seconds: Input should be greater than or equal to 60"},
		{"admin only", 403, `{"detail":"Only organisation admins can manage cluster access"}`, exitForbidden, "Only organisation admins"},
		{"permission denied", 403, `{"detail":"permission_denied","permission":"kube_access.manage"}`, exitForbidden, "kube_access.manage"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetAccessPolicyFlags(t)
			mock := &accessPolicyProposalMock{policy: defaultLivePolicy(),
				refusal: client.ClusterAccessPolicyProposalResponseError("propose", testCase.status, []byte(testCase.body))}
			setMockClient(t, mock)
			_, err := executeCommand("org", "access-policy", "set", "--max-role", "edit", "--reason", "x y z")
			if err == nil || exitCodeFor(err) != testCase.wantCode || !strings.Contains(err.Error(), testCase.wantText) {
				t.Fatalf("got %v (exit %d), want exit %d naming %q", err, exitCodeFor(err), testCase.wantCode, testCase.wantText)
			}
		})
	}
}

func TestOrgAccessPolicySetWaitExitsByOutcome(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		statuses []string
		timeout  string
		wantCode int
		wantText string
	}{
		{"applied", []string{"pending", "pending", "applied"}, "1m", exitOK, "was applied at 2026-10-19 12:00 UTC"},
		{"dismissed", []string{"pending", "dismissed"}, "1m", exitCancelled, "dismissed"},
		{"superseded", []string{"superseded"}, "1m", exitError, "replaced by a newer proposal"},
		{"expired", []string{"expired"}, "1m", exitError, "expired"},
		{"timeout", []string{"pending"}, "30ms", exitWaitTimeout, "can still be applied at https://"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetAccessPolicyFlags(t)
			mock := &accessPolicyProposalMock{policy: defaultLivePolicy(), statuses: testCase.statuses}
			setMockClient(t, mock)
			output, err := executeCommand("org", "access-policy", "set", "--max-role", "edit", "--reason", "x y z",
				"--wait", "--timeout", testCase.timeout)
			if exitCodeFor(err) != testCase.wantCode {
				t.Fatalf("exit %d (%v), want %d\n%s", exitCodeFor(err), err, testCase.wantCode, output)
			}
			combined := output
			if err != nil {
				combined += err.Error()
			}
			if !strings.Contains(combined, testCase.wantText) {
				t.Fatalf("expected %q in:\n%s", testCase.wantText, combined)
			}
		})
	}
}

func TestOrgAccessPolicySetWaitJSONPrintsOneDocument(t *testing.T) {
	resetAccessPolicyFlags(t)
	mock := &accessPolicyProposalMock{policy: defaultLivePolicy(), statuses: []string{"applied"}}
	setMockClient(t, mock)

	var stdout, stderr strings.Builder
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"org", "access-policy", "set", "--max-role", "edit", "--reason", "x y z", "--wait", "-o", "json"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("set --wait -o json: %v\n%s", err, stderr.String())
	}
	var decoded client.ClusterAccessPolicyProposal
	if err := json.Unmarshal([]byte(stdout.String()), &decoded); err != nil {
		t.Fatalf("stdout must be one JSON document: %v\n%s", err, stdout.String())
	}
	if decoded.Status != client.ProposalStatusApplied || decoded.ApplyURL == "" {
		t.Fatalf("decoded = %+v", decoded)
	}
	if !strings.Contains(stderr.String(), "the CLI cannot apply it") {
		t.Fatalf("the human text belongs on stderr, got: %s", stderr.String())
	}
}

func TestOrgAccessPolicyProposalsListsAndFilters(t *testing.T) {
	resetAccessPolicyFlags(t)
	live := defaultLivePolicy()
	pending := proposalFixture(client.ProposalStatusPending, live,
		client.ProposedClusterAccessPolicy{CreatorGrantRole: "view", MaxGrantRole: "edit"})
	mock := &accessPolicyProposalMock{policy: live, listing: []client.ClusterAccessPolicyProposal{*pending}}
	setMockClient(t, mock)

	output, err := executeCommand("org", "access-policy", "proposals")
	if err != nil {
		t.Fatalf("proposals: %v", err)
	}
	for _, want := range []string{testProposalID, "pending", "creator role view, ceiling edit", "Apply " + testProposalID, "the CLI cannot apply it"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in:\n%s", want, output)
		}
	}
	resetAccessPolicyFlags(t)
	if _, err := executeCommand("org", "access-policy", "proposals", "--status", "all"); err != nil {
		t.Fatalf("proposals --status all: %v", err)
	}
	if strings.Join(mock.listed, ",") != "pending," {
		t.Fatalf("status filters sent: %q", mock.listed)
	}
	resetAccessPolicyFlags(t)
	if _, err := executeCommand("org", "access-policy", "proposals", "--status", "open"); exitCodeFor(err) != exitUsage {
		t.Fatalf("an unknown --status must be a usage error, got %v", err)
	}
}

func TestOrgAccessPolicyProposalShowsOneAndNotFoundExits3(t *testing.T) {
	resetAccessPolicyFlags(t)
	mock := &accessPolicyProposalMock{policy: defaultLivePolicy()}
	mock.proposed = proposalFixture(client.ProposalStatusPending, defaultLivePolicy(),
		client.ProposedClusterAccessPolicy{CreatorGrantRole: "cluster-admin", MaxGrantRole: "admin"})
	mock.proposed.BaseMatchesCurrent = false
	setMockClient(t, mock)

	output, err := executeCommand("org", "access-policy", "proposal", testProposalID)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	for _, want := range []string{"PLA-878 no standing admin", "applying it will be refused", "Apply:", "the CLI cannot apply it"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in:\n%s", want, output)
		}
	}
	_, err = executeCommand("org", "access-policy", "proposal", "00000000-0000-4000-8000-000000000000")
	if exitCodeFor(err) != exitNotFound {
		t.Fatalf("an unknown proposal must exit 3, got %v (exit %d)", err, exitCodeFor(err))
	}
}

func TestOrgAccessPolicyGetNamesThePendingProposal(t *testing.T) {
	resetAccessPolicyFlags(t)
	live := defaultLivePolicy()
	pending := proposalFixture(client.ProposalStatusPending, live,
		client.ProposedClusterAccessPolicy{CreatorGrantRole: "view", MaxGrantRole: "edit"})
	mock := &accessPolicyProposalMock{policy: live, listing: []client.ClusterAccessPolicyProposal{*pending}}
	setMockClient(t, mock)

	output, err := executeCommand("org", "access-policy", "get")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(output, "Pending proposal:   "+testProposalID) || !strings.Contains(output, pending.ApplyURL) {
		t.Fatalf("expected the pending proposal and its link, got:\n%s", output)
	}

	resetAccessPolicyFlags(t)
	jsonOutput, err := executeCommand("org", "access-policy", "get", "-o", "json")
	if err != nil {
		t.Fatalf("get -o json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonOutput), &decoded); err != nil {
		t.Fatalf("get -o json: %v\n%s", err, jsonOutput)
	}
	if decoded["max_grant_role"] != "cluster-admin" || decoded["pending_proposal"] == nil {
		t.Fatalf("get -o json must keep the policy keys and add pending_proposal, got: %v", decoded)
	}
}

// A proposals read that fails is said out loud on stderr, so "could not
// check" never reads as "no proposal"; a platform without the route (404)
// is simply "no proposal".
func TestOrgAccessPolicyGetWarnsWhenItCannotCheckForAProposal(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		err      error
		wantWarn bool
	}{
		{"older platform", client.ClusterAccessPolicyProposalResponseError("list", 404, []byte(`{"detail":"Not Found"}`)), false},
		{"refused", client.ClusterAccessPolicyProposalResponseError("list", 403,
			[]byte(`{"detail":"Only organisation admins can view cluster access"}`)), true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetAccessPolicyFlags(t)
			mock := &accessPolicyProposalMock{policy: defaultLivePolicy(), listError: testCase.err}
			setMockClient(t, mock)
			var stdout, stderr strings.Builder
			rootCmd.SetOut(&stdout)
			rootCmd.SetErr(&stderr)
			rootCmd.SetArgs([]string{"org", "access-policy", "get"})
			t.Cleanup(func() {
				rootCmd.SetOut(nil)
				rootCmd.SetErr(nil)
			})
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("get must not fail on the proposals read: %v", err)
			}
			if !strings.Contains(stdout.String(), "Ceiling:") {
				t.Fatalf("the policy must still print, got: %s", stdout.String())
			}
			if warned := strings.Contains(stderr.String(), "could not check for a pending proposal"); warned != testCase.wantWarn {
				t.Fatalf("warned=%v, want %v; stderr: %s", warned, testCase.wantWarn, stderr.String())
			}
		})
	}
}

// TestOrgAccessPolicyCommandsNameTheOrganisation pins ankra-gsjnfb.5: get,
// set, proposals and proposal each print the organisation they read or
// proposed for, -o json carries it, and a proposal id that is not in the
// organisation read names that organisation and still exits 3. On
// 2026-10-19 a support read in the wrong organisation looked exactly like
// the customer's, and the customer was told their applied policy was not
// applied.
func TestOrgAccessPolicyCommandsNameTheOrganisation(t *testing.T) {
	depictName := "Depict"
	depict := client.ClusterAccessPolicyOrganisation{OrganisationID: "2f853b14-78b0-471a-937c-1ee13a08ddff", OrganisationName: &depictName}
	const depictLine = "Depict (2f853b14-78b0-471a-937c-1ee13a08ddff)"
	live := defaultLivePolicy()
	pending := proposalFixture(client.ProposalStatusPending, live,
		client.ProposedClusterAccessPolicy{CreatorGrantRole: "view", MaxGrantRole: "edit"})
	pending.ClusterAccessPolicyOrganisation = depict
	mock := &accessPolicyProposalMock{policy: live, listing: []client.ClusterAccessPolicyProposal{*pending}, organisation: depict}
	setMockClient(t, mock)

	resetAccessPolicyFlags(t)
	output, err := executeCommand("org", "access-policy", "get")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.HasPrefix(output, "Organisation:       "+depictLine+"\n") {
		t.Fatalf("get must open with the organisation it read, got:\n%s", output)
	}

	resetAccessPolicyFlags(t)
	jsonOutput, err := executeCommand("org", "access-policy", "get", "-o", "json")
	if err != nil {
		t.Fatalf("get -o json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonOutput), &decoded); err != nil {
		t.Fatalf("get -o json: %v\n%s", err, jsonOutput)
	}
	if decoded["organisation_id"] != depict.OrganisationID || decoded["organisation_name"] != "Depict" ||
		decoded["max_grant_role"] != "cluster-admin" || decoded["is_configured"] != false {
		t.Fatalf("get -o json must add the organisation and keep the policy keys, got: %v", decoded)
	}

	resetAccessPolicyFlags(t)
	output, err = executeCommand("org", "access-policy", "proposals")
	if err != nil || !strings.HasPrefix(output, "Organisation: "+depictLine+"\n") {
		t.Fatalf("proposals must name the organisation it listed, got %v:\n%s", err, output)
	}
	resetAccessPolicyFlags(t)
	jsonOutput, err = executeCommand("org", "access-policy", "proposals", "-o", "json")
	if err != nil || !strings.Contains(jsonOutput, `"organisation_name": "Depict"`) {
		t.Fatalf("proposals -o json must carry the organisation, got %v:\n%s", err, jsonOutput)
	}

	resetAccessPolicyFlags(t)
	output, err = executeCommand("org", "access-policy", "set", "--max-role", "edit", "--reason", "tighten")
	if err != nil || !strings.Contains(output, "Organisation: "+depictLine+"\n") {
		t.Fatalf("set must name the organisation it proposed for, got %v:\n%s", err, output)
	}
	resetAccessPolicyFlags(t)
	output, err = executeCommand("org", "access-policy", "proposal", testProposalID)
	if err != nil || !strings.Contains(output, "Organisation: "+depictLine+"\n") {
		t.Fatalf("proposal must name its organisation, got %v:\n%s", err, output)
	}

	resetAccessPolicyFlags(t)
	_, err = executeCommand("org", "access-policy", "proposal", "a7aad9f0-0000-4000-8000-000000000000")
	if exitCodeFor(err) != exitNotFound {
		t.Fatalf("a proposal not in the organisation read must still exit 3, got %v (exit %d)", err, exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "not found in organisation "+depictLine) || !strings.Contains(err.Error(), "--org") {
		t.Fatalf("a proposal 404 must name the organisation searched and point at --org, got %q", err.Error())
	}
}
