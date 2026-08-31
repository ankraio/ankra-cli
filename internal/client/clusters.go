package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
)

// ErrClusterNotFound marks a lookup that completed and found nothing, as
// opposed to one that could not be completed. Callers that act on a cluster's
// absence must test for it: a transport or authorisation failure says nothing
// about whether the cluster exists.
var ErrClusterNotFound = errors.New("cluster not found")

// ClusterListItem mirrors cluster's ClusterListItem from
// src/usecase/cluster/list_clusters.py. Backend fields the CLI does not
// currently render (operation, agent_*, resources, *_count, etc.) are
// silently ignored by Go's JSON decoder.
type ClusterListItem struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	State             string          `json:"state"`
	Description       string          `json:"description"`
	Environment       string          `json:"environment"`
	OrganisationID    string          `json:"organisation_id"`
	KubeVersion       string          `json:"kube_version"`
	ControlPlanes     int             `json:"control_planes"`
	Nodes             int             `json:"nodes"`
	CreatedAt         string          `json:"created_at"`
	OperationalAt     *string         `json:"operational_at"`
	SlatedForDeletion *string         `json:"slated_for_deletion_at"`
	DeletedAt         *string         `json:"deleted_at"`
	Kind              string          `json:"kind"`
	Network           *ClusterNetwork `json:"network,omitempty"`
}

// ClusterNetwork mirrors the backend's optional provider network identifiers
// for Ankra-provisioned clusters (VPC, NAT gateway, bastion).
type ClusterNetwork struct {
	Provider     string                 `json:"provider"`
	VPCID        string                 `json:"vpc_id,omitempty"`
	IPRange      string                 `json:"ip_range,omitempty"`
	NATGatewayID string                 `json:"nat_gateway_id,omitempty"`
	EgressIP     string                 `json:"egress_ip,omitempty"`
	Bastion      *ClusterNetworkBastion `json:"bastion,omitempty"`
}

// ClusterNetworkBastion carries the bastion identifiers inside ClusterNetwork.
type ClusterNetworkBastion struct {
	ID        string `json:"id,omitempty"`
	PublicIP  string `json:"public_ip,omitempty"`
	PrivateIP string `json:"private_ip,omitempty"`
}

type ClusterListResponse struct {
	Result     []ClusterListItem `json:"result"`
	Pagination Pagination        `json:"pagination"`
}

func (c *Client) ListClusters(page int, pageSize int) (*ClusterListResponse, error) {
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = 25
	}

	url := fmt.Sprintf("%s/api/v1/clusters?page=%d&page_size=%d", c.BaseURL, page, pageSize)
	var response *ClusterListResponse
	if err := c.getJSON(url, &response); err != nil {
		return nil, err
	}

	return response, nil
}

// GetCluster looks up a cluster by exact name. The backend's
// /api/v1/clusters?cluster_name=... mode returns a ClusterListResponse
// with the matching row in `result` (or an empty result on no match).
func (c *Client) GetCluster(name string) (ClusterListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters?cluster_name=%s", c.BaseURL, neturl.QueryEscape(name))
	var wrapper ClusterListResponse
	if err := c.getJSON(url, &wrapper); err != nil {
		return ClusterListItem{}, err
	}
	for _, cluster := range wrapper.Result {
		if cluster.Name == name {
			return cluster, nil
		}
	}
	return ClusterListItem{}, fmt.Errorf("no cluster found for name %q: %w", name, ErrClusterNotFound)
}

// GetClusterByID looks up a cluster by its UUID. Passing an explicit page
// forces the backend's paginated ClusterListResponse shape so the matching
// row is returned in `result`. The Kind field on the result identifies the
// cloud provider (hetzner, ovh, upcloud) for provider-agnostic commands.
func (c *Client) GetClusterByID(clusterID string) (ClusterListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters?cluster_id=%s&page=1&page_size=1", c.BaseURL, neturl.QueryEscape(clusterID))
	var wrapper ClusterListResponse
	if err := c.getJSON(url, &wrapper); err != nil {
		return ClusterListItem{}, err
	}
	for _, cluster := range wrapper.Result {
		if cluster.ID == clusterID {
			return cluster, nil
		}
	}
	return ClusterListItem{}, fmt.Errorf("no cluster found for id %q: %w", clusterID, ErrClusterNotFound)
}

func (c *Client) DeleteCluster(ctx context.Context, name string) error {
	url := fmt.Sprintf("%s/api/v1/clusters/%s", c.BaseURL, neturl.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("creating DELETE request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("sending DELETE to %s: %w", url, err)
	}
	defer closeBody(resp)

	bodyBytes, err := readResponseBody(resp)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("status %d: %s", resp.StatusCode, redactedBodyForError(bodyBytes, 500)))
	}
	return nil
}

type ProvisionClusterResult struct {
	MarkedToStartAt string `json:"marked_to_start_at"`
}

type DeprovisionClusterResult struct {
	MarkedForDeprovisionAt string `json:"marked_for_deprovision_at"`
}

func (c *Client) ProvisionCluster(ctx context.Context, clusterID string) (*ProvisionClusterResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/provision", c.BaseURL, neturl.PathEscape(clusterID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("provision failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result ProvisionClusterResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

// DeprovisionCluster calls the generic deprovision endpoint. The historical
// auto_delete/force query parameters are gone: the backend parses and
// discards both (a preserved quirk of the original API), so sending them
// only suggested behaviour that never happened.
func (c *Client) DeprovisionCluster(ctx context.Context, clusterID string) (*DeprovisionClusterResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/deprovision", c.BaseURL, neturl.PathEscape(clusterID))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("deprovision failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result DeprovisionClusterResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

type RollToClusterResourceVersionRequest struct {
	ClusterID string `json:"cluster_id"`
	VersionID string `json:"version_id"`
}

type RollToClusterResourceVersionResult struct {
	Ok bool `json:"ok"`
}

func (c *Client) RollToClusterResourceVersion(ctx context.Context, clusterID, versionID string) (*RollToClusterResourceVersionResult, error) {
	url := c.BaseURL + "/api/v1/clusters/resources/roll-to"
	reqBody := RollToClusterResourceVersionRequest{
		ClusterID: clusterID,
		VersionID: versionID,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("roll-to failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result RollToClusterResourceVersionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

type AnkraResourceKind string

type Parent struct {
	Name string            `json:"name" yaml:"name"`
	Kind AnkraResourceKind `json:"kind" yaml:"kind"`
}

type AddonStandaloneConfiguration struct {
	ValuesBase64   string   `json:"values_base64,omitempty"`
	EncryptedPaths []string `json:"encrypted_paths,omitempty"`
}

type Addon struct {
	Name                   string                 `json:"name"`
	ChartName              string                 `json:"chart_name"`
	ChartVersion           string                 `json:"chart_version"`
	RepositoryURL          string                 `json:"repository_url,omitempty"`
	Namespace              string                 `json:"namespace,omitempty"`
	Configuration          interface{}            `json:"configuration,omitempty"`
	Parents                []Parent               `json:"parents"`
	RegistryName           string                 `json:"registry_name,omitempty"`
	RegistryURL            string                 `json:"registry_url,omitempty"`
	RegistryCredentialName string                 `json:"registry_credential_name,omitempty"`
	Settings               map[string]interface{} `json:"settings,omitempty"`
	// Group is the optional organizational grouping label within the stack,
	// the same field AddonSpec carries in the exported IaC. Omitted when
	// empty: apply is declarative, so an absent key means "ungrouped".
	Group string `json:"group,omitempty"`
	// AgentsMd is the addon's AGENTS.md content (operational learnings in
	// plain markdown). Pointer semantics matter: nil = field absent, the
	// backend preserves the stored value; pointer to "" = explicit clear.
	AgentsMd *string `json:"agents_md,omitempty"`
	// AgentsMdFromFile is the repo-relative path the AGENTS.md content was
	// authored in, stored by the backend as the pointer in the exported IaC.
	AgentsMdFromFile *string `json:"agents_md_from_file,omitempty"`
}

type Manifest struct {
	Name           string   `json:"name"`
	ManifestBase64 string   `json:"manifest_base64"`
	Namespace      string   `json:"namespace,omitempty"`
	Parents        []Parent `json:"parents"`
	EncryptedPaths []string `json:"encrypted_paths,omitempty"`
	// Group: see the Addon field of the same name.
	Group string `json:"group,omitempty"`
	// AgentsMd / AgentsMdFromFile: see the Addon fields of the same name.
	// nil = preserve stored value, pointer to "" = clear.
	AgentsMd         *string `json:"agents_md,omitempty"`
	AgentsMdFromFile *string `json:"agents_md_from_file,omitempty"`
}

type Stack struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Manifests   []Manifest `json:"manifests,omitempty"`
	Addons      []Addon    `json:"addons,omitempty"`
	// DeployWave orders stacks against each other: stacks in wave N deploy
	// only after every stack in a lower wave finished. Nil = unordered.
	DeployWave *int `json:"deploy_wave,omitempty"`
	// Variables are the stack-scoped rendering variables, the same map the
	// partial-stack PATCH carries as StackSpec.Variables. The backend replaces
	// the stored map with whatever apply sends, so omitting it clears the
	// stack's variables - which is what happened before the CLI read them
	// (ankra-yxxa).
	Variables map[string]string `json:"variables,omitempty"`
}

type GitRepository struct {
	Provider       string `json:"provider"`
	CredentialName string `json:"credential_name"`
	Branch         string `json:"branch"`
	Repository     string `json:"repository,omitempty"`
	Workspace      string `json:"workspace,omitempty"`
	RepoSlug       string `json:"repo_slug,omitempty"`
	ProjectKey     string `json:"project_key,omitempty"`
	InstanceURL    string `json:"instance_url,omitempty"`
}

type PrometheusMetrics struct {
	Endpoint       string `json:"endpoint"`
	CredentialName string `json:"credential_name,omitempty"`
	Flavor         string `json:"flavor,omitempty"`
}

type CreateResourceSpec struct {
	GitRepository     *GitRepository     `json:"git_repository,omitempty"`
	PrometheusMetrics *PrometheusMetrics `json:"prometheus_metrics,omitempty"`
	Stacks            []Stack            `json:"stacks"`
}

type CreateImportClusterRequest struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Spec        CreateResourceSpec `json:"spec"`
	// AllowRepoint acknowledges that moving the cluster to a different GitOps
	// repository or branch prunes whatever the new source does not define.
	// Omitted unless asked for, so an ordinary apply carries neither flag.
	AllowRepoint bool `json:"allow_repoint,omitempty"`
	// AllowRepointDestroyingData is the separate acknowledgement the server
	// requires when the cluster holds PersistentVolumeClaims.
	AllowRepointDestroyingData bool `json:"allow_repoint_destroying_data,omitempty"`
}

type ImportResponseErrorItem struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

type ImportResponseResourceError struct {
	Name   string                    `json:"name"`
	Kind   string                    `json:"kind"`
	Errors []ImportResponseErrorItem `json:"errors"`
}

// ImportResponse mirrors the backend apply/import result.
// GitPushDeferred/GitPushMessage are CLI-synthesized from a git-push-deferral
// 422 (see GitPushDeferral): the cluster write is committed and live, only
// the commit back to Git waits on the background sync — the platform sends
// no result payload in that case, so every other field is empty.
type ImportResponse struct {
	Name            string                        `json:"name"`
	ClusterId       string                        `json:"cluster_id"`
	ImportCommand   string                        `json:"import_command"`
	Errors          []ImportResponseResourceError `json:"errors,omitempty"`
	GitPushDeferred bool                          `json:"git_push_deferred,omitempty"`
	GitPushMessage  string                        `json:"git_push_message,omitempty"`
}

// TriggerReconcileResult mirrors the platform's reconcile response (openapi
// TriggerReconcileResult): the number of operations the manual reconcile
// planned. Zero is a success too - stored state was already in sync. The
// route works for every cluster kind; "imported" in its path is historical
// naming.
type TriggerReconcileResult struct {
	CreatedOperations int `json:"created_operations"`
}

func (c *Client) TriggerReconcile(ctx context.Context, clusterID string) (*TriggerReconcileResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/reconcile", c.BaseURL, neturl.PathEscape(clusterID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("reconcile failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result TriggerReconcileResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) ApplyCluster(ctx context.Context, clusterReq CreateImportClusterRequest, wait bool) (*ImportResponse, bool, error) {
	for i := range clusterReq.Spec.Stacks {
		if clusterReq.Spec.Stacks[i].Manifests == nil {
			clusterReq.Spec.Stacks[i].Manifests = make([]Manifest, 0)
		}
		if clusterReq.Spec.Stacks[i].Addons == nil {
			clusterReq.Spec.Stacks[i].Addons = make([]Addon, 0)
		}
	}
	payload, err := json.Marshal(clusterReq)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := c.BaseURL + "/api/v1/clusters/import"
	var importResponse ImportResponse
	submitted, err := c.doJSONWriteRequest(ctx, http.MethodPost, endpoint, payload, wait, &importResponse)
	if err != nil {
		var deferred *gitPushDeferredError
		if errors.As(err, &deferred) {
			return &ImportResponse{
				GitPushDeferred: true,
				GitPushMessage:  deferred.deferral.Message,
			}, false, nil
		}
		return nil, false, err
	}
	if submitted {
		return nil, true, nil
	}
	if len(importResponse.Errors) > 0 {
		return nil, false, fmt.Errorf("import failed: %v", importResponse.Errors)
	}
	return &importResponse, false, nil
}

type ValidateClusterRequest struct {
	Spec          CreateResourceSpec `json:"spec"`
	StrictSecrets bool               `json:"strict_secrets"`
}

type ValidationWarning struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Key      string `json:"key"`
	Message  string `json:"message"`
	Category string `json:"category"`
}

type ValidateClusterResponse struct {
	Errors   []ImportResponseResourceError `json:"errors"`
	Warnings []ValidationWarning           `json:"warnings"`
}

// ValidateCluster runs the server-side validation that the offline checks
// cannot - chart existence in connected registries, plaintext-secret
// detection, and parent references against live cluster state. A non-empty
// clusterID validates the spec against that cluster's existing resources.
func (c *Client) ValidateCluster(ctx context.Context, spec CreateResourceSpec, strictSecrets bool, clusterID string) (*ValidateClusterResponse, error) {
	for i := range spec.Stacks {
		if spec.Stacks[i].Manifests == nil {
			spec.Stacks[i].Manifests = make([]Manifest, 0)
		}
		if spec.Stacks[i].Addons == nil {
			spec.Stacks[i].Addons = make([]Addon, 0)
		}
	}

	payload, err := json.Marshal(ValidateClusterRequest{Spec: spec, StrictSecrets: strictSecrets})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.BaseURL + "/api/v1/clusters/validate"
	if clusterID != "" {
		url += "?cluster_id=" + neturl.QueryEscape(clusterID)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newUnexpectedResponseError("validation request failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result ValidateClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

type stackDraftRequest struct {
	Spec stackDraftSpec `json:"spec"`
}

type stackDraftSpec struct {
	Stacks []Stack `json:"stacks"`
}

// StackDraftResult captures the outcome of staging a single stack as a draft.
// NoChange is true when the stack already matches the cluster's desired state
// (the server reports no diff to save); Errors holds per-resource validation
// failures when the draft could not be created.
type StackDraftResult struct {
	DraftID  string
	NoChange bool
	Errors   []ImportResponseResourceError
}

// CreateStackDraft stages a single stack as a reviewable resource draft on an
// existing cluster, without deploying anything. It reuses the same backend
// path the stack builder uses.
func (c *Client) CreateStackDraft(ctx context.Context, clusterID string, stack Stack) (*StackDraftResult, error) {
	if stack.Manifests == nil {
		stack.Manifests = make([]Manifest, 0)
	}
	if stack.Addons == nil {
		stack.Addons = make([]Addon, 0)
	}

	payload, err := json.Marshal(stackDraftRequest{Spec: stackDraftSpec{Stacks: []Stack{stack}}})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/clusters/%s/stacks/draft", c.BaseURL, neturl.PathEscape(clusterID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return &StackDraftResult{NoChange: true}, nil
	case resp.StatusCode == http.StatusUnprocessableEntity:
		var detail struct {
			Detail []ImportResponseResourceError `json:"detail"`
		}
		if err := json.Unmarshal(body, &detail); err != nil {
			return nil, fmt.Errorf("draft failed: status 422, body: %s", redactedBodyForError(body, 500))
		}
		return &StackDraftResult{Errors: detail.Detail}, nil
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, newUnexpectedResponseError("draft request failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var parsed struct {
		DraftID string `json:"draft_id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &StackDraftResult{DraftID: parsed.DraftID}, nil
}

// MoveClusterRequest names the organisation a cluster moves into.
type MoveClusterRequest struct {
	DestinationOrganisationID string `json:"destination_organisation_id"`
}

// MoveClusterDetached counts the source-organisation bindings the platform
// revoked or removed while moving the cluster.
type MoveClusterDetached struct {
	AccessGrants          int64   `json:"access_grants"`
	KubeTokens            int64   `json:"kube_tokens"`
	Subscriptions         int64   `json:"subscriptions"`
	NotificationMutes     int64   `json:"notification_mutes"`
	NotificationRoutes    int64   `json:"notification_routes"`
	ReportSchedules       int64   `json:"report_schedules"`
	TrustedActions        int64   `json:"trusted_actions"`
	GroupMemberships      int64   `json:"group_memberships"`
	PromotionLinks        int64   `json:"promotion_links"`
	RoleAssignments       int64   `json:"role_assignments"`
	PlatformResourceLinks int64   `json:"platform_resource_links"`
	ContextRepositories   int64   `json:"context_repositories"`
	GitopsRepository      *string `json:"gitops_repository"`
}

// MoveClusterResult is the platform's answer to a successful move.
type MoveClusterResult struct {
	ClusterID                   string              `json:"cluster_id"`
	ClusterName                 string              `json:"cluster_name"`
	SourceOrganisationID        string              `json:"source_organisation_id"`
	DestinationOrganisationID   string              `json:"destination_organisation_id"`
	DestinationOrganisationName string              `json:"destination_organisation_name"`
	Detached                    MoveClusterDetached `json:"detached"`
	SecretsRelocated            int                 `json:"secrets_relocated"`
	Warnings                    []string            `json:"warnings"`
}

// MoveClusterRefusedError is the platform's 409: a precondition the operator
// can act on (running operations, a name clash, a cluster mesh membership).
type MoveClusterRefusedError struct {
	Code      string   `json:"code"`
	Detail    string   `json:"detail"`
	Conflicts []string `json:"conflicts"`
}

func (e *MoveClusterRefusedError) Error() string {
	if e == nil {
		return ""
	}
	return e.Detail
}

// MoveCluster moves the cluster into another organisation the caller
// administers. A 403 surfaces as *PermissionDeniedError (exit 7), a 409 as
// *MoveClusterRefusedError, a 404 as the platform's detail.
func (c *Client) MoveCluster(ctx context.Context, clusterID string, destinationOrganisationID string) (*MoveClusterResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/move", c.BaseURL, neturl.PathEscape(clusterID))
	payload, marshalError := json.Marshal(MoveClusterRequest{DestinationOrganisationID: destinationOrganisationID})
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	request, requestError := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	switch response.StatusCode {
	case http.StatusOK:
		var result MoveClusterResult
		if unmarshalError := json.Unmarshal(body, &result); unmarshalError != nil {
			return nil, fmt.Errorf("decode response: %w", unmarshalError)
		}
		return &result, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusConflict:
		var refused MoveClusterRefusedError
		if unmarshalError := json.Unmarshal(body, &refused); unmarshalError == nil && refused.Detail != "" {
			return nil, &refused
		}
	}
	if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
		return nil, denied
	}
	if detail := detailFromBody(body); detail != "" {
		return nil, newUnexpectedResponseErrorWithMessage(response.StatusCode, detail)
	}
	return nil, newUnexpectedResponseError("move failed", response.StatusCode, redactedBodyForError(body, 500))
}
