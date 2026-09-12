package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type aiLanesMock struct {
	baseMock
	lanes        []client.AILaneModel
	setLane      string
	setModelKey  string
	setCallCount int
}

func (m *aiLanesMock) ListAILaneModels() ([]client.AILaneModel, error) {
	return m.lanes, nil
}

func (m *aiLanesMock) SetAILaneModel(lane string, modelKey string) ([]client.AILaneModel, error) {
	m.setLane, m.setModelKey = lane, modelKey
	m.setCallCount++
	return m.lanes, nil
}

func TestAILanesListShowsEachLaneWithTheModelItRunsOn(t *testing.T) {
	mock := &aiLanesMock{lanes: []client.AILaneModel{
		{Lane: "pr_review", DisplayName: "AI code review", DefaultTier: "think", EffectiveModelID: "z-ai/glm-5.2"},
		{Lane: "stack_description", DisplayName: "Stack README", DefaultTier: "think",
			SelectedModelKey: "retired-model", EffectiveModelID: "z-ai/glm-5.2", IsSelectionStale: true},
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "lanes", "list")
	})

	for _, expected := range []string{"pr_review", "z-ai/glm-5.2", "(default tier)", "stale"} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected %q in output, got: %s", expected, stdoutOutput)
		}
	}
}

func TestAILanesSetSendsTheLaneAndModel(t *testing.T) {
	mock := &aiLanesMock{lanes: []client.AILaneModel{
		{Lane: "pr_review", DefaultTier: "think", SelectedModelKey: "expert", EffectiveModelID: "moonshotai/kimi-k3"},
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "lanes", "set", "pr_review", "expert")
	})

	if mock.setLane != "pr_review" || mock.setModelKey != "expert" {
		t.Errorf("SetAILaneModel(%q, %q), want pr_review/expert", mock.setLane, mock.setModelKey)
	}
	if !strings.Contains(stdoutOutput, "moonshotai/kimi-k3") {
		t.Errorf("expected the model the lane now runs on, got: %s", stdoutOutput)
	}
}

func TestAILanesClearReturnsTheLaneToItsDefaultTier(t *testing.T) {
	mock := &aiLanesMock{lanes: []client.AILaneModel{
		{Lane: "pr_review", DefaultTier: "think", EffectiveModelID: "z-ai/glm-5.2"},
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "lanes", "clear", "pr_review")
	})

	if mock.setCallCount != 1 || mock.setLane != "pr_review" || mock.setModelKey != "" {
		t.Errorf("SetAILaneModel calls=%d (%q, %q), want one clear of pr_review",
			mock.setCallCount, mock.setLane, mock.setModelKey)
	}
	if !strings.Contains(stdoutOutput, "think") {
		t.Errorf("expected the default tier in output, got: %s", stdoutOutput)
	}
}

func TestAILanesSetRefusesAMissingModel(t *testing.T) {
	mock := &aiLanesMock{}
	setMockClient(t, mock)

	if _, executeError := executeCommand("ai", "lanes", "set", "pr_review"); executeError == nil {
		t.Error("expected an argument error when the model is missing")
	}
	if mock.setCallCount != 0 {
		t.Errorf("SetAILaneModel was called %d times without a model", mock.setCallCount)
	}
}

func TestAILanesSetReportsAnUnconfirmedChangeAsAnError(t *testing.T) {
	mock := &aiLanesMock{lanes: []client.AILaneModel{
		{Lane: "troubleshoot", DefaultTier: "expert", EffectiveModelID: "moonshotai/kimi-k3"},
	}}
	setMockClient(t, mock)

	var executeError error
	stdoutOutput := captureStdout(t, func() {
		_, executeError = executeCommand("ai", "lanes", "set", "pr_review", "think")
	})

	if executeError == nil || !strings.Contains(executeError.Error(), "unconfirmed") {
		t.Errorf("expected an unconfirmed-change error, got: %v", executeError)
	}
	if strings.Contains(stdoutOutput, "updated") || strings.Contains(stdoutOutput, "now runs on") {
		t.Errorf("an answer without the lane must not be reported as a success, got: %s", stdoutOutput)
	}
}
