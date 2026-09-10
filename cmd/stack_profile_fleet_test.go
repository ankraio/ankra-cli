package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

const rolloutExportDocument = `apiVersion: v1
kind: ImportCluster
metadata:
  name: hello-fleet
spec:
  stacks:
  - name: hello-fleet
    manifests:
    - name: hello-namespace
      manifest_base64: YXBpVmVyc2lvbjogdjEKa2luZDogTmFtZXNwYWNlCm1ldGFkYXRhOgogIG5hbWU6IGhlbGxvCg==
      parents: []
    addons: []
`

type rolloutMock struct {
	baseMock
	deployments   string
	exportVersion int
	applied       []client.CreateImportClusterRequest
	failCluster   string
	clusters      []client.ClusterListItem
}

func (mock *rolloutMock) GetStackProfile(profileID string) (*client.StackProfileDetail, error) {
	return &client.StackProfileDetail{Profile: client.StackProfileSummary{ID: profileID, Name: "hello-fleet", CurrentVersion: 2}}, nil
}

func (mock *rolloutMock) ListStackProfileInstantiations(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return json.RawMessage(mock.deployments), nil
}

func (mock *rolloutMock) ExportStackProfileIac(profileID string, version int) (*client.StackProfileIacExport, error) {
	mock.exportVersion = version
	return &client.StackProfileIacExport{ProfileID: profileID, Version: version,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte(rolloutExportDocument))}, nil
}

func (mock *rolloutMock) ApplyCluster(ctx context.Context, request client.CreateImportClusterRequest, wait bool) (*client.ImportResponse, bool, error) {
	mock.applied = append(mock.applied, request)
	if request.Name == mock.failCluster {
		return nil, false, errors.New("cluster is offline")
	}
	return &client.ImportResponse{Name: request.Name, ClusterId: "cluster-id"}, false, nil
}

func (mock *rolloutMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{Result: mock.clusters, Pagination: client.Pagination{TotalPages: 1}}, nil
}

func newRolloutMock() *rolloutMock {
	return &rolloutMock{
		deployments: fleetDeploymentsPayload,
		clusters: []client.ClusterListItem{
			{ID: "11111111-1111-1111-1111-111111111111", Name: "prod-eu"},
			{ID: "22222222-2222-2222-2222-222222222222", Name: "prod-us"},
			{ID: "33333333-3333-3333-3333-333333333333", Name: "staging"},
		},
	}
}

func TestStackProfilesRolloutAllOutdated(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--outdated")
	if executeError != nil {
		t.Fatalf("rollout failed: %v\n%s", executeError, output)
	}
	if mock.exportVersion != 2 {
		t.Errorf("exported version = %d, want the current version 2", mock.exportVersion)
	}
	if len(mock.applied) != 1 {
		t.Fatalf("applied %d clusters, want only the outdated one: %+v", len(mock.applied), mock.applied)
	}
	request := mock.applied[0]
	if request.Name != "prod-eu" {
		t.Errorf("metadata.name = %q, want the target cluster prod-eu", request.Name)
	}
	if len(request.Spec.Stacks) != 1 || request.Spec.Stacks[0].Name != "hello-fleet" {
		t.Errorf("stack = %+v", request.Spec.Stacks)
	}
	for _, want := range []string{"Rolling out 'hello-fleet' v2 to 1 stack", "prod-eu / hello-fleet: v1 -> v2 applied", "operations list"} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}

func TestStackProfilesRolloutRenamesStackToTheDeployedName(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.deployments = `{"current_version": 2, "result": [
	  {"id": "i-1", "target_cluster_id": "11111111-1111-1111-1111-111111111111", "cluster_name": "prod-eu", "stack_name": "web-blue", "stack_state": "up", "version": 1, "outdated": true}
	]}`
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu"); executeError != nil {
		t.Fatalf("rollout failed: %v", executeError)
	}
	if len(mock.applied) != 1 || mock.applied[0].Spec.Stacks[0].Name != "web-blue" {
		t.Fatalf("the exported stack should take the deployed stack's name: %+v", mock.applied)
	}
}

func TestStackProfilesRolloutByClusterRollsEveryVersion(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet",
		"--cluster", "prod-eu", "--cluster", "22222222-2222-2222-2222-222222222222", "--version", "v2")
	if executeError != nil {
		t.Fatalf("rollout failed: %v\n%s", executeError, output)
	}
	if len(mock.applied) != 2 || mock.applied[0].Name != "prod-eu" || mock.applied[1].Name != "prod-us" {
		t.Fatalf("applied = %+v", mock.applied)
	}
}

func TestStackProfilesRolloutRefusesClusterWithoutDeployment(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	_, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "staging")
	if executeError == nil || !strings.Contains(executeError.Error(), "no stack deployed from this profile") {
		t.Fatalf("expected a refusal for a cluster without a deployment, got %v", executeError)
	}
	if len(mock.applied) != 0 {
		t.Errorf("nothing should be applied: %+v", mock.applied)
	}
}

func TestStackProfilesRolloutDryRunWritesNothing(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--dry-run")
	if executeError != nil {
		t.Fatalf("dry run failed: %v", executeError)
	}
	if len(mock.applied) != 0 || mock.exportVersion != 0 {
		t.Errorf("dry run must not export or apply: applied=%d export=%d", len(mock.applied), mock.exportVersion)
	}
	for _, want := range []string{"Dry run", "prod-eu", "prod-us", "planned"} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}

func TestStackProfilesRolloutKeepsGoingPastAFailure(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.failCluster = "prod-eu"
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all")
	if executeError == nil {
		t.Fatal("expected the failed cluster to be reported")
	}
	if len(mock.applied) != 2 {
		t.Fatalf("the second cluster should still be rolled after the first failed: %+v", mock.applied)
	}
	if !strings.Contains(output, "failed (cluster is offline)") || !strings.Contains(output, "prod-us / hello-fleet: v2 -> v2 applied") {
		t.Errorf("output = %s", output)
	}
}

func TestStackProfilesRolloutNeedsTargets(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet"); executeError == nil {
		t.Fatal("expected an error without --cluster or --all")
	}
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--cluster", "prod-eu"); executeError == nil {
		t.Fatal("expected an error with both --cluster and --all")
	}
}

func TestUnifiedDiff(t *testing.T) {
	from := "a\nb\nc\nd\ne\nf\ng\nh\n"
	to := "a\nb\nc\nD\ne\nf\ng\nh\n"
	want := "--- from\n+++ to\n@@ -1,7 +1,7 @@\n a\n b\n c\n-d\n+D\n e\n f\n g\n"
	if got := unifiedDiff("from", "to", from, to); got != want {
		t.Errorf("unifiedDiff =\n%s\nwant\n%s", got, want)
	}
	if got := unifiedDiff("from", "to", from, from); got != "" {
		t.Errorf("identical inputs should produce no diff, got %q", got)
	}
	if got := unifiedDiff("/dev/null", "to", "", "x\n"); got != "--- /dev/null\n+++ to\n@@ -0,0 +1 @@\n+x\n" {
		t.Errorf("all-added diff = %q", got)
	}
}
