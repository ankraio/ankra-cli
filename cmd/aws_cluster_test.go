package cmd

import (
	"encoding/json"
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
	return &client.CreateAwsClusterResponse{ClusterID: testClusterID, Name: request.Name, Kind: "aws", State: "creating", OperationID: stringPointer("op-1")}, nil
}

func (mock *awsClusterMock) PreflightAwsCluster(request client.CreateAwsClusterRequest) (*client.AwsPreflightResult, error) {
	mock.preflightRequest = &request
	return mock.preflightResult, nil
}

func (mock *awsClusterMock) StopAwsCluster(clusterID string, options client.StopClusterOptions) (*client.ProviderStopClusterResponse, error) {
	mock.stopClusterID = clusterID
	mock.stopForce = options.Force
	return &client.ProviderStopClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (mock *awsClusterMock) StartAwsCluster(clusterID string, options client.StartClusterOptions) (*client.ProviderStartClusterResult, error) {
	mock.startClusterID = clusterID
	mock.startScope = options.Scope
	return &client.ProviderStartClusterResult{Scope: options.Scope, CreatedOperations: 2}, nil
}

func floatPointer(value float64) *float64 { return &value }

func (mock *awsClusterMock) ListAwsRegions(credentialID string) (*client.AwsRegionsCatalog, error) {
	mock.catalogCalls = append(mock.catalogCalls, "regions:"+credentialID)
	return &client.AwsRegionsCatalog{Regions: []client.AwsRegion{{Slug: "eu-north-1", Name: "Europe (Stockholm)"}}, PricingComplete: true}, nil
}

func (mock *awsClusterMock) ListAwsInstanceTypes(credentialID, region string) (*client.AwsInstanceTypesCatalog, error) {
	mock.catalogCalls = append(mock.catalogCalls, "instance-types:"+credentialID+":"+region)
	return &client.AwsInstanceTypesCatalog{
		InstanceTypes: []client.AwsInstanceType{
			{Name: "t3.medium", VCPUs: 2, MemoryGiB: 4, Architecture: "amd64", Category: "general", HourlyPriceUSD: floatPointer(0.0418), MonthlyPriceUSD: floatPointer(30.5), CurrentGeneration: true},
			{Name: "m7g.large", VCPUs: 2, MemoryGiB: 8, Architecture: "arm64", Category: "general", HourlyPriceUSD: nil, MonthlyPriceUSD: nil, CurrentGeneration: true},
		},
		PricingComplete:   false,
		IncompleteReasons: []string{"EBS pricing not published"},
	}, nil
}

func (mock *awsClusterMock) ListAwsVpcs(credentialID, region string) (*client.AwsVpcsCatalog, error) {
	mock.catalogCalls = append(mock.catalogCalls, "vpcs:"+credentialID+":"+region)
	return &client.AwsVpcsCatalog{Vpcs: []client.AwsVpc{
		{ID: "vpc-0abc", Name: "prod", CIDR: "10.0.0.0/16", IsDefault: false, DhcpDomainName: stringPointer("eu-north-1.compute.internal"), DhcpDomainNameState: "set"},
		{ID: "vpc-0def", Name: "lab", CIDR: "10.1.0.0/16", IsDefault: false, DhcpDomainName: nil, DhcpDomainNameState: "empty"},
		{ID: "vpc-0fff", Name: "opaque", CIDR: "10.2.0.0/16", IsDefault: true, DhcpDomainName: nil, DhcpDomainNameState: "unknown"},
	}}, nil
}

func (mock *awsClusterMock) ListAwsSubnets(credentialID, region, vpcID string) (*client.AwsSubnetsCatalog, error) {
	mock.catalogCalls = append(mock.catalogCalls, "subnets:"+credentialID+":"+region+":"+vpcID)
	return &client.AwsSubnetsCatalog{Subnets: []client.AwsSubnet{
		{ID: "subnet-0aaa", Name: "private-a", CIDR: "10.0.1.0/24", AvailabilityZone: "eu-north-1a", MapPublicIPOnLaunch: false, Egress: client.AwsSubnetEgress{Kind: "nat_gateway"}, ForeignInstanceCount: intPointer(0)},
		{ID: "subnet-0ccc", Name: "public-a", CIDR: "10.0.100.0/24", AvailabilityZone: "eu-north-1a", MapPublicIPOnLaunch: true, Egress: client.AwsSubnetEgress{Kind: "internet_gateway"}, ForeignInstanceCount: nil},
	}}, nil
}

func (mock *awsClusterMock) ListAwsAvailabilityZones(credentialID, region string) (*client.AwsAvailabilityZonesCatalog, error) {
	mock.catalogCalls = append(mock.catalogCalls, "availability-zones:"+credentialID+":"+region)
	return &client.AwsAvailabilityZonesCatalog{Zones: []client.AwsAvailabilityZone{{Name: "eu-north-1a", ID: "eun1-az1", State: "available"}}}, nil
}

func (mock *awsClusterMock) ListAwsImages(credentialID, region string) (*client.AwsImagesCatalog, error) {
	mock.catalogCalls = append(mock.catalogCalls, "images:"+credentialID+":"+region)
	return &client.AwsImagesCatalog{UbuntuSeries: []string{"22.04", "24.04"}, Architectures: []string{"amd64"}, DefaultSeries: "24.04"}, nil
}

func (mock *awsClusterMock) ListAwsPricing(credentialID, region string) (*client.AwsPricingCatalog, error) {
	mock.catalogCalls = append(mock.catalogCalls, "pricing:"+credentialID+":"+region)
	return &client.AwsPricingCatalog{
		InstanceTypes:   []client.AwsInstanceType{{Name: "t3.medium", VCPUs: 2, MemoryGiB: 4, Architecture: "amd64", Category: "general", HourlyPriceUSD: floatPointer(0.0418), MonthlyPriceUSD: floatPointer(30.5), CurrentGeneration: true}},
		Storage:         client.AwsStoragePrice{Gp3GiBMonthUSD: nil},
		PricingComplete: false, IncompleteReasons: []string{"EBS pricing not published"},
	}, nil
}

func (mock *awsClusterMock) GetAwsAccessInfo(clusterID string) (*client.AwsAccessInfo, error) {
	return &client.AwsAccessInfo{
		BastionIP: stringPointer("203.0.113.10"), BastionHost: "203.0.113.10", BastionPort: 2222, BastionUser: "ec2-user", TargetUser: "ubuntu",
		ControlPlaneIP: stringPointer("10.0.1.10"), ControlPlaneIPs: []string{"10.0.1.10"}, ClusterName: stringPointer("prod"),
	}, nil
}

// awsCreateArgs is a complete, valid create invocation in the default mode,
// where Ankra creates the network: no VPC or subnets are named. Tests
// append to it.
var awsCreateArgs = []string{
	"--name", "prod",
	"--credential-id", "cred-aws",
	"--ssh-key-credential-id", "cred-ssh",
	"--region", "eu-north-1",
	"--bastion-allowed-ips", "203.0.113.0/24,198.51.100.7/32",
}

// awsAdoptedNetworkArgs switches a create to adopting an existing VPC.
var awsAdoptedNetworkArgs = []string{
	"--vpc-id", "vpc-0abc",
	"--node-subnet-ids", "subnet-0aaa,subnet-0bbb",
	"--bastion-subnet-id", "subnet-0ccc",
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
// its snake_case member and the omitted ones have to stay omitted. This is
// the adopted-network shape; the created-network one is pinned below.
func TestAwsCreateSerialisesTheRequest(t *testing.T) {
	mock := &awsClusterMock{}
	output, runError := runAwsCreate(t, mock, "create", append(awsAdoptedNetworkArgs,
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
		"--cni-features", "hubble,wireguard_encryption",
		"--k3s-disabled-components", "traefik,servicelb",
		"--ubuntu-series", "24.04",
		"--architecture", "amd64",
		"--root-volume-gib", "80",
		"--gitops-credential-name", "github-prod",
		"--gitops-repository", "acme/platform",
		"--gitops-branch", "main",
		"--retention-policy", "delete",
		"--environment", "production",
		"--criticality", "high",
	)...)
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
		"ubuntu_series":         {request.UbuntuSeries, "24.04"},
		"architecture":          {request.Architecture, "amd64"},
		"retention_policy":      {request.RetentionPolicy, "delete"},
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
	// cni_features is an object of booleans on the wire: the named ones are
	// on, the rest stay off.
	if request.CNIFeatures == nil || !request.CNIFeatures.Hubble || !request.CNIFeatures.WireguardEncryption ||
		request.CNIFeatures.KubeProxyReplacement || request.CNIFeatures.EbpfDataplane {
		t.Errorf("cni_features = %+v, want hubble and wireguard_encryption on only", request.CNIFeatures)
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
		"environment":            {request.Environment, stringPointer("production")},
		"criticality":            {request.Criticality, stringPointer("high")},
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
	// The created-network members do not apply to an adopted VPC and stay
	// absent.
	if request.NetworkIPRange != "" || len(request.AvailabilityZones) != 0 || request.NatGatewaySingleZone {
		t.Errorf("created-network members must be absent when adopting a VPC, got range=%q zones=%v single_zone=%v",
			request.NetworkIPRange, request.AvailabilityZones, request.NatGatewaySingleZone)
	}
	if !strings.Contains(output, "AWS cluster 'prod' created successfully") || !strings.Contains(output, testClusterID) ||
		!strings.Contains(output, "State: creating") || !strings.Contains(output, "Operation ID: op-1") {
		t.Errorf("unexpected output: %s", output)
	}
}

// Without --vpc-id Ankra creates the network, and the request carries the
// created-network members only: no VPC, no subnets, and the CIDR, zones and
// NAT layout the flags named.
func TestAwsCreateSerialisesACreatedNetwork(t *testing.T) {
	mock := &awsClusterMock{}
	if _, runError := runAwsCreate(t, mock, "create",
		"--network-ip-range", "10.42.0.0/16",
		"--availability-zones", "eu-north-1a,eu-north-1b,eu-north-1c",
		"--nat-gateway-single-zone",
		"--egress-mode", "nat_gateway",
		"--control-plane-count", "3",
	); runError != nil {
		t.Fatalf("create failed: %v", runError)
	}
	if mock.createRequest == nil {
		t.Fatal("create request was never sent")
	}
	request := *mock.createRequest
	if request.VpcID != "" || len(request.NodeSubnetIDs) != 0 || request.BastionSubnetID != "" {
		t.Errorf("a created network must name no VPC or subnets, got vpc=%q subnets=%v bastion=%q", request.VpcID, request.NodeSubnetIDs, request.BastionSubnetID)
	}
	if request.NetworkIPRange != "10.42.0.0/16" {
		t.Errorf("network_ip_range = %q, want 10.42.0.0/16", request.NetworkIPRange)
	}
	if got := strings.Join(request.AvailabilityZones, ","); got != "eu-north-1a,eu-north-1b,eu-north-1c" {
		t.Errorf("availability_zones = %q, want the three zones", got)
	}
	if !request.NatGatewaySingleZone {
		t.Error("nat_gateway_single_zone must be sent when the flag is set")
	}
	if request.EgressMode != "nat_gateway" || request.ControlPlaneCount != 3 {
		t.Errorf("egress_mode/control_plane_count = %q/%d, want nat_gateway/3", request.EgressMode, request.ControlPlaneCount)
	}
	if got := strings.Join(request.BastionAllowedIPs, ","); got != "203.0.113.0/24,198.51.100.7/32" {
		t.Errorf("bastion_allowed_ips = %q, want the two CIDRs", got)
	}
}

// The minimal created-network create sends nothing about the network at
// all, so the server's defaults (10.0.0.0/16, its zone count, NAT gateways)
// apply rather than the CLI's idea of them.
func TestAwsCreateLeavesTheCreatedNetworkToTheServerByDefault(t *testing.T) {
	mock := &awsClusterMock{}
	if _, runError := runAwsCreate(t, mock, "create"); runError != nil {
		t.Fatalf("create failed: %v", runError)
	}
	request := *mock.createRequest
	if request.VpcID != "" || len(request.NodeSubnetIDs) != 0 || request.BastionSubnetID != "" ||
		request.NetworkIPRange != "" || len(request.AvailabilityZones) != 0 || request.NatGatewaySingleZone || request.EgressMode != "" {
		t.Errorf("every network member must be omitted by default, got %+v", request)
	}
}

// --vpc-id switches to adopting a network, which has to name the subnets;
// without it the subnet flags have nothing to refer to. Both directions are
// refused before the request is sent, naming the flag.
func TestAwsCreateRefusesAMixedNetworkMode(t *testing.T) {
	for name, testCase := range map[string]struct {
		extra []string
		want  []string
	}{
		"vpc without subnets": {
			extra: []string{"--vpc-id", "vpc-0abc"},
			want:  []string{"--vpc-id", "--node-subnet-ids", "--bastion-subnet-id"},
		},
		"vpc without bastion subnet": {
			extra: []string{"--vpc-id", "vpc-0abc", "--node-subnet-ids", "subnet-0aaa"},
			want:  []string{"--bastion-subnet-id"},
		},
		"node subnets without vpc": {
			extra: []string{"--node-subnet-ids", "subnet-0aaa"},
			want:  []string{"--node-subnet-ids", "--vpc-id"},
		},
		"bastion subnet without vpc": {
			extra: []string{"--bastion-subnet-id", "subnet-0ccc"},
			want:  []string{"--bastion-subnet-id", "--vpc-id"},
		},
		"network range with vpc": {
			extra: append(append([]string{}, awsAdoptedNetworkArgs...), "--network-ip-range", "10.42.0.0/16"),
			want:  []string{"--network-ip-range", "--vpc-id"},
		},
		"zones with vpc": {
			extra: append(append([]string{}, awsAdoptedNetworkArgs...), "--availability-zones", "eu-north-1a"),
			want:  []string{"--availability-zones", "--vpc-id"},
		},
		"single-zone nat with vpc": {
			extra: append(append([]string{}, awsAdoptedNetworkArgs...), "--nat-gateway-single-zone"),
			want:  []string{"--nat-gateway-single-zone", "--vpc-id"},
		},
		"nat_gateway egress with vpc": {
			extra: append(append([]string{}, awsAdoptedNetworkArgs...), "--egress-mode", "nat_gateway"),
			want:  []string{"nat_gateway", "existing", "bastion_nat"},
		},
		"existing egress without vpc": {
			extra: []string{"--egress-mode", "existing"},
			want:  []string{"existing", "--vpc-id", "nat_gateway"},
		},
		"unknown egress mode": {
			extra: []string{"--egress-mode", "igw"},
			want:  []string{"igw", "nat_gateway", "bastion_nat", "existing"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			mock := &awsClusterMock{}
			_, runError := runAwsCreate(t, mock, "create", testCase.extra...)
			if runError == nil {
				t.Fatal("a mixed network mode must be refused")
			}
			for _, want := range testCase.want {
				if !strings.Contains(runError.Error(), want) {
					t.Errorf("refusal must name %q, got: %v", want, runError)
				}
			}
			if mock.createRequest != nil {
				t.Fatal("a refused network mode must not reach the client")
			}
		})
	}
}

// bastion_nat works in both modes, so it is the one egress mode neither
// direction refuses.
func TestAwsCreateAcceptsBastionNatInBothNetworkModes(t *testing.T) {
	created := &awsClusterMock{}
	if _, runError := runAwsCreate(t, created, "create", "--egress-mode", "bastion_nat"); runError != nil {
		t.Fatalf("bastion_nat with a created network: %v", runError)
	}
	adopted := &awsClusterMock{}
	if _, runError := runAwsCreate(t, adopted, "create", append(append([]string{}, awsAdoptedNetworkArgs...), "--egress-mode", "bastion_nat")...); runError != nil {
		t.Fatalf("bastion_nat with an adopted VPC: %v", runError)
	}
	if created.createRequest.EgressMode != "bastion_nat" || adopted.createRequest.EgressMode != "bastion_nat" {
		t.Errorf("egress_mode = %q/%q, want bastion_nat in both", created.createRequest.EgressMode, adopted.createRequest.EgressMode)
	}
}

// A feature name outside the cni_features object is refused before the
// request is sent, rather than silently dropped.
func TestAwsCreateRefusesUnknownCNIFeature(t *testing.T) {
	mock := &awsClusterMock{}
	_, runError := runAwsCreate(t, mock, "create", "--cni-features", "hubble,wireguard")
	if runError == nil || !strings.Contains(runError.Error(), "wireguard") || !strings.Contains(runError.Error(), "wireguard_encryption") {
		t.Fatalf("an unknown feature must be refused naming the accepted set, got %v", runError)
	}
	if mock.createRequest != nil {
		t.Fatal("a refused feature list must not reach the client")
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
	if request.Description != nil || request.KubernetesVersion != nil || request.GitopsBranch != nil || request.Environment != nil || request.Criticality != nil {
		t.Errorf("blank optional strings must be omitted, got description=%v version=%v branch=%v environment=%v criticality=%v",
			request.Description, request.KubernetesVersion, request.GitopsBranch, request.Environment, request.Criticality)
	}
	if request.EgressMode != "" {
		t.Errorf("egress_mode = %q, want omitted so the server auto-detects it", request.EgressMode)
	}
	if request.CNIFeatures != nil {
		t.Errorf("cni_features = %+v, want omitted when no feature is named", request.CNIFeatures)
	}
}

// The required set is what a created network needs: the VPC and subnet
// flags are no longer required, because the default is a network Ankra
// creates.
func TestAwsCreateRequiresTheCreatedNetworkFlags(t *testing.T) {
	setMockClient(t, &awsClusterMock{})
	t.Cleanup(func() { resetTreeFlags(t, awsCreateCmd) })
	for _, required := range []string{"name", "credential-id", "ssh-key-credential-id", "region"} {
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
		CanProceed:                true,
		ResolvedEgressMode:        stringPointer("existing"),
		NetworkOwnership:          client.AwsNetworkOwnershipAdopted,
		ResolvedAvailabilityZones: []string{"eu-north-1a", "eu-north-1b"},
		Items: []client.AwsPreflightItem{
			{Check: "vpc", Status: "ok", Message: "vpc-0abc is reachable"},
			{Check: "node_subnets", Status: "ok", Message: "route to nat-0f00"},
		},
	}}
	output, runError := runAwsCreate(t, mock, "preflight", awsAdoptedNetworkArgs...)
	if runError != nil {
		t.Fatalf("preflight failed: %v", runError)
	}
	if mock.preflightRequest == nil || mock.preflightRequest.VpcID != "vpc-0abc" {
		t.Fatalf("preflight must send the same body as create, got %+v", mock.preflightRequest)
	}
	for _, want := range []string{
		"node_subnets", "route to nat-0f00",
		"Network ownership: adopted",
		"Resolved availability zones: eu-north-1a, eu-north-1b",
		"Resolved egress mode: existing",
		"Preflight passed",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, output)
		}
	}
}

// A created-network preflight reports the ownership and the zones the
// server settled on, which is the only place the one-or-three default is
// visible before the network exists.
func TestAwsPreflightRendersACreatedNetwork(t *testing.T) {
	mock := &awsClusterMock{preflightResult: &client.AwsPreflightResult{
		CanProceed:                true,
		ResolvedEgressMode:        stringPointer("nat_gateway"),
		NetworkOwnership:          client.AwsNetworkOwnershipCreated,
		ResolvedAvailabilityZones: []string{"eu-north-1a"},
		Items:                     []client.AwsPreflightItem{{Check: "network_ip_range", Status: "ok", Message: "10.0.0.0/16 is free"}},
	}}
	output, runError := runAwsCreate(t, mock, "preflight")
	if runError != nil {
		t.Fatalf("preflight failed: %v", runError)
	}
	if mock.preflightRequest == nil || mock.preflightRequest.VpcID != "" || len(mock.preflightRequest.NodeSubnetIDs) != 0 {
		t.Fatalf("a created-network preflight must name no VPC, got %+v", mock.preflightRequest)
	}
	for _, want := range []string{
		"10.0.0.0/16 is free",
		"Network ownership: created",
		"Resolved availability zones: eu-north-1a",
		"Resolved egress mode: nat_gateway",
		"Preflight passed",
	} {
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
	// A null resolved mode is "could not resolve", and an ownership or zone
	// list the server did not report is unknown: each has to be said
	// rather than leaving the line out and implying a default.
	for _, want := range []string{
		"Resolved egress mode: not resolved",
		"Network ownership: not reported",
		"Resolved availability zones: not resolved",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, output)
		}
	}
}

func TestAwsAccessInfoRendersTheJumpDetails(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsAccessInfoCmd) })
	var runError error
	output := captureStdout(t, func() {
		_, runError = executeCommand("cluster", "aws", "access-info", testClusterID)
	})
	if runError != nil {
		t.Fatalf("access-info failed: %v", runError)
	}
	for _, want := range []string{
		"Cluster: prod",
		"Bastion IP: 203.0.113.10",
		"Bastion SSH: ec2-user@203.0.113.10:2222",
		"Control Plane IP: 10.0.1.10",
		"Node user: ubuntu",
		"ssh -J ec2-user@203.0.113.10:2222 ubuntu@10.0.1.10",
		"ssh -p 2222 -L 6443:10.0.1.10:6443 ec2-user@203.0.113.10",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, output)
		}
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

func (m *awsDeprovisionConfirmMock) DeprovisionAwsCluster(clusterID string, force bool) (*client.ProviderDeprovisionClusterResponse, error) {
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
		{args: []string{"images", "--credential-id", "cred-aws", "--region", "eu-north-1"}, wantCall: "images:cred-aws:eu-north-1", wantRow: "24.04"},
		{args: []string{"pricing", "--credential-id", "cred-aws", "--region", "eu-north-1"}, wantCall: "pricing:cred-aws:eu-north-1", wantRow: "t3.medium"},
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

// A null price is a price the pricing API did not publish, not a free
// instance: it renders as "-", never as 0.0000, while a published one keeps
// its value.
func TestAwsInstanceTypesRendersUnknownPricesAsDash(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsInstanceTypesCmd) })
	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "aws", "instance-types", "--credential-id", "cred-aws", "--region", "eu-north-1")
	})
	priced := awsTableRow(output, "t3.medium")
	if !strings.Contains(priced, "0.0418 USD") || !strings.Contains(priced, "30.50 USD") || !strings.Contains(priced, "general") {
		t.Errorf("the priced row must carry its prices and category, got: %s", priced)
	}
	unpriced := awsTableRow(output, "m7g.large")
	if unpriced == "" {
		t.Fatalf("the unpriced type must still be listed, got:\n%s", output)
	}
	if strings.Contains(unpriced, "0.0000") || strings.Contains(unpriced, "0.00 USD") {
		t.Errorf("a null price must not render as zero, got: %s", unpriced)
	}
	if strings.Count(unpriced, " - ") < 2 {
		t.Errorf("a null price must render as -, got: %s", unpriced)
	}
}

// awsTableRow returns the rendered table line naming the given cell.
func awsTableRow(output, cell string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, cell) {
			return line
		}
	}
	return ""
}

// The pricing catalog's storage line follows the same rule: a null gp3
// price is unknown, not free.
func TestAwsPricingRendersUnknownStoragePriceAsDash(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsPricingCmd) })
	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "aws", "pricing", "--credential-id", "cred-aws", "--region", "eu-north-1")
	})
	if !strings.Contains(output, "Storage: gp3 - per GiB-month") {
		t.Errorf("a null storage price must render as -, got:\n%s", output)
	}
	if !strings.Contains(output, "Pricing is incomplete: EBS pricing not published") {
		t.Errorf("the incomplete reason must be printed, got:\n%s", output)
	}
}

// The DHCP domain column is three-valued: the domain when set, "(none)"
// when the option set names none, and "?" when it could not be read - the
// last two must not collapse into each other.
func TestAwsVpcsRendersTheDhcpDomainThreeWays(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsVpcsCmd) })
	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "aws", "vpcs", "--credential-id", "cred-aws", "--region", "eu-north-1")
	})
	if row := awsTableRow(output, "vpc-0abc"); !strings.Contains(row, "eu-north-1.compute.internal") {
		t.Errorf("a set domain must be shown, got: %s", row)
	}
	if row := awsTableRow(output, "vpc-0def"); !strings.Contains(row, "(none)") {
		t.Errorf("an empty domain must read (none), got: %s", row)
	}
	if row := awsTableRow(output, "vpc-0fff"); !strings.Contains(row, "?") || strings.Contains(row, "(none)") {
		t.Errorf("an unreadable domain must read ?, not (none), got: %s", row)
	}
}

// The foreign-instance column distinguishes a counted zero from a count
// that could not be taken, and the egress kind is what the subnet's route
// table actually reaches.
func TestAwsSubnetsRendersEgressAndForeignInstances(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsSubnetsCmd) })
	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "aws", "subnets", "--credential-id", "cred-aws", "--region", "eu-north-1", "--vpc-id", "vpc-0abc")
	})
	private := awsTableRow(output, "subnet-0aaa")
	if !strings.Contains(private, "nat_gateway") || !strings.Contains(private, " 0 ") {
		t.Errorf("the private subnet must show its NAT egress and a counted zero, got: %s", private)
	}
	public := awsTableRow(output, "subnet-0ccc")
	if !strings.Contains(public, "internet_gateway") || !strings.Contains(public, " - ") || strings.Contains(public, " 0 ") {
		t.Errorf("the public subnet must show its IGW egress and an unknown count as -, got: %s", public)
	}
}

func TestAwsImagesRendersSeriesAndArchitectures(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsImagesCmd) })
	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "aws", "images", "--credential-id", "cred-aws", "--region", "eu-north-1")
	})
	if row := awsTableRow(output, "24.04"); !strings.Contains(row, "yes") {
		t.Errorf("the default series must be marked, got: %s", row)
	}
	if row := awsTableRow(output, "22.04"); strings.Contains(row, "yes") {
		t.Errorf("a non-default series must not be marked, got: %s", row)
	}
	if !strings.Contains(output, "Architectures: amd64") {
		t.Errorf("architectures must be listed, got:\n%s", output)
	}
}

type awsCredentialsMock struct {
	baseMock
	listCalls       int
	onboardingScope *string
	roleRequest     *client.AwsRoleCredentialCreateRequest
	keysRequest     *client.AwsKeysCredentialCreateRequest
	configured      bool
}

func (m *awsCredentialsMock) ListAwsCredentials() ([]client.Credential, error) {
	m.listCalls++
	state := "connected"
	return []client.Credential{{ID: "cred-aws", Name: "aws-prod", Provider: "aws", State: &state, Available: true, CreatedAt: "2026-09-01T00:00:00Z"}}, nil
}

func (m *awsCredentialsMock) GetAwsOnboarding(scope string) (*client.AwsOnboardingResponse, error) {
	m.onboardingScope = &scope
	if !m.configured {
		return &client.AwsOnboardingResponse{Configured: false, Scope: "cost", ExternalID: "ext-456", Region: "us-east-1"}, nil
	}
	return &client.AwsOnboardingResponse{
		Configured: true, Scope: scope, ExternalID: "ext-123", Region: "us-east-1",
		LaunchStackURL:    stringPointer("https://console.aws.amazon.com/cloudformation/home#/stacks/create/review?param_ExternalId=ext-123"),
		TrustPrincipalARN: stringPointer("arn:aws:iam::111122223333:root"),
		TemplateURL:       stringPointer("https://templates.example/self_managed.yaml"),
	}, nil
}

func (m *awsCredentialsMock) CreateAwsRoleCredential(request client.AwsRoleCredentialCreateRequest) (*client.AwsCredentialCreateResponse, error) {
	m.roleRequest = &request
	return &client.AwsCredentialCreateResponse{ID: "cred-role", Name: request.Name, Provider: "aws", Available: true}, nil
}

func (m *awsCredentialsMock) CreateAwsKeysCredential(request client.AwsKeysCredentialCreateRequest) (*client.AwsCredentialCreateResponse, error) {
	m.keysRequest = &request
	return &client.AwsCredentialCreateResponse{ID: "cred-keys", Name: request.Name, Provider: "aws", Available: true}, nil
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

func TestAwsCredentialsOnboardingPrintsTheLaunchMaterial(t *testing.T) {
	mock := &awsCredentialsMock{configured: true}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsCredOnboardingCmd) })
	var runError error
	output := captureStdout(t, func() {
		_, runError = executeCommand("credentials", "aws", "onboarding", "--scope", "self_managed")
	})
	if runError != nil {
		t.Fatalf("onboarding failed: %v", runError)
	}
	if mock.onboardingScope == nil || *mock.onboardingScope != "self_managed" {
		t.Fatalf("scope = %v, want self_managed forwarded", mock.onboardingScope)
	}
	for _, want := range []string{
		"Scope: self_managed",
		"Configured: yes",
		"External ID: ext-123",
		"Trust principal: arn:aws:iam::111122223333:root",
		"Launch stack URL: https://console.aws.amazon.com/cloudformation",
		"create-role --name <name> --role-arn <arn> --external-id ext-123 --scope self_managed",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, output)
		}
	}
}

// An unconfigured scope has no launch URL; the output must say so rather
// than print an empty URL the reader would try to open.
func TestAwsCredentialsOnboardingReportsAnUnconfiguredScope(t *testing.T) {
	mock := &awsCredentialsMock{configured: false}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsCredOnboardingCmd) })
	output := captureStdout(t, func() {
		_, _ = executeCommand("credentials", "aws", "onboarding")
	})
	if mock.onboardingScope == nil || *mock.onboardingScope != "" {
		t.Fatalf("scope = %v, want empty so the server default applies", mock.onboardingScope)
	}
	if !strings.Contains(output, "Configured: no") || !strings.Contains(output, "Launch stack URL: -") || strings.Contains(output, "create-role --name") {
		t.Errorf("an unconfigured scope must be reported without a launch hint, got:\n%s", output)
	}
}

func TestAwsCredentialsOnboardingRefusesAnUnknownScope(t *testing.T) {
	mock := &awsCredentialsMock{configured: true}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsCredOnboardingCmd) })
	_, runError := executeCommand("credentials", "aws", "onboarding", "--scope", "billing")
	if runError == nil || !strings.Contains(runError.Error(), "self_managed") {
		t.Fatalf("an unknown scope must be refused naming the accepted set, got %v", runError)
	}
	if mock.onboardingScope != nil {
		t.Fatal("a refused scope must not reach the client")
	}
}

func TestAwsCredentialsCreateRoleSendsTheRequest(t *testing.T) {
	mock := &awsCredentialsMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsCredCreateRoleCmd) })
	var runError error
	output := captureStdout(t, func() {
		_, runError = executeCommand("credentials", "aws", "create-role",
			"--name", "aws-prod", "--role-arn", "arn:aws:iam::123456789012:role/Ankra",
			"--external-id", "ext-123", "--region", "eu-north-1", "--scope", "self_managed")
	})
	if runError != nil {
		t.Fatalf("create-role failed: %v", runError)
	}
	request := mock.roleRequest
	if request == nil {
		t.Fatal("the role request was never sent")
	}
	if request.Name != "aws-prod" || request.RoleARN != "arn:aws:iam::123456789012:role/Ankra" || request.ExternalID != "ext-123" || request.Region != "eu-north-1" || request.Scope != "self_managed" {
		t.Errorf("request = %+v", request)
	}
	if !strings.Contains(output, "AWS role credential 'aws-prod' created successfully") || !strings.Contains(output, "Credential ID: cred-role") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestAwsCredentialsCreateRoleRequiresItsFlags(t *testing.T) {
	mock := &awsCredentialsMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsCredCreateRoleCmd) })
	for _, missing := range []string{"name", "role-arn", "external-id"} {
		args := []string{"credentials", "aws", "create-role"}
		for _, pair := range [][2]string{{"name", "aws-prod"}, {"role-arn", "arn"}, {"external-id", "ext"}} {
			if pair[0] != missing {
				args = append(args, "--"+pair[0], pair[1])
			}
		}
		_, runError := executeCommand(args...)
		resetTreeFlags(t, awsCredCreateRoleCmd)
		if runError == nil || !strings.Contains(runError.Error(), missing) {
			t.Errorf("omitting --%s must be refused naming the flag, got %v", missing, runError)
		}
	}
	if mock.roleRequest != nil {
		t.Fatal("an incomplete request must not reach the client")
	}
	if _, runError := executeCommand("credentials", "aws", "create-role", "--name", "n", "--role-arn", "a", "--external-id", "e", "--scope", "billing"); runError == nil || !strings.Contains(runError.Error(), "provisioning") {
		t.Errorf("an unknown scope must be refused, got %v", runError)
	}
}

// The secret access key is never a flag: piped stdin feeds it in scripts
// (the masked prompt covers the interactive path, which needs a TTY).
func TestAwsCredentialsCreateKeysReadsTheSecretFromStdin(t *testing.T) {
	mock := &awsCredentialsMock{}
	var runError error
	output := captureStdout(t, func() {
		_, runError = runConfirmCommand(t, mock, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n",
			[]*cobra.Command{awsCredCreateKeysCmd},
			"credentials", "aws", "create-keys", "--name", "aws-keys", "--access-key-id", "AKIAIOSFODNN7EXAMPLE", "--region", "eu-north-1")
	})
	if runError != nil {
		t.Fatalf("create-keys failed: %v", runError)
	}
	request := mock.keysRequest
	if request == nil {
		t.Fatal("the keys request was never sent")
	}
	if request.Name != "aws-keys" || request.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" || request.SecretAccessKey != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" || request.Region != "eu-north-1" {
		t.Errorf("request = %+v", request)
	}
	if strings.Contains(output, "wJalrXUtnFEMI") {
		t.Errorf("the secret must never be echoed, got:\n%s", output)
	}
	if !strings.Contains(output, "AWS keys credential 'aws-keys' created successfully") || !strings.Contains(output, "Credential ID: cred-keys") {
		t.Errorf("unexpected output: %s", output)
	}
	if awsCredCreateKeysCmd.Flags().Lookup("secret-access-key") != nil {
		t.Error("the secret access key must not be accepted as a flag")
	}
}

func TestAwsCredentialsCreateKeysRefusesAnEmptySecret(t *testing.T) {
	mock := &awsCredentialsMock{}
	_, runError := runConfirmCommand(t, mock, "\n",
		[]*cobra.Command{awsCredCreateKeysCmd},
		"credentials", "aws", "create-keys", "--name", "aws-keys", "--access-key-id", "AKIAIOSFODNN7EXAMPLE")
	if runError == nil || !strings.Contains(runError.Error(), "Secret Access Key is required") {
		t.Fatalf("an empty secret must be refused, got %v", runError)
	}
	if mock.keysRequest != nil {
		t.Fatal("an empty secret must not reach the client")
	}
}

// The platform decodes bastion_allowed_ips as a list and reads an EMPTY one
// as its own default (open to everyone); a null is refused as "not a list".
// So an omitted --bastion-allowed-ips has to reach the wire as [], or the
// omitted case the flag help advertises fails with a 422 before anything is
// created.
func TestAwsCreateSendsAnEmptyBastionListWhenTheFlagIsOmitted(t *testing.T) {
	for _, verb := range []string{"create", "preflight"} {
		t.Run(verb, func(subtest *testing.T) {
			mock := &awsClusterMock{preflightResult: &client.AwsPreflightResult{CanProceed: true}}
			setMockClient(subtest, mock)
			subtest.Cleanup(func() { resetTreeFlags(subtest, awsCreateCmd, awsPreflightCmd) })
			var runError error
			captureStdout(subtest, func() {
				_, runError = executeCommand("cluster", "aws", verb,
					"--name", "prod", "--credential-id", "cred-aws",
					"--ssh-key-credential-id", "cred-ssh", "--region", "eu-north-1")
			})
			if runError != nil {
				subtest.Fatalf("%s without --bastion-allowed-ips: %v", verb, runError)
			}
			request := mock.createRequest
			if verb == "preflight" {
				request = mock.preflightRequest
			}
			if request == nil {
				subtest.Fatal("the request never reached the client")
			}
			if request.BastionAllowedIPs == nil || len(request.BastionAllowedIPs) != 0 {
				subtest.Fatalf("bastion_allowed_ips = %#v, want an empty (not nil) list", request.BastionAllowedIPs)
			}
			body, marshalError := json.Marshal(request)
			if marshalError != nil {
				subtest.Fatal(marshalError)
			}
			if !strings.Contains(string(body), `"bastion_allowed_ips":[]`) {
				subtest.Fatalf("the wire must carry [] for the platform default, got %s", body)
			}
		})
	}
}
