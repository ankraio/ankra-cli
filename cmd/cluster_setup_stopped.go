package cmd

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
)

// clusterStateText is the State a person reads for a cluster. A failed
// create leaves the stored state at creating (or an access stage after it),
// so a cluster whose setup stopped says so next to that state instead of
// reading as a setup still in progress (ankra-2v9ewh). Every other cluster
// reads its stored state.
func clusterStateText(cluster client.ClusterListItem) string {
	if cluster.SetupStopped != nil {
		return cluster.State + " (setup stopped)"
	}
	return cluster.State
}

// clusterStateCell is clusterStateText coloured for the cluster table: a
// stopped setup in red, online in green as before.
func clusterStateCell(cluster client.ClusterListItem) string {
	if cluster.SetupStopped != nil {
		return text.FgRed.Sprint(clusterStateText(cluster))
	}
	if strings.ToLower(cluster.State) == "online" {
		return text.FgGreen.Sprint(cluster.State)
	}
	return cluster.State
}

// countStoppedSetups counts the listed clusters whose setup stopped.
func countStoppedSetups(clusters []client.ClusterListItem) int {
	stoppedCount := 0
	for _, cluster := range clusters {
		if cluster.SetupStopped != nil {
			stoppedCount++
		}
	}
	return stoppedCount
}

// printStoppedSetupHint follows the cluster table when a listed cluster's
// setup stopped, naming the command that shows the operation and its error.
func printStoppedSetupHint(out io.Writer, clusters []client.ClusterListItem) {
	stoppedCount := countStoppedSetups(clusters)
	if stoppedCount == 0 {
		return
	}
	subject := "1 cluster"
	if stoppedCount > 1 {
		subject = fmt.Sprintf("%d clusters", stoppedCount)
	}
	fmt.Fprintf(out, "Setup stopped on %s: `ankra cluster info <name>` shows the operation it stopped at and its error.\n", subject)
}

// printStoppedSetup prints the operation a cluster's setup stopped at, for
// `cluster info`: its id, status, when it ended and its error, and the
// command that opens it. Nothing for a cluster whose setup has not stopped.
func printStoppedSetup(out io.Writer, cluster client.ClusterListItem) {
	stopped := cluster.SetupStopped
	if stopped == nil {
		return
	}
	ended := ""
	if stopped.StoppedAt != nil && strings.TrimSpace(*stopped.StoppedAt) != "" {
		ended = " " + formatTimeAgo(*stopped.StoppedAt)
	}
	fmt.Fprintf(out, "  Setup stopped: operation %s %s%s\n", stopped.ExecutionID, stopped.Status, ended)
	if stopped.ErrorExcerpt != nil && strings.TrimSpace(*stopped.ErrorExcerpt) != "" {
		errorLines := strings.Split(strings.TrimSpace(*stopped.ErrorExcerpt), "\n")
		fmt.Fprintf(out, "    Error: %s\n", strings.Join(errorLines, "\n           "))
	}
	fmt.Fprintf(out, "    Details: ankra cluster operations list %s\n", stopped.ExecutionID)
}
