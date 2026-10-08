package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testServiceInstanceID   = "c9b96bb8-0000-4000-8000-000000000003"
	testServiceRetirementID = "7d3c1f0a-5b2e-4c9d-8e1f-0a2b3c4d5e6f"
	testServiceReviewID     = "0b6f2a8e-3a1c-4b8e-9a51-2c7d4f1e9a20"
	testServiceClusterID    = "c1a2b3c4-0000-4000-8000-000000000001"
)

type recordedServiceCall struct {
	method string
	path   string
	query  string
	body   string
}

// Lane parity: each method hits the exact bearer route, method, query and
// body the platform serves (openapi.json, /api/v1/org/service-packages and
// /api/v1/org/service-admission). A typo here is a 404 against a real
// platform that every command test with a mock would miss.
func TestManagedServicesLaneParity(t *testing.T) {
	var calls []recordedServiceCall
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		bodyBytes, _ := io.ReadAll(request.Body)
		calls = append(calls, recordedServiceCall{request.Method, request.URL.Path, request.URL.RawQuery, string(bodyBytes)})
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("missing bearer token on %s %s", request.Method, request.URL.Path)
		}
		switch {
		case strings.HasSuffix(request.URL.Path, "/confirm") || strings.HasSuffix(request.URL.Path, "/retirements") && request.Method == http.MethodPost:
			jsonResponse(t, writer, http.StatusOK, map[string]any{"id": testServiceRetirementID, "digest": "sha256:" + strings.Repeat("b", 64)})
		default:
			jsonResponse(t, writer, http.StatusOK, map[string]any{"items": []any{}, "next_cursor": nil})
		}
	})
	ctx := context.Background()
	page := ServicePageOptions{Limit: 50, After: "11111111-1111-4111-8111-111111111111"}

	steps := []func() error{
		func() error { _, err := client.ListServicePackages(ctx, page); return err },
		func() error {
			_, err := client.GetServicePackage(ctx, "a5e62beb-cbe7-4340-9986-635e09cf086e")
			return err
		},
		func() error { _, err := client.GetServiceClusterPolicy(ctx, testServiceClusterID); return err },
		func() error {
			_, err := client.SetServiceClusterPolicy(ctx, testServiceClusterID, ServiceClusterPolicyRequest{Region: "eu-north-1", DataBoundary: "eu"})
			return err
		},
		func() error {
			_, err := client.ListServiceConsumers(ctx, "0a000000-0000-4000-8000-000000000002", ServicePageOptions{})
			return err
		},
		func() error {
			_, err := client.GetServiceConsumer(ctx, "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11")
			return err
		},
		func() error {
			_, err := client.BindServiceConsumer(ctx, ServiceConsumerRequest{ApplicationID: "0a000000-0000-4000-8000-000000000002",
				ClusterID: testServiceClusterID, Namespace: "orders"})
			return err
		},
		func() error {
			_, err := client.PrepareServiceReview(ctx, ServiceReviewRequest{PackageVersionID: "a5e62beb-cbe7-4340-9986-635e09cf086e",
				Name: "orders-db", Mode: "customer", Region: "eu-north-1", DataBoundary: "eu", ClusterID: testServiceClusterID,
				ConsumerIDs: []string{"5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11"}})
			return err
		},
		func() error { _, err := client.ListServiceReviews(ctx, ServicePageOptions{}); return err },
		func() error { _, err := client.GetServiceReview(ctx, testServiceReviewID); return err },
		func() error {
			_, err := client.ConfirmServiceReview(ctx, testServiceReviewID, "sha256:abc")
			return err
		},
		func() error {
			_, err := client.ListServiceInstances(ctx, ServiceInstanceListOptions{ServicePageOptions: page})
			return err
		},
		func() error { _, err := client.GetServiceInstance(ctx, testServiceInstanceID); return err },
		func() error {
			_, err := client.PrepareServiceRetirement(ctx, testServiceInstanceID, ServiceRetirementRequest{
				ExpectedGeneration: 2, AcknowledgeDataLoss: true})
			return err
		},
		func() error {
			_, err := client.ListServiceRetirements(ctx, testServiceInstanceID, ServiceRetirementListOptions{
				ServicePageOptions: ServicePageOptions{Limit: 50, After: testServiceRetirementID}, State: "pending"})
			return err
		},
		func() error {
			_, err := client.GetServiceRetirement(ctx, testServiceInstanceID, testServiceRetirementID)
			return err
		},
		func() error {
			_, err := client.ConfirmServiceRetirement(ctx, testServiceInstanceID, testServiceRetirementID, "sha256:def")
			return err
		},
	}
	for index, step := range steps {
		if stepError := step(); stepError != nil {
			t.Fatalf("step %d: %v", index, stepError)
		}
	}

	admission := "/api/v1/org/service-admission"
	expected := []recordedServiceCall{
		{http.MethodGet, "/api/v1/org/service-packages", "after=11111111-1111-4111-8111-111111111111&limit=50", ""},
		{http.MethodGet, "/api/v1/org/service-packages/a5e62beb-cbe7-4340-9986-635e09cf086e", "", ""},
		{http.MethodGet, admission + "/cluster-policies/" + testServiceClusterID, "", ""},
		{http.MethodPut, admission + "/cluster-policies/" + testServiceClusterID, "",
			`{"expected_revision":0,"region":"eu-north-1","data_boundary":"eu","local_only":false}`},
		{http.MethodGet, admission + "/consumers", "application_id=0a000000-0000-4000-8000-000000000002", ""},
		{http.MethodGet, admission + "/consumers/5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11", "", ""},
		{http.MethodPost, admission + "/consumers", "",
			`{"application_id":"0a000000-0000-4000-8000-000000000002","cluster_id":"` + testServiceClusterID +
				`","namespace":"orders","allow_planned":false,"local_only":false,"expected_revision":0}`},
		// Parameters and secret_references are required objects: a review
		// that chose no parameters sends {} rather than null.
		{http.MethodPost, admission + "/reviews", "",
			`{"package_version_id":"a5e62beb-cbe7-4340-9986-635e09cf086e","name":"orders-db","mode":"customer",` +
				`"region":"eu-north-1","data_boundary":"eu","cluster_id":"` + testServiceClusterID +
				`","consumer_ids":["5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11"],"parameters":{},"secret_references":{}}`},
		{http.MethodGet, admission + "/reviews", "", ""},
		{http.MethodGet, admission + "/reviews/" + testServiceReviewID, "", ""},
		{http.MethodPost, admission + "/reviews/" + testServiceReviewID + "/confirm", "", `{"digest":"sha256:abc"}`},
		{http.MethodGet, admission + "/instances", "after=11111111-1111-4111-8111-111111111111&limit=50", ""},
		{http.MethodGet, admission + "/instances/" + testServiceInstanceID, "", ""},
		// disconnect_consumer_ids is never null: an empty list is sent.
		{http.MethodPost, admission + "/instances/" + testServiceInstanceID + "/retirements", "",
			`{"expected_generation":2,"disconnect_consumer_ids":[],"acknowledge_data_loss":true}`},
		{http.MethodGet, admission + "/instances/" + testServiceInstanceID + "/retirements",
			"after=" + testServiceRetirementID + "&limit=50&state=pending", ""},
		{http.MethodGet, admission + "/instances/" + testServiceInstanceID + "/retirements/" + testServiceRetirementID, "", ""},
		{http.MethodPost, admission + "/instances/" + testServiceInstanceID + "/retirements/" + testServiceRetirementID + "/confirm", "",
			`{"digest":"sha256:def"}`},
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

// Both confirmations carry the reviewed digest and nothing else, and refuse
// to send a confirmation without one. A retirement prepare is refused before
// any request unless the data loss was acknowledged.
func TestManagedServicesConfirmationsNeedDigestAndAcknowledgement(t *testing.T) {
	requests := 0
	client := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		jsonResponse(t, writer, http.StatusOK, map[string]any{})
	})
	ctx := context.Background()
	if _, err := client.ConfirmServiceReview(ctx, testServiceReviewID, ""); err == nil {
		t.Error("a review confirmation without a digest must be refused")
	}
	if _, err := client.ConfirmServiceRetirement(ctx, testServiceInstanceID, testServiceRetirementID, ""); err == nil {
		t.Error("a retirement confirmation without a digest must be refused")
	}
	if _, err := client.PrepareServiceRetirement(ctx, testServiceInstanceID, ServiceRetirementRequest{ExpectedGeneration: 1}); err == nil ||
		!strings.Contains(err.Error(), "acknowledge") {
		t.Errorf("a retirement without the data-loss acknowledgement must be refused, got %v", err)
	}
	if requests != 0 {
		t.Errorf("refused calls must not reach the platform, got %d requests", requests)
	}
}

// The global --org flag reaches these routes as it does every other: the
// override header rides each request.
func TestManagedServicesHonourOrganisationOverride(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = append(seen, request.Header.Get(orgOverrideHeader))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"items":[],"next_cursor":null}`))
	}))
	t.Cleanup(server.Close)
	client := New(testToken, server.URL)
	client.SetOrganisationOverride("3b7dccca-0788-4470-9910-19478ae345ae")
	if _, err := client.ListServiceInstances(context.Background(), ServiceInstanceListOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListServicePackages(context.Background(), ServicePageOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, header := range seen {
		if header != "3b7dccca-0788-4470-9910-19478ae345ae" {
			t.Errorf("expected the organisation override header on every request, got %q", header)
		}
	}
}

// The platform's refusals reach the caller as its own sentence with the
// status, so the commands can tell a stale digest (409) from a missing
// resource (404) and exit accordingly.
func TestManagedServicesRelayPlatformRefusals(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, writer, http.StatusConflict, map[string]any{"detail": "Service retirement has changed"})
	})
	_, err := client.ConfirmServiceRetirement(context.Background(), testServiceInstanceID, testServiceRetirementID, "sha256:x")
	var unexpected *UnexpectedResponseError
	if err == nil || !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusConflict ||
		unexpected.Detail != "Service retirement has changed" {
		t.Fatalf("expected the platform's 409 detail, got %#v", err)
	}
}

// An instance decodes into a shape with nowhere to keep a credential value:
// even a response that carried one would not survive a round trip.
func TestServiceInstanceHasNoPlaceForCredentialValues(t *testing.T) {
	raw := `{"id":"` + testServiceInstanceID + `","name":"orders-db","connection":{"namespace":"orders-db",` +
		`"endpoints":[{"name":"rw","address":"postgresql-rw.orders-db.svc:5432"}],` +
		`"secret":{"name":"postgresql-app","keys":["password","uri"],"password":"hunter2-plaintext"},"password":"hunter2-plaintext"},` +
		`"credentials":{"password":"hunter2-plaintext"}}`
	var instance ServiceInstance
	if err := json.Unmarshal([]byte(raw), &instance); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(instance)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "hunter2") {
		t.Fatalf("a credential value survived decoding: %s", encoded)
	}
	if instance.Connection == nil || instance.Connection.Secret.Name != "postgresql-app" || len(instance.Connection.Secret.Keys) != 2 {
		t.Fatalf("the Secret reference itself must survive: %+v", instance.Connection)
	}
}

// Unbinding carries the revision as the expected_revision query on DELETE,
// accepts the platform's bodyless 204, and refuses a revision below 1
// without a request.
func TestUnbindServiceConsumerLane(t *testing.T) {
	var calls []recordedServiceCall
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		bodyBytes, _ := io.ReadAll(request.Body)
		calls = append(calls, recordedServiceCall{request.Method, request.URL.Path, request.URL.RawQuery, string(bodyBytes)})
		writer.WriteHeader(http.StatusNoContent)
	})
	if err := client.UnbindServiceConsumer(context.Background(), "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11", 3); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if err := client.UnbindServiceConsumer(context.Background(), "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11", 0); err == nil {
		t.Error("an unbind without a revision must be refused")
	}
	want := []recordedServiceCall{{http.MethodDelete, "/api/v1/org/service-admission/consumers/5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11", "expected_revision=3", ""}}
	if len(calls) != 1 || calls[0] != want[0] {
		t.Fatalf("got %+v, want %+v", calls, want)
	}
}
