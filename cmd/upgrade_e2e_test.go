package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

// resetUpgradeCommandFlags clears the upgrade commands' flag state between
// tests. Cobra's pflag arrays accumulate across Execute() calls because the
// rootCmd is a process-global; without this reset, `--set` values from one
// test would leak into the next.
func resetUpgradeCommandFlags(t *testing.T) {
	t.Helper()
	reset := func(cmd interface{ Flags() *pflag.FlagSet }) {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
				return
			}
			_ = f.Value.Set(f.DefValue)
		})
	}
	reset(clusterAddonsUpgradeCmd)
	reset(clusterManifestsUpgradeCmd)
}

const sampleIaCYAMLForCmd = `apiVersion: v1
kind: ImportCluster
metadata:
  name: website-demo
spec:
  stacks:
    - name: demo-web-app
      description: The demo web app stack
      addons:
        - name: website
          chart_name: website
          chart_version: 1.0.145
          namespace: web
      manifests:
        - name: demo-namespace
          namespace: web
`

type upgradeMock struct {
	baseMock
	iac                string
	addonValues        string
	manifestB64        string
	capturedRequests   []capturedPatch
	patchResult        *client.PatchStackResult
	patchErr           error
	getIaCErr          error
	getAddonValuesErr  error
	getManifestCfgErr  error
}

type capturedPatch struct {
	ClusterID string
	StackName string
	Body      client.PatchStackRequest
}

func (m *upgradeMock) GetClusterIaC(ctx context.Context, clusterID string) (string, error) {
	if m.getIaCErr != nil {
		return "", m.getIaCErr
	}
	return m.iac, nil
}

func (m *upgradeMock) GetClusterAddonValues(ctx context.Context, clusterID, addonName string) (string, error) {
	if m.getAddonValuesErr != nil {
		return "", m.getAddonValuesErr
	}
	return m.addonValues, nil
}

func (m *upgradeMock) GetClusterManifestConfiguration(ctx context.Context, clusterID, manifestName string) (string, error) {
	if m.getManifestCfgErr != nil {
		return "", m.getManifestCfgErr
	}
	return m.manifestB64, nil
}

func (m *upgradeMock) PatchClusterStackPartial(ctx context.Context, clusterID, stackName string, body client.PatchStackRequest) (*client.PatchStackResult, error) {
	m.capturedRequests = append(m.capturedRequests, capturedPatch{
		ClusterID: clusterID, StackName: stackName, Body: body,
	})
	if m.patchErr != nil {
		return nil, m.patchErr
	}
	if m.patchResult != nil {
		return m.patchResult, nil
	}
	return &client.PatchStackResult{StackName: stackName, JobCount: 1}, nil
}

const fakeClusterUUID = "00000000-0000-0000-0000-000000000001"

func TestRunAddonsUpgrade_ChartVersionOnly(t *testing.T) {
	mock := &upgradeMock{
		iac:         sampleIaCYAMLForCmd,
		patchResult: &client.PatchStackResult{StackName: "demo-web-app", JobCount: 1},
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--chart-version", "1.0.146",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected exactly one PATCH, got %d", len(mock.capturedRequests))
	}
	req := mock.capturedRequests[0]
	if !req.Body.PartialStack {
		t.Error("expected partial_stack=true")
	}
	if req.StackName != "demo-web-app" {
		t.Errorf("stack name = %q, want demo-web-app", req.StackName)
	}
	if len(req.Body.Spec.Stacks) != 1 || len(req.Body.Spec.Stacks[0].Addons) != 1 {
		t.Fatalf("expected 1 stack with 1 addon, got %+v", req.Body.Spec.Stacks)
	}
	addon := req.Body.Spec.Stacks[0].Addons[0]
	if addon.ChartVersion != "1.0.146" {
		t.Errorf("addon.chart_version = %q, want 1.0.146", addon.ChartVersion)
	}
	if addon.Configuration != nil {
		t.Errorf("configuration must be omitted when no values flags, got %v", addon.Configuration)
	}
	// Stack metadata must survive
	if req.Body.Spec.Stacks[0].Description != "The demo web app stack" {
		t.Errorf("stack description not preserved")
	}
}

func TestRunAddonsUpgrade_ValuesAndSetConflict(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--values-from-file", "/tmp/does-not-matter.yaml",
		"--set", "image.tag=1.0.0",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected conflict error, got success; output: %s", out.String())
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' error, got %v", err)
	}
	if len(mock.capturedRequests) != 0 {
		t.Errorf("expected 0 PATCH calls on conflict, got %d", len(mock.capturedRequests))
	}
}

func TestRunAddonsUpgrade_NoMutation(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing mutation flags")
	}
	if !strings.Contains(err.Error(), "at least one mutating flag") {
		t.Errorf("expected 'at least one mutating flag' error, got %v", err)
	}
}

func TestRunAddonsUpgrade_AmbiguousStack(t *testing.T) {
	iac := `apiVersion: v1
kind: ImportCluster
metadata:
  name: x
spec:
  stacks:
    - name: a
      addons:
        - {name: shared, chart_name: c, chart_version: 1}
      manifests: []
    - name: b
      addons:
        - {name: shared, chart_name: c, chart_version: 1}
      manifests: []
`
	mock := &upgradeMock{iac: iac}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "shared",
		"--chart-version", "2",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected ambiguous-stack error")
	}
	if !strings.Contains(err.Error(), "multiple stacks") {
		t.Errorf("expected ambiguous-stack error, got %v", err)
	}
}

func TestRunAddonsUpgrade_EmptyClusterIaC(t *testing.T) {
	mock := &upgradeMock{getIaCErr: client.ErrClusterEmpty}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--chart-version", "1.0.146",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected empty-cluster error")
	}
	if !strings.Contains(err.Error(), "no resources") {
		t.Errorf("expected 'no resources' error, got %v", err)
	}
}

func TestRunAddonsUpgrade_SetMutationFetchesValues(t *testing.T) {
	mock := &upgradeMock{
		iac:         sampleIaCYAMLForCmd,
		addonValues: "image:\n  tag: 1.0.0\n  repository: foo/bar\n",
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--set", "image.tag=1.0.146",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
	addon := mock.capturedRequests[0].Body.Spec.Stacks[0].Addons[0]
	if addon.Configuration == nil {
		t.Fatal("expected configuration in patch")
	}
	decoded, err := base64.StdEncoding.DecodeString(addon.Configuration.ValuesBase64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !strings.Contains(string(decoded), "tag: 1.0.146") {
		t.Errorf("expected mutated tag, got:\n%s", string(decoded))
	}
	if !strings.Contains(string(decoded), "repository: foo/bar") {
		t.Errorf("expected sibling preserved, got:\n%s", string(decoded))
	}
}

func TestRunAddonsUpgrade_DryRunNoApiPatch(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--chart-version", "1.0.146",
		"--cluster", fakeClusterUUID,
		"--dry-run",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.capturedRequests) != 0 {
		t.Errorf("expected 0 PATCH calls in --dry-run, got %d", len(mock.capturedRequests))
	}
	if !strings.Contains(out.String(), "Before:") || !strings.Contains(out.String(), "After:") {
		t.Errorf("expected dry-run output, got:\n%s", out.String())
	}
}

func TestRunAddonsUpgrade_DryRunJSONEnvelope(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--chart-version", "1.0.146",
		"--cluster", fakeClusterUUID,
		"--dry-run",
		"-o", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	body := out.String()
	if !strings.Contains(body, `"before"`) || !strings.Contains(body, `"after"`) {
		t.Errorf("expected {before, after} envelope, got:\n%s", body)
	}
}

func TestRunAddonsUpgrade_BackendErrorMapping(t *testing.T) {
	mock := &upgradeMock{
		iac:      sampleIaCYAMLForCmd,
		patchErr: &client.PatchStackError{StatusCode: 422, Body: []byte(`{"detail":"push failed"}`)},
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--chart-version", "1.0.146",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from backend mapping")
	}
	if !strings.Contains(err.Error(), "git push failed") {
		t.Errorf("expected 'git push failed' message, got %v", err)
	}
}

func TestRunManifestsUpgrade_FetchesExistingWhenNamespaceOnly(t *testing.T) {
	mock := &upgradeMock{
		iac:         sampleIaCYAMLForCmd,
		manifestB64: base64.StdEncoding.EncodeToString([]byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: web\n")),
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "upgrade", "demo-namespace",
		"--namespace", "web2",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
	mPatch := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if mPatch.ManifestBase64 == "" {
		t.Errorf("expected manifest_base64 to be re-sent from fetched content, got empty")
	}
	if mPatch.Namespace != "web2" {
		t.Errorf("manifest namespace = %q, want web2", mPatch.Namespace)
	}
}

func TestRunManifestsUpgrade_NoMutation(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "upgrade", "demo-namespace",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected mutation-required error")
	}
	if !strings.Contains(err.Error(), "at least one mutating flag") {
		t.Errorf("expected mutation error, got %v", err)
	}
}

// Sanity check: ensure that an error from PatchClusterStackPartial that isn't
// a *PatchStackError still surfaces cleanly.
func TestRunAddonsUpgrade_NonTypedErrorPasses(t *testing.T) {
	mock := &upgradeMock{
		iac:      sampleIaCYAMLForCmd,
		patchErr: errors.New("network is unreachable"),
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--chart-version", "1.0.146",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if !strings.Contains(err.Error(), "network is unreachable") {
		t.Errorf("expected network error to surface, got %v", err)
	}
}
