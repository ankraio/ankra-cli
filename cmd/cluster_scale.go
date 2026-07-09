package cmd

import (
	"fmt"
	"strconv"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

type workerScaleFunc func(clusterID string, workerCount int) (*client.ScaleWorkersResult, error)

// scaleFunctionForKind maps a cluster's kind (as returned by the backend) to
// the provider-specific worker scaling call. Only the cloud-managed kinds
// support worker scaling.
func scaleFunctionForKind(kind string) (workerScaleFunc, bool) {
	switch kind {
	case "hetzner":
		return apiClient.ScaleHetznerWorkers, true
	case "ovh":
		return apiClient.ScaleOvhWorkers, true
	case "upcloud":
		return apiClient.ScaleUpcloudWorkers, true
	case "digitalocean":
		return apiClient.ScaleDigitaloceanWorkers, true
	default:
		return nil, false
	}
}

var clusterScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <worker_count>",
	Short: "Scale the default worker pool of a cloud cluster",
	Long: `Scale the number of default-pool worker nodes up or down for a cloud cluster.

The cloud provider (Hetzner, OVH, UpCloud, or DigitalOcean) is detected automatically from
the cluster, so you do not need to remember which provider it runs on. To scale
a named node group instead, use 'ankra cluster node-group scale'.

Example:
  ankra cluster scale 62f4559a-a44d-46d7-aab3-a57c9dd6b4c6 3`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		workerCount, convertError := strconv.Atoi(args[1])
		if convertError != nil {
			return fmt.Errorf("invalid worker count: %w", convertError)
		}

		cluster, lookupError := apiClient.GetClusterByID(clusterID)
		if lookupError != nil {
			return fmt.Errorf("looking up cluster %q: %w", clusterID, lookupError)
		}

		scale, supported := scaleFunctionForKind(cluster.Kind)
		if !supported {
			return fmt.Errorf(
				"cluster %q (kind %q) does not support worker scaling. Only Hetzner, OVH, UpCloud, and DigitalOcean clusters can be scaled with this command",
				clusterID, cluster.Kind)
		}

		result, scaleError := scale(clusterID, workerCount)
		if scaleError != nil {
			return fmt.Errorf("scaling workers: %w", scaleError)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("  Provider: %s\n", cluster.Kind)
		if result.PreviousCount == result.NewCount {
			fmt.Printf("Worker count is already %d, no changes needed.\n", result.NewCount)
		} else if result.NewCount > result.PreviousCount {
			fmt.Printf("Scaling %s from %d to %d workers.\n",
				text.FgGreen.Sprint("up"), result.PreviousCount, result.NewCount)
		} else {
			fmt.Printf("Scaling %s from %d to %d workers.\n",
				text.FgYellow.Sprint("down"), result.PreviousCount, result.NewCount)
		}
		return nil
	},
}

func init() {
	registerStructuredOutputFlags(clusterScaleCmd)
	clusterCmd.AddCommand(clusterScaleCmd)
}
