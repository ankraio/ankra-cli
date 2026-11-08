package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

// The commands that give the CLI the operations MCP already had
// (ankra-hq41xb.4): alert definitions and triggers, etcd membership, the
// rolling OS patch lane, typed storage writes and the billing standing and
// wallet. Each runs through rootCmd against a recording mock.

type cliParityMock struct {
	baseMock

	alertRules       []client.AlertRule
	listOptions      *client.ListAlertRulesOptions
	createdBodies    []map[string]any
	deletedAlerts    []string
	enabledCalls     map[string]bool
	snoozeMinutes    int
	alertRulesError  error
	etcdProviders    []string
	removedMembers   []string
	etcdError        error
	policyUpdates    []client.OSPatchPolicyUpdate
	nodePolicyCalls  []client.NodeOSPolicyRequest
	appliedManifests []map[string]any
	deletedResources []client.DeleteResourceRequest
	volumePhase      string
	standing         *client.BillingStanding
	walletError      error
}

func (m *cliParityMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: testClusterID, Name: name}, nil
}

func (m *cliParityMock) ListAlertRules(options client.ListAlertRulesOptions) (*client.AlertRuleList, error) {
	m.listOptions = &options
	if m.alertRulesError != nil {
		return nil, m.alertRulesError
	}
	return &client.AlertRuleList{Items: m.alertRules,
		Pagination: client.Pagination{Page: 1, TotalPages: 1, TotalCount: len(m.alertRules)}}, nil
}

func (m *cliParityMock) CreateAlertRule(definition map[string]any) (*client.AlertRule, error) {
	m.createdBodies = append(m.createdBodies, definition)
	name, _ := definition["name"].(string)
	return &client.AlertRule{ID: "alert-1", Name: name}, nil
}

func (m *cliParityMock) DeleteAlertRule(alertID string) (*client.AlertRuleDeleteResult, error) {
	m.deletedAlerts = append(m.deletedAlerts, alertID)
	return &client.AlertRuleDeleteResult{Success: true}, nil
}

func (m *cliParityMock) SetAlertRuleEnabled(alertID string, enabled bool) (*client.AlertRule, error) {
	if m.enabledCalls == nil {
		m.enabledCalls = map[string]bool{}
	}
	m.enabledCalls[alertID] = enabled
	return &client.AlertRule{ID: alertID, Name: "disk", Enabled: enabled}, nil
}

func (m *cliParityMock) SnoozeAlertTrigger(alertID string, triggerID string, durationMinutes int, notes string) (map[string]any, error) {
	m.snoozeMinutes = durationMinutes
	return map[string]any{"id": triggerID}, nil
}

func (m *cliParityMock) GetEtcdMembers(ctx context.Context, provider string, clusterID string, nodeID string) (*client.NodeMaintenanceResult, error) {
	m.etcdProviders = append(m.etcdProviders, provider)
	if m.etcdError != nil {
		return nil, m.etcdError
	}
	return &client.NodeMaintenanceResult{OperationID: "operation-1", Status: "success", Completed: true,
		Report: map[string]any{"members": []any{}}}, nil
}

func (m *cliParityMock) RemoveEtcdMember(ctx context.Context, provider string, clusterID string, nodeID string, memberID string) (*client.NodeMaintenanceResult, error) {
	m.etcdProviders = append(m.etcdProviders, provider)
	m.removedMembers = append(m.removedMembers, memberID)
	return &client.NodeMaintenanceResult{OperationID: "operation-2", Status: "success", Completed: true}, nil
}

func (m *cliParityMock) SetOSPatchPolicy(ctx context.Context, provider string, clusterID string, update client.OSPatchPolicyUpdate) (map[string]any, error) {
	m.policyUpdates = append(m.policyUpdates, update)
	return map[string]any{"enabled": true}, nil
}

func (m *cliParityMock) ApplyNodeOSPolicy(ctx context.Context, provider string, clusterID string, request client.NodeOSPolicyRequest) (*client.NodeMaintenanceResult, error) {
	m.nodePolicyCalls = append(m.nodePolicyCalls, request)
	return &client.NodeMaintenanceResult{OperationID: "operation-3", Status: "running"}, nil
}

func (m *cliParityMock) ApplyResource(clusterID string, request client.ApplyResourceRequest) (*client.ResourceMutationResponse, error) {
	m.appliedManifests = append(m.appliedManifests, request.Manifest)
	return &client.ResourceMutationResponse{Status: "success"}, nil
}

func (m *cliParityMock) DeleteResource(clusterID string, request client.DeleteResourceRequest) (*client.ResourceMutationResponse, error) {
	m.deletedResources = append(m.deletedResources, request)
	return &client.ResourceMutationResponse{Status: "success"}, nil
}

func (m *cliParityMock) GetResources(clusterID string, request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	response := &client.GetResourcesResponse{}
	for _, item := range request.ResourceRequests {
		name := item.Name
		response.ResourceResponses = append(response.ResourceResponses, client.ResourceResponseItem{
			Status: "success", Kind: item.Kind, Name: &name,
			Items: []interface{}{map[string]interface{}{
				"status": map[string]interface{}{"phase": m.volumePhase},
				"spec":   map[string]interface{}{"claimRef": map[string]interface{}{"namespace": "prod", "name": "data-db-0"}},
			}},
		})
	}
	return response, nil
}

func (m *cliParityMock) GetBillingStanding() (*client.BillingStanding, error) {
	return m.standing, nil
}

func (m *cliParityMock) GetWallet() (*client.Wallet, error) {
	return nil, m.walletError
}

// runParityCommand runs the command at args through rootCmd with input on
// stdin, its flags (and the cluster flags) reset before and after.
func runParityCommand(t *testing.T, mock *cliParityMock, input string, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	if found, _, findError := rootCmd.Find(args); findError == nil {
		resetTreeFlags(t, found)
		t.Cleanup(func() { resetTreeFlags(t, found) })
	}
	return runWithInput(t, mock, input, args...)
}

func TestAlertsRulesListSendsTheFiltersAndRendersEachDefinition(t *testing.T) {
	mock := &cliParityMock{alertRules: []client.AlertRule{{ID: "alert-1", Name: "node-disk", Severity: "critical", Enabled: true}}}
	output, runError := runParityCommand(t, mock, "", "alerts", "rules", "list", "--enabled", "--severity", "critical")
	if runError != nil {
		t.Fatalf("list failed: %v\n%s", runError, output)
	}
	if mock.listOptions == nil || mock.listOptions.Enabled == nil || !*mock.listOptions.Enabled || mock.listOptions.Severity != "critical" {
		t.Fatalf("list options = %+v, want enabled and severity critical", mock.listOptions)
	}
	if !strings.Contains(output, "node-disk") || !strings.Contains(output, "alert-1") {
		t.Errorf("the definition is not listed:\n%s", output)
	}
}

func TestAlertsRulesListWithoutThePermissionExitsSeven(t *testing.T) {
	mock := &cliParityMock{alertRulesError: &client.PermissionDeniedError{Permission: "alerts.read"}}
	_, runError := runParityCommand(t, mock, "", "alerts", "rules", "list")
	if exitCodeFor(runError) != exitForbidden {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(runError), runError, exitForbidden)
	}
}

func TestAlertsRulesCreateSendsTheFileAsTheBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alert.yaml")
	document := "name: node-disk\nenabled: true\nrules:\n  - name: disk\n    resource_type: node\n    conditions: []\n"
	if writeError := os.WriteFile(path, []byte(document), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	mock := &cliParityMock{}
	output, runError := runParityCommand(t, mock, "", "alerts", "rules", "create", "-f", path, "-o", "json")
	if runError != nil {
		t.Fatalf("create failed: %v\n%s", runError, output)
	}
	if len(mock.createdBodies) != 1 || mock.createdBodies[0]["name"] != "node-disk" || mock.createdBodies[0]["rules"] == nil {
		t.Fatalf("create body = %+v", mock.createdBodies)
	}
	var created map[string]any
	if decodeError := json.Unmarshal([]byte(output), &created); decodeError != nil || created["id"] != "alert-1" {
		t.Errorf("-o json is not the created definition: %v\n%s", decodeError, output)
	}
}

func TestAlertsRulesCreateRefusesADefinitionWithoutAName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alert.json")
	if writeError := os.WriteFile(path, []byte(`{"enabled": true}`), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	mock := &cliParityMock{}
	_, runError := runParityCommand(t, mock, "", "alerts", "rules", "create", "-f", path)
	if exitCodeFor(runError) != exitUsage || len(mock.createdBodies) != 0 {
		t.Fatalf("exit %d (%v) with %d creates, want a usage error and nothing sent", exitCodeFor(runError), runError, len(mock.createdBodies))
	}
}

func TestAlertsRulesDeleteDeclinedSendsNothing(t *testing.T) {
	mock := &cliParityMock{}
	_, runError := runParityCommand(t, mock, "n\n", "alerts", "rules", "delete", "alert-1")
	if !errors.Is(runError, errCancelled) || len(mock.deletedAlerts) != 0 {
		t.Fatalf("decline: error %v, deletes %v; want errCancelled and none", runError, mock.deletedAlerts)
	}
	_, runError = runParityCommand(t, mock, "", "alerts", "rules", "delete", "alert-1", "--yes")
	if runError != nil || len(mock.deletedAlerts) != 1 {
		t.Fatalf("--yes: error %v, deletes %v", runError, mock.deletedAlerts)
	}
}

func TestAlertsRulesDisableAndEnableSetTheFlag(t *testing.T) {
	mock := &cliParityMock{}
	if _, runError := runParityCommand(t, mock, "", "alerts", "rules", "disable", "alert-1"); runError != nil {
		t.Fatal(runError)
	}
	if enabled, called := mock.enabledCalls["alert-1"]; !called || enabled {
		t.Fatalf("disable sent %v (called %v), want enabled=false", enabled, called)
	}
	if _, runError := runParityCommand(t, mock, "", "alerts", "rules", "enable", "alert-1"); runError != nil {
		t.Fatal(runError)
	}
	if !mock.enabledCalls["alert-1"] {
		t.Fatal("enable did not send enabled=true")
	}
}

func TestAlertsTriggersSnoozeRefusesANonPositiveDuration(t *testing.T) {
	mock := &cliParityMock{}
	_, runError := runParityCommand(t, mock, "", "alerts", "triggers", "snooze", "alert-1", "trigger-1", "--minutes", "0")
	if exitCodeFor(runError) != exitUsage || mock.snoozeMinutes != 0 {
		t.Fatalf("exit %d (%v), snoozed %d; want a usage error and no call", exitCodeFor(runError), runError, mock.snoozeMinutes)
	}
	if _, runError = runParityCommand(t, mock, "", "alerts", "triggers", "snooze", "alert-1", "trigger-1", "--minutes", "90"); runError != nil {
		t.Fatal(runError)
	}
	if mock.snoozeMinutes != 90 {
		t.Fatalf("snoozed %d minutes, want 90", mock.snoozeMinutes)
	}
}

func TestEtcdRemoveConfirmsAndCallsTheProvidersRoute(t *testing.T) {
	mock := &cliParityMock{}
	_, runError := runParityCommand(t, mock, "n\n", "cluster", "hetzner", "etcd", "remove", testClusterID, "node-1", "8e9e05c52164694d")
	if !errors.Is(runError, errCancelled) || len(mock.removedMembers) != 0 {
		t.Fatalf("decline: error %v, removals %v; want errCancelled and none", runError, mock.removedMembers)
	}
	output, runError := runParityCommand(t, mock, "", "cluster", "hetzner", "etcd", "remove", testClusterID, "node-1", "8e9e05c52164694d", "--yes")
	if runError != nil {
		t.Fatalf("remove failed: %v\n%s", runError, output)
	}
	if len(mock.removedMembers) != 1 || mock.removedMembers[0] != "8e9e05c52164694d" || mock.etcdProviders[0] != "hetzner" {
		t.Fatalf("removals %v on %v, want the member on hetzner", mock.removedMembers, mock.etcdProviders)
	}
}

func TestEtcdMembersWithoutCredentialsExitsSix(t *testing.T) {
	mock := &cliParityMock{etcdError: client.ErrUnauthorized}
	_, runError := runParityCommand(t, mock, "", "cluster", "ovh", "etcd", "members", testClusterID, "node-1")
	if exitCodeFor(runError) != exitAuth {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(runError), runError, exitAuth)
	}
}

func TestNodeMaintenanceCommandsFollowTheProvidersLanes(t *testing.T) {
	for _, testCase := range []struct {
		path      []string
		mustExist bool
	}{
		{[]string{"cluster", "hetzner", "etcd", "members"}, true},
		{[]string{"cluster", "scaleway", "os-patches", "run"}, true},
		{[]string{"cluster", "proxmox", "os-patches", "node-policy"}, true},
		{[]string{"cluster", "proxmox", "etcd"}, false},
		{[]string{"cluster", "proxmox", "os-patches", "run"}, false},
		{[]string{"cluster", "scaleway", "os-patches", "node-policy"}, false},
	} {
		found, remaining, findError := rootCmd.Find(testCase.path)
		exists := findError == nil && len(remaining) == 0 && found.Name() == testCase.path[len(testCase.path)-1]
		if exists != testCase.mustExist {
			t.Errorf("%s exists = %v, want %v", strings.Join(testCase.path, " "), exists, testCase.mustExist)
		}
	}
}

func TestOSPatchPolicySetSendsOnlyTheNamedSettings(t *testing.T) {
	mock := &cliParityMock{}
	_, runError := runParityCommand(t, mock, "", "cluster", "hetzner", "os-patches", "policy", "set", testClusterID,
		"--enable", "--window-days", "sat,sun", "--window-start-hour", "2")
	if runError != nil {
		t.Fatal(runError)
	}
	if len(mock.policyUpdates) != 1 {
		t.Fatalf("policy updates = %d, want 1", len(mock.policyUpdates))
	}
	update := mock.policyUpdates[0]
	if update.Enabled == nil || !*update.Enabled || update.WindowStartHourUTC == nil || *update.WindowStartHourUTC != 2 ||
		strings.Join(update.WindowDays, ",") != "sat,sun" {
		t.Errorf("update = %+v", update)
	}
	if update.CadenceDays != nil || update.WindowDurationHours != nil || update.IncludeStatefulNodes != nil {
		t.Errorf("a setting that was not named was sent: %+v", update)
	}

	_, runError = runParityCommand(t, mock, "", "cluster", "hetzner", "os-patches", "policy", "set", testClusterID)
	if exitCodeFor(runError) != exitUsage || len(mock.policyUpdates) != 1 {
		t.Fatalf("no flags: exit %d (%v), want a usage error and nothing sent", exitCodeFor(runError), runError)
	}
}

func TestNodeOSPolicyNeedsATargetAndConfirms(t *testing.T) {
	mock := &cliParityMock{}
	_, runError := runParityCommand(t, mock, "", "cluster", "hetzner", "os-patches", "node-policy", testClusterID, "--yes")
	if exitCodeFor(runError) != exitUsage || len(mock.nodePolicyCalls) != 0 {
		t.Fatalf("no target: exit %d (%v), want a usage error and nothing sent", exitCodeFor(runError), runError)
	}
	output, runError := runParityCommand(t, mock, "", "cluster", "hetzner", "os-patches", "node-policy", testClusterID,
		"--all-nodes", "--revert", "--yes")
	if runError != nil {
		t.Fatalf("node-policy failed: %v\n%s", runError, output)
	}
	if len(mock.nodePolicyCalls) != 1 || !mock.nodePolicyCalls[0].AllNodes || !mock.nodePolicyCalls[0].Revert {
		t.Fatalf("node policy calls = %+v", mock.nodePolicyCalls)
	}
	if !strings.Contains(output, "Still running") {
		t.Errorf("an unfinished run should say how to follow it:\n%s", output)
	}
}

func TestStorageClassCreateAppliesTheManifestFromFlags(t *testing.T) {
	mock := &cliParityMock{}
	output, runError := runParityCommand(t, mock, "", "cluster", "storage-classes", "create", "fast",
		"--provisioner", "csi.hetzner.cloud", "--reclaim-policy", "Retain", "--parameter", "fsType=ext4", "--default",
		"--cluster", "prod")
	if runError != nil {
		t.Fatalf("create failed: %v\n%s", runError, output)
	}
	if len(mock.appliedManifests) != 1 {
		t.Fatalf("applied %d manifests, want 1", len(mock.appliedManifests))
	}
	manifest := mock.appliedManifests[0]
	parameters, _ := manifest["parameters"].(map[string]any)
	metadata, _ := manifest["metadata"].(map[string]any)
	if manifest["kind"] != "StorageClass" || manifest["provisioner"] != "csi.hetzner.cloud" ||
		manifest["reclaimPolicy"] != "Retain" || parameters["fsType"] != "ext4" || metadata["annotations"] == nil {
		t.Errorf("manifest = %+v", manifest)
	}

	_, runError = runParityCommand(t, mock, "", "cluster", "storage-classes", "create", "slow",
		"--provisioner", "csi.hetzner.cloud", "--reclaim-policy", "Keep", "--cluster", "prod")
	if exitCodeFor(runError) != exitUsage || len(mock.appliedManifests) != 1 {
		t.Fatalf("bad reclaim policy: exit %d (%v), want a usage error and nothing applied", exitCodeFor(runError), runError)
	}
}

func TestPersistentVolumeDeleteRefusesABoundVolumeUnlessForced(t *testing.T) {
	mock := &cliParityMock{volumePhase: "Bound"}
	_, runError := runParityCommand(t, mock, "", "cluster", "persistent-volumes", "delete", "pvc-1", "--yes", "--cluster", "prod")
	if exitCodeFor(runError) != exitBlocked || len(mock.deletedResources) != 0 {
		t.Fatalf("bound: exit %d (%v) with %d deletes, want exit %d and none", exitCodeFor(runError), runError, len(mock.deletedResources), exitBlocked)
	}
	if !strings.Contains(runError.Error(), "prod/data-db-0") {
		t.Errorf("the refusal should name the claim: %v", runError)
	}
	_, runError = runParityCommand(t, mock, "", "cluster", "persistent-volumes", "delete", "pvc-1", "--yes", "--force", "--cluster", "prod")
	if runError != nil || len(mock.deletedResources) != 1 || mock.deletedResources[0].Kind != "PersistentVolume" {
		t.Fatalf("--force: error %v, deletes %+v", runError, mock.deletedResources)
	}
}

func TestPersistentVolumeDeleteDeletesAReleasedVolume(t *testing.T) {
	mock := &cliParityMock{volumePhase: "Released"}
	_, runError := runParityCommand(t, mock, "", "cluster", "persistent-volumes", "delete", "pvc-1", "--yes", "--cluster", "prod")
	if runError != nil || len(mock.deletedResources) != 1 {
		t.Fatalf("released: error %v, deletes %+v", runError, mock.deletedResources)
	}
}

func TestOrgBillingStandingRendersTheStateAndJSON(t *testing.T) {
	mock := &cliParityMock{standing: &client.BillingStanding{State: "restricted", Restricted: true,
		GatedActions: []string{"create_cluster"}}}
	output, runError := runParityCommand(t, mock, "", "org", "billing", "standing")
	if runError != nil {
		t.Fatal(runError)
	}
	if !strings.Contains(output, "restricted") || !strings.Contains(output, "create_cluster") {
		t.Errorf("standing output:\n%s", output)
	}
	output, runError = runParityCommand(t, mock, "", "org", "billing", "standing", "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil || decoded["state"] != "restricted" {
		t.Errorf("-o json: %v\n%s", decodeError, output)
	}
}

func TestOrgBillingWalletWithoutThePermissionExitsSeven(t *testing.T) {
	mock := &cliParityMock{walletError: &client.PermissionDeniedError{Permission: "billing.read"}}
	_, runError := runParityCommand(t, mock, "", "org", "billing", "wallet")
	if exitCodeFor(runError) != exitForbidden {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(runError), runError, exitForbidden)
	}
}
