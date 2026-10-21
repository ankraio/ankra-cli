package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// deleteGuardMock answers the kind lookup `ankra delete cluster` makes before
// the generic DELETE, and records whether that DELETE was ever sent.
type deleteGuardMock struct {
	baseMock

	cluster     client.ClusterListItem
	lookupError error
	deletes     int
}

func (m *deleteGuardMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.lookupError != nil {
		return client.ClusterListItem{}, m.lookupError
	}
	return m.cluster, nil
}

func (m *deleteGuardMock) DeleteCluster(ctx context.Context, name string) error {
	m.deletes++
	return nil
}

func runDeleteClusterWithYes(t *testing.T, mock *deleteGuardMock) (string, error) {
	t.Helper()
	originalForce := forceDelete
	forceDelete = true
	t.Cleanup(func() { forceDelete = originalForce })
	setMockClient(t, mock)

	var runError error
	output := captureStdout(t, func() {
		runError = deleteClusterCmd.RunE(deleteClusterCmd, []string{"games"})
	})
	return output, runError
}

// TestDeleteClusterFailsClosedWhenTheKindCannotBeRead pins ankra-kpq4ae: the
// cloud-cluster refusal used to run only when the lookup succeeded, so a
// transport, auth or platform failure skipped it and the generic DELETE went
// out anyway. Every failure now refuses with nothing deleted, exiting with
// the failure's own class.
func TestDeleteClusterFailsClosedWhenTheKindCannotBeRead(t *testing.T) {
	for name, testCase := range map[string]struct {
		lookupError error
		wantExit    int
	}{
		"a transport failure": {
			lookupError: errors.New(`Get "https://platform.ankra.app/api/v1/clusters": dial tcp: i/o timeout`),
			wantExit:    exitError,
		},
		"a platform error": {
			lookupError: client.NewUnexpectedResponseError(502, "unexpected status: 502 Bad Gateway"),
			wantExit:    exitError,
		},
		"a deadline": {
			lookupError: fmt.Errorf("listing clusters: %w", context.DeadlineExceeded),
			wantExit:    exitError,
		},
		"rejected credentials": {
			lookupError: client.ErrUnauthorized,
			wantExit:    exitAuth,
		},
		"a role without the permission": {
			lookupError: &client.PermissionDeniedError{Permission: "clusters.read"},
			wantExit:    exitForbidden,
		},
	} {
		mock := &deleteGuardMock{lookupError: testCase.lookupError}
		_, runError := runDeleteClusterWithYes(t, mock)
		if mock.deletes != 0 {
			t.Fatalf("%s: the DELETE was sent although the cluster's kind was never read", name)
		}
		if runError == nil || exitCodeFor(runError) != testCase.wantExit {
			t.Fatalf("%s: want a refusal with exit %d, got %v (exit %d)", name, testCase.wantExit, runError, exitCodeFor(runError))
		}
		if !strings.Contains(runError.Error(), `could not read what kind of cluster "games" is, so nothing was deleted`) ||
			!strings.Contains(runError.Error(), "try again") {
			t.Fatalf("%s: the refusal must say nothing was deleted and to retry, got %q", name, runError)
		}
	}
}

func TestDeleteClusterOnAMissingClusterExitsThreeWithoutDeleting(t *testing.T) {
	mock := &deleteGuardMock{lookupError: fmt.Errorf("no cluster found for name %q: %w", "games", client.ErrClusterNotFound)}
	_, runError := runDeleteClusterWithYes(t, mock)
	want := `cluster "games" does not exist: the name is wrong or it has already been deleted`
	if mock.deletes != 0 || runError == nil || runError.Error() != want || exitCodeFor(runError) != exitNotFound {
		t.Fatalf("want %q with exit 3 and no DELETE, got %v (exit %d, %d deletes)", want, runError, exitCodeFor(runError), mock.deletes)
	}
}

func TestDeleteClusterRefusesAnUnreportedKind(t *testing.T) {
	mock := &deleteGuardMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "games"}}
	_, runError := runDeleteClusterWithYes(t, mock)
	if mock.deletes != 0 {
		t.Fatal("the DELETE was sent for a cluster whose kind the platform did not report")
	}
	if runError == nil || exitCodeFor(runError) != exitError ||
		!strings.Contains(runError.Error(), `the platform did not report what kind of cluster "games" is, so nothing was deleted`) {
		t.Fatalf("want a refusal with exit 1, got %v (exit %d)", runError, exitCodeFor(runError))
	}
}

func TestDeleteClusterRefusesEveryKindWithItsOwnRemoval(t *testing.T) {
	for kind, wantCommand := range map[string]string{
		"hetzner":        "ankra cluster deprovision " + testClusterID,
		"ovh":            "ankra cluster deprovision " + testClusterID,
		"upcloud":        "ankra cluster deprovision " + testClusterID,
		"digitalocean":   "ankra cluster deprovision " + testClusterID,
		"scaleway":       "ankra cluster deprovision " + testClusterID,
		"ankracloud":     "ankra cluster deprovision " + testClusterID,
		"aws":            "ankra cluster deprovision " + testClusterID,
		"proxmox":        "ankra cluster deprovision " + testClusterID,
		"morpheus":       "ankra cluster deprovision " + testClusterID,
		"doks":           "ankra cluster managed delete " + testClusterID + " --provider doks",
		"uks":            "ankra cluster managed delete " + testClusterID + " --provider uks",
		"gke":            "ankra cluster managed delete " + testClusterID + " --provider gke",
		"ovh_mks":        "ankra cluster managed delete " + testClusterID + " --provider ovh_mks",
		"aks":            "ankra cluster managed delete " + testClusterID + " --provider aks",
		"eks":            "ankra cluster managed delete " + testClusterID + " --provider eks",
		"kapsule":        "ankra cluster managed delete " + testClusterID + " --provider kapsule",
		"ankracloud_k8s": "ankra cluster managed delete " + testClusterID + " --provider ankracloud_k8s",
		"playground":     "ankra cluster playground destroy " + testClusterID,
	} {
		mock := &deleteGuardMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "games", Kind: kind}}
		output, runError := runDeleteClusterWithYes(t, mock)
		if mock.deletes != 0 {
			t.Fatalf("%s: the generic DELETE was sent", kind)
		}
		if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "`"+wantCommand+"`") {
			t.Fatalf("%s: want exit 2 naming `%s`, got %v (exit %d)", kind, wantCommand, runError, exitCodeFor(runError))
		}
		if strings.Contains(output, wantCommand) {
			t.Fatalf("%s: the refusal belongs on stderr, not stdout: %q", kind, output)
		}
	}
}

func TestDeleteClusterDeletesAnImportedCluster(t *testing.T) {
	mock := &deleteGuardMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "games", Kind: "imported"}}
	output, runError := runDeleteClusterWithYes(t, mock)
	if runError != nil || mock.deletes != 1 || !strings.Contains(output, `Cluster "games" deleted successfully.`) {
		t.Fatalf("an imported cluster must be deleted once, got %v (%d deletes), output %q", runError, mock.deletes, output)
	}
}

func TestDeleteClusterDeclinedSendsNothing(t *testing.T) {
	originalForce := forceDelete
	forceDelete = false
	t.Cleanup(func() { forceDelete = originalForce })
	mock := &deleteGuardMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "games", Kind: "imported"}}
	setMockClient(t, mock)
	reader, writer, pipeError := os.Pipe()
	if pipeError != nil {
		t.Fatal(pipeError)
	}
	_, _ = writer.WriteString("n\n")
	_ = writer.Close()
	originalStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = originalStdin; _ = reader.Close() })

	var runError error
	_ = captureStdout(t, func() {
		runError = deleteClusterCmd.RunE(deleteClusterCmd, []string{"games"})
	})
	if mock.deletes != 0 || exitCodeFor(runError) != exitCancelled {
		t.Fatalf("a declined prompt must exit 4 with nothing deleted, got %v (exit %d, %d deletes)", runError, exitCodeFor(runError), mock.deletes)
	}
}

// deprovisionGuardMock answers the kind lookups `ankra cluster deprovision`
// makes and counts every deprovision it is asked for, generic or provider.
type deprovisionGuardMock struct {
	baseMock

	cluster      client.ClusterListItem
	lookupError  error
	deprovisions int
}

func (m *deprovisionGuardMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.lookupError != nil {
		return client.ClusterListItem{}, m.lookupError
	}
	if clusterID == m.cluster.ID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, fmt.Errorf("no cluster found for id %q: %w", clusterID, client.ErrClusterNotFound)
}

func (m *deprovisionGuardMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.lookupError != nil {
		return client.ClusterListItem{}, m.lookupError
	}
	return m.cluster, nil
}

func (m *deprovisionGuardMock) DeprovisionCluster(ctx context.Context, clusterID string) (*client.DeprovisionClusterResult, error) {
	m.deprovisions++
	return &client.DeprovisionClusterResult{}, nil
}

func (m *deprovisionGuardMock) DeprovisionHetznerCluster(clusterID string, options client.DeprovisionOptions) (*client.DeprovisionHetznerClusterResponse, error) {
	m.deprovisions++
	return &client.DeprovisionHetznerClusterResponse{}, nil
}

// TestDeprovisionTheSelectedClusterFailsClosedWhenItsKindCannotBeRead pins
// the second half of ankra-kpq4ae: with no argument, a failed lookup of the
// selected cluster used to return it with no kind, so a cloud cluster went
// down the generic lane with its volume check and record warning skipped.
func TestDeprovisionTheSelectedClusterFailsClosedWhenItsKindCannotBeRead(t *testing.T) {
	for name, testCase := range map[string]struct {
		lookupError error
		wantExit    int
	}{
		"a platform error":     {lookupError: client.NewUnexpectedResponseError(503, "unexpected status: 503 Service Unavailable"), wantExit: exitError},
		"rejected credentials": {lookupError: client.ErrUnauthorized, wantExit: exitAuth},
		"a stale selection": {
			lookupError: fmt.Errorf("no cluster found for name %q: %w", "test-cluster", client.ErrClusterNotFound),
			wantExit:    exitNotFound,
		},
	} {
		mock := &deprovisionGuardMock{lookupError: testCase.lookupError}
		_, runError := runDeprovisionOnSelection(t, mock)
		if mock.deprovisions != 0 {
			t.Fatalf("%s: a deprovision was sent although the selected cluster's kind was never read", name)
		}
		if runError == nil || exitCodeFor(runError) != testCase.wantExit {
			t.Fatalf("%s: want a refusal with exit %d, got %v (exit %d)", name, testCase.wantExit, runError, exitCodeFor(runError))
		}
	}
}

func TestDeprovisionRefusesAKindWithoutALaneHere(t *testing.T) {
	for kind, want := range map[string]struct {
		exit    int
		message string
	}{
		"":           {exitError, "the platform did not report what kind of cluster demo is, so nothing was deprovisioned"},
		"eks":        {exitUsage, "`ankra cluster managed delete " + testClusterID + " --provider eks`"},
		"kapsule":    {exitUsage, "`ankra cluster managed delete " + testClusterID + " --provider kapsule`"},
		"playground": {exitUsage, "`ankra cluster playground destroy " + testClusterID + "`"},
	} {
		mock := &deprovisionGuardMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "demo", Kind: kind}}
		_, runError := runConfirmCommand(t, mock, "", []*cobra.Command{clusterDeprovisionCmd},
			"cluster", "deprovision", testClusterID, "--yes")
		if mock.deprovisions != 0 {
			t.Fatalf("kind %q: a deprovision was sent", kind)
		}
		if runError == nil || exitCodeFor(runError) != want.exit || !strings.Contains(runError.Error(), want.message) {
			t.Fatalf("kind %q: want exit %d containing %q, got %v (exit %d)", kind, want.exit, want.message, runError, exitCodeFor(runError))
		}
	}
}

func runDeprovisionOnSelection(t *testing.T, mock APIClient) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	writeSelectedClusterJSON(t)
	out := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"cluster", "deprovision", "--yes"})
	t.Cleanup(func() { resetTreeFlags(t, clusterDeprovisionCmd) })
	runError := rootCmd.Execute()
	return out.String(), runError
}
