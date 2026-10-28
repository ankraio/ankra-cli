package client

import (
	"encoding/json"
	"net/http"
	"net/url"
)

// The upgrade preflight's check statuses and verdicts, as the platform
// sends them.
const (
	UpgradePreflightStatusPass    = "pass"
	UpgradePreflightStatusWarn    = "warn"
	UpgradePreflightStatusBlock   = "block"
	UpgradePreflightStatusSkipped = "skipped"

	UpgradePreflightVerdictReady   = "ready"
	UpgradePreflightVerdictBlocked = "blocked"
)

// UpgradePreflightFinding is one check of an upgrade preflight. A warning or
// a skipped check whose evidence could not be read was not checked; it did
// not pass.
type UpgradePreflightFinding struct {
	Check   string `json:"check"`
	Status  string `json:"status"`
	Message string `json:"message"`
	// OverridableWithForce marks the one block `ankra cluster upgrade
	// --force` goes past: a PodDisruptionBudget that would park the drain.
	OverridableWithForce bool            `json:"overridable_with_force"`
	Evidence             json.RawMessage `json:"evidence,omitempty"`
}

// UpgradePreflightReport is the read-only upgrade preflight of a cluster
// for one target version.
type UpgradePreflightReport struct {
	RawDocument            `json:"-" yaml:"-"`
	ClusterID              string                    `json:"cluster_id"`
	RequestedTargetVersion string                    `json:"requested_target_version"`
	TargetVersion          string                    `json:"target_version"`
	Distribution           string                    `json:"distribution"`
	Verdict                string                    `json:"verdict"`
	Blocks                 int                       `json:"blocks"`
	Warnings               int                       `json:"warnings"`
	Checks                 []UpgradePreflightFinding `json:"checks"`
	CheckedAt              string                    `json:"checked_at"`
}

// GetUpgradePreflight runs the read-only upgrade preflight of a self-managed
// cluster of the given kind for a hop to targetVersion. Nothing on the
// cluster changes.
func (c *Client) GetUpgradePreflight(kind, clusterID, targetVersion string) (*UpgradePreflightReport, error) {
	endpoint := c.providerClusterURL(kind, clusterID, "upgrade-preflight") +
		"?" + url.Values{"target_version": []string{targetVersion}}.Encode()
	var report UpgradePreflightReport
	if requestError := c.sendJSON(http.MethodGet, endpoint, nil, &report); requestError != nil {
		return nil, requestError
	}
	return &report, nil
}
