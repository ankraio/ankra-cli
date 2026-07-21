package client

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
)

const csrfHeaderName = "X-Ankra-CSRF"

type MFAMethod struct {
	ID        string  `json:"id" yaml:"id"`
	Type      string  `json:"type" yaml:"type"`
	Confirmed bool    `json:"confirmed" yaml:"confirmed"`
	CreatedAt *string `json:"created_at" yaml:"created_at,omitempty"`
}

type PasskeyInfo struct {
	ID        string  `json:"id" yaml:"id"`
	Type      string  `json:"type" yaml:"type"`
	Name      *string `json:"name" yaml:"name,omitempty"`
	CreatedAt *string `json:"created_at" yaml:"created_at,omitempty"`
}

type MFAStatus struct {
	Required               bool          `json:"required" yaml:"required"`
	Enrolled               bool          `json:"enrolled" yaml:"enrolled"`
	Methods                []MFAMethod   `json:"methods" yaml:"methods"`
	Passkeys               []PasskeyInfo `json:"passkeys" yaml:"passkeys"`
	RecoveryCodesRemaining int           `json:"recovery_codes_remaining" yaml:"recovery_codes_remaining"`
}

type StartTOTPEnrollmentResponse struct {
	Secret     string `json:"secret" yaml:"secret"`
	OtpAuthURI string `json:"otpauth_uri" yaml:"otpauth_uri"`
}

type RecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes" yaml:"recovery_codes"`
}

type RemoveMFAResponse struct {
	Success bool `json:"success" yaml:"success"`
}

type ConfirmTOTPEnrollmentRequest struct {
	Code string `json:"code"`
}

func (c *Client) GetMFAStatus() (*MFAStatus, error) {
	requestURL := c.BaseURL + "/api/v1/org/account/mfa"
	var status MFAStatus
	if err := c.getJSON(requestURL, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func (c *Client) StartTOTPEnrollment() (*StartTOTPEnrollmentResponse, error) {
	requestURL := c.BaseURL + "/api/v1/org/account/mfa/totp/start"
	var response StartTOTPEnrollmentResponse
	if err := c.postCSRFJSON(requestURL, nil, &response, "start authenticator setup"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) ConfirmTOTPEnrollment(code string) (*RecoveryCodesResponse, error) {
	requestURL := c.BaseURL + "/api/v1/org/account/mfa/totp/confirm"
	requestBody := ConfirmTOTPEnrollmentRequest{Code: code}
	var response RecoveryCodesResponse
	if err := c.postCSRFJSON(requestURL, requestBody, &response, "confirm authenticator setup"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) RemoveMFAMethod(methodID string) (*RemoveMFAResponse, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/account/mfa/methods/%s", c.BaseURL, neturl.PathEscape(methodID))
	var response RemoveMFAResponse
	if err := c.deleteCSRFJSON(requestURL, &response, "remove authenticator"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) RegenerateRecoveryCodes() (*RecoveryCodesResponse, error) {
	requestURL := c.BaseURL + "/api/v1/org/account/mfa/recovery-code"
	var response RecoveryCodesResponse
	if err := c.postCSRFJSON(requestURL, nil, &response, "regenerate recovery codes"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) RemovePasskey(credentialID string) (*RemoveMFAResponse, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/account/mfa/webauthn/%s", c.BaseURL, neturl.PathEscape(credentialID))
	var response RemoveMFAResponse
	if err := c.deleteCSRFJSON(requestURL, &response, "remove passkey"); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) postCSRFJSON(requestURL string, requestBody interface{}, target interface{}, operation string) error {
	payload, err := marshalOptionalJSON(requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	c.applyAuthAndCSRFHeaders(request)
	return c.doJSON(request, target, operation)
}

func (c *Client) patchCSRFJSON(requestURL string, requestBody interface{}, target interface{}, operation string) error {
	payload, err := marshalOptionalJSON(requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPatch, requestURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	c.applyAuthAndCSRFHeaders(request)
	return c.doJSON(request, target, operation)
}

func (c *Client) deleteCSRFJSON(requestURL string, target interface{}, operation string) error {
	request, err := http.NewRequest(http.MethodDelete, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.applyAuthAndCSRFHeaders(request)
	return c.doJSON(request, target, operation)
}

func (c *Client) applyAuthAndCSRFHeaders(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+c.Token)
	csrfToken := generateClientCSRFToken()
	request.Header.Set(csrfHeaderName, csrfToken)
	request.AddCookie(&http.Cookie{Name: "ankra_csrf", Value: csrfToken})
}

func (c *Client) doJSON(request *http.Request, target interface{}, operation string) error {
	response, err := c.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(response)

	body, err := readResponseBody(response)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		detail := detailFromBody(body)
		if detail != "" {
			return newUnexpectedResponseErrorWithMessage(response.StatusCode, detail)
		}
		return newUnexpectedResponseError(operation, response.StatusCode, redactedBodyForError(body, 500))
	}
	if target == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

func marshalOptionalJSON(value interface{}) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return payload, nil
}

func generateClientCSRFToken() string {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "ankra-cli-csrf"
	}
	return base64.RawURLEncoding.EncodeToString(tokenBytes)
}
