package kubeconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const foreignKubeconfig = `apiVersion: v1
kind: Config
preferences:
  colors: true
clusters:
- name: prod
  cluster:
    server: https://prod.example:6443
    certificate-authority-data: QUJD
    tls-server-name: prod.internal
users:
- name: prod-admin
  user:
    client-certificate-data: REVG
    client-key-data: R0hJ
contexts:
- name: prod
  context:
    cluster: prod
    user: prod-admin
    namespace: kube-system
current-context: prod
`

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Cluster":   "my-cluster",
		"prod_eu-1":    "prod-eu-1",
		"  Edge  ":     "edge",
		"":             "cluster",
		"!!!":          "cluster",
		"already-good": "already-good",
	}
	for input, want := range cases {
		if got := Slugify(input); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestContextName(t *testing.T) {
	if got := ContextName("My Cluster"); got != "ankra-my-cluster" {
		t.Fatalf("ContextName = %q", got)
	}
}

func TestBuildExecEntryShape(t *testing.T) {
	entry, err := BuildExecEntry("ankra-demo", "https://api/k8s", "", []string{"cluster", "kube-token", "--cluster", "id-1"}, "team-a", false)
	if err != nil {
		t.Fatal(err)
	}
	var user execUser
	if err := entry.User.Decode(&user); err != nil {
		t.Fatal(err)
	}
	if user.Exec.APIVersion != ExecAPIVersion {
		t.Errorf("exec apiVersion = %q", user.Exec.APIVersion)
	}
	if user.Exec.Command != "ankra" {
		t.Errorf("exec command = %q (empty should default to ankra)", user.Exec.Command)
	}
	if user.Exec.InteractiveMode != "Never" {
		t.Errorf("interactiveMode = %q", user.Exec.InteractiveMode)
	}
	if strings.Join(user.Exec.Args, " ") != "cluster kube-token --cluster id-1" {
		t.Errorf("exec args = %v", user.Exec.Args)
	}

	custom, err := BuildExecEntry("ankra-demo", "https://api/k8s", "/usr/local/bin/ankra", []string{"x"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var customUser execUser
	if err := custom.User.Decode(&customUser); err != nil {
		t.Fatal(err)
	}
	if customUser.Exec.Command != "/usr/local/bin/ankra" {
		t.Errorf("custom exec command = %q", customUser.Exec.Command)
	}
	var context contextBody
	if err := entry.Context.Decode(&context); err != nil {
		t.Fatal(err)
	}
	if context.Cluster != "ankra-demo" || context.User != "ankra-demo" || context.Namespace != "team-a" {
		t.Errorf("context body = %+v", context)
	}
}

func TestUpsertReplacesByName(t *testing.T) {
	config := &Config{APIVersion: "v1", Kind: "Config"}
	first, _ := BuildExecEntry("ankra-demo", "https://one/k8s", "", []string{"a"}, "", false)
	second, _ := BuildExecEntry("ankra-demo", "https://two/k8s", "", []string{"b"}, "", false)
	config.Upsert(first)
	config.Upsert(second)
	if len(config.Clusters) != 1 || len(config.Users) != 1 || len(config.Contexts) != 1 {
		t.Fatalf("expected single entry per list, got clusters=%d users=%d contexts=%d",
			len(config.Clusters), len(config.Users), len(config.Contexts))
	}
	if got := config.ClusterServer("ankra-demo"); got != "https://two/k8s" {
		t.Errorf("server after replace = %q", got)
	}
}

func TestUpsertPreservesForeignEntries(t *testing.T) {
	config := &Config{}
	if err := yaml.Unmarshal([]byte(foreignKubeconfig), config); err != nil {
		t.Fatal(err)
	}
	entry, _ := BuildExecEntry("ankra-demo", "https://api/k8s", "", []string{"x"}, "", false)
	config.Upsert(entry)

	data, err := Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)

	// Foreign cluster body fields must survive verbatim.
	for _, fragment := range []string{
		"certificate-authority-data: QUJD",
		"tls-server-name: prod.internal",
		"client-certificate-data: REVG",
		"client-key-data: R0hJ",
		"colors: true", // preferences preserved via inline Extra
	} {
		if !strings.Contains(rendered, fragment) {
			t.Errorf("expected rendered config to retain %q\n---\n%s", fragment, rendered)
		}
	}
	if !strings.Contains(rendered, "name: ankra-demo") {
		t.Errorf("managed entry missing\n%s", rendered)
	}
	if config.CurrentContext != "prod" {
		t.Errorf("current-context should be untouched, got %q", config.CurrentContext)
	}
}

func TestRemoveClearsCurrentContext(t *testing.T) {
	config := &Config{CurrentContext: "ankra-demo"}
	entry, _ := BuildExecEntry("ankra-demo", "https://api/k8s", "", []string{"x"}, "", false)
	config.Upsert(entry)
	if removed := config.Remove("ankra-demo"); !removed {
		t.Fatal("expected removal to report true")
	}
	if len(config.Clusters) != 0 || len(config.Users) != 0 || len(config.Contexts) != 0 {
		t.Fatal("entry not fully removed")
	}
	if config.CurrentContext != "" {
		t.Errorf("current-context should be cleared, got %q", config.CurrentContext)
	}
	if config.Remove("does-not-exist") {
		t.Error("removing a missing entry should report false")
	}
}

func TestRemoveLeavesOtherCurrentContext(t *testing.T) {
	config := &Config{CurrentContext: "prod"}
	entry, _ := BuildExecEntry("ankra-demo", "https://api/k8s", "", []string{"x"}, "", false)
	config.Upsert(entry)
	config.Remove("ankra-demo")
	if config.CurrentContext != "prod" {
		t.Errorf("unrelated current-context changed to %q", config.CurrentContext)
	}
}

func TestManagedContextNames(t *testing.T) {
	config := &Config{}
	if err := yaml.Unmarshal([]byte(foreignKubeconfig), config); err != nil {
		t.Fatal(err)
	}
	demo, _ := BuildExecEntry("ankra-demo", "https://api/k8s", "", []string{"x"}, "", false)
	staging, _ := BuildExecEntry("ankra-staging", "https://api2/k8s", "", []string{"y"}, "", false)
	config.Upsert(demo)
	config.Upsert(staging)
	names := config.ManagedContextNames()
	if len(names) != 2 || names[0] != "ankra-demo" || names[1] != "ankra-staging" {
		t.Fatalf("managed names = %v", names)
	}
}

func TestLoadMissingFileReturnsEmptyConfig(t *testing.T) {
	config, err := Load(filepath.Join(t.TempDir(), "nope", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if config.APIVersion != "v1" || config.Kind != "Config" {
		t.Errorf("unexpected empty config: %+v", config)
	}
}

func TestSaveAndReloadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config")
	config := &Config{APIVersion: "v1", Kind: "Config"}
	entry, _ := BuildTokenEntry("ankra-demo", "https://api/k8s", "secret-token", "team-a", true)
	config.Upsert(entry)
	config.SetCurrentContext("ankra-demo")
	if err := Save(path, config); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("kubeconfig perms = %o, want 600", info.Mode().Perm())
		}
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CurrentContext != "ankra-demo" {
		t.Errorf("current-context = %q", reloaded.CurrentContext)
	}
	if got := reloaded.ClusterServer("ankra-demo"); got != "https://api/k8s" {
		t.Errorf("server = %q", got)
	}
	var user tokenUser
	if err := reloaded.Users[0].User.Decode(&user); err != nil {
		t.Fatal(err)
	}
	if user.Token != "secret-token" {
		t.Errorf("token = %q", user.Token)
	}
	var cluster clusterBody
	if err := reloaded.Clusters[0].Cluster.Decode(&cluster); err != nil {
		t.Fatal(err)
	}
	if !cluster.InsecureSkipTLSVerify {
		t.Error("insecure-skip-tls-verify should be set")
	}
}
