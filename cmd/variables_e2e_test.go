package cmd

import (
	"bytes"
	"context"
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

	iac                string
	orgVars            []client.OrganisationVariable
	clusterVars        []client.ClusterVariable
	createOrgCalls     []createVarCall
	updateOrgCalls     []updateVarCall
	deleteOrgCalls     []string
	createOrgErr       error
	updateOrgErr       error
	deleteOrgErr       error
	revealOrgCalls     []string
	revealOrgValues    map[string]string
	revealOrgErr       error
	createClusterCalls []createClusterVarCall
	updateClusterCalls []updateClusterVarCall
	deleteClusterCalls []deleteClusterVarCall
	createClusterErr   error

	patchCalls []capturedPatch
	patchErr   error
}

type createVarCall struct {
	Name, Value, Description string
	IsSecret                 bool
}
type updateVarCall struct {
	Name, Description string
	Value             *string
	IsSecret          *bool
}
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

func (m *varsMock) CreateOrganisationVariable(ctx context.Context, name, value, description string, isSecret bool) (*client.OrganisationVariableResult, error) {
	m.createOrgCalls = append(m.createOrgCalls, createVarCall{Name: name, Value: value, Description: description, IsSecret: isSecret})
	if m.createOrgErr != nil {
		return nil, m.createOrgErr
	}
	v := client.OrganisationVariable{Name: name, Value: value, Description: description, IsSecret: isSecret, UpdatedAt: time.Now()}
	m.orgVars = append(m.orgVars, v)
	return &client.OrganisationVariableResult{Variable: v}, nil
}

func (m *varsMock) UpdateOrganisationVariable(ctx context.Context, name string, value *string, description string, isSecret *bool) (*client.OrganisationVariableResult, error) {
	m.updateOrgCalls = append(m.updateOrgCalls, updateVarCall{Name: name, Value: value, Description: description, IsSecret: isSecret})
	if m.updateOrgErr != nil {
		return nil, m.updateOrgErr
	}
	v := client.OrganisationVariable{Name: name, Description: description, UpdatedAt: time.Now()}
	if value != nil {
		v.Value = *value
	}
	return &client.OrganisationVariableResult{Variable: v}, nil
}

func (m *varsMock) RevealOrganisationVariable(ctx context.Context, name string) (*client.OrganisationVariableValue, error) {
	m.revealOrgCalls = append(m.revealOrgCalls, name)
	if m.revealOrgErr != nil {
		return nil, m.revealOrgErr
	}
	value, found := m.revealOrgValues[name]
	if !found {
		return nil, client.ErrVariableNotFound
	}
	return &client.OrganisationVariableValue{Name: name, Value: value}, nil
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
	if value := mock.updateOrgCalls[0].Value; value == nil || *value != "db.new.example.com" {
		t.Errorf("update value = %v, want db.new.example.com", value)
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

func runOrgVariablesCommand(t *testing.T, mock *varsMock, arguments ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(append([]string{"org", "variables"}, arguments...))
	executeError := cmd.Execute()
	return out.String(), executeError
}

func TestRunOrgVariablesSet_SecretCreatesSecretVariable(t *testing.T) {
	mock := &varsMock{}
	output, executeError := runOrgVariablesCommand(t, mock, "set", "API_TOKEN", "s3cret", "--secret")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.createOrgCalls) != 1 || !mock.createOrgCalls[0].IsSecret {
		t.Fatalf("expected one secret create call, got %+v", mock.createOrgCalls)
	}
	if !strings.Contains(output, `Secret organisation variable "API_TOKEN" created`) {
		t.Errorf("expected secret created message, got: %s", output)
	}
	if strings.Contains(output, "s3cret") {
		t.Errorf("output leaked the secret value: %s", output)
	}
}

func TestRunOrgVariablesSet_WithoutSecretCreatesPlainVariable(t *testing.T) {
	mock := &varsMock{}
	output, executeError := runOrgVariablesCommand(t, mock, "set", "DB_HOST", "db")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.createOrgCalls) != 1 || mock.createOrgCalls[0].IsSecret {
		t.Fatalf("expected one plain create call, got %+v", mock.createOrgCalls)
	}
}

func TestRunOrgVariablesSet_UpdateSecretFlag(t *testing.T) {
	cases := []struct {
		name         string
		arguments    []string
		wantSet      bool
		wantIsSecret bool
	}{
		{name: "no flag keeps the stored setting", arguments: []string{"set", "API_TOKEN", "v2"}},
		{name: "--secret marks it secret", arguments: []string{"set", "API_TOKEN", "v2", "--secret"}, wantSet: true, wantIsSecret: true},
		{name: "--secret=false makes it plain", arguments: []string{"set", "API_TOKEN", "v2", "--secret=false"}, wantSet: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &varsMock{createOrgErr: client.ErrVariableDuplicate}
			output, executeError := runOrgVariablesCommand(t, mock, testCase.arguments...)
			if executeError != nil {
				t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
			}
			if len(mock.updateOrgCalls) != 1 {
				t.Fatalf("expected one update call, got %d", len(mock.updateOrgCalls))
			}
			secretUpdate := mock.updateOrgCalls[0].IsSecret
			if (secretUpdate != nil) != testCase.wantSet {
				t.Fatalf("is_secret sent = %v, want %v", secretUpdate != nil, testCase.wantSet)
			}
			if secretUpdate != nil && *secretUpdate != testCase.wantIsSecret {
				t.Errorf("is_secret = %v, want %v", *secretUpdate, testCase.wantIsSecret)
			}
		})
	}
}

func TestRunOrgVariablesSet_UpdateMessageSaysItIsNowSecret(t *testing.T) {
	mock := &varsMock{createOrgErr: client.ErrVariableDuplicate}
	output, executeError := runOrgVariablesCommand(t, mock, "set", "API_TOKEN", "v2", "--secret")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, `"API_TOKEN" updated; it is now secret`) {
		t.Errorf("expected the update to say it is now secret, got: %s", output)
	}
}

func TestRunOrgVariablesSet_SecretWithoutValueKeepsTheValue(t *testing.T) {
	mock := &varsMock{}
	output, executeError := runOrgVariablesCommand(t, mock, "set", "API_TOKEN", "--secret")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.createOrgCalls) != 0 {
		t.Errorf("expected no create call, got %+v", mock.createOrgCalls)
	}
	if len(mock.updateOrgCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(mock.updateOrgCalls))
	}
	call := mock.updateOrgCalls[0]
	if call.Value != nil || call.IsSecret == nil || !*call.IsSecret {
		t.Errorf("update call = %+v, want no value and is_secret true", call)
	}
	if !strings.Contains(output, "it is now secret") {
		t.Errorf("expected the update message, got: %s", output)
	}
}

func TestRunOrgVariablesSet_NameAloneIsAUsageError(t *testing.T) {
	mock := &varsMock{}
	_, executeError := runOrgVariablesCommand(t, mock, "set", "API_TOKEN")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected a usage error, got %v", executeError)
	}
	if len(mock.updateOrgCalls) != 0 || len(mock.createOrgCalls) != 0 {
		t.Errorf("expected no write, got %+v / %+v", mock.createOrgCalls, mock.updateOrgCalls)
	}
}

func TestRunOrgVariablesSet_SecretWithoutValueOnAMissingVariableExitsThree(t *testing.T) {
	mock := &varsMock{updateOrgErr: client.ErrVariableNotFound}
	_, executeError := runOrgVariablesCommand(t, mock, "set", "MISSING", "--secret")
	if executeError == nil || exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("expected a not-found exit, got %v", executeError)
	}
}

func TestRunOrgVariablesList_HidesSecretValue(t *testing.T) {
	mock := &varsMock{orgVars: []client.OrganisationVariable{
		{Name: "API_TOKEN", IsSecret: true, UpdatedAt: time.Now()},
		{Name: "DB_HOST", Value: "db.example.com", UpdatedAt: time.Now()},
	}}
	output, executeError := runOrgVariablesCommand(t, mock, "list")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	for _, want := range []string{"SECRET", "(hidden)", "db.example.com"} {
		if !strings.Contains(output, want) {
			t.Errorf("list output missing %q:\n%s", want, output)
		}
	}
}

func TestRunOrgVariablesList_JSONCarriesIsSecret(t *testing.T) {
	mock := &varsMock{orgVars: []client.OrganisationVariable{{Name: "API_TOKEN", IsSecret: true}}}
	output, executeError := runOrgVariablesCommand(t, mock, "list", "-o", "json")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, `"is_secret": true`) {
		t.Errorf("json output missing is_secret: %s", output)
	}
}

func TestRunOrgVariablesGet_SecretShowsRevealHint(t *testing.T) {
	mock := &varsMock{orgVars: []client.OrganisationVariable{{Name: "API_TOKEN", IsSecret: true}}}
	output, executeError := runOrgVariablesCommand(t, mock, "get", "API_TOKEN")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	for _, want := range []string{"Secret:      yes", `(hidden) (run "ankra org variables reveal API_TOKEN")`} {
		if !strings.Contains(output, want) {
			t.Errorf("get output missing %q:\n%s", want, output)
		}
	}
}

func TestRunOrgVariablesReveal_PrintsValue(t *testing.T) {
	mock := &varsMock{revealOrgValues: map[string]string{"API_TOKEN": "s3cret"}}
	output, executeError := runOrgVariablesCommand(t, mock, "reveal", "API_TOKEN")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if output != "s3cret\n" {
		t.Errorf("output = %q, want the bare value", output)
	}
	if len(mock.revealOrgCalls) != 1 || mock.revealOrgCalls[0] != "API_TOKEN" {
		t.Errorf("reveal calls = %+v", mock.revealOrgCalls)
	}
}

func TestRunOrgVariablesReveal_JSON(t *testing.T) {
	mock := &varsMock{revealOrgValues: map[string]string{"API_TOKEN": "s3cret"}}
	output, executeError := runOrgVariablesCommand(t, mock, "reveal", "API_TOKEN", "-o", "json")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, `"name": "API_TOKEN"`) || !strings.Contains(output, `"value": "s3cret"`) {
		t.Errorf("json output = %s", output)
	}
}

func TestRunOrgVariablesReveal_NotFoundExitsThree(t *testing.T) {
	mock := &varsMock{}
	output, executeError := runOrgVariablesCommand(t, mock, "reveal", "MISSING")
	if executeError == nil {
		t.Fatalf("expected not-found error, output: %s", output)
	}
	if exitCodeFor(executeError) != exitNotFound {
		t.Errorf("exit code = %d, want %d (%v)", exitCodeFor(executeError), exitNotFound, executeError)
	}
}

func TestRunOrgVariablesReveal_APIErrorIsReturned(t *testing.T) {
	mock := &varsMock{revealOrgErr: client.ErrUnauthorized}
	_, executeError := runOrgVariablesCommand(t, mock, "reveal", "API_TOKEN")
	if executeError == nil || exitCodeFor(executeError) != exitAuth {
		t.Errorf("error = %v, want an auth failure", executeError)
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
