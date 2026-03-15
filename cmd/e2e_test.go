package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

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

func TestClusterGetCommand(t *testing.T) {
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
		_, _ = executeCommand("cluster", "get", "my-cluster")
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

func TestClusterGetNotFoundCommand(t *testing.T) {
	mock := &clusterGetMock{
		cluster: client.ClusterWithStatus{
			Name: "existing-cluster",
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "get", "nonexistent-cluster")
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
