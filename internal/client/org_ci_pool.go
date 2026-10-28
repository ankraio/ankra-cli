package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"time"
)

// OrganisationCIPoolMember is one cluster in the organisation's CI cluster
// pool (GET /api/v1/org/ci-settings/pool).
//
// IsPrimary says the cluster is the organisation's declared pipeline cluster
// (ci_cluster_id), which is always a member. IsListed says it has a pool row
// of its own; the primary is a member without one, at weight 100. CreatedAt
// is nil for a member with no row.
type OrganisationCIPoolMember struct {
	ClusterID   string     `json:"cluster_id" yaml:"cluster_id"`
	ClusterName string     `json:"cluster_name" yaml:"cluster_name"`
	Weight      int        `json:"weight" yaml:"weight"`
	IsPrimary   bool       `json:"is_primary" yaml:"is_primary"`
	IsListed    bool       `json:"is_listed" yaml:"is_listed"`
	CreatedAt   *time.Time `json:"created_at" yaml:"created_at"`
}

// OrganisationCIPool is the organisation's CI cluster pool: the clusters its
// pipeline runs are spread across. IsPooled says it lists at least one
// member; when false every run goes to the declared pipeline cluster, exactly
// as before pools existed.
type OrganisationCIPool struct {
	IsPooled bool                       `json:"is_pooled" yaml:"is_pooled"`
	Members  []OrganisationCIPoolMember `json:"members" yaml:"members"`
}

// ErrCIPoolUnavailable is a platform that does not serve the CI pool routes:
// it predates them and answers a bare 404.
var ErrCIPoolUnavailable = errors.New("the platform does not support CI cluster pools yet")

// ciPoolPath is the CI pool's collection path; a member is addressed below it
// by cluster id.
const ciPoolPath = "/api/v1/org/ci-settings/pool"

// GetOrganisationCIPool reads the organisation's CI cluster pool. Readable by
// any organisation member.
func (c *Client) GetOrganisationCIPool(ctx context.Context) (*OrganisationCIPool, error) {
	body, requestError := c.doCISettingsRequestAt(ctx, http.MethodGet, ciPoolPath, nil)
	if requestError != nil {
		return nil, ciPoolError(requestError)
	}
	return decodeOrganisationCIPool(body)
}

// SetOrganisationCIPoolMember lists a cluster in the organisation's CI pool,
// or re-weights it, and answers the pool as stored. A nil weight keeps a
// listed member's weight and gives a new member the platform's default (100).
// Requires organisation admin.
func (c *Client) SetOrganisationCIPoolMember(ctx context.Context, clusterID string,
	weight *int) (*OrganisationCIPool, error) {
	var encoded []byte
	if weight != nil {
		body, marshalError := json.Marshal(map[string]int{"weight": *weight})
		if marshalError != nil {
			return nil, fmt.Errorf("encode request: %w", marshalError)
		}
		encoded = body
	}
	body, requestError := c.doCISettingsRequestAt(ctx, http.MethodPut,
		ciPoolPath+"/"+neturl.PathEscape(clusterID), encoded)
	if requestError != nil {
		return nil, ciPoolError(requestError)
	}
	return decodeOrganisationCIPool(body)
}

// RemoveOrganisationCIPoolMember unlists a cluster from the organisation's CI
// pool and answers the pool that remains. Runs already pinned to it finish
// there. Requires organisation admin.
func (c *Client) RemoveOrganisationCIPoolMember(ctx context.Context, clusterID string) (*OrganisationCIPool, error) {
	body, requestError := c.doCISettingsRequestAt(ctx, http.MethodDelete,
		ciPoolPath+"/"+neturl.PathEscape(clusterID), nil)
	if requestError != nil {
		return nil, ciPoolError(requestError)
	}
	return decodeOrganisationCIPool(body)
}

// ciPoolError maps a bare router 404 - a platform without the pool routes -
// onto ErrCIPoolUnavailable, and leaves every other error, the platform's own
// not-found sentences included, as it was.
func ciPoolError(requestError error) error {
	var unexpected *UnexpectedResponseError
	if errors.As(requestError, &unexpected) && unexpected.StatusCode == http.StatusNotFound &&
		unexpected.Detail == "" {
		return ErrCIPoolUnavailable
	}
	return requestError
}

func decodeOrganisationCIPool(body []byte) (*OrganisationCIPool, error) {
	var pool OrganisationCIPool
	if unmarshalError := json.Unmarshal(body, &pool); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	if pool.Members == nil {
		pool.Members = []OrganisationCIPoolMember{}
	}
	return &pool, nil
}
