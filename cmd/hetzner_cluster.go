package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var hetznerCmd = &cobra.Command{
	Use:   "hetzner",
	Short: "Manage Hetzner clusters",
	Long:  "Commands to create, deprovision, and scale Hetzner clusters.",
}

var hetznerCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new Hetzner cluster",
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		credentialID, _ := cmd.Flags().GetString("credential-id")
		sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
		location, _ := cmd.Flags().GetString("location")
		networkIPRange, _ := cmd.Flags().GetString("network-ip-range")
		subnetRange, _ := cmd.Flags().GetString("subnet-range")
		bastionServerType, _ := cmd.Flags().GetString("bastion-server-type")
		cpCount, _ := cmd.Flags().GetInt("control-plane-count")
		cpServerType, _ := cmd.Flags().GetString("control-plane-server-type")
		workerCount, _ := cmd.Flags().GetInt("worker-count")
		workerServerType, _ := cmd.Flags().GetString("worker-server-type")
		distribution, _ := cmd.Flags().GetString("distribution")
		kubeVersion, _ := cmd.Flags().GetString("kubernetes-version")

		req := client.CreateHetznerClusterRequest{
			Name:                   name,
			CredentialID:           credentialID,
			SSHKeyCredentialID:     sshKeyCredentialID,
			Location:               location,
			NetworkIPRange:         networkIPRange,
			SubnetRange:            subnetRange,
			BastionServerType:      bastionServerType,
			ControlPlaneCount:      cpCount,
			ControlPlaneServerType: cpServerType,
			WorkerCount:            workerCount,
			WorkerServerType:       workerServerType,
			Distribution:           distribution,
		}
		if kubeVersion != "" {
			req.KubernetesVersion = &kubeVersion
		}

		result, err := client.CreateHetznerCluster(apiToken, baseURL, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating Hetzner cluster: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Hetzner cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
	},
}

var hetznerDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id>",
	Short: "Deprovision a Hetzner cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.DeprovisionHetznerCluster(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deprovisioning cluster: %v\n", err)
			os.Exit(1)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("Hetzner cluster deprovisioned successfully!"))
		} else {
			fmt.Println("Cluster deprovisioned with issues.")
		}

		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if len(result.DeletedServers) > 0 {
			fmt.Printf("  Deleted servers: %d\n", len(result.DeletedServers))
		}
		if len(result.DeletedNetworks) > 0 {
			fmt.Printf("  Deleted networks: %d\n", len(result.DeletedNetworks))
		}
		if len(result.DeletedSSHKeys) > 0 {
			fmt.Printf("  Deleted SSH keys: %d\n", len(result.DeletedSSHKeys))
		}
		if len(result.Errors) > 0 {
			fmt.Println(text.FgYellow.Sprint("  Warnings:"))
			for _, e := range result.Errors {
				fmt.Printf("    - %s\n", e)
			}
		}
	},
}

var hetznerWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for a Hetzner cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.GetHetznerWorkerCount(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching worker count: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Worker Count: %d\n", result.WorkerCount)
		fmt.Printf("  Min: %d\n", result.Min)
		fmt.Printf("  Max: %d\n", result.Max)
	},
}

var hetznerScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <worker_count>",
	Short: "Scale workers for a Hetzner cluster",
	Long:  "Scale the number of worker nodes up or down for a Hetzner cluster.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		workerCount, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid worker count: %v\n", err)
			os.Exit(1)
		}

		result, err := client.ScaleHetznerWorkers(apiToken, baseURL, clusterID, workerCount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scaling workers: %v\n", err)
			os.Exit(1)
		}

		if result.PreviousCount == result.NewCount {
			fmt.Printf("Worker count is already %d, no changes needed.\n", result.NewCount)
		} else if result.NewCount > result.PreviousCount {
			fmt.Printf("Scaling %s from %d to %d workers.\n",
				text.FgGreen.Sprint("up"), result.PreviousCount, result.NewCount)
		} else {
			fmt.Printf("Scaling %s from %d to %d workers.\n",
				text.FgYellow.Sprint("down"), result.PreviousCount, result.NewCount)
		}
	},
}

func init() {
	hetznerCreateCmd.Flags().String("name", "", "Cluster name (required)")
	hetznerCreateCmd.Flags().String("credential-id", "", "Hetzner API credential ID (required)")
	hetznerCreateCmd.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (required)")
	hetznerCreateCmd.Flags().String("location", "", "Hetzner location (required)")
	hetznerCreateCmd.Flags().String("network-ip-range", "10.0.0.0/16", "Network IP range")
	hetznerCreateCmd.Flags().String("subnet-range", "10.0.1.0/24", "Subnet range")
	hetznerCreateCmd.Flags().String("bastion-server-type", "cx23", "Bastion server type")
	hetznerCreateCmd.Flags().Int("control-plane-count", 1, "Number of control plane nodes")
	hetznerCreateCmd.Flags().String("control-plane-server-type", "cx33", "Control plane server type")
	hetznerCreateCmd.Flags().Int("worker-count", 1, "Number of worker nodes")
	hetznerCreateCmd.Flags().String("worker-server-type", "cx33", "Worker server type")
	hetznerCreateCmd.Flags().String("distribution", "k3s", "Kubernetes distribution")
	hetznerCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional)")

	_ = hetznerCreateCmd.MarkFlagRequired("name")
	_ = hetznerCreateCmd.MarkFlagRequired("credential-id")
	_ = hetznerCreateCmd.MarkFlagRequired("ssh-key-credential-id")
	_ = hetznerCreateCmd.MarkFlagRequired("location")

	hetznerCmd.AddCommand(hetznerCreateCmd)
	hetznerCmd.AddCommand(hetznerDeprovisionCmd)
	hetznerCmd.AddCommand(hetznerWorkersCmd)
	hetznerCmd.AddCommand(hetznerScaleCmd)

	clusterCmd.AddCommand(hetznerCmd)
}
