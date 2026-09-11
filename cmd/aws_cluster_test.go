package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type awsClusterMock struct {
	baseMock

	createRequest    *client.CreateAwsClusterRequest
	preflightRequest *client.CreateAwsClusterRequest
	preflightResult  *client.AwsPreflightResult

	startClusterID string
	startScope     string
	stopClusterID  string
	stopForce      bool

	catalogCalls []string
}

func (mock *awsClusterMock) CreateAwsCluster(request client.CreateAwsClusterRequest) (*client.CreateAwsClusterResponse, error) {
	mock.createRequest = &request
	return &client.CreateAwsClusterResponse{ClusterID: testClusterID, Name: request.Name}, nil
}

func (mock *awsClusterMock) PreflightAwsCluster(request client.CreateAwsClusterRequest) (*client.AwsPreflightResult, error) {
	mock.preflightRequest = &request
	return mock.preflightResult, nil
}

func (mock *awsClusterMock) StopAwsCluster(clusterID string, force bool) (*client.ProviderStopClusterResponse, error) {
	mock.stopClusterID = clusterID
	mock.stopForce = force
	return &client.ProviderStopClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (mock *awsClusterMock) StartAwsCluster(clusterID, scope string) (*client.ProviderStartClusterResult, error) {
	mock.startClusterID = clusterID
	mock.startScope = scope
	return &client.ProviderStartClusterResult{Scope: scope, CreatedOperations: 2}, nil
}

func (mock *awsClusterMock) ListAwsRegions(credentialID string) (*client.AwsCatalogResult, error) {
	mock.catalogCalls = append(mock.catalogCalls, "regions:"+credentialID)
	return &client.AwsCatalogResult{Regions: []client.AwsRegion{{Name: "eu-north-1", DisplayName: "Europe (Stockholm)"}}}, nil
}

func (mock *awsClusterMock) ListAwsInstanceTypes(credentialID, region string) (*client.AwsCatalogResult, error) {
	mock.catalogCalls = append(mock.catalogCalls, "instance-types:"+credentialID+":"+region)
	return &client.AwsCatalogResult{
		InstanceTypes:     []client.AwsInstanceType{{Name: "t3.medium", VCPUs: 2, MemoryGiB: 4, Architecture: "x86_64", HourlyPrice: 0.0418, MonthlyPrice: 30.5, Currency: "usd"}},
		PricingComplete:   false,
		IncompleteReasons: []string{"EBS pricing not published"},
	}, nil
}

func (mock *awsClusterMock) ListAwsVpcs(credentialID, region string) (*client.AwsCatalogResult, error) {
	mock.catalogCalls = append(mock.catalogCalls, "vpcs:"+credentialID+":"+region)
	return &client.AwsCatalogResult{Vpcs: []client.AwsVpc{{ID: "vpc-0abc", Name: "prod", CIDR: "10.0.0.0/16", IsDefault: false}}}, nil
}

func (mock *awsClusterMock) ListAwsSubnets(credentialID, region, vpcID string) (*client.AwsCatalogResult, error) {
	mock.catalogCalls = append(mock.catalogCalls, "subnets:"+credentialID+":"+region+":"+vpcID)
	return &client.AwsCatalogResult{Subnets: []client.AwsSubnet{{ID: "subnet-0aaa", CIDR: "10.0.1.0/24", AvailabilityZone: "eu-north-1a", VpcID: vpcID, Public: true}}}, nil
}

func (mock *awsClusterMock) ListAwsAvailabilityZones(credentialID, region string) (*client.AwsCatalogResult, error) {
	mock.catalogCalls = append(mock.catalogCalls, "availability-zones:"+credentialID+":"+region)
	return &client.AwsCatalogResult{AvailabilityZones: []client.AwsAvailabilityZone{{Name: "eu-north-1a", ZoneID: "eun1-az1", State: "available"}}}, nil
}

func (mock *awsClusterMock) ListAwsImages(credentialID, region string) (*client.AwsCatalogResult, error) {
	mock.catalogCalls = append(mock.catalogCalls, "images:"+credentialID+":"+region)
	return &client.AwsCatalogResult{Images: []client.AwsImage{{ID: "ami-0123", Name: "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server", UbuntuSeries: "noble", Architecture: "x86_64"}}}, nil
}

func (mock *awsClusterMock) ListAwsPricing(credentialID, region string) (*client.AwsCatalogResult, error) {
	mock.catalogCalls = append(mock.catalogCalls, "pricing:"+credentialID+":"+region)
	return &client.AwsCatalogResult{Pricing: []client.AwsPriceItem{{Item: "nat-gateway", HourlyPrice: 0.045, MonthlyPrice: 32.85, Currency: "usd"}}, PricingComplete: true}, nil
}

// awsCreateArgs is a complete, valid create invocation; tests append to it.
var awsCreateArgs = []string{
	"--name", "prod",
	"--credential-id", "cred-aws",
	"--ssh-key-credential-id", "cred-ssh",
	"--region", "eu-north-1",
	"--vpc-id", "vpc-0abc",
	"--node-subnet-ids", "subnet-0aaa,subnet-0bbb",
	"--bastion-subnet-id", "subnet-0ccc",
	"--bastion-allowed-ips", "203.0.113.0/24,198.51.100.7/32",
}

func runAwsCreate(t *testing.T, mock *awsClusterMock, verb string, extra ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsCreateCmd, awsPreflightCmd) })
	args := append([]string{"cluster", "aws", verb}, awsCreateArgs...)
	args = append(args, extra...)
	var runError error
	output := captureStdout(t, func() {
		_, runError = executeCommand(args...)
	})
	return output, runError
}

// The create body is what the platform decodes, so every flag has to land on
// its snake_case member and the omitted ones have to stay omitted.
func TestAwsCreateSerialisesTheRequest(t *testing.T) {
	mock := &awsClusterMock{}
	output, runError := runAwsCreate(t, mock, "create",
		"--description", "the prod cluster",
		"--egress-mode", "bastion_nat",
		"--bastion-instance-type", "t3.micro",
		"--control-plane-count", "3",
		"--control-plane-type", "t3.large",
		"--worker-type", "m6i.large",
		"--distribution", "kubeadm",
		"--kubernetes-version", "v1.33.2",
		"--etcd-topology", "external",
		"--etcd-node-count", "5",
		"--etcd-type", "t3.medium",
		"--cni", "cilium",
		"--cni-features", "hubble,wireguard",
		"--k3s-disabled-components", "traefik,servicelb",
		"--ubuntu-series", "noble",
		"--architecture", "arm64",
		"--root-volume-gib", "80",
		"--gitops-credential-name", "github-prod",
		"--gitops-repository", "acme/platform",
		"--gitops-branch", "main",
		"--retention-policy", "delete",
		"--classification", "production",
	)
	if runError != nil {
		t.Fatalf("create failed: %v", runError)
	}
	if mock.createRequest == nil {
		t.Fatal("create request was never sent")
	}
	request := *mock.createRequest

	stringFields := map[string][2]string{
		"name":                  {request.Name, "prod"},
		"credential_id":         {request.CredentialID, "cred-aws"},
		"ssh_key_credential_id": {request.SSHKeyCredentialID, "cred-ssh"},
		"region":                {request.Region, "eu-north-1"},
		"vpc_id":                {request.VpcID, "vpc-0abc"},
		"bastion_subnet_id":     {request.BastionSubnetID, "subnet-0ccc"},
		"egress_mode":           {request.EgressMode, "bastion_nat"},
		"bastion_instance_type": {request.BastionInstanceType, "t3.micro"},
		"control_plane_type":    {request.ControlPlaneType, "t3.large"},
		"worker_type":           {request.WorkerType, "m6i.large"},
		"distribution":          {request.Distribution, "kubeadm"},
		"etcd_topology":         {request.EtcdTopology, "external"},
		"etcd_type":             {request.EtcdType, "t3.medium"},
		"cni":                   {request.CNI, "cilium"},
		"ubuntu_series":         {request.UbuntuSeries, "noble"},
		"architecture":          {request.Architecture, "arm64"},
		"retention_policy":      {request.RetentionPolicy, "delete"},
		"classification":        {request.Classification, "production"},
	}
	for field, pair := range stringFields {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", field, pair[0], pair[1])
		}
	}
	if request.ControlPlaneCount != 3 || request.EtcdNodeCount != 5 || request.RootVolumeGiB != 80 {
		t.Errorf("counts = cp %d, etcd %d, root %d; want 3, 5, 80", request.ControlPlaneCount, request.EtcdNodeCount, request.RootVolumeGiB)
	}
	if got := strings.Join(request.NodeSubnetIDs, ","); got != "subnet-0aaa,subnet-0bbb" {
		t.Errorf("node_subnet_ids = %q, want the two subnets", got)
	}
	if got := strings.Join(request.BastionAllowedIPs, ","); got != "203.0.113.0/24,198.51.100.7/32" {
		t.Errorf("bastion_allowed_ips = %q, want the two CIDRs", got)
	}
	if got := strings.Join(request.CNIFeatures, ","); got != "hubble,wireguard" {
		t.Errorf("cni_features = %q, want hubble,wireguard", got)
	}
	if got := strings.Join(request.K3sDisabledComponents, ","); got != "traefik,servicelb" {
		t.Errorf("k3s_disabled_components = %q, want traefik,servicelb", got)
	}
	for field, pair := range map[string][2]*string{
		"description":            {request.Description, stringPointer("the prod cluster")},
		"kubernetes_version":     {request.KubernetesVersion, stringPointer("v1.33.2")},
		"gitops_credential_name": {request.GitopsCredentialName, stringPointer("github-prod")},
		"gitops_repository":      {request.GitopsRepository, stringPointer("acme/platform")},
		"gitops_branch":          {request.GitopsBranch, stringPointer("main")},
	} {
		if pair[0] == nil || *pair[0] != *pair[1] {
			t.Errorf("%s = %v, want %q", field, pair[0], *pair[1])
		}
	}
	// Untouched tri-state flags stay absent so the server default applies.
	if request.WorkerCount != nil {
		t.Errorf("worker_count = %d, want omitted when the flag is not set", *request.WorkerCount)
	}
	if request.IncludeNetworking != nil || request.IncludeDNS != nil {
		t.Errorf("include_networking/include_dns must be omitted when untouched, got %v/%v", request.IncludeNetworking, request.IncludeDNS)
	}
	if !strings.Contains(output, "AWS cluster 'prod' created successfully") || !strings.Contains(output, testClusterID) {
		t.Errorf("unexpected output: %s", output)
	}
}

// Zero workers and the two include flags set to false are legitimate values
// the server must see, not defaults it may overwrite.
func TestAwsCreateSendsTriStateFlagsWhenSet(t *testing.T) {
	mock := &awsClusterMock{}
	if _, runError := runAwsCreate(t, mock, "create",
		"--worker-count", "0",
		"--include-networking=false",
		"--include-dns=false",
	); runError != nil {
		t.Fatalf("create failed: %v", runError)
	}
	request := mock.createRequest
	if request.WorkerCount == nil || *request.WorkerCount != 0 {
		t.Errorf("worker_count = %v, want 0 sent explicitly", request.WorkerCount)
	}
	if request.IncludeNetworking == nil || *request.IncludeNetworking {
		t.Errorf("include_networking = %v, want false", request.IncludeNetworking)
	}
	if request.IncludeDNS == nil || *request.IncludeDNS {
		t.Errorf("include_dns = %v, want false", request.IncludeDNS)
	}
	// Optional strings left blank are omitted, not sent empty.
	if request.Description != nil || request.KubernetesVersion != nil || request.GitopsBranch != nil {
		t.Errorf("blank optional strings must be omitted, got description=%v version=%v branch=%v",
			request.Description, request.KubernetesVersion, request.GitopsBranch)
	}
	if request.EgressMode != "" {
		t.Errorf("egress_mode = %q, want omitted so the server auto-detects it", request.EgressMode)
	}
}

func TestAwsCreateRequiresTheNetworkingFlags(t *testing.T) {
	setMockClient(t, &awsClusterMock{})
	t.Cleanup(func() { resetTreeFlags(t, awsCreateCmd) })
	for _, required := range []string{"name", "credential-id", "ssh-key-credential-id", "region", "vpc-id", "node-subnet-ids", "bastion-subnet-id", "bastion-allowed-ips"} {
		var args []string
		for index := 0; index < len(awsCreateArgs); index += 2 {
			if strings.TrimPrefix(awsCreateArgs[index], "--") == required {
				continue
			}
			args = append(args, awsCreateArgs[index], awsCreateArgs[index+1])
		}
		_, runError := executeCommand(append([]string{"cluster", "aws", "create"}, args...)...)
		resetTreeFlags(t, awsCreateCmd)
		if runError == nil || !strings.Contains(runError.Error(), required) {
			t.Errorf("omitting --%s must be refused naming the flag, got %v", required, runError)
		}
	}
}

func TestAwsPreflightRendersChecksAndResolvedEgress(t *testing.T) {
	mock := &awsClusterMock{preflightResult: &client.AwsPreflightResult{
		CanProceed:         true,
		ResolvedEgressMode: "existing",
		Items: []client.AwsPreflightItem{
			{Check: "vpc", Status: "ok", Message: "vpc-0abc is reachable"},
			{Check: "node_subnets", Status: "ok", Message: "route to nat-0f00"},
		},
	}}
	output, runError := runAwsCreate(t, mock, "preflight")
	if runError != nil {
		t.Fatalf("preflight failed: %v", runError)
	}
	if mock.preflightRequest == nil || mock.preflightRequest.VpcID != "vpc-0abc" {
		t.Fatalf("preflight must send the same body as create, got %+v", mock.preflightRequest)
	}
	for _, want := range []string{"node_subnets", "route to nat-0f00", "Resolved egress mode: existing", "Preflight passed"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, output)
		}
	}
}

func TestAwsPreflightFailsWhenItCannotProceed(t *testing.T) {
	mock := &awsClusterMock{preflightResult: &client.AwsPreflightResult{
		CanProceed: false,
		Items:      []client.AwsPreflightItem{{Check: "bastion_subnet", Status: "error", Message: "subnet-0ccc has no route to an internet gateway"}},
	}}
	output, runError := runAwsCreate(t, mock, "preflight")
	if runError == nil || !strings.Contains(runError.Error(), "preflight failed") {
		t.Fatalf("a failed preflight must return an error, got %v", runError)
	}
	if !strings.Contains(output, "no route to an internet gateway") {
		t.Errorf("the failing check must be rendered, got:\n%s", output)
	}
}

func TestAwsStopCommand(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsStopCmd) })

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "aws", "stop", testClusterID, "--force")
	})

	if mock.stopClusterID != testClusterID {
		t.Fatalf("cluster id = %q, want %q", mock.stopClusterID, testClusterID)
	}
	if !mock.stopForce {
		t.Error("--force must reach the client")
	}
	if !strings.Contains(output, "AWS cluster stop initiated") {
		t.Fatalf("unexpected output: %s", output)
	}
}

func TestAwsStartCommandWithScope(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsStartCmd) })

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "aws", "start", testClusterID, "--scope", "control_plane")
	})

	if mock.startClusterID != testClusterID || mock.startScope != "control_plane" {
		t.Fatalf("start = %q/%q, want %q/control_plane", mock.startClusterID, mock.startScope, testClusterID)
	}
	if !strings.Contains(output, "AWS cluster start initiated") {
		t.Fatalf("unexpected output: %s", output)
	}
}

type awsDeprovisionConfirmMock struct {
	baseMock
	clusters      []client.ClusterListItem
	deprovisioned string
}

func (m *awsDeprovisionConfirmMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{
		Result:     m.clusters,
		Pagination: client.Pagination{TotalPages: 1, Page: page, PageSize: pageSize},
	}, nil
}

func (m *awsDeprovisionConfirmMock) DeprovisionAwsCluster(clusterID string) (*client.ProviderDeprovisionClusterResponse, error) {
	m.deprovisioned = clusterID
	return &client.ProviderDeprovisionClusterResponse{ClusterID: clusterID}, nil
}

// Deprovision is a permanent delete that resolves a short name silently, so
// it has to prompt like the other providers and honour --yes for scripts.
func TestAwsDeprovisionRefusesWithoutConfirmation(t *testing.T) {
	clusters := []client.ClusterListItem{{ID: testClusterID, Name: "prod"}}

	declined := &awsDeprovisionConfirmMock{clusters: clusters}
	if _, runError := runConfirmCommand(t, declined, "n\n",
		[]*cobra.Command{awsDeprovisionCmd}, "cluster", "aws", "deprovision", "prod"); runError == nil {
		t.Fatal("declining the prompt must not proceed")
	}
	if declined.deprovisioned != "" {
		t.Fatalf("a declined prompt must not deprovision, but %q was torn down", declined.deprovisioned)
	}

	accepted := &awsDeprovisionConfirmMock{clusters: clusters}
	if _, runError := runConfirmCommand(t, accepted, "y\n",
		[]*cobra.Command{awsDeprovisionCmd}, "cluster", "aws", "deprovision", "prod"); runError != nil {
		t.Fatalf("accepting the prompt must proceed: %v", runError)
	}
	if accepted.deprovisioned != testClusterID {
		t.Fatalf("an accepted prompt must deprovision the resolved id, got %q", accepted.deprovisioned)
	}

	skipped := &awsDeprovisionConfirmMock{clusters: clusters}
	if _, runError := runConfirmCommand(t, skipped, "",
		[]*cobra.Command{awsDeprovisionCmd}, "cluster", "aws", "deprovision", "prod", "--yes"); runError != nil {
		t.Fatalf("--yes must skip the prompt: %v", runError)
	}
	if skipped.deprovisioned != testClusterID {
		t.Fatalf("--yes must deprovision the resolved id, got %q", skipped.deprovisioned)
	}
}

// Each catalog command forwards exactly the scope it takes: the credential
// alone for regions, plus the region for the regional ones, plus the VPC
// for subnets.
func TestAwsCatalogCommandsForwardTheirScope(t *testing.T) {
	for _, testCase := range []struct {
		args     []string
		wantCall string
		wantRow  string
	}{
		{args: []string{"regions", "--credential-id", "cred-aws"}, wantCall: "regions:cred-aws", wantRow: "Europe (Stockholm)"},
		{args: []string{"instance-types", "--credential-id", "cred-aws", "--region", "eu-north-1"}, wantCall: "instance-types:cred-aws:eu-north-1", wantRow: "t3.medium"},
		{args: []string{"vpcs", "--credential-id", "cred-aws", "--region", "eu-north-1"}, wantCall: "vpcs:cred-aws:eu-north-1", wantRow: "vpc-0abc"},
		{args: []string{"subnets", "--credential-id", "cred-aws", "--region", "eu-north-1", "--vpc-id", "vpc-0abc"}, wantCall: "subnets:cred-aws:eu-north-1:vpc-0abc", wantRow: "subnet-0aaa"},
		{args: []string{"availability-zones", "--credential-id", "cred-aws", "--region", "eu-north-1"}, wantCall: "availability-zones:cred-aws:eu-north-1", wantRow: "eun1-az1"},
		{args: []string{"images", "--credential-id", "cred-aws", "--region", "eu-north-1"}, wantCall: "images:cred-aws:eu-north-1", wantRow: "ami-0123"},
		{args: []string{"pricing", "--credential-id", "cred-aws", "--region", "eu-north-1"}, wantCall: "pricing:cred-aws:eu-north-1", wantRow: "nat-gateway"},
	} {
		t.Run(testCase.args[0], func(t *testing.T) {
			mock := &awsClusterMock{}
			setMockClient(t, mock)
			t.Cleanup(func() {
				resetTreeFlags(t, awsRegionsCmd, awsInstanceTypesCmd, awsVpcsCmd, awsSubnetsCmd, awsAvailabilityZonesCmd, awsImagesCmd, awsPricingCmd)
			})
			var runError error
			output := captureStdout(t, func() {
				_, runError = executeCommand(append([]string{"cluster", "aws"}, testCase.args...)...)
			})
			if runError != nil {
				t.Fatalf("%s failed: %v", testCase.args[0], runError)
			}
			if len(mock.catalogCalls) != 1 || mock.catalogCalls[0] != testCase.wantCall {
				t.Fatalf("catalog calls = %v, want [%s]", mock.catalogCalls, testCase.wantCall)
			}
			if !strings.Contains(output, testCase.wantRow) {
				t.Errorf("expected %q in output, got:\n%s", testCase.wantRow, output)
			}
		})
	}
}

// The subnets catalog is VPC-scoped and a region-less read is meaningless
// for every regional catalog, so the flags are required rather than
// silently sent empty.
func TestAwsCatalogCommandsRequireTheirScope(t *testing.T) {
	setMockClient(t, &awsClusterMock{})
	t.Cleanup(func() {
		resetTreeFlags(t, awsRegionsCmd, awsInstanceTypesCmd, awsSubnetsCmd)
	})
	if _, runError := executeCommand("cluster", "aws", "regions"); runError == nil || !strings.Contains(runError.Error(), "credential-id") {
		t.Errorf("regions without --credential-id must be refused, got %v", runError)
	}
	resetTreeFlags(t, awsRegionsCmd)
	if _, runError := executeCommand("cluster", "aws", "instance-types", "--credential-id", "cred-aws"); runError == nil || !strings.Contains(runError.Error(), "region") {
		t.Errorf("instance-types without --region must be refused, got %v", runError)
	}
	resetTreeFlags(t, awsInstanceTypesCmd)
	if _, runError := executeCommand("cluster", "aws", "subnets", "--credential-id", "cred-aws", "--region", "eu-north-1"); runError == nil || !strings.Contains(runError.Error(), "vpc-id") {
		t.Errorf("subnets without --vpc-id must be refused, got %v", runError)
	}
}

func TestAwsInstanceTypesWarnsWhenPricingIsIncomplete(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsInstanceTypesCmd) })
	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "aws", "instance-types", "--credential-id", "cred-aws", "--region", "eu-north-1")
	})
	if !strings.Contains(output, "Pricing is incomplete: EBS pricing not published") {
		t.Errorf("an incomplete price list must say what is missing, got:\n%s", output)
	}
}

type awsCredentialsMock struct {
	baseMock
	listCalls int
}

func (m *awsCredentialsMock) ListAwsCredentials() ([]client.Credential, error) {
	m.listCalls++
	state := "connected"
	return []client.Credential{{ID: "cred-aws", Name: "aws-prod", Provider: "aws", State: &state, Available: true, CreatedAt: "2026-09-01T00:00:00Z"}}, nil
}

func TestAwsCredentialsListRendersTheProviderFilteredListing(t *testing.T) {
	mock := &awsCredentialsMock{}
	setMockClient(t, mock)
	output := captureStdout(t, func() {
		if _, runError := executeCommand("credentials", "aws", "list"); runError != nil {
			t.Fatalf("list failed: %v", runError)
		}
	})
	if mock.listCalls != 1 {
		t.Fatalf("list calls = %d, want 1", mock.listCalls)
	}
	for _, want := range []string{"cred-aws", "aws-prod", "connected"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, output)
		}
	}
}
