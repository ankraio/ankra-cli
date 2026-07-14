package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
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
