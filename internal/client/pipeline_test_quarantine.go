package client

// The flaky test quarantine routes (cluster ankra-q573dh.9.10,
// go/internal/pipelineapi/testquarantine.go): a repository's quarantine
// settings, its quarantined and recently released tests, the default
// branch's tests that qualify, and quarantining or releasing one test. Every
// route is addressable by application or by repository; reads need
// pipelines.read and writes pipelines.manage.
//
// A quarantined test still runs and is still reported. What changes is that
// a failing run step whose only failed tests are quarantined settles as a
// success, and the run's test summary lists those failures under
// "quarantined" whether or not they rescued their step.

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

// PipelineTestQuarantineSettings is a repository's quarantine configuration:
// whether it forgives anything at all (off by default), the default-branch
// flaky rate and the number of observed runs a test must reach to be
// quarantined, and how many default-branch passes in a row release one.
type PipelineTestQuarantineSettings struct {
	Enabled            bool    `json:"enabled"`
	MinFlakyRate       float64 `json:"min_flaky_rate"`
	MinSamples         int     `json:"min_samples"`
	ReleaseAfterPasses int     `json:"release_after_passes"`
}

// PipelineTestQuarantine is one quarantine decision. ReleasedAt and the
// release fields are nil while it is live; Suite, File and the actor emails
// are nil when nothing was recorded. ReleasedByEmail is "system" for a
// release after enough passes, whose ReleaseReason is "passes".
type PipelineTestQuarantine struct {
	ID                string  `json:"id"`
	TestKey           string  `json:"test_key"`
	Suite             *string `json:"suite"`
	Name              string  `json:"name"`
	File              *string `json:"file"`
	Reason            string  `json:"reason"`
	Source            string  `json:"source"`
	FlakyRate         float64 `json:"flaky_rate"`
	Samples           int     `json:"samples"`
	CreatedByEmail    *string `json:"created_by_email"`
	CreatedAt         string  `json:"created_at"`
	ConsecutivePasses int     `json:"consecutive_passes"`
	ReleasedAt        *string `json:"released_at"`
	ReleasedByEmail   *string `json:"released_by_email"`
	ReleaseReason     *string `json:"release_reason"`
	ReleaseNote       *string `json:"release_note"`
}

// The release reasons the platform records.
const (
	PipelineTestQuarantineReleasedManually = "manual"
	PipelineTestQuarantineReleasedByPasses = "passes"
)

// PipelineTestQuarantineView is GET …/pipeline-tests/quarantine: the
// settings, the live quarantines (newest first) and the ones released in the
// last 30 days (newest first, at most 50).
type PipelineTestQuarantineView struct {
	Settings         PipelineTestQuarantineSettings `json:"settings"`
	Quarantines      []PipelineTestQuarantine       `json:"quarantines"`
	RecentlyReleased []PipelineTestQuarantine       `json:"recently_released"`
}

// PipelineTestQuarantineCandidate is one default-branch test that reaches
// the repository's thresholds over the last 30 days and is not quarantined.
type PipelineTestQuarantineCandidate struct {
	TestKey   string  `json:"test_key"`
	Suite     string  `json:"suite"`
	Name      string  `json:"name"`
	File      string  `json:"file"`
	FlakyRate float64 `json:"flaky_rate"`
	Flaky     int     `json:"flaky"`
	Observed  int     `json:"observed"`
}

// PipelineTestQuarantineCandidates is GET …/pipeline-tests/quarantine/candidates,
// most flaky first.
type PipelineTestQuarantineCandidates struct {
	DefaultBranch string                            `json:"default_branch"`
	Settings      PipelineTestQuarantineSettings    `json:"settings"`
	Candidates    []PipelineTestQuarantineCandidate `json:"candidates"`
}

// PipelineTestQuarantineSettingsUpdate is the PUT …/quarantine/settings
// body. A nil member is left out and keeps its stored value; the platform
// refuses a null, so a nil is never sent as one.
type PipelineTestQuarantineSettingsUpdate struct {
	Enabled            *bool    `json:"enabled,omitempty"`
	MinFlakyRate       *float64 `json:"min_flaky_rate,omitempty"`
	MinSamples         *int     `json:"min_samples,omitempty"`
	ReleaseAfterPasses *int     `json:"release_after_passes,omitempty"`
}

// IsEmpty reports whether the update names no setting.
func (update PipelineTestQuarantineSettingsUpdate) IsEmpty() bool {
	return update.Enabled == nil && update.MinFlakyRate == nil && update.MinSamples == nil &&
		update.ReleaseAfterPasses == nil
}

// QuarantinePipelineTestRequest is the POST …/pipeline-tests/quarantine body.
type QuarantinePipelineTestRequest struct {
	TestKey string `json:"test_key"`
	Reason  string `json:"reason"`
}

// releasePipelineTestQuarantineRequest is the POST …/{quarantine_id}/release
// body. An empty reason is left out; the body itself is always an object.
type releasePipelineTestQuarantineRequest struct {
	Reason string `json:"reason,omitempty"`
}

// GetPipelineTestQuarantine reads the selected repository's quarantine.
func (c *Client) GetPipelineTestQuarantine(ctx context.Context,
	selector PipelineSelector) (*PipelineTestQuarantineView, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineTestQuarantineView
	if requestError := c.doPipelineRequest(ctx, http.MethodGet, c.BaseURL+base+"/pipeline-tests/quarantine",
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// SetPipelineTestQuarantineSettings stores the named settings and answers
// the repository's settings as stored.
func (c *Client) SetPipelineTestQuarantineSettings(ctx context.Context, selector PipelineSelector,
	update PipelineTestQuarantineSettingsUpdate) (*PipelineTestQuarantineSettings, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineTestQuarantineSettings
	if requestError := c.doPipelineRequest(ctx, http.MethodPut,
		c.BaseURL+base+"/pipeline-tests/quarantine/settings", update, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ListPipelineTestQuarantineCandidates reads the default branch's tests that
// qualify for quarantine. A limit of zero leaves the count to the server.
func (c *Client) ListPipelineTestQuarantineCandidates(ctx context.Context, selector PipelineSelector,
	limit int) (*PipelineTestQuarantineCandidates, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	query := neturl.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var result PipelineTestQuarantineCandidates
	if requestError := c.doPipelineRequest(ctx, http.MethodGet,
		withQuery(c.BaseURL+base+"/pipeline-tests/quarantine/candidates", query), nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// QuarantinePipelineTest quarantines one test and answers the new
// quarantine. The platform copies the test's suite, name and file from its
// newest default-branch result, and refuses (409) while the quarantine is
// off, for a test not flaky enough, or for one already quarantined.
func (c *Client) QuarantinePipelineTest(ctx context.Context, selector PipelineSelector,
	request QuarantinePipelineTestRequest) (*PipelineTestQuarantine, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineTestQuarantine
	if requestError := c.doPipelineRequest(ctx, http.MethodPost, c.BaseURL+base+"/pipeline-tests/quarantine",
		request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// ReleasePipelineTestQuarantine releases one live quarantine and answers it.
// The reason is optional and left out when empty.
func (c *Client) ReleasePipelineTestQuarantine(ctx context.Context, selector PipelineSelector,
	quarantineID string, reason string) (*PipelineTestQuarantine, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineTestQuarantine
	if requestError := c.doPipelineRequest(ctx, http.MethodPost,
		fmt.Sprintf("%s%s/pipeline-tests/quarantine/%s/release", c.BaseURL, base, neturl.PathEscape(quarantineID)),
		releasePipelineTestQuarantineRequest{Reason: reason}, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}
