package cmd

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"ankra/internal/client"
)

func TestCloudDNSZoneNotFoundUsesExitThree(t *testing.T) {
	if code := exitCodeFor(fmt.Errorf("get access: %w", client.ErrCloudDNSZoneNotFound)); code != exitNotFound {
		t.Fatalf("exit code = %d", code)
	}
	if code := exitCodeFor(&client.PermissionDeniedError{Permission: "members.manage"}); code != exitForbidden {
		t.Fatalf("permission denial exit code = %d", code)
	}
	if code := exitCodeFor(&client.CloudDNSValidationError{Detail: "invalid_proxy_access_policy"}); code != exitUsage {
		t.Fatalf("validation exit code = %d", code)
	}
}

type cloudAccessPolicyMock struct {
	baseMock
	zoneID string
	policy client.CloudDNSAccessPolicy
}

func (mock *cloudAccessPolicyMock) UpdateCloudDNSAccess(_ context.Context, zoneID string, policy client.CloudDNSAccessPolicy) (*client.CloudDNSAccessPolicy, error) {
	mock.zoneID, mock.policy = zoneID, policy
	return &policy, nil
}

func TestAccessGatewaySetValidatesAndSendsPolicy(t *testing.T) {
	previous := apiClient
	mock := &cloudAccessPolicyMock{}
	apiClient = mock
	defer func() { apiClient = previous }()

	invalid := newAccessGatewaySetCommand()
	invalid.SetArgs([]string{"zone-1", "--mode", "public", "--role", "admin"})
	invalid.SetOut(&bytes.Buffer{})
	if invalid.Execute() == nil || mock.zoneID != "" {
		t.Fatal("public policy with a role was sent")
	}

	valid := newAccessGatewaySetCommand()
	valid.SetArgs([]string{"zone-1", "--mode", "organisation", "--role", "admin", "--role", "operator", "-o", "json"})
	output := &bytes.Buffer{}
	valid.SetOut(output)
	if runError := valid.Execute(); runError != nil {
		t.Fatal(runError)
	}
	if mock.zoneID != "zone-1" || mock.policy.Mode != "organisation" || len(mock.policy.Roles) != 2 || output.String() == "" {
		t.Fatalf("set = %q %+v %q", mock.zoneID, mock.policy, output.String())
	}
}

type accessGatewayMock struct {
	baseMock
	relaySecretMissing bool
}

func (mock accessGatewayMock) GetResources(clusterID string, request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	if !request.SkipCache || request.RevealSecretValues || len(request.ResourceRequests) != 1 {
		return nil, fmt.Errorf("access gate must use a live read without revealing secrets")
	}
	query := request.ResourceRequests[0]
	item := client.ResourceResponseItem{Status: "success", Kind: query.Kind}
	if query.Kind == "Deployment" {
		if query.Group != "apps" || query.Version != "v1" {
			return nil, fmt.Errorf("wrong Deployment API version")
		}
		item.Items = []interface{}{gatewayTestDeployment(query.Name, query.Name == "ankra-tunnel-relay")}
	} else if query.Kind == "Secret" {
		if query.Group != "" || query.Version != "v1" || query.Name != accessGatewaySecretName {
			return nil, fmt.Errorf("wrong Secret API request")
		}
		if clusterID == "relay" && mock.relaySecretMissing {
			item.NotFound = true
		} else {
			item.SecretValues = client.SecretValuesWithheld
			item.Items = []interface{}{map[string]interface{}{"data": map[string]interface{}{"token": "cluster-keyed-digest"}}}
		}
	} else {
		return nil, fmt.Errorf("unexpected resource %s", query.Kind)
	}
	return &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{item}}, nil
}

func gatewayTestDeployment(name string, relay bool) map[string]interface{} {
	environment := []interface{}{map[string]interface{}{
		"name": "ANKRA_ACCESS_GATE_TOKEN", "valueFrom": map[string]interface{}{
			"secretKeyRef": map[string]interface{}{"name": accessGatewaySecretName, "key": "token"},
		},
	}}
	if relay {
		environment = append(environment, map[string]interface{}{"name": "ANKRA_ACCESS_GATE_URL", "value": "https://api.platform.ankra.app"})
	}
	return map[string]interface{}{
		"metadata": map[string]interface{}{"name": name},
		"spec": map[string]interface{}{"replicas": float64(2), "template": map[string]interface{}{
			"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{"env": environment}}},
		}},
		"status": map[string]interface{}{"readyReplicas": float64(2)},
	}
}

func TestAccessGatewayReportUsesLiveRedactedReads(t *testing.T) {
	previous := apiClient
	apiClient = accessGatewayMock{}
	defer func() { apiClient = previous }()
	report, err := readAccessGatewayReport("platform", "relay")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || len(report.PlatformDeployments) != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestAccessGatewayReportRequiresBothSecrets(t *testing.T) {
	previous := apiClient
	apiClient = accessGatewayMock{relaySecretMissing: true}
	defer func() { apiClient = previous }()
	report, err := readAccessGatewayReport("platform", "relay")
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.RelaySecretPresent {
		t.Fatalf("missing relay Secret reported ready: %+v", report)
	}
}

func TestAccessGatewayDeploymentRequiresSecretReferenceAndHTTPSOrigin(t *testing.T) {
	object := gatewayTestDeployment("ankra-tunnel-relay", true)
	spec := object["spec"].(map[string]interface{})
	template := spec["template"].(map[string]interface{})
	pod := template["spec"].(map[string]interface{})
	container := pod["containers"].([]interface{})[0].(map[string]interface{})
	environment := container["env"].([]interface{})
	environment[1].(map[string]interface{})["value"] = "http://platform.example.test/path"
	if inspectAccessGatewayDeployment(object, "ankra-tunnel-relay", true).Ready {
		t.Fatal("insecure broker origin reported ready")
	}
	environment[1].(map[string]interface{})["value"] = "https://api.platform.ankra.app"
	environment[0].(map[string]interface{})["valueFrom"] = nil
	if inspectAccessGatewayDeployment(object, "ankra-tunnel-relay", true).Ready {
		t.Fatal("missing Secret reference reported ready")
	}
}
