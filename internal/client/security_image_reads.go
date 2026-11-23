package client

// The Security Center's two image reads (ankra-oq7jks.9.10,
// ankra-oq7jks.9.12), the REST twins of the platform's
// find_image_fix_candidates and find_image_declarations tools:
//
//   - GET /api/v1/org/security/fix-candidates: which newer tags of an image
//     carry the fix for one vulnerability, read from what Ankra has already
//     scanned;
//   - GET /api/v1/org/security/image-declarations: where an image is
//     declared across the stacks of the clusters the caller can read, and
//     what runs it.
//
// Both answers say what they could not read in their own fields (the
// registry state and unexamined tags, the completeness reasons), so the
// types embed RawDocument: -o json|yaml prints the platform's whole answer,
// and the fields declared here are the ones the human rendering reads.

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
)

// Fix-candidate verdicts. Only carries_fix may be read as fixed:
// not_scanned is unknown, not affected and not fixed.
const (
	FixCandidateVerdictCarriesFix    = "carries_fix"
	FixCandidateVerdictStillAffected = "still_affected"
	FixCandidateVerdictNotScanned    = "not_scanned"
)

// Registry states of a fix-candidate read. unreadable and not_read mean no
// candidate is offered because nothing was listed, not because none exists.
const (
	FixCandidateRegistryRead       = "read"
	FixCandidateRegistryAbsent     = "absent"
	FixCandidateRegistryUnreadable = "unreadable"
	FixCandidateRegistryNotRead    = "not_read"
)

// SecurityImageFixCandidatesOptions names the image and the vulnerability.
// The platform needs CVEID, FindingID or both; FindingID supplies the CVE
// when CVEID is empty.
type SecurityImageFixCandidatesOptions struct {
	Image     string
	CVEID     string
	FindingID string
}

// SecurityImageFixCandidates is the platform's fix-candidate answer.
type SecurityImageFixCandidates struct {
	RawDocument    `json:"-" yaml:"-"`
	Image          string                      `json:"image" yaml:"image"`
	CVEID          string                      `json:"cve_id" yaml:"cve_id"`
	Running        FixCandidateRunning         `json:"running" yaml:"running"`
	Answer         FixCandidateAnswer          `json:"answer" yaml:"answer"`
	Candidates     []FixCandidate              `json:"candidates" yaml:"candidates"`
	Registry       FixCandidateRegistryListing `json:"registry" yaml:"registry"`
	UnexaminedTags []string                    `json:"unexamined_tags" yaml:"unexamined_tags"`
	HowToRead      string                      `json:"how_to_read" yaml:"how_to_read"`
}

// FixCandidateRunning is what the organisation's findings say the running
// image carries.
type FixCandidateRunning struct {
	Tag      string                       `json:"tag" yaml:"tag"`
	Digest   string                       `json:"digest" yaml:"digest"`
	Packages []FixCandidateRunningPackage `json:"packages" yaml:"packages"`
	Detail   string                       `json:"detail" yaml:"detail"`
}

// FixCandidateRunningPackage is one affected package of the running image.
type FixCandidateRunningPackage struct {
	FindingID        string `json:"finding_id" yaml:"finding_id"`
	Package          string `json:"package" yaml:"package"`
	PackageType      string `json:"package_type" yaml:"package_type"`
	InstalledVersion string `json:"installed_version" yaml:"installed_version"`
	FixedVersion     string `json:"fixed_version" yaml:"fixed_version"`
	Occurrences      int    `json:"occurrences" yaml:"occurrences"`
}

// FixCandidateAnswer is the answer in short: the nearest tag verified to
// carry the fix (nil when none is), the newest release examined (nil when
// none was) and the summary sentence, which says why there is none.
type FixCandidateAnswer struct {
	SmallestStepWithFix *FixCandidatePick `json:"smallest_step_with_fix" yaml:"smallest_step_with_fix"`
	NewestRelease       *FixCandidatePick `json:"newest_release" yaml:"newest_release"`
	Summary             string            `json:"summary" yaml:"summary"`
}

// FixCandidatePick is one tag the answer names, pinned as tag@sha256.
type FixCandidatePick struct {
	Tag     string `json:"tag" yaml:"tag"`
	Pin     string `json:"pin" yaml:"pin"`
	Verdict string `json:"verdict" yaml:"verdict"`
}

// FixCandidate is one newer tag examined, with its verdict and evidence.
type FixCandidate struct {
	Tag            string                 `json:"tag" yaml:"tag"`
	Line           string                 `json:"line" yaml:"line"`
	SameLine       bool                   `json:"same_line" yaml:"same_line"`
	IndexDigest    string                 `json:"index_digest" yaml:"index_digest"`
	Pin            string                 `json:"pin" yaml:"pin"`
	Verdict        string                 `json:"verdict" yaml:"verdict"`
	Evidence       []FixCandidateEvidence `json:"evidence" yaml:"evidence"`
	RegistryDetail string                 `json:"registry_detail" yaml:"registry_detail"`
}

// FixCandidateEvidence is what decided one package's verdict on a
// candidate, and where it came from.
type FixCandidateEvidence struct {
	Package          string `json:"package" yaml:"package"`
	InstalledVersion string `json:"installed_version" yaml:"installed_version"`
	FixedVersion     string `json:"fixed_version" yaml:"fixed_version"`
	Verdict          string `json:"verdict" yaml:"verdict"`
	Source           string `json:"source" yaml:"source"`
	Detail           string `json:"detail" yaml:"detail"`
}

// FixCandidateRegistryListing is how the image's tag listing went.
// ListingComplete is false when the listing stopped at its page budget, so
// a tag missing from it was not read.
type FixCandidateRegistryListing struct {
	Registry        string `json:"registry" yaml:"registry"`
	Repository      string `json:"repository" yaml:"repository"`
	State           string `json:"state" yaml:"state"`
	Detail          string `json:"detail" yaml:"detail"`
	TagsListed      int    `json:"tags_listed" yaml:"tags_listed"`
	ListingComplete bool   `json:"listing_complete" yaml:"listing_complete"`
	NewerTags       int    `json:"newer_tags" yaml:"newer_tags"`
	ManifestsRead   int    `json:"manifests_read" yaml:"manifests_read"`
}

// FindSecurityImageFixCandidates reads which newer tags of an image carry
// the fix for one vulnerability. The platform reads the registry for up to
// its own budget (40 seconds) before it answers, past the shared client's
// 30-second response-header timeout, so the call rides the slow-call client
// and is bounded by ctx.
func (c *Client) FindSecurityImageFixCandidates(ctx context.Context,
	options SecurityImageFixCandidatesOptions) (*SecurityImageFixCandidates, error) {
	query := neturl.Values{}
	query.Set("image", strings.TrimSpace(options.Image))
	if cveID := strings.TrimSpace(options.CVEID); cveID != "" {
		query.Set("cve_id", cveID)
	}
	if findingID := strings.TrimSpace(options.FindingID); findingID != "" {
		query.Set("finding_id", findingID)
	}
	var answer SecurityImageFixCandidates
	requestURL := c.securityScopedURL(securityURL(c.BaseURL, "/fix-candidates", query))
	if err := c.sendJSONSlow(ctx, http.MethodGet, requestURL, nil, &answer); err != nil {
		return nil, fmt.Errorf("fix candidates request failed: %w", err)
	}
	return &answer, nil
}

// SecurityImageDeclarationsOptions names the image and, optionally, the
// clusters to search. No ClusterIDs searches every cluster the caller can
// read.
type SecurityImageDeclarationsOptions struct {
	Image      string
	ClusterIDs []string
}

// SecurityImageDeclarations is the platform's answer to where an image is
// declared. Completeness says whether the search read everything it was
// asked about: when it did not, an empty or short list of declarations does
// not mean the image is declared nowhere else.
type SecurityImageDeclarations struct {
	RawDocument              `json:"-" yaml:"-"`
	Image                    ImageDeclarationsImage        `json:"image" yaml:"image"`
	Completeness             ImageDeclarationsCompleteness `json:"completeness" yaml:"completeness"`
	ClustersSearched         int                           `json:"clusters_searched" yaml:"clusters_searched"`
	ClustersExcluded         []ImageDeclarationsExcluded   `json:"clusters_excluded" yaml:"clusters_excluded"`
	ClustersExcludedCapped   bool                          `json:"clusters_excluded_capped" yaml:"clusters_excluded_capped"`
	Declarations             []ImageDeclaration            `json:"declarations" yaml:"declarations"`
	DeclarationsCapped       bool                          `json:"declarations_capped" yaml:"declarations_capped"`
	UnresolvedMentions       []ImageDeclarationMention     `json:"unresolved_mentions" yaml:"unresolved_mentions"`
	UnresolvedMentionsCapped bool                          `json:"unresolved_mentions_capped" yaml:"unresolved_mentions_capped"`
	RunningUndeclared        []ImageRunningWorkload        `json:"running_undeclared" yaml:"running_undeclared"`
	RunningByApplication     []ImageRunningWorkload        `json:"running_by_application" yaml:"running_by_application"`
	RunningMatchUnknown      []ImageRunningWorkload        `json:"running_match_unknown" yaml:"running_match_unknown"`
	RunningInventory         string                        `json:"running_inventory" yaml:"running_inventory"`
	NotSearched              []ImageDeclarationNotSearched `json:"not_searched" yaml:"not_searched"`
}

// ImageDeclarationsImage is the image searched for, normalised the way a
// container runtime resolves it.
type ImageDeclarationsImage struct {
	Input      string `json:"input" yaml:"input"`
	Repository string `json:"repository" yaml:"repository"`
	Tag        string `json:"tag" yaml:"tag"`
	Digest     string `json:"digest" yaml:"digest"`
}

// ImageDeclarationsCompleteness is whether the search read everything; each
// reason names what it could not, with how many.
type ImageDeclarationsCompleteness struct {
	IsComplete        bool                      `json:"is_complete" yaml:"is_complete"`
	IncompleteReasons []ImageDeclarationsReason `json:"incomplete_reasons" yaml:"incomplete_reasons"`
}

// ImageDeclarationsReason is one reason the search is incomplete.
type ImageDeclarationsReason struct {
	Code  string `json:"code" yaml:"code"`
	Count int    `json:"count" yaml:"count"`
}

// ImageDeclarationsExcluded is a readable cluster an organisation-wide
// search left out, and why (archived, draft, draft_not_recorded).
type ImageDeclarationsExcluded struct {
	ClusterID   string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName string `json:"cluster_name" yaml:"cluster_name"`
	Reason      string `json:"reason" yaml:"reason"`
}

// ImageDeclaration is one place a stack member declares the image.
type ImageDeclaration struct {
	ClusterID         string                  `json:"cluster_id" yaml:"cluster_id"`
	ClusterName       string                  `json:"cluster_name" yaml:"cluster_name"`
	StackName         string                  `json:"stack_name" yaml:"stack_name"`
	MemberKind        string                  `json:"member_kind" yaml:"member_kind"`
	MemberName        string                  `json:"member_name" yaml:"member_name"`
	ResourceKind      string                  `json:"resource_kind" yaml:"resource_kind"`
	ResourceName      string                  `json:"resource_name" yaml:"resource_name"`
	KeyPath           string                  `json:"key_path" yaml:"key_path"`
	Reference         string                  `json:"reference" yaml:"reference"`
	StoredLine        int                     `json:"stored_line" yaml:"stored_line"`
	ChangeVia         string                  `json:"change_via" yaml:"change_via"`
	GitOps            *ImageDeclarationGitOps `json:"gitops" yaml:"gitops"`
	GitOpsUnknown     string                  `json:"gitops_unknown_reason" yaml:"gitops_unknown_reason"`
	StackHasOpenDraft bool                    `json:"stack_has_open_draft" yaml:"stack_has_open_draft"`
	RunningWorkloads  []ImageRunningWorkload  `json:"running_workloads" yaml:"running_workloads"`
	RunningCapped     bool                    `json:"running_workloads_capped" yaml:"running_workloads_capped"`
}

// ImageDeclarationGitOps is where the declaration lives in the cluster's
// GitOps repository. Line is nil when the line is not known, and
// LineUnknownReason says why.
type ImageDeclarationGitOps struct {
	Repository        string `json:"repository" yaml:"repository"`
	Branch            string `json:"branch" yaml:"branch"`
	FilePath          string `json:"file_path" yaml:"file_path"`
	Line              *int   `json:"line" yaml:"line"`
	LineUnknownReason string `json:"line_unknown_reason" yaml:"line_unknown_reason"`
	FileURL           string `json:"file_url" yaml:"file_url"`
}

// ImageRunningWorkload is a workload running the image.
// MatchUnknownReasons says why its declaration is not known, when it is
// not.
type ImageRunningWorkload struct {
	ClusterName         string   `json:"cluster_name" yaml:"cluster_name"`
	Namespace           string   `json:"namespace" yaml:"namespace"`
	WorkloadKind        string   `json:"workload_kind" yaml:"workload_kind"`
	WorkloadName        string   `json:"workload_name" yaml:"workload_name"`
	ContainerName       string   `json:"container_name" yaml:"container_name"`
	Image               string   `json:"image" yaml:"image"`
	Pods                int      `json:"pods" yaml:"pods"`
	MatchUnknownReasons []string `json:"match_unknown_reasons" yaml:"match_unknown_reasons"`
}

// ImageDeclarationMention is a mention of the image the search found but
// could not resolve to a declaration.
type ImageDeclarationMention struct {
	ClusterName string `json:"cluster_name" yaml:"cluster_name"`
	StackName   string `json:"stack_name" yaml:"stack_name"`
	MemberKind  string `json:"member_kind" yaml:"member_kind"`
	MemberName  string `json:"member_name" yaml:"member_name"`
	StoredLine  int    `json:"stored_line" yaml:"stored_line"`
	KeyPath     string `json:"key_path" yaml:"key_path"`
	Reason      string `json:"reason" yaml:"reason"`
}

// ImageDeclarationNotSearched is a stack member the search could not read.
type ImageDeclarationNotSearched struct {
	ClusterName string `json:"cluster_name" yaml:"cluster_name"`
	StackName   string `json:"stack_name" yaml:"stack_name"`
	MemberKind  string `json:"member_kind" yaml:"member_kind"`
	MemberName  string `json:"member_name" yaml:"member_name"`
	Reason      string `json:"reason" yaml:"reason"`
}

// FindSecurityImageDeclarations reads where an image is declared across the
// stacks of the clusters the caller can read. A search of many clusters can
// outlast the shared client's response-header timeout, so it rides the
// slow-call client and is bounded by ctx.
func (c *Client) FindSecurityImageDeclarations(ctx context.Context,
	options SecurityImageDeclarationsOptions) (*SecurityImageDeclarations, error) {
	query := neturl.Values{}
	query.Set("image", strings.TrimSpace(options.Image))
	for _, clusterID := range options.ClusterIDs {
		if trimmed := strings.TrimSpace(clusterID); trimmed != "" {
			query.Add("cluster_id", trimmed)
		}
	}
	var answer SecurityImageDeclarations
	requestURL := c.securityScopedURL(securityURL(c.BaseURL, "/image-declarations", query))
	if err := c.sendJSONSlow(ctx, http.MethodGet, requestURL, nil, &answer); err != nil {
		return nil, fmt.Errorf("image declarations request failed: %w", err)
	}
	return &answer, nil
}
