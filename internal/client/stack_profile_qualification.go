package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// ProfileRuntimeQualification records observations, not permission to enable AI.
type ProfileRuntimeQualification struct {
	RawDocument              `json:"-" yaml:"-"`
	OperationID              string                             `json:"operation_id"`
	RequestID                string                             `json:"request_id"`
	State                    string                             `json:"state"`
	Outcome                  string                             `json:"outcome"`
	ClusterID                string                             `json:"cluster_id"`
	AgentID                  string                             `json:"agent_id"`
	AgentVersion             string                             `json:"agent_version"`
	Image                    string                             `json:"image"`
	CreatedAt                string                             `json:"created_at"`
	UpdatedAt                string                             `json:"updated_at"`
	Report                   *ProfileRuntimeQualificationReport `json:"report"`
	SubjectRetentionObserved bool                               `json:"subject_retention_observed"`
	CleanupState             string                             `json:"cleanup_state"`
}

type ProfileRuntimeQualificationReport struct {
	Version string            `json:"version"`
	Nonce   string            `json:"nonce"`
	Policy  string            `json:"policy"`
	Image   string            `json:"image"`
	Checks  map[string]string `json:"checks"`
}

const profileRuntimeQualificationPath = stackProfilesAPIPath + "/summary-runtime-qualifications"

func (client *Client) StartProfileRuntimeQualification(ctx context.Context, requestID string) (*ProfileRuntimeQualification, error) {
	return client.profileQualificationRequest(ctx, http.MethodPost, profileRuntimeQualificationPath, struct {
		RequestID string `json:"request_id"`
	}{requestID})
}

func (client *Client) GetProfileRuntimeQualification(ctx context.Context, operationID string) (*ProfileRuntimeQualification, error) {
	return client.profileQualificationRequest(ctx, http.MethodGet, profileRuntimeQualificationPath+"/"+url.PathEscape(operationID), nil)
}

func (client *Client) CancelProfileRuntimeQualification(ctx context.Context, operationID string) (*ProfileRuntimeQualification, error) {
	return client.profileQualificationRequest(ctx, http.MethodPost, profileRuntimeQualificationPath+"/"+url.PathEscape(operationID)+"/cancel", struct{}{})
}

func (client *Client) profileQualificationRequest(ctx context.Context, method, path string, request any) (*ProfileRuntimeQualification, error) {
	payload, requestError := client.profileSummaryRequest(ctx, method, path, request)
	if requestError != nil {
		var responseError *UnexpectedResponseError
		if errors.As(requestError, &responseError) {
			message := "Runtime qualification could not be confirmed. Recover with the original request ID or read the operation ID."
			switch responseError.StatusCode {
			case http.StatusNotFound:
				message = "The runtime qualification operation was not found in this organisation."
			case http.StatusConflict:
				message = "A runtime qualification is already active or the request identity conflicts. Recover using the original request ID."
			case http.StatusUnprocessableEntity:
				message = "The platform refused the runtime qualification request as invalid. Check the request or operation ID."
			}
			return nil, newUnexpectedResponseError(message, responseError.StatusCode, "")
		}
		return nil, requestError
	}
	var result ProfileRuntimeQualification
	if decodeError := decodeKeepingRawDocument(payload, &result); decodeError != nil || result.OperationID == "" || result.RequestID == "" || result.State == "" {
		return nil, fmt.Errorf("invalid runtime qualification response; retain the request ID and read back before retrying")
	}
	return &result, nil
}
