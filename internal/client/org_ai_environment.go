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
	//
	// Empty means either "checked, nothing to report" or "this platform does
	// not send the field at all", and the two are not distinguished. Against
	// a platform predating demo_preview_dns_warning the silence is therefore
	// unknown rather than all-clear. Left as version skew deliberately: the
	// field ships on the hosted platform, and a per-field "could not be
	// determined" state would cost every reader a distinction almost nobody
	// is on the wrong side of.
	PreviewDNSWarning string `json:"demo_preview_dns_warning" yaml:"demo_preview_dns_warning"`
}

type organisationPreviewSettingsBody struct {
	DemoBaseDomain       *string `json:"demo_base_domain"`
	DemoIngressClassName *string `json:"demo_ingress_class_name"`
	DemoTLSSecretName    *string `json:"demo_tls_secret_name"`
	DemoCertIssuerName   *string `json:"demo_cert_issuer_name"`
	PreviewTLSWarning    *string `json:"demo_preview_tls_warning"`
	PreviewDNSWarning    *string `json:"demo_preview_dns_warning"`
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
		settings.PreviewDNSWarning = *decoded.PreviewDNSWarning
	}
	return &settings, nil
}
