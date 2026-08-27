package cmd

import (
	"fmt"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var orgCustomDNSCmd = &cobra.Command{
	Use:     "custom-dns-zones",
	Aliases: []string{"custom-dns"},
	Short:   "Zones every cluster in the organisation serves with your own credential",
	Long: `Declare, list, and withdraw the DNS zones every cluster in the organisation
serves with the organisation's own external-dns webhook credential -
alongside the generated domain Ankra serves itself.

A zone declared here behaves like the generated domain: every cluster in the
organisation, the ones that exist and the ones created later, gets its own
external-dns for it, rendered and reconciled by Ankra with a DNS credential
you supply ('ankra org dns credentials'). Each cluster's controller is pinned
to exactly the zone with its own record ownership, so it can never fight
Ankra's controller, another cluster's, or records you publish yourself.

'ankra cluster custom-dns-zones' declares the same thing for one cluster. A
cluster's own declaration of a zone wins over the organisation's on that
cluster, which is how one cluster serves the zone with a different
credential.

Withdrawing a zone removes the controllers Ankra rendered from every cluster
that inherited it; clusters that declared the zone themselves keep theirs,
and the zone's records are yours and are left untouched.`,
}

var orgCustomDNSListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the zones declared for every cluster in the organisation",
	RunE: func(cmd *cobra.Command, args []string) error {
		zones, listError := apiClient.ListOrganisationCustomDNSZones()
		if listError != nil {
			return fmt.Errorf("listing the organisation's custom dns zones: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, zones); rendered || renderError != nil {
			return renderError
		}
		if len(zones) == 0 {
			fmt.Println("No organisation-wide custom DNS zones declared. Clusters serve their generated domain and whatever 'ankra cluster custom-dns-zones' declares on them.")
			return nil
		}
		zoneTable := table.NewWriter()
		zoneTable.SetOutputMirror(os.Stdout)
		zoneTable.SetStyle(table.StyleRounded)
		zoneTable.AppendHeader(table.Row{"Zone", "Credential"})
		for _, zone := range zones {
			zoneTable.AppendRow(table.Row{zone.Zone, zone.CredentialName})
		}
		zoneTable.Render()
		return nil
	},
}

var (
	orgCustomDNSZone       string
	orgCustomDNSCredential string
)

var orgCustomDNSAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Declare a zone every cluster in the organisation serves",
	Long: `Declare a zone every cluster in the organisation serves, with the DNS
credential that can publish into it. Ankra renders an external-dns for the
zone on each cluster on the next reconciler pass, and on every cluster
created afterwards without further declaration.

Refused when the zone is not a domain name, when the organisation has no DNS
credential of that name, or when the zone overlaps the domain Ankra already
serves for the organisation - that would put a second controller on names
Ankra's own external-dns publishes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		declaration, addError := apiClient.AddOrganisationCustomDNSZone(orgCustomDNSZone, orgCustomDNSCredential)
		if addError != nil {
			return fmt.Errorf("declaring the organisation custom dns zone: %w", addError)
		}
		if rendered, renderError := renderStructured(cmd, declaration); rendered || renderError != nil {
			return renderError
		}
		fmt.Printf("Zone %s declared for every cluster in the organisation with credential %s.\n",
			declaration.Zone, declaration.CredentialName)
		fmt.Println("Ankra renders its external-dns on each cluster on the next reconciler pass (within ~2 minutes), and on every cluster created from now on.")
		return nil
	},
}

var orgCustomDNSRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Withdraw an organisation-wide zone",
	Long: `Withdraw an organisation-wide zone. Ankra removes the controllers it rendered
from every cluster that inherited the zone on the next reconciler pass;
clusters that declared the zone themselves keep theirs. The zone's records
are yours and are left untouched.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		removedZone, removeError := apiClient.RemoveOrganisationCustomDNSZone(orgCustomDNSZone)
		if removeError != nil {
			return fmt.Errorf("withdrawing the organisation custom dns zone: %w", removeError)
		}
		if rendered, renderError := renderStructured(cmd,
			map[string]string{"zone": removedZone}); rendered || renderError != nil {
			return renderError
		}
		fmt.Printf("Zone %s withdrawn from the organisation. Its controllers are removed from every inheriting cluster on the next reconciler pass; the zone's records are untouched.\n", removedZone)
		return nil
	},
}

func init() {
	orgCustomDNSAddCmd.Flags().StringVar(&orgCustomDNSZone, "zone", "", "Zone to declare for every cluster, e.g. example.com (required)")
	orgCustomDNSAddCmd.Flags().StringVar(&orgCustomDNSCredential, "credential", "", "Name of the organisation DNS credential that publishes into the zone (required)")
	_ = orgCustomDNSAddCmd.MarkFlagRequired("zone")
	_ = orgCustomDNSAddCmd.MarkFlagRequired("credential")

	orgCustomDNSRemoveCmd.Flags().StringVar(&orgCustomDNSZone, "zone", "", "Zone to withdraw (required)")
	_ = orgCustomDNSRemoveCmd.MarkFlagRequired("zone")

	registerStructuredOutputFlags(orgCustomDNSListCmd, orgCustomDNSAddCmd, orgCustomDNSRemoveCmd)

	orgCustomDNSCmd.AddCommand(orgCustomDNSListCmd)
	orgCustomDNSCmd.AddCommand(orgCustomDNSAddCmd)
	orgCustomDNSCmd.AddCommand(orgCustomDNSRemoveCmd)
	orgCmd.AddCommand(orgCustomDNSCmd)
}
