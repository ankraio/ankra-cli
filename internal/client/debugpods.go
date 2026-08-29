package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Debug pods: a pod Ankra creates in a namespace that impersonates another
// pod - same service account, node, volumes and mounts, environment - under
// an image chosen for its tools. The platform builds the mirror; these calls
// ride the bearer twins of the portal's debug-pod routes.

type DebugPodImage struct {
	Image       string `json:"image"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
}

type DebugPodImagesResponse struct {
	Images []DebugPodImage `json:"images"`
}

type CreateDebugPodRequest struct {
	Namespace           string   `json:"namespace"`
	TargetPodName       string   `json:"target_pod_name,omitempty"`
	TargetContainerName string   `json:"target_container_name,omitempty"`
	Image               string   `json:"image,omitempty"`
	Command             []string `json:"command,omitempty"`
	MirrorVolumeMounts  bool     `json:"mirror_volume_mounts"`
	MirrorEnvironment   bool     `json:"mirror_environment"`
	TTLSeconds          int      `json:"ttl_seconds"`
}

type DebugPodResponse struct {
	PodName                      string   `json:"pod_name"`
	Namespace                    string   `json:"namespace"`
	ContainerName                string   `json:"container_name"`
	Image                        string   `json:"image"`
	NodeName                     string   `json:"node_name"`
	Phase                        string   `json:"phase"`
	Ready                        bool     `json:"ready"`
	ExpiresAt                    string   `json:"expires_at"`
	TargetPodName                *string  `json:"target_pod_name"`
	TargetContainerName          *string  `json:"target_container_name"`
	ServiceAccountName           *string  `json:"service_account_name"`
	MirroredVolumes              []string `json:"mirrored_volumes"`
	MirroredVolumeMounts         []string `json:"mirrored_volume_mounts"`
	MirroredEnvironmentVariables int      `json:"mirrored_environment_variables"`
	MirroredEnvironmentSources   int      `json:"mirrored_environment_sources"`
	Warnings                     []string `json:"warnings"`
	RequestedBy                  string   `json:"requested_by"`
}

type DebugPodSummary struct {
	PodName             string  `json:"pod_name"`
	Namespace           string  `json:"namespace"`
	Phase               string  `json:"phase"`
	NodeName            string  `json:"node_name"`
	Image               string  `json:"image"`
	TargetPodName       *string `json:"target_pod_name"`
	TargetContainerName *string `json:"target_container_name"`
	RequestedBy         string  `json:"requested_by"`
	CreatedAt           string  `json:"created_at"`
	ExpiresAt           string  `json:"expires_at"`
	IsExpired           bool    `json:"is_expired"`
}

type ListDebugPodsResponse struct {
	DebugPods []DebugPodSummary `json:"debug_pods"`
}

type DeleteDebugPodResponse struct {
	Status  string  `json:"status"`
	Message *string `json:"message"`
}

func (c *Client) debugPodsEndpoint(clusterID string) string {
	return fmt.Sprintf("%s/api/v1/clusters/%s/kubernetes/debug-pods", c.BaseURL, url.PathEscape(clusterID))
}

// ListDebugPodImages reads the platform's tag-pinned image catalogue.
func (c *Client) ListDebugPodImages(clusterID string) (*DebugPodImagesResponse, error) {
	var response DebugPodImagesResponse
	if err := c.getJSON(c.debugPodsEndpoint(clusterID)+"/images", &response); err != nil {
		return nil, fmt.Errorf("listing debug pod images: %w", err)
	}
	return &response, nil
}

// CreateDebugPod creates the pod and waits for it to start. A refusal -
// the platform's, the agent's, or an agent too old to build one - carries
// its reason in the error; a cluster that is offline or agentless surfaces
// as ClusterUnavailableError like the other kubernetes calls.
func (c *Client) CreateDebugPod(clusterID string, request CreateDebugPodRequest) (*DebugPodResponse, error) {
	var response DebugPodResponse
	if err := c.sendKubernetesJSON(http.MethodPost, c.debugPodsEndpoint(clusterID), request, &response); err != nil {
		return nil, fmt.Errorf("creating debug pod: %w", err)
	}
	return &response, nil
}

// ListDebugPods lists the cluster's debug pods, optionally within one namespace.
func (c *Client) ListDebugPods(clusterID string, namespace string) (*ListDebugPodsResponse, error) {
	endpoint := c.debugPodsEndpoint(clusterID)
	if namespace != "" {
		endpoint += "?namespace=" + url.QueryEscape(namespace)
	}
	var response ListDebugPodsResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return nil, fmt.Errorf("listing debug pods: %w", err)
	}
	return &response, nil
}

// DeleteDebugPod deletes a debug pod; a pod without the debug label answers 404.
func (c *Client) DeleteDebugPod(clusterID string, namespace string, podName string) (*DeleteDebugPodResponse, error) {
	endpoint := c.debugPodsEndpoint(clusterID) + "/" + url.PathEscape(namespace) + "/" + url.PathEscape(podName)
	var response DeleteDebugPodResponse
	if err := c.sendKubernetesJSON(http.MethodDelete, endpoint, nil, &response); err != nil {
		return nil, fmt.Errorf("deleting debug pod: %w", err)
	}
	return &response, nil
}

// sendKubernetesJSON issues an authenticated JSON request on the kubernetes
// lane: a 503 CLUSTER_OFFLINE / NO_AGENT / AGENT_TIMEOUT envelope becomes
// ClusterUnavailableError so the command prints the shared advice, an RBAC
// 403 becomes PermissionDeniedError, and any other refusal surfaces the
// backend's detail string.
func (c *Client) sendKubernetesJSON(method string, endpoint string, payload any, target any) error {
	var bodyReader *bytes.Reader
	if payload != nil {
		encoded, marshalError := json.Marshal(payload)
		if marshalError != nil {
			return fmt.Errorf("marshal request: %w", marshalError)
		}
		bodyReader = bytes.NewReader(encoded)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	request, requestError := http.NewRequest(method, endpoint, bodyReader)
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return fmt.Errorf("read response: %w", readError)
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case response.StatusCode == http.StatusServiceUnavailable:
		return parseClusterError(body)
	case response.StatusCode < 200 || response.StatusCode >= 300:
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return denied
		}
		if detail := detailFromBody(body); detail != "" {
			return newUnexpectedResponseErrorWithMessage(response.StatusCode, detail)
		}
		return newUnexpectedResponseError("request failed", response.StatusCode, redactedBodyForError(body, 500))
	}
	if target == nil {
		return nil
	}
	if unmarshalError := json.Unmarshal(body, target); unmarshalError != nil {
		return fmt.Errorf("parse response: %w", unmarshalError)
	}
	return nil
}

// Recorded terminal sessions: what crossed the relay while a pod terminal
// was open, read behind audit.read.

type TerminalSession struct {
	ID                string  `json:"id"`
	OrganisationID    string  `json:"organisation_id"`
	ClusterID         string  `json:"cluster_id"`
	AuditLogID        *string `json:"audit_log_id"`
	UserEmail         string  `json:"user_email"`
	Namespace         string  `json:"namespace"`
	PodName           string  `json:"pod_name"`
	ContainerName     string  `json:"container_name"`
	Shell             string  `json:"shell"`
	StartedAt         string  `json:"started_at"`
	EndedAt           *string `json:"ended_at"`
	EndReason         *string `json:"end_reason"`
	RecordedBytes     int64   `json:"recorded_bytes"`
	IsTruncated       bool    `json:"is_truncated"`
	RecordingDegraded bool    `json:"recording_degraded"`
}

type TerminalTranscriptChunk struct {
	Sequence   int    `json:"sequence"`
	Direction  string `json:"direction"`
	RecordedAt string `json:"recorded_at"`
	Data       string `json:"data"`
}

type TerminalTranscriptPage struct {
	SessionID string                    `json:"session_id"`
	Chunks    []TerminalTranscriptChunk `json:"chunks"`
	NextAfter int                       `json:"next_after"`
	HasMore   bool                      `json:"has_more"`
}

func (c *Client) terminalSessionEndpoint(sessionID string) string {
	return c.BaseURL + "/api/v1/org/organisation/terminal-sessions/" + url.PathEscape(sessionID)
}

// GetTerminalSession reads one recorded session's facts.
func (c *Client) GetTerminalSession(sessionID string) (*TerminalSession, error) {
	var session TerminalSession
	if err := c.getJSON(c.terminalSessionEndpoint(sessionID), &session); err != nil {
		return nil, fmt.Errorf("getting terminal session: %w", err)
	}
	return &session, nil
}

// GetTerminalTranscript reads one page of a session's transcript, keyset on
// the chunk sequence.
func (c *Client) GetTerminalTranscript(sessionID string, afterSequence int, limit int) (*TerminalTranscriptPage, error) {
	query := url.Values{}
	if afterSequence > 0 {
		query.Set("after", strconv.Itoa(afterSequence))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	endpoint := c.terminalSessionEndpoint(sessionID) + "/transcript"
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var page TerminalTranscriptPage
	if err := c.getJSON(endpoint, &page); err != nil {
		return nil, fmt.Errorf("getting terminal transcript: %w", err)
	}
	return &page, nil
}
