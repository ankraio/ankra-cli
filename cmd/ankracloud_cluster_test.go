package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type ankraCloudClusterMock struct {
	baseMock
	createRequests    []client.CreateAnkraCloudClusterRequest
	preflightRequests []client.CreateAnkraCloudClusterRequest
	preflightResult   client.AnkraCloudPreflightResult
	catalogCalls      []string
	catalogResult     client.AnkraCloudCatalogResult
	stopClusterID     string
	startScope        string
	deprovisionedID   string
}

func (mock *ankraCloudClusterMock) CreateAnkraCloudCluster(request client.CreateAnkraCloudClusterRequest) (*client.CreateAnkraCloudClusterResponse, error) {
	mock.createRequests = append(mock.createRequests, request)
	return &client.CreateAnkraCloudClusterResponse{ClusterID: testClusterID, Name: request.Name}, nil
}

func (mock *ankraCloudClusterMock) PreflightAnkraCloudCluster(request client.CreateAnkraCloudClusterRequest) (*client.AnkraCloudPreflightResult, error) {
	mock.preflightRequests = append(mock.preflightRequests, request)
	return &mock.preflightResult, nil
}

func (mock *ankraCloudClusterMock) StopAnkraCloudCluster(clusterID string, options client.StopClusterOptions) (*client.ProviderStopClusterResponse, error) {
	mock.stopClusterID = clusterID
	return &client.ProviderStopClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (mock *ankraCloudClusterMock) StartAnkraCloudCluster(clusterID string, options client.StartClusterOptions) (*client.ProviderStartClusterResult, error) {
	mock.startScope = options.Scope
	return &client.ProviderStartClusterResult{Scope: options.Scope, CreatedOperations: 3}, nil
}

func (mock *ankraCloudClusterMock) DeprovisionAnkraCloudCluster(clusterID string, options client.DeprovisionOptions) (*client.ProviderDeprovisionClusterResponse, error) {
	mock.deprovisionedID = clusterID
	return &client.ProviderDeprovisionClusterResponse{ClusterID: clusterID}, nil
}

func (mock *ankraCloudClusterMock) GetAnkraCloudWorkerCount(clusterID string) (*client.WorkerCountResult, error) {
	return &client.WorkerCountResult{WorkerCount: 3, Min: 0, Max: 100}, nil
}

func (mock *ankraCloudClusterMock) GetAnkraCloudK8sVersion(clusterID string) (*client.K8sVersionInfo, error) {
	version := "1.33.1"
	return &client.K8sVersionInfo{CurrentVersion: &version, Distribution: "k3s"}, nil
}

func (mock *ankraCloudClusterMock) recordCatalog(name string) (*client.AnkraCloudCatalogResult, error) {
	mock.catalogCalls = append(mock.catalogCalls, name)
	return &mock.catalogResult, nil
}

func (mock *ankraCloudClusterMock) ListAnkraCloudZones(credentialID string) (*client.AnkraCloudCatalogResult, error) {
	return mock.recordCatalog("zones:" + credentialID)
}

func (mock *ankraCloudClusterMock) ListAnkraCloudPlans(credentialID string) (*client.AnkraCloudCatalogResult, error) {
	return mock.recordCatalog("plans:" + credentialID)
}

func (mock *ankraCloudClusterMock) ListAnkraCloudClusterPlans(clusterID string) (*client.AnkraCloudCatalogResult, error) {
	return mock.recordCatalog("cluster-plans:" + clusterID)
}

func (mock *ankraCloudClusterMock) ListAnkraCloudTemplates(credentialID string) (*client.AnkraCloudCatalogResult, error) {
	return mock.recordCatalog("templates:" + credentialID)
}

func (mock *ankraCloudClusterMock) ListAnkraCloudNetworks(credentialID, zone string) (*client.AnkraCloudCatalogResult, error) {
	return mock.recordCatalog("networks:" + credentialID + ":" + zone)
}

func (mock *ankraCloudClusterMock) ListAnkraCloudPricing(credentialID string) (*client.AnkraCloudCatalogResult, error) {
	return mock.recordCatalog("pricing:" + credentialID)
}

func ankraCloudCatalogFixture() client.AnkraCloudCatalogResult {
	publicIPv4Cents := 300
	return client.AnkraCloudCatalogResult{
		Zones:                  []client.AnkraCloudZone{{Name: "fi-hel1", Region: "fi", DisplayName: "Helsinki", Country: "FI"}},
		Plans:                  []client.AnkraCloudPlan{{Name: "s-2", Family: "standard", Cores: 2, MemoryMebibytes: 4096, StorageGibibytes: 80, PriceMonthlyCents: 900, Available: true}, {Name: "s-64", Available: false}},
		Templates:              []client.AnkraCloudTemplate{{ID: "debian-13", DisplayName: "Debian 13", OperatingSystem: "debian", Version: "13"}},
		Networks:               []client.AnkraCloudNetwork{{ID: "network-1", Name: "shared", Zone: "fi-hel1", CIDR: "10.0.0.0/16"}},
		StoragePrices:          []client.AnkraCloudStoragePrice{{Tier: "ssd", GBMonthCents: 10}},
		PublicIPv4MonthlyCents: &publicIPv4Cents,
		PricingComplete:        false,
		IncompleteReasons:      []string{"hourly prices"},
	}
}

var ankraCloudRequiredCreateArguments = []string{
	"--name", "prod", "--credential-id", "credential-1", "--ssh-key-credential-id", "ssh-key-1",
	"--zone", "fi-hel1", "--bastion-plan", "s-1", "--control-plane-plan", "s-4", "--worker-plan", "s-8",
}

func runAnkraCloudCommand(t *testing.T, mock APIClient, arguments ...string) (string, error) {
	t.Helper()
	var runError error
	var cobraOutput string
	standardOutput := captureStdout(t, func() {
		cobraOutput, runError = runConfirmCommand(t, mock, "", []*cobra.Command{
			ankraCloudCmd, ankraCloudCreateCmd, ankraCloudPreflightCmd, ankraCloudDeprovisionCmd,
			ankraCloudStopCmd, ankraCloudStartCmd, ankraCloudWorkersCmd, ankraCloudK8sVersionCmd,
			ankraCloudZonesCmd, ankraCloudPlansCmd, ankraCloudTemplatesCmd, ankraCloudNetworksCmd, ankraCloudPricingCmd,
		}, append([]string{"cluster", "ankracloud"}, arguments...)...)
	})
	return standardOutput + cobraOutput, runError
}

func TestAnkraCloudCreateDefaultsToKubeadm(t *testing.T) {
	mock := &ankraCloudClusterMock{}
	output, runError := runAnkraCloudCommand(t, mock, append([]string{"create"}, ankraCloudRequiredCreateArguments...)...)
	if runError != nil {
		t.Fatalf("create: %v\n%s", runError, output)
	}
	if len(mock.createRequests) != 1 {
		t.Fatalf("create calls = %d, want 1", len(mock.createRequests))
	}
	request := mock.createRequests[0]
	if request.Distribution != client.AnkraCloudDistributionKubeadm {
		t.Errorf("distribution = %q, want kubeadm", request.Distribution)
	}
	if request.Name != "prod" || request.Zone != "fi-hel1" || request.BastionPlan != "s-1" || request.ControlPlanePlan != "s-4" || request.WorkerPlan != "s-8" {
		t.Errorf("request = %+v, want the flag values", request)
	}
	if request.WorkerCount != nil || request.IncludeDNS != nil || request.GitopsBranch != nil {
		t.Errorf("request carries unset optional members: %+v", request)
	}
	if !strings.Contains(output, "created successfully") || !strings.Contains(output, testClusterID) {
		t.Errorf("output = %q, want the created cluster", output)
	}
}

func TestAnkraCloudCreateSendsK3sAndOptionalMembers(t *testing.T) {
	mock := &ankraCloudClusterMock{}
	arguments := append([]string{"create"}, ankraCloudRequiredCreateArguments...)
	arguments = append(arguments,
		"--distribution", "K3S", "--worker-count", "0", "--network-ip-range", "10.20.0.0/20",
		"--description", "edge", "--runtime-credential-id", "credential-2", "--kubernetes-version", "1.33.1",
		"--bastion-allowed-ips", "203.0.113.0/24", "--include-dns=false", "--include-networking=false",
		"--gitops-credential-name", "github", "--gitops-repository", "https://github.com/acme/gitops", "--gitops-branch", "main")
	if _, runError := runAnkraCloudCommand(t, mock, arguments...); runError != nil {
		t.Fatalf("create: %v", runError)
	}
	request := mock.createRequests[0]
	if request.Distribution != client.AnkraCloudDistributionK3s {
		t.Errorf("distribution = %q, want k3s", request.Distribution)
	}
	if request.WorkerCount == nil || *request.WorkerCount != 0 {
		t.Errorf("worker count = %v, want an explicit 0", request.WorkerCount)
	}
	if request.NetworkIPRange == nil || *request.NetworkIPRange != "10.20.0.0/20" {
		t.Errorf("network ip range = %v, want 10.20.0.0/20", request.NetworkIPRange)
	}
	if request.IncludeDNS == nil || *request.IncludeDNS || request.IncludeNetworking == nil || *request.IncludeNetworking {
		t.Errorf("include flags = %v/%v, want explicit false", request.IncludeDNS, request.IncludeNetworking)
	}
	if request.GitopsBranch == nil || *request.GitopsBranch != "main" || request.GitopsRepository == nil || request.GitopsCredentialName == nil {
		t.Errorf("gitops members = %+v, want repository, credential and branch", request)
	}
	if request.Description == nil || request.RuntimeCredentialID == nil || request.KubernetesVersion == nil {
		t.Errorf("optional members missing: %+v", request)
	}
	if len(request.BastionAllowedIPs) != 1 || request.BastionAllowedIPs[0] != "203.0.113.0/24" {
		t.Errorf("bastion allowed ips = %v, want [203.0.113.0/24]", request.BastionAllowedIPs)
	}
}

func TestAnkraCloudCreateRefusesUnknownDistribution(t *testing.T) {
	mock := &ankraCloudClusterMock{}
	_, runError := runAnkraCloudCommand(t, mock, append([]string{"create", "--distribution", "rke2"}, ankraCloudRequiredCreateArguments...)...)
	if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "kubeadm, k3s") {
		t.Fatalf("error = %v, want a usage error naming kubeadm, k3s", runError)
	}
	if len(mock.createRequests) != 0 {
		t.Errorf("create calls = %d, want 0", len(mock.createRequests))
	}
}

func TestAnkraCloudCreateRefusesNetworkAndRangeTogether(t *testing.T) {
	mock := &ankraCloudClusterMock{}
	arguments := append([]string{"create", "--private-network-id", "network-1", "--network-ip-range", "10.20.0.0/20"}, ankraCloudRequiredCreateArguments...)
	if _, runError := runAnkraCloudCommand(t, mock, arguments...); exitCodeFor(runError) != exitUsage {
		t.Fatalf("error = %v, want a usage error", runError)
	}
	if len(mock.createRequests) != 0 {
		t.Errorf("create calls = %d, want 0", len(mock.createRequests))
	}
}

func TestAnkraCloudCreateStructuredOutput(t *testing.T) {
	mock := &ankraCloudClusterMock{}
	output, runError := runAnkraCloudCommand(t, mock, append([]string{"create", "-o", "json"}, ankraCloudRequiredCreateArguments...)...)
	if runError != nil {
		t.Fatalf("create: %v", runError)
	}
	var decoded client.CreateAnkraCloudClusterResponse
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil || decoded.ClusterID != testClusterID {
		t.Fatalf("output %q is not the JSON response: %v", output, decodeError)
	}
}

func TestAnkraCloudPreflightPassesAndFails(t *testing.T) {
	passing := &ankraCloudClusterMock{preflightResult: client.AnkraCloudPreflightResult{
		CanProceed: true,
		Items:      []client.AnkraCloudPreflightItem{{Check: "zone", Status: "pass", Message: "fi-hel1"}},
	}}
	output, runError := runAnkraCloudCommand(t, passing, append([]string{"preflight"}, ankraCloudRequiredCreateArguments...)...)
	if runError != nil {
		t.Fatalf("passing preflight: %v", runError)
	}
	if !strings.Contains(output, "Preflight passed") || len(passing.preflightRequests) != 1 {
		t.Errorf("output = %q, want the passing verdict", output)
	}

	failing := &ankraCloudClusterMock{preflightResult: client.AnkraCloudPreflightResult{
		Items: []client.AnkraCloudPreflightItem{{Check: "plan", Status: "fail", Message: "s-4 unavailable"}},
	}}
	output, runError = runAnkraCloudCommand(t, failing, append([]string{"preflight"}, ankraCloudRequiredCreateArguments...)...)
	if runError == nil || !strings.Contains(output, "s-4 unavailable") {
		t.Fatalf("failing preflight: error %v, output %q; want an error and the failing check", runError, output)
	}
	if len(failing.createRequests) != 0 {
		t.Errorf("preflight created a cluster")
	}
}

func TestAnkraCloudLifecycleCommands(t *testing.T) {
	mock := &ankraCloudClusterMock{}
	output, runError := runAnkraCloudCommand(t, mock, "stop", testClusterID)
	if runError != nil || mock.stopClusterID != testClusterID || !strings.Contains(output, "Ankra Cloud cluster stop initiated") {
		t.Fatalf("stop: error %v, cluster %q, output %q", runError, mock.stopClusterID, output)
	}

	output, runError = runAnkraCloudCommand(t, mock, "start", testClusterID, "--scope", "control_plane")
	if runError != nil || mock.startScope != "control_plane" || !strings.Contains(output, "Ankra Cloud cluster start initiated") {
		t.Fatalf("start: error %v, scope %q, output %q", runError, mock.startScope, output)
	}

	if _, runError = runAnkraCloudCommand(t, mock, "start", testClusterID, "--scope", "workers"); exitCodeFor(runError) != exitUsage {
		t.Fatalf("start with a bad scope: error %v, want a usage error", runError)
	}

	output, runError = runAnkraCloudCommand(t, mock, "deprovision", testClusterID, "--yes")
	if runError != nil || mock.deprovisionedID != testClusterID || !strings.Contains(output, "deprovision initiated") {
		t.Fatalf("deprovision: error %v, cluster %q, output %q", runError, mock.deprovisionedID, output)
	}

	output, runError = runAnkraCloudCommand(t, mock, "workers", testClusterID)
	if runError != nil || !strings.Contains(output, "Worker Count: 3") {
		t.Fatalf("workers: error %v, output %q", runError, output)
	}

	output, runError = runAnkraCloudCommand(t, mock, "k8s-version", testClusterID)
	if runError != nil || !strings.Contains(output, "1.33.1") || !strings.Contains(output, "k3s") {
		t.Fatalf("k8s-version: error %v, output %q", runError, output)
	}
}

func TestAnkraCloudCatalogCommands(t *testing.T) {
	testCases := []struct {
		arguments        []string
		expectedCall     string
		expectedFragment string
	}{
		{[]string{"zones", "--credential-id", "credential-1"}, "zones:credential-1", "Helsinki"},
		{[]string{"plans", "--credential-id", "credential-1"}, "plans:credential-1", "9.00 EUR"},
		{[]string{"plans", "--cluster", testClusterID}, "cluster-plans:" + testClusterID, "s-64"},
		{[]string{"templates", "--credential-id", "credential-1"}, "templates:credential-1", "Debian 13"},
		{[]string{"networks", "--credential-id", "credential-1", "--zone", "fi-hel1"}, "networks:credential-1:fi-hel1", "10.0.0.0/16"},
		{[]string{"pricing", "--credential-id", "credential-1"}, "pricing:credential-1", "3.00 EUR"},
	}
	for _, testCase := range testCases {
		t.Run(strings.Join(testCase.arguments[:2], " "), func(t *testing.T) {
			mock := &ankraCloudClusterMock{catalogResult: ankraCloudCatalogFixture()}
			output, runError := runAnkraCloudCommand(t, mock, testCase.arguments...)
			if runError != nil {
				t.Fatalf("%v: %v", testCase.arguments, runError)
			}
			if len(mock.catalogCalls) != 1 || mock.catalogCalls[0] != testCase.expectedCall {
				t.Errorf("catalog calls = %v, want [%s]", mock.catalogCalls, testCase.expectedCall)
			}
			if !strings.Contains(output, testCase.expectedFragment) {
				t.Errorf("output = %q, want it to contain %q", output, testCase.expectedFragment)
			}
		})
	}
}

func TestAnkraCloudCatalogsReportEmptyResults(t *testing.T) {
	for _, catalog := range []string{"zones", "plans", "templates", "networks"} {
		t.Run(catalog, func(t *testing.T) {
			mock := &ankraCloudClusterMock{}
			output, runError := runAnkraCloudCommand(t, mock, catalog, "--credential-id", "credential-1")
			if runError != nil {
				t.Fatalf("%s: %v", catalog, runError)
			}
			if !strings.Contains(output, "No ") {
				t.Errorf("output = %q, want the empty-catalog message", output)
			}
		})
	}
}

func TestAnkraCloudCatalogStructuredOutput(t *testing.T) {
	mock := &ankraCloudClusterMock{catalogResult: ankraCloudCatalogFixture()}
	output, runError := runAnkraCloudCommand(t, mock, "zones", "--credential-id", "credential-1", "-o", "json")
	if runError != nil {
		t.Fatalf("zones: %v", runError)
	}
	var decoded client.AnkraCloudCatalogResult
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil || len(decoded.Zones) != 1 {
		t.Fatalf("output %q is not the JSON catalog: %v", output, decodeError)
	}
}

func TestAnkraCloudPlansNeedsExactlyOneSource(t *testing.T) {
	for _, arguments := range [][]string{
		{"plans"},
		{"plans", "--credential-id", "credential-1", "--cluster", testClusterID},
	} {
		mock := &ankraCloudClusterMock{}
		if _, runError := runAnkraCloudCommand(t, mock, arguments...); exitCodeFor(runError) != exitUsage {
			t.Errorf("%v: error %v, want a usage error", arguments, runError)
		}
		if len(mock.catalogCalls) != 0 {
			t.Errorf("%v: catalog calls = %v, want none", arguments, mock.catalogCalls)
		}
	}
}

type ankraCloudFailingMock struct {
	baseMock
}

func (mock *ankraCloudFailingMock) CreateAnkraCloudCluster(request client.CreateAnkraCloudClusterRequest) (*client.CreateAnkraCloudClusterResponse, error) {
	return nil, errors.New("zone fi-hel9 does not exist")
}

func (mock *ankraCloudFailingMock) ListAnkraCloudZones(credentialID string) (*client.AnkraCloudCatalogResult, error) {
	return nil, errors.New("Ankra Cloud is temporarily unavailable")
}

func TestAnkraCloudCommandsWrapAPIErrors(t *testing.T) {
	mock := &ankraCloudFailingMock{}
	if _, runError := runAnkraCloudCommand(t, mock, append([]string{"create"}, ankraCloudRequiredCreateArguments...)...); runError == nil ||
		!strings.Contains(runError.Error(), "creating Ankra Cloud cluster") {
		t.Errorf("create error = %v, want it wrapped", runError)
	}
	if _, runError := runAnkraCloudCommand(t, mock, "zones", "--credential-id", "credential-1"); runError == nil ||
		!strings.Contains(runError.Error(), "listing Ankra Cloud zones") {
		t.Errorf("zones error = %v, want it wrapped", runError)
	}
}

func TestAnkraCloudIsWiredIntoTheSharedProviderSwitches(t *testing.T) {
	setMockClient(t, &ankraCloudClusterMock{})
	if nodeGroupListForKind("ankracloud") == nil {
		t.Error("node groups do not dispatch ankracloud")
	}
	if sshKeysGetForKind("ankracloud") == nil {
		t.Error("ssh keys do not dispatch ankracloud")
	}
	if _, isSupported := scaleFunctionForKind("ankracloud"); !isSupported {
		t.Error("worker scaling does not dispatch ankracloud")
	}
	if _, isSupported := upgradeFunctionForKind("ankracloud"); !isSupported {
		t.Error("Kubernetes upgrades do not dispatch ankracloud")
	}
	if !volumeDeletingKinds[cloudClusterKindAnkraCloud] {
		t.Error("ankracloud deprovision does not name its volumes first")
	}
	for _, subcommand := range []string{"nodes", "bastion", "control-plane"} {
		if found, _, findError := ankraCloudCmd.Find([]string{subcommand}); findError != nil || found == ankraCloudCmd {
			t.Errorf("ankra cluster ankracloud %s is not registered", subcommand)
		}
	}
}
