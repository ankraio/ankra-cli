package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ankra/internal/client"
)

type alertIngestCredentialsMock struct {
	baseMock
	credentials   []client.AlertIngestCredential
	clusters      map[string]client.ClusterListItem
	rebound       string
	rebindRequest *client.RebindAlertIngestCredentialRequest
	createRequest *client.CreateAlertIngestCredentialRequest
	createError   error
	deleteCalls   []string
}

const (
	sampleIngestCredentialID    = "6a15a1c2-58ab-4a5a-8f1f-2f2e2b1a9c01"
	sampleIngestCredentialToken = "ankra_ingest_s3cr3tTokenValueThatMustNotLeak"
	sampleIngestClusterID       = "1fa75b8f-f87c-4d21-ab01-d97cfb4d795c"
)

func (mock *alertIngestCredentialsMock) CreateAlertIngestCredential(
	request client.CreateAlertIngestCredentialRequest) (*client.CreatedAlertIngestCredential, error) {
	mock.createRequest = &request
	if mock.createError != nil {
		return nil, mock.createError
	}
	scope := request.Scope
	if scope == "" {
		scope = "cluster"
	}
	credential := client.AlertIngestCredential{ID: sampleIngestCredentialID, Name: request.Name, Scope: scope, Enabled: true}
	if request.ClusterID != nil {
		clusterID, clusterName := *request.ClusterID, "prod-hel1"
		credential.ClusterID = &clusterID
		credential.ClusterName = &clusterName
	}
	return &client.CreatedAlertIngestCredential{
		Item:        credential,
		Token:       sampleIngestCredentialToken,
		WebhookPath: "/webhooks/alerts/" + sampleIngestCredentialID,
	}, nil
}

func (mock *alertIngestCredentialsMock) DeleteAlertIngestCredential(credentialID string) (*client.DeleteAlertIngestCredentialResult, error) {
	mock.deleteCalls = append(mock.deleteCalls, credentialID)
	return &client.DeleteAlertIngestCredentialResult{Success: true, Message: "Ingest credential deleted"}, nil
}

func (mock *alertIngestCredentialsMock) ListAlertIngestCredentials() (*client.AlertIngestCredentialList, error) {
	return &client.AlertIngestCredentialList{Items: mock.credentials}, nil
}

func (mock *alertIngestCredentialsMock) RebindAlertIngestCredential(credentialID string,
	request client.RebindAlertIngestCredentialRequest) (*client.AlertIngestCredential, error) {
	mock.rebound = credentialID
	mock.rebindRequest = &request
	scope := "cluster"
	if request.Scope != nil {
		scope = *request.Scope
	}
	credential := client.AlertIngestCredential{ID: credentialID, Name: "hel1-alertmanager", Scope: scope, Enabled: true}
	if request.ClusterIDSet && request.ClusterID != nil {
		credential.ClusterID = request.ClusterID
		name := "prod-hel1"
		credential.ClusterName = &name
	}
	return &credential, nil
}

func (mock *alertIngestCredentialsMock) GetCluster(name string) (client.ClusterListItem, error) {
	cluster, found := mock.clusters[name]
	if !found {
		return client.ClusterListItem{}, errors.New("cluster not found")
	}
	return cluster, nil
}

func sampleIngestCredentials() []client.AlertIngestCredential {
	pinnedName, brokenName, lastUsed := "prod-hel1", "old-prod", "2026-09-04T10:00:00Z"
	pinnedID, brokenID := "1fa75b8f-f87c-4d21-ab01-d97cfb4d795c", "d8f216ef-b627-4d38-b0f8-ef963d3e24bf"
	return []client.AlertIngestCredential{
		{ID: "c1", Name: "hel1-alertmanager", ClusterID: &pinnedID, ClusterName: &pinnedName, Scope: "mixed", Enabled: true, LastUsedAt: &lastUsed},
		{ID: "c2", Name: "old-alertmanager", ClusterID: &brokenID, ClusterName: &brokenName, ClusterUnavailable: true, Scope: "cluster", Enabled: true},
		{ID: "c3", Name: "opensearch-monitor", Scope: "platform", Enabled: false},
	}
}

func TestAlertsIngestCredentialsListRendersPinsAndBrokenOnes(t *testing.T) {
	mock := &alertIngestCredentialsMock{credentials: sampleIngestCredentials()}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	for _, fragment := range []string{"hel1-alertmanager", "prod-hel1", "mixed", "old-alertmanager", "BROKEN",
		"opensearch-monitor", "platform", "never", "2026-09-04T10:00:00Z"} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected the table to contain %q, got:\n%s", fragment, stdout)
		}
	}
	empty := &alertIngestCredentialsMock{}
	stdout, _, runError = runAlertsCommand(t, empty, "", "alerts", "ingest-credentials", "list")
	if runError != nil || !strings.Contains(stdout, "No alert ingest credentials found.") {
		t.Fatalf("empty list: %v %q", runError, stdout)
	}
}

func TestAlertsIngestCredentialsRebindPinsByClusterNameWithoutTouchingTheToken(t *testing.T) {
	mock := &alertIngestCredentialsMock{clusters: map[string]client.ClusterListItem{
		"prod-hel1": {ID: "1fa75b8f-f87c-4d21-ab01-d97cfb4d795c", Name: "prod-hel1"},
	}}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "rebind", "c1",
		"--cluster", "prod-hel1", "--scope", "mixed")
	if runError != nil {
		t.Fatalf("rebind failed: %v", runError)
	}
	if mock.rebound != "c1" || mock.rebindRequest == nil || !mock.rebindRequest.ClusterIDSet ||
		mock.rebindRequest.ClusterID == nil || *mock.rebindRequest.ClusterID != "1fa75b8f-f87c-4d21-ab01-d97cfb4d795c" ||
		mock.rebindRequest.Scope == nil || *mock.rebindRequest.Scope != "mixed" {
		t.Fatalf("rebind request = %+v", mock.rebindRequest)
	}
	if !strings.Contains(stdout, "rebound: scope mixed, cluster prod-hel1") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAlertsIngestCredentialsRebindUnpinsAndRefusesConflictingFlags(t *testing.T) {
	mock := &alertIngestCredentialsMock{}
	if _, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "rebind", "c2",
		"--unpin", "--scope", "platform"); runError != nil {
		t.Fatalf("unpin failed: %v", runError)
	}
	if mock.rebindRequest == nil || !mock.rebindRequest.ClusterIDSet || mock.rebindRequest.ClusterID != nil ||
		mock.rebindRequest.Scope == nil || *mock.rebindRequest.Scope != "platform" {
		t.Fatalf("unpin request = %+v", mock.rebindRequest)
	}
	// A slice, not a map: "nothing to change" only reads as nothing when no
	// earlier case's flags are still on the shared command, and randomised
	// map iteration would decide that.
	for _, testCase := range []struct {
		name      string
		arguments []string
	}{
		{name: "cluster and unpin together", arguments: []string{"rebind", "c2", "--cluster", "x", "--unpin"}},
		{name: "nothing to change", arguments: []string{"rebind", "c2"}},
		{name: "an unknown scope", arguments: []string{"rebind", "c2", "--scope", "everything"}},
	} {
		if _, _, runError := runAlertsCommand(t, &alertIngestCredentialsMock{}, "",
			append([]string{"alerts", "ingest-credentials"}, testCase.arguments...)...); runError == nil {
			t.Errorf("%s: expected a usage error", testCase.name)
		}
	}
}

func TestRebindAlertIngestCredentialRequestSendsOnlyWhatWasSet(t *testing.T) {
	scope := "platform"
	for name, testCase := range map[string]struct {
		request client.RebindAlertIngestCredentialRequest
		want    string
	}{
		"unpin":      {client.RebindAlertIngestCredentialRequest{ClusterIDSet: true}, `{"cluster_id":null}`},
		"scope only": {client.RebindAlertIngestCredentialRequest{Scope: &scope}, `{"scope":"platform"}`},
	} {
		encoded, marshalError := testCase.request.MarshalJSON()
		if marshalError != nil || string(encoded) != testCase.want {
			t.Errorf("%s: %s %v", name, encoded, marshalError)
		}
	}
}

func TestAlertsIngestCredentialsCreatePrintsIDWebhookURLAndTokenOnce(t *testing.T) {
	mock := &alertIngestCredentialsMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "create",
		"--name", "hel1-alertmanager", "--cluster", sampleIngestClusterID)
	if runError != nil {
		t.Fatalf("create failed: %v", runError)
	}
	if mock.createRequest == nil || mock.createRequest.Name != "hel1-alertmanager" || mock.createRequest.Scope != "cluster" ||
		mock.createRequest.ClusterID == nil || *mock.createRequest.ClusterID != sampleIngestClusterID {
		t.Fatalf("create request = %+v", mock.createRequest)
	}
	for _, fragment := range []string{
		sampleIngestCredentialID,
		"https://test.ankra.app/webhooks/alerts/" + sampleIngestCredentialID,
		sampleIngestCredentialToken,
		"shown once",
		"prod-hel1",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected the output to contain %q, got:\n%s", fragment, stdout)
		}
	}
}

func TestAlertsIngestCredentialsCreateJSONCarriesTheResponseAndWebhookURL(t *testing.T) {
	mock := &alertIngestCredentialsMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "create",
		"--name", "platform-monitor", "--scope", "platform", "-o", "json")
	if runError != nil {
		t.Fatalf("create -o json failed: %v", runError)
	}
	if mock.createRequest == nil || mock.createRequest.ClusterID != nil || mock.createRequest.Scope != "platform" {
		t.Fatalf("create request = %+v", mock.createRequest)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if decoded["token"] != sampleIngestCredentialToken ||
		decoded["webhook_path"] != "/webhooks/alerts/"+sampleIngestCredentialID ||
		decoded["webhook_url"] != "https://test.ankra.app/webhooks/alerts/"+sampleIngestCredentialID {
		t.Fatalf("decoded = %v", decoded)
	}
}

func TestAlertsIngestCredentialsCreateTokenFileIsPrivateAndKeepsTheTokenOffStdout(t *testing.T) {
	tokenFilePath := filepath.Join(t.TempDir(), "ingest-token")
	mock := &alertIngestCredentialsMock{}
	stdout, stderr, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "create",
		"--name", "hel1-alertmanager", "--cluster", sampleIngestClusterID, "--token-file", tokenFilePath, "-o", "json")
	if runError != nil {
		t.Fatalf("create --token-file failed: %v", runError)
	}
	if strings.Contains(stdout, sampleIngestCredentialToken) || strings.Contains(stderr, sampleIngestCredentialToken) {
		t.Fatalf("the token leaked into the output:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if _, hasToken := decoded["token"]; hasToken || decoded["token_file"] != tokenFilePath {
		t.Fatalf("decoded = %v", decoded)
	}
	contents, readError := os.ReadFile(tokenFilePath)
	if readError != nil || string(contents) != sampleIngestCredentialToken {
		t.Fatalf("token file = %q, %v", contents, readError)
	}
	if runtime.GOOS != "windows" {
		information, statError := os.Stat(tokenFilePath)
		if statError != nil {
			t.Fatalf("stat token file: %v", statError)
		}
		if information.Mode().Perm() != 0o600 {
			t.Fatalf("token file mode = %v, want 0600", information.Mode().Perm())
		}
	}

	humanPath := filepath.Join(t.TempDir(), "human-token")
	stdout, _, runError = runAlertsCommand(t, &alertIngestCredentialsMock{}, "", "alerts", "ingest-credentials", "create",
		"--name", "hel1-alertmanager", "--token-file", humanPath)
	if runError != nil {
		t.Fatalf("create --token-file (human) failed: %v", runError)
	}
	if strings.Contains(stdout, sampleIngestCredentialToken) || !strings.Contains(stdout, "written to "+humanPath) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAlertsIngestCredentialsCreateRefusesAnExistingTokenFile(t *testing.T) {
	tokenFilePath := filepath.Join(t.TempDir(), "ingest-token")
	if writeError := os.WriteFile(tokenFilePath, []byte("previous secret"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	mock := &alertIngestCredentialsMock{}
	if _, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "create",
		"--name", "hel1-alertmanager", "--token-file", tokenFilePath); runError == nil {
		t.Fatal("expected an existing --token-file to be refused")
	}
	if mock.createRequest != nil {
		t.Fatal("a refused --token-file must not mint a credential")
	}
	if contents, _ := os.ReadFile(tokenFilePath); string(contents) != "previous secret" {
		t.Fatalf("the existing file was overwritten: %q", contents)
	}
}

func TestAlertsIngestCredentialsCreateReportsTheAPIErrorAndRemovesTheTokenFile(t *testing.T) {
	tokenFilePath := filepath.Join(t.TempDir(), "ingest-token")
	mock := &alertIngestCredentialsMock{createError: errors.New("An ingest credential named 'hel1-alertmanager' already exists")}
	_, _, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "create",
		"--name", "hel1-alertmanager", "--token-file", tokenFilePath)
	if runError == nil || !strings.Contains(runError.Error(), "already exists") {
		t.Fatalf("expected the API error, got %v", runError)
	}
	if _, statError := os.Stat(tokenFilePath); !os.IsNotExist(statError) {
		t.Fatalf("a failed create must not leave the token file behind: %v", statError)
	}
}

func TestAlertsIngestCredentialsCreateValidatesBeforeCallingTheAPI(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		arguments []string
	}{
		{name: "platform scope with a cluster pin", arguments: []string{"--name", "x", "--scope", "platform", "--cluster", sampleIngestClusterID}},
		{name: "mixed scope without a cluster pin", arguments: []string{"--name", "x", "--scope", "mixed"}},
		{name: "an unknown scope", arguments: []string{"--name", "x", "--scope", "everything"}},
		{name: "no name", arguments: []string{"--cluster", sampleIngestClusterID}},
	} {
		mock := &alertIngestCredentialsMock{}
		_, _, runError := runAlertsCommand(t, mock, "",
			append([]string{"alerts", "ingest-credentials", "create"}, testCase.arguments...)...)
		if runError == nil {
			t.Errorf("%s: expected a usage error", testCase.name)
		}
		if exitCodeFor(runError) != exitUsage {
			t.Errorf("%s: exit code = %d, want %d", testCase.name, exitCodeFor(runError), exitUsage)
		}
		if mock.createRequest != nil {
			t.Errorf("%s: the API was called: %+v", testCase.name, mock.createRequest)
		}
	}
}

func TestAlertsIngestCredentialsDeleteAsksFirstAndIsCancelledWithoutYes(t *testing.T) {
	mock := &alertIngestCredentialsMock{}
	_, stderr, runError := runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "delete", sampleIngestCredentialID)
	if !errors.Is(runError, errCancelled) {
		t.Fatalf("expected a non-interactive delete without --yes to be cancelled, got %v", runError)
	}
	if !strings.Contains(stderr, "Delete alert ingest credential") {
		t.Errorf("expected the confirmation prompt on stderr, got %q", stderr)
	}
	if len(mock.deleteCalls) != 0 {
		t.Fatalf("a cancelled delete must not call the API, got %v", mock.deleteCalls)
	}

	stdout, _, runError := runAlertsCommand(t, mock, "y\n", "alerts", "ingest-credentials", "delete", sampleIngestCredentialID)
	if runError != nil || !strings.Contains(stdout, "Alert ingest credential "+sampleIngestCredentialID+" deleted.") {
		t.Fatalf("confirmed delete: %v %q", runError, stdout)
	}
	if _, _, runError = runAlertsCommand(t, mock, "", "alerts", "ingest-credentials", "delete", "c2", "--yes"); runError != nil {
		t.Fatalf("delete --yes failed: %v", runError)
	}
	if len(mock.deleteCalls) != 2 || mock.deleteCalls[1] != "c2" {
		t.Fatalf("delete calls = %v", mock.deleteCalls)
	}
}

func TestCreateAlertIngestCredentialRequestOmitsAnAbsentPin(t *testing.T) {
	encoded, marshalError := json.Marshal(client.CreateAlertIngestCredentialRequest{Name: "monitor", Scope: "platform"})
	if marshalError != nil || string(encoded) != `{"name":"monitor","scope":"platform"}` {
		t.Fatalf("%s %v", encoded, marshalError)
	}
}
