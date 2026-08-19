package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// stackProfileManageMock records the owner-lifecycle calls the new commands
// make and answers each with a fixed payload.
type stackProfileManageMock struct {
	baseMock
	payload json.RawMessage

	createRequest      *client.CreateStackProfileFromStackRequest
	updateProfileID    string
	updateRequest      *client.UpdateStackProfileRequest
	deleteCalls        int
	saveRequest        *client.SaveStackProfileVersionRequest
	currentVersion     int
	versionRequested   int
	diffFrom           int
	diffTo             int
	instantiationCalls int
	shareSlug          string
	shareRemovedID     string
	shareListCalls     int
	approveRequest     *client.ApproveStackProfileSuggestionRequest
	rejectNote         string
	withdrawCalls      int
	launchRequest      *client.LaunchStackProfileDemoRequest
	demoStopped        string
	logo               *client.StackProfileLogo
	logoPutType        string
	logoClearCalls     int
	validatedDraftID   string
	rebaseRequest      *client.RebaseStackProfileDraftRequest
	suggestionTitle    string
}

func (mock *stackProfileManageMock) answer() (json.RawMessage, error) {
	if mock.payload != nil {
		return mock.payload, nil
	}
	return json.RawMessage(`{"ok":true}`), nil
}

func (mock *stackProfileManageMock) CreateStackProfile(requestContext context.Context, createRequest client.CreateStackProfileFromStackRequest) (json.RawMessage, error) {
	mock.createRequest = &createRequest
	return mock.answer()
}

func (mock *stackProfileManageMock) UpdateStackProfile(requestContext context.Context, profileID string, updateRequest client.UpdateStackProfileRequest) (json.RawMessage, error) {
	mock.updateProfileID = profileID
	mock.updateRequest = &updateRequest
	return mock.answer()
}

func (mock *stackProfileManageMock) DeleteStackProfile(requestContext context.Context, profileID string) (json.RawMessage, error) {
	mock.deleteCalls++
	return mock.answer()
}

func (mock *stackProfileManageMock) SaveStackProfileVersion(requestContext context.Context, profileID string, saveRequest client.SaveStackProfileVersionRequest) (json.RawMessage, error) {
	mock.saveRequest = &saveRequest
	return mock.answer()
}

func (mock *stackProfileManageMock) SetStackProfileCurrentVersion(requestContext context.Context, profileID string, version int) (json.RawMessage, error) {
	mock.currentVersion = version
	return mock.answer()
}

func (mock *stackProfileManageMock) GetStackProfileVersion(requestContext context.Context, profileID string, version int) (json.RawMessage, error) {
	mock.versionRequested = version
	return mock.answer()
}

func (mock *stackProfileManageMock) DiffStackProfileVersions(requestContext context.Context, profileID string, fromVersion int, toVersion int) (json.RawMessage, error) {
	mock.diffFrom = fromVersion
	mock.diffTo = toVersion
	return mock.answer()
}

func (mock *stackProfileManageMock) ListStackProfileInstantiations(requestContext context.Context, profileID string) (json.RawMessage, error) {
	mock.instantiationCalls++
	return mock.answer()
}

func (mock *stackProfileManageMock) ListStackProfileShares(requestContext context.Context, profileID string) (json.RawMessage, error) {
	mock.shareListCalls++
	return mock.answer()
}

func (mock *stackProfileManageMock) CreateStackProfileShare(requestContext context.Context, profileID string, organisationSlug string) (json.RawMessage, error) {
	mock.shareSlug = organisationSlug
	return mock.answer()
}

func (mock *stackProfileManageMock) DeleteStackProfileShare(requestContext context.Context, profileID string, shareID string) (json.RawMessage, error) {
	mock.shareRemovedID = shareID
	return mock.answer()
}

func (mock *stackProfileManageMock) ListStackProfileSuggestions(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return mock.answer()
}

func (mock *stackProfileManageMock) GetStackProfileSuggestion(requestContext context.Context, profileID string, suggestionID string) (json.RawMessage, error) {
	return mock.answer()
}

func (mock *stackProfileManageMock) ApproveStackProfileSuggestion(requestContext context.Context, profileID string, suggestionID string, approveRequest client.ApproveStackProfileSuggestionRequest) (json.RawMessage, error) {
	mock.approveRequest = &approveRequest
	return mock.answer()
}

func (mock *stackProfileManageMock) RejectStackProfileSuggestion(requestContext context.Context, profileID string, suggestionID string, note string) (json.RawMessage, error) {
	mock.rejectNote = note
	return mock.answer()
}

func (mock *stackProfileManageMock) WithdrawStackProfileSuggestion(requestContext context.Context, profileID string, suggestionID string) (json.RawMessage, error) {
	mock.withdrawCalls++
	return mock.answer()
}

func (mock *stackProfileManageMock) ListStackProfileDemos(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return mock.answer()
}

func (mock *stackProfileManageMock) LaunchStackProfileDemo(requestContext context.Context, profileID string, launchRequest client.LaunchStackProfileDemoRequest) (json.RawMessage, error) {
	mock.launchRequest = &launchRequest
	return mock.answer()
}

func (mock *stackProfileManageMock) StopStackProfileDemo(requestContext context.Context, profileID string, workspaceID string) (json.RawMessage, error) {
	mock.demoStopped = workspaceID
	return mock.answer()
}

func (mock *stackProfileManageMock) GetStackProfileLogo(requestContext context.Context, profileID string) (*client.StackProfileLogo, error) {
	return mock.logo, nil
}

func (mock *stackProfileManageMock) PutStackProfileLogo(requestContext context.Context, profileID string, content []byte, contentType string) (json.RawMessage, error) {
	mock.logoPutType = contentType
	return mock.answer()
}

func (mock *stackProfileManageMock) DeleteStackProfileLogo(requestContext context.Context, profileID string) (json.RawMessage, error) {
	mock.logoClearCalls++
	return mock.answer()
}

func (mock *stackProfileManageMock) ValidateStackProfileDraft(requestContext context.Context, draftID string) (json.RawMessage, error) {
	mock.validatedDraftID = draftID
	return mock.answer()
}

func (mock *stackProfileManageMock) RebaseStackProfileDraft(requestContext context.Context, draftID string, rebaseRequest client.RebaseStackProfileDraftRequest) (json.RawMessage, error) {
	mock.rebaseRequest = &rebaseRequest
	return mock.answer()
}

func (mock *stackProfileManageMock) SubmitStackProfileSuggestion(requestContext context.Context, draftID string, title string) (json.RawMessage, error) {
	mock.suggestionTitle = title
	return mock.answer()
}

// resetStackProfileCommandFlags returns package-var commands' sticky flags
// (including slice flags) to their defaults between tests.
func resetStackProfileCommandFlags(t *testing.T, commands ...*cobra.Command) {
	t.Helper()
	for _, command := range commands {
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
				_ = sliceValue.Replace([]string{})
			} else {
				_ = flag.Value.Set(flag.DefValue)
			}
			flag.Changed = false
		})
	}
}

func runStackProfilesCommand(t *testing.T, mock APIClient, input string, arguments ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	var output bytes.Buffer
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&output)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"stack-profiles"}, arguments...))
	executeError := rootCmd.Execute()
	return output.String(), executeError
}

func TestStackProfilesCreateRequiresNameAndStack(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesCreateCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "create"); executeError == nil {
		t.Fatal("expected an error without --name")
	}
	if _, executeError := runStackProfilesCommand(t, mock, "", "create", "--name", "postgres-ha"); executeError == nil {
		t.Fatal("expected an error without --stack")
	}
	if mock.createRequest != nil {
		t.Fatal("expected no create call on usage errors")
	}
}

func TestStackProfilesCreateMapsRequest(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesCreateCmd)
	mock := &stackProfileManageMock{}
	_, executeError := runStackProfilesCommand(t, mock, "", "create",
		"--name", "postgres-ha", "--stack", "postgres", "--cluster", testClusterUUID,
		"--description", "Production PostgreSQL", "--category", "database",
		"--tag", "postgresql", "--tag", "ha", "--visibility", "public",
		"--changelog", "first cut")
	if executeError != nil {
		t.Fatalf("create failed: %v", executeError)
	}
	request := mock.createRequest
	if request == nil {
		t.Fatal("expected a create call")
	}
	if request.Name != "postgres-ha" || request.StackName != "postgres" ||
		request.SourceClusterID != testClusterUUID || request.Category != "database" {
		t.Errorf("request = %+v", request)
	}
	if request.Description == nil || *request.Description != "Production PostgreSQL" {
		t.Errorf("description = %v", request.Description)
	}
	if request.Visibility == nil || *request.Visibility != "public" {
		t.Errorf("visibility = %v", request.Visibility)
	}
	if len(request.Tags) != 2 || request.Tags[0] != "postgresql" || request.Tags[1] != "ha" {
		t.Errorf("tags = %v", request.Tags)
	}
	if !request.IncludeAddonConfigurations {
		t.Error("expected include_addon_configurations to default true")
	}
}

func TestStackProfilesUpdateSendsOnlyChangedFields(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesUpdateCmd)
	mock := &stackProfileManageMock{}
	_, executeError := runStackProfilesCommand(t, mock, "", "update", "profile-1",
		"--description", "Updated", "--visibility", "organisation")
	if executeError != nil {
		t.Fatalf("update failed: %v", executeError)
	}
	request := mock.updateRequest
	if request == nil {
		t.Fatal("expected an update call")
	}
	if request.Description == nil || *request.Description != "Updated" {
		t.Errorf("description = %v", request.Description)
	}
	if request.Visibility == nil || *request.Visibility != "organisation" {
		t.Errorf("visibility = %v", request.Visibility)
	}
	if request.Name != nil || request.Category != nil || request.LogoURL != nil || request.Tags != nil {
		t.Errorf("unset fields must stay nil: %+v", request)
	}
}

func TestStackProfilesUpdateWithoutFlagsIsUsageError(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesUpdateCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "update", "profile-1"); executeError == nil {
		t.Fatal("expected a usage error with no flags")
	}
	if mock.updateRequest != nil {
		t.Fatal("expected no update call")
	}
}

func TestStackProfilesDeleteConfirmation(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesDeleteCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "n\n", "delete", "profile-1"); executeError == nil {
		t.Fatal("expected the declined confirmation to error")
	}
	if mock.deleteCalls != 0 {
		t.Fatalf("expected no delete call on decline, got %d", mock.deleteCalls)
	}
	resetStackProfileCommandFlags(t, stackProfilesDeleteCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "", "delete", "profile-1", "--yes"); executeError != nil {
		t.Fatalf("delete --yes failed: %v", executeError)
	}
	if mock.deleteCalls != 1 {
		t.Fatalf("expected one delete call, got %d", mock.deleteCalls)
	}
}

func TestStackProfilesSaveVersionMapsRequest(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesSaveVersionCmd)
	mock := &stackProfileManageMock{}
	_, executeError := runStackProfilesCommand(t, mock, "", "save-version", "profile-1",
		"--stack", "postgres", "--cluster", testClusterUUID,
		"--channel", "beta", "--changelog", "chart bump")
	if executeError != nil {
		t.Fatalf("save-version failed: %v", executeError)
	}
	request := mock.saveRequest
	if request == nil {
		t.Fatal("expected a save-version call")
	}
	if request.StackName != "postgres" || request.SourceClusterID != testClusterUUID ||
		request.Channel != "beta" {
		t.Errorf("request = %+v", request)
	}
	if request.Changelog == nil || *request.Changelog != "chart bump" {
		t.Errorf("changelog = %v", request.Changelog)
	}
}

func TestStackProfilesSetCurrentVersionParsesPrefixedVersion(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesSetCurrentVersionCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "set-current-version", "profile-1", "v3"); executeError != nil {
		t.Fatalf("set-current-version failed: %v", executeError)
	}
	if mock.currentVersion != 3 {
		t.Errorf("version = %d, want 3", mock.currentVersion)
	}
	if _, executeError := runStackProfilesCommand(t, mock, "", "set-current-version", "profile-1", "abc"); executeError == nil {
		t.Fatal("expected an error for a malformed version")
	}
}

func TestStackProfilesDiffRequiresRange(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesDiffCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "diff", "profile-1"); executeError == nil {
		t.Fatal("expected an error without --from/--to")
	}
	resetStackProfileCommandFlags(t, stackProfilesDiffCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "", "diff", "profile-1",
		"--from", "v1", "--to", "2"); executeError != nil {
		t.Fatalf("diff failed: %v", executeError)
	}
	if mock.diffFrom != 1 || mock.diffTo != 2 {
		t.Errorf("diff range = %d..%d, want 1..2", mock.diffFrom, mock.diffTo)
	}
}

func TestStackProfilesDeploymentsCallsInstantiations(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesDeploymentsCmd)
	mock := &stackProfileManageMock{payload: json.RawMessage(`{"result":[]}`)}
	output, executeError := runStackProfilesCommand(t, mock, "", "deployments", "profile-1")
	if executeError != nil {
		t.Fatalf("deployments failed: %v", executeError)
	}
	if mock.instantiationCalls != 1 {
		t.Fatalf("expected one instantiations call, got %d", mock.instantiationCalls)
	}
	if !strings.Contains(output, `"result"`) {
		t.Errorf("output = %q", output)
	}
}

func TestStackProfilesShareLifecycle(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesShareListCmd, stackProfilesShareAddCmd, stackProfilesShareRemoveCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "share", "add", "profile-1", "acme-corp"); executeError != nil {
		t.Fatalf("share add failed: %v", executeError)
	}
	if mock.shareSlug != "acme-corp" {
		t.Errorf("slug = %q", mock.shareSlug)
	}
	if _, executeError := runStackProfilesCommand(t, mock, "", "share", "list", "profile-1"); executeError != nil {
		t.Fatalf("share list failed: %v", executeError)
	}
	if mock.shareListCalls != 1 {
		t.Errorf("share list calls = %d", mock.shareListCalls)
	}
	if _, executeError := runStackProfilesCommand(t, mock, "", "share", "remove", "profile-1", "share-9"); executeError != nil {
		t.Fatalf("share remove failed: %v", executeError)
	}
	if mock.shareRemovedID != "share-9" {
		t.Errorf("removed share = %q", mock.shareRemovedID)
	}
}

func TestStackProfilesSuggestionVerbs(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesSuggestionsApproveCmd, stackProfilesSuggestionsRejectCmd,
		stackProfilesSuggestionsWithdrawCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "suggestions", "approve",
		"profile-1", "suggestion-1", "--changelog", "From the community"); executeError != nil {
		t.Fatalf("approve failed: %v", executeError)
	}
	if mock.approveRequest == nil || mock.approveRequest.Channel != "stable" ||
		mock.approveRequest.Changelog == nil || *mock.approveRequest.Changelog != "From the community" {
		t.Errorf("approve request = %+v", mock.approveRequest)
	}
	if _, executeError := runStackProfilesCommand(t, mock, "", "suggestions", "reject",
		"profile-1", "suggestion-1", "--note", "Conflicts with naming"); executeError != nil {
		t.Fatalf("reject failed: %v", executeError)
	}
	if mock.rejectNote != "Conflicts with naming" {
		t.Errorf("reject note = %q", mock.rejectNote)
	}
	if _, executeError := runStackProfilesCommand(t, mock, "", "suggestions", "withdraw",
		"profile-1", "suggestion-1"); executeError != nil {
		t.Fatalf("withdraw failed: %v", executeError)
	}
	if mock.withdrawCalls != 1 {
		t.Errorf("withdraw calls = %d", mock.withdrawCalls)
	}
}

func TestStackProfilesDemoLaunchSendsOnlyChangedFields(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesDemoLaunchCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "demo", "launch", "profile-1"); executeError != nil {
		t.Fatalf("demo launch failed: %v", executeError)
	}
	if mock.launchRequest == nil || mock.launchRequest.Version != nil || mock.launchRequest.TTLHours != nil {
		t.Errorf("default launch request = %+v", mock.launchRequest)
	}
	resetStackProfileCommandFlags(t, stackProfilesDemoLaunchCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "", "demo", "launch", "profile-1",
		"--version", "v2", "--ttl-hours", "4", "--set", "replicas=1"); executeError != nil {
		t.Fatalf("demo launch with flags failed: %v", executeError)
	}
	request := mock.launchRequest
	if request.Version == nil || *request.Version != 2 || request.TTLHours == nil || *request.TTLHours != 4 {
		t.Errorf("launch request = %+v", request)
	}
	if len(request.Parameters) != 1 || request.Parameters[0].Name != "replicas" || request.Parameters[0].Value != "1" {
		t.Errorf("parameters = %+v", request.Parameters)
	}
}

func TestStackProfilesDemoStop(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesDemoStopCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "demo", "stop", "profile-1", "workspace-1"); executeError != nil {
		t.Fatalf("demo stop failed: %v", executeError)
	}
	if mock.demoStopped != "workspace-1" {
		t.Errorf("stopped workspace = %q", mock.demoStopped)
	}
}

func TestStackProfilesLogoGetWritesFile(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesLogoGetCmd)
	outputPath := filepath.Join(t.TempDir(), "logo.png")
	mock := &stackProfileManageMock{logo: &client.StackProfileLogo{
		Content: []byte("png-bytes"), ContentType: "image/png"}}
	if _, executeError := runStackProfilesCommand(t, mock, "", "logo", "get", "profile-1",
		"--output", outputPath); executeError != nil {
		t.Fatalf("logo get failed: %v", executeError)
	}
	written, readError := os.ReadFile(outputPath)
	if readError != nil || string(written) != "png-bytes" {
		t.Fatalf("written = %q, error = %v", written, readError)
	}
}

func TestStackProfilesLogoSetRejectsNonImages(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesLogoSetCmd)
	notAnImage := filepath.Join(t.TempDir(), "notes.txt")
	if writeError := os.WriteFile(notAnImage, []byte("plain text, not an image"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "logo", "set", "profile-1", notAnImage); executeError == nil {
		t.Fatal("expected the non-image upload to be refused")
	}
	if mock.logoPutType != "" {
		t.Fatal("expected no upload call")
	}
}

func TestStackProfilesDraftVerbTwins(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfileDraftsValidateCmd, stackProfileDraftsRebaseCmd,
		stackProfileDraftsSubmitSuggestionCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "drafts", "validate", "draft-1"); executeError != nil {
		t.Fatalf("drafts validate failed: %v", executeError)
	}
	if mock.validatedDraftID != "draft-1" {
		t.Errorf("validated draft = %q", mock.validatedDraftID)
	}
	if _, executeError := runStackProfilesCommand(t, mock, "", "drafts", "rebase", "draft-1"); executeError != nil {
		t.Fatalf("drafts rebase failed: %v", executeError)
	}
	if mock.rebaseRequest == nil || mock.rebaseRequest.Strategy != "acknowledge" {
		t.Errorf("rebase request = %+v", mock.rebaseRequest)
	}
	if _, executeError := runStackProfilesCommand(t, mock, "", "drafts", "submit-suggestion", "draft-1"); executeError == nil {
		t.Fatal("expected an error without --title")
	}
	if _, executeError := runStackProfilesCommand(t, mock, "", "drafts", "submit-suggestion", "draft-1",
		"--title", "Bump replicas"); executeError != nil {
		t.Fatalf("drafts submit-suggestion failed: %v", executeError)
	}
	if mock.suggestionTitle != "Bump replicas" {
		t.Errorf("suggestion title = %q", mock.suggestionTitle)
	}
}

func TestStackProfilesUpdateValidatesOutputBeforeRequest(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesUpdateCmd)
	mock := &stackProfileManageMock{}
	if _, executeError := runStackProfilesCommand(t, mock, "", "update", "profile-1",
		"--description", "x", "-o", "bogus"); executeError == nil {
		t.Fatal("expected an invalid -o to error")
	}
	if mock.updateRequest != nil {
		t.Fatal("expected no update call with an invalid output format")
	}
}
