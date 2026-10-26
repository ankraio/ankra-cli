package client

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// ProfileSummaryRouting identifies the broker connection and permitted upstream.
// Empty Upstream allows any provider satisfying the platform privacy requirements.
type ProfileSummaryRouting struct {
	Ingress  string `json:"ingress"`
	Upstream string `json:"upstream"`
}

// UnmarshalJSON distinguishes an explicitly empty upstream from an absent one.
func (routing *ProfileSummaryRouting) UnmarshalJSON(content []byte) error {
	var fields struct {
		Ingress  *string `json:"ingress"`
		Upstream *string `json:"upstream"`
	}
	if json.Unmarshal(content, &fields) != nil || fields.Ingress == nil || fields.Upstream == nil {
		return fmt.Errorf("incomplete summary routing")
	}
	*routing = ProfileSummaryRouting{Ingress: *fields.Ingress, Upstream: *fields.Upstream}
	return nil
}

var summaryUpstreamPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*(?:/[a-z0-9]+(?:[-_][a-z0-9]+)*)?$`)

// Valid restricts routing to supported connections and exact provider identifiers.
func (routing ProfileSummaryRouting) Valid() bool {
	return (routing.Ingress == "global" || routing.Ingress == "europe") && len(routing.Upstream) <= 128 && (routing.Upstream == "" || summaryUpstreamPattern.MatchString(routing.Upstream))
}

// PreviewProfileSummaryPolicy reads a proposed destination without saving it.
// An older backend that ignores the query cannot confirm the requested route.
func (client *Client) PreviewProfileSummaryPolicy(ctx context.Context, routing ProfileSummaryRouting) (json.RawMessage, error) {
	if !routing.Valid() {
		return nil, fmt.Errorf("invalid summary routing")
	}
	query := url.Values{"openrouter_ingress": {routing.Ingress}, "openrouter_upstream": {routing.Upstream}}
	payload, requestError := client.profileSummaryRequest(ctx, http.MethodGet, profileSummaryPolicyPath+"?"+query.Encode(), nil)
	if requestError != nil {
		return nil, requestError
	}
	var preview struct {
		RouteState string `json:"route_state"`
		Route      *struct {
			Provider string                 `json:"provider"`
			Endpoint string                 `json:"endpoint_url"`
			Digest   string                 `json:"consent_digest"`
			Routing  *ProfileSummaryRouting `json:"openrouter_routing"`
		} `json:"route"`
	}
	invalid := fmt.Errorf("the proposed AI destination could not be confirmed; read policy get before continuing")
	if json.Unmarshal(payload, &preview) != nil || preview.RouteState != "available" || preview.Route == nil || preview.Route.Provider != "openrouter" || preview.Route.Routing == nil || *preview.Route.Routing != routing {
		return nil, invalid
	}
	endpoint := "https://openrouter.ai/api/v1"
	if routing.Ingress == "europe" {
		endpoint = "https://eu.openrouter.ai/api/v1"
	}
	digest, decodeError := hex.DecodeString(preview.Route.Digest)
	if preview.Route.Endpoint != endpoint || decodeError != nil || len(digest) != 32 || strings.ToLower(preview.Route.Digest) != preview.Route.Digest {
		return nil, invalid
	}
	return payload, nil
}
