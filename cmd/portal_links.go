package cmd

import (
	"net/url"
	"strings"

	"ankra/internal/client"
)

// Portal links: the address of the portal page for a resource the CLI just
// printed, so a script or an assistant can hand a teammate a link instead of
// guessing one from the API paths. The routes are the documented, stable
// subset (docs: reference/portal-links). Every cluster lives under the
// "imported" segment however it was created, clusters are addressed by id,
// and stacks, manifests and add-ons by name. The owning organisation rides
// along as ?org= so the portal offers to switch organisation instead of
// answering "not found" to a member whose active organisation is another one.

const (
	portalStackMemberManifest = "manifest"
	portalStackMemberAddon    = "addon"
)

// portalBaseURL is the portal the CLI is logged in to: the scheme and host of
// the API address, since the two share one origin. A path on the configured
// address belongs to the API, so it is dropped rather than carried into every
// link. An address that does not parse to a scheme and host falls back to the
// default portal.
func portalBaseURL() string {
	parsed, parseError := url.Parse(strings.TrimSpace(baseURL))
	if parseError != nil || parsed.Scheme == "" || parsed.Host == "" {
		return defaultBaseURL
	}
	return parsed.Scheme + "://" + parsed.Host
}

// portalURL joins escaped path segments under /organisation and appends the
// query, with the owning organisation when it is known. The caller's query
// is copied, never changed.
func portalURL(organisationID string, query url.Values, segments ...string) string {
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		escaped = append(escaped, url.PathEscape(segment))
	}
	link := portalBaseURL() + "/organisation/" + strings.Join(escaped, "/")
	linkQuery := url.Values{}
	for key, values := range query {
		linkQuery[key] = append([]string(nil), values...)
	}
	if organisationID != "" {
		linkQuery.Set("org", organisationID)
	}
	if encoded := linkQuery.Encode(); encoded != "" {
		link += "?" + encoded
	}
	return link
}

func portalClusterSegments(clusterID string, rest ...string) []string {
	return append([]string{"clusters", "cluster", "imported", clusterID}, rest...)
}

// portalClusterURL is the cluster's overview page, or "" for a cluster with
// no id to address it by.
func portalClusterURL(cluster client.ClusterListItem) string {
	if cluster.ID == "" {
		return ""
	}
	return portalURL(cluster.OrganisationID, nil, portalClusterSegments(cluster.ID, "overview")...)
}

// portalStackURL opens one stack in the stack editor.
func portalStackURL(cluster client.ClusterListItem, stackName string) string {
	if cluster.ID == "" || stackName == "" {
		return ""
	}
	return portalURL(cluster.OrganisationID, nil, portalClusterSegments(cluster.ID, "stacks", stackName, "edit")...)
}

// portalStackMemberURL opens the stack editor on one manifest or add-on of
// the stack, on its configuration tab.
func portalStackMemberURL(cluster client.ClusterListItem, stackName string, memberKind string, memberName string) string {
	if cluster.ID == "" || stackName == "" || memberName == "" {
		return ""
	}
	query := url.Values{}
	query.Set("activeTab", memberKind+":"+memberName)
	query.Set("activeSubTab", "configuration")
	return portalURL(cluster.OrganisationID, query, portalClusterSegments(cluster.ID, "stacks", stackName, "edit")...)
}

// portalAddonURL is an add-on's own page on its cluster, outside the stack
// editor.
func portalAddonURL(cluster client.ClusterListItem, addonName string) string {
	if cluster.ID == "" || addonName == "" {
		return ""
	}
	return portalURL(cluster.OrganisationID, nil, portalClusterSegments(cluster.ID, "add-ons", "add-on", addonName)...)
}

// The annotate helpers fill portal_url on what a list command is about to
// print. A link the platform already sent is kept, so a platform that starts
// returning its own takes over without a CLI change.

func annotateClusterPortalURLs(clusters []client.ClusterListItem) {
	for index := range clusters {
		if clusters[index].PortalURL == "" {
			clusters[index].PortalURL = portalClusterURL(clusters[index])
		}
	}
}

func annotateStackPortalURLs(cluster client.ClusterListItem, stacks []client.ClusterStackListItem) {
	for stackIndex := range stacks {
		stack := &stacks[stackIndex]
		if stack.PortalURL == "" {
			stack.PortalURL = portalStackURL(cluster, stack.Name)
		}
		for manifestIndex := range stack.Manifests {
			manifest := &stack.Manifests[manifestIndex]
			if manifest.PortalURL == "" {
				manifest.PortalURL = portalStackMemberURL(cluster, stack.Name, portalStackMemberManifest, manifest.Name)
			}
		}
		for addonIndex := range stack.Addons {
			addon := &stack.Addons[addonIndex]
			if addon.PortalURL == "" {
				addon.PortalURL = portalStackMemberURL(cluster, stack.Name, portalStackMemberAddon, addon.Name)
			}
		}
	}
}

// annotateManifestPortalURLs links each manifest into its stack's editor. A
// manifest outside any stack has no page of its own and stays without a link.
func annotateManifestPortalURLs(cluster client.ClusterListItem, manifests []client.ClusterManifestListItem) {
	for index := range manifests {
		manifest := &manifests[index]
		if manifest.PortalURL != "" || manifest.StackName == nil {
			continue
		}
		manifest.PortalURL = portalStackMemberURL(cluster, *manifest.StackName, portalStackMemberManifest, manifest.Name)
	}
}

func annotateAddonPortalURLs(cluster client.ClusterListItem, addons []client.ClusterAddonListItem) {
	for index := range addons {
		if addons[index].PortalURL == "" {
			addons[index].PortalURL = portalAddonURL(cluster, addons[index].Name)
		}
	}
}
