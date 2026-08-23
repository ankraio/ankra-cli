package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ankra/internal/client"
)

type applicationAIConfigMock struct {
	baseMock
	sentConfiguration json.RawMessage
	resetCalls        int
	publishRequest    *client.PublishApplicationAddonRequest
	publishedCalls    int
}

func (mock *applicationAIConfigMock) GetApplicationAIConfig(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return json.RawMessage(`{"pull_request_review":{"enabled":true}}`), nil
}

func (mock *applicationAIConfigMock) UpdateApplicationAIConfig(requestContext context.Context, applicationID string, configuration json.RawMessage) (json.RawMessage, error) {
	mock.sentConfiguration = configuration
	return configuration, nil
}

func (mock *applicationAIConfigMock) ResetApplicationAIConfig(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.resetCalls++
	return json.RawMessage(`{"reset":true}`), nil
}

func (mock *applicationAIConfigMock) PublishApplicationAddon(requestContext context.Context, applicationID string, publishRequest client.PublishApplicationAddonRequest) (json.RawMessage, error) {
	mock.publishRequest = &publishRequest
	return json.RawMessage(`{"id":"addon-1"}`), nil
}

func (mock *applicationAIConfigMock) GetApplicationPublishedAddon(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.publishedCalls++
	return json.RawMessage(`{"id":"addon-1"}`), nil
}

func TestApplicationAIConfigSetRequiresValidJSONFile(t *testing.T) {
	mock := &applicationAIConfigMock{}
	if _, executeError := runApplicationCommand(t, mock, "ai-config", "set", testApplicationID); executeError == nil {
		t.Fatal("expected an error without --file")
	}
	badJSONPath := filepath.Join(t.TempDir(), "lanes.json")
	if writeError := os.WriteFile(badJSONPath, []byte("{not json"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if _, executeError := runApplicationCommand(t, mock,
		"ai-config", "set", testApplicationID, "--file", badJSONPath); executeError == nil {
		t.Fatal("expected invalid JSON to be refused")
	}
	if mock.sentConfiguration != nil {
		t.Fatal("expected no update call on validation failures")
	}
}

func TestApplicationAIConfigSetSendsConfiguration(t *testing.T) {
	mock := &applicationAIConfigMock{}
	configurationPath := filepath.Join(t.TempDir(), "lanes.json")
	configuration := `{"pull_request_review":{"enabled":false}}`
	if writeError := os.WriteFile(configurationPath, []byte(configuration), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if _, executeError := runApplicationCommand(t, mock,
		"ai-config", "set", testApplicationID, "--file", configurationPath); executeError != nil {
		t.Fatalf("ai-config set failed: %v", executeError)
	}
	if string(mock.sentConfiguration) != configuration {
		t.Errorf("sent configuration = %s", mock.sentConfiguration)
	}
}

func TestApplicationAIConfigClearConfirmation(t *testing.T) {
	mock := &applicationAIConfigMock{}
	if _, executeError := runApplicationCommandWithInput(t, mock, "n\n",
		"ai-config", "clear", testApplicationID); executeError == nil {
		t.Fatal("expected the declined confirmation to error")
	}
	if mock.resetCalls != 0 {
		t.Fatalf("expected no reset call on decline, got %d", mock.resetCalls)
	}
	if _, executeError := runApplicationCommand(t, mock,
		"ai-config", "clear", testApplicationID, "--yes"); executeError != nil {
		t.Fatalf("ai-config clear --yes failed: %v", executeError)
	}
	if mock.resetCalls != 1 {
		t.Fatalf("expected one reset call, got %d", mock.resetCalls)
	}
}

func TestApplicationPublishAddonMapsFlags(t *testing.T) {
	mock := &applicationAIConfigMock{}
	if _, executeError := runApplicationCommand(t, mock, "publish-addon", testApplicationID,
		"--version", "1.2.0", "--display-name", "Website",
		"--category", "web", "--changelog", "TLS defaults"); executeError != nil {
		t.Fatalf("publish-addon failed: %v", executeError)
	}
	request := mock.publishRequest
	if request == nil {
		t.Fatal("expected a publish call")
	}
	if request.Version != "1.2.0" || request.DisplayName != "Website" ||
		request.Category != "web" || request.Changelog != "TLS defaults" {
		t.Errorf("publish request = %+v", request)
	}
}

func TestApplicationPublishedAddonReads(t *testing.T) {
	mock := &applicationAIConfigMock{}
	output, executeError := runApplicationCommand(t, mock, "published-addon", testApplicationID)
	if executeError != nil {
		t.Fatalf("published-addon failed: %v", executeError)
	}
	if mock.publishedCalls != 1 {
		t.Fatalf("expected one published-addon call, got %d", mock.publishedCalls)
	}
	if !json.Valid([]byte(output)) {
		t.Errorf("output = %q", output)
	}
}
