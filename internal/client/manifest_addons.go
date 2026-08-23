package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// The published manifest add-on lane. `ankra application publish-addon` turns
// an application's manifest set into an entry in the organisation's add-on
// catalog; these are the operations on what that produced - inspect it, diff
// two published versions, install it onto a cluster, take it out of the
// catalog, or delete it outright.
//
// The add-on id is a catalog identity rather than an application subresource,
// so these hang off their own path root. They still go through
// applicationResourceRequest: despite its name that helper is the bearer JSON
// request path for this whole surface (auth header, FastAPI `detail` error
// surfacing, status preserved for exit-code classification) and builds no
// path of its own, so reusing it is what keeps the error contract identical
// rather than a second one drifting alongside it.

const manifestAddonsAPIPath = "/api/v1/org/manifest-addons"

func manifestAddonPath(addonID string, suffix string) string {
	return manifestAddonsAPIPath + "/" + url.PathEscape(addonID) + suffix
}

// InstallManifestAddonRequest mirrors the install body. Only cluster_id is
// required; namespace and version default from the add-on's own descriptor,
// and inputs answers whatever the published manifests declare.
type InstallManifestAddonRequest struct {
	ClusterID string            `json:"cluster_id"`
	Namespace string            `json:"namespace,omitempty"`
	Version   string            `json:"version,omitempty"`
	Inputs    map[string]string `json:"inputs,omitempty"`
}

func (client *Client) GetManifestAddon(requestContext context.Context, addonID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodGet, manifestAddonPath(addonID, ""), nil, nil)
}

// DiffManifestAddon compares two published snapshots of one add-on. toVersion
// is required by the endpoint; an empty fromVersion lets the backend pick the
// version published before it, and paths narrows the comparison to the named
// manifests.
func (client *Client) DiffManifestAddon(requestContext context.Context, addonID string,
	toVersion string, fromVersion string, paths []string) (json.RawMessage, error) {
	query := url.Values{}
	query.Set("to", toVersion)
	if fromVersion != "" {
		query.Set("from", fromVersion)
	}
	for _, path := range paths {
		query.Add("paths", path)
	}
	return client.applicationResourceRequest(requestContext, http.MethodGet, manifestAddonPath(addonID, "/diff"), query, nil)
}

func (client *Client) InstallManifestAddon(requestContext context.Context, addonID string,
	installRequest InstallManifestAddonRequest) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost,
		manifestAddonPath(addonID, "/install"), nil, installRequest)
}

func (client *Client) UnpublishManifestAddon(requestContext context.Context, addonID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodPost,
		manifestAddonPath(addonID, "/unpublish"), nil, nil)
}

func (client *Client) DeleteManifestAddon(requestContext context.Context, addonID string) (json.RawMessage, error) {
	return client.applicationResourceRequest(requestContext, http.MethodDelete, manifestAddonPath(addonID, ""), nil, nil)
}
