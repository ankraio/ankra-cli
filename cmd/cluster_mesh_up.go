package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// mesh up: the whole path from "these clusters" to "one mesh", driven from
// readiness. Each pass reads readiness for the set, prepares every cluster
// that only needs a make-ready (identity, overlay, and with --renumber-pods
// a fresh pod range), joins every cluster that is ready, and stops with the
// reason for anything a running cluster cannot be fixed into. Re-running is
// safe: members are skipped, a make-ready re-arms, a join is idempotent.

// Readiness item names the planner reasons about; they mirror the
// platform's stable identifiers.
const (
	meshItemIdentity  = "network_identity"
	meshItemTransport = "transport"
	meshItemRanges    = "ranges"
)

// mesh up action kinds.
const (
	meshUpMember    = "member"
	meshUpJoin      = "join"
	meshUpMakeReady = "make-ready"
	meshUpRenumber  = "renumber"
	meshUpWait      = "wait"
	meshUpBlocked   = "blocked"
)

// meshUpAction is one step of a pass for one cluster.
type meshUpAction struct {
	Kind      string
	ClusterID string
	Label     string
	Reason    string
}

// planMeshUp decides, from one readiness answer, what each cluster in order
// needs. A cluster already in the mesh is left alone; a ready one joins; a
// cluster whose only failures are the identity or the transport gets a
// make-ready; an overlapping pod range is fixed by renumbering every
// colliding cluster AFTER the first one in the order - the first keeps its
// range - and only when the caller allowed it, because a renumber drains
// and re-registers every node once; anything a running cluster cannot be
// fixed into is blocked with the platform's reasons.
func planMeshUp(order []string, labels map[string]string, readiness map[string]client.ClusterMeshReadiness, members map[string]bool, renumber bool) []meshUpAction {
	actions := make([]meshUpAction, 0, len(order))
	for index, clusterID := range order {
		label := labels[clusterID]
		if members[clusterID] {
			actions = append(actions, meshUpAction{Kind: meshUpMember, ClusterID: clusterID, Label: label})
			continue
		}
		result, isKnown := readiness[clusterID]
		if !isKnown {
			actions = append(actions, meshUpAction{Kind: meshUpBlocked, ClusterID: clusterID, Label: label, Reason: "the platform reported no readiness for it"})
			continue
		}
		if result.Ready {
			actions = append(actions, meshUpAction{Kind: meshUpJoin, ClusterID: clusterID, Label: label})
			continue
		}
		var fatal []string
		needsMakeReady, rangesOverlap, otherRemediable := false, false, false
		for _, item := range result.Items {
			if item.Ready {
				continue
			}
			switch {
			case !item.Remediable:
				fatal = append(fatal, item.Name+": "+strings.TrimSpace(item.Detail))
			case item.Name == meshItemIdentity || item.Name == meshItemTransport:
				needsMakeReady = true
			case item.Name == meshItemRanges:
				rangesOverlap = true
			default:
				otherRemediable = true
			}
		}
		if len(fatal) > 0 {
			actions = append(actions, meshUpAction{Kind: meshUpBlocked, ClusterID: clusterID, Label: label,
				Reason: "cannot be fixed on a running cluster - " + strings.Join(fatal, "; ")})
			continue
		}
		if rangesOverlap && index > 0 {
			if !renumber {
				actions = append(actions, meshUpAction{Kind: meshUpBlocked, ClusterID: clusterID, Label: label,
					Reason: "its pod range overlaps another member's; rerun with --renumber-pods to move it onto a fresh range " +
						"(every node of this cluster is drained and re-registered once)"})
				continue
			}
			actions = append(actions, meshUpAction{Kind: meshUpRenumber, ClusterID: clusterID, Label: label,
				Reason: "pod range overlaps another member's"})
			continue
		}
		if needsMakeReady {
			actions = append(actions, meshUpAction{Kind: meshUpMakeReady, ClusterID: clusterID, Label: label,
				Reason: "needs its network identity and the overlay"})
			continue
		}
		reason := "waiting for its nodes to converge"
		if rangesOverlap {
			reason = "keeps its pod range; the colliding member is renumbered"
		} else if otherRemediable {
			reason = "waiting for a remediable check to clear"
		}
		actions = append(actions, meshUpAction{Kind: meshUpWait, ClusterID: clusterID, Label: label, Reason: reason})
	}
	return actions
}

// findClusterMesh answers the organisation's mesh named by id, slug or name.
func findClusterMesh(nameOrID string) (*client.ClusterMesh, error) {
	meshes, listError := apiClient.ListClusterMeshes()
	if listError != nil {
		return nil, fmt.Errorf("listing cluster meshes: %w", listError)
	}
	for _, mesh := range meshes {
		if mesh.ID == nameOrID || mesh.Slug == nameOrID || strings.EqualFold(mesh.Name, nameOrID) {
			found := mesh
			return &found, nil
		}
	}
	return nil, nil
}

var (
	clusterMeshUpRenumber bool
	clusterMeshUpPlan     bool
	clusterMeshUpWait     time.Duration
	clusterMeshUpInterval time.Duration
)

var clusterMeshUpCmd = &cobra.Command{
	Use:   "up <mesh_name|mesh_id> <cluster_id|name> [cluster_id|name...]",
	Short: "Prepare clusters and join them into one mesh, end to end",
	Long: "Bring a set of clusters into one mesh from wherever they are: read their readiness, run make-ready on " +
		"every cluster that only lacks its network identity or the overlay, join every cluster that is ready, and " +
		"keep going until each member reports ready or --wait runs out. The mesh is created when no mesh of that " +
		"name exists.\n\n" +
		"Two clusters prepared from the kubeadm default share one pod range and cannot mesh with each other; " +
		"--renumber-pods lets `up` move every colliding cluster after the first onto a fresh range from the " +
		"organisation's pool. That drains and re-registers each of their nodes once, so it is never done without the flag.\n\n" +
		"--plan prints what a pass would do and changes nothing. Re-running is safe: members are skipped and a " +
		"make-ready re-arms a cluster whose nodes did not converge.",
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		order := make([]string, 0, len(args)-1)
		labels := map[string]string{}
		for _, arg := range args[1:] {
			clusterID, resolveError := resolveClusterArg(arg)
			if resolveError != nil {
				return resolveError
			}
			order = append(order, clusterID)
			labels[clusterID] = clusterTarget(arg, clusterID)
		}
		mesh, findError := findClusterMesh(args[0])
		if findError != nil {
			return findError
		}
		justCreated := false
		if mesh == nil {
			if clusterMeshUpPlan {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "plan: create mesh '%s'\n", args[0])
			} else {
				created, createError := apiClient.CreateClusterMesh(args[0])
				if createError != nil {
					return fmt.Errorf("creating cluster mesh: %w", createError)
				}
				mesh = created
				justCreated = true
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created cluster mesh '%s' (%s).\n", mesh.Name, mesh.ID)
			}
		}
		deadline := time.Now().Add(clusterMeshUpWait)
		for pass := 1; ; pass++ {
			members := map[string]bool{}
			// A mesh created a moment ago has no members to read back.
			if mesh != nil && (!justCreated || pass != 1) {
				current, getError := apiClient.GetClusterMesh(mesh.ID)
				if getError != nil {
					return fmt.Errorf("reading cluster mesh: %w", getError)
				}
				mesh = current
				for _, member := range mesh.Members {
					members[member.ClusterID] = true
				}
			}
			readiness, readinessError := apiClient.CheckClusterMeshReadiness(order)
			if readinessError != nil {
				return fmt.Errorf("checking cluster mesh readiness: %w", readinessError)
			}
			actions := planMeshUp(order, labels, readiness, members, clusterMeshUpRenumber)
			if clusterMeshUpPlan {
				printMeshUpPlan(cmd, actions)
				return nil
			}
			blocked, pending, applyError := applyMeshUpPass(cmd, mesh, actions)
			if applyError != nil {
				return applyError
			}
			if len(blocked) > 0 {
				return fmt.Errorf("%d cluster(s) cannot join mesh %s: %s", len(blocked), mesh.Slug, strings.Join(blocked, "; "))
			}
			if !pending {
				if settled, waitError := waitForMeshMembersReady(cmd, mesh.ID, deadline); waitError != nil {
					return waitError
				} else if settled {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Mesh %s: every member is ready.\n", mesh.Slug)
					return nil
				}
				return fmt.Errorf("mesh %s: members are still configuring after %s; `ankra cluster mesh show %s` reports their progress",
					mesh.Slug, clusterMeshUpWait, mesh.ID)
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("mesh %s: clusters were still converging after %s; rerun `ankra cluster mesh up` to continue",
					mesh.Slug, clusterMeshUpWait)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "pass %d: waiting %s for the clusters to converge\n", pass, clusterMeshUpInterval)
			time.Sleep(clusterMeshUpInterval)
		}
	},
}

func printMeshUpPlan(cmd *cobra.Command, actions []meshUpAction) {
	for _, action := range actions {
		switch action.Kind {
		case meshUpMember:
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "plan: %s is already a member\n", action.Label)
		case meshUpJoin:
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "plan: join %s\n", action.Label)
		case meshUpMakeReady:
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "plan: make-ready %s (%s)\n", action.Label, action.Reason)
		case meshUpRenumber:
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "plan: make-ready %s --pod-cidr auto (%s; every node drains and re-registers once)\n", action.Label, action.Reason)
		case meshUpWait:
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "plan: wait for %s (%s)\n", action.Label, action.Reason)
		case meshUpBlocked:
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "plan: %s is blocked - %s\n", action.Label, action.Reason)
		}
	}
}

// applyMeshUpPass runs one pass's actions and answers the blocked clusters
// and whether anything is still pending (a make-ready, a renumber or a wait
// means another pass is needed).
func applyMeshUpPass(cmd *cobra.Command, mesh *client.ClusterMesh, actions []meshUpAction) (blocked []string, pending bool, applyError error) {
	for _, action := range actions {
		switch action.Kind {
		case meshUpMember:
			continue
		case meshUpJoin:
			if joinError := apiClient.JoinClusterMesh(mesh.ID, action.ClusterID); joinError != nil {
				return nil, false, fmt.Errorf("joining %s: %w", action.Label, joinError)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cluster %s joined mesh %s.\n", action.Label, mesh.Slug)
		case meshUpMakeReady, meshUpRenumber:
			podCIDR := ""
			if action.Kind == meshUpRenumber {
				podCIDR = "auto"
			}
			result, makeReadyError := apiClient.MakeClusterMeshReady(action.ClusterID, "", podCIDR)
			if makeReadyError != nil {
				blocked = append(blocked, action.Label+": make-ready refused - "+makeReadyError.Error())
				continue
			}
			pending = true
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cluster %s: %s; cilium-id=%d name=%s; %d resources converging",
				action.Label, action.Reason, result.CiliumClusterID, result.CiliumClusterName, result.TransitionedResources)
			if result.PodCIDRChanged {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "; pod range moved to %s (nodes re-register one at a time)", result.PodCIDR)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), ".")
		case meshUpWait:
			pending = true
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cluster %s: %s.\n", action.Label, action.Reason)
		case meshUpBlocked:
			blocked = append(blocked, action.Label+": "+action.Reason)
		}
	}
	return blocked, pending, nil
}

// waitForMeshMembersReady polls the mesh until every member reports ready
// or the deadline passes; a zero --wait answers after one read.
func waitForMeshMembersReady(cmd *cobra.Command, meshID string, deadline time.Time) (bool, error) {
	for {
		mesh, getError := apiClient.GetClusterMesh(meshID)
		if getError != nil {
			return false, fmt.Errorf("reading cluster mesh: %w", getError)
		}
		statuses := map[string]int{}
		allReady := len(mesh.Members) > 0
		for _, member := range mesh.Members {
			statuses[member.Status]++
			if member.Status != "ready" {
				allReady = false
			}
		}
		if allReady {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		keys := make([]string, 0, len(statuses))
		for status := range statuses {
			keys = append(keys, status)
		}
		sort.Strings(keys)
		summary := make([]string, 0, len(keys))
		for _, status := range keys {
			summary = append(summary, fmt.Sprintf("%d %s", statuses[status], status))
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mesh %s: members %s; waiting %s\n", mesh.Slug, strings.Join(summary, ", "), clusterMeshUpInterval)
		time.Sleep(clusterMeshUpInterval)
	}
}

func init() {
	clusterMeshUpCmd.Flags().BoolVar(&clusterMeshUpRenumber, "renumber-pods", false,
		"Move every colliding cluster after the first onto a fresh pod range from the organisation's pool (drains and re-registers each of its nodes once)")
	clusterMeshUpCmd.Flags().BoolVar(&clusterMeshUpPlan, "plan", false,
		"Print what one pass would do and change nothing")
	clusterMeshUpCmd.Flags().DurationVar(&clusterMeshUpWait, "wait", 45*time.Minute,
		"How long to keep passing over the clusters and waiting for members to report ready (0 runs one pass)")
	clusterMeshUpCmd.Flags().DurationVar(&clusterMeshUpInterval, "interval", 30*time.Second,
		"Pause between passes")
	clusterMeshCmd.AddCommand(clusterMeshUpCmd)
}
