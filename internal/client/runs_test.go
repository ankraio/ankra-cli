package client

import (
	"net/http"
	"testing"
)

func TestListRuns_CarriesEveryFilterItWasGiven(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/runs" {
			t.Errorf("path = %s", r.URL.Path)
		}
		query := r.URL.Query()
		for name, expected := range map[string]string{
			"kind": RunKindRestore, "status": RunStatusFailed,
			"cluster_id": testClusterUUID, "stack_name": "shop", "limit": "10",
		} {
			if got := query.Get(name); got != expected {
				t.Errorf("%s = %q, want %q", name, got, expected)
			}
		}
		jsonResponse(t, w, http.StatusOK, RunListResult{Runs: []Run{{ID: "run-1", Kind: RunKindRestore}}})
	}
	testClient := newTestClient(t, handler)

	result, listError := testClient.ListRuns(ListRunsOptions{
		Kind: RunKindRestore, Status: RunStatusFailed,
		ClusterID: testClusterUUID, StackName: "shop", Limit: 10,
	})

	if listError != nil {
		t.Fatalf("ListRuns: %v", listError)
	}
	if len(result.Runs) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestListRuns_SendsNoEmptyFilters(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if raw := r.URL.RawQuery; raw != "" {
			t.Errorf("an unfiltered listing must send no query, got %q", raw)
		}
		jsonResponse(t, w, http.StatusOK, RunListResult{})
	}
	testClient := newTestClient(t, handler)

	if _, listError := testClient.ListRuns(ListRunsOptions{}); listError != nil {
		t.Fatalf("ListRuns: %v", listError)
	}
}

func TestGetRun_DecodesTheDataMovementPayloadAndItsSteps(t *testing.T) {
	excerpt := "the volume was still attached"
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/runs/run-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, Run{
			ID: "run-1", Kind: RunKindBackup, Status: RunStatusFailed,
			DataRun: &DataRun{
				ID: "run-1", Kind: RunKindBackup, Phase: "backup",
				Plan:      []string{"verify_vault_access", "backup", "upload_verify"},
				AssetPlan: DataRunAssetPlan{Engine: "velero"},
				Steps: []DataRunStep{
					{ID: "s1", StepKey: "backup", Position: 2, Attempt: 1, Status: "failed", ErrorExcerpt: &excerpt},
					{ID: "s2", StepKey: "backup", Position: 2, Attempt: 2, Status: "succeeded"},
				},
			},
		})
	}
	testClient := newTestClient(t, handler)

	run, getError := testClient.GetRun("run-1")

	if getError != nil {
		t.Fatalf("GetRun: %v", getError)
	}
	if run.DataRun == nil || len(run.DataRun.Steps) != 2 {
		t.Fatalf("every attempt must survive: %+v", run.DataRun)
	}
	if run.DataRun.Steps[0].ErrorExcerpt == nil || *run.DataRun.Steps[0].ErrorExcerpt != excerpt {
		t.Fatalf("the failure excerpt must survive: %+v", run.DataRun.Steps[0])
	}
}

// A retry answers 201 with a NEW run, and that is the one the caller must be
// handed: the failed row will never move again.
func TestRetryRun_ReturnsTheNewRun(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/runs/run-1/retry" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, Run{ID: "run-2", Kind: RunKindBackup, Status: RunStatusPending})
	}
	testClient := newTestClient(t, handler)

	run, retryError := testClient.RetryRun("run-1")

	if retryError != nil {
		t.Fatalf("RetryRun: %v", retryError)
	}
	if run.ID != "run-2" {
		t.Fatalf("the new run must come back, got %q", run.ID)
	}
}

func TestCancelRun_RelaysTheAlreadyFinishedRefusal(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/runs/run-1/cancel" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusConflict, map[string]string{"detail": "This run has already finished"})
	}
	testClient := newTestClient(t, handler)

	_, cancelError := testClient.CancelRun("run-1")

	if cancelError == nil {
		t.Fatal("cancelling a finished run must be an error, not a silent success")
	}
	if cancelError.Error() != "This run has already finished" {
		t.Fatalf("the detail must be relayed verbatim, got %q", cancelError.Error())
	}
}
