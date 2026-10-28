package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type adoptMock struct {
	baseMock
	clusters   []client.ClusterListItem
	requests   []client.AdoptStackProfileRequest
	clusterIDs []string
	result     *client.AdoptStackProfileResult
	failWith   error
}

func (mock *adoptMock) AdoptStackProfile(ctx context.Context, clusterID string, adoptRequest client.AdoptStackProfileRequest) (*client.AdoptStackProfileResult, error) {
	mock.requests = append(mock.requests, adoptRequest)
	mock.clusterIDs = append(mock.clusterIDs, clusterID)
	if mock.failWith != nil {
		return nil, mock.failWith
	}
	return mock.result, nil
}

func (mock *adoptMock) GetStackProfile(profileID string) (*client.StackProfileDetail, error) {
	return &client.StackProfileDetail{Profile: client.StackProfileSummary{
		ID: profileID, Name: "so-cilium", CurrentVersion: 2}}, nil
}

func (mock *adoptMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{Result: mock.clusters, Pagination: client.Pagination{TotalPages: 1}}, nil
}

func newAdoptMock() *adoptMock {
	return &adoptMock{
		clusters: []client.ClusterListItem{
			{ID: "11111111-1111-1111-1111-111111111111", Name: "so-upcloud-production"},
		},
		result: &client.AdoptStackProfileResult{
			StackName:      "so-cilium",
			ProfileVersion: 2,
			CurrentVersion: 2,
			Drift:          &client.AdoptStackProfileDrift{OnlyOnCluster: []string{}, OnlyInVersion: []string{}},
		},
	}
}

func TestStackProfilesAdoptSendsTheStackAndCluster(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesAdoptCmd)
	mock := newAdoptMock()
	output, executeError := runStackProfilesCommand(t, mock, "", "adopt", "so-cilium",
		"--stack", "so-cilium", "--cluster", "so-upcloud-production")
	if executeError != nil {
		t.Fatalf("adopt failed: %v\n%s", executeError, output)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("sent %d adopt requests, want 1", len(mock.requests))
	}
	if mock.requests[0].StackName != "so-cilium" {
		t.Errorf("stack_name = %q, want so-cilium", mock.requests[0].StackName)
	}
	if mock.requests[0].Version != nil {
		t.Errorf("version = %v, want nil so the platform picks the current version", *mock.requests[0].Version)
	}
	if mock.clusterIDs[0] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("cluster id = %q, want the resolved id of so-upcloud-production", mock.clusterIDs[0])
	}
	if !strings.Contains(output, "Nothing was written to the cluster") {
		t.Errorf("the output must say the cluster was untouched:\n%s", output)
	}
}

func TestStackProfilesAdoptPassesAnExplicitVersion(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesAdoptCmd)
	mock := newAdoptMock()
	mock.result.ProfileVersion = 1
	mock.result.Outdated = true
	output, executeError := runStackProfilesCommand(t, mock, "", "adopt", "so-cilium",
		"--stack", "so-cilium", "--cluster", "so-upcloud-production", "--version", "v1")
	if executeError != nil {
		t.Fatalf("adopt failed: %v\n%s", executeError, output)
	}
	if mock.requests[0].Version == nil || *mock.requests[0].Version != 1 {
		t.Fatalf("version = %v, want 1", mock.requests[0].Version)
	}
	if !strings.Contains(output, "outdated") {
		t.Errorf("an adoption behind the current version must say so:\n%s", output)
	}
}

// The prune warning is the whole safety story of adoption: the command
// writes nothing dangerous, but the rollout that follows deletes whatever
// the version omits. A silent adoption of a drifted stack is the failure
// mode this test exists to prevent.
func TestStackProfilesAdoptWarnsAboutMembersARolloutWouldRemove(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesAdoptCmd)
	mock := newAdoptMock()
	mock.result.Drift = &client.AdoptStackProfileDrift{
		OnlyOnCluster: []string{"addon/hubble", "manifest/cve-patch"},
		OnlyInVersion: []string{"manifest/baseline-policy"},
	}
	output, executeError := runStackProfilesCommand(t, mock, "", "adopt", "so-cilium",
		"--stack", "so-cilium", "--cluster", "so-upcloud-production")
	if executeError != nil {
		t.Fatalf("adopt failed: %v\n%s", executeError, output)
	}
	for _, member := range []string{"addon/hubble", "manifest/cve-patch", "manifest/baseline-policy"} {
		if !strings.Contains(output, member) {
			t.Errorf("the drift report must name %s:\n%s", member, output)
		}
	}
	if !strings.Contains(output, "REMOVES these") {
		t.Errorf("the output must warn that a rollout removes the cluster-only members:\n%s", output)
	}
}

func TestStackProfilesAdoptRequiresStackAndCluster(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesAdoptCmd)
	mock := newAdoptMock()
	if _, executeError := runStackProfilesCommand(t, mock, "", "adopt", "so-cilium",
		"--cluster", "so-upcloud-production"); executeError == nil {
		t.Fatal("adopt without --stack must be refused")
	}
	resetStackProfileCommandFlags(t, stackProfilesAdoptCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "", "adopt", "so-cilium",
		"--stack", "so-cilium"); executeError == nil {
		t.Fatal("adopt without --cluster must be refused")
	}
	if len(mock.requests) != 0 {
		t.Fatalf("a refused adopt must not reach the platform, got %d requests", len(mock.requests))
	}
}

func TestStackProfilesAdoptReportsAPlatformRefusal(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesAdoptCmd)
	mock := newAdoptMock()
	mock.failWith = errors.New("409 Conflict: already tracked by another stack profile")
	output, executeError := runStackProfilesCommand(t, mock, "", "adopt", "so-cilium",
		"--stack", "so-cilium", "--cluster", "so-upcloud-production")
	if executeError == nil {
		t.Fatalf("a refused adoption must fail the command:\n%s", output)
	}
	if !strings.Contains(executeError.Error(), "already tracked") {
		t.Errorf("the refusal must carry the platform's reason: %v", executeError)
	}
}
