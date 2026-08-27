package migrate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeModule is a built-in stand-in with a fixed detection score.
type fakeModule struct {
	name       string
	confidence float64
}

func (m fakeModule) Describe() Description {
	return Description{Name: m.name, Version: "test", Protocol: ProtocolVersion, Builtin: true}
}

func (m fakeModule) Detect(context.Context, string) (Detection, error) {
	return Detection{Confidence: m.confidence, Reason: "fake"}, nil
}

func (m fakeModule) Convert(context.Context, ConvertRequest) (Result, error) {
	return Result{Cluster: Cluster{Metadata: Metadata{Name: m.name}}}, nil
}

// echoModule is a complete external module in shell: enough to prove the
// wire protocol end to end without a second language in the test tree.
const echoModule = `#!/bin/sh
case "$1" in
  describe)
    printf '%s' '{"name":"echo","version":"0.1","protocol":1,"summary":"echo test module","file_patterns":["echo.txt"]}'
    ;;
  detect)
    dir=$(sed -n 's/.*"dir":"\([^"]*\)".*/\1/p')
    if [ -f "$dir/echo.txt" ]; then
      printf '%s' '{"confidence":0.9,"files":["echo.txt"],"reason":"echo.txt found"}'
    else
      printf '%s' '{"confidence":0,"reason":"no echo.txt"}'
    fi
    ;;
  convert)
    request=$(cat)
    name=$(printf '%s' "$request" | sed -n 's/.*"cluster_name":"\([^"]*\)".*/\1/p')
    printf '{"cluster":{"apiVersion":"ankra.io/v1alpha1","kind":"ImportCluster","metadata":{"name":"%s"},"spec":{"stacks":[{"name":"echo","manifests":[{"name":"m","from_file":"manifests/m.yaml"}]}]}},"files":{"manifests/m.yaml":"kind: ConfigMap\\n"},"warnings":["hello from echo"]}' "$name"
    ;;
  *)
    echo "unknown verb $1" >&2
    exit 2
    ;;
esac
`

func writeModule(t *testing.T, dir, name, body string, executable bool) {
	t.Helper()
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if err := os.WriteFile(filepath.Join(dir, ExternalModulePrefix+name), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func testRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script modules")
	}
	dir := t.TempDir()
	writeModule(t, dir, "echo", echoModule, true)
	writeModule(t, dir, "old", "#!/bin/sh\nprintf '%s' '{\"name\":\"old\",\"protocol\":0}'\n", true)
	writeModule(t, dir, "broken", "#!/bin/sh\necho 'cannot start: missing dependency' >&2\nexit 1\n", true)
	writeModule(t, dir, "noexec", echoModule, false)
	writeModule(t, dir, "fake", echoModule, true) // collides with the built-in

	registry := NewRegistry(fakeModule{name: "fake", confidence: 0.2})
	registry.searchDirs = func() []string { return []string{dir, filepath.Join(dir, "missing")} }
	return registry, dir
}

func TestRegistryDiscoversExternalModules(t *testing.T) {
	registry, _ := testRegistry(t)
	modules, notes := registry.Modules(context.Background())

	var names []string
	for _, module := range modules {
		names = append(names, module.Describe().Name)
	}
	if got := strings.Join(names, ","); got != "fake,echo" {
		t.Errorf("modules = %s, want built-in first, then echo; old/broken/noexec skipped and the external fake shadowed", got)
	}

	echo := modules[1].Describe()
	if echo.Builtin || !strings.HasSuffix(echo.Path, "ankra-module-echo") || echo.Version != "0.1" || echo.FilePatterns[0] != "echo.txt" {
		t.Errorf("echo description = %+v", echo)
	}

	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "ankra-module-old") || !strings.Contains(joined, "protocol 0") {
		t.Errorf("expected a protocol note for old, got %v", notes)
	}
	if !strings.Contains(joined, "ankra-module-broken") || !strings.Contains(joined, "missing dependency") {
		t.Errorf("a failing describe should relay the module's stderr, got %v", notes)
	}
	if strings.Contains(joined, "noexec") {
		t.Errorf("a non-executable file is not a module and should not be reported, got %v", notes)
	}
}

func TestRegistryLookup(t *testing.T) {
	registry, _ := testRegistry(t)
	if _, err := registry.Lookup(context.Background(), "echo"); err != nil {
		t.Error(err)
	}
	if _, err := registry.Lookup(context.Background(), "nope"); err == nil || !strings.Contains(err.Error(), "ankra migrate modules") {
		t.Errorf("unknown module should point at the listing command, got %v", err)
	}
}

func TestRegistryDetectOrdersByConfidence(t *testing.T) {
	registry, _ := testRegistry(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "echo.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	candidates, _ := registry.Detect(context.Background(), project)
	if len(candidates) != 2 || candidates[0].Module.Describe().Name != "echo" || candidates[0].Detection.Confidence != 0.9 {
		t.Fatalf("candidates = %+v, want echo (0.9) ahead of fake (0.2)", candidates)
	}
	if candidates[0].Detection.Files[0] != "echo.txt" || candidates[1].Detection.Confidence != 0.2 {
		t.Errorf("candidates = %+v", candidates)
	}

	empty, _ := registry.Detect(context.Background(), t.TempDir())
	if empty[0].Module.Describe().Name != "fake" {
		t.Errorf("without echo.txt the built-in should lead, got %s", empty[0].Module.Describe().Name)
	}
}

func TestExternalModuleConvert(t *testing.T) {
	registry, _ := testRegistry(t)
	module, err := registry.Lookup(context.Background(), "echo")
	if err != nil {
		t.Fatal(err)
	}
	result, err := module.Convert(context.Background(), ConvertRequest{Dir: "/tmp/x", ClusterName: "from-request", Namespace: "ns"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Cluster.Metadata.Name != "from-request" {
		t.Errorf("the module did not receive the request: %+v", result.Cluster.Metadata)
	}
	if result.Files["manifests/m.yaml"] != "kind: ConfigMap\n" || len(result.Warnings) != 1 {
		t.Errorf("result = %+v", result)
	}
	if err := Validate(result); err != nil {
		t.Errorf("valid result rejected: %v", err)
	}
}

func TestExternalModuleFailureCarriesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script modules")
	}
	dir := t.TempDir()
	writeModule(t, dir, "flaky", "#!/bin/sh\n[ \"$1\" = describe ] && printf '%s' '{\"name\":\"flaky\",\"protocol\":1}' && exit 0\necho 'line one' >&2\necho 'the real reason' >&2\nexit 3\n", true)
	registry := NewRegistry()
	registry.searchDirs = func() []string { return []string{dir} }
	module, err := registry.Lookup(context.Background(), "flaky")
	if err != nil {
		t.Fatal(err)
	}
	_, err = module.Detect(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "detect failed") || !strings.Contains(err.Error(), "the real reason") {
		t.Errorf("error should name the verb and carry stderr, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	good := Result{
		Cluster: Cluster{Metadata: Metadata{Name: "x"}, Spec: Spec{Stacks: []Stack{{Manifests: []Manifest{{Name: "m", FromFile: "manifests/m.yaml"}}}}}},
		Files:   map[string]string{"manifests/m.yaml": "kind: ConfigMap\n"},
	}
	if err := Validate(good); err != nil {
		t.Errorf("valid result rejected: %v", err)
	}

	cases := map[string]Result{
		"absolute path": {Cluster: good.Cluster, Files: map[string]string{"/etc/passwd": "", "manifests/m.yaml": ""}},
		"parent escape": {Cluster: good.Cluster, Files: map[string]string{"../x.yaml": "", "manifests/m.yaml": ""}},
		"nested escape": {Cluster: good.Cluster, Files: map[string]string{"a/../../x.yaml": "", "manifests/m.yaml": ""}},
		"missing file":  {Cluster: good.Cluster, Files: map[string]string{}},
		"no name":       {Cluster: Cluster{}, Files: map[string]string{}},
	}
	for name, result := range cases {
		if err := Validate(result); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestExternalModuleName(t *testing.T) {
	if name, ok := externalModuleName("ankra-module-heroku"); !ok || name != "heroku" {
		t.Errorf("got %q %v", name, ok)
	}
	if _, ok := externalModuleName("ankra-module-"); ok {
		t.Error("empty name accepted")
	}
	if _, ok := externalModuleName("something-else"); ok {
		t.Error("unrelated file accepted")
	}
}
