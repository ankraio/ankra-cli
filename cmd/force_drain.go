package cmd

import (
	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// forceDrainFlag is the flag every worker-removing write takes to remove a
// node whose drain its pods' PodDisruptionBudgets refuse (force_drain on the
// wire, client.DrainOptions in the client). It is off by default and applies
// to the one request that carries it.
const forceDrainFlag = "force-drain"

// Flag usages for --force-drain. The removal writes (worker and node-group
// scale-downs, node-group delete) would otherwise keep a node whose drain is
// refused; the instance-type change drains each node before power-cycling
// it rather than removing it; the control-plane instance-type change drains
// each controller only on its rolling lane, so a stopped cluster ignores it.
const (
	forceDrainScaleUsage = "Bypass PodDisruptionBudgets for the nodes a scale-down removes: a node is removed " +
		"even if its pods' disruption budget refuses the drain, instead of being kept. Use it only for a node " +
		"you have decided to lose. Has no effect on a scale-up"
	forceDrainDeleteUsage = "Bypass PodDisruptionBudgets: each node of the group is removed even if its pods' " +
		"disruption budget refuses the drain, instead of being kept. Use it only for nodes you have decided to lose"
	forceDrainResizeUsage = "Bypass PodDisruptionBudgets when draining each node before its resize: a node is " +
		"power-cycled even if its pods' disruption budget refuses the drain, so those pods lose the " +
		"availability their budget protects. Use it only when you accept that disruption"
	forceDrainControlPlaneResizeUsage = "Bypass PodDisruptionBudgets for the rolling resize's drain of each " +
		"control plane: pods whose disruption budget refuses eviction are evicted anyway. It only matters for a " +
		"live rolling resize of a running cluster and has no effect on a stopped cluster's offline resize"
)

// controlPlaneForceDrainHelp is the Long-help paragraph of the control-plane
// instance-type change. Unlike the worker writes, this one removes nothing:
// only its rolling lane drains, and only that lane reads force_drain.
const controlPlaneForceDrainHelp = `On a running cluster the rolling resize drains each controller before resizing
it, honouring its pods' PodDisruptionBudgets. --force-drain bypasses them for
that drain, so pods whose budget refuses eviction are evicted anyway. It
applies to this request only, and only a live rolling resize reads it: a
stopped cluster's offline resize drains nothing and ignores it, and so does a
request the rolling resize refuses (fewer than three controllers) or has
nothing to do for (the controllers already run that type).`

// guardedDrainHelp is the Long-help paragraph shared by the commands that
// remove workers: what a refused drain does by default, and what
// --force-drain changes.
const guardedDrainHelp = `Each node removed is drained first, honouring its pods' PodDisruptionBudgets. A
node whose drain is refused (a budget allows no eviction, or the node cannot be
drained at all) is kept in service instead of removed, and a notice on the
cluster says why. Give the blocking workloads eviction headroom (more replicas
or a looser budget) and run the command again, or pass --force-drain to remove
the node anyway without honouring its PodDisruptionBudget. --force-drain
applies to this request only; use it only for a node you have decided to lose.`

// registerForceDrainFlag adds --force-drain (default false) to command.
func registerForceDrainFlag(command *cobra.Command, usage string) {
	command.Flags().Bool(forceDrainFlag, false, usage)
}

// drainOptionsFromFlags reads --force-drain into the client's drain options.
// A command without the flag gets the zero value, which sends no force_drain.
func drainOptionsFromFlags(command *cobra.Command) client.DrainOptions {
	forceDrain, _ := command.Flags().GetBool(forceDrainFlag)
	return client.DrainOptions{ForceDrain: forceDrain}
}
