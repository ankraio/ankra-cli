package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

// relatedReposMock records the related-repositories calls and the
// installation each listing and write was addressed to.
type relatedReposMock struct {
	baseMock

	credentials      []client.Credential
	credentialsError error
	related          []client.RelatedRepository
	created          *client.RelatedRepository
	deleteErrors     map[string]error

	credentialListings  int
	listedInstallations []string
	createCalls         []relatedReposCreateCall
	deletedIDs          []string
}

type relatedReposCreateCall struct {
	installationID    string
	repository        string
	relatedRepository string
}

func (m *relatedReposMock) ListCredentials(provider *string) ([]client.Credential, error) {
	m.credentialListings++
	return m.credentials, m.credentialsError
}

func (m *relatedReposMock) ListRelatedRepositories(ctx context.Context,
	installationID string) ([]client.RelatedRepository, error) {
	m.listedInstallations = append(m.listedInstallations, installationID)
	return m.related, nil
}

func (m *relatedReposMock) CreateRelatedRepository(ctx context.Context, installationID string, repoFullName string,
	relatedRepoFullName string) (*client.RelatedRepository, error) {
	m.createCalls = append(m.createCalls, relatedReposCreateCall{installationID, repoFullName, relatedRepoFullName})
	return m.created, nil
}

func (m *relatedReposMock) DeleteRelatedRepository(ctx context.Context, relatedRepositoryID string) error {
	m.deletedIDs = append(m.deletedIDs, relatedRepositoryID)
	return m.deleteErrors[relatedRepositoryID]
}

func relatedReposInstallationID(value int) *int {
	return &value
}

// relatedReposCredentials is one GitHub App credential among the credentials
// that must never be picked: a token-backed GitHub credential, and a
// credential of another provider that happens to carry an installation id.
func relatedReposCredentials() []client.Credential {
	return []client.Credential{
		{ID: "credential-app", Name: "github-app-my-org", Provider: "github",
			InstallationID: relatedReposInstallationID(135197959)},
		{ID: "credential-token", Name: "github-token", Provider: "github"},
		{ID: "credential-other", Name: "hetzner-main", Provider: "hetzner",
			InstallationID: relatedReposInstallationID(1)},
	}
}

func runRelatedRepos(t *testing.T, mock *relatedReposMock, stdin string, args ...string) (string, error) {
	t.Helper()
	stdout, stderr, executeError := runRelatedReposSplit(t, mock, stdin, args...)
	return stdout + stderr, executeError
}

// runRelatedReposSplit captures stdout and stderr separately, so a test can
// prove what a script capturing stdout would receive.
func runRelatedReposSplit(t *testing.T, mock *relatedReposMock, stdin string, args ...string) (string, string, error) {
	t.Helper()
	setMockClient(t, mock)
	for _, flagged := range []interface{ Flags() *pflag.FlagSet }{
		orgAIReviewRelatedReposListCmd, orgAIReviewRelatedReposAddCmd, orgAIReviewRelatedReposRemoveCmd,
	} {
		flagged.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	}
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(stdin))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	rootCmd.SetArgs(append([]string{"org", "ai-review", "related-repos"}, args...))
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

func TestRelatedReposListUsesTheOnlyGitHubAppInstallation(t *testing.T) {
	mock := &relatedReposMock{
		credentials: relatedReposCredentials(),
		related: []client.RelatedRepository{
			{ID: "relation-1", Provider: "github", RepoFullName: "my-org/api", RelatedRepoFullName: "my-org/portal"},
		},
	}

	output, executeError := runRelatedRepos(t, mock, "", "list")
	if executeError != nil {
		t.Fatalf("list: %v\n%s", executeError, output)
	}
	if len(mock.listedInstallations) != 1 || mock.listedInstallations[0] != "135197959" {
		t.Errorf("listed installations = %v, want the App credential's 135197959", mock.listedInstallations)
	}
	for _, expected := range []string{"relation-1", "my-org/api", "my-org/portal", "github-app-my-org (installation 135197959)"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in output, got: %s", expected, output)
		}
	}
}

func TestRelatedReposListRefusesToGuessBetweenInstallations(t *testing.T) {
	mock := &relatedReposMock{credentials: []client.Credential{
		{ID: "credential-a", Name: "github-app-a", Provider: "github", InstallationID: relatedReposInstallationID(111)},
		{ID: "credential-b", Name: "github-app-b", Provider: "github", InstallationID: relatedReposInstallationID(222)},
	}}

	_, executeError := runRelatedRepos(t, mock, "", "list")
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(executeError), executeError, exitUsage)
	}
	if !strings.Contains(executeError.Error(), "github-app-a") || !strings.Contains(executeError.Error(), "github-app-b") {
		t.Errorf("error = %v, want both candidate credentials named", executeError)
	}
	if len(mock.listedInstallations) != 0 {
		t.Errorf("listed %v, want no listing before an installation is chosen", mock.listedInstallations)
	}
}

func TestRelatedReposCredentialFlagPicksTheInstallationByNameOrID(t *testing.T) {
	credentials := []client.Credential{
		{ID: "credential-a", Name: "github-app-a", Provider: "github", InstallationID: relatedReposInstallationID(111)},
		{ID: "credential-b", Name: "github-app-b", Provider: "github", InstallationID: relatedReposInstallationID(222)},
	}
	for _, requested := range []string{"github-app-b", "credential-b"} {
		mock := &relatedReposMock{credentials: credentials, related: []client.RelatedRepository{}}
		if _, executeError := runRelatedRepos(t, mock, "", "list", "--credential", requested); executeError != nil {
			t.Fatalf("list --credential %s: %v", requested, executeError)
		}
		if len(mock.listedInstallations) != 1 || mock.listedInstallations[0] != "222" {
			t.Errorf("--credential %s listed %v, want installation 222", requested, mock.listedInstallations)
		}
	}
}

func TestRelatedReposRefusesACredentialWithoutAnInstallation(t *testing.T) {
	mock := &relatedReposMock{credentials: relatedReposCredentials()}

	_, executeError := runRelatedRepos(t, mock, "", "list", "--credential", "github-token")
	if exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "not backed by a GitHub App installation") {
		t.Fatalf("error = %v (exit %d), want a usage refusal naming the missing installation",
			executeError, exitCodeFor(executeError))
	}
	if len(mock.listedInstallations) != 0 {
		t.Errorf("listed %v, want nothing listed", mock.listedInstallations)
	}
}

func TestRelatedReposUnknownCredentialIsNotFound(t *testing.T) {
	mock := &relatedReposMock{credentials: relatedReposCredentials()}

	_, executeError := runRelatedRepos(t, mock, "", "list", "--credential", "hetzner-main")
	if exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("exit code = %d (%v), want %d: a credential of another provider is not a GitHub credential",
			exitCodeFor(executeError), executeError, exitNotFound)
	}
}

// A credential listing that failed says nothing about which installations
// exist, so it must not read as an organisation without a GitHub App.
func TestRelatedReposCredentialListingFailureIsNotReportedAsNoInstallation(t *testing.T) {
	mock := &relatedReposMock{credentialsError: errors.New("platform unavailable")}

	_, executeError := runRelatedRepos(t, mock, "", "list")
	if executeError == nil {
		t.Fatal("expected an error when the credentials cannot be listed")
	}
	if strings.Contains(executeError.Error(), "no GitHub credential") || !strings.Contains(executeError.Error(), "platform unavailable") {
		t.Errorf("error = %v, want the listing failure rather than an absent installation", executeError)
	}
}

func TestRelatedReposListSaysWhenNothingIsStated(t *testing.T) {
	mock := &relatedReposMock{credentials: relatedReposCredentials(), related: []client.RelatedRepository{}}

	output, executeError := runRelatedRepos(t, mock, "", "list")
	if executeError != nil {
		t.Fatalf("list: %v", executeError)
	}
	if !strings.Contains(output, "No related repositories are stated under github-app-my-org") {
		t.Errorf("output = %s, want the empty listing named as such", output)
	}
}

func TestRelatedReposListJSONIsTheBareList(t *testing.T) {
	mock := &relatedReposMock{
		credentials: relatedReposCredentials(),
		related: []client.RelatedRepository{
			{ID: "relation-1", Provider: "github", RepoFullName: "my-org/api", RelatedRepoFullName: "my-org/portal"},
		},
	}

	output, executeError := runRelatedRepos(t, mock, "", "list", "-o", "json")
	if executeError != nil {
		t.Fatalf("list -o json: %v", executeError)
	}
	var decoded []client.RelatedRepository
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("-o json output is not a JSON list: %v\n%s", decodeError, output)
	}
	if len(decoded) != 1 || decoded[0].RelatedRepoFullName != "my-org/portal" {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestRelatedReposAddRelatesBothUnderTheInstallation(t *testing.T) {
	mock := &relatedReposMock{
		credentials: relatedReposCredentials(),
		created: &client.RelatedRepository{ID: "relation-2", Provider: "github",
			RepoFullName: "my-org/shared", RelatedRepoFullName: "my-org/web"},
	}

	output, executeError := runRelatedRepos(t, mock, "", "add", "my-org/web", "my-org/shared")
	if executeError != nil {
		t.Fatalf("add: %v\n%s", executeError, output)
	}
	expectedCall := relatedReposCreateCall{"135197959", "my-org/web", "my-org/shared"}
	if len(mock.createCalls) != 1 || mock.createCalls[0] != expectedCall {
		t.Errorf("create calls = %+v, want %+v", mock.createCalls, expectedCall)
	}
	if !strings.Contains(output, "Related my-org/shared ↔ my-org/web") {
		t.Errorf("output = %s, want the stored pair reported", output)
	}
}

func TestRelatedReposAddTreatsAnAnswerWithoutTheRowAsUnconfirmed(t *testing.T) {
	mock := &relatedReposMock{credentials: relatedReposCredentials(), created: &client.RelatedRepository{}}

	output, executeError := runRelatedRepos(t, mock, "", "add", "my-org/web", "my-org/shared")
	if executeError == nil || !strings.Contains(executeError.Error(), "unconfirmed") {
		t.Fatalf("error = %v, want the write reported as unconfirmed", executeError)
	}
	if strings.Contains(output, "Related my-org") {
		t.Errorf("output = %s, want no success line for an unconfirmed write", output)
	}
}

// A relationship has no direction, so removing it by its repositories removes
// every stored order of it - and nothing that merely shares one repository.
func TestRelatedReposRemoveByRepositoriesRemovesEveryStoredOrder(t *testing.T) {
	mock := &relatedReposMock{
		credentials: relatedReposCredentials(),
		related: []client.RelatedRepository{
			{ID: "relation-1", RepoFullName: "my-org/api", RelatedRepoFullName: "my-org/portal"},
			{ID: "relation-2", RepoFullName: "My-Org/Portal", RelatedRepoFullName: "my-org/api"},
			{ID: "relation-3", RepoFullName: "my-org/portal", RelatedRepoFullName: "my-org/other"},
		},
	}

	output, executeError := runRelatedRepos(t, mock, "", "remove", "my-org/portal", "my-org/api", "--yes")
	if executeError != nil {
		t.Fatalf("remove: %v\n%s", executeError, output)
	}
	if strings.Join(mock.deletedIDs, ",") != "relation-1,relation-2" {
		t.Errorf("deleted = %v, want relation-1 and relation-2 only", mock.deletedIDs)
	}
}

func TestRelatedReposRemoveDeclinedRemovesNothing(t *testing.T) {
	mock := &relatedReposMock{
		credentials: relatedReposCredentials(),
		related: []client.RelatedRepository{
			{ID: "relation-1", RepoFullName: "my-org/api", RelatedRepoFullName: "my-org/portal"},
		},
	}

	_, executeError := runRelatedRepos(t, mock, "n\n", "remove", "my-org/portal", "my-org/api")
	if exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(executeError), executeError, exitCancelled)
	}
	if len(mock.deletedIDs) != 0 {
		t.Errorf("deleted %v after the prompt was declined", mock.deletedIDs)
	}
}

// The confirmation prompt is for the person at the terminal, so it goes to
// stderr: a script capturing stdout gets the answer and nothing else. -y is
// the same shorthand for --yes the other confirming commands take.
func TestRelatedReposRemovePromptStaysOffStdout(t *testing.T) {
	mock := &relatedReposMock{}

	stdout, stderr, executeError := runRelatedReposSplit(t, mock, "y\n", "remove", "relation-9")
	if executeError != nil {
		t.Fatalf("remove: %v\nstdout=%s\nstderr=%s", executeError, stdout, stderr)
	}
	if strings.Join(mock.deletedIDs, ",") != "relation-9" {
		t.Errorf("deleted = %v, want relation-9", mock.deletedIDs)
	}
	if strings.TrimSpace(stdout) != "Removed related repository relationship relation-9." {
		t.Errorf("stdout = %q, want the answer line only", stdout)
	}
	if !strings.Contains(stderr, "Remove related repository relationship relation-9?") {
		t.Errorf("the prompt did not reach stderr: %q", stderr)
	}

	mock.deletedIDs = nil
	if _, executeError := runRelatedRepos(t, mock, "", "remove", "relation-9", "-y"); executeError != nil ||
		strings.Join(mock.deletedIDs, ",") != "relation-9" {
		t.Errorf("remove -y: error=%v deleted=%v", executeError, mock.deletedIDs)
	}
}

func TestRelatedReposRemoveByIDNeedsNoCredential(t *testing.T) {
	mock := &relatedReposMock{}

	if _, executeError := runRelatedRepos(t, mock, "", "remove", "relation-9", "--yes"); executeError != nil {
		t.Fatalf("remove by id: %v", executeError)
	}
	if strings.Join(mock.deletedIDs, ",") != "relation-9" || mock.credentialListings != 0 {
		t.Errorf("deleted = %v with %d credential listings, want relation-9 and none",
			mock.deletedIDs, mock.credentialListings)
	}
}

func TestRelatedReposRemoveOfAnUnstatedPairIsNotFound(t *testing.T) {
	mock := &relatedReposMock{
		credentials: relatedReposCredentials(),
		related: []client.RelatedRepository{
			{ID: "relation-3", RepoFullName: "my-org/portal", RelatedRepoFullName: "my-org/other"},
		},
	}

	_, executeError := runRelatedRepos(t, mock, "", "remove", "my-org/portal", "my-org/api", "--yes")
	if exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(executeError), executeError, exitNotFound)
	}
	if len(mock.deletedIDs) != 0 {
		t.Errorf("deleted %v for a pair that is not stated", mock.deletedIDs)
	}
}

// When one stored order is removed and the next fails, the relationship is
// still in force; the error must say so rather than read as a clean failure.
func TestRelatedReposRemoveReportsAPartialRemoval(t *testing.T) {
	mock := &relatedReposMock{
		credentials: relatedReposCredentials(),
		related: []client.RelatedRepository{
			{ID: "relation-1", RepoFullName: "my-org/api", RelatedRepoFullName: "my-org/portal"},
			{ID: "relation-2", RepoFullName: "my-org/portal", RelatedRepoFullName: "my-org/api"},
		},
		deleteErrors: map[string]error{"relation-2": errors.New("platform unavailable")},
	}

	_, executeError := runRelatedRepos(t, mock, "", "remove", "my-org/portal", "my-org/api", "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "removed 1 of the 2 stored rows") ||
		!strings.Contains(executeError.Error(), "still stated") {
		t.Fatalf("error = %v, want the partial removal named", executeError)
	}
}
