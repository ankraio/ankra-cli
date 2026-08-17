package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var clusterDomainCmd = &cobra.Command{
	Use:   "domain <cluster>",
	Short: "Show the cluster's generated public domain, enabling it if needed",
	Long: `Show the cluster's generated public domain, queueing the zone if the
cluster does not have one yet. The domain nests under the organisation's
Ankra-managed root (ankra.cc by default; organisations may select another
offered root, such as smartoptics.dev, in the organisation's AI environment
settings).

The call is idempotent: a cluster that already has a zone reports its
existing domain unchanged, so this doubles as a lookup. A fresh zone reads
"pending" until it is published to the authoritative nameservers and then
turns "active"; external-dns is wired to it on the next cloud-provider pass,
after which any ingress hostname under the domain resolves with TLS.

--remove hands the zone back for teardown instead: the removal step before
an organisation switches its Ankra root domain (the switch is refused while
cluster zones still live under the old root). A later call without --remove
re-enables it under the organisation's current root.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := resolveClusterID(args[0])
		if err != nil {
			return err
		}

		var result *client.ClusterDNSZoneResponse
		if clusterDomainRemove {
			result, err = apiClient.DisableClusterDNSZone(clusterID)
			if err != nil {
				return fmt.Errorf("removing cluster dns zone: %w", err)
			}
		} else {
			result, err = apiClient.EnableClusterDNSZone(clusterID)
			if err != nil {
				return fmt.Errorf("enabling cluster dns zone: %w", err)
			}
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Domain: %s\n", result.FQDN)
		fmt.Printf("State:  %s\n", result.State)
		switch {
		case clusterDomainRemove:
			fmt.Println("\nThe zone is being torn down; hostnames under it stop resolving once the teardown completes.")
		case result.State != "active":
			fmt.Println("\nThe zone publishes shortly; once active, any hostname under the domain is yours the moment an ingress claims it.")
		}
		return nil
	},
}

var clusterDomainRemove bool

func init() {
	clusterCmd.AddCommand(clusterDomainCmd)
	registerStructuredOutputFlags(clusterDomainCmd)
	clusterDomainCmd.Flags().BoolVar(&clusterDomainRemove, "remove", false,
		"Remove the cluster's generated public domain (tear the zone down) instead of enabling it")
}
