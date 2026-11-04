package client

import (
	neturl "net/url"
	"testing"
)

func TestSecurityProjectScopeIsSentOnTheSecurityReads(t *testing.T) {
	testClient, platform := newRecordingClient(t, 200, `{}`)
	testClient.SetSecurityProjectScope(CostProjectScopeAll)
	const clusterID = "6f1c1f0e-0000-4000-8000-000000000001"
	reads := []struct {
		name string
		read func() error
		path string
	}{
		{"overview", func() error { _, err := testClient.GetSecurityOverview(SecurityOverviewOptions{}); return err }, "/api/v1/org/security/overview"},
		{"findings", func() error { _, err := testClient.ListSecurityFindings(SecurityFindingsOptions{}); return err }, "/api/v1/org/security/findings"},
		{"finding", func() error { _, err := testClient.GetSecurityFinding("f"); return err }, "/api/v1/org/security/findings/f"},
		{"occurrences", func() error {
			_, err := testClient.ListSecurityFindingOccurrences(SecurityOccurrencesOptions{FindingID: "f"})
			return err
		}, "/api/v1/org/security/findings/f/occurrences"},
		{"clusters", func() error { _, err := testClient.ListSecurityClusters(SecurityClustersOptions{}); return err }, "/api/v1/org/security/clusters"},
		{"access posture", func() error { _, err := testClient.GetSecurityClusterAccessPosture(clusterID); return err }, "/api/v1/org/security/clusters/" + clusterID + "/access-posture"},
		{"advisory", func() error { _, err := testClient.GetSecurityAdvisory("CVE-2026-1"); return err }, "/api/v1/org/security/advisories/CVE-2026-1"},
		{"workloads", func() error { _, err := testClient.ListSecurityWorkloads(SecurityFindingsOptions{}); return err }, "/api/v1/org/security/workloads"},
		{"dispositions", func() error { _, err := testClient.ListSecurityDispositions(SecurityDispositionsOptions{}); return err }, "/api/v1/org/security/vulnerability-dispositions"},
		{"namespaces", func() error { _, err := testClient.ListSecurityNamespaces(SecurityNamespacesOptions{}); return err }, "/api/v1/org/security/namespaces"},
		{"pods", func() error { _, err := testClient.ListSecurityPods(SecurityPodsOptions{}); return err }, "/api/v1/org/security/pods"},
		{"sbom", func() error {
			_, err := testClient.ListSecuritySBOMComponents(SecuritySBOMComponentsOptions{})
			return err
		}, "/api/v1/org/security/sbom"},
		{"sbom images", func() error { _, err := testClient.ListSecuritySBOMImages(SecuritySBOMImagesOptions{}); return err }, "/api/v1/org/security/sbom/images"},
		{"sbom containers", func() error {
			_, err := testClient.ListSecuritySBOMContainers(SecuritySBOMContainersOptions{})
			return err
		}, "/api/v1/org/security/sbom/containers"},
		{"stacks", func() error { _, err := testClient.ListClusterSecurityStacks(clusterID); return err }, "/api/v1/org/clusters/imported/" + clusterID + "/security/stacks"},
		{"stack", func() error { _, err := testClient.GetStackSecurity(clusterID, "web"); return err }, "/api/v1/org/clusters/imported/" + clusterID + "/stacks/web/security"},
		{"pod", func() error { _, err := testClient.GetPodSecurity(clusterID, "n", "p"); return err }, "/api/v1/org/clusters/imported/" + clusterID + "/security/pods/n/p"},
		{"benchmarks", func() error { _, err := testClient.GetSecurityClusterBenchmarks(clusterID); return err }, "/api/v1/org/clusters/imported/" + clusterID + "/security/compliance"},
		{"violations", func() error { _, err := testClient.GetSecurityClusterPolicyViolations(clusterID); return err }, "/api/v1/org/clusters/imported/" + clusterID + "/security/policy-violations"},
		{"network exposure", func() error { _, err := testClient.GetSecurityClusterNetworkExposure(clusterID); return err }, "/api/v1/org/clusters/imported/" + clusterID + "/security/network-policy-overprivilege"},
		{"addon", func() error { _, err := testClient.GetSecurityAddonPosture(clusterID, "cert-manager"); return err }, "/api/v1/org/clusters/imported/" + clusterID + "/addons/cert-manager/security"},
	}
	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			if readError := read.read(); readError != nil {
				t.Fatalf("%s: %v", read.name, readError)
			}
			request := platform.last(t)
			if request.path != read.path || !hasProjectAll(request.query) {
				t.Fatalf("request = %s?%s, want %s with project_id=all", request.path, request.query, read.path)
			}
		})
	}

	testClient.SetSecurityProjectScope("")
	if _, readError := testClient.GetSecurityOverview(SecurityOverviewOptions{}); readError != nil {
		t.Fatal(readError)
	}
	if query := platform.last(t).query; query != "" {
		t.Fatalf("query without a scope = %q, want none", query)
	}
}

func TestSecurityProjectScopeStaysOffTheOrganisationDocuments(t *testing.T) {
	testClient, platform := newRecordingClient(t, 200, `{}`)
	testClient.SetSecurityProjectScope(CostProjectScopeAll)
	reads := []func() error{
		func() error { _, err := testClient.GetSecurityComplianceOverview(); return err },
		func() error { _, err := testClient.ListSecurityComplianceFrameworks(); return err },
		func() error { _, err := testClient.GetSecurityComplianceFrameworkReport("soc2", ""); return err },
	}
	for _, read := range reads {
		_ = read()
		if request := platform.last(t); hasProjectAll(request.query) {
			t.Fatalf("%s sent %q: an organisation document has no project form", request.path, request.query)
		}
	}
}

func hasProjectAll(query string) bool {
	values, parseError := neturl.ParseQuery(query)
	return parseError == nil && values.Get("project_id") == CostProjectScopeAll
}
