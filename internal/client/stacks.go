package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"time"
)

type ClusterStackListItem struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Manifests   []StackManifest `json:"manifests"`
	Addons      []StackAddon    `json:"addons"`
	// Applications are the third member kind a stack can hold. A stack
	// deployed from an Ankra application is often nothing but these, so
	// omitting them rendered such a stack as empty.
	Applications []StackApplication `json:"applications"`
	// DeployWave orders stacks against each other (nil = unordered).
	DeployWave        *int   `json:"deploy_wave,omitempty"`
	State             string `json:"state"`
	DeletePermanently bool   `json:"delete_permanently"`
}

type StackManifest struct {
	Name              string   `json:"name"`
	ManifestBase64    string   `json:"manifest_base64"`
	Namespace         string   `json:"namespace"`
	Parents           []Parent `json:"parents"`
	DeletePermanently bool     `json:"delete_permanently"`
	State             string   `json:"state"`
}

type StackAddon struct {
	Name              string           `json:"name"`
	ChartName         string           `json:"chart_name"`
	ChartVersion      string           `json:"chart_version"`
	RepositoryURL     string           `json:"repository_url"`
	Namespace         string           `json:"namespace"`
	Configuration     StackAddonConfig `json:"configuration"`
	Parents           []Parent         `json:"parents"`
	State             string           `json:"state"`
	ChartIcon         *string          `json:"chart_icon"`
	DeletePermanently bool             `json:"delete_permanently"`
}

type StackAddonConfig struct {
	ValuesBase64 string `json:"values_base64"`
}

// StackApplication is a stack member backed by an Ankra application. The
// namespace and version members are nullable on the wire, so a missing one
// decodes to the empty string rather than failing the whole listing.
type StackApplication struct {
	Name                       string   `json:"name"`
	Namespace                  string   `json:"namespace"`
	PlatformApplicationID      string   `json:"platform_application_id"`
	PlatformApplicationVersion string   `json:"platform_application_version"`
	Parents                    []Parent `json:"parents"`
	State                      string   `json:"state"`
	Health                     string   `json:"health"`
	DeletePermanently          bool     `json:"delete_permanently"`
}

type ListClusterStacksResponse struct {
	Stacks     []ClusterStackListItem `json:"stacks"`
	Pagination Pagination             `json:"pagination"`
}

// StackVersionHistoryEntry mirrors the backend's VersionHistoryEntry: one
// stored version of a stack member resource.
type StackVersionHistoryEntry struct {
	VersionID    string           `json:"version_id"`
	CreatedAt    *time.Time       `json:"created_at"`
	Delta        []map[string]any `json:"delta"`
	UserID       string           `json:"user_id"`
	UserName     *string          `json:"user_name"`
	ExternalUser *string          `json:"external_user"`
	ChangeType   *string          `json:"change_type"`
}

// StackHistoryItem mirrors the backend's StackHistoryItem: the history is
// grouped per stack member (addon or manifest), newest version first.
type StackHistoryItem struct {
	ResourceName   string                     `json:"resource_name"`
	ResourceType   string                     `json:"resource_type"`
	ResourceID     string                     `json:"resource_id"`
	VersionHistory []StackVersionHistoryEntry `json:"version_history"`
}

// GetStackHistoryResponse mirrors GetClusterStackHistoryResult.
type GetStackHistoryResponse struct {
	History []StackHistoryItem `json:"history"`
}

type DeleteStackResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type RenameStackRequest struct {
	NewName string `json:"new_name"`
}

type RenameStackResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	// GitPushDeferred marks a designed git-push refusal: the rename is
	// applied and live, only the commit back to Git waits on the background
	// sync. Message then carries the platform's detail verbatim.
	GitPushDeferred bool `json:"git_push_deferred,omitempty"`
}

// ListClusterStacks pages through the full stack listing. The
// /api/v1/clusters/{id}/stacks twin always serves page 1 of 25, so this
// uses the imported-cluster route, which accepts paging (page_size max 100)
// and renders the identical stack items.
func (c *Client) ListClusterStacks(clusterID string) ([]ClusterStackListItem, error) {
	var stacks []ClusterStackListItem
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks?page=%d&page_size=100",
			c.BaseURL, neturl.PathEscape(clusterID), page)
		var response ListClusterStacksResponse
		if err := c.getJSON(url, &response); err != nil {
			return nil, fmt.Errorf("failed to list cluster stacks: %w", err)
		}
		stacks = append(stacks, response.Stacks...)
		if page >= response.Pagination.TotalPages || len(response.Stacks) == 0 {
			break
		}
	}
	return stacks, nil
}

func (c *Client) GetStackHistory(clusterID, stackName string) (*GetStackHistoryResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/history",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	var resp GetStackHistoryResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to get stack history: %w", err)
	}
	return &resp, nil
}

// GetStackAddonResourceID resolves an addon's resource id through the stack
// history endpoint — the addon listing carries no id, and the uninstall
// endpoint takes only the resource UUID. max_versions=1 keeps the response
// minimal; the resource ids arrive regardless of version depth.
func (c *Client) GetStackAddonResourceID(clusterID, stackName, addonName string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/history?resource_type=addon&max_versions=1",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	var resp GetStackHistoryResponse
	if err := c.getJSON(url, &resp); err != nil {
		return "", fmt.Errorf("failed to resolve addon resource id: %w", err)
	}
	for _, item := range resp.History {
		if item.ResourceType == "addon" && item.ResourceName == addonName {
			return item.ResourceID, nil
		}
	}
	return "", fmt.Errorf("addon %q: %w", addonName, ErrAddonNotFound)
}

func (c *Client) DeleteStack(ctx context.Context, clusterID, stackName string) (*DeleteStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
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
		return nil, newUnexpectedResponseError("delete failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &DeleteStackResult{Success: true, Message: "Stack deleted"}, nil
}

func (c *Client) RenameStack(ctx context.Context, clusterID, stackName, newName string) (*RenameStackResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/rename-stack",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	reqBody := RenameStackRequest{NewName: newName}
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
		if deferral := gitPushDeferralFromResponse(resp.StatusCode, body); deferral != nil {
			return &RenameStackResult{Success: true, Message: deferral.Message, GitPushDeferred: true}, nil
		}
		return nil, newUnexpectedResponseError("rename failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &RenameStackResult{Success: true, Message: "Stack renamed"}, nil
}

// Data clone modes, mirroring importedwrite's CloneDataModeFresh and
// CloneDataModeLatest. Fresh takes a new restore point on the source as part
// of the clone, so the copy is of the stack as it is now; latest restores the
// newest complete restore point the stack already has and moves nothing out
// of the source.
const (
	CloneDataModeFresh  = "fresh"
	CloneDataModeLatest = "latest"
)

// CloneDataSelection is decision D12's selection as a clone request carries
// it. Databases is a pointer because the platform tells an absent field from
// an explicit false: absent takes the stack's stored backup selection and,
// failing that, the default, while false is a deliberate exclusion.
//
// The route reads `databases` and `persistent_volume_claims` and nothing
// else, so the acknowledgement that a database exclusion needs is a CLI-side
// guard rather than a field: sending one the platform does not define would
// read as a promise it had been recorded.
type CloneDataSelection struct {
	Databases              *bool    `json:"databases,omitempty" yaml:"databases,omitempty"`
	PersistentVolumeClaims []string `json:"persistent_volume_claims,omitempty" yaml:"persistent_volume_claims,omitempty"`
}

// CloneDataAsset is one asset a with-data clone carries, as the clone's
// result reports it. SizeBytes is zero for a capture that has not run yet,
// which is every asset of a clone in `fresh` mode.
type CloneDataAsset struct {
	ID             string `json:"id" yaml:"id"`
	Kind           string `json:"kind" yaml:"kind"`
	Engine         string `json:"engine" yaml:"engine"`
	Namespace      string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Name           string `json:"name" yaml:"name"`
	DatabaseEngine string `json:"database_engine,omitempty" yaml:"database_engine,omitempty"`
	SizeBytes      int64  `json:"size_bytes" yaml:"size_bytes"`
}

// CloneNeedsInputItem is one cloned member that still holds stripped secrets
// and must be filled in before the target stack can come up.
type CloneNeedsInputItem struct {
	MemberKind string   `json:"member_kind" yaml:"member_kind"`
	MemberName string   `json:"member_name" yaml:"member_name"`
	Reason     string   `json:"reason" yaml:"reason"`
	Paths      []string `json:"paths" yaml:"paths"`
}

// CloneStackToClusterRequest is the clone body. The data half (bead
// ankra-0xsdd.9, cluster#3101) is absent from a configuration-only clone,
// which stays byte-identical to what the CLI sent before it existed.
//
// DataSelection is a pointer for the same reason the platform carries a
// Present flag: a body with no selection falls back to the stack's stored
// one, while an empty selection would be read as a selection covering
// nothing.
type CloneStackToClusterRequest struct {
	SourceClusterID            string              `json:"source_cluster_id"`
	StackName                  string              `json:"stack_name"`
	NewStackName               string              `json:"new_stack_name,omitempty"`
	IncludeAddonConfigurations bool                `json:"include_addon_configurations"`
	DeployAfterClone           bool                `json:"deploy_after_clone,omitempty"`
	IncludeData                bool                `json:"include_data,omitempty"`
	DataCloneMode              string              `json:"data_clone_mode,omitempty"`
	BackupVaultID              string              `json:"backup_vault_id,omitempty"`
	DataSelection              *CloneDataSelection `json:"data_selection,omitempty"`
	ProtectSource              bool                `json:"protect_source,omitempty"`
	// IdempotencyKey is sent as the optional Idempotency-Key header, never in
	// the body. A with-data clone takes a restore point and dispatches a
	// capture, so a retry that slipped past a dropped response would take a
	// second restore point of the same stack; replaying the key answers the
	// recorded response instead.
	IdempotencyKey string `json:"-"`
}

type CloneStackToClusterResult struct {
	DraftID         string   `json:"draft_id" yaml:"draft_id"`
	StackName       string   `json:"stack_name" yaml:"stack_name"`
	Warnings        []string `json:"warnings" yaml:"warnings"`
	AddonsCloned    int      `json:"addons_cloned" yaml:"addons_cloned"`
	ManifestsCloned int      `json:"manifests_cloned" yaml:"manifests_cloned"`
	// ApplicationsCloned is absent from platforms that predate application
	// cloning (cluster#1971) and decodes to 0 there, which is also what those
	// platforms cloned.
	ApplicationsCloned int `json:"applications_cloned" yaml:"applications_cloned"`
	// NeedsInput names the cloned members whose secrets were stripped, and
	// Deployed / OperationID / DeployError report the deploy a
	// deploy_after_clone request ran - or why one that was asked for did not
	// happen. The draft is kept either way.
	NeedsInput  []CloneNeedsInputItem `json:"needs_input,omitempty" yaml:"needs_input,omitempty"`
	Deployed    bool                  `json:"deployed" yaml:"deployed"`
	OperationID *string               `json:"operation_id,omitempty" yaml:"operation_id,omitempty"`
	DeployError *string               `json:"deploy_error,omitempty" yaml:"deploy_error,omitempty"`
	// DataCloneRunID is the run carrying the data half of the clone, nil for
	// a configuration-only clone. DataRestorePointID is the restore point
	// that run fills or reads, DataAssets what it was asked to carry, and
	// DataWarnings everything it will not carry - the omissions first,
	// because they are what changes the answer to "is this a copy".
	DataCloneRunID     *string          `json:"data_clone_run_id,omitempty" yaml:"data_clone_run_id,omitempty"`
	DataRestorePointID *string          `json:"data_restore_point_id,omitempty" yaml:"data_restore_point_id,omitempty"`
	DataAssets         []CloneDataAsset `json:"data_assets,omitempty" yaml:"data_assets,omitempty"`
	DataWarnings       []string         `json:"data_warnings,omitempty" yaml:"data_warnings,omitempty"`
}

func (c *Client) CloneStackToCluster(ctx context.Context, targetClusterID string, cloneRequest CloneStackToClusterRequest) (*CloneStackToClusterResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/clone",
		c.BaseURL, neturl.PathEscape(targetClusterID))

	payload, err := json.Marshal(cloneRequest)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	if cloneRequest.IdempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", cloneRequest.IdempotencyKey)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// The data half refuses with sentences the caller has to read to act
		// on - which permission is missing, why a rename cannot carry volume
		// data, which storage class the target lacks - so the detail is
		// surfaced as the message rather than buried in a raw body dump.
		if denied := PermissionDeniedFromResponse(resp.StatusCode, body); denied != nil {
			return nil, denied
		}
		if detail := detailFromBody(body); detail != "" {
			return nil, newBackendDetailError(resp.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("clone failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result CloneStackToClusterResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &result, nil
}
