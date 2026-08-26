package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type execCredentialStatus struct {
	Token               string `json:"token"`
	ExpirationTimestamp string `json:"expirationTimestamp,omitempty"`
}

type execCredential struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Status     execCredentialStatus `json:"status"`
}

var clusterKubeTokenCmd = &cobra.Command{
	Use:   "kube-token",
	Short: "Print a Kubernetes ExecCredential for use as a kubeconfig credential plugin",
	Long: `Print a short-lived Kubernetes ExecCredential so kubectl can authenticate to the
Ankra cluster gateway.

This command is intended to be invoked by kubectl as a client-go credential plugin,
for example in a kubeconfig:

  users:
  - name: ankra
    user:
      exec:
        apiVersion: client.authentication.k8s.io/v1
        command: ankra
        args: ["cluster", "kube-token", "--cluster", "<cluster-name-or-id>", "--org", "<organisation-id>"]

Pinning --org to the cluster's organisation ID keeps the entry working when
your selected organisation differs from the cluster's ('ankra cluster
kubeconfig add' writes it automatically). An entry written without --org still
works: a cluster ID the selected organisation does not have is looked up
across the organisations you belong to, and the token is minted against the
one that owns it. Your selected organisation is not changed.

It prints JSON to stdout and never prompts; run 'ankra login' first.`,
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		clusterID, organisationKnown, err := resolveGatewayClusterID(clusterFlag, nil)
		if err != nil {
			return err
		}

		kubeToken, err := apiClient.GetClusterKubeToken(context.Background(), clusterID)
		if err != nil {
			return decorateKubeTokenError(err, kubeTokenClusterReference(clusterFlag, clusterID), clusterID, organisationKnown)
		}

		credential := execCredential{
			APIVersion: "client.authentication.k8s.io/v1",
			Kind:       "ExecCredential",
			Status: execCredentialStatus{
				Token:               kubeToken.Token,
				ExpirationTimestamp: normalizeExpirationTimestamp(kubeToken.ExpiresAt),
			},
		}
		output, err := json.Marshal(credential)
		if err != nil {
			return err
		}
		fmt.Println(string(output))
		return nil
	},
}

// decorateKubeTokenError turns the two kube-token mint failures that look like
// a cluster problem but are not into errors that name the real cause. Both
// decorations keep the original error in the chain, so exit-code
// classification is unchanged; every other failure passes through untouched.
//
// organisationKnown says the caller already established which organisation has
// this cluster. The cross-organisation sweep then cannot change the answer, so
// it is skipped: on the kubectl credential-plugin path, which reruns on every
// command, it would be an organisation list plus a lookup per organisation on
// an already-failing invocation.
func decorateKubeTokenError(err error, clusterRef, clusterID string, organisationKnown bool) error {
	var unexpected *client.UnexpectedResponseError
	if !organisationKnown && errors.As(err, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
		return explainKubeTokenNotFound(err, clusterRef, clusterID)
	}
	return suggestAccessOnKubeTokenDenied(err, clusterRef)
}

// explainKubeTokenNotFound decorates a 404 from the token mint. The gateway
// resolves the cluster inside the organisation the request is scoped to, so
// "Cluster not found" is also the answer for a cluster that exists, that you
// hold a grant on, and whose ID is right there in the kubeconfig server URL —
// it just belongs to another organisation. Name the organisations instead, so
// nobody goes hunting through access grants and RBAC for an organisation
// selection problem.
func explainKubeTokenNotFound(err error, clusterRef, clusterID string) error {
	search := findClusterInOtherOrganisations(clusterID)
	explained := notInScopedOrganisationError(search, clusterRef, err)
	if !search.found {
		return explained
	}
	return fmt.Errorf("%w\nOr re-add the context so it pins the organisation itself and survives 'ankra org switch':\n  ankra cluster kubeconfig add %s", explained, clusterID)
}

// suggestAccessOnKubeTokenDenied decorates a kube-token mint failure. A 403
// from the token endpoint means the caller has no access grant on the cluster
// (the gateway's per-user access check), not bad credentials — re-login won't
// help, but an organisation admin running 'ankra cluster access grant' will,
// so the error points at that command. Every other error passes through
// untouched, and the original error stays in the chain so exit-code
// classification is unchanged.
func suggestAccessOnKubeTokenDenied(err error, clusterRef string) error {
	var unexpected *client.UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusForbidden {
		return err
	}
	return fmt.Errorf("%w\nYou have no access grant on this cluster. Ask an organisation admin to add one, then retry:\n  ankra cluster access grant <your-email> --cluster %s --role view", err, clusterRef)
}

// kubeTokenClusterReference picks the most readable cluster reference for the
// access-grant suggestion: what the user typed, else the selected cluster's
// name, else the resolved ID.
func kubeTokenClusterReference(clusterFlag, clusterID string) string {
	if clusterFlag != "" {
		return clusterFlag
	}
	if selected, err := loadSelectedCluster(); err == nil && selected.Name != "" {
		return selected.Name
	}
	return clusterID
}

// resolveKubeTokenClusterID resolves the cluster reference the kube-gateway
// commands were given (kube-token, cluster access) to a cluster ID. It is the
// quiet variant: kubectl re-runs the credential plugin on every command, so
// nothing here may write to stderr.
func resolveKubeTokenClusterID(clusterFlag string) (string, error) {
	clusterID, _, err := resolveGatewayClusterID(clusterFlag, nil)
	return clusterID, err
}

// resolveGatewayClusterID resolves the cluster reference and, for an ID the
// organisation in scope does not have, re-scopes this invocation to the
// organisation that owns it. Without that the ID is forwarded as-is and
// resolved inside the selected organisation, which answers 404 for a cluster
// that plainly exists.
//
// The second return value reports whether the owning organisation ended up
// known. A mint that fails afterwards is then not an organisation-scoping
// problem, so the error path can skip the cross-organisation sweep.
//
// notify, when non-nil, receives a note whenever the organisation was
// re-scoped.
func resolveGatewayClusterID(clusterFlag string, notify io.Writer) (string, bool, error) {
	if clusterFlag != "" {
		cluster, err := apiClient.GetCluster(clusterFlag)
		if err == nil {
			// A name only resolves inside the organisation in scope, so a hit
			// here settles the organisation question by construction.
			return cluster.ID, true, nil
		}
		if isLikelyClusterID(clusterFlag) {
			return resolveGatewayClusterByID(clusterFlag, notify)
		}
		return "", false, fmt.Errorf("cluster %q not found; pass a cluster name or ID (not the kubeconfig context name): %w", clusterFlag, err)
	}
	cluster, err := loadSelectedCluster()
	if err != nil {
		return "", false, fmt.Errorf("no cluster specified and no active cluster selected; pass --cluster <name|id>")
	}
	// The selection is a local file written by 'ankra cluster select', so it
	// long outlives the organisation that was selected alongside it. Give it
	// the same treatment as an ID typed at the prompt rather than trusting it:
	// omitting --cluster must not fail where passing the very same ID
	// succeeds.
	if isLikelyClusterID(cluster.ID) {
		return resolveGatewayClusterByID(cluster.ID, notify)
	}
	return cluster.ID, false, nil
}

// resolveGatewayClusterByID settles the organisation for a cluster ID and
// reports whether the owner ended up known. Absent is the only outcome that
// fails: unknown leaves the ID to the backend, as before the lookup existed.
func resolveGatewayClusterByID(clusterID string, notify io.Writer) (string, bool, error) {
	resolution := resolveClusterOrganisation(clusterID, notify)
	if resolution.absent {
		return "", false, withExitCode(exitNotFound, notInScopedOrganisationError(resolution.search, clusterID, nil))
	}
	return clusterID, resolution.settled(), nil
}

func normalizeExpirationTimestamp(expiresAt string) string {
	if expiresAt == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return expiresAt
	}
	return parsed.UTC().Format(time.RFC3339)
}

func init() {
	clusterKubeTokenCmd.Flags().String("cluster", "", "Cluster name or ID (defaults to the selected cluster)")
	clusterCmd.AddCommand(clusterKubeTokenCmd)
}
