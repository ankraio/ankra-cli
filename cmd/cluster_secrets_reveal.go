package cmd

import (
	"errors"
	"fmt"
	"os"

	"ankra/internal/client"
)

// Since the platform's Secret value policy (cluster ankra-z0p76), a
// resources/get read returns every Secret value as a sha256 digest. The
// plaintext comes back only for one Secret read by name, when the request
// sets reveal_secret_values and the caller holds kubernetes.secrets_reveal
// on the cluster; every reveal is audited, and a listing never reveals.
// This file is the CLI side of that contract: `cluster get secrets <name>
// -n <namespace> --reveal`.

const secretsLongHelp = `List Secrets, or read one Secret by name.

The platform returns every Secret value as a sha256 digest (sha256: followed
by 12 hex characters) unless you ask for the values. Keys, type and metadata
are readable; the values are not. A digest still changes when its value does.

--reveal prints the plaintext values of ONE named Secret. It needs the
Secret's namespace (-n), it is refused on a listing (no name, -A or -l), and
it needs the kubernetes.secrets_reveal permission on the cluster, which the
operator, admin and owner roles carry. The platform records every reveal in
the organisation's audit log. Values under data stay base64-encoded, as with
kubectl.

Exit codes with --reveal: 7 when your role lacks kubernetes.secrets_reveal,
3 when the Secret does not exist, 1 when the platform could not hand out live
values (for example the cluster is unreachable), 2 for a reveal on a listing.

Examples:
  ankra cluster get secrets -n default
  ankra cluster get secrets db-credentials -n default
  ankra cluster get secrets db-credentials -n default --reveal`

// secretDigestNote is printed to stderr when structured output carries
// digests, so nobody mistakes sha256:... for a Secret's value.
const secretDigestNote = "Note: Secret values are shown as sha256 digests, not their contents. " +
	"To read one Secret's values, run: ankra cluster get secrets <name> -n <namespace> --reveal " +
	"(needs the kubernetes.secrets_reveal permission; every reveal is audited)."

// validateSecretReveal refuses --reveal on anything but a read of one
// Secret by name in one namespace. The platform would refuse a reveal on a
// listing anyway (it returns digests); refusing here says so instead of
// printing digests to someone who asked for values. The namespace is
// required because a name read without one spans every namespace, and
// --reveal is a request for exactly one Secret.
func validateSecretReveal(args []string, namespace string, allNamespaces bool, labelSelector string) error {
	const example = "e.g. ankra cluster get secrets db-credentials -n default --reveal"
	if len(args) != 1 {
		return withExitCode(exitUsage, fmt.Errorf(
			"--reveal reads the values of one named Secret and is refused on a listing: pass the Secret's name, %s", example))
	}
	if allNamespaces {
		return withExitCode(exitUsage, errors.New(
			"--reveal reads one Secret and cannot be combined with --all-namespaces (-A): pass its namespace with -n"))
	}
	if labelSelector != "" {
		return withExitCode(exitUsage, errors.New(
			"--reveal reads one named Secret and cannot be combined with a label selector (-l)"))
	}
	if namespace == "" {
		return withExitCode(exitUsage, fmt.Errorf(
			"--reveal needs the Secret's namespace (-n) so it reads exactly one Secret, %s", example))
	}
	return nil
}

// secretValuesMarker returns the first secret_values marker in a response.
// A command here sends one request item, so there is at most one.
func secretValuesMarker(response *client.GetResourcesResponse) string {
	for _, item := range response.ResourceResponses {
		if item.SecretValues != "" {
			return item.SecretValues
		}
	}
	return ""
}

// checkSecretValues turns the platform's secret_values marker into what the
// caller sees, before anything is rendered. Without --reveal it only adds a
// stderr note when structured output is about to print digests. With
// --reveal anything but "revealed" is an error, so a script that asked for
// values never receives digests on stdout as if they were values.
func checkSecretValues(response *client.GetResourcesResponse, items []interface{}, namespace, name, outputFormat string, reveal bool) error {
	marker := secretValuesMarker(response)
	if !reveal {
		if marker == client.SecretValuesWithheld && len(items) > 0 && outputFormat != "table" {
			fmt.Fprintln(os.Stderr, secretDigestNote)
		}
		return nil
	}
	target := namespace + "/" + name
	switch marker {
	case client.SecretValuesRevealed:
		return nil
	case client.SecretValuesPermissionRequired:
		return withExitCode(exitForbidden, fmt.Errorf(
			"not revealed: you do not hold the kubernetes.secrets_reveal permission on this cluster, so the platform "+
				"withheld the values of Secret %s. Its keys and value digests are readable without --reveal. "+
				"The operator, admin and owner roles carry the permission; ask an organisation admin for one of them "+
				"or for a custom role that grants it", target))
	case client.SecretValuesUnavailable:
		return fmt.Errorf(
			"not revealed: the platform could not hand out live values for Secret %s. The cluster may be "+
				"unreachable (the synced cache holds digests only), or the reveal could not be checked or audited. "+
				"Try again when the cluster is connected", target)
	case client.SecretValuesWithheld:
		return fmt.Errorf("not revealed: the platform withheld the values of Secret %s", target)
	}
	if len(items) == 0 {
		return withExitCode(exitNotFound, fmt.Errorf("secret %q not found in namespace %s", name, namespace))
	}
	// No marker on a found Secret. A platform that predates the value
	// policy returned values as they are, but an absent answer is not a
	// confirmed reveal: digests without a marker are refused, and anything
	// else is printed with a note saying the platform did not confirm it.
	if itemsCarrySecretDigests(items) {
		return fmt.Errorf("not revealed: the platform returned digests for Secret %s without saying why", target)
	}
	fmt.Fprintln(os.Stderr, "Note: the platform did not confirm the reveal (it may predate the Secret value policy); "+
		"printing the values it returned.")
	return nil
}

// itemsCarrySecretDigests reports whether any data or stringData value of
// the returned objects has the platform's digest shape.
func itemsCarrySecretDigests(items []interface{}) bool {
	for _, item := range items {
		object, isObject := item.(map[string]interface{})
		if !isObject {
			continue
		}
		for _, field := range []string{"data", "stringData"} {
			values, isMap := object[field].(map[string]interface{})
			if !isMap {
				continue
			}
			for _, value := range values {
				if rendered, isString := value.(string); isString && isSecretValueDigest(rendered) {
					return true
				}
			}
		}
	}
	return false
}

// isSecretValueDigest reports whether a value has the exact shape of the
// platform's withheld Secret value: "sha256:" and 12 lowercase hex
// characters (cluster enginekit/k8sredact SecretValueDigest).
func isSecretValueDigest(value string) bool {
	const prefix = "sha256:"
	const digestLength = 12
	if len(value) != len(prefix)+digestLength || value[:len(prefix)] != prefix {
		return false
	}
	for _, character := range value[len(prefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
