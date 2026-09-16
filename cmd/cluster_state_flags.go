package cmd

// The stop/start state flags shared by every self-managed provider's
// stop and start commands (epic ankra-u3jsj): on the providers whose stop
// terminates the VMs, the backend first captures the cluster's state - an
// encrypted etcd snapshot - and the next start restores it, so Secrets,
// ConfigMaps, custom resources and persistent volume claims survive a stop.

import (
	"fmt"
	"strings"

	"ankra/internal/client"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

const preserveStateFlagUsage = "Cluster state across the stop: omit to let Ankra capture an encrypted etcd " +
	"snapshot whenever the provider and distribution support it (the VMs are terminated only once it is stored, " +
	"and the next start restores it); 'true' to require the capture (a stop whose state cannot be captured is " +
	"refused); 'false' to tear down without it (everything stored in the cluster itself is lost; cloud block " +
	"volumes are kept). --force never captures."

const restoreStateFlagUsage = "Cluster state on start: omit to restore the newest state snapshot captured at " +
	"stop when there is one; 'true' to require one (refused otherwise); 'false' to start a fresh cluster."

// preserveStateFlag reads --preserve-state as the three-state choice the API
// takes: unset is nil (backend default), "true"/"false" the two answers.
func preserveStateFlag(cmd *cobra.Command) *bool {
	return threeStateFlag(cmd, "preserve-state")
}

// restoreStateFlag reads --restore-state the same way.
func restoreStateFlag(cmd *cobra.Command) *bool {
	return threeStateFlag(cmd, "restore-state")
}

func threeStateFlag(cmd *cobra.Command, name string) *bool {
	raw, _ := cmd.Flags().GetString(name)
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "yes", "on":
		value := true
		return &value
	case "false", "no", "off":
		value := false
		return &value
	default:
		return nil
	}
}

// printStopStateOutcome tells the operator what happened to the cluster's
// state, after the stop's cluster and operation lines.
func printStopStateOutcome(statePreserved bool, snapshot *client.StateSnapshotRef, message string) {
	if statePreserved {
		fmt.Println(text.FgGreen.Sprint("  Cluster state: preserved - an encrypted etcd snapshot is captured first; the VMs are terminated once it is stored, and the next start restores it."))
		if snapshot != nil {
			fmt.Printf("  State snapshot: %s (execution %s)\n", snapshot.ID, snapshot.ExecutionID)
		}
	} else if message != "" {
		fmt.Println(text.FgYellow.Sprint("  Cluster state: " + message))
	}
}

// printStartStateOutcome mirrors it for a start.
func printStartStateOutcome(stateRestore string, snapshotID *string) {
	switch stateRestore {
	case "requested":
		line := "  Cluster state: the first control plane restores the snapshot captured at stop"
		if snapshotID != nil {
			line += " (" + *snapshotID + ")"
		}
		fmt.Println(text.FgGreen.Sprint(line + "."))
	case "skipped":
		fmt.Println("  Cluster state: starting fresh, the captured snapshot is not restored.")
	case "none":
		fmt.Println("  Cluster state: no captured snapshot to restore; the cluster starts fresh.")
	}
}
