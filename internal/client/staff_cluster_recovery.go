package client

// The staff recovery levers of a failed Kubernetes version hop: the
// operations hold on a cluster and the repair of its Kubernetes version
// record. These are Ankra staff routes under /admin/api: the platform admits
// a signed-in staff browser session there and refuses every API token, so
// this client never sends the CLI's login token. It presents the staff
// session the caller supplies as the session cookie, plus the double-submit
// CSRF pair the routes check on writes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"time"
)

// staffSessionCookieName is the platform's session cookie, the one a staff
// member's admin-portal sign-in sets.
const staffSessionCookieName = "ankra_session"

// StaffSessionClient calls the staff-only /admin/api routes with a staff
// browser session instead of an API token.
type StaffSessionClient struct {
	BaseURL      string
	SessionToken string
	HTTP         *http.Client
}

// NewStaffSessionClient builds a client for baseURL that authenticates with
// sessionToken (the value of a staff session's ankra_session cookie).
func NewStaffSessionClient(baseURL string, sessionToken string) *StaffSessionClient {
	return &StaffSessionClient{
		BaseURL:      baseURL,
		SessionToken: sessionToken,
		HTTP:         &http.Client{Timeout: 60 * time.Second},
	}
}

// OperationsHoldRecordNode is one node of a version record check.
type OperationsHoldRecordNode struct {
	ResourceID      string     `json:"resource_id"`
	Name            string     `json:"name"`
	Role            string     `json:"role"`
	State           string     `json:"state"`
	Version         string     `json:"version"`
	ObservedVersion string     `json:"observed_version"`
	RunningVersion  string     `json:"running_version"`
	ObservedAt      *time.Time `json:"observed_at"`
	Status          string     `json:"status"`
}

// OperationsHoldRecordCheck is the confirmation state of the newest version
// record repair since the last lift.
type OperationsHoldRecordCheck struct {
	RepairedAt time.Time                  `json:"repaired_at"`
	Version    string                     `json:"version"`
	Confirmed  bool                       `json:"confirmed"`
	Nodes      []OperationsHoldRecordNode `json:"nodes"`
}

// OperationsHold is a cluster's operations hold as the platform reports it.
type OperationsHold struct {
	RawDocument
	ClusterID        string                     `json:"cluster_id"`
	OrganisationID   string                     `json:"organisation_id"`
	Held             bool                       `json:"held"`
	BlockedUntil     *time.Time                 `json:"blocked_until"`
	RemainingSeconds int                        `json:"remaining_seconds"`
	Source           string                     `json:"source"`
	SetBy            *string                    `json:"set_by"`
	SetAt            *time.Time                 `json:"set_at"`
	Reason           *string                    `json:"reason"`
	RecordCheck      *OperationsHoldRecordCheck `json:"record_check"`
}

// OperationsHoldLift is the result of lifting a hold.
type OperationsHoldLift struct {
	RawDocument
	ClusterID          string     `json:"cluster_id"`
	Lifted             bool       `json:"lifted"`
	LiftedBlockedUntil *time.Time `json:"lifted_blocked_until"`
	Forced             bool       `json:"forced"`
}

// RecordedK8sNode is one k8s row as a version record repair found and wrote
// it.
type RecordedK8sNode struct {
	ResourceID                  string     `json:"resource_id"`
	Name                        string     `json:"name"`
	Role                        string     `json:"role"`
	PreviousState               string     `json:"previous_state"`
	PreviousVersion             string     `json:"previous_version"`
	Version                     string     `json:"version"`
	ObservedVersion             string     `json:"observed_version"`
	RunningVersion              string     `json:"running_version"`
	ObservedAt                  *time.Time `json:"observed_at"`
	LastWriteAttemptAt          *time.Time `json:"last_write_attempt_at"`
	ObservationPostdatesAttempt bool       `json:"observation_postdates_attempt"`
}

// K8sVersionRecordRepair is the result of a version record repair.
type K8sVersionRecordRepair struct {
	RawDocument
	ClusterID             string            `json:"cluster_id"`
	Version               string            `json:"version"`
	RepairedAt            time.Time         `json:"repaired_at"`
	BlockedUntil          *time.Time        `json:"blocked_until"`
	KubernetesRowRepaired bool              `json:"kubernetes_row_repaired"`
	Nodes                 []RecordedK8sNode `json:"nodes"`
}

func staffOperationsHoldPath(clusterID string) string {
	return "/admin/api/clusters/" + neturl.PathEscape(clusterID) + "/operations-hold"
}

// GetOperationsHold reads the cluster's operations hold.
func (c *StaffSessionClient) GetOperationsHold(ctx context.Context, clusterID string) (*OperationsHold, error) {
	var hold OperationsHold
	if requestError := c.send(ctx, http.MethodGet, staffOperationsHoldPath(clusterID), nil, &hold); requestError != nil {
		return nil, requestError
	}
	return &hold, nil
}

// SetOperationsHold holds the cluster for duration from now (replacing any
// hold in force), with reason recorded on the audit row.
func (c *StaffSessionClient) SetOperationsHold(ctx context.Context, clusterID string, duration time.Duration, reason string) (*OperationsHold, error) {
	body := map[string]any{"duration_seconds": int(duration / time.Second), "reason": reason}
	var hold OperationsHold
	if requestError := c.send(ctx, http.MethodPost, staffOperationsHoldPath(clusterID), body, &hold); requestError != nil {
		return nil, requestError
	}
	return &hold, nil
}

// LiftOperationsHold lifts the cluster's operations hold. force overrides a
// version record repair the platform has not confirmed yet.
func (c *StaffSessionClient) LiftOperationsHold(ctx context.Context, clusterID string, reason string, force bool) (*OperationsHoldLift, error) {
	body := map[string]any{"reason": reason, "force": force}
	var lift OperationsHoldLift
	if requestError := c.send(ctx, http.MethodDelete, staffOperationsHoldPath(clusterID), body, &lift); requestError != nil {
		return nil, requestError
	}
	return &lift, nil
}

// RollbackK8sVersionRecord repairs the cluster's Kubernetes version record to
// version after a manual rollback. The platform refuses it without an
// operations hold, while a write runs, or when its last read of the control
// planes does not show them at version.
func (c *StaffSessionClient) RollbackK8sVersionRecord(ctx context.Context, clusterID string, version string, reason string) (*K8sVersionRecordRepair, error) {
	body := map[string]any{"version": version, "reason": reason}
	var repair K8sVersionRecordRepair
	path := "/admin/api/clusters/" + neturl.PathEscape(clusterID) + "/k8s-version/rollback-record"
	if requestError := c.send(ctx, http.MethodPost, path, body, &repair); requestError != nil {
		return nil, requestError
	}
	return &repair, nil
}

func (c *StaffSessionClient) send(ctx context.Context, method string, path string, body any, target any) error {
	var bodyReader io.Reader
	if body != nil {
		payload, marshalError := json.Marshal(body)
		if marshalError != nil {
			return fmt.Errorf("marshal request: %w", marshalError)
		}
		bodyReader = bytes.NewReader(payload)
	}
	request, requestError := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(&http.Cookie{Name: staffSessionCookieName, Value: c.SessionToken})
	csrfToken := generateClientCSRFToken()
	request.Header.Set(csrfHeaderName, csrfToken)
	request.AddCookie(&http.Cookie{Name: "ankra_csrf", Value: csrfToken})

	response, sendError := c.HTTP.Do(request)
	if sendError != nil {
		return fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)
	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("the staff session was refused (%s): sign in to the admin portal again and use its new session: %w",
			detailOrStatus(responseBody, response.StatusCode), ErrUnauthorized)
	}
	if response.StatusCode != http.StatusOK {
		if detail := detailFromBody(responseBody); detail != "" {
			return newBackendDetailError(response.StatusCode, detail)
		}
		return newUnexpectedResponseError(method+" "+path, response.StatusCode, redactedBodyForError(responseBody, 500))
	}
	if decodeError := decodeKeepingRawDocument(responseBody, target); decodeError != nil {
		return fmt.Errorf("parse response: %w", decodeError)
	}
	return nil
}

func detailOrStatus(body []byte, statusCode int) string {
	if detail := detailFromBody(body); detail != "" {
		return detail
	}
	return http.StatusText(statusCode)
}
