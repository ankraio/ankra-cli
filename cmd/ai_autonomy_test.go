package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type aiAutonomyMock struct {
	baseMock
	identity      *client.BoardIdentity
	provisioned   []string
	revokes       int
	pauseState    client.AIPauseState
	pauseCalls    []bool
	pauseReasons  []string
	provisionRole string

	autonomyState   client.AIAutonomyState
	autonomyCalls   []bool
	autonomyReasons []string
	autonomyOutcome *client.AIAutonomyOutcome
}

func (m *aiAutonomyMock) GetBoardIdentity() (*client.BoardIdentity, error) {
	if m.identity == nil {
		return &client.BoardIdentity{Provisioned: false}, nil
	}
	return m.identity, nil
}

func (m *aiAutonomyMock) ProvisionBoardIdentity(roleSlug string) (*client.BoardIdentity, error) {
	m.provisioned = append(m.provisioned, roleSlug)
	m.provisionRole = roleSlug
	role := roleSlug
	if role == "" {
		role = "operator"
	}
	userID := "b0a1c2d3-0000-4000-8000-000000000001"
	return &client.BoardIdentity{Provisioned: true, RoleSlug: role,
		Subject: "service-principal|board", AnkraUserID: &userID}, nil
}

func (m *aiAutonomyMock) RevokeBoardIdentity() (*client.BoardIdentity, error) {
	m.revokes++
	return &client.BoardIdentity{Provisioned: false}, nil
}

func (m *aiAutonomyMock) GetAIPauseState() (*client.AIPauseState, error) {
	state := m.pauseState
	return &state, nil
}

func (m *aiAutonomyMock) SetAIPause(paused bool, reason string) (*client.AIPauseOutcome, error) {
	m.pauseCalls = append(m.pauseCalls, paused)
	m.pauseReasons = append(m.pauseReasons, reason)
	m.pauseState.Paused = paused
	return &client.AIPauseOutcome{AIPauseState: client.AIPauseState{Paused: paused},
		CancelledSessions: 2, CancelledRuns: 1}, nil
}

// An organisation with no board identity is the state in which every
// designated worker escalates instead of working, so the CLI says so and
// names the command that fixes it rather than printing an empty record.
func TestBoardIdentityStatusNamesTheFixWhenAbsent(t *testing.T) {
	mock := &aiAutonomyMock{}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiBoardIdentityStatusCmd},
		"ai", "board-identity", "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(out, "no identity") ||
		!strings.Contains(out, "ankra ai board-identity provision") {
		t.Fatalf("status output = %s", out)
	}
}

func TestBoardIdentityProvisionDefaultsToOperator(t *testing.T) {
	mock := &aiAutonomyMock{}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiBoardIdentityProvisionCmd},
		"ai", "board-identity", "provision")
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	if len(mock.provisioned) != 1 || mock.provisioned[0] != "" {
		t.Fatalf("provision sent %+v, want the platform default", mock.provisioned)
	}
	if !strings.Contains(out, "operator role") {
		t.Fatalf("provision output = %s", out)
	}

	resetTreeFlags(t, aiBoardIdentityProvisionCmd)
	mock = &aiAutonomyMock{}
	if _, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiBoardIdentityProvisionCmd},
		"ai", "board-identity", "provision", "--role", "viewer"); err != nil {
		t.Fatalf("provision --role failed: %v", err)
	}
	if mock.provisionRole != "viewer" {
		t.Fatalf("role sent = %q", mock.provisionRole)
	}
}

// Standing the identity down stops every board agent, so it asks first.
func TestBoardIdentityRevokeAsksBeforeStandingDown(t *testing.T) {
	mock := &aiAutonomyMock{}

	_, err := runConfirmCommand(t, mock, "n\n", []*cobra.Command{aiBoardIdentityRevokeCmd},
		"ai", "board-identity", "revoke")
	if err == nil {
		t.Fatal("a declined confirmation must not revoke")
	}
	if got := exitCodeFor(err); got != exitCancelled {
		t.Errorf("expected exit code %d, got %d", exitCancelled, got)
	}
	if mock.revokes != 0 {
		t.Fatalf("revoked despite declining: %d", mock.revokes)
	}

	resetTreeFlags(t, aiBoardIdentityRevokeCmd)
	out, err := runConfirmCommand(t, mock, "y\n", []*cobra.Command{aiBoardIdentityRevokeCmd},
		"ai", "board-identity", "revoke")
	if err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	if mock.revokes != 1 || !strings.Contains(out, "stood down") {
		t.Fatalf("revokes=%d out=%s", mock.revokes, out)
	}
}

func TestAutonomyStatusReadsTheKillSwitch(t *testing.T) {
	reason := "incident 42"
	pausedAt := "2026-08-25T18:00:00Z"
	mock := &aiAutonomyMock{pauseState: client.AIPauseState{
		Paused: true, PausedAt: &pausedAt, Reason: &reason}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiAutonomyStatusCmd},
		"ai", "autonomy", "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	for _, want := range []string{"STOPPED", "incident 42", "ankra ai autonomy start-all"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output lacks %q: %s", want, out)
		}
	}
}

// Stopping all AI cancels live work, so it asks first and reports what it
// cancelled rather than claiming a silent success.
func TestAutonomyPauseConfirmsAndReportsWhatItCancelled(t *testing.T) {
	mock := &aiAutonomyMock{}

	_, err := runConfirmCommand(t, mock, "n\n", []*cobra.Command{aiAutonomyStopAllCmd},
		"ai", "autonomy", "stop-all")
	if err == nil || exitCodeFor(err) != exitCancelled {
		t.Fatalf("declining must cancel, got %v", err)
	}
	if len(mock.pauseCalls) != 0 {
		t.Fatalf("paused despite declining: %+v", mock.pauseCalls)
	}

	resetTreeFlags(t, aiAutonomyStopAllCmd)
	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiAutonomyStopAllCmd},
		"ai", "autonomy", "stop-all", "--yes", "--reason", "  incident 42  ")
	if err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	if len(mock.pauseCalls) != 1 || !mock.pauseCalls[0] || mock.pauseReasons[0] != "incident 42" {
		t.Fatalf("pause sent %+v / %+v", mock.pauseCalls, mock.pauseReasons)
	}
	if !strings.Contains(out, "Cancelled 2 session(s) and 1 agent run(s)") {
		t.Fatalf("pause output = %s", out)
	}
}

func TestAutonomyResumeReleasesTheSwitch(t *testing.T) {
	mock := &aiAutonomyMock{pauseState: client.AIPauseState{Paused: true}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiAutonomyStartAllCmd},
		"ai", "autonomy", "start-all")
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if len(mock.pauseCalls) != 1 || mock.pauseCalls[0] {
		t.Fatalf("resume sent %+v", mock.pauseCalls)
	}
	if !strings.Contains(out, "available") {
		t.Fatalf("resume output = %s", out)
	}
}

func (m *aiAutonomyMock) GetAIAutonomyState() (*client.AIAutonomyState, error) {
	state := m.autonomyState
	return &state, nil
}

func (m *aiAutonomyMock) SetAIAutonomyPause(paused bool, reason string) (*client.AIAutonomyOutcome, error) {
	m.autonomyCalls = append(m.autonomyCalls, paused)
	m.autonomyReasons = append(m.autonomyReasons, reason)
	if m.autonomyOutcome != nil {
		return m.autonomyOutcome, nil
	}
	return &client.AIAutonomyOutcome{
		AIAutonomyState:   client.AIAutonomyState{Paused: paused},
		DisabledPolicyNow: paused,
		PausedNow:         []client.AutonomyAgent{{ID: "a1", Name: "Gap Scanner"}},
	}, nil
}

// Pausing autonomous actions reports what it switched off, because that is
// exactly what a later resume will restore - the record the browser used to
// keep to itself.
func TestAutonomyPauseReportsWhatItSwitchedOff(t *testing.T) {
	mock := &aiAutonomyMock{}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiAutonomyPauseCmd},
		"ai", "autonomy", "pause", "--reason", "  change freeze  ")
	if err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	if len(mock.autonomyCalls) != 1 || !mock.autonomyCalls[0] ||
		mock.autonomyReasons[0] != "change freeze" {
		t.Fatalf("pause sent %+v / %+v", mock.autonomyCalls, mock.autonomyReasons)
	}
	for _, want := range []string{"1 agent(s) switched off and auto-remediation",
		"Gap Scanner", "ankra ai autonomy resume"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pause output lacks %q: %s", want, out)
		}
	}
}

func TestAutonomyResumeReportsWhatItRestored(t *testing.T) {
	mock := &aiAutonomyMock{autonomyOutcome: &client.AIAutonomyOutcome{
		AIAutonomyState: client.AIAutonomyState{Paused: false},
		ReEnabledPolicy: true,
		ResumedAgents:   []client.AutonomyAgent{{ID: "a1", Name: "Gap Scanner"}},
		AgentsGone:      2,
	}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiAutonomyResumeCmd},
		"ai", "autonomy", "resume")
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if len(mock.autonomyCalls) != 1 || mock.autonomyCalls[0] {
		t.Fatalf("resume sent %+v", mock.autonomyCalls)
	}
	if !strings.Contains(out, "1 agent(s) restored and auto-remediation") {
		t.Fatalf("resume output = %s", out)
	}
	// Agents deleted while paused are reported, not silently dropped.
	if !strings.Contains(out, "2 agent(s) the pause switched off no longer exist") {
		t.Fatalf("resume output hides the gone agents: %s", out)
	}
}

// One status answers "is anything switched off?" for both stops.
func TestAutonomyStatusReportsBothStops(t *testing.T) {
	reason := "change freeze"
	pausedAt := "2026-08-25T19:00:00Z"
	mock := &aiAutonomyMock{autonomyState: client.AIAutonomyState{
		Paused: true, PausedAt: &pausedAt, Reason: &reason, DisabledPolicy: true,
		PausedAgents: []client.AutonomyAgent{{ID: "a1", Name: "Gap Scanner"}},
	}}

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiAutonomyStatusCmd},
		"ai", "autonomy", "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(out, "AI is available for this organisation.") {
		t.Fatalf("status must report the hard stop too: %s", out)
	}
	for _, want := range []string{"Autonomous actions: PAUSED", "change freeze",
		"switched off 1 agent(s) and auto-remediation", "ankra ai autonomy resume"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output lacks %q: %s", want, out)
		}
	}
}
