package cmd

import (
	"errors"
	"fmt"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var alertsIngestCredentialsCmd = &cobra.Command{
	Use:   "ingest-credentials",
	Short: "List and rebind the credentials external notifiers deliver alerts with",
	Long: `Ingest credentials are the per-credential secrets an external Alertmanager
or log monitor uses to deliver alerts into Ankra. A credential is pinned to
one cluster (scope cluster), carries the platform's own alerts with no pin
(scope platform), or does both from a self-hosting cluster (scope mixed).

The token is minted once, in the portal, and never shown again. These
commands never touch it: a rebind moves the pin or the scope on the
existing credential, so the notifier keeps delivering.

  ankra alerts ingest-credentials list
  ankra alerts ingest-credentials rebind <credential-id> --cluster prod-hel1
  ankra alerts ingest-credentials rebind <credential-id> --unpin --scope platform`,
}

var alertsIngestCredentialsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List alert ingest credentials",
	Long: `List the organisation's alert ingest credentials with the cluster each one
is pinned to. A pin whose cluster is deleted, archived, slated for deletion
or gone shows as BROKEN: the notifier still delivers, but its alerts have no
cluster to land on until the credential is rebound.

  ankra alerts ingest-credentials list
  ankra alerts ingest-credentials list -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		list, listError := apiClient.ListAlertIngestCredentials()
		if listError != nil {
			return fmt.Errorf("listing alert ingest credentials: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, list); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		if len(list.Items) == 0 {
			_, _ = fmt.Fprintln(out, "No alert ingest credentials found.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Name", "ID", "Scope", "Cluster", "Pin", "Enabled", "Last used"})
		for _, credential := range list.Items {
			writer.AppendRow(table.Row{
				credential.Name,
				credential.ID,
				credential.Scope,
				alertIngestCredentialCluster(credential),
				alertIngestCredentialPin(credential),
				credential.Enabled,
				alertIngestCredentialLastUsed(credential),
			})
		}
		writer.Render()
		return nil
	},
}

var alertsIngestCredentialsRebindCmd = &cobra.Command{
	Use:   "rebind <credential-id>",
	Short: "Move an ingest credential's cluster pin or scope without minting a new token",
	Long: `Rebind an ingest credential: pin it to another cluster, unpin it, or change
its scope. The token is untouched, so the notifier keeps delivering through
the same credential.

Pass --cluster with a cluster name to pin, --unpin to clear the pin, and
--scope with cluster, platform or mixed. A platform-scoped credential
carries no pin; a mixed one must keep one.

  ankra alerts ingest-credentials rebind <credential-id> --cluster prod-hel1
  ankra alerts ingest-credentials rebind <credential-id> --cluster prod-hel1 --scope mixed
  ankra alerts ingest-credentials rebind <credential-id> --unpin --scope platform`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName, _ := cmd.Flags().GetString("cluster")
		unpin, _ := cmd.Flags().GetBool("unpin")
		scope := changedStringFlag(cmd, "scope")
		if clusterName != "" && unpin {
			return withExitCode(exitUsage, errors.New("--cluster and --unpin are exclusive"))
		}
		if clusterName == "" && !unpin && scope == nil {
			return withExitCode(exitUsage, errors.New("nothing to rebind: pass --cluster, --unpin or --scope"))
		}
		if scope != nil && *scope != "cluster" && *scope != "platform" && *scope != "mixed" {
			return withExitCode(exitUsage, fmt.Errorf("--scope must be cluster, platform or mixed, got %q", *scope))
		}
		request := client.RebindAlertIngestCredentialRequest{Scope: scope}
		if unpin {
			request.ClusterIDSet = true
		}
		if clusterName != "" {
			cluster, clusterError := apiClient.GetCluster(clusterName)
			if clusterError != nil {
				return fmt.Errorf("resolving cluster %q: %w", clusterName, clusterError)
			}
			clusterID := cluster.ID
			request.ClusterID = &clusterID
			request.ClusterIDSet = true
		}
		credential, rebindError := apiClient.RebindAlertIngestCredential(args[0], request)
		if rebindError != nil {
			return fmt.Errorf("rebinding alert ingest credential: %w", rebindError)
		}
		if rendered, renderError := renderStructured(cmd, credential); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Alert ingest credential %q rebound: scope %s, cluster %s.\n",
			credential.Name, credential.Scope, alertIngestCredentialCluster(*credential))
		return nil
	},
}

// alertIngestCredentialCluster renders the pin for a table cell.
func alertIngestCredentialCluster(credential client.AlertIngestCredential) string {
	if credential.ClusterName != nil && *credential.ClusterName != "" {
		return *credential.ClusterName
	}
	if credential.ClusterID != nil && *credential.ClusterID != "" {
		return *credential.ClusterID
	}
	return "-"
}

// alertIngestCredentialPin says whether the pin still points at a cluster
// that exists: BROKEN is a cluster that is deleted, archived, slated for
// deletion or gone, not a credential without a pin.
func alertIngestCredentialPin(credential client.AlertIngestCredential) string {
	switch {
	case credential.ClusterUnavailable:
		return "BROKEN"
	case credential.ClusterID != nil && *credential.ClusterID != "":
		return "ok"
	default:
		return "-"
	}
}

func alertIngestCredentialLastUsed(credential client.AlertIngestCredential) string {
	if credential.LastUsedAt == nil || *credential.LastUsedAt == "" {
		return "never"
	}
	return *credential.LastUsedAt
}

func init() {
	alertsIngestCredentialsRebindCmd.Flags().String("cluster", "", "cluster name to pin the credential to")
	alertsIngestCredentialsRebindCmd.Flags().Bool("unpin", false, "clear the cluster pin")
	alertsIngestCredentialsRebindCmd.Flags().String("scope", "", "credential scope: cluster, platform or mixed")
	alertsIngestCredentialsCmd.AddCommand(alertsIngestCredentialsListCmd)
	alertsIngestCredentialsCmd.AddCommand(alertsIngestCredentialsRebindCmd)
	alertsCmd.AddCommand(alertsIngestCredentialsCmd)
}
