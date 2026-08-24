package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var orgAIEnvironmentCmd = &cobra.Command{
	Use:   "ai-environment",
	Short: "Show or change where PR demos are published",
	Long: `Show or change the organisation's preview settings - where an on-demand
demo or PR preview is published, and how it terminates TLS.

  ankra org ai-environment get
  ankra org ai-environment set --demo-base-domain previews.example.com
  ankra org ai-environment set --demo-cert-issuer letsencrypt-prod
  ankra org ai-environment set --demo-base-domain ""

THE PREVIEW DOMAIN IS NOT THE ROOT DOMAIN

Both settings live on the same portal screen (AI > Settings > Workspaces) and
they are easy to confuse, so:

  --demo-base-domain    "Preview domain". A wildcard zone YOU point at the
  (this command)        staging cluster's ingress. Demos are published at
                        <namespace>.<that domain>. Ankra writes nothing in it
                        and nothing else changes. One field, no blockers.

  ankra org domain      "Custom Ankra domain". The root every Ankra-generated
                        hostname nests under. Ankra delegates a subzone in it,
                        and the switch is refused while cluster DNS zones or
                        DNS records still live under the old root.

If you only want demos on your own domain, this command is the one you want.

PUBLISHING THE RECORDS IS YOURS

Ankra mints each demo a hostname under the preview domain, but it publishes
DNS only inside the subzones it delegates, and the external-dns credential it
provisions for a cluster is scoped to that cluster's own Ankra subzone and
nothing else. Nothing in the platform can create a record on your domain, so
a preview hostname resolves only because you published it - in practice a
wildcard pointing at the staging cluster's ingress.

Without it a demo still deploys and still reports ready, and its URL does not
load. The certificate goes the same way: an HTTP-01 challenge is answered
over the hostname being certified, so if the name does not resolve Let's
Encrypt cannot reach the solver and none is ever issued. An unpublished
preview domain costs you the URL and the TLS together.

'get' says so when the domain answers nothing, and names the wildcard to
create and the address to point it at. On the Ankra subzone none of this
applies: the platform provisions the zone and the in-cluster external-dns
publishes each preview record itself.

TLS ON YOUR OWN PREVIEW DOMAIN

Demos on your own base domain request a per-preview certificate from the
staging cluster's cert-manager issuer, the same way demos on an Ankra subzone
do. Each demo hostname is concrete when the ingress is written, so an HTTP-01
challenge answers for it; no wildcard is involved. Two flags change that:

  --demo-tls-secret     A secret already holding a certificate for the preview
                        domain. Ankra serves it and requests nothing.
  --demo-cert-issuer    A different cert-manager ClusterIssuer to ask, for a
                        cluster whose issuer is not the one Ankra's networking
                        stack installs.

A cluster carrying no ACME HTTP-01 issuer cannot be asked for a certificate,
so its previews stay on plain http. 'get' says so when that is the case.

Reading requires organisation membership; changing it requires organisation
admin.`,
}

var orgAIEnvironmentGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the organisation's preview settings",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		settings, requestError := apiClient.GetOrganisationPreviewSettings(ctx)
		if requestError != nil {
			return fmt.Errorf("get organisation preview settings: %w", requestError)
		}
		return renderOrganisationPreviewSettings(cmd, settings, out)
	},
}

var orgAIEnvironmentSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Change the organisation's preview settings",
	Long: `Change where PR demos are published, and how they terminate TLS.

Only the flags you pass are written; the rest keep their stored values. Pass a
flag with an empty value to clear that field:

  ankra org ai-environment set --demo-base-domain previews.example.com
  ankra org ai-environment set --demo-cert-issuer letsencrypt-prod
  ankra org ai-environment set --demo-cert-issuer ""

--demo-cert-issuer only applies alongside a demo base domain: on the Ankra
subzone Ankra picks the issuer itself, from the networking stack it can
verify. Naming one without a base domain is refused rather than stored.

A write that leaves previews on plain http reports it in the output rather
than succeeding silently.

Requires organisation admin.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}

		changes := map[string]*string{}
		for flagName, field := range previewSettingFlagFields {
			flag := cmd.Flags().Lookup(flagName)
			if flag == nil || !flag.Changed {
				continue
			}
			if flag.Value.String() == "" {
				changes[field] = nil
				continue
			}
			value := flag.Value.String()
			changes[field] = &value
		}
		if len(changes) == 0 {
			return withExitCode(exitUsage, errors.New(
				"pass at least one of --demo-base-domain, --demo-ingress-class, "+
					"--demo-tls-secret or --demo-cert-issuer"))
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		settings, requestError := apiClient.UpdateOrganisationPreviewSettings(ctx, changes)
		if requestError != nil {
			return fmt.Errorf("set organisation preview settings: %w", requestError)
		}
		return renderOrganisationPreviewSettings(cmd, settings, out)
	},
}

// renderOrganisationPreviewSettings prints the settings, naming what an empty
// field falls back to. The plain-http verdict is the backend's rather than
// this command's: whether a certificate can be requested depends on what the
// staging cluster carries, and that state used to be invisible until someone
// opened a preview.
func renderOrganisationPreviewSettings(cmd *cobra.Command,
	settings *client.OrganisationPreviewSettings, format outputFormat) error {
	switch format {
	case outputJSON:
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(settings)
	case outputYAML:
		encoder := yaml.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent(2)
		defer func() { _ = encoder.Close() }()
		return encoder.Encode(settings)
	}

	out := cmd.OutOrStdout()
	// The three overrides only take effect alongside a preview domain, but a
	// stored value has to stay visible without one: clearing only the domain
	// otherwise leaves settings on the organisation that nothing can show and
	// that come back into force the moment a domain is set again.
	hasStoredOverride := settings.DemoIngressClassName != "" ||
		settings.DemoTLSSecretName != "" || settings.DemoCertIssuerName != ""
	if settings.DemoBaseDomain == "" {
		_, _ = fmt.Fprintln(out, "Preview domain:    (none - demos use the staging cluster's Ankra subzone)")
	} else {
		_, _ = fmt.Fprintf(out, "Preview domain:    %s\n", settings.DemoBaseDomain)
	}
	if settings.DemoBaseDomain != "" || hasStoredOverride {
		_, _ = fmt.Fprintf(out, "Ingress class:     %s\n", orEmptyDefault(settings.DemoIngressClassName))
		_, _ = fmt.Fprintf(out, "TLS secret:        %s\n", orEmptyDefault(settings.DemoTLSSecretName))
		_, _ = fmt.Fprintf(out, "Certificate issuer:%s\n", " "+orEmptyDefault(settings.DemoCertIssuerName))
	}
	if settings.DemoBaseDomain == "" && hasStoredOverride {
		_, _ = fmt.Fprintln(out,
			"\nThese three apply only alongside a preview domain, so they are inert until one is set.")
	}
	// Printed whatever the fields say. The backend decides when previews
	// lack TLS, and tying the display to a field here would put this command
	// back in the business of second-guessing that.
	// DNS first: an unresolvable hostname is why the TLS one is usually
	// there too, and reading the certificate complaint before the reason for
	// it sends people to the wrong setting.
	if settings.PreviewDNSWarning != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", settings.PreviewDNSWarning)
	}
	if settings.PreviewTLSWarning != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", settings.PreviewTLSWarning)
	}
	return nil
}

func orEmptyDefault(value string) string {
	if value == "" {
		return "(cluster default)"
	}
	return value
}

// previewSettingFlagFields maps each set flag to the wire field it writes.
// Registration and parsing both read it, so a renamed flag cannot become a
// silent no-op that cobra accepts and the update loop never matches.
var previewSettingFlagFields = map[string]string{
	"demo-base-domain":   "demo_base_domain",
	"demo-ingress-class": "demo_ingress_class_name",
	"demo-tls-secret":    "demo_tls_secret_name",
	"demo-cert-issuer":   "demo_cert_issuer_name",
}

// previewSettingFlagUsage is the help string for each flag in
// previewSettingFlagFields. Kept beside it so registration can walk the field
// map itself: a field added without a usage string then fails at init rather
// than registering no flag at all, which the update loop would skip in
// silence.
var previewSettingFlagUsage = map[string]string{
	"demo-base-domain":   "Wildcard zone demos are published under; empty clears it",
	"demo-ingress-class": "Ingress class for demo ingresses; empty clears it",
	"demo-tls-secret":    "Secret holding a certificate for the preview domain; empty clears it",
	"demo-cert-issuer":   "cert-manager ClusterIssuer to ask instead of the cluster's own; empty clears it",
}

func init() {
	registerStructuredOutputFlags(orgAIEnvironmentGetCmd, orgAIEnvironmentSetCmd)
	// Both directions of drift are fatal here: walking the field map catches
	// a field with no usage string, and the count check catches a usage
	// string naming a flag that writes nothing.
	if len(previewSettingFlagUsage) != len(previewSettingFlagFields) {
		panic("preview setting flag usage and wire-field maps disagree")
	}
	for flagName := range previewSettingFlagFields {
		usage, hasUsage := previewSettingFlagUsage[flagName]
		if !hasUsage {
			panic("preview setting flag " + flagName + " has no usage string")
		}
		orgAIEnvironmentSetCmd.Flags().String(flagName, "", usage)
	}
	orgAIEnvironmentCmd.AddCommand(orgAIEnvironmentGetCmd)
	orgAIEnvironmentCmd.AddCommand(orgAIEnvironmentSetCmd)
	orgCmd.AddCommand(orgAIEnvironmentCmd)
}
