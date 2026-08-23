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

TLS ON YOUR OWN PREVIEW DOMAIN

With a demo base domain set, Ankra will not order a certificate unless you say
how. Give it one of:

  --demo-tls-secret     An existing secret on the staging cluster holding a
                        wildcard certificate for the preview domain.
  --demo-cert-issuer    A cert-manager ClusterIssuer. Ankra annotates each demo
                        ingress with it, so cert-manager issues a per-preview
                        certificate. Use this when you hold no wildcard.

With neither, previews are served over plain http.

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

Requires organisation admin.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}

		changes := map[string]*string{}
		for flagName, field := range map[string]string{
			"demo-base-domain":   "demo_base_domain",
			"demo-ingress-class": "demo_ingress_class_name",
			"demo-tls-secret":    "demo_tls_secret_name",
			"demo-cert-issuer":   "demo_cert_issuer_name",
		} {
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
// field falls back to. The TLS line is the one worth spelling out: a preview
// domain with neither a secret nor an issuer serves plain http, which is the
// state that used to be invisible until someone opened a preview.
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
	if settings.DemoBaseDomain == "" {
		_, _ = fmt.Fprintln(out, "Preview domain:    (none - demos use the staging cluster's Ankra subzone)")
		return nil
	}
	_, _ = fmt.Fprintf(out, "Preview domain:    %s\n", settings.DemoBaseDomain)
	_, _ = fmt.Fprintf(out, "Ingress class:     %s\n", orEmptyDefault(settings.DemoIngressClassName))
	_, _ = fmt.Fprintf(out, "TLS secret:        %s\n", orEmptyDefault(settings.DemoTLSSecretName))
	_, _ = fmt.Fprintf(out, "Certificate issuer:%s\n", " "+orEmptyDefault(settings.DemoCertIssuerName))
	if settings.DemoTLSSecretName == "" && settings.DemoCertIssuerName == "" {
		_, _ = fmt.Fprintln(out,
			"\nPreviews on this domain are served over plain http. Set --demo-tls-secret to a\n"+
				"wildcard certificate, or --demo-cert-issuer to a cert-manager ClusterIssuer for\n"+
				"per-preview certificates.")
	}
	return nil
}

func orEmptyDefault(value string) string {
	if value == "" {
		return "(cluster default)"
	}
	return value
}

func init() {
	registerStructuredOutputFlags(orgAIEnvironmentGetCmd, orgAIEnvironmentSetCmd)
	orgAIEnvironmentSetCmd.Flags().String("demo-base-domain", "",
		"Wildcard zone demos are published under; empty clears it")
	orgAIEnvironmentSetCmd.Flags().String("demo-ingress-class", "",
		"Ingress class for demo ingresses; empty clears it")
	orgAIEnvironmentSetCmd.Flags().String("demo-tls-secret", "",
		"Secret holding a wildcard certificate for the preview domain; empty clears it")
	orgAIEnvironmentSetCmd.Flags().String("demo-cert-issuer", "",
		"cert-manager ClusterIssuer for per-preview certificates; empty clears it")
	orgAIEnvironmentCmd.AddCommand(orgAIEnvironmentGetCmd)
	orgAIEnvironmentCmd.AddCommand(orgAIEnvironmentSetCmd)
	orgCmd.AddCommand(orgAIEnvironmentCmd)
}
