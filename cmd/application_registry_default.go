package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// ankraRegistryHost is the host an organisation's own Ankra registry project
// sits on.
//
// It is only ever COMPARED against, never printed and never presented as an
// application's registry. The platform decides an application's default, the
// create response does not report it back (CreateApplicationResponse carries
// an id and errors, nothing more), and a CLI that printed this constant as
// the answer would be asserting as fact something it had not been told - a
// per-organisation registry, a host migration or a non-production environment
// would each make it a confident lie. That is the same shape of defect this
// notice exists to remove, so the notice names the registry by what it IS -
// the organisation's own Ankra registry project - and leaves the host to the
// surfaces that actually read it back (`ankra application list` reports
// container_image_url per application).
const ankraRegistryHost = "registry.ankra.cloud"

// applicationSiblingRegistryLimit bounds the listing the default-registry
// hint reads. The hint is a nudge, not a census: an organisation whose first
// page of applications is all on the Ankra registry is not one where the
// answer changes on page two, and `application add` must not become slow to
// tell the user something it could have left unsaid.
const applicationSiblingRegistryLimit = 100

// siblingRegistryHosts reports the registry hosts other applications of this
// organisation publish to, excluding Ankra's own and excluding the
// application just created.
//
// Strictly best-effort: it runs after the application exists, and every
// failure answers nil. A hint is not worth an error on a command that has
// already succeeded, and a user who cannot list applications is not a user
// this sentence helps.
func siblingRegistryHosts(requestContext context.Context, createdApplicationID string) []string {
	payload, listError := apiClient.ListApplicationsRaw(requestContext, 1, applicationSiblingRegistryLimit, "")
	if listError != nil {
		return nil
	}
	var listing struct {
		Result []struct {
			ID                string `json:"id"`
			ContainerImageURL string `json:"container_image_url"`
		} `json:"result"`
	}
	if unmarshalError := json.Unmarshal(payload, &listing); unmarshalError != nil {
		return nil
	}
	seen := map[string]bool{}
	hosts := []string{}
	for _, application := range listing.Result {
		if application.ID == createdApplicationID {
			continue
		}
		host := registryHostOfImageURL(application.ContainerImageURL)
		if host == "" || host == ankraRegistryHost || seen[host] {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

// registryHostOfImageURL reads the host out of a container image reference,
// with or without an oci:// scheme and with or without a repository path.
func registryHostOfImageURL(imageURL string) string {
	trimmed := strings.TrimSpace(imageURL)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimPrefix(trimmed, "oci://")
	trimmed = strings.TrimPrefix(trimmed, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	host, _, _ := strings.Cut(trimmed, "/")
	return strings.TrimSpace(host)
}

// printDefaultRegistryNotice states the registry an application that declared
// none will publish to, and - when the organisation demonstrably publishes
// somewhere else too - says so before the user finds out from a workflow
// logging into the wrong registry.
//
// The silent default is what PLA-825 reported: `application add` with no
// --registry-url points the application at the organisation's Ankra registry
// project without saying so, and an organisation that also runs its own
// Harbor has no way to see the choice was made. Naming it costs one line;
// not naming it cost a customer a round trip through their own build
// workflow.
//
// The sibling listing is the half that catches the real mistake. An
// organisation with four applications on artifact.example.com and a fifth
// silently on Ankra's registry is not expressing a preference, it is missing
// a flag.
func printDefaultRegistryNotice(command *cobra.Command, applicationID string) {
	output := command.OutOrStdout()
	_, _ = fmt.Fprintln(output,
		"  Registry:   the organisation's own Ankra registry project (the default - no --registry-url was given)")
	hosts := siblingRegistryHosts(command.Context(), applicationID)
	if len(hosts) == 0 {
		return
	}
	// Deliberately NOT "run application registry set": the build workflow is
	// generated from the declaration the application is CREATED with, so a
	// registry declared afterwards can leave a workflow logging in to the
	// wrong one - which would walk the reader into the exact state the next
	// sentence warns about. The flag is the remedy, so the flag is what this
	// names.
	_, _ = fmt.Fprintf(output,
		"\nOther applications in this organisation publish to %s.\n", strings.Join(hosts, ", "))
	_, _ = fmt.Fprintln(output,
		"If this one should too, add it with --registry-url instead: the build workflow is\n"+
			"generated from the declaration the application is created with, so declaring the\n"+
			"registry afterwards can leave a workflow that logs in to the wrong one.")
}
