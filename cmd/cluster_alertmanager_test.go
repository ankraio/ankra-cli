package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// `ankra cluster alertmanager silences`, the CLI side of the assistant's
// Alertmanager silence tools (ankra-hq41xb.11), run through rootCmd against a
// recording mock.

const silenceTestID = "35833f27-aaed-47c3-956e-9b071140f062"

type alertmanagerSilencesMock struct {
	cliParityMock

	listedStates    []string
	listedLocations []client.AlertmanagerLocation
	createdRequests []client.CreateAlertmanagerSilenceRequest
	expiredSilences []string
	silences        []client.AlertmanagerSilence
	silencesError   error
}

var silenceTestTarget = client.AlertmanagerTarget{Namespace: "monitoring", Service: "alertmanager-operated", Port: 9093}

func (m *alertmanagerSilencesMock) ListAlertmanagerSilences(clusterID string, state string, location client.AlertmanagerLocation) (*client.AlertmanagerSilenceList, error) {
	m.listedStates = append(m.listedStates, state)
	m.listedLocations = append(m.listedLocations, location)
	if m.silencesError != nil {
		return nil, m.silencesError
	}
	return &client.AlertmanagerSilenceList{Alertmanager: silenceTestTarget, State: state, Silences: m.silences,
		Count: len(m.silences)}, nil
}

func (m *alertmanagerSilencesMock) CreateAlertmanagerSilence(clusterID string, request client.CreateAlertmanagerSilenceRequest) (*client.AlertmanagerSilenceCreated, error) {
	m.createdRequests = append(m.createdRequests, request)
	if m.silencesError != nil {
		return nil, m.silencesError
	}
	return &client.AlertmanagerSilenceCreated{SilenceID: silenceTestID, Matchers: request.Matchers,
		EndsAt: "2026-08-29T12:00:00Z", Comment: request.Comment, Alertmanager: silenceTestTarget}, nil
}

func (m *alertmanagerSilencesMock) ExpireAlertmanagerSilence(clusterID string, silenceID string, location client.AlertmanagerLocation) (*client.AlertmanagerSilenceExpired, error) {
	m.expiredSilences = append(m.expiredSilences, silenceID)
	if m.silencesError != nil {
		return nil, m.silencesError
	}
	return &client.AlertmanagerSilenceExpired{SilenceID: silenceID, Expired: true, Alertmanager: silenceTestTarget}, nil
}

func runSilencesCommand(t *testing.T, mock *alertmanagerSilencesMock, input string, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	full := append([]string{"cluster", "alertmanager", "silences"}, args...)
	if found, _, findError := rootCmd.Find(full); findError == nil {
		resetTreeFlags(t, found)
		t.Cleanup(func() { resetTreeFlags(t, found) })
	}
	return runWithInput(t, mock, input, full...)
}

func TestSilencesListRendersTheSilencesAndJSON(t *testing.T) {
	mock := &alertmanagerSilencesMock{silences: []client.AlertmanagerSilence{{
		ID: silenceTestID, State: "active", EndsAt: "2026-08-29T12:00:00Z", CreatedBy: "ankra-ai", Comment: "upgrade",
		Matchers: []client.AlertmanagerSilenceMatcher{{Name: "alertname", Value: "KubeVersionMismatch"}},
	}}}
	output, runError := runSilencesCommand(t, mock, "", "list", "--cluster", "prod")
	if runError != nil {
		t.Fatalf("list failed: %v\n%s", runError, output)
	}
	if !strings.Contains(output, silenceTestID) || !strings.Contains(output, `alertname="KubeVersionMismatch"`) {
		t.Errorf("the silence is not listed:\n%s", output)
	}
	if mock.listedStates[0] != "active" {
		t.Errorf("default state = %q, want active", mock.listedStates[0])
	}

	output, runError = runSilencesCommand(t, mock, "", "list", "--state", "all", "--namespace", "monitoring",
		"-o", "json", "--cluster", "prod")
	if runError != nil {
		t.Fatal(runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil || decoded["count"].(float64) != 1 {
		t.Fatalf("-o json is not the listing: %v\n%s", decodeError, output)
	}
	if mock.listedStates[1] != "all" || mock.listedLocations[1].Namespace != "monitoring" {
		t.Errorf("state/location = %q/%+v", mock.listedStates[1], mock.listedLocations[1])
	}
}

func TestSilencesListUsageErrorsSendNothing(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--state", "muted", "--cluster", "prod"},
		{"list", "--service", "am", "--cluster", "prod"},
		{"list", "--port", "70000", "--cluster", "prod"},
	} {
		mock := &alertmanagerSilencesMock{}
		_, runError := runSilencesCommand(t, mock, "", args...)
		if exitCodeFor(runError) != exitUsage || len(mock.listedStates) != 0 {
			t.Fatalf("%v: exit %d (%v), want a usage error and nothing sent", args, exitCodeFor(runError), runError)
		}
	}
}

func TestSilencesListWithoutCredentialsExitsSix(t *testing.T) {
	mock := &alertmanagerSilencesMock{silencesError: client.ErrUnauthorized}
	_, runError := runSilencesCommand(t, mock, "", "list", "--cluster", "prod")
	if exitCodeFor(runError) != exitAuth {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(runError), runError, exitAuth)
	}
}

func TestSilencesCreateSendsTheMatchersAndComment(t *testing.T) {
	mock := &alertmanagerSilencesMock{}
	output, runError := runSilencesCommand(t, mock, "", "create", "--matcher", "alertname=KubeVersionMismatch",
		"--matcher", "pod =~ web-.*", "--duration", "2w", "--comment", "pending upgrade", "--cluster", "prod")
	if runError != nil {
		t.Fatalf("create failed: %v\n%s", runError, output)
	}
	if len(mock.createdRequests) != 1 {
		t.Fatalf("created %d silences, want 1", len(mock.createdRequests))
	}
	request := mock.createdRequests[0]
	if len(request.Matchers) != 2 || request.Matchers[0].IsRegex || !request.Matchers[1].IsRegex ||
		request.Matchers[1].Value != "web-.*" || request.Duration != "2w" || request.Comment != "pending upgrade" {
		t.Errorf("request = %+v", request)
	}
	if !strings.Contains(output, silenceTestID) {
		t.Errorf("the created silence id is not shown:\n%s", output)
	}
}

func TestSilencesCreateUsageErrorsSendNothing(t *testing.T) {
	for _, args := range [][]string{
		{"create", "--comment", "x", "--cluster", "prod"},
		{"create", "--matcher", "alertname=X", "--cluster", "prod"},
		{"create", "--matcher", "novalue", "--comment", "x", "--cluster", "prod"},
		{"create", "--matcher", "alertname=~.*", "--comment", "x", "--cluster", "prod"},
	} {
		mock := &alertmanagerSilencesMock{}
		_, runError := runSilencesCommand(t, mock, "", args...)
		if exitCodeFor(runError) != exitUsage || len(mock.createdRequests) != 0 {
			t.Fatalf("%v: exit %d (%v), want a usage error and nothing sent", args, exitCodeFor(runError), runError)
		}
	}
}

func TestSilencesCreateRefusedByThePlatformGuardrailsIsAUsageError(t *testing.T) {
	mock := &alertmanagerSilencesMock{silencesError: client.NewUnexpectedResponseError(400,
		"duration 60d exceeds the maximum of 30d")}
	_, runError := runSilencesCommand(t, mock, "", "create", "--matcher", "alertname=X", "--duration", "60d",
		"--comment", "x", "--cluster", "prod")
	if exitCodeFor(runError) != exitUsage {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(runError), runError, exitUsage)
	}
}

func TestSilencesCreateWithoutThePermissionExitsSeven(t *testing.T) {
	mock := &alertmanagerSilencesMock{silencesError: &client.PermissionDeniedError{Permission: "alerts.write"}}
	_, runError := runSilencesCommand(t, mock, "", "create", "--matcher", "alertname=X", "--comment", "x", "--cluster", "prod")
	if exitCodeFor(runError) != exitForbidden {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(runError), runError, exitForbidden)
	}
}

func TestSilencesExpireConfirmsFirst(t *testing.T) {
	mock := &alertmanagerSilencesMock{}
	_, runError := runSilencesCommand(t, mock, "n\n", "expire", silenceTestID, "--cluster", "prod")
	if !errors.Is(runError, errCancelled) || len(mock.expiredSilences) != 0 {
		t.Fatalf("decline: error %v, expired %v; want errCancelled and none", runError, mock.expiredSilences)
	}
	output, runError := runSilencesCommand(t, mock, "", "expire", silenceTestID, "--yes", "-o", "json", "--cluster", "prod")
	if runError != nil {
		t.Fatalf("expire failed: %v\n%s", runError, output)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil || decoded["expired"] != true {
		t.Fatalf("-o json is not the expiry: %v\n%s", decodeError, output)
	}
	if len(mock.expiredSilences) != 1 || mock.expiredSilences[0] != silenceTestID {
		t.Fatalf("expired %v", mock.expiredSilences)
	}
}

func TestSilencesExpireRefusesABlankID(t *testing.T) {
	mock := &alertmanagerSilencesMock{}
	_, runError := runSilencesCommand(t, mock, "", "expire", " ", "--yes", "--cluster", "prod")
	if exitCodeFor(runError) != exitUsage || len(mock.expiredSilences) != 0 {
		t.Fatalf("exit %d (%v), want a usage error and nothing sent", exitCodeFor(runError), runError)
	}
}

func TestSilencesExpireOfAnUnknownSilenceExitsThree(t *testing.T) {
	mock := &alertmanagerSilencesMock{silencesError: client.NewUnexpectedResponseError(404, "silence not found")}
	_, runError := runSilencesCommand(t, mock, "", "expire", silenceTestID, "--yes", "--cluster", "prod")
	if exitCodeFor(runError) != exitNotFound {
		t.Fatalf("exit code = %d (%v), want %d", exitCodeFor(runError), runError, exitNotFound)
	}
}
