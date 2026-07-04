package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

const sampleIaCYAML = `apiVersion: v1
kind: ImportCluster
metadata:
  name: website-demo
spec:
  stacks:
    - name: demo-web-app
      description: The demo web app stack
      variables:
        VERSION: "1.0.0"
      addons:
        - name: website
          chart_name: website
          chart_version: 1.0.145
          namespace: web
          registry_name: my-registry
          registry_url: oci://registry.example.com
        - name: cert-manager
          chart_name: cert-manager
          chart_version: 1.14.0
          namespace: cert-manager
      manifests:
        - name: demo-namespace
          namespace: web
    - name: monitoring
      addons:
        - name: prometheus
          chart_name: kube-prometheus-stack
          chart_version: 56.0.0
          namespace: monitoring
        - name: website
          chart_name: website
          chart_version: 1.0.140
          namespace: monitoring-web
      manifests: []
`

func TestParseImportClusterYAML_OK(t *testing.T) {
	doc, err := parseImportClusterYAML([]byte(sampleIaCYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Metadata.Name != "website-demo" {
		t.Errorf("metadata.name = %q, want website-demo", doc.Metadata.Name)
	}
	if len(doc.Spec.Stacks) != 2 {
		t.Fatalf("expected 2 stacks, got %d", len(doc.Spec.Stacks))
	}
	if doc.Spec.Stacks[0].Name != "demo-web-app" {
		t.Errorf("stacks[0].Name = %q, want demo-web-app", doc.Spec.Stacks[0].Name)
	}
	if v := doc.Spec.Stacks[0].Variables["VERSION"]; v != "1.0.0" {
		t.Errorf("variables.VERSION = %q, want 1.0.0", v)
	}
	if len(doc.Spec.Stacks[0].Addons) != 2 {
		t.Errorf("stacks[0].Addons = %d, want 2", len(doc.Spec.Stacks[0].Addons))
	}
}

func TestParseImportClusterYAML_WrongKind(t *testing.T) {
	src := `kind: Deployment
metadata:
  name: x
spec:
  stacks: []
`
	if _, err := parseImportClusterYAML([]byte(src)); err == nil {
		t.Error("expected error for wrong kind")
	}
}

func TestFindAddonInIaC_Unique(t *testing.T) {
	doc, _ := parseImportClusterYAML([]byte(sampleIaCYAML))
	stack, addon, err := findAddonInIaC(doc, "cert-manager", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stack.Name != "demo-web-app" {
		t.Errorf("stack.Name = %q, want demo-web-app", stack.Name)
	}
	if addon.ChartVersion != "1.14.0" {
		t.Errorf("addon.ChartVersion = %q, want 1.14.0", addon.ChartVersion)
	}
}

func TestFindAddonInIaC_Ambiguous(t *testing.T) {
	doc, _ := parseImportClusterYAML([]byte(sampleIaCYAML))
	_, _, err := findAddonInIaC(doc, "website", "")
	if err == nil {
		t.Fatal("expected ambiguous-stack error")
	}
	if !strings.Contains(err.Error(), "demo-web-app") || !strings.Contains(err.Error(), "monitoring") {
		t.Errorf("expected error to list both stacks, got: %v", err)
	}
}

func TestFindAddonInIaC_DisambiguateWithStack(t *testing.T) {
	doc, _ := parseImportClusterYAML([]byte(sampleIaCYAML))
	stack, addon, err := findAddonInIaC(doc, "website", "monitoring")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stack.Name != "monitoring" {
		t.Errorf("stack.Name = %q, want monitoring", stack.Name)
	}
	if addon.Namespace != "monitoring-web" {
		t.Errorf("addon.Namespace = %q, want monitoring-web", addon.Namespace)
	}
}

func TestFindAddonInIaC_StackMismatch(t *testing.T) {
	doc, _ := parseImportClusterYAML([]byte(sampleIaCYAML))
	_, _, err := findAddonInIaC(doc, "cert-manager", "monitoring")
	if err == nil {
		t.Fatal("expected stack-mismatch error")
	}
	if !strings.Contains(err.Error(), "not part of stack") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestFindAddonInIaC_NotFound(t *testing.T) {
	doc, _ := parseImportClusterYAML([]byte(sampleIaCYAML))
	_, _, err := findAddonInIaC(doc, "does-not-exist", "")
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestFindManifestInIaC_Unique(t *testing.T) {
	doc, _ := parseImportClusterYAML([]byte(sampleIaCYAML))
	stack, m, err := findManifestInIaC(doc, "demo-namespace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stack.Name != "demo-web-app" {
		t.Errorf("stack.Name = %q, want demo-web-app", stack.Name)
	}
	if m.Namespace != "web" {
		t.Errorf("manifest.Namespace = %q, want web", m.Namespace)
	}
}

func TestFindManifestInIaC_NotFound(t *testing.T) {
	doc, _ := parseImportClusterYAML([]byte(sampleIaCYAML))
	_, _, err := findManifestInIaC(doc, "does-not-exist")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestFindManifestInIaC_DataIntegrityBugOnDuplicates(t *testing.T) {
	// Synthesise a doc where the same manifest name appears in two stacks
	// to confirm we fail loudly rather than silently pick one.
	doc, _ := parseImportClusterYAML([]byte(sampleIaCYAML))
	dupStack := client.StackSpec{
		Name:      "other-stack",
		Manifests: []client.ManifestSpec{{Name: "demo-namespace", Namespace: "other"}},
	}
	doc.Spec.Stacks = append(doc.Spec.Stacks, dupStack)
	_, _, err := findManifestInIaC(doc, "demo-namespace")
	if err == nil {
		t.Fatal("expected data-integrity error for duplicate manifest names")
	}
	if !strings.Contains(err.Error(), "multiple stacks") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCopyStackMetadata_PreservesDescriptionAndVariables(t *testing.T) {
	src := &client.StackSpec{
		Name:        "demo",
		Description: "Demo stack",
		Variables:   map[string]string{"A": "1", "B": "2"},
		Addons:      []client.AddonSpec{{Name: "a"}},
		Manifests:   []client.ManifestSpec{{Name: "m"}},
	}
	copied := copyStackMetadata(src)
	if copied.Description != "Demo stack" {
		t.Errorf("description not preserved: %q", copied.Description)
	}
	if len(copied.Variables) != 2 {
		t.Errorf("variables not preserved: %v", copied.Variables)
	}
	if len(copied.Addons) != 0 || len(copied.Manifests) != 0 {
		t.Errorf("addons/manifests should start empty, got addons=%d manifests=%d", len(copied.Addons), len(copied.Manifests))
	}
	// Mutation must not affect src.
	copied.Variables["C"] = "3"
	if _, ok := src.Variables["C"]; ok {
		t.Errorf("copy must not share variables map with source")
	}
}

func TestBuildPartialStackPatch_Shape(t *testing.T) {
	stack := client.StackSpec{
		Name:      "demo",
		Manifests: []client.ManifestSpec{},
		Addons:    []client.AddonSpec{{Name: "x", ChartName: "x", ChartVersion: "1"}},
	}
	req := buildPartialStackPatch(stack)
	if !req.PartialStack {
		t.Error("expected partial_stack=true")
	}
	if len(req.Spec.Stacks) != 1 {
		t.Errorf("expected exactly 1 stack in spec, got %d", len(req.Spec.Stacks))
	}
	// Round-trip through JSON to assert the wire shape.
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	_ = json.Unmarshal(b, &generic)
	if v, _ := generic["partial_stack"].(bool); !v {
		t.Errorf("partial_stack missing in JSON body: %s", string(b))
	}
}

func TestApplyAddonMutations_OnlyChangesRequestedFields(t *testing.T) {
	orig := client.AddonSpec{
		Name:                   "website",
		ChartName:              "website",
		ChartVersion:           "1.0.0",
		Namespace:              "web",
		RegistryName:           "regA",
		RegistryURL:            "oci://a",
		RegistryCredentialName: "credA",
	}
	flags := addonsUpgradeFlags{ChartVersion: "1.0.146"}
	out := applyAddonMutations(orig, flags, nil)
	if out.ChartVersion != "1.0.146" {
		t.Errorf("chart_version = %q, want 1.0.146", out.ChartVersion)
	}
	if out.Namespace != "web" {
		t.Errorf("namespace should be untouched, got %q", out.Namespace)
	}
	if out.RegistryName != "regA" {
		t.Errorf("registry_name should be untouched, got %q", out.RegistryName)
	}
	if out.Configuration != nil {
		t.Errorf("configuration should remain nil when no values flags, got %v", out.Configuration)
	}
}

func TestApplyAddonMutations_EmptyRegistryFlagIsNoChange(t *testing.T) {
	orig := client.AddonSpec{Name: "x", RegistryName: "regA"}
	flags := addonsUpgradeFlags{RegistryName: ""}
	out := applyAddonMutations(orig, flags, nil)
	if out.RegistryName != "regA" {
		t.Errorf("empty registry flag should not clear existing value, got %q", out.RegistryName)
	}
}

func TestApplyAddonMutations_ValuesB64Set(t *testing.T) {
	orig := client.AddonSpec{Name: "x"}
	newB64 := "aGVsbG8="
	out := applyAddonMutations(orig, addonsUpgradeFlags{}, &newB64)
	if out.Configuration == nil {
		t.Fatal("expected configuration to be populated")
	}
	if out.Configuration.ValuesBase64 != newB64 {
		t.Errorf("configuration.values_base64 = %q, want %q", out.Configuration.ValuesBase64, newB64)
	}
}

func TestAddonsUpgradeFlags_HasAnyMutation(t *testing.T) {
	cases := map[string]struct {
		flags addonsUpgradeFlags
		want  bool
	}{
		"empty":        {addonsUpgradeFlags{}, false},
		"chart":        {addonsUpgradeFlags{ChartVersion: "1.0.0"}, true},
		"namespace":    {addonsUpgradeFlags{Namespace: "x"}, true},
		"registry":     {addonsUpgradeFlags{RegistryName: "r"}, true},
		"values_file":  {addonsUpgradeFlags{ValuesFromFile: "v.yaml"}, true},
		"values_stdin": {addonsUpgradeFlags{ValuesStdin: "-"}, true},
		"set":          {addonsUpgradeFlags{SetEntries: []string{"a=b"}}, true},
	}
	for name, tc := range cases {
		if got := tc.flags.HasAnyMutation(); got != tc.want {
			t.Errorf("%s: HasAnyMutation() = %v, want %v", name, got, tc.want)
		}
	}
}

func TestAddonsUpgradeFlags_ConflictDetection(t *testing.T) {
	f := addonsUpgradeFlags{ValuesFromFile: "v.yaml", SetEntries: []string{"a=b"}}
	if !f.HasValuesReplace() || !f.HasSet() {
		t.Fatal("setup error")
	}
	// The actual conflict error is raised in runAddonsUpgrade; this just
	// asserts the detection booleans line up.
}

func TestParseOutputFormat(t *testing.T) {
	cases := map[string]struct {
		in      string
		want    outputFormat
		wantErr bool
	}{
		"empty":    {"", outputDefault, false},
		"json":     {"json", outputJSON, false},
		"yaml":     {"yaml", outputYAML, false},
		"yml":      {"yml", outputYAML, false},
		"JSON":     {"JSON", outputJSON, false},
		"trailing": {"  json  ", outputJSON, false},
		"invalid":  {"xml", outputDefault, true},
	}
	for name, tc := range cases {
		got, err := parseOutputFormat(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", name, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestRenderDryRun_HumanReadable(t *testing.T) {
	before := client.StackSpec{Name: "demo", Addons: []client.AddonSpec{{Name: "a", ChartVersion: "1.0.0"}}}
	after := client.StackSpec{Name: "demo", Addons: []client.AddonSpec{{Name: "a", ChartVersion: "1.0.1"}}}
	var buf bytes.Buffer
	if err := renderDryRun(&buf, before, after, []string{"namespace will be re-created"}, outputDefault); err != nil {
		t.Fatalf("renderDryRun: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Before:") || !strings.Contains(out, "After:") {
		t.Errorf("expected Before/After headings, got:\n%s", out)
	}
	if !strings.Contains(out, "1.0.1") {
		t.Errorf("expected after value, got:\n%s", out)
	}
	if !strings.Contains(out, "namespace will be re-created") {
		t.Errorf("expected notice, got:\n%s", out)
	}
}

func TestRenderDryRun_JSONEnvelope(t *testing.T) {
	before := client.StackSpec{Name: "demo", Addons: []client.AddonSpec{{Name: "a", ChartName: "c", ChartVersion: "1"}}}
	after := client.StackSpec{Name: "demo", Addons: []client.AddonSpec{{Name: "a", ChartName: "c", ChartVersion: "2"}}}
	var buf bytes.Buffer
	if err := renderDryRun(&buf, before, after, []string{"n1"}, outputJSON); err != nil {
		t.Fatalf("renderDryRun: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if _, ok := env["before"]; !ok {
		t.Error("expected before key in envelope")
	}
	if _, ok := env["after"]; !ok {
		t.Error("expected after key in envelope")
	}
	notices, _ := env["notices"].([]any)
	if len(notices) != 1 {
		t.Errorf("expected one notice, got %v", env["notices"])
	}
}

func TestPrintAsOutput_HumanIncludesCommitURL(t *testing.T) {
	res := &client.PatchStackResult{
		StackName: "demo",
		CommitSHA: "deadbeef",
		CommitURL: "https://github.com/org/repo/commit/deadbeef",
		JobCount:  3,
	}
	var buf bytes.Buffer
	if err := printAsOutput(&buf, res, outputDefault); err != nil {
		t.Fatalf("printAsOutput: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"demo"`) {
		t.Errorf("expected stack name in output: %s", out)
	}
	if !strings.Contains(out, "deadbeef") {
		t.Errorf("expected commit SHA in output: %s", out)
	}
	if !strings.Contains(out, "https://github.com/org/repo/commit/deadbeef") {
		t.Errorf("expected commit URL in output: %s", out)
	}
}

func TestMapPatchError_StatusCodes(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
		wantSub    string
	}{
		{"400 validation", 400, `{"detail":"Exactly one stack must be provided"}`, "Exactly one stack"},
		{"403 sandbox", 403, `{"detail":"sandbox mode is enabled"}`, "sandbox"},
		{"409 pending", 409, `{"detail":"Stack is pending deletion"}`, "pending deletion"},
		{"422 git push", 422, `{"detail":"permission denied"}`, "git push failed"},
		{"422 circular dep", 422, `{"detail":"Circular dependency detected: a -> b -> a"}`, "Circular dependency detected"},
		{"401 unauth", 401, ``, "unauthorized"},
		{"500 other", 500, `{"detail":"unexpected"}`, "status 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perr := &client.PatchStackError{StatusCode: tc.statusCode, Body: []byte(tc.body)}
			got := mapPatchError(perr)
			if got == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(strings.ToLower(got.Error()), strings.ToLower(tc.wantSub)) {
				t.Errorf("error %q does not contain %q", got.Error(), tc.wantSub)
			}
		})
	}
}

func TestMapPatchError_ExitCodeClassification(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		wantExit   int
	}{
		{"401 unauthorized -> exitAuth", 401, exitAuth},
		{"403 forbidden -> exitAuth", 403, exitAuth},
		{"404 not found -> exitNotFound", 404, exitNotFound},
		{"500 server error -> exitError", 500, exitError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perr := &client.PatchStackError{StatusCode: tc.statusCode, Body: []byte(`{"detail":"boom"}`)}
			err := mapPatchError(perr)
			if err == nil {
				t.Fatal("expected error")
			}
			if got := exitCodeFor(err); got != tc.wantExit {
				t.Errorf("exitCodeFor(mapPatchError(%d)) = %d, want %d", tc.statusCode, got, tc.wantExit)
			}
		})
	}
}

func TestMapPatchError_ServerErrorTriggersSupportHint(t *testing.T) {
	perr := &client.PatchStackError{StatusCode: 500, Body: []byte(`{"detail":"boom"}`)}
	err := mapPatchError(perr)
	var unexpected *client.UnexpectedResponseError
	if !errors.As(err, &unexpected) {
		t.Fatalf("5xx should wrap *client.UnexpectedResponseError so the support hint fires, got %T: %v", err, err)
	}
	if unexpected.StatusCode != 500 {
		t.Errorf("wrapped status = %d, want 500", unexpected.StatusCode)
	}
	if got := err.Error(); !strings.Contains(got, "status 500") {
		t.Errorf("message text changed: %q", got)
	}
}

func TestMapPatchError_MessagesUnchanged(t *testing.T) {
	cases := []struct {
		statusCode int
		body       string
		want       string
	}{
		{401, ``, "unauthorized. Run `ankra login` to re-authenticate"},
		{403, `{"detail":"nope"}`, "forbidden: nope"},
		{403, `sandbox mode enabled`, "sandbox clusters cannot be patched via the CLI; promote the cluster first"},
		{404, `{"detail":"missing"}`, "update stack failed: status 404, body: missing"},
		{500, `{"detail":"boom"}`, "update stack failed: status 500, body: boom"},
	}
	for _, tc := range cases {
		perr := &client.PatchStackError{StatusCode: tc.statusCode, Body: []byte(tc.body)}
		if got := mapPatchError(perr).Error(); got != tc.want {
			t.Errorf("status %d message = %q, want %q", tc.statusCode, got, tc.want)
		}
	}
}

func TestConfirmNamespaceChange_YesFlagSkipsPrompt(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("") // empty — would block if prompted
	if err := confirmNamespaceChange(context.Background(), in, &out, "old", "new", true); err != nil {
		t.Fatalf("--yes should bypass prompt, got %v", err)
	}
}

func TestConfirmNamespaceChange_YesInput(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("y\n")
	if err := confirmNamespaceChange(context.Background(), in, &out, "old", "new", false); err != nil {
		t.Fatalf("expected y to confirm, got %v", err)
	}
}

func TestConfirmNamespaceChange_NoInput(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("n\n")
	if err := confirmNamespaceChange(context.Background(), in, &out, "old", "new", false); err == nil {
		t.Fatal("expected cancellation error for n input")
	}
}

func TestReadSource_StdinSentinel(t *testing.T) {
	// readSource with empty filePath + "-" stdinFlag should attempt to read
	// from stdin; we just verify the function returns when given an explicit
	// "" filePath without stdinFlag.
	if _, err := readSource("", ""); err == nil {
		t.Error("expected error when no source provided")
	}
}
