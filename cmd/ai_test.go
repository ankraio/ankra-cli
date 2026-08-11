package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type aiSettingsMock struct {
	baseMock
	status             *client.AIProviderStatus
	models             []client.AICatalogModel
	endpoints          []client.AIEndpoint
	discovered         []string
	createdModel       *client.AIModelRequest
	updatedModelRef    string
	updatedModel       *client.AIModelRequest
	deletedModelRef    string
	setProvider        string
	resetCalled        bool
	savedOpenRouterKey string
	openRouterSaveErr  error
	openRouterDeleted  bool
}

func (m *aiSettingsMock) GetAIProviderStatus() (*client.AIProviderStatus, error) {
	return m.status, nil
}

func (m *aiSettingsMock) SetAIProvider(provider string) (*client.AIProviderStatus, error) {
	m.setProvider = provider
	return &client.AIProviderStatus{Provider: provider}, nil
}

func (m *aiSettingsMock) ListAIModels() ([]client.AICatalogModel, error) {
	return m.models, nil
}

func (m *aiSettingsMock) CreateAIModel(request client.AIModelRequest) (*client.AICatalogModel, error) {
	m.createdModel = &request
	return &client.AICatalogModel{Key: request.Key, DisplayName: request.DisplayName, ModelID: request.ModelID}, nil
}

func (m *aiSettingsMock) UpdateAIModel(reference string, request client.AIModelRequest) (*client.AICatalogModel, error) {
	m.updatedModelRef = reference
	m.updatedModel = &request
	return &client.AICatalogModel{Key: request.Key, DisplayName: request.DisplayName, ModelID: request.ModelID}, nil
}

func (m *aiSettingsMock) DeleteAIModel(reference string) error {
	m.deletedModelRef = reference
	return nil
}

func (m *aiSettingsMock) ResetAIModels() ([]client.AICatalogModel, error) {
	m.resetCalled = true
	return m.models, nil
}

func (m *aiSettingsMock) SaveOpenRouterKey(apiKey string) (*client.AIAnthropicStatus, error) {
	if m.openRouterSaveErr != nil {
		return nil, m.openRouterSaveErr
	}
	m.savedOpenRouterKey = apiKey
	preview := "sk-or-...cdef"
	return &client.AIAnthropicStatus{Configured: true, KeyPreview: &preview}, nil
}

func (m *aiSettingsMock) DeleteOpenRouterKey() (*client.AIAnthropicStatus, error) {
	m.openRouterDeleted = true
	return &client.AIAnthropicStatus{Configured: false}, nil
}

func (m *aiSettingsMock) ListAIEndpoints() ([]client.AIEndpoint, error) {
	return m.endpoints, nil
}

func (m *aiSettingsMock) DiscoverEndpointModels(endpointID string) ([]string, error) {
	return m.discovered, nil
}

func TestAIStatusCommand(t *testing.T) {
	keyPreview := "sk-ant-...abcd"
	mock := &aiSettingsMock{
		status: &client.AIProviderStatus{
			Provider:  "anthropic",
			Anthropic: client.AIAnthropicStatus{Configured: true, KeyPreview: &keyPreview},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "status")
	})

	if !strings.Contains(stdoutOutput, "anthropic") {
		t.Errorf("expected provider in output, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, keyPreview) {
		t.Errorf("expected key preview in output, got: %s", stdoutOutput)
	}
}

func TestAIModelsListCommand(t *testing.T) {
	tier := "expert"
	modelID := "row-1"
	mock := &aiSettingsMock{
		models: []client.AICatalogModel{
			{ID: &modelID, Key: "expert", DisplayName: "Expert", Provider: "ankra", ModelID: "claude-opus-4-8", Tier: &tier, IsEnabled: true, IsDefault: true},
			{Key: "gpt4o", DisplayName: "GPT-4o", Provider: "openai_compatible", ModelID: "gpt-4o", IsEnabled: true},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "models", "list")
	})

	if !strings.Contains(stdoutOutput, "expert") {
		t.Errorf("expected expert key in output, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "gpt-4o") {
		t.Errorf("expected gpt-4o model id in output, got: %s", stdoutOutput)
	}
}

func TestAIModelsCreateCommand(t *testing.T) {
	mock := &aiSettingsMock{}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "models", "create",
			"--key", "my-model", "--name", "My Model", "--model-id", "gpt-4o", "--endpoint", "ep-123")
	})

	if mock.createdModel == nil {
		t.Fatalf("expected CreateAIModel to be called")
	}
	if mock.createdModel.Key != "my-model" || mock.createdModel.ModelID != "gpt-4o" {
		t.Errorf("unexpected create request: %+v", mock.createdModel)
	}
	if mock.createdModel.EndpointID == nil || *mock.createdModel.EndpointID != "ep-123" {
		t.Errorf("expected endpoint id ep-123, got: %+v", mock.createdModel.EndpointID)
	}
	if !strings.Contains(stdoutOutput, "created") {
		t.Errorf("expected creation confirmation, got: %s", stdoutOutput)
	}
}

func TestAIModelsUpdatePreservesUnsetFields(t *testing.T) {
	modelID := "row-9"
	mock := &aiSettingsMock{
		models: []client.AICatalogModel{
			{ID: &modelID, Key: "expert", DisplayName: "Expert", Provider: "ankra", ModelID: "claude-opus-4-8", ContextWindowTokens: 200000, MaxOutputTokens: 8192, SupportsTools: true, IsEnabled: true},
		},
	}
	setMockClient(t, mock)

	_ = captureStdout(t, func() {
		_, _ = executeCommand("ai", "models", "update", "expert", "--name", "Deep Thinker")
	})

	if mock.updatedModel == nil {
		t.Fatalf("expected UpdateAIModel to be called")
	}
	if mock.updatedModelRef != "expert" {
		t.Errorf("expected reference expert, got: %s", mock.updatedModelRef)
	}
	if mock.updatedModel.DisplayName != "Deep Thinker" {
		t.Errorf("expected new display name, got: %s", mock.updatedModel.DisplayName)
	}
	if mock.updatedModel.ModelID != "claude-opus-4-8" {
		t.Errorf("expected model id preserved, got: %s", mock.updatedModel.ModelID)
	}
	if mock.updatedModel.ContextWindowTokens != 200000 {
		t.Errorf("expected context window preserved, got: %d", mock.updatedModel.ContextWindowTokens)
	}
}

func TestAIModelsDeleteCommand(t *testing.T) {
	mock := &aiSettingsMock{}
	setMockClient(t, mock)

	_ = captureStdout(t, func() {
		_, _ = executeCommand("ai", "models", "delete", "gpt4o", "--yes")
	})

	if mock.deletedModelRef != "gpt4o" {
		t.Errorf("expected delete of gpt4o, got: %s", mock.deletedModelRef)
	}
}

func TestAIProviderCommand(t *testing.T) {
	mock := &aiSettingsMock{}
	setMockClient(t, mock)

	_ = captureStdout(t, func() {
		_, _ = executeCommand("ai", "provider", "openai_compatible")
	})

	if mock.setProvider != "openai_compatible" {
		t.Errorf("expected provider openai_compatible, got: %s", mock.setProvider)
	}
}

func TestAIEndpointsListCommand(t *testing.T) {
	mock := &aiSettingsMock{
		endpoints: []client.AIEndpoint{
			{ID: "ep-1", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", KeyPreview: "sk-...789"},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "endpoints", "list")
	})

	if !strings.Contains(stdoutOutput, "OpenRouter") {
		t.Errorf("expected endpoint name in output, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "ep-1") {
		t.Errorf("expected endpoint id in output, got: %s", stdoutOutput)
	}
}

func TestAIEndpointsDiscoverCommand(t *testing.T) {
	mock := &aiSettingsMock{discovered: []string{"gpt-4o", "gpt-4o-mini"}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "endpoints", "discover", "ep-1")
	})

	if !strings.Contains(stdoutOutput, "gpt-4o-mini") {
		t.Errorf("expected discovered model in output, got: %s", stdoutOutput)
	}
}

func TestAIStatusShowsOpenRouter(t *testing.T) {
	openRouterPreview := "sk-or-...wxyz"
	mock := &aiSettingsMock{
		status: &client.AIProviderStatus{
			Provider:   "openrouter",
			OpenRouter: client.AIAnthropicStatus{Configured: true, KeyPreview: &openRouterPreview},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "status")
	})

	if !strings.Contains(stdoutOutput, "OpenRouter (custom key):") {
		t.Errorf("expected OpenRouter block in output, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, openRouterPreview) {
		t.Errorf("expected key preview in output, got: %s", stdoutOutput)
	}
}

func TestAIOpenRouterSetWithFlag(t *testing.T) {
	const rawKey = "sk-or-v1-flag-secret-000"
	mock := &aiSettingsMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = aiOpenRouterSetCmd.Flags().Set("api-key", "") })

	var commandOutput string
	stdoutOutput := captureStdout(t, func() {
		commandOutput, _ = executeCommand("ai", "openrouter", "set", "--api-key", rawKey)
	})

	if mock.savedOpenRouterKey != rawKey {
		t.Errorf("expected SaveOpenRouterKey with the flag value, got: %q", mock.savedOpenRouterKey)
	}
	if !strings.Contains(stdoutOutput, "OpenRouter API key saved") {
		t.Errorf("expected save confirmation, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, rawKey) || strings.Contains(commandOutput, rawKey) {
		t.Errorf("the raw API key must never be echoed, got stdout %q and command output %q",
			stdoutOutput, commandOutput)
	}
}

func TestAIOpenRouterSetFromStdin(t *testing.T) {
	const rawKey = "sk-or-v1-stdin-secret-111"
	mock := &aiSettingsMock{}
	setMockClient(t, mock)
	rootCmd.SetIn(strings.NewReader(rawKey + "\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })

	var commandOutput string
	stdoutOutput := captureStdout(t, func() {
		commandOutput, _ = executeCommand("ai", "openrouter", "set")
	})

	if mock.savedOpenRouterKey != rawKey {
		t.Errorf("expected SaveOpenRouterKey with the stdin value, got: %q", mock.savedOpenRouterKey)
	}
	if strings.Contains(stdoutOutput, rawKey) || strings.Contains(commandOutput, rawKey) {
		t.Errorf("the raw API key must never be echoed, got stdout %q and command output %q",
			stdoutOutput, commandOutput)
	}
}

func TestAIOpenRouterSetMissingKey(t *testing.T) {
	mock := &aiSettingsMock{}
	setMockClient(t, mock)
	rootCmd.SetIn(strings.NewReader("\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })

	var executeError error
	_ = captureStdout(t, func() {
		_, executeError = executeCommand("ai", "openrouter", "set")
	})

	if executeError == nil {
		t.Fatalf("expected an error when no key is provided")
	}
	if mock.savedOpenRouterKey != "" {
		t.Errorf("expected no save call, got: %q", mock.savedOpenRouterKey)
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("expected usage exit code, got: %d", exitCodeFor(executeError))
	}
}

func TestAIOpenRouterSetPlatformRejects(t *testing.T) {
	mock := &aiSettingsMock{
		openRouterSaveErr: client.NewUnexpectedResponseError(400,
			"Invalid OpenRouter API key format (must start with sk-or-)"),
	}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = aiOpenRouterSetCmd.Flags().Set("api-key", "") })

	var executeError error
	_ = captureStdout(t, func() {
		_, executeError = executeCommand("ai", "openrouter", "set", "--api-key", "sk-bad-key")
	})

	if executeError == nil {
		t.Fatalf("expected the platform rejection to surface as an error")
	}
	if !strings.Contains(executeError.Error(), "Invalid OpenRouter API key format") {
		t.Errorf("expected the platform detail in the error, got: %v", executeError)
	}
}

func TestAIOpenRouterRemoveCommand(t *testing.T) {
	mock := &aiSettingsMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = aiOpenRouterRemoveCmd.Flags().Set("yes", "false") })

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("ai", "openrouter", "remove", "--yes")
	})

	if !mock.openRouterDeleted {
		t.Errorf("expected DeleteOpenRouterKey to be called")
	}
	if !strings.Contains(stdoutOutput, "OpenRouter API key removed") {
		t.Errorf("expected removal confirmation, got: %s", stdoutOutput)
	}
}

func TestAIOpenRouterRemoveDeleteAlias(t *testing.T) {
	mock := &aiSettingsMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = aiOpenRouterRemoveCmd.Flags().Set("yes", "false") })

	_ = captureStdout(t, func() {
		_, _ = executeCommand("ai", "openrouter", "delete", "--yes")
	})

	if !mock.openRouterDeleted {
		t.Errorf("expected DeleteOpenRouterKey via the delete alias")
	}
}

func TestAIOpenRouterRemoveDeclined(t *testing.T) {
	mock := &aiSettingsMock{}
	setMockClient(t, mock)
	rootCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })

	var executeError error
	_ = captureStdout(t, func() {
		_, executeError = executeCommand("ai", "openrouter", "remove")
	})

	if mock.openRouterDeleted {
		t.Errorf("expected no delete call after a declined prompt")
	}
	if !errors.Is(executeError, errCancelled) {
		t.Errorf("expected errCancelled, got: %v", executeError)
	}
}
