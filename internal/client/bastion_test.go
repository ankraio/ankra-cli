package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestUpdateHetznerBastionInstanceType_SubmittedWithoutWait(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner/cluster-123/bastion/instance-type" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("wait"); got != "false" {
			t.Errorf("wait query = %q, want false", got)
		}
		var body UpdateInstanceTypeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.InstanceType != "cx31" {
			t.Errorf("InstanceType = %s, want cx31", body.InstanceType)
		}
		jsonResponse(t, w, http.StatusAccepted, AsyncWriteAcceptedResponse{Status: "accepted"})
	}
	testClient := newTestClient(t, handler)
	result, submitted, err := testClient.UpdateHetznerBastionInstanceType(context.Background(), "cluster-123", "cx31", false)
	if err != nil {
		t.Fatalf("UpdateHetznerBastionInstanceType: %v", err)
	}
	if !submitted {
		t.Error("submitted = false, want true")
	}
	if result != nil {
		t.Errorf("result = %+v, want nil", result)
	}
}

func TestUpdateHetznerBastionInstanceType_WaitReturnsResult(t *testing.T) {
	expectedResponse := UpdateBastionInstanceTypeResult{
		NodeID:       "node-789",
		Kind:         "hetzner_bastion",
		Name:         "bastion",
		InstanceType: "cx31",
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("wait"); got != "true" {
			t.Errorf("wait query = %q, want true", got)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, submitted, err := testClient.UpdateHetznerBastionInstanceType(context.Background(), "cluster-123", "cx31", true)
	if err != nil {
		t.Fatalf("UpdateHetznerBastionInstanceType: %v", err)
	}
	if submitted {
		t.Error("submitted = true, want false")
	}
	if result == nil || result.Name != "bastion" || result.InstanceType != "cx31" {
		t.Errorf("result = %+v, want bastion resized to cx31", result)
	}
}

func TestUpdateOvhBastionInstanceType_Path(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/ovh/cluster-1/bastion/instance-type" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, UpdateBastionInstanceTypeResult{NodeID: "n1", Name: "gateway", InstanceType: "b2-7"})
	}
	testClient := newTestClient(t, handler)
	if _, _, err := testClient.UpdateOvhBastionInstanceType(context.Background(), "cluster-1", "b2-7", true); err != nil {
		t.Fatalf("UpdateOvhBastionInstanceType: %v", err)
	}
}

func TestUpdateUpcloudBastionInstanceType_Path(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/upcloud/cluster-1/bastion/instance-type" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, UpdateBastionInstanceTypeResult{NodeID: "n1", Name: "gateway", InstanceType: "1xCPU-1GB"})
	}
	testClient := newTestClient(t, handler)
	if _, _, err := testClient.UpdateUpcloudBastionInstanceType(context.Background(), "cluster-1", "1xCPU-1GB", true); err != nil {
		t.Fatalf("UpdateUpcloudBastionInstanceType: %v", err)
	}
}

func TestUpdateDigitaloceanBastionInstanceType_Path(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/digitalocean/cluster-1/bastion/instance-type" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, UpdateBastionInstanceTypeResult{NodeID: "n1", Name: "gateway", InstanceType: "s-1vcpu-1gb"})
	}
	testClient := newTestClient(t, handler)
	if _, _, err := testClient.UpdateDigitaloceanBastionInstanceType(context.Background(), "cluster-1", "s-1vcpu-1gb", true); err != nil {
		t.Fatalf("UpdateDigitaloceanBastionInstanceType: %v", err)
	}
}

func TestUpdateHetznerBastionInstanceType_InvalidStateSurfacesDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("wait"); got != "true" {
			t.Errorf("wait query = %q, want true", got)
		}
		jsonResponse(t, w, http.StatusConflict, map[string]string{"detail": "No bastion or gateway node found for this cluster"})
	}
	testClient := newTestClient(t, handler)
	_, _, err := testClient.UpdateHetznerBastionInstanceType(context.Background(), "cluster-123", "cx31", true)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if err.Error() == "" {
		t.Error("expected a non-empty error message")
	}
}

// The health read is a plain GET of the recorded verdict; every member of
// the platform's document has to survive the round trip, because the CLI
// renders the failure fields only when the loop actually wrote them.
func TestGetHetznerBastionHealth_ReturnsTheRecordedVerdict(t *testing.T) {
	expectedResponse := BastionHealthResult{
		ResourceID:          "11111111-1111-1111-1111-111111111111",
		Kind:                "hetzner_bastion",
		Provider:            "hetzner",
		State:               "offline",
		Hop:                 "bastion",
		Detail:              "ssh dial timed out",
		ConsecutiveFailures: 3,
		VMStatus:            "running",
		CheckedAt:           "2026-08-17T10:00:00",
		DiagnoseSupported:   true,
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner/cluster-123/bastion/health" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetHetznerBastionHealth("cluster-123")
	if err != nil {
		t.Fatalf("GetHetznerBastionHealth: %v", err)
	}
	if *result != expectedResponse {
		t.Errorf("result = %+v, want %+v", *result, expectedResponse)
	}
}

func TestGetOvhBastionHealth_Path(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/ovh/cluster-1/bastion/health" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, BastionHealthResult{Provider: "ovh"})
	}
	testClient := newTestClient(t, handler)
	if _, err := testClient.GetOvhBastionHealth("cluster-1"); err != nil {
		t.Fatalf("GetOvhBastionHealth: %v", err)
	}
}

func TestGetUpcloudBastionHealth_Path(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/upcloud/cluster-1/bastion/health" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, BastionHealthResult{Provider: "upcloud"})
	}
	testClient := newTestClient(t, handler)
	if _, err := testClient.GetUpcloudBastionHealth("cluster-1"); err != nil {
		t.Fatalf("GetUpcloudBastionHealth: %v", err)
	}
}

func TestGetDigitaloceanBastionHealth_Path(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/digitalocean/cluster-1/bastion/health" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, BastionHealthResult{Provider: "digitalocean"})
	}
	testClient := newTestClient(t, handler)
	if _, err := testClient.GetDigitaloceanBastionHealth("cluster-1"); err != nil {
		t.Fatalf("GetDigitaloceanBastionHealth: %v", err)
	}
}

// A cluster with no bastion answers 404 with the platform's own wording. It
// has to reach the user verbatim and keep the 404 status, which is what maps
// the command onto the not-found exit code.
func TestGetHetznerBastionHealth_NoBastionSurfacesDetail(t *testing.T) {
	const refusal = "This cluster has no bastion resource. Bastions exist only on clusters Ankra provisioned; " +
		"imported clusters are reached through their agent instead."
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"detail": refusal})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.GetHetznerBastionHealth("cluster-123")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if err.Error() != refusal {
		t.Errorf("error = %q, want the platform's refusal wording", err.Error())
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Errorf("expected a 404 UnexpectedResponseError, got %#v", err)
	}
}

func TestDiagnoseHetznerBastion_ReturnsTheReport(t *testing.T) {
	expectedResponse := BastionDiagnoseResult{
		OperationID: "22222222-2222-2222-2222-222222222222",
		StepID:      "33333333-3333-3333-3333-333333333333",
		ResourceID:  "11111111-1111-1111-1111-111111111111",
		JobName:     "hetzner_bastion_diagnose",
		Status:      "completed",
		Completed:   true,
		Report:      map[string]any{"disk_usage_percent": float64(41)},
		Health:      map[string]any{"state": "healthy"},
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/hetzner/cluster-123/bastion/diagnose" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		jsonResponse(t, w, http.StatusOK, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.DiagnoseHetznerBastion(context.Background(), "cluster-123")
	if err != nil {
		t.Fatalf("DiagnoseHetznerBastion: %v", err)
	}
	if result.JobName != expectedResponse.JobName || !result.Completed {
		t.Fatalf("result = %+v", result)
	}
	if result.Report["disk_usage_percent"] != float64(41) || result.Health["state"] != "healthy" {
		t.Errorf("report/health diverged: %+v", result)
	}
}

func TestDiagnoseOvhBastion_Path(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/ovh/cluster-1/bastion/diagnose" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, BastionDiagnoseResult{JobName: "ovh_bastion_diagnose"})
	}
	testClient := newTestClient(t, handler)
	if _, err := testClient.DiagnoseOvhBastion(context.Background(), "cluster-1"); err != nil {
		t.Fatalf("DiagnoseOvhBastion: %v", err)
	}
}

func TestDiagnoseUpcloudBastion_Path(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/upcloud/cluster-1/bastion/diagnose" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, BastionDiagnoseResult{JobName: "upcloud_bastion_diagnose"})
	}
	testClient := newTestClient(t, handler)
	if _, err := testClient.DiagnoseUpcloudBastion(context.Background(), "cluster-1"); err != nil {
		t.Fatalf("DiagnoseUpcloudBastion: %v", err)
	}
}

func TestDiagnoseDigitaloceanBastion_Path(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters/digitalocean/cluster-1/bastion/diagnose" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, BastionDiagnoseResult{JobName: "digitalocean_bastion_diagnose"})
	}
	testClient := newTestClient(t, handler)
	if _, err := testClient.DiagnoseDigitaloceanBastion(context.Background(), "cluster-1"); err != nil {
		t.Fatalf("DiagnoseDigitaloceanBastion: %v", err)
	}
}

// Every diagnose refusal - no bastion, an unsupported provider, a diagnosis
// already running - arrives as a 409 carrying the platform's wording.
func TestDiagnoseHetznerBastion_RefusalSurfacesDetail(t *testing.T) {
	const refusal = "A bastion diagnosis is already running for this cluster."
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]string{"detail": refusal})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.DiagnoseHetznerBastion(context.Background(), "cluster-123")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if err.Error() != refusal {
		t.Errorf("error = %q, want the platform's refusal wording", err.Error())
	}
}

// The diagnose endpoint blocks for up to two minutes, so a caller that gave
// up has to surface a cancelled context rather than a parse failure - the
// command maps it onto the wait-timeout exit code.
func TestDiagnoseHetznerBastion_HonoursTheContextDeadline(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}
	testClient := newTestClient(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := testClient.DiagnoseHetznerBastion(ctx, "cluster-123")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}
