package client

import "testing"

// The platform sends demo_preview_dns_warning without omitempty, so a healthy
// organisation gets an explicit null. Decoding that into a *string makes it
// nil - identical to a key that was never sent - and reading one as the other
// would put the "not an all-clear" caveat in front of every healthy
// organisation. These pin the three cases apart (PLA-773).
func TestDecodeOrganisationPreviewSettingsSeparatesNullFromAbsent(t *testing.T) {
	checked, decodeError := decodeOrganisationPreviewSettings(
		[]byte(`{"demo_base_domain":"smartoptics.dev","demo_preview_dns_warning":null}`))
	if decodeError != nil {
		t.Fatalf("unexpected error: %v", decodeError)
	}
	if !checked.PreviewDNSReported {
		t.Errorf("an explicit null is the platform reporting no problem, not staying silent")
	}
	if checked.PreviewDNSWarning != "" {
		t.Errorf("and carries no warning text, got %q", checked.PreviewDNSWarning)
	}

	silent, decodeError := decodeOrganisationPreviewSettings(
		[]byte(`{"demo_base_domain":"smartoptics.dev"}`))
	if decodeError != nil {
		t.Fatalf("unexpected error: %v", decodeError)
	}
	if silent.PreviewDNSReported {
		t.Errorf("a key that was never sent is not a verdict")
	}

	warned, decodeError := decodeOrganisationPreviewSettings(
		[]byte(`{"demo_base_domain":"smartoptics.dev","demo_preview_dns_warning":"hostnames do not resolve"}`))
	if decodeError != nil {
		t.Fatalf("unexpected error: %v", decodeError)
	}
	if !warned.PreviewDNSReported || warned.PreviewDNSWarning != "hostnames do not resolve" {
		t.Errorf("a sent warning must survive decoding, got reported=%v %q",
			warned.PreviewDNSReported, warned.PreviewDNSWarning)
	}
}
