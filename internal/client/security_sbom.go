package client

import (
	"fmt"
	neturl "net/url"
	"strconv"
	"strings"
)

// SecurityNamespace is one namespace on one cluster in the Security Center's
// breakdown: its scanned workloads and images, the pods the cluster runs
// there now, and the actionable findings by severity. ReportScope "cluster"
// with an empty Namespace carries the cluster-scoped reports (node and
// control-plane images) so the rows still add up to the cluster.
type SecurityNamespace struct {
	ClusterID       string                 `json:"cluster_id" yaml:"cluster_id"`
	ClusterName     string                 `json:"cluster_name" yaml:"cluster_name"`
	Namespace       string                 `json:"namespace" yaml:"namespace"`
	ReportScope     string                 `json:"report_scope" yaml:"report_scope"`
	Workloads       int                    `json:"workloads" yaml:"workloads"`
	Images          int                    `json:"images" yaml:"images"`
	Pods            int                    `json:"pods" yaml:"pods"`
	Observed        int                    `json:"observed" yaml:"observed"`
	Actionable      SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	ActionableTotal int                    `json:"actionable_total" yaml:"actionable_total"`
	AcceptedRisk    int                    `json:"accepted_risk" yaml:"accepted_risk"`
	FixableSevere   int                    `json:"fixable_severe" yaml:"fixable_severe"`
	KnownExploited  int                    `json:"known_exploited" yaml:"known_exploited"`
	Priority        string                 `json:"priority" yaml:"priority"`
	LastScan        string                 `json:"last_scan" yaml:"last_scan"`
	ScannerStatus   string                 `json:"scanner_status" yaml:"scanner_status"`
}

// SecurityNamespaceList is the paginated namespace breakdown.
type SecurityNamespaceList struct {
	Result     []SecurityNamespace `json:"result" yaml:"result"`
	Pagination SecurityPagination  `json:"pagination" yaml:"pagination"`
	Scanner    SecurityScanner     `json:"scanner" yaml:"scanner"`
}

// SecurityPodContainer is one container of a pod with the findings of the
// scanned workload container behind it. Scanned false means no report
// covers the container, which is not the same as a clean one.
type SecurityPodContainer struct {
	Name           string                 `json:"name" yaml:"name"`
	Image          string                 `json:"image" yaml:"image"`
	ImageDigest    *string                `json:"image_digest" yaml:"image_digest"`
	Ready          bool                   `json:"ready" yaml:"ready"`
	RestartCount   int                    `json:"restart_count" yaml:"restart_count"`
	Scanned        bool                   `json:"scanned" yaml:"scanned"`
	Observed       int                    `json:"observed" yaml:"observed"`
	Actionable     SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	KnownExploited int                    `json:"known_exploited" yaml:"known_exploited"`
	FixableSevere  int                    `json:"fixable_severe" yaml:"fixable_severe"`
}

// SecurityPod is one running pod resolved to the scanned workload that owns
// it, with the findings summed over its containers.
type SecurityPod struct {
	Name           string                 `json:"name" yaml:"name"`
	UID            *string                `json:"uid" yaml:"uid"`
	Namespace      string                 `json:"namespace" yaml:"namespace"`
	Node           *string                `json:"node" yaml:"node"`
	Phase          *string                `json:"phase" yaml:"phase"`
	OwnerKind      *string                `json:"owner_kind" yaml:"owner_kind"`
	OwnerName      *string                `json:"owner_name" yaml:"owner_name"`
	WorkloadKind   *string                `json:"workload_kind" yaml:"workload_kind"`
	WorkloadName   *string                `json:"workload_name" yaml:"workload_name"`
	CreatedAt      *string                `json:"created_at" yaml:"created_at"`
	Containers     []SecurityPodContainer `json:"containers" yaml:"containers"`
	Scanned        bool                   `json:"scanned" yaml:"scanned"`
	Observed       int                    `json:"observed" yaml:"observed"`
	Actionable     SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	KnownExploited int                    `json:"known_exploited" yaml:"known_exploited"`
	FixableSevere  int                    `json:"fixable_severe" yaml:"fixable_severe"`
	Priority       string                 `json:"priority" yaml:"priority"`
}

// SecurityPodList is the paginated pod breakdown of one namespace; Capped
// says the namespace held more pods than one read loads.
type SecurityPodList struct {
	Result     []SecurityPod      `json:"result" yaml:"result"`
	Pagination SecurityPagination `json:"pagination" yaml:"pagination"`
	Capped     bool               `json:"capped" yaml:"capped"`
}

// SecuritySBOMComponent is one package name, version and ecosystem across
// every image that carries it, with where it runs and the findings that name
// that exact package and version on those images.
type SecuritySBOMComponent struct {
	Name               string   `json:"name" yaml:"name"`
	Version            string   `json:"version" yaml:"version"`
	PackageType        string   `json:"package_type" yaml:"package_type"`
	ComponentType      string   `json:"component_type" yaml:"component_type"`
	PURL               *string  `json:"purl" yaml:"purl"`
	Licenses           []string `json:"licenses" yaml:"licenses"`
	Images             int      `json:"images" yaml:"images"`
	Workloads          int      `json:"workloads" yaml:"workloads"`
	Clusters           int      `json:"clusters" yaml:"clusters"`
	VulnerableFindings int      `json:"vulnerable_findings" yaml:"vulnerable_findings"`
	ActionableFindings int      `json:"actionable_findings" yaml:"actionable_findings"`
	KnownExploited     int      `json:"known_exploited" yaml:"known_exploited"`
}

// SecuritySBOMCoverage says how much of the scanned fleet publishes a bill
// of materials. ClustersWithSBOM below ScannedClusters means the rest have
// SBOM generation switched off, not that they run nothing.
type SecuritySBOMCoverage struct {
	ScannedClusters   int     `json:"scanned_clusters" yaml:"scanned_clusters"`
	ClustersWithSBOM  int     `json:"clusters_with_sbom" yaml:"clusters_with_sbom"`
	Images            int     `json:"images" yaml:"images"`
	Components        int     `json:"components" yaml:"components"`
	Workloads         int     `json:"workloads" yaml:"workloads"`
	LatestGeneratedAt *string `json:"latest_generated_at" yaml:"latest_generated_at"`
}

// SecuritySBOMComponentFacets mirrors the component filters.
type SecuritySBOMComponentFacets struct {
	PackageType []SecurityFacetCount `json:"package_type" yaml:"package_type"`
}

// SecuritySBOMComponentList is the paginated component inventory.
type SecuritySBOMComponentList struct {
	Result     []SecuritySBOMComponent     `json:"result" yaml:"result"`
	Pagination SecurityPagination          `json:"pagination" yaml:"pagination"`
	Facets     SecuritySBOMComponentFacets `json:"facets" yaml:"facets"`
	Coverage   SecuritySBOMCoverage        `json:"coverage" yaml:"coverage"`
}

// SecuritySBOMImage is one image with a bill of materials, where it runs and
// the actionable findings the scanner attributes to it.
type SecuritySBOMImage struct {
	ImageIdentity   string                 `json:"image_identity" yaml:"image_identity"`
	ImageRef        string                 `json:"image_ref" yaml:"image_ref"`
	ImageRepository *string                `json:"image_repository" yaml:"image_repository"`
	ImageTag        *string                `json:"image_tag" yaml:"image_tag"`
	ImageDigest     *string                `json:"image_digest" yaml:"image_digest"`
	Registry        *string                `json:"registry" yaml:"registry"`
	OSFamily        *string                `json:"os_family" yaml:"os_family"`
	OSName          *string                `json:"os_name" yaml:"os_name"`
	BomFormat       *string                `json:"bom_format" yaml:"bom_format"`
	SpecVersion     *string                `json:"spec_version" yaml:"spec_version"`
	ComponentCount  int                    `json:"component_count" yaml:"component_count"`
	DependencyCount int                    `json:"dependency_count" yaml:"dependency_count"`
	Workloads       int                    `json:"workloads" yaml:"workloads"`
	Clusters        int                    `json:"clusters" yaml:"clusters"`
	Namespaces      []string               `json:"namespaces" yaml:"namespaces"`
	Observed        int                    `json:"observed" yaml:"observed"`
	Actionable      SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	KnownExploited  int                    `json:"known_exploited" yaml:"known_exploited"`
	GeneratedAt     *string                `json:"generated_at" yaml:"generated_at"`
	FirstSeenAt     string                 `json:"first_seen_at" yaml:"first_seen_at"`
	LastSeenAt      string                 `json:"last_seen_at" yaml:"last_seen_at"`
}

// SecuritySBOMImageList is the paginated image inventory.
type SecuritySBOMImageList struct {
	Result     []SecuritySBOMImage  `json:"result" yaml:"result"`
	Pagination SecurityPagination   `json:"pagination" yaml:"pagination"`
	Coverage   SecuritySBOMCoverage `json:"coverage" yaml:"coverage"`
}

// SecuritySBOMWorkload is one workload container currently running an image.
type SecuritySBOMWorkload struct {
	ClusterID         string  `json:"cluster_id" yaml:"cluster_id"`
	ClusterName       string  `json:"cluster_name" yaml:"cluster_name"`
	ReportScope       string  `json:"report_scope" yaml:"report_scope"`
	WorkloadKind      *string `json:"workload_kind" yaml:"workload_kind"`
	WorkloadNamespace *string `json:"workload_namespace" yaml:"workload_namespace"`
	WorkloadName      *string `json:"workload_name" yaml:"workload_name"`
	ContainerName     *string `json:"container_name" yaml:"container_name"`
	LastSeenAt        string  `json:"last_seen_at" yaml:"last_seen_at"`
}

// SecuritySBOMImageDetail is one image, a page of its components and every
// workload container running it.
type SecuritySBOMImageDetail struct {
	Image      SecuritySBOMImage           `json:"image" yaml:"image"`
	Components []SecuritySBOMComponent     `json:"components" yaml:"components"`
	Pagination SecurityPagination          `json:"pagination" yaml:"pagination"`
	Facets     SecuritySBOMComponentFacets `json:"facets" yaml:"facets"`
	Workloads  []SecuritySBOMWorkload      `json:"workloads" yaml:"workloads"`
}

// SecurityNamespacesOptions mirrors the namespace breakdown controls.
type SecurityNamespacesOptions struct {
	Page      int
	PageSize  int
	Search    string
	ClusterID string
	Sort      string
	Order     string
}

// SecurityPodsOptions selects the pods of one namespace on one cluster,
// optionally only those a scanned workload owns.
type SecurityPodsOptions struct {
	ClusterID    string
	Namespace    string
	WorkloadUID  string
	WorkloadKind string
	WorkloadName string
	Page         int
	PageSize     int
}

// SecuritySBOMComponentsOptions mirrors the component inventory filters.
// Vulnerable is tri-state: nil keeps every component.
type SecuritySBOMComponentsOptions struct {
	Page          int
	PageSize      int
	Search        string
	PackageTypes  []string
	ClusterID     string
	Namespace     string
	ImageIdentity string
	Vulnerable    *bool
	Sort          string
	Order         string
}

// SecuritySBOMImagesOptions mirrors the image inventory filters.
type SecuritySBOMImagesOptions struct {
	Page      int
	PageSize  int
	Search    string
	ClusterID string
	Namespace string
	Sort      string
	Order     string
}

// SecuritySBOMImageOptions pages the components of one image.
type SecuritySBOMImageOptions struct {
	ImageIdentity string
	Page          int
	PageSize      int
	Search        string
	PackageTypes  []string
	Sort          string
	Order         string
}

func setListControls(query neturl.Values, search string, sort string, order string) {
	if trimmed := strings.TrimSpace(search); trimmed != "" {
		query.Set("search", trimmed)
	}
	if sort != "" {
		query.Set("sort", sort)
	}
	if order != "" {
		query.Set("order", order)
	}
}

// ListSecurityNamespaces pages through the fleet's per-namespace posture.
func (c *Client) ListSecurityNamespaces(options SecurityNamespacesOptions) (*SecurityNamespaceList, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	setListControls(query, options.Search, options.Sort, options.Order)
	if options.ClusterID != "" {
		query.Set("cluster_id", options.ClusterID)
	}
	var list SecurityNamespaceList
	if err := c.getJSON(securityURL(c.BaseURL, "/namespaces", query), &list); err != nil {
		return nil, fmt.Errorf("security namespaces request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecurityNamespace{}
	}
	return &list, nil
}

// ListSecurityPods lists the pods of one namespace joined to their scanned
// workload's findings.
func (c *Client) ListSecurityPods(options SecurityPodsOptions) (*SecurityPodList, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	query.Set("cluster_id", options.ClusterID)
	query.Set("namespace", options.Namespace)
	if options.WorkloadUID != "" {
		query.Set("workload_uid", options.WorkloadUID)
	}
	if options.WorkloadKind != "" {
		query.Set("workload_kind", options.WorkloadKind)
	}
	if options.WorkloadName != "" {
		query.Set("workload_name", options.WorkloadName)
	}
	var list SecurityPodList
	if err := c.getJSON(securityURL(c.BaseURL, "/pods", query), &list); err != nil {
		return nil, fmt.Errorf("security pods request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecurityPod{}
	}
	return &list, nil
}

// ListSecuritySBOMComponents pages through the fleet's package inventory.
func (c *Client) ListSecuritySBOMComponents(options SecuritySBOMComponentsOptions) (*SecuritySBOMComponentList, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	setListControls(query, options.Search, options.Sort, options.Order)
	for _, packageType := range options.PackageTypes {
		if trimmed := strings.TrimSpace(packageType); trimmed != "" {
			query.Add("package_type", trimmed)
		}
	}
	if options.ClusterID != "" {
		query.Set("cluster_id", options.ClusterID)
	}
	if options.Namespace != "" {
		query.Set("namespace", options.Namespace)
	}
	if options.ImageIdentity != "" {
		query.Set("image", options.ImageIdentity)
	}
	if options.Vulnerable != nil {
		query.Set("vulnerable", strconv.FormatBool(*options.Vulnerable))
	}
	var list SecuritySBOMComponentList
	if err := c.getJSON(securityURL(c.BaseURL, "/sbom", query), &list); err != nil {
		return nil, fmt.Errorf("security sbom request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecuritySBOMComponent{}
	}
	return &list, nil
}

// ListSecuritySBOMImages pages through the images with a bill of materials.
func (c *Client) ListSecuritySBOMImages(options SecuritySBOMImagesOptions) (*SecuritySBOMImageList, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	setListControls(query, options.Search, options.Sort, options.Order)
	if options.ClusterID != "" {
		query.Set("cluster_id", options.ClusterID)
	}
	if options.Namespace != "" {
		query.Set("namespace", options.Namespace)
	}
	var list SecuritySBOMImageList
	if err := c.getJSON(securityURL(c.BaseURL, "/sbom/images", query), &list); err != nil {
		return nil, fmt.Errorf("security sbom images request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecuritySBOMImage{}
	}
	return &list, nil
}

// GetSecuritySBOMImage reads one image's bill of materials.
func (c *Client) GetSecuritySBOMImage(options SecuritySBOMImageOptions) (*SecuritySBOMImageDetail, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	setListControls(query, options.Search, options.Sort, options.Order)
	query.Set("image", options.ImageIdentity)
	for _, packageType := range options.PackageTypes {
		if trimmed := strings.TrimSpace(packageType); trimmed != "" {
			query.Add("package_type", trimmed)
		}
	}
	var detail SecuritySBOMImageDetail
	if err := c.getJSON(securityURL(c.BaseURL, "/sbom/image", query), &detail); err != nil {
		return nil, fmt.Errorf("security sbom image request failed: %w", err)
	}
	if detail.Components == nil {
		detail.Components = []SecuritySBOMComponent{}
	}
	if detail.Workloads == nil {
		detail.Workloads = []SecuritySBOMWorkload{}
	}
	return &detail, nil
}
