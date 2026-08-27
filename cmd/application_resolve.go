package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// maxApplicationLookupPageSize is the largest page_size the applications
// endpoint accepts. It does not clamp an oversized value, it rejects the
// request with a 422, so asking for one big page instead of paging is not an
// option (see maxProfileLookupPageSize for what asking for more cost us
// there).
//
// maxApplicationLookupPages bounds the walk against a backend that keeps
// reporting more pages, matching listAllClusters and listAllHelmRegistries.
const (
	maxApplicationLookupPageSize = 100
	maxApplicationLookupPages    = 100
)

// applicationListingPage is the slice of the applications listing the lookup
// reads: the id/name pairs, and the paging metadata that says whether more
// pages are waiting.
type applicationListingPage struct {
	Result []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
	Pagination struct {
		TotalPages int `json:"total_pages"`
	} `json:"pagination"`
}

// applicationLookupFailure reports that a name could not be resolved because
// the listing itself was unusable.
//
// The failure has to surface here. Only a name reaches the lookup - every
// application id the platform issues is a canonical uuid, and looksLikeUUID
// accepts every one of them - so handing the reference on unchanged would
// put a name in a uuid-typed path parameter and reproduce the bare 500 this
// resolver exists to prevent (PLA-786). The cause is wrapped so an
// unauthorised or forbidden listing still classifies into its own exit code.
func applicationLookupFailure(reference string, cause error) error {
	return fmt.Errorf("could not look up the application named %q: %w - pass the application id "+
		"to skip the lookup, or run 'ankra application list' to find it", reference, cause)
}

// resolveApplicationID accepts either an application id or an application
// name.
//
// `application list` prints a name column, so a name is the obvious thing to
// pass to the per-application commands; before this a name travelled to the
// backend's uuid-typed lookup unchecked and came back as a bare
// 500 Internal Server Error (PLA-786).
func resolveApplicationID(requestContext context.Context, applications APIClient, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		// An empty reference must not reach the lookup: ListApplicationsRaw
		// omits an empty `search`, so the filter disappears and the walk pages
		// the whole organisation. Any application whose definition carries no
		// name then matches the empty reference exactly, and an unset shell
		// variable - `ankra application delete "$APP" --yes` - would act on
		// it. It is a usage error, and it was one before this resolver too.
		return "", withExitCode(exitUsage, errors.New("an application id or name is required"))
	}
	if looksLikeUUID(reference) {
		return reference, nil
	}
	// The server-side `search` filter is a case-insensitive *substring* match
	// (definition->>'name' ILIKE '%reference%') ordered by creation date, so
	// the exact match has no reason to sit on the first page: an org with
	// more than a page of applications whose names contain the reference
	// would otherwise be told its application does not exist. Page until the
	// listing is exhausted, the way resolveClusterID already does.
	exactIDs := []string{}
	foldedIDs := []string{}
	listingExhausted := false
	for page := 1; page <= maxApplicationLookupPages; page++ {
		payload, listError := applications.ListApplicationsRaw(
			requestContext, page, maxApplicationLookupPageSize, reference)
		if listError != nil {
			return "", applicationLookupFailure(reference, listError)
		}
		var listing applicationListingPage
		if unmarshalError := json.Unmarshal(payload, &listing); unmarshalError != nil {
			return "", applicationLookupFailure(reference, unmarshalError)
		}
		// Match exactly first, and keep case variants aside as a fallback for
		// when no exact match exists anywhere in the listing.
		for _, application := range listing.Result {
			switch {
			case application.Name == reference:
				exactIDs = append(exactIDs, application.ID)
			case strings.EqualFold(application.Name, reference):
				foldedIDs = append(foldedIDs, application.ID)
			}
		}
		if listing.Pagination.TotalPages <= page || len(listing.Result) == 0 {
			listingExhausted = true
			break
		}
	}
	if !listingExhausted {
		// Stopping at the cap means the listing was only partly read, and
		// neither answer below can be trusted on partial data: a second
		// application sharing the name could sit past the cap, so a lone match
		// is not provably unique, and "no application named" is not provably
		// absent. Same rule as a failed listing - say so rather than answer
		// from what happened to fit.
		return "", applicationLookupFailure(reference, fmt.Errorf(
			"the application listing did not end within %d pages", maxApplicationLookupPages))
	}
	matchedIDs := exactIDs
	if len(matchedIDs) == 0 {
		matchedIDs = foldedIDs
	}
	switch len(matchedIDs) {
	case 1:
		return matchedIDs[0], nil
	case 0:
		// exitNotFound keeps the name path and the id path telling scripts the
		// same thing: an id that does not exist reaches the API and comes back
		// 404, which exitCodeFor already maps to exitNotFound, so a name that
		// does not exist must not exit with the generic failure code instead.
		return "", withExitCode(exitNotFound, fmt.Errorf(
			"no application named %q - run 'ankra application list' to see the available applications", reference))
	default:
		// An ambiguous name is a bad argument, not a missing application: the
		// invocation has to change before it can succeed.
		return "", withExitCode(exitUsage,
			fmt.Errorf("%d applications are named %q - pass the application id instead (%s)",
				len(matchedIDs), reference, strings.Join(matchedIDs, ", ")))
	}
}

// resolveApplicationArgument resolves a command's leading <application-id>
// argument, which every per-application command also accepts as a name.
func resolveApplicationArgument(command *cobra.Command, arguments []string) (string, error) {
	return resolveApplicationID(command.Context(), apiClient, arguments[0])
}
