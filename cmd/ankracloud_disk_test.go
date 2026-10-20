package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestAnkraCloudCreateAndPreflightSendTheDiskSizes(t *testing.T) {
	mock := &ankraCloudClusterMock{preflightResult: client.AnkraCloudPreflightResult{CanProceed: true}}
	diskArguments := []string{"--bastion-disk-gib", "10", "--control-plane-disk-gib", "40",
		"--etcd-disk-gib", "20", "--worker-disk-gib", "60"}
	for _, verb := range []string{"create", "preflight"} {
		arguments := append(append([]string{verb}, ankraCloudRequiredCreateArguments...), diskArguments...)
		if output, runError := runAnkraCloudCommand(t, mock, arguments...); runError != nil {
			t.Fatalf("%s: %v\n%s", verb, runError, output)
		}
	}
	for _, request := range []client.CreateAnkraCloudClusterRequest{mock.createRequests[0], mock.preflightRequests[0]} {
		if request.BastionDiskGibibytes != 10 || request.ControlPlaneDiskGibibytes != 40 ||
			request.EtcdDiskGibibytes != 20 || request.WorkerDiskGibibytes != 60 {
			t.Fatalf("request = %+v", request)
		}
	}
}

func TestAnkraCloudCreateOmitsUnsetDiskSizes(t *testing.T) {
	mock := &ankraCloudClusterMock{}
	if _, runError := runAnkraCloudCommand(t, mock, append([]string{"create"}, ankraCloudRequiredCreateArguments...)...); runError != nil {
		t.Fatalf("create: %v", runError)
	}
	body, marshalError := json.Marshal(mock.createRequests[0])
	if marshalError != nil {
		t.Fatal(marshalError)
	}
	if strings.Contains(string(body), "disk_gibibytes") {
		t.Fatalf("an unset disk size must be omitted so the plan's included disk applies: %s", body)
	}
}

func TestAnkraCloudCreateRefusesAnInvalidDiskSize(t *testing.T) {
	for _, value := range []string{"0", "-5", "4097"} {
		mock := &ankraCloudClusterMock{}
		arguments := append(append([]string{"create"}, ankraCloudRequiredCreateArguments...), "--worker-disk-gib", value)
		_, runError := runAnkraCloudCommand(t, mock, arguments...)
		if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--worker-disk-gib") {
			t.Fatalf("--worker-disk-gib %s: error = %v, want a usage error naming the flag", value, runError)
		}
		if len(mock.createRequests) != 0 {
			t.Fatalf("--worker-disk-gib %s: a request was sent", value)
		}
	}
}

type nodeGroupAddDiskMock struct {
	baseMock
	kind       string
	gotRequest *client.AddNodeGroupRequest
}

func (m *nodeGroupAddDiskMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: clusterID, Kind: m.kind}, nil
}

func (m *nodeGroupAddDiskMock) record(request client.AddNodeGroupRequest) (*client.AddNodeGroupResult, bool, error) {
	m.gotRequest = &request
	return &client.AddNodeGroupResult{GroupName: request.Name, Count: request.Count}, false, nil
}

func (m *nodeGroupAddDiskMock) AddAnkraCloudNodeGroup(_ context.Context, _ string, request client.AddNodeGroupRequest,
	_ bool) (*client.AddNodeGroupResult, bool, error) {
	return m.record(request)
}

func (m *nodeGroupAddDiskMock) AddHetznerNodeGroup(_ context.Context, _ string, request client.AddNodeGroupRequest,
	_ bool) (*client.AddNodeGroupResult, bool, error) {
	return m.record(request)
}

func runNodeGroupAddWithDisk(t *testing.T, mock *nodeGroupAddDiskMock, extra ...string) error {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, clusterNodeGroupAddCmd) })
	var runError error
	_ = captureStdout(t, func() {
		arguments := append([]string{"cluster", "node-group", "add", testClusterID, "--name", "ci",
			"--instance-type", "starter-16c-32g", "--wait"}, extra...)
		_, runError = executeCommand(arguments...)
	})
	return runError
}

func TestClusterNodeGroupAddSendsTheDiskSizeOnAnkraCloud(t *testing.T) {
	mock := &nodeGroupAddDiskMock{kind: "ankracloud"}
	if runError := runNodeGroupAddWithDisk(t, mock, "--disk-gib", "30"); runError != nil {
		t.Fatalf("add: %v", runError)
	}
	if mock.gotRequest == nil || mock.gotRequest.DiskGibibytes != 30 {
		t.Fatalf("request = %+v", mock.gotRequest)
	}
}

func TestClusterNodeGroupAddOmitsAnUnsetDiskSize(t *testing.T) {
	mock := &nodeGroupAddDiskMock{kind: "ankracloud"}
	if runError := runNodeGroupAddWithDisk(t, mock); runError != nil {
		t.Fatalf("add: %v", runError)
	}
	body, _ := json.Marshal(mock.gotRequest)
	if strings.Contains(string(body), "disk_gibibytes") {
		t.Fatalf("an unset disk size must be omitted: %s", body)
	}
}

func TestClusterNodeGroupAddRefusesTheDiskSizeOffAnkraCloud(t *testing.T) {
	mock := &nodeGroupAddDiskMock{kind: "hetzner"}
	runError := runNodeGroupAddWithDisk(t, mock, "--disk-gib", "30")
	if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "only on Ankra Cloud") {
		t.Fatalf("error = %v, want a usage error", runError)
	}
	if mock.gotRequest != nil {
		t.Fatalf("a request was sent: %+v", mock.gotRequest)
	}
}

func TestClusterNodeGroupAddRefusesAnInvalidDiskSize(t *testing.T) {
	mock := &nodeGroupAddDiskMock{kind: "ankracloud"}
	runError := runNodeGroupAddWithDisk(t, mock, "--disk-gib", "0")
	if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--disk-gib") {
		t.Fatalf("error = %v, want a usage error", runError)
	}
	if mock.gotRequest != nil {
		t.Fatalf("a request was sent: %+v", mock.gotRequest)
	}
}

func TestNodeGroupDiskSuffix(t *testing.T) {
	if suffix := nodeGroupDiskSuffix(client.NodeGroupInfo{}); suffix != "" {
		t.Fatalf("suffix = %q", suffix)
	}
	if suffix := nodeGroupDiskSuffix(client.NodeGroupInfo{DiskGibibytes: 30}); suffix != "  disk=30GiB" {
		t.Fatalf("suffix = %q", suffix)
	}
}
