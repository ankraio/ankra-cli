package cmd

import (
	"fmt"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/dustin/go-humanize"
)

// addonSecuritySummary is the Security column of `cluster addons list`:
// the worst advisory and the chart version that fixes it, "unknown" when
// the platform could not judge the addon (an empty list then is not a
// clean answer), "ok" for a judged addon with nothing to fix, and "-" when
// no advisory source is tracked for the chart or the platform predates
// the field.
//
// When the advisory feed behind the listing is stale, a clean answer reads
// "ok (stale)": it only says no advisory was known when the feed was last
// read.
func addonSecuritySummary(addon client.ClusterAddonListItem, feed addonAdvisoryFeed) string {
	if len(addon.SecurityAdvisories) > 0 {
		worst := addon.SecurityAdvisories[0]
		summary := fmt.Sprintf("update: %s (%s)", worst.AdvisoryID, worst.Severity)
		if more := len(addon.SecurityAdvisories) - 1; more > 0 {
			summary += fmt.Sprintf(" +%d", more)
		}
		if addon.SecurityUpgradeChartVersion != nil && *addon.SecurityUpgradeChartVersion != "" {
			summary += " -> " + *addon.SecurityUpgradeChartVersion
		}
		return summary
	}
	switch addon.SecurityAdvisoryStatus {
	case client.SecurityAdvisoryStatusChecked:
		if feed.stale {
			return "ok (stale)"
		}
		return "ok"
	case client.SecurityAdvisoryStatusUnknown:
		return "unknown"
	default:
		return "-"
	}
}

// countAddonsWithSecurityUpdates counts the addons with at least one
// advisory.
func countAddonsWithSecurityUpdates(addons []client.ClusterAddonListItem) int {
	count := 0
	for _, addon := range addons {
		if len(addon.SecurityAdvisories) > 0 {
			count++
		}
	}
	return count
}

// printAddonSecurityDetails prints the security section of a single addon's
// details: each advisory with its severity, summary, link and fixed
// release, and the upgrade command that applies the fix. The command names
// clusterName, so pasting it upgrades the cluster that was listed even when
// it was reached with --cluster rather than 'cluster select'. Ankra never
// runs that upgrade for the owner.
func printAddonSecurityDetails(addon client.ClusterAddonListItem, clusterName string, feed addonAdvisoryFeed) {
	switch {
	case len(addon.SecurityAdvisories) > 0:
		fmt.Println()
		fmt.Println("Security update available:")
		for _, advisory := range addon.SecurityAdvisories {
			fmt.Printf("  %s (%s)\n", advisory.AdvisoryID, advisory.Severity)
			if summary := strings.TrimSpace(advisory.Summary); summary != "" {
				fmt.Printf("    %s\n", summary)
			}
			if advisory.AffectedVersion != "" {
				fmt.Printf("    Running:   %s\n", advisory.AffectedVersion)
			}
			if advisory.FixedVersion != nil && *advisory.FixedVersion != "" {
				fmt.Printf("    Fixed in:  %s\n", *advisory.FixedVersion)
			}
			if advisory.URL != "" {
				fmt.Printf("    Details:   %s\n", advisory.URL)
			}
		}
		if addon.SecurityUpgradeChartVersion != nil && *addon.SecurityUpgradeChartVersion != "" {
			fmt.Printf("\n  To upgrade: ankra cluster addons upgrade %s --chart-version %s --cluster %s\n",
				addon.Name, *addon.SecurityUpgradeChartVersion, clusterName)
		}
	case addon.SecurityAdvisoryStatus == client.SecurityAdvisoryStatusChecked:
		fmt.Println("  Security:        no published advisory with a fix covers this version")
	case addon.SecurityAdvisoryStatus == client.SecurityAdvisoryStatusUnknown:
		fmt.Println("  Security:        not checked - the platform could not read this addon's version or advisory data")
	}
	if addon.SecurityAdvisoryStatus == client.SecurityAdvisoryStatusChecked && feed.stale {
		fmt.Printf("  Advisory data:   stale (%s) - a newer advisory may be missing\n", feed.age())
	}
}

// addonAdvisoryFeed is the advisory feed state the platform reported with
// the addon listing.
type addonAdvisoryFeed struct {
	stale     bool
	checkedAt *time.Time
}

func newAddonAdvisoryFeed(listing client.ClusterAddonListing) addonAdvisoryFeed {
	return addonAdvisoryFeed{stale: listing.SecurityAdvisoriesStale, checkedAt: listing.SecurityAdvisoriesCheckedAt}
}

// qualifies reports whether the stale feed changes the meaning of any
// listed answer: only a "checked" addon was judged from the feed.
func (feed addonAdvisoryFeed) qualifies(addons []client.ClusterAddonListItem) bool {
	if !feed.stale {
		return false
	}
	for _, addon := range addons {
		if addon.SecurityAdvisoryStatus == client.SecurityAdvisoryStatusChecked {
			return true
		}
	}
	return false
}

// age says when the feed was last read.
func (feed addonAdvisoryFeed) age() string {
	if feed.checkedAt == nil {
		return "never read successfully"
	}
	return "last read " + humanize.Time(*feed.checkedAt)
}

// note is the listing footer for a stale feed.
func (feed addonAdvisoryFeed) note() string {
	return "Security advisory data is stale (" + feed.age() +
		`): "ok" means no advisory was known when it was read, not that none exists now.`
}
