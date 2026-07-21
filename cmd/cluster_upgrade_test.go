package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type k8sUpgradeCall struct {
	ClusterID     string
	TargetVersion string
	Force         bool
}

type clusterUpgradeMock struct {
	baseMock
	cluster       client.ClusterListItem
	getClusterErr error

	hetznerCalls []k8sUpgradeCall
	ovhCalls     []k8sUpgradeCall
	upcloudCalls []k8sUpgradeCall
}

func (m *clusterUpgradeMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.getClusterErr != nil {
		return client.ClusterListItem{}, m.getClusterErr
	}
	return m.cluster, nil
}

func (m *clusterUpgradeMock) upgradeResult() *client.UpgradeK8sVersionResult {
	previousVersion := "v1.35.0+k3s1"
	return &client.UpgradeK8sVersionResult{
		PreviousVersion: &previousVersion,
		NewVersion:      "v1.36.1+k3s1",
		NodesAffected:   3,
	}
}

func (m *clusterUpgradeMock) UpgradeHetznerK8sVersion(clusterID, targetVersion string, force bool) (*client.UpgradeK8sVersionResult, error) {
	m.hetznerCalls = append(m.hetznerCalls, k8sUpgradeCall{ClusterID: clusterID, TargetVersion: targetVersion, Force: force})
	return m.upgradeResult(), nil
}

func (m *clusterUpgradeMock) UpgradeOvhK8sVersion(clusterID, targetVersion string, force bool) (*client.UpgradeK8sVersionResult, error) {
	m.ovhCalls = append(m.ovhCalls, k8sUpgradeCall{ClusterID: clusterID, TargetVersion: targetVersion, Force: force})
	return m.upgradeResult(), nil
}

func (m *clusterUpgradeMock) UpgradeUpcloudK8sVersion(clusterID, targetVersion string, force bool) (*client.UpgradeK8sVersionResult, error) {
	m.upcloudCalls = append(m.upcloudCalls, k8sUpgradeCall{ClusterID: clusterID, TargetVersion: targetVersion, Force: force})
	return m.upgradeResult(), nil
}

func TestClusterUpgrade_DispatchesByKind(t *testing.T) {
	const clusterID = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"
	const targetVersion = "v1.36.1+k3s1"

	cases := []struct {
		kind        string
		wantHetzner int
		wantOvh     int
		wantUpcloud int
	}{
		{kind: "hetzner", wantHetzner: 1},
		{kind: "ovh", wantOvh: 1},
		{kind: "upcloud", wantUpcloud: 1},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			mock := &clusterUpgradeMock{
				cluster: client.ClusterListItem{ID: clusterID, Name: "demo", Kind: tc.kind},
			}
			setMockClient(t, mock)

			out := captureStdout(t, func() {
				rootCmd.SetArgs([]string{"cluster", "upgrade", clusterID, targetVersion})
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
			if !strings.Contains(out, "v1.36.1+k3s1") {
				t.Errorf("expected new version in output, got:\n%s", out)
			}
			if !strings.Contains(out, tc.kind) {
				t.Errorf("expected provider %q in output, got:\n%s", tc.kind, out)
			}
		})
	}
}

func TestClusterUpgrade_ForcePropagates(t *testing.T) {
	const clusterID = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"

	for _, force := range []bool{false, true} {
		mock := &clusterUpgradeMock{
			cluster: client.ClusterListItem{ID: clusterID, Name: "demo", Kind: "hetzner"},
		}
		setMockClient(t, mock)
		t.Cleanup(func() {
			clusterUpgradeForce = false
			_ = clusterUpgradeCmd.Flags().Set("force", "false")
		})

		args := []string{"cluster", "upgrade", clusterID, "v1.33.2"}
		if force {
			args = append(args, "--force")
		}
		captureStdout(t, func() {
			rootCmd.SetArgs(args)
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("execute failed (force=%v): %v", force, err)
			}
		})

		if len(mock.hetznerCalls) != 1 {
			t.Fatalf("hetzner calls = %d, want 1 (force=%v)", len(mock.hetznerCalls), force)
		}
		if got := mock.hetznerCalls[0].Force; got != force {
			t.Errorf("client received force=%v, want %v", got, force)
		}
	}
}

func TestUpgradeFunctionForKind_UnsupportedKinds(t *testing.T) {
	mock := &clusterUpgradeMock{}
	setMockClient(t, mock)

	for _, kind := range []string{"imported", "ankra", "sandbox", "managed", ""} {
		if _, supported := upgradeFunctionForKind(kind); supported {
			t.Errorf("kind %q should not be upgradeable", kind)
		}
	}
}

func TestUpgradeFunctionForKind_SupportedKinds(t *testing.T) {
	mock := &clusterUpgradeMock{}
	setMockClient(t, mock)

	for _, kind := range []string{"hetzner", "ovh", "upcloud", "digitalocean", "proxmox", "morpheus"} {
		if _, supported := upgradeFunctionForKind(kind); !supported {
			t.Errorf("kind %q should be upgradeable", kind)
		}
	}
}
