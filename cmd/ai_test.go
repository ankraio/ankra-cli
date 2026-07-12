package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type aiSettingsMock struct {
	baseMock
	status          *client.AIProviderStatus
	models          []client.AICatalogModel
	endpoints       []client.AIEndpoint
	discovered      []string
	createdModel    *client.AIModelRequest
	updatedModelRef string
	updatedModel    *client.AIModelRequest
	deletedModelRef string
	setProvider     string
	resetCalled     bool
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
