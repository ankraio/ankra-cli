package client

import (
	"fmt"
	neturl "net/url"
)

// SecurityStackScope says how a stack was attributed: how many members it
// has, how many workloads they matched, and how many members matched nothing.
type SecurityStackScope struct {
	Addons           int `json:"addons" yaml:"addons"`
	Manifests        int `json:"manifests" yaml:"manifests"`
	DeclaredObjects  int `json:"declared_objects" yaml:"declared_objects"`
	MatchedWorkloads int `json:"matched_workloads" yaml:"matched_workloads"`
	UnmatchedMembers int `json:"unmatched_members" yaml:"unmatched_members"`
}

// SecurityStackFindings is the CVE posture attributed to a stack.
type SecurityStackFindings struct {
	Observed               SecuritySeverityCounts `json:"observed" yaml:"observed"`
	Actionable             SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	Acknowledged           SecuritySeverityCounts `json:"acknowledged" yaml:"acknowledged"`
	AcceptedRisk           SecuritySeverityCounts `json:"accepted_risk" yaml:"accepted_risk"`
	Findings               int                    `json:"findings" yaml:"findings"`
	FixableSevere          int                    `json:"fixable_severe" yaml:"fixable_severe"`
	KnownExploited         int                    `json:"known_exploited" yaml:"known_exploited"`
	KnownExploitedFindings int                    `json:"known_exploited_findings" yaml:"known_exploited_findings"`
	AffectedImages         int                    `json:"affected_images" yaml:"affected_images"`
	AffectedWorkloads      int                    `json:"affected_workloads" yaml:"affected_workloads"`
	LastScan               *string                `json:"last_scan" yaml:"last_scan"`
}

// SecurityStackMember is one add-on or manifest of a stack with the posture
// of the workloads it resolved to. Workloads 0 means unmatched, not clean.
type SecurityStackMember struct {
	ResourceID            string                 `json:"resource_id" yaml:"resource_id"`
	Kind                  string                 `json:"kind" yaml:"kind"`
	Name                  string                 `json:"name" yaml:"name"`
	Namespace             *string                `json:"namespace" yaml:"namespace"`
	ReleaseName           *string                `json:"release_name" yaml:"release_name"`
	ChartName             *string                `json:"chart_name" yaml:"chart_name"`
	DeclaredObjects       int                    `json:"declared_objects" yaml:"declared_objects"`
	Workloads             int                    `json:"workloads" yaml:"workloads"`
	Observed              SecuritySeverityCounts `json:"observed" yaml:"observed"`
	Actionable            SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	FixableSevere         int                    `json:"fixable_severe" yaml:"fixable_severe"`
	KnownExploited        int                    `json:"known_exploited" yaml:"known_exploited"`
	AffectedImages        int                    `json:"affected_images" yaml:"affected_images"`
	Containers            int                    `json:"containers" yaml:"containers"`
	ContainersWithSBOM    int                    `json:"containers_with_sbom" yaml:"containers_with_sbom"`
	ContainersWithoutSBOM int                    `json:"containers_without_sbom" yaml:"containers_without_sbom"`
	SBOMImages            int                    `json:"sbom_images" yaml:"sbom_images"`
	SBOMComponents        int                    `json:"sbom_components" yaml:"sbom_components"`
	LastScan              *string                `json:"last_scan" yaml:"last_scan"`
}

// SecurityStackSBOM is the bill-of-materials picture of a stack's containers.
type SecurityStackSBOM struct {
	Containers            int                  `json:"containers" yaml:"containers"`
	ContainersWithSBOM    int                  `json:"containers_with_sbom" yaml:"containers_with_sbom"`
	ContainersWithoutSBOM int                  `json:"containers_without_sbom" yaml:"containers_without_sbom"`
	Pods                  int                  `json:"pods" yaml:"pods"`
	Images                int                  `json:"images" yaml:"images"`
	Components            int                  `json:"components" yaml:"components"`
	Workloads             int                  `json:"workloads" yaml:"workloads"`
	LatestGeneratedAt     *string              `json:"latest_generated_at" yaml:"latest_generated_at"`
	Coverage              SecuritySBOMCoverage `json:"coverage" yaml:"coverage"`
}

// SecurityStackPosture is one stack's security posture: its members, the
// findings attributed to them, the exploited CVEs and the bill of materials.
type SecurityStackPosture struct {
	Status                string                         `json:"status" yaml:"status"`
	ClusterID             string                         `json:"cluster_id" yaml:"cluster_id"`
	StackName             string                         `json:"stack_name" yaml:"stack_name"`
	StackResourceID       string                         `json:"stack_resource_id" yaml:"stack_resource_id"`
	Scanner               SecurityScanner                `json:"scanner" yaml:"scanner"`
	Intelligence          SecurityIntelligenceStatus     `json:"intelligence" yaml:"intelligence"`
	Scope                 SecurityStackScope             `json:"scope" yaml:"scope"`
	Findings              SecurityStackFindings          `json:"findings" yaml:"findings"`
	KnownExploited        []SecurityRemediationCandidate `json:"known_exploited" yaml:"known_exploited"`
	TopActionableFindings []SecurityRemediationCandidate `json:"top_actionable_findings" yaml:"top_actionable_findings"`
	SBOM                  SecurityStackSBOM              `json:"sbom" yaml:"sbom"`
	Members               []SecurityStackMember          `json:"members" yaml:"members"`
}

// SecurityClusterStackSummary is one stack row of a cluster's stack list.
type SecurityClusterStackSummary struct {
	StackName             string                `json:"stack_name" yaml:"stack_name"`
	StackResourceID       string                `json:"stack_resource_id" yaml:"stack_resource_id"`
	Status                string                `json:"status" yaml:"status"`
	Scope                 SecurityStackScope    `json:"scope" yaml:"scope"`
	Findings              SecurityStackFindings `json:"findings" yaml:"findings"`
	Containers            int                   `json:"containers" yaml:"containers"`
	ContainersWithSBOM    int                   `json:"containers_with_sbom" yaml:"containers_with_sbom"`
	ContainersWithoutSBOM int                   `json:"containers_without_sbom" yaml:"containers_without_sbom"`
	Pods                  int                   `json:"pods" yaml:"pods"`
	SBOMImages            int                   `json:"sbom_images" yaml:"sbom_images"`
	SBOMComponents        int                   `json:"sbom_components" yaml:"sbom_components"`
}

// SecurityClusterStackOutside is everything on the cluster no stack owns,
// so the stack rows add up to the cluster.
type SecurityClusterStackOutside struct {
	Workloads             int                    `json:"workloads" yaml:"workloads"`
	Observed              SecuritySeverityCounts `json:"observed" yaml:"observed"`
	Actionable            SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	FixableSevere         int                    `json:"fixable_severe" yaml:"fixable_severe"`
	KnownExploited        int                    `json:"known_exploited" yaml:"known_exploited"`
	Containers            int                    `json:"containers" yaml:"containers"`
	ContainersWithSBOM    int                    `json:"containers_with_sbom" yaml:"containers_with_sbom"`
	ContainersWithoutSBOM int                    `json:"containers_without_sbom" yaml:"containers_without_sbom"`
	Pods                  int                    `json:"pods" yaml:"pods"`
	LastScan              *string                `json:"last_scan" yaml:"last_scan"`
}

// SecurityClusterStackList is a cluster's security posture broken down by
// the stacks Ankra deployed on it, riskiest first.
type SecurityClusterStackList struct {
	ClusterID    string                        `json:"cluster_id" yaml:"cluster_id"`
	Scanner      SecurityScanner               `json:"scanner" yaml:"scanner"`
	Intelligence SecurityIntelligenceStatus    `json:"intelligence" yaml:"intelligence"`
	Coverage     SecuritySBOMCoverage          `json:"coverage" yaml:"coverage"`
	Stacks       []SecurityClusterStackSummary `json:"stacks" yaml:"stacks"`
	Outside      SecurityClusterStackOutside   `json:"outside" yaml:"outside"`
}

// SecurityStackWorkload is one Kubernetes workload a stack member resolved
// to. Scanned false means no report names it: the counts are absent, not
// clean.
type SecurityStackWorkload struct {
	MemberResourceID      string                 `json:"member_resource_id" yaml:"member_resource_id"`
	MemberKind            string                 `json:"member_kind" yaml:"member_kind"`
	MemberName            string                 `json:"member_name" yaml:"member_name"`
	Kind                  string                 `json:"kind" yaml:"kind"`
	Namespace             string                 `json:"namespace" yaml:"namespace"`
	Name                  string                 `json:"name" yaml:"name"`
	UID                   string                 `json:"uid" yaml:"uid"`
	Pods                  int                    `json:"pods" yaml:"pods"`
	Scanned               bool                   `json:"scanned" yaml:"scanned"`
	Observed              SecuritySeverityCounts `json:"observed" yaml:"observed"`
	Actionable            SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	FixableSevere         int                    `json:"fixable_severe" yaml:"fixable_severe"`
	KnownExploited        int                    `json:"known_exploited" yaml:"known_exploited"`
	Containers            int                    `json:"containers" yaml:"containers"`
	ContainersWithSBOM    int                    `json:"containers_with_sbom" yaml:"containers_with_sbom"`
	ContainersWithoutSBOM int                    `json:"containers_without_sbom" yaml:"containers_without_sbom"`
	LastScan              *string                `json:"last_scan" yaml:"last_scan"`
}

// SecurityStackWorkloadList lists a stack's workloads by member.
type SecurityStackWorkloadList struct {
	ClusterID string                  `json:"cluster_id" yaml:"cluster_id"`
	StackName string                  `json:"stack_name" yaml:"stack_name"`
	Result    []SecurityStackWorkload `json:"result" yaml:"result"`
}

// SecurityPodSecurityContainer is one container of a pod with its scan
// state, findings and bill of materials.
type SecurityPodSecurityContainer struct {
	Name           string                 `json:"name" yaml:"name"`
	Kind           string                 `json:"kind" yaml:"kind"`
	Image          string                 `json:"image" yaml:"image"`
	ImageDigest    *string                `json:"image_digest" yaml:"image_digest"`
	ImageIdentity  *string                `json:"image_identity" yaml:"image_identity"`
	Ready          bool                   `json:"ready" yaml:"ready"`
	RestartCount   int                    `json:"restart_count" yaml:"restart_count"`
	Scanned        bool                   `json:"scanned" yaml:"scanned"`
	Observed       int                    `json:"observed" yaml:"observed"`
	Actionable     SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	KnownExploited int                    `json:"known_exploited" yaml:"known_exploited"`
	FixableSevere  int                    `json:"fixable_severe" yaml:"fixable_severe"`
	SBOMStatus     string                 `json:"sbom_status" yaml:"sbom_status"`
	ComponentCount *int                   `json:"component_count" yaml:"component_count"`
	OSName         *string                `json:"os_name" yaml:"os_name"`
	GeneratedAt    *string                `json:"generated_at" yaml:"generated_at"`
}

// SecurityPodSecurityFindings sums the pod's scanned containers.
type SecurityPodSecurityFindings struct {
	Observed          int                    `json:"observed" yaml:"observed"`
	Actionable        SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	KnownExploited    int                    `json:"known_exploited" yaml:"known_exploited"`
	FixableSevere     int                    `json:"fixable_severe" yaml:"fixable_severe"`
	Containers        int                    `json:"containers" yaml:"containers"`
	ScannedContainers int                    `json:"scanned_containers" yaml:"scanned_containers"`
}

// SecurityPodSecuritySBOM is the bill-of-materials picture of one pod.
type SecurityPodSecuritySBOM struct {
	Containers        int                  `json:"containers" yaml:"containers"`
	WithSBOM          int                  `json:"with_sbom" yaml:"with_sbom"`
	WithoutSBOM       int                  `json:"without_sbom" yaml:"without_sbom"`
	Images            int                  `json:"images" yaml:"images"`
	Components        int                  `json:"components" yaml:"components"`
	LatestGeneratedAt *string              `json:"latest_generated_at" yaml:"latest_generated_at"`
	Coverage          SecuritySBOMCoverage `json:"coverage" yaml:"coverage"`
}

// SecurityPodPosture is one pod's security posture, container by container.
type SecurityPodPosture struct {
	Status       string                         `json:"status" yaml:"status"`
	ClusterID    string                         `json:"cluster_id" yaml:"cluster_id"`
	Namespace    string                         `json:"namespace" yaml:"namespace"`
	PodName      string                         `json:"pod_name" yaml:"pod_name"`
	PodUID       *string                        `json:"pod_uid" yaml:"pod_uid"`
	Node         *string                        `json:"node" yaml:"node"`
	Phase        *string                        `json:"phase" yaml:"phase"`
	CreatedAt    *string                        `json:"created_at" yaml:"created_at"`
	OwnerKind    *string                        `json:"owner_kind" yaml:"owner_kind"`
	OwnerName    *string                        `json:"owner_name" yaml:"owner_name"`
	WorkloadKind *string                        `json:"workload_kind" yaml:"workload_kind"`
	WorkloadName *string                        `json:"workload_name" yaml:"workload_name"`
	Scanner      SecurityScanner                `json:"scanner" yaml:"scanner"`
	Intelligence SecurityIntelligenceStatus     `json:"intelligence" yaml:"intelligence"`
	Findings     SecurityPodSecurityFindings    `json:"findings" yaml:"findings"`
	SBOM         SecurityPodSecuritySBOM        `json:"sbom" yaml:"sbom"`
	Containers   []SecurityPodSecurityContainer `json:"containers" yaml:"containers"`
}

func importedClusterSecurityURL(base string, clusterID string, path string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/imported/%s%s", base, neturl.PathEscape(clusterID), path)
}

// ListClusterSecurityStacks reads a cluster's security posture broken down
// by stack, with the outside-any-stack remainder.
func (c *Client) ListClusterSecurityStacks(clusterID string) (*SecurityClusterStackList, error) {
	var list SecurityClusterStackList
	if err := c.getJSON(importedClusterSecurityURL(c.BaseURL, clusterID, "/security/stacks"), &list); err != nil {
		return nil, fmt.Errorf("cluster security stacks request failed: %w", err)
	}
	if list.Stacks == nil {
		list.Stacks = []SecurityClusterStackSummary{}
	}
	return &list, nil
}

// GetStackSecurity reads one stack's security posture.
func (c *Client) GetStackSecurity(clusterID string, stackName string) (*SecurityStackPosture, error) {
	var posture SecurityStackPosture
	path := "/stacks/" + neturl.PathEscape(stackName) + "/security"
	if err := c.getJSON(importedClusterSecurityURL(c.BaseURL, clusterID, path), &posture); err != nil {
		return nil, fmt.Errorf("stack security request failed: %w", err)
	}
	if posture.Members == nil {
		posture.Members = []SecurityStackMember{}
	}
	return &posture, nil
}

// ListStackSecurityWorkloads lists the workloads each member of a stack
// resolved to.
func (c *Client) ListStackSecurityWorkloads(clusterID string, stackName string) (*SecurityStackWorkloadList, error) {
	var list SecurityStackWorkloadList
	path := "/stacks/" + neturl.PathEscape(stackName) + "/security/workloads"
	if err := c.getJSON(importedClusterSecurityURL(c.BaseURL, clusterID, path), &list); err != nil {
		return nil, fmt.Errorf("stack security workloads request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecurityStackWorkload{}
	}
	return &list, nil
}

// GetPodSecurity reads one pod's security posture, container by container.
func (c *Client) GetPodSecurity(clusterID string, namespace string, podName string) (*SecurityPodPosture, error) {
	var posture SecurityPodPosture
	path := "/security/pods/" + neturl.PathEscape(namespace) + "/" + neturl.PathEscape(podName)
	if err := c.getJSON(importedClusterSecurityURL(c.BaseURL, clusterID, path), &posture); err != nil {
		return nil, fmt.Errorf("pod security request failed: %w", err)
	}
	if posture.Containers == nil {
		posture.Containers = []SecurityPodSecurityContainer{}
	}
	return &posture, nil
}
