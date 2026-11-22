package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// These pin the method, path and body of the kept checks read and of "Check
// now" of one environment, and that each answer keeps the platform's document.

const provenanceChecksTestBody = `{"application_id": "app-1", "read_state": "ok", "kept_from": "2026-11-01T00:00:00Z",
  "environments": [{"environment_id": "env-1", "placements": [
    {"installation_id": "inst-1", "cluster_id": "cl-1", "namespace": "shop", "last_check": {
      "checked_at": "2026-11-18T09:30:00Z", "source": "check_now", "by": {"kind": "member", "name": "Ada"},
      "image_tag": "sha-1", "mode": "warn", "decision": "deploy_with_finding", "state": "refused",
      "message": "registry.example/shop:sha-1 carries no attestation", "images": [{"image": "registry.example/shop:sha-1",
      "digest": null, "state": "refused", "reason": "unsigned", "message": "no attestation", "key_id": null,
      "pipeline_run_id": null, "commit_sha": null}], "unchecked_images": [], "images_capped": false, "is_current": false}},
    {"installation_id": "inst-2", "cluster_id": "cl-2", "namespace": null, "last_check": null}],
  "placements_capped": false, "placements_not_checkable": 1}], "a_newer_field": true}`

func writeProvenanceChecksAnswer(writer http.ResponseWriter, status int, body string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(body))
}

func TestGetApplicationProvenanceChecksReadsEachPlacementAndKeepsTheDocument(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/org/applications/app-1/provenance/checks" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		writeProvenanceChecksAnswer(writer, http.StatusOK, provenanceChecksTestBody)
	})

	checks, readError := testClient.GetApplicationProvenanceChecks(context.Background(), "app-1")

	if readError != nil {
		t.Fatalf("GetApplicationProvenanceChecks error = %v", readError)
	}
	if checks.ReadState != ProvenanceChecksReadOK || len(checks.Environments) != 1 {
		t.Fatalf("checks = %+v", checks)
	}
	environment := checks.Environments[0]
	if len(environment.Placements) != 2 || environment.PlacementsNotCheckable != 1 {
		t.Fatalf("environment = %+v", environment)
	}
	kept := environment.Placements[0].LastCheck
	if kept == nil || kept.State != "refused" || kept.By == nil || kept.By.Kind != "member" ||
		kept.IsCurrent == nil || *kept.IsCurrent || len(kept.Images) != 1 || kept.Images[0].Digest != nil {
		t.Errorf("kept check = %+v", kept)
	}
	if environment.Placements[1].LastCheck != nil || environment.Placements[1].Namespace != nil {
		t.Errorf("a placement with no kept check = %+v", environment.Placements[1])
	}
	if !strings.Contains(string(checks.RawJSON()), "a_newer_field") {
		t.Errorf("the platform's document was not kept: %s", checks.RawJSON())
	}
}

func TestGetApplicationProvenanceChecksUnreadableIsAnEmptyListWithItsState(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writeProvenanceChecksAnswer(writer, http.StatusOK,
			`{"application_id": "app-1", "read_state": "unknown", "kept_from": "2026-11-01T00:00:00Z", "environments": null}`)
	})

	checks, readError := testClient.GetApplicationProvenanceChecks(context.Background(), "app-1")

	if readError != nil {
		t.Fatalf("GetApplicationProvenanceChecks error = %v", readError)
	}
	if checks.ReadState != ProvenanceChecksReadUnknown || checks.Environments == nil {
		t.Errorf("checks = %+v", checks)
	}
}

func TestCheckApplicationProvenanceEnvironmentPostsOnlyTheEnvironment(t *testing.T) {
	answer := `{"application_id": "app-1", "mode": "enforce", "decision": "refuse",
	  "verdict": {"state": "refused", "images": [{"image": "registry.example/shop:sha-1", "state": "refused", "reason": "unsigned", "message": "no attestation"}], "unchecked_images": []},
	  "checks": [{"installation_id": "inst-1", "cluster_id": "cl-1", "namespace": "shop", "checked_at": "2026-11-19T08:00:00Z",
	  "image_tag": "sha-1", "state": "refused", "message": "no attestation"}], "checks_capped": false, "images_capped": false}`
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/applications/app-1/provenance/verify" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		payload, _ := io.ReadAll(request.Body)
		var body map[string]any
		if decodeError := json.Unmarshal(payload, &body); decodeError != nil || len(body) != 1 || body["environment_id"] != "env-1" {
			t.Errorf("body = %s", payload)
		}
		writeProvenanceChecksAnswer(writer, http.StatusOK, answer)
	})

	check, checkError := testClient.CheckApplicationProvenanceEnvironment(context.Background(), "app-1", "env-1")

	if checkError != nil {
		t.Fatalf("CheckApplicationProvenanceEnvironment error = %v", checkError)
	}
	if check.Decision != "refuse" || len(check.Checks) != 1 || check.Checks[0].State != "refused" || len(check.Verdict.Images) != 1 {
		t.Errorf("check = %+v", check)
	}
	if string(check.RawJSON()) != answer {
		t.Errorf("the platform's document was not kept: %s", check.RawJSON())
	}
}

func TestCheckApplicationProvenanceEnvironmentRefusalsKeepThePlatformsSentence(t *testing.T) {
	cases := map[string]struct {
		status int
		detail string
	}{
		"nothing to check":    {http.StatusConflict, "This environment has no installation whose images Ankra can check"},
		"not found":           {http.StatusNotFound, "Environment not found"},
		"environments unread": {http.StatusServiceUnavailable, "The application's environments could not be read; retry shortly"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				writeProvenanceChecksAnswer(writer, testCase.status, `{"detail": "`+testCase.detail+`"}`)
			})

			_, checkError := testClient.CheckApplicationProvenanceEnvironment(context.Background(), "app-1", "env-1")

			var unexpected *UnexpectedResponseError
			if !errors.As(checkError, &unexpected) || unexpected.StatusCode != testCase.status || unexpected.Detail != testCase.detail {
				t.Errorf("error = %v (%T)", checkError, checkError)
			}
		})
	}
}
