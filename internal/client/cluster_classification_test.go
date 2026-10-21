package client

import (
	"encoding/json"
	"testing"
)

// Every cluster create body carries the optional environment and reporting
// level the platform's create decoders read (ankra-lhy8e). Unset, neither key
// is sent, so the platform keeps its default; set, both travel under the
// platform's names.
func TestClusterCreateRequestsCarryTheClassification(t *testing.T) {
	environment, criticality := "production", "staging"
	requests := map[string]struct{ unset, set any }{
		"hetzner": {CreateHetznerClusterRequest{},
			CreateHetznerClusterRequest{Environment: &environment, Criticality: &criticality}},
		"ovh": {CreateOvhClusterRequest{},
			CreateOvhClusterRequest{Environment: &environment, Criticality: &criticality}},
		"upcloud": {CreateUpcloudClusterRequest{},
			CreateUpcloudClusterRequest{Environment: &environment, Criticality: &criticality}},
		"digitalocean": {CreateDigitaloceanClusterRequest{},
			CreateDigitaloceanClusterRequest{Environment: &environment, Criticality: &criticality}},
		"scaleway": {CreateScalewayClusterRequest{},
			CreateScalewayClusterRequest{Environment: &environment, Criticality: &criticality}},
		"proxmox": {CreateProxmoxClusterRequest{},
			CreateProxmoxClusterRequest{Environment: &environment, Criticality: &criticality}},
		"morpheus": {CreateMorpheusClusterRequest{},
			CreateMorpheusClusterRequest{Environment: &environment, Criticality: &criticality}},
		"ankracloud": {CreateAnkraCloudClusterRequest{},
			CreateAnkraCloudClusterRequest{Environment: &environment, Criticality: &criticality}},
		"aws": {CreateAwsClusterRequest{},
			CreateAwsClusterRequest{Environment: &environment, Criticality: &criticality}},
		"managed create": {CreateManagedClusterRequest{},
			CreateManagedClusterRequest{Environment: &environment, Criticality: &criticality}},
		"managed import": {ImportManagedClusterRequest{},
			ImportManagedClusterRequest{Environment: &environment, Criticality: &criticality}},
	}
	for family, pair := range requests {
		t.Run(family, func(t *testing.T) {
			unset := marshalToMap(t, pair.unset)
			for _, key := range []string{"environment", "criticality"} {
				if _, present := unset[key]; present {
					t.Errorf("unset request sends %q: %v", key, unset[key])
				}
			}
			set := marshalToMap(t, pair.set)
			if set["environment"] != environment || set["criticality"] != criticality {
				t.Errorf("set request sends environment=%v criticality=%v, want %s/%s",
					set["environment"], set["criticality"], environment, criticality)
			}
		})
	}
}

func marshalToMap(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, marshalError := json.Marshal(value)
	if marshalError != nil {
		t.Fatalf("marshal: %v", marshalError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal(encoded, &decoded); unmarshalError != nil {
		t.Fatalf("unmarshal: %v", unmarshalError)
	}
	return decoded
}
