package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type notificationRoutesMock struct {
	baseMock
	routes        []client.NotificationRoute
	createRequest *client.CreateNotificationRouteRequest
	updatedID     string
	updateRequest *client.UpdateNotificationRouteRequest
	deleteCalls   []string
	testCalls     []string
}

func (m *notificationRoutesMock) ListNotificationRoutes() (*client.NotificationRouteList, error) {
	return &client.NotificationRouteList{Items: m.routes}, nil
}

func (m *notificationRoutesMock) CreateNotificationRoute(request client.CreateNotificationRouteRequest) (*client.NotificationRoute, error) {
	m.createRequest = &request
	priority := 100
	if request.Priority != nil {
		priority = *request.Priority
	}
	mode := "include"
	if request.Mode != nil {
		mode = *request.Mode
	}
	return &client.NotificationRoute{
		ID:             "7c1e0d5a-0000-4000-8000-000000000010",
		OrganisationID: "0f0f0f0f-0000-4000-8000-000000000001",
		Kind:           request.Kind,
		Severity:       request.Severity,
		ClusterID:      request.ClusterID,
		SourceID:       request.SourceID,
		DestinationID:  request.DestinationID,
		Priority:       priority,
		StopOnMatch:    request.StopOnMatch != nil && *request.StopOnMatch,
		Mode:           mode,
		Enabled:        request.Enabled == nil || *request.Enabled,
		CreatedAt:      "2026-08-17T10:00:00Z",
		UpdatedAt:      "2026-08-17T10:00:00Z",
	}, nil
}

func (m *notificationRoutesMock) UpdateNotificationRoute(routeID string, request client.UpdateNotificationRouteRequest) (*client.NotificationRoute, error) {
	m.updatedID = routeID
	m.updateRequest = &request
	for index := range m.routes {
		if m.routes[index].ID == routeID {
			updated := m.routes[index]
			if request.Priority != nil {
				updated.Priority = *request.Priority
			}
			if request.Enabled != nil {
				updated.Enabled = *request.Enabled
			}
			return &updated, nil
		}
	}
	return nil, client.NewUnexpectedResponseError(404, "Route not found")
}

func (m *notificationRoutesMock) DeleteNotificationRoute(routeID string) error {
	m.deleteCalls = append(m.deleteCalls, routeID)
	return nil
}

func (m *notificationRoutesMock) TestNotificationRoute(routeID string) (*client.NotificationRouteTestResult, error) {
	m.testCalls = append(m.testCalls, routeID)
	return &client.NotificationRouteTestResult{DeliveryID: "9e9e9e9e-0000-4000-8000-000000000042"}, nil
}

func sampleNotificationRoutes() []client.NotificationRoute {
	return []client.NotificationRoute{
		{
			ID:             "7c1e0d5a-0000-4000-8000-000000000001",
			OrganisationID: "0f0f0f0f-0000-4000-8000-000000000001",
			Severity:       strPtrCmd("critical"),
			DestinationID:  "3d0f6a2e-0000-4000-8000-000000000001",
			Priority:       10,
			StopOnMatch:    true,
			Mode:           "include",
			Enabled:        true,
			CreatedAt:      "2026-08-01T10:00:00Z",
			UpdatedAt:      "2026-08-02T10:00:00Z",
		},
		{
			ID:             "7c1e0d5a-0000-4000-8000-000000000002",
			OrganisationID: "0f0f0f0f-0000-4000-8000-000000000001",
			Kind:           strPtrCmd("gitops_sync_failed"),
			ClusterID:      strPtrCmd("c1c1c1c1-0000-4000-8000-000000000001"),
			DestinationID:  "3d0f6a2e-0000-4000-8000-000000000002",
			Priority:       100,
			Mode:           "exclude",
			Enabled:        false,
			CreatedAt:      "2026-08-01T10:00:00Z",
			UpdatedAt:      "2026-08-02T10:00:00Z",
		},
	}
}

func TestAlertsRoutesListRendersTable(t *testing.T) {
	mock := &notificationRoutesMock{routes: sampleNotificationRoutes()}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "routes", "list")
	if runError != nil {
		t.Fatalf("alerts routes list failed: %v", runError)
	}
	for _, fragment := range []string{
		"7c1e0d5a-0000-4000-8000-000000000001", "critical", "include",
		"7c1e0d5a-0000-4000-8000-000000000002", "gitops_sync_failed", "c1c1c1c1-0000-4000-8000-000000000001", "exclude",
		"3d0f6a2e-0000-4000-8000-000000000002",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected table to contain %q, got:\n%s", fragment, stdout)
		}
	}
	if !strings.Contains(stdout, "any") {
		t.Errorf("expected unset filters to read 'any', got:\n%s", stdout)
	}
}

func TestAlertsRoutesListEmptyAndJSON(t *testing.T) {
	mock := &notificationRoutesMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "", "alerts", "routes", "list")
	if runError != nil {
		t.Fatalf("alerts routes list failed: %v", runError)
	}
	if !strings.Contains(stdout, "No notification routes found.") {
		t.Errorf("expected the empty message, got: %s", stdout)
	}

	mock.routes = sampleNotificationRoutes()
	stdout, _, runError = runAlertsCommand(t, mock, "", "alerts", "routes", "list", "-o", "json")
	if runError != nil {
		t.Fatalf("alerts routes list -o json failed: %v", runError)
	}
	var decoded struct {
		Items []map[string]interface{} `json:"items"`
	}
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalError, stdout)
	}
	if len(decoded.Items) != 2 || decoded.Items[0]["severity"] != "critical" || decoded.Items[0]["kind"] != nil {
		t.Errorf("unexpected items in JSON output: %s", stdout)
	}
}

func TestAlertsRoutesCreateSendsOnlyPassedFlags(t *testing.T) {
	mock := &notificationRoutesMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "create",
		"--destination-id", "3d0f6a2e-0000-4000-8000-000000000001",
		"--severity", "critical", "--priority", "10", "--stop-on-match", "--disabled")
	if runError != nil {
		t.Fatalf("alerts routes create failed: %v", runError)
	}
	request := mock.createRequest
	if request == nil {
		t.Fatal("expected the create call to reach the client")
	}
	if request.DestinationID != "3d0f6a2e-0000-4000-8000-000000000001" {
		t.Errorf("destination_id = %q", request.DestinationID)
	}
	if request.Severity == nil || *request.Severity != "critical" {
		t.Errorf("severity = %v, want critical", request.Severity)
	}
	if request.Priority == nil || *request.Priority != 10 {
		t.Errorf("priority = %v, want 10", request.Priority)
	}
	if request.StopOnMatch == nil || !*request.StopOnMatch {
		t.Errorf("stop_on_match = %v, want true", request.StopOnMatch)
	}
	if request.Enabled == nil || *request.Enabled {
		t.Errorf("enabled = %v, want false from --disabled", request.Enabled)
	}
	if request.Kind != nil || request.ClusterID != nil || request.SourceID != nil || request.Mode != nil {
		t.Errorf("unset filters must stay nil so the backend defaults apply, got %+v", request)
	}
	for _, fragment := range []string{
		"Notification route 7c1e0d5a-0000-4000-8000-000000000010 created.",
		"Priority:      10",
		"Severity:      critical",
		"Kind:          any",
		"Stop on match: true",
		"Enabled:       false",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected output to contain %q, got:\n%s", fragment, stdout)
		}
	}
}

func TestAlertsRoutesCreateSendsAllFilters(t *testing.T) {
	mock := &notificationRoutesMock{}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "create",
		"--destination-id", "3d0f6a2e-0000-4000-8000-000000000001",
		"--kind", "execution_failed", "--cluster-id", "c1c1c1c1-0000-4000-8000-000000000001",
		"--source-id", "stack:platform", "--mode", "exclude")
	if runError != nil {
		t.Fatalf("alerts routes create failed: %v", runError)
	}
	request := mock.createRequest
	if request.Kind == nil || *request.Kind != "execution_failed" {
		t.Errorf("kind = %v, want execution_failed", request.Kind)
	}
	if request.ClusterID == nil || *request.ClusterID != "c1c1c1c1-0000-4000-8000-000000000001" {
		t.Errorf("cluster_id = %v", request.ClusterID)
	}
	if request.SourceID == nil || *request.SourceID != "stack:platform" {
		t.Errorf("source_id = %v, want stack:platform", request.SourceID)
	}
	if request.Mode == nil || *request.Mode != "exclude" {
		t.Errorf("mode = %v, want exclude", request.Mode)
	}
	if request.Priority != nil || request.StopOnMatch != nil || request.Enabled != nil {
		t.Errorf("untouched members must stay nil, got %+v", request)
	}
}

func TestAlertsRoutesCreateValidatesEnums(t *testing.T) {
	mock := &notificationRoutesMock{}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "create", "--destination-id", "3d0f6a2e-0000-4000-8000-000000000001", "--mode", "drop")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Errorf("expected an unsupported mode to be a usage error, got %v", runError)
	}
	_, _, runError = runAlertsCommand(t, mock, "",
		"alerts", "routes", "create", "--destination-id", "3d0f6a2e-0000-4000-8000-000000000001", "--severity", "fatal")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Errorf("expected an unsupported severity to be a usage error, got %v", runError)
	}
	if mock.createRequest != nil {
		t.Error("usage errors must not reach the client")
	}
}

func TestAlertsRoutesCreateRequiresDestination(t *testing.T) {
	mock := &notificationRoutesMock{}
	_, _, runError := runAlertsCommand(t, mock, "", "alerts", "routes", "create", "--severity", "critical")
	if runError == nil {
		t.Fatal("expected create without --destination-id to fail")
	}
	if got := exitCodeFor(runError); got != exitUsage {
		t.Errorf("expected exit code %d, got %d (%v)", exitUsage, got, runError)
	}
}

func TestAlertsRoutesCreateNotFoundDestinationUsesExitNotFound(t *testing.T) {
	mock := &notFoundDestinationRoutesMock{}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "create", "--destination-id", "3d0f6a2e-0000-4000-8000-00000000dead")
	if runError == nil {
		t.Fatal("expected an error for an unknown destination")
	}
	if got := exitCodeFor(runError); got != exitNotFound {
		t.Errorf("expected exit code %d, got %d (%v)", exitNotFound, got, runError)
	}
	if !strings.Contains(runError.Error(), "not found in this organisation") {
		t.Errorf("expected the backend detail to surface, got: %v", runError)
	}
}

type notFoundDestinationRoutesMock struct{ baseMock }

func (m *notFoundDestinationRoutesMock) CreateNotificationRoute(request client.CreateNotificationRouteRequest) (*client.NotificationRoute, error) {
	return nil, client.NewUnexpectedResponseError(404,
		"Destination "+request.DestinationID+" not found in this organisation.")
}

func TestAlertsRoutesCreateJSONOutputIsPure(t *testing.T) {
	mock := &notificationRoutesMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "create", "--destination-id", "3d0f6a2e-0000-4000-8000-000000000001", "-o", "json")
	if runError != nil {
		t.Fatalf("alerts routes create -o json failed: %v", runError)
	}
	var decoded map[string]interface{}
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalError, stdout)
	}
	if decoded["id"] != "7c1e0d5a-0000-4000-8000-000000000010" || decoded["mode"] != "include" {
		t.Errorf("unexpected JSON document: %s", stdout)
	}
	if decoded["priority"] != float64(100) {
		t.Errorf("priority = %v, want the backend default 100", decoded["priority"])
	}
}

func TestAlertsRoutesUpdateSendsOnlyChangedFields(t *testing.T) {
	mock := &notificationRoutesMock{routes: sampleNotificationRoutes()}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "update", "7c1e0d5a-0000-4000-8000-000000000001", "--priority", "5")
	if runError != nil {
		t.Fatalf("alerts routes update failed: %v", runError)
	}
	if mock.updatedID != "7c1e0d5a-0000-4000-8000-000000000001" {
		t.Errorf("updated id = %q", mock.updatedID)
	}
	request := mock.updateRequest
	if request == nil {
		t.Fatal("expected the update call to reach the client")
	}
	if request.Priority == nil || *request.Priority != 5 {
		t.Errorf("priority = %v, want 5", request.Priority)
	}
	if request.DestinationID != nil || request.Kind != nil || request.Severity != nil || request.ClusterID != nil ||
		request.SourceID != nil || request.StopOnMatch != nil || request.Mode != nil || request.Enabled != nil {
		t.Errorf("unchanged members must stay nil for the PATCH, got %+v", request)
	}
	if !strings.Contains(stdout, "Notification route 7c1e0d5a-0000-4000-8000-000000000001 updated.") ||
		!strings.Contains(stdout, "Priority:      5") {
		t.Errorf("expected the updated line and new priority, got: %s", stdout)
	}
}

func TestAlertsRoutesUpdateEnabledAndDestination(t *testing.T) {
	mock := &notificationRoutesMock{routes: sampleNotificationRoutes()}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "update", "7c1e0d5a-0000-4000-8000-000000000002",
		"--enabled", "--destination-id", "3d0f6a2e-0000-4000-8000-000000000001", "--stop-on-match=false")
	if runError != nil {
		t.Fatalf("alerts routes update failed: %v", runError)
	}
	request := mock.updateRequest
	if request.Enabled == nil || !*request.Enabled {
		t.Errorf("enabled = %v, want true from --enabled", request.Enabled)
	}
	if request.DestinationID == nil || *request.DestinationID != "3d0f6a2e-0000-4000-8000-000000000001" {
		t.Errorf("destination_id = %v", request.DestinationID)
	}
	if request.StopOnMatch == nil || *request.StopOnMatch {
		t.Errorf("stop_on_match = %v, want an explicit false", request.StopOnMatch)
	}
	if request.Priority != nil {
		t.Errorf("priority must stay nil when not passed, got %d", *request.Priority)
	}
}

func TestAlertsRoutesUpdateWithoutFlagsIsUsageError(t *testing.T) {
	mock := &notificationRoutesMock{routes: sampleNotificationRoutes()}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "update", "7c1e0d5a-0000-4000-8000-000000000001")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Errorf("expected update without flags to be a usage error, got %v", runError)
	}
	if mock.updateRequest != nil {
		t.Error("an empty update must not reach the client")
	}
}

func TestAlertsRoutesUpdateNotFoundUsesExitNotFound(t *testing.T) {
	mock := &notificationRoutesMock{}
	_, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "update", "7c1e0d5a-0000-4000-8000-00000000dead", "--priority", "1")
	if runError == nil {
		t.Fatal("expected an error for a missing route")
	}
	if got := exitCodeFor(runError); got != exitNotFound {
		t.Errorf("expected exit code %d, got %d (%v)", exitNotFound, got, runError)
	}
}

func TestAlertsRoutesDeleteDeclinedUsesExitCancelled(t *testing.T) {
	mock := &notificationRoutesMock{}
	_, _, runError := runAlertsCommand(t, mock, "n\n",
		"alerts", "routes", "delete", "7c1e0d5a-0000-4000-8000-000000000001")
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

func TestAlertsRoutesDeleteConfirmedAndYes(t *testing.T) {
	mock := &notificationRoutesMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "yes\n",
		"alerts", "routes", "delete", "7c1e0d5a-0000-4000-8000-000000000001")
	if runError != nil {
		t.Fatalf("alerts routes delete failed: %v", runError)
	}
	if !strings.Contains(stdout, "Notification route 7c1e0d5a-0000-4000-8000-000000000001 deleted.") {
		t.Errorf("expected the deleted line, got: %s", stdout)
	}
	_, _, runError = runAlertsCommand(t, mock, "",
		"alerts", "routes", "delete", "7c1e0d5a-0000-4000-8000-000000000002", "-y")
	if runError != nil {
		t.Fatalf("alerts routes delete -y failed: %v", runError)
	}
	if len(mock.deleteCalls) != 2 {
		t.Fatalf("expected two delete calls, got %v", mock.deleteCalls)
	}
}

func TestAlertsRoutesTestPrintsDeliveryID(t *testing.T) {
	mock := &notificationRoutesMock{}
	stdout, _, runError := runAlertsCommand(t, mock, "",
		"alerts", "routes", "test", "7c1e0d5a-0000-4000-8000-000000000001")
	if runError != nil {
		t.Fatalf("alerts routes test failed: %v", runError)
	}
	if len(mock.testCalls) != 1 || mock.testCalls[0] != "7c1e0d5a-0000-4000-8000-000000000001" {
		t.Errorf("test calls = %v", mock.testCalls)
	}
	if !strings.Contains(stdout, "Test notification queued (delivery 9e9e9e9e-0000-4000-8000-000000000042).") {
		t.Errorf("expected the delivery id line, got: %s", stdout)
	}

	stdout, _, runError = runAlertsCommand(t, mock, "",
		"alerts", "routes", "test", "7c1e0d5a-0000-4000-8000-000000000001", "-o", "json")
	if runError != nil {
		t.Fatalf("alerts routes test -o json failed: %v", runError)
	}
	var decoded map[string]interface{}
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalError, stdout)
	}
	if decoded["delivery_id"] != "9e9e9e9e-0000-4000-8000-000000000042" {
		t.Errorf("unexpected JSON document: %s", stdout)
	}
}
