package cmd

import (
	"context"

	"ankra/internal/client"
)

// ScalewayAPI groups the provider surface outside APIClient. This keeps the
// long-lived command mock interface stable and avoids one method per provider
// being imposed on unrelated tests and integrations.
type ScalewayAPI interface {
	ListScalewayCredentials() ([]client.ScalewayCredentialListItem, error)
	CreateScalewayCredential(client.CreateScalewayCredentialRequest) (*client.CreateScalewayCredentialResponse, error)
	CreateScalewayCluster(context.Context, client.CreateScalewayClusterRequest) (*client.ScalewayCreateClusterResponse, error)
	PreflightScalewayCluster(context.Context, client.CreateScalewayClusterRequest) (*client.ScalewayPreflightResult, error)
	ListScalewayLocations(context.Context, string) (*client.ScalewayCatalogResult, error)
	ListScalewayRegions(context.Context, string) (*client.ScalewayCatalogResult, error)
	ListScalewayZones(context.Context, string) (*client.ScalewayCatalogResult, error)
	ListScalewayNetworks(context.Context, string, string) (*client.ScalewayCatalogResult, error)
	ListScalewayInstanceTypes(context.Context, string, string) (*client.ScalewayCatalogResult, error)
	ListScalewayGatewayTypes(context.Context, string, string) (*client.ScalewayCatalogResult, error)
	ListScalewayPricing(context.Context, string, string) (*client.ScalewayCatalogResult, error)
	GetScalewayClusterInstanceTypes(context.Context, string) (*client.ScalewayCatalogResult, error)
	GetScalewayClusterGatewayTypes(context.Context, string) (*client.ScalewayCatalogResult, error)
	DeprovisionScalewayCluster(string, bool) (*client.ScalewayLifecycleResponse, error)
	StopScalewayCluster(string) (*client.ScalewayLifecycleResponse, error)
	StartScalewayCluster(string) (*client.StartUpcloudClusterResult, error)
	GetScalewayAccessInfo(string) (*client.ScalewayAccessInfo, error)
	GetScalewayWorkerCount(string) (*client.WorkerCountResult, error)
	ScaleScalewayWorkers(string, int) (*client.ScaleWorkersResult, error)
	GetScalewayK8sVersion(string) (*client.K8sVersionInfo, error)
	UpgradeScalewayK8sVersion(string, string, bool) (*client.UpgradeK8sVersionResult, error)
	ListScalewayNodeGroups(string) (*client.NodeGroupListResult, error)
	AddScalewayNodeGroup(context.Context, string, client.AddNodeGroupRequest, bool) (*client.AddNodeGroupResult, bool, error)
	AddScalewayNodeGroupFull(context.Context, string, client.ScalewayCreateNodeGroupRequest, bool) (*client.AddNodeGroupResult, bool, error)
	ScaleScalewayNodeGroup(context.Context, string, string, int, bool) (*client.ScaleNodeGroupResult, bool, error)
	UpdateScalewayNodeGroupInstanceType(context.Context, string, string, string, bool) (*client.UpdateNodeGroupResult, bool, error)
	UpdateScalewayNodeGroupLabels(context.Context, string, string, map[string]string, bool) (*client.UpdateNodeGroupResult, bool, error)
	UpdateScalewayNodeGroupTaints(context.Context, string, string, []client.NodeTaint, bool) (*client.UpdateNodeGroupResult, bool, error)
	DeleteScalewayNodeGroup(context.Context, string, string, bool) (*client.DeleteNodeGroupResult, bool, error)
	GetScalewayNodeGroupAutoscaling(string, string) (*client.NodeGroupAutoscalingResult, error)
	UpdateScalewayNodeGroupAutoscaling(context.Context, string, string, client.NodeGroupAutoscalingRequest, bool) (*client.NodeGroupAutoscalingResult, bool, error)
	GetScalewayControlPlane(string) (*client.ControlPlaneInfo, error)
	ChangeScalewayControlPlaneCount(string, int) (*client.ChangeControlPlaneCountResult, error)
	ChangeScalewayControlPlaneInstanceType(string, string) (*client.ChangeControlPlaneInstanceTypeResult, error)
	ListScalewayClusterNodes(string) (*client.NodeListResult, error)
	GetScalewayClusterNode(string, string) (*client.NodeDetail, error)
	RestartScalewayClusterNode(string, string) (*client.RestartNodeResult, error)
	GetScalewayClusterSSHKeys(string) (*client.ClusterSSHKeysResult, error)
	UpdateScalewayClusterSSHKeys(string, []string) (*client.UpdateClusterSSHKeysResult, error)
	ResyncScalewayClusterSSHKeys(string) (*client.ResyncSSHKeysResult, error)
	UpdateScalewayBastionInstanceType(context.Context, string, string, bool) (*client.UpdateBastionInstanceTypeResult, bool, error)
}

var _ ScalewayAPI = (*client.Client)(nil)

func activeManagedK8sAPI() client.ManagedK8sAPI {
	return apiClient.(client.ManagedK8sAPI)
}

func activeScalewayAPI() ScalewayAPI {
	return apiClient.(ScalewayAPI)
}
