package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type managedClusterMock struct {
	baseMock

	createProvider client.ManagedK8sProvider
	createRequests []client.CreateManagedClusterRequest
	createError    error

	addPoolProvider client.ManagedK8sProvider
	addPoolRequests []client.AddManagedNodePoolRequest

	updatePoolProvider client.ManagedK8sProvider
	updatePoolCluster  string
	updatePoolName     string
	updatePoolRequests []client.UpdateManagedNodePoolRequest
	updatePoolError    error

	stopCalls []string
	stopError error

	startCalls []string
	startError error
}

func (m *managedClusterMock) CreateManagedCluster(provider client.ManagedK8sProvider, request client.CreateManagedClusterRequest) (*client.CreateManagedClusterResponse, error) {
	m.createProvider = provider
	m.createRequests = append(m.createRequests, request)
	if m.createError != nil {
		return nil, m.createError
	}
	return &client.CreateManagedClusterResponse{ClusterID: "cluster-1", Name: request.Name}, nil
}

func (m *managedClusterMock) AddManagedNodePool(provider client.ManagedK8sProvider, clusterID string, request client.AddManagedNodePoolRequest) (*client.AddManagedNodePoolResponse, error) {
	m.addPoolProvider = provider
	m.addPoolRequests = append(m.addPoolRequests, request)
	return &client.AddManagedNodePoolResponse{ClusterID: clusterID, NodePoolName: request.Name, Count: request.Count}, nil
}

func (m *managedClusterMock) UpdateManagedNodePool(provider client.ManagedK8sProvider, clusterID, nodePoolName string, request client.UpdateManagedNodePoolRequest) (*client.UpdateManagedNodePoolResponse, error) {
	m.updatePoolProvider = provider
	m.updatePoolCluster = clusterID
	m.updatePoolName = nodePoolName
	m.updatePoolRequests = append(m.updatePoolRequests, request)
	if m.updatePoolError != nil {
		return nil, m.updatePoolError
	}
	response := &client.UpdateManagedNodePoolResponse{ClusterID: clusterID, NodePoolName: nodePoolName}
	response.Count = request.Count
	response.AutoscalingEnabled = request.AutoscalingEnabled
	response.AutoscalingMin = request.AutoscalingMin
	response.AutoscalingMax = request.AutoscalingMax
	return response, nil
}

func (m *managedClusterMock) StopManagedCluster(provider client.ManagedK8sProvider, clusterID string) (*client.ManagedClusterLifecycleResponse, error) {
	m.stopCalls = append(m.stopCalls, clusterID)
	if m.stopError != nil {
		return nil, m.stopError
	}
	return &client.ManagedClusterLifecycleResponse{ClusterID: clusterID, Status: "stopping"}, nil
}

func (m *managedClusterMock) StartManagedCluster(provider client.ManagedK8sProvider, clusterID string) (*client.ManagedClusterLifecycleResponse, error) {
	m.startCalls = append(m.startCalls, clusterID)
	if m.startError != nil {
		return nil, m.startError
	}
	return &client.ManagedClusterLifecycleResponse{ClusterID: clusterID, Status: "starting"}, nil
}

func TestParseManagedProviderFlag_AcceptsAllProviders(t *testing.T) {
	cases := []struct {
		input string
		want  client.ManagedK8sProvider
	}{
		{input: "doks", want: client.ManagedK8sProviderDoks},
		{input: "uks", want: client.ManagedK8sProviderUks},
		{input: "gke", want: client.ManagedK8sProviderGke},
		{input: "ovh_mks", want: client.ManagedK8sProviderOvhMks},
		{input: "ovh-mks", want: client.ManagedK8sProviderOvhMks},
		{input: "mks", want: client.ManagedK8sProviderOvhMks},
		{input: "aks", want: client.ManagedK8sProviderAks},
		{input: "eks", want: client.ManagedK8sProviderEks},
		{input: "kapsule", want: client.ManagedK8sProviderKapsule},
		{input: "KAPSULE", want: client.ManagedK8sProviderKapsule},
	}
	for _, testCase := range cases {
		t.Run(testCase.input, func(t *testing.T) {
			resetConfirmFlag(t, managedStopCmd)
			if err := managedStopCmd.Flags().Set("provider", testCase.input); err != nil {
				t.Fatalf("set provider: %v", err)
			}
			got, err := parseManagedProviderFlag(managedStopCmd)
			if err != nil {
				t.Fatalf("parseManagedProviderFlag(%q): %v", testCase.input, err)
			}
			if got != testCase.want {
				t.Errorf("provider = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestParseManagedProviderFlag_RejectsUnknownProvider(t *testing.T) {
	resetConfirmFlag(t, managedStopCmd)
	if err := managedStopCmd.Flags().Set("provider", "openshift"); err != nil {
		t.Fatalf("set provider: %v", err)
	}
	_, err := parseManagedProviderFlag(managedStopCmd)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "kapsule") {
		t.Errorf("error should list kapsule as a valid provider, got: %v", err)
	}
}

func managedCreateArgs(extra ...string) []string {
	args := []string{
		"cluster", "managed", "create",
		"--name", "demo",
		"--credential-id", "cred-1",
		"--location", "fr-par",
		"--node-pool-size", "DEV1-M",
	}
	return append(args, extra...)
}

func TestManagedCreate_KapsuleSendsPrivateNetwork(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedCreateCmd)
	out, err := runWithInput(t, mock, "",
		managedCreateArgs("--provider", "kapsule", "--private-network-id", "pn-123")...)
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.createRequests) != 1 {
		t.Fatalf("create calls = %d, want 1", len(mock.createRequests))
	}
	if mock.createProvider != client.ManagedK8sProviderKapsule {
		t.Errorf("provider = %q, want kapsule", mock.createProvider)
	}
	request := mock.createRequests[0]
	if request.Kapsule == nil || request.Kapsule.PrivateNetworkID != "pn-123" {
		t.Errorf("kapsule options = %+v, want private network pn-123", request.Kapsule)
	}
}

func TestManagedCreate_KapsuleRequiresPrivateNetworkID(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedCreateCmd)
	_, err := runWithInput(t, mock, "", managedCreateArgs("--provider", "kapsule")...)
	if err == nil {
		t.Fatal("expected usage error without --private-network-id")
	}
	if exitCodeFor(err) != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", exitCodeFor(err), exitUsage)
	}
	if !strings.Contains(err.Error(), "--private-network-id") {
		t.Errorf("error should mention --private-network-id, got: %v", err)
	}
	if len(mock.createRequests) != 0 {
		t.Errorf("expected no API call, got %d", len(mock.createRequests))
	}
}

func TestManagedCreate_PrivateNetworkIDRejectedForOtherProviders(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedCreateCmd)
	_, err := runWithInput(t, mock, "",
		managedCreateArgs("--provider", "doks", "--private-network-id", "pn-123")...)
	if err == nil {
		t.Fatal("expected usage error for --private-network-id with doks")
	}
	if exitCodeFor(err) != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", exitCodeFor(err), exitUsage)
	}
	if len(mock.createRequests) != 0 {
		t.Errorf("expected no API call, got %d", len(mock.createRequests))
	}
}

func TestManagedCreate_AutoscalingFlagsBuildPoolSpec(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedCreateCmd)
	out, err := runWithInput(t, mock, "",
		managedCreateArgs("--provider", "doks", "--node-pool-count", "2",
			"--autoscaling", "--autoscaling-min", "1", "--autoscaling-max", "5")...)
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.createRequests) != 1 {
		t.Fatalf("create calls = %d, want 1", len(mock.createRequests))
	}
	autoscaling := mock.createRequests[0].NodePools[0].Autoscaling
	if autoscaling == nil {
		t.Fatal("expected autoscaling on the pool spec")
	}
	if !autoscaling.Enabled || autoscaling.MinCount != 1 || autoscaling.MaxCount != 5 {
		t.Errorf("autoscaling = %+v, want enabled with min 1 max 5", autoscaling)
	}
}

func TestManagedCreate_AutoscalingUsageErrors(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
	}{
		{name: "bounds without autoscaling", extra: []string{"--autoscaling-min", "1"}},
		{name: "autoscaling without bounds", extra: []string{"--autoscaling"}},
		{name: "autoscaling with only min", extra: []string{"--autoscaling", "--autoscaling-min", "1"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &managedClusterMock{}
			resetConfirmFlag(t, managedCreateCmd)
			_, err := runWithInput(t, mock, "",
				managedCreateArgs(append([]string{"--provider", "doks"}, testCase.extra...)...)...)
			if err == nil {
				t.Fatal("expected usage error")
			}
			if exitCodeFor(err) != exitUsage {
				t.Errorf("exit code = %d, want %d (usage)", exitCodeFor(err), exitUsage)
			}
			if len(mock.createRequests) != 0 {
				t.Errorf("expected no API call, got %d", len(mock.createRequests))
			}
		})
	}
}

func TestManagedCreate_APIErrorPassesThrough(t *testing.T) {
	mock := &managedClusterMock{
		createError: client.NewUnexpectedResponseError(400, "uks does not support autoscaling"),
	}
	resetConfirmFlag(t, managedCreateCmd)
	_, err := runWithInput(t, mock, "",
		managedCreateArgs("--provider", "uks",
			"--autoscaling", "--autoscaling-min", "1", "--autoscaling-max", "3")...)
	if err == nil {
		t.Fatal("expected API error to surface")
	}
	if !strings.Contains(err.Error(), "uks does not support autoscaling") {
		t.Errorf("expected backend detail in error, got: %v", err)
	}
	if exitCodeFor(err) != exitError {
		t.Errorf("exit code = %d, want %d", exitCodeFor(err), exitError)
	}
}

func TestManagedNodePoolAdd_SendsAutoscaling(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedNodePoolAddCmd)
	out, err := runWithInput(t, mock, "",
		"cluster", "managed", "node-pool", "add", "cluster-1",
		"--provider", "gke", "--name", "workers", "--size", "e2-medium", "--count", "3",
		"--autoscaling", "--autoscaling-min", "2", "--autoscaling-max", "6")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.addPoolRequests) != 1 {
		t.Fatalf("add pool calls = %d, want 1", len(mock.addPoolRequests))
	}
	request := mock.addPoolRequests[0]
	if request.Autoscaling == nil || !request.Autoscaling.Enabled ||
		request.Autoscaling.MinCount != 2 || request.Autoscaling.MaxCount != 6 {
		t.Errorf("autoscaling = %+v, want enabled with min 2 max 6", request.Autoscaling)
	}
}

func TestManagedNodePoolAdd_WithoutAutoscalingOmitsIt(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedNodePoolAddCmd)
	_, err := runWithInput(t, mock, "",
		"cluster", "managed", "node-pool", "add", "cluster-1",
		"--provider", "gke", "--name", "workers", "--size", "e2-medium")
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if mock.addPoolRequests[0].Autoscaling != nil {
		t.Errorf("autoscaling = %+v, want nil", mock.addPoolRequests[0].Autoscaling)
	}
}

func TestManagedNodePoolUpdate_SendsChangedFlagsOnly(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedNodePoolUpdateCmd)
	out := captureStdout(t, func() {
		_, err := runWithInput(t, mock, "",
			"cluster", "managed", "node-pool", "update", "cluster-1", "workers",
			"--provider", "kapsule", "--count", "4")
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if len(mock.updatePoolRequests) != 1 {
		t.Fatalf("update calls = %d, want 1", len(mock.updatePoolRequests))
	}
	request := mock.updatePoolRequests[0]
	if request.Count == nil || *request.Count != 4 {
		t.Errorf("count = %v, want 4", request.Count)
	}
	if request.AutoscalingEnabled != nil || request.AutoscalingMin != nil || request.AutoscalingMax != nil {
		t.Errorf("autoscaling fields should be unset, got %+v", request)
	}
	if mock.updatePoolCluster != "cluster-1" || mock.updatePoolName != "workers" {
		t.Errorf("target = %s/%s, want cluster-1/workers", mock.updatePoolCluster, mock.updatePoolName)
	}
	if !strings.Contains(out, `Node pool "workers" updated on cluster cluster-1`) {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestManagedNodePoolUpdate_SendsAutoscalingBounds(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedNodePoolUpdateCmd)
	_, err := runWithInput(t, mock, "",
		"cluster", "managed", "node-pool", "update", "cluster-1", "workers",
		"--provider", "kapsule", "--autoscaling", "--autoscaling-min", "1", "--autoscaling-max", "9")
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	request := mock.updatePoolRequests[0]
	if request.AutoscalingEnabled == nil || !*request.AutoscalingEnabled {
		t.Errorf("autoscaling_enabled = %v, want true", request.AutoscalingEnabled)
	}
	if request.AutoscalingMin == nil || *request.AutoscalingMin != 1 {
		t.Errorf("autoscaling_min = %v, want 1", request.AutoscalingMin)
	}
	if request.AutoscalingMax == nil || *request.AutoscalingMax != 9 {
		t.Errorf("autoscaling_max = %v, want 9", request.AutoscalingMax)
	}
	if request.Count != nil {
		t.Errorf("count should be unset, got %v", request.Count)
	}
}

func TestManagedNodePoolUpdate_DisableAutoscaling(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedNodePoolUpdateCmd)
	_, err := runWithInput(t, mock, "",
		"cluster", "managed", "node-pool", "update", "cluster-1", "workers",
		"--provider", "kapsule", "--autoscaling=false")
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	request := mock.updatePoolRequests[0]
	if request.AutoscalingEnabled == nil || *request.AutoscalingEnabled {
		t.Errorf("autoscaling_enabled = %v, want false", request.AutoscalingEnabled)
	}
}

func TestManagedNodePoolUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedNodePoolUpdateCmd)
	_, err := runWithInput(t, mock, "",
		"cluster", "managed", "node-pool", "update", "cluster-1", "workers",
		"--provider", "kapsule")
	if err == nil {
		t.Fatal("expected usage error without update flags")
	}
	if exitCodeFor(err) != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", exitCodeFor(err), exitUsage)
	}
	if len(mock.updatePoolRequests) != 0 {
		t.Errorf("expected no API call, got %d", len(mock.updatePoolRequests))
	}
}

func TestManagedNodePoolUpdate_APIErrorPassesThrough(t *testing.T) {
	mock := &managedClusterMock{
		updatePoolError: client.NewUnexpectedResponseError(409, "node pool is not in an updatable state"),
	}
	resetConfirmFlag(t, managedNodePoolUpdateCmd)
	_, err := runWithInput(t, mock, "",
		"cluster", "managed", "node-pool", "update", "cluster-1", "workers",
		"--provider", "kapsule", "--count", "2")
	if err == nil {
		t.Fatal("expected API error to surface")
	}
	if !strings.Contains(err.Error(), "updating node pool") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestManagedStop_Success(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedStopCmd)
	out := captureStdout(t, func() {
		_, err := runWithInput(t, mock, "",
			"cluster", "managed", "stop", "cluster-1", "--provider", "aks")
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if len(mock.stopCalls) != 1 || mock.stopCalls[0] != "cluster-1" {
		t.Fatalf("stop calls = %v, want [cluster-1]", mock.stopCalls)
	}
	if !strings.Contains(out, "Managed cluster stop initiated.") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if !strings.Contains(out, "Status: stopping") {
		t.Errorf("expected status in output, got:\n%s", out)
	}
}

func TestManagedStart_Success(t *testing.T) {
	mock := &managedClusterMock{}
	resetConfirmFlag(t, managedStartCmd)
	out := captureStdout(t, func() {
		_, err := runWithInput(t, mock, "",
			"cluster", "managed", "start", "cluster-1", "--provider", "aks")
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if len(mock.startCalls) != 1 || mock.startCalls[0] != "cluster-1" {
		t.Fatalf("start calls = %v, want [cluster-1]", mock.startCalls)
	}
	if !strings.Contains(out, "Managed cluster start initiated.") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestManagedStop_UnsupportedProviderMentionsAKS(t *testing.T) {
	mock := &managedClusterMock{
		stopError: client.NewUnexpectedResponseError(400, "provider gke does not support cluster stop"),
	}
	resetConfirmFlag(t, managedStopCmd)
	_, err := runWithInput(t, mock, "",
		"cluster", "managed", "stop", "cluster-1", "--provider", "gke")
	if err == nil {
		t.Fatal("expected refusal to surface as an error")
	}
	if !strings.Contains(err.Error(), "only AKS currently supports managed cluster stop/start") {
		t.Errorf("expected AKS hint, got: %v", err)
	}
}

func TestManagedStart_UnsupportedProviderMentionsAKS(t *testing.T) {
	mock := &managedClusterMock{
		startError: client.NewUnexpectedResponseError(400, "provider doks does not support cluster start"),
	}
	resetConfirmFlag(t, managedStartCmd)
	_, err := runWithInput(t, mock, "",
		"cluster", "managed", "start", "cluster-1", "--provider", "doks")
	if err == nil {
		t.Fatal("expected refusal to surface as an error")
	}
	if !strings.Contains(err.Error(), "only AKS currently supports managed cluster stop/start") {
		t.Errorf("expected AKS hint, got: %v", err)
	}
}

func TestManagedStop_UnrelatedErrorHasNoAKSHint(t *testing.T) {
	mock := &managedClusterMock{
		stopError: errors.New("connection refused"),
	}
	resetConfirmFlag(t, managedStopCmd)
	_, err := runWithInput(t, mock, "",
		"cluster", "managed", "stop", "cluster-1", "--provider", "aks")
	if err == nil {
		t.Fatal("expected error to surface")
	}
	if strings.Contains(err.Error(), "only AKS") {
		t.Errorf("AKS hint should only appear on provider refusals, got: %v", err)
	}
}
