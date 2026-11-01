package client

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// Disconnecting one application environment from a managed service that
// keeps running for its other applications (cluster#5595, ankra-9sb1gw). It
// is two steps on the platform, like a retirement: a prepare that resolves
// and stores a plan with a digest and a ten-minute expiry and changes
// nothing, and a confirm that carries that digest and is the only effect. The
// client mirrors the split one method per route; it never confirms on its
// own.
//
// Every statement in a plan is the platform's own sentence, resolved on the
// server: the CLI prints them as sent. Enumerations (workloads state, network
// reason, phase, outcome) are kept as plain strings, so a value a newer
// platform adds decodes and prints as it was sent rather than failing.
//
// Nothing here carries a credential value: a plan names the connection
// Secret and the variables it delivers, never what they hold.

// ServiceDisconnectAfterNextDeploy is the timing every disconnection takes
// today: the application keeps working until it is deployed again. The
// platform refuses "now" while every application of a service shares one
// credential, so the client never sends it.
const ServiceDisconnectAfterNextDeploy = "after_next_deploy"

// The phases of a confirmed disconnection, in order (cluster
// serviceruntime.DisconnectionPhase*). A phase not listed here comes from a
// newer platform and is shown as sent.
const (
	// ServiceDisconnectionDraining: the environment's deploys keep its
	// connection Secret in place while pods started before the disconnection
	// still read it, or a deployed workload still refers to it.
	ServiceDisconnectionDraining = "draining"
	// ServiceDisconnectionRemoving: the connection Secret is being removed;
	// the platform waits to see it gone.
	ServiceDisconnectionRemoving = "removing"
	// ServiceDisconnectionRevoking: the environment's network access is being
	// removed where the review said it would be.
	ServiceDisconnectionRevoking = "revoking"
)

// The values ServiceInstanceDisconnections.State takes.
const (
	// ServiceDisconnectionsKnown: Items are every disconnection in progress,
	// and none when Items is empty.
	ServiceDisconnectionsKnown = "known"
	// ServiceDisconnectionsUnknown: they could not be read; Items is empty
	// and says nothing about whether one is in progress.
	ServiceDisconnectionsUnknown = "unknown"
)

// ServiceDisconnectionRequest is what a person states when preparing a
// disconnection. The platform requires every field:
// ConnectionNamesInstanceID is sent as null when no service is chosen.
// ConnectionNamesInstanceID and ConnectionNamesNone are set only when a
// review asks who gives the conventional names (DATABASE_URL, ...) from now
// on; the platform refuses a choice where none is asked.
type ServiceDisconnectionRequest struct {
	ConsumerID                string  `json:"consumer_id"`
	ExpectedGeneration        int64   `json:"expected_generation"`
	Timing                    string  `json:"timing"`
	KeepNetworkAccess         bool    `json:"keep_network_access"`
	ConnectionNamesInstanceID *string `json:"connection_names_instance_id"`
	ConnectionNamesNone       bool    `json:"connection_names_none"`
}

// ServiceDisconnectionConsumer is the application environment a
// disconnection takes off the service.
type ServiceDisconnectionConsumer struct {
	ID            string `json:"id" yaml:"id"`
	ApplicationID string `json:"application_id" yaml:"application_id"`
	ClusterID     string `json:"cluster_id" yaml:"cluster_id"`
	Namespace     string `json:"namespace" yaml:"namespace"`
}

// ServiceDisconnectionCredential says what happens to the credential. Kind
// is "shared" today: every application of the service uses the same one, so
// a disconnection revokes nothing.
type ServiceDisconnectionCredential struct {
	Kind string `json:"kind" yaml:"kind"`
	// OtherConsumers counts the other application environments the service
	// serves.
	OtherConsumers  int    `json:"other_consumers" yaml:"other_consumers"`
	Statement       string `json:"statement" yaml:"statement"`
	CannotGuarantee string `json:"cannot_guarantee" yaml:"cannot_guarantee"`
}

// ServiceDisconnectionNamesChoice is one service that may give the
// application its conventional names from now on, or nobody (None).
// Installing marks a service still being set up: choosing it points the
// application at a new, empty service.
type ServiceDisconnectionNamesChoice struct {
	InstanceID  *string `json:"instance_id" yaml:"instance_id"`
	ServiceName string  `json:"service_name" yaml:"service_name"`
	None        bool    `json:"none" yaml:"none"`
	Installing  bool    `json:"installing" yaml:"installing"`
}

// ServiceDisconnectionNames is what the application stops receiving and, when
// this service gives the protocol's conventional names (DATABASE_URL, ...),
// who gives them next. ChoiceRequired means another service of the protocol
// is connected: the review must be prepared again with a choice before it
// can be confirmed.
type ServiceDisconnectionNames struct {
	Variables           []string                          `json:"variables" yaml:"variables"`
	Conventional        []string                          `json:"conventional" yaml:"conventional"`
	CarriesConventional bool                              `json:"carries_conventional" yaml:"carries_conventional"`
	ChoiceRequired      bool                              `json:"choice_required" yaml:"choice_required"`
	Choices             []ServiceDisconnectionNamesChoice `json:"choices" yaml:"choices"`
	Choice              *ServiceDisconnectionNamesChoice  `json:"choice" yaml:"choice"`
	Statement           string                            `json:"statement" yaml:"statement"`
}

// ServiceDisconnectionSecret is the connection Secret in the environment's
// namespace. Delivered: the environment's deploys rendered it, so it is kept
// in place until no pod reads it, then removed.
type ServiceDisconnectionSecret struct {
	SecretName string `json:"secret_name" yaml:"secret_name"`
	Delivered  bool   `json:"delivered" yaml:"delivered"`
	Statement  string `json:"statement" yaml:"statement"`
}

// ServiceDisconnectionWorkloads are the deployed workloads that read the
// connection Secret: through the reference Ankra attached (the next deploy
// removes it and rolls them) or through one written by hand (a person
// changes them). State is known, not_stored or not_deployed.
type ServiceDisconnectionWorkloads struct {
	State           string   `json:"state" yaml:"state"`
	AttachedByAnkra []string `json:"attached_by_ankra" yaml:"attached_by_ankra"`
	WrittenByHand   []string `json:"written_by_hand" yaml:"written_by_hand"`
	Statement       string   `json:"statement" yaml:"statement"`
}

// ServiceDisconnectionNetwork says whether the environment's namespace leaves
// the service's client port. Narrows is what the review found; Reason is the
// platform's word for why (narrows, keep_requested, open_by_design, ...).
// Port is 0 when not known.
type ServiceDisconnectionNetwork struct {
	Namespace         string `json:"namespace" yaml:"namespace"`
	Narrows           bool   `json:"narrows" yaml:"narrows"`
	KeepNetworkAccess bool   `json:"keep_network_access" yaml:"keep_network_access"`
	Reason            string `json:"reason" yaml:"reason"`
	Port              int    `json:"port" yaml:"port"`
	Statement         string `json:"statement" yaml:"statement"`
}

// ServiceDisconnectionTiming says when the disconnection takes effect.
// DeployNowOffered: deploying the environment now finishes it sooner.
// NowOffered stays false while no engine revokes per application.
type ServiceDisconnectionTiming struct {
	Mode             string `json:"mode" yaml:"mode"`
	DeployNowOffered bool   `json:"deploy_now_offered" yaml:"deploy_now_offered"`
	NowOffered       bool   `json:"now_offered" yaml:"now_offered"`
	Statement        string `json:"statement" yaml:"statement"`
}

// ServiceDisconnectionPlan is the reviewed statement of what a disconnection
// removes, keeps and cannot do. Every field is resolved by the platform, and
// the digest covers all of it.
type ServiceDisconnectionPlan struct {
	SchemaVersion  int    `json:"schema_version" yaml:"schema_version"`
	InstanceID     string `json:"instance_id" yaml:"instance_id"`
	OrganisationID string `json:"organisation_id" yaml:"organisation_id"`
	ActorID        string `json:"actor_id" yaml:"actor_id"`
	ServiceName    string `json:"service_name" yaml:"service_name"`
	ClusterID      string `json:"cluster_id" yaml:"cluster_id"`
	Generation     int64  `json:"generation" yaml:"generation"`
	// Engine is the engine's label (PostgreSQL, Valkey, ...), empty for a
	// package Ankra knows only by its declared connection.
	Engine   string                       `json:"engine" yaml:"engine"`
	Protocol string                       `json:"protocol" yaml:"protocol"`
	Consumer ServiceDisconnectionConsumer `json:"consumer" yaml:"consumer"`
	// OnServiceCluster is false for an environment on another cluster than
	// the service: Ankra never delivered it a connection, so only the record
	// is removed.
	OnServiceCluster bool                           `json:"on_service_cluster" yaml:"on_service_cluster"`
	Credential       ServiceDisconnectionCredential `json:"credential" yaml:"credential"`
	ConnectionNames  ServiceDisconnectionNames      `json:"connection_names" yaml:"connection_names"`
	ConnectionSecret ServiceDisconnectionSecret     `json:"connection_secret" yaml:"connection_secret"`
	Workloads        ServiceDisconnectionWorkloads  `json:"workloads" yaml:"workloads"`
	Network          ServiceDisconnectionNetwork    `json:"network" yaml:"network"`
	Data             string                         `json:"data" yaml:"data"`
	Timing           ServiceDisconnectionTiming     `json:"timing" yaml:"timing"`
	// Kept are what stays after the disconnection has finished.
	Kept []string `json:"kept" yaml:"kept"`
	// Rotation is what a person does when the application must no longer be
	// able to authenticate.
	Rotation  string `json:"rotation" yaml:"rotation"`
	ExpiresAt string `json:"expires_at" yaml:"expires_at"`
}

// ServiceDisconnectionOutcome is what a settled disconnection observed, and
// only what it observed. ConnectionSecret is removed, not_delivered,
// not_confirmed or record_only; Network is narrowed, left (NetworkReason says
// why) or not_confirmed.
type ServiceDisconnectionOutcome struct {
	ConnectionSecret  string   `json:"connection_secret" yaml:"connection_secret"`
	Network           string   `json:"network" yaml:"network"`
	NetworkReason     string   `json:"network_reason,omitempty" yaml:"network_reason,omitempty"`
	RemovedNamespaces []string `json:"removed_namespaces" yaml:"removed_namespaces"`
	ObservedAt        string   `json:"observed_at" yaml:"observed_at"`
}

// ServiceDisconnectionReaders are the pods in the environment's namespace
// that read the connection Secret, read when the disconnection was served.
type ServiceDisconnectionReaders struct {
	Count int      `json:"count" yaml:"count"`
	Names []string `json:"names" yaml:"names"`
}

// ServiceDisconnection is one disconnection as the person who prepared it
// sees it: the plan and digest and, once confirmed, its progress. State
// pending and expired describe an unconfirmed review's window, in_progress
// and settled a confirmed disconnection. RunningPods is not part of the plan
// or its digest; it is null once settled, for an environment on another
// cluster, and when the pods could not be read.
type ServiceDisconnection struct {
	ID          string                       `json:"id" yaml:"id"`
	InstanceID  string                       `json:"instance_id" yaml:"instance_id"`
	Plan        ServiceDisconnectionPlan     `json:"plan" yaml:"plan"`
	Digest      string                       `json:"digest" yaml:"digest"`
	State       string                       `json:"state" yaml:"state"`
	CreatedAt   string                       `json:"created_at" yaml:"created_at"`
	ExpiresAt   string                       `json:"expires_at" yaml:"expires_at"`
	ConfirmedAt *string                      `json:"confirmed_at" yaml:"confirmed_at"`
	Phase       *string                      `json:"phase" yaml:"phase"`
	SettledAt   *string                      `json:"settled_at" yaml:"settled_at"`
	Outcome     *ServiceDisconnectionOutcome `json:"outcome" yaml:"outcome"`
	RunningPods *ServiceDisconnectionReaders `json:"running_pods" yaml:"running_pods"`
}

// ServiceInstanceDisconnections are the disconnections of an instance that
// were confirmed and have not finished, as the single-instance read answers
// them (cluster ankra-h45ken); a listing carries none.
type ServiceInstanceDisconnections struct {
	State string                         `json:"state" yaml:"state"`
	Items []ServiceInstanceDisconnection `json:"items" yaml:"items"`
}

// ServiceInstanceDisconnection is one confirmed disconnection that has not
// finished: recorded progress, not an observation of the cluster. The
// consumer it removed is no longer among the instance's consumers.
// KeepsConnectionSecret is set while every deploy of the environment renders
// the connection Secret again, unchanged and referenced by no workload, so
// pods started before the disconnection keep finding it.
type ServiceInstanceDisconnection struct {
	ID                    string `json:"id" yaml:"id"`
	ConsumerID            string `json:"consumer_id" yaml:"consumer_id"`
	ApplicationID         string `json:"application_id" yaml:"application_id"`
	ClusterID             string `json:"cluster_id" yaml:"cluster_id"`
	Namespace             string `json:"namespace" yaml:"namespace"`
	Phase                 string `json:"phase" yaml:"phase"`
	RequestedAt           string `json:"requested_at" yaml:"requested_at"`
	PhaseChangedAt        string `json:"phase_changed_at" yaml:"phase_changed_at"`
	KeepsConnectionSecret bool   `json:"keeps_connection_secret" yaml:"keeps_connection_secret"`
}

// PrepareServiceDisconnection stores a disconnection review of one consumer
// of the instance: what disconnecting it removes, keeps and cannot do, with
// a digest and a ten-minute expiry. Nothing changes. It is not idempotent:
// every call stores another review against the caller's open quota (10 per
// person, 50 per organisation). An empty Timing is sent as
// ServiceDisconnectAfterNextDeploy.
func (c *Client) PrepareServiceDisconnection(ctx context.Context, instanceID string, request ServiceDisconnectionRequest) (*ServiceDisconnection, error) {
	if strings.TrimSpace(request.ConsumerID) == "" {
		return nil, errors.New("disconnecting an application from a service needs the consumer to disconnect")
	}
	if request.ExpectedGeneration < 1 {
		return nil, errors.New("disconnecting an application from a service needs the service generation that was read (1 or higher)")
	}
	if request.ConnectionNamesNone && request.ConnectionNamesInstanceID != nil {
		return nil, errors.New("choose one service to give the conventional names, or none, not both")
	}
	if request.Timing == "" {
		request.Timing = ServiceDisconnectAfterNextDeploy
	}
	var disconnection ServiceDisconnection
	address := c.servicesURL("/api/v1/org/service-admission/instances/%s/disconnections", nil, instanceID)
	if requestError := c.sendJSONContext(ctx, http.MethodPost, address, request, &disconnection); requestError != nil {
		return nil, requestError
	}
	return &disconnection, nil
}

// ConfirmServiceDisconnection confirms a disconnection review by the digest
// the person reviewed. This is the call that takes the application
// environment off the service; a retry returns the same record.
func (c *Client) ConfirmServiceDisconnection(ctx context.Context, instanceID string, disconnectionID string, digest string) (*ServiceDisconnection, error) {
	if digest == "" {
		return nil, errors.New("confirming a service disconnection needs the digest that was reviewed")
	}
	var disconnection ServiceDisconnection
	address := c.servicesURL("/api/v1/org/service-admission/instances/%s/disconnections/%s/confirm", nil, instanceID, disconnectionID)
	if requestError := c.sendJSONContext(ctx, http.MethodPost, address, serviceConfirmRequest{Digest: digest}, &disconnection); requestError != nil {
		return nil, requestError
	}
	return &disconnection, nil
}
