package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestStartZoneMoveSendsThePlanRequestAndDecodesTheMove(t *testing.T) {
	controlPlaneCount := 3
	apiClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/clusters/imported/c-fr/zone-move" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Errorf("decode: %v", decodeError)
		}
		if body["zone"] != "se-sto1" || body["plan_only"] != true || body["control_plane_count"] != float64(3) {
			t.Errorf("body = %v", body)
		}
		if _, sent := body["worker_count"]; sent {
			t.Errorf("an unset count must not be sent: %v", body)
		}
		if _, sent := body["target_cluster_id"]; sent {
			t.Errorf("an unset target must not be sent: %v", body)
		}
		if _, sent := body["vault"]; sent {
			t.Errorf("an unset vault must not be sent: %v", body)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"move": map[string]any{
			"id": "", "source_cluster_id": "c-fr", "zone": "se-sto1", "status": "planned",
			"plan": map[string]any{
				"target": map[string]any{"name": "avura-stockholm", "control_plane": map[string]any{"plan": "starter-4c-8g", "count": 3}},
				"data":   []map[string]any{{"namespace": "avura", "name": "miniodata-minio-0", "kind": "pvc", "requested_bytes": 10737418240}},
				"dns":    []map[string]any{{"hostname": "avura.work", "mechanism": "ankracloud_proxied", "source_origin": "lb-1", "target_origin": nil}},
			},
		}})
	})
	move, startError := apiClient.StartZoneMove(context.Background(), "c-fr",
		ZoneMoveRequest{Zone: "se-sto1", PlanOnly: true, ControlPlaneCount: &controlPlaneCount})
	if startError != nil {
		t.Fatalf("start: %v", startError)
	}
	if move.Plan.Target.Name != "avura-stockholm" || move.Plan.Target.ControlPlane.Count != 3 ||
		move.Plan.Data[0].RequestedBytes != 10737418240 || move.Plan.DNS[0].TargetOrigin != nil {
		t.Fatalf("move = %+v", move)
	}
}

func TestStartZoneMoveConflictSurfacesTheDetail(t *testing.T) {
	apiClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{"detail": "A zone move of this cluster is already active"})
	})
	_, startError := apiClient.StartZoneMove(context.Background(), "c-fr", ZoneMoveRequest{Zone: "se-sto1"})
	if startError == nil || startError.Error() != "A zone move of this cluster is already active" {
		t.Fatalf("error = %v", startError)
	}
}

func TestGetZoneMoveNotFound(t *testing.T) {
	apiClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/org/clusters/imported/c-fr/zone-move" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		jsonResponse(t, w, http.StatusNotFound, map[string]any{"detail": "No zone move"})
	})
	_, readError := apiClient.GetZoneMove(context.Background(), "c-fr")
	if !IsZoneMoveNotFound(readError) {
		t.Fatalf("expected a not-found, got %v", readError)
	}
}

func TestAdvanceZoneMove(t *testing.T) {
	for _, testCase := range []struct {
		action   ZoneMoveAction
		emptyAck bool
	}{
		{ZoneMoveActionCutover, false},
		{ZoneMoveActionRollback, true},
		{ZoneMoveActionRetire, true},
	} {
		t.Run(string(testCase.action), func(t *testing.T) {
			apiClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/clusters/imported/c-fr/zone-move/"+string(testCase.action) {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if testCase.emptyAck {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				jsonResponse(t, w, http.StatusAccepted, map[string]any{"move": map[string]any{"id": "move-1", "status": "freezing"}})
			})
			move, advanceError := apiClient.AdvanceZoneMove(context.Background(), "c-fr", testCase.action)
			if advanceError != nil {
				t.Fatalf("advance: %v", advanceError)
			}
			if testCase.emptyAck != (move == nil) {
				t.Fatalf("move = %+v", move)
			}
		})
	}
	if _, unknownError := (&Client{}).AdvanceZoneMove(context.Background(), "c-fr", "teleport"); unknownError == nil {
		t.Fatal("an unknown action must be refused before sending")
	}
}

func TestZoneMoveActionStatuses(t *testing.T) {
	if ZoneMoveActionCutover.AllowedFrom() != ZoneMoveStatusReadyForCutover || ZoneMoveActionCutover.SettlesAt() != ZoneMoveStatusSoaking ||
		ZoneMoveActionRollback.AllowedFrom() != ZoneMoveStatusSoaking || ZoneMoveActionRollback.SettlesAt() != ZoneMoveStatusRolledBack ||
		ZoneMoveActionRetire.AllowedFrom() != ZoneMoveStatusSoaking || ZoneMoveActionRetire.SettlesAt() != ZoneMoveStatusRetired {
		t.Fatal("action statuses drifted from the contract")
	}
	for _, status := range []string{ZoneMoveStatusReadyForCutover, ZoneMoveStatusSoaking, ZoneMoveStatusRolledBack, ZoneMoveStatusRetired, ZoneMoveStatusFailed} {
		if !IsZoneMoveResting(status) {
			t.Fatalf("%s must be resting", status)
		}
	}
	for _, status := range []string{ZoneMoveStatusSeeding, ZoneMoveStatusFinalCopy, ZoneMoveStatusRetiring} {
		if IsZoneMoveResting(status) {
			t.Fatalf("%s must not be resting", status)
		}
	}
}

func TestStartZoneMoveSendsTheVaultAndDecodesEachStacksVault(t *testing.T) {
	apiClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Errorf("decode: %v", decodeError)
		}
		if body["vault"] != "backups" {
			t.Errorf("body = %v, want vault backups", body)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"move": map[string]any{
			"id": "", "zone": "se-sto1", "status": "planned",
			"plan": map[string]any{
				"stacks": []map[string]any{
					{"name": "avura", "carried": true, "vault": map[string]any{"id": "vault-1", "name": "backups", "basis": "request"}},
					{"name": "web", "carried": true, "vault": nil},
				},
				"not_carried": []map[string]any{{"what": "the data of stack 'ledger'", "reason": "no vault"}},
				"warnings":    []string{"The move cannot tell which backup vault carries the data of stack 'ledger'"},
			},
		}})
	})
	move, startError := apiClient.StartZoneMove(context.Background(), "c-fr", ZoneMoveRequest{Zone: "se-sto1", PlanOnly: true, Vault: "backups"})
	if startError != nil {
		t.Fatalf("start: %v", startError)
	}
	vault := move.Plan.Stacks[0].Vault
	if vault == nil || vault.ID != "vault-1" || vault.Name != "backups" || vault.Basis != ZoneMoveVaultBasisRequest {
		t.Fatalf("vault = %+v", vault)
	}
	if move.Plan.Stacks[1].Vault != nil || len(move.Plan.Warnings) != 1 || len(move.Plan.NotCarried) != 1 {
		t.Fatalf("plan = %+v", move.Plan)
	}
}
