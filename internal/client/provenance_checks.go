package client

// The kept provenance checks of an application, by environment, and "Check
// now" of one environment: GET .../provenance/checks and the environment form
// of POST .../provenance/verify. A check is kept for each installation the
// platform checks, at its release or when a person asks; the read answers the
// newest one of each installation the caller reaches.

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// Read states of the kept checks read, as ApplicationProvenanceChecks.ReadState
// says them.
const (
	// ProvenanceChecksReadOK: every environment the caller reaches is listed.
	ProvenanceChecksReadOK = "ok"
	// ProvenanceChecksReadUnknown: the environments or the kept checks could
	// not be read and Environments is empty. It never means nothing was
	// checked.
	ProvenanceChecksReadUnknown = "unknown"
)

// ProvenanceCheckActor is who asked for a kept check: Kind is ankra (the
// check Ankra made at a release), member, service_account, former_member or
// not_recorded. Name is a member's or service account's name, nil for every
// other kind.
type ProvenanceCheckActor struct {
	Kind string  `json:"kind" yaml:"kind"`
	Name *string `json:"name" yaml:"name"`
}

// ProvenanceKeptImage is the verdict a kept check reached for one image.
// State is verified, refused or unknown; unknown means the image could not be
// checked, never that it was refused.
type ProvenanceKeptImage struct {
	Image         string  `json:"image" yaml:"image"`
	Digest        *string `json:"digest" yaml:"digest"`
	State         string  `json:"state" yaml:"state"`
	Reason        string  `json:"reason" yaml:"reason"`
	Message       string  `json:"message" yaml:"message"`
	KeyID         *string `json:"key_id" yaml:"key_id"`
	PipelineRunID *string `json:"pipeline_run_id" yaml:"pipeline_run_id"`
	CommitSHA     *string `json:"commit_sha" yaml:"commit_sha"`
}

// ProvenanceKeptCheck is the newest check kept for one installation. Source is
// release (Ankra's check when it was deployed) or check_now (a person asked).
// State is verified, refused (the check ran and the images did not pass) or
// unknown (the check could not reach a verdict, or found no image of the
// application's registry to check); Message says why when it is not
// verified. IsCurrent says whether ImageTag is still the tag the installation
// stores for its next release, nil when either is not known.
type ProvenanceKeptCheck struct {
	CheckedAt       string                `json:"checked_at" yaml:"checked_at"`
	Source          string                `json:"source" yaml:"source"`
	By              *ProvenanceCheckActor `json:"by" yaml:"by"`
	ImageTag        *string               `json:"image_tag" yaml:"image_tag"`
	Mode            string                `json:"mode" yaml:"mode"`
	Decision        string                `json:"decision" yaml:"decision"`
	State           string                `json:"state" yaml:"state"`
	Message         *string               `json:"message" yaml:"message"`
	Images          []ProvenanceKeptImage `json:"images" yaml:"images"`
	UncheckedImages []string              `json:"unchecked_images" yaml:"unchecked_images"`
	ImagesCapped    bool                  `json:"images_capped" yaml:"images_capped"`
	IsCurrent       *bool                 `json:"is_current" yaml:"is_current"`
}

// ProvenanceCheckPlacement is one installation of an environment and its
// newest kept check. LastCheck is nil when no check of it was kept since the
// read's KeptFrom.
type ProvenanceCheckPlacement struct {
	InstallationID string               `json:"installation_id" yaml:"installation_id"`
	ClusterID      string               `json:"cluster_id" yaml:"cluster_id"`
	Namespace      *string              `json:"namespace" yaml:"namespace"`
	LastCheck      *ProvenanceKeptCheck `json:"last_check" yaml:"last_check"`
}

// ProvenanceCheckEnvironment is one environment the caller reaches, with the
// id the environments read gives it. Placements are its installations, at
// most 20 (PlacementsCapped says more were left out); PlacementsNotCheckable
// counts its placements with no installation record, which no check covers.
type ProvenanceCheckEnvironment struct {
	EnvironmentID          string                     `json:"environment_id" yaml:"environment_id"`
	Placements             []ProvenanceCheckPlacement `json:"placements" yaml:"placements"`
	PlacementsCapped       bool                       `json:"placements_capped" yaml:"placements_capped"`
	PlacementsNotCheckable int                        `json:"placements_not_checkable" yaml:"placements_not_checkable"`
}

// ApplicationProvenanceChecks is the newest kept check of every installation
// of the application, by environment, in the order of the environments read.
// KeptFrom is a moment from which every check is kept.
type ApplicationProvenanceChecks struct {
	RawDocument `json:"-" yaml:"-"`

	ApplicationID string                       `json:"application_id" yaml:"application_id"`
	ReadState     string                       `json:"read_state" yaml:"read_state"`
	KeptFrom      string                       `json:"kept_from" yaml:"kept_from"`
	Environments  []ProvenanceCheckEnvironment `json:"environments" yaml:"environments"`
}

// ProvenanceInstallationCheck is the check "Check now" made of one
// installation and kept as its last check. State is verified, refused or
// unknown, as on a kept check; ImageTag is nil when its images could not be
// named.
type ProvenanceInstallationCheck struct {
	InstallationID string  `json:"installation_id" yaml:"installation_id"`
	ClusterID      string  `json:"cluster_id" yaml:"cluster_id"`
	Namespace      string  `json:"namespace" yaml:"namespace"`
	CheckedAt      string  `json:"checked_at" yaml:"checked_at"`
	ImageTag       *string `json:"image_tag" yaml:"image_tag"`
	State          string  `json:"state" yaml:"state"`
	Message        *string `json:"message" yaml:"message"`
}

// ProvenanceEnvironmentCheck is the answer to "Check now" of one environment:
// the verdict and the decision a deploy of what it runs would reach, and one
// check for each installation checked. ChecksCapped says the environment
// holds more than 20 installations and only the first 20 were checked;
// ImagesCapped says the verdict's image lists were cut.
type ProvenanceEnvironmentCheck struct {
	RawDocument `json:"-" yaml:"-"`

	ApplicationID string                        `json:"application_id" yaml:"application_id"`
	Mode          string                        `json:"mode" yaml:"mode"`
	Decision      string                        `json:"decision" yaml:"decision"`
	Verdict       ProvenanceVerdict             `json:"verdict" yaml:"verdict"`
	Checks        []ProvenanceInstallationCheck `json:"checks" yaml:"checks"`
	ChecksCapped  bool                          `json:"checks_capped" yaml:"checks_capped"`
	ImagesCapped  bool                          `json:"images_capped" yaml:"images_capped"`
}

type provenanceEnvironmentCheckRequest struct {
	EnvironmentID string `json:"environment_id"`
}

func applicationProvenanceChecksURL(baseURL string, applicationID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/provenance/checks", baseURL, neturl.PathEscape(applicationID))
}

// GetApplicationProvenanceChecks reads the newest kept check of every
// installation of the application, by environment.
// GET /api/v1/org/applications/{application_id}/provenance/checks
func (c *Client) GetApplicationProvenanceChecks(ctx context.Context, applicationID string) (*ApplicationProvenanceChecks, error) {
	var checks ApplicationProvenanceChecks
	if requestError := c.sendJSONContext(ctx, http.MethodGet,
		applicationProvenanceChecksURL(c.BaseURL, applicationID), nil, &checks); requestError != nil {
		return nil, requestError
	}
	if checks.Environments == nil {
		checks.Environments = []ProvenanceCheckEnvironment{}
	}
	return &checks, nil
}

// CheckApplicationProvenanceEnvironment is "Check now" of one environment:
// the platform checks what each installation of it was deployed with and
// keeps each check as that installation's last one. environmentID is an id
// the environments read lists.
// POST /api/v1/org/applications/{application_id}/provenance/verify
func (c *Client) CheckApplicationProvenanceEnvironment(ctx context.Context, applicationID string, environmentID string) (*ProvenanceEnvironmentCheck, error) {
	var check ProvenanceEnvironmentCheck
	if requestError := c.sendJSONContext(ctx, http.MethodPost,
		applicationProvenanceVerifyURL(c.BaseURL, applicationID),
		provenanceEnvironmentCheckRequest{EnvironmentID: environmentID}, &check); requestError != nil {
		return nil, requestError
	}
	if check.Checks == nil {
		check.Checks = []ProvenanceInstallationCheck{}
	}
	return &check, nil
}
