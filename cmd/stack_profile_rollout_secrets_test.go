package cmd

import (
	"encoding/base64"
	"strings"
	"testing"

	"ankra/internal/client"
)

// secretExportMock serves an export the way the platform renders a version
// with a secret input: the placeholder where the secret goes and the input in
// the parameter list.
type secretExportMock struct {
	*rolloutMock
	exportParameters []client.StackProfileParameter
}

func (mock *secretExportMock) ExportStackProfileIac(profileID string, version int) (*client.StackProfileIacExport, error) {
	export, exportError := mock.rolloutMock.ExportStackProfileIac(profileID, version)
	if exportError != nil {
		return nil, exportError
	}
	export.Parameters = mock.exportParameters
	return export, nil
}

func secretRolloutExportDocument() string {
	secretManifest := base64.StdEncoding.EncodeToString([]byte(
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: redis-auth\nstringData:\n  password: ANKRA_SECRET_PARAMETER\n"))
	return `apiVersion: v1
kind: ImportCluster
metadata:
  name: hello-fleet
spec:
  stacks:
  - name: hello-fleet
    manifests:
    - name: redis-auth
      manifest_base64: ` + secretManifest + `
      encrypted_paths:
      - stringData.password
      parents: []
    addons: []
`
}

// The fleet-wide incident shape (ankra-c42hih): every target's bound redis
// password was applied over with the placeholder text. Nothing may be applied.
func TestStackProfilesRolloutViaApplyRefusesAVersionWithSecretInputs(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := &secretExportMock{rolloutMock: newRolloutMock(), exportParameters: []client.StackProfileParameter{
		{Name: "redis_password", Type: "secret"},
		{Name: "replicas", Type: "number"},
	}}
	mock.exportDocument = secretRolloutExportDocument()
	_, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--via-apply")
	if executeError == nil {
		t.Fatal("a --via-apply rollout of a version with a secret input must be refused")
	}
	for _, expected := range []string{"redis_password", "ANKRA_SECRET_PARAMETER", "drop --via-apply"} {
		if !strings.Contains(executeError.Error(), expected) {
			t.Errorf("refusal %q does not mention %q", executeError.Error(), expected)
		}
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("refusal exit code = %d, want the usage code %d", exitCodeFor(executeError), exitUsage)
	}
	if len(mock.applied) != 0 {
		t.Fatalf("the placeholder export was applied to %d cluster(s)", len(mock.applied))
	}
}

// A version whose secret declaration was lost still carries the placeholder:
// the content is checked, not only the parameter list.
func TestStackProfilesRolloutViaApplyRefusesAnExportCarryingThePlaceholder(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := &secretExportMock{rolloutMock: newRolloutMock()}
	mock.exportDocument = secretRolloutExportDocument()
	_, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--via-apply")
	if executeError == nil || !strings.Contains(executeError.Error(), "carries the placeholder ANKRA_SECRET_PARAMETER") {
		t.Fatalf("expected the placeholder refusal, got %v", executeError)
	}
	if len(mock.applied) != 0 {
		t.Fatalf("the placeholder export was applied to %d cluster(s)", len(mock.applied))
	}
}

// A version without secrets keeps rolling out through the apply lane.
func TestStackProfilesRolloutViaApplyStillAppliesAVersionWithoutSecrets(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := &secretExportMock{rolloutMock: newRolloutMock(), exportParameters: []client.StackProfileParameter{
		{Name: "replicas", Type: "number"},
	}}
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--via-apply"); executeError != nil {
		t.Fatalf("rollout failed: %v", executeError)
	}
	if len(mock.applied) != 1 {
		t.Fatalf("applied %d, want 1", len(mock.applied))
	}
}

// The export carries the version's stored values, so applying it would reset
// the inputs a deployment was applied with. A target whose deployment recorded
// inputs is refused; nothing is applied anywhere.
func TestStackProfilesRolloutViaApplyRefusesTargetsWithRecordedInputs(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := &secretExportMock{rolloutMock: newRolloutMock()}
	mock.deployments = `{"current_version": 2, "result": [
  {"id": "i-1", "target_cluster_id": "11111111-1111-1111-1111-111111111111", "cluster_name": "prod-eu", "stack_name": "hello-fleet", "stack_state": "up", "version": 1, "outdated": true, "created_at": "2026-09-10T19:49:37Z",
   "parameters": [{"name": "hostname", "value": "shop.example.com"}]}
]}`
	_, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--via-apply")
	if executeError == nil {
		t.Fatal("a --via-apply rollout over a deployment with recorded inputs must be refused")
	}
	for _, expected := range []string{"prod-eu/hello-fleet (hostname)", "drop --via-apply"} {
		if !strings.Contains(executeError.Error(), expected) {
			t.Errorf("refusal %q does not mention %q", executeError.Error(), expected)
		}
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("refusal exit code = %d, want the usage code %d", exitCodeFor(executeError), exitUsage)
	}
	if len(mock.applied) != 0 {
		t.Fatalf("the export was applied to %d cluster(s)", len(mock.applied))
	}
}
