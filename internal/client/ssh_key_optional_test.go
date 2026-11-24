package client

import (
	"encoding/json"
	"strings"
	"testing"
)

// A self-managed create that names no personal SSH key leaves the member out
// of the body: the platform reads an absent ssh_key_credential_id as "no
// personal key" and refuses an empty string, which is not a UUID.
func TestSelfManagedCreateBodiesOmitAnUnnamedSSHKey(t *testing.T) {
	for name, request := range map[string]any{
		"ovh":          CreateOvhClusterRequest{Name: "c1", CredentialID: "cred"},
		"upcloud":      CreateUpcloudClusterRequest{Name: "c1", CredentialID: "cred"},
		"digitalocean": CreateDigitaloceanClusterRequest{Name: "c1", CredentialID: "cred"},
		"scaleway":     CreateScalewayClusterRequest{Name: "c1", CredentialID: "cred"},
		"proxmox":      CreateProxmoxClusterRequest{Name: "c1", CredentialID: "cred"},
		"morpheus":     CreateMorpheusClusterRequest{Name: "c1", CredentialID: "cred"},
		"aws":          CreateAwsClusterRequest{Name: "c1", CredentialID: "cred"},
	} {
		body, marshalError := json.Marshal(request)
		if marshalError != nil {
			t.Fatalf("%s: %v", name, marshalError)
		}
		if strings.Contains(string(body), "ssh_key_credential_id") {
			t.Errorf("%s: a create naming no key must omit ssh_key_credential_id, got %s", name, body)
		}
	}
}
