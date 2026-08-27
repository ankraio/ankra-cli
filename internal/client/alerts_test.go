package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// alertsRequestRecorder captures the one request a handler receives so
// tests can pin the method, path, query, bearer header, and body.
type alertsRequestRecorder struct {
	method string
	path   string
	query  string
	bearer string
	body   []byte
}

func recordAlertsRequest(t *testing.T, statusCode int, responseBody interface{}) (*alertsRequestRecorder, http.HandlerFunc) {
	t.Helper()
	recorder := &alertsRequestRecorder{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		recorder.method = r.Method
		recorder.path = r.URL.Path
		recorder.query = r.URL.RawQuery
		recorder.bearer = r.Header.Get("Authorization")
		body, readError := io.ReadAll(r.Body)
		if readError != nil {
			t.Fatalf("read request body: %v", readError)
		}
		recorder.body = body
		if responseBody == nil {
			w.WriteHeader(statusCode)
			return
		}
		jsonResponse(t, w, statusCode, responseBody)
	}
	return recorder, handler
}

func (recorder *alertsRequestRecorder) assert(t *testing.T, method, path string) {
	t.Helper()
	if recorder.method != method {
		t.Errorf("method = %s, want %s", recorder.method, method)
	}
	if recorder.path != path {
		t.Errorf("path = %s, want %s", recorder.path, path)
	}
	if recorder.bearer != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want the bearer PAT", recorder.bearer)
	}
}

func (recorder *alertsRequestRecorder) decodedBody(t *testing.T) map[string]interface{} {
	t.Helper()
	var decoded map[string]interface{}
	if unmarshalError := json.Unmarshal(recorder.body, &decoded); unmarshalError != nil {
		t.Fatalf("decode request body %q: %v", recorder.body, unmarshalError)
	}
	return decoded
}

func TestListAlertDestinations_SendsFiltersAndDecodesPage(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"items": []map[string]interface{}{{
			"id": "dest-1", "name": "ops-slack", "url": "https://***", "channel_id": nil,
			"enabled": true, "created_at": "2026-08-01T10:00:00Z", "updated_at": "2026-08-01T10:00:00Z",
		}},
		"pagination": map[string]int{"page": 2, "page_size": 50, "total_count": 51, "total_pages": 2},
	})
	testClient := newTestClient(t, handler)

	enabled := true
	list, listError := testClient.ListAlertDestinations(ListAlertDestinationsOptions{
		Page: 2, PageSize: 50, Search: "ops", Enabled: &enabled,
	})
	if listError != nil {
		t.Fatalf("ListAlertDestinations: %v", listError)
	}
	recorder.assert(t, http.MethodGet, "/api/v1/org/alerts/integrations")
	if recorder.query != "enabled=true&page=2&page_size=50&search=ops" {
		t.Errorf("query = %q", recorder.query)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "ops-slack" || list.Items[0].URL == nil || *list.Items[0].URL != "https://***" {
		t.Errorf("items = %+v", list.Items)
	}
	if list.Items[0].ChannelID != nil {
		t.Errorf("null channel_id must decode to nil, got %q", *list.Items[0].ChannelID)
	}
	if list.Pagination.TotalCount != 51 || list.Pagination.TotalPages != 2 {
		t.Errorf("pagination = %+v", list.Pagination)
	}
}

func TestListAlertDestinations_NoOptionsSendsNoQuery(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"items": []interface{}{}, "pagination": map[string]int{"page": 1, "page_size": 20, "total_count": 0, "total_pages": 0},
	})
	testClient := newTestClient(t, handler)
	if _, listError := testClient.ListAlertDestinations(ListAlertDestinationsOptions{}); listError != nil {
		t.Fatalf("ListAlertDestinations: %v", listError)
	}
	if recorder.query != "" {
		t.Errorf("query = %q, want none", recorder.query)
	}
}

func TestGetAlertDestination_UnwrapsItemEnvelope(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"item": map[string]interface{}{
			"id": "dest-1", "name": "ops-teams", "url": nil, "channel_id": "19:abc@thread.tacv2",
			"channel_name": "Platform Alerts", "enabled": true,
			"created_at": "2026-08-01T10:00:00Z", "updated_at": "2026-08-01T10:00:00Z",
		},
	})
	testClient := newTestClient(t, handler)

	destination, getError := testClient.GetAlertDestination("dest-1")
	if getError != nil {
		t.Fatalf("GetAlertDestination: %v", getError)
	}
	recorder.assert(t, http.MethodGet, "/api/v1/org/alerts/integrations/dest-1")
	if destination.Name != "ops-teams" || destination.ChannelName == nil || *destination.ChannelName != "Platform Alerts" {
		t.Errorf("destination = %+v", destination)
	}
	if destination.URL != nil {
		t.Errorf("null url must decode to nil, got %q", *destination.URL)
	}
}

func TestGetAlertDestination_NotFoundSurfacesDetailAndStatus(t *testing.T) {
	_, handler := recordAlertsRequest(t, http.StatusNotFound, map[string]string{"detail": "Integration not found"})
	testClient := newTestClient(t, handler)

	_, getError := testClient.GetAlertDestination("missing")
	if getError == nil {
		t.Fatal("expected an error for 404")
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(getError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("expected an UnexpectedResponseError with status 404, got %v", getError)
	}
	if !strings.Contains(getError.Error(), "Integration not found") {
		t.Errorf("expected the backend detail, got: %v", getError)
	}
}

func TestCreateAlertDestination_SendsBodyAndOmitsUnset(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"item": map[string]interface{}{
			"id": "dest-9", "name": "oncall", "url": "https://events.pagerduty.com/v2/enqueue", "enabled": false,
			"organisation_id": "org-1", "created_at": "2026-08-01T10:00:00Z", "updated_at": "2026-08-01T10:00:00Z",
		},
	})
	testClient := newTestClient(t, handler)

	enabled := false
	destination, createError := testClient.CreateAlertDestination(CreateAlertDestinationRequest{
		Name:            "oncall",
		URL:             strPtr("https://events.pagerduty.com/v2/enqueue"),
		IntegrationType: strPtr("pagerduty"),
		Enabled:         &enabled,
	})
	if createError != nil {
		t.Fatalf("CreateAlertDestination: %v", createError)
	}
	recorder.assert(t, http.MethodPost, "/api/v1/org/alerts/integrations")
	body := recorder.decodedBody(t)
	if body["name"] != "oncall" || body["url"] != "https://events.pagerduty.com/v2/enqueue" || body["integration_type"] != "pagerduty" {
		t.Errorf("body = %v", body)
	}
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want an explicit false", body["enabled"])
	}
	for _, absent := range []string{"channel_id", "channel_name", "teams_tenant_id", "description", "template"} {
		if _, present := body[absent]; present {
			t.Errorf("%s must be omitted when unset, body = %v", absent, body)
		}
	}
	if destination.ID != "dest-9" || destination.OrganisationID == nil || *destination.OrganisationID != "org-1" {
		t.Errorf("destination = %+v", destination)
	}
}

func TestCreateAlertDestination_DuplicateNameSurfacesDetail(t *testing.T) {
	_, handler := recordAlertsRequest(t, http.StatusBadRequest,
		map[string]string{"detail": "A webhook with the name 'oncall' already exists"})
	testClient := newTestClient(t, handler)

	_, createError := testClient.CreateAlertDestination(CreateAlertDestinationRequest{Name: "oncall", URL: strPtr("https://x")})
	if createError == nil || !strings.Contains(createError.Error(), "already exists") {
		t.Errorf("expected the duplicate-name detail, got %v", createError)
	}
}

func TestUpdateAlertDestination_PutsOnlySetMembers(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"item": map[string]interface{}{
			"id": "dest-1", "name": "ops-slack", "url": "https://***", "enabled": true,
			"created_at": "2026-08-01T10:00:00Z", "updated_at": "2026-08-02T10:00:00Z",
		},
	})
	testClient := newTestClient(t, handler)

	enabled := true
	if _, updateError := testClient.UpdateAlertDestination("dest-1", UpdateAlertDestinationRequest{
		Description: strPtr("primary"), Enabled: &enabled,
	}); updateError != nil {
		t.Fatalf("UpdateAlertDestination: %v", updateError)
	}
	recorder.assert(t, http.MethodPut, "/api/v1/org/alerts/integrations/dest-1")
	body := recorder.decodedBody(t)
	if body["description"] != "primary" || body["enabled"] != true {
		t.Errorf("body = %v", body)
	}
	if len(body) != 2 {
		t.Errorf("only the set members may be sent, body = %v", body)
	}
}

func TestDeleteAlertDestination_DecodesSuccessBody(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK,
		map[string]interface{}{"success": true, "message": "Integration deleted successfully"})
	testClient := newTestClient(t, handler)

	result, deleteError := testClient.DeleteAlertDestination("dest-1")
	if deleteError != nil {
		t.Fatalf("DeleteAlertDestination: %v", deleteError)
	}
	recorder.assert(t, http.MethodDelete, "/api/v1/org/alerts/integrations/dest-1")
	if !result.Success || result.Message != "Integration deleted successfully" {
		t.Errorf("result = %+v", result)
	}
}

func TestTestAlertDestination_DecodesNullableOutcome(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"success": false, "status_code": nil, "response_time_ms": nil, "error": "connection refused",
	})
	testClient := newTestClient(t, handler)

	result, testError := testClient.TestAlertDestination("dest-1")
	if testError != nil {
		t.Fatalf("TestAlertDestination: %v", testError)
	}
	recorder.assert(t, http.MethodPost, "/api/v1/org/alerts/integrations/dest-1/test")
	if result.Success || result.StatusCode != nil || result.ResponseTimeMS != nil {
		t.Errorf("result = %+v, want a failed outcome with null status and timing", result)
	}
	if result.Error == nil || *result.Error != "connection refused" {
		t.Errorf("error = %v, want connection refused", result.Error)
	}
}

func TestTestAlertDestinationURL_SendsURLAndTemplate(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"success": true, "status_code": 200, "response_time_ms": 88.5, "error": nil,
	})
	testClient := newTestClient(t, handler)

	result, testError := testClient.TestAlertDestinationURL(TestAlertDestinationURLRequest{
		URL: "https://hooks.example/1", Template: strPtr(`{"text":"hi"}`),
	})
	if testError != nil {
		t.Fatalf("TestAlertDestinationURL: %v", testError)
	}
	recorder.assert(t, http.MethodPost, "/api/v1/org/alerts/integrations/test-url")
	body := recorder.decodedBody(t)
	if body["url"] != "https://hooks.example/1" || body["template"] != `{"text":"hi"}` {
		t.Errorf("body = %v", body)
	}
	if !result.Success || result.StatusCode == nil || *result.StatusCode != 200 ||
		result.ResponseTimeMS == nil || *result.ResponseTimeMS != 88.5 {
		t.Errorf("result = %+v", result)
	}
}

func TestListSlackChannels_DecodesWorkspaceAndChannels(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"team_id": "T123", "team_name": nil,
		"channels": []map[string]interface{}{{"id": "C1", "name": "alerts", "is_private": true}},
	})
	testClient := newTestClient(t, handler)

	list, listError := testClient.ListSlackChannels()
	if listError != nil {
		t.Fatalf("ListSlackChannels: %v", listError)
	}
	recorder.assert(t, http.MethodGet, "/api/v1/org/alerts/integrations/slack/channels")
	if list.TeamID != "T123" || list.TeamName != nil || len(list.Channels) != 1 || !list.Channels[0].IsPrivate {
		t.Errorf("list = %+v", list)
	}
}

func TestListSlackChannels_NotConnectedKeepsStatus(t *testing.T) {
	_, handler := recordAlertsRequest(t, http.StatusNotFound,
		map[string]string{"detail": "No Slack workspace is connected to this organisation"})
	testClient := newTestClient(t, handler)

	_, listError := testClient.ListSlackChannels()
	var unexpected *UnexpectedResponseError
	if !errors.As(listError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("expected a 404 UnexpectedResponseError, got %v", listError)
	}
}

func TestListTeamsChannels_DecodesChannelsAndNotConfigured(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"channels": []map[string]interface{}{{
			"id": "19:abc@thread.tacv2", "name": "Platform Alerts", "team_id": "team-1", "team_name": "Platform", "tenant_id": "tenant-1",
		}},
	})
	testClient := newTestClient(t, handler)

	list, listError := testClient.ListTeamsChannels()
	if listError != nil {
		t.Fatalf("ListTeamsChannels: %v", listError)
	}
	recorder.assert(t, http.MethodGet, "/api/v1/org/alerts/integrations/teams/channels")
	if len(list.Channels) != 1 || list.Channels[0].TenantID != "tenant-1" || list.Channels[0].TeamName != "Platform" {
		t.Errorf("list = %+v", list)
	}

	_, unavailableHandler := recordAlertsRequest(t, http.StatusServiceUnavailable,
		map[string]string{"detail": "Teams bot service is not configured"})
	unavailableClient := newTestClient(t, unavailableHandler)
	_, unavailableError := unavailableClient.ListTeamsChannels()
	var unexpected *UnexpectedResponseError
	if !errors.As(unavailableError, &unexpected) || unexpected.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected a 503 UnexpectedResponseError, got %v", unavailableError)
	}
}

func TestListNotificationRoutes_DecodesItems(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"items": []map[string]interface{}{{
			"id": "route-1", "organisation_id": "org-1", "kind": nil, "severity": "critical",
			"cluster_id": nil, "source_id": nil, "destination_id": "dest-1", "priority": 10,
			"stop_on_match": true, "mode": "include", "enabled": true,
			"created_at": "2026-08-01T10:00:00Z", "updated_at": "2026-08-01T10:00:00Z",
		}},
	})
	testClient := newTestClient(t, handler)

	list, listError := testClient.ListNotificationRoutes()
	if listError != nil {
		t.Fatalf("ListNotificationRoutes: %v", listError)
	}
	recorder.assert(t, http.MethodGet, "/api/v1/org/notifications/routes")
	if len(list.Items) != 1 {
		t.Fatalf("items = %+v", list.Items)
	}
	route := list.Items[0]
	if route.Kind != nil || route.Severity == nil || *route.Severity != "critical" || route.Priority != 10 || !route.StopOnMatch {
		t.Errorf("route = %+v", route)
	}
}

func TestCreateNotificationRoute_SendsOnlySetMembers(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"id": "route-9", "organisation_id": "org-1", "destination_id": "dest-1", "priority": 100,
		"stop_on_match": false, "mode": "include", "enabled": true, "severity": "critical",
		"created_at": "2026-08-01T10:00:00Z", "updated_at": "2026-08-01T10:00:00Z",
	})
	testClient := newTestClient(t, handler)

	route, createError := testClient.CreateNotificationRoute(CreateNotificationRouteRequest{
		DestinationID: "dest-1", Severity: strPtr("critical"),
	})
	if createError != nil {
		t.Fatalf("CreateNotificationRoute: %v", createError)
	}
	recorder.assert(t, http.MethodPost, "/api/v1/org/notifications/routes")
	body := recorder.decodedBody(t)
	if body["destination_id"] != "dest-1" || body["severity"] != "critical" {
		t.Errorf("body = %v", body)
	}
	for _, absent := range []string{"kind", "cluster_id", "source_id", "priority", "stop_on_match", "mode", "enabled"} {
		if _, present := body[absent]; present {
			t.Errorf("%s must be omitted so the backend default applies, body = %v", absent, body)
		}
	}
	if route.ID != "route-9" || route.Priority != 100 {
		t.Errorf("route = %+v", route)
	}
}

func TestCreateNotificationRoute_UnknownDestinationIs404WithDetail(t *testing.T) {
	_, handler := recordAlertsRequest(t, http.StatusNotFound,
		map[string]string{"detail": "Destination dest-x not found in this organisation."})
	testClient := newTestClient(t, handler)

	_, createError := testClient.CreateNotificationRoute(CreateNotificationRouteRequest{DestinationID: "dest-x"})
	var unexpected *UnexpectedResponseError
	if !errors.As(createError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("expected a 404 UnexpectedResponseError, got %v", createError)
	}
	if !strings.Contains(createError.Error(), "not found in this organisation") {
		t.Errorf("expected the backend detail, got %v", createError)
	}
}

func TestUpdateNotificationRoute_PatchesOnlySetMembers(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]interface{}{
		"id": "route-1", "organisation_id": "org-1", "destination_id": "dest-1", "priority": 5,
		"stop_on_match": false, "mode": "include", "enabled": false,
		"created_at": "2026-08-01T10:00:00Z", "updated_at": "2026-08-02T10:00:00Z",
	})
	testClient := newTestClient(t, handler)

	priority := 5
	enabled := false
	route, updateError := testClient.UpdateNotificationRoute("route-1", UpdateNotificationRouteRequest{
		Priority: &priority, Enabled: &enabled,
	})
	if updateError != nil {
		t.Fatalf("UpdateNotificationRoute: %v", updateError)
	}
	recorder.assert(t, http.MethodPatch, "/api/v1/org/notifications/routes/route-1")
	body := recorder.decodedBody(t)
	if body["priority"] != float64(5) || body["enabled"] != false {
		t.Errorf("body = %v", body)
	}
	if len(body) != 2 {
		t.Errorf("PATCH must carry only the set members, body = %v", body)
	}
	if route.Priority != 5 || route.Enabled {
		t.Errorf("route = %+v", route)
	}
}

func TestUpdateNotificationRoute_NotFound(t *testing.T) {
	_, handler := recordAlertsRequest(t, http.StatusNotFound, map[string]string{"detail": "Route not found"})
	testClient := newTestClient(t, handler)

	priority := 1
	_, updateError := testClient.UpdateNotificationRoute("route-x", UpdateNotificationRouteRequest{Priority: &priority})
	var unexpected *UnexpectedResponseError
	if !errors.As(updateError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("expected a 404 UnexpectedResponseError, got %v", updateError)
	}
}

func TestDeleteNotificationRoute_Accepts204WithoutBody(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusNoContent, nil)
	testClient := newTestClient(t, handler)

	if deleteError := testClient.DeleteNotificationRoute("route-1"); deleteError != nil {
		t.Fatalf("DeleteNotificationRoute: %v", deleteError)
	}
	recorder.assert(t, http.MethodDelete, "/api/v1/org/notifications/routes/route-1")
}

func TestDeleteNotificationRoute_NotFound(t *testing.T) {
	_, handler := recordAlertsRequest(t, http.StatusNotFound, map[string]string{"detail": "Route not found"})
	testClient := newTestClient(t, handler)

	deleteError := testClient.DeleteNotificationRoute("route-x")
	var unexpected *UnexpectedResponseError
	if !errors.As(deleteError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("expected a 404 UnexpectedResponseError, got %v", deleteError)
	}
}

func TestTestNotificationRoute_Accepts202WithDeliveryID(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusAccepted, map[string]string{"delivery_id": "delivery-42"})
	testClient := newTestClient(t, handler)

	result, testError := testClient.TestNotificationRoute("route-1")
	if testError != nil {
		t.Fatalf("TestNotificationRoute: %v", testError)
	}
	recorder.assert(t, http.MethodPost, "/api/v1/org/notifications/routes/route-1/test")
	if result.DeliveryID != "delivery-42" {
		t.Errorf("delivery id = %q, want delivery-42", result.DeliveryID)
	}
}

func TestAlertsClient_UnauthorizedMapsToErrUnauthorized(t *testing.T) {
	_, handler := recordAlertsRequest(t, http.StatusUnauthorized, map[string]string{"detail": "Not authenticated"})
	testClient := newTestClient(t, handler)

	if _, listError := testClient.ListNotificationRoutes(); !errors.Is(listError, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", listError)
	}
}
