package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// A deploy that names no image_tag renders the build the platform recorded as
// the application's newest, and that record can hold a commit with no tag: an
// Ankra pipeline records a build by digest first and only adds the tag when
// its publish step runs, so a newer push whose run was superseded or failed
// before publishing leaves the record tagless while the registry still holds
// every image published before it. The platform then refuses the deploy as
// not ready although the portal's version picker, which lists the registry,
// offers those images. The deploy command answers that refusal by deploying
// the tracked branch's newest published build once more, or by naming the
// newest image the registry holds.

// imageTagInputName is the deploy input that selects the image to run.
const imageTagInputName = "image_tag"

// deployTagNotRecordedMarker is part of the platform's fixed sentence for a
// deploy refused because no tag is recorded. The refusal is answered before
// anything is written, so deploying again with a tag is safe.
const deployTagNotRecordedMarker = "has not recorded the tag of a published image"

// isDeployTagNotRecorded reports whether a deploy was refused only because the
// platform has no recorded image tag to default to.
func isDeployTagNotRecorded(deployError error) bool {
	var unexpected *client.UnexpectedResponseError
	return errors.As(deployError, &unexpected) &&
		unexpected.StatusCode == http.StatusConflict &&
		strings.Contains(unexpected.Error(), deployTagNotRecordedMarker)
}

// deployNewestPublishedImage deploys again with image_tag set to the newest
// published build of the application's tracked branch, and says on stderr
// which one once the platform accepted it. An image of another branch is never
// chosen on its own: the refusal then names the newest image the registry
// holds, to pass with --set image_tag. With no registry listing to choose
// from, it answers the original refusal unchanged.
func deployNewestPublishedImage(command *cobra.Command, applicationID string,
	deployRequest client.DeployApplicationRequest, refusal error) (json.RawMessage, error) {
	choice, readError := newestPublishedImageTags(command.Context(), applicationID)
	if readError != nil {
		return nil, refusal
	}
	if choice.TrackedBranchTag == "" {
		if choice.NewestRegistryTag == "" {
			return nil, refusal
		}
		return nil, fmt.Errorf("%w\nThe newest image in the application's registry is %s, but it is not a build of the tracked branch "+
			"Ankra can confirm; pass --set image_tag=%s to deploy it", refusal, choice.NewestRegistryTag, choice.NewestRegistryTag)
	}
	inputs := make(map[string]string, len(deployRequest.Inputs)+1)
	for name, value := range deployRequest.Inputs {
		inputs[name] = value
	}
	inputs[imageTagInputName] = choice.TrackedBranchTag
	deployRequest.Inputs = inputs
	payload, deployError := apiClient.DeployApplication(command.Context(), applicationID, deployRequest)
	if deployError != nil {
		return nil, deployError
	}
	_, _ = fmt.Fprintf(command.ErrOrStderr(),
		"No image tag is recorded for this application's newest build; deployed %s, the newest published build of %s. "+
			"Pass --set image_tag=<tag> to choose another.\n", choice.TrackedBranchTag, choice.TrackedBranch)
	return payload, nil
}

// publishedImageTagChoice is what a deploy without image_tag can fall back
// to: the newest published build of the tracked branch, and the newest image
// the registry holds for every component, either empty when there is none.
type publishedImageTagChoice struct {
	TrackedBranch     string
	TrackedBranchTag  string
	NewestRegistryTag string
}

// newestPublishedImageTags reads the registry's deployable images and the
// tracked branch's builds. A failed branch-builds read leaves TrackedBranchTag
// empty; a failed registry read is an error, since nothing can be chosen.
func newestPublishedImageTags(requestContext context.Context, applicationID string) (publishedImageTagChoice, error) {
	versions, versionsError := apiClient.GetApplicationSecurityVersions(requestContext, applicationID, "")
	if versionsError != nil {
		return publishedImageTagChoice{}, versionsError
	}
	deployable := deployableImageTags(versions)
	if len(deployable) == 0 {
		return publishedImageTagChoice{}, nil
	}
	choice := publishedImageTagChoice{NewestRegistryTag: deployable[0]}
	branchBuilds, branchBuildsError := apiClient.GetApplicationBranchBuilds(requestContext, applicationID)
	if branchBuildsError != nil || branchBuilds.TrackedBranch == nil {
		return choice, nil
	}
	trackedBranch := *branchBuilds.TrackedBranch
	for _, build := range branchBuilds.Builds {
		if build.Branch == trackedBranch && slices.Contains(deployable, build.ImageTag) {
			choice.TrackedBranch = trackedBranch
			choice.TrackedBranchTag = build.ImageTag
			return choice, nil
		}
	}
	return choice, nil
}

// artifactTagPattern matches the tags cosign-style signatures, attestations
// and bills of materials are stored under beside an image; none is an image
// a cluster can run.
var artifactTagPattern = regexp.MustCompile(`^sha256-[a-f0-9]{64}(\.(att|sig|sbom))?$|\.(att|sig|sbom)$`)

// imageTagPattern is the OCI tag grammar.
var imageTagPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)

// deployableImageTags is the portal version picker's list: the tags the
// registry holds for every component of the application, newest push first,
// without "latest" or signature and attestation artefacts. It is empty when
// any component's registry could not be listed, because an unlisted registry
// proves nothing about what is published.
func deployableImageTags(versions *client.ApplicationSecurityVersions) []string {
	if versions == nil || len(versions.Components) == 0 {
		return nil
	}
	for _, component := range versions.Components {
		if component.RegistryStatus != "listed" {
			return nil
		}
	}
	newestPush := map[string]time.Time{}
	componentsByTag := map[string]map[string]bool{}
	var tags []string
	for _, version := range versions.Versions {
		if !slices.Contains(version.Sources, "registry") {
			continue
		}
		tag := version.Tag
		if tag == "latest" || artifactTagPattern.MatchString(tag) || !imageTagPattern.MatchString(tag) {
			continue
		}
		if componentsByTag[tag] == nil {
			componentsByTag[tag] = map[string]bool{}
			tags = append(tags, tag)
		}
		componentsByTag[tag][version.Component] = true
		if version.PushedAt == nil {
			continue
		}
		pushedAt, parseError := time.Parse(time.RFC3339Nano, *version.PushedAt)
		if parseError == nil && pushedAt.After(newestPush[tag]) {
			newestPush[tag] = pushedAt
		}
	}
	deployable := make([]string, 0, len(tags))
	for _, tag := range tags {
		isOnEveryComponent := true
		for _, component := range versions.Components {
			if !componentsByTag[tag][component.Name] {
				isOnEveryComponent = false
				break
			}
		}
		if isOnEveryComponent {
			deployable = append(deployable, tag)
		}
	}
	sort.SliceStable(deployable, func(left int, right int) bool {
		leftPush, rightPush := newestPush[deployable[left]], newestPush[deployable[right]]
		if leftPush.IsZero() || rightPush.IsZero() {
			return !leftPush.IsZero() && rightPush.IsZero()
		}
		return leftPush.After(rightPush)
	})
	return deployable
}
