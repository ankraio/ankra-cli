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
("Custom Ankra domain").

The SECOND domain field on that screen, "Preview domain", is a different
setting: it decides only where PR demos and on-demand previews are published,
it changes nothing else, and it is not gated by the guard below. If previews
on your own domain are what you are after, you want
'ankra org ai-environment set --demo-base-domain', not this command.

Changing the root domain is refused while cluster DNS zones or DNS records
still live under the old root; the refusal lists exactly what to remove. Use
'ankra org dns zones' and 'ankra org dns list' to inventory them, and
'ankra cluster domain <cluster> --remove' and 'ankra org dns delete <record>'
to clear them. Ankra re-creates the organisation zone under the new domain
automatically once the switch is accepted.

WHAT ANKRA TOUCHES IN YOUR DOMAIN

Ankra creates one subzone in the domain you register - <org_short_id>.<domain>
- and works only inside it. It does not read, write or delete records anywhere
else in the domain. Records you already publish at the apex or under any other
name are untouched by registering the domain, by the switch itself, and by
everything Ankra does afterwards. A domain that already serves your production
hostnames is safe to register on that count.

The delegation is strict below that too: each cluster gets its own subzone
under the organisation zone, and the external-dns Ankra installs on a cluster
holds a token pinned to that cluster's subzone alone.

WHAT A SWITCH DOES NOT CHANGE

Zone labels are derived from the organisation and cluster ids, so they are the
same under any root. A cluster keeps its label across a switch, and with it
the --txt-owner-id of the external-dns Ankra manages for it and any GitOps
path built from it. A switch never re-stamps an owner id and never leaves an
external-dns pointed at a TXT registry it no longer matches.

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
nameservers.

Existing records in that domain are preserved. Ankra creates one subzone,
<org_short_id>.<domain>, and confines itself to it - nothing at the apex or
under any other name is read, written or deleted, by this command or by
anything Ankra runs afterwards.

The switch is refused while cluster DNS zones or DNS records still live under
the old root; the refusal lists them, and says which of them you clear
yourself and which are published by something you have to remove first:

  Cluster domains  ankra cluster domain <cluster> --remove
                   Each such cluster runs Ankra's external-dns against its own
                   zone. The removal is held once it completes, so external-dns
                   cannot re-create the zone while you finish the switch.
                   Restore it afterwards with --enable, which re-mints the zone
                   under the new root and keeps the cluster's label and
                   external-dns --txt-owner-id exactly as they were.
  Your DNS records ankra org dns delete <record>
  Ankra's records  These are reconciled, so deleting one lasts until the lane
                   that owns it runs again. The refusal names what publishes
                   them - today, a playground, cleared with
                   'ankra cluster playground destroy <cluster_id>'.

Clearing every blocker by hand is the current sequence. See
'ankra org domain --help' for what a switch does and does not change.`,
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
	BlockingPlaygrounds           []client.OrganisationDomainBlockingPlayground  `json:"blocking_playgrounds" yaml:"blocking_playgrounds"`
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
		BlockingPlaygrounds:           blocked.Playgrounds,
	}
	if document.BlockingClusterZones == nil {
		document.BlockingClusterZones = []client.OrganisationDomainBlockingClusterZone{}
	}
	if document.BlockingDnsRecords == nil {
		document.BlockingDnsRecords = []client.OrganisationDomainBlockingDnsRecord{}
	}
	if document.BlockingPlaygrounds == nil {
		document.BlockingPlaygrounds = []client.OrganisationDomainBlockingPlayground{}
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
//
// Records are split by who wrote them. A record an admin created is cleared
// with 'ankra org dns delete'; a record a platform lane owns is reconciled,
// so deleting it only lasts until that lane's next pass and the environment
// behind it has to go instead. Printing one instruction for both kinds is
// what sent the reporter of PLA-771 round the same loop repeatedly.
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
		message += "\n  Each of these clusters runs Ankra's external-dns against its own zone."
		message += "\n  The removal is held once it completes, so external-dns cannot bring the"
		message += "\n  zone back; 'ankra cluster domain <cluster> --enable' is what restores it."
		message += "\n  Re-enabling keeps the zone's label, and with it external-dns's"
		message += "\n  --txt-owner-id and your GitOps paths - a root switch never re-labels."
	}
	adminRecords, platformRecords := partitionBlockingDnsRecords(blocked.DnsRecords)
	if len(adminRecords) > 0 {
		message += "\n\nDNS records still under the current root:"
		for _, record := range adminRecords {
			message += fmt.Sprintf("\n  %s  %s  (%s)", record.RecordType, record.Name, record.State)
		}
		if blocked.DnsRecordsTruncated {
			message += "\n  ... and more; run 'ankra org dns list' for the full list."
		}
		message += "\n  Remove each with: ankra org dns delete <record>"
	}
	if len(platformRecords) > 0 {
		message += "\n\nDNS records Ankra owns and re-creates:"
		for _, record := range platformRecords {
			message += fmt.Sprintf("\n  %s  %s  (created by %s)", record.RecordType, record.Name, record.CreatedBy)
		}
		if len(adminRecords) == 0 && blocked.DnsRecordsTruncated {
			message += "\n  ... and more; run 'ankra org dns list' for the full list."
		}
		message += "\n  Deleting these does not clear them - the lane that owns each one writes"
		message += "\n  it again on its next pass. Remove what publishes them instead."
	}
	if len(blocked.Playgrounds) > 0 {
		message += "\n\nPlayground environments publishing those records:"
		for _, playground := range blocked.Playgrounds {
			clusterName := playground.ClusterName
			if clusterName == "" {
				clusterName = playground.ClusterID
			}
			message += fmt.Sprintf("\n  %s  %s  (%s)", clusterName, playground.ClusterID, playground.Phase)
		}
		message += "\n  Destroy each with: ankra cluster playground destroy <cluster_id>"
		message += "\n  The switch cannot be attempted while a playground exists."
	}
	return message
}

// partitionBlockingDnsRecords splits the refusal's records into the ones an
// admin can delete and the ones a platform lane re-asserts. A record with no
// created_by - an older backend that does not send the member - is treated as
// the admin's, which keeps the advice for existing records exactly what it
// was before this split existed.
func partitionBlockingDnsRecords(records []client.OrganisationDomainBlockingDnsRecord) (
	adminRecords []client.OrganisationDomainBlockingDnsRecord,
	platformRecords []client.OrganisationDomainBlockingDnsRecord) {
	for _, record := range records {
		if platformOwnedDnsRecordWriters[record.CreatedBy] {
			platformRecords = append(platformRecords, record)
			continue
		}
		adminRecords = append(adminRecords, record)
	}
	return adminRecords, platformRecords
}

// platformOwnedDnsRecordWriters are the created_by values the platform's own
// lanes stamp on records they reconcile. Everything else is a record some
// human asked for, whatever wrote it on their behalf.
var platformOwnedDnsRecordWriters = map[string]bool{
	"playground_provisioner": true,
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
