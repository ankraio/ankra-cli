package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"ankra/internal/client"
)

const testTagNotRecordedRefusal = "Ankra has not recorded the tag of a published image for this application " +
	"yet, so a deploy without image_tag has no image to run."

type deployDefaultTagMock struct {
	baseMock
	refuseFirstDeploy bool
	deployRequests    []client.DeployApplicationRequest
	versions          *client.ApplicationSecurityVersions
	versionsError     error
	branchBuilds      *client.ApplicationBranchBuilds
	branchBuildsError error
}

func (mock *deployDefaultTagMock) DeployApplication(_ context.Context, _ string, deployRequest client.DeployApplicationRequest) (json.RawMessage, error) {
	mock.deployRequests = append(mock.deployRequests, deployRequest)
	if mock.refuseFirstDeploy && len(mock.deployRequests) == 1 {
		return nil, client.NewUnexpectedResponseError(409, testTagNotRecordedRefusal)
	}
	return json.RawMessage(`{"status":"deploying"}`), nil
}

func (mock *deployDefaultTagMock) GetApplicationSecurityVersions(context.Context, string, string) (*client.ApplicationSecurityVersions, error) {
	return mock.versions, mock.versionsError
}

func (mock *deployDefaultTagMock) GetApplicationBranchBuilds(context.Context, string) (*client.ApplicationBranchBuilds, error) {
	return mock.branchBuilds, mock.branchBuildsError
}

func registryVersion(tag string, pushedAt string) client.ApplicationImageVersion {
	return client.ApplicationImageVersion{Component: "web", Tag: tag, PushedAt: &pushedAt, Sources: []string{"registry"}}
}

func listedVersions(versions ...client.ApplicationImageVersion) *client.ApplicationSecurityVersions {
	return &client.ApplicationSecurityVersions{
		Components: []client.ApplicationImageVersionComponent{{Name: "web", RegistryStatus: "listed"}},
		Versions:   versions,
	}
}

func TestDeployWithoutRecordedTagDeploysTrackedBranchBuild(t *testing.T) {
	trackedBranch := "main"
	mock := &deployDefaultTagMock{
		refuseFirstDeploy: true,
		versions: listedVersions(
			registryVersion("sha-932b4da", "2026-11-02T09:00:00Z"),
			registryVersion("sha-60c938b", "2026-11-02T07:00:00Z"),
		),
		branchBuilds: &client.ApplicationBranchBuilds{TrackedBranch: &trackedBranch, Builds: []client.ApplicationBranchBuild{
			{Branch: "develop", ImageTag: "sha-932b4da"},
			{Branch: "main", ImageTag: "sha-60c938b"},
		}},
	}
	output, executeError := runApplicationCommand(t, mock, "deploy", testApplicationID, "--cluster", testClusterID)
	if executeError != nil {
		t.Fatalf("deploy error = %v", executeError)
	}
	if len(mock.deployRequests) != 2 {
		t.Fatalf("deploy calls = %d, want 2", len(mock.deployRequests))
	}
	if tag := mock.deployRequests[1].Inputs[imageTagInputName]; tag != "sha-60c938b" {
		t.Errorf("retried image_tag = %q, want sha-60c938b", tag)
	}
	if !strings.Contains(output, "deploying sha-60c938b, the newest published build of main") {
		t.Errorf("output does not name the chosen image: %q", output)
	}
}

func TestDeployWithoutRecordedTagFallsBackToNewestRegistryImage(t *testing.T) {
	mock := &deployDefaultTagMock{
		refuseFirstDeploy: true,
		versions: listedVersions(
			registryVersion("sha-60c938b", "2026-11-02T07:00:00Z"),
			registryVersion("sha-932b4da", "2026-11-02T09:00:00Z"),
		),
		branchBuildsError: errors.New("not found"),
	}
	if _, executeError := runApplicationCommand(t, mock, "deploy", testApplicationID, "--cluster", testClusterID,
		"--set", "replicas=2"); executeError != nil {
		t.Fatalf("deploy error = %v", executeError)
	}
	retried := mock.deployRequests[1].Inputs
	if retried[imageTagInputName] != "sha-932b4da" || retried["replicas"] != "2" {
		t.Errorf("retried inputs = %v, want image_tag sha-932b4da and replicas 2", retried)
	}
}

func TestDeployWithoutPublishedImageKeepsRefusal(t *testing.T) {
	mock := &deployDefaultTagMock{refuseFirstDeploy: true, versions: listedVersions()}
	_, executeError := runApplicationCommand(t, mock, "deploy", testApplicationID, "--cluster", testClusterID)
	if executeError == nil || !strings.Contains(executeError.Error(), "has not recorded the tag") {
		t.Fatalf("deploy error = %v, want the platform's refusal", executeError)
	}
	if len(mock.deployRequests) != 1 {
		t.Errorf("deploy calls = %d, want 1", len(mock.deployRequests))
	}
}

func TestDeployWithPinnedTagIsNotRetried(t *testing.T) {
	mock := &deployDefaultTagMock{refuseFirstDeploy: true, versions: listedVersions(registryVersion("sha-1", "2026-11-02T07:00:00Z"))}
	_, executeError := runApplicationCommand(t, mock, "deploy", testApplicationID, "--cluster", testClusterID,
		"--set", "image_tag=sha-2")
	if executeError == nil {
		t.Fatal("deploy with a pinned tag succeeded after a refusal; want the refusal")
	}
	if len(mock.deployRequests) != 1 {
		t.Errorf("deploy calls = %d, want 1", len(mock.deployRequests))
	}
}

func TestDeployableImageTagsMatchesThePortalPicker(t *testing.T) {
	newer := registryVersion("sha-b", "2026-11-02T09:00:00Z")
	versions := &client.ApplicationSecurityVersions{
		Components: []client.ApplicationImageVersionComponent{
			{Name: "web", RegistryStatus: "listed"}, {Name: "worker", RegistryStatus: "listed"},
		},
		Versions: []client.ApplicationImageVersion{
			registryVersion("sha-a", "2026-11-02T07:00:00Z"),
			{Component: "worker", Tag: "sha-a", Sources: []string{"registry"}},
			newer,
			{Component: "worker", Tag: "sha-b", Sources: []string{"registry"}},
			registryVersion("sha-only-web", "2026-11-02T10:00:00Z"),
			registryVersion("latest", "2026-11-02T11:00:00Z"),
			{Component: "worker", Tag: "latest", Sources: []string{"registry"}},
			registryVersion("sha256-"+strings.Repeat("a", 64)+".sig", "2026-11-02T11:00:00Z"),
			{Component: "web", Tag: "sha-scan-only", Sources: []string{"findings"}},
			{Component: "worker", Tag: "sha-scan-only", Sources: []string{"findings"}},
		},
	}
	if got, want := deployableImageTags(versions), []string{"sha-b", "sha-a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deployable tags = %v, want %v", got, want)
	}
	versions.Components[1].RegistryStatus = "unavailable"
	if got := deployableImageTags(versions); len(got) != 0 {
		t.Errorf("deployable tags with an unlisted registry = %v, want none", got)
	}
}
