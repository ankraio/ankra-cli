package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
)

// ApplicationImageVersionComponent is one image the application publishes
// and whether its registry could be listed (listed, empty, unavailable).
type ApplicationImageVersionComponent struct {
	Name           string  `json:"name" yaml:"name"`
	Registry       string  `json:"registry" yaml:"registry"`
	Repository     string  `json:"repository" yaml:"repository"`
	RegistryStatus string  `json:"registry_status" yaml:"registry_status"`
	Message        *string `json:"message" yaml:"message"`
}

// ApplicationImageVersionSBOM is what the platform stores for one tag's
// bill of materials. Status absent means no BOM is stored; the counts are
// nil until one is.
type ApplicationImageVersionSBOM struct {
	Status          string                   `json:"status" yaml:"status"`
	ImageIdentity   *string                  `json:"image_identity" yaml:"image_identity"`
	ComponentCount  *int                     `json:"component_count" yaml:"component_count"`
	OSName          *string                  `json:"os_name" yaml:"os_name"`
	GeneratedAt     *string                  `json:"generated_at" yaml:"generated_at"`
	LicenseExposure *SecurityLicenseExposure `json:"license_exposure" yaml:"license_exposure"`
}

// ApplicationImageVersionFindings is one tag's vulnerability posture.
// Scanned false means no report names the image, which is not clean.
type ApplicationImageVersionFindings struct {
	Scanned        bool                   `json:"scanned" yaml:"scanned"`
	Observed       int                    `json:"observed" yaml:"observed"`
	Actionable     SecuritySeverityCounts `json:"actionable" yaml:"actionable"`
	KnownExploited int                    `json:"known_exploited" yaml:"known_exploited"`
	FixableSevere  int                    `json:"fixable_severe" yaml:"fixable_severe"`
	LastScanAt     *string                `json:"last_scan_at" yaml:"last_scan_at"`
}

// ApplicationImageVersionRunning says where one tag runs right now.
type ApplicationImageVersionRunning struct {
	Workloads  int      `json:"workloads" yaml:"workloads"`
	Clusters   int      `json:"clusters" yaml:"clusters"`
	Namespaces []string `json:"namespaces" yaml:"namespaces"`
	LastSeenAt *string  `json:"last_seen_at" yaml:"last_seen_at"`
}

// ApplicationImageVersion is one published tag with its BOM, findings and
// where it runs. Sources says which of registry, sbom and findings knew it.
type ApplicationImageVersion struct {
	Component  string                          `json:"component" yaml:"component"`
	Registry   string                          `json:"registry" yaml:"registry"`
	Repository string                          `json:"repository" yaml:"repository"`
	Tag        string                          `json:"tag" yaml:"tag"`
	Digest     *string                         `json:"digest" yaml:"digest"`
	PushedAt   *string                         `json:"pushed_at" yaml:"pushed_at"`
	Sources    []string                        `json:"sources" yaml:"sources"`
	SBOM       ApplicationImageVersionSBOM     `json:"sbom" yaml:"sbom"`
	Findings   ApplicationImageVersionFindings `json:"findings" yaml:"findings"`
	Running    ApplicationImageVersionRunning  `json:"running" yaml:"running"`
}

// ApplicationImageVersionsSummary totals the version list.
type ApplicationImageVersionsSummary struct {
	Versions     int `json:"versions" yaml:"versions"`
	WithSBOM     int `json:"with_sbom" yaml:"with_sbom"`
	WithFindings int `json:"with_findings" yaml:"with_findings"`
	Running      int `json:"running" yaml:"running"`
}

// ApplicationRepository is the source repository and its visibility
// (public, private or unknown when not yet synced).
type ApplicationRepository struct {
	Provider   string `json:"provider" yaml:"provider"`
	Owner      string `json:"owner" yaml:"owner"`
	Name       string `json:"name" yaml:"name"`
	Visibility string `json:"visibility" yaml:"visibility"`
}

// ApplicationLicenseComponent is one component whose licence obliges
// something of the repository.
type ApplicationLicenseComponent struct {
	Component     string   `json:"component" yaml:"component"`
	Tag           string   `json:"tag" yaml:"tag"`
	ImageIdentity string   `json:"image_identity" yaml:"image_identity"`
	Name          string   `json:"name" yaml:"name"`
	Version       string   `json:"version" yaml:"version"`
	PackageType   string   `json:"package_type" yaml:"package_type"`
	Licenses      []string `json:"licenses" yaml:"licenses"`
	LicenseRisk   string   `json:"license_risk" yaml:"license_risk"`
}

// ApplicationLicenseExposure is the source-disclosure verdict over the
// newest bill of materials of every component. Latest is nil until a BOM
// was assessed.
type ApplicationLicenseExposure struct {
	Status                   string                        `json:"status" yaml:"status"`
	Latest                   *SecurityLicenseExposure      `json:"latest" yaml:"latest"`
	SourceDisclosureRequired bool                          `json:"source_disclosure_required" yaml:"source_disclosure_required"`
	ServiceLicenceRequired   bool                          `json:"service_licence_required" yaml:"service_licence_required"`
	FlaggedComponents        int                           `json:"flagged_components" yaml:"flagged_components"`
	Components               []ApplicationLicenseComponent `json:"components" yaml:"components"`
}

// ApplicationSecurityVersions is the application's published image
// versions joined to their bills of materials and findings.
type ApplicationSecurityVersions struct {
	ApplicationID   string                             `json:"application_id" yaml:"application_id"`
	Repository      ApplicationRepository              `json:"repository" yaml:"repository"`
	Components      []ApplicationImageVersionComponent `json:"components" yaml:"components"`
	Versions        []ApplicationImageVersion          `json:"versions" yaml:"versions"`
	Summary         ApplicationImageVersionsSummary    `json:"summary" yaml:"summary"`
	LicenseExposure ApplicationLicenseExposure         `json:"license_exposure" yaml:"license_exposure"`
}

// GetApplicationSecurityVersions reads one application's image versions
// with their BOMs and findings; component narrows a monorepo to one image.
func (client *Client) GetApplicationSecurityVersions(requestContext context.Context, applicationID string, component string) (*ApplicationSecurityVersions, error) {
	query := neturl.Values{}
	if trimmed := strings.TrimSpace(component); trimmed != "" {
		query.Set("component", trimmed)
	}
	payload, requestError := client.applicationResourceRequest(requestContext, http.MethodGet, applicationPath(applicationID, "/security/versions"), query, nil)
	if requestError != nil {
		return nil, fmt.Errorf("application security versions request failed: %w", requestError)
	}
	var versions ApplicationSecurityVersions
	if parseError := json.Unmarshal(payload, &versions); parseError != nil {
		return nil, fmt.Errorf("parse application security versions: %w", parseError)
	}
	if versions.Versions == nil {
		versions.Versions = []ApplicationImageVersion{}
	}
	if versions.Components == nil {
		versions.Components = []ApplicationImageVersionComponent{}
	}
	return &versions, nil
}
