package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	testServiceDisconnectionID = "d15c0000-0000-4000-8000-000000000001"
	testServiceConsumerID      = "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11"
	testServiceOtherInstanceID = "c9b96bb8-0000-4000-8000-0000000000a1"
)

// Lane parity: prepare and confirm hit the exact bearer routes of
// openapi.json (/api/v1/org/service-admission/instances/{instance_id}/
// disconnections and .../{disconnection_id}/confirm) with the bodies the
// platform requires: every field of the request, the choice for the
// conventional names null unless one is made, and the digest alone on
// confirm.
func TestServiceDisconnectionLaneParity(t *testing.T) {
	var calls []recordedServiceCall
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		bodyBytes, _ := io.ReadAll(request.Body)
		calls = append(calls, recordedServiceCall{request.Method, request.URL.Path, request.URL.RawQuery, string(bodyBytes)})
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("missing bearer token on %s %s", request.Method, request.URL.Path)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"id": testServiceDisconnectionID, "digest": "sha256:" + strings.Repeat("a", 64)})
	})
	ctx := context.Background()
	other := testServiceOtherInstanceID
	steps := []func() error{
		func() error {
			_, err := client.PrepareServiceDisconnection(ctx, testServiceInstanceID, ServiceDisconnectionRequest{
				ConsumerID: testServiceConsumerID, ExpectedGeneration: 2})
			return err
		},
		func() error {
			_, err := client.PrepareServiceDisconnection(ctx, testServiceInstanceID, ServiceDisconnectionRequest{
				ConsumerID: testServiceConsumerID, ExpectedGeneration: 2, Timing: ServiceDisconnectAfterNextDeploy,
				KeepNetworkAccess: true, ConnectionNamesInstanceID: &other})
			return err
		},
		func() error {
			_, err := client.PrepareServiceDisconnection(ctx, testServiceInstanceID, ServiceDisconnectionRequest{
				ConsumerID: testServiceConsumerID, ExpectedGeneration: 2, ConnectionNamesNone: true})
			return err
		},
		func() error {
			_, err := client.ConfirmServiceDisconnection(ctx, testServiceInstanceID, testServiceDisconnectionID, "sha256:abc")
			return err
		},
	}
	for index, step := range steps {
		if stepError := step(); stepError != nil {
			t.Fatalf("step %d: %v", index, stepError)
		}
	}
	disconnections := "/api/v1/org/service-admission/instances/" + testServiceInstanceID + "/disconnections"
	expected := []recordedServiceCall{
		{http.MethodPost, disconnections, "", `{"consumer_id":"` + testServiceConsumerID + `","expected_generation":2,` +
			`"timing":"after_next_deploy","keep_network_access":false,"connection_names_instance_id":null,"connection_names_none":false}`},
		{http.MethodPost, disconnections, "", `{"consumer_id":"` + testServiceConsumerID + `","expected_generation":2,` +
			`"timing":"after_next_deploy","keep_network_access":true,"connection_names_instance_id":"` + testServiceOtherInstanceID +
			`","connection_names_none":false}`},
		{http.MethodPost, disconnections, "", `{"consumer_id":"` + testServiceConsumerID + `","expected_generation":2,` +
			`"timing":"after_next_deploy","keep_network_access":false,"connection_names_instance_id":null,"connection_names_none":true}`},
		{http.MethodPost, disconnections + "/" + testServiceDisconnectionID + "/confirm", "", `{"digest":"sha256:abc"}`},
	}
	if len(calls) != len(expected) {
		t.Fatalf("expected %d calls, got %d: %+v", len(expected), len(calls), calls)
	}
	for index, want := range expected {
		if calls[index] != want {
			t.Errorf("call %d:\n got  %+v\n want %+v", index, calls[index], want)
		}
	}
}

// What no platform could accept is refused before any request: a prepare
// without a consumer or a generation, or choosing a service and nobody at
// once, and a confirm without the reviewed digest.
func TestServiceDisconnectionRefusesBeforeTheRequest(t *testing.T) {
	requests := 0
	client := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		jsonResponse(t, writer, http.StatusOK, map[string]any{})
	})
	ctx := context.Background()
	other := testServiceOtherInstanceID
	for name, request := range map[string]ServiceDisconnectionRequest{
		"no consumer":   {ExpectedGeneration: 2},
		"no generation": {ConsumerID: testServiceConsumerID},
		"two choices":   {ConsumerID: testServiceConsumerID, ExpectedGeneration: 2, ConnectionNamesNone: true, ConnectionNamesInstanceID: &other},
	} {
		if _, err := client.PrepareServiceDisconnection(ctx, testServiceInstanceID, request); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
	if _, err := client.ConfirmServiceDisconnection(ctx, testServiceInstanceID, testServiceDisconnectionID, ""); err == nil {
		t.Error("a confirmation without a digest must be refused")
	}
	if requests != 0 {
		t.Errorf("refused calls must not reach the platform, got %d requests", requests)
	}
}

// The platform's refusal of a disconnection (409: the plan moved, the review
// expired, a choice is needed) reaches the caller as its own sentence with
// the status.
func TestServiceDisconnectionRelaysPlatformRefusals(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, writer, http.StatusConflict, map[string]any{"detail": "The disconnection review has expired; review it again"})
	})
	_, err := client.ConfirmServiceDisconnection(context.Background(), testServiceInstanceID, testServiceDisconnectionID, "sha256:x")
	var unexpected *UnexpectedResponseError
	if err == nil || !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusConflict ||
		unexpected.Detail != "The disconnection review has expired; review it again" {
		t.Fatalf("expected the platform's 409 detail, got %#v", err)
	}
}

// A disconnection decodes every field of the platform's document, keeps
// enumeration values a newer platform adds as they were sent, and carries
// them through -o json and -o yaml.
func TestServiceDisconnectionDecodesAndKeepsUnknownValues(t *testing.T) {
	raw := `{"id":"` + testServiceDisconnectionID + `","instance_id":"` + testServiceInstanceID + `","digest":"sha256:` + strings.Repeat("a", 64) + `",
	"state":"in_progress","created_at":"2026-11-01T10:00:00Z","expires_at":"2026-11-01T10:10:00Z","confirmed_at":"2026-11-01T10:01:00Z",
	"phase":"settling","settled_at":null,
	"outcome":{"connection_secret":"archived","network":"left","network_reason":"still_needed","removed_namespaces":[],"observed_at":"2026-11-01T10:05:00Z"},
	"running_pods":{"count":3,"names":["api-1","api-2"]},
	"plan":{"schema_version":1,"instance_id":"` + testServiceInstanceID + `","organisation_id":"o","actor_id":"a","service_name":"orders-db",
	"cluster_id":"` + testServiceClusterID + `","generation":2,"engine":"PostgreSQL","protocol":"postgres",
	"consumer":{"id":"` + testServiceConsumerID + `","application_id":"app","cluster_id":"` + testServiceClusterID + `","namespace":"orders"},
	"on_service_cluster":true,
	"credential":{"kind":"per_application","other_consumers":1,"statement":"S1","cannot_guarantee":"S2"},
	"connection_names":{"variables":["DATABASE_URL"],"conventional":["DATABASE_URL"],"carries_conventional":true,"choice_required":true,
	"choices":[{"instance_id":"` + testServiceOtherInstanceID + `","service_name":"orders-db-v2","none":false,"installing":true}],
	"choice":{"instance_id":null,"service_name":"","none":true,"installing":false},"statement":"S3"},
	"connection_secret":{"secret_name":"orders-db-connection","delivered":true,"statement":"S4"},
	"workloads":{"state":"partially_known","attached_by_ankra":["Deployment/api"],"written_by_hand":[],"statement":"S5"},
	"network":{"namespace":"orders","narrows":false,"keep_network_access":false,"reason":"mesh_policy","port":5432,"statement":"S6"},
	"data":"S7","timing":{"mode":"after_next_deploy","deploy_now_offered":true,"now_offered":false,"statement":"S8"},
	"kept":["S9"],"rotation":"S10","expires_at":"2026-11-01T10:10:00Z"}}`
	var disconnection ServiceDisconnection
	if err := json.Unmarshal([]byte(raw), &disconnection); err != nil {
		t.Fatalf("decode: %v", err)
	}
	plan := disconnection.Plan
	if disconnection.Phase == nil || *disconnection.Phase != "settling" || plan.Workloads.State != "partially_known" ||
		plan.Network.Reason != "mesh_policy" || plan.Credential.Kind != "per_application" ||
		disconnection.Outcome == nil || disconnection.Outcome.ConnectionSecret != "archived" || disconnection.Outcome.NetworkReason != "still_needed" {
		t.Fatalf("unknown values must decode as sent: %+v", disconnection)
	}
	if !plan.ConnectionNames.ChoiceRequired || len(plan.ConnectionNames.Choices) != 1 || !plan.ConnectionNames.Choices[0].Installing ||
		plan.ConnectionNames.Choice == nil || !plan.ConnectionNames.Choice.None || plan.ConnectionNames.Choice.InstanceID != nil {
		t.Fatalf("the choices and the choice must decode: %+v", plan.ConnectionNames)
	}
	if disconnection.RunningPods == nil || disconnection.RunningPods.Count != 3 || len(disconnection.RunningPods.Names) != 2 {
		t.Fatalf("running pods must decode: %+v", disconnection.RunningPods)
	}
	for _, statement := range []string{plan.Credential.Statement, plan.Credential.CannotGuarantee, plan.ConnectionNames.Statement,
		plan.ConnectionSecret.Statement, plan.Workloads.Statement, plan.Network.Statement, plan.Data, plan.Timing.Statement,
		plan.Kept[0], plan.Rotation} {
		if !strings.HasPrefix(statement, "S") {
			t.Errorf("a statement did not decode: %q", statement)
		}
	}
	encodedJSON, err := json.Marshal(disconnection)
	if err != nil {
		t.Fatal(err)
	}
	encodedYAML, err := yaml.Marshal(disconnection)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"settling", "partially_known", "mesh_policy", "per_application", "archived"} {
		if !strings.Contains(string(encodedJSON), `"`+value+`"`) || !strings.Contains(string(encodedYAML), value) {
			t.Errorf("%s must survive -o json and -o yaml", value)
		}
	}
}

// The single instance read's disconnections in progress decode with their
// phase as sent; a listing, which carries none, stays without the field when
// encoded again rather than reading as an empty answer.
func TestServiceInstanceCarriesDisconnectionsInProgress(t *testing.T) {
	raw := `{"id":"` + testServiceInstanceID + `","name":"orders-db","consumers":[],"disconnections":{"state":"known","items":[
	{"id":"` + testServiceDisconnectionID + `","consumer_id":"` + testServiceConsumerID + `","application_id":"app","cluster_id":"` +
		testServiceClusterID + `","namespace":"orders","phase":"draining","requested_at":"2026-11-01T10:01:00Z",
	"phase_changed_at":"2026-11-01T10:01:00Z","keeps_connection_secret":true},
	{"id":"d2","consumer_id":"c2","application_id":"app","cluster_id":"` + testServiceClusterID + `","namespace":"qa","phase":"settling",
	"requested_at":"2026-11-01T09:00:00Z","phase_changed_at":"2026-11-01T09:30:00Z","keeps_connection_secret":false}]}}`
	var instance ServiceInstance
	if err := json.Unmarshal([]byte(raw), &instance); err != nil {
		t.Fatal(err)
	}
	disconnections := instance.Disconnections
	if disconnections == nil || disconnections.State != ServiceDisconnectionsKnown || len(disconnections.Items) != 2 ||
		disconnections.Items[0].Phase != ServiceDisconnectionDraining || !disconnections.Items[0].KeepsConnectionSecret ||
		disconnections.Items[1].Phase != "settling" {
		t.Fatalf("the disconnections in progress must decode as sent: %+v", disconnections)
	}

	var listed ServiceInstance
	if err := json.Unmarshal([]byte(`{"id":"`+testServiceInstanceID+`","name":"orders-db"}`), &listed); err != nil {
		t.Fatal(err)
	}
	encodedJSON, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	encodedYAML, err := yaml.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if listed.Disconnections != nil || strings.Contains(string(encodedJSON), "disconnections") || strings.Contains(string(encodedYAML), "disconnections") {
		t.Errorf("an instance read without disconnections must stay without them:\n%s\n%s", encodedJSON, encodedYAML)
	}
}
