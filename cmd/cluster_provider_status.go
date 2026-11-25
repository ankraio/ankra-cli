package cmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var clusterProviderStatusCmd = withClusterArgument(clusterSelectionAllowed, &cobra.Command{
	Use:   "provider-status [cluster]",
	Short: "Show the open incidents the cluster's cloud provider reports where the cluster runs",
	Long: `Show the open incidents the cluster's cloud provider lists on its own public
status page for the cluster's region and the zones its machines are in. When a
cluster loses its agent or its nodes, this answers whether the provider itself
reports an outage there.

An incident matches on a zone when it names a zone one of the cluster's
machines is in, and on the region when it names the cluster's region. Ankra
never guesses a zone: when a machine has no recorded zone, an incident in a
zone of the cluster's region matches on the region and says the zone is not
known. An incident the page scopes to the whole provider is listed as
provider-wide.

When the status page cannot be read, does not list the cluster's region and
zones, or Ankra has none recorded for the cluster, the answer says so: an
empty list then means the provider's status could not be checked, not that it
reports nothing. Status pages are read for
OVHcloud, UpCloud, DigitalOcean and Scaleway; for other providers the read
says there is no status page Ankra reads, and for a managed Kubernetes
cluster that Ankra does not read where its nodes run yet. The platform caches
each page for a few minutes.

Left out, the cluster is --cluster or the one 'ankra cluster select' saved.
Needs clusters.read.`,
	Example: `  ankra cluster provider-status prod-multi-az
  ankra cluster provider-status --cluster prod-multi-az -o json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		typed, clusterID, resolveError := resolveOptionalClusterArg(cmd, args)
		if resolveError != nil {
			return resolveError
		}
		result, readError := apiClient.GetClusterProviderIncidents(clusterID)
		if readError != nil {
			return fmt.Errorf("reading the provider status of %s: %w", typed, readError)
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			return renderError
		}
		printProviderStatus(cmd.OutOrStdout(), typed, result)
		return nil
	},
})

// providerStatusTitleWidth caps the incident title column so a long title
// does not push the link off a terminal line.
const providerStatusTitleWidth = 72

// providerStatusMatchProviderWide is the match of an incident the status page
// scopes to the whole provider rather than to a region or zone.
const providerStatusMatchProviderWide = "provider_wide"

// providerFeedReasonLocationNotListed is the reason of an answer whose status
// pages were read but cannot be placed: they list none of the cluster's
// regions and zones, or the platform has none recorded for it.
const providerFeedReasonLocationNotListed = "location_not_listed"

// printProviderStatus writes the human-readable provider status of one
// cluster: what was read, where the cluster runs, and the matching incidents.
// An unknown feed is said in words so an empty list never reads as "nothing
// reported".
func printProviderStatus(output io.Writer, clusterReference string, result *client.ProviderIncidentsResponse) {
	providerName := result.ProviderName
	if providerName == "" {
		providerName = result.Provider
	}
	if providerName == "" {
		providerName = "unknown"
	}
	_, _ = fmt.Fprintf(output, "Cluster:   %s\n", clusterReference)
	_, _ = fmt.Fprintf(output, "Provider:  %s\n", providerName)
	_, _ = fmt.Fprintf(output, "Regions:   %s\n", joinedOrNone(result.Regions))
	_, _ = fmt.Fprintf(output, "Zones:     %s\n", joinedOrNone(result.Zones))
	if result.MachinesWithoutRecordedZone > 0 {
		_, _ = fmt.Fprintf(output, "Machines:  %d (%d without a recorded zone)\n",
			result.Machines, result.MachinesWithoutRecordedZone)
	} else {
		_, _ = fmt.Fprintf(output, "Machines:  %d\n", result.Machines)
	}
	_, _ = fmt.Fprintf(output, "Status page: %s\n", providerFeedStateText(result))
	printUnreadProviderStatusPages(output, result)

	if len(result.Incidents) == 0 {
		if result.FeedState == client.ProviderFeedStateOK {
			_, _ = fmt.Fprintf(output, "\nNo open incident on %s's status page matches this cluster (%d open in total).\n",
				providerName, result.OpenIncidentsOnPages)
		} else {
			_, _ = fmt.Fprintln(output,
				"\nThe provider's status could not be checked. This does not mean the provider reports nothing.")
		}
		return
	}

	_, _ = fmt.Fprintln(output)
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "MATCH\tWHERE\tSINCE (UTC)\tSTATUS\tTITLE")
	for _, incident := range result.Incidents {
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			providerIncidentMatchText(incident),
			providerIncidentLocationText(incident),
			providerIncidentSinceText(incident.StartedAt),
			truncateCell(incident.Status, 20),
			truncateCell(incident.Title, providerStatusTitleWidth))
	}
	_ = writer.Flush()

	_, _ = fmt.Fprintln(output, "\nLinks:")
	for _, incident := range result.Incidents {
		if incident.Link != "" {
			_, _ = fmt.Fprintf(output, "  %s\n", incident.Link)
		}
	}
	if result.Summary != "" {
		_, _ = fmt.Fprintf(output, "\n%s\n", result.Summary)
	}
	if result.FeedState != client.ProviderFeedStateOK {
		if result.FeedReason == providerFeedReasonLocationNotListed {
			_, _ = fmt.Fprintln(output,
				"Ankra cannot tell whether more incidents are open where this cluster runs.")
			return
		}
		_, _ = fmt.Fprintln(output,
			"Not every status page of the provider could be read, so more incidents may be open.")
	}
}

// printUnreadProviderStatusPages lists each status page with its own state
// when the provider has several and one of them could not be read, so the
// output says which page failed and why.
func printUnreadProviderStatusPages(output io.Writer, result *client.ProviderIncidentsResponse) {
	if len(result.Feeds) < 2 {
		return
	}
	isAnyUnread := false
	for _, feed := range result.Feeds {
		if feed.State != client.ProviderFeedStateOK {
			isAnyUnread = true
		}
	}
	if !isAnyUnread {
		return
	}
	for _, feed := range result.Feeds {
		_, _ = fmt.Fprintf(output, "  %s: %s\n", feed.Page, providerStatusPageText(feed))
	}
}

// providerStatusPageText says whether one status page was read, and when.
func providerStatusPageText(feed client.ProviderStatusFeed) string {
	if feed.State == client.ProviderFeedStateOK {
		if feed.FetchedAt == nil || feed.FetchedAt.IsZero() {
			return "read"
		}
		return "read at " + feed.FetchedAt.UTC().Format("2006-01-02 15:04") + " UTC"
	}
	switch feed.Reason {
	case "unreachable":
		return "could not be read (unreachable)"
	case "timeout":
		return "could not be read (timed out)"
	case "":
		return "could not be read"
	}
	return "could not be read (" + feed.Reason + ")"
}

// providerFeedStateText says whether the provider's status pages were read
// and, when they were not, why.
func providerFeedStateText(result *client.ProviderIncidentsResponse) string {
	if result.FeedState == client.ProviderFeedStateOK {
		return "read"
	}
	switch result.FeedReason {
	case "no_feed":
		return "none read: Ankra reads no status page for this provider"
	case "provider_not_known":
		return "none read: the cluster's provider is not known"
	case "managed_kubernetes":
		return "none read: Ankra does not read where a managed Kubernetes cluster's nodes run yet"
	case providerFeedReasonLocationNotListed:
		if len(result.Regions)+len(result.Zones) == 0 {
			return "read, but Ankra has no region or zone recorded for this cluster"
		}
		return "read, but it does not list the cluster's region or zones"
	case "unreachable":
		return "could not be read (unreachable)"
	case "timeout":
		return "could not be read (timed out)"
	case "":
		return "could not be read"
	}
	return "could not be read (" + result.FeedReason + ")"
}

func providerIncidentMatchText(incident client.ProviderIncidentMatch) string {
	if incident.Match == providerStatusMatchProviderWide {
		return "provider-wide"
	}
	return incident.Match
}

func providerIncidentLocationText(incident client.ProviderIncidentMatch) string {
	location := incident.MatchedLocation
	if location == "" {
		location = "-"
	}
	if incident.ZoneUnknown {
		return location + " (zone not recorded)"
	}
	return location
}

func providerIncidentSinceText(startedAt *time.Time) string {
	if startedAt == nil || startedAt.IsZero() {
		return "-"
	}
	return startedAt.UTC().Format("2006-01-02 15:04")
}

func joinedOrNone(values []string) string {
	if len(values) == 0 {
		return "(none recorded)"
	}
	return strings.Join(values, ", ")
}

func init() {
	clusterCmd.AddCommand(clusterProviderStatusCmd)
	registerStructuredOutputFlags(clusterProviderStatusCmd)
}
