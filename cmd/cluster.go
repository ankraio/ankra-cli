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
//
// The help text has to name the boundary. The cluster's DNS credential is
// pinned to the delegated subzone, so external-dns manages hostnames under it
// and silently ignores every other ingress host - it logs "All records are
// already up to date" and publishes nothing. A user who reads "an ingress
// hostname" as "any ingress hostname" only finds out when their own zone never
// resolves and HTTP-01 issuance stalls behind it.
func registerIncludeDNSFlag(cmds ...*cobra.Command) {
	for _, cmd := range cmds {
		cmd.Flags().Bool("include-dns", true, "Give the cluster its own subdomain under ankra.cc and install external-dns, so an ingress hostname under that subdomain gets its DNS record and TLS certificate with no manual setup. Scope: external-dns only manages the generated subdomain - ingress hosts on your own domains are ignored, and their DNS records stay yours to create (default on; pass --include-dns=false to skip)")
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

// resolveClusterArg turns a positional cluster argument into the id the
// platform routes want, accepting either the id itself or the cluster's name
// as `ankra cluster list` shows it.
//
// A UUID short-circuits, so a scripted caller that already holds the id pays
// no extra request. The trade-off is that a cluster whose NAME is itself
// UUID-shaped cannot be addressed by that name: the argument is forwarded as
// an id and the route answers 404. Nothing in the CLI or the API constrains a
// cluster's name, so this is reachable in principle; it stays this way because
// the alternative costs every scripted caller a listing request on every
// command, and the cluster id is always an unambiguous way to name it.
//
// A name is looked up once. Before this existed, only the
// three playground verbs resolved a name (ankra-y8l44.35) and every other
// cluster-scoped command forwarded the name verbatim: it reached the route as
// a non-UUID path segment and came back as a bare 404, or as a uuid_parsing
// 422 from a query parameter, with nothing in either to say an id was
// expected (ankra-aprvp).
func resolveClusterArg(nameOrID string) (string, error) {
	if isLikelyClusterID(nameOrID) {
		return nameOrID, nil
	}
	clusterID, resolveError := resolveClusterID(nameOrID)
	if resolveError != nil {
		return "", fmt.Errorf("%w (pass the cluster's name as `ankra cluster list` shows it, or its id)", resolveError)
	}
	return clusterID, nil
}

// clusterTarget renders the cluster a confirmation prompt is about. The
// argument the user typed is what they recognise, so it leads; the id it
// resolved to is appended when they differ, so a destructive prompt names the
// cluster the API is actually about to be asked to act on.
func clusterTarget(typed, clusterID string) string {
	if typed == clusterID {
		return fmt.Sprintf("%q", typed)
	}
	return fmt.Sprintf("%q (cluster %s)", typed, clusterID)
}
