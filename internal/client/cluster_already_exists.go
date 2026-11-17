package client

import (
	"encoding/json"
	"net/http"
)

// ClusterAlreadyExistsCode is the error_code of the import's 409 for a write
// that was held to creating a cluster and found the name taken.
const ClusterAlreadyExistsCode = "CLUSTER_ALREADY_EXISTS"

// ClusterAlreadyExistsError is that refusal: a cluster already holds the name
// and nothing was written. ClusterID and Name are the existing cluster's, and
// either is empty when the platform did not say.
//
// The import answers other 409s (an apply of the same cluster already
// running, a changed Git repository, a repoint without its acknowledgement),
// so a caller matches this type, never the status.
type ClusterAlreadyExistsError struct {
	StatusCode int
	Message    string
	ClusterID  string
	Name       string
}

func (refusal *ClusterAlreadyExistsError) Error() string {
	return refusal.Message
}

// Unwrap gives the *UnexpectedResponseError a 409 with a detail is everywhere
// else, so a caller that reads the status and the detail of a refused write
// sees this one as it sees the others.
func (refusal *ClusterAlreadyExistsError) Unwrap() error {
	return newBackendDetailError(refusal.StatusCode, refusal.Message)
}

// clusterAlreadyExistsFromBody parses the structured refusal, and returns nil
// for any other answer.
func clusterAlreadyExistsFromBody(statusCode int, body []byte) *ClusterAlreadyExistsError {
	if statusCode != http.StatusConflict {
		return nil
	}
	var parsed struct {
		ErrorCode string `json:"error_code"`
		Detail    string `json:"detail"`
		ClusterID string `json:"cluster_id"`
		Name      string `json:"name"`
	}
	if json.Unmarshal(body, &parsed) != nil || parsed.ErrorCode != ClusterAlreadyExistsCode {
		return nil
	}
	message := parsed.Detail
	if message == "" {
		message = "a cluster with this name already exists"
	}
	return &ClusterAlreadyExistsError{
		StatusCode: statusCode,
		Message:    message,
		ClusterID:  parsed.ClusterID,
		Name:       parsed.Name,
	}
}
