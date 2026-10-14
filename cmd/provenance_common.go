package cmd

// Shared pieces of the verified-provenance commands (ankra-q573dh.44):
// `ankra application provenance` and `ankra org provenance`. Both read and
// write a mode (off, warn or enforce) and both must say, wherever enforce is
// in play, what enforce costs: a version published before an application's
// first attested publish carries no attestation, so it cannot be deployed or
// rolled back to until the application is switched to warn.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// provenanceModes are the modes a write may name, in the order of strictness.
var provenanceModes = []string{client.ProvenanceModeOff, client.ProvenanceModeWarn, client.ProvenanceModeEnforce}

// provenanceModeHelp is the one-line explanation of each mode, shared by every
// command's help so the three do not drift apart.
const provenanceModeHelp = `Modes:
  off      nothing is checked at deploy, exactly as before
  warn     every image in the application's registry is checked; the deploy
           goes ahead whatever the result, and an image that is not verified
           files a finding on the application's installation
  enforce  the deploy is refused unless every image in the application's
           registry carries a valid attestation from this organisation's
           pipeline, and each verified image is pinned to its digest`

// provenanceEnforceCaveat is printed wherever the effective mode is enforce.
// subject is how the thing is named in the follow-up command, for example
// "ankra application provenance set <application> --mode warn".
func provenanceEnforceCaveat(out io.Writer, switchCommand string) {
	_, _ = fmt.Fprintln(out, "\nUnder enforce a deploy is refused unless every image carries a valid attestation. Versions")
	_, _ = fmt.Fprintln(out, "published before the first attested publish are not attested, so they cannot be deployed or")
	_, _ = fmt.Fprintln(out, "rolled back to. To roll back past that point, switch to warn, deploy, then set enforce again:")
	_, _ = fmt.Fprintf(out, "  %s\n", switchCommand)
}

// parseProvenanceModeFlags reads --mode and --clear. A nil mode with
// clear==true clears the choice. Exactly one of the two is required, so a
// write is always explicit.
func parseProvenanceModeFlags(command *cobra.Command, clearMeaning string) (mode *string, isClear bool, err error) {
	modeValue, _ := command.Flags().GetString("mode")
	isClear, _ = command.Flags().GetBool("clear")
	hasMode := command.Flags().Changed("mode")
	switch {
	case hasMode && isClear:
		return nil, false, withExitCode(exitUsage, errors.New("--mode and --clear cannot be combined"))
	case !hasMode && !isClear:
		return nil, false, withExitCode(exitUsage, fmt.Errorf(
			"--mode off|warn|enforce is required (or --clear %s)", clearMeaning))
	case isClear:
		return nil, true, nil
	}
	normalised := strings.ToLower(strings.TrimSpace(modeValue))
	for _, candidate := range provenanceModes {
		if normalised == candidate {
			return &normalised, false, nil
		}
	}
	return nil, false, withExitCode(exitUsage, fmt.Errorf(
		"--mode %q is not valid: use off, warn or enforce", modeValue))
}

// provenanceModeLabel renders a possibly-unset mode.
func provenanceModeLabel(mode *string, unset string) string {
	if mode == nil {
		return unset
	}
	return *mode
}

// provenanceSourceLabel says where an application's effective mode came from.
func provenanceSourceLabel(source string) string {
	switch source {
	case "application":
		return "this application's own setting"
	case "organisation":
		return "the organisation default"
	case "default":
		return "Ankra's default"
	}
	return source
}

// provenanceError maps a refusal onto what the user can act on: a 403 on a
// write is a missing security.manage (exit 7), a 404 with the platform's own
// sentence is an unknown application or key (exit 3), and the router's bare
// 404 is a platform that predates the feature (exit 1).
func provenanceError(apiError error, operation string, writing bool) error {
	var denied *client.PermissionDeniedError
	var unexpected *client.UnexpectedResponseError
	hasStatus := errors.As(apiError, &unexpected)
	if errors.As(apiError, &denied) || (hasStatus && unexpected.StatusCode == http.StatusForbidden) {
		if writing {
			return withExitCode(exitForbidden, fmt.Errorf(
				"%s: you need the security.manage permission to change deploy provenance settings and keys. "+
					"Ask an organisation admin for a role that has it", operation))
		}
		return withExitCode(exitForbidden, fmt.Errorf("%s: %w", operation, apiError))
	}
	if hasStatus && unexpected.StatusCode == http.StatusNotFound {
		switch unexpected.Detail {
		case "", routeAbsentDetail:
			return withExitCode(exitError, fmt.Errorf(
				"%s: this Ankra platform does not offer verified provenance yet (the route answered 404)", operation))
		default:
			return withExitCode(exitNotFound, fmt.Errorf("%s: %w", operation, apiError))
		}
	}
	if hasStatus && unexpected.StatusCode == http.StatusServiceUnavailable {
		return fmt.Errorf("%s: the provenance keys could not be read right now, try again shortly: %w", operation, apiError)
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}
