package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const (
	portalTestOrganisationID = "0c0ffee0-0000-4000-8000-000000000001"
	portalTestClusterPrefix  = "/organisation/clusters/cluster/imported/" + testClusterID
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

func TestPortalURLBuilders(t *testing.T) {
	originalBaseURL := baseURL
	t.Cleanup(func() { baseURL = originalBaseURL })
	baseURL = "https://portal.example.test/"

	cluster := client.ClusterListItem{ID: "cluster-1", OrganisationID: "organisation-1"}
	cases := map[string]struct{ got, want string }{
		"cluster": {
			portalClusterURL(cluster),
			"https://portal.example.test/organisation/clusters/cluster/imported/cluster-1/overview?org=organisation-1",
		},
		"stack": {
			portalStackURL(cluster, "platform-secrets"),
			"https://portal.example.test/organisation/clusters/cluster/imported/cluster-1/stacks/platform-secrets/edit?org=organisation-1",
		},
		"manifest in a stack": {
			portalStackMemberURL(cluster, "platform-secrets", portalStackMemberManifest, "hub-secrets"),
			"https://portal.example.test/organisation/clusters/cluster/imported/cluster-1/stacks/platform-secrets/edit" +
				"?activeSubTab=configuration&activeTab=manifest%3Ahub-secrets&org=organisation-1",
		},
		"add-on in a stack": {
			portalStackMemberURL(cluster, "monitoring", portalStackMemberAddon, "grafana"),
			"https://portal.example.test/organisation/clusters/cluster/imported/cluster-1/stacks/monitoring/edit" +
				"?activeSubTab=configuration&activeTab=addon%3Agrafana&org=organisation-1",
		},
		"add-on page": {
			portalAddonURL(cluster, "grafana"),
			"https://portal.example.test/organisation/clusters/cluster/imported/cluster-1/add-ons/add-on/grafana?org=organisation-1",
		},
		"name that needs escaping": {
			portalStackURL(cluster, "my stack/one"),
			"https://portal.example.test/organisation/clusters/cluster/imported/cluster-1/stacks/my%20stack%2Fone/edit?org=organisation-1",
		},
		"no organisation leaves the marker off": {
			portalClusterURL(client.ClusterListItem{ID: "cluster-1"}),
			"https://portal.example.test/organisation/clusters/cluster/imported/cluster-1/overview",
		},
		"no cluster id gives no link":    {portalClusterURL(client.ClusterListItem{}), ""},
		"no stack name gives no link":    {portalStackURL(cluster, ""), ""},
		"no member name gives no link":   {portalStackMemberURL(cluster, "monitoring", portalStackMemberAddon, ""), ""},
		"no add-on name gives no link":   {portalAddonURL(cluster, ""), ""},
		"member needs a cluster id too":  {portalStackMemberURL(client.ClusterListItem{}, "monitoring", portalStackMemberAddon, "grafana"), ""},
		"add-on page needs a cluster id": {portalAddonURL(client.ClusterListItem{}, "grafana"), ""},
	}
	for name, testCase := range cases {
		if testCase.got != testCase.want {
			t.Errorf("%s:\n got  %s\n want %s", name, testCase.got, testCase.want)
		}
	}

	baseURL = ""
	if got := portalClusterURL(client.ClusterListItem{ID: "cluster-1"}); !strings.HasPrefix(got, defaultBaseURL+"/organisation/") {
		t.Errorf("an unset base URL should fall back to %s, got %s", defaultBaseURL, got)
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
	if want := portalTestClusterPrefix + "/overview?org=" + portalTestOrganisationID; portalURLSuffix(t, link) != want {
		t.Errorf("portal_url = %s, want suffix %s", link, want)
	}
}

func TestClusterInfoPrintsPortalLine(t *testing.T) {
	setMockClient(t, &clusterGetMock{cluster: client.ClusterListItem{
		ID: testClusterID, Name: "production", OrganisationID: portalTestOrganisationID,
	}})

	output := runPortalLinkCommand(t, clusterInfoCmd, "cluster", "info", "production")

	if want := "Portal: " + portalBaseURL() + portalTestClusterPrefix + "/overview?org=" + portalTestOrganisationID; !strings.Contains(output, want) {
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
	editPath := portalTestClusterPrefix + "/stacks/platform-secrets/edit"
	organisation := "org=" + portalTestOrganisationID
	for name, pair := range map[string][2]string{
		"stack":    {stacks[0].PortalURL, editPath + "?" + organisation},
		"manifest": {stacks[0].Manifests[0].PortalURL, editPath + "?activeSubTab=configuration&activeTab=manifest%3Ahub-secrets&" + organisation},
		"add-on":   {stacks[0].Addons[0].PortalURL, editPath + "?activeSubTab=configuration&activeTab=addon%3Aexternal-secrets&" + organisation},
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

	if want := portalTestClusterPrefix + "/stacks/platform-secrets/edit?org=" + portalTestOrganisationID; !strings.Contains(output, "Portal:") || !strings.Contains(output, want) {
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
	want := portalTestClusterPrefix + "/stacks/platform-secrets/edit?activeSubTab=configuration&activeTab=manifest%3Ahub-secrets&org=" + portalTestOrganisationID
	if got := portalURLSuffix(t, link); got != want {
		t.Errorf("portal_url suffix = %s, want %s", got, want)
	}
	if _, hasLink := manifests[1]["portal_url"]; hasLink {
		t.Errorf("a manifest outside any stack has no page, but got portal_url %v", manifests[1]["portal_url"])
	}
}

func TestClusterAddonsListYAMLCarriesPortalURL(t *testing.T) {
	writeSelectedClusterWithOrganisation(t)
	setMockClient(t, &clusterAddonsListMock{addons: []client.ClusterAddonListItem{{Name: "grafana", ChartName: "grafana"}}})

	output := runPortalLinkCommand(t, clusterAddonsListCmd, "cluster", "addons", "list", "-o", "yaml")

	want := "portal_url: " + portalBaseURL() + portalTestClusterPrefix + "/add-ons/add-on/grafana?org=" + portalTestOrganisationID
	if !strings.Contains(output, want) {
		t.Errorf("expected %q in the YAML output, got:\n%s", want, output)
	}
}
