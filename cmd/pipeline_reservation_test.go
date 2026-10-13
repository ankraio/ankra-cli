package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// A running run whose first step waits for a node with room for its
// reservation says so under Waiting:, with the platform's own sentence, and a
// Reserving: line naming the room and how long it has waited
// (ankra-q573dh.3.4).
func TestPipelineRunDetailPrintsTheReservationWait(t *testing.T) {
	var detail client.PipelineRunDetail
	if decodeError := json.Unmarshal([]byte(`{
		"id": "run-9", "run_number": 9, "status": "running",
		"reservation": {"mode": "on", "cpu_millicores": 6250, "memory_bytes": 13743895347},
		"reservation_wait": {"state": "waiting", "since": "2026-10-13T13:37:43Z", "waited_seconds": 250,
			"cpu_millicores": 6250, "memory_bytes": 13743895347, "returned_to_queue_count": 0,
			"message": "Waiting for a node with room for this run (6.25 CPU and 12.8Gi memory) for 4m 10s. A node is being added for it."},
		"steps": [{"step_key": "checkout", "status": "running"}]
	}`), &detail); decodeError != nil {
		t.Fatalf("decoding the run detail: %v", decodeError)
	}
	var output bytes.Buffer
	printPipelineRunDetail(&output, detail, client.PipelineSelector{})
	rendered := output.String()
	for _, expected := range []string{
		"  Waiting:   Waiting for a node with room for this run (6.25 CPU and 12.8Gi memory) for 4m 10s. A node is being added for it.",
		"  Reserving: waiting for a node with room for this run for 4m10s (6.25 CPU and 12.8Gi memory)",
		"  Reservation: on (held on the run's node), estimated 6.25 CPU and 12.8Gi memory",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("output missing %q:\n%s", expected, rendered)
		}
	}
}

// A first step handed back to the queue while it waited says how many times.
func TestPipelineRunDetailPrintsAReservationReturnedToTheQueue(t *testing.T) {
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 10, ID: "run-10", Status: "running"},
		ReservationWait: &client.PipelineRunReservationWait{State: "returned_to_queue", WaitedSeconds: 1200,
			ReturnedToQueueCount: 2, Message: "This run's first step was returned to the queue 2 times."},
	}, client.PipelineSelector{})
	if rendered := output.String(); !strings.Contains(rendered,
		"  Reserving: returned to the queue 2 times while waiting for room for 20m0s") {
		t.Errorf("output missing the returned-to-queue line:\n%s", rendered)
	}
}

// A server that could not tell whether the run waits on its reservation says
// so: no line at all would read as "nothing is holding this run back".
func TestPipelineRunDetailPrintsAnUnreadableReservationWait(t *testing.T) {
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun:                client.PipelineRun{RunNumber: 11, ID: "run-11", Status: "running"},
		ReservationWaitUnavailable: "Whether this run's first step is waiting for a node with room for the run could not be read; read the run again.",
	}, client.PipelineSelector{})
	if rendered := output.String(); !strings.Contains(rendered, "  Waiting:   Whether this run's first step is waiting") {
		t.Errorf("output missing the unavailable sentence:\n%s", rendered)
	}
}

// A run with the reservation off, a shadow run's observed peak, and a server
// older than the fields: off and absent print nothing, shadow says it held
// nothing.
func TestPipelineRunReservationLine(t *testing.T) {
	cpu, memory := int64(4000), int64(9771089510)
	for name, testCase := range map[string]struct {
		reservation *client.PipelineRunReservation
		expected    string
	}{
		"absent": {nil, ""},
		"off":    {&client.PipelineRunReservation{Mode: "off"}, ""},
		"shadow with an observed peak": {&client.PipelineRunReservation{Mode: "shadow",
			ObservedCPUMillicores: &cpu, ObservedMemoryBytes: &memory},
			"  Reservation: shadow (estimated, not held), observed peak 4 CPU and 9.1Gi memory\n"},
	} {
		var output bytes.Buffer
		printPipelineRunReservation(&output, client.PipelineRun{Reservation: testCase.reservation})
		if output.String() != testCase.expected {
			t.Errorf("%s: expected %q, got %q", name, testCase.expected, output.String())
		}
	}
}

// A state this CLI does not know is named as the server sent it rather than
// read as "waiting", and a reservation wait is printed even when the server
// could not derive a queue reason.
func TestPipelineRunDetailPrintsAnUnknownReservationWaitStateAsSent(t *testing.T) {
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun:            client.PipelineRun{RunNumber: 12, ID: "run-12", Status: "running"},
		QueueReasonUnavailable: "Why this run is queued could not be read.",
		ReservationWait:        &client.PipelineRunReservationWait{State: "placing", WaitedSeconds: 30},
	}, client.PipelineSelector{})
	rendered := output.String()
	for _, expected := range []string{"  Reserving: reservation wait \"placing\" for 30s", "could not be read"} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("output missing %q:\n%s", expected, rendered)
		}
	}
}

// A clock skew that puts the wait's start ahead of the server's now never
// prints a negative wait.
func TestPipelineRunDetailNeverPrintsANegativeReservationWait(t *testing.T) {
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun:     client.PipelineRun{RunNumber: 13, ID: "run-13", Status: "running"},
		ReservationWait: &client.PipelineRunReservationWait{State: "waiting", WaitedSeconds: -5},
	}, client.PipelineSelector{})
	if rendered := output.String(); !strings.Contains(rendered, "for 0s") || strings.Contains(rendered, "-5s") {
		t.Errorf("a negative wait must print as 0s:\n%s", rendered)
	}
}
