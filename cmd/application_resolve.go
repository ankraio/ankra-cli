package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// maxApplicationLookupPageSize is the largest page_size the applications
// endpoint accepts. The `search` filter runs server-side on the name, so one
// page is more than enough to find an exact match (see
// maxProfileLookupPageSize for the history of asking for more).
const maxApplicationLookupPageSize = 100

// resolveApplicationID accepts either an application id or an application
// name.
//
// `application list` prints a name column, so a name is the obvious thing to
// pass to the per-application commands; before this a name travelled to the
// backend's uuid-typed lookup unchecked and came back as a bare
// 500 Internal Server Error (PLA-786).
func resolveApplicationID(requestContext context.Context, applications APIClient, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if looksLikeUUID(reference) {
		return reference, nil
	}
	payload, listError := applications.ListApplicationsRaw(requestContext, 1, maxApplicationLookupPageSize, reference)
	if listError != nil {
		// The lookup is a convenience, not the operation. If listing is
		// unavailable, hand the reference to the API unchanged and let the
		// real call decide - failing here would turn a working id into an
		// error just because the search endpoint was unhappy.
		return reference, nil
	}
	var listing struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if unmarshalError := json.Unmarshal(payload, &listing); unmarshalError != nil {
		return reference, nil
	}
	// The server-side search is a case-insensitive substring filter, so match
	// exactly first and fall back to a case-insensitive match only when no
	// exact one exists.
	exactIDs := []string{}
	foldedIDs := []string{}
	for _, application := range listing.Result {
		if application.Name == reference {
			exactIDs = append(exactIDs, application.ID)
		} else if strings.EqualFold(application.Name, reference) {
			foldedIDs = append(foldedIDs, application.ID)
		}
	}
	matchedIDs := exactIDs
	if len(matchedIDs) == 0 {
		matchedIDs = foldedIDs
	}
	switch len(matchedIDs) {
	case 1:
		return matchedIDs[0], nil
	case 0:
		return "", fmt.Errorf(
			"no application named %q - run 'ankra application list' to see the available applications", reference)
	default:
		return "", fmt.Errorf("%d applications are named %q - pass the application id instead (%s)",
			len(matchedIDs), reference, strings.Join(matchedIDs, ", "))
	}
}

// resolveApplicationArgument resolves a command's leading <application-id>
// argument, which every per-application command also accepts as a name.
func resolveApplicationArgument(command *cobra.Command, arguments []string) (string, error) {
	return resolveApplicationID(command.Context(), apiClient, arguments[0])
}
