package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func TestDecodeManagedCreateFileStrictJSONPreservesSemantics(t *testing.T) {
	input := []byte(`{
	  "name":"prod",
	  "credential_id":"cred",
	  "location":"fr-par",
	  "cni":"cilium_native",
	  "node_pools":[{"name":"default","size":"DEV1-M","count":1,"public_ip":false}],
	  "kapsule":{"private_network_id":"pn-1","private_endpoint":null},
	  "ha":null
	}`)
	raw, document, err := decodeManagedCreateFile(input, "request.json")
	if err != nil {
		t.Fatal(err)
	}
	if document.CNI == nil || *document.CNI != "cilium_native" {
		t.Fatalf("CNI = %#v", document.CNI)
	}
	if string(raw) != string(input) {
		t.Fatalf("JSON semantics not preserved:\n%s", raw)
	}
}

func TestValidateManagedCreateFileRejectsHyphenatedRootVolumeType(t *testing.T) {
	input := []byte(`{
	  "name":"prod",
	  "credential_id":"cred",
	  "location":"fr-par",
	  "node_pools":[{"name":"default","size":"DEV1-M","count":1,"root_volume_type":"sbs-5k"}],
	  "kapsule":{"private_network_id":"pn-1"}
	}`)
	_, _, err := decodeManagedCreateFile(input, "request.json")
	if err == nil || !strings.Contains(err.Error(), "use underscores") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateManagedCreateFileRejectsSmallRootVolume(t *testing.T) {
	input := []byte(`{
	  "name":"prod",
	  "credential_id":"cred",
	  "location":"fr-par",
	  "node_pools":[{"name":"default","size":"DEV1-M","count":1,"root_volume_type":"sbs_5k","root_volume_size_gb":10}],
	  "kapsule":{"private_network_id":"pn-1"}
	}`)
	_, _, err := decodeManagedCreateFile(input, "request.json")
	if err == nil || !strings.Contains(err.Error(), "root_volume_size_gb: must be at least 20") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeManagedCreateFileRejectsUnknownJSONAndYAMLFieldsWithLocation(t *testing.T) {
	jsonInput := []byte(`{"name":"x","credential_id":"c","location":"fr-par","node_pools":[{"name":"p","size":"s","count":1}],"kapsule":{"private_network_id":"pn"},"unknown":true}`)
	_, _, err := decodeManagedCreateFile(jsonInput, "bad.json")
	if err == nil || !strings.Contains(err.Error(), "$.unknown: unknown field") || !strings.Contains(err.Error(), "bad.json") {
		t.Fatalf("error = %v", err)
	}

	yamlInput := []byte("name: x\ncredential_id: c\nlocation: fr-par\nnode_pools:\n  - name: p\n    size: s\n    count: 1\nkapsule:\n  private_network_id: pn\nunknown: true\n")
	_, _, err = decodeManagedCreateFile(yamlInput, "bad.yaml")
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") || !strings.Contains(err.Error(), "line 10") {
		t.Fatalf("error = %v", err)
	}
}

func TestReadManagedRequestFileSupportsStdin(t *testing.T) {
	command := &cobra.Command{Use: "test"}
	command.Flags().String("file", "", "")
	if err := command.Flags().Set("file", "-"); err != nil {
		t.Fatal(err)
	}
	command.SetIn(strings.NewReader(`{"name":"stdin"}`))
	data, source, err := readManagedRequestFile(command)
	if err != nil {
		t.Fatal(err)
	}
	if source != "stdin" || string(data) != `{"name":"stdin"}` {
		t.Fatalf("source=%q data=%s", source, data)
	}
}

func TestManagedRequestDefaultsPreserveOmissionAndExplicitValues(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		input            string
		wantCount        *int
		wantEnabled      *bool
		wantGitopsBranch *string
	}{
		{
			name: "json omitted defaults", source: "request.json",
			input: `{"name":"prod","credential_id":"cred","location":"fr-par","node_pools":[{"name":"default","size":"DEV1-M","autoscaling":{"min_count":1,"max_count":3}}],"kapsule":{"private_network_id":"pn-1"}}`,
		},
		{
			name: "json explicit defaults", source: "request.json",
			input:     `{"name":"prod","credential_id":"cred","location":"fr-par","gitops_branch":"","node_pools":[{"name":"default","size":"DEV1-M","count":1,"autoscaling":{"enabled":false,"min_count":1,"max_count":3}}],"kapsule":{"private_network_id":"pn-1"}}`,
			wantCount: intPointer(1), wantEnabled: boolPointer(false), wantGitopsBranch: stringPointer(""),
		},
		{
			name: "yaml omitted defaults", source: "request.yaml",
			input: "name: prod\ncredential_id: cred\nlocation: fr-par\nnode_pools:\n  - name: default\n    size: DEV1-M\n    autoscaling:\n      min_count: 1\n      max_count: 3\nkapsule:\n  private_network_id: pn-1\n",
		},
		{
			name: "yaml explicit defaults", source: "request.yaml",
			input:     "name: prod\ncredential_id: cred\nlocation: fr-par\ngitops_branch: \"\"\nnode_pools:\n  - name: default\n    size: DEV1-M\n    count: 1\n    autoscaling:\n      enabled: false\n      min_count: 1\n      max_count: 3\nkapsule:\n  private_network_id: pn-1\n",
			wantCount: intPointer(1), wantEnabled: boolPointer(false), wantGitopsBranch: stringPointer(""),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, document, err := decodeManagedCreateFile([]byte(test.input), test.source)
			if err != nil {
				t.Fatal(err)
			}
			pool := document.NodePools[0]
			assertOptionalInt(t, "count", pool.Count, test.wantCount)
			assertOptionalBool(t, "autoscaling.enabled", pool.Autoscaling.Enabled, test.wantEnabled)
			assertOptionalString(t, "gitops_branch", document.GitopsBranch, test.wantGitopsBranch)

			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			poolPayload := payload["node_pools"].([]any)[0].(map[string]any)
			_, hasCount := poolPayload["count"]
			if hasCount != (test.wantCount != nil) {
				t.Fatalf("count presence = %t, want %t; payload=%s", hasCount, test.wantCount != nil, raw)
			}
			autoscalingPayload := poolPayload["autoscaling"].(map[string]any)
			_, hasEnabled := autoscalingPayload["enabled"]
			if hasEnabled != (test.wantEnabled != nil) {
				t.Fatalf("enabled presence = %t, want %t; payload=%s", hasEnabled, test.wantEnabled != nil, raw)
			}
			_, hasBranch := payload["gitops_branch"]
			if hasBranch != (test.wantGitopsBranch != nil) {
				t.Fatalf("gitops_branch presence = %t, want %t; payload=%s", hasBranch, test.wantGitopsBranch != nil, raw)
			}
		})
	}
}

func TestManagedRequestDefaultsFromStdin(t *testing.T) {
	command := &cobra.Command{Use: "test"}
	command.Flags().String("file", "", "")
	_ = command.Flags().Set("file", "-")
	command.SetIn(strings.NewReader("name: prod\ncredential_id: cred\nlocation: fr-par\nnode_pools:\n  - name: default\n    size: DEV1-M\nkapsule:\n  private_network_id: pn-1\n"))
	data, source, err := readManagedRequestFile(command)
	if err != nil {
		t.Fatal(err)
	}
	raw, document, err := decodeManagedCreateFile(data, source)
	if err != nil {
		t.Fatal(err)
	}
	if document.NodePools[0].Count != nil {
		t.Fatalf("stdin omitted count became explicit: %s", raw)
	}
}

func TestManagedPoolFlagsPreserveOmittedCount(t *testing.T) {
	newCommand := func() *cobra.Command {
		command := &cobra.Command{Use: "pool"}
		command.Flags().String("name", "blue", "")
		command.Flags().String("size", "DEV1-M", "")
		command.Flags().Int("count", 1, "")
		command.Flags().String("labels", "", "")
		command.Flags().String("taints", "", "")
		command.Flags().String("zone", "", "")
		command.Flags().String("root-volume-type", "", "")
		command.Flags().Int("root-volume-size-gb", 0, "")
		command.Flags().String("security-group-id", "", "")
		command.Flags().Bool("public-ip", false, "")
		command.Flags().Bool("autohealing", true, "")
		command.Flags().Bool("autoscaling", false, "")
		command.Flags().Int("autoscaling-min", 1, "")
		command.Flags().Int("autoscaling-max", 5, "")
		command.Flags().String("upgrade-policy", "", "")
		return command
	}

	omitted, err := managedPoolRequestFromFlags(newCommand())
	if err != nil {
		t.Fatal(err)
	}
	if omitted.Count != nil {
		t.Fatalf("omitted --count became explicit: %v", *omitted.Count)
	}

	explicitCommand := newCommand()
	if err := explicitCommand.Flags().Set("count", "1"); err != nil {
		t.Fatal(err)
	}
	explicit, err := managedPoolRequestFromFlags(explicitCommand)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Count == nil || *explicit.Count != 1 {
		t.Fatalf("explicit --count was not preserved: %v", explicit.Count)
	}
}

func intPointer(value int) *int          { return &value }
func boolPointer(value bool) *bool       { return &value }
func stringPointer(value string) *string { return &value }

func assertOptionalInt(t *testing.T, field string, got, want *int) {
	t.Helper()
	if (got == nil) != (want == nil) || got != nil && *got != *want {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

func assertOptionalBool(t *testing.T, field string, got, want *bool) {
	t.Helper()
	if (got == nil) != (want == nil) || got != nil && *got != *want {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

func assertOptionalString(t *testing.T, field string, got, want *string) {
	t.Helper()
	if (got == nil) != (want == nil) || got != nil && *got != *want {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

func TestScalewayCNIValidationMatrix(t *testing.T) {
	tests := []struct {
		distribution string
		cni          string
		features     client.ScalewayCNIFeatures
		wantError    bool
	}{
		{"k3s", "flannel", client.ScalewayCNIFeatures{}, false},
		{"k3s", "calico", client.ScalewayCNIFeatures{EBPFDataplane: true}, false},
		{"k3s", "cilium", client.ScalewayCNIFeatures{Hubble: true, WireguardEncryption: true}, false},
		{"kubeadm", "cilium", client.ScalewayCNIFeatures{KubeProxyReplacement: true}, false},
		{"kubeadm", "flannel", client.ScalewayCNIFeatures{}, true},
		{"k3s", "calico", client.ScalewayCNIFeatures{Hubble: true}, true},
		{"k3s", "flannel", client.ScalewayCNIFeatures{WireguardEncryption: true}, true},
	}
	for _, test := range tests {
		err := validateScalewayCNI(test.distribution, test.cni, test.features)
		if (err != nil) != test.wantError {
			t.Errorf("%s/%s features=%#v error=%v", test.distribution, test.cni, test.features, err)
		}
	}
}

type scalewayAccessMock struct{ baseMock }

func (scalewayAccessMock) GetScalewayAccessInfo(string) (*client.ScalewayAccessInfo, error) {
	cp := "10.0.0.10"
	return &client.ScalewayAccessInfo{
		BastionHost: "203.0.113.10", BastionPort: 61000, BastionUser: "bastion", TargetUser: "root",
		ControlPlaneIP: &cp, ControlPlaneIPs: []string{cp},
	}, nil
}

func TestScalewayAccessInfoRendersNonDefaultUserAndPort(t *testing.T) {
	setMockClient(t, scalewayAccessMock{})
	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "scaleway", "access-info", "cluster-id")
	})
	for _, expected := range []string{
		"Bastion: bastion@203.0.113.10:61000",
		"ssh -J bastion@203.0.113.10:61000 root@10.0.0.10",
		"ssh -p 61000 -L 6443:10.0.0.10:6443 -N bastion@203.0.113.10",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("missing %q in:\n%s", expected, output)
		}
	}
}

type managedImportIncompleteMock struct{ baseMock }

func (managedImportIncompleteMock) ManagedDiscover(context.Context, string, string) (*client.ManagedDiscoveryResponse, error) {
	return &client.ManagedDiscoveryResponse{Incomplete: true, IncompleteRegions: []string{"nl-ams"}}, nil
}

func TestKapsuleImportBlocksIncompleteDiscovery(t *testing.T) {
	setMockClient(t, managedImportIncompleteMock{})
	_, err := executeCommand("cluster", "managed", "kapsule", "import",
		"--credential-id", "cred", "--provider-cluster-id", "regions/fr-par/clusters/id")
	if err == nil || !strings.Contains(err.Error(), "selection/import is blocked") {
		t.Fatalf("error = %v", err)
	}
}

type managedUpgradeMock struct{ baseMock }

func (managedUpgradeMock) ManagedUpgrade(context.Context, string, string, string) (*client.ManagedUpgradeResponse, error) {
	return &client.ManagedUpgradeResponse{ClusterID: "cluster", Version: "1.32", OperationID: "operation-123"}, nil
}

func TestKapsuleUpgradeRendersOperationID(t *testing.T) {
	setMockClient(t, managedUpgradeMock{})
	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "managed", "kapsule", "upgrade", "cluster", "1.32", "--yes")
	})
	if !strings.Contains(output, "operation-123") {
		t.Fatalf("output = %s", output)
	}
}

type managedImportedStatusMock struct{ baseMock }

func (managedImportedStatusMock) ManagedStatus(context.Context, string, string) (*client.ManagedStatusResponse, error) {
	return &client.ManagedStatusResponse{
		ClusterID: "cluster", ProviderClusterID: "regions/fr-par/clusters/provider-id",
		Ownership: client.ManagedProvenanceImported,
	}, nil
}

func TestKapsuleProviderDeleteRequiresForceForImportedOwnership(t *testing.T) {
	setMockClient(t, managedImportedStatusMock{})
	_, err := executeCommand("cluster", "managed", "kapsule", "delete-provider-cluster", "cluster", "--yes")
	if err == nil || !strings.Contains(err.Error(), "requires --force") {
		t.Fatalf("error = %v", err)
	}
}

type scalewayDispatchMock struct{ baseMock }

func (scalewayDispatchMock) GetClusterByID(string) (client.ClusterListItem, error) {
	return client.ClusterListItem{Kind: "scaleway"}, nil
}
func (scalewayDispatchMock) ScaleScalewayWorkers(_ string, count int) (*client.ScaleWorkersResult, error) {
	return &client.ScaleWorkersResult{PreviousCount: 1, NewCount: count}, nil
}

func TestGenericScaleDispatchesScaleway(t *testing.T) {
	setMockClient(t, scalewayDispatchMock{})
	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "scale", "cluster", "3")
	})
	if !strings.Contains(output, "Provider: scaleway") || !strings.Contains(output, "1 to 3") {
		t.Fatalf("output = %s", output)
	}
}

func TestManagedPoolUpdatePreservesExplicitFalseAndZero(t *testing.T) {
	command := &cobra.Command{Use: "test"}
	command.Flags().Bool("autohealing", true, "")
	command.Flags().Bool("autoscaling-enabled", true, "")
	command.Flags().Int("count", 1, "")
	command.Flags().Int("autoscaling-min", 1, "")
	command.Flags().Int("autoscaling-max", 1, "")
	command.Flags().Int("root-volume-size-gb", 1, "")
	command.Flags().Bool("public-ip", true, "")
	command.Flags().String("upgrade-policy", "", "")
	command.Flags().String("zone", "", "")
	command.Flags().String("root-volume-type", "", "")
	command.Flags().String("security-group-id", "", "")
	if err := command.Flags().Set("autohealing", "false"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("count", "0"); err != nil {
		t.Fatal(err)
	}
	request, err := managedPoolUpdateFromFlags(command)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(request)
	if string(encoded) != `{"count":0,"autohealing":false}` {
		t.Fatalf("payload = %s", encoded)
	}
}

type scalewayStructuredMutationMock struct{ baseMock }

func (scalewayStructuredMutationMock) DeprovisionScalewayCluster(id string, _ bool) (*client.ScalewayLifecycleResponse, error) {
	operationID := "operation-deprovision"
	return &client.ScalewayLifecycleResponse{Success: true, ClusterID: id, OperationID: &operationID}, nil
}
func (scalewayStructuredMutationMock) StopScalewayCluster(id string) (*client.ScalewayLifecycleResponse, error) {
	operationID := "operation-stop"
	return &client.ScalewayLifecycleResponse{Success: true, ClusterID: id, OperationID: &operationID}, nil
}
func (scalewayStructuredMutationMock) StartScalewayCluster(string) (*client.StartUpcloudClusterResult, error) {
	return &client.StartUpcloudClusterResult{CreatedOperations: 2}, nil
}
func (scalewayStructuredMutationMock) ScaleScalewayWorkers(_ string, count int) (*client.ScaleWorkersResult, error) {
	return &client.ScaleWorkersResult{PreviousCount: 1, NewCount: count}, nil
}
func (scalewayStructuredMutationMock) UpgradeScalewayK8sVersion(_ string, version string, _ bool) (*client.UpgradeK8sVersionResult, error) {
	operationID := "operation-upgrade"
	return &client.UpgradeK8sVersionResult{NewVersion: version, NodesAffected: 2, OperationID: &operationID}, nil
}
func (scalewayStructuredMutationMock) AddScalewayNodeGroupFull(context.Context, string, client.ScalewayCreateNodeGroupRequest, bool) (*client.AddNodeGroupResult, bool, error) {
	return nil, true, nil
}
func (scalewayStructuredMutationMock) ScaleScalewayNodeGroup(context.Context, string, string, int, bool) (*client.ScaleNodeGroupResult, bool, error) {
	return nil, true, nil
}
func (scalewayStructuredMutationMock) UpdateScalewayNodeGroupInstanceType(context.Context, string, string, string, bool) (*client.UpdateNodeGroupResult, bool, error) {
	return nil, true, nil
}
func (scalewayStructuredMutationMock) DeleteScalewayNodeGroup(context.Context, string, string, bool) (*client.DeleteNodeGroupResult, bool, error) {
	return nil, true, nil
}
func (scalewayStructuredMutationMock) UpdateScalewayNodeGroupLabels(context.Context, string, string, map[string]string, bool) (*client.UpdateNodeGroupResult, bool, error) {
	return nil, true, nil
}
func (scalewayStructuredMutationMock) UpdateScalewayNodeGroupTaints(context.Context, string, string, []client.NodeTaint, bool) (*client.UpdateNodeGroupResult, bool, error) {
	return nil, true, nil
}
func (scalewayStructuredMutationMock) UpdateScalewayNodeGroupAutoscaling(context.Context, string, string, client.NodeGroupAutoscalingRequest, bool) (*client.NodeGroupAutoscalingResult, bool, error) {
	return nil, true, nil
}
func (scalewayStructuredMutationMock) UpdateScalewayBastionInstanceType(context.Context, string, string, bool) (*client.UpdateBastionInstanceTypeResult, bool, error) {
	return nil, true, nil
}
func (scalewayStructuredMutationMock) UpdateScalewayClusterSSHKeys(string, []string) (*client.UpdateClusterSSHKeysResult, error) {
	return &client.UpdateClusterSSHKeysResult{SSHKeyCredentialIDs: []string{"key"}}, nil
}
func (scalewayStructuredMutationMock) ResyncScalewayClusterSSHKeys(string) (*client.ResyncSSHKeysResult, error) {
	return &client.ResyncSSHKeysResult{ResourceIDs: []string{"resource"}}, nil
}
func (scalewayStructuredMutationMock) ChangeScalewayControlPlaneCount(string, int) (*client.ChangeControlPlaneCountResult, error) {
	return &client.ChangeControlPlaneCountResult{PreviousCount: 1, NewCount: 3}, nil
}
func (scalewayStructuredMutationMock) RestartScalewayClusterNode(_, nodeID string) (*client.RestartNodeResult, error) {
	return &client.RestartNodeResult{NodeID: nodeID, OperationID: "operation-restart", JobName: "scaleway_restart_server"}, nil
}

func TestScalewayMutationsHonorStructuredOutput(t *testing.T) {
	setMockClient(t, scalewayStructuredMutationMock{})
	tests := []struct {
		name string
		args []string
	}{
		{"deprovision", []string{"cluster", "scaleway", "deprovision", "cluster", "--yes"}},
		{"stop", []string{"cluster", "scaleway", "stop", "cluster"}},
		{"start", []string{"cluster", "scaleway", "start", "cluster"}},
		{"worker scale", []string{"cluster", "scaleway", "scale", "cluster", "3"}},
		{"kubernetes upgrade", []string{"cluster", "scaleway", "upgrade", "cluster", "1.32"}},
		{"node group add async", []string{"cluster", "scaleway", "node-group", "add", "cluster", "--name", "blue", "--instance-type", "DEV1-M"}},
		{"node group scale async", []string{"cluster", "scaleway", "node-group", "scale", "cluster", "blue", "2"}},
		{"node group upgrade async", []string{"cluster", "scaleway", "node-group", "upgrade", "cluster", "blue", "DEV1-L"}},
		{"node group delete async", []string{"cluster", "scaleway", "node-group", "delete", "cluster", "blue", "--yes"}},
		{"labels async", []string{"cluster", "scaleway", "node-group", "labels", "cluster", "blue", "--labels", "role=web"}},
		{"taints async", []string{"cluster", "scaleway", "node-group", "taints", "cluster", "blue", "--taints", "dedicated=web:NoSchedule"}},
		{"autoscaling async", []string{"cluster", "scaleway", "node-group", "autoscaling", "set", "cluster", "blue", "--enabled=false"}},
		{"gateway resize async", []string{"cluster", "scaleway", "bastion", "resize", "cluster", "VPC-GW-M"}},
		{"ssh keys set", []string{"cluster", "scaleway", "ssh-keys", "set", "cluster", "--ssh-key-credential-ids", "key"}},
		{"ssh keys resync", []string{"cluster", "scaleway", "ssh-keys", "resync", "cluster"}},
		{"control plane", []string{"cluster", "scaleway", "control-plane", "set-count", "cluster", "3"}},
		{"node restart", []string{"cluster", "scaleway", "nodes", "restart", "cluster", "node"}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			format := "json"
			if index%2 == 1 {
				format = "yaml"
			}
			args := append(append([]string{}, test.args...), "--wait=false", "-o", format)
			// Commands without async flags reject --wait; retry those without it.
			var output string
			var err error
			humanOutput := captureStdout(t, func() {
				output, err = executeCommand(args...)
				if err != nil && strings.Contains(err.Error(), "unknown flag: --wait") {
					args = append(append([]string{}, test.args...), "-o", format)
					output, err = executeCommand(args...)
				}
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if strings.TrimSpace(humanOutput) != "" {
				t.Fatalf("human stdout leaked into %s output:\n%s", format, humanOutput)
			}
			var decoded map[string]any
			if format == "json" {
				err = json.Unmarshal([]byte(output), &decoded)
			} else {
				err = yaml.Unmarshal([]byte(output), &decoded)
			}
			if err != nil || len(decoded) == 0 {
				t.Fatalf("invalid %s output: %v\n%s", format, err, output)
			}
			for _, human := range []string{"submitted.\n", "The platform is applying", "Scaleway cluster stop initiated", "Node group \""} {
				if strings.Contains(output, human) {
					t.Fatalf("human text %q leaked into %s output:\n%s", human, format, output)
				}
			}
		})
	}
}

func TestAsyncSubmittedStructuredResultIncludesOperationIDWhenAvailable(t *testing.T) {
	command := &cobra.Command{Use: "mutation"}
	registerStructuredOutputFlags(command)
	if err := command.Flags().Set("output", "json"); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	command.SetOut(&output)
	operationID := "operation-123"
	if err := renderAsyncWriteSubmitted(command, "Scale", &operationID); err != nil {
		t.Fatal(err)
	}
	var result asyncSubmittedResult
	if err := json.Unmarshal([]byte(output.String()), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Submitted || result.Status != "accepted" || result.OperationID == nil || *result.OperationID != operationID {
		t.Fatalf("result = %#v", result)
	}
}

type managedStructuredMutationMock struct{ baseMock }

func (managedStructuredMutationMock) ManagedStatus(context.Context, string, string) (*client.ManagedStatusResponse, error) {
	return &client.ManagedStatusResponse{
		ClusterID: "cluster", ProviderClusterID: "regions/fr-par/clusters/provider",
		Ownership: client.ManagedProvenanceCreated,
	}, nil
}
func (managedStructuredMutationMock) ManagedDisconnect(context.Context, string, string, bool) (*client.ManagedDeprovisionResponse, error) {
	operationID := "operation-disconnect"
	return &client.ManagedDeprovisionResponse{Success: true, ClusterID: "cluster", OperationID: &operationID}, nil
}
func (managedStructuredMutationMock) ManagedDeleteProviderCluster(context.Context, string, string, bool, string) (*client.ManagedDeprovisionResponse, error) {
	operationID := "operation-delete"
	return &client.ManagedDeprovisionResponse{Success: true, ClusterID: "cluster", OperationID: &operationID}, nil
}
func (managedStructuredMutationMock) ManagedAddPool(context.Context, string, string, client.ManagedNodePoolRequest) (*client.ManagedPoolOperationResponse, error) {
	count := 1
	return &client.ManagedPoolOperationResponse{ClusterID: "cluster", NodePoolName: "blue", Count: &count}, nil
}
func (managedStructuredMutationMock) ManagedScalePool(context.Context, string, string, string, int) (*client.ManagedPoolOperationResponse, error) {
	count := 2
	return &client.ManagedPoolOperationResponse{ClusterID: "cluster", NodePoolName: "blue", Count: &count}, nil
}
func (managedStructuredMutationMock) ManagedUpdatePool(context.Context, string, string, string, client.ManagedPoolUpdateRequest) (*client.ManagedPoolOperationResponse, error) {
	enabled := false
	return &client.ManagedPoolOperationResponse{ClusterID: "cluster", NodePoolName: "blue", AutoscalingEnabled: &enabled}, nil
}
func (managedStructuredMutationMock) ManagedDeletePool(context.Context, string, string, string) (*client.ManagedPoolOperationResponse, error) {
	return &client.ManagedPoolOperationResponse{ClusterID: "cluster", NodePoolName: "blue"}, nil
}
func (managedStructuredMutationMock) ManagedUpgrade(context.Context, string, string, string) (*client.ManagedUpgradeResponse, error) {
	return &client.ManagedUpgradeResponse{ClusterID: "cluster", Version: "1.32", OperationID: "operation-upgrade"}, nil
}

func TestKapsuleMutationsHonorStructuredOutput(t *testing.T) {
	setMockClient(t, managedStructuredMutationMock{})
	tests := [][]string{
		{"cluster", "managed", "kapsule", "disconnect", "cluster", "--yes"},
		{"cluster", "managed", "kapsule", "delete-provider-cluster", "cluster", "--yes"},
		{"cluster", "managed", "kapsule", "pool", "add", "cluster", "--name", "blue", "--size", "DEV1-M"},
		{"cluster", "managed", "kapsule", "pool", "scale", "cluster", "blue", "2"},
		{"cluster", "managed", "kapsule", "pool", "update", "cluster", "blue", "--autoscaling-enabled=false"},
		{"cluster", "managed", "kapsule", "pool", "delete", "cluster", "blue", "--yes"},
		{"cluster", "managed", "kapsule", "upgrade", "cluster", "1.32", "--yes"},
	}
	for index, args := range tests {
		format := "json"
		if index%2 == 1 {
			format = "yaml"
		}
		var output string
		var err error
		humanOutput := captureStdout(t, func() {
			output, err = executeCommand(append(args, "-o", format)...)
		})
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.TrimSpace(humanOutput) != "" {
			t.Fatalf("%v leaked human stdout:\n%s", args, humanOutput)
		}
		if !strings.Contains(output, "cluster") {
			t.Fatalf("%v produced no typed structured output:\n%s", args, output)
		}
		for _, human := range []string{"Disconnected cluster", "Provider deletion initiated", "Pool \"", "Upgrade to"} {
			if strings.Contains(output, human) {
				t.Fatalf("%v leaked human text %q:\n%s", args, human, output)
			}
		}
	}
}
