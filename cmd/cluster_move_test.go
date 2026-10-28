package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type moveMock struct {
	baseMock
	cluster       client.ClusterListItem
	organisations []client.OrganisationSummary
	moveCalls     int
	movedTo       string
	moveError     error
}

func (m *moveMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.cluster.ID == clusterID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *moveMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.cluster.Name == name || m.cluster.ID == name {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *moveMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	return m.organisations, nil
}

func (m *moveMock) MoveCluster(ctx context.Context, clusterID string, destinationOrganisationID string) (*client.MoveClusterResult, error) {
	m.moveCalls++
	m.movedTo = destinationOrganisationID
	if m.moveError != nil {
		return nil, m.moveError
	}
	return &client.MoveClusterResult{
		ClusterID: clusterID, ClusterName: m.cluster.Name,
		DestinationOrganisationID: destinationOrganisationID, DestinationOrganisationName: "Acme Prod",
		Detached: client.MoveClusterDetached{KubeTokens: 2}, SecretsRelocated: 3, Warnings: []string{},
	}, nil
}

func newMoveMock() *moveMock {
	acmeName, acmeSlug := "Acme Prod", "acme-prod"
	currentName := "Current"
	return &moveMock{
		cluster: client.ClusterListItem{ID: "c-1", Name: "edge-01"},
		organisations: []client.OrganisationSummary{
			{OrganisationID: "org-current", Name: &currentName, UserCurrent: true},
			{OrganisationID: "org-acme", Name: &acmeName, Slug: &acmeSlug},
		},
	}
}

func TestClusterMove_RequiresOrganisationFlag(t *testing.T) {
	mock := newMoveMock()
	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{clusterMoveCmd}, "cluster", "move", "edge-01")
	if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "--organisation is required") {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if mock.moveCalls != 0 {
		t.Fatalf("expected no move call, got %d", mock.moveCalls)
	}
}

func TestClusterMove_DeclineDoesNotMove(t *testing.T) {
	mock := newMoveMock()
	output, err := runConfirmCommand(t, mock, "n\n", []*cobra.Command{clusterMoveCmd},
		"cluster", "move", "edge-01", "--organisation", "acme-prod")
	if !errors.Is(err, errCancelled) || exitCodeFor(err) != exitCancelled {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if !strings.Contains(output, `Move cluster "edge-01" to organisation "Acme Prod"?`) {
		t.Fatalf("prompt must name both sides, got %q", output)
	}
	if mock.moveCalls != 0 {
		t.Fatalf("expected no move call on decline, got %d", mock.moveCalls)
	}
}

func TestClusterMove_YesFlagProceedsAndResolvesTheOrganisationByName(t *testing.T) {
	mock := newMoveMock()
	output, err := runConfirmCommand(t, mock, "", []*cobra.Command{clusterMoveCmd},
		"cluster", "move", "edge-01", "--organisation", "acme prod", "--yes")
	if err != nil {
		t.Fatalf("expected success with --yes, got %v", err)
	}
	if mock.moveCalls != 1 || mock.movedTo != "org-acme" {
		t.Fatalf("expected one move to org-acme, got %d to %q", mock.moveCalls, mock.movedTo)
	}
	for _, want := range []string{`Cluster "edge-01" moved to organisation "Acme Prod".`, "2 kube token(s)", "Secrets relocated: 3", "ankra org switch org-acme"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestClusterMove_PipedYesProceedsWithJSONOutput(t *testing.T) {
	mock := newMoveMock()
	output, err := runConfirmCommand(t, mock, "y\n", []*cobra.Command{clusterMoveCmd},
		"cluster", "move", "edge-01", "--organisation", "org-acme", "-o", "json")
	if err != nil {
		t.Fatalf("expected success with piped y, got %v", err)
	}
	if mock.moveCalls != 1 || !strings.Contains(output, `"destination_organisation_id": "org-acme"`) {
		t.Fatalf("expected a JSON move result, got calls=%d output=%q", mock.moveCalls, output)
	}
}

func TestClusterMove_UnknownOrganisationExitsNotFound(t *testing.T) {
	mock := newMoveMock()
	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{clusterMoveCmd},
		"cluster", "move", "edge-01", "--organisation", "nowhere", "--yes")
	if err == nil || exitCodeFor(err) != exitNotFound {
		t.Fatalf("expected exit %d for an unknown organisation, got %v", exitNotFound, err)
	}
	if mock.moveCalls != 0 {
		t.Fatalf("expected no move call, got %d", mock.moveCalls)
	}
}

func TestClusterMove_RefusalAndDenialSurface(t *testing.T) {
	mock := newMoveMock()
	mock.moveError = &client.MoveClusterRefusedError{Code: "name_conflict", Detail: "The destination organisation already has a cluster named 'edge-01'."}
	_, err := runConfirmCommand(t, mock, "", []*cobra.Command{clusterMoveCmd},
		"cluster", "move", "edge-01", "--organisation", "acme-prod", "--yes")
	if err == nil || !strings.Contains(err.Error(), "move refused (name_conflict)") {
		t.Fatalf("expected the refusal to surface, got %v", err)
	}

	denied := newMoveMock()
	denied.moveError = &client.PermissionDeniedError{Permission: "organisation.manage"}
	_, err = runConfirmCommand(t, denied, "", []*cobra.Command{clusterMoveCmd},
		"cluster", "move", "edge-01", "--organisation", "acme-prod", "--yes")
	if err == nil || exitCodeFor(err) != exitForbidden {
		t.Fatalf("expected exit %d for a permission denial, got %v", exitForbidden, err)
	}
}
