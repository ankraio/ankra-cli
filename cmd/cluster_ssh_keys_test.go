package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type clusterSSHKeysMock struct {
	baseMock
	cluster client.ClusterListItem

	hetznerResync  []string
	ovhResync      []string
	upcloudResync  []string
	scalewayResync []string
	awsResync      []string
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

func (m *clusterSSHKeysMock) ResyncScalewayClusterSSHKeys(clusterID string) (*client.ResyncSSHKeysResult, error) {
	m.scalewayResync = append(m.scalewayResync, clusterID)
	return m.resyncResult(), nil
}

func (m *clusterSSHKeysMock) ResyncAwsClusterSSHKeys(clusterID string) (*client.ResyncSSHKeysResult, error) {
	m.awsResync = append(m.awsResync, clusterID)
	return m.resyncResult(), nil
}

func TestClusterSSHKeysResync_DispatchesByKind(t *testing.T) {
	const clusterID = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"

	cases := []struct {
		kind         string
		wantHetzner  int
		wantOvh      int
		wantUpcloud  int
		wantScaleway int
		wantAws      int
	}{
		{kind: "hetzner", wantHetzner: 1},
		{kind: "ovh", wantOvh: 1},
		{kind: "upcloud", wantUpcloud: 1},
		{kind: "scaleway", wantScaleway: 1},
		{kind: "aws", wantAws: 1},
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
			if len(mock.scalewayResync) != tc.wantScaleway {
				t.Errorf("scaleway resync calls = %d, want %d", len(mock.scalewayResync), tc.wantScaleway)
			}
			if len(mock.awsResync) != tc.wantAws {
				t.Errorf("aws resync calls = %d, want %d", len(mock.awsResync), tc.wantAws)
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

	for _, kind := range []string{"hetzner", "ovh", "upcloud", "digitalocean", "scaleway", "aws", "proxmox", "morpheus"} {
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

func (m *clusterSSHKeysMock) GetHetznerClusterSSHKeys(clusterID string) (*client.ClusterSSHKeysResult, error) {
	return &client.ClusterSSHKeysResult{
		SSHKeyCredentialIDs: []string{
			"d07ea4d0-5928-455b-87e3-e2fe8e6b50ac",
			"c350ebd2-318a-44ec-8376-34356ead4c1a",
			"99999999-9999-4999-8999-999999999999",
		},
		AvailableSSHKeys: []client.ClusterSSHKeyEntry{
			{CredentialID: "66687213-9190-48d4-81d6-4748de371220", Name: "maas"},
			{CredentialID: "d07ea4d0-5928-455b-87e3-e2fe8e6b50ac", Name: "mash"},
			{CredentialID: "c350ebd2-318a-44ec-8376-34356ead4c1a", Name: "ssh-key-shiv"},
		},
	}, nil
}

// The attached keys used to print as bare ids while the names sat in the
// same response's available list.
func TestClusterSSHKeysGet_NamesTheAttachedKeys(t *testing.T) {
	const clusterID = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"
	mock := &clusterSSHKeysMock{cluster: client.ClusterListItem{ID: clusterID, Name: "demo", Kind: "hetzner"}}
	setMockClient(t, mock)

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"cluster", "ssh-keys", "get", clusterID})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})

	attachedSection, _, _ := strings.Cut(out, "Available SSH key credentials:")
	for _, line := range []string{
		"d07ea4d0-5928-455b-87e3-e2fe8e6b50ac    mash",
		"c350ebd2-318a-44ec-8376-34356ead4c1a    ssh-key-shiv",
		"99999999-9999-4999-8999-999999999999    (name unknown",
	} {
		if !strings.Contains(attachedSection, line) {
			t.Errorf("attached section lacks %q:\n%s", line, out)
		}
	}
	if strings.Contains(attachedSection, "maas") {
		t.Errorf("unattached key listed as attached:\n%s", out)
	}
	if !strings.Contains(out, "66687213-9190-48d4-81d6-4748de371220    maas") {
		t.Errorf("available list lost:\n%s", out)
	}
}
