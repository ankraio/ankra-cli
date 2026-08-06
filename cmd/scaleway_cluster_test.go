package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// newScalewayCreateTestCommand builds a bare command carrying the create flag
// set so scalewayCreateRequestFromFlags can be exercised without running the
// full cobra dispatch (and without any API call). Stderr is redirected so the
// advisory runtime-credential warning does not leak into test output.
func newScalewayCreateTestCommand(t *testing.T, flags map[string]string, repeated map[string][]string) *cobra.Command {
	t.Helper()
	command := &cobra.Command{Use: "create"}
	addScalewayCreateFlags(command)
	command.SetErr(new(bytes.Buffer))
	for name, value := range flags {
		if err := command.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s=%q: %v", name, value, err)
		}
	}
	for name, values := range repeated {
		for _, value := range values {
			if err := command.Flags().Set(name, value); err != nil {
				t.Fatalf("set --%s=%q: %v", name, value, err)
			}
		}
	}
	return command
}

func TestScalewayCreateRequestFromFlagsBuildsRequest(t *testing.T) {
	command := newScalewayCreateTestCommand(t, map[string]string{
		"name":                  "prod",
		"credential-id":         "cred",
		"ssh-key-credential-id": "ssh",
		"region":                "fr-par",
		"zone":                  "fr-par-1",
		"private-network-id":    "pn-1",
		"gateway-allowed-ips":   "203.0.113.0/24",
		"runtime-credential-id": "runtime",
		"kubernetes-version":    "v1.30.0",
		"control-plane-count":   "3",
		"worker-count":          "2",
		"cni":                   "cilium",
		"cni-hubble":            "true",
	}, map[string][]string{
		"node-group": {"blue:DEV1-M:2"},
	})

	request, err := scalewayCreateRequestFromFlags(command)
	if err != nil {
		t.Fatalf("scalewayCreateRequestFromFlags: %v", err)
	}

	if request.Name != "prod" || request.CredentialID != "cred" || request.SSHKeyCredentialID != "ssh" {
		t.Fatalf("identity fields = %+v", request)
	}
	if request.Region != "fr-par" || request.Zone != "fr-par-1" {
		t.Fatalf("location fields = %+v", request)
	}
	if request.PrivateNetworkID == nil || *request.PrivateNetworkID != "pn-1" {
		t.Fatalf("private network id = %v", request.PrivateNetworkID)
	}
	if request.NetworkIPRange != nil {
		t.Fatalf("network ip range should be unset in existing-network mode: %v", *request.NetworkIPRange)
	}
	if request.RuntimeCredentialID == nil || *request.RuntimeCredentialID != "runtime" {
		t.Fatalf("runtime credential id = %v", request.RuntimeCredentialID)
	}
	if request.KubernetesVersion == nil || *request.KubernetesVersion != "v1.30.0" {
		t.Fatalf("kubernetes version = %v", request.KubernetesVersion)
	}
	if len(request.GatewayAllowedIPs) != 1 || request.GatewayAllowedIPs[0] != "203.0.113.0/24" {
		t.Fatalf("gateway allowed ips = %v", request.GatewayAllowedIPs)
	}
	if request.ControlPlaneCount != 3 || request.WorkerCount != 2 {
		t.Fatalf("counts = cp:%d worker:%d", request.ControlPlaneCount, request.WorkerCount)
	}
	if request.CNI != "cilium" || !request.CNIFeatures.Hubble {
		t.Fatalf("cni = %q features = %+v", request.CNI, request.CNIFeatures)
	}
	if !request.ExternalCloudProvider {
		t.Fatal("ExternalCloudProvider should be forced true")
	}
	if len(request.NodeGroups) != 1 {
		t.Fatalf("node groups = %+v", request.NodeGroups)
	}
	group := request.NodeGroups[0]
	if group.Name != "blue" || group.InstanceType != "DEV1-M" || group.Count != 2 {
		t.Fatalf("node group = %+v", group)
	}
}

func TestScalewayCreateRequestFromFlagsNetworkModes(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{
			"name":                  "prod",
			"credential-id":         "cred",
			"ssh-key-credential-id": "ssh",
			"region":                "fr-par",
			"zone":                  "fr-par-1",
			"gateway-allowed-ips":   "203.0.113.0/24",
		}
	}

	t.Run("new network mode sets ip range", func(t *testing.T) {
		flags := base()
		flags["network-ip-range"] = "10.10.0.0/16"
		request, err := scalewayCreateRequestFromFlags(newScalewayCreateTestCommand(t, flags, nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if request.NetworkIPRange == nil || *request.NetworkIPRange != "10.10.0.0/16" {
			t.Fatalf("network ip range = %v", request.NetworkIPRange)
		}
		if request.PrivateNetworkID != nil {
			t.Fatalf("private network id should be unset in new-network mode: %v", *request.PrivateNetworkID)
		}
	})

	t.Run("both network flags are mutually exclusive", func(t *testing.T) {
		flags := base()
		flags["private-network-id"] = "pn-1"
		flags["network-ip-range"] = "10.10.0.0/16"
		_, err := scalewayCreateRequestFromFlags(newScalewayCreateTestCommand(t, flags, nil))
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("one network mode is required", func(t *testing.T) {
		_, err := scalewayCreateRequestFromFlags(newScalewayCreateTestCommand(t, base(), nil))
		if err == nil || !strings.Contains(err.Error(), "existing network mode") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestScalewayCreateRequestFromFlagsRequiresGatewayAllowedIPs(t *testing.T) {
	flags := map[string]string{
		"name":                  "prod",
		"credential-id":         "cred",
		"ssh-key-credential-id": "ssh",
		"region":                "fr-par",
		"zone":                  "fr-par-1",
		"private-network-id":    "pn-1",
	}
	_, err := scalewayCreateRequestFromFlags(newScalewayCreateTestCommand(t, flags, nil))
	if err == nil || !strings.Contains(err.Error(), "gateway-allowed-ips") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateGatewayAllowedIPs(t *testing.T) {
	if err := validateGatewayAllowedIPs([]string{"203.0.113.0/24", "2001:db8::/32", " 198.51.100.7/32 "}); err != nil {
		t.Fatalf("valid CIDRs rejected: %v", err)
	}
	if err := validateGatewayAllowedIPs(nil); err == nil {
		t.Fatal("empty list must be rejected")
	}
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{"ipv4 world-open", "0.0.0.0/0", "world-open"},
		{"ipv6 world-open", "::/0", "world-open"},
		{"zero prefix equivalent", "10.0.0.0/0", "world-open"},
		{"malformed", "not-a-cidr", "not a valid CIDR"},
		{"bare ip without prefix", "203.0.113.5", "not a valid CIDR"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateGatewayAllowedIPs([]string{"203.0.113.0/24", test.value})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestScalewayCreateRequestFromFlagsRejectsWorldOpenGatewayCIDR(t *testing.T) {
	for _, worldOpen := range []string{"0.0.0.0/0", "::/0"} {
		flags := map[string]string{
			"name":                  "prod",
			"credential-id":         "cred",
			"ssh-key-credential-id": "ssh",
			"region":                "fr-par",
			"zone":                  "fr-par-1",
			"private-network-id":    "pn-1",
			"gateway-allowed-ips":   worldOpen,
		}
		_, err := scalewayCreateRequestFromFlags(newScalewayCreateTestCommand(t, flags, nil))
		if err == nil || !strings.Contains(err.Error(), "world-open") {
			t.Fatalf("error for %q = %v", worldOpen, err)
		}
	}
}

func TestScalewayCreateRequestFromFlagsRejectsMalformedGatewayCIDR(t *testing.T) {
	flags := map[string]string{
		"name":                  "prod",
		"credential-id":         "cred",
		"ssh-key-credential-id": "ssh",
		"region":                "fr-par",
		"zone":                  "fr-par-1",
		"private-network-id":    "pn-1",
		"gateway-allowed-ips":   "not-a-cidr",
	}
	_, err := scalewayCreateRequestFromFlags(newScalewayCreateTestCommand(t, flags, nil))
	if err == nil || !strings.Contains(err.Error(), "not a valid CIDR") {
		t.Fatalf("error = %v", err)
	}
}

func TestScalewayCreateRequestFromFlagsRejectsInvalidCNIFeatureCombo(t *testing.T) {
	flags := map[string]string{
		"name":                  "prod",
		"credential-id":         "cred",
		"ssh-key-credential-id": "ssh",
		"region":                "fr-par",
		"zone":                  "fr-par-1",
		"private-network-id":    "pn-1",
		"gateway-allowed-ips":   "203.0.113.0/24",
		"cni":                   "flannel",
		"cni-hubble":            "true",
	}
	_, err := scalewayCreateRequestFromFlags(newScalewayCreateTestCommand(t, flags, nil))
	if err == nil || !strings.Contains(err.Error(), "flannel does not support") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseScalewayNodeGroups(t *testing.T) {
	valid, err := parseScalewayNodeGroups([]string{"blue:DEV1-M:2", "green:DEV1-L:0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(valid) != 2 {
		t.Fatalf("groups = %+v", valid)
	}
	if valid[0].Name != "blue" || valid[0].InstanceType != "DEV1-M" || valid[0].Count != 2 {
		t.Fatalf("group 0 = %+v", valid[0])
	}
	if valid[1].Name != "green" || valid[1].Count != 0 {
		t.Fatalf("group 1 = %+v", valid[1])
	}

	for _, bad := range []string{"blue:DEV1-M", "blue:DEV1-M:2:extra", "blue:DEV1-M:notanumber", "blue:DEV1-M:-1", ":DEV1-M:2", "blue::2"} {
		if _, err := parseScalewayNodeGroups([]string{bad}); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

type scalewayLifecycleMock struct{ baseMock }

func (scalewayLifecycleMock) StopScalewayCluster(id string) (*client.ScalewayLifecycleResponse, error) {
	operationID := "operation-stop"
	return &client.ScalewayLifecycleResponse{Success: true, ClusterID: id, OperationID: &operationID}, nil
}
func (scalewayLifecycleMock) StartScalewayCluster(string, string) (*client.StartUpcloudClusterResult, error) {
	return &client.StartUpcloudClusterResult{CreatedOperations: 2}, nil
}
func (scalewayLifecycleMock) GetScalewayWorkerCount(string) (*client.WorkerCountResult, error) {
	return &client.WorkerCountResult{WorkerCount: 3, Min: 1, Max: 5}, nil
}

func TestScalewayStopStartWorkersHumanOutput(t *testing.T) {
	setMockClient(t, scalewayLifecycleMock{})
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"stop", []string{"cluster", "scaleway", "stop", "cluster-id"}, "Scaleway cluster stop initiated (operation operation-stop)."},
		{"start", []string{"cluster", "scaleway", "start", "cluster-id"}, "Scaleway start created 2 operation(s)."},
		{"workers", []string{"cluster", "scaleway", "workers", "cluster-id"}, "Worker count: 3 (min 1, max 5)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := captureStdout(t, func() {
				if _, err := executeCommand(test.args...); err != nil {
					t.Fatalf("execute: %v", err)
				}
			})
			if !strings.Contains(output, test.want) {
				t.Fatalf("missing %q in:\n%s", test.want, output)
			}
		})
	}
}

type scalewayStartScopeMock struct {
	baseMock
	scopes *[]string
}

func (m scalewayStartScopeMock) StartScalewayCluster(_ string, scope string) (*client.StartUpcloudClusterResult, error) {
	*m.scopes = append(*m.scopes, scope)
	return &client.StartUpcloudClusterResult{CreatedOperations: 1, Scope: scope}, nil
}

func TestScalewayStartScopeFlag(t *testing.T) {
	scopes := []string{}
	setMockClient(t, scalewayStartScopeMock{scopes: &scopes})
	// The start command is a package-level singleton: restore the flag
	// default so later tests are unaffected by explicit --scope values.
	t.Cleanup(func() {
		for _, candidate := range scalewayCmd.Commands() {
			if strings.HasPrefix(candidate.Use, "start ") {
				_ = candidate.Flags().Set("scope", "all")
			}
		}
	})

	if _, err := executeCommand("cluster", "scaleway", "start", "cluster-id", "--scope", "bogus"); err == nil ||
		!strings.Contains(err.Error(), "invalid --scope") {
		t.Fatalf("invalid scope error = %v", err)
	}
	if len(scopes) != 0 {
		t.Fatalf("API must not be called for an invalid scope, got %v", scopes)
	}

	output := captureStdout(t, func() {
		if _, err := executeCommand("cluster", "scaleway", "start", "cluster-id", "--scope", "control_plane"); err != nil {
			t.Fatalf("execute control_plane: %v", err)
		}
		if _, err := executeCommand("cluster", "scaleway", "start", "cluster-id", "--scope", "all"); err != nil {
			t.Fatalf("execute all: %v", err)
		}
	})
	if len(scopes) != 2 || scopes[0] != "control_plane" || scopes[1] != "all" {
		t.Fatalf("scopes passed to API = %v", scopes)
	}
	if !strings.Contains(output, "Scaleway start created 1 operation(s).") {
		t.Fatalf("output = %s", output)
	}
}

type scalewayNodeGroupListMock struct{ baseMock }

func (scalewayNodeGroupListMock) ListScalewayNodeGroups(string) (*client.NodeGroupListResult, error) {
	return &client.NodeGroupListResult{NodeGroups: []client.NodeGroupInfo{
		{Name: "blue", InstanceType: "DEV1-M", Count: 2, Labels: map[string]string{"role": "web"}},
	}}, nil
}

func TestScalewayNodeGroupListHumanOutput(t *testing.T) {
	setMockClient(t, scalewayNodeGroupListMock{})
	output := captureStdout(t, func() {
		if _, err := executeCommand("cluster", "scaleway", "node-group", "list", "cluster-id"); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(output, "blue") || !strings.Contains(output, "type=DEV1-M") || !strings.Contains(output, "count=2") {
		t.Fatalf("output = %s", output)
	}
}

type scalewayInstanceTypesMock struct{ baseMock }

func (scalewayInstanceTypesMock) ListScalewayInstanceTypes(_ context.Context, _, _ string) (*client.ScalewayCatalogResult, error) {
	return &client.ScalewayCatalogResult{
		InstanceTypes:     []client.ScalewayInstanceType{{Name: "DEV1-M", VCPUs: 3, MemoryBytes: 4 << 30, MonthlyEUR: 12.34, Available: true}},
		StoragePrices:     []client.ScalewayStoragePrice{{Type: "sbs_5k", GBMonthEUR: 0.08}},
		PricingComplete:   false,
		IncompleteReasons: []string{"pricing api throttled"},
	}, nil
}

func TestScalewayInstanceTypesCatalogHumanOutput(t *testing.T) {
	setMockClient(t, scalewayInstanceTypesMock{})
	// Human rows are written to os.Stdout (captureStdout); the incomplete-pricing
	// warning is written to the command's Err stream, which executeCommand
	// returns as its string.
	var errStream string
	var execErr error
	human := captureStdout(t, func() {
		errStream, execErr = executeCommand("cluster", "scaleway", "instance-types", "--credential-id", "cred", "--zone", "fr-par-1")
	})
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}
	if !strings.Contains(human, "DEV1-M") || !strings.Contains(human, "monthly_eur=12.34") {
		t.Fatalf("instance type line missing:\n%s", human)
	}
	if !strings.Contains(human, "storage sbs_5k") {
		t.Fatalf("storage price line missing:\n%s", human)
	}
	if !strings.Contains(errStream, "pricing is incomplete") || !strings.Contains(errStream, "pricing api throttled") {
		t.Fatalf("incomplete-pricing warning missing:\n%s", errStream)
	}
}
