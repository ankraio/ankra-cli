package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// annotationIgnoresLocalOrganisation marks a command that must reach the API
// without the saved organisation selection: the commands that list, show or
// change the selection itself, so a selection the login no longer belongs to
// can always be inspected and replaced.
const annotationIgnoresLocalOrganisation = "ankra.ignores_local_organisation"

// notOrganisationMemberDetail is the platform's refusal when a request names
// an organisation the caller does not belong to.
const notOrganisationMemberDetail = "Not a member of the requested organisation"

// organisationScopedByLocalSelection is true when this invocation's requests
// carry the organisation saved by `ankra org switch` rather than an explicit
// --org or ANKRA_ORG. An explicit pin is never silently replaced; the saved
// selection is only the default scope.
var organisationScopedByLocalSelection bool

// applyLocalOrganisationSelection sends the saved organisation selection with
// every request. It applies only to the saved `ankra login` token: a token
// passed with --token or ANKRA_API_TOKEN (a service account, a CI token) is
// bound to its own organisation and is not paired with a selection made for a
// different login.
func applyLocalOrganisationSelection(cmd *cobra.Command, tokenSource credentialSource) {
	if tokenSource != sourceConfigFile {
		return
	}
	if cmd.Annotations[annotationIgnoresLocalOrganisation] == "true" {
		return
	}
	selected, err := loadSelectedOrganisation()
	if err != nil {
		return
	}
	selectedID := strings.TrimSpace(selected.OrganisationID)
	if selectedID == "" {
		return
	}
	apiClient.SetOrganisationOverride(selectedID)
	organisationScopedByLocalSelection = true
}

func setIgnoresLocalOrganisation(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[annotationIgnoresLocalOrganisation] = "true"
}

// isOrganisationRefusal reports whether err is a 403 the platform could have
// answered because of the organisation the request named.
func isOrganisationRefusal(err error) bool {
	var permissionDenied *client.PermissionDeniedError
	if errors.As(err, &permissionDenied) {
		return true
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(err, &unexpected) && unexpected.StatusCode == http.StatusForbidden {
		return true
	}
	return strings.Contains(err.Error(), notOrganisationMemberDetail)
}

// organisationSelectionHint explains a refusal caused by a saved selection the
// login does not belong to, or returns "" when the selection is not the cause.
// It runs only on the error path, and asks the API for the login's
// organisations without the selection attached so the answer cannot be the
// same refusal.
func organisationSelectionHint(err error) string {
	if err == nil || !organisationScopedByLocalSelection || apiClient == nil {
		return ""
	}
	if !isOrganisationRefusal(err) {
		return ""
	}
	selectedID := apiClient.OrganisationOverride()
	if selectedID == "" {
		return ""
	}
	label := scopedOrganisationLabel()
	apiClient.SetOrganisationOverride("")
	defer apiClient.SetOrganisationOverride(selectedID)
	organisations, listError := apiClient.ListOrganisations()
	if listError != nil {
		return ""
	}
	for _, organisation := range organisations {
		if organisation.OrganisationID == selectedID {
			return ""
		}
	}
	return fmt.Sprintf("Your selected organisation %s does not match this login: it is not an organisation you belong to.\n"+
		"Run 'ankra org switch <organisation>' to select one ('ankra org list' shows them), or set ANKRA_ORG for a single command.", label)
}

func printOrganisationSelectionHint(out io.Writer, err error) {
	if hint := organisationSelectionHint(err); hint != "" {
		_, _ = fmt.Fprintln(out, hint)
	}
}

// serverCurrentOrganisation returns the organisation the platform marks as the
// user's current one, the pointer the portal moves when the user switches there.
func serverCurrentOrganisation(organisations []client.OrganisationSummary) (client.OrganisationSummary, bool) {
	for _, organisation := range organisations {
		if organisation.UserCurrent {
			return organisation, true
		}
	}
	return client.OrganisationSummary{}, false
}

// organisationScopeWarning explains when the organisation this CLI runs
// commands in differs from the one the platform considers current, or when the
// saved selection names an organisation the login does not belong to. It
// returns "" when there is nothing to warn about.
func organisationScopeWarning(target client.OrganisationSummary, source string, organisations []client.OrganisationSummary) string {
	switch source {
	case "local":
		serverCurrent, found := serverCurrentOrganisation(organisations)
		if !found || serverCurrent.OrganisationID == target.OrganisationID {
			return ""
		}
		return fmt.Sprintf("Warning: the platform's current organisation is %s (the portal's selection), but this CLI runs commands in %s.\n"+
			"Run 'ankra org switch %s' to use the same organisation in both.",
			organisationLabel(serverCurrent), organisationLabel(target), target.OrganisationID)
	case "server":
		selected, err := loadSelectedOrganisation()
		if err != nil || strings.TrimSpace(selected.OrganisationID) == "" {
			return ""
		}
		return fmt.Sprintf("Warning: the saved organisation selection %s is not an organisation you belong to, so commands will be refused.\n"+
			"Run 'ankra org switch <organisation>' to select one.", selected.OrganisationID)
	}
	return ""
}
