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
	deployments    string
	exportDocument string
	exportVersion  int
	applied        []client.CreateImportClusterRequest
	failCluster    string
	clusters       []client.ClusterListItem
	// in-place lane
	upgraded        []client.InstantiateStackProfileRequest
	upgradeClusters []string
	legacyServer    bool
}

func (mock *rolloutMock) InstantiateStackProfile(ctx context.Context, clusterID string, request client.InstantiateStackProfileRequest) (*client.InstantiateStackProfileResult, error) {
	mock.upgraded = append(mock.upgraded, request)
	mock.upgradeClusters = append(mock.upgradeClusters, clusterID)
	name := ""
	for _, cluster := range mock.clusters {
		if cluster.ID == clusterID {
			name = cluster.Name
		}
	}
	if name == mock.failCluster {
		return nil, errors.New("cluster is offline")
	}
	if mock.legacyServer {
		return &client.InstantiateStackProfileResult{DraftID: "draft-9", StackName: request.NewStackName + "-copy", ProfileVersion: 2}, nil
	}
	operationID := "op-" + clusterID[:8]
	return &client.InstantiateStackProfileResult{StackName: request.NewStackName, ProfileVersion: 2, Deployed: true, OperationID: &operationID, JobCount: 4, ManifestsCount: 4}, nil
}

func (mock *rolloutMock) GetStackProfile(profileID string) (*client.StackProfileDetail, error) {
	return &client.StackProfileDetail{Profile: client.StackProfileSummary{ID: profileID, Name: "hello-fleet", CurrentVersion: 2}}, nil
}

func (mock *rolloutMock) ListStackProfileInstantiations(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return json.RawMessage(mock.deployments), nil
}

func (mock *rolloutMock) ExportStackProfileIac(profileID string, version int) (*client.StackProfileIacExport, error) {
	mock.exportVersion = version
	document := mock.exportDocument
	if document == "" {
		document = rolloutExportDocument
	}
	return &client.StackProfileIacExport{ProfileID: profileID, Version: version,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte(document))}, nil
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

func TestStackProfilesRolloutViaApplyAllOutdated(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--outdated", "--via-apply")
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

func TestStackProfilesRolloutViaApplyRenamesStackToTheDeployedName(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.deployments = `{"current_version": 2, "result": [
	  {"id": "i-1", "target_cluster_id": "11111111-1111-1111-1111-111111111111", "cluster_name": "prod-eu", "stack_name": "web-blue", "stack_state": "up", "version": 1, "outdated": true}
	]}`
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--via-apply"); executeError != nil {
		t.Fatalf("rollout failed: %v", executeError)
	}
	if len(mock.applied) != 1 || mock.applied[0].Spec.Stacks[0].Name != "web-blue" {
		t.Fatalf("the exported stack should take the deployed stack's name: %+v", mock.applied)
	}
}

func TestStackProfilesRolloutViaApplyByClusterRollsEveryVersion(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet",
		"--cluster", "prod-eu", "--cluster", "22222222-2222-2222-2222-222222222222", "--version", "v2", "--via-apply")
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

func TestStackProfilesRolloutViaApplyKeepsGoingPastAFailure(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.failCluster = "prod-eu"
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--via-apply")
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

func TestStackProfilesRolloutViaApplyStructuredOutputStillFailsOnPartialFailure(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.failCluster = "prod-eu"
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--via-apply", "-o", "json")
	if executeError == nil {
		t.Fatal("-o json must still exit non-zero when a target failed")
	}
	if !strings.Contains(output, `"status": "failed"`) || !strings.Contains(output, `"status": "applied"`) {
		t.Errorf("the JSON payload should still be emitted before the error:\n%s", output)
	}
}

func TestStackProfilesRolloutViaApplyRefusesMultiStackExport(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.exportDocument = rolloutExportDocument + `  - name: second
    manifests: []
    addons: []
`
	_, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--via-apply")
	if executeError == nil || !strings.Contains(executeError.Error(), "exports 2 stacks") {
		t.Fatalf("expected a refusal for a multi-stack export, got %v", executeError)
	}
	if len(mock.applied) != 0 {
		t.Errorf("nothing should be applied: %+v", mock.applied)
	}
}

func TestStackProfilesRolloutSaysWhenNothingIsDeployed(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.deployments = `{"current_version": 2, "result": []}`
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all")
	if executeError != nil {
		t.Fatalf("rollout failed: %v", executeError)
	}
	if !strings.Contains(output, "no stack has been deployed from 'hello-fleet' yet") {
		t.Errorf("output = %q", output)
	}
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	output, executeError = runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--outdated")
	if executeError != nil {
		t.Fatalf("rollout failed: %v", executeError)
	}
	if !strings.Contains(output, "no stack has been deployed") {
		t.Errorf("output = %q", output)
	}
}

func TestStackProfilesRolloutViaApplyRefusesAFileReferenceInTheExport(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.exportDocument = `apiVersion: v1
kind: ImportCluster
metadata:
  name: hello-fleet
spec:
  stacks:
  - name: hello-fleet
    manifests:
    - name: hello-namespace
      from_file: namespace.yaml
    addons: []
`
	_, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--via-apply")
	if executeError == nil || !strings.Contains(executeError.Error(), "invalid ImportCluster in the exported profile version") {
		t.Fatalf("a from_file in an export must fail loudly, got %v", executeError)
	}
	if len(mock.applied) != 0 {
		t.Errorf("nothing should be applied: %+v", mock.applied)
	}
}

func TestUnifiedDiffShowsATrailingNewlineOnlyChange(t *testing.T) {
	got := unifiedDiff("from", "to", "a\nb", "a\nb\n")
	want := "--- from\n+++ to\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n"
	if got != want {
		t.Errorf("unifiedDiff =\n%s\nwant\n%s", got, want)
	}
	if got := unifiedDiff("from", "to", "a\nb", "a\nb"); got != "" {
		t.Errorf("identical inputs without a trailing newline should produce no diff, got %q", got)
	}
}

func TestStackProfilesDeploymentsWithoutCurrentVersion(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesDeploymentsCmd)
	mock := &stackProfileManageMock{payload: json.RawMessage(`{"result": [
	  {"id": "i-1", "target_cluster_id": "11111111-1111-1111-1111-111111111111", "cluster_name": "prod-eu", "stack_name": "hello-fleet", "stack_state": "up", "version": 1, "outdated": true}
	]}`)}
	output, executeError := runStackProfilesCommand(t, mock, "", "deployments", "profile-1")
	if executeError != nil {
		t.Fatalf("deployments failed: %v", executeError)
	}
	if strings.Contains(output, "v0") {
		t.Errorf("a missing current_version must not be shown as v0:\n%s", output)
	}
	for _, want := range []string{"Current version unknown", "1 behind", "update available"} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}

func TestStackProfilesRolloutInPlaceAllOutdated(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--outdated")
	if executeError != nil {
		t.Fatalf("rollout failed: %v\n%s", executeError, output)
	}
	if mock.exportVersion != 0 || len(mock.applied) != 0 {
		t.Errorf("the in-place lane must not export or cluster-apply: export=%d applied=%d", mock.exportVersion, len(mock.applied))
	}
	if len(mock.upgraded) != 1 {
		t.Fatalf("upgraded %d stacks, want only the outdated one: %+v", len(mock.upgraded), mock.upgraded)
	}
	request := mock.upgraded[0]
	if !request.UpgradeExisting || !request.Deploy || request.NewStackName != "hello-fleet" || request.Version == nil || *request.Version != 2 {
		t.Errorf("request = %+v", request)
	}
	if mock.upgradeClusters[0] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("cluster = %q", mock.upgradeClusters[0])
	}
	for _, want := range []string{"prod-eu / hello-fleet: v1 -> v2 applied, 4 jobs scheduled", "now reports the new version"} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}

func TestStackProfilesRolloutInPlaceCarriesRecordedBindingsAndOverrides(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.deployments = `{"current_version": 2, "result": [
	  {"id": "i-1", "target_cluster_id": "11111111-1111-1111-1111-111111111111", "cluster_name": "prod-eu", "stack_name": "web-blue", "stack_state": "up", "version": 1, "outdated": true,
	   "parameters": [{"name": "host", "value": "web.prod-eu.example"}, {"name": "replicas", "value": "2"}]}
	]}`
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--set", "replicas=3"); executeError != nil {
		t.Fatalf("rollout failed: %v", executeError)
	}
	if len(mock.upgraded) != 1 || mock.upgraded[0].NewStackName != "web-blue" {
		t.Fatalf("upgraded = %+v", mock.upgraded)
	}
	got := mock.upgraded[0].Parameters
	want := []client.ParameterBinding{{Name: "host", Value: "web.prod-eu.example"}, {Name: "replicas", Value: "3"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("bindings = %+v, want recorded ones with --set layered over: %+v", got, want)
	}
}

func TestStackProfilesRolloutInPlaceDetectsALegacyPlatform(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.legacyServer = true
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all")
	if executeError == nil {
		t.Fatal("a platform that answers with a renamed draft must be reported as a failure")
	}
	if !strings.Contains(output, "predates in-place upgrades") || !strings.Contains(output, "hello-fleet-copy") {
		t.Errorf("output = %s", output)
	}
}

func TestStackProfilesRolloutInPlaceKeepsGoingPastAFailure(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.failCluster = "prod-eu"
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "-o", "json")
	if executeError == nil {
		t.Fatal("expected the failed cluster to be reported")
	}
	if len(mock.upgraded) != 2 {
		t.Fatalf("the second cluster should still be rolled after the first failed: %+v", mock.upgraded)
	}
	if !strings.Contains(output, `"status": "failed"`) || !strings.Contains(output, `"status": "applied"`) || !strings.Contains(output, `"operation_id"`) {
		t.Errorf("output = %s", output)
	}
}

func TestStackProfilesRolloutViaApplyRefusesBindings(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	_, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--via-apply", "--set", "a=b")
	if executeError == nil || !strings.Contains(executeError.Error(), "cannot be combined with --via-apply") {
		t.Fatalf("expected a usage error, got %v", executeError)
	}
}
