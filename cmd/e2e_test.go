package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

type baseMock struct{}

func (m baseMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetCluster(name string) (client.ClusterWithStatus, error) {
	return client.ClusterWithStatus{}, errors.New("not implemented")
}

func (m baseMock) DeleteCluster(ctx context.Context, name string) error {
	return errors.New("not implemented")
}

func (m baseMock) TriggerReconcile(ctx context.Context, clusterID string) (*client.TriggerReconcileResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ProvisionCluster(ctx context.Context, clusterID string) (*client.ProvisionClusterResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeprovisionCluster(ctx context.Context, clusterID string, autoDelete, force bool) (*client.DeprovisionClusterResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) RollToClusterResourceVersion(ctx context.Context, clusterID, versionID string) (*client.RollToClusterResourceVersionResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ApplyCluster(ctx context.Context, clusterReq client.CreateImportClusterRequest) (*client.ImportResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListClusterAddons(clusterID string) ([]client.ClusterAddonListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListAvailableAddons(clusterID string) ([]client.AvailableAddon, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetAddonSettings(clusterID, addonName string) (*client.GetAddonSettingsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpdateAddonSettings(ctx context.Context, clusterID, addonName string, settings client.AddonSettings) error {
	return errors.New("not implemented")
}

func (m baseMock) GetAddonByName(clusterID, addonName string) (*client.ClusterAddonListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UninstallAddon(ctx context.Context, clusterID, addonResourceID string, deletePermanently bool) (*client.UninstallAddonResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListClusterOperations(clusterID string) ([]client.OperationResponseListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CancelOperation(ctx context.Context, operationID string) (*client.CancelOperationResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CancelJob(ctx context.Context, operationID, jobID string) (*client.CancelJobResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListOperationJobs(clusterID, operationID string, opts *client.ListOperationJobsOptions) (client.GetJobStatusResponse, error) {
	return client.GetJobStatusResponse{}, errors.New("not implemented")
}

func (m baseMock) GetSopsConfig() (*client.SopsConfigResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) EncryptYAML(yamlContent string, encryptedPaths []string) (string, error) {
	return "", errors.New("not implemented")
}

func (m baseMock) DecryptYAML(encryptedYaml string) (string, error) {
	return "", errors.New("not implemented")
}

func (m baseMock) ListClusterManifests(clusterID string) ([]client.ClusterManifestListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListClusterStacks(clusterID string) ([]client.ClusterStackListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateStack(ctx context.Context, clusterID, name, description string) (*client.CreateStackResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteStack(ctx context.Context, clusterID, stackName string) (*client.DeleteStackResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) RenameStack(ctx context.Context, clusterID, stackName, newName string) (*client.RenameStackResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetStackHistory(clusterID, stackName string) (*client.GetStackHistoryResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CloneStackToCluster(ctx context.Context, targetClusterID string, cloneReq client.CloneStackToClusterRequest) (*client.CloneStackToClusterResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) SwitchOrganisation(orgID string) (*client.SwitchOrganisationResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateOrganisation(name string, country *string) (*client.CreateOrganisationResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetOrganisation(orgID string) (*client.OrganisationFull, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) InviteUserToOrganisation(inviteReq client.InviteUserRequest) (*client.InviteUserResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) RemoveUserFromOrganisation(removeReq client.RemoveUserRequest) (*client.RemoveUserResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListCredentials(provider *string) ([]client.Credential, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ValidateCredentialName(name string) (*client.CredentialValidationResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteCredential(ctx context.Context, credentialID, organisationID string) (*client.DeleteCredentialResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetCredential(credentialID string) (*client.CredentialDetail, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListAPITokens() ([]client.APIToken, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateAPIToken(name string, expiresAt *string) (*client.CreateAPITokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) RevokeAPIToken(tokenID string) (*client.RevokeAPITokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteAPIToken(tokenID string) (*client.DeleteAPITokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) StreamChat(clusterID *string, chatReq client.ChatRequest) (<-chan client.ChatStreamEvent, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListChatHistory(clusterID *string, limit, offset int) (*client.ListConversationsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetChatConversation(conversationID string) (*client.ChatConversation, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteChatConversation(conversationID string) (*client.DeleteConversationResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetClusterHealth(clusterID string, includeAI bool) (*client.ClusterHealth, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListCharts(page, pageSize int, onlySubscribed bool) (*client.ListChartsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) SearchCharts(query string) ([]client.ChartItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetChartDetails(chartName, repositoryURL string) (*client.ChartDetails, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListPods(clusterID string, opts *client.ListPodsOptions) (*client.ListPodsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetResources(clusterID string, req client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) StreamPodLogs(ctx context.Context, clusterID string, opts client.PodLogOptions, writer io.Writer) error {
	return errors.New("not implemented")
}

func (m baseMock) ListHelmReleases(clusterID string, opts *client.HelmReleasesOptions) (*client.HelmReleasesResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UninstallHelmRelease(clusterID, releaseName, namespace string) (*client.UninstallHelmReleaseResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListHelmRegistries() (*client.ListHelmRegistriesResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetHelmRegistry(registryName string) (*client.GetHelmRegistryResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateHelmRegistry(req client.CreateHelmRegistryRequest) (*client.CreateHelmRegistryResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteHelmRegistry(registryName string) (*client.DeleteHelmRegistryResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListHelmRegistryCredentials() (*client.ListHelmCredentialsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateHelmRegistryCredential(req client.CreateHelmCredentialRequest) (*client.CreateHelmCredentialResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteHelmRegistryCredential(credentialName string) (*client.DeleteHelmCredentialResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetClusterAgent(clusterID string) (*client.AgentInfo, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetAgentToken(clusterID string) (*client.AgentToken, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GenerateAgentToken(ctx context.Context, clusterID string) (*client.AgentToken, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpgradeClusterAgent(ctx context.Context, clusterID string) (*client.UpgradeAgentResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateHetznerCluster(req client.CreateHetznerClusterRequest) (*client.CreateHetznerClusterResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeprovisionHetznerCluster(clusterID string) (*client.DeprovisionHetznerClusterResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetHetznerWorkerCount(clusterID string) (*client.WorkerCountResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ScaleHetznerWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetHetznerK8sVersion(clusterID string) (*client.K8sVersionInfo, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpgradeHetznerK8sVersion(clusterID, targetVersion string) (*client.UpgradeK8sVersionResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListHetznerNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) AddHetznerNodeGroup(clusterID string, req client.AddNodeGroupRequest) (*client.AddNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ScaleHetznerNodeGroup(clusterID, groupName string, count int) (*client.ScaleNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpdateHetznerNodeGroupInstanceType(clusterID, groupName, instanceType string) (*client.UpdateNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteHetznerNodeGroup(clusterID, groupName string) (*client.DeleteNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListHetznerCredentials() ([]client.HetznerCredentialListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateHetznerCredential(req client.CreateHetznerCredentialRequest) (*client.CreateHetznerCredentialResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListSSHKeyCredentials() ([]client.HetznerCredentialListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateSSHKeyCredential(req client.CreateSSHKeyCredentialRequest) (*client.CreateSSHKeyCredentialResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateOvhCluster(req client.CreateOvhClusterRequest) (*client.CreateOvhClusterResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeprovisionOvhCluster(clusterID string) (*client.DeprovisionOvhClusterResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetOvhWorkerCount(clusterID string) (*client.WorkerCountResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ScaleOvhWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetOvhK8sVersion(clusterID string) (*client.K8sVersionInfo, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpgradeOvhK8sVersion(clusterID, targetVersion string) (*client.UpgradeK8sVersionResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListOvhNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) AddOvhNodeGroup(clusterID string, req client.AddNodeGroupRequest) (*client.AddNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ScaleOvhNodeGroup(clusterID, groupName string, count int) (*client.ScaleNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpdateOvhNodeGroupInstanceType(clusterID, groupName, instanceType string) (*client.UpdateNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteOvhNodeGroup(clusterID, groupName string) (*client.DeleteNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListOvhCredentials() ([]client.OvhCredentialListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateOvhCredential(req client.CreateOvhCredentialRequest) (*client.CreateOvhCredentialResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListOvhSSHKeyCredentials() ([]client.OvhCredentialListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateOvhSSHKeyCredential(req client.CreateSSHKeyCredentialRequest) (*client.CreateSSHKeyCredentialResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateUpcloudCluster(req client.CreateUpcloudClusterRequest) (*client.CreateUpcloudClusterResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeprovisionUpcloudCluster(clusterID string) (*client.DeprovisionUpcloudClusterResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetUpcloudWorkerCount(clusterID string) (*client.WorkerCountResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ScaleUpcloudWorkers(clusterID string, workerCount int) (*client.ScaleWorkersResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) GetUpcloudK8sVersion(clusterID string) (*client.K8sVersionInfo, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpgradeUpcloudK8sVersion(clusterID, targetVersion string) (*client.UpgradeK8sVersionResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListUpcloudNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) AddUpcloudNodeGroup(clusterID string, req client.AddNodeGroupRequest) (*client.AddNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ScaleUpcloudNodeGroup(clusterID, groupName string, count int) (*client.ScaleNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) UpdateUpcloudNodeGroupInstanceType(clusterID, groupName, instanceType string) (*client.UpdateNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) DeleteUpcloudNodeGroup(clusterID, groupName string) (*client.DeleteNodeGroupResult, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListUpcloudCredentials() ([]client.UpcloudCredentialListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateUpcloudCredential(req client.CreateUpcloudCredentialRequest) (*client.CreateUpcloudCredentialResponse, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) ListUpcloudSSHKeyCredentials() ([]client.UpcloudCredentialListItem, error) {
	return nil, errors.New("not implemented")
}

func (m baseMock) CreateUpcloudSSHKeyCredential(req client.CreateSSHKeyCredentialRequest) (*client.CreateSSHKeyCredentialResponse, error) {
	return nil, errors.New("not implemented")
}

type clusterListMock struct {
	baseMock
	clusters []client.ClusterListItem
}

func (m *clusterListMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{
		Result: m.clusters,
		Pagination: client.Pagination{
			TotalCount: len(m.clusters),
			TotalPages: 1,
			Page:       1,
			PageSize:   25,
		},
	}, nil
}

type clusterGetMock struct {
	baseMock
	cluster client.ClusterWithStatus
}

func (m *clusterGetMock) GetCluster(name string) (client.ClusterWithStatus, error) {
	if m.cluster.Name == name {
		return m.cluster, nil
	}
	return client.ClusterWithStatus{}, errors.New("cluster not found")
}

type orgListMock struct {
	baseMock
	organisations []client.OrganisationSummary
}

func (m *orgListMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	return m.organisations, nil
}

type credentialsListMock struct {
	baseMock
	credentials []client.Credential
}

func (m *credentialsListMock) ListCredentials(provider *string) ([]client.Credential, error) {
	if provider != nil && *provider != "" {
		var filtered []client.Credential
		for _, cred := range m.credentials {
			if cred.Provider == *provider {
				filtered = append(filtered, cred)
			}
		}
		return filtered, nil
	}
	return m.credentials, nil
}

func setMockClient(t *testing.T, mock APIClient) {
	t.Helper()
	originalClient := apiClient
	originalToken := apiToken
	originalBaseURL := baseURL
	apiClient = mock
	apiToken = "test-token"
	baseURL = "https://test.ankra.app"
	t.Setenv("ANKRA_API_TOKEN", "test-token")
	t.Cleanup(func() {
		apiClient = originalClient
		apiToken = originalToken
		baseURL = originalBaseURL
	})
}

func TestClusterListCommand(t *testing.T) {
	mock := &clusterListMock{
		clusters: []client.ClusterListItem{
			{
				ID:           "cluster-id-1",
				Name:         "production-cluster",
				KubeVersion:  "1.28.0",
				Nodes:        3,
				ControlPlanes: 1,
				State:        "online",
				Kind:         "imported",
				CreatedAt:    "2024-01-01T00:00:00Z",
			},
			{
				ID:           "cluster-id-2",
				Name:         "staging-cluster",
				KubeVersion:  "1.27.0",
				Nodes:        2,
				ControlPlanes: 1,
				State:        "offline",
				Kind:         "imported",
				CreatedAt:    "2024-02-01T00:00:00Z",
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "list")
	})

	if !strings.Contains(stdoutOutput, "production-cluster") {
		t.Errorf("expected output to contain 'production-cluster', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "staging-cluster") {
		t.Errorf("expected output to contain 'staging-cluster', got: %s", stdoutOutput)
	}
}

func TestClusterListEmptyCommand(t *testing.T) {
	mock := &clusterListMock{
		clusters: []client.ClusterListItem{},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "list")
	})

	if !strings.Contains(stdoutOutput, "No clusters found") {
		t.Errorf("expected 'No clusters found' message, got: %s", stdoutOutput)
	}
}

func TestClusterInfoCommand(t *testing.T) {
	mock := &clusterGetMock{
		cluster: client.ClusterWithStatus{
			ID:          "test-cluster-id",
			Name:        "my-cluster",
			Environment: "production",
			KubeVersion: "1.28.0",
			State:       "online",
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "info", "my-cluster")
	})

	if !strings.Contains(stdoutOutput, "my-cluster") {
		t.Errorf("expected output to contain 'my-cluster', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "production") {
		t.Errorf("expected output to contain 'production', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "1.28.0") {
		t.Errorf("expected output to contain '1.28.0', got: %s", stdoutOutput)
	}
}

func TestClusterInfoNotFoundCommand(t *testing.T) {
	mock := &clusterGetMock{
		cluster: client.ClusterWithStatus{
			Name: "existing-cluster",
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "info", "nonexistent-cluster")
	})

	if !strings.Contains(stdoutOutput, "Error") {
		t.Errorf("expected error output for nonexistent cluster, got: %s", stdoutOutput)
	}
}

func TestOrgListCommand(t *testing.T) {
	orgName := "Ankra Inc"
	orgRole := "admin"
	orgStatus := "active"
	mock := &orgListMock{
		organisations: []client.OrganisationSummary{
			{
				OrganisationID: "org-id-1",
				Name:           &orgName,
				Role:           &orgRole,
				Status:         &orgStatus,
				UserCurrent:    true,
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("org", "list")
	})

	if !strings.Contains(stdoutOutput, "Ankra Inc") {
		t.Errorf("expected output to contain 'Ankra Inc', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "org-id-1") {
		t.Errorf("expected output to contain 'org-id-1', got: %s", stdoutOutput)
	}
}

func TestOrgListEmptyCommand(t *testing.T) {
	mock := &orgListMock{
		organisations: []client.OrganisationSummary{},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("org", "list")
	})

	if !strings.Contains(stdoutOutput, "No organisations found") {
		t.Errorf("expected 'No organisations found' message, got: %s", stdoutOutput)
	}
}

func TestCredentialsListCommand(t *testing.T) {
	mock := &credentialsListMock{
		credentials: []client.Credential{
			{
				ID:           "cred-id-1",
				Name:         "github-deploy-key",
				Provider:     "github",
				ClusterCount: 2,
				CreatedAt:    "2024-01-15T00:00:00Z",
			},
			{
				ID:           "cred-id-2",
				Name:         "gitlab-token",
				Provider:     "gitlab",
				ClusterCount: 1,
				CreatedAt:    "2024-03-01T00:00:00Z",
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("credentials", "list")
	})

	if !strings.Contains(stdoutOutput, "github-deploy-key") {
		t.Errorf("expected output to contain 'github-deploy-key', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "gitlab-token") {
		t.Errorf("expected output to contain 'gitlab-token', got: %s", stdoutOutput)
	}
}

func TestCredentialsListEmptyCommand(t *testing.T) {
	mock := &credentialsListMock{
		credentials: []client.Credential{},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("credentials", "list")
	})

	if !strings.Contains(stdoutOutput, "No credentials found") {
		t.Errorf("expected 'No credentials found' message, got: %s", stdoutOutput)
	}
}

func TestHelpCommandDoesNotError(t *testing.T) {
	_, err := executeCommand("--help")
	if err != nil {
		t.Errorf("expected no error for --help, got: %v", err)
	}
}

func TestClusterHelpCommandDoesNotError(t *testing.T) {
	_, err := executeCommand("cluster", "--help")
	if err != nil {
		t.Errorf("expected no error for cluster --help, got: %v", err)
	}
}

func writeSelectedClusterJSON(t *testing.T) {
	t.Helper()
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)
	ankraDir := filepath.Join(tempDir, ".ankra")
	if err := os.MkdirAll(ankraDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(client.ClusterListItem{ID: "test-cluster-id", Name: "test-cluster"})
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ankraDir, "selected.json"), data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

type clusterAddonsListMock struct {
	baseMock
	addons []client.ClusterAddonListItem
}

func (m *clusterAddonsListMock) ListClusterAddons(clusterID string) ([]client.ClusterAddonListItem, error) {
	return m.addons, nil
}

func TestClusterAddonsListCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &clusterAddonsListMock{
		addons: []client.ClusterAddonListItem{
			{
				ID:            "addon-res-id",
				Name:          "nginx-ingress",
				ChartName:     "ingress-nginx",
				ChartVersion:  "4.11.0",
				RepositoryURL: "https://kubernetes.github.io/ingress-nginx",
				Namespace:     "ingress-nginx",
				CreatedAt:     time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC),
				UpdatedAt:     time.Date(2024, 3, 2, 12, 0, 0, 0, time.UTC),
				ThroughAnkra:  true,
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "addons", "list")
	})

	if !strings.Contains(stdoutOutput, "nginx-ingress") {
		t.Errorf("expected output to contain 'nginx-ingress', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "ingress-nginx") {
		t.Errorf("expected output to contain 'ingress-nginx', got: %s", stdoutOutput)
	}
}

type clusterManifestsListMock struct {
	baseMock
	manifests []client.ClusterManifestListItem
}

func (m *clusterManifestsListMock) ListClusterManifests(clusterID string) ([]client.ClusterManifestListItem, error) {
	return m.manifests, nil
}

func TestClusterManifestsListCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	yamlBody := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm1\n"
	mock := &clusterManifestsListMock{
		manifests: []client.ClusterManifestListItem{
			{
				Name:           "cm-manifest",
				ManifestBase64: base64.StdEncoding.EncodeToString([]byte(yamlBody)),
				Namespace:      "default",
				State:          "up",
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "manifests", "list")
	})

	if !strings.Contains(stdoutOutput, "cm-manifest") {
		t.Errorf("expected output to contain 'cm-manifest', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "ConfigMap") {
		t.Errorf("expected output to contain 'ConfigMap', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "default") {
		t.Errorf("expected output to contain 'default', got: %s", stdoutOutput)
	}
}

type clusterStacksListMock struct {
	baseMock
	stacks []client.ClusterStackListItem
}

func (m *clusterStacksListMock) ListClusterStacks(clusterID string) ([]client.ClusterStackListItem, error) {
	return m.stacks, nil
}

func TestClusterStacksListCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &clusterStacksListMock{
		stacks: []client.ClusterStackListItem{
			{
				Name:        "production-stack",
				Description: "Main workloads",
				State:       "up",
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "stacks", "list")
	})

	if !strings.Contains(stdoutOutput, "production-stack") {
		t.Errorf("expected output to contain 'production-stack', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Main workloads") {
		t.Errorf("expected output to contain 'Main workloads', got: %s", stdoutOutput)
	}
}

type clusterOperationsListMock struct {
	baseMock
	operations []client.OperationResponseListItem
}

func (m *clusterOperationsListMock) ListClusterOperations(clusterID string) ([]client.OperationResponseListItem, error) {
	return m.operations, nil
}

func TestClusterOperationsListCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &clusterOperationsListMock{
		operations: []client.OperationResponseListItem{
			{
				ID:        "operation-uuid-1",
				Name:      "sync-cluster",
				Status:    "success",
				CreatedAt: "2024-04-01T10:00:00Z",
				UpdatedAt: "2024-04-01T10:05:00Z",
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "operations", "list")
	})

	if !strings.Contains(stdoutOutput, "operation-uuid-1") {
		t.Errorf("expected output to contain 'operation-uuid-1', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "sync-cluster") {
		t.Errorf("expected output to contain 'sync-cluster', got: %s", stdoutOutput)
	}
}

type clusterAgentStatusMock struct {
	baseMock
	agent *client.AgentInfo
}

func (m *clusterAgentStatusMock) GetClusterAgent(clusterID string) (*client.AgentInfo, error) {
	return m.agent, nil
}

func TestClusterAgentStatusCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	agentVersion := "2.5.1"
	checkedIn := "2024-05-01T08:00:00Z"
	mock := &clusterAgentStatusMock{
		agent: &client.AgentInfo{
			AgentVersion: &agentVersion,
			CreatedAt:    "2024-01-10T00:00:00Z",
			CheckedInAt:  &checkedIn,
			Upgrading:    false,
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "agent", "status")
	})

	if !strings.Contains(stdoutOutput, "test-cluster") {
		t.Errorf("expected output to contain 'test-cluster', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "2.5.1") {
		t.Errorf("expected output to contain '2.5.1', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "connected") {
		t.Errorf("expected output to contain 'connected', got: %s", stdoutOutput)
	}
}

type helmRegistriesListMock struct {
	baseMock
	registries []client.HelmRegistryListItem
}

func (m *helmRegistriesListMock) ListHelmRegistries() (*client.ListHelmRegistriesResponse, error) {
	return &client.ListHelmRegistriesResponse{
		Result: m.registries,
	}, nil
}

type helmRegistryCredentialsListMock struct {
	baseMock
	credentials []client.HelmCredentialListItem
}

func (m *helmRegistryCredentialsListMock) ListHelmRegistryCredentials() (*client.ListHelmCredentialsResponse, error) {
	return &client.ListHelmCredentialsResponse{
		Result: m.credentials,
	}, nil
}

func TestHelmRegistriesListCommand(t *testing.T) {
	mock := &helmRegistriesListMock{
		registries: []client.HelmRegistryListItem{
			{
				Name:      "oci-production",
				Type:      "oci",
				URL:       "oci://registry.example.com/charts",
				Status:    "ready",
				CreatedAt: "2024-01-01T00:00:00Z",
			},
			{
				Name:      "github-charts",
				Type:      "github",
				URL:       "https://github.com/org/charts",
				Status:    "ready",
				CreatedAt: "2024-02-01T00:00:00Z",
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("helm", "registries", "list")
	})

	if !strings.Contains(stdoutOutput, "oci-production") {
		t.Errorf("expected output to contain 'oci-production', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "github-charts") {
		t.Errorf("expected output to contain 'github-charts', got: %s", stdoutOutput)
	}
}

func TestHelmRegistriesListEmptyCommand(t *testing.T) {
	mock := &helmRegistriesListMock{
		registries: []client.HelmRegistryListItem{},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("helm", "registries", "list")
	})

	if !strings.Contains(stdoutOutput, "No Helm registries found.") {
		t.Errorf("expected 'No Helm registries found.' message, got: %s", stdoutOutput)
	}
}

func TestHelmCredentialsListCommand(t *testing.T) {
	mock := &helmRegistryCredentialsListMock{
		credentials: []client.HelmCredentialListItem{
			{
				Name:      "registry-pull-secret",
				CreatedAt: "2024-01-15T00:00:00Z",
			},
			{
				Name:      "github-helm-token",
				CreatedAt: "2024-03-01T00:00:00Z",
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("helm", "credentials", "list")
	})

	if !strings.Contains(stdoutOutput, "registry-pull-secret") {
		t.Errorf("expected output to contain 'registry-pull-secret', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "github-helm-token") {
		t.Errorf("expected output to contain 'github-helm-token', got: %s", stdoutOutput)
	}
}

type chartsListMock struct {
	baseMock
	charts []client.ChartItem
}

func (m *chartsListMock) ListCharts(page, pageSize int, onlySubscribed bool) (*client.ListChartsResponse, error) {
	return &client.ListChartsResponse{
		Charts: m.charts,
		Pagination: client.ChartsPagination{
			Page:       1,
			PageSize:   pageSize,
			TotalPages: 1,
		},
	}, nil
}

type chartsSearchMock struct {
	baseMock
	charts []client.ChartItem
}

func (m *chartsSearchMock) SearchCharts(query string) ([]client.ChartItem, error) {
	return m.charts, nil
}

type tokensListMock struct {
	baseMock
	tokens []client.APIToken
}

func (m *tokensListMock) ListAPITokens() ([]client.APIToken, error) {
	return m.tokens, nil
}

type orgSwitchMock struct {
	baseMock
}

func (m *orgSwitchMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	orgName := "Switchable Org"
	return []client.OrganisationSummary{
		{
			OrganisationID: "org-switch-target",
			Name:           &orgName,
		},
	}, nil
}

func (m *orgSwitchMock) SwitchOrganisation(orgID string) (*client.SwitchOrganisationResponse, error) {
	return &client.SwitchOrganisationResponse{Success: true, Message: "Switched."}, nil
}

type orgCreateOnlyMock struct {
	baseMock
}

func (m *orgCreateOnlyMock) CreateOrganisation(name string, country *string) (*client.CreateOrganisationResponse, error) {
	return &client.CreateOrganisationResponse{
		OrganisationID: "new-org-created-id",
		Message:        "Organisation created.",
	}, nil
}

type credentialGetMock struct {
	baseMock
	detail *client.CredentialDetail
}

func (m *credentialGetMock) GetCredential(credentialID string) (*client.CredentialDetail, error) {
	return m.detail, nil
}

func TestChartsListCommand(t *testing.T) {
	mock := &chartsListMock{
		charts: []client.ChartItem{
			{Name: "nginx-chart", Version: "1.0.0", RepositoryName: "repo-a", Description: "Nginx"},
			{Name: "redis-chart", Version: "2.1.0", RepositoryName: "repo-b", Description: "Redis"},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("charts", "list")
	})

	if !strings.Contains(stdoutOutput, "nginx-chart") {
		t.Errorf("expected output to contain 'nginx-chart', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "redis-chart") {
		t.Errorf("expected output to contain 'redis-chart', got: %s", stdoutOutput)
	}
}

func TestChartsSearchCommand(t *testing.T) {
	mock := &chartsSearchMock{
		charts: []client.ChartItem{
			{Name: "prometheus-stack", Version: "45.0.0", RepositoryName: "prom", Description: "Prometheus"},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("charts", "search", "prom")
	})

	if !strings.Contains(stdoutOutput, "prometheus-stack") {
		t.Errorf("expected output to contain 'prometheus-stack', got: %s", stdoutOutput)
	}
}

func TestTokensListCommand(t *testing.T) {
	mock := &tokensListMock{
		tokens: []client.APIToken{
			{ID: "token-id-1", Name: "ci-token", CreatedAt: "2024-06-01T12:00:00Z", ExpiresAt: "2025-06-01T12:00:00Z", Revoked: false},
			{ID: "token-id-2", Name: "local-dev", CreatedAt: "2024-07-01T12:00:00Z", ExpiresAt: "2025-07-01T12:00:00Z", Revoked: false},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("tokens", "list")
	})

	if !strings.Contains(stdoutOutput, "ci-token") {
		t.Errorf("expected output to contain 'ci-token', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "local-dev") {
		t.Errorf("expected output to contain 'local-dev', got: %s", stdoutOutput)
	}
}

func TestTokensListEmptyCommand(t *testing.T) {
	mock := &tokensListMock{tokens: []client.APIToken{}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("tokens", "list")
	})

	if !strings.Contains(stdoutOutput, "No API tokens found") {
		t.Errorf("expected 'No API tokens found' message, got: %s", stdoutOutput)
	}
}

func TestOrgSwitchCommand(t *testing.T) {
	mock := &orgSwitchMock{}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("org", "switch", "org-switch-target")
	})

	if !strings.Contains(stdoutOutput, "Switched to organisation") {
		t.Errorf("expected success message for org switch, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Switchable Org") {
		t.Errorf("expected organisation name in output, got: %s", stdoutOutput)
	}
}

func TestOrgCreateCommand(t *testing.T) {
	mock := &orgCreateOnlyMock{}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("org", "create", "Fresh Org")
	})

	if !strings.Contains(stdoutOutput, "new-org-created-id") {
		t.Errorf("expected organisation ID in output, got: %s", stdoutOutput)
	}
}

func TestCredentialsGetCommand(t *testing.T) {
	mock := &credentialGetMock{
		detail: &client.CredentialDetail{
			ID:             "cred-detail-1",
			Name:           "github-app-cred",
			Provider:       "github",
			CreatedAt:      "2024-03-15T10:00:00Z",
			OrganisationID: "org-1",
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("credentials", "get", "cred-detail-1")
	})

	if !strings.Contains(stdoutOutput, "github-app-cred") {
		t.Errorf("expected credential name in output, got: %s", stdoutOutput)
	}
}

type hetznerCredentialsListMock struct {
	baseMock
	creds []client.HetznerCredentialListItem
}

func (m *hetznerCredentialsListMock) ListHetznerCredentials() ([]client.HetznerCredentialListItem, error) {
	return m.creds, nil
}

func TestHetznerCredentialsListCommand(t *testing.T) {
	mock := &hetznerCredentialsListMock{
		creds: []client.HetznerCredentialListItem{
			{ID: "hz-cred-1", Name: "hetzner-prod-api", Available: true, CreatedAt: "2024-01-10T00:00:00Z"},
			{ID: "hz-cred-2", Name: "hetzner-staging-api", Available: true, CreatedAt: "2024-02-10T00:00:00Z"},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("credentials", "hetzner", "list")
	})

	if !strings.Contains(stdoutOutput, "hetzner-prod-api") {
		t.Errorf("expected output to contain 'hetzner-prod-api', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "hetzner-staging-api") {
		t.Errorf("expected output to contain 'hetzner-staging-api', got: %s", stdoutOutput)
	}
}

type sshKeyCredentialsListMock struct {
	baseMock
	creds []client.HetznerCredentialListItem
}

func (m *sshKeyCredentialsListMock) ListSSHKeyCredentials() ([]client.HetznerCredentialListItem, error) {
	return m.creds, nil
}

func TestHetznerSSHKeyListCommand(t *testing.T) {
	mock := &sshKeyCredentialsListMock{
		creds: []client.HetznerCredentialListItem{
			{ID: "ssh-1", Name: "deploy-key-main", Available: true, CreatedAt: "2024-03-01T00:00:00Z"},
			{ID: "ssh-2", Name: "ci-runner-key", Available: true, CreatedAt: "2024-03-15T00:00:00Z"},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("credentials", "hetzner", "ssh-key", "list")
	})

	if !strings.Contains(stdoutOutput, "deploy-key-main") {
		t.Errorf("expected output to contain 'deploy-key-main', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "ci-runner-key") {
		t.Errorf("expected output to contain 'ci-runner-key', got: %s", stdoutOutput)
	}
}

type ovhCredentialsListMock struct {
	baseMock
	creds []client.OvhCredentialListItem
}

func (m *ovhCredentialsListMock) ListOvhCredentials() ([]client.OvhCredentialListItem, error) {
	return m.creds, nil
}

func TestOvhCredentialsListCommand(t *testing.T) {
	mock := &ovhCredentialsListMock{
		creds: []client.OvhCredentialListItem{
			{ID: "ovh-cred-1", Name: "ovh-eu-west", Available: true, CreatedAt: "2024-04-01T00:00:00Z"},
			{ID: "ovh-cred-2", Name: "ovh-ca-central", Available: true, CreatedAt: "2024-05-01T00:00:00Z"},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("credentials", "ovh", "list")
	})

	if !strings.Contains(stdoutOutput, "ovh-eu-west") {
		t.Errorf("expected output to contain 'ovh-eu-west', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "ovh-ca-central") {
		t.Errorf("expected output to contain 'ovh-ca-central', got: %s", stdoutOutput)
	}
}

type upcloudCredentialsListMock struct {
	baseMock
	creds []client.UpcloudCredentialListItem
}

func (m *upcloudCredentialsListMock) ListUpcloudCredentials() ([]client.UpcloudCredentialListItem, error) {
	return m.creds, nil
}

func TestUpcloudCredentialsListCommand(t *testing.T) {
	mock := &upcloudCredentialsListMock{
		creds: []client.UpcloudCredentialListItem{
			{ID: "uc-cred-1", Name: "upcloud-helsinki", Available: true, CreatedAt: "2024-06-01T00:00:00Z"},
			{ID: "uc-cred-2", Name: "upcloud-london", Available: true, CreatedAt: "2024-06-15T00:00:00Z"},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("credentials", "upcloud", "list")
	})

	if !strings.Contains(stdoutOutput, "upcloud-helsinki") {
		t.Errorf("expected output to contain 'upcloud-helsinki', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "upcloud-london") {
		t.Errorf("expected output to contain 'upcloud-london', got: %s", stdoutOutput)
	}
}

type hetznerNodeGroupsListMock struct {
	baseMock
	result *client.NodeGroupListResult
}

func (m *hetznerNodeGroupsListMock) ListHetznerNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return m.result, nil
}

func TestClusterHetznerNodeGroupListCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &hetznerNodeGroupsListMock{
		result: &client.NodeGroupListResult{
			NodeGroups: []client.NodeGroupInfo{
				{Name: "workers", InstanceType: "cx33", Count: 2, Labels: map[string]string{"role": "worker"}},
				{Name: "extra-pool", InstanceType: "cx43", Count: 1},
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "node-group", "list", "test-cluster-id")
	})

	if !strings.Contains(stdoutOutput, "workers") {
		t.Errorf("expected output to contain 'workers', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "extra-pool") {
		t.Errorf("expected output to contain 'extra-pool', got: %s", stdoutOutput)
	}
}

type ovhNodeGroupsListMock struct {
	baseMock
	result *client.NodeGroupListResult
}

func (m *ovhNodeGroupsListMock) ListOvhNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return m.result, nil
}

func TestClusterOvhNodeGroupListCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &ovhNodeGroupsListMock{
		result: &client.NodeGroupListResult{
			NodeGroups: []client.NodeGroupInfo{
				{Name: "ovh-default", InstanceType: "b2-7", Count: 3},
				{Name: "ovh-gpu", InstanceType: "r2-15", Count: 1},
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "ovh", "node-group", "list", "test-cluster-id")
	})

	if !strings.Contains(stdoutOutput, "ovh-default") {
		t.Errorf("expected output to contain 'ovh-default', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "ovh-gpu") {
		t.Errorf("expected output to contain 'ovh-gpu', got: %s", stdoutOutput)
	}
}

type upcloudNodeGroupsListMock struct {
	baseMock
	result *client.NodeGroupListResult
}

func (m *upcloudNodeGroupsListMock) ListUpcloudNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return m.result, nil
}

func TestClusterUpcloudNodeGroupListCommand(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &upcloudNodeGroupsListMock{
		result: &client.NodeGroupListResult{
			NodeGroups: []client.NodeGroupInfo{
				{Name: "uc-workers", InstanceType: "2xCPU-4GB", Count: 4},
				{Name: "uc-memory", InstanceType: "4xCPU-8GB", Count: 2},
			},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "upcloud", "node-group", "list", "test-cluster-id")
	})

	if !strings.Contains(stdoutOutput, "uc-workers") {
		t.Errorf("expected output to contain 'uc-workers', got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "uc-memory") {
		t.Errorf("expected output to contain 'uc-memory', got: %s", stdoutOutput)
	}
}
