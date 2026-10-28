package cmd

import (
	"errors"
	"fmt"
)

// customDNSProviderCloudflare is the provider a --cloudflare-credential
// declaration asks the platform for.
const customDNSProviderCloudflare = "cloudflare"

// resolveCustomDNSCredentialFlags turns the two mutually exclusive credential
// flags of a custom DNS zone declaration into the credential name and the
// provider the platform must find it to be.
//
// --credential accepts whichever kind of organisation credential the name
// resolves to (a DNS webhook credential, a Cloudflare credential, or the
// Avura connection), exactly as before the flag existed. --cloudflare-credential
// names an organisation Cloudflare credential ('ankra org cloudflare
// connect') and has the platform refuse anything else, so the zone is
// published by external-dns's native Cloudflare provider and never by a
// webhook credential that happens to share the name.
func resolveCustomDNSCredentialFlags(credential string, cloudflareCredential string) (string, string, error) {
	switch {
	case credential != "" && cloudflareCredential != "":
		return "", "", withExitCode(exitUsage, errors.New("pass either --credential or --cloudflare-credential, not both"))
	case cloudflareCredential != "":
		return cloudflareCredential, customDNSProviderCloudflare, nil
	case credential != "":
		return credential, "", nil
	}
	return "", "", withExitCode(exitUsage, errors.New("name the credential that publishes into the zone: --credential <name>, or --cloudflare-credential <name> for an organisation Cloudflare credential"))
}

// customDNSPublishingSummary is the human line describing how a declared zone
// will be published, from what the platform reported.
func customDNSPublishingSummary(provider string, policy string, cloudflareZone string, dnsEditAccess string) string {
	if provider == "" {
		return ""
	}
	summary := fmt.Sprintf("Published with the %s provider.", provider)
	if policy != "" {
		summary = fmt.Sprintf("Published with the %s provider, policy %s.", provider, policy)
	}
	if policy == "upsert-only" {
		summary += " Ankra creates and updates only the records it owns and never changes or deletes a record someone else made."
	}
	if cloudflareZone != "" {
		summary += fmt.Sprintf(" Records land in the Cloudflare zone %s", cloudflareZone)
		if dnsEditAccess != "granted" {
			summary += " (Cloudflare did not report the token's DNS edit permission; if nothing is published, check the token has DNS:Edit on it)."
		} else {
			summary += "."
		}
	}
	return summary
}

// customDNSProviderLabel renders a listed zone's provider, naming the case
// where the platform could not resolve the credential rather than leaving a
// blank cell.
func customDNSProviderLabel(provider string) string {
	if provider == "" {
		return "-"
	}
	return provider
}
