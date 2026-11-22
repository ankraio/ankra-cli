package cmd

import (
	"net/url"
	"strings"

	"ankra/internal/client"
)

// Portal links: the address of the portal page for a resource the CLI just
// printed, so a script or an assistant can hand a teammate a link instead of
// guessing one from the API paths. Every link is a page of the web app under
// /next: the Classic portal at the root (/organisation/...) is being retired
// (ankra-t5jf5.6.15), so nothing here may build a Classic path. Clusters are
// addressed by id, and stacks, manifests and add-ons by name. The owning
// organisation rides along as ?org= so the app offers to switch organisation
// instead of answering "not found" to a member whose active organisation is
// another one, and the project this invocation acts in rides along as
// ?project= for the same reason (ADR 0022).

const (
	// portalAppPath is where the web app is served on the portal's origin.
	portalAppPath = "/next/"

	portalStackMemberManifest = "manifest"
	portalStackMemberAddon    = "addon"

	// portalPodViewExec is the pod page's terminal view.
	portalPodViewExec = "exec"
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

// portalURL is a page of the web app that belongs to a project-scoped
// resource: the owning organisation when it is known and the project this
// invocation acts in both ride along.
func portalURL(organisationID string, query url.Values, segments ...string) string {
	return portalLink(organisationID, selectedProjectIDForLinks(), query, segments...)
}

// portalOrganisationPageURL is an organisation-level page (billing): the
// organisation rides along, the project does not, because the page is the
// same in every project and a ?project= for another one would stop on a
// project switch offer before showing it.
func portalOrganisationPageURL(organisationID string, segments ...string) string {
	return portalLink(organisationID, "", nil, segments...)
}

// portalPageURL is a page that is the person's own or the active
// organisation's, with nothing to carry (account settings, the AI agent runs
// list).
func portalPageURL(segments ...string) string {
	return portalLink("", "", nil, segments...)
}

// portalLink joins escaped path segments under the web app's path and
// appends the query, with the organisation and project when they are given.
// The caller's query is copied, never changed.
func portalLink(organisationID string, projectID string, query url.Values, segments ...string) string {
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		escaped = append(escaped, url.PathEscape(segment))
	}
	link := portalBaseURL() + portalAppPath + strings.Join(escaped, "/")
	linkQuery := url.Values{}
	for key, values := range query {
		linkQuery[key] = append([]string(nil), values...)
	}
	if organisationID != "" {
		linkQuery.Set("org", organisationID)
	}
	if projectID != "" {
		linkQuery.Set("project", projectID)
	}
	if encoded := linkQuery.Encode(); encoded != "" {
		link += "?" + encoded
	}
	return link
}

func portalClusterSegments(clusterID string, rest ...string) []string {
	return append([]string{"clusters", clusterID}, rest...)
}

// portalClusterURL is the cluster's overview page, or "" for a cluster with
// no id to address it by.
func portalClusterURL(cluster client.ClusterListItem) string {
	if cluster.ID == "" {
		return ""
	}
	return portalURL(cluster.OrganisationID, nil, portalClusterSegments(cluster.ID)...)
}

// portalClusterURLByID is the overview page of a cluster this invocation
// created, imported or staged drafts on, where only the id is in hand. Those
// answers carry no organisation, so the link names the one the request was
// sent to (invocationOrganisationIDForLinks) and, when that is not known
// locally, the one the platform reports for the cluster. A link without
// ?org= opens "not found" for a member whose portal is on another
// organisation. A cluster that cannot be read keeps the link without it.
func portalClusterURLByID(clusterID string) string {
	organisationID := invocationOrganisationIDForLinks()
	if organisationID == "" && clusterID != "" && apiClient != nil {
		if cluster, readError := apiClient.GetClusterByID(clusterID); readError == nil {
			organisationID = strings.TrimSpace(cluster.OrganisationID)
		}
	}
	return portalClusterURL(client.ClusterListItem{ID: clusterID, OrganisationID: organisationID})
}

// invocationOrganisationIDForLinks is the organisation this invocation's
// requests were scoped to: --org/ANKRA_ORG, or the organisation `ankra org
// switch` saved when it is the one being sent. "" when the requests named
// none, so the platform answered in the token's current organisation, which
// nothing local records.
func invocationOrganisationIDForLinks() string {
	if apiClient == nil {
		return ""
	}
	return strings.TrimSpace(apiClient.OrganisationOverride())
}

// portalStackURL opens one stack in the stack editor.
func portalStackURL(cluster client.ClusterListItem, stackName string) string {
	if cluster.ID == "" || stackName == "" {
		return ""
	}
	return portalURL(cluster.OrganisationID, nil, portalClusterSegments(cluster.ID, "stacks", stackName)...)
}

// portalStackMemberURL opens the stack editor with one manifest or add-on of
// the stack selected: the editor reads ?member=<name> and ?kind=manifest
// (any other kind selects an add-on).
func portalStackMemberURL(cluster client.ClusterListItem, stackName string, memberKind string, memberName string) string {
	if cluster.ID == "" || stackName == "" || memberName == "" {
		return ""
	}
	query := url.Values{}
	query.Set("member", memberName)
	query.Set("kind", memberKind)
	return portalURL(cluster.OrganisationID, query, portalClusterSegments(cluster.ID, "stacks", stackName)...)
}

// portalAddonURL is an add-on's own page on its cluster, outside the stack
// editor. The namespace, when known, picks between two add-ons of one name.
func portalAddonURL(cluster client.ClusterListItem, addonName string, namespace string) string {
	if cluster.ID == "" || addonName == "" {
		return ""
	}
	var query url.Values
	if namespace != "" {
		query = url.Values{"namespace": {namespace}}
	}
	return portalURL(cluster.OrganisationID, query, portalClusterSegments(cluster.ID, "addons", addonName)...)
}

// portalPodURL is one pod's page on its cluster, on the given view
// (overview, logs, metrics or exec), or "" without a cluster, namespace or
// pod to address it by.
func portalPodURL(cluster client.ClusterListItem, namespace string, podName string, view string) string {
	if cluster.ID == "" || namespace == "" || podName == "" {
		return ""
	}
	var query url.Values
	if view != "" {
		query = url.Values{"view": {view}}
	}
	return portalURL(cluster.OrganisationID, query, portalClusterSegments(cluster.ID, "kubernetes", "pods", namespace, podName)...)
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
			addons[index].PortalURL = portalAddonURL(cluster, addons[index].Name, addons[index].Namespace)
		}
	}
}
