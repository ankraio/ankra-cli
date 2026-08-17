package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var clusterDomainCmd = &cobra.Command{
	Use:   "domain <cluster>",
	Short: "Show the cluster's generated public domain, enabling it if needed",
	Long: `Show the cluster's generated public domain on ankra.cc, queueing the zone
if the cluster does not have one yet.

The call is idempotent: a cluster that already has a zone reports its
existing domain unchanged, so this doubles as a lookup. A fresh zone reads
"pending" until it is published to the authoritative nameservers and then
turns "active"; external-dns is wired to it on the next cloud-provider pass,
after which any ingress hostname under the domain resolves with TLS.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := resolveClusterID(args[0])
		if err != nil {
			return err
		}

		result, err := apiClient.EnableClusterDNSZone(clusterID)
		if err != nil {
			return fmt.Errorf("enabling cluster dns zone: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Domain: %s\n", result.FQDN)
		fmt.Printf("State:  %s\n", result.State)
		if result.State != "active" {
			fmt.Println("\nThe zone publishes shortly; once active, any hostname under the domain is yours the moment an ingress claims it.")
		}
		return nil
	},
}

func init() {
	clusterCmd.AddCommand(clusterDomainCmd)
	registerStructuredOutputFlags(clusterDomainCmd)
}
