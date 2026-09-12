package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// ankraRegistryHost is the host every organisation's own Ankra registry
// project sits on. It is compared against, never constructed: the platform
// decides an application's default registry, and this only needs to tell
// "the Ankra one" apart from "a registry the organisation operates".
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
	_, _ = fmt.Fprintf(output, "  Registry:   %s (Ankra's own, the default - no --registry-url was given)\n",
		ankraRegistryHost)
	hosts := siblingRegistryHosts(command.Context(), applicationID)
	if len(hosts) == 0 {
		return
	}
	_, _ = fmt.Fprintf(output,
		"\nOther applications in this organisation publish to %s.\n", strings.Join(hosts, ", "))
	_, _ = fmt.Fprintln(output,
		"If this one should too, declare it now, before the setup pull request is generated:")
	_, _ = fmt.Fprintf(output,
		"  ankra application registry set %s --url oci://<host>/<project> --credential <name>\n", applicationID)
	_, _ = fmt.Fprintln(output,
		"The build workflow is generated from the declaration the application is created with,\n"+
			"so a registry added after that leaves a workflow logging in to the wrong one.")
}
