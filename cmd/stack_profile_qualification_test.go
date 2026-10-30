package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
	"github.com/google/uuid"
)

const qualificationOperationID = "abf2b51d-0b90-489c-ad57-6472494e8740"
const qualificationRequestID = "52c69bf4-b97a-4c0f-ac11-58bd91af8287"

type qualificationMock struct {
	baseMock
	starts, gets, cancels int
	requestID             string
	view                  *client.ProfileRuntimeQualification
	start                 func(context.Context, string) (*client.ProfileRuntimeQualification, error)
	get                   func(context.Context, string) (*client.ProfileRuntimeQualification, error)
}

func (mock *qualificationMock) StartProfileRuntimeQualification(ctx context.Context, id string) (*client.ProfileRuntimeQualification, error) {
	mock.starts++
	mock.requestID = id
	if mock.start != nil {
		return mock.start(ctx, id)
	}
	return mock.view, nil
}
func (mock *qualificationMock) GetProfileRuntimeQualification(ctx context.Context, id string) (*client.ProfileRuntimeQualification, error) {
	mock.gets++
	if mock.get != nil {
		return mock.get(ctx, id)
	}
	return mock.view, nil
}
func (mock *qualificationMock) CancelProfileRuntimeQualification(context.Context, string) (*client.ProfileRuntimeQualification, error) {
	mock.cancels++
	return mock.view, nil
}

func qualificationView(state string) *client.ProfileRuntimeQualification {
	result := &client.ProfileRuntimeQualification{OperationID: qualificationOperationID, RequestID: qualificationRequestID, State: state, Outcome: "unverified", CleanupState: "unverified"}
	if state == "completed" {
		result.Outcome = "completed"
		result.Image = "registry.ankra.cloud/cluster/cluster@sha256:" + strings.Repeat("a", 64)
		result.Report = &client.ProfileRuntimeQualificationReport{Version: "profile_runtime_qualification_v1", Policy: "profile_analysis_stdin_v1", Image: result.Image, Checks: map[string]string{"normal_cleanup": "satisfied", "network_egress_ipv4": "violated", "cancel_cleanup": "unverified"}}
	}
	return result
}

func runQualificationTest(t *testing.T, mock *qualificationMock, ctx context.Context, input string, args ...string) (string, string, error) {
	t.Helper()
	setMockClient(t, mock)
	command := newProfileRuntimeQualificationCommand()
	var output, diagnostic bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&diagnostic)
	command.SetIn(strings.NewReader(input))
	command.SetArgs(args)
	command.SilenceErrors = true
	command.SilenceUsage = true
	executeError := command.ExecuteContext(ctx)
	return output.String(), diagnostic.String(), executeError
}

func TestQualificationInvalidInputsDoNotMutate(t *testing.T) {
	for _, args := range [][]string{
		{"start", "--request-id", ""}, {"start", "--request-id", "not-uuid"}, {"start", "--request-id", uuid.Nil.String()},
		{"start", "--wait", "--timeout", "0s"}, {"start", "--wait", "--timeout", "-1s"}, {"start", "--timeout", "1s"},
		{"start", "-o", "xml"}, {"get", "bad-id"}, {"cancel", "bad-id", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := &qualificationMock{}
			_, _, failure := runQualificationTest(t, mock, context.Background(), "", args...)
			if exitCodeFor(failure) != exitUsage || mock.starts+mock.gets+mock.cancels != 0 {
				t.Fatalf("invalid input caused request: %v %+v", failure, mock)
			}
		})
	}
}

func TestQualificationRecoveryIdentityPrecedesUncertainStart(t *testing.T) {
	mock := &qualificationMock{}
	setMockClient(t, mock)
	command := newProfileRuntimeQualificationCommand()
	var output, diagnostic bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&diagnostic)
	command.SilenceErrors = true
	command.SilenceUsage = true
	mock.start = func(_ context.Context, id string) (*client.ProfileRuntimeQualification, error) {
		if !strings.Contains(diagnostic.String(), id) {
			t.Fatal("request started before recovery identity was printed")
		}
		return nil, errors.New("connection lost")
	}
	command.SetArgs([]string{"start", "-o", "json"})
	if failure := command.ExecuteContext(context.Background()); failure == nil || output.Len() != 0 || mock.starts != 1 {
		t.Fatalf("unexpected start result: %v", failure)
	}
	if _, parseError := uuid.Parse(mock.requestID); parseError != nil {
		t.Fatal(parseError)
	}
	savedID := mock.requestID
	_, _, failure := runQualificationTest(t, mock, context.Background(), "", "start", "--request-id", savedID)
	if failure == nil || mock.starts != 2 || mock.requestID != savedID {
		t.Fatal("retry did not preserve identity")
	}
}

func TestQualificationCompletedReportIsNotAnOverallPass(t *testing.T) {
	mock := &qualificationMock{view: qualificationView("completed")}
	output, _, failure := runQualificationTest(t, mock, context.Background(), "", "get", qualificationOperationID, "--wait", "-o", "json")
	var document map[string]any
	if failure != nil || json.Unmarshal([]byte(output), &document) != nil {
		t.Fatalf("not one JSON document: %s %v", output, failure)
	}
	report := document["report"].(map[string]any)["checks"].(map[string]any)
	if report["network_egress_ipv4"] != "violated" || report["cancel_cleanup"] != "unverified" {
		t.Fatal("observations changed")
	}
	output, _, failure = runQualificationTest(t, mock, context.Background(), "", "get", qualificationOperationID)
	if failure != nil || !strings.Contains(output, "cancel_cleanup: unverified") || !strings.Contains(output, "network_egress_ipv4: violated") || !strings.Contains(output, "Subject retention: unverified") {
		t.Fatalf("misleading human output: %s %v", output, failure)
	}
}

func TestQualificationWaitBoundsInflightRead(t *testing.T) {
	previous := qualificationPollInterval
	qualificationPollInterval = time.Millisecond
	t.Cleanup(func() { qualificationPollInterval = previous })
	mock := &qualificationMock{view: qualificationView("running")}
	mock.get = func(ctx context.Context, _ string) (*client.ProfileRuntimeQualification, error) {
		if mock.gets == 1 {
			return mock.view, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	output, _, failure := runQualificationTest(t, mock, context.Background(), "", "get", qualificationOperationID, "--wait", "--timeout", "30ms", "-o", "json")
	if exitCodeFor(failure) != exitWaitTimeout || !json.Valid([]byte(output)) || mock.cancels != 0 {
		t.Fatalf("wait not bounded or cancelled remote: %s %v", output, failure)
	}
}

func TestQualificationInterruptDoesNotCancelRemote(t *testing.T) {
	previous := qualificationPollInterval
	qualificationPollInterval = time.Millisecond
	t.Cleanup(func() { qualificationPollInterval = previous })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock := &qualificationMock{view: qualificationView("running")}
	mock.get = func(context.Context, string) (*client.ProfileRuntimeQualification, error) {
		cancel()
		return mock.view, nil
	}
	_, _, failure := runQualificationTest(t, mock, ctx, "", "get", qualificationOperationID, "--wait")
	if !errors.Is(failure, context.Canceled) || exitCodeFor(failure) == exitWaitTimeout || mock.cancels != 0 {
		t.Fatalf("interrupt misreported: %v", failure)
	}
}

func TestQualificationCancellationConfirmsAndKeepsCleanupUnknown(t *testing.T) {
	mock := &qualificationMock{view: qualificationView("cancelled")}
	_, _, failure := runQualificationTest(t, mock, context.Background(), "n\n", "cancel", qualificationOperationID)
	if exitCodeFor(failure) != exitCancelled || mock.cancels != 0 {
		t.Fatalf("declined cancel submitted: %v", failure)
	}
	output, _, failure := runQualificationTest(t, mock, context.Background(), "", "cancel", qualificationOperationID, "--yes", "-o", "json")
	if failure != nil || mock.cancels != 1 || !strings.Contains(output, `"cleanup_state": "unverified"`) {
		t.Fatalf("cancel changed proof: %s %v", output, failure)
	}
}

func TestQualificationWaitRejectsUnverifiedTerminalAndUnknownStates(t *testing.T) {
	for _, state := range []string{"failed", "timeout", "cancelled", "future_state"} {
		t.Run(state, func(t *testing.T) {
			mock := &qualificationMock{view: qualificationView(state)}
			_, _, failure := runQualificationTest(t, mock, context.Background(), "", "get", qualificationOperationID, "--wait")
			if exitCodeFor(failure) != exitError {
				t.Fatalf("%s accepted: %v", state, failure)
			}
		})
	}
	for _, bad := range []*client.ProfileRuntimeQualification{nil, {OperationID: qualificationOperationID, RequestID: qualificationRequestID, State: "completed", Outcome: "completed"}} {
		if failure := validateQualificationView(bad); failure == nil {
			t.Fatal("malformed response accepted")
		}
	}
}

func TestQualificationWaitNeverFollowsChangedIdentity(t *testing.T) {
	previous := qualificationPollInterval
	qualificationPollInterval = time.Millisecond
	t.Cleanup(func() { qualificationPollInterval = previous })
	mock := &qualificationMock{view: qualificationView("running")}
	mock.get = func(context.Context, string) (*client.ProfileRuntimeQualification, error) {
		result := qualificationView("completed")
		result.OperationID = qualificationRequestID
		return result, nil
	}
	setMockClient(t, mock)
	_, failure := waitProfileQualification(context.Background(), mock.view)
	if failure == nil || !strings.Contains(fmt.Sprint(failure), "identity changed") {
		t.Fatalf("followed wrong operation: %v", failure)
	}
}

func TestQualificationWaitCollectsReportAndYAMLPreservesFutureFields(t *testing.T) {
	previous := qualificationPollInterval
	qualificationPollInterval = time.Millisecond
	t.Cleanup(func() { qualificationPollInterval = previous })
	mock := &qualificationMock{view: qualificationView("running")}
	mock.get = func(context.Context, string) (*client.ProfileRuntimeQualification, error) {
		result := qualificationView("completed")
		result.RawDocument = client.NewRawDocument([]byte(`{"operation_id":"` + qualificationOperationID + `","state":"completed","future_observation":{"verified":false},"report":{"checks":{"cancel_cleanup":"unverified"}}}`))
		return result, nil
	}
	output, diagnostic, failure := runQualificationTest(t, mock, context.Background(), "", "start", "--request-id", qualificationRequestID, "--wait", "-o", "yaml")
	if failure != nil || mock.starts != 1 || mock.gets != 1 || !strings.Contains(output, "future_observation:") || strings.Contains(output, "Request ID:") || !strings.Contains(diagnostic, qualificationRequestID) {
		t.Fatalf("wait or YAML lost semantics: %s %s %v", output, diagnostic, failure)
	}
}

type qualificationBrokenWriter struct{}

func (qualificationBrokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("closed diagnostic pipe")
}

func TestQualificationDoesNotStartIfRecoveryIdentityCannotBeWritten(t *testing.T) {
	mock := &qualificationMock{view: qualificationView("running")}
	setMockClient(t, mock)
	command := newProfileRuntimeQualificationCommand()
	command.SetErr(qualificationBrokenWriter{})
	command.SetArgs([]string{"start"})
	command.SilenceErrors = true
	command.SilenceUsage = true
	if failure := command.ExecuteContext(context.Background()); failure == nil || mock.starts != 0 {
		t.Fatalf("started without recovery identity: %v", failure)
	}
}

func TestQualificationPreservesAuthorityRefusalWhileWaiting(t *testing.T) {
	previous := qualificationPollInterval
	qualificationPollInterval = time.Millisecond
	t.Cleanup(func() { qualificationPollInterval = previous })
	mock := &qualificationMock{view: qualificationView("running")}
	mock.get = func(context.Context, string) (*client.ProfileRuntimeQualification, error) {
		return nil, &client.PermissionDeniedError{Detail: "access revoked"}
	}
	output, diagnostic, failure := runQualificationTest(t, mock, context.Background(), "", "start", "--request-id", qualificationRequestID, "--wait", "-o", "json")
	if exitCodeFor(failure) != exitForbidden || !json.Valid([]byte(output)) || !strings.Contains(diagnostic, "last observed") || mock.starts != 1 || mock.cancels != 0 {
		t.Fatalf("authority refusal lost: %s %s %v", output, diagnostic, failure)
	}
}

func TestQualificationRejectsMismatchedReport(t *testing.T) {
	for _, field := range []string{"version", "policy", "image", "observation"} {
		t.Run(field, func(t *testing.T) {
			view := qualificationView("completed")
			switch field {
			case "version":
				view.Report.Version = "future"
			case "policy":
				view.Report.Policy = "different"
			case "image":
				view.Report.Image = "different"
			case "observation":
				view.Report.Checks["normal_cleanup"] = "maybe"
			}
			if failure := validateQualificationView(view); failure == nil {
				t.Fatal("mismatched report was accepted")
			}
		})
	}
}
