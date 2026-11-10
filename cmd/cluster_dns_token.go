package cmd

// `ankra cluster dns-token status|rotate|revoke-previous` (ankra-hq41xb.15):
// the CLI surface over GET/POST /api/v1/clusters/{cluster_id}/dns-zone/token-rotation
// and POST .../token-rotation/revoke-previous, the rotation of the write
// token the cluster's external-dns publishes into its DNS zone with. No
// route here returns a token, and no command prints one.

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func newClusterDNSTokenCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "dns-token",
		Short: "Rotate the token the cluster publishes its DNS records with",
		Long: `The cluster's external-dns publishes into the cluster's generated DNS zone
('ankra cluster domain') with a write token Ankra minted for it. Rotating it is
three steps, so the cluster never stops publishing:

  1. 'ankra cluster dns-token rotate' asks Ankra for a replacement. Ankra mints
     it within about a minute and republishes the external-dns Secret. The
     replaced token stays valid.
  2. Restart external-dns on the cluster so it reads the new Secret, and
     confirm records still publish.
  3. 'ankra cluster dns-token revoke-previous' revokes the replaced token.

'ankra cluster dns-token status' says where a rotation stands. No command here
shows a token.`,
	}
	command.AddCommand(newClusterDNSTokenStatusCommand(), newClusterDNSTokenRotateCommand(), newClusterDNSTokenRevokePreviousCommand())
	return command
}

func newClusterDNSTokenStatusCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Show where the rotation of the cluster's DNS token stands",
		Long: `Show whether a rotation is waiting for its replacement token, and whether the
token the last rotation replaced is still valid and waiting to be revoked. The
cluster is --cluster or the one 'ankra cluster select' saved. A cluster without
a DNS zone exits 3.`,
		Example: `  ankra cluster dns-token status
  ankra cluster dns-token status --cluster prod -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			cluster, resolveError := resolveActiveCluster(command)
			if resolveError != nil {
				return resolveError
			}
			ctx, cancel := hostedLogsContext(command)
			defer cancel()
			rotation, getError := apiClient.GetClusterDNSTokenRotation(ctx, cluster.ID)
			if getError != nil {
				return clusterSettingRouteError("reading the DNS token rotation", cluster, getError)
			}
			return renderClusterDNSTokenRotation(command, format, cluster, rotation, "")
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newClusterDNSTokenRotateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "rotate",
		Short: "Ask Ankra to replace the cluster's DNS token",
		Long: `Ask Ankra to replace the write token the cluster's external-dns publishes with.
Ankra mints the replacement within about a minute and republishes the
external-dns Secret; the replaced token stays valid until you revoke it, so the
cluster keeps publishing meanwhile. Asking again while a rotation is pending
changes nothing.

The platform refuses (409) while the zone is not active, or while the token the
last rotation replaced has not been revoked yet. The cluster is --cluster or
the one 'ankra cluster select' saved.`,
		Example: `  ankra cluster dns-token rotate
  ankra cluster dns-token rotate --cluster prod`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			cluster, resolveError := resolveActiveCluster(command)
			if resolveError != nil {
				return resolveError
			}
			rotation, rotateError := apiClient.RotateClusterDNSToken(cluster.ID)
			if rotateError != nil {
				return clusterSettingRouteError("rotating the DNS token", cluster, rotateError)
			}
			return renderClusterDNSTokenRotation(command, format, cluster, rotation,
				"Rotation requested. Once 'ankra cluster dns-token status' shows the replacement is minted, restart "+
					"external-dns on the cluster, confirm records still publish, then run 'ankra cluster dns-token revoke-previous'.")
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newClusterDNSTokenRevokePreviousCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "revoke-previous",
		Short: "Revoke the DNS token the last rotation replaced",
		Long: `Ask Ankra to revoke the DNS write token the last rotation replaced. Run it only
once the cluster publishes with the new token: a cluster whose external-dns
still uses the replaced token stops publishing DNS records when it is revoked.
That is why this asks for confirmation; --yes skips the question and is
required when there is no terminal to ask on.

The platform refuses (409) while the replacement token is not minted yet, or
when there is no replaced token to revoke. Asking again once requested changes
nothing. The cluster is --cluster or the one 'ankra cluster select' saved.`,
		Example: `  ankra cluster dns-token revoke-previous
  ankra cluster dns-token revoke-previous --cluster prod --yes`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			cluster, resolveError := resolveActiveCluster(command)
			if resolveError != nil {
				return resolveError
			}
			if confirmError := confirmClusterDNSTokenRevocation(command, cluster); confirmError != nil {
				return confirmError
			}
			rotation, revokeError := apiClient.RevokePreviousClusterDNSToken(cluster.ID)
			if revokeError != nil {
				return clusterSettingRouteError("revoking the replaced DNS token", cluster, revokeError)
			}
			return renderClusterDNSTokenRotation(command, format, cluster, rotation, "Revocation of the replaced token requested.")
		},
	}
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	registerStructuredOutputFlags(command)
	return command
}

// confirmClusterDNSTokenRevocation asks before the replaced token is revoked.
// Everything goes to stderr so -o json|yaml stays parseable.
func confirmClusterDNSTokenRevocation(command *cobra.Command, cluster client.ClusterListItem) error {
	yes, _ := command.Flags().GetBool("yes")
	if yes {
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"revoking the replaced DNS token of cluster '%s' stops DNS publishing if external-dns still uses it, and "+
				"needs confirmation, but there is no terminal to ask on; re-run with --yes to confirm", cluster.Name))
	}
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprintf(errOut, "Revoking the replaced DNS token of cluster '%s' stops its DNS publishing if external-dns "+
		"has not been restarted onto the new token.\n", cluster.Name)
	if promptError := confirmPrompt(command.InOrStdin(), errOut, "Revoke the replaced token? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

func renderClusterDNSTokenRotation(command *cobra.Command, format outputFormat, cluster client.ClusterListItem,
	rotation *client.ClusterDNSTokenRotation, note string) error {
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, rotation)
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "DNS token of cluster '%s':\n\n", cluster.Name)
	printClusterDNSTokenRotation(out, rotation)
	if note != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", note)
	}
	return nil
}

func printClusterDNSTokenRotation(out io.Writer, rotation *client.ClusterDNSTokenRotation) {
	_, _ = fmt.Fprintf(out, "  Zone:           %s (%s)\n", rotation.FQDN, rotation.State)
	if rotation.RotationRequestedAt != nil {
		_, _ = fmt.Fprintf(out, "  Rotation:       waiting for the replacement token, requested %s\n", *rotation.RotationRequestedAt)
	} else {
		_, _ = fmt.Fprintln(out, "  Rotation:       none pending")
	}
	switch {
	case rotation.RevocationRequestedAt != nil:
		_, _ = fmt.Fprintf(out, "  Replaced token: revocation requested %s\n", *rotation.RevocationRequestedAt)
	case rotation.PreviousTokenPendingRevocation:
		_, _ = fmt.Fprintln(out, "  Replaced token: still valid, waiting for 'ankra cluster dns-token revoke-previous'")
	default:
		_, _ = fmt.Fprintln(out, "  Replaced token: none waiting to be revoked")
	}
}

func init() {
	clusterCmd.AddCommand(newClusterDNSTokenCommand())
}
