package cmd

// `ankra cluster held-updates take` (ankra-hq41xb.15): the CLI surface over
// POST /api/v1/org/clusters/{cluster_id}/held-platform-updates/{notification_id}/take.
// On a cluster that is production, or that nobody has confirmed is not,
// Ankra holds every platform add-on version or image update and raises a
// card instead; taking the card applies exactly the updates it names on the
// next publish of their stack.
//
// `ankra cluster metrics source` beside it reads the cluster's registered
// metrics source (GET /api/v1/org/clusters/imported/{cluster_id}/metrics-source).

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func newClusterHeldUpdatesCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "held-updates",
		Short: "Take the platform add-on updates Ankra is holding for the cluster",
		Long: `On a cluster that is production, or that nobody has confirmed is not production,
Ankra holds every update of a platform add-on's version or image and raises a
"held platform update" card on the cluster instead of applying it. Taking the
card is how you accept one set of those updates without editing YAML.`,
	}
	command.AddCommand(newClusterHeldUpdatesTakeCommand())
	return command
}

func newClusterHeldUpdatesTakeCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "take <notification-id> --fingerprint <fingerprint>",
		Short: "Take Ankra's update on one held platform update card",
		Long: `Take the updates one held platform update card names: Ankra applies exactly the
platform add-on updates the card lists as waiting for your call, at the versions
it shows, on the next publish of their stack.

<notification-id> is the card's id and --fingerprint is the card's
data.fingerprint as you read it. A card that changed since is refused (the
platform answers 409), so you never take updates you have not seen. A version
someone on your team chose is kept and listed under "Kept", and an update still
waits while the cluster's agent or control plane cannot take it. A later, newer
version is held again and raises a new card.

Every take is recorded in the cluster's audit log. It needs permission to
change cluster settings (clusters.write). The cluster is --cluster or the one
'ankra cluster select' saved.`,
		Example: `  ankra cluster held-updates take 0b6f1c1e-7c0e-4b7e-9a57-0d5f3c1a2b3c --fingerprint 3f9a...
  ankra cluster held-updates take <notification-id> --fingerprint <fingerprint> --cluster prod -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterHeldUpdatesTake(command, arguments[0])
		},
	}
	command.Flags().String("fingerprint", "", "The card's data.fingerprint as you read it (required)")
	registerStructuredOutputFlags(command)
	return command
}

func runClusterHeldUpdatesTake(command *cobra.Command, notificationID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	fingerprint, _ := command.Flags().GetString("fingerprint")
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return withExitCode(exitUsage, errors.New(
			"--fingerprint is required: pass the card's data.fingerprint as you read it, so a card that changed since is refused"))
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	take, takeError := apiClient.TakeHeldPlatformUpdate(ctx, cluster.ID, strings.TrimSpace(notificationID), fingerprint)
	if takeError != nil {
		var unexpected *client.UnexpectedResponseError
		if errors.As(takeError, &unexpected) && unexpected.StatusCode == http.StatusConflict {
			return fmt.Errorf(
				"nothing was taken for cluster '%s': %w. Read the card again and pass its current fingerprint", cluster.Name, takeError)
		}
		return clusterSettingRouteError("taking the held platform update", cluster, takeError)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, take)
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Took %d held platform update(s) for cluster '%s'; the next publish of their stack applies them.\n",
		len(take.Taken), cluster.Name)
	printHeldPlatformUpdates(out, "Taken", take.Taken)
	printHeldPlatformUpdates(out, "Kept", take.Kept)
	return nil
}

func printHeldPlatformUpdates(out io.Writer, heading string, updates []client.HeldPlatformUpdate) {
	if len(updates) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\n%s:\n", heading)
	for _, update := range updates {
		target := update.Member
		if update.Image != "" {
			target += " (" + update.Image + ")"
		}
		_, _ = fmt.Fprintf(out, "  %s/%s %s: %s -> %s [%s]\n",
			update.Stack, update.MemberKind, target, update.Running, update.Pinned, update.Reason)
	}
}

var clusterMetricsSourceCmd = &cobra.Command{
	Use:   "source",
	Short: "Show the cluster's registered metrics source",
	Long: `Show the metrics source registered for the cluster, the one 'ankra cluster
metrics query' and the platform's metrics pages read: its provider, endpoint,
state, flavor, the latest health probe and the credential it resolves to. The
credential's password and token are never returned.

The source itself is declared in the cluster YAML (spec.prometheus_metrics) and
changed with 'ankra cluster apply'. A cluster without a metrics source exits 3.`,
	Example: `  ankra cluster metrics source
  ankra cluster metrics source --cluster prod -o json`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		clusterID, resolveError := resolveMetricsClusterID(command)
		if resolveError != nil {
			return resolveError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		source, getError := apiClient.GetClusterMetricsSource(ctx, clusterID)
		if getError != nil {
			var unexpected *client.UnexpectedResponseError
			if errors.As(getError, &unexpected) && unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == routeAbsentDetail {
				return withExitCode(exitError, errors.New(
					"reading the metrics source: this Ankra platform does not serve this route yet (it answered 404)"))
			}
			return fmt.Errorf("reading the metrics source: %w", getError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, source)
		}
		printClusterMetricsSource(command.OutOrStdout(), source)
		return nil
	},
}

func printClusterMetricsSource(out io.Writer, source *client.ClusterMetricsSource) {
	_, _ = fmt.Fprintf(out, "Provider:   %s\n", source.Provider)
	_, _ = fmt.Fprintf(out, "Endpoint:   %s\n", source.Endpoint)
	_, _ = fmt.Fprintf(out, "State:      %s\n", source.State)
	if source.Flavor != nil && *source.Flavor != "" {
		_, _ = fmt.Fprintf(out, "Flavor:     %s\n", *source.Flavor)
	}
	if source.TenantID != nil && *source.TenantID != "" {
		_, _ = fmt.Fprintf(out, "Tenant:     %s\n", *source.TenantID)
	}
	switch {
	case source.Credential != nil:
		_, _ = fmt.Fprintf(out, "Credential: %s (%s)\n", source.Credential.Name, source.Credential.State)
	case source.CredentialName != nil && *source.CredentialName != "":
		_, _ = fmt.Fprintf(out, "Credential: %s\n", *source.CredentialName)
	default:
		_, _ = fmt.Fprintln(out, "Credential: none")
	}
	if source.Health == nil {
		_, _ = fmt.Fprintln(out, "Health:     not probed yet")
		return
	}
	_, _ = fmt.Fprintf(out, "Health:     %s\n", source.Health.State)
	if source.Health.CheckedAt != nil {
		_, _ = fmt.Fprintf(out, "Checked:    %s (%s)\n", formatTimeAgo(*source.Health.CheckedAt), *source.Health.CheckedAt)
	}
	if source.Health.Detail != nil && *source.Health.Detail != "" {
		_, _ = fmt.Fprintf(out, "Detail:     %s\n", *source.Health.Detail)
	}
}

func init() {
	registerStructuredOutputFlags(clusterMetricsSourceCmd)
	clusterMetricsCmd.AddCommand(clusterMetricsSourceCmd)
	clusterCmd.AddCommand(newClusterHeldUpdatesCommand())
}
