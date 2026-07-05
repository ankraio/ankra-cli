package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
	"ankra/internal/kubeconfig"
)

type kubeconfigMock struct {
	baseMock
	cluster     client.ClusterListItem
	clusters    []client.ClusterListItem
	token       string
	proxyBase   string
	orgOverride string
}

func (m kubeconfigMock) OrganisationOverride() string { return m.orgOverride }

func (m kubeconfigMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.cluster.Name == name || m.cluster.ID == name {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m kubeconfigMock) GetClusterKubeToken(ctx context.Context, clusterID string) (*client.KubeToken, error) {
	base := m.proxyBase
	if base == "" {
		base = "https://api.platform.ankra.dev"
	}
	return &client.KubeToken{
		Token:  m.token,
		Server: base + "/api/v1/clusters/" + clusterID + "/k8s",
	}, nil
}

func (m kubeconfigMock) ListClusters(page, pageSize int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{
		Result:     m.clusters,
		Pagination: client.Pagination{Page: 1, TotalPages: 1, PageSize: pageSize},
	}, nil
}

func withKubeconfigMock(t *testing.T, mock kubeconfigMock) {
	t.Helper()
	// Isolate HOME so org/cluster selection fallbacks never read the
	// developer's real ~/.ankra state.
	t.Setenv("HOME", t.TempDir())
	originalClient := apiClient
	originalBaseURL := baseURL
	apiClient = mock
	baseURL = "https://test.ankra.app"
	t.Cleanup(func() {
		apiClient = originalClient
		baseURL = originalBaseURL
	})
}

// execArgsForUser decodes the exec credential-plugin args of a managed user
// entry from a loaded kubeconfig.
func execArgsForUser(t *testing.T, config *kubeconfig.Config, name string) []string {
	t.Helper()
	for _, user := range config.Users {
		if user.Name != name {
			continue
		}
		var body struct {
			Exec kubeconfig.ExecConfig `yaml:"exec"`
		}
		if err := user.User.Decode(&body); err != nil {
			t.Fatalf("decode user %q: %v", name, err)
		}
		return body.Exec.Args
	}
	t.Fatalf("user %q not found in kubeconfig", name)
	return nil
}

func resetKubeconfigFlags(path string) {
	kubeconfigClusterFlag = ""
	kubeconfigAllFlag = false
	kubeconfigEmbedToken = false
	kubeconfigNamespace = ""
	kubeconfigUse = false
	kubeconfigPrint = false
	kubeconfigPathFlag = path
	kubeconfigInsecure = false
	kubeconfigExecCommand = "ankra"
}

func TestKubeconfigAddExecMode(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo", OrganisationID: "org-1"}})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"
	kubeconfigNamespace = "team-a"

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}

	config, err := kubeconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	names := config.ManagedContextNames()
	if len(names) != 1 || names[0] != "ankra-demo" {
		t.Fatalf("managed contexts = %v", names)
	}
	if got := config.ClusterServer("ankra-demo"); got != "https://api.platform.ankra.dev/api/v1/clusters/id-1/k8s" {
		t.Errorf("server = %q (must come from the backend, not the CLI base URL)", got)
	}
	rendered, _ := kubeconfig.Marshal(config)
	body := string(rendered)
	for _, fragment := range []string{
		"command: ankra",
		"client.authentication.k8s.io/v1",
		"--cluster",
		"id-1",
		"namespace: team-a",
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("rendered kubeconfig missing %q\n%s", fragment, body)
		}
	}
	// The exec args must pin the cluster's owning organisation so kube-token
	// keeps working when the selected organisation later differs.
	want := "cluster kube-token --cluster id-1 --org org-1"
	if got := strings.Join(execArgsForUser(t, config, "ankra-demo"), " "); got != want {
		t.Errorf("exec args = %q, want %q", got, want)
	}
	if config.CurrentContext != "" {
		t.Errorf("current-context should be unset without --use, got %q", config.CurrentContext)
	}
}

func TestKubeconfigAddUseSetsCurrentContext(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo"}})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"
	kubeconfigUse = true

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	if config.CurrentContext != "ankra-demo" {
		t.Errorf("current-context = %q", config.CurrentContext)
	}
}

func TestKubeconfigAddEmbedToken(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{
		cluster: client.ClusterListItem{ID: "id-1", Name: "demo"},
		token:   "tok-123",
	})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"
	kubeconfigEmbedToken = true

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	rendered, _ := kubeconfig.Marshal(config)
	body := string(rendered)
	if !strings.Contains(body, "token: tok-123") {
		t.Errorf("expected embedded token, got\n%s", body)
	}
	if strings.Contains(body, "command: ankra") {
		t.Errorf("embed-token mode should not write an exec stanza\n%s", body)
	}
}

func TestKubeconfigAddPrintDoesNotWriteFile(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo"}})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"
	kubeconfigPrint = true

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ankra-demo") {
		t.Errorf("stdout missing context\n%s", out.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("--print must not create %s", path)
	}
}

func TestKubeconfigAddAll(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{clusters: []client.ClusterListItem{
		{ID: "id-1", Name: "demo", OrganisationID: "org-1"},
		{ID: "id-2", Name: "staging", OrganisationID: "org-2"},
	}})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigAllFlag = true

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	if len(config.ManagedContextNames()) != 2 {
		t.Fatalf("expected 2 managed contexts, got %v", config.ManagedContextNames())
	}
	// Each entry pins the organisation of its own cluster, not a shared one.
	for _, expectation := range []struct{ user, args string }{
		{"ankra-demo", "cluster kube-token --cluster id-1 --org org-1"},
		{"ankra-staging", "cluster kube-token --cluster id-2 --org org-2"},
	} {
		if got := strings.Join(execArgsForUser(t, config, expectation.user), " "); got != expectation.args {
			t.Errorf("%s exec args = %q, want %q", expectation.user, got, expectation.args)
		}
	}
}

func TestKubeconfigRemove(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo"}})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}

	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"
	out.Reset()
	if err := kubeconfigRemove(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	if len(config.ManagedContextNames()) != 0 {
		t.Fatalf("expected no managed contexts after remove, got %v", config.ManagedContextNames())
	}
	if !strings.Contains(out.String(), "Removed context") {
		t.Errorf("unexpected remove output: %s", out.String())
	}
}

func TestKubeconfigList(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo"}})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"
	kubeconfigUse = true

	var addOut bytes.Buffer
	if err := kubeconfigAdd(&addOut); err != nil {
		t.Fatal(err)
	}

	resetKubeconfigFlags(path)
	var out bytes.Buffer
	if err := kubeconfigList(&out); err != nil {
		t.Fatal(err)
	}
	listing := out.String()
	if !strings.Contains(listing, "ankra-demo") {
		t.Errorf("list missing context:\n%s", listing)
	}
	if !strings.Contains(listing, "https://api.platform.ankra.dev/api/v1/clusters/id-1/k8s") {
		t.Errorf("list missing server:\n%s", listing)
	}
	if !strings.Contains(listing, "*") {
		t.Errorf("active marker missing for current-context:\n%s", listing)
	}
}

func TestKubeconfigListEmpty(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	var out bytes.Buffer
	if err := kubeconfigList(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No Ankra-managed contexts") {
		t.Errorf("unexpected empty list output: %s", out.String())
	}
}

func TestResolveContextNames(t *testing.T) {
	noCollision := resolveContextNames([]kubeTarget{
		{id: "id-1", name: "demo"},
		{id: "id-2", name: "staging"},
	})
	if noCollision[0] != "ankra-demo" || noCollision[1] != "ankra-staging" {
		t.Fatalf("no-collision names = %v", noCollision)
	}

	collision := resolveContextNames([]kubeTarget{
		{id: "id-1", name: "demo"},
		{id: "id-2", name: "Demo"},
	})
	if collision[0] != "ankra-demo" {
		t.Errorf("first name = %q, want ankra-demo", collision[0])
	}
	if collision[1] == collision[0] {
		t.Errorf("collision not disambiguated: both %q", collision[1])
	}
	if !strings.HasPrefix(collision[1], "ankra-demo-") {
		t.Errorf("disambiguated name = %q, want ankra-demo-<id> prefix", collision[1])
	}
}

func TestKubeconfigAddAllDisambiguatesCollision(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{clusters: []client.ClusterListItem{
		{ID: "id-1", Name: "demo"},
		{ID: "id-2", Name: "Demo"},
	}})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigAllFlag = true

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	if len(config.ManagedContextNames()) != 2 {
		t.Fatalf("collision should yield 2 distinct contexts, got %v", config.ManagedContextNames())
	}
}

func TestKubeconfigAddInsecure(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo"}})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"
	kubeconfigInsecure = true

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	rendered, _ := kubeconfig.Marshal(config)
	if !strings.Contains(string(rendered), "insecure-skip-tls-verify: true") {
		t.Errorf("expected insecure-skip-tls-verify, got\n%s", rendered)
	}
}

func TestKubeconfigAddExecCommandOverride(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo"}})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"
	kubeconfigExecCommand = "/opt/ankra/bin/ankra"

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	rendered, _ := kubeconfig.Marshal(config)
	if !strings.Contains(string(rendered), "command: /opt/ankra/bin/ankra") {
		t.Errorf("expected custom exec command, got\n%s", rendered)
	}
}

func TestKubeconfigAddTreatsUUIDFlagAsID(t *testing.T) {
	// Empty mock cluster => GetCluster never matches => a UUID flag is accepted as an ID.
	withKubeconfigMock(t, kubeconfigMock{})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	const clusterUUID = "11111111-1111-1111-1111-111111111111"
	kubeconfigClusterFlag = clusterUUID

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	names := config.ManagedContextNames()
	wantContext := "ankra-" + clusterUUID
	if len(names) != 1 || names[0] != wantContext {
		t.Fatalf("context names = %v", names)
	}
	if got := config.ClusterServer(wantContext); got != "https://api.platform.ankra.dev/api/v1/clusters/"+clusterUUID+"/k8s" {
		t.Errorf("server = %q", got)
	}
	// With no cluster lookup, no override, and no selected organisation the
	// add still succeeds; the exec args just omit --org.
	want := "cluster kube-token --cluster " + clusterUUID
	if got := strings.Join(execArgsForUser(t, config, wantContext), " "); got != want {
		t.Errorf("exec args = %q, want %q", got, want)
	}
}

func TestKubeconfigAddUUIDFallbackEmbedsSelectedOrg(t *testing.T) {
	// A raw cluster-ID passthrough carries no organisation from the backend;
	// the exec args fall back to the persistently selected organisation.
	withKubeconfigMock(t, kubeconfigMock{})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ankra"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection := []byte(`{"organisation_id":"org-selected","name":"Selected","role":"admin"}`)
	if err := os.WriteFile(filepath.Join(home, ".ankra", "organisation.json"), selection, 0o644); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	const clusterUUID = "11111111-1111-1111-1111-111111111111"
	kubeconfigClusterFlag = clusterUUID

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	want := "cluster kube-token --cluster " + clusterUUID + " --org org-selected"
	if got := strings.Join(execArgsForUser(t, config, "ankra-"+clusterUUID), " "); got != want {
		t.Errorf("exec args = %q, want %q", got, want)
	}
}

func TestKubeconfigAddUUIDFallbackEmbedsOrgOverride(t *testing.T) {
	// The resolved --org/ANKRA_ORG override wins over the selected
	// organisation for targets whose owning organisation is unknown.
	withKubeconfigMock(t, kubeconfigMock{orgOverride: "org-override"})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ankra"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection := []byte(`{"organisation_id":"org-selected","name":"Selected","role":"admin"}`)
	if err := os.WriteFile(filepath.Join(home, ".ankra", "organisation.json"), selection, 0o644); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	const clusterUUID = "11111111-1111-1111-1111-111111111111"
	kubeconfigClusterFlag = clusterUUID

	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, _ := kubeconfig.Load(path)
	want := "cluster kube-token --cluster " + clusterUUID + " --org org-override"
	if got := strings.Join(execArgsForUser(t, config, "ankra-"+clusterUUID), " "); got != want {
		t.Errorf("exec args = %q, want %q", got, want)
	}
}

func TestKubeconfigAddUnknownNameReturnsError(t *testing.T) {
	// A value that is neither a known name nor a UUID must fail clearly rather
	// than being forwarded to the backend as a bogus cluster_id.
	withKubeconfigMock(t, kubeconfigMock{})
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "ankra-hetzner-kube"

	var out bytes.Buffer
	err := kubeconfigAdd(&out)
	if err == nil {
		t.Fatal("expected an error for an unknown non-UUID cluster value")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found'", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("no kubeconfig should be written on failure: %s", path)
	}
}

func TestResolveKubeTokenClusterID(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo"}})

	if id, err := resolveKubeTokenClusterID("demo"); err != nil || id != "id-1" {
		t.Fatalf("name resolves to id: got %q err=%v", id, err)
	}

	const clusterUUID = "11111111-1111-1111-1111-111111111111"
	if id, err := resolveKubeTokenClusterID(clusterUUID); err != nil || id != clusterUUID {
		t.Fatalf("uuid passes through: got %q err=%v", id, err)
	}

	// The exact failure case: passing the kubeconfig context name.
	if _, err := resolveKubeTokenClusterID("ankra-hetzner-kube"); err == nil {
		t.Fatal("expected error for unknown non-uuid value")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found'", err)
	}
}

func TestResolveKubeconfigPath(t *testing.T) {
	if got, err := resolveKubeconfigPath("/explicit/config"); err != nil || got != "/explicit/config" {
		t.Fatalf("explicit flag path = %q, err=%v", got, err)
	}

	t.Setenv("KUBECONFIG", strings.Join([]string{"/first/config", "/second/config"}, string(filepath.ListSeparator)))
	if got, err := resolveKubeconfigPath(""); err != nil || got != "/first/config" {
		t.Fatalf("KUBECONFIG first entry = %q, err=%v", got, err)
	}

	home := t.TempDir()
	t.Setenv("KUBECONFIG", "")
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".kube", "config")
	if got, err := resolveKubeconfigPath(""); err != nil || got != want {
		t.Fatalf("default path = %q, want %q, err=%v", got, want, err)
	}
}
