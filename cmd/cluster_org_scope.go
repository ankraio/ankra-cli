package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"
)

// The platform resolves a cluster reference inside the organisation the
// request is scoped to, so a perfectly real cluster owned by another
// organisation is indistinguishable from a typo: the backend answers 404
// "Cluster not found". That message blames the cluster, which sends people
// looking at access grants and RBAC when the actual problem is which
// organisation the CLI is selected on — a genuine trap on a machine that
// works across organisations, and the one that bites hardest through the
// kubeconfig exec credential plugin, where nobody typed an organisation at
// all.
//
// The kube-gateway commands (kube-token, kubeconfig, access) therefore look a
// cluster ID up across every organisation the caller belongs to, re-scope the
// request to the owner, and — when the cluster really is nowhere — say which
// organisations were searched instead of repeating the bare 404.

// organisationScopeSearch is the outcome of looking for a cluster ID outside
// the organisation this invocation is scoped to.
type organisationScopeSearch struct {
	// cluster and owner are only meaningful when found is true.
	cluster client.ClusterListItem
	owner   client.OrganisationSummary
	found   bool

	// scopedLabel names the organisation the invocation was scoped to.
	scopedLabel string

	// searchedLabels names the other organisations that were searched, in
	// listing order.
	searchedLabels []string

	// err records why the search could not be completed. A search that could
	// not run proves nothing about the cluster, so callers must fall back to
	// their previous behaviour rather than declaring the cluster non-existent.
	err error
}

// findClusterInOtherOrganisations looks for clusterID in every organisation
// the caller belongs to except the one already in scope (which the caller has
// just tried without success).
//
// Only IDs are searched. Cluster names are unique only within an
// organisation, so resolving one across organisations would mean guessing
// which of several same-named clusters was meant.
//
// The client's organisation scope is restored before returning, so a match
// never silently re-targets later calls; adoptOwningOrganisation is what
// commits to the owning organisation.
func findClusterInOtherOrganisations(clusterID string) organisationScopeSearch {
	search := organisationScopeSearch{scopedLabel: scopedOrganisationLabel()}
	if !isLikelyClusterID(clusterID) {
		search.err = fmt.Errorf("%q is not a cluster ID", clusterID)
		return search
	}
	organisations, err := apiClient.ListOrganisations()
	if err != nil {
		search.err = err
		return search
	}

	scopedID := effectiveOrganisationID()
	for _, organisation := range organisations {
		if organisation.OrganisationID != "" && organisation.OrganisationID == scopedID {
			search.scopedLabel = organisationLabel(organisation)
			break
		}
	}

	restore := apiClient.OrganisationOverride()
	defer apiClient.SetOrganisationOverride(restore)
	for _, organisation := range organisations {
		if organisation.OrganisationID == "" || organisation.OrganisationID == scopedID {
			continue
		}
		search.searchedLabels = append(search.searchedLabels, organisationLabel(organisation))
		apiClient.SetOrganisationOverride(organisation.OrganisationID)
		cluster, lookupErr := apiClient.GetClusterByID(clusterID)
		if lookupErr != nil || cluster.ID != clusterID {
			continue
		}
		search.cluster = cluster
		search.owner = organisation
		search.found = true
		break
	}
	return search
}

// adoptOwningOrganisation re-scopes this invocation to the organisation that
// owns clusterID when the organisation in scope does not have it. The caller
// named one specific cluster by ID and holds a grant on it, so the only scope
// in which that request means anything is the owning organisation.
//
// This is what keeps a kubeconfig context working across an 'ankra org
// switch' even when the exec args predate the pinned --org. It changes only
// this process's scope: the persistently selected organisation, which other
// terminals and sessions share, is never touched.
//
// notify, when non-nil, receives a one-line note about the re-scoping. The
// kube-token credential plugin passes nil: kubectl runs it on every command,
// so a note there would be per-invocation noise on stderr.
func adoptOwningOrganisation(clusterID string, notify io.Writer) (organisationScopeSearch, bool) {
	search := findClusterInOtherOrganisations(clusterID)
	if !search.found {
		return search, false
	}
	apiClient.SetOrganisationOverride(search.owner.OrganisationID)
	if notify != nil {
		_, _ = fmt.Fprintf(notify, "Note: cluster %s is in organisation %s, not %s; running against the owning organisation.\n",
			clusterID, organisationLabel(search.owner), search.scopedLabel)
	}
	return search, true
}

// notInScopedOrganisationError explains a cluster the scoped organisation
// does not have, naming the organisations involved instead of repeating the
// backend's bare "Cluster not found". cause, when non-nil, stays in the error
// chain so exit-code classification still sees the original response.
func notInScopedOrganisationError(search organisationScopeSearch, reference string, cause error) error {
	var message strings.Builder
	fmt.Fprintf(&message, "cluster %s is not in organisation %s", reference, search.scopedLabel)
	switch {
	case search.found:
		fmt.Fprintf(&message, "; it belongs to %s.\nRetry scoped to the owning organisation:\n  ankra --org %s <command>   (or ANKRA_ORG=%s)",
			organisationLabel(search.owner), search.owner.OrganisationID, search.owner.OrganisationID)
	case search.err != nil:
		fmt.Fprintf(&message, " (the other organisations you belong to could not be checked: %v)", search.err)
	case len(search.searchedLabels) > 0:
		fmt.Fprintf(&message, ", and it was not found in the %d other organisation(s) you belong to: %s",
			len(search.searchedLabels), strings.Join(search.searchedLabels, ", "))
	default:
		message.WriteString(", the only organisation you belong to")
	}
	if cause != nil {
		return fmt.Errorf("%w\n%s", cause, message.String())
	}
	return errors.New(message.String())
}

// effectiveOrganisationID returns the organisation ID this invocation is
// scoped to: the resolved --org/ANKRA_ORG override when one was applied,
// otherwise the persistently selected organisation. Returns "" when no
// organisation can be determined.
func effectiveOrganisationID() string {
	if override := apiClient.OrganisationOverride(); override != "" {
		return override
	}
	if orgID, err := resolveOrganisationID(); err == nil {
		return orgID
	}
	return ""
}

// scopedOrganisationLabel names the organisation this invocation runs
// against, without an API call — error messages must not depend on a second
// request succeeding.
func scopedOrganisationLabel() string {
	if override := apiClient.OrganisationOverride(); override != "" {
		return override
	}
	if selected, err := loadSelectedOrganisation(); err == nil && selected.OrganisationID != "" {
		if name := trimmedOptional(selected.Name); name != "" {
			return fmt.Sprintf("%q (%s)", name, selected.OrganisationID)
		}
		return selected.OrganisationID
	}
	return "the selected organisation"
}

// organisationLabel renders an organisation as name-and-ID, falling back to
// the slug and then the bare ID. The ID is always present because it is what
// the user has to paste into --org.
func organisationLabel(organisation client.OrganisationSummary) string {
	if name := trimmedOptional(organisation.Name); name != "" {
		return fmt.Sprintf("%q (%s)", name, organisation.OrganisationID)
	}
	if slug := trimmedOptional(organisation.Slug); slug != "" {
		return fmt.Sprintf("%q (%s)", slug, organisation.OrganisationID)
	}
	return organisation.OrganisationID
}

// trimmedOptional dereferences a nullable API string, yielding "" for both a
// nil pointer and a whitespace-only value so callers can test one thing.
func trimmedOptional(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
