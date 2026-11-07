package client

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// A run detail from a platform newer than this CLI: a step field the struct
// declares (memory_working_set_peak_bytes) beside ones it does not.
const pipelineRunDetailWithFutureFields = `{"id":"run-1","run_number":7,"status":"concluded",` +
	`"outcome":"success","future_run_field":{"kept":true},"steps":[{"id":"step-1","step_key":"build",` +
	`"status":"concluded","memory_peak_bytes":7516192768,"memory_working_set_peak_bytes":5690831667,` +
	`"future_step_field":12345678901234567890}]}`

// The run detail keeps the body it was decoded from (ankra-ga9b8k), so -o json
// can print the fields this CLI does not declare, and decodes the working-set
// peak it now does.
func TestGetPipelineRunKeepsTheResponseDocumentBesideTheDecode(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, pipelineRunDetailWithFutureFields)
	})
	detail, err := testClient.GetPipelineRun(context.Background(), PipelineSelector{RepositoryID: "repo-1"}, "run-1")
	if err != nil {
		t.Fatalf("GetPipelineRun: %v", err)
	}
	if got := string(detail.RawJSON()); got != pipelineRunDetailWithFutureFields {
		t.Fatalf("RawJSON = %s, want the body as answered", got)
	}
	if len(detail.Steps) != 1 || detail.Steps[0].MemoryWorkingSetPeakBytes == nil ||
		*detail.Steps[0].MemoryWorkingSetPeakBytes != 5690831667 {
		t.Fatalf("steps = %+v, want the working-set peak decoded", detail.Steps)
	}
}
