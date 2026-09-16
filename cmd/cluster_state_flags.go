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

const stopModeFlagUsage = "How the stop leaves the VMs: 'delete_resources' (default) terminates them (capturing the " +
	"cluster state first where supported); 'pause' powers the servers off and keeps them with their disks, so " +
	"etcd, local data and node identities survive and start powers them back on - compute and storage keep " +
	"billing while paused. Pause is available for k3s clusters on Hetzner, UpCloud and DigitalOcean (a stop on " +
	"AWS and Scaleway always pauses), cannot be combined with --force, and takes no --preserve-state."

// stopModeFlag reads --mode, normalised; anything but delete_resources or
// pause is refused here so a typo never reaches the API as a teardown.
func stopModeFlag(cmd *cobra.Command) (string, error) {
	raw, _ := cmd.Flags().GetString("mode")
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "delete_resources", "pause":
		return mode, nil
	}
	return "", fmt.Errorf("invalid --mode %q: must be delete_resources or pause", raw)
}

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
func printStopStateOutcome(statePreserved bool, snapshot *client.StateSnapshotRef, message string, stopMode string) {
	if stopMode == "pause" {
		fmt.Println(text.FgGreen.Sprint("  Stop mode: pause - the servers are powered off and kept with their disks; start powers them back on. Compute and storage keep billing while paused."))
	}
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
