package client

// The stack data inventory (epic ankra-0xsdd, bead ankra-0xsdd.14): what a
// stack holds that a backup would have to carry. The platform resolves it
// from the synced inventory plus a live read of the database operators, so it
// is the answer to "what would a restore point of this stack contain" before
// one exists.

import (
	"fmt"
	"net/http"
	neturl "net/url"
)

// CustomResourceScanComplete and CustomResourceScanUnavailable are the two
// states of the live database-operator read. Unavailable means no relay or an
// offline agent, not "this stack runs no databases": the PVC-label fallbacks
// still classify what they can, so a partial answer is reported as partial
// rather than presented as the whole truth.
const (
	CustomResourceScanComplete    = "complete"
	CustomResourceScanUnavailable = "unavailable"
)

// StackDataAssetOwner is the workload or custom resource an asset hangs off,
// when one could be attributed.
type StackDataAssetOwner struct {
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
}

// StackDataAssetMember is the stack member - addon or manifest - an asset was
// attributed to, when one could be.
type StackDataAssetMember struct {
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
}

// StackDataAsset is one datum-bearing unit of the stack: a standalone
// persistent volume claim, or a database custom resource with its claims
// folded in.
type StackDataAsset struct {
	Kind                       string                `json:"kind" yaml:"kind"`
	Namespace                  string                `json:"namespace" yaml:"namespace"`
	Name                       string                `json:"name" yaml:"name"`
	Engine                     string                `json:"engine" yaml:"engine"`
	Consistency                string                `json:"consistency" yaml:"consistency"`
	RequestedBytes             int64                 `json:"requested_bytes" yaml:"requested_bytes"`
	StorageClasses             []string              `json:"storage_classes,omitempty" yaml:"storage_classes,omitempty"`
	PersistentVolumeClaimNames []string              `json:"persistent_volume_claim_names,omitempty" yaml:"persistent_volume_claim_names,omitempty"`
	Owner                      *StackDataAssetOwner  `json:"owner,omitempty" yaml:"owner,omitempty"`
	Member                     *StackDataAssetMember `json:"member,omitempty" yaml:"member,omitempty"`
	HelmRelease                string                `json:"helm_release,omitempty" yaml:"helm_release,omitempty"`
	Status                     string                `json:"status,omitempty" yaml:"status,omitempty"`
}

// StackDataInventory is the full answer for one stack.
type StackDataInventory struct {
	StackName           string           `json:"stack_name" yaml:"stack_name"`
	Namespaces          []string         `json:"namespaces" yaml:"namespaces"`
	Assets              []StackDataAsset `json:"assets" yaml:"assets"`
	TotalRequestedBytes int64            `json:"total_requested_bytes" yaml:"total_requested_bytes"`
	CustomResourceScan  string           `json:"custom_resource_scan" yaml:"custom_resource_scan"`
}

// GetStackDataAssets returns a stack's data inventory.
// GET /api/v1/org/clusters/imported/{cluster_id}/stacks/{stack_name}/data-assets
func (c *Client) GetStackDataAssets(clusterID string, stackName string) (*StackDataInventory, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/data-assets",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	var result StackDataInventory
	if requestError := c.sendJSON(http.MethodGet, url, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}
