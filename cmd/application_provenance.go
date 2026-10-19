package cmd

// `ankra application provenance get|set|verify` (ankra-q573dh.44): the CLI
// over GET/PUT /api/v1/org/applications/{id}/provenance-policy and
// POST .../provenance/verify, the per-application mode of verified provenance
// at deploy (ankra-q573dh.13) and its dry-run.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const provenanceRequestTimeout = 60 * time.Second

func newApplicationProvenanceCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "provenance",
		Short: "Show or change whether an application's deploys verify signed provenance",
		Long: `An application can refuse to deploy an image unless the image carries signed
provenance showing it passed this organisation's pipeline gate. A pipeline's
publish step signs a statement about every image it publishes; the deploy
verifies it before rolling the image.

` + provenanceModeHelp + `

An application's own mode wins; without one it follows the organisation default
('ankra org provenance'), and without that Ankra's default applies.

Third-party images (a public database image, a chart's sidecar) are outside the
application's registry, so they are listed as unchecked and never refused.`,
	}
	command.AddCommand(
		newApplicationProvenanceGetCommand(),
		newApplicationProvenanceSetCommand(),
		newApplicationProvenanceVerifyCommand(),
	)
	return command
}

func newApplicationProvenanceGetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "get <application>",
		Short: "Show the application's effective provenance mode and where it comes from",
		Long: `Show the mode deploys of the application run under, whether it is the
application's own setting, the organisation default or Ankra's default, and what
that mode means for rollbacks.

To see what the next deploy of specific images would do, use
'ankra application provenance verify'.`,
		Example: `  ankra application provenance get shop
  ankra application provenance get 23298741-6a5a-401a-a681-66f31fbdebe1 -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
			defer cancel()
			policy, getError := apiClient.GetApplicationProvenancePolicy(requestContext, applicationID)
			if getError != nil {
				return provenanceError(getError, "reading deploy provenance for the application", false)
			}
			if rendered, renderError := renderStructured(command, policy); rendered || renderError != nil {
				return renderError
			}
			printApplicationProvenancePolicy(command.OutOrStdout(), arguments[0], policy)
			return nil
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newApplicationProvenanceSetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "set <application> --mode off|warn|enforce",
		Short: "Set the application's provenance mode",
		Long: `Set the mode deploys of the application run under, or clear the application's
own choice with --clear so it follows the organisation default again. Needs the
security.manage permission.

` + provenanceModeHelp + `

Turn warn on first and read the findings: an application whose images are built
outside Ankra pipelines is unsigned, and enforce would refuse every deploy of
it. Set enforce once the images you would deploy, and the rollback targets you
might need, were published by a pipeline run that attests them.

Under enforce, versions published before the first attested publish cannot be
deployed or rolled back to. Switching to warn lifts that; this command asks
before turning enforce on (--yes skips the question).`,
		Example: `  ankra application provenance set shop --mode warn
  ankra application provenance set shop --mode enforce --yes
  ankra application provenance set shop --clear`,
		Args: cobra.ExactArgs(1),
		RunE: runApplicationProvenanceSet,
	}
	command.Flags().String("mode", "", "Provenance mode: off, warn or enforce")
	command.Flags().Bool("clear", false, "Clear the application's own mode so it follows the organisation default")
	command.Flags().Bool("yes", false, "Skip the confirmation asked before turning enforce on")
	registerStructuredOutputFlags(command)
	return command
}

func runApplicationProvenanceSet(command *cobra.Command, arguments []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	mode, _, modeError := parseProvenanceModeFlags(command, "to follow the organisation default")
	if modeError != nil {
		return modeError
	}
	applicationID, resolveError := resolveApplicationArgument(command, arguments)
	if resolveError != nil {
		return resolveError
	}
	if mode != nil && *mode == client.ProvenanceModeEnforce {
		yes, _ := command.Flags().GetBool("yes")
		prompt := fmt.Sprintf("Turn enforce on for %q? Deploys are then refused unless every image is attested, and "+
			"versions published before the first attested publish cannot be deployed or rolled back to "+
			"(switch to warn to lift that). [y/N]: ", arguments[0])
		if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), prompt, yes); confirmError != nil {
			return confirmError
		}
	}
	requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
	defer cancel()
	policy, setError := apiClient.SetApplicationProvenancePolicy(requestContext, applicationID, mode)
	if setError != nil {
		return provenanceError(setError, "changing deploy provenance for the application", true)
	}
	// A stored state that disagrees with the request is a change that did not
	// take effect; report it and exit non-zero so a script does not read it as
	// the mode having moved.
	var disagreement error
	if provenanceModeLabel(policy.ApplicationMode, "") != provenanceModeLabel(mode, "") {
		disagreement = withExitCode(exitError, fmt.Errorf(
			"the platform stored the application's provenance mode as %s, not %s as requested; run "+
				"'ankra application provenance get %s' to check it",
			provenanceModeLabel(policy.ApplicationMode, "unset"), provenanceModeLabel(mode, "unset"), arguments[0]))
	}
	if rendered, renderError := renderStructured(command, policy); rendered || renderError != nil {
		if renderError != nil {
			return renderError
		}
		return disagreement
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Deploy provenance updated for %q.\n\n", arguments[0])
	printApplicationProvenancePolicy(out, arguments[0], policy)
	return disagreement
}

func printApplicationProvenancePolicy(out io.Writer, reference string, policy *client.ApplicationProvenancePolicy) {
	_, _ = fmt.Fprintf(out, "  Effective mode:     %s (%s)\n", policy.Mode, provenanceSourceLabel(policy.Source))
	_, _ = fmt.Fprintf(out, "  Application's own:  %s\n", provenanceModeLabel(policy.ApplicationMode, "not set"))
	_, _ = fmt.Fprintf(out, "  Organisation:       %s\n", provenanceModeLabel(policy.OrganisationMode, "no default set"))
	if policy.Mode == client.ProvenanceModeEnforce {
		provenanceEnforceCaveat(out, fmt.Sprintf("ankra application provenance set %s --mode warn", reference))
	}
}

func newApplicationProvenanceVerifyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "verify <application> --image <ref> [--image <ref>...]",
		Short: "Dry-run: check images against the application's provenance mode",
		Long: `Verify images the way a deploy would and report what a deploy under the
application's mode would do, without deploying anything or filing a finding.

Each image is checked for a signed attestation from this organisation's
pipeline: that it exists, that its signature verifies against a key the
organisation trusts, and that it names this application, repository and digest.
Images outside the application's registry are listed as unchecked and are never
refused. An image that could not be checked (the registry or key store did not
answer) is reported as unknown, never as refused.

The decision is one of: deploy, deploy_with_finding (warn, an image is not
verified), refuse (enforce, an image is not verified) or not_checked (off).
The command exits 1 when the decision is refuse, so it can gate a script.`,
		Example: `  ankra application provenance verify shop --image registry.ankra.cloud/org-1234/shop:sha-1a2b3c4
  ankra application provenance verify shop --image <current> --image <rollback target> -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			images, _ := command.Flags().GetStringArray("image")
			cleaned := make([]string, 0, len(images))
			for _, image := range images {
				if trimmed := strings.TrimSpace(image); trimmed != "" {
					cleaned = append(cleaned, trimmed)
				}
			}
			if len(cleaned) == 0 || len(cleaned) > 32 {
				return withExitCode(exitUsage, errors.New("pass between 1 and 32 images with --image"))
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			requestContext, cancel := context.WithTimeout(command.Context(), provenanceRequestTimeout)
			defer cancel()
			verification, verifyError := apiClient.VerifyApplicationProvenance(requestContext, applicationID, cleaned)
			if verifyError != nil {
				return provenanceError(verifyError, "verifying images for the application", false)
			}
			refused := verification.Decision == "refuse"
			var refusal error
			if refused {
				refusal = withExitCode(exitError, errors.New("a deploy of these images would be refused"))
			}
			if rendered, renderError := renderStructured(command, verification); rendered || renderError != nil {
				if renderError != nil {
					return renderError
				}
				return refusal
			}
			printProvenanceVerification(command.OutOrStdout(), arguments[0], verification)
			return refusal
		},
	}
	command.Flags().StringArray("image", nil, "Image reference to verify (repeatable, 1 to 32)")
	registerStructuredOutputFlags(command)
	return command
}

func provenanceDecisionSentence(decision string) string {
	switch decision {
	case "deploy":
		return "a deploy would go ahead"
	case "deploy_with_finding":
		return "a deploy would go ahead and file a finding (warn)"
	case "refuse":
		return "a deploy would be REFUSED (enforce)"
	case "not_checked":
		return "a deploy would not check provenance (off)"
	}
	return decision
}

func printProvenanceVerification(out io.Writer, reference string, verification *client.ProvenanceVerification) {
	_, _ = fmt.Fprintf(out, "Mode: %s\nDecision: %s\n\n", verification.Mode, provenanceDecisionSentence(verification.Decision))
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "IMAGE\tSTATE\tREASON\tDETAIL")
	for _, image := range verification.Verdict.Images {
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", image.Image, image.State, image.Reason, image.Message)
	}
	_ = writer.Flush()
	for _, unchecked := range verification.Verdict.UncheckedImages {
		_, _ = fmt.Fprintf(out, "  not checked (outside the application's registry): %s\n", unchecked)
	}
	if verification.Mode == client.ProvenanceModeEnforce && verification.Decision == "refuse" {
		_, _ = fmt.Fprintln(out, "\nUnder enforce, an image published before the first attested publish cannot be deployed or rolled back to.")
		_, _ = fmt.Fprintf(out, "To deploy it anyway, switch to warn, deploy, then set enforce again:\n  ankra application provenance set %s --mode warn\n", reference)
	}
}
