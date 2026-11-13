package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var alertsIngestCredentialsCmd = &cobra.Command{
	Use:   "ingest-credentials",
	Short: "Create, list, rebind and delete the credentials external notifiers deliver alerts with",
	Long: `Ingest credentials are the per-credential secrets an external Alertmanager
or log monitor uses to deliver alerts into Ankra. A credential is pinned to
one cluster (scope cluster), carries the platform's own alerts with no pin
(scope platform), or does both from a self-hosting cluster (scope mixed).

The notifier posts to <platform>/webhooks/alerts/<credential-id> with the
token as a Bearer Authorization header (or an X-Ankra-Alert-Token header).
The token is minted by create and shown that once; it cannot be read back.
A rebind moves the pin or the scope on the existing credential, so the
notifier keeps delivering; a delete revokes the credential.

  ankra alerts ingest-credentials create --name hel1-alertmanager --cluster prod-hel1 --token-file ./ingest-token
  ankra alerts ingest-credentials list
  ankra alerts ingest-credentials rebind <credential-id> --cluster prod-hel1
  ankra alerts ingest-credentials rebind <credential-id> --unpin --scope platform
  ankra alerts ingest-credentials delete <credential-id>`,
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

var alertsIngestCredentialsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Mint an alert ingest credential and show its token once",
	Long: `Mint an ingest credential for an external Alertmanager or log monitor.
The output carries the credential id, the webhook URL the notifier posts to,
and the token. The token is shown this once and cannot be read back: store
it in the notifier's configuration (or a secret) straight away.

--cluster pins the credential to a cluster, by name or id. --scope is
cluster (the default), platform (the platform's own alerts, never pinned)
or mixed (both, always pinned).

--token-file writes the token to a new file with mode 0600 and leaves it out
of the output, -o json included, so automation never prints the secret. The
file must not exist yet.

Alertmanager delivers with a webhook receiver:

  receivers:
    - name: ankra
      webhook_configs:
        - url: <webhook URL>
          http_config:
            authorization:
              credentials_file: /etc/alertmanager/secrets/ankra-ingest-token

  ankra alerts ingest-credentials create --name hel1-alertmanager --cluster prod-hel1
  ankra alerts ingest-credentials create --name platform-monitor --scope platform --token-file ./ingest-token
  ankra alerts ingest-credentials create --name hel1-alertmanager --cluster prod-hel1 --token-file ./ingest-token -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		clusterReference, _ := cmd.Flags().GetString("cluster")
		scope, _ := cmd.Flags().GetString("scope")
		tokenFilePath, _ := cmd.Flags().GetString("token-file")
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		if name == "" {
			return withExitCode(exitUsage, errors.New("--name is required"))
		}
		if scope != "cluster" && scope != "platform" && scope != "mixed" {
			return withExitCode(exitUsage, fmt.Errorf("--scope must be cluster, platform or mixed, got %q", scope))
		}
		if scope == "platform" && clusterReference != "" {
			return withExitCode(exitUsage, errors.New("a platform-scoped credential cannot be pinned to a cluster: drop --cluster or use --scope mixed"))
		}
		if scope == "mixed" && clusterReference == "" {
			return withExitCode(exitUsage, errors.New("a mixed-scope credential must be pinned to a cluster: pass --cluster"))
		}
		request := client.CreateAlertIngestCredentialRequest{Name: name, Scope: scope}
		if clusterReference != "" {
			clusterID, resolveError := resolveClusterArg(clusterReference)
			if resolveError != nil {
				return fmt.Errorf("resolving cluster %q: %w", clusterReference, resolveError)
			}
			request.ClusterID = &clusterID
		}
		var tokenFile *os.File
		if tokenFilePath != "" {
			createdFile, openError := createTokenFile(tokenFilePath)
			if openError != nil {
				return withExitCode(exitUsage, fmt.Errorf("opening --token-file: %w", openError))
			}
			tokenFile = createdFile
			defer func() { _ = tokenFile.Close() }()
		}
		created, createError := apiClient.CreateAlertIngestCredential(request)
		if createError != nil {
			if tokenFile != nil {
				_ = tokenFile.Close()
				_ = os.Remove(tokenFilePath)
			}
			return fmt.Errorf("creating alert ingest credential: %w", createError)
		}
		output := createdAlertIngestCredentialOutput{
			Item:        created.Item,
			Token:       created.Token,
			WebhookPath: created.WebhookPath,
			WebhookURL:  alertIngestWebhookURL(*created),
		}
		if tokenFile != nil {
			if writeError := writeTokenFile(tokenFile, created.Token); writeError != nil {
				_ = tokenFile.Close()
				_ = os.Remove(tokenFilePath)
				return fmt.Errorf("alert ingest credential %s was created but its token could not be written to %s: %w; "+
					"the token cannot be read back, so delete the credential with "+
					"'ankra alerts ingest-credentials delete %s' and create another", created.Item.ID, tokenFilePath, writeError, created.Item.ID)
			}
			output.Token = ""
			output.TokenFile = tokenFilePath
		}
		if rendered, renderError := renderStructured(cmd, output); rendered || renderError != nil {
			return renderError
		}
		writeCreatedAlertIngestCredential(cmd.OutOrStdout(), output)
		return nil
	},
}

var alertsIngestCredentialsDeleteCmd = &cobra.Command{
	Use:   "delete <credential-id>",
	Short: "Delete an alert ingest credential, revoking its token",
	Long: `Delete an alert ingest credential. Its token is revoked: every delivery the
notifier makes with it from then on is refused. Asks first; --yes skips the
question, and without it a non-interactive run is cancelled.

  ankra alerts ingest-credentials delete <credential-id>
  ankra alerts ingest-credentials delete <credential-id> --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Delete alert ingest credential %q? Its notifier's deliveries will be refused. [y/N]: ", credentialID),
			yes); confirmError != nil {
			return confirmError
		}
		result, deleteError := apiClient.DeleteAlertIngestCredential(credentialID)
		if deleteError != nil {
			return fmt.Errorf("deleting alert ingest credential: %w", deleteError)
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Alert ingest credential %s deleted.\n", credentialID)
		return nil
	},
}

// createdAlertIngestCredentialOutput is what create prints with -o: the
// create response plus the full webhook URL, with the token replaced by
// the file it went to when --token-file is set.
type createdAlertIngestCredentialOutput struct {
	Item        client.AlertIngestCredential `json:"item" yaml:"item"`
	Token       string                       `json:"token,omitempty" yaml:"token,omitempty"`
	TokenFile   string                       `json:"token_file,omitempty" yaml:"token_file,omitempty"`
	WebhookPath string                       `json:"webhook_path" yaml:"webhook_path"`
	WebhookURL  string                       `json:"webhook_url" yaml:"webhook_url"`
}

func writeCreatedAlertIngestCredential(out io.Writer, output createdAlertIngestCredentialOutput) {
	_, _ = fmt.Fprintf(out, "Alert ingest credential %q created.\n", output.Item.Name)
	_, _ = fmt.Fprintf(out, "  ID:          %s\n", output.Item.ID)
	_, _ = fmt.Fprintf(out, "  Scope:       %s\n", output.Item.Scope)
	_, _ = fmt.Fprintf(out, "  Cluster:     %s\n", alertIngestCredentialCluster(output.Item))
	_, _ = fmt.Fprintf(out, "  Webhook URL: %s\n", output.WebhookURL)
	if output.TokenFile != "" {
		_, _ = fmt.Fprintf(out, "  Token:       written to %s (mode 0600)\n", output.TokenFile)
		_, _ = fmt.Fprintln(out, "\nThe token cannot be read back: keep that file, or move its contents into a secret, before you delete it.")
	} else {
		_, _ = fmt.Fprintf(out, "  Token:       %s\n", output.Token)
		_, _ = fmt.Fprintln(out, "\nThis token is shown once and cannot be read back. Store it now.")
	}
	_, _ = fmt.Fprintln(out, "The notifier sends it as 'Authorization: Bearer <token>' (or an X-Ankra-Alert-Token header).")
}

// alertIngestWebhookURL is the address the notifier posts to. The platform
// serves /webhooks/ on the origin of its API, so the path the server
// answers is joined to the scheme and host the CLI talks to.
func alertIngestWebhookURL(created client.CreatedAlertIngestCredential) string {
	webhookPath := created.WebhookPath
	if webhookPath == "" {
		webhookPath = "/webhooks/alerts/" + created.Item.ID
	}
	return portalBaseURL() + webhookPath
}

// createTokenFile creates the --token-file before the credential is
// minted, so a path that cannot be written fails while nothing exists yet.
// It refuses an existing file: truncating it would destroy whatever secret
// it held if the create then failed.
func createTokenFile(path string) (*os.File, error) {
	file, openError := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if openError != nil {
		return nil, openError
	}
	if chmodError := file.Chmod(0o600); chmodError != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, chmodError
	}
	return file, nil
}

// writeTokenFile writes the token with no trailing newline, so a reader
// that takes the file verbatim (a Kubernetes Secret, a credentials_file)
// gets exactly the token.
func writeTokenFile(file *os.File, token string) error {
	if _, writeError := file.WriteString(token); writeError != nil {
		return writeError
	}
	if syncError := file.Sync(); syncError != nil {
		return syncError
	}
	return file.Close()
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
	alertsIngestCredentialsCreateCmd.Flags().String("name", "", "credential name, unique in the organisation (required)")
	alertsIngestCredentialsCreateCmd.Flags().String("cluster", "", "cluster name or id to pin the credential to")
	alertsIngestCredentialsCreateCmd.Flags().String("scope", "cluster", "credential scope: cluster, platform or mixed")
	alertsIngestCredentialsCreateCmd.Flags().String("token-file", "", "write the token to this new file (mode 0600) instead of printing it")
	alertsIngestCredentialsDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	alertsIngestCredentialsCmd.AddCommand(alertsIngestCredentialsCreateCmd)
	alertsIngestCredentialsCmd.AddCommand(alertsIngestCredentialsListCmd)
	alertsIngestCredentialsCmd.AddCommand(alertsIngestCredentialsRebindCmd)
	alertsIngestCredentialsCmd.AddCommand(alertsIngestCredentialsDeleteCmd)
	alertsCmd.AddCommand(alertsIngestCredentialsCmd)
}
