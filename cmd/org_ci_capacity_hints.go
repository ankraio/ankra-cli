package cmd

import (
	"ankra/internal/client"
	"fmt"
	"io"
)

// renderOrganisationCICapacityHints names only the member whose measured slots
// are full. A pool can have waiting steps pinned to one member while another
// is idle; its waiting-step count does not make every member full.
func renderOrganisationCICapacityHints(writer io.Writer, capacity *client.OrganisationCICapacity, clusterName string) {
	if capacity.OrganisationStepsWaitingOnSlots > 0 {
		if capacity.IsPooled {
			for _, member := range capacity.PoolMembers {
				if member.CanRunSteps && member.IsFull {
					_, _ = fmt.Fprintf(writer, "Some steps are waiting for busy workers on %s. To increase its worker limit:\n  ankra cluster agent ci set --workers N --cluster %s\n", member.ClusterName, member.ClusterName)
				}
			}
		} else if clusterName != "" && capacity.CIWorkerCount > 0 && capacity.StepsInFlightOnCluster >= capacity.CIWorkerCount {
			_, _ = fmt.Fprintf(writer, "Every slot on %s is taken, so steps wait for one to free. To run more steps at once:\n  ankra cluster agent ci set --workers N --cluster %s\n", clusterName, clusterName)
		}
	}
}
