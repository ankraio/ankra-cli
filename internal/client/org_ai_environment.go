package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// OrganisationPreviewSettings is the preview half of the organisation's AI
// environment settings (GET/PUT /api/v1/org/ai-environment) - the fields that
// decide where a PR demo is published and how it terminates TLS. The root
// domain on the same endpoint is a different setting with a much wider blast
// radius and stays with 'ankra org domain'.
//
// Empty means unset in every field: the demos then hang off the staging
// cluster's own Ankra subzone, or stay in-cluster-only.
type OrganisationPreviewSettings struct {
	DemoBaseDomain       string `json:"demo_base_domain" yaml:"demo_base_domain"`
	DemoIngressClassName string `json:"demo_ingress_class_name" yaml:"demo_ingress_class_name"`
	DemoTLSSecretName    string `json:"demo_tls_secret_name" yaml:"demo_tls_secret_name"`
	DemoCertIssuerName   string `json:"demo_cert_issuer_name" yaml:"demo_cert_issuer_name"`

	// PreviewTLSWarning is the backend's own verdict on whether these
	// settings publish demos over plain http. It depends on what the
	// staging cluster carries, so it cannot be worked out from the fields
	// above alone. Empty when previews have a certificate story.
	PreviewTLSWarning string `json:"demo_preview_tls_warning" yaml:"demo_preview_tls_warning"`

	// PreviewDNSWarning is the backend's verdict on whether the preview
	// hostnames resolve at all. It is a separate answer from the TLS one and
	// the more fundamental of the two: nothing in the platform publishes DNS
	// on an organisation's own domain, so an unpublished preview domain costs
	// the URL and the certificate together.
	PreviewDNSWarning string `json:"demo_preview_dns_warning" yaml:"demo_preview_dns_warning"`

	// PreviewDNSReported is false when the platform did not send the field at
	// all, which is not the same as sending it empty. Both leave
	// PreviewDNSWarning blank, and without this the two are indistinguishable
	// - a platform too old to check would read exactly like one that checked
	// and found nothing wrong. Letting an absent answer pass for a good one
	// is the failure this whole lane has been undoing, so it is not repeated
	// here for the sake of one bool.
	//
	// Kept out of the structured output: it describes the platform that
	// answered, not the organisation's settings, and -o json mirrors the wire.
	PreviewDNSReported bool `json:"-" yaml:"-"`
}

type organisationPreviewSettingsBody struct {
	DemoBaseDomain       *string `json:"demo_base_domain"`
	DemoIngressClassName *string `json:"demo_ingress_class_name"`
	DemoTLSSecretName    *string `json:"demo_tls_secret_name"`
	DemoCertIssuerName   *string `json:"demo_cert_issuer_name"`
	PreviewTLSWarning    *string `json:"demo_preview_tls_warning"`

	// Decoded as raw JSON, not *string, because those two answer different
	// questions here. The platform sends this field without omitempty, so a
	// healthy organisation gets an explicit null - and *string cannot tell an
	// explicit null from a key that was never sent, since both decode to nil.
	// Reading one as the other would have put the "not an all-clear" caveat
	// in front of every healthy organisation. RawMessage is nil only when the
	// key is genuinely absent.
	PreviewDNSWarning json.RawMessage `json:"demo_preview_dns_warning"`
}

// GetOrganisationPreviewSettings reads the organisation's preview settings.
func (c *Client) GetOrganisationPreviewSettings(ctx context.Context) (*OrganisationPreviewSettings, error) {
	body, requestError := c.doAIEnvironmentRequest(ctx, http.MethodGet, nil)
	if requestError != nil {
		return nil, requestError
	}
	return decodeOrganisationPreviewSettings(body)
}

// UpdateOrganisationPreviewSettings writes only the fields present in
// changes. The endpoint reads presence, not emptiness: a key carrying nil
// clears that field, and a key left out of the map is not touched - so
// setting one field never silently clears its neighbours.
func (c *Client) UpdateOrganisationPreviewSettings(ctx context.Context,
	changes map[string]*string) (*OrganisationPreviewSettings, error) {
	payload := make(map[string]any, len(changes))
	for field, value := range changes {
		if value == nil {
			payload[field] = nil
			continue
		}
		payload[field] = *value
	}
	encoded, marshalError := json.Marshal(payload)
	if marshalError != nil {
		return nil, fmt.Errorf("encode request: %w", marshalError)
	}
	body, requestError := c.doAIEnvironmentRequest(ctx, http.MethodPut, encoded)
	if requestError != nil {
		return nil, requestError
	}
	return decodeOrganisationPreviewSettings(body)
}

func decodeOrganisationPreviewSettings(body []byte) (*OrganisationPreviewSettings, error) {
	var decoded organisationPreviewSettingsBody
	if unmarshalError := json.Unmarshal(body, &decoded); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	settings := OrganisationPreviewSettings{}
	if decoded.DemoBaseDomain != nil {
		settings.DemoBaseDomain = *decoded.DemoBaseDomain
	}
	if decoded.DemoIngressClassName != nil {
		settings.DemoIngressClassName = *decoded.DemoIngressClassName
	}
	if decoded.DemoTLSSecretName != nil {
		settings.DemoTLSSecretName = *decoded.DemoTLSSecretName
	}
	if decoded.DemoCertIssuerName != nil {
		settings.DemoCertIssuerName = *decoded.DemoCertIssuerName
	}
	if decoded.PreviewTLSWarning != nil {
		settings.PreviewTLSWarning = *decoded.PreviewTLSWarning
	}
	if decoded.PreviewDNSWarning != nil {
		settings.PreviewDNSReported = true
		var warning *string
		if unmarshalError := json.Unmarshal(decoded.PreviewDNSWarning, &warning); unmarshalError == nil &&
			warning != nil {
			settings.PreviewDNSWarning = *warning
		}
	}
	return &settings, nil
}
