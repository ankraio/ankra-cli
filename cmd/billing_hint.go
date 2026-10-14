package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"

	"ankra/internal/client"
)

// billingRestrictedHintPrefix opens the one line printed after a billing
// refusal, with the portal's billing page appended when the organisation is
// known locally.
const billingRestrictedHintPrefix = "Add credit in the portal under Billing"

// billingPortalURL is the organisation's billing page in the portal, or ""
// when no organisation is known without an API call: neither a --org
// override nor a persisted `ankra org select`. The refusal is already an
// error path, so it never fetches the organisation just to build a link.
func billingPortalURL() string {
	organisationID := ""
	if apiClient != nil {
		organisationID = apiClient.OrganisationOverride()
	}
	if organisationID == "" {
		if selected, err := loadSelectedOrganisation(); err == nil {
			organisationID = selected.OrganisationID
		}
	}
	if organisationID == "" {
		return ""
	}
	return portalBaseURL() + "/organisation/" + url.PathEscape(organisationID) + "/billing"
}

// billingRestrictedHint is the line that follows the platform's sentence on
// a billing refusal: where to top up, with the page's address when the CLI
// can name it.
func billingRestrictedHint() string {
	if link := billingPortalURL(); link != "" {
		return billingRestrictedHintPrefix + ": " + link
	}
	return billingRestrictedHintPrefix + "."
}

// printBillingRestrictedHint writes the top-up hint after an error that is,
// or wraps, the platform's billing refusal; any other error prints nothing.
func printBillingRestrictedHint(out io.Writer, err error) {
	var restricted *client.BillingRestrictedError
	if !errors.As(err, &restricted) {
		return
	}
	_, _ = fmt.Fprintln(out, billingRestrictedHint())
}
