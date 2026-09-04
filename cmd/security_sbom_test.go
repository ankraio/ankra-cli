package cmd

import (
	"os"
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
	imagesOptions     *client.SecuritySBOMImagesOptions
	containers        *client.SecuritySBOMContainerList
	containersOptions *client.SecuritySBOMContainersOptions
	export            *client.SecuritySBOMExport
	exportOptions     *client.SecuritySBOMExportOptions
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

func (m *securitySbomMock) ListSecuritySBOMImages(options client.SecuritySBOMImagesOptions) (*client.SecuritySBOMImageList, error) {
	m.imagesOptions = &options
	return m.images, nil
}

func (m *securitySbomMock) ListSecuritySBOMContainers(options client.SecuritySBOMContainersOptions) (*client.SecuritySBOMContainerList, error) {
	m.containersOptions = &options
	return m.containers, nil
}

func (m *securitySbomMock) ExportSecuritySBOMImage(options client.SecuritySBOMExportOptions) (*client.SecuritySBOMExport, error) {
	m.exportOptions = &options
	return m.export, nil
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

func TestSecuritySbomInventoriesAcceptTheWorkloadSelectorAndStructuredOutput(t *testing.T) {
	mock := &securitySbomMock{
		components: &client.SecuritySBOMComponentList{Result: []client.SecuritySBOMComponent{sbomComponent()},
			Pagination: client.SecurityPagination{Page: 1, PageSize: 50, TotalPages: 1, TotalCount: 1}, Coverage: sbomCoverage()},
		images: &client.SecuritySBOMImageList{Result: []client.SecuritySBOMImage{},
			Pagination: client.SecurityPagination{Page: 1, PageSize: 50}, Coverage: sbomCoverage()},
	}
	output, executeError := runSecurityCommand(t, mock, "security", "sbom",
		"--workload-kind", "Deployment", "--workload-name", "grafana", "-o", "json")
	if executeError != nil {
		t.Fatalf("sbom -o json returned an error: %v", executeError)
	}
	if mock.componentsOptions.WorkloadKind != "Deployment" || mock.componentsOptions.WorkloadName != "grafana" {
		t.Fatalf("workload selector not passed: %+v", mock.componentsOptions)
	}
	if !strings.Contains(output, "\"coverage\"") || !strings.Contains(output, "\"openssl\"") {
		t.Fatalf("-o json must render the full API document, got %s", output)
	}
	if _, executeError := runSecurityCommand(t, mock, "security", "sbom", "images",
		"--workload-name", "redis", "-o", "yaml"); executeError != nil {
		t.Fatalf("sbom images -o yaml returned an error: %v", executeError)
	}
	if mock.imagesOptions.WorkloadName != "redis" {
		t.Fatalf("image workload selector not passed: %+v", mock.imagesOptions)
	}
}

func TestSecuritySbomContainersListsAbsentRowsFirstAndTalliesTheScope(t *testing.T) {
	deployment, grafana, components := "Deployment", "grafana", 212
	os := "debian 12.7"
	identity := "sha256:abc"
	generated := "2026-09-03T21:39:44Z"
	mock := &securitySbomMock{containers: &client.SecuritySBOMContainerList{
		Result: []client.SecuritySBOMContainer{
			{ClusterName: "prod", Namespace: "smart-collector", PodName: "backend-6bbcfbf97f-x1", ContainerName: "backend",
				ContainerKind: "app", Image: "registry.test/backend:sha-8a7cd7d", SBOMStatus: "absent", LastSeenAt: generated},
			{ClusterName: "prod", Namespace: "security", PodName: "grafana-7d9f8-abcde", ContainerName: "migrate",
				ContainerKind: "init", Image: "registry.test/redis:1.0.0", SBOMStatus: "present", WorkloadKind: &deployment,
				WorkloadName: &grafana, ImageIdentity: &identity, ComponentCount: &components, OSName: &os,
				GeneratedAt: &generated, LastSeenAt: generated},
		},
		Pagination: client.SecurityPagination{Page: 1, PageSize: 50, TotalPages: 1, TotalCount: 2},
		Inventory:  client.SecuritySBOMContainerInventory{Containers: 113, WithSBOM: 84, WithoutSBOM: 29, Pods: 98},
		Coverage:   sbomCoverage(),
	}}
	output, executeError := runSecurityCommand(t, mock, "security", "sbom", "containers",
		"--namespace", "smart-collector", "--status", "any", "--workload-kind", "deployment")
	if executeError != nil {
		t.Fatalf("sbom containers returned an error: %v", executeError)
	}
	if mock.containersOptions.Namespace != "smart-collector" || mock.containersOptions.Status != "" ||
		mock.containersOptions.WorkloadKind != "deployment" {
		t.Fatalf("filters not passed (any must clear the status): %+v", mock.containersOptions)
	}
	for _, expected := range []string{
		"113 containers in 98 pods, 84 with a bill of materials", "29",
		"ABSENT", "(bare pod)", "migrate (init)", "Deployment grafana", "present", "212", "debian 12.7",
		"Page 1 of 1 · 2 containers",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q:\n%s", expected, output)
		}
	}
	if _, executeError := runSecurityCommand(t, mock, "security", "sbom", "containers", "--status", "nope"); executeError == nil {
		t.Fatalf("an unknown status must be refused")
	}
	if _, executeError := runSecurityCommand(t, mock, "security", "sbom", "containers", "--status", " ABSENT "); executeError != nil {
		t.Fatalf("any casing of a known status is accepted: %v", executeError)
	}
	if mock.containersOptions.Status != "absent" {
		t.Fatalf("the status reaches the platform in its canonical form, got %q", mock.containersOptions.Status)
	}
}

func TestSecuritySbomExportWritesTheDocumentToStdoutOrAFile(t *testing.T) {
	mock := &securitySbomMock{export: &client.SecuritySBOMExport{
		FileName: "registry.test-grafana_1.0.0.cdx.json", ContentType: "application/vnd.cyclonedx+json; version=1.5",
		Body: []byte("{\"bomFormat\":\"CycloneDX\"}\n"),
	}}
	output, executeError := runSecurityCommand(t, mock, "security", "sbom", "export", "sha256:abc", "--output-file", "-")
	if executeError != nil {
		t.Fatalf("sbom export returned an error: %v", executeError)
	}
	if mock.exportOptions.ImageIdentity != "sha256:abc" || mock.exportOptions.Format != "cyclonedx" {
		t.Fatalf("export options = %+v", mock.exportOptions)
	}
	if strings.TrimSpace(output) != "{\"bomFormat\":\"CycloneDX\"}" {
		t.Fatalf("stdout must carry the document verbatim, got %q", output)
	}
	target := t.TempDir() + "/bom.csv"
	mock.export = &client.SecuritySBOMExport{FileName: "x.csv", ContentType: "text/csv", Body: []byte("name,version\n")}
	output, executeError = runSecurityCommand(t, mock, "security", "sbom", "export", "sha256:abc", "--format", "csv", "--output-file", target)
	if executeError != nil {
		t.Fatalf("sbom export --format csv returned an error: %v", executeError)
	}
	if mock.exportOptions.Format != "csv" || !strings.Contains(output, "Wrote "+target) {
		t.Fatalf("csv export = %+v %q", mock.exportOptions, output)
	}
	written, readError := os.ReadFile(target)
	if readError != nil || string(written) != "name,version\n" {
		t.Fatalf("file not written: %v %q", readError, written)
	}
	if _, executeError := runSecurityCommand(t, mock, "security", "sbom", "export", "sha256:abc", "--format", "csv", "--output-file", target); executeError == nil {
		t.Fatalf("an existing target must be refused without --force")
	}
	if _, executeError := runSecurityCommand(t, mock, "security", "sbom", "export", "sha256:abc", "--format", "csv", "--output-file", target, "--force"); executeError != nil {
		t.Fatalf("--force must overwrite: %v", executeError)
	}
	if _, executeError := runSecurityCommand(t, mock, "security", "sbom", "export", "sha256:abc", "--format", "yaml", "--output-file", "-"); executeError == nil {
		t.Fatalf("an unknown format must be a usage error before any request is made")
	}
}

func TestSecuritySbomExportNeverTreatsTheServerFileNameAsAPath(t *testing.T) {
	for suggested, expected := range map[string]string{
		"registry.test-grafana_1.0.0.cdx.json": "registry.test-grafana_1.0.0.cdx.json",
		"../../etc/passwd":                     "passwd",
		"/tmp/evil.json":                       "evil.json",
		"":                                     "sbom.cdx.json",
		".":                                    "sbom.cdx.json",
		"..":                                   "sbom.cdx.json",
		".hidden":                              "sbom.cdx.json",
	} {
		if got := sbomExportLocalFileName(suggested, "cyclonedx"); got != expected {
			t.Fatalf("sbomExportLocalFileName(%q) = %q, want %q", suggested, got, expected)
		}
	}
	if got := sbomExportLocalFileName("", "csv"); got != "sbom.csv" {
		t.Fatalf("csv fallback = %q", got)
	}
	workDirectory := t.TempDir()
	previous, _ := os.Getwd()
	if changeError := os.Chdir(workDirectory); changeError != nil {
		t.Fatal(changeError)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	mock := &securitySbomMock{export: &client.SecuritySBOMExport{FileName: "../../escaped.json", ContentType: "application/json", Body: []byte("{}")}}
	output, executeError := runSecurityCommand(t, mock, "security", "sbom", "export", "sha256:abc", "--format", "cyclonedx", "--output-file", "")
	if executeError != nil {
		t.Fatalf("export returned an error: %v", executeError)
	}
	if !strings.Contains(output, "Wrote escaped.json") {
		t.Fatalf("the download must land in the current directory under the base name, got %q", output)
	}
	if _, statError := os.Stat(workDirectory + "/escaped.json"); statError != nil {
		t.Fatalf("file not written where announced: %v", statError)
	}
}
