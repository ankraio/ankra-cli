package client

import (
	"net/http"
	"strings"
)

// CloudWasteFinding is one open finding of GET /org/cloud-cost/waste, read
// for what a waste cleanup's consent needs: the id a waste_cleanup proposal's
// plan names and the kind its consent has to name.
type CloudWasteFinding struct {
	ID           string `json:"id" yaml:"id"`
	Kind         string `json:"kind" yaml:"kind"`
	ResourceName string `json:"resource_name" yaml:"resource_name"`
}

// CloudWasteConsentPolicy is the listing's consent policy (openapi
// CloudWasteConsentPolicy). ConsentByKind is the consent value that names
// each kind, the one a cleanup of that kind sends (cluster#5646); a platform
// that predates it sends none, and a nil map reads as "names none", never as
// "needs none". SentenceByKind is what the person agrees to for each kind.
type CloudWasteConsentPolicy struct {
	Kinds          []string          `json:"kinds" yaml:"kinds"`
	SentenceByKind map[string]string `json:"sentence_by_kind" yaml:"sentence_by_kind"`
	ConsentByKind  map[string]string `json:"consent_by_kind" yaml:"consent_by_kind"`
}

// CloudWaste is GET /org/cloud-cost/waste as far as the CLI reads it: the
// open findings and the consent policy.
type CloudWaste struct {
	RawDocument `json:"-" yaml:"-"`

	Findings []CloudWasteFinding     `json:"findings" yaml:"findings"`
	Consent  CloudWasteConsentPolicy `json:"consent" yaml:"consent"`
}

// GetCloudWaste reads the organisation's open waste findings and the consent
// policy. It is read organisation-wide, without the cost project scope: a
// proposal is not filed under a project, so the finding it names may sit
// outside any one project's listing.
// GET /api/v1/org/cloud-cost/waste
func (c *Client) GetCloudWaste() (*CloudWaste, error) {
	var result CloudWaste
	if err := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/cloud-cost/waste", nil, &result); err != nil {
		return nil, err
	}
	if result.Findings == nil {
		result.Findings = []CloudWasteFinding{}
	}
	return &result, nil
}

// WasteKindConsent is the consent a waste cleanup's run sends when the
// platform names one for the kind of the finding it cleans up.
type WasteKindConsent struct {
	// Value is the kind-named consent, e.g. delete_stopped_server_acknowledged.
	Value string
	// Kind is the finding's kind, e.g. stopped_server.
	Kind string
	// ResourceName is the finding's resource, as the listing names it.
	ResourceName string
	// Sentence is what the person agrees to for the kind, in the platform's
	// words; empty when the platform gives none.
	Sentence string
}

// KindConsentFor is the kind-named consent for a waste_cleanup proposal: the
// value consent_by_kind gives the kind of the finding its plan names
// (plan.parameters.waste_finding_id). found is false when the proposal is no
// waste cleanup, its plan names no finding, the finding is not among the
// open ones listed, or the platform names no value for its kind (a platform
// that predates consent_by_kind). The caller then sends the generic consent,
// which the platform takes for a single finding.
func (waste *CloudWaste) KindConsentFor(proposal *DecisionProposal) (consent WasteKindConsent, found bool) {
	if waste == nil || proposal == nil || proposal.Kind != "waste_cleanup" {
		return WasteKindConsent{}, false
	}
	parameters, _ := proposal.Plan["parameters"].(map[string]any)
	findingID, _ := parameters["waste_finding_id"].(string)
	findingID = strings.TrimSpace(findingID)
	if findingID == "" {
		return WasteKindConsent{}, false
	}
	for _, finding := range waste.Findings {
		if !strings.EqualFold(strings.TrimSpace(finding.ID), findingID) {
			continue
		}
		value := strings.TrimSpace(waste.Consent.ConsentByKind[finding.Kind])
		if value == "" {
			return WasteKindConsent{}, false
		}
		return WasteKindConsent{
			Value:        value,
			Kind:         finding.Kind,
			ResourceName: finding.ResourceName,
			Sentence:     strings.TrimSpace(waste.Consent.SentenceByKind[finding.Kind]),
		}, true
	}
	return WasteKindConsent{}, false
}
