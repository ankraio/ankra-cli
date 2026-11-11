package cmd

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const (
	portalTestOrganisationID = "0c0ffee0-0000-4000-8000-000000000001"
	portalTestClusterPrefix  = "/next/clusters/" + testClusterID
)

func writeSelectedClusterWithOrganisation(t *testing.T) {
	t.Helper()
	writeSelectedClusterJSON(t)
	if saveError := saveSelectedCluster(client.ClusterListItem{
		ID: testClusterID, Name: "test-cluster", OrganisationID: portalTestOrganisationID,
	}); saveError != nil {
		t.Fatalf("saving the selected cluster: %v", saveError)
	}
}

func runPortalLinkCommand(t *testing.T, command *cobra.Command, args ...string) string {
	t.Helper()
	resetCommandFlags(t, command)
	t.Cleanup(func() { resetCommandFlags(t, command) })
	var output string
	var runError error
	stdout := captureStdout(t, func() {
		output, runError = executeCommand(args...)
	})
	if runError != nil {
		t.Fatalf("%s: %v", strings.Join(args, " "), runError)
	}
	return output + stdout
}

func portalURLSuffix(t *testing.T, link string) string {
	t.Helper()
	base := portalBaseURL()
	if !strings.HasPrefix(link, base+"/") {
		t.Fatalf("link %q does not start with the portal base %q", link, base)
	}
	return strings.TrimPrefix(link, base)
}

// TestPortalURLBuilders pins every page the CLI links, one row per target,
// to its /next address. The Classic portal at the root (/organisation/...)
// is being retired (ankra-t5jf5.6.15); no row may produce a Classic path.
func TestPortalURLBuilders(t *testing.T) {
	originalBaseURL := baseURL
	t.Cleanup(func() { baseURL = originalBaseURL })
	baseURL = "https://portal.example.test/"

	cluster := client.ClusterListItem{ID: "cluster-1", OrganisationID: "organisation-1"}
	for _, testCase := range []struct {
		name string
		got  string
		want string
	}{
		{"cluster overview", portalClusterURL(cluster),
			"https://portal.example.test/next/clusters/cluster-1?org=organisation-1"},
		{"stack editor", portalStackURL(cluster, "platform-secrets"),
			"https://portal.example.test/next/clusters/cluster-1/stacks/platform-secrets?org=organisation-1"},
		{"manifest selected in its stack", portalStackMemberURL(cluster, "platform-secrets", portalStackMemberManifest, "hub-secrets"),
			"https://portal.example.test/next/clusters/cluster-1/stacks/platform-secrets?kind=manifest&member=hub-secrets&org=organisation-1"},
		{"add-on selected in its stack", portalStackMemberURL(cluster, "monitoring", portalStackMemberAddon, "grafana"),
			"https://portal.example.test/next/clusters/cluster-1/stacks/monitoring?kind=addon&member=grafana&org=organisation-1"},
		{"add-on page", portalAddonURL(cluster, "grafana", "monitoring"),
			"https://portal.example.test/next/clusters/cluster-1/addons/grafana?namespace=monitoring&org=organisation-1"},
		{"add-on page without a namespace", portalAddonURL(cluster, "grafana", ""),
			"https://portal.example.test/next/clusters/cluster-1/addons/grafana?org=organisation-1"},
		{"pod terminal", portalPodURL(cluster, "payments", "debug-api-7f3a", portalPodViewExec),
			"https://portal.example.test/next/clusters/cluster-1/kubernetes/pods/payments/debug-api-7f3a?org=organisation-1&view=exec"},
		{"pod overview", portalPodURL(cluster, "payments", "api-0", ""),
			"https://portal.example.test/next/clusters/cluster-1/kubernetes/pods/payments/api-0?org=organisation-1"},
		{"organisation billing", portalOrganisationPageURL("organisation-1", "organisation", "billing"),
			"https://portal.example.test/next/organisation/billing?org=organisation-1"},
		{"profile authentication", portalPageURL("account", "security"),
			"https://portal.example.test/next/account/security"},
		{"AI agent runs", portalPageURL("ai", "agent-runs"),
			"https://portal.example.test/next/ai/agent-runs"},
		{"name that needs escaping", portalStackURL(cluster, "my stack/one"),
			"https://portal.example.test/next/clusters/cluster-1/stacks/my%20stack%2Fone?org=organisation-1"},
		{"no organisation leaves the marker off", portalClusterURL(client.ClusterListItem{ID: "cluster-1"}),
			"https://portal.example.test/next/clusters/cluster-1"},
		{"no cluster id gives no link", portalClusterURL(client.ClusterListItem{}), ""},
		{"no stack name gives no link", portalStackURL(cluster, ""), ""},
		{"no member name gives no link", portalStackMemberURL(cluster, "monitoring", portalStackMemberAddon, ""), ""},
		{"no add-on name gives no link", portalAddonURL(cluster, "", "monitoring"), ""},
		{"member needs a cluster id too", portalStackMemberURL(client.ClusterListItem{}, "monitoring", portalStackMemberAddon, "grafana"), ""},
		{"add-on page needs a cluster id", portalAddonURL(client.ClusterListItem{}, "grafana", ""), ""},
		{"pod needs a cluster id", portalPodURL(client.ClusterListItem{}, "payments", "api-0", portalPodViewExec), ""},
		{"pod needs a namespace", portalPodURL(cluster, "", "api-0", portalPodViewExec), ""},
		{"pod needs a name", portalPodURL(cluster, "payments", "", portalPodViewExec), ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.got != testCase.want {
				t.Errorf("\n got  %s\n want %s", testCase.got, testCase.want)
			}
			if strings.Contains(testCase.got, "/organisation/clusters") || strings.Contains(testCase.got, "activeTab") {
				t.Errorf("a Classic path or parameter reached the link: %s", testCase.got)
			}
		})
	}
}

// portalProjectMock is an invocation scoped to a project (--project).
type portalProjectMock struct {
	baseMock
	projectID string
}

func (m *portalProjectMock) ProjectOverride() string { return m.projectID }

// The project this invocation acts in rides along on a project-scoped page
// and stays off an organisation-level one, where a ?project= for another
// project would stop on a switch offer before showing the page.
func TestPortalURLCarriesTheProjectOnlyOnProjectScopedPages(t *testing.T) {
	originalBaseURL := baseURL
	t.Cleanup(func() { baseURL = originalBaseURL })
	baseURL = "https://portal.example.test"
	setMockClient(t, &portalProjectMock{projectID: "project-1"})

	cluster := client.ClusterListItem{ID: "cluster-1", OrganisationID: "organisation-1"}
	if got, want := portalClusterURL(cluster), "https://portal.example.test/next/clusters/cluster-1?org=organisation-1&project=project-1"; got != want {
		t.Errorf("cluster link:\n got  %s\n want %s", got, want)
	}
	if got, want := portalOrganisationPageURL("organisation-1", "organisation", "billing"), "https://portal.example.test/next/organisation/billing?org=organisation-1"; got != want {
		t.Errorf("billing link:\n got  %s\n want %s", got, want)
	}
	if got, want := portalPageURL("account", "security"), "https://portal.example.test/next/account/security"; got != want {
		t.Errorf("account link:\n got  %s\n want %s", got, want)
	}
}

func TestPortalBaseURLIsSchemeAndHostOnly(t *testing.T) {
	originalBaseURL := baseURL
	t.Cleanup(func() { baseURL = originalBaseURL })

	for configured, want := range map[string]string{
		"https://portal.example.test":                "https://portal.example.test",
		"https://portal.example.test/":               "https://portal.example.test",
		"https://portal.example.test/api":            "https://portal.example.test",
		"https://portal.example.test/prefix/api/v1/": "https://portal.example.test",
		"http://localhost:8080/api?debug=1#fragment": "http://localhost:8080",
		" https://portal.example.test/api ":          "https://portal.example.test",
		"":                                           defaultBaseURL,
		"portal.example.test/api":                    defaultBaseURL,
		"://not a url":                               defaultBaseURL,
	} {
		baseURL = configured
		if got := portalBaseURL(); got != want {
			t.Errorf("portalBaseURL() with base %q = %q, want %q", configured, got, want)
		}
	}

	baseURL = "https://portal.example.test/prefix/api"
	want := "https://portal.example.test/next/clusters/cluster-1"
	if got := portalClusterURL(client.ClusterListItem{ID: "cluster-1"}); got != want {
		t.Errorf("a path on the API address leaked into the link:\n got  %s\n want %s", got, want)
	}
}

func TestPortalURLLeavesTheCallersQueryAlone(t *testing.T) {
	query := url.Values{"member": {"rules"}}

	link := portalURL("organisation-1", query, "clusters")

	if !strings.Contains(link, "org=organisation-1") {
		t.Fatalf("expected the organisation on the link, got %s", link)
	}
	if _, hasOrganisation := query["org"]; hasOrganisation || len(query) != 1 {
		t.Errorf("the caller's query was changed: %v", query)
	}
}

func TestPortalURLAnnotationKeepsALinkThePlatformSent(t *testing.T) {
	cluster := client.ClusterListItem{ID: "cluster-1"}
	clusters := []client.ClusterListItem{{ID: "cluster-1", PortalURL: "https://sent.example.test/cluster"}}
	annotateClusterPortalURLs(clusters)
	stacks := []client.ClusterStackListItem{{
		Name: "monitoring", PortalURL: "https://sent.example.test/stack",
		Manifests: []client.StackManifest{{Name: "rules", PortalURL: "https://sent.example.test/manifest"}},
		Addons:    []client.StackAddon{{Name: "grafana", PortalURL: "https://sent.example.test/addon"}},
	}}
	annotateStackPortalURLs(cluster, stacks)
	stackName := "monitoring"
	manifests := []client.ClusterManifestListItem{{Name: "rules", StackName: &stackName, PortalURL: "https://sent.example.test/manifest"}}
	annotateManifestPortalURLs(cluster, manifests)
	addons := []client.ClusterAddonListItem{{Name: "grafana", PortalURL: "https://sent.example.test/addon"}}
	annotateAddonPortalURLs(cluster, addons)

	for name, got := range map[string]string{
		"cluster":        clusters[0].PortalURL,
		"stack":          stacks[0].PortalURL,
		"stack manifest": stacks[0].Manifests[0].PortalURL,
		"stack add-on":   stacks[0].Addons[0].PortalURL,
		"manifest":       manifests[0].PortalURL,
		"add-on":         addons[0].PortalURL,
	} {
		if !strings.HasPrefix(got, "https://sent.example.test/") {
			t.Errorf("%s: the platform's link was replaced with %s", name, got)
		}
	}
}

func TestClusterListJSONCarriesPortalURL(t *testing.T) {
	setMockClient(t, &clusterListMock{clusters: []client.ClusterListItem{
		{ID: testClusterID, Name: "production", OrganisationID: portalTestOrganisationID},
	}})

	output := runPortalLinkCommand(t, clusterListCmd, "cluster", "list", "-o", "json")

	var clusters []map[string]any
	if decodeError := json.Unmarshal([]byte(output), &clusters); decodeError != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", decodeError, output)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected one cluster, got %d", len(clusters))
	}
	link, _ := clusters[0]["portal_url"].(string)
	if want := portalTestClusterPrefix + "?org=" + portalTestOrganisationID; portalURLSuffix(t, link) != want {
		t.Errorf("portal_url = %s, want suffix %s", link, want)
	}
}

func TestClusterInfoPrintsPortalLine(t *testing.T) {
	setMockClient(t, &clusterGetMock{cluster: client.ClusterListItem{
		ID: testClusterID, Name: "production", OrganisationID: portalTestOrganisationID,
	}})

	output := runPortalLinkCommand(t, clusterInfoCmd, "cluster", "info", "production")

	if want := "Portal: " + portalBaseURL() + portalTestClusterPrefix + "?org=" + portalTestOrganisationID; !strings.Contains(output, want) {
		t.Errorf("expected %q in the detail view, got:\n%s", want, output)
	}
}

func TestClusterStacksListJSONLinksStackAndMembers(t *testing.T) {
	writeSelectedClusterWithOrganisation(t)
	setMockClient(t, &clusterStacksListMock{stacks: []client.ClusterStackListItem{{
		Name:      "platform-secrets",
		State:     "up",
		Manifests: []client.StackManifest{{Name: "hub-secrets"}},
		Addons:    []client.StackAddon{{Name: "external-secrets"}},
	}}})

	output := runPortalLinkCommand(t, clusterStacksListCmd, "cluster", "stacks", "list", "-o", "json")

	var stacks []client.ClusterStackListItem
	if decodeError := json.Unmarshal([]byte(output), &stacks); decodeError != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", decodeError, output)
	}
	if len(stacks) != 1 || len(stacks[0].Manifests) != 1 || len(stacks[0].Addons) != 1 {
		t.Fatalf("unexpected shape: %s", output)
	}
	editPath := portalTestClusterPrefix + "/stacks/platform-secrets"
	organisation := "org=" + portalTestOrganisationID
	for name, pair := range map[string][2]string{
		"stack":    {stacks[0].PortalURL, editPath + "?" + organisation},
		"manifest": {stacks[0].Manifests[0].PortalURL, editPath + "?kind=manifest&member=hub-secrets&" + organisation},
		"add-on":   {stacks[0].Addons[0].PortalURL, editPath + "?kind=addon&member=external-secrets&" + organisation},
	} {
		if got := portalURLSuffix(t, pair[0]); got != pair[1] {
			t.Errorf("%s portal_url suffix = %s, want %s", name, got, pair[1])
		}
	}
}

func TestClusterStacksListDetailPrintsPortalLine(t *testing.T) {
	writeSelectedClusterWithOrganisation(t)
	setMockClient(t, &clusterStacksListMock{stacks: []client.ClusterStackListItem{{Name: "platform-secrets", State: "up"}}})

	output := runPortalLinkCommand(t, clusterStacksListCmd, "cluster", "stacks", "list", "platform-secrets")

	if want := portalTestClusterPrefix + "/stacks/platform-secrets?org=" + portalTestOrganisationID; !strings.Contains(output, "Portal:") || !strings.Contains(output, want) {
		t.Errorf("expected a Portal line ending %q, got:\n%s", want, output)
	}
}

func TestClusterManifestsListJSONLinksOnlyManifestsInAStack(t *testing.T) {
	writeSelectedClusterWithOrganisation(t)
	stackName := "platform-secrets"
	setMockClient(t, &clusterManifestsListMock{manifests: []client.ClusterManifestListItem{
		{Name: "hub-secrets", State: "up", StackName: &stackName},
		{Name: "standalone", State: "up"},
	}})

	output := runPortalLinkCommand(t, clusterManifestsListCmd, "cluster", "manifests", "list", "-o", "json")

	var manifests []map[string]any
	if decodeError := json.Unmarshal([]byte(output), &manifests); decodeError != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", decodeError, output)
	}
	if len(manifests) != 2 {
		t.Fatalf("expected two manifests, got %d", len(manifests))
	}
	link, _ := manifests[0]["portal_url"].(string)
	want := portalTestClusterPrefix + "/stacks/platform-secrets?kind=manifest&member=hub-secrets&org=" + portalTestOrganisationID
	if got := portalURLSuffix(t, link); got != want {
		t.Errorf("portal_url suffix = %s, want %s", got, want)
	}
	if _, hasLink := manifests[1]["portal_url"]; hasLink {
		t.Errorf("a manifest outside any stack has no page, but got portal_url %v", manifests[1]["portal_url"])
	}
}

func TestClusterAddonsListYAMLCarriesPortalURL(t *testing.T) {
	writeSelectedClusterWithOrganisation(t)
	setMockClient(t, &clusterAddonsListMock{addons: []client.ClusterAddonListItem{{Name: "grafana", ChartName: "grafana", Namespace: "monitoring"}}})

	output := runPortalLinkCommand(t, clusterAddonsListCmd, "cluster", "addons", "list", "-o", "yaml")

	want := "portal_url: " + portalBaseURL() + portalTestClusterPrefix + "/addons/grafana?namespace=monitoring&org=" + portalTestOrganisationID
	if !strings.Contains(output, want) {
		t.Errorf("expected %q in the YAML output, got:\n%s", want, output)
	}
}

// createLinkMock is a Hetzner create whose request went to organisationID
// (--org/ANKRA_ORG) and whose cluster record reports recordOrganisationID.
type createLinkMock struct {
	hetznerCreateMock
	organisationID       string
	recordOrganisationID string
}

func (m *createLinkMock) OrganisationOverride() string { return m.organisationID }

func (m *createLinkMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.recordOrganisationID == "" {
		return client.ClusterListItem{}, errors.New("not found")
	}
	return client.ClusterListItem{ID: clusterID, OrganisationID: m.recordOrganisationID}, nil
}

// The "View it in the UI" link a create prints was built by hand without
// ?org=, so it opened "not found" for a member whose portal was on another
// organisation (ankra-v3qdto.6). It names the organisation the request went
// to, else the one the platform reports for the new cluster.
func TestCreateLinkCarriesTheOrganisation(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mock *createLinkMock
		want string
	}{
		{name: "request organisation", mock: &createLinkMock{organisationID: portalTestOrganisationID},
			want: "/next/clusters/hz-cluster-123?org=" + portalTestOrganisationID},
		{name: "cluster record organisation", mock: &createLinkMock{recordOrganisationID: portalTestOrganisationID},
			want: "/next/clusters/hz-cluster-123?org=" + portalTestOrganisationID},
		{name: "organisation unknown", mock: &createLinkMock{},
			want: "/next/clusters/hz-cluster-123\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetTreeFlags(t, hetznerCreateCmd)
			var output string
			var runError error
			stdout := captureStdout(t, func() {
				output, runError = runConfirmCommand(t, testCase.mock, "", []*cobra.Command{hetznerCreateCmd},
					"cluster", "hetzner", "create", "--name", "linked", "--credential-id", "cred-1", "--location", "fsn1")
			})
			if runError != nil {
				t.Fatalf("create: %v\n%s", runError, output)
			}
			if combined := output + stdout; !strings.Contains(combined, testCase.want) {
				t.Errorf("create output lacks %q:\n%s", testCase.want, combined)
			}
		})
	}
}
