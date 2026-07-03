package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
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
				f.Changed = false
				return
			}
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
	reset(clusterAddonsUpgradeCmd)
	reset(clusterManifestsUpgradeCmd)
	reset(clusterAddonsValuesCmd)
	reset(clusterManifestsGetCmd)
	reset(clusterManifestsDeleteCmd)
	reset(clusterEncryptManifestCmd)
	reset(clusterEncryptAddonCmd)
	reset(clusterDecryptManifestCmd)
	reset(clusterDecryptAddonCmd)
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
	iac               string
	addonValues       string
	manifestB64       string
	capturedRequests  []capturedPatch
	patchResult       *client.PatchStackResult
	patchErr          error
	getIaCErr         error
	getAddonValuesErr error
	getManifestCfgErr error
	disconnectCalls   []disconnectCall
	disconnectErr     error

	encryptCalls  []encryptCall
	encryptResult string
	encryptErr    error
	decryptResult string
	decryptErr    error
}

type encryptCall struct {
	YamlContent    string
	EncryptedPaths []string
}

type capturedPatch struct {
	ClusterID string
	StackName string
	Body      client.PatchStackRequest
}

type disconnectCall struct {
	ClusterID    string
	StackName    string
	ManifestName string
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

func (m *upgradeMock) EncryptYAML(yamlContent string, encryptedPaths []string) (string, error) {
	m.encryptCalls = append(m.encryptCalls, encryptCall{YamlContent: yamlContent, EncryptedPaths: encryptedPaths})
	if m.encryptErr != nil {
		return "", m.encryptErr
	}
	if m.encryptResult != "" {
		return m.encryptResult, nil
	}
	return "ENCRYPTED_PLACEHOLDER\n", nil
}

func (m *upgradeMock) DecryptYAML(encryptedYaml string) (string, error) {
	if m.decryptErr != nil {
		return "", m.decryptErr
	}
	if m.decryptResult != "" {
		return m.decryptResult, nil
	}
	return "decrypted: true\n", nil
}

func (m *upgradeMock) DisconnectManifest(ctx context.Context, clusterID, stackName, manifestName string) (*client.DisconnectManifestResult, error) {
	m.disconnectCalls = append(m.disconnectCalls, disconnectCall{ClusterID: clusterID, StackName: stackName, ManifestName: manifestName})
	if m.disconnectErr != nil {
		return nil, m.disconnectErr
	}
	return &client.DisconnectManifestResult{DisconnectedAt: "2026-05-30T00:00:00Z"}, nil
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

func TestRunAddonsUpgrade_DryRunNamespaceChangeNotice(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--namespace", "web-next",
		"--cluster", fakeClusterUUID,
		"--dry-run",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.capturedRequests) != 0 {
		t.Errorf("expected 0 PATCH calls in --dry-run, got %d", len(mock.capturedRequests))
	}
	body := out.String()
	if !strings.Contains(body, "Notices:") || !strings.Contains(body, "left orphaned") {
		t.Errorf("expected namespace-change orphan notice in dry-run output, got:\n%s", body)
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

const sampleDeploymentManifestYAML = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: app
          image: nginx:1.0
`

func TestRunManifestsUpgrade_SetMutationFetchesContent(t *testing.T) {
	mock := &upgradeMock{
		iac:         sampleIaCYAMLForCmd,
		manifestB64: base64.StdEncoding.EncodeToString([]byte(sampleDeploymentManifestYAML)),
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "upgrade", "demo-namespace",
		"--set", "spec.template.spec.containers[name=app].image=nginx:1.27",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
	mPatch := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	decoded, err := base64.StdEncoding.DecodeString(mPatch.ManifestBase64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !strings.Contains(string(decoded), "nginx:1.27") {
		t.Errorf("expected mutated image, got:\n%s", string(decoded))
	}
	if !strings.Contains(string(decoded), "replicas: 1") {
		t.Errorf("expected sibling field preserved, got:\n%s", string(decoded))
	}
}

func TestRunManifestsUpgrade_ContentAndSetConflict(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "upgrade", "demo-namespace",
		"--from-file", "/tmp/does-not-matter.yaml",
		"--set", "spec.replicas=3",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected conflict error, got success; output: %s", out.String())
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' error, got %v", err)
	}
}

func TestRunManifestsUpgrade_DryRunSetNoApiPatch(t *testing.T) {
	mock := &upgradeMock{
		iac:         sampleIaCYAMLForCmd,
		manifestB64: base64.StdEncoding.EncodeToString([]byte(sampleDeploymentManifestYAML)),
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "upgrade", "demo-namespace",
		"--set", "spec.replicas=3",
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

const sampleIaCAddonWithParentForCmd = `apiVersion: v1
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
          parents:
            - name: demo-namespace
              kind: manifest
      manifests:
        - name: demo-namespace
          namespace: web
`

func TestRunAddonsUpgrade_AddParent(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--add-parent", "name=demo-namespace,kind=manifest",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
	parents := mock.capturedRequests[0].Body.Spec.Stacks[0].Addons[0].Parents
	if len(parents) != 1 || parents[0].Name != "demo-namespace" || parents[0].Kind != "manifest" {
		t.Errorf("expected parent demo-namespace/manifest, got %+v", parents)
	}
}

func TestRunAddonsUpgrade_RemoveLastParentClears(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCAddonWithParentForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--remove-parent", "name=demo-namespace,kind=manifest",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	parents := mock.capturedRequests[0].Body.Spec.Stacks[0].Addons[0].Parents
	if len(parents) != 0 {
		t.Errorf("expected parents cleared, got %+v", parents)
	}
}

func TestRunManifestsUpgrade_AddParent(t *testing.T) {
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
		"--add-parent", "name=website,kind=addon",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if len(manifest.Parents) != 1 || manifest.Parents[0].Name != "website" || manifest.Parents[0].Kind != "addon" {
		t.Errorf("expected parent website/addon, got %+v", manifest.Parents)
	}
	if manifest.ManifestBase64 == "" {
		t.Error("expected existing manifest content to be preserved on parent-only edit")
	}
}

func TestRunManifestsUpgrade_BadParentKindErrors(t *testing.T) {
	mock := &upgradeMock{
		iac:         sampleIaCYAMLForCmd,
		manifestB64: base64.StdEncoding.EncodeToString([]byte("apiVersion: v1\nkind: Namespace\n")),
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "upgrade", "demo-namespace",
		"--add-parent", "name=svc,kind=service",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for invalid parent kind")
	}
	if len(mock.capturedRequests) != 0 {
		t.Errorf("expected no PATCH on validation error, got %d", len(mock.capturedRequests))
	}
}

func TestRunManifestsGet_DecodesContent(t *testing.T) {
	yamlDoc := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: web\n"
	mock := &upgradeMock{manifestB64: base64.StdEncoding.EncodeToString([]byte(yamlDoc))}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "get", "demo-namespace",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "kind: Namespace") {
		t.Errorf("expected decoded YAML, got:\n%s", out.String())
	}
}

func TestRunAddonsValues_PrintsDecoded(t *testing.T) {
	mock := &upgradeMock{addonValues: "replicaCount: 3\nimage:\n  tag: 1.0.0\n"}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "addons", "values", "website",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "replicaCount: 3") {
		t.Errorf("expected decoded values, got:\n%s", out.String())
	}
}

func TestRunManifestsDelete_DisconnectsResolvedStack(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "delete", "demo-namespace",
		"--cluster", fakeClusterUUID,
		"--yes",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.disconnectCalls) != 1 {
		t.Fatalf("expected one disconnect call, got %d", len(mock.disconnectCalls))
	}
	if mock.disconnectCalls[0].StackName != "demo-web-app" || mock.disconnectCalls[0].ManifestName != "demo-namespace" {
		t.Errorf("unexpected disconnect call: %+v", mock.disconnectCalls[0])
	}
}

func TestRunManifestsDelete_NotFoundErrors(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "delete", "does-not-exist",
		"--cluster", fakeClusterUUID,
		"--yes",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when manifest is not found")
	}
	if len(mock.disconnectCalls) != 0 {
		t.Errorf("must not disconnect when manifest is missing, got %d calls", len(mock.disconnectCalls))
	}
}

const plainSecretManifestYAML = `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: web
type: Opaque
data:
  username: YWRtaW4=
  password: aHVudGVyMg==
`

const encryptedSecretManifestYAML = `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: web
type: Opaque
data:
  username: YWRtaW4=
  password: ENC[AES256_GCM,data:abc,iv:def,tag:ghi,type:str]
sops:
  age:
    - recipient: age1example
  lastmodified: "2026-06-11T00:00:00Z"
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(password)$
`

const sopsMetadataOnlySecretManifestYAML = `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: web
type: Opaque
data:
  username: YWRtaW4=
  password: aHVudGVyMg==
sops:
  age:
    - recipient: age1example
  lastmodified: "2026-06-11T00:00:00Z"
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(data.password)$
`

func TestRunEncryptManifest_ClusterModeDottedKeyEncryptsLeaf(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(plainSecretManifestYAML)),
		encryptResult: encryptedSecretManifestYAML,
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "demo-namespace",
		"--key", "data.password",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected one EncryptYAML call, got %d", len(mock.encryptCalls))
	}
	if len(mock.encryptCalls[0].EncryptedPaths) != 1 || mock.encryptCalls[0].EncryptedPaths[0] != "password" {
		t.Errorf("encrypted paths = %v, want [password]", mock.encryptCalls[0].EncryptedPaths)
	}
	if !strings.Contains(out.String(), `encrypting key "password"`) {
		t.Errorf("expected normalisation note, got:\n%s", out.String())
	}

	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if len(manifest.EncryptedPaths) != 1 || manifest.EncryptedPaths[0] != "password" {
		t.Errorf("manifest.encrypted_paths = %v, want [password]", manifest.EncryptedPaths)
	}
	decoded, err := base64.StdEncoding.DecodeString(manifest.ManifestBase64)
	if err != nil {
		t.Fatalf("decode manifest_base64: %v", err)
	}
	if !strings.Contains(string(decoded), "ENC[AES256_GCM") {
		t.Errorf("expected encrypted content in PATCH, got: %s", decoded)
	}
}

func TestRunEncryptManifest_ClusterModeFailsWhenValueStaysPlaintext(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(plainSecretManifestYAML)),
		encryptResult: sopsMetadataOnlySecretManifestYAML,
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "demo-namespace",
		"--key", "password",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected verification error when value stays plaintext")
	}
	if !strings.Contains(err.Error(), "still plaintext") {
		t.Errorf("expected plaintext verification error, got: %v", err)
	}
	if len(mock.capturedRequests) != 0 {
		t.Errorf("must not PATCH unverified content, got %d", len(mock.capturedRequests))
	}
}

func TestRunEncryptManifest_ClusterModeFailsWhenKeyMissing(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(plainSecretManifestYAML)),
		encryptResult: encryptedSecretManifestYAML,
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "demo-namespace",
		"--key", "does-not-exist",
		"--cluster", fakeClusterUUID,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected verification error when the key is absent from the output")
	}
	if !strings.Contains(err.Error(), "SOPS encrypted nothing") {
		t.Errorf("expected missing-key verification error, got: %v", err)
	}
	if len(mock.capturedRequests) != 0 {
		t.Errorf("must not PATCH unverified content, got %d", len(mock.capturedRequests))
	}
}

func writeEncryptFileModeFixture(t *testing.T, manifestYAML string) (clusterPath, manifestPath string) {
	t.Helper()
	dir := t.TempDir()
	manifestPath = filepath.Join(dir, "manifests", "secret.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("create manifests dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest fixture: %v", err)
	}
	clusterPath = filepath.Join(dir, "cluster.yaml")
	clusterYAML := `apiVersion: v1
kind: ImportCluster
metadata:
  name: file-mode-test
spec:
  stacks:
    - name: web
      manifests:
        - name: my-secret
          from_file: manifests/secret.yaml
`
	if err := os.WriteFile(clusterPath, []byte(clusterYAML), 0o644); err != nil {
		t.Fatalf("write cluster fixture: %v", err)
	}
	return clusterPath, manifestPath
}

func TestRunEncryptManifest_FileModeDottedKeyEncryptsLeaf(t *testing.T) {
	clusterPath, manifestPath := writeEncryptFileModeFixture(t, plainSecretManifestYAML)

	mock := &upgradeMock{encryptResult: encryptedSecretManifestYAML}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "my-secret",
		"--key", "data.password",
		"-f", clusterPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected one EncryptYAML call, got %d", len(mock.encryptCalls))
	}
	if len(mock.encryptCalls[0].EncryptedPaths) != 1 || mock.encryptCalls[0].EncryptedPaths[0] != "password" {
		t.Errorf("encrypted paths = %v, want [password]", mock.encryptCalls[0].EncryptedPaths)
	}

	writtenManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read written manifest: %v", err)
	}
	if string(writtenManifest) != encryptedSecretManifestYAML {
		t.Errorf("manifest file = %q, want the encrypted content", writtenManifest)
	}

	writtenCluster, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatalf("read written cluster file: %v", err)
	}
	if !strings.Contains(string(writtenCluster), "- password") {
		t.Errorf("expected encrypted_paths entry 'password' in cluster file, got:\n%s", writtenCluster)
	}
	if strings.Contains(string(writtenCluster), "data.password") {
		t.Errorf("cluster file must store the leaf key, not the dotted path:\n%s", writtenCluster)
	}
}

func TestRunEncryptManifest_FileModeRefusesSilentPlaintext(t *testing.T) {
	clusterPath, manifestPath := writeEncryptFileModeFixture(t, plainSecretManifestYAML)

	mock := &upgradeMock{encryptResult: sopsMetadataOnlySecretManifestYAML}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "my-secret",
		"--key", "data.password",
		"-f", clusterPath,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected verification error when SOPS encrypts nothing")
	}
	if !strings.Contains(err.Error(), "still plaintext") {
		t.Errorf("expected plaintext verification error, got: %v", err)
	}

	manifestAfter, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatalf("read manifest after failed run: %v", readErr)
	}
	if string(manifestAfter) != plainSecretManifestYAML {
		t.Errorf("manifest file must be untouched on verification failure, got:\n%s", manifestAfter)
	}

	clusterAfter, readErr := os.ReadFile(clusterPath)
	if readErr != nil {
		t.Fatalf("read cluster file after failed run: %v", readErr)
	}
	if strings.Contains(string(clusterAfter), "encrypted_paths") {
		t.Errorf("cluster file must not gain encrypted_paths on verification failure, got:\n%s", clusterAfter)
	}
}

func TestRunEncryptManifest_FileModeStillWorks(t *testing.T) {
	// Smoke check that providing -f triggers the file-mode branch (which exits
	// with a clear error before any cluster fetch). Confirms the dispatcher.
	mock := &upgradeMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "any",
		"--key", "foo",
		"-f", "/tmp/does-not-exist.yaml",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected file-mode error for missing file")
	}
	if !strings.Contains(err.Error(), "failed to read cluster file") {
		t.Errorf("expected file-mode error, got %v", err)
	}
	if len(mock.capturedRequests) != 0 {
		t.Errorf("file mode must not PATCH, got %d", len(mock.capturedRequests))
	}
}

func TestRunEncryptManifest_FileAndClusterMutuallyExclusive(t *testing.T) {
	mock := &upgradeMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "any",
		"--key", "foo",
		"-f", "/tmp/x.yaml",
		"--cluster", "demo",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected mutual-exclusion error")
	}
}

func TestRunEncryptAddon_ClusterModePatchesEncryptedValues(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		addonValues:   "adminPassword: hunter2\n",
		encryptResult: "adminPassword: ENC[AES256_GCM,data:abc]\n",
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "addon",
		"--name", "website",
		"--key", "adminPassword",
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
		t.Fatal("expected configuration in PATCH")
	}
	if len(addon.Configuration.EncryptedPaths) != 1 || addon.Configuration.EncryptedPaths[0] != "adminPassword" {
		t.Errorf("addon.encrypted_paths = %v, want [adminPassword]", addon.Configuration.EncryptedPaths)
	}
	decoded, err := base64.StdEncoding.DecodeString(addon.Configuration.ValuesBase64)
	if err != nil {
		t.Fatalf("decode values_base64: %v", err)
	}
	if !strings.Contains(string(decoded), "ENC[AES256_GCM") {
		t.Errorf("expected encrypted values in PATCH, got: %s", decoded)
	}
}

func TestRunDecryptManifest_ClusterModePrintsDecrypted(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte("kind: Secret\ndata:\n  password: ENC[...]\n")),
		decryptResult: "kind: Secret\ndata:\n  password: hunter2\n",
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "decrypt", "manifest", "demo-namespace",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "password: hunter2") {
		t.Errorf("expected decrypted YAML, got:\n%s", out.String())
	}
	if len(mock.capturedRequests) != 0 {
		t.Errorf("decrypt must not PATCH, got %d", len(mock.capturedRequests))
	}
}

func TestRunDecryptAddon_ClusterModePrintsDecrypted(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		addonValues:   "secret: ENC[...]\n",
		decryptResult: "secret: hunter2\n",
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "decrypt", "addon",
		"--name", "website",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "secret: hunter2") {
		t.Errorf("expected decrypted YAML, got:\n%s", out.String())
	}
}

func TestRunManifestsDelete_DryRunNoCall(t *testing.T) {
	mock := &upgradeMock{iac: sampleIaCYAMLForCmd}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "manifests", "delete", "demo-namespace",
		"--cluster", fakeClusterUUID,
		"--dry-run",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.disconnectCalls) != 0 {
		t.Errorf("dry-run must not call disconnect, got %d", len(mock.disconnectCalls))
	}
	if !strings.Contains(out.String(), "Would disconnect") {
		t.Errorf("expected dry-run notice, got:\n%s", out.String())
	}
}
