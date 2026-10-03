package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Managed services ("Services"): reviewed, health-checked platform services
// (PostgreSQL, Valkey, OpenSearch, ...) that run on a customer's cluster.
//
// The platform splits them across two route families, both bearer twins of
// the portal's routes:
//
//   - /api/v1/org/service-packages: the catalogue of immutable package
//     versions an organisation may set up.
//   - /api/v1/org/service-admission: cluster placement policies, consumer
//     bindings, the actor-bound setup reviews, the durable instance inventory
//     and the reviewed retirements that remove an instance.
//
// Setting a service up and retiring one are both two steps on the platform:
// a prepare that stores a plan with a digest and changes nothing, and a
// confirm that carries that digest and is the only effect. The client
// mirrors that split one method per route; it never confirms on its own.
//
// Nothing here carries credential values. An instance's connection names the
// Secret holding the generated credentials and its key names only.

// ServicePageOptions pages a keyset listing: Limit is 1-50 (0 leaves the
// server default of 25), After the previous page's next_cursor.
type ServicePageOptions struct {
	Limit int
	After string
}

func (options ServicePageOptions) query() url.Values {
	values := url.Values{}
	if options.Limit > 0 {
		values.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.After != "" {
		values.Set("after", options.After)
	}
	return values
}

// ServicePackageSummary is one immutable package version in the catalogue.
// Being listed is not being deployable: setup re-checks access and the
// pinned profile when a review is prepared.
type ServicePackageSummary struct {
	ID          string `json:"id" yaml:"id"`
	PublisherID string `json:"publisher_id" yaml:"publisher_id"`
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	Capability  string `json:"capability" yaml:"capability"`
	Digest      string `json:"digest" yaml:"digest"`
	// FirstParty marks a version Ankra publishes itself, readable by every
	// organisation the rollout admits without a share.
	FirstParty bool   `json:"first_party" yaml:"first_party"`
	CreatedAt  string `json:"created_at" yaml:"created_at"`
}

// ServicePackagePage is one page of the catalogue.
type ServicePackagePage struct {
	Items      []ServicePackageSummary `json:"items" yaml:"items"`
	NextCursor *string                 `json:"next_cursor" yaml:"next_cursor"`
}

// ServiceProfileReference pins the stack profile version a mode deploys.
type ServiceProfileReference struct {
	ID        string `json:"id" yaml:"id"`
	VersionID string `json:"version_id" yaml:"version_id"`
	Digest    string `json:"digest" yaml:"digest"`
}

// ServicePackageParameter is a bounded integer setting a review may choose.
type ServicePackageParameter struct {
	Name    string `json:"name" yaml:"name"`
	Minimum int64  `json:"minimum" yaml:"minimum"`
	Maximum int64  `json:"maximum" yaml:"maximum"`
	Default int64  `json:"default" yaml:"default"`
	Unit    string `json:"unit" yaml:"unit"`
}

// ServicePackageSecret is a declared secret input: a review supplies a
// credential grant id for it, never a value.
type ServicePackageSecret struct {
	Name  string   `json:"name" yaml:"name"`
	Modes []string `json:"modes" yaml:"modes"`
}

// ServicePackageOutput is what the service publishes to its consumers.
type ServicePackageOutput struct {
	Name string `json:"name" yaml:"name"`
	Kind string `json:"kind" yaml:"kind"`
}

// ServicePackageLifecycle names the registered adapters a package uses.
type ServicePackageLifecycle struct {
	Verify   string `json:"verify" yaml:"verify"`
	Upgrade  string `json:"upgrade" yaml:"upgrade"`
	Recovery string `json:"recovery" yaml:"recovery"`
	Delete   string `json:"delete" yaml:"delete"`
}

// ServicePackageContract is a package version's immutable contract. Profiles
// is keyed by operating mode (hosted, customer, existing); a mode missing
// from it is one the package does not offer.
type ServicePackageContract struct {
	SchemaVersion int                                `json:"schema_version" yaml:"schema_version"`
	Name          string                             `json:"name" yaml:"name"`
	Version       string                             `json:"version" yaml:"version"`
	PublisherID   string                             `json:"publisher_id" yaml:"publisher_id"`
	Capability    string                             `json:"capability" yaml:"capability"`
	Profiles      map[string]ServiceProfileReference `json:"profiles" yaml:"profiles"`
	Parameters    []ServicePackageParameter          `json:"parameters" yaml:"parameters"`
	Secrets       []ServicePackageSecret             `json:"secrets" yaml:"secrets"`
	Outputs       []ServicePackageOutput             `json:"outputs" yaml:"outputs"`
	Lifecycle     ServicePackageLifecycle            `json:"lifecycle" yaml:"lifecycle"`
}

// ServicePackageDetail is one package version with its contract.
type ServicePackageDetail struct {
	ServicePackageSummary `yaml:",inline"`
	Contract              ServicePackageContract `json:"contract" yaml:"contract"`
}

// ServiceClusterPolicy is the organisation's declaration of where a cluster
// is and which data boundary it serves. It is a declaration, not verified
// location evidence, and setup refuses a cluster without one.
type ServiceClusterPolicy struct {
	ClusterID    string `json:"cluster_id" yaml:"cluster_id"`
	Region       string `json:"region" yaml:"region"`
	DataBoundary string `json:"data_boundary" yaml:"data_boundary"`
	LocalOnly    bool   `json:"local_only" yaml:"local_only"`
	Revision     int64  `json:"revision" yaml:"revision"`
	Source       string `json:"source" yaml:"source"`
	UpdatedAt    string `json:"updated_at" yaml:"updated_at"`
}

// ServiceClusterPolicyRequest declares or replaces a cluster's policy.
// ExpectedRevision 0 creates; replacing one names the revision reviewed.
type ServiceClusterPolicyRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Region           string `json:"region"`
	DataBoundary     string `json:"data_boundary"`
	LocalOnly        bool   `json:"local_only"`
}

// ServiceConsumerBinding is a stored binding of an application namespace as
// a service consumer, as last saved.
type ServiceConsumerBinding struct {
	ID            string `json:"id" yaml:"id"`
	ApplicationID string `json:"application_id" yaml:"application_id"`
	ClusterID     string `json:"cluster_id" yaml:"cluster_id"`
	Namespace     string `json:"namespace" yaml:"namespace"`
	AllowPlanned  bool   `json:"allow_planned" yaml:"allow_planned"`
	LocalOnly     bool   `json:"local_only" yaml:"local_only"`
	Revision      int64  `json:"revision" yaml:"revision"`
	UpdatedAt     string `json:"updated_at" yaml:"updated_at"`
}

// ServiceConsumerPage is one page of an application's consumer bindings. A
// page may hold fewer rows than its limit, or none, while NextCursor is set.
type ServiceConsumerPage struct {
	Items      []ServiceConsumerBinding `json:"items" yaml:"items"`
	NextCursor *string                  `json:"next_cursor" yaml:"next_cursor"`
}

// ServiceConsumerRequest binds an application namespace as a consumer.
// ExpectedRevision 0 creates; updating one names its current revision.
type ServiceConsumerRequest struct {
	ApplicationID    string `json:"application_id"`
	ClusterID        string `json:"cluster_id"`
	Namespace        string `json:"namespace"`
	AllowPlanned     bool   `json:"allow_planned"`
	LocalOnly        bool   `json:"local_only"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// ServiceConsumer is a binding resolved against the cluster policy and the
// application's installation. InstallationID is null for a planned binding.
type ServiceConsumer struct {
	ID              string  `json:"id" yaml:"id"`
	ApplicationID   string  `json:"application_id" yaml:"application_id"`
	OrganisationID  string  `json:"organisation_id" yaml:"organisation_id"`
	ClusterID       string  `json:"cluster_id" yaml:"cluster_id"`
	Namespace       string  `json:"namespace" yaml:"namespace"`
	Region          string  `json:"region" yaml:"region"`
	DataBoundary    string  `json:"data_boundary" yaml:"data_boundary"`
	PolicyRevision  int64   `json:"policy_revision" yaml:"policy_revision"`
	BindingRevision int64   `json:"binding_revision" yaml:"binding_revision"`
	BindingKind     string  `json:"binding_kind" yaml:"binding_kind"`
	InstallationID  *string `json:"installation_id" yaml:"installation_id"`
	LocalOnly       bool    `json:"local_only" yaml:"local_only"`
}

// ServiceReviewRequest is what a setup review is prepared from. Every field
// is required by the platform: Parameters and SecretReferences are sent as
// empty objects, never null.
type ServiceReviewRequest struct {
	PackageVersionID string            `json:"package_version_id"`
	Name             string            `json:"name"`
	Mode             string            `json:"mode"`
	Region           string            `json:"region"`
	DataBoundary     string            `json:"data_boundary"`
	ClusterID        string            `json:"cluster_id"`
	ConsumerIDs      []string          `json:"consumer_ids"`
	Parameters       map[string]int64  `json:"parameters"`
	SecretReferences map[string]string `json:"secret_references"`
}

// ServiceReview is a stored setup review: the server-resolved plan exactly
// as its digest was taken, and once confirmed the execution receipt. The
// plan is kept as the platform sent it, so the document shown is the one the
// digest covers; it never carries credential values.
type ServiceReview struct {
	ID               string         `json:"id" yaml:"id"`
	PackageVersionID string         `json:"package_version_id" yaml:"package_version_id"`
	Plan             map[string]any `json:"plan" yaml:"plan"`
	Digest           string         `json:"digest" yaml:"digest"`
	CreatedAt        string         `json:"created_at" yaml:"created_at"`
	ExecutionID      *string        `json:"execution_id" yaml:"execution_id"`
	ConfirmedAt      *string        `json:"confirmed_at" yaml:"confirmed_at"`
	// Capacity is whether the service cluster can admit the service, as the
	// prepare or confirm assessed it. Nil on a platform that predates the
	// assessment, and on a confirm that answered an existing receipt.
	Capacity *ServiceCapacity `json:"capacity" yaml:"capacity"`
}

// ServiceCapacity is the platform's capacity verdict for a setup: "fits",
// "short" (the review is refused, with this reason), "unknown" (the estimate
// could not be completed, so the install may still hit the cluster's quota)
// or "unchecked" (the platform sets no quota for this cluster). Only
// playgrounds are checked. The figures are null when the decision did not
// reach them.
type ServiceCapacity struct {
	State     string                    `json:"state" yaml:"state"`
	Reason    string                    `json:"reason" yaml:"reason"`
	PlanID    string                    `json:"plan_id" yaml:"plan_id"`
	Quota     *ServiceCapacityResources `json:"quota" yaml:"quota"`
	Committed *ServiceCapacityResources `json:"committed" yaml:"committed"`
	Transient *ServiceCapacityResources `json:"transient" yaml:"transient"`
	Required  *ServiceCapacityResources `json:"required" yaml:"required"`
}

// ServiceCapacityResources are the CPU and memory figures a capacity verdict
// was made from.
type ServiceCapacityResources struct {
	LimitsCPUMillicores   int64 `json:"limits_cpu_millicores" yaml:"limits_cpu_millicores"`
	LimitsMemoryMiB       int64 `json:"limits_memory_mib" yaml:"limits_memory_mib"`
	RequestsCPUMillicores int64 `json:"requests_cpu_millicores" yaml:"requests_cpu_millicores"`
	RequestsMemoryMiB     int64 `json:"requests_memory_mib" yaml:"requests_memory_mib"`
}

// ServiceDestination is a requested consumer destination: reviewed intent,
// not proof of a live connection.
type ServiceDestination struct {
	ApplicationID string `json:"application_id" yaml:"application_id"`
	ClusterID     string `json:"cluster_id" yaml:"cluster_id"`
	Namespace     string `json:"namespace" yaml:"namespace"`
}

// ServiceReviewSummary is the history projection of a review: the
// selection, destinations, digest and state, never secret references.
// State pending only means the ten-minute window has not passed.
type ServiceReviewSummary struct {
	ID               string               `json:"id" yaml:"id"`
	PackageVersionID string               `json:"package_version_id" yaml:"package_version_id"`
	ClusterID        string               `json:"cluster_id" yaml:"cluster_id"`
	Name             string               `json:"name" yaml:"name"`
	Region           string               `json:"region" yaml:"region"`
	DataBoundary     string               `json:"data_boundary" yaml:"data_boundary"`
	Digest           string               `json:"digest" yaml:"digest"`
	Mode             string               `json:"mode" yaml:"mode"`
	Parameters       map[string]int64     `json:"parameters" yaml:"parameters"`
	SecretInputs     []string             `json:"secret_inputs" yaml:"secret_inputs"`
	Destinations     []ServiceDestination `json:"destinations" yaml:"destinations"`
	State            string               `json:"state" yaml:"state"`
	CreatedAt        string               `json:"created_at" yaml:"created_at"`
	ExpiresAt        string               `json:"expires_at" yaml:"expires_at"`
	ExecutionID      *string              `json:"execution_id" yaml:"execution_id"`
	ConfirmedAt      *string              `json:"confirmed_at" yaml:"confirmed_at"`
}

// ServiceReviewPage is one page of the caller's own reviews, newest first. A
// short or empty page may still carry NextCursor; follow it until null.
type ServiceReviewPage struct {
	Items      []ServiceReviewSummary `json:"items" yaml:"items"`
	NextCursor *string                `json:"next_cursor" yaml:"next_cursor"`
}

// ServiceInstanceConsumer is one of an instance's reviewed consumers: the
// set a retirement names in disconnect_consumer_ids.
type ServiceInstanceConsumer struct {
	ID            string `json:"id" yaml:"id"`
	ApplicationID string `json:"application_id" yaml:"application_id"`
	ClusterID     string `json:"cluster_id" yaml:"cluster_id"`
	Namespace     string `json:"namespace" yaml:"namespace"`
}

// ServiceReadinessCheck is the canary evidence behind an instance's
// readiness, folded against the database clock at read time.
type ServiceReadinessCheck struct {
	ObservedAt   string  `json:"observed_at" yaml:"observed_at"`
	ExpiresAt    string  `json:"expires_at" yaml:"expires_at"`
	Reason       string  `json:"reason" yaml:"reason"`
	LastPassedAt *string `json:"last_passed_at" yaml:"last_passed_at"`
}

// ServiceEndpoint is one address the service is reached at in its cluster.
type ServiceEndpoint struct {
	Name    string `json:"name" yaml:"name"`
	Address string `json:"address" yaml:"address"`
}

// ServiceSecretReference names the Secret in the instance namespace that
// holds the generated credentials, and its keys. The values stay in the
// cluster: the platform never returns them, and this type has nowhere to
// hold one.
type ServiceSecretReference struct {
	Name string   `json:"name" yaml:"name"`
	Keys []string `json:"keys" yaml:"keys"`
}

// ServiceConnection is where a first-party engine instance is reached.
type ServiceConnection struct {
	Namespace string                 `json:"namespace" yaml:"namespace"`
	Endpoints []ServiceEndpoint      `json:"endpoints" yaml:"endpoints"`
	Secret    ServiceSecretReference `json:"secret" yaml:"secret"`
}

// ServiceHealth is what the platform last observed of the instance from the
// service cluster's synced state: serving, progressing, degraded or unknown.
type ServiceHealth struct {
	State      string `json:"state" yaml:"state"`
	ObservedAt string `json:"observed_at" yaml:"observed_at"`
	ExpiresAt  string `json:"expires_at" yaml:"expires_at"`
	Reason     string `json:"reason" yaml:"reason"`
}

// ServiceInstanceRetirement is the latest confirmed retirement's progress.
type ServiceInstanceRetirement struct {
	ID          string  `json:"id" yaml:"id"`
	State       string  `json:"state" yaml:"state"`
	Phase       *string `json:"phase" yaml:"phase"`
	ExecutionID string  `json:"execution_id" yaml:"execution_id"`
	RequestedAt string  `json:"requested_at" yaml:"requested_at"`
	SettledAt   *string `json:"settled_at" yaml:"settled_at"`
	Outcome     *string `json:"outcome" yaml:"outcome"`
	Reason      *string `json:"reason" yaml:"reason"`
}

// ServiceInstance is one service in the organisation's durable inventory.
// DeploymentState is recorded operation progress; Health and Readiness are
// the observed state, each with its own evidence.
type ServiceInstance struct {
	ID                    string                     `json:"id" yaml:"id"`
	PackageVersionID      string                     `json:"package_version_id" yaml:"package_version_id"`
	ClusterID             string                     `json:"cluster_id" yaml:"cluster_id"`
	AdmissionOperationID  string                     `json:"admission_operation_id" yaml:"admission_operation_id"`
	Name                  string                     `json:"name" yaml:"name"`
	Region                string                     `json:"region" yaml:"region"`
	DataBoundary          string                     `json:"data_boundary" yaml:"data_boundary"`
	Generation            int64                      `json:"generation" yaml:"generation"`
	Mode                  string                     `json:"mode" yaml:"mode"`
	RequestedDestinations []ServiceDestination       `json:"requested_destinations" yaml:"requested_destinations"`
	Consumers             []ServiceInstanceConsumer  `json:"consumers" yaml:"consumers"`
	DeploymentOperationID *string                    `json:"deployment_operation_id" yaml:"deployment_operation_id"`
	DeploymentState       string                     `json:"deployment_state" yaml:"deployment_state"`
	Readiness             string                     `json:"readiness" yaml:"readiness"`
	ReadinessCheck        *ServiceReadinessCheck     `json:"readiness_check" yaml:"readiness_check"`
	CreatedAt             string                     `json:"created_at" yaml:"created_at"`
	ReleasedAt            *string                    `json:"released_at" yaml:"released_at"`
	EvidenceUpdatedAt     *string                    `json:"evidence_updated_at" yaml:"evidence_updated_at"`
	Connection            *ServiceConnection         `json:"connection" yaml:"connection"`
	Health                *ServiceHealth             `json:"health" yaml:"health"`
	Retirement            *ServiceInstanceRetirement `json:"retirement" yaml:"retirement"`
}

// ServiceInstancePage is one page of the inventory, ascending by id. An empty
// page can carry NextCursor when hidden rows used the scan budget.
type ServiceInstancePage struct {
	Items      []ServiceInstance `json:"items" yaml:"items"`
	NextCursor *string           `json:"next_cursor" yaml:"next_cursor"`
}

// ServiceRetirementRequest prepares a retirement review. Every field is
// required: DisconnectConsumerIDs is exactly the instance's consumers (sent
// as an empty list, never null), and AcknowledgeDataLoss must be true.
type ServiceRetirementRequest struct {
	ExpectedGeneration    int64    `json:"expected_generation"`
	DisconnectConsumerIDs []string `json:"disconnect_consumer_ids"`
	AcknowledgeDataLoss   bool     `json:"acknowledge_data_loss"`
}

// ServiceNamespaceObjectOwner is the controller that created an object when
// that owner is a kind the platform's index does not hold.
type ServiceNamespaceObjectOwner struct {
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
}

// ServiceNamespaceObject is an object in the service namespace that the
// service's own releases did not create; it is deleted with the namespace.
type ServiceNamespaceObject struct {
	ResourceType string                       `json:"resource_type" yaml:"resource_type"`
	Kind         *string                      `json:"kind" yaml:"kind"`
	Name         string                       `json:"name" yaml:"name"`
	CreatedAt    *string                      `json:"created_at" yaml:"created_at"`
	Owner        *ServiceNamespaceObjectOwner `json:"owner" yaml:"owner"`
}

// ServiceNamespaceVolume is a volume claim in the service namespace, with
// the reclaim policy that decides what happens to its data.
type ServiceNamespaceVolume struct {
	ClaimName        string  `json:"claim_name" yaml:"claim_name"`
	ClaimUID         *string `json:"claim_uid" yaml:"claim_uid"`
	CreatedByService bool    `json:"created_by_service" yaml:"created_by_service"`
	StorageClass     *string `json:"storage_class" yaml:"storage_class"`
	VolumeName       *string `json:"volume_name" yaml:"volume_name"`
	ReclaimPolicy    *string `json:"reclaim_policy" yaml:"reclaim_policy"`
}

// ServiceNamespaceContents is what the platform's resource index held in the
// namespace when the plan was resolved. Items and Volumes are nil when the
// index was never synced: an absent list is "not known", never "nothing".
type ServiceNamespaceContents struct {
	ObservedAt       *string                  `json:"observed_at" yaml:"observed_at"`
	Complete         bool                     `json:"complete" yaml:"complete"`
	IncompleteReason *string                  `json:"incomplete_reason" yaml:"incomplete_reason"`
	Items            []ServiceNamespaceObject `json:"items" yaml:"items"`
	Volumes          []ServiceNamespaceVolume `json:"volumes" yaml:"volumes"`
}

// ServiceRetirementStackMember is one stack member the retirement removes.
type ServiceRetirementStackMember struct {
	ID        string  `json:"id" yaml:"id"`
	Kind      string  `json:"kind" yaml:"kind"`
	Name      string  `json:"name" yaml:"name"`
	Namespace *string `json:"namespace" yaml:"namespace"`
}

// ServiceRetirementStack is the stack Services installed for the instance.
type ServiceRetirementStack struct {
	ResourceID   string                         `json:"resource_id" yaml:"resource_id"`
	Name         string                         `json:"name" yaml:"name"`
	State        string                         `json:"state" yaml:"state"`
	Members      []ServiceRetirementStackMember `json:"members" yaml:"members"`
	MemberDigest string                         `json:"member_digest" yaml:"member_digest"`
}

// ServiceSharedStack is a shared, cluster-wide stack the service needed (the
// CloudNativePG operator, for one). A retirement never removes it.
type ServiceSharedStack struct {
	StackName string `json:"stack_name" yaml:"stack_name"`
	Title     string `json:"title" yaml:"title"`
}

// ServiceRetirementData states what the retirement does to the data.
type ServiceRetirementData struct {
	NamespaceDeleted bool   `json:"namespace_deleted" yaml:"namespace_deleted"`
	VolumesDeleted   bool   `json:"volumes_deleted" yaml:"volumes_deleted"`
	SecretsDeleted   bool   `json:"secrets_deleted" yaml:"secrets_deleted"`
	ExportOffered    bool   `json:"export_offered" yaml:"export_offered"`
	RestorePoints    string `json:"restore_points" yaml:"restore_points"`
	Statement        string `json:"statement" yaml:"statement"`
}

// ServiceRetirementPlan is what retiring the instance removes and keeps,
// resolved by the platform; nothing in it is supplied by the client.
type ServiceRetirementPlan struct {
	SchemaVersion         int                       `json:"schema_version" yaml:"schema_version"`
	InstanceID            string                    `json:"instance_id" yaml:"instance_id"`
	OrganisationID        string                    `json:"organisation_id" yaml:"organisation_id"`
	ActorID               string                    `json:"actor_id" yaml:"actor_id"`
	Name                  string                    `json:"name" yaml:"name"`
	ClusterID             string                    `json:"cluster_id" yaml:"cluster_id"`
	Generation            int64                     `json:"generation" yaml:"generation"`
	Namespace             *string                   `json:"namespace" yaml:"namespace"`
	NamespaceContents     *ServiceNamespaceContents `json:"namespace_contents,omitempty" yaml:"namespace_contents,omitempty"`
	Stack                 *ServiceRetirementStack   `json:"stack" yaml:"stack"`
	NothingDeployed       bool                      `json:"nothing_deployed" yaml:"nothing_deployed"`
	OwnedDraftID          *string                   `json:"owned_draft_id" yaml:"owned_draft_id"`
	ConsumersToDisconnect []ServiceInstanceConsumer `json:"consumers_to_disconnect" yaml:"consumers_to_disconnect"`
	SharedKept            []ServiceSharedStack      `json:"shared_kept" yaml:"shared_kept"`
	Data                  ServiceRetirementData     `json:"data" yaml:"data"`
	ExpiresAt             string                    `json:"expires_at" yaml:"expires_at"`
}

// ServiceVolumeOutcome is what the retirement lane observed of one reviewed
// volume after the namespace was gone.
type ServiceVolumeOutcome struct {
	ClaimName     string  `json:"claim_name" yaml:"claim_name"`
	VolumeName    *string `json:"volume_name" yaml:"volume_name"`
	State         string  `json:"state" yaml:"state"`
	ReclaimPolicy *string `json:"reclaim_policy" yaml:"reclaim_policy"`
	Phase         *string `json:"phase" yaml:"phase"`
}

// ServiceVolumeDisposal is recorded once a retirement settles: RetainedVolumes
// still hold the service's data until someone removes them.
type ServiceVolumeDisposal struct {
	ObservedAt      string                 `json:"observed_at" yaml:"observed_at"`
	Volumes         []ServiceVolumeOutcome `json:"volumes" yaml:"volumes"`
	RetainedVolumes []string               `json:"retained_volumes" yaml:"retained_volumes"`
	Complete        bool                   `json:"complete" yaml:"complete"`
}

// ServiceRetirement is a retirement review and, once confirmed, its
// progress. State pending and expired describe an unconfirmed review's
// window; in_progress and settled a confirmed retirement.
type ServiceRetirement struct {
	ID             string                 `json:"id" yaml:"id"`
	InstanceID     string                 `json:"instance_id" yaml:"instance_id"`
	Plan           ServiceRetirementPlan  `json:"plan" yaml:"plan"`
	Digest         string                 `json:"digest" yaml:"digest"`
	State          string                 `json:"state" yaml:"state"`
	CreatedAt      string                 `json:"created_at" yaml:"created_at"`
	ExpiresAt      string                 `json:"expires_at" yaml:"expires_at"`
	ExecutionID    *string                `json:"execution_id" yaml:"execution_id"`
	ConfirmedAt    *string                `json:"confirmed_at" yaml:"confirmed_at"`
	Phase          *string                `json:"phase" yaml:"phase"`
	SettledAt      *string                `json:"settled_at" yaml:"settled_at"`
	Outcome        *string                `json:"outcome" yaml:"outcome"`
	Reason         *string                `json:"reason" yaml:"reason"`
	VolumeDisposal *ServiceVolumeDisposal `json:"volume_disposal" yaml:"volume_disposal"`
}

// ServiceRetirementPage is one page of the caller's own retirements of an
// instance, newest first. NextCursor is null exactly when no more match.
type ServiceRetirementPage struct {
	Items      []ServiceRetirement `json:"items" yaml:"items"`
	NextCursor *string             `json:"next_cursor" yaml:"next_cursor"`
}

// ServiceRetirementListOptions filters and pages the retirement listing.
// State is one of pending, expired, in_progress or settled; empty lists all.
type ServiceRetirementListOptions struct {
	ServicePageOptions
	State string
}

// serviceConfirmRequest carries the digest the person reviewed; the
// platform trusts nothing else in a confirmation.
type serviceConfirmRequest struct {
	Digest string `json:"digest"`
}

// servicesURL builds a managed-services route from its literal path, so
// every route stays one string the route census can check (see
// cluster_routes_test.go). Each %s is a path segment and is escaped.
func (c *Client) servicesURL(pathFormat string, query url.Values, segments ...string) string {
	escaped := make([]any, len(segments))
	for index, segment := range segments {
		escaped[index] = url.PathEscape(segment)
	}
	address := c.BaseURL + fmt.Sprintf(pathFormat, escaped...)
	if encoded := query.Encode(); encoded != "" {
		address += "?" + encoded
	}
	return address
}

// ListServicePackages reads one page of the service catalogue.
func (c *Client) ListServicePackages(ctx context.Context, options ServicePageOptions) (*ServicePackagePage, error) {
	var page ServicePackagePage
	address := c.servicesURL("/api/v1/org/service-packages", options.query())
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &page); requestError != nil {
		return nil, requestError
	}
	return &page, nil
}

// GetServicePackage reads one package version and its contract.
func (c *Client) GetServicePackage(ctx context.Context, versionID string) (*ServicePackageDetail, error) {
	var detail ServicePackageDetail
	address := c.servicesURL("/api/v1/org/service-packages/%s", nil, versionID)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &detail); requestError != nil {
		return nil, requestError
	}
	return &detail, nil
}

// GetServiceClusterPolicy reads a cluster's service placement policy.
func (c *Client) GetServiceClusterPolicy(ctx context.Context, clusterID string) (*ServiceClusterPolicy, error) {
	var policy ServiceClusterPolicy
	address := c.servicesURL("/api/v1/org/service-admission/cluster-policies/%s", nil, clusterID)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// SetServiceClusterPolicy declares or replaces a cluster's placement policy.
func (c *Client) SetServiceClusterPolicy(ctx context.Context, clusterID string, request ServiceClusterPolicyRequest) (*ServiceClusterPolicy, error) {
	var policy ServiceClusterPolicy
	address := c.servicesURL("/api/v1/org/service-admission/cluster-policies/%s", nil, clusterID)
	if requestError := c.sendJSONContext(ctx, http.MethodPut, address, request, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// ListServiceConsumers reads one page of an application's consumer bindings.
func (c *Client) ListServiceConsumers(ctx context.Context, applicationID string, options ServicePageOptions) (*ServiceConsumerPage, error) {
	query := options.query()
	query.Set("application_id", applicationID)
	var page ServiceConsumerPage
	address := c.servicesURL("/api/v1/org/service-admission/consumers", query)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &page); requestError != nil {
		return nil, requestError
	}
	return &page, nil
}

// GetServiceConsumer reads one consumer binding, resolved.
func (c *Client) GetServiceConsumer(ctx context.Context, consumerID string) (*ServiceConsumer, error) {
	var consumer ServiceConsumer
	address := c.servicesURL("/api/v1/org/service-admission/consumers/%s", nil, consumerID)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &consumer); requestError != nil {
		return nil, requestError
	}
	return &consumer, nil
}

// BindServiceConsumer binds an application namespace as a service consumer.
func (c *Client) BindServiceConsumer(ctx context.Context, request ServiceConsumerRequest) (*ServiceConsumer, error) {
	var consumer ServiceConsumer
	address := c.servicesURL("/api/v1/org/service-admission/consumers", nil)
	if requestError := c.sendJSONContext(ctx, http.MethodPost, address, request, &consumer); requestError != nil {
		return nil, requestError
	}
	return &consumer, nil
}

// UnbindServiceConsumer removes a consumer binding at the revision the
// caller last read. The platform keeps (409) a binding a service that has
// not been released was planned with, and refuses a stale revision (409).
// A binding whose application or cluster has since been removed can still
// be removed, although it may no longer be readable by id.
func (c *Client) UnbindServiceConsumer(ctx context.Context, consumerID string, expectedRevision int64) error {
	if expectedRevision < 1 {
		return errors.New("removing a consumer binding needs its current revision (1 or higher)")
	}
	query := url.Values{}
	query.Set("expected_revision", strconv.FormatInt(expectedRevision, 10))
	address := c.servicesURL("/api/v1/org/service-admission/consumers/%s", query, consumerID)
	return c.sendJSONContext(ctx, http.MethodDelete, address, nil, nil)
}

// PrepareServiceReview stores an actor-bound setup plan with a digest and a
// ten-minute expiry. Nothing is deployed; it is not idempotent, since every
// call stores another review against the caller's open quota.
func (c *Client) PrepareServiceReview(ctx context.Context, request ServiceReviewRequest) (*ServiceReview, error) {
	if request.ConsumerIDs == nil {
		request.ConsumerIDs = []string{}
	}
	if request.Parameters == nil {
		request.Parameters = map[string]int64{}
	}
	if request.SecretReferences == nil {
		request.SecretReferences = map[string]string{}
	}
	var review ServiceReview
	address := c.servicesURL("/api/v1/org/service-admission/reviews", nil)
	if requestError := c.sendJSONContext(ctx, http.MethodPost, address, request, &review); requestError != nil {
		return nil, requestError
	}
	return &review, nil
}

// ListServiceReviews reads one page of the caller's own setup reviews.
func (c *Client) ListServiceReviews(ctx context.Context, options ServicePageOptions) (*ServiceReviewPage, error) {
	var page ServiceReviewPage
	address := c.servicesURL("/api/v1/org/service-admission/reviews", options.query())
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &page); requestError != nil {
		return nil, requestError
	}
	return &page, nil
}

// GetServiceReview reads one of the caller's own setup reviews.
func (c *Client) GetServiceReview(ctx context.Context, reviewID string) (*ServiceReviewSummary, error) {
	var review ServiceReviewSummary
	address := c.servicesURL("/api/v1/org/service-admission/reviews/%s", nil, reviewID)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &review); requestError != nil {
		return nil, requestError
	}
	return &review, nil
}

// ConfirmServiceReview confirms a setup review by the digest the person
// reviewed. This is the call that sets the service up; a retry returns the
// same receipt.
func (c *Client) ConfirmServiceReview(ctx context.Context, reviewID string, digest string) (*ServiceReview, error) {
	if digest == "" {
		return nil, errors.New("confirming a service review needs the digest that was reviewed")
	}
	var review ServiceReview
	address := c.servicesURL("/api/v1/org/service-admission/reviews/%s/confirm", nil, reviewID)
	if requestError := c.sendJSONContext(ctx, http.MethodPost, address, serviceConfirmRequest{Digest: digest}, &review); requestError != nil {
		return nil, requestError
	}
	return &review, nil
}

// ListServiceInstances reads one page of the organisation's service inventory.
func (c *Client) ListServiceInstances(ctx context.Context, options ServicePageOptions) (*ServiceInstancePage, error) {
	var page ServiceInstancePage
	address := c.servicesURL("/api/v1/org/service-admission/instances", options.query())
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &page); requestError != nil {
		return nil, requestError
	}
	return &page, nil
}

// GetServiceInstance reads one service with its health, readiness and
// connection.
func (c *Client) GetServiceInstance(ctx context.Context, instanceID string) (*ServiceInstance, error) {
	var instance ServiceInstance
	address := c.servicesURL("/api/v1/org/service-admission/instances/%s", nil, instanceID)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &instance); requestError != nil {
		return nil, requestError
	}
	return &instance, nil
}

// PrepareServiceRetirement stores a retirement review of the instance: what
// retiring it removes and keeps, with a digest and a ten-minute expiry.
// Nothing on the cluster changes. Like setup, it is not idempotent.
func (c *Client) PrepareServiceRetirement(ctx context.Context, instanceID string, request ServiceRetirementRequest) (*ServiceRetirement, error) {
	if !request.AcknowledgeDataLoss {
		return nil, errors.New("retiring a service deletes its data; the retirement must acknowledge the data loss")
	}
	if request.DisconnectConsumerIDs == nil {
		request.DisconnectConsumerIDs = []string{}
	}
	var retirement ServiceRetirement
	address := c.servicesURL("/api/v1/org/service-admission/instances/%s/retirements", nil, instanceID)
	if requestError := c.sendJSONContext(ctx, http.MethodPost, address, request, &retirement); requestError != nil {
		return nil, requestError
	}
	return &retirement, nil
}

// ListServiceRetirements reads one page of the caller's own retirements of
// the instance. A client whose prepare answer was lost finds it here with
// State "pending" instead of preparing again.
func (c *Client) ListServiceRetirements(ctx context.Context, instanceID string, options ServiceRetirementListOptions) (*ServiceRetirementPage, error) {
	query := options.query()
	if options.State != "" {
		query.Set("state", options.State)
	}
	var page ServiceRetirementPage
	address := c.servicesURL("/api/v1/org/service-admission/instances/%s/retirements", query, instanceID)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &page); requestError != nil {
		return nil, requestError
	}
	return &page, nil
}

// GetServiceRetirement reads one of the caller's retirements with its
// progress, and volume_disposal once it settled.
func (c *Client) GetServiceRetirement(ctx context.Context, instanceID string, retirementID string) (*ServiceRetirement, error) {
	var retirement ServiceRetirement
	address := c.servicesURL("/api/v1/org/service-admission/instances/%s/retirements/%s", nil, instanceID, retirementID)
	if requestError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &retirement); requestError != nil {
		return nil, requestError
	}
	return &retirement, nil
}

// ConfirmServiceRetirement confirms a retirement review by its digest. This
// is the call that removes the service and deletes its data.
func (c *Client) ConfirmServiceRetirement(ctx context.Context, instanceID string, retirementID string, digest string) (*ServiceRetirement, error) {
	if digest == "" {
		return nil, errors.New("confirming a service retirement needs the digest that was reviewed")
	}
	var retirement ServiceRetirement
	address := c.servicesURL("/api/v1/org/service-admission/instances/%s/retirements/%s/confirm", nil, instanceID, retirementID)
	if requestError := c.sendJSONContext(ctx, http.MethodPost, address, serviceConfirmRequest{Digest: digest}, &retirement); requestError != nil {
		return nil, requestError
	}
	return &retirement, nil
}
