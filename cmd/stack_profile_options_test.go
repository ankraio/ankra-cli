package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"ankra/internal/client"
)

// stackProfileDraftMock serves one draft and records the parameter list
// written back, so a test can assert exactly what an authoring command
// changed and that it left every other field alone.
type stackProfileDraftMock struct {
	baseMock
	draft         *client.StackProfileDraft
	updateRequest *client.UpdateStackProfileDraftRequest
}

func (mock *stackProfileDraftMock) GetStackProfileDraft(draftID string) (*client.StackProfileDraft, error) {
	return mock.draft, nil
}

func (mock *stackProfileDraftMock) UpdateStackProfileDraft(draftID string, request client.UpdateStackProfileDraftRequest) (*client.StackProfileDraft, error) {
	mock.updateRequest = &request
	updated := *mock.draft
	updated.Parameters = request.Parameters
	updated.Version = request.Version + 1
	return &updated, nil
}

func gpuChatDraft() *client.StackProfileDraft {
	return &client.StackProfileDraft{
		ID:      "draft-1",
		Name:    "gpu-chat",
		Spec:    json.RawMessage(`{"stacks":[]}`),
		Version: 3,
		Parameters: []map[string]any{
			{"name": "model_id", "type": "string", "required": true, "enum_values": []any{}, "group": "variables"},
			{"name": "max_model_len", "type": "number", "required": false, "default": "16384", "enum_values": []any{}},
			{"name": "api_key", "type": "secret", "required": true, "group": "secrets"},
		},
	}
}

func resetStackProfileDraftAuthoringFlags(t *testing.T) {
	t.Helper()
	for _, command := range []struct {
		flags *pflag.FlagSet
		reset map[string]string
	}{
		{stackProfileDraftsAnnotateCmd.Flags(), map[string]string{
			"parameter": "", "description": "", "title": "", "default": "", "type": "", "enum": "",
			"required": "false", "add": "false", "output": ""}},
		{stackProfileDraftsOptionsSetCmd.Flags(), map[string]string{
			"parameter": "", "value": "", "title": "", "description": "", "output": ""}},
		{stackProfileDraftsOptionsRemoveCmd.Flags(), map[string]string{"parameter": "", "value": "", "output": ""}},
	} {
		for name, value := range command.reset {
			_ = command.flags.Set(name, value)
			if flag := command.flags.Lookup(name); flag != nil {
				flag.Changed = false
			}
		}
		for _, name := range []string{"set", "unset"} {
			if flag := command.flags.Lookup(name); flag != nil {
				if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
					_ = sliceValue.Replace([]string{})
				}
				flag.Changed = false
			}
		}
	}
}

func parameterFromRequest(t *testing.T, request *client.UpdateStackProfileDraftRequest, name string) map[string]any {
	t.Helper()
	if request == nil {
		t.Fatal("expected the draft to be written back")
	}
	parameter := draftParameterByName(request.Parameters, name)
	if parameter == nil {
		t.Fatalf("parameter %q missing from the written draft; have %v", name, draftParameterNames(request.Parameters))
	}
	return parameter
}

func TestDraftsAnnotateDeclaresAChoiceInput(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	mock := &stackProfileDraftMock{draft: gpuChatDraft()}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "drafts", "annotate", "draft-1",
			"--parameter", "model_size", "--add", "--type", "enum", "--enum", "8b, 32b",
			"--default", "8b", "--title", "Model size", "--required")
	})

	parameter := parameterFromRequest(t, mock.updateRequest, "model_size")
	if parameter["type"] != "enum" || parameter["default"] != "8b" || parameter["title"] != "Model size" {
		t.Errorf("declared parameter = %v", parameter)
	}
	if required, _ := parameter["required"].(bool); !required {
		t.Errorf("expected required=true, got %v", parameter["required"])
	}
	enumValues, _ := parameter["enum_values"].([]any)
	if len(enumValues) != 2 || enumValues[0] != "8b" || enumValues[1] != "32b" {
		t.Errorf("enum_values = %v", parameter["enum_values"])
	}
	if len(mock.updateRequest.Parameters) != 4 {
		t.Errorf("expected the three detected inputs plus the declared one, got %d", len(mock.updateRequest.Parameters))
	}
	// The platform keeps an unreferenced input only while it offers choices,
	// so a declared input is born with its enum values as choices.
	options := draftParameterOptions(parameter)
	if len(options) != 2 || options[0]["value"] != "8b" || options[1]["value"] != "32b" {
		t.Errorf("expected the enum values seeded as choices, got %v", parameter["options"])
	}
	if !strings.Contains(stdout, "Declared input model_size") || !strings.Contains(stdout, "drafts options set draft-1 --parameter model_size") {
		t.Errorf("expected the declare guidance, got: %s", stdout)
	}
}

func TestDraftsAnnotateAddNeedsEnumSoTheInputSurvivesTheSave(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	mock := &stackProfileDraftMock{draft: gpuChatDraft()}
	setMockClient(t, mock)

	_, err := executeCommand("stack-profiles", "drafts", "annotate", "draft-1", "--parameter", "model_size", "--add", "--title", "Model size")

	if err == nil || !strings.Contains(err.Error(), "--add needs --enum") {
		t.Fatalf("expected the --enum requirement, got %v", err)
	}
	if mock.updateRequest != nil {
		t.Error("an input that would be dropped on save must not be written")
	}
}

func TestDraftsAnnotateEnumReconcilesExistingChoices(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	draft := gpuChatDraft()
	draft.Parameters = append(draft.Parameters, map[string]any{
		"name": "model_size", "type": "enum", "enum_values": []any{"8b", "32b"},
		"options": []any{
			map[string]any{"value": "8b", "sets": map[string]any{"model_id": "Qwen/Qwen3-8B"}},
			map[string]any{"value": "32b", "sets": map[string]any{"model_id": "Qwen/Qwen3-32B"}},
		},
	})
	mock := &stackProfileDraftMock{draft: draft}
	setMockClient(t, mock)

	_, _ = executeCommand("stack-profiles", "drafts", "annotate", "draft-1", "--parameter", "model_size", "--enum", "8b,70b")

	options := draftParameterOptions(parameterFromRequest(t, mock.updateRequest, "model_size"))
	if len(options) != 2 || options[0]["value"] != "8b" || options[1]["value"] != "70b" {
		t.Fatalf("options = %v", options)
	}
	if draftOptionSets(options[0])["model_id"] != "Qwen/Qwen3-8B" {
		t.Errorf("a kept choice must keep its sets, got %v", options[0])
	}
	if len(draftOptionSets(options[1])) != 0 {
		t.Errorf("a new choice starts bare, got %v", options[1])
	}
}

func TestDraftsAnnotateRefusesUnknownInputWithoutAdd(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	mock := &stackProfileDraftMock{draft: gpuChatDraft()}
	setMockClient(t, mock)

	_, err := executeCommand("stack-profiles", "drafts", "annotate", "draft-1", "--parameter", "model_size", "--title", "Model size")

	if err == nil || !strings.Contains(err.Error(), "Pass --add") {
		t.Fatalf("expected the --add hint, got %v", err)
	}
	if mock.updateRequest != nil {
		t.Error("nothing must be written when the input is unknown")
	}
}

func TestDraftsAnnotateRejectsABadTypeAndSecretReshaping(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	mock := &stackProfileDraftMock{draft: gpuChatDraft()}
	setMockClient(t, mock)

	_, err := executeCommand("stack-profiles", "drafts", "annotate", "draft-1", "--parameter", "model_id", "--type", "list")
	if err == nil || !strings.Contains(err.Error(), "--type must be one of") {
		t.Fatalf("expected a type usage error, got %v", err)
	}

	resetStackProfileDraftAuthoringFlags(t)
	_, err = executeCommand("stack-profiles", "drafts", "annotate", "draft-1", "--parameter", "api_key", "--default", "hunter2")
	if err == nil || !strings.Contains(err.Error(), "secret input") {
		t.Fatalf("expected the secret refusal, got %v", err)
	}
	if mock.updateRequest != nil {
		t.Error("nothing must be written on a refused annotation")
	}
}

func TestDraftsAnnotateKeepsExistingOptionsWhenRetitling(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	draft := gpuChatDraft()
	draft.Parameters = append(draft.Parameters, map[string]any{
		"name": "model_size", "type": "enum", "enum_values": []any{"8b"},
		"options": []any{map[string]any{"value": "8b", "sets": map[string]any{"model_id": "Qwen/Qwen3-8B"}}},
	})
	mock := &stackProfileDraftMock{draft: draft}
	setMockClient(t, mock)

	_, _ = executeCommand("stack-profiles", "drafts", "annotate", "draft-1", "--parameter", "model_size", "--title", "Model size")

	parameter := parameterFromRequest(t, mock.updateRequest, "model_size")
	if len(draftParameterOptions(parameter)) != 1 {
		t.Errorf("options must survive a title edit, got %v", parameter["options"])
	}
}

func TestDraftsOptionsSetAddsAChoiceThatAnswersOtherInputs(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	draft := gpuChatDraft()
	draft.Parameters = append(draft.Parameters, map[string]any{"name": "model_size", "type": "string", "enum_values": []any{}})
	mock := &stackProfileDraftMock{draft: draft}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "drafts", "options", "set", "draft-1",
			"--parameter", "model_size", "--value", "32b", "--title", "Qwen3 32B",
			"--set", "model_id=Qwen/Qwen3-32B", "--set", "max_model_len=28672")
	})

	parameter := parameterFromRequest(t, mock.updateRequest, "model_size")
	options := draftParameterOptions(parameter)
	if len(options) != 1 || options[0]["value"] != "32b" || options[0]["title"] != "Qwen3 32B" {
		t.Fatalf("options = %v", parameter["options"])
	}
	sets := draftOptionSets(options[0])
	if sets["model_id"] != "Qwen/Qwen3-32B" || sets["max_model_len"] != "28672" {
		t.Errorf("sets = %v", sets)
	}
	if parameter["type"] != "enum" {
		t.Errorf("an input with options must become an enum, got %v", parameter["type"])
	}
	if enumValues, _ := parameter["enum_values"].([]any); len(enumValues) != 1 || enumValues[0] != "32b" {
		t.Errorf("enum_values must mirror the option values, got %v", parameter["enum_values"])
	}
	if !strings.Contains(stdout, "Added choice '32b' on model_size (1 choice).") ||
		!strings.Contains(stdout, "sets max_model_len=28672\n  sets model_id=Qwen/Qwen3-32B") {
		t.Errorf("unexpected output: %s", stdout)
	}
}

func TestDraftsOptionsSetUpdatesAnExistingChoiceInPlace(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	draft := gpuChatDraft()
	draft.Parameters = append(draft.Parameters, map[string]any{
		"name": "model_size", "type": "enum", "enum_values": []any{"8b", "32b"},
		"options": []any{
			map[string]any{"value": "8b", "sets": map[string]any{"model_id": "Qwen/Qwen3-8B", "max_model_len": "16384"}},
			map[string]any{"value": "32b", "sets": map[string]any{"model_id": "Qwen/Qwen3-32B"}},
		},
	})
	mock := &stackProfileDraftMock{draft: draft}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "drafts", "options", "set", "draft-1",
			"--parameter", "model_size", "--value", "8b", "--set", "model_id=Qwen/Qwen3-8B-AWQ", "--unset", "max_model_len")
	})

	options := draftParameterOptions(parameterFromRequest(t, mock.updateRequest, "model_size"))
	if len(options) != 2 {
		t.Fatalf("expected both choices to remain, got %d", len(options))
	}
	sets := draftOptionSets(options[0])
	if sets["model_id"] != "Qwen/Qwen3-8B-AWQ" {
		t.Errorf("model_id = %q", sets["model_id"])
	}
	if _, present := sets["max_model_len"]; present {
		t.Errorf("--unset must drop the assignment, got %v", sets)
	}
	if !strings.Contains(stdout, "Updated choice '8b' on model_size (2 choices).") {
		t.Errorf("unexpected output: %s", stdout)
	}
}

func TestDraftsOptionsSetRefusesBadTargets(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   string
	}{
		{"secret", "api_key=x", "secret input"},
		{"self", "model_size=8b", "itself"},
		{"unknown", "gone=1", "not an input on this draft"},
		{"chained", "tier=ha", "options of its own"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resetStackProfileDraftAuthoringFlags(t)
			draft := gpuChatDraft()
			draft.Parameters = append(draft.Parameters,
				map[string]any{"name": "model_size", "type": "string", "enum_values": []any{}},
				map[string]any{"name": "tier", "type": "enum", "options": []any{map[string]any{"value": "ha"}}},
			)
			mock := &stackProfileDraftMock{draft: draft}
			setMockClient(t, mock)

			_, err := executeCommand("stack-profiles", "drafts", "options", "set", "draft-1",
				"--parameter", "model_size", "--value", "8b", "--set", testCase.target)

			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("expected %q in the error, got %v", testCase.want, err)
			}
			if mock.updateRequest != nil {
				t.Error("a refused assignment must not write the draft")
			}
		})
	}
}

func TestDraftsOptionsSetRefusesASecretSelector(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	mock := &stackProfileDraftMock{draft: gpuChatDraft()}
	setMockClient(t, mock)

	_, err := executeCommand("stack-profiles", "drafts", "options", "set", "draft-1", "--parameter", "api_key", "--value", "x")
	if err == nil || !strings.Contains(err.Error(), "secret input and cannot offer choices") {
		t.Fatalf("expected the secret refusal, got %v", err)
	}
	if mock.updateRequest != nil {
		t.Error("a refused selector must not write the draft")
	}
}

// The platform keeps an unreferenced input only while it offers choices, so
// the first choice is what declares it - no separate declare step.
func TestDraftsOptionsSetDeclaresAMissingInputWithItsFirstChoice(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	mock := &stackProfileDraftMock{draft: gpuChatDraft()}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "drafts", "options", "set", "draft-1",
			"--parameter", "model_size", "--value", "8b", "--set", "model_id=Qwen/Qwen3-8B")
	})

	parameter := parameterFromRequest(t, mock.updateRequest, "model_size")
	if parameter["type"] != "enum" || parameter["title"] != "model_size" {
		t.Errorf("declared parameter = %v", parameter)
	}
	options := draftParameterOptions(parameter)
	if len(options) != 1 || draftOptionSets(options[0])["model_id"] != "Qwen/Qwen3-8B" {
		t.Errorf("options = %v", parameter["options"])
	}
	if !strings.Contains(stdout, "Declared input model_size on draft 'gpu-chat'.") || !strings.Contains(stdout, "Added choice '8b' on model_size (1 choice).") {
		t.Errorf("unexpected output: %s", stdout)
	}
}

func TestDraftsOptionsRemoveDropsAChoice(t *testing.T) {
	resetStackProfileDraftAuthoringFlags(t)
	draft := gpuChatDraft()
	draft.Parameters = append(draft.Parameters, map[string]any{
		"name": "model_size", "type": "enum", "enum_values": []any{"8b", "32b"},
		"options": []any{map[string]any{"value": "8b"}, map[string]any{"value": "32b"}},
	})
	mock := &stackProfileDraftMock{draft: draft}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "drafts", "options", "remove", "draft-1", "--parameter", "model_size", "--value", "8b")
	})

	parameter := parameterFromRequest(t, mock.updateRequest, "model_size")
	options := draftParameterOptions(parameter)
	if len(options) != 1 || options[0]["value"] != "32b" {
		t.Errorf("options = %v", parameter["options"])
	}
	if enumValues, _ := parameter["enum_values"].([]any); len(enumValues) != 1 || enumValues[0] != "32b" {
		t.Errorf("enum_values must follow the remaining choices, got %v", parameter["enum_values"])
	}
	if !strings.Contains(stdout, "Removed choice '8b' from model_size (1 choice left).") {
		t.Errorf("unexpected output: %s", stdout)
	}

	resetStackProfileDraftAuthoringFlags(t)
	mock.updateRequest = nil
	_, err := executeCommand("stack-profiles", "drafts", "options", "remove", "draft-1", "--parameter", "model_size", "--value", "70b")
	if err == nil || !strings.Contains(err.Error(), "has no choice") {
		t.Fatalf("expected the missing-choice error, got %v", err)
	}
}

func TestDraftsGetListsChoices(t *testing.T) {
	draft := gpuChatDraft()
	draft.Parameters = append(draft.Parameters, map[string]any{
		"name": "model_size", "type": "enum", "description": "Pick the model.",
		"options": []any{
			map[string]any{"value": "8b", "sets": map[string]any{"model_id": "Qwen/Qwen3-8B"}},
			map[string]any{"value": "32b", "sets": map[string]any{"model_id": "Qwen/Qwen3-32B", "max_model_len": "28672"}},
		},
	})
	setMockClient(t, &stackProfileDraftMock{draft: draft})

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "drafts", "get", "draft-1")
	})

	if !strings.Contains(stdout, "choices: 8b (sets model_id=Qwen/Qwen3-8B), 32b (sets max_model_len=28672, model_id=Qwen/Qwen3-32B)") {
		t.Errorf("expected the choices line, got: %s", stdout)
	}
	if strings.Count(stdout, "choices:") != 1 {
		t.Errorf("inputs without options must not get a choices line: %s", stdout)
	}
}

func stringPointer(value string) *string { return &value }

func gpuChatParameters() []client.StackProfileParameter {
	return []client.StackProfileParameter{
		{Name: "model_size", Type: "enum", Default: stringPointer("8b"), EnumValues: []string{"8b", "32b"},
			Options: []client.StackProfileParameterOption{
				{Value: "8b", Sets: map[string]string{"model_id": "Qwen/Qwen3-8B", "max_model_len": "16384"}},
				{Value: "32b", Sets: map[string]string{"model_id": "Qwen/Qwen3-32B", "max_model_len": "28672"}},
			}},
		{Name: "model_id", Type: "string", Required: true},
		{Name: "max_model_len", Type: "number", Required: true},
		{Name: "gpu_memory_utilization", Type: "number", Default: stringPointer("0.90")},
		{Name: "chat_host", Type: "string", Required: true},
		{Name: "api_key", Type: "secret", Required: true},
	}
}

func TestPreviewResolvedBindingsFollowsThePlatformOrder(t *testing.T) {
	rows := previewResolvedBindings(gpuChatParameters(), []client.ParameterBinding{
		{Name: "model_size", Value: "32b"},
		{Name: "max_model_len", Value: "8192"},
		{Name: "api_key", Value: "hunter2"},
		{Name: "typo", Value: "x"},
	})
	byName := map[string]bindingPreview{}
	for _, row := range rows {
		byName[row.Name] = row
	}
	if byName["model_id"].Value != "Qwen/Qwen3-32B" || byName["model_id"].Source != "choice model_size=32b" {
		t.Errorf("model_id = %+v", byName["model_id"])
	}
	if byName["max_model_len"].Value != "8192" || byName["max_model_len"].Source != "--set" {
		t.Errorf("a --set must win over the choice, got %+v", byName["max_model_len"])
	}
	if byName["gpu_memory_utilization"].Value != "0.90" || byName["gpu_memory_utilization"].Source != "default" {
		t.Errorf("gpu_memory_utilization = %+v", byName["gpu_memory_utilization"])
	}
	if byName["chat_host"].Source != "unset" || !byName["chat_host"].Required {
		t.Errorf("chat_host = %+v", byName["chat_host"])
	}
	if byName["api_key"].Value != "********" {
		t.Errorf("a secret value must never be echoed, got %+v", byName["api_key"])
	}
	if byName["typo"].Source != "--set (not an input of this version)" {
		t.Errorf("an unknown --set must be called out, got %+v", byName["typo"])
	}
	if missing := unsetRequiredInputs(rows); len(missing) != 1 || missing[0] != "chat_host" {
		t.Errorf("unset required = %v", missing)
	}
}

func TestPreviewResolvedBindingsUsesTheSelectorDefaultAndLaterSelectorWins(t *testing.T) {
	parameters := []client.StackProfileParameter{
		{Name: "size", Type: "enum", Default: stringPointer("small"), Options: []client.StackProfileParameterOption{
			{Value: "small", Sets: map[string]string{"replicas": "1", "storage": "20Gi"}}}},
		{Name: "tier", Type: "enum", Default: stringPointer("ha"), Options: []client.StackProfileParameterOption{
			{Value: "ha", Sets: map[string]string{"replicas": "3"}}}},
		{Name: "replicas", Type: "number"},
		{Name: "storage", Type: "string"},
	}
	rows := previewResolvedBindings(parameters, nil)
	byName := map[string]bindingPreview{}
	for _, row := range rows {
		byName[row.Name] = row
	}
	if byName["replicas"].Value != "3" || byName["replicas"].Source != "choice tier=ha" {
		t.Errorf("replicas = %+v", byName["replicas"])
	}
	if byName["storage"].Value != "20Gi" || byName["storage"].Source != "choice size=small" {
		t.Errorf("storage = %+v", byName["storage"])
	}
}

func TestStackProfilesApplyDryRunPreviewsWithoutCreating(t *testing.T) {
	resetStackProfileApplyFlags(t)
	mock := &stackProfileMock{detail: &client.StackProfileDetail{
		Profile: client.StackProfileSummary{ID: "profile-1", Name: "gpu-chat", CurrentVersion: 11, LatestVersion: 11},
		CurrentVersionDetail: &client.StackProfileVersionDetail{
			Version: 11, Channel: "stable", Parameters: gpuChatParameters(),
		},
	}}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "apply", "profile-1", "--dry-run",
			"--set", "model_size=32b", "--set", "chat_host=chat.example.com", "--set", "api_key=hunter2")
	})

	if mock.instantiateRequest != nil {
		t.Fatal("--dry-run must not instantiate anything")
	}
	for _, expected := range []string{
		"Dry run: inputs 'gpu-chat' v11 would deploy with (nothing created):",
		"Qwen/Qwen3-32B",
		"choice model_size=32b",
		"Every required input is set.",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("expected %q in output, got: %s", expected, stdout)
		}
	}
	if strings.Contains(stdout, "hunter2") {
		t.Errorf("secret value leaked into the preview: %s", stdout)
	}

	resetStackProfileApplyFlags(t)
	output, err := executeCommand("stack-profiles", "apply", "profile-1", "--dry-run", "--set", "model_size=8b", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var structured struct {
		Version  int              `json:"version"`
		Bindings []bindingPreview `json:"bindings"`
	}
	if unmarshalError := json.Unmarshal([]byte(output), &structured); unmarshalError != nil {
		t.Fatalf("expected JSON, got %v: %s", unmarshalError, output)
	}
	if structured.Version != 11 || len(structured.Bindings) != len(gpuChatParameters()) {
		t.Errorf("structured = %+v", structured)
	}
	for _, row := range structured.Bindings {
		if row.Name == "model_id" && (row.Value != "Qwen/Qwen3-8B" || row.Source != "choice model_size=8b") {
			t.Errorf("model_id row = %+v", row)
		}
	}
}
