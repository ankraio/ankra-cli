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

// clusterLookup is the answer to "does the organisation in scope have this
// cluster?". It has three states, not two, and that is the point: this bug
// was written three separate times (the search loop, the in-scope retry, and
// the kubeconfig target lookup) because a (cluster, error) pair spells "the
// backend said no" and "the backend said nothing" identically, and `err !=
// nil` reads naturally as absence at every one of those call sites.
type clusterLookup struct {
	cluster client.ClusterListItem

	// present is true only when the backend answered and the cluster was in
	// the answer.
	present bool

	// unanswered holds the reason the lookup could not be completed. While it
	// is non-nil, present being false means nothing at all.
	unanswered error
}

// found reports that the cluster is definitely in the scoped organisation.
func (l clusterLookup) found() bool { return l.present }

// absent reports that the backend answered and the cluster was not in the
// answer. It is deliberately not the negation of found: a lookup that never
// completed is neither.
func (l clusterLookup) absent() bool { return !l.present && l.unanswered == nil }

// lookUpClusterInScope asks the organisation in scope for a cluster ID.
//
// This is the only place in the gateway flow permitted to turn a
// (cluster, error) pair into a judgement about existence. Everything else
// reads found()/absent(), so "we did not get an answer" cannot be spelled the
// same way as "there is no such cluster" — which is the whole failure mode
// this file exists to remove, and which kept reappearing while each call site
// classified the error itself.
func lookUpClusterInScope(clusterID string) clusterLookup {
	cluster, err := apiClient.GetClusterByID(clusterID)
	switch {
	case err == nil && cluster.ID == clusterID:
		return clusterLookup{cluster: cluster, present: true}
	case err == nil, errors.Is(err, client.ErrClusterNotFound):
		return clusterLookup{}
	default:
		return clusterLookup{unanswered: err}
	}
}

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

	// A lookup that failed is not a miss. Counting one as a miss would put the
	// organisation in searchedLabels and let the caller report "searched
	// everywhere, found nothing" about an organisation that never answered.
	var firstFailure error
	for _, organisation := range organisations {
		if organisation.OrganisationID == "" || organisation.OrganisationID == scopedID {
			continue
		}
		apiClient.SetOrganisationOverride(organisation.OrganisationID)
		switch answer := lookUpClusterInScope(clusterID); {
		case answer.found():
			search.cluster = answer.cluster
			search.owner = organisation
			search.found = true
			return search
		case answer.absent():
			search.searchedLabels = append(search.searchedLabels, organisationLabel(organisation))
		default:
			if firstFailure == nil {
				firstFailure = fmt.Errorf("%s: %w", organisationLabel(organisation), answer.unanswered)
			}
		}
	}
	if firstFailure != nil {
		search.err = firstFailure
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
			clusterID, organisationLabel(search.owner), scopedOrganisationPhrase(search.scopedLabel))
	}
	return search, true
}

// resolveOwningOrganisation is the entry point the gateway commands use: it
// adopts the organisation that owns clusterID, except when the caller pinned
// one explicitly with --org or ANKRA_ORG. An explicit pin is a statement about
// which organisation to use, so it is never silently replaced; the search
// still runs, but only so the error can name the owner and the exact retry.
func resolveOwningOrganisation(clusterID string, notify io.Writer) (organisationScopeSearch, bool) {
	if apiClient.OrganisationOverride() != "" {
		return findClusterInOtherOrganisations(clusterID), false
	}
	return adoptOwningOrganisation(clusterID, notify)
}

// clusterOrganisationResolution settles which organisation a cluster ID
// should be requested in. It carries the same three-state discipline as
// clusterLookup, one level up: settled() and absent() are separate questions,
// and neither is the negation of the other.
type clusterOrganisationResolution struct {
	// cluster and organisationID are populated when settled() is true.
	cluster        client.ClusterListItem
	organisationID string

	// inScope: the organisation already in scope has the cluster.
	inScope bool
	// adopted: another organisation owns it and the client was re-scoped.
	adopted bool
	// absent: every organisation answered, and none has it.
	absent bool

	search organisationScopeSearch
}

// settled reports that the owning organisation is known, either because it
// was already in scope or because the client was re-scoped to it. A mint that
// fails after this is not an organisation-scoping problem.
func (r clusterOrganisationResolution) settled() bool { return r.inScope || r.adopted }

// resolveClusterOrganisation is the single door the gateway commands use to
// decide which organisation a cluster ID belongs to.
//
// Exactly one of three things is true when it returns, and a caller that
// handles only two has the bug this file is about:
//   - settled():   the owner is known (in scope, or adopted).
//   - absent:      every organisation answered and none has the cluster.
//   - neither:     nothing answered usefully. Unknown is not absence, so the
//     caller must fall back to letting the backend answer for the ID, which
//     is the behaviour that predates the cross-organisation lookup.
func resolveClusterOrganisation(clusterID string, notify io.Writer) clusterOrganisationResolution {
	if answer := lookUpClusterInScope(clusterID); answer.found() {
		return clusterOrganisationResolution{
			cluster:        answer.cluster,
			organisationID: answer.cluster.OrganisationID,
			inScope:        true,
		}
	}
	search, adopted := resolveOwningOrganisation(clusterID, notify)
	if adopted {
		return clusterOrganisationResolution{
			cluster:        search.cluster,
			organisationID: search.owner.OrganisationID,
			adopted:        true,
			search:         search,
		}
	}
	if search.err != nil {
		return clusterOrganisationResolution{search: search}
	}
	// The first lookup may have failed for a reason that says nothing about
	// the cluster. The search has just proved the API is reachable, so ask the
	// organisation in scope once more before settling on absent.
	switch retry := lookUpClusterInScope(clusterID); {
	case retry.found():
		return clusterOrganisationResolution{
			cluster:        retry.cluster,
			organisationID: retry.cluster.OrganisationID,
			inScope:        true,
			search:         search,
		}
	case retry.absent():
		return clusterOrganisationResolution{absent: true, search: search}
	default:
		// The organisation in scope still has not answered, so its silence is
		// not an absence either.
		return clusterOrganisationResolution{search: search}
	}
}

// notInScopedOrganisationError explains a cluster the scoped organisation
// does not have, naming the organisations involved instead of repeating the
// backend's bare "Cluster not found". cause, when non-nil, stays in the error
// chain so exit-code classification still sees the original response.
func notInScopedOrganisationError(search organisationScopeSearch, reference string, cause error) error {
	var message strings.Builder
	fmt.Fprintf(&message, "cluster %s is not in %s", reference, scopedOrganisationPhrase(search.scopedLabel))
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
// request succeeding. Returns "" when nothing local identifies it.
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
	return ""
}

// scopedOrganisationPhrase renders a scope label for prose, so an
// unidentifiable scope reads as a sentence instead of naming an organisation
// called "the selected organisation".
func scopedOrganisationPhrase(label string) string {
	if label == "" {
		return "the organisation this command is scoped to"
	}
	return "organisation " + label
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
