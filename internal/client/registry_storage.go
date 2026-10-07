package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Registry storage: how much the organisation's registry projects hold
// against their storage limit, which repositories hold it, and the image
// retention policy that frees it (/api/v1/org/registry-storage). The
// registry enforces the limit per project; a push that would cross it is
// refused, so a full project stops every build from publishing.

// RegistryStorageNoLimit is the limit_bytes value of a project without a
// storage limit.
const RegistryStorageNoLimit int64 = -1

// The usage_status vocabulary.
const (
	RegistryStorageUsageComplete       = "complete"
	RegistryStorageUsagePartial        = "partial"
	RegistryStorageUsageUnknown        = "unknown"
	RegistryStorageUsageNotProvisioned = "not_provisioned"
)

// RegistryStorageLimitKind is the limit-request kind that asks for more
// registry storage; its requested_value is in GiB.
const RegistryStorageLimitKind = "registry_storage"

// RegistryStorage is the organisation's storage limit and usage.
type RegistryStorage struct {
	OrganisationID string `json:"organisation_id" yaml:"organisation_id"`
	// LimitBytes is the limit in force per registry project;
	// RegistryStorageNoLimit when there is none.
	LimitBytes        int64 `json:"limit_bytes" yaml:"limit_bytes"`
	DefaultLimitBytes int64 `json:"default_limit_bytes" yaml:"default_limit_bytes"`
	// Source is default (the platform default) or organisation (Ankra set
	// it for this organisation: a purchase or an approved request).
	Source    string  `json:"source" yaml:"source"`
	Reason    *string `json:"reason" yaml:"reason"`
	UpdatedBy *string `json:"updated_by" yaml:"updated_by"`
	UpdatedAt *string `json:"updated_at" yaml:"updated_at"`
	// UsedBytes is nil when the usage could not be read: unknown, never 0.
	UsedBytes *int64 `json:"used_bytes" yaml:"used_bytes"`
	// UsageStatus is complete, partial (UsedBytes is a lower bound),
	// unknown, or not_provisioned (no registry project yet).
	UsageStatus string                   `json:"usage_status" yaml:"usage_status"`
	Projects    []RegistryStorageProject `json:"projects" yaml:"projects"`
}

// RegistryStorageProject is one registry project's usage.
type RegistryStorageProject struct {
	// Name is how the project is addressed: default or an extra project's
	// name. Project is its name on the registry.
	Name       string `json:"name" yaml:"name"`
	Project    string `json:"project" yaml:"project"`
	LimitBytes *int64 `json:"limit_bytes" yaml:"limit_bytes"`
	UsedBytes  *int64 `json:"used_bytes" yaml:"used_bytes"`
	// Status is ok or unknown.
	Status string `json:"status" yaml:"status"`
}

// RegistryStorageRepositories is the per-repository breakdown, largest
// first.
type RegistryStorageRepositories struct {
	Status       string                      `json:"status" yaml:"status"`
	Repositories []RegistryStorageRepository `json:"repositories" yaml:"repositories"`
	// SizesAreApproximate: layers shared between images are counted for
	// each image, so sizes rank repositories and do not add up to the usage.
	SizesAreApproximate bool `json:"sizes_are_approximate" yaml:"sizes_are_approximate"`
}

// RegistryStorageRepository is one repository's share of the storage.
type RegistryStorageRepository struct {
	Name          string `json:"name" yaml:"name"`
	Project       string `json:"project" yaml:"project"`
	ArtifactCount int64  `json:"artifact_count" yaml:"artifact_count"`
	SizeBytes     int64  `json:"size_bytes" yaml:"size_bytes"`
	LastPushedAt  string `json:"last_pushed_at" yaml:"last_pushed_at"`
	// Truncated reports that SizeBytes is a lower bound.
	Truncated bool `json:"truncated" yaml:"truncated"`
	// Unreadable reports that the repository's images could not be read:
	// its size is unknown, never 0, and the list's status is partial.
	Unreadable bool `json:"unreadable" yaml:"unreadable"`
	// RetentionRuleID is the rule governing the repository; nil when the
	// organisation's default policy does.
	RetentionRuleID *string `json:"retention_rule_id" yaml:"retention_rule_id"`
}

// RegistryRetentionPolicy keeps an image pushed within KeepDays (0 turns
// that part off) or among the KeepLatest newest of its repository.
type RegistryRetentionPolicy struct {
	KeepDays   int `json:"keep_days" yaml:"keep_days"`
	KeepLatest int `json:"keep_latest" yaml:"keep_latest"`
}

// RegistryRetentionRule replaces the default policy for the repositories
// its pattern matches.
type RegistryRetentionRule struct {
	ID                string  `json:"id" yaml:"id"`
	RepositoryPattern string  `json:"repository_pattern" yaml:"repository_pattern"`
	ApplicationID     *string `json:"application_id" yaml:"application_id"`
	KeepDays          int     `json:"keep_days" yaml:"keep_days"`
	KeepLatest        int     `json:"keep_latest" yaml:"keep_latest"`
	UpdatedBy         *string `json:"updated_by" yaml:"updated_by"`
	UpdatedAt         *string `json:"updated_at" yaml:"updated_at"`
}

// RegistryRetention is the organisation's image retention policy.
type RegistryRetention struct {
	OrganisationID  string                  `json:"organisation_id" yaml:"organisation_id"`
	Default         RegistryRetentionPolicy `json:"default" yaml:"default"`
	PlatformDefault RegistryRetentionPolicy `json:"platform_default" yaml:"platform_default"`
	// Source is default (the platform's policy) or organisation.
	Source    string                  `json:"source" yaml:"source"`
	UpdatedBy *string                 `json:"updated_by" yaml:"updated_by"`
	UpdatedAt *string                 `json:"updated_at" yaml:"updated_at"`
	Rules     []RegistryRetentionRule `json:"rules" yaml:"rules"`
	// RecentlyPulledDays: an organisation-set policy also keeps anything
	// pulled within this many days; 0 on the platform default.
	RecentlyPulledDays int    `json:"recently_pulled_days" yaml:"recently_pulled_days"`
	RunsDailyAt        string `json:"runs_daily_at" yaml:"runs_daily_at"`
	SpaceFreedAt       string `json:"space_freed_at" yaml:"space_freed_at"`
}

// RegistryRetentionRuleInput is one rule as it is written.
type RegistryRetentionRuleInput struct {
	RepositoryPattern string  `json:"repository_pattern"`
	ApplicationID     *string `json:"application_id,omitempty"`
	KeepDays          int     `json:"keep_days"`
	KeepLatest        int     `json:"keep_latest"`
}

// RegistryRetentionUpdate is a partial write of the retention policy. A
// part whose Set flag is false is not sent, and the platform keeps it.
// DefaultSet with a nil Default sends null: back to the platform default.
// RulesSet sends the whole rule list, replacing the stored one; an empty
// list removes every rule.
type RegistryRetentionUpdate struct {
	DefaultSet bool
	Default    *RegistryRetentionPolicy
	RulesSet   bool
	Rules      []RegistryRetentionRuleInput
}

// MarshalJSON sends only the parts that were set.
func (update RegistryRetentionUpdate) MarshalJSON() ([]byte, error) {
	body := map[string]any{}
	if update.DefaultSet {
		body["default"] = update.Default
	}
	if update.RulesSet {
		rules := update.Rules
		if rules == nil {
			rules = []RegistryRetentionRuleInput{}
		}
		body["rules"] = rules
	}
	return json.Marshal(body)
}

// RegistryRetentionUpdateResult is the answer to a policy write. A false
// HarborApplied is not a failure: the policy is saved and the registry
// picks it up automatically within about five minutes.
type RegistryRetentionUpdateResult struct {
	Retention        RegistryRetention `json:"retention" yaml:"retention"`
	HarborApplied    bool              `json:"harbor_applied" yaml:"harbor_applied"`
	HarborApplyError *string           `json:"harbor_apply_error" yaml:"harbor_apply_error"`
}

// RegistryRetentionRun is the answer to starting a cleanup now.
type RegistryRetentionRun struct {
	Projects     []RegistryRetentionRunProject `json:"projects" yaml:"projects"`
	SpaceFreedAt string                        `json:"space_freed_at" yaml:"space_freed_at"`
}

// RegistryRetentionRunProject is whether the cleanup started on one
// registry project, and why not when it did not.
type RegistryRetentionRunProject struct {
	Project string  `json:"project" yaml:"project"`
	Started bool    `json:"started" yaml:"started"`
	Reason  *string `json:"reason" yaml:"reason"`
}

const (
	registryStorageAPIPath             = "/api/v1/org/registry-storage"
	registryStorageRepositoriesAPIPath = registryStorageAPIPath + "/repositories"
	registryStorageRetentionAPIPath    = registryStorageAPIPath + "/retention"
	registryStorageRetentionRunAPIPath = registryStorageRetentionAPIPath + "/run"
)

const registryStorageRequestFailedOperation = "registry storage request failed"

// GetRegistryStorage reads the organisation's storage limit and usage.
func (c *Client) GetRegistryStorage(ctx context.Context) (*RegistryStorage, error) {
	var storage RegistryStorage
	if requestError := c.registryStorageRequest(ctx, http.MethodGet, registryStorageAPIPath, nil, &storage,
		http.StatusOK); requestError != nil {
		return nil, requestError
	}
	return &storage, nil
}

// ListRegistryStorageRepositories reads which repositories hold the
// storage, largest first. It walks the registry and is slower than the
// usage read.
func (c *Client) ListRegistryStorageRepositories(ctx context.Context) (*RegistryStorageRepositories, error) {
	var repositories RegistryStorageRepositories
	if requestError := c.registryStorageRequest(ctx, http.MethodGet, registryStorageRepositoriesAPIPath, nil,
		&repositories, http.StatusOK); requestError != nil {
		return nil, requestError
	}
	return &repositories, nil
}

// GetRegistryRetention reads the image retention policy.
func (c *Client) GetRegistryRetention(ctx context.Context) (*RegistryRetention, error) {
	var retention RegistryRetention
	if requestError := c.registryStorageRequest(ctx, http.MethodGet, registryStorageRetentionAPIPath, nil,
		&retention, http.StatusOK); requestError != nil {
		return nil, requestError
	}
	return &retention, nil
}

// UpdateRegistryRetention writes the parts of the retention policy the
// update sets.
func (c *Client) UpdateRegistryRetention(ctx context.Context, update RegistryRetentionUpdate) (*RegistryRetentionUpdateResult, error) {
	if !update.DefaultSet && !update.RulesSet {
		return nil, fmt.Errorf("a retention update must set the default policy, the rules, or both")
	}
	encoded, marshalError := json.Marshal(update)
	if marshalError != nil {
		return nil, fmt.Errorf("encode request: %w", marshalError)
	}
	var result RegistryRetentionUpdateResult
	if requestError := c.registryStorageRequest(ctx, http.MethodPut, registryStorageRetentionAPIPath, encoded,
		&result, http.StatusOK); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// RunRegistryRetention starts the retention cleanup now instead of at its
// daily time. Space is freed at the registry's next garbage collection.
func (c *Client) RunRegistryRetention(ctx context.Context) (*RegistryRetentionRun, error) {
	var run RegistryRetentionRun
	if requestError := c.registryStorageRequest(ctx, http.MethodPost, registryStorageRetentionRunAPIPath, nil,
		&run, http.StatusAccepted, http.StatusOK); requestError != nil {
		return nil, requestError
	}
	return &run, nil
}

func (c *Client) registryStorageRequest(ctx context.Context, method string, path string, body []byte, target any,
	successStatuses ...int) error {
	responseBody, requestError := c.doRegistryRequest(ctx, registryStorageRequestFailedOperation, method, path, body,
		successStatuses...)
	if requestError != nil {
		return requestError
	}
	// Every success answer on this lane carries a document. An empty one
	// would decode as a zero value - a usage status of "", a policy keeping
	// nothing - that reads like a real answer, so it is an error instead.
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return fmt.Errorf("%s: the platform answered %s %s with an empty body", registryStorageRequestFailedOperation, method, path)
	}
	if unmarshalError := json.Unmarshal(responseBody, target); unmarshalError != nil {
		return fmt.Errorf("parse response: %w", unmarshalError)
	}
	return nil
}
