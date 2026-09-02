package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type securitySbomMock struct {
	baseMock
	namespaces        *client.SecurityNamespaceList
	pods              *client.SecurityPodList
	podsOptions       *client.SecurityPodsOptions
	components        *client.SecuritySBOMComponentList
	componentsOptions *client.SecuritySBOMComponentsOptions
	images            *client.SecuritySBOMImageList
	detail            *client.SecuritySBOMImageDetail
	detailOptions     *client.SecuritySBOMImageOptions
}

func (m *securitySbomMock) ListSecurityNamespaces(client.SecurityNamespacesOptions) (*client.SecurityNamespaceList, error) {
	return m.namespaces, nil
}

func (m *securitySbomMock) ListSecurityPods(options client.SecurityPodsOptions) (*client.SecurityPodList, error) {
	m.podsOptions = &options
	return m.pods, nil
}

func (m *securitySbomMock) ListSecuritySBOMComponents(options client.SecuritySBOMComponentsOptions) (*client.SecuritySBOMComponentList, error) {
	m.componentsOptions = &options
	return m.components, nil
}

func (m *securitySbomMock) ListSecuritySBOMImages(client.SecuritySBOMImagesOptions) (*client.SecuritySBOMImageList, error) {
	return m.images, nil
}

func (m *securitySbomMock) GetSecuritySBOMImage(options client.SecuritySBOMImageOptions) (*client.SecuritySBOMImageDetail, error) {
	m.detailOptions = &options
	return m.detail, nil
}

func sbomCoverage() client.SecuritySBOMCoverage {
	return client.SecuritySBOMCoverage{ScannedClusters: 3, ClustersWithSBOM: 1, Images: 12, Components: 1840, Workloads: 20}
}

func sbomComponent() client.SecuritySBOMComponent {
	purl := "pkg:deb/debian/openssl@3.0.14"
	return client.SecuritySBOMComponent{
		Name: "openssl", Version: "3.0.14", PackageType: "deb", ComponentType: "library", PURL: &purl,
		Licenses: []string{"Apache-2.0"}, Images: 4, Workloads: 7, Clusters: 2,
		VulnerableFindings: 2, ActionableFindings: 1, KnownExploited: 1,
	}
}

func TestSecurityNamespacesRendersClusterScopedRowsAndCounts(t *testing.T) {
	namespaces := &client.SecurityNamespaceList{
		Result: []client.SecurityNamespace{
			{ClusterName: "prod", Namespace: "backend", ReportScope: "namespaced", Workloads: 3, Images: 2, Pods: 5,
				ActionableTotal: 9, Actionable: client.SecuritySeverityCounts{Critical: 2, High: 4}, KnownExploited: 1,
				FixableSevere: 4, LastScan: "2026-08-28T20:25:14Z"},
			{ClusterName: "prod", Namespace: "", ReportScope: "cluster", Workloads: 1, Images: 1,
				ActionableTotal: 2, Actionable: client.SecuritySeverityCounts{High: 1, Medium: 1}, LastScan: "2026-08-28T20:25:14Z"},
		},
		Pagination: client.SecurityPagination{Page: 1, PageSize: 50, TotalPages: 1, TotalCount: 2},
	}
	output, executeError := runSecurityCommand(t, &securitySbomMock{namespaces: namespaces}, "security", "namespaces")
	if executeError != nil {
		t.Fatalf("security namespaces failed: %v", executeError)
	}
	for _, expected := range []string{"backend", "(cluster-scoped)", "KNOWN EXPLOITED", "Page 1 of 1 · 2 namespaces"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestSecurityPodsRequiresClusterAndNamespaceAndMapsTheWorkloadFilters(t *testing.T) {
	if _, executeError := runSecurityCommand(t, &securitySbomMock{}, "security", "pods", "--namespace", "backend"); executeError == nil {
		t.Fatal("expected an error without --cluster")
	}
	node, phase, kind, name := "worker-1", "Running", "deployment", "api"
	pods := &client.SecurityPodList{
		Result: []client.SecurityPod{{
			Name: "api-7d9f8-abcde", Namespace: "backend", Node: &node, Phase: &phase,
			WorkloadKind: &kind, WorkloadName: &name, Scanned: true, Observed: 3,
			Actionable: client.SecuritySeverityCounts{Critical: 1, High: 1}, KnownExploited: 1, Priority: "critical",
			Containers: []client.SecurityPodContainer{
				{Name: "api", Scanned: true, Ready: true, Actionable: client.SecuritySeverityCounts{Critical: 1, High: 1}, KnownExploited: 1},
				{Name: "sidecar", Scanned: false, Ready: false},
			},
		}},
		Pagination: client.SecurityPagination{Page: 1, PageSize: 50, TotalPages: 1, TotalCount: 1},
		Capped:     true,
	}
	mock := &securitySbomMock{pods: pods}
	output, executeError := runSecurityCommand(t, mock, "security", "pods",
		"--cluster", "00000000-0000-0000-0000-000000000001", "--namespace", "backend",
		"--workload-kind", "Deployment", "--workload-name", "api")
	if executeError != nil {
		t.Fatalf("security pods failed: %v", executeError)
	}
	if mock.podsOptions == nil || mock.podsOptions.Namespace != "backend" || mock.podsOptions.WorkloadKind != "Deployment" || mock.podsOptions.WorkloadName != "api" {
		t.Fatalf("pods options = %+v", mock.podsOptions)
	}
	for _, expected := range []string{"api-7d9f8-abcde", "deployment api", "sidecar: not scanned", "not ready", "capped", "Page 1 of 1 · 1 pods"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestSecuritySbomMapsFlagsAndRendersCoverage(t *testing.T) {
	mock := &securitySbomMock{components: &client.SecuritySBOMComponentList{
		Result:     []client.SecuritySBOMComponent{sbomComponent()},
		Pagination: client.SecurityPagination{Page: 1, PageSize: 50, TotalPages: 1, TotalCount: 1},
		Coverage:   sbomCoverage(),
	}}
	output, executeError := runSecurityCommand(t, mock, "security", "sbom",
		"--search", "openssl", "--type", "deb", "--type", "apk", "--vulnerable", "true", "--namespace", "backend", "--sort", "vulnerable")
	if executeError != nil {
		t.Fatalf("security sbom failed: %v", executeError)
	}
	options := mock.componentsOptions
	if options == nil || options.Search != "openssl" || len(options.PackageTypes) != 2 || options.Vulnerable == nil || !*options.Vulnerable ||
		options.Namespace != "backend" || options.Sort != "vulnerable" {
		t.Fatalf("component options = %+v", options)
	}
	for _, expected := range []string{"1 of 3 scanned clusters publish one", "trivy_sbom_generation_enabled", "openssl", "2 (1 actionable)", "KEV", "Page 1 of 1 · 1 components"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if _, executeError := runSecurityCommand(t, mock, "security", "sbom", "--vulnerable", "maybe"); executeError == nil {
		t.Fatal("expected --vulnerable maybe to be refused")
	}
}

func TestSecuritySbomImagesAndImageDetail(t *testing.T) {
	digest, osName := "sha256:abc", "debian 12.7"
	image := client.SecuritySBOMImage{
		ImageIdentity: digest, ImageRef: "registry.example.com/backend/api:1.0.0", ImageDigest: &digest, OSName: &osName,
		ComponentCount: 212, DependencyCount: 180, Workloads: 2, Clusters: 1, Namespaces: []string{"backend"},
		Observed: 5, Actionable: client.SecuritySeverityCounts{Critical: 1, High: 2}, KnownExploited: 1,
	}
	mock := &securitySbomMock{
		images: &client.SecuritySBOMImageList{
			Result:     []client.SecuritySBOMImage{image},
			Pagination: client.SecurityPagination{Page: 1, PageSize: 50, TotalPages: 1, TotalCount: 1},
			Coverage:   sbomCoverage(),
		},
		detail: &client.SecuritySBOMImageDetail{
			Image:      image,
			Components: []client.SecuritySBOMComponent{sbomComponent()},
			Pagination: client.SecurityPagination{Page: 1, PageSize: 100, TotalPages: 1, TotalCount: 1},
			Workloads:  []client.SecuritySBOMWorkload{},
		},
	}
	output, executeError := runSecurityCommand(t, mock, "security", "sbom", "images")
	if executeError != nil {
		t.Fatalf("security sbom images failed: %v", executeError)
	}
	for _, expected := range []string{"registry.example.com/backend/api:1.0.0", "debian 12.7", "backend", "Page 1 of 1 · 1 images"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("images output lacks %q:\n%s", expected, output)
		}
	}
	output, executeError = runSecurityCommand(t, mock, "security", "sbom", "image", digest, "--type", "deb")
	if executeError != nil {
		t.Fatalf("security sbom image failed: %v", executeError)
	}
	if mock.detailOptions == nil || mock.detailOptions.ImageIdentity != digest || len(mock.detailOptions.PackageTypes) != 1 {
		t.Fatalf("detail options = %+v", mock.detailOptions)
	}
	for _, expected := range []string{"Digest:      sha256:abc", "212 (180 dependencies)", "No workload currently runs this image", "openssl", "Page 1 of 1 · 1 components"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("detail output lacks %q:\n%s", expected, output)
		}
	}
}
