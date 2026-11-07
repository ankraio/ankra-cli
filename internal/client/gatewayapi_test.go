package client

import (
	"encoding/json"
	"testing"
)

// gateway_api is sent only when asked for, so a create without --gateway-api
// sends exactly the body it did before the option existed, and a platform
// that predates the option sees nothing new.
func TestCreateRequestsSendGatewayAPIOnlyWhenSet(t *testing.T) {
	requests := map[string]func(isGatewayAPIEnabled bool) any{
		"ankracloud": func(isGatewayAPIEnabled bool) any {
			return CreateAnkraCloudClusterRequest{GatewayAPI: isGatewayAPIEnabled}
		},
		"aws": func(isGatewayAPIEnabled bool) any { return CreateAwsClusterRequest{GatewayAPI: isGatewayAPIEnabled} },
		"digitalocean": func(isGatewayAPIEnabled bool) any {
			return CreateDigitaloceanClusterRequest{GatewayAPI: isGatewayAPIEnabled}
		},
		"hetzner": func(isGatewayAPIEnabled bool) any {
			return CreateHetznerClusterRequest{GatewayAPI: isGatewayAPIEnabled}
		},
		"morpheus": func(isGatewayAPIEnabled bool) any {
			return CreateMorpheusClusterRequest{GatewayAPI: isGatewayAPIEnabled}
		},
		"ovh": func(isGatewayAPIEnabled bool) any { return CreateOvhClusterRequest{GatewayAPI: isGatewayAPIEnabled} },
		"proxmox": func(isGatewayAPIEnabled bool) any {
			return CreateProxmoxClusterRequest{GatewayAPI: isGatewayAPIEnabled}
		},
		"scaleway": func(isGatewayAPIEnabled bool) any {
			return CreateScalewayClusterRequest{GatewayAPI: isGatewayAPIEnabled}
		},
		"upcloud": func(isGatewayAPIEnabled bool) any {
			return CreateUpcloudClusterRequest{GatewayAPI: isGatewayAPIEnabled}
		},
	}
	for name, build := range requests {
		t.Run(name, func(t *testing.T) {
			for _, isGatewayAPIEnabled := range []bool{false, true} {
				encoded, marshalError := json.Marshal(build(isGatewayAPIEnabled))
				if marshalError != nil {
					t.Fatalf("marshal: %v", marshalError)
				}
				var body map[string]any
				if unmarshalError := json.Unmarshal(encoded, &body); unmarshalError != nil {
					t.Fatalf("unmarshal: %v", unmarshalError)
				}
				value, present := body["gateway_api"]
				if present != isGatewayAPIEnabled || (present && value != true) {
					t.Errorf("GatewayAPI=%v: body carries gateway_api=%v (present %v)", isGatewayAPIEnabled, value, present)
				}
			}
		})
	}
}
