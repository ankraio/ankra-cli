package client

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestListPowerSchedules_Success(t *testing.T) {
	nextRun := "2026-01-05T19:00:00Z"
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/clusters/imported/cluster-123/power-schedules" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, PowerScheduleListResult{Schedules: []PowerSchedule{
			{ID: "sched-1", Action: "stop", ScheduleKind: "cron", Timezone: "Europe/Stockholm",
				Enabled: true, NextRunAt: &nextRun},
		}})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListPowerSchedules("cluster-123")
	if err != nil {
		t.Fatalf("ListPowerSchedules: %v", err)
	}
	if len(result.Schedules) != 1 || result.Schedules[0].ID != "sched-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Schedules[0].NextRunAt == nil || *result.Schedules[0].NextRunAt != nextRun {
		t.Errorf("NextRunAt = %v, want %s", result.Schedules[0].NextRunAt, nextRun)
	}
}

func TestCreatePowerSchedule_SendsBodyAndDecodesListing(t *testing.T) {
	cronExpression := "0 19 * * 1-5"
	timezone := "Europe/Stockholm"
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/clusters/imported/cluster-123/power-schedules" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		if body["action"] != "stop" || body["schedule_kind"] != "cron" ||
			body["cron_expression"] != cronExpression || body["timezone"] != timezone ||
			body["enabled"] != true {
			t.Errorf("unexpected body: %+v", body)
		}
		if _, hasRunAt := body["run_at"]; hasRunAt {
			t.Errorf("run_at must be omitted for cron schedules, body: %+v", body)
		}
		jsonResponse(t, w, http.StatusOK, PowerScheduleListResult{Schedules: []PowerSchedule{
			{ID: "sched-new", Action: "stop", ScheduleKind: "cron"},
		}})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.CreatePowerSchedule("cluster-123", PowerScheduleRequest{
		Action: "stop", ScheduleKind: "cron",
		CronExpression: &cronExpression, Timezone: &timezone, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePowerSchedule: %v", err)
	}
	if len(result.Schedules) != 1 || result.Schedules[0].ID != "sched-new" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestUpdatePowerSchedule_PutsFullReplace(t *testing.T) {
	runAt := "2026-02-01T07:00:00Z"
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/v1/org/clusters/imported/cluster-123/power-schedules/sched-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		// enabled is not omitempty: a paused (false) update must still
		// serialize, because the backend refuses a body without it.
		if body["enabled"] != false || body["run_at"] != runAt {
			t.Errorf("unexpected body: %+v", body)
		}
		jsonResponse(t, w, http.StatusOK, PowerScheduleListResult{Schedules: []PowerSchedule{
			{ID: "sched-1", Action: "start", ScheduleKind: "once", Enabled: false},
		}})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.UpdatePowerSchedule("cluster-123", "sched-1", PowerScheduleRequest{
		Action: "start", ScheduleKind: "once", RunAt: &runAt, Enabled: false,
	})
	if err != nil {
		t.Fatalf("UpdatePowerSchedule: %v", err)
	}
	if len(result.Schedules) != 1 || result.Schedules[0].Enabled {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDeletePowerSchedule_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/org/clusters/imported/cluster-123/power-schedules/sched-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, DeletePowerScheduleResult{Deleted: true})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.DeletePowerSchedule("cluster-123", "sched-1")
	if err != nil {
		t.Fatalf("DeletePowerSchedule: %v", err)
	}
	if !result.Deleted {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestPowerSchedules_BackendDetailSurfaces(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]string{
			"detail": "At most 20 power schedules are allowed per cluster"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.CreatePowerSchedule("cluster-123", PowerScheduleRequest{
		Action: "stop", ScheduleKind: "once", Enabled: true,
	})
	if err == nil || !strings.Contains(err.Error(), "At most 20 power schedules") {
		t.Fatalf("expected the backend detail to surface, got %v", err)
	}
}
