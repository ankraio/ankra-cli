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

var orgDomainCmd = &cobra.Command{
	Use:   "domain",
	Short: "Show or register the organisation's Ankra root domain",
	Long: `Show or register the root domain every Ankra-generated hostname in the
organisation nests under - the organisation's delegated DNS zone, its clusters'
domains, and the preview hostnames built from them.

Unset, the platform default (ankra.cc) applies. Registering your own domain
requires it to be delegated to the Ankra nameservers first; the write is
refused otherwise, naming the nameservers to point at.

  ankra org domain get
  ankra org domain set example.com
  ankra org domain set --default

This is the same setting the portal writes at AI > Settings > Workspaces
("Custom Ankra domain"). Changing it is refused while cluster DNS zones or DNS
records still live under the old root; the refusal lists exactly what to
remove. Use 'ankra org dns zones' and 'ankra org dns list' to inventory them,
'ankra cluster domain <cluster> --remove' and 'ankra org dns delete <record>'
to clear them. Ankra re-creates the organisation zone under the new domain
automatically once the switch is accepted.

Reading requires organisation membership; changing it requires organisation
admin.`,
}

var orgDomainGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the organisation's Ankra root domain",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		domain, err := apiClient.GetOrganisationDomain(ctx)
		if err != nil {
			return fmt.Errorf("get organisation domain: %w", err)
		}
		return renderOrganisationDomain(cmd, domain, out)
	},
}

var orgDomainSetCmd = &cobra.Command{
	Use:   "set [domain]",
	Short: "Register the organisation's own Ankra root domain",
	Long: `Register the organisation's own root domain, or clear it back to the
platform default with --default.

The domain must be a bare domain you own (example.com, not sub.example.com or
a domain under the platform default), already delegated to the Ankra
nameservers. The switch is refused while cluster DNS zones or DNS records
still live under the old root; the refusal lists them.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		if orgDomainUseDefault && len(args) > 0 {
			return withExitCode(exitUsage, errors.New("pass a domain or --default, not both"))
		}
		if !orgDomainUseDefault && len(args) == 0 {
			return withExitCode(exitUsage,
				errors.New("pass the domain to register, or --default to follow the platform default"))
		}
		rootDomain := ""
		if len(args) > 0 {
			rootDomain = args[0]
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
		defer cancel()

		domain, err := apiClient.SetOrganisationDomain(ctx, rootDomain)
		if err != nil {
			var blocked *client.OrganisationDomainBlockedError
			if errors.As(err, &blocked) {
				// A refused switch is the one failure whose payload a script
				// wants: with -o json|yaml the blocker inventory goes to
				// stdout in the requested format and the error stays short,
				// so structured output is still parseable. The exit code is
				// non-zero either way.
				if out == outputJSON || out == outputYAML {
					if renderError := renderOrganisationDomainBlocked(cmd, blocked, out); renderError != nil {
						return renderError
					}
					return withExitCode(exitError, errors.New("the organisation domain was not changed"))
				}
				return withExitCode(exitError, errors.New(organisationDomainBlockedMessage(blocked)))
			}
			return fmt.Errorf("set organisation domain: %w", err)
		}
		return renderOrganisationDomain(cmd, domain, out)
	},
}

var orgDomainUseDefault bool

// organisationDomainBlockedDocument is the machine-readable rendering of a
// refused switch: the same members the backend sent, so a script reading
// -o json|yaml sees the blocker inventory rather than an unparseable error
// string.
type organisationDomainBlockedDocument struct {
	Detail                        string                                         `json:"detail" yaml:"detail"`
	BlockingClusterZones          []client.OrganisationDomainBlockingClusterZone `json:"blocking_cluster_zones" yaml:"blocking_cluster_zones"`
	BlockingClusterZonesTruncated bool                                           `json:"blocking_cluster_zones_truncated,omitempty" yaml:"blocking_cluster_zones_truncated,omitempty"`
	BlockingDnsRecords            []client.OrganisationDomainBlockingDnsRecord   `json:"blocking_dns_records" yaml:"blocking_dns_records"`
	BlockingDnsRecordsTruncated   bool                                           `json:"blocking_dns_records_truncated,omitempty" yaml:"blocking_dns_records_truncated,omitempty"`
}

// renderOrganisationDomainBlocked writes the refusal inventory to stdout in
// the requested structured format.
func renderOrganisationDomainBlocked(cmd *cobra.Command, blocked *client.OrganisationDomainBlockedError, format outputFormat) error {
	document := organisationDomainBlockedDocument{
		Detail:                        blocked.Detail,
		BlockingClusterZones:          blocked.ClusterZones,
		BlockingClusterZonesTruncated: blocked.ClusterZonesTruncated,
		BlockingDnsRecords:            blocked.DnsRecords,
		BlockingDnsRecordsTruncated:   blocked.DnsRecordsTruncated,
	}
	if document.BlockingClusterZones == nil {
		document.BlockingClusterZones = []client.OrganisationDomainBlockingClusterZone{}
	}
	if document.BlockingDnsRecords == nil {
		document.BlockingDnsRecords = []client.OrganisationDomainBlockingDnsRecord{}
	}
	switch format {
	case outputJSON:
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(document)
	case outputYAML:
		encoder := yaml.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent(2)
		defer func() { _ = encoder.Close() }()
		return encoder.Encode(document)
	}
	return nil
}

// organisationDomainBlockedMessage turns the backend's structured refusal
// into an actionable error: the refusal text, every blocking cluster zone and
// DNS record, and the command that clears each kind.
func organisationDomainBlockedMessage(blocked *client.OrganisationDomainBlockedError) string {
	message := blocked.Detail
	if len(blocked.ClusterZones) > 0 {
		message += "\n\nCluster domains still under the current root:"
		for _, zone := range blocked.ClusterZones {
			clusterName := zone.ClusterName
			if clusterName == "" {
				clusterName = zone.ClusterID
			}
			message += fmt.Sprintf("\n  %s  %s  (%s)", clusterName, zone.FQDN, zone.State)
		}
		if blocked.ClusterZonesTruncated {
			message += "\n  ... and more; run 'ankra org dns zones' for the full list."
		}
		message += "\n  Remove each with: ankra cluster domain <cluster> --remove"
	}
	if len(blocked.DnsRecords) > 0 {
		message += "\n\nDNS records still under the current root:"
		for _, record := range blocked.DnsRecords {
			message += fmt.Sprintf("\n  %s  %s  (%s)", record.RecordType, record.Name, record.State)
		}
		if blocked.DnsRecordsTruncated {
			message += "\n  ... and more; run 'ankra org dns list' for the full list."
		}
		message += "\n  Remove each with: ankra org dns delete <record>"
	}
	return message
}

func renderOrganisationDomain(cmd *cobra.Command, domain *client.OrganisationDomain, format outputFormat) error {
	switch format {
	case outputJSON:
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(domain)
	case outputYAML:
		encoder := yaml.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent(2)
		defer func() { _ = encoder.Close() }()
		return encoder.Encode(domain)
	}
	if domain.DNSRootDomain == "" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Domain:  %s (platform default)\nDefault: %s\n",
			domain.DNSRootDomainDefault, domain.DNSRootDomainDefault)
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Domain:  %s\nDefault: %s\n",
		domain.DNSRootDomain, domain.DNSRootDomainDefault)
	return nil
}

func init() {
	registerStructuredOutputFlags(orgDomainGetCmd, orgDomainSetCmd)
	orgDomainSetCmd.Flags().BoolVar(&orgDomainUseDefault, "default", false,
		"Clear the custom domain and follow the platform default")
	orgDomainCmd.AddCommand(orgDomainGetCmd)
	orgDomainCmd.AddCommand(orgDomainSetCmd)
	orgCmd.AddCommand(orgDomainCmd)
}
