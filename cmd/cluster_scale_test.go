package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type workerScaleCall struct {
	ClusterID   string
	WorkerCount int
}

type clusterScaleMock struct {
	baseMock
	cluster client.ClusterListItem

	hetznerCalls  []workerScaleCall
	ovhCalls      []workerScaleCall
	upcloudCalls  []workerScaleCall
	proxmoxCalls  []workerScaleCall
	morpheusCalls []workerScaleCall
}

func (m *clusterScaleMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return m.cluster, nil
}

func (m *clusterScaleMock) scaleResult() *client.ScaleWorkersResult {
	return &client.ScaleWorkersResult{PreviousCount: 1, NewCount: 3}
}

func (m *clusterScaleMock) ScaleHetznerWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error) {
	m.hetznerCalls = append(m.hetznerCalls, workerScaleCall{ClusterID: clusterID, WorkerCount: workerCount})
	return m.scaleResult(), nil
}

func (m *clusterScaleMock) ScaleOvhWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error) {
	m.ovhCalls = append(m.ovhCalls, workerScaleCall{ClusterID: clusterID, WorkerCount: workerCount})
	return m.scaleResult(), nil
}

func (m *clusterScaleMock) ScaleUpcloudWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error) {
	m.upcloudCalls = append(m.upcloudCalls, workerScaleCall{ClusterID: clusterID, WorkerCount: workerCount})
	return m.scaleResult(), nil
}

func (m *clusterScaleMock) ScaleProxmoxWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error) {
	m.proxmoxCalls = append(m.proxmoxCalls, workerScaleCall{ClusterID: clusterID, WorkerCount: workerCount})
	return m.scaleResult(), nil
}

func (m *clusterScaleMock) ScaleMorpheusWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error) {
	m.morpheusCalls = append(m.morpheusCalls, workerScaleCall{ClusterID: clusterID, WorkerCount: workerCount})
	return m.scaleResult(), nil
}

func TestClusterScale_DispatchesByKind(t *testing.T) {
	const clusterID = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"

	cases := []struct {
		kind         string
		wantHetzner  int
		wantOvh      int
		wantUpcloud  int
		wantProxmox  int
		wantMorpheus int
	}{
		{kind: "hetzner", wantHetzner: 1},
		{kind: "ovh", wantOvh: 1},
		{kind: "upcloud", wantUpcloud: 1},
		{kind: "proxmox", wantProxmox: 1},
		{kind: "morpheus", wantMorpheus: 1},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			mock := &clusterScaleMock{
				cluster: client.ClusterListItem{ID: clusterID, Name: "demo", Kind: tc.kind},
			}
			setMockClient(t, mock)

			out := captureStdout(t, func() {
				rootCmd.SetArgs([]string{"cluster", "scale", clusterID, "3"})
				if err := rootCmd.Execute(); err != nil {
					t.Fatalf("execute failed: %v", err)
				}
			})

			if len(mock.hetznerCalls) != tc.wantHetzner {
				t.Errorf("hetzner calls = %d, want %d", len(mock.hetznerCalls), tc.wantHetzner)
			}
			if len(mock.ovhCalls) != tc.wantOvh {
				t.Errorf("ovh calls = %d, want %d", len(mock.ovhCalls), tc.wantOvh)
			}
			if len(mock.upcloudCalls) != tc.wantUpcloud {
				t.Errorf("upcloud calls = %d, want %d", len(mock.upcloudCalls), tc.wantUpcloud)
			}
			if len(mock.proxmoxCalls) != tc.wantProxmox {
				t.Errorf("proxmox calls = %d, want %d", len(mock.proxmoxCalls), tc.wantProxmox)
			}
			if len(mock.morpheusCalls) != tc.wantMorpheus {
				t.Errorf("morpheus calls = %d, want %d", len(mock.morpheusCalls), tc.wantMorpheus)
			}
			if !strings.Contains(out, tc.kind) {
				t.Errorf("expected provider %q in output, got:\n%s", tc.kind, out)
			}
		})
	}
}

func TestScaleFunctionForKind_SupportedAndUnsupported(t *testing.T) {
	mock := &clusterScaleMock{}
	setMockClient(t, mock)

	for _, kind := range []string{"hetzner", "ovh", "upcloud", "digitalocean", "proxmox", "morpheus"} {
		if _, supported := scaleFunctionForKind(kind); !supported {
			t.Errorf("kind %q should be scalable", kind)
		}
	}
	for _, kind := range []string{"imported", "ankra", "sandbox", ""} {
		if _, supported := scaleFunctionForKind(kind); supported {
			t.Errorf("kind %q should not be scalable", kind)
		}
	}
}

func TestNodeGroupSelectorsForKind(t *testing.T) {
	mock := &clusterScaleMock{}
	setMockClient(t, mock)

	for _, kind := range []string{"hetzner", "ovh", "upcloud", "digitalocean", "proxmox", "morpheus"} {
		if nodeGroupListForKind(kind) == nil ||
			nodeGroupAddForKind(kind) == nil ||
			nodeGroupScaleForKind(kind) == nil ||
			nodeGroupUpgradeForKind(kind) == nil ||
			nodeGroupDeleteForKind(kind) == nil {
			t.Errorf("kind %q should resolve all node-group operations", kind)
		}
	}
	for _, kind := range []string{"imported", "ankra", ""} {
		if nodeGroupListForKind(kind) != nil ||
			nodeGroupAddForKind(kind) != nil ||
			nodeGroupScaleForKind(kind) != nil ||
			nodeGroupUpgradeForKind(kind) != nil ||
			nodeGroupDeleteForKind(kind) != nil {
			t.Errorf("kind %q should not resolve node-group operations", kind)
		}
	}
}
