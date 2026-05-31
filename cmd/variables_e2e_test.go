package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

// varsMock captures variable CRUD calls so the e2e tests can assert wire-shape
// and upsert fallback. It embeds baseMock for the rest of the interface and
// the IaC / PATCH surface from upgradeMock for stack variables.
type varsMock struct {
	baseMock

	iac              string
	orgVars          []client.OrganisationVariable
	clusterVars      []client.ClusterVariable
	createOrgCalls   []createVarCall
	updateOrgCalls   []updateVarCall
	deleteOrgCalls   []string
	createOrgErr     error
	updateOrgErr     error
	deleteOrgErr     error
	createClusterCalls   []createClusterVarCall
	updateClusterCalls   []updateClusterVarCall
	deleteClusterCalls   []deleteClusterVarCall
	createClusterErr     error

	patchCalls []capturedPatch
	patchErr   error
}

type createVarCall struct{ Name, Value, Description string }
type updateVarCall struct{ Name, Value, Description string }
type createClusterVarCall struct{ ClusterID, Name, Value, Description string }
type updateClusterVarCall struct{ ClusterID, Name, Value, Description string }
type deleteClusterVarCall struct{ ClusterID, Name string }

func (m *varsMock) GetClusterIaC(ctx context.Context, clusterID string) (string, error) {
	return m.iac, nil
}

func (m *varsMock) PatchClusterStackPartial(ctx context.Context, clusterID, stackName string, body client.PatchStackRequest) (*client.PatchStackResult, error) {
	m.patchCalls = append(m.patchCalls, capturedPatch{ClusterID: clusterID, StackName: stackName, Body: body})
	if m.patchErr != nil {
		return nil, m.patchErr
	}
	return &client.PatchStackResult{StackName: stackName, JobCount: 1}, nil
}

func (m *varsMock) ListOrganisationVariables(ctx context.Context) (*client.OrganisationVariablesListResult, error) {
	return &client.OrganisationVariablesListResult{Variables: m.orgVars}, nil
}

func (m *varsMock) CreateOrganisationVariable(ctx context.Context, name, value, description string) (*client.OrganisationVariableResult, error) {
	m.createOrgCalls = append(m.createOrgCalls, createVarCall{Name: name, Value: value, Description: description})
	if m.createOrgErr != nil {
		return nil, m.createOrgErr
	}
	v := client.OrganisationVariable{Name: name, Value: value, Description: description, UpdatedAt: time.Now()}
	m.orgVars = append(m.orgVars, v)
	return &client.OrganisationVariableResult{Variable: v}, nil
}

func (m *varsMock) UpdateOrganisationVariable(ctx context.Context, name, value, description string) (*client.OrganisationVariableResult, error) {
	m.updateOrgCalls = append(m.updateOrgCalls, updateVarCall{Name: name, Value: value, Description: description})
	if m.updateOrgErr != nil {
		return nil, m.updateOrgErr
	}
	v := client.OrganisationVariable{Name: name, Value: value, Description: description, UpdatedAt: time.Now()}
	return &client.OrganisationVariableResult{Variable: v}, nil
}

func (m *varsMock) DeleteOrganisationVariable(ctx context.Context, name string) error {
	m.deleteOrgCalls = append(m.deleteOrgCalls, name)
	return m.deleteOrgErr
}

func (m *varsMock) ListClusterVariables(ctx context.Context, clusterID string) (*client.ClusterVariablesListResult, error) {
	return &client.ClusterVariablesListResult{Variables: m.clusterVars}, nil
}

func (m *varsMock) CreateClusterVariable(ctx context.Context, clusterID, name, value, description string) (*client.ClusterVariableResult, error) {
	m.createClusterCalls = append(m.createClusterCalls, createClusterVarCall{ClusterID: clusterID, Name: name, Value: value, Description: description})
	if m.createClusterErr != nil {
		return nil, m.createClusterErr
	}
	return &client.ClusterVariableResult{Variable: client.ClusterVariable{Name: name, Value: value, Description: description, UpdatedAt: time.Now()}}, nil
}

func (m *varsMock) UpdateClusterVariable(ctx context.Context, clusterID, name, value, description string) (*client.ClusterVariableResult, error) {
	m.updateClusterCalls = append(m.updateClusterCalls, updateClusterVarCall{ClusterID: clusterID, Name: name, Value: value, Description: description})
	return &client.ClusterVariableResult{Variable: client.ClusterVariable{Name: name, Value: value, Description: description, UpdatedAt: time.Now()}}, nil
}

func (m *varsMock) DeleteClusterVariable(ctx context.Context, clusterID, name string) error {
	m.deleteClusterCalls = append(m.deleteClusterCalls, deleteClusterVarCall{ClusterID: clusterID, Name: name})
	return nil
}

func resetVariablesCmdFlags(t *testing.T) {
	t.Helper()
	resetUpgradeCommandFlags(t)
	for _, c := range []interface{ ResetFlags() }{} {
		_ = c
	}
}

// --- org variables ---

func TestRunOrgVariablesSet_CreateThenList(t *testing.T) {
	mock := &varsMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "variables", "set", "DB_HOST", "db.example.com",
		"--description", "Primary DB",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.createOrgCalls) != 1 {
		t.Fatalf("expected one create call, got %d", len(mock.createOrgCalls))
	}
	got := mock.createOrgCalls[0]
	if got.Name != "DB_HOST" || got.Value != "db.example.com" || got.Description != "Primary DB" {
		t.Errorf("create call = %+v, want {DB_HOST, db.example.com, Primary DB}", got)
	}
	if !strings.Contains(out.String(), `"DB_HOST" created`) {
		t.Errorf("expected created message, got: %s", out.String())
	}
}

func TestRunOrgVariablesSet_UpsertFallsBackToUpdateOn409(t *testing.T) {
	mock := &varsMock{createOrgErr: client.ErrVariableDuplicate}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "variables", "set", "DB_HOST", "db.new.example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.createOrgCalls) != 1 || len(mock.updateOrgCalls) != 1 {
		t.Fatalf("expected one create + one update, got %d create / %d update", len(mock.createOrgCalls), len(mock.updateOrgCalls))
	}
	if mock.updateOrgCalls[0].Value != "db.new.example.com" {
		t.Errorf("update value = %q, want db.new.example.com", mock.updateOrgCalls[0].Value)
	}
	if !strings.Contains(out.String(), `"DB_HOST" updated`) {
		t.Errorf("expected updated message, got: %s", out.String())
	}
}

func TestRunOrgVariablesSet_StdinValue(t *testing.T) {
	mock := &varsMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader("from-stdin-token\n"))
	cmd.SetArgs([]string{"org", "variables", "set", "API_TOKEN", "-"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.createOrgCalls) != 1 || mock.createOrgCalls[0].Value != "from-stdin-token" {
		t.Errorf("expected stdin-read value, got %+v", mock.createOrgCalls)
	}
}

func TestRunOrgVariablesGet_NotFound(t *testing.T) {
	mock := &varsMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "variables", "get", "MISSING"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestRunOrgVariablesDelete_YesSkipsPrompt(t *testing.T) {
	mock := &varsMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "variables", "delete", "DB_HOST", "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.deleteOrgCalls) != 1 || mock.deleteOrgCalls[0] != "DB_HOST" {
		t.Errorf("expected one delete call for DB_HOST, got %+v", mock.deleteOrgCalls)
	}
}

func TestRunOrgVariablesDelete_NotFound(t *testing.T) {
	mock := &varsMock{deleteOrgErr: client.ErrVariableNotFound}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "variables", "delete", "DB_HOST", "--yes"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want not-found", err)
	}
}

// --- cluster variables ---

func TestRunClusterVariablesSet_RoutesToCluster(t *testing.T) {
	mock := &varsMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "variables", "set", "DB_HOST", "db.prod.example.com",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.createClusterCalls) != 1 {
		t.Fatalf("expected one create cluster var call, got %d", len(mock.createClusterCalls))
	}
	got := mock.createClusterCalls[0]
	if got.ClusterID != fakeClusterUUID || got.Name != "DB_HOST" {
		t.Errorf("create cluster call = %+v, want cluster=%s name=DB_HOST", got, fakeClusterUUID)
	}
}

func TestRunClusterVariablesDelete_RoutesToCluster(t *testing.T) {
	mock := &varsMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "variables", "delete", "DB_HOST", "--cluster", fakeClusterUUID, "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.deleteClusterCalls) != 1 || mock.deleteClusterCalls[0].ClusterID != fakeClusterUUID {
		t.Errorf("expected one delete cluster call, got %+v", mock.deleteClusterCalls)
	}
}

// --- stack variables ---

const sampleIaCWithStackVarsForTest = `apiVersion: v1
kind: ImportCluster
metadata:
  name: website-demo
spec:
  stacks:
    - name: demo-web-app
      description: The demo web app stack
      variables:
        EXISTING: old-value
      addons: []
      manifests: []
`

func TestRunStackVariablesSet_PatchesStackWithMergedMap(t *testing.T) {
	mock := &varsMock{iac: sampleIaCWithStackVarsForTest}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "stacks", "variables", "set", "demo-web-app",
		"NEW_KEY", "new-value",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.patchCalls) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.patchCalls))
	}
	got := mock.patchCalls[0].Body.Spec.Stacks[0].Variables
	if got["EXISTING"] != "old-value" {
		t.Errorf("existing variable lost: %v", got)
	}
	if got["NEW_KEY"] != "new-value" {
		t.Errorf("new variable not set: %v", got)
	}
}

func TestRunStackVariablesSet_DryRunNoPatch(t *testing.T) {
	mock := &varsMock{iac: sampleIaCWithStackVarsForTest}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "stacks", "variables", "set", "demo-web-app",
		"NEW_KEY", "new-value",
		"--cluster", fakeClusterUUID,
		"--dry-run",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.patchCalls) != 0 {
		t.Errorf("dry-run must not PATCH, got %d", len(mock.patchCalls))
	}
	if !strings.Contains(out.String(), "Would set stack") {
		t.Errorf("expected dry-run notice, got:\n%s", out.String())
	}
}

func TestRunStackVariablesDelete_NotFound(t *testing.T) {
	mock := &varsMock{iac: sampleIaCWithStackVarsForTest}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "stacks", "variables", "delete", "demo-web-app", "MISSING",
		"--cluster", fakeClusterUUID, "--yes",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want not-found", err)
	}
	if len(mock.patchCalls) != 0 {
		t.Errorf("must not PATCH on not-found, got %d", len(mock.patchCalls))
	}
}

func TestRunStackVariablesDelete_RemovesAndPatches(t *testing.T) {
	mock := &varsMock{iac: sampleIaCWithStackVarsForTest}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "stacks", "variables", "delete", "demo-web-app", "EXISTING",
		"--cluster", fakeClusterUUID, "--yes",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.patchCalls) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.patchCalls))
	}
	got := mock.patchCalls[0].Body.Spec.Stacks[0].Variables
	if _, present := got["EXISTING"]; present {
		t.Errorf("EXISTING should have been removed: %v", got)
	}
}

func TestRunStackVariablesList_RendersTable(t *testing.T) {
	mock := &varsMock{iac: sampleIaCWithStackVarsForTest}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "stacks", "variables", "list", "demo-web-app",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "EXISTING") || !strings.Contains(out.String(), "old-value") {
		t.Errorf("expected table to render EXISTING=old-value, got:\n%s", out.String())
	}
}

func TestRunStackVariablesList_UnknownStackErrors(t *testing.T) {
	mock := &varsMock{iac: sampleIaCWithStackVarsForTest}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "stacks", "variables", "list", "ghost",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown stack")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v", err)
	}
}

// ensure compile-time interface compliance — varsMock must satisfy APIClient
var _ APIClient = (*varsMock)(nil)

// silence unused import (errors) when other helper tests slim down
var _ = errors.New
var _ = fmt.Sprintf
