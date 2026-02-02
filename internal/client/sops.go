package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type EncryptContentRequest struct {
	YamlContent    string   `json:"yaml_content"`
	EncryptedPaths []string `json:"encrypted_paths"`
}

type EncryptContentResponse struct {
	EncryptedYaml string `json:"encrypted_yaml"`
	Success       bool   `json:"success"`
}

type DecryptContentRequest struct {
	EncryptedYaml string `json:"encrypted_yaml"`
}

type DecryptContentResponse struct {
	DecryptedContent string `json:"decrypted_content"`
	IsEncrypted      bool   `json:"is_encrypted"`
}

type APIErrorResponse struct {
	Detail string `json:"detail"`
}

// EncryptYAML encrypts specific paths in a YAML document using SOPS via the server API
func EncryptYAML(token, baseURL, yamlContent string, encryptedPaths []string) (string, error) {
	reqBody := EncryptContentRequest{
		YamlContent:    yamlContent,
		EncryptedPaths: encryptedPaths,
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
		// Try to extract the error message from the response
		var apiErr APIErrorResponse
		if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Detail != "" {
			return "", fmt.Errorf("%s", apiErr.Detail)
		}
		return "", fmt.Errorf("encrypt failed: status %d", resp.StatusCode)
	}

	var encryptResp EncryptContentResponse
	if err := json.Unmarshal(body, &encryptResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if !encryptResp.Success {
		return "", fmt.Errorf("encryption failed")
	}

	return encryptResp.EncryptedYaml, nil
}

// DecryptYAML decrypts a SOPS-encrypted YAML document via the server API
func DecryptYAML(token, baseURL, encryptedYaml string) (string, error) {
	reqBody := DecryptContentRequest{
		EncryptedYaml: encryptedYaml,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/api/v1/org/sops/decrypt"
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
		// Try to extract the error message from the response
		var apiErr APIErrorResponse
		if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Detail != "" {
			return "", fmt.Errorf("%s", apiErr.Detail)
		}
		return "", fmt.Errorf("decrypt failed: status %d", resp.StatusCode)
	}

	var decryptResp DecryptContentResponse
	if err := json.Unmarshal(body, &decryptResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	return decryptResp.DecryptedContent, nil
}
