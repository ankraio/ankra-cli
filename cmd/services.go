package cmd

import (
	"context"
	"io"

	"ankra/internal/client"
)

type APIClient interface {
	ListClusters(page int, pageSize int) (*client.ClusterListResponse, error)
	GetCluster(name string) (client.ClusterWithStatus, error)
	DeleteCluster(ctx context.Context, name string) error
	TriggerReconcile(ctx context.Context, clusterID string) (*client.TriggerReconcileResult, error)
	ProvisionCluster(ctx context.Context, clusterID string) (*client.ProvisionClusterResult, error)
	DeprovisionCluster(ctx context.Context, clusterID string, autoDelete, force bool) (*client.DeprovisionClusterResult, error)
	RollToClusterResourceVersion(ctx context.Context, clusterID, versionID string) (*client.RollToClusterResourceVersionResult, error)
	ApplyCluster(ctx context.Context, clusterReq client.CreateImportClusterRequest) (*client.ImportResponse, error)

	ListClusterAddons(clusterID string) ([]client.ClusterAddonListItem, error)
	ListAvailableAddons(clusterID string) ([]client.AvailableAddon, error)
	GetAddonSettings(clusterID, addonName string) (*client.GetAddonSettingsResponse, error)
	UpdateAddonSettings(ctx context.Context, clusterID, addonName string, settings client.AddonSettings) error
	GetAddonByName(clusterID, addonName string) (*client.ClusterAddonListItem, error)
	UninstallAddon(ctx context.Context, clusterID, addonResourceID string, deletePermanently bool) (*client.UninstallAddonResult, error)

	ListClusterOperations(clusterID string) ([]client.OperationResponseListItem, error)
	CancelOperation(ctx context.Context, operationID string) (*client.CancelOperationResult, error)
	CancelJob(ctx context.Context, operationID, jobID string) (*client.CancelJobResult, error)
	ListOperationJobs(clusterID, operationID string, opts *client.ListOperationJobsOptions) (client.GetJobStatusResponse, error)

	GetSopsConfig() (*client.SopsConfigResult, error)
	EncryptYAML(yamlContent string, encryptedPaths []string) (string, error)
	DecryptYAML(encryptedYaml string) (string, error)

	ListClusterManifests(clusterID string) ([]client.ClusterManifestListItem, error)

	ListClusterStacks(clusterID string) ([]client.ClusterStackListItem, error)
	CreateStack(ctx context.Context, clusterID, name, description string) (*client.CreateStackResult, error)
	DeleteStack(ctx context.Context, clusterID, stackName string) (*client.DeleteStackResult, error)
	RenameStack(ctx context.Context, clusterID, stackName, newName string) (*client.RenameStackResult, error)
	GetStackHistory(clusterID, stackName string) (*client.GetStackHistoryResponse, error)
	CloneStackToCluster(ctx context.Context, targetClusterID string, cloneReq client.CloneStackToClusterRequest) (*client.CloneStackToClusterResult, error)

	ListOrganisations() ([]client.OrganisationSummary, error)
	SwitchOrganisation(orgID string) (*client.SwitchOrganisationResponse, error)
	CreateOrganisation(name string, country *string) (*client.CreateOrganisationResponse, error)
	GetOrganisation(orgID string) (*client.OrganisationFull, error)
	InviteUserToOrganisation(inviteReq client.InviteUserRequest) (*client.InviteUserResponse, error)
	RemoveUserFromOrganisation(removeReq client.RemoveUserRequest) (*client.RemoveUserResponse, error)

	ListCredentials(provider *string) ([]client.Credential, error)
	ValidateCredentialName(name string) (*client.CredentialValidationResult, error)
	DeleteCredential(ctx context.Context, credentialID, organisationID string) (*client.DeleteCredentialResult, error)
	GetCredential(credentialID string) (*client.CredentialDetail, error)

	ListAPITokens() ([]client.APIToken, error)
	CreateAPIToken(name string, expiresAt *string) (*client.CreateAPITokenResponse, error)
	RevokeAPIToken(tokenID string) (*client.RevokeAPITokenResponse, error)
	DeleteAPIToken(tokenID string) (*client.DeleteAPITokenResponse, error)

	StreamChat(clusterID *string, chatReq client.ChatRequest) (<-chan client.ChatStreamEvent, error)
	ListChatHistory(clusterID *string, limit, offset int) (*client.ListConversationsResponse, error)
	GetChatConversation(conversationID string) (*client.ChatConversation, error)
	DeleteChatConversation(conversationID string) (*client.DeleteConversationResponse, error)
	GetClusterHealth(clusterID string, includeAI bool) (*client.ClusterHealth, error)

	ListCharts(page, pageSize int, onlySubscribed bool) (*client.ListChartsResponse, error)
	SearchCharts(query string) ([]client.ChartItem, error)
	GetChartDetails(chartName, repositoryURL string) (*client.ChartDetails, error)

	ListPods(clusterID string, opts *client.ListPodsOptions) (*client.ListPodsResponse, error)
	GetResources(clusterID string, req client.GetResourcesRequest) (*client.GetResourcesResponse, error)
	StreamPodLogs(ctx context.Context, clusterID string, opts client.PodLogOptions, writer io.Writer) error
	ListHelmReleases(clusterID string, opts *client.HelmReleasesOptions) (*client.HelmReleasesResponse, error)
	UninstallHelmRelease(clusterID, releaseName, namespace string) (*client.UninstallHelmReleaseResponse, error)

	ListHelmRegistries() (*client.ListHelmRegistriesResponse, error)
	GetHelmRegistry(registryName string) (*client.GetHelmRegistryResponse, error)
	CreateHelmRegistry(req client.CreateHelmRegistryRequest) (*client.CreateHelmRegistryResponse, error)
	DeleteHelmRegistry(registryName string) (*client.DeleteHelmRegistryResponse, error)
	ListHelmRegistryCredentials() (*client.ListHelmCredentialsResponse, error)
	CreateHelmRegistryCredential(req client.CreateHelmCredentialRequest) (*client.CreateHelmCredentialResponse, error)
	DeleteHelmRegistryCredential(credentialName string) (*client.DeleteHelmCredentialResponse, error)

	GetClusterAgent(clusterID string) (*client.AgentInfo, error)
	GetAgentToken(clusterID string) (*client.AgentToken, error)
	GenerateAgentToken(ctx context.Context, clusterID string) (*client.AgentToken, error)
	UpgradeClusterAgent(ctx context.Context, clusterID string) (*client.UpgradeAgentResult, error)

	CreateHetznerCluster(req client.CreateHetznerClusterRequest) (*client.CreateHetznerClusterResponse, error)
	DeprovisionHetznerCluster(clusterID string) (*client.DeprovisionHetznerClusterResponse, error)
	GetHetznerWorkerCount(clusterID string) (*client.WorkerCountResult, error)
	ScaleHetznerWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error)
	GetHetznerK8sVersion(clusterID string) (*client.K8sVersionInfo, error)
	UpgradeHetznerK8sVersion(clusterID, targetVersion string) (*client.UpgradeK8sVersionResult, error)
	ListHetznerNodeGroups(clusterID string) (*client.NodeGroupListResult, error)
	AddHetznerNodeGroup(clusterID string, req client.AddNodeGroupRequest) (*client.AddNodeGroupResult, error)
	ScaleHetznerNodeGroup(clusterID, groupName string, count int) (*client.ScaleNodeGroupResult, error)
	UpdateHetznerNodeGroupInstanceType(clusterID, groupName, instanceType string) (*client.UpdateNodeGroupResult, error)
	DeleteHetznerNodeGroup(clusterID, groupName string) (*client.DeleteNodeGroupResult, error)

	ListHetznerCredentials() ([]client.HetznerCredentialListItem, error)
	CreateHetznerCredential(req client.CreateHetznerCredentialRequest) (*client.CreateHetznerCredentialResponse, error)
	ListSSHKeyCredentials() ([]client.HetznerCredentialListItem, error)
	CreateSSHKeyCredential(req client.CreateSSHKeyCredentialRequest) (*client.CreateSSHKeyCredentialResponse, error)

	CreateOvhCluster(req client.CreateOvhClusterRequest) (*client.CreateOvhClusterResponse, error)
	DeprovisionOvhCluster(clusterID string) (*client.DeprovisionOvhClusterResponse, error)
	GetOvhWorkerCount(clusterID string) (*client.WorkerCountResult, error)
	ScaleOvhWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error)
	GetOvhK8sVersion(clusterID string) (*client.K8sVersionInfo, error)
	UpgradeOvhK8sVersion(clusterID, targetVersion string) (*client.UpgradeK8sVersionResult, error)
	ListOvhNodeGroups(clusterID string) (*client.NodeGroupListResult, error)
	AddOvhNodeGroup(clusterID string, req client.AddNodeGroupRequest) (*client.AddNodeGroupResult, error)
	ScaleOvhNodeGroup(clusterID, groupName string, count int) (*client.ScaleNodeGroupResult, error)
	UpdateOvhNodeGroupInstanceType(clusterID, groupName, instanceType string) (*client.UpdateNodeGroupResult, error)
	DeleteOvhNodeGroup(clusterID, groupName string) (*client.DeleteNodeGroupResult, error)

	ListOvhCredentials() ([]client.OvhCredentialListItem, error)
	CreateOvhCredential(req client.CreateOvhCredentialRequest) (*client.CreateOvhCredentialResponse, error)
	ListOvhSSHKeyCredentials() ([]client.OvhCredentialListItem, error)
	CreateOvhSSHKeyCredential(req client.CreateSSHKeyCredentialRequest) (*client.CreateSSHKeyCredentialResponse, error)

	CreateUpcloudCluster(req client.CreateUpcloudClusterRequest) (*client.CreateUpcloudClusterResponse, error)
	DeprovisionUpcloudCluster(clusterID string) (*client.DeprovisionUpcloudClusterResponse, error)
	GetUpcloudWorkerCount(clusterID string) (*client.WorkerCountResult, error)
	ScaleUpcloudWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error)
	GetUpcloudK8sVersion(clusterID string) (*client.K8sVersionInfo, error)
	UpgradeUpcloudK8sVersion(clusterID, targetVersion string) (*client.UpgradeK8sVersionResult, error)
	ListUpcloudNodeGroups(clusterID string) (*client.NodeGroupListResult, error)
	AddUpcloudNodeGroup(clusterID string, req client.AddNodeGroupRequest) (*client.AddNodeGroupResult, error)
	ScaleUpcloudNodeGroup(clusterID, groupName string, count int) (*client.ScaleNodeGroupResult, error)
	UpdateUpcloudNodeGroupInstanceType(clusterID, groupName, instanceType string) (*client.UpdateNodeGroupResult, error)
	DeleteUpcloudNodeGroup(clusterID, groupName string) (*client.DeleteNodeGroupResult, error)

	ListUpcloudCredentials() ([]client.UpcloudCredentialListItem, error)
	CreateUpcloudCredential(req client.CreateUpcloudCredentialRequest) (*client.CreateUpcloudCredentialResponse, error)
	ListUpcloudSSHKeyCredentials() ([]client.UpcloudCredentialListItem, error)
	CreateUpcloudSSHKeyCredential(req client.CreateSSHKeyCredentialRequest) (*client.CreateSSHKeyCredentialResponse, error)
}
