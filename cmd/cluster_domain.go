package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var clusterDomainCmd = &cobra.Command{
	Use:   "domain [cluster]",
	Short: "Show the cluster's generated public domain",
	Long: `Show the cluster's generated public domain. The domain nests under the
organisation's Ankra-managed root - ankra.cc by default, or the organisation's
own domain when one is registered (portal: AI > Settings > Workspaces, field
"Custom Ankra domain"; CLI: 'ankra org domain').

The read also reports the cluster's PUBLIC domain: the domain hostnames on the
cluster are published under, and what ${{ ankra.cluster_domain }} resolves to
in stacks and stack profiles. It is the organisation's preview domain
('ankra org ai-environment set --demo-base-domain') whenever a custom DNS zone
declared for the organisation or the cluster ('ankra org custom-dns-zones',
'ankra cluster custom-dns-zones') covers it - the cluster's own external-dns
then publishes every hostname under it - and the generated domain otherwise.
An organisation whose domain cannot be registered as the Ankra root (it lives
in its own DNS account) gets its own domain as the cluster domain this way.

The plain command is a read: it never creates a zone. A cluster that has no
domain reports state "none", and 'ankra org dns zones' lists every cluster in
the organisation that does have one.

--enable queues the zone for a cluster that has none. It is idempotent: a
cluster that already has a zone reports its existing domain unchanged. A fresh
zone reads "pending" until it is published to the authoritative nameservers
and then turns "active"; external-dns is wired to it on the next
cloud-provider pass, after which any ingress hostname under the domain
resolves with TLS.

--remove hands the zone back for teardown: the removal step before an
organisation switches its Ankra root domain (the switch is refused while
cluster zones still live under the old root). The removal is then HELD -
nothing re-creates the zone, including the external-dns Ankra runs on the
cluster and the discovery that mints zones for clusters that lack one - until
you ask for it back. A read reports "opted out" for as long as the hold
stands.

--enable withdraws the hold and re-creates the zone under the organisation's
current root. The cluster's label is derived from its id, so the zone comes
back under exactly the name it had before, and the external-dns Ankra manages
for it keeps the same --txt-owner-id.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if clusterDomainEnable && clusterDomainRemove {
			return withExitCode(exitUsage, errors.New("--enable and --remove are mutually exclusive"))
		}

		// Without a positional argument the command targets the selected
		// cluster, like its sibling cluster commands. The fallback names the
		// cluster it resolved on stderr, because --enable/--remove mutate and
		// a silently selected target must not be silent.
		var clusterID string
		var clusterReference string
		if len(args) == 1 {
			clusterReference = args[0]
			resolvedClusterID, err := resolveClusterID(clusterReference)
			if err != nil {
				return err
			}
			clusterID = resolvedClusterID
		} else {
			cluster, err := resolveActiveCluster(cmd)
			if err != nil {
				return err
			}
			clusterID = cluster.ID
			clusterReference = cluster.Name
			if clusterReference == "" {
				clusterReference = cluster.ID
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Cluster: %s\n", clusterReference)
		}

		var result *client.ClusterDNSZoneResponse
		var err error
		switch {
		case clusterDomainRemove:
			result, err = apiClient.DisableClusterDNSZone(clusterID)
			if err != nil {
				return fmt.Errorf("removing cluster dns zone: %w", err)
			}
		case clusterDomainEnable:
			result, err = apiClient.EnableClusterDNSZone(clusterID)
			if err != nil {
				return fmt.Errorf("enabling cluster dns zone: %w", err)
			}
		default:
			result, err = apiClient.GetClusterDNSZone(clusterID)
			if err != nil {
				return fmt.Errorf("reading cluster dns zone: %w", err)
			}
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		// Only the bare lookup can meaningfully report "none": --enable
		// always returns a queued zone and --remove answers 404 when there
		// was nothing to remove. Guarding the branch keeps it that way, so a
		// backend that ever reported "none" from either verb could not make
		// the CLI answer a removal with an "run --enable" hint.
		if result.State == clusterDomainStateNone && !clusterDomainEnable && !clusterDomainRemove {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Domain: (none)\nState:  none\n")
			printClusterPublicDomain(cmd, result)
			if result.OptedOut {
				// A removal that reports "none" is the exact moment PLA-771
				// was watched from. Saying only "run --enable to create one"
				// here reads as "nothing happened", which is what sent the
				// reporter back to watch for the zone returning.
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Opted out: yes\n")
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\nThis cluster's domain was removed and the removal is held: nothing will "+
						"re-create it.\nRun 'ankra cluster domain %s --enable' when you want it back.\n",
					clusterReference)
				return nil
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"\nThis cluster has no public domain. Run 'ankra cluster domain %s --enable' to create one.\n",
				clusterReference)
			return nil
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Domain: %s\n", result.FQDN)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "State:  %s\n", result.State)
		if result.OptedOut {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Opted out: yes\n")
		}
		printClusterPublicDomain(cmd, result)
		switch {
		case clusterDomainRemove:
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"\nThe zone is being torn down; hostnames under it stop resolving once the teardown completes."+
					"\nThe removal is held once it finishes - nothing re-creates the zone until "+
					"'ankra cluster domain <cluster> --enable'.")
		case clusterDomainEnable && result.State != "active":
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"\nThe zone publishes shortly; once active, any hostname under the domain is yours the moment an ingress claims it.")
		}
		return nil
	},
}

// clusterDomainStateNone is the state the read surface reports for a cluster
// that holds no DNS zone at all.
const clusterDomainStateNone = "none"

// clusterPublicDomainSourcePreviewDomain is the public_domain_source a backend
// reports when the cluster domain is the organisation's preview domain,
// published by a custom DNS zone the cluster serves.
const clusterPublicDomainSourcePreviewDomain = "preview_domain"

// printClusterPublicDomain reports the domain hostnames on the cluster are
// actually published under when the backend resolved one that is not simply
// the generated zone - the organisation's own preview domain, which is what
// "the cluster domain" means to an organisation that configured one. A
// backend too old to report it, or a public domain equal to the generated
// zone, prints nothing extra.
func printClusterPublicDomain(cmd *cobra.Command, result *client.ClusterDNSZoneResponse) {
	if result.PublicDomain == "" || result.PublicDomain == result.FQDN {
		return
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Public domain: %s\n", result.PublicDomain)
	if result.PublicDomainSource == clusterPublicDomainSourcePreviewDomain {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"\nThe cluster domain (${{ ankra.cluster_domain }}) is the organisation's preview domain %s: "+
				"a custom DNS zone this cluster serves (%s) publishes every hostname under it.\n",
			result.PublicDomain, result.PublicDomainPublishedZone)
	}
}

var (
	clusterDomainEnable bool
	clusterDomainRemove bool
)

func init() {
	clusterCmd.AddCommand(clusterDomainCmd)
	registerStructuredOutputFlags(clusterDomainCmd)
	clusterDomainCmd.Flags().BoolVar(&clusterDomainEnable, "enable", false,
		"Create the cluster's generated public domain if it does not have one (idempotent)")
	clusterDomainCmd.Flags().BoolVar(&clusterDomainRemove, "remove", false,
		"Remove the cluster's generated public domain (tear the zone down)")
}
