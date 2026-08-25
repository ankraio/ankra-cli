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
	for _, want := range []string{"STOPPED", "incident 42", "ankra ai autonomy resume"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output lacks %q: %s", want, out)
		}
	}
}

// Stopping all AI cancels live work, so it asks first and reports what it
// cancelled rather than claiming a silent success.
func TestAutonomyPauseConfirmsAndReportsWhatItCancelled(t *testing.T) {
	mock := &aiAutonomyMock{}

	_, err := runConfirmCommand(t, mock, "n\n", []*cobra.Command{aiAutonomyPauseCmd},
		"ai", "autonomy", "pause")
	if err == nil || exitCodeFor(err) != exitCancelled {
		t.Fatalf("declining must cancel, got %v", err)
	}
	if len(mock.pauseCalls) != 0 {
		t.Fatalf("paused despite declining: %+v", mock.pauseCalls)
	}

	resetTreeFlags(t, aiAutonomyPauseCmd)
	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiAutonomyPauseCmd},
		"ai", "autonomy", "pause", "--yes", "--reason", "  incident 42  ")
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

	out, err := runConfirmCommand(t, mock, "", []*cobra.Command{aiAutonomyResumeCmd},
		"ai", "autonomy", "resume")
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
