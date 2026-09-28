package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// volumeDeletingKinds are the cloud cluster kinds whose deprovision deletes
// the persistent volumes the cluster's CSI driver provisioned. Proxmox and
// Morpheus disks go with their VMs, so they have no volumes to name and never
// need the acknowledgement.
var volumeDeletingKinds = map[cloudClusterKind]bool{
	cloudClusterKindHetzner:      true,
	cloudClusterKindOvh:          true,
	cloudClusterKindUpcloud:      true,
	cloudClusterKindDigitalocean: true,
	cloudClusterKindScaleway:     true,
	cloudClusterKindAws:          true,
}

// deprovisionVolumeNameLimit is how many volumes the warning spells out.
const deprovisionVolumeNameLimit = 10

// acceptVolumeDataLossFlag is the flag a deprovision reads the acknowledgement
// from; the platform's field is accept_volume_data_loss.
const acceptVolumeDataLossFlag = "accept-volume-data-loss"

// registerAcceptVolumeDataLossFlag declares the acknowledgement on a
// deprovision command.
func registerAcceptVolumeDataLossFlag(cmd *cobra.Command) {
	cmd.Flags().Bool(acceptVolumeDataLossFlag, false,
		"Accept that deprovisioning deletes the cluster's persistent volumes (the cloud volumes its CSI driver "+
			"provisioned) and the data on them, forced or not. Required when the cluster has such volumes, or Ankra "+
			"cannot list them, and you are not answering the prompt on a terminal; --yes does not imply it. A cluster "+
			"whose retention_policy is retain (AWS, Scaleway) keeps its volumes and needs no acknowledgement")
}

// deprovisionVolumeList names the volumes, capped, with the remainder counted.
func deprovisionVolumeList(volumes []client.DeprovisionVolume) string {
	shown := make([]string, 0, min(len(volumes), deprovisionVolumeNameLimit))
	for _, volume := range volumes[:min(len(volumes), deprovisionVolumeNameLimit)] {
		shown = append(shown, volume.Label())
	}
	named := strings.Join(shown, ", ")
	if more := len(volumes) - len(shown); more > 0 {
		named += fmt.Sprintf(" and %d more", more)
	}
	return named
}

// acknowledgeVolumeDataLoss runs before a cloud cluster is deprovisioned. It
// reads the persistent volumes the deprovision deletes and, when there are
// some or the platform cannot list them, needs the loss accepted: by
// --accept-volume-data-loss, or by answering the prompt on a terminal.
// Anywhere else it fails naming the volumes, because the platform refuses the
// deprovision without the acknowledgement. A reading that could not be made
// is unknown, never "no volumes": it asks too, and so does any answer whose
// consent_required the platform set. Volumes the cluster's retention policy
// keeps are named as kept and need nothing. The notes go to
// stderr so --output json|yaml stays parseable. It answers whether the
// deprovision carries the acknowledgement.
func acknowledgeVolumeDataLoss(cmd *cobra.Command, kind cloudClusterKind, clusterID string, clusterLabel string) (bool, error) {
	if !volumeDeletingKinds[kind] {
		return false, nil
	}
	isAccepted, _ := cmd.Flags().GetBool(acceptVolumeDataLossFlag)
	errOut := cmd.ErrOrStderr()
	volumes, readError := apiClient.GetDeprovisionVolumes(string(kind), clusterID)
	named := ""
	switch {
	case readError == nil && volumes != nil && volumes.State == "none" && len(volumes.Volumes) == 0 &&
		!volumes.ConsentRequired:
		if len(volumes.KeptVolumes) > 0 {
			_, _ = fmt.Fprintf(errOut, "Note: the retention policy of cluster %s keeps its persistent volumes; "+
				"they stay in your cloud account and keep billing: %s\n", clusterLabel, deprovisionVolumeList(volumes.KeptVolumes))
		}
		return false, nil
	case readError == nil && volumes != nil && volumes.State == "present" && len(volumes.Volumes) > 0:
		named = deprovisionVolumeList(volumes.Volumes)
		_, _ = fmt.Fprintf(errOut, "Warning: deprovisioning cluster %s deletes its persistent volumes and the data on them: %s\n",
			clusterLabel, named)
	default:
		reason := ""
		if readError != nil {
			reason = " (" + readError.Error() + ")"
		}
		_, _ = fmt.Fprintf(errOut, "Warning: Ankra could not list the persistent volumes of cluster %s%s. "+
			"Any volume its CSI driver provisioned is deleted with it, together with its data.\n", clusterLabel, reason)
	}
	if isAccepted {
		return true, nil
	}
	if !promptIsInteractive(cmd.InOrStdin()) {
		what := "its persistent volumes, which Ankra could not list,"
		if named != "" {
			what = "its persistent volumes (" + named + ")"
		}
		return false, withExitCode(exitUsage, fmt.Errorf(
			"deprovisioning cluster %s deletes %s and the data on them; "+
				"re-run with --%s to delete them with the cluster", clusterLabel, what, acceptVolumeDataLossFlag))
	}
	if promptError := confirmPrompt(cmd.InOrStdin(), errOut,
		"Delete these volumes and their data with the cluster? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return false, promptError
		}
		return false, fmt.Errorf("reading the acknowledgement: %w", promptError)
	}
	return true, nil
}
