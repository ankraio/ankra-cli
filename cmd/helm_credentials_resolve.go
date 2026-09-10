package cmd

import (
	"errors"
	"fmt"
	"strings"
)

// helmCredentialLookupFailure reports that an id could not be resolved
// because the listing itself was unusable. The cause is wrapped so an
// unauthorised or forbidden listing still classifies into its own exit code.
func helmCredentialLookupFailure(reference string, cause error) error {
	return fmt.Errorf("could not look up the Helm registry credential with id %q: %w - pass the "+
		"credential name to skip the lookup, or run 'ankra helm credentials list' to find it", reference, cause)
}

// resolveHelmCredentialName accepts either a Helm registry credential name or
// its id and returns the name the API addresses it by.
//
// The platform keys /api/v1/org/helm/credentials/{credential_name} by name,
// while `helm credentials list` prints an ID column first, so the obvious next
// command - `helm credentials get <id>` - reached the API with an id in the
// name slot and came back as a bare 404 that read as "credentials are
// write-only" (PLA-825). A canonical uuid is looked up in the listing instead;
// anything else is already a name and goes through unchanged.
func resolveHelmCredentialName(credentials APIClient, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", withExitCode(exitUsage, errors.New("a Helm registry credential name or id is required"))
	}
	if !looksLikeUUID(reference) {
		return reference, nil
	}
	listing, listError := credentials.ListHelmRegistryCredentials()
	if listError != nil {
		return "", helmCredentialLookupFailure(reference, listError)
	}
	for _, credential := range listing.Credentials {
		if strings.EqualFold(credential.ID, reference) {
			return credential.Name, nil
		}
	}
	// A credential whose name happens to have the uuid shape is still
	// addressable by that name: it is only treated as an id while no
	// credential carries it as one.
	for _, credential := range listing.Credentials {
		if credential.Name == reference {
			return reference, nil
		}
	}
	if listing.TotalCount > len(listing.Credentials) {
		// The listing is paged server-side and the client reads its first
		// page only, so a miss on a partial read is not proof of absence.
		return "", helmCredentialLookupFailure(reference, fmt.Errorf(
			"the listing holds %d credentials and only %d were read", listing.TotalCount, len(listing.Credentials)))
	}
	// exitNotFound keeps the id path and the name path telling scripts the
	// same thing: a name that does not exist reaches the API and comes back
	// 404, which exitCodeFor already maps to exitNotFound.
	return "", withExitCode(exitNotFound, fmt.Errorf(
		"no Helm registry credential with id %q - run 'ankra helm credentials list' to see the available credentials", reference))
}
