package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// k8sResourcesMock returns two same-named resources with no formatRow-friendly
// shape. Under the old code, `-o wide` on a named lookup fell through to the
// table renderer and panicked on the named-pod path's nil formatRow; the up-front
// output-format validation must reject the value before any API call.
type k8sResourcesMock struct {
	baseMock
	called bool
}

func (m *k8sResourcesMock) GetResources(clusterID string, req client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	m.called = true
	return &client.GetResourcesResponse{
		ResourceResponses: []client.ResourceResponseItem{
			{Status: "ok", Kind: "Pod", Version: "v1", Items: []interface{}{
				map[string]interface{}{"metadata": map[string]interface{}{"name": "web-1"}},
				map[string]interface{}{"metadata": map[string]interface{}{"name": "web-2"}},
			}},
		},
	}, nil
}

func (m *k8sResourcesMock) ListPods(clusterID string, opts *client.ListPodsOptions) (*client.ListPodsResponse, error) {
	m.called = true
	return &client.ListPodsResponse{}, nil
}

func TestValidateK8sOutputFormat(t *testing.T) {
	for _, ok := range []string{"table", "json", "yaml"} {
		if err := validateK8sOutputFormat(ok); err != nil {
			t.Errorf("validateK8sOutputFormat(%q) = %v, want nil", ok, err)
		}
	}
	err := validateK8sOutputFormat("wide")
	if err == nil {
		t.Fatal("expected an error for -o wide")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("-o wide should classify as exitUsage (%d), got %d", exitUsage, got)
	}
}

// TestGetPodsNamedWideExitsUsageNoPanic drives `cluster get pods <name> -o wide`
// end-to-end. It must exit usage (2) and must not panic; the mock records
// whether the API was reached so we can confirm the value is rejected up front.
func TestGetPodsNamedWideExitsUsageNoPanic(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &k8sResourcesMock{}
	setMockClient(t, mock)

	_, err := executeCommand("cluster", "get", "pods", "web", "-o", "wide")
	if err == nil {
		t.Fatal("expected a usage error for -o wide on a named pod")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("-o wide on get pods <name> should exit %d, got %d", exitUsage, got)
	}
	if mock.called {
		t.Error("invalid output format should be rejected before any API call")
	}
}

// TestGetDeploymentsNamedWideExitsUsageNoPanic exercises the registerKindCommand
// family (deployments) on the same panic-prone named-lookup path.
func TestGetDeploymentsNamedWideExitsUsageNoPanic(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &k8sResourcesMock{}
	setMockClient(t, mock)

	_, err := executeCommand("cluster", "get", "deployments", "web", "-o", "wide")
	if err == nil {
		t.Fatal("expected a usage error for -o wide on a named deployment")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("-o wide on get deployments <name> should exit %d, got %d", exitUsage, got)
	}
	if mock.called {
		t.Error("invalid output format should be rejected before any API call")
	}
}

// TestResourcesWideExitsUsage confirms the generic `cluster resources` command
// rejects an unsupported -o value up front with exitUsage, matching the
// get/pods family rather than silently rendering a table.
func TestResourcesWideExitsUsage(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &k8sResourcesMock{}
	setMockClient(t, mock)

	_, err := executeCommand("cluster", "resources", "PersistentVolumeClaim", "-o", "wide")
	if err == nil {
		t.Fatal("expected a usage error for -o wide on cluster resources")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("-o wide on cluster resources should exit %d, got %d", exitUsage, got)
	}
	if mock.called {
		t.Error("invalid output format should be rejected before any API call")
	}
}

// findClusterGetSubcommand resolves a `cluster get` subcommand by name so
// tests can reset its persisted flag values (the cobra tree is shared
// across the test binary).
func findClusterGetSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, subcommand := range clusterGetCmd.Commands() {
		if subcommand.Name() == name {
			return subcommand
		}
	}
	t.Fatalf("cluster get %s command not registered", name)
	return nil
}

// capturedResourcesMock records the resource request it received and serves
// a canned response.
type capturedResourcesMock struct {
	baseMock
	lastRequest client.GetResourcesRequest
	response    *client.GetResourcesResponse
}

func (m *capturedResourcesMock) GetResources(clusterID string, req client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	m.lastRequest = req
	return m.response, nil
}

func emptyResourcesResponse(kind string) *client.GetResourcesResponse {
	return &client.GetResourcesResponse{
		ResourceResponses: []client.ResourceResponseItem{
			{Status: "success", Kind: kind, Version: "v1", Items: []interface{}{}},
		},
	}
}

// TestGetDeploymentsEmptyJSONStaysParseable pins the reorder of the empty
// early-return: -o json must emit the structured envelope even when the
// listing is empty, never the human "No deployments found." line.
func TestGetDeploymentsEmptyJSONStaysParseable(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &capturedResourcesMock{response: emptyResourcesResponse("Deployment")}
	setMockClient(t, mock)
	deploymentsCmd := findClusterGetSubcommand(t, "deployments")
	t.Cleanup(func() { _ = deploymentsCmd.Flags().Set("output", "table") })

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "get", "deployments", "-o", "json")
	})

	if strings.Contains(stdoutOutput, "No deployments found.") {
		t.Errorf("human empty message leaked into -o json output: %s", stdoutOutput)
	}
	var decoded client.GetResourcesResponse
	if err := json.Unmarshal([]byte(stdoutOutput), &decoded); err != nil {
		t.Fatalf("expected parseable JSON for an empty listing, got error %v for output: %s", err, stdoutOutput)
	}
	if len(decoded.ResourceResponses) != 1 {
		t.Errorf("expected the full envelope, got %+v", decoded)
	}
}

// podsPagedMock serves the pod listing across a fixed number of pages.
type podsPagedMock struct {
	baseMock
	pages      map[int][]client.PodSummary
	totalPages int
	totalCount int
}

func (m *podsPagedMock) ListPods(clusterID string, opts *client.ListPodsOptions) (*client.ListPodsResponse, error) {
	return &client.ListPodsResponse{
		Pods:       m.pages[opts.Page],
		TotalCount: m.totalCount,
		Page:       opts.Page,
		PageSize:   opts.PageSize,
		TotalPages: m.totalPages,
	}, nil
}

// TestGetPodsJSONAlwaysEmitsEnvelope pins the consistent -o json shape:
// the ListPodsResponse envelope with all pages merged, both when the
// listing fits one page and when the CLI had to walk several.
func TestGetPodsJSONAlwaysEmitsEnvelope(t *testing.T) {
	cases := []struct {
		name       string
		pages      map[int][]client.PodSummary
		totalPages int
		wantPods   int
	}{
		{
			name: "single_page",
			pages: map[int][]client.PodSummary{
				1: {{Name: "web-1", Phase: "Running"}, {Name: "web-2", Phase: "Running"}},
			},
			totalPages: 1,
			wantPods:   2,
		},
		{
			name: "multiple_pages",
			pages: map[int][]client.PodSummary{
				1: {{Name: "web-1", Phase: "Running"}, {Name: "web-2", Phase: "Running"}},
				2: {{Name: "web-3", Phase: "Pending"}},
			},
			totalPages: 2,
			wantPods:   3,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			writeSelectedClusterJSON(t)
			mock := &podsPagedMock{
				pages:      testCase.pages,
				totalPages: testCase.totalPages,
				totalCount: testCase.wantPods,
			}
			setMockClient(t, mock)
			t.Cleanup(func() { _ = clusterPodsCmd.Flags().Set("output", "table") })

			stdoutOutput := captureStdout(t, func() {
				_, _ = executeCommand("cluster", "get", "pods", "-o", "json")
			})

			if !strings.HasPrefix(strings.TrimSpace(stdoutOutput), "{") {
				t.Fatalf("expected a JSON object envelope, got: %s", stdoutOutput)
			}
			var decoded client.ListPodsResponse
			if err := json.Unmarshal([]byte(stdoutOutput), &decoded); err != nil {
				t.Fatalf("expected the ListPodsResponse envelope, got error %v for output: %s", err, stdoutOutput)
			}
			if len(decoded.Pods) != testCase.wantPods {
				t.Errorf("expected %d merged pods, got %d", testCase.wantPods, len(decoded.Pods))
			}
			if decoded.TotalCount != testCase.wantPods || decoded.Page != 1 || decoded.TotalPages != 1 {
				t.Errorf("expected pagination to describe the merged result, got %+v", decoded)
			}
		})
	}
}

// TestGetStorageClassesCommand covers the dedicated storageclasses entry:
// cluster-scoped, group storage.k8s.io, provisioner column rendered.
func TestGetStorageClassesCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &capturedResourcesMock{
		response: &client.GetResourcesResponse{
			ResourceResponses: []client.ResourceResponseItem{
				{Status: "success", Kind: "StorageClass", Version: "v1", Items: []interface{}{
					map[string]interface{}{
						"metadata":          map[string]interface{}{"name": "local-path"},
						"provisioner":       "rancher.io/local-path",
						"reclaimPolicy":     "Delete",
						"volumeBindingMode": "WaitForFirstConsumer",
					},
				}},
			},
		},
	}
	setMockClient(t, mock)
	storageClassesCmd := findClusterGetSubcommand(t, "storageclasses")
	if storageClassesCmd.Flags().Lookup("namespace") != nil {
		t.Error("storageclasses is cluster-scoped and must not register --namespace")
	}

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "get", "storageclasses")
	})

	if len(mock.lastRequest.ResourceRequests) != 1 {
		t.Fatalf("expected one resource request, got %+v", mock.lastRequest)
	}
	requested := mock.lastRequest.ResourceRequests[0]
	if requested.Kind != "StorageClass" || requested.Group != "storage.k8s.io" || requested.Version != "v1" {
		t.Errorf("expected StorageClass storage.k8s.io/v1 request, got %+v", requested)
	}
	for _, expected := range []string{"local-path", "rancher.io/local-path", "Delete", "WaitForFirstConsumer"} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected output to contain %q, got: %s", expected, stdoutOutput)
		}
	}
}

// TestResourcesGroupHint covers the empty-result guidance of `cluster get
// resources <Kind>`: hint when --group defaulted and the kind is outside
// core/v1, silence for core kinds and for an explicit --group.
func TestResourcesGroupHint(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantHint bool
	}{
		{name: "non_core_kind_without_group", args: []string{"cluster", "get", "resources", "StorageClass"}, wantHint: true},
		{name: "core_kind_without_group", args: []string{"cluster", "get", "resources", "ConfigMap"}, wantHint: false},
		{name: "non_core_kind_with_group", args: []string{"cluster", "get", "resources", "StorageClass", "--group", "storage.k8s.io"}, wantHint: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			writeSelectedClusterJSON(t)
			kind := testCase.args[3]
			mock := &capturedResourcesMock{response: emptyResourcesResponse(kind)}
			setMockClient(t, mock)
			t.Cleanup(func() {
				flags := clusterGenericResourcesCmd.Flags()
				_ = flags.Set("group", "")
				_ = flags.Set("output", "table")
			})

			stdoutOutput := captureStdout(t, func() {
				_, _ = executeCommand(testCase.args...)
			})

			if !strings.Contains(stdoutOutput, "No "+kind+" found.") {
				t.Errorf("expected the empty message for %s, got: %s", kind, stdoutOutput)
			}
			hasHint := strings.Contains(stdoutOutput, "pass --group")
			if hasHint != testCase.wantHint {
				t.Errorf("hint presence = %v, want %v; output: %s", hasHint, testCase.wantHint, stdoutOutput)
			}
			if testCase.wantHint && !strings.Contains(stdoutOutput, "storage.k8s.io") {
				t.Errorf("expected the hint to name an example group, got: %s", stdoutOutput)
			}
		})
	}
}

// formatRowFor returns the table row a kind's formatter renders for obj.
func formatRowFor(t *testing.T, commandName string, obj map[string]interface{}) []interface{} {
	t.Helper()
	for _, config := range kindConfigs {
		if config.commandName == commandName {
			if config.formatRow == nil {
				t.Fatalf("%s has no formatRow", commandName)
			}
			return config.formatRow(obj)
		}
	}
	t.Fatalf("no kind config named %s", commandName)
	return nil
}

// loadBalancerStatus builds a status.loadBalancer.ingress[] array from
// ip-or-hostname entries, matching the shape the API serves.
func loadBalancerStatus(entries ...map[string]interface{}) map[string]interface{} {
	ingress := make([]interface{}, 0, len(entries))
	for _, entry := range entries {
		ingress = append(ingress, entry)
	}
	return map[string]interface{}{
		"loadBalancer": map[string]interface{}{"ingress": ingress},
	}
}

// TestServiceExternalIPReadsLoadBalancerStatus pins the EXTERNAL-IP column to
// kubectl's rule. The old code read a spec.externalIP field that the Service
// API does not have, so a healthy LoadBalancer holding a public address
// printed '<none>' - which a customer reasonably read as "the load balancer
// was never provisioned".
func TestServiceExternalIPReadsLoadBalancerStatus(t *testing.T) {
	const externalIPCell = 4

	cases := []struct {
		name    string
		service map[string]interface{}
		want    string
	}{
		{
			name: "load_balancer_with_dual_stack_status",
			service: map[string]interface{}{
				"spec":   map[string]interface{}{"type": "LoadBalancer"},
				"status": loadBalancerStatus(map[string]interface{}{"ip": "129.212.253.107"}, map[string]interface{}{"ip": "2a03:b0c0:3:f0:0:2:b564:e000"}),
			},
			want: "129.212.253.107,2a03:b0c0:3:f0:0:2:b564:e000",
		},
		{
			name: "load_balancer_with_hostname",
			service: map[string]interface{}{
				"spec":   map[string]interface{}{"type": "LoadBalancer"},
				"status": loadBalancerStatus(map[string]interface{}{"hostname": "lb.example.com"}),
			},
			want: "lb.example.com",
		},
		{
			name: "load_balancer_awaiting_the_cloud_controller",
			service: map[string]interface{}{
				"spec":   map[string]interface{}{"type": "LoadBalancer"},
				"status": map[string]interface{}{"loadBalancer": map[string]interface{}{}},
			},
			want: "<pending>",
		},
		{
			name: "load_balancer_also_carrying_spec_external_ips",
			service: map[string]interface{}{
				"spec": map[string]interface{}{
					"type":        "LoadBalancer",
					"externalIPs": []interface{}{"203.0.113.9"},
				},
				"status": loadBalancerStatus(map[string]interface{}{"ip": "129.212.253.107"}),
			},
			want: "129.212.253.107,203.0.113.9",
		},
		{
			name: "cluster_ip_with_external_ips",
			service: map[string]interface{}{
				"spec": map[string]interface{}{
					"type":        "ClusterIP",
					"externalIPs": []interface{}{"203.0.113.9", "203.0.113.10"},
				},
			},
			want: "203.0.113.9,203.0.113.10",
		},
		{
			name:    "cluster_ip_without_external_ips",
			service: map[string]interface{}{"spec": map[string]interface{}{"type": "ClusterIP"}},
			want:    "<none>",
		},
		{
			name: "external_name",
			service: map[string]interface{}{
				"spec": map[string]interface{}{"type": "ExternalName", "externalName": "db.example.com"},
			},
			want: "db.example.com",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			row := formatRowFor(t, "services", testCase.service)
			if got := row[externalIPCell]; got != testCase.want {
				t.Errorf("EXTERNAL-IP = %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestIngressAddressAndPortsFromStatus covers the other half of the same
// defect: the ingress row hardcoded its ADDRESS and PORTS cells to empty
// strings, so every ingress looked unrouted no matter what the API served.
func TestIngressAddressAndPortsFromStatus(t *testing.T) {
	const (
		addressCell = 3
		portsCell   = 4
	)

	cases := []struct {
		name        string
		ingress     map[string]interface{}
		wantAddress string
		wantPorts   string
	}{
		{
			name: "published_ingress",
			ingress: map[string]interface{}{
				"spec":   map[string]interface{}{"rules": []interface{}{map[string]interface{}{"host": "demo.example.com"}}},
				"status": loadBalancerStatus(map[string]interface{}{"ip": "129.212.253.107"}),
			},
			wantAddress: "129.212.253.107",
			wantPorts:   "80",
		},
		{
			name: "tls_ingress_serves_443_too",
			ingress: map[string]interface{}{
				"spec": map[string]interface{}{
					"rules": []interface{}{map[string]interface{}{"host": "demo.example.com"}},
					"tls":   []interface{}{map[string]interface{}{"secretName": "demo-tls"}},
				},
				"status": loadBalancerStatus(map[string]interface{}{"ip": "129.212.253.107"}),
			},
			wantAddress: "129.212.253.107",
			wantPorts:   "80, 443",
		},
		{
			name: "ingress_with_no_address_yet",
			ingress: map[string]interface{}{
				"spec": map[string]interface{}{"rules": []interface{}{map[string]interface{}{"host": "demo.example.com"}}},
			},
			wantAddress: "",
			wantPorts:   "80",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			row := formatRowFor(t, "ingresses", testCase.ingress)
			if got := row[addressCell]; got != testCase.wantAddress {
				t.Errorf("ADDRESS = %v, want %v", got, testCase.wantAddress)
			}
			if got := row[portsCell]; got != testCase.wantPorts {
				t.Errorf("PORTS = %v, want %v", got, testCase.wantPorts)
			}
		})
	}
}
