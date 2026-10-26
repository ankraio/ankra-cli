package cmd

import (
	"context"
	"strings"
	"testing"

	"ankra/internal/client"
)

// applyRemovalMock refuses an apply that does not declare the removal the
// platform would make, the way the platform answers since ankra-kwr1sp.
type applyRemovalMock struct {
	applyOverrideMock
	removes string
}

func (mock *applyRemovalMock) ApplyCluster(ctx context.Context, request client.CreateImportClusterRequest, wait bool) (*client.ImportResponse, bool, error) {
	mock.applied = append(mock.applied, request)
	confirmed := request.Prune
	for _, member := range request.ConfirmedMemberRemovals {
		confirmed = confirmed || member == mock.removes
	}
	if mock.removes != "" && !confirmed {
		return nil, false, &client.MemberRemovalUnconfirmedError{StatusCode: 422,
			Message: "This change was not deployed: it would remove 1 member of stack 'hello-fleet' (" + mock.removes + "). Nothing was changed.",
			Members: []string{mock.removes}}
	}
	return &client.ImportResponse{Name: request.Name, ClusterId: "11111111-1111-1111-1111-111111111111"}, false, nil
}

// A stale file that would remove a member is refused with the platform's
// sentence and the flags that would confirm it (ankra-kwr1sp).
func TestClusterApplyReportsARefusedRemovalWithTheConfirmingFlags(t *testing.T) {
	resetClusterApplyFlags(t)
	mock := &applyRemovalMock{removes: "addon/team-exporter"}
	setMockClient(t, mock)
	path := writeApplyOverrideDocument(t)

	var output string
	var executeError error
	captureStdout(t, func() {
		output, executeError = executeCommand("cluster", "apply", "-f", path, "--wait")
	})
	if executeError == nil || !strings.Contains(executeError.Error(), "nothing was changed") {
		t.Fatalf("a refused removal must fail the apply, got %v", executeError)
	}
	for _, want := range []string{"addon/team-exporter", "--confirm-removal addon/team-exporter", "--prune"} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
	if mock.applied[0].Prune || len(mock.applied[0].ConfirmedMemberRemovals) != 0 {
		t.Errorf("an apply without the flags must declare nothing: %+v", mock.applied[0])
	}
}

func TestClusterApplySendsTheRemovalDeclarations(t *testing.T) {
	resetClusterApplyFlags(t)
	mock := &applyRemovalMock{removes: "addon/team-exporter"}
	setMockClient(t, mock)
	path := writeApplyOverrideDocument(t)
	captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "apply", "-f", path, "--wait",
			"--confirm-removal", "addon/team-exporter"); executeError != nil {
			t.Errorf("a confirmed apply failed: %v", executeError)
		}
	})
	if got := mock.applied[len(mock.applied)-1].ConfirmedMemberRemovals; strings.Join(got, ",") != "addon/team-exporter" {
		t.Fatalf("confirmed_member_removals = %v", got)
	}

	resetClusterApplyFlags(t)
	captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "apply", "-f", path, "--wait", "--prune"); executeError != nil {
			t.Errorf("a pruning apply failed: %v", executeError)
		}
	})
	if !mock.applied[len(mock.applied)-1].Prune {
		t.Fatal("--prune must be sent")
	}
}

func TestClusterApplyRefusesAScopedOrMalformedRemovalConfirmation(t *testing.T) {
	for _, value := range []string{"prod-eu:addon/team-exporter", "team-exporter", "volume/data"} {
		resetClusterApplyFlags(t)
		mock := &applyRemovalMock{}
		setMockClient(t, mock)
		path := writeApplyOverrideDocument(t)
		var executeError error
		captureStdout(t, func() {
			_, executeError = executeCommand("cluster", "apply", "-f", path, "--confirm-removal", value)
		})
		if executeError == nil {
			t.Errorf("--confirm-removal %q must be a usage error", value)
		}
		if len(mock.applied) != 0 {
			t.Errorf("--confirm-removal %q: nothing should be sent: %+v", value, mock.applied)
		}
	}
}
