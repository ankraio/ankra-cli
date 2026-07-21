package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type clusterSSHKeysMock struct {
	baseMock
	cluster client.ClusterListItem

	hetznerResync []string
	ovhResync     []string
	upcloudResync []string
}

func (m *clusterSSHKeysMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return m.cluster, nil
}

func (m *clusterSSHKeysMock) resyncResult() *client.ResyncSSHKeysResult {
	return &client.ResyncSSHKeysResult{ResourceIDs: []string{"resource-1"}}
}

func (m *clusterSSHKeysMock) ResyncHetznerClusterSSHKeys(clusterID string) (*client.ResyncSSHKeysResult, error) {
	m.hetznerResync = append(m.hetznerResync, clusterID)
	return m.resyncResult(), nil
}

func (m *clusterSSHKeysMock) ResyncOvhClusterSSHKeys(clusterID string) (*client.ResyncSSHKeysResult, error) {
	m.ovhResync = append(m.ovhResync, clusterID)
	return m.resyncResult(), nil
}

func (m *clusterSSHKeysMock) ResyncUpcloudClusterSSHKeys(clusterID string) (*client.ResyncSSHKeysResult, error) {
	m.upcloudResync = append(m.upcloudResync, clusterID)
	return m.resyncResult(), nil
}

func TestClusterSSHKeysResync_DispatchesByKind(t *testing.T) {
	const clusterID = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"

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
			mock := &clusterSSHKeysMock{
				cluster: client.ClusterListItem{ID: clusterID, Name: "demo", Kind: tc.kind},
			}
			setMockClient(t, mock)

			out := captureStdout(t, func() {
				rootCmd.SetArgs([]string{"cluster", "ssh-keys", "resync", clusterID})
				if err := rootCmd.Execute(); err != nil {
					t.Fatalf("execute failed: %v", err)
				}
			})

			if len(mock.hetznerResync) != tc.wantHetzner {
				t.Errorf("hetzner resync calls = %d, want %d", len(mock.hetznerResync), tc.wantHetzner)
			}
			if len(mock.ovhResync) != tc.wantOvh {
				t.Errorf("ovh resync calls = %d, want %d", len(mock.ovhResync), tc.wantOvh)
			}
			if len(mock.upcloudResync) != tc.wantUpcloud {
				t.Errorf("upcloud resync calls = %d, want %d", len(mock.upcloudResync), tc.wantUpcloud)
			}
			if !strings.Contains(out, "resource-1") {
				t.Errorf("expected resource id in output, got:\n%s", out)
			}
		})
	}
}

func TestSSHKeysSelectorsForKind(t *testing.T) {
	mock := &clusterSSHKeysMock{}
	setMockClient(t, mock)

	for _, kind := range []string{"hetzner", "ovh", "upcloud", "digitalocean", "proxmox", "morpheus"} {
		if sshKeysGetForKind(kind) == nil ||
			sshKeysSetForKind(kind) == nil ||
			sshKeysResyncForKind(kind) == nil {
			t.Errorf("kind %q should resolve all ssh-key operations", kind)
		}
	}
	for _, kind := range []string{"imported", "ankra", ""} {
		if sshKeysGetForKind(kind) != nil ||
			sshKeysSetForKind(kind) != nil ||
			sshKeysResyncForKind(kind) != nil {
			t.Errorf("kind %q should not resolve ssh-key operations", kind)
		}
	}
}
