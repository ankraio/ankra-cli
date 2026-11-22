package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// These pin the method, path and body of the application alert routes, and
// that a refusal arrives as the platform's own sentence.

const applicationAlertTestBody = `{"alert_id": "alert-1", "rule_id": "rule-1", "name": "Errors", "own": true,
  "environment_id": "env-1", "says": "failed requests above 5% for 10 minutes",
  "condition": {"condition_type": "app_metric", "metric": "failed_share_pct", "operator": "gt", "threshold": 5, "hold_minutes": 10},
  "severity": "warning", "send_to": [{"integration_id": "dest-1", "type": "slack", "label": "ops-slack"}],
  "enabled": true, "last_firing": null, "evaluated_at": null, "evaluation": "not_evaluated", "unknown_reason": null}`

func writeApplicationAlertAnswer(writer http.ResponseWriter, status int, body string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(body))
}

func TestListApplicationAlertsTargetsTheRouteAndKeepsTheDocument(t *testing.T) {
	listBody := `{"alerts": [` + applicationAlertTestBody + `], "truncated": false, "limit": 100,
	  "covered_by_organisation_rules": true, "a_newer_field": 1}`
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != "/api/v1/org/applications/app-1/alerts" {
			t.Errorf("path = %s", request.URL.Path)
		}
		if request.URL.Query().Get("environment_id") != "env-1" {
			t.Errorf("query = %s", request.URL.RawQuery)
		}
		writeApplicationAlertAnswer(writer, http.StatusOK, listBody)
	})

	list, listError := testClient.ListApplicationAlerts(context.Background(), "app-1", "env-1")

	if listError != nil {
		t.Fatalf("ListApplicationAlerts error = %v", listError)
	}
	if len(list.Alerts) != 1 || list.Alerts[0].Condition == nil || list.Alerts[0].Condition.HoldMinutes != 10 {
		t.Errorf("alerts = %+v", list.Alerts)
	}
	if list.CoveredByOrganisationRules == nil || !*list.CoveredByOrganisationRules {
		t.Errorf("covered_by_organisation_rules = %v", list.CoveredByOrganisationRules)
	}
	if !strings.Contains(string(list.RawJSON()), "a_newer_field") {
		t.Errorf("the platform's document was not kept: %s", list.RawJSON())
	}
}

func TestListApplicationAlertsWithoutEnvironmentSendsNoQuery(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.RawQuery != "" {
			t.Errorf("query = %q, want none", request.URL.RawQuery)
		}
		writeApplicationAlertAnswer(writer, http.StatusOK, `{"alerts": null, "truncated": false, "limit": 100, "covered_by_organisation_rules": null}`)
	})

	list, listError := testClient.ListApplicationAlerts(context.Background(), "app-1", "")

	if listError != nil {
		t.Fatalf("ListApplicationAlerts error = %v", listError)
	}
	if list.Alerts == nil || list.CoveredByOrganisationRules != nil {
		t.Errorf("list = %+v", list)
	}
}

func TestCreateApplicationAlertPostsTheBodyAndAcceptsCreated(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/applications/app-1/alerts" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		condition, _ := body["condition"].(map[string]any)
		if body["name"] != "Errors" || body["environment_id"] != "env-1" || condition["metric"] != "failed_share_pct" {
			t.Errorf("body = %v", body)
		}
		if _, hasOperator := condition["operator"]; hasOperator {
			t.Errorf("an empty operator was sent: %v", condition)
		}
		if _, hasSeverity := body["severity"]; hasSeverity {
			t.Errorf("an empty severity was sent: %v", body)
		}
		writeApplicationAlertAnswer(writer, http.StatusCreated, applicationAlertTestBody)
	})

	created, createError := testClient.CreateApplicationAlert(context.Background(), "app-1", ApplicationAlertRequest{
		Name:          "Errors",
		EnvironmentID: "env-1",
		Condition:     ApplicationAlertCondition{ConditionType: "app_metric", Metric: "failed_share_pct", Threshold: 5, HoldMinutes: 10},
		SendTo:        []string{"dest-1"},
	})

	if createError != nil {
		t.Fatalf("CreateApplicationAlert error = %v", createError)
	}
	if created.AlertID != "alert-1" || created.Evaluation != "not_evaluated" {
		t.Errorf("created = %+v", created)
	}
}

func TestCreateApplicationAlertRefusalIsThePlatformsSentence(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writeApplicationAlertAnswer(writer, http.StatusBadRequest, `{"detail": "Connect a place to send alerts to first."}`)
	})

	_, createError := testClient.CreateApplicationAlert(context.Background(), "app-1", ApplicationAlertRequest{Name: "Errors"})

	var unexpected *UnexpectedResponseError
	if !errors.As(createError, &unexpected) || unexpected.StatusCode != http.StatusBadRequest ||
		createError.Error() != "Connect a place to send alerts to first." {
		t.Errorf("error = %v", createError)
	}
}

func TestUpdateApplicationAlertPutsToTheAlert(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/api/v1/org/applications/app-1/alerts/alert-1" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["severity"] != "critical" {
			t.Errorf("body = %v", body)
		}
		writeApplicationAlertAnswer(writer, http.StatusOK, applicationAlertTestBody)
	})

	_, updateError := testClient.UpdateApplicationAlert(context.Background(), "app-1", "alert-1",
		ApplicationAlertRequest{Name: "Errors", Severity: "critical"})

	if updateError != nil {
		t.Fatalf("UpdateApplicationAlert error = %v", updateError)
	}
}

func TestDeleteApplicationAlertTargetsTheAlertAndReadsTheResult(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/api/v1/org/applications/app-1/alerts/alert-1" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		writeApplicationAlertAnswer(writer, http.StatusOK, `{"success": true, "message": "Alert deleted"}`)
	})

	result, deleteError := testClient.DeleteApplicationAlert(context.Background(), "app-1", "alert-1")

	if deleteError != nil {
		t.Fatalf("DeleteApplicationAlert error = %v", deleteError)
	}
	if !result.Success || result.Message != "Alert deleted" {
		t.Errorf("result = %+v", result)
	}
}

func TestDeleteApplicationAlertRefusalsKeepTheirClass(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		check  func(t *testing.T, deleteError error)
	}{
		"permission denied": {http.StatusForbidden, `{"detail": "permission_denied", "permission": "applications.write", "scope_type": "application"}`,
			func(t *testing.T, deleteError error) {
				var denied *PermissionDeniedError
				if !errors.As(deleteError, &denied) || denied.Permission != "applications.write" {
					t.Errorf("error = %v, want a permission denial naming applications.write", deleteError)
				}
			}},
		"organisation alert": {http.StatusConflict, `{"detail": "Change it under Alerts."}`,
			func(t *testing.T, deleteError error) {
				var unexpected *UnexpectedResponseError
				if !errors.As(deleteError, &unexpected) || unexpected.StatusCode != http.StatusConflict ||
					unexpected.Detail != "Change it under Alerts." {
					t.Errorf("error = %v", deleteError)
				}
			}},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				writeApplicationAlertAnswer(writer, testCase.status, testCase.body)
			})

			_, deleteError := testClient.DeleteApplicationAlert(context.Background(), "app-1", "alert-1")

			testCase.check(t, deleteError)
		})
	}
}
