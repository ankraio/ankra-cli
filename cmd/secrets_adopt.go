package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// `ankra secrets adopt` creates a secret slot from one key of a Kubernetes
// Secret that already runs in a cluster (POST /api/v1/org/secret-slots/adopt,
// cluster#6139). The platform reads the key live through the cluster's agent
// and stores it; the request names the Secret and the key, and the answer is
// the slot without its value. Nothing here ever holds, prints or logs a
// value.

// maxSecretLabelLength is the platform's limit on a slot's name (openapi
// SecretSlotAdoptRequest.label), checked here so the mistake is named before
// anything is sent.
const maxSecretLabelLength = 120

var secretsAdoptCmd = &cobra.Command{
	Use:   "adopt <label>",
	Short: "Move one key of a Secret running in a cluster into a secret, without the value leaving Ankra",
	Long: `Create a secret from one key of a Kubernetes Secret that already runs in a
cluster, without the value passing through your terminal. <label> is the new
secret's name, as 'ankra secrets list' shows it.

Use it to bring a credential that lives only in a cluster (a password a chart
generated, a token someone applied by hand) under Ankra's secrets, so
manifests and addon values can reference it by its sentinel instead of
carrying it. Revealing the Secret and piping the value into 'ankra secrets
create' would put every key of it on your screen and in your shell; adopt
never does. Ankra reads that one key live through the cluster's agent and
stores it in the new secret. The Secret's other keys are never read out, and
the value is never shown or sent to the CLI.

The value is stored byte for byte. A value saved with a trailing line ending
(written with echo, or a file an editor saved) keeps it unless
--strip-trailing-newline is given, which drops exactly one \n or \r\n.

The sentinel alone goes to stdout, so it can be captured; the summary goes
to stderr:

  SENTINEL="$(ankra secrets adopt db-password --cluster prod -n app --name db-credentials --key password)"

--cluster is required, by name or id: the selected cluster is never used,
since every session on the machine shares that selection.

Needs the kubernetes.secrets_reveal permission on the cluster, which the
operator, admin and owner roles carry, as well as variables.write, which
every role but viewer holds. The adopt is recorded in the organisation's
audit log as a reveal of that one key. Without either permission the command
names it and exits 7.

Exit codes: 2 for a missing flag, or a name, hint or value the platform
refuses (an empty value, one that is not UTF-8 text or is over the size
limit); 3 when the cluster, the Secret or the key does not exist; 7 for a
missing permission; 1 when the name is already taken, when the cluster
answered from its synced cache (which holds no values), or when its agent
could not be reached or could not read the Secret. Nothing is stored in any
of them.`,
	Example: `  ankra secrets adopt db-password --cluster prod -n app --name db-credentials --key password
  ankra secrets adopt grafana-admin --cluster prod -n monitoring --name grafana --key admin-password \
    --hint "Grafana admin login" --strip-trailing-newline
  ankra secrets adopt ci-token --cluster 5f0c3b8e-1d2a-4c6b-9e7f-0a1b2c3d4e5f -n ci --name runner --key token -o json`,
	Args: secretsAdoptArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		request, clusterReference, buildError := secretsAdoptRequestFromFlags(cmd, args[0])
		if buildError != nil {
			return buildError
		}
		clusterID, resolveError := resolveClusterArg(clusterReference)
		if resolveError != nil {
			return resolveError
		}
		request.ClusterID = clusterID

		ctx, cancel := context.WithTimeout(cmd.Context(), secretsRequestTimeout)
		defer cancel()
		slot, adoptError := apiClient.AdoptSecretSlot(ctx, request)
		if adoptError != nil {
			return secretsAdoptError(request, adoptError)
		}
		if rendered, renderError := renderStructured(cmd, slot); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"Adopted key %q of Secret %s/%s on cluster %s into secret %q (id %s, %s). Its sentinel:\n",
			request.Key, request.Namespace, request.Name, clusterReference, slot.Label, slot.SlotID,
			secretSlotExpiryPhrase(slot))
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), slot.Sentinel)
		return nil
	},
}

// secretsAdoptArgs takes exactly one argument, the new secret's name. A
// second one is refused without repeating it.
func secretsAdoptArgs(_ *cobra.Command, args []string) error {
	switch {
	case len(args) == 0:
		return withExitCode(exitUsage, errors.New(
			"the new secret's name is required: ankra secrets adopt <label> --cluster <name|id> --namespace <ns> --name <secret> --key <key>"))
	case len(args) > 1:
		return withExitCode(exitUsage, errors.New(
			"adopt takes one argument, the new secret's name; name the Secret with --cluster, --namespace, --name and --key"))
	}
	return nil
}

// secretsAdoptRequestFromFlags checks the invocation and builds the request,
// all before anything is sent: every flag the platform requires is present
// and not blank, and the name and hint are within the platform's limits. It
// returns the request without its cluster id and the --cluster value to
// resolve.
func secretsAdoptRequestFromFlags(cmd *cobra.Command, label string) (client.SecretSlotAdoptRequest, string, error) {
	request := client.SecretSlotAdoptRequest{Label: strings.TrimSpace(label)}
	switch {
	case request.Label == "":
		return request, "", withExitCode(exitUsage, errors.New("the new secret's name is required"))
	case strings.ContainsAny(request.Label, "\r\n"):
		return request, "", withExitCode(exitUsage, errors.New("the new secret's name cannot contain a line break"))
	case len([]rune(request.Label)) > maxSecretLabelLength:
		return request, "", withExitCode(exitUsage, fmt.Errorf("the new secret's name is at most %d characters", maxSecretLabelLength))
	}
	request.Hint, _ = cmd.Flags().GetString("hint")
	if len([]rune(request.Hint)) > maxSecretHintLength {
		return request, "", withExitCode(exitUsage, fmt.Errorf("--hint is at most %d characters", maxSecretHintLength))
	}
	request.StripTrailingLineEnding, _ = cmd.Flags().GetBool("strip-trailing-newline")

	values := map[string]string{}
	var missing []string
	for _, flag := range []string{"cluster", "namespace", "name", "key"} {
		value, _ := cmd.Flags().GetString(flag)
		values[flag] = strings.TrimSpace(value)
		if values[flag] == "" {
			missing = append(missing, "--"+flag)
		}
	}
	if len(missing) > 0 {
		return request, "", withExitCode(exitUsage, fmt.Errorf(
			"missing %s: adopt reads one key of one Secret, so it needs --cluster, --namespace, --name and --key "+
				"(the selected cluster is never used)", strings.Join(missing, ", ")))
	}
	request.Namespace, request.Name, request.Key = values["namespace"], values["name"], values["key"]
	return request, values["cluster"], nil
}

// secretsAdoptError names what was asked for and keeps the exit-code
// contract. 401 (6), 403 (7) and 404 (3) keep the codes exitCodeFor gives
// every command. 400 and 422 mean the request has to change before it can
// succeed (a name or hint the platform refuses, a value a secret cannot hold),
// so they are usage errors (2), as for the AI board's writes. 409 (a taken
// name, a read answered from the synced cache), 502 (the agent could not read
// the Secret) and the 503 agent envelopes are refusals a retry or another
// state can change, so they stay 1, as for 'secrets create' and
// 'cluster get secrets --reveal'.
func secretsAdoptError(request client.SecretSlotAdoptRequest, adoptError error) error {
	asked := fmt.Sprintf("adopt key %q of Secret %s/%s into secret %q", request.Key, request.Namespace, request.Name, request.Label)
	if unavailable, isUnavailable := client.AsClusterAgentUnavailable(adoptError); isUnavailable {
		return fmt.Errorf("%s: the Secret could not be read live, so nothing was stored (%w); "+
			"try again once the cluster's agent is connected", asked, unavailable)
	}
	wrapped := fmt.Errorf("%s: %w", asked, adoptError)
	var refusal *client.UnexpectedResponseError
	if errors.As(adoptError, &refusal) &&
		(refusal.StatusCode == http.StatusBadRequest || refusal.StatusCode == http.StatusUnprocessableEntity) {
		return withExitCode(exitUsage, wrapped)
	}
	return wrapped
}

func init() {
	registerStructuredOutputFlags(secretsAdoptCmd)
	secretsAdoptCmd.Flags().String("cluster", "", "The cluster the Secret runs in, by name or id (required; the selected cluster is never used)")
	secretsAdoptCmd.Flags().StringP("namespace", "n", "", "The Secret's namespace (required)")
	secretsAdoptCmd.Flags().String("name", "", "The Secret's name (required)")
	secretsAdoptCmd.Flags().String("key", "", "The key under the Secret's data to adopt; no other key is read out (required)")
	secretsAdoptCmd.Flags().String("hint", "", "A note about what the secret is for (at most 240 characters)")
	secretsAdoptCmd.Flags().Bool("strip-trailing-newline", false, "Drop one trailing line ending (\\n or \\r\\n) from the value before storing it")
	secretsCmd.AddCommand(secretsAdoptCmd)
}
