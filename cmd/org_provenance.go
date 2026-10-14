package cmd

// `ankra org provenance get|set|keys|rotate|revoke` (ankra-q573dh.44): the
// organisation's default provenance mode and its provenance signing keys,
// over /api/v1/org/provenance/{policy,keys,keys/rotate,keys/revoke}.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var orgProvenanceCmd = &cobra.Command{
	Use:   "provenance",
	Short: "Show or change the organisation's deploy provenance default and signing keys",
	Long: `Verified provenance at deploy: a pipeline's publish step signs a statement about
every image it publishes, and a deploy can refuse an image that is not attested.
This command holds the organisation default and the signing keys.

  ankra org provenance get                    the default mode
  ankra org provenance set --mode warn        change it
  ankra org provenance keys                   the signing keys
  ankra org provenance rotate                 start a new key generation
  ankra org provenance revoke <key-id>        stop trusting a key

An application follows this default unless it has its own mode
('ankra application provenance').

` + provenanceModeHelp,
}

func newOrgProvenanceGetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "get",
		Short: "Show the organisation's default provenance mode",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
			defer cancel()
			policy, getError := apiClient.GetOrganisationProvenancePolicy(requestContext)
			if getError != nil {
				return provenanceError(getError, "reading the organisation's deploy provenance default", false)
			}
			if rendered, renderError := renderStructured(command, policy); rendered || renderError != nil {
				return renderError
			}
			printOrganisationProvenancePolicy(command.OutOrStdout(), policy)
			return nil
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func printOrganisationProvenancePolicy(out io.Writer, policy *client.OrganisationProvenancePolicy) {
	_, _ = fmt.Fprintf(out, "  Organisation default: %s\n", provenanceModeLabel(policy.Mode,
		"not set (applications use Ankra's default: off for applications created before 2026-10-14, warn since)"))
	if policy.Mode != nil && *policy.Mode == client.ProvenanceModeEnforce {
		provenanceEnforceCaveat(out, "ankra org provenance set --mode warn")
	}
}

func newOrgProvenanceSetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "set --mode off|warn|enforce",
		Short: "Set the organisation's default provenance mode",
		Long: `Set the mode every application without its own mode runs under, or remove the
default with --clear. Needs the security.manage permission.

` + provenanceModeHelp + `

Start with warn. enforce as an organisation default refuses the deploys of every
application that follows it and whose images are not attested; under enforce,
versions published before an application's first attested publish cannot be
deployed or rolled back to. This command asks before turning enforce on (--yes
skips the question).`,
		Example: `  ankra org provenance set --mode warn
  ankra org provenance set --clear`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			mode, _, modeError := parseProvenanceModeFlags(command, "to remove the default")
			if modeError != nil {
				return modeError
			}
			if mode != nil && *mode == client.ProvenanceModeEnforce {
				yes, _ := command.Flags().GetBool("yes")
				prompt := "Turn enforce on as the organisation default? Every application that follows it then refuses " +
					"unattested images, including rollbacks to versions published before its first attested publish " +
					"(switch to warn to lift that). [y/N]: "
				if confirmError := confirmPrompt(command.InOrStdin(), command.OutOrStdout(), prompt, yes); confirmError != nil {
					return confirmError
				}
			}
			requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
			defer cancel()
			policy, setError := apiClient.SetOrganisationProvenancePolicy(requestContext, mode)
			if setError != nil {
				return provenanceError(setError, "changing the organisation's deploy provenance default", true)
			}
			var disagreement error
			if provenanceModeLabel(policy.Mode, "") != provenanceModeLabel(mode, "") {
				disagreement = withExitCode(exitError, fmt.Errorf(
					"the platform stored the organisation default as %s, not %s as requested; run "+
						"'ankra org provenance get' to check it",
					provenanceModeLabel(policy.Mode, "unset"), provenanceModeLabel(mode, "unset")))
			}
			if rendered, renderError := renderStructured(command, policy); rendered || renderError != nil {
				if renderError != nil {
					return renderError
				}
				return disagreement
			}
			out := command.OutOrStdout()
			_, _ = fmt.Fprintln(out, "Organisation deploy provenance default updated.")
			printOrganisationProvenancePolicy(out, policy)
			return disagreement
		},
	}
	command.Flags().String("mode", "", "Provenance mode: off, warn or enforce")
	command.Flags().Bool("clear", false, "Remove the organisation default")
	command.Flags().Bool("yes", false, "Skip the confirmation asked before turning enforce on")
	registerStructuredOutputFlags(command)
	return command
}

func newOrgProvenanceKeysCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "keys",
		Short: "List the organisation's provenance signing keys",
		Long: `List the organisation's provenance signing keys, newest first. Only public keys
are shown.

  active    signs new attestations and verifies
  trusted   an earlier generation: verifies, no longer signs
  revoked   nothing it signed verifies any more

--public-keys prints the PEM of every key that is not revoked, ready to save as
a file for 'cosign verify-attestation --key'.`,
		Example: `  ankra org provenance keys
  ankra org provenance keys --public-keys > provenance.pub`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
			defer cancel()
			keys, listError := apiClient.ListProvenanceKeys(requestContext)
			if listError != nil {
				return provenanceError(listError, "listing the organisation's provenance keys", false)
			}
			if rendered, renderError := renderStructured(command, keys); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if onlyPEM, _ := command.Flags().GetBool("public-keys"); onlyPEM {
				for _, key := range keys.Keys {
					if key.Status != "revoked" {
						_, _ = fmt.Fprint(out, strings.TrimRight(key.PublicKeyPEM, "\n")+"\n")
					}
				}
				return nil
			}
			if len(keys.Keys) == 0 {
				_, _ = fmt.Fprintln(out, "No provenance key exists yet. The first publish through a pipeline creates one.")
				return nil
			}
			writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(writer, "GENERATION\tKEY ID\tSTATUS\tREVOKED")
			for _, key := range keys.Keys {
				revoked := "-"
				if key.RevokedAt != nil {
					revoked = *key.RevokedAt
				}
				_, _ = fmt.Fprintf(writer, "%d\t%s\t%s\t%s\n", key.Generation, key.KeyID, key.Status, revoked)
			}
			_ = writer.Flush()
			if keys.PredicateType != "" {
				_, _ = fmt.Fprintf(out, "\nAttestation type: %s\n", keys.PredicateType)
			}
			return nil
		},
	}
	command.Flags().Bool("public-keys", false, "Print the PEM of every key that is not revoked instead of the table")
	registerStructuredOutputFlags(command)
	return command
}

func newOrgProvenanceRotateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "rotate",
		Short: "Start a new provenance key generation",
		Long: `Create the next key generation. The next publish signs with it. Earlier
generations stay trusted, so images already published keep verifying. At most
16 generations exist per organisation. Needs the security.manage permission.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
			defer cancel()
			key, rotateError := apiClient.RotateProvenanceKey(requestContext)
			if rotateError != nil {
				return provenanceError(rotateError, "rotating the organisation's provenance key", true)
			}
			if rendered, renderError := renderStructured(command, key); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(),
				"Rotated. Generation %d (%s) is now the signing key. Earlier generations still verify.\n",
				key.Generation, key.KeyID)
			return nil
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newOrgProvenanceRevokeCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "revoke <key-id>",
		Short: "Stop trusting a provenance key",
		Long: `Revoke a key. Nothing it signed verifies any more: an application under enforce
refuses those images until they are published again, which includes rolling back
to them. Revoking the active key makes the next publish create a successor.
Needs the security.manage permission. Use 'ankra org provenance keys' for key ids.`,
		Example: `  ankra org provenance revoke <key-id> --reason "key exposed in a build log"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			keyID := strings.TrimSpace(arguments[0])
			if keyID == "" {
				return withExitCode(exitUsage, errors.New("a key id is required"))
			}
			reason, _ := command.Flags().GetString("reason")
			if len(reason) > 500 {
				return withExitCode(exitUsage, errors.New("--reason is at most 500 characters"))
			}
			yes, _ := command.Flags().GetBool("yes")
			prompt := fmt.Sprintf("Revoke key %s? Nothing it signed will verify any more, so applications under enforce "+
				"refuse those images until they are published again. [y/N]: ", keyID)
			if confirmError := confirmPrompt(command.InOrStdin(), command.OutOrStdout(), prompt, yes); confirmError != nil {
				return confirmError
			}
			requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
			defer cancel()
			key, revokeError := apiClient.RevokeProvenanceKey(requestContext, keyID, reason)
			if revokeError != nil {
				return provenanceError(revokeError, "revoking the provenance key", true)
			}
			if rendered, renderError := renderStructured(command, key); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Revoked generation %d (%s).\n", key.Generation, key.KeyID)
			return nil
		},
	}
	command.Flags().String("reason", "", "Why the key is revoked (recorded in the audit log, at most 500 characters)")
	command.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(command)
	return command
}

func init() {
	orgProvenanceCmd.AddCommand(
		newOrgProvenanceGetCommand(),
		newOrgProvenanceSetCommand(),
		newOrgProvenanceKeysCommand(),
		newOrgProvenanceRotateCommand(),
		newOrgProvenanceRevokeCommand(),
	)
	orgCmd.AddCommand(orgProvenanceCmd)
}
