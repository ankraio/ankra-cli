package cmd

import (
	"context"
	"encoding/json"
	"errors"

	"ankra/internal/client"
)

var errScalewayManagedMockNotImplemented = errors.New("not implemented")

func (baseMock) ManagedOptions(context.Context, string, string) (*client.ManagedOptions, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedClusterOptions(context.Context, string, string) (*client.ManagedOptions, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedPreflight(context.Context, string, json.RawMessage) (*client.ManagedPreflightResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedCreate(context.Context, string, json.RawMessage) (*client.ManagedClusterCreateResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedDiscover(context.Context, string, string) (*client.ManagedDiscoveryResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedImport(context.Context, string, client.ManagedImportRequest) (*client.ManagedImportResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedStatus(context.Context, string, string) (*client.ManagedStatusResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedDisconnect(context.Context, string, string, bool) (*client.ManagedDeprovisionResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedDeleteProviderCluster(context.Context, string, string, bool, string) (*client.ManagedDeprovisionResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedListPools(context.Context, string, string) (*client.ManagedNodePoolsResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedPoolCatalog(context.Context, string, string) (*client.ManagedPoolCatalog, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedAddPool(context.Context, string, string, client.ManagedNodePoolRequest) (*client.ManagedPoolOperationResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedScalePool(context.Context, string, string, string, int) (*client.ManagedPoolOperationResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedUpdatePool(context.Context, string, string, string, client.ManagedPoolUpdateRequest) (*client.ManagedPoolOperationResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedDeletePool(context.Context, string, string, string) (*client.ManagedPoolOperationResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedUpgrades(context.Context, string, string) (*client.ManagedAvailableUpgradesResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ManagedUpgrade(context.Context, string, string, string) (*client.ManagedUpgradeResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}

func (baseMock) ListScalewayCredentials() ([]client.ScalewayCredentialListItem, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) CreateScalewayCredential(client.CreateScalewayCredentialRequest) (*client.CreateScalewayCredentialResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) CreateScalewayCluster(context.Context, client.CreateScalewayClusterRequest) (*client.ScalewayCreateClusterResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) PreflightScalewayCluster(context.Context, client.CreateScalewayClusterRequest) (*client.ScalewayPreflightResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ListScalewayLocations(context.Context, string) (*client.ScalewayCatalogResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ListScalewayRegions(context.Context, string) (*client.ScalewayCatalogResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ListScalewayZones(context.Context, string) (*client.ScalewayCatalogResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ListScalewayNetworks(context.Context, string, string) (*client.ScalewayCatalogResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ListScalewayInstanceTypes(context.Context, string, string) (*client.ScalewayCatalogResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ListScalewayGatewayTypes(context.Context, string, string) (*client.ScalewayCatalogResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ListScalewayPricing(context.Context, string, string) (*client.ScalewayCatalogResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) GetScalewayClusterInstanceTypes(context.Context, string) (*client.ScalewayCatalogResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) GetScalewayClusterGatewayTypes(context.Context, string) (*client.ScalewayCatalogResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) DeprovisionScalewayCluster(string, bool) (*client.ScalewayLifecycleResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) StopScalewayCluster(string) (*client.ScalewayLifecycleResponse, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) StartScalewayCluster(string) (*client.StartUpcloudClusterResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) GetScalewayAccessInfo(string) (*client.ScalewayAccessInfo, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) GetScalewayWorkerCount(string) (*client.WorkerCountResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ScaleScalewayWorkers(string, int) (*client.ScaleWorkersResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) GetScalewayK8sVersion(string) (*client.K8sVersionInfo, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) UpgradeScalewayK8sVersion(string, string, bool) (*client.UpgradeK8sVersionResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ListScalewayNodeGroups(string) (*client.NodeGroupListResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) AddScalewayNodeGroup(context.Context, string, client.AddNodeGroupRequest, bool) (*client.AddNodeGroupResult, bool, error) {
	return nil, false, errScalewayManagedMockNotImplemented
}
func (baseMock) AddScalewayNodeGroupFull(context.Context, string, client.ScalewayCreateNodeGroupRequest, bool) (*client.AddNodeGroupResult, bool, error) {
	return nil, false, errScalewayManagedMockNotImplemented
}
func (baseMock) ScaleScalewayNodeGroup(context.Context, string, string, int, bool) (*client.ScaleNodeGroupResult, bool, error) {
	return nil, false, errScalewayManagedMockNotImplemented
}
func (baseMock) UpdateScalewayNodeGroupInstanceType(context.Context, string, string, string, bool) (*client.UpdateNodeGroupResult, bool, error) {
	return nil, false, errScalewayManagedMockNotImplemented
}
func (baseMock) UpdateScalewayNodeGroupLabels(context.Context, string, string, map[string]string, bool) (*client.UpdateNodeGroupResult, bool, error) {
	return nil, false, errScalewayManagedMockNotImplemented
}
func (baseMock) UpdateScalewayNodeGroupTaints(context.Context, string, string, []client.NodeTaint, bool) (*client.UpdateNodeGroupResult, bool, error) {
	return nil, false, errScalewayManagedMockNotImplemented
}
func (baseMock) DeleteScalewayNodeGroup(context.Context, string, string, bool) (*client.DeleteNodeGroupResult, bool, error) {
	return nil, false, errScalewayManagedMockNotImplemented
}
func (baseMock) GetScalewayNodeGroupAutoscaling(string, string) (*client.NodeGroupAutoscalingResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) UpdateScalewayNodeGroupAutoscaling(context.Context, string, string, client.NodeGroupAutoscalingRequest, bool) (*client.NodeGroupAutoscalingResult, bool, error) {
	return nil, false, errScalewayManagedMockNotImplemented
}
func (baseMock) GetScalewayControlPlane(string) (*client.ControlPlaneInfo, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ChangeScalewayControlPlaneCount(string, int) (*client.ChangeControlPlaneCountResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ChangeScalewayControlPlaneInstanceType(string, string) (*client.ChangeControlPlaneInstanceTypeResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ListScalewayClusterNodes(string) (*client.NodeListResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) GetScalewayClusterNode(string, string) (*client.NodeDetail, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) RestartScalewayClusterNode(string, string) (*client.RestartNodeResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) GetScalewayClusterSSHKeys(string) (*client.ClusterSSHKeysResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) UpdateScalewayClusterSSHKeys(string, []string) (*client.UpdateClusterSSHKeysResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) ResyncScalewayClusterSSHKeys(string) (*client.ResyncSSHKeysResult, error) {
	return nil, errScalewayManagedMockNotImplemented
}
func (baseMock) UpdateScalewayBastionInstanceType(context.Context, string, string, bool) (*client.UpdateBastionInstanceTypeResult, bool, error) {
	return nil, false, errScalewayManagedMockNotImplemented
}
