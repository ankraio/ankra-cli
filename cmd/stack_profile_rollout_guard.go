package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// What a profile rollout would remove, and the guards in front of it
// (ankra-sgqurr).
//
// A rollout replaces each target stack with the version, and a member the
// version does not list is stopped and deleted. Before this, nothing listed
// those members, the fleet loop kept going after a failure, and a multi-
// cluster write started without a question. Now:
//
//   - --dry-run lists, per target, the members the version would remove,
//     read from the live stack (nothing is written; a target whose members
//     cannot be read says so instead of reporting nothing).
//   - a write to more than one stack asks first (--yes skips the question).
//   - the loop stops at the first target that fails; --continue-on-error
//     keeps going.
//   - the in-place lane removes, without asking, only a member the platform
//     can show the profile put there, and refuses any other removal unless it
//     is named with --confirm-removal. --via-apply cannot tell whose a member
//     is, so it refuses EVERY removal --confirm-removal does not name.

// confirmedRemovalKinds are the member kinds a removal confirmation names.
var confirmedRemovalKinds = map[string]bool{"addon": true, "manifest": true, "application": true}

// rolloutRemovalConfirmation is one --confirm-removal: a member as
// kind/name, scoped to one cluster when it names one.
type rolloutRemovalConfirmation struct {
	Cluster string
	Member  string
}

// rolloutConfirmations are the --confirm-removal values of a rollout.
type rolloutConfirmations []rolloutRemovalConfirmation

// parseConfirmedRemovals validates --confirm-removal values: [cluster:]kind/name,
// with kind addon, manifest or application. A confirmation confirms the
// removal of that member from one stack, so a rollout to more than one stack
// must say which cluster each confirmation is for: an unscoped one would
// authorise removing a same-named member on every stack in the run,
// including stacks whose plan nobody looked at.
func parseConfirmedRemovals(values []string, targets []rolloutTarget) (rolloutConfirmations, error) {
	confirmations := rolloutConfirmations{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		cluster := ""
		if scope, member, isScoped := strings.Cut(trimmed, ":"); isScoped {
			cluster, trimmed = strings.TrimSpace(scope), strings.TrimSpace(member)
		}
		kind, name, hasSeparator := strings.Cut(trimmed, "/")
		if !hasSeparator || strings.TrimSpace(name) == "" || !confirmedRemovalKinds[kind] {
			return nil, withExitCode(exitUsage, fmt.Errorf("--confirm-removal %q: name a member as kind/name (or cluster:kind/name), with kind addon, manifest or application", value))
		}
		if cluster == "" && len(targets) > 1 {
			return nil, withExitCode(exitUsage, fmt.Errorf("--confirm-removal %q: this rollout writes to %d stacks, so name the cluster the removal is for, as cluster:%s", value, len(targets), trimmed))
		}
		if cluster != "" && !rolloutTargetsCluster(targets, cluster) {
			return nil, withExitCode(exitUsage, fmt.Errorf("--confirm-removal %q: cluster %q is not one of this rollout's targets", value, cluster))
		}
		confirmations = append(confirmations, rolloutRemovalConfirmation{Cluster: cluster, Member: kind + "/" + name})
	}
	return confirmations, nil
}

func rolloutTargetsCluster(targets []rolloutTarget, cluster string) bool {
	for _, target := range targets {
		if strings.EqualFold(target.ClusterName, cluster) || target.ClusterID == cluster {
			return true
		}
	}
	return false
}

// forTarget lists the members confirmed for one target: the ones scoped to
// its cluster, and the unscoped ones (which a single-target rollout allows).
func (confirmations rolloutConfirmations) forTarget(target rolloutTarget) []string {
	members := []string{}
	for _, confirmation := range confirmations {
		if confirmation.Cluster == "" || strings.EqualFold(confirmation.Cluster, target.ClusterName) || confirmation.Cluster == target.ClusterID {
			members = append(members, confirmation.Member)
		}
	}
	return members
}

// stackMemberNames is the member listing shared by a profile version's stack
// and a live stack document: only the names, by kind.
type stackMemberNames struct {
	Addons []struct {
		Name string `json:"name"`
	} `json:"addons"`
	Manifests []struct {
		Name string `json:"name"`
	} `json:"manifests"`
	Applications []struct {
		Name string `json:"name"`
	} `json:"applications"`
}

func (members stackMemberNames) keys() map[string]bool {
	keys := map[string]bool{}
	for _, addon := range members.Addons {
		keys["addon/"+addon.Name] = true
	}
	for _, manifest := range members.Manifests {
		keys["manifest/"+manifest.Name] = true
	}
	for _, application := range members.Applications {
		keys["application/"+application.Name] = true
	}
	return keys
}

// rolloutRemovalPlan is what one target would lose: Removes when the live
// stack could be read, ReadError when it could not. A plan that could not be
// read is unknown, never "nothing".
type rolloutRemovalPlan struct {
	Removes   []string
	ReadError error
}

// versionMemberKeys reads the members the rolled-out version lists.
func versionMemberKeys(requestContext context.Context, profileID string, version int) (map[string]bool, error) {
	raw, getError := apiClient.GetStackProfileVersion(requestContext, profileID, version)
	if getError != nil {
		return nil, fmt.Errorf("reading profile version v%d: %w", version, getError)
	}
	var payload struct {
		Spec struct {
			Stacks []stackMemberNames `json:"stacks"`
		} `json:"spec"`
	}
	if unmarshalError := json.Unmarshal(raw, &payload); unmarshalError != nil {
		return nil, fmt.Errorf("parsing profile version v%d: %w", version, unmarshalError)
	}
	if len(payload.Spec.Stacks) != 1 {
		return nil, fmt.Errorf("profile version v%d carries %d stacks, not one", version, len(payload.Spec.Stacks))
	}
	return payload.Spec.Stacks[0].keys(), nil
}

// liveStackMemberKeys reads the members of the deployed stack a target
// names, from the cluster's stack listing. A name with no deployed stack, or
// more than one, is an error rather than an empty answer.
func liveStackMemberKeys(documents []client.ClusterStackDocument, stackName string) (map[string]bool, error) {
	var found []client.ClusterStackDocument
	for _, document := range documents {
		if document.Name() == stackName && !document.IsDraftOnly() {
			found = append(found, document)
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("the cluster lists %d deployed stacks named %q", len(found), stackName)
	}
	encoded, encodeError := json.Marshal(found[0])
	if encodeError != nil {
		return nil, fmt.Errorf("reading stack %q: %w", stackName, encodeError)
	}
	var members stackMemberNames
	if decodeError := json.Unmarshal(encoded, &members); decodeError != nil {
		return nil, fmt.Errorf("reading the members of stack %q: %w", stackName, decodeError)
	}
	return members.keys(), nil
}

// planRolloutRemovals reads, for every target, the members the version would
// remove from it. It writes nothing.
func planRolloutRemovals(requestContext context.Context, profileID string, version int, targets []rolloutTarget) []rolloutRemovalPlan {
	plans := make([]rolloutRemovalPlan, len(targets))
	versionKeys, versionError := versionMemberKeys(requestContext, profileID, version)
	if versionError != nil {
		for index := range plans {
			plans[index].ReadError = versionError
		}
		return plans
	}
	documentsByCluster := map[string][]client.ClusterStackDocument{}
	listErrors := map[string]error{}
	for index, target := range targets {
		documents, listed := documentsByCluster[target.ClusterID]
		if !listed && listErrors[target.ClusterID] == nil {
			listedDocuments, listError := apiClient.ListClusterStackDocuments(target.ClusterID)
			if listError != nil {
				listErrors[target.ClusterID] = listError
			} else {
				documentsByCluster[target.ClusterID] = listedDocuments
				documents = listedDocuments
			}
		}
		if listError := listErrors[target.ClusterID]; listError != nil {
			plans[index].ReadError = fmt.Errorf("listing the stacks of %s: %w", target.ClusterName, listError)
			continue
		}
		liveKeys, liveError := liveStackMemberKeys(documents, target.StackName)
		if liveError != nil {
			plans[index].ReadError = liveError
			continue
		}
		removes := []string{}
		for key := range liveKeys {
			if !versionKeys[key] {
				removes = append(removes, key)
			}
		}
		sort.Strings(removes)
		plans[index].Removes = removes
	}
	return plans
}

// describeRemovalPlan renders a plan for the dry-run table.
func describeRemovalPlan(plan rolloutRemovalPlan) string {
	switch {
	case plan.ReadError != nil:
		return "unknown (" + plan.ReadError.Error() + ")"
	case len(plan.Removes) == 0:
		return "nothing"
	default:
		return strings.Join(plan.Removes, ", ")
	}
}

// unconfirmedRemovals lists the removals of a plan the confirmations do not
// name.
func unconfirmedRemovals(plan rolloutRemovalPlan, confirmed []string) []string {
	confirmedKeys := map[string]bool{}
	for _, key := range confirmed {
		confirmedKeys[key] = true
	}
	unconfirmed := []string{}
	for _, key := range plan.Removes {
		if !confirmedKeys[key] {
			unconfirmed = append(unconfirmed, key)
		}
	}
	return unconfirmed
}

// confirmRolloutRemovalFlags renders --confirm-removal flags for a hint,
// scoped to the target's cluster.
func confirmRolloutRemovalFlags(clusterName string, members []string) string {
	flags := make([]string, 0, len(members))
	for _, member := range members {
		flags = append(flags, "--confirm-removal "+clusterName+":"+member)
	}
	return strings.Join(flags, " ")
}

// confirmMultiTargetRollout asks before a rollout writes to more than one
// stack. A single target is the operator's direct instruction and is not
// asked about; --yes skips the question, and a declined or unanswerable
// prompt (no terminal) cancels with exit code 4.
func confirmMultiTargetRollout(cmd *cobra.Command, profileName string, version int, targets []rolloutTarget, plans []rolloutRemovalPlan, yes bool) error {
	if len(targets) <= 1 || yes {
		return nil
	}
	clusters := map[string]bool{}
	removing := 0
	unknown := 0
	for index, target := range targets {
		clusters[target.ClusterID] = true
		switch {
		case plans[index].ReadError != nil:
			unknown++
		case len(plans[index].Removes) > 0:
			removing++
		}
	}
	message := fmt.Sprintf("\nRoll '%s' v%d out to %d %s on %d %s?", profileName, version,
		len(targets), pluralise(len(targets), "stack", "stacks"), len(clusters), pluralise(len(clusters), "cluster", "clusters"))
	if removing > 0 {
		message += fmt.Sprintf(" %d of them would lose members the version does not list (see --dry-run).", removing)
	}
	if unknown > 0 {
		message += fmt.Sprintf(" The members of %d could not be read.", unknown)
	}
	message += " The rollout stops at the first stack that fails. [y/N] "
	return confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), message, yes)
}

// describeRemovalRefusal renders the platform's refusal of unconfirmed
// removals, with the flags that would confirm them on that cluster.
func describeRemovalRefusal(clusterName string, refusal *client.MemberRemovalUnconfirmedError) string {
	message := refusal.Message
	if len(refusal.Members) > 0 {
		message += " To remove them anyway, roll out again with " + confirmRolloutRemovalFlags(clusterName, refusal.Members) + "."
	}
	return message
}

// asMemberRemovalRefusal unwraps the platform's removal refusal.
func asMemberRemovalRefusal(applyError error) (*client.MemberRemovalUnconfirmedError, bool) {
	var refusal *client.MemberRemovalUnconfirmedError
	if errors.As(applyError, &refusal) {
		return refusal, true
	}
	return nil, false
}
