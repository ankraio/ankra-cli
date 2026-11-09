package client

// Uninstalling and installing a stack (ankra-vrujb7, ankraio/cluster#6021).
//
// An uninstall takes every add-on, manifest and application of a stack off
// its cluster while the stack stays declared in Ankra and in Git; an install
// deploys the stack again with the definitions it holds now. Both are the
// bearer twins of the portal's Uninstall stack and Install stack:
//
//	POST /api/v1/org/clusters/imported/{cluster_id}/stacks/{stack_name}/uninstall  (stacks.delete)
//	POST /api/v1/org/clusters/imported/{cluster_id}/stacks/{stack_name}/install    (stacks.deploy)
//
// Neither takes a body. Every 409 they answer is a refusal that changed
// nothing, with a sentence saying what to do first; the error_code that rides
// with it is kept, so a caller can tell the classes apart without reading the
// sentence.

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// The error_code values a stack uninstall or install answers its 409 with.
// A 409 may also carry none: a managed service owns the stack (retire the
// service instead), or the stack name is stored as more than one record.
const (
	// StackInstallationRefusedCode: the stack's own state refuses it right
	// now (a member still deploying or failed, an open draft, being
	// deleted, down with its stopped cluster; for an install also a stack
	// that is not uninstalled, or whose uninstall is still running).
	StackInstallationRefusedCode = "stack_installation_refused"
	// StackInstallationClusterUnavailableCode: the cluster is not running
	// or not online.
	StackInstallationClusterUnavailableCode = "cluster_unavailable"
	// StackUninstallInProgressCode: the write would change what a stack's
	// uninstall is still removing from the cluster. The same write succeeds
	// once the removal is done.
	StackUninstallInProgressCode = "STACK_UNINSTALL_IN_PROGRESS"
)

// StackInstallationResult is the platform's answer to a stack uninstall or
// install. ResourceIDs names the members the call sent off the cluster
// (uninstall) or back onto it (install), empty when nothing was left to move.
// The embedded RawDocument keeps the platform's own document, so -o json
// prints exactly what it answered.
type StackInstallationResult struct {
	RawDocument `json:"-" yaml:"-"`
	StackName   string   `json:"stack_name" yaml:"stack_name"`
	Installed   bool     `json:"installed" yaml:"installed"`
	ResourceIDs []string `json:"resource_ids" yaml:"resource_ids"`
}

// StackInstallationRefusedError is a 409 from a stack uninstall or install:
// the platform refused it and changed nothing. Detail is the platform's own
// sentence, which says what to wait for or do first, and is the error's whole
// message. ErrorCode is one of the StackInstallation*Code /
// StackUninstallInProgressCode values, or empty when the platform sent none.
type StackInstallationRefusedError struct {
	Detail    string
	ErrorCode string
}

func (refusal *StackInstallationRefusedError) Error() string {
	if refusal == nil {
		return ""
	}
	return refusal.Detail
}

// UninstallStack takes every member of stackName off the cluster while the
// stack stays declared in Ankra and in Git. It needs stacks.delete on the
// cluster.
func (c *Client) UninstallStack(ctx context.Context, clusterID, stackName string) (*StackInstallationResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/uninstall",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	return c.changeStackInstallation(ctx, url, clusterID, stackName, "uninstall")
}

// InstallStack deploys an uninstalled stack again. It needs stacks.deploy on
// the cluster.
func (c *Client) InstallStack(ctx context.Context, clusterID, stackName string) (*StackInstallationResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/install",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName))
	return c.changeStackInstallation(ctx, url, clusterID, stackName, "install")
}

// changeStackInstallation sends the bodiless POST both verbs share to url and
// keeps the refusal's error_code, which the shared send helper drops. Each
// verb spells its own path, so the cluster route check can resolve both.
func (c *Client) changeStackInstallation(ctx context.Context, url, clusterID, stackName, action string) (*StackInstallationResult, error) {
	request, requestError := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, doError := c.HTTP.Do(request)
	if doError != nil {
		// A timeout means the request may have reached the platform and
		// been applied, so it is reported as an unknown outcome rather than
		// as a failure; any other transport error is returned as it is.
		return nil, unknownOutcome(fmt.Errorf("request failed: %w", doError), action+" stack '"+stackName+"'",
			"ankra cluster stacks list "+stackName+" --cluster "+clusterID, "")
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, stackInstallationResponseError(response.StatusCode, body)
	}
	var result StackInstallationResult
	if decodeError := decodeKeepingRawDocument(body, &result); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	return &result, nil
}

// stackInstallationResponseError classifies a non-2xx answer. It is the
// shared send helper's classification with one addition: a 409 carrying a
// sentence becomes a StackInstallationRefusedError that keeps its error_code.
// A billing refusal is matched before it, so a BILLING_RESTRICTED body keeps
// its own type whatever status it rides on.
func stackInstallationResponseError(statusCode int, body []byte) error {
	if denied := PermissionDeniedFromResponse(statusCode, body); denied != nil {
		return denied
	}
	if restricted := BillingRestrictedFromResponse(body); restricted != nil {
		return restricted
	}
	if statusCode == http.StatusConflict {
		if decoded, isAPIError := DecodeAPIErrorBody(body); isAPIError && decoded.Detail != "" {
			return &StackInstallationRefusedError{Detail: decoded.Detail, ErrorCode: decoded.ErrorCode}
		}
	}
	if detail := detailFromBody(body); detail != "" {
		return newBackendDetailError(statusCode, detail)
	}
	return newUnexpectedResponseError("request failed", statusCode, redactedBodyForError(body, 500))
}
