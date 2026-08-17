package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// alertsCommandTree lists every alerts command whose flags carry state
// between Execute calls, for resetTreeFlags.
func alertsCommandTree() []*cobra.Command {
	return []*cobra.Command{
		alertsDestinationsListCmd,
		alertsDestinationsGetCmd,
		alertsDestinationsCreateCmd,
		alertsDestinationsUpdateCmd,
		alertsDestinationsDeleteCmd,
		alertsDestinationsTestCmd,
		alertsDestinationsTestURLCmd,
		alertsDestinationsChannelsCmd,
		alertsRoutesListCmd,
		alertsRoutesCreateCmd,
		alertsRoutesUpdateCmd,
		alertsRoutesDeleteCmd,
		alertsRoutesTestCmd,
	}
}

// runAlertsCommand executes rootCmd with the given stdin and separate
// stdout/stderr captures, so structured-output tests can prove stdout stays
// parseable. The alerts flag tree is reset afterwards so Changed markers do
// not leak between cases.
func runAlertsCommand(t *testing.T, mock APIClient, input string, args ...string) (string, string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	t.Cleanup(func() { resetTreeFlags(t, alertsCommandTree()...) })
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

type alertDestinationsMock struct {
	baseMock
	destinations   []client.AlertDestination
	listOptions    *client.ListAlertDestinationsOptions
	createRequest  *client.CreateAlertDestinationRequest
	updatedID      string
	updateRequest  *client.UpdateAlertDestinationRequest
	deleteCalls    []string
	testCalls      []string
	testURLRequest *client.TestAlertDestinationURLRequest
	testResult     *client.AlertDestinationTestResult
	slackChannels  *client.SlackChannelList
	slackError     error
	teamsChannels  *client.TeamsChannelList
	teamsError     error
}

func (m *alertDestinationsMock) ListAlertDestinations(options client.ListAlertDestinationsOptions) (*client.AlertDestinationList, error) {
	m.listOptions = &options
	return &client.AlertDestinationList{
		Items: m.destinations,
		Pagination: client.AlertDestinationPagination{
			Page: 1, PageSize: 20, TotalCount: len(m.destinations), TotalPages: 1,
		},
	}, nil
}

func (m *alertDestinationsMock) GetAlertDestination(destinationID string) (*client.AlertDestination, error) {
	for index := range m.destinations {
		if m.destinations[index].ID == destinationID {
			return &m.destinations[index], nil
		}
	}
	return nil, client.NewUnexpectedResponseError(404, "Integration not found")
}

func (m *alertDestinationsMock) CreateAlertDestination(request client.CreateAlertDestinationRequest) (*client.AlertDestination, error) {
	m.createRequest = &request
	created := client.AlertDestination{
		ID:        "3d0f6a2e-0000-4000-8000-000000000010",
		Name:      request.Name,
		URL:       request.URL,
		ChannelID: request.ChannelID,
		Enabled:   request.Enabled == nil || *request.Enabled,
		CreatedAt: "2026-08-17T10:00:00Z",
		UpdatedAt: "2026-08-17T10:00:00Z",
	}
	return &created, nil
}

func (m *alertDestinationsMock) UpdateAlertDestination(destinationID string, request client.UpdateAlertDestinationRequest) (*client.AlertDestination, error) {
	m.updatedID = destinationID
	m.updateRequest = &request
	for index := range m.destinations {
		if m.destinations[index].ID == destinationID {
			updated := m.destinations[index]
			if request.Name != nil {
				updated.Name = *request.Name
			}
			return &updated, nil
		}
	}
	return nil, client.NewUnexpectedResponseError(404, "Integration not found")
}

func (m *alertDestinationsMock) DeleteAlertDestination(destinationID string) (*client.DeleteAlertDestinationResult, error) {
	m.deleteCalls = append(m.deleteCalls, destinationID)
	return &client.DeleteAlertDestinationResult{Success: true, Message: "Integration deleted successfully"}, nil
}

func (m *alertDestinationsMock) TestAlertDestination(destinationID string) (*client.AlertDestinationTestResult, error) {
	m.testCalls = append(m.testCalls, destinationID)
	return m.testResult, nil
}

func (m *alertDestinationsMock) TestAlertDestinationURL(request client.TestAlertDestinationURLRequest) (*client.AlertDestinationTestResult, error) {
	m.testURLRequest = &request
	return m.testResult, nil
}

func (m *alertDestinationsMock) ListSlackChannels() (*client.SlackChannelList, error) {
	if m.slackError != nil {
		return nil, m.slackError
	}
	return m.slackChannels, nil
}

func (m *alertDestinationsMock) ListTeamsChannels() (*client.TeamsChannelList, error) {
	if m.teamsError != nil {
		return nil, m.teamsError
	}
	return m.teamsChannels, nil
}

func sampleAlertDestinations() []client.AlertDestination {
	return []client.AlertDestination{
		{
			ID:        "3d0f6a2e-0000-4000-8000-000000000001",
			Name:      "ops-slack",
			URL:       strPtrCmd("https://***"),
			Enabled:   true,
			CreatedAt: "2026-08-01T10:00:00Z",
			UpdatedAt: "2026-08-02T10:00:00Z",
		},
		{
			ID:              "3d0f6a2e-0000-4000-8000-000000000002",
			Name:            "ops-teams",
			ChannelID:       strPtrCmd("19:abc@thread.tacv2"),
			ChannelName:     strPtrCmd("Platform Alerts"),
			IntegrationType: "teams",
			TeamsTenantID:   strPtrCmd("tenant-1"),
			Description:     strPtrCmd("Teams channel for the platform team"),
			Enabled:         false,
			CreatedAt:       "2026-08-01T10:00:00Z",
			UpdatedAt:       "2026-08-02T10:00:00Z",
		},
	}
}

func TestAlertsDestinationsListRendersTableAndFilters(t *testing.T) {
	mock := &alertDestinationsMock{destinations: sampleAlertDestinations()}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "list", "--search", "ops", "--enabled", "--page", "2", "--page-size", "50")
	if runError != nil {
		t.Fatalf("alerts destinations list failed: %v", runError)
	}
	if mock.listOptions == nil {
		t.Fatal("expected the list call to reach the client")
	}
	if mock.listOptions.Search != "ops" || mock.listOptions.Page != 2 || mock.listOptions.PageSize != 50 {
		t.Errorf("list options = %+v, want search ops page 2 size 50", mock.listOptions)
	}
	if mock.listOptions.Enabled == nil || !*mock.listOptions.Enabled {
		t.Errorf("expected --enabled to filter enabled=true, got %v", mock.listOptions.Enabled)
	}
	for _, fragment := range []string{
		"ops-slack", "webhook", "https://***",
		"ops-teams", "teams", "Platform Alerts",
		"Page 1 of 1 (total 2)",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected table to contain %q, got:\n%s", fragment, stdout)
		}
	}
}

func TestAlertsDestinationsListDisabledFilterAndEmpty(t *testing.T) {
	mock := &alertDestinationsMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "destinations", "list", "--disabled")
	if runError != nil {
		t.Fatalf("alerts destinations list failed: %v", runError)
	}
	if mock.listOptions.Enabled == nil || *mock.listOptions.Enabled {
		t.Errorf("expected --disabled to filter enabled=false, got %v", mock.listOptions.Enabled)
	}
	if !strings.Contains(stdout, "No alert destinations found.") {
		t.Errorf("expected the empty message, got: %s", stdout)
	}
}

func TestAlertsDestinationsListEnabledAndDisabledAreExclusive(t *testing.T) {
	mock := &alertDestinationsMock{}
	_, _, runError := runAlertsCommand(t, mock, "", "alerts", "destinations", "list", "--enabled", "--disabled")
	if runError == nil {
		t.Fatal("expected --enabled with --disabled to be rejected")
	}
	if got := exitCodeFor(runError); got != exitUsage {
		t.Errorf("expected exit code %d, got %d (%v)", exitUsage, got, runError)
	}
}

func TestAlertsDestinationsListJSONOutputIsPure(t *testing.T) {
	mock := &alertDestinationsMock{destinations: sampleAlertDestinations()}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "destinations", "list", "-o", "json")
	if runError != nil {
		t.Fatalf("alerts destinations list -o json failed: %v", runError)
	}
	var decoded struct {
		Items      []map[string]interface{} `json:"items"`
		Pagination map[string]interface{}   `json:"pagination"`
	}
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalError, stdout)
	}
	if len(decoded.Items) != 2 || decoded.Items[0]["name"] != "ops-slack" {
		t.Errorf("unexpected items in JSON output: %s", stdout)
	}
	if decoded.Pagination["total_count"] != float64(2) {
		t.Errorf("pagination.total_count = %v, want 2", decoded.Pagination["total_count"])
	}
}

func TestAlertsDestinationsGetRendersDetails(t *testing.T) {
	mock := &alertDestinationsMock{destinations: sampleAlertDestinations()}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "get", "3d0f6a2e-0000-4000-8000-000000000002")
	if runError != nil {
		t.Fatalf("alerts destinations get failed: %v", runError)
	}
	for _, fragment := range []string{
		"Name:        ops-teams",
		"Type:        teams",
		"Target:      Platform Alerts",
		"Description: Teams channel for the platform team",
		"Enabled:     false",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected output to contain %q, got:\n%s", fragment, stdout)
		}
	}
}

func TestAlertsDestinationsGetNotFoundUsesExitNotFound(t *testing.T) {
	mock := &alertDestinationsMock{}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "get", "3d0f6a2e-0000-4000-8000-00000000dead")
	if runError == nil {
		t.Fatal("expected an error for a missing destination")
	}
	if got := exitCodeFor(runError); got != exitNotFound {
		t.Errorf("expected exit code %d, got %d (%v)", exitNotFound, got, runError)
	}
}

func TestAlertsDestinationsGetJSONOutputIsPure(t *testing.T) {
	mock := &alertDestinationsMock{destinations: sampleAlertDestinations()}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "get", "3d0f6a2e-0000-4000-8000-000000000001", "-o", "json")
	if runError != nil {
		t.Fatalf("alerts destinations get -o json failed: %v", runError)
	}
	var decoded map[string]interface{}
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalError, stdout)
	}
	if decoded["name"] != "ops-slack" || decoded["url"] != "https://***" {
		t.Errorf("unexpected JSON document: %s", stdout)
	}
	if _, present := decoded["channel_id"]; !present {
		t.Errorf("expected nullable channel_id to be present as null: %s", stdout)
	}
}

func TestAlertsDestinationsCreateSendsOnlyPassedFlags(t *testing.T) {
	mock := &alertDestinationsMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "create",
		"--name", "oncall", "--url", "https://events.pagerduty.com/v2/enqueue",
		"--type", "pagerduty", "--disabled")
	if runError != nil {
		t.Fatalf("alerts destinations create failed: %v", runError)
	}
	request := mock.createRequest
	if request == nil {
		t.Fatal("expected the create call to reach the client")
	}
	if request.Name != "oncall" {
		t.Errorf("name = %q, want oncall", request.Name)
	}
	if request.URL == nil || *request.URL != "https://events.pagerduty.com/v2/enqueue" {
		t.Errorf("url = %v, want the pagerduty url", request.URL)
	}
	if request.IntegrationType == nil || *request.IntegrationType != "pagerduty" {
		t.Errorf("integration_type = %v, want pagerduty", request.IntegrationType)
	}
	if request.Enabled == nil || *request.Enabled {
		t.Errorf("enabled = %v, want false from --disabled", request.Enabled)
	}
	if request.ChannelID != nil || request.ChannelName != nil || request.TeamsTenantID != nil ||
		request.Description != nil || request.Template != nil {
		t.Errorf("unset flags must stay nil, got %+v", request)
	}
	if !strings.Contains(stdout, `Alert destination "oncall" created (3d0f6a2e-0000-4000-8000-000000000010).`) {
		t.Errorf("expected the created line, got: %s", stdout)
	}
}

func TestAlertsDestinationsCreateChannelWithTemplateFile(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "payload.json")
	if writeError := os.WriteFile(templatePath, []byte(`{"text": "{{title}}"}`), 0o600); writeError != nil {
		t.Fatalf("write template: %v", writeError)
	}
	mock := &alertDestinationsMock{}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "create",
		"--name", "ops-teams", "--type", "teams",
		"--channel-id", "19:abc@thread.tacv2", "--channel-name", "Platform Alerts",
		"--teams-tenant-id", "tenant-1", "--description", "Teams channel",
		"--template-file", templatePath)
	if runError != nil {
		t.Fatalf("alerts destinations create failed: %v", runError)
	}
	request := mock.createRequest
	if request.URL != nil {
		t.Errorf("url must be nil for a channel destination, got %q", *request.URL)
	}
	if request.ChannelID == nil || *request.ChannelID != "19:abc@thread.tacv2" {
		t.Errorf("channel_id = %v, want 19:abc@thread.tacv2", request.ChannelID)
	}
	if request.ChannelName == nil || *request.ChannelName != "Platform Alerts" {
		t.Errorf("channel_name = %v, want Platform Alerts", request.ChannelName)
	}
	if request.TeamsTenantID == nil || *request.TeamsTenantID != "tenant-1" {
		t.Errorf("teams_tenant_id = %v, want tenant-1", request.TeamsTenantID)
	}
	if request.Description == nil || *request.Description != "Teams channel" {
		t.Errorf("description = %v, want Teams channel", request.Description)
	}
	if request.Template == nil || *request.Template != `{"text": "{{title}}"}` {
		t.Errorf("template = %v, want the file contents", request.Template)
	}
	if request.Enabled != nil {
		t.Errorf("enabled must be nil when --disabled is not passed, got %v", *request.Enabled)
	}
}

func TestAlertsDestinationsCreateRequiresURLOrChannel(t *testing.T) {
	mock := &alertDestinationsMock{}
	_, _, runError := runAlertsCommand(t, mock, "", "alerts", "destinations", "create", "--name", "lonely")
	if runError == nil {
		t.Fatal("expected create without --url or --channel-id to fail")
	}
	if got := exitCodeFor(runError); got != exitUsage {
		t.Errorf("expected exit code %d, got %d (%v)", exitUsage, got, runError)
	}
	if mock.createRequest != nil {
		t.Error("usage errors must not reach the client")
	}
}

func TestAlertsDestinationsCreateJSONOutputIsPure(t *testing.T) {
	mock := &alertDestinationsMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "create", "--name", "ops", "--url", "https://hooks.example/1", "-o", "json")
	if runError != nil {
		t.Fatalf("alerts destinations create -o json failed: %v", runError)
	}
	var decoded map[string]interface{}
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalError, stdout)
	}
	if decoded["id"] != "3d0f6a2e-0000-4000-8000-000000000010" || decoded["name"] != "ops" {
		t.Errorf("unexpected JSON document: %s", stdout)
	}
}

func TestAlertsDestinationsUpdateSendsOnlyChangedFields(t *testing.T) {
	mock := &alertDestinationsMock{destinations: sampleAlertDestinations()}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "update", "3d0f6a2e-0000-4000-8000-000000000001",
		"--description", "primary on-call channel", "--enabled")
	if runError != nil {
		t.Fatalf("alerts destinations update failed: %v", runError)
	}
	if mock.updatedID != "3d0f6a2e-0000-4000-8000-000000000001" {
		t.Errorf("updated id = %q", mock.updatedID)
	}
	request := mock.updateRequest
	if request == nil {
		t.Fatal("expected the update call to reach the client")
	}
	if request.Description == nil || *request.Description != "primary on-call channel" {
		t.Errorf("description = %v, want the new description", request.Description)
	}
	if request.Enabled == nil || !*request.Enabled {
		t.Errorf("enabled = %v, want true from --enabled", request.Enabled)
	}
	if request.Name != nil || request.URL != nil || request.ChannelID != nil ||
		request.ChannelName != nil || request.TeamsTenantID != nil || request.Template != nil {
		t.Errorf("unchanged flags must stay nil, got %+v", request)
	}
	if !strings.Contains(stdout, `Alert destination "ops-slack" updated`) {
		t.Errorf("expected the updated line, got: %s", stdout)
	}
}

func TestAlertsDestinationsUpdateDisabledOnly(t *testing.T) {
	mock := &alertDestinationsMock{destinations: sampleAlertDestinations()}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "update", "3d0f6a2e-0000-4000-8000-000000000001", "--disabled")
	if runError != nil {
		t.Fatalf("alerts destinations update failed: %v", runError)
	}
	request := mock.updateRequest
	if request.Enabled == nil || *request.Enabled {
		t.Errorf("enabled = %v, want false from --disabled", request.Enabled)
	}
	if request.Description != nil {
		t.Errorf("description must stay nil, got %q", *request.Description)
	}
}

func TestAlertsDestinationsUpdateWithoutFlagsIsUsageError(t *testing.T) {
	mock := &alertDestinationsMock{destinations: sampleAlertDestinations()}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "update", "3d0f6a2e-0000-4000-8000-000000000001")
	if runError == nil {
		t.Fatal("expected update without flags to fail")
	}
	if got := exitCodeFor(runError); got != exitUsage {
		t.Errorf("expected exit code %d, got %d (%v)", exitUsage, got, runError)
	}
	if mock.updateRequest != nil {
		t.Error("an empty update must not reach the client")
	}
}

func TestAlertsDestinationsDeleteDeclinedUsesExitCancelled(t *testing.T) {
	mock := &alertDestinationsMock{}
	_, _, runError := runAlertsCommand(t, mock, "n\n",
		"alerts", "destinations", "delete", "3d0f6a2e-0000-4000-8000-000000000001")
	if !errors.Is(runError, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", runError)
	}
	if got := exitCodeFor(runError); got != exitCancelled {
		t.Errorf("expected exit code %d, got %d", exitCancelled, got)
	}
	if len(mock.deleteCalls) != 0 {
		t.Errorf("declined confirmation must not delete, got %v", mock.deleteCalls)
	}
}

func TestAlertsDestinationsDeleteConfirmedAndYes(t *testing.T) {
	mock := &alertDestinationsMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "y\n",
		"alerts", "destinations", "delete", "3d0f6a2e-0000-4000-8000-000000000001")
	if runError != nil {
		t.Fatalf("alerts destinations delete failed: %v", runError)
	}
	if !strings.Contains(stdout, "Alert destination 3d0f6a2e-0000-4000-8000-000000000001 deleted.") {
		t.Errorf("expected the deleted line, got: %s", stdout)
	}
	_, _, runError = runAlertsCommand(t, mock, "",
		"alerts", "destinations", "delete", "3d0f6a2e-0000-4000-8000-000000000002", "--yes")
	if runError != nil {
		t.Fatalf("alerts destinations delete --yes failed: %v", runError)
	}
	if len(mock.deleteCalls) != 2 {
		t.Fatalf("expected two delete calls, got %v", mock.deleteCalls)
	}
}

func TestAlertsDestinationsTestReportsSuccess(t *testing.T) {
	statusCode := 200
	responseTime := 123.4
	mock := &alertDestinationsMock{testResult: &client.AlertDestinationTestResult{
		Success: true, StatusCode: &statusCode, ResponseTimeMS: &responseTime,
	}}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "test", "3d0f6a2e-0000-4000-8000-000000000001")
	if runError != nil {
		t.Fatalf("alerts destinations test failed: %v", runError)
	}
	if len(mock.testCalls) != 1 || mock.testCalls[0] != "3d0f6a2e-0000-4000-8000-000000000001" {
		t.Errorf("test calls = %v", mock.testCalls)
	}
	if !strings.Contains(stdout, "Test delivery succeeded (HTTP 200, 123 ms).") {
		t.Errorf("expected the success line, got: %s", stdout)
	}
}

func TestAlertsDestinationsTestFailureExitsNonZero(t *testing.T) {
	statusCode := 500
	failure := "receiver returned 500"
	mock := &alertDestinationsMock{testResult: &client.AlertDestinationTestResult{
		Success: false, StatusCode: &statusCode, Error: &failure,
	}}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "test", "3d0f6a2e-0000-4000-8000-000000000001")
	if runError == nil {
		t.Fatal("expected a failed delivery to return an error")
	}
	if got := exitCodeFor(runError); got != exitError {
		t.Errorf("expected exit code %d, got %d", exitError, got)
	}
	if !strings.Contains(runError.Error(), "HTTP 500") || !strings.Contains(runError.Error(), "receiver returned 500") {
		t.Errorf("expected the failure details in the error, got: %v", runError)
	}
	if strings.Contains(stdout, "succeeded") {
		t.Errorf("stdout must not claim success: %s", stdout)
	}
}

func TestAlertsDestinationsTestJSONOutputIsPureEvenOnFailure(t *testing.T) {
	failure := "connection refused"
	mock := &alertDestinationsMock{testResult: &client.AlertDestinationTestResult{Success: false, Error: &failure}}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "test", "3d0f6a2e-0000-4000-8000-000000000001", "-o", "json")
	if runError == nil {
		t.Fatal("expected a failed delivery to return an error")
	}
	var decoded map[string]interface{}
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalError, stdout)
	}
	if decoded["success"] != false || decoded["error"] != "connection refused" {
		t.Errorf("unexpected JSON document: %s", stdout)
	}
	if decoded["status_code"] != nil {
		t.Errorf("status_code should be null when the request never completed: %s", stdout)
	}
}

func TestAlertsDestinationsTestURLSendsURLAndTemplate(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "payload.json")
	if writeError := os.WriteFile(templatePath, []byte(`{"text":"hi"}`), 0o600); writeError != nil {
		t.Fatalf("write template: %v", writeError)
	}
	statusCode := 204
	mock := &alertDestinationsMock{testResult: &client.AlertDestinationTestResult{Success: true, StatusCode: &statusCode}}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "destinations", "test-url", "--url", "https://hooks.example/1", "--template-file", templatePath)
	if runError != nil {
		t.Fatalf("alerts destinations test-url failed: %v", runError)
	}
	if mock.testURLRequest == nil || mock.testURLRequest.URL != "https://hooks.example/1" {
		t.Fatalf("test-url request = %+v", mock.testURLRequest)
	}
	if mock.testURLRequest.Template == nil || *mock.testURLRequest.Template != `{"text":"hi"}` {
		t.Errorf("template = %v, want the file contents", mock.testURLRequest.Template)
	}
	if !strings.Contains(stdout, "Test delivery succeeded (HTTP 204).") {
		t.Errorf("expected the success line, got: %s", stdout)
	}
}

func TestAlertsDestinationsTestURLRequiresURL(t *testing.T) {
	mock := &alertDestinationsMock{}
	_, _, runError := runAlertsCommand(t, mock, "", "alerts", "destinations", "test-url")
	if runError == nil {
		t.Fatal("expected test-url without --url to fail")
	}
	if got := exitCodeFor(runError); got != exitUsage {
		t.Errorf("expected exit code %d, got %d (%v)", exitUsage, got, runError)
	}
}

func TestAlertsDestinationsChannelsShowsBothProviders(t *testing.T) {
	mock := &alertDestinationsMock{
		slackChannels: &client.SlackChannelList{
			TeamID:   "T123",
			TeamName: strPtrCmd("Acme"),
			Channels: []client.SlackChannel{{ID: "C1", Name: "alerts", IsPrivate: false}},
		},
		teamsChannels: &client.TeamsChannelList{
			Channels: []client.TeamsChannel{{ID: "19:abc@thread.tacv2", Name: "Platform Alerts", TeamID: "team-1", TeamName: "Platform", TenantID: "tenant-1"}},
		},
	}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "destinations", "channels")
	if runError != nil {
		t.Fatalf("alerts destinations channels failed: %v", runError)
	}
	for _, fragment := range []string{
		"Slack (workspace Acme):", "alerts", "C1",
		"Teams:", "Platform Alerts", "19:abc@thread.tacv2", "tenant-1",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected output to contain %q, got:\n%s", fragment, stdout)
		}
	}
}

func TestAlertsDestinationsChannelsReportsNotConnectedAndNotAvailable(t *testing.T) {
	mock := &alertDestinationsMock{
		slackError: client.NewUnexpectedResponseError(404, "No Slack workspace is connected to this organisation"),
		teamsError: client.NewUnexpectedResponseError(503, "Teams bot service is not configured"),
	}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "destinations", "channels")
	if runError != nil {
		t.Fatalf("not-connected providers must not fail the command: %v", runError)
	}
	if !strings.Contains(stdout, "Slack: not connected") {
		t.Errorf("expected the Slack not-connected line, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Teams: not available") {
		t.Errorf("expected the Teams not-available line, got: %s", stdout)
	}
}

func TestAlertsDestinationsChannelsProviderFilterAndOtherErrors(t *testing.T) {
	mock := &alertDestinationsMock{
		slackChannels: &client.SlackChannelList{TeamID: "T123", Channels: []client.SlackChannel{{ID: "C1", Name: "alerts"}}},
		teamsError:    errors.New("boom"),
	}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "destinations", "channels", "--provider", "slack")
	if runError != nil {
		t.Fatalf("--provider slack must not touch Teams: %v", runError)
	}
	if strings.Contains(stdout, "Teams") {
		t.Errorf("Teams must not be listed with --provider slack, got: %s", stdout)
	}

	_, _, runError = runAlertsCommand(t, mock, "", "alerts", "destinations", "channels", "--provider", "teams")
	if runError == nil || !strings.Contains(runError.Error(), "boom") {
		t.Errorf("expected a non-picker Teams error to surface, got %v", runError)
	}

	_, _, runError = runAlertsCommand(t, mock, "", "alerts", "destinations", "channels", "--provider", "discord")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Errorf("expected an unsupported provider to be a usage error, got %v", runError)
	}
}

func TestAlertsDestinationsChannelsJSONKeepsStdoutPure(t *testing.T) {
	mock := &alertDestinationsMock{
		slackError:    client.NewUnexpectedResponseError(404, "No Slack workspace is connected to this organisation"),
		teamsChannels: &client.TeamsChannelList{Channels: []client.TeamsChannel{{ID: "19:abc@thread.tacv2", Name: "Platform Alerts"}}},
	}
	stdout, stderr, runError := runAlertsCommand(t, mock, "", "alerts", "destinations", "channels", "-o", "json")
	if runError != nil {
		t.Fatalf("alerts destinations channels -o json failed: %v", runError)
	}
	var decoded map[string]interface{}
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalError, stdout)
	}
	if decoded["slack"] != nil {
		t.Errorf("slack should be null when not connected: %s", stdout)
	}
	teams, ok := decoded["teams"].(map[string]interface{})
	if !ok || len(teams["channels"].([]interface{})) != 1 {
		t.Errorf("unexpected teams document: %s", stdout)
	}
	if !strings.Contains(stderr, "Slack: not connected") {
		t.Errorf("expected the not-connected notice on stderr, got: %q", stderr)
	}
}
