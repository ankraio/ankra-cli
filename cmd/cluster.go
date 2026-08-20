package cmd

import (
	"fmt"
	"regexp"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

// formatTimeAgo converts an RFC3339 timestamp string to a human-readable relative time
func formatTimeAgo(tStr string) string {
	t, err := time.Parse(time.RFC3339, tStr)
	if err != nil {
		return tStr
	}
	return humanize.Time(t)
}

// formatOptionalTimeAgo renders a nullable timestamp as a relative time,
// with "-" for absent values.
func formatOptionalTimeAgo(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return humanize.Time(*t)
}

var clusterIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isLikelyClusterID reports whether value has the shape of a cluster UUID. It
// is used to decide whether an unresolved --cluster value can be forwarded to
// the API as an ID, or should be rejected as an unknown name rather than
// producing an opaque server-side UUID-parsing error.
func isLikelyClusterID(value string) bool {
	return clusterIDPattern.MatchString(value)
}

// registerIncludeDNSFlag adds --include-dns to a provider create command. The
// server defaults include_dns to true on every provider lane, so the flag
// exists to opt out; the delegated subzone is what makes an ingress hostname
// resolve and get a certificate without bringing your own domain. external-dns
// itself ships inside the networking stack, so opting out of that stack leaves
// the zone delegated but unused.
func registerIncludeDNSFlag(cmds ...*cobra.Command) {
	for _, cmd := range cmds {
		cmd.Flags().Bool("include-dns", true, "Give the cluster its own subdomain under ankra.cc and install external-dns, so an ingress hostname gets its DNS record and TLS certificate with no manual setup (default on; pass --include-dns=false to skip)")
	}
}

// resolveCloudProviderNetworking reconciles the --external-cloud-provider and
// --include-networking flags. Ingress networking (the Traefik LoadBalancer) is
// provisioned by the cloud controller manager, so --include-networking requires
// --external-cloud-provider. When the cloud provider is disabled, networking is
// disabled with it; explicitly asking for both at once is a contradiction.
func resolveCloudProviderNetworking(cmd *cobra.Command) (externalCloudProvider bool, includeNetworking bool, err error) {
	externalCloudProvider, _ = cmd.Flags().GetBool("external-cloud-provider")
	includeNetworking, _ = cmd.Flags().GetBool("include-networking")
	if !externalCloudProvider {
		if cmd.Flags().Changed("include-networking") && includeNetworking {
			return false, false, fmt.Errorf("--include-networking requires --external-cloud-provider (the ingress LoadBalancer is provisioned by the cloud controller manager); drop --external-cloud-provider=false or pass --include-networking=false")
		}
		includeNetworking = false
	}
	return externalCloudProvider, includeNetworking, nil
}
