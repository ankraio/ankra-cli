package cmd

import (
	"fmt"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var clusterCustomDNSCmd = &cobra.Command{
	Use:     "custom-dns-zones",
	Aliases: []string{"custom-dns"},
	Short:   "Zones the cluster serves with your own external-dns credential",
	Long: `Declare, list, and withdraw the DNS zones a cluster serves with the
organisation's own external-dns webhook credential, alongside the generated
domain Ankra serves itself.

The external-dns Ankra manages publishes only under the cluster's generated
subdomain: its credential is scoped to that zone by the DNS provider, so
ingress hostnames on your own zones are dropped silently. Declaring a zone
here has Ankra render and reconcile a separate external-dns for it, using a
DNS credential you supply ('ankra org dns credentials'). Each controller is
pinned to exactly its zone with its own record ownership, so it can never
fight Ankra's controller or another cluster's.

To serve a zone from every cluster in the organisation - including clusters
created later - declare it once with 'ankra org custom-dns-zones add'
instead. 'list' shows those inherited zones with source 'organisation'; a
zone declared here on the cluster itself takes precedence over the
organisation's on this cluster, which is how one cluster serves a zone with
a different credential.

Withdrawing a zone removes the controller Ankra rendered; the zone's records
are yours and are left untouched.`,
}

var clusterCustomDNSListCmd = &cobra.Command{
	Use:   "list <cluster>",
	Short: "List the zones declared on a cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterID(args[0])
		if resolveError != nil {
			return resolveError
		}
		zones, listError := apiClient.ListClusterCustomDNSZones(clusterID)
		if listError != nil {
			return fmt.Errorf("listing custom dns zones: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, zones); rendered || renderError != nil {
			return renderError
		}
		if len(zones) == 0 {
			fmt.Println("No custom DNS zones declared. The cluster serves only its generated domain.")
			return nil
		}
		zoneTable := table.NewWriter()
		zoneTable.SetOutputMirror(os.Stdout)
		zoneTable.SetStyle(table.StyleRounded)
		zoneTable.AppendHeader(table.Row{"Zone", "Credential", "Source"})
		for _, zone := range zones {
			zoneTable.AppendRow(table.Row{zone.Zone, zone.CredentialName, customDNSZoneSourceLabel(zone.Source)})
		}
		zoneTable.Render()
		return nil
	},
}

// customDNSZoneSourceLabel names where a listed zone was declared. A platform
// that predates the organisation-wide lane sends no source; that is the
// cluster's own declaration, the only kind it could hold.
func customDNSZoneSourceLabel(source string) string {
	if source == "" {
		return "cluster"
	}
	return source
}

var (
	clusterCustomDNSZone       string
	clusterCustomDNSCredential string
)

var clusterCustomDNSAddCmd = &cobra.Command{
	Use:   "add <cluster>",
	Short: "Declare a zone the cluster also serves",
	Long: `Declare a zone the cluster also serves, with the DNS credential that can
publish into it. Ankra renders an external-dns for the zone on the next
reconciler pass, scoped to exactly that zone.

Refused when the zone is not a domain name, when the organisation has no DNS
credential of that name, or when the zone overlaps the generated domain Ankra
already serves for this cluster - that would put a second controller on names
Ankra's own external-dns publishes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterID(args[0])
		if resolveError != nil {
			return resolveError
		}
		binding, addError := apiClient.AddClusterCustomDNSZone(
			clusterID, clusterCustomDNSZone, clusterCustomDNSCredential)
		if addError != nil {
			return fmt.Errorf("declaring the custom dns zone: %w", addError)
		}
		if rendered, renderError := renderStructured(cmd, binding); rendered || renderError != nil {
			return renderError
		}
		fmt.Printf("Zone %s declared with credential %s.\n", binding.Zone, binding.CredentialName)
		fmt.Println("Ankra renders its external-dns on the next reconciler pass (within ~2 minutes).")
		return nil
	},
}

var clusterCustomDNSRemoveCmd = &cobra.Command{
	Use:   "remove <cluster>",
	Short: "Withdraw a declared zone",
	Long: `Withdraw a declared zone. Ankra removes the controller it rendered on the
next reconciler pass. The zone's records are yours and are left untouched.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterID(args[0])
		if resolveError != nil {
			return resolveError
		}
		removedZone, removeError := apiClient.RemoveClusterCustomDNSZone(clusterID, clusterCustomDNSZone)
		if removeError != nil {
			return fmt.Errorf("withdrawing the custom dns zone: %w", removeError)
		}
		if rendered, renderError := renderStructured(cmd,
			map[string]string{"zone": removedZone}); rendered || renderError != nil {
			return renderError
		}
		fmt.Printf("Zone %s withdrawn. Its controller is removed on the next reconciler pass; the zone's records are untouched.\n", removedZone)
		return nil
	},
}

func init() {
	clusterCustomDNSAddCmd.Flags().StringVar(&clusterCustomDNSZone, "zone", "", "Zone to declare, e.g. launch.example.com (required)")
	clusterCustomDNSAddCmd.Flags().StringVar(&clusterCustomDNSCredential, "credential", "", "Name of the organisation DNS credential that publishes into the zone (required)")
	_ = clusterCustomDNSAddCmd.MarkFlagRequired("zone")
	_ = clusterCustomDNSAddCmd.MarkFlagRequired("credential")

	clusterCustomDNSRemoveCmd.Flags().StringVar(&clusterCustomDNSZone, "zone", "", "Zone to withdraw (required)")
	_ = clusterCustomDNSRemoveCmd.MarkFlagRequired("zone")

	registerStructuredOutputFlags(clusterCustomDNSListCmd, clusterCustomDNSAddCmd, clusterCustomDNSRemoveCmd)

	clusterCustomDNSCmd.AddCommand(clusterCustomDNSListCmd)
	clusterCustomDNSCmd.AddCommand(clusterCustomDNSAddCmd)
	clusterCustomDNSCmd.AddCommand(clusterCustomDNSRemoveCmd)
	clusterCmd.AddCommand(clusterCustomDNSCmd)
}
