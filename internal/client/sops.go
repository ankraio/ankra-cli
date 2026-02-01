package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type EncryptContentRequest struct {
	YamlContent    string   `json:"yaml_content"`
	EncryptedPaths []string `json:"encrypted_paths"`
}

type EncryptContentResponse struct {
	EncryptedYaml string `json:"encrypted_yaml"`
	Success       bool   `json:"success"`
}

func EncryptSecret(token, baseURL, secret string) (string, error) {
	yamlContent := fmt.Sprintf("value: %q\n", secret)

	reqBody := EncryptContentRequest{
		YamlContent:    yamlContent,
		EncryptedPaths: []string{"value"},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/api/v1/org/sops/encrypt"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("encrypt failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var encryptResp EncryptContentResponse
	if err := json.Unmarshal(body, &encryptResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if !encryptResp.Success {
		return "", fmt.Errorf("encryption failed")
	}

	var encryptedYaml struct {
		Value string `yaml:"value"`
	}
	if err := yaml.Unmarshal([]byte(encryptResp.EncryptedYaml), &encryptedYaml); err != nil {
		return "", fmt.Errorf("parse encrypted yaml: %w", err)
	}

	return encryptedYaml.Value, nil
}
