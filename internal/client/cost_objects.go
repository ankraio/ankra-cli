package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The object kinds GET /api/v1/org/cloud-cost/objects/{kind}/... serves a
// cost projection for (ankra-cozgu.3.5.1).
const (
	ObjectCostKindCluster     = "cluster"
	ObjectCostKindNamespace   = "namespace"
	ObjectCostKindStack       = "stack"
	ObjectCostKindApplication = "application"
	ObjectCostKindCredential  = "credential"
	ObjectCostKindProject     = "project"
)

// ObjectCostProjection is what one cluster, namespace, stack, application or
// credential costs now and over the last 30 days. Every field but
// ByEnvironment is always present; a figure the platform cannot give is
// null, never zero. When
// Priced is false every figure is null and UnpricedReason says why.
// CoverageIncomplete true makes MonthlyCents a floor: a cluster behind the
// object could not be priced in full or contributed nothing.
type ObjectCostProjection struct {
	RawDocument `json:"-" yaml:"-"`

	Kind   string            `json:"kind" yaml:"kind"`
	Object ObjectCostSubject `json:"object" yaml:"object"`
	// Currency is the display currency; every *_cents figure is in its
	// minor units.
	Currency           string          `json:"currency" yaml:"currency"`
	Priced             bool            `json:"priced" yaml:"priced"`
	UnpricedReason     *string         `json:"unpriced_reason" yaml:"unpriced_reason"`
	MonthlyCents       *int64          `json:"monthly_cents" yaml:"monthly_cents"`
	IdlePct            *float64        `json:"idle_pct" yaml:"idle_pct"`
	ShareOfFleetPct    *float64        `json:"share_of_fleet_pct" yaml:"share_of_fleet_pct"`
	Confidence         *string         `json:"confidence" yaml:"confidence"`
	CoverageIncomplete *bool           `json:"coverage_incomplete" yaml:"coverage_incomplete"`
	OpenWaste          ObjectCostWaste `json:"open_waste" yaml:"open_waste"`
	// Trend30d is the object's daily spend over 30 UTC days, oldest first:
	// what each day cost, not a run rate like MonthlyCents. The current day
	// is still metering (ObjectCostDay.Partial).
	Trend30d                []ObjectCostDay       `json:"trend_30d" yaml:"trend_30d"`
	Clusters                []ObjectCostCluster   `json:"clusters" yaml:"clusters"`
	Namespaces              []ObjectCostNamespace `json:"namespaces" yaml:"namespaces"`
	SnapshotStaleAfterHours int                   `json:"snapshot_stale_after_hours" yaml:"snapshot_stale_after_hours"`
	// ByEnvironment is an application's cost split by its environments: null
	// for every other kind, and absent from a platform that predates it.
	// Encoding gives back the key exactly when the platform sent it.
	ByEnvironment OptionalObjectCostByEnvironment `json:"by_environment,omitzero" yaml:"by_environment,omitempty"`
}

// The states of ObjectCostByEnvironment.State and ObjectCostRateParts.State.
const (
	ObjectCostStateAvailable = "available"
	ObjectCostStatePartial   = "partial"
	ObjectCostStateUnknown   = "unknown"
)

// The reasons ObjectCostRateParts.Gaps can give for parts that are partial
// or unknown. A platform may add more; a reader names one it does not know
// rather than dropping it.
const (
	ObjectCostGapNamespaceNotPriced = "namespace_not_priced"
	ObjectCostGapNoPodsSeen         = "no_pods_seen"
	ObjectCostGapRequestsUnreadable = "requests_unreadable"
	ObjectCostGapSplitUnknown       = "split_unknown"
	ObjectCostGapSplitRounded       = "split_rounded"
	ObjectCostGapPodsCapped         = "pods_capped"
	ObjectCostGapNodesNotAllPriced  = "nodes_not_all_priced"
	ObjectCostGapUnreadable         = "unreadable"
)

// ObjectCostRatePartKindBackups is the row kind for backup storage, which
// Ankra does not price, so the row is shown beside the rate and is not part
// of it.
const ObjectCostRatePartKindBackups = "backups"

// OptionalObjectCostByEnvironment is the by_environment block, which the
// platform may send as an object or null, or leave out. Present tells the
// last case from null, as OptionalBool does.
type OptionalObjectCostByEnvironment struct {
	Present bool
	Value   *ObjectCostByEnvironment
}

// IsZero is what omitzero (JSON) and omitempty (YAML) consult: only an
// absent key is left out.
func (o OptionalObjectCostByEnvironment) IsZero() bool {
	return !o.Present
}

// UnmarshalJSON runs only for a key that is present, null included.
func (o *OptionalObjectCostByEnvironment) UnmarshalJSON(data []byte) error {
	o.Present = true
	o.Value = nil
	if string(data) == "null" {
		return nil
	}
	var value ObjectCostByEnvironment
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

func (o OptionalObjectCostByEnvironment) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.Value)
}

func (o OptionalObjectCostByEnvironment) MarshalYAML() (any, error) {
	if o.Value == nil {
		return nil, nil
	}
	return o.Value, nil
}

// ObjectCostByEnvironment is an application's daily spend by environment.
// State is available when every source was read, and unknown (with Reason
// and no environments) when one could not be, which is never "no
// environment". An environment with no installation behind the
// application's cost has no line, so an available block may list none.
// Spend the metering recorded that no line counts is in Uncounted.
type ObjectCostByEnvironment struct {
	State  string  `json:"state" yaml:"state"`
	Reason *string `json:"reason" yaml:"reason"`
	// Environments holds at most 50 lines; EnvironmentsTruncated says more
	// were behind the application, and their spend is in Uncounted as
	// environment_not_listed.
	Environments          []ObjectCostEnvironment `json:"environments" yaml:"environments"`
	EnvironmentsTruncated bool                    `json:"environments_truncated" yaml:"environments_truncated"`
	// Uncounted holds at most 50 entries, the most recent first;
	// UncountedTruncated says more were found.
	Uncounted          []ObjectCostUncountedSpend `json:"uncounted" yaml:"uncounted"`
	UncountedTruncated bool                       `json:"uncounted_truncated" yaml:"uncounted_truncated"`
	// UncountedKnownFrom is the first UTC day the application's spend
	// records cover; Uncounted cannot be complete for days before it. Null
	// when there is no record at all.
	UncountedKnownFrom *string `json:"uncounted_known_from" yaml:"uncounted_known_from"`
}

// ObjectCostEnvironment is one environment's line. EnvironmentID is the
// named environment's id when Named is true, else the id of the cluster the
// unnamed environment is. Shared says a namespace of it also holds another
// application, so that namespace is counted whole, as the total counts it.
// Parts is what the environment's monthly rate is made of now; it is nil
// from a platform that predates it.
type ObjectCostEnvironment struct {
	EnvironmentID string               `json:"environment_id" yaml:"environment_id"`
	Named         bool                 `json:"named" yaml:"named"`
	Shared        bool                 `json:"shared" yaml:"shared"`
	Trend30d      []ObjectCostDay      `json:"trend_30d" yaml:"trend_30d"`
	Parts         *ObjectCostRateParts `json:"parts,omitempty" yaml:"parts,omitempty"`
}

// ObjectCostRateParts is what one environment's monthly rate is made of:
// the rows it splits into and what no row explains. Wherever MonthlyCents is
// a number, the rows counted in the rate, TruncatedMonthlyCents and
// UnattributedMonthlyCents add up to it exactly. State partial says, in
// Gaps, why some of the rate is on no row, why MonthlyCents leaves a
// namespace out (a floor), or why the rows are not exact; unknown means the
// rate cannot be stated, so MonthlyCents and UnattributedMonthlyCents are
// null and there are no rows. No rows never means the environment costs
// nothing.
type ObjectCostRateParts struct {
	State        string   `json:"state" yaml:"state"`
	Gaps         []string `json:"gaps" yaml:"gaps"`
	MonthlyCents *int64   `json:"monthly_cents" yaml:"monthly_cents"`
	// Rows holds at most 30 rows, the largest first and the rows with a null
	// amount last. RowsTruncated says more were found, and
	// TruncatedMonthlyCents is what those left out add up to in the rate.
	Rows                     []ObjectCostRatePart `json:"rows" yaml:"rows"`
	RowsTruncated            bool                 `json:"rows_truncated" yaml:"rows_truncated"`
	TruncatedMonthlyCents    *int64               `json:"truncated_monthly_cents" yaml:"truncated_monthly_cents"`
	UnattributedMonthlyCents *int64               `json:"unattributed_monthly_cents" yaml:"unattributed_monthly_cents"`
}

// ObjectCostRatePart is one row of an environment's rate. MonthlyCents is
// null when the row is not priced (backups, which are not part of the rate)
// or when the platform cannot say how much of the rate is this row's: such a
// row is still part of the rate, and its amount is inside
// UnattributedMonthlyCents. Null is never zero.
type ObjectCostRatePart struct {
	Kind         string  `json:"kind" yaml:"kind"`
	Name         string  `json:"name" yaml:"name"`
	Detail       *string `json:"detail" yaml:"detail"`
	ClusterID    *string `json:"cluster_id" yaml:"cluster_id"`
	Namespace    *string `json:"namespace" yaml:"namespace"`
	MonthlyCents *int64  `json:"monthly_cents" yaml:"monthly_cents"`
	Shared       bool    `json:"shared" yaml:"shared"`
	// CountedInRate is false on a row shown beside the rate and not part of
	// it (backups). Absent from a platform that predates it; read it through
	// IsCountedInRate.
	CountedInRate         OptionalBool `json:"counted_in_rate,omitzero" yaml:"counted_in_rate,omitempty"`
	Role                  *string      `json:"role" yaml:"role"`
	Database              *string      `json:"database" yaml:"database"`
	Engine                *string      `json:"engine" yaml:"engine"`
	ImageVersion          *string      `json:"image_version" yaml:"image_version"`
	Instances             *int64       `json:"instances" yaml:"instances"`
	Copies                *int64       `json:"copies" yaml:"copies"`
	ReservedCPUMillicores *int64       `json:"reserved_cpu_millicores" yaml:"reserved_cpu_millicores"`
	ReservedMemoryBytes   *int64       `json:"reserved_memory_bytes" yaml:"reserved_memory_bytes"`
	CopiesAlike           *bool        `json:"copies_alike" yaml:"copies_alike"`
	ClaimCount            *int64       `json:"claim_count" yaml:"claim_count"`
	ClaimBytes            *int64       `json:"claim_bytes" yaml:"claim_bytes"`
	ClaimSizeKnown        *bool        `json:"claim_size_known" yaml:"claim_size_known"`
}

// IsCountedInRate says whether the row is part of the environment's rate:
// the platform's counted_in_rate when it sends one, and otherwise every
// kind but backups, which the platform never counts.
func (part ObjectCostRatePart) IsCountedInRate() bool {
	if part.CountedInRate.Present && part.CountedInRate.Value != nil {
		return *part.CountedInRate.Value
	}
	return part.Kind != ObjectCostRatePartKindBackups
}

// ObjectCostUncountedSpend is the days one namespace spent for the
// application that no environment line counts (Dates, UTC days, oldest
// first). Reason is
// installation_removed (the installation is gone, its spend is in no line
// and not in the total), cluster_not_read (its cluster was deleted or left
// the organisation) or environment_not_listed (its environment's line was
// cut by the cap; the spend is in the total). EnvironmentID is null for
// installation_removed.
type ObjectCostUncountedSpend struct {
	Reason        string   `json:"reason" yaml:"reason"`
	EnvironmentID *string  `json:"environment_id" yaml:"environment_id"`
	Named         bool     `json:"named" yaml:"named"`
	ClusterID     string   `json:"cluster_id" yaml:"cluster_id"`
	Namespace     string   `json:"namespace" yaml:"namespace"`
	Dates         []string `json:"dates" yaml:"dates"`
}

// ObjectCostSubject names the object. ClusterID and ClusterName are set for
// an object that lives on one cluster.
type ObjectCostSubject struct {
	ID          string  `json:"id" yaml:"id"`
	Name        string  `json:"name" yaml:"name"`
	ClusterID   *string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName *string `json:"cluster_name" yaml:"cluster_name"`
}

// ObjectCostWaste is the open cloud waste attributed to the object.
// Available false means waste is not attributed to this kind of object,
// which is unknown, not none; Reason says why.
type ObjectCostWaste struct {
	Available     bool    `json:"available" yaml:"available"`
	Reason        *string `json:"reason" yaml:"reason"`
	Count         int     `json:"count" yaml:"count"`
	MonthlyCents  *int64  `json:"monthly_cents" yaml:"monthly_cents"`
	UnpricedCount int     `json:"unpriced_count" yaml:"unpriced_count"`
}

// ObjectCostDay is one UTC day of the trend: what the object spent that day.
// Cents is null on a day none of the object's clusters was metered: unknown,
// never zero.
//
// Partial is true for the day the trend was read in, which is still
// metering: its Cents covers only HoursMetered of the day's 24 UTC hours and
// is not comparable to a whole day. HoursMetered is, for an object behind
// several clusters, the fewest hours any metered cluster covered. Both are
// absent from a platform that predates them (cluster#5617), so Partial
// tells absent from false and HoursMetered is nil there.
//
// Incomplete is true when Cents is short of an hour the day had already had
// (a missed metering pass longer than the platform's 3-hour backfill, a
// stopped or unpriced hour, the cluster's creation day), so Cents is a floor
// of the day, never its whole and never a drop in spend
// (ankra-bn86k4.4.8). Absent from a platform that predates it.
//
// HoursMetered counts every hour with a figure, an hour a later metering
// pass filled in included (cluster#5909). HoursMeasured is the hours a
// metering pass measured, HoursEstimated the ones filled in (in Cents, never
// measured) and HoursMissing the ended hours with no figure at all. Behind
// several clusters each is one cluster's worst (the fewest measured, the
// most estimated or missing), so they need not add up to 24 and none is
// worked out from the others. All three are nil from a platform or a row
// that predates them: not known, never "every hour was measured".
type ObjectCostDay struct {
	Date            string       `json:"date" yaml:"date"`
	Cents           *int64       `json:"cents" yaml:"cents"`
	Partial         OptionalBool `json:"partial,omitzero" yaml:"partial,omitempty"`
	HoursMetered    *int         `json:"hours_metered,omitempty" yaml:"hours_metered,omitempty"`
	HoursMeasured   *int         `json:"hours_measured,omitempty" yaml:"hours_measured,omitempty"`
	HoursEstimated  *int         `json:"hours_estimated,omitempty" yaml:"hours_estimated,omitempty"`
	HoursMissing    *int         `json:"hours_missing,omitempty" yaml:"hours_missing,omitempty"`
	Incomplete      OptionalBool `json:"incomplete,omitzero" yaml:"incomplete,omitempty"`
	ClustersMetered int          `json:"clusters_metered" yaml:"clusters_metered"`
	ClustersTotal   int          `json:"clusters_total" yaml:"clusters_total"`
}

// ObjectCostCluster is one cluster behind the object. MonthlyCents is null
// when the cluster contributed nothing to the object's figure.
// CoverageIncomplete is the cluster's own snapshot flag: true when a node or
// billed resource of it could not be priced, false when it priced
// everything, null when it is unpriced, and absent from a platform that
// predates the field (cluster#3453).
type ObjectCostCluster struct {
	ClusterID          string       `json:"cluster_id" yaml:"cluster_id"`
	ClusterName        string       `json:"cluster_name" yaml:"cluster_name"`
	Priced             bool         `json:"priced" yaml:"priced"`
	MonthlyCents       *int64       `json:"monthly_cents" yaml:"monthly_cents"`
	Confidence         *string      `json:"confidence" yaml:"confidence"`
	CoverageIncomplete OptionalBool `json:"coverage_incomplete,omitzero" yaml:"coverage_incomplete,omitempty"`
}

// OptionalBool is a boolean the platform may send as true, false or null,
// or leave out entirely. Present tells the last case from null, so
// structured output re-emits the key exactly when the platform sent it: an
// absent key never comes back as a null, which the contract reads as
// something else.
type OptionalBool struct {
	Present bool
	Value   *bool
}

// IsTrue reports a present, non-null true.
func (o OptionalBool) IsTrue() bool {
	return o.Value != nil && *o.Value
}

// IsZero is what omitzero (JSON) and omitempty (YAML) consult: only an
// absent key is left out.
func (o OptionalBool) IsZero() bool {
	return !o.Present
}

// UnmarshalJSON runs only for a key that is present, null included.
func (o *OptionalBool) UnmarshalJSON(data []byte) error {
	o.Present = true
	o.Value = nil
	if string(data) == "null" {
		return nil
	}
	var value bool
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

func (o OptionalBool) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.Value)
}

func (o OptionalBool) MarshalYAML() (any, error) {
	if o.Value == nil {
		return nil, nil
	}
	return *o.Value, nil
}

// ObjectCostNamespace is one namespace behind a namespace-based object.
// Shared says another application also runs in it, so its whole cost is
// counted for each.
type ObjectCostNamespace struct {
	ClusterID    string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName  string `json:"cluster_name" yaml:"cluster_name"`
	Namespace    string `json:"namespace" yaml:"namespace"`
	MonthlyCents *int64 `json:"monthly_cents" yaml:"monthly_cents"`
	Shared       bool   `json:"shared" yaml:"shared"`
}

// objectCostPaths is each kind's route, written out whole (one %s per
// path segment) so the cluster-routes check verifies every one against the
// cluster's router rather than seeing only their shared prefix.
var objectCostPaths = map[string]string{
	ObjectCostKindCluster:     "/api/v1/org/cloud-cost/objects/cluster/%s",
	ObjectCostKindNamespace:   "/api/v1/org/cloud-cost/objects/namespace/%s/%s",
	ObjectCostKindStack:       "/api/v1/org/cloud-cost/objects/stack/%s/%s",
	ObjectCostKindApplication: "/api/v1/org/cloud-cost/objects/application/%s",
	ObjectCostKindCredential:  "/api/v1/org/cloud-cost/objects/credential/%s",
	ObjectCostKindProject:     "/api/v1/org/cloud-cost/objects/project/%s",
}

// GetObjectCost reads one object's cost projection: the cluster id for a
// cluster; the cluster id and the namespace for a namespace; the cluster id
// and the stack name for a stack; the application id; the credential id; or
// the project id.
// Each segment is path-escaped, so a name carrying a space or a slash stays
// one segment. A kind this client does not know, or the wrong number of
// segments for it, is refused before any request.
func (c *Client) GetObjectCost(kind string, pathSegments ...string) (*ObjectCostProjection, error) {
	path, isKnown := objectCostPaths[kind]
	if !isKnown {
		return nil, fmt.Errorf("unknown object kind %q", kind)
	}
	if want := strings.Count(path, "%s"); want != len(pathSegments) {
		return nil, fmt.Errorf("a %s object takes %d path segments, got %d", kind, want, len(pathSegments))
	}
	escaped := make([]any, len(pathSegments))
	for index, segment := range pathSegments {
		escaped[index] = url.PathEscape(segment)
	}
	endpoint := c.BaseURL + fmt.Sprintf(path, escaped...)
	var result ObjectCostProjection
	if err := c.sendJSON(http.MethodGet, endpoint, nil, &result); err != nil {
		return nil, err
	}
	if result.Trend30d == nil {
		result.Trend30d = []ObjectCostDay{}
	}
	if result.Clusters == nil {
		result.Clusters = []ObjectCostCluster{}
	}
	if result.Namespaces == nil {
		result.Namespaces = []ObjectCostNamespace{}
	}
	return &result, nil
}
