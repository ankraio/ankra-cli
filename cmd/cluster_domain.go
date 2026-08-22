package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var clusterDomainCmd = &cobra.Command{
	Use:   "domain <cluster>",
	Short: "Show the cluster's generated public domain",
	Long: `Show the cluster's generated public domain. The domain nests under the
organisation's Ankra-managed root - ankra.cc by default, or the organisation's
own domain when one is registered (portal: AI > Settings > Workspaces, field
"Custom Ankra domain"; CLI: 'ankra org domain').

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
cluster zones still live under the old root). A later --enable re-creates it
under the organisation's current root.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if clusterDomainEnable && clusterDomainRemove {
			return withExitCode(exitUsage, errors.New("--enable and --remove are mutually exclusive"))
		}

		clusterID, err := resolveClusterID(args[0])
		if err != nil {
			return err
		}

		var result *client.ClusterDNSZoneResponse
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
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"\nThis cluster has no public domain. Run 'ankra cluster domain %s --enable' to create one.\n",
				args[0])
			return nil
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Domain: %s\n", result.FQDN)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "State:  %s\n", result.State)
		switch {
		case clusterDomainRemove:
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"\nThe zone is being torn down; hostnames under it stop resolving once the teardown completes.")
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
