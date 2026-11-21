package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
)

// A zone move carries a cluster to another Ankra Cloud zone: the platform
// builds (or adopts) a target cluster in the zone, seeds the source's stacks
// and data into it in standby, and switches traffic only when a person says
// go. Every route takes the SOURCE cluster's id. These types are the whole
// wire contract the CLI depends on, kept in this one file so a change in its
// shape is a change here.

// Zone move statuses, as the platform reports them.
const (
	ZoneMoveStatusPlanned         = "planned"
	ZoneMoveStatusProvisioning    = "provisioning"
	ZoneMoveStatusSeeding         = "seeding"
	ZoneMoveStatusReadyForCutover = "ready_for_cutover"
	ZoneMoveStatusFreezing        = "freezing"
	ZoneMoveStatusFinalCopy       = "final_copy"
	ZoneMoveStatusCuttingOver     = "cutting_over"
	ZoneMoveStatusSoaking         = "soaking"
	ZoneMoveStatusRollingBack     = "rolling_back"
	ZoneMoveStatusRolledBack      = "rolled_back"
	ZoneMoveStatusRetiring        = "retiring"
	ZoneMoveStatusRetired         = "retired"
	ZoneMoveStatusFailed          = "failed"
)

// Zone move step statuses.
const (
	ZoneMoveStepPending   = "pending"
	ZoneMoveStepRunning   = "running"
	ZoneMoveStepSucceeded = "succeeded"
	ZoneMoveStepFailed    = "failed"
	ZoneMoveStepSkipped   = "skipped"
)

// ZoneMoveDNSMechanismAnkraCloudProxied is a hostname proxied by the Ankra
// Cloud edge: the cutover repoints every such record from the source's load
// balancer to the target's in one transaction.
const ZoneMoveDNSMechanismAnkraCloudProxied = "ankracloud_proxied"

// IsZoneMoveResting reports whether a move has stopped moving on its own:
// it is waiting for a person (ready_for_cutover, soaking) or it has ended.
func IsZoneMoveResting(status string) bool {
	switch status {
	case ZoneMoveStatusReadyForCutover, ZoneMoveStatusSoaking, ZoneMoveStatusRolledBack,
		ZoneMoveStatusRetired, ZoneMoveStatusFailed:
		return true
	}
	return false
}

// ZoneMoveAction is a person's step on a move that is already under way.
type ZoneMoveAction string

const (
	// ZoneMoveActionCutover puts the maintenance page on, freezes the
	// source, takes the final copy and switches traffic to the target.
	ZoneMoveActionCutover ZoneMoveAction = "cutover"
	// ZoneMoveActionRollback sends traffic back to the source.
	ZoneMoveActionRollback ZoneMoveAction = "rollback"
	// ZoneMoveActionRetire deprovisions the source.
	ZoneMoveActionRetire ZoneMoveAction = "retire"
)

// AllowedFrom is the one status the platform accepts the action from.
func (action ZoneMoveAction) AllowedFrom() string {
	switch action {
	case ZoneMoveActionCutover:
		return ZoneMoveStatusReadyForCutover
	case ZoneMoveActionRollback, ZoneMoveActionRetire:
		return ZoneMoveStatusSoaking
	}
	return ""
}

// SettlesAt is the resting status the action is heading for.
func (action ZoneMoveAction) SettlesAt() string {
	switch action {
	case ZoneMoveActionCutover:
		return ZoneMoveStatusSoaking
	case ZoneMoveActionRollback:
		return ZoneMoveStatusRolledBack
	case ZoneMoveActionRetire:
		return ZoneMoveStatusRetired
	}
	return ""
}

// ZoneMoveRequest starts a zone move, or with PlanOnly computes its plan and
// writes nothing. Zone is required unless TargetClusterID names a cluster
// that already exists. The shape fields override the twin the platform
// derives from the source; a nil count leaves the derived one. Vault names
// the backup vault (name or id) every data copy of the move writes through;
// empty leaves each stack's to the platform: the stack's backup policy vault,
// else the organisation's only ready vault.
type ZoneMoveRequest struct {
	Zone              string `json:"zone,omitempty"`
	TargetClusterID   string `json:"target_cluster_id,omitempty"`
	TargetName        string `json:"target_name,omitempty"`
	PlanOnly          bool   `json:"plan_only"`
	ControlPlaneCount *int   `json:"control_plane_count,omitempty"`
	ControlPlanePlan  string `json:"control_plane_plan,omitempty"`
	WorkerPlan        string `json:"worker_plan,omitempty"`
	WorkerCount       *int   `json:"worker_count,omitempty"`
	StorageTier       string `json:"storage_tier,omitempty"`
	SoakDays          *int   `json:"soak_days,omitempty"`
	Vault             string `json:"vault,omitempty"`
}

// ZoneMove is a zone move as the platform reports it.
type ZoneMove struct {
	ID              string         `json:"id" yaml:"id"`
	SourceClusterID string         `json:"source_cluster_id" yaml:"source_cluster_id"`
	TargetClusterID *string        `json:"target_cluster_id" yaml:"target_cluster_id"`
	Zone            string         `json:"zone" yaml:"zone"`
	Status          string         `json:"status" yaml:"status"`
	Plan            ZoneMovePlan   `json:"plan" yaml:"plan"`
	Steps           []ZoneMoveStep `json:"steps" yaml:"steps"`
	SoakUntil       *string        `json:"soak_until" yaml:"soak_until"`
	CreatedAt       string         `json:"created_at" yaml:"created_at"`
	UpdatedAt       string         `json:"updated_at" yaml:"updated_at"`
}

// ZoneMovePlan is what the move builds, carries, leaves behind and cuts over.
type ZoneMovePlan struct {
	Target     ZoneMoveTarget       `json:"target" yaml:"target"`
	Stacks     []ZoneMoveStack      `json:"stacks" yaml:"stacks"`
	Data       []ZoneMoveDataVolume `json:"data" yaml:"data"`
	NotCarried []ZoneMoveOmission   `json:"not_carried" yaml:"not_carried"`
	DNS        []ZoneMoveHostname   `json:"dns" yaml:"dns"`
	Warnings   []string             `json:"warnings" yaml:"warnings"`
}

// ZoneMoveTarget is the shape of the cluster the move lands in.
type ZoneMoveTarget struct {
	Name         string               `json:"name" yaml:"name"`
	Zone         string               `json:"zone" yaml:"zone"`
	NetworkMode  string               `json:"network_mode" yaml:"network_mode"`
	StorageTier  string               `json:"storage_tier" yaml:"storage_tier"`
	ControlPlane ZoneMoveControlPlane `json:"control_plane" yaml:"control_plane"`
	BastionPlan  string               `json:"bastion_plan" yaml:"bastion_plan"`
	NodeGroups   []ZoneMoveNodeGroup  `json:"node_groups" yaml:"node_groups"`
}

// ZoneMoveControlPlane is the target's control plane.
type ZoneMoveControlPlane struct {
	Plan  string `json:"plan" yaml:"plan"`
	Count int    `json:"count" yaml:"count"`
}

// ZoneMoveNodeGroup is one of the target's worker node groups.
type ZoneMoveNodeGroup struct {
	Name  string `json:"name" yaml:"name"`
	Plan  string `json:"plan" yaml:"plan"`
	Count int    `json:"count" yaml:"count"`
}

// ZoneMoveStack is one of the source's stacks and whether the move carries
// it. Vault is nil for a stack whose data the move does not copy, and for one
// whose vault cannot be chosen (named in NotCarried and Warnings instead).
type ZoneMoveStack struct {
	Name    string              `json:"name" yaml:"name"`
	Carried bool                `json:"carried" yaml:"carried"`
	Reason  *string             `json:"reason" yaml:"reason"`
	Vault   *ZoneMoveStackVault `json:"vault,omitempty" yaml:"vault,omitempty"`
}

// What chose a carried stack's backup vault (ZoneMoveStackVault.Basis).
const (
	ZoneMoveVaultBasisRequest     = "request"
	ZoneMoveVaultBasisStackPolicy = "stack_policy"
	ZoneMoveVaultBasisOnlyReady   = "only_ready_vault"
)

// ZoneMoveStackVault is the backup vault a carried stack's data copies write
// their restore point to, and what chose it.
type ZoneMoveStackVault struct {
	ID    string `json:"id" yaml:"id"`
	Name  string `json:"name" yaml:"name"`
	Basis string `json:"basis" yaml:"basis"`
}

// ZoneMoveDataVolume is a data volume the move copies to the target.
type ZoneMoveDataVolume struct {
	Stack          string `json:"stack" yaml:"stack"`
	Namespace      string `json:"namespace" yaml:"namespace"`
	Name           string `json:"name" yaml:"name"`
	Kind           string `json:"kind" yaml:"kind"`
	RequestedBytes int64  `json:"requested_bytes" yaml:"requested_bytes"`
}

// ZoneMoveOmission is something the move does not carry, and why.
type ZoneMoveOmission struct {
	What   string `json:"what" yaml:"what"`
	Reason string `json:"reason" yaml:"reason"`
}

// ZoneMoveHostname is a public hostname and how the cutover moves it.
type ZoneMoveHostname struct {
	Hostname     string  `json:"hostname" yaml:"hostname"`
	Mechanism    string  `json:"mechanism" yaml:"mechanism"`
	SourceOrigin *string `json:"source_origin" yaml:"source_origin"`
	TargetOrigin *string `json:"target_origin" yaml:"target_origin"`
}

// ZoneMoveStep is one step of a move.
type ZoneMoveStep struct {
	Name       string  `json:"name" yaml:"name"`
	Status     string  `json:"status" yaml:"status"`
	StartedAt  *string `json:"started_at" yaml:"started_at"`
	FinishedAt *string `json:"finished_at" yaml:"finished_at"`
	Message    *string `json:"message" yaml:"message"`
}

type zoneMoveEnvelope struct {
	Move *ZoneMove `json:"move"`
}

// IsZoneMoveNotFound reports a 404 from the zone move routes: the source
// cluster has no move (or the cluster itself is not found).
func IsZoneMoveNotFound(requestError error) bool {
	var unexpected *UnexpectedResponseError
	return errors.As(requestError, &unexpected) && unexpected.StatusCode == http.StatusNotFound
}

func (c *Client) zoneMoveURL(sourceClusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/zone-move", c.BaseURL, neturl.PathEscape(sourceClusterID))
}

// StartZoneMove starts a zone move of the source cluster, or with
// request.PlanOnly returns the plan and writes nothing. A 409 (a move for
// this source is already active) and the 400/422 refusals surface the
// platform's own sentence.
func (c *Client) StartZoneMove(ctx context.Context, sourceClusterID string, request ZoneMoveRequest) (*ZoneMove, error) {
	move, sendError := c.sendZoneMove(ctx, http.MethodPost, c.zoneMoveURL(sourceClusterID), request)
	if sendError != nil {
		return nil, sendError
	}
	if move == nil {
		return nil, errors.New("the platform accepted the zone move but did not return it; read it with --status")
	}
	return move, nil
}

// GetZoneMove reads the source cluster's latest zone move. A cluster with
// none answers 404, which IsZoneMoveNotFound recognises.
func (c *Client) GetZoneMove(ctx context.Context, sourceClusterID string) (*ZoneMove, error) {
	move, sendError := c.sendZoneMove(ctx, http.MethodGet, c.zoneMoveURL(sourceClusterID), nil)
	if sendError != nil {
		return nil, sendError
	}
	if move == nil {
		return nil, errors.New("the platform answered without a zone move")
	}
	return move, nil
}

// AdvanceZoneMove takes a person's step on the source cluster's move. The
// platform may accept it without returning the move, in which case the
// result is nil and the caller reads it with GetZoneMove.
func (c *Client) AdvanceZoneMove(ctx context.Context, sourceClusterID string, action ZoneMoveAction) (*ZoneMove, error) {
	if action.AllowedFrom() == "" {
		return nil, fmt.Errorf("unknown zone move action %q", action)
	}
	return c.sendZoneMove(ctx, http.MethodPost, c.zoneMoveURL(sourceClusterID)+"/"+neturl.PathEscape(string(action)), nil)
}

// sendZoneMove sends one zone move request and decodes the {"move": ...}
// envelope. An empty 2xx body is a nil move, not an error.
func (c *Client) sendZoneMove(ctx context.Context, method string, url string, payload any) (*ZoneMove, error) {
	var bodyReader *bytes.Reader
	if payload != nil {
		encoded, marshalError := json.Marshal(payload)
		if marshalError != nil {
			return nil, fmt.Errorf("marshal request: %w", marshalError)
		}
		bodyReader = bytes.NewReader(encoded)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	request, requestError := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)

	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if responseError := errorFromResponse(response.StatusCode, body); responseError != nil {
		return nil, responseError
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	var envelope zoneMoveEnvelope
	if unmarshalError := json.Unmarshal(body, &envelope); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return envelope.Move, nil
}
