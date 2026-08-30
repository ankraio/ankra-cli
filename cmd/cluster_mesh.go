package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var clusterMeshCmd = &cobra.Command{
	Use:   "mesh",
	Short: "Manage Cilium cluster meshes",
	Long: "Manage Cilium ClusterMeshes: sets of Ankra clusters whose pods and services resolve each other.\n\n" +
		"A cluster can join a mesh only if it was created on the platform's WireGuard overlay with a unique " +
		"network identity, so `ankra cluster mesh readiness` is the place to start - it says which of your " +
		"clusters can mesh, and why the others cannot.",
}

var clusterMeshListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's cluster meshes",
	RunE: func(cmd *cobra.Command, args []string) error {
		meshes, listError := apiClient.ListClusterMeshes()
		if listError != nil {
			return fmt.Errorf("listing cluster meshes: %w", listError)
		}
		if handled, renderError := renderStructured(cmd, meshes); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		if len(meshes) == 0 {
			fmt.Println("No cluster meshes. Create one with `ankra cluster mesh create <name>`.")
			return nil
		}
		for _, mesh := range meshes {
			fmt.Printf("%-36s  %-24s %s\n", mesh.ID, mesh.Slug, mesh.Status)
			for _, member := range mesh.Members {
				fmt.Printf("    %-36s  cilium-id=%-4d %-24s %s\n",
					member.ClusterID, member.CiliumClusterID, member.CiliumClusterName, member.Status)
			}
		}
		return nil
	},
}

var clusterMeshShowCmd = &cobra.Command{
	Use:   "show <mesh_id>",
	Short: "Show one cluster mesh and its members",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mesh, getError := apiClient.GetClusterMesh(args[0])
		if getError != nil {
			return fmt.Errorf("reading cluster mesh: %w", getError)
		}
		if handled, renderError := renderStructured(cmd, mesh); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		fmt.Printf("Mesh:   %s (%s)\nStatus: %s\n", mesh.Name, mesh.Slug, mesh.Status)
		if len(mesh.Members) == 0 {
			fmt.Println("Members: none yet. Add one with `ankra cluster mesh join`.")
			return nil
		}
		fmt.Println("Members:")
		for _, member := range mesh.Members {
			fmt.Printf("  %-36s  cilium-id=%-4d %-24s %s\n",
				member.ClusterID, member.CiliumClusterID, member.CiliumClusterName, member.Status)
		}
		return nil
	},
}

var clusterMeshCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an empty cluster mesh",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mesh, createError := apiClient.CreateClusterMesh(args[0])
		if createError != nil {
			return fmt.Errorf("creating cluster mesh: %w", createError)
		}
		if handled, renderError := renderStructured(cmd, mesh); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		fmt.Printf("Created cluster mesh '%s' (%s).\nAdd clusters with `ankra cluster mesh join %s <cluster_id>`.\n",
			mesh.Name, mesh.ID, mesh.ID)
		return nil
	},
}

var clusterMeshDeleteCmd = &cobra.Command{
	Use:   "delete <mesh_id>",
	Short: "Delete an empty cluster mesh",
	Long:  "Delete a cluster mesh. The mesh must be empty; remove its members first so their Cilium configuration is torn down rather than left pointing at a mesh that no longer exists.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if deleteError := apiClient.DeleteClusterMesh(args[0]); deleteError != nil {
			return fmt.Errorf("deleting cluster mesh: %w", deleteError)
		}
		fmt.Printf("Deleted cluster mesh %s.\n", args[0])
		return nil
	},
}

var clusterMeshJoinCmd = &cobra.Command{
	Use:   "join <mesh_id> <cluster_id>",
	Short: "Add a cluster to a mesh",
	Long: "Add a cluster to a mesh. The platform mints the mesh's shared certificate authority on the first join, " +
		"hands it to the joining cluster, and re-renders every member's peer list.\n\n" +
		"A cluster that cannot mesh is refused with the reason; `ankra cluster mesh readiness` reports the same checks up front.",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if joinError := apiClient.JoinClusterMesh(args[0], args[1]); joinError != nil {
			return fmt.Errorf("joining cluster mesh: %w", joinError)
		}
		fmt.Printf("Cluster %s joined mesh %s.\n", args[1], args[0])
		return nil
	},
}

var clusterMeshLeaveCmd = &cobra.Command{
	Use:   "leave <mesh_id> <cluster_id>",
	Short: "Remove a cluster from a mesh",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if leaveError := apiClient.LeaveClusterMesh(args[0], args[1]); leaveError != nil {
			return fmt.Errorf("leaving cluster mesh: %w", leaveError)
		}
		fmt.Printf("Cluster %s left mesh %s.\n", args[1], args[0])
		return nil
	},
}

var clusterMeshReadinessCmd = &cobra.Command{
	Use:   "readiness <cluster_id> [cluster_id...]",
	Short: "Check whether clusters can mesh together, and why not",
	Long: "Check whether the given clusters could form one mesh. Each cluster is reported ready or not, with the " +
		"failing checks spelled out.\n\nSome failures cannot be fixed on a running cluster: the Cilium identity and " +
		"the overlay network mode are set when the cluster is created, so a cluster without them has to be rebuilt to mesh.",
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		readiness, readinessError := apiClient.CheckClusterMeshReadiness(args)
		if readinessError != nil {
			return fmt.Errorf("checking cluster mesh readiness: %w", readinessError)
		}
		if handled, renderError := renderStructured(cmd, readiness); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		for _, clusterID := range args {
			result, isKnown := readiness[clusterID]
			if !isKnown {
				fmt.Printf("%s  unknown\n", clusterID)
				continue
			}
			if result.Ready {
				fmt.Printf("%s  ready\n", clusterID)
				continue
			}
			fmt.Printf("%s  NOT ready\n", clusterID)
			for _, item := range result.Items {
				if item.Ready {
					continue
				}
				suffix := " (cannot be fixed on a running cluster; the cluster must be recreated)"
				if item.Remediable {
					suffix = ""
				}
				fmt.Printf("    %-16s %s%s\n", item.Name, strings.TrimSpace(item.Detail), suffix)
			}
		}
		return nil
	},
}

func init() {
	clusterMeshCmd.AddCommand(clusterMeshListCmd)
	clusterMeshCmd.AddCommand(clusterMeshShowCmd)
	clusterMeshCmd.AddCommand(clusterMeshCreateCmd)
	clusterMeshCmd.AddCommand(clusterMeshDeleteCmd)
	clusterMeshCmd.AddCommand(clusterMeshJoinCmd)
	clusterMeshCmd.AddCommand(clusterMeshLeaveCmd)
	clusterMeshCmd.AddCommand(clusterMeshReadinessCmd)
	clusterCmd.AddCommand(clusterMeshCmd)
}
