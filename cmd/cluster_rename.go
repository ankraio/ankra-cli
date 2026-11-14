package cmd

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/spf13/cobra"
)

// clusterNameLimit is the platform's longest cluster name, the same limit a
// new cluster is held to.
const clusterNameLimit = 53

// clusterNamePattern is the shape the platform accepts for a cluster name: an
// RFC 1035 DNS label, the rule every create already enforces.
var clusterNamePattern = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// clusterRenameOutput is the structured (-o json/yaml) shape of a rename.
type clusterRenameOutput struct {
	ClusterID    string `json:"cluster_id"`
	Name         string `json:"name"`
	PreviousName string `json:"previous_name"`
	IacDirectory string `json:"iac_directory"`
	Changed      bool   `json:"changed"`
}

var clusterRenameCmd = &cobra.Command{
	Use:   "rename <cluster> <new-name>",
	Short: "Rename a cluster in place",
	Long: `Rename a cluster. The cluster is given by its current name or its id.

A rename changes the name Ankra knows the cluster by: the portal, the cluster
list, 'ankra' commands and Ankra AI all use the new name. Nothing is rebuilt
or replaced, and the cluster id does not change.

What keeps its old name:
  - server names, hostnames and Kubernetes node names, which were set when
    the nodes were created and cannot change on a running node;
  - the cluster's DNS zone and the hosts of applications already deployed;
  - the GitOps directory (clusters/<old-name>-<suffix>/), which Ankra keeps
    using so stored paths stay valid;
  - existing kubeconfig contexts ('ankra-<old-name>'). They keep working,
    because they reach the cluster by id; run 'ankra cluster kubeconfig add
    <new-name>' for a context with the new name.

API tokens, kube tokens, access grants, hosted metrics and logs, backups and
alert routes are tied to the cluster id and keep working unchanged.

An ImportCluster file that still says metadata.name: <old-name> is refused by
'ankra cluster apply' with a message naming the new name; it never creates a
second cluster. Change metadata.name to the new name before the next apply, or
apply with --cluster <new-name>.

A cluster synced with a connected GitOps repository, and an Ankra Cloud
cluster, cannot be renamed yet; the platform refuses and changes nothing.

The name must be 1 to 53 lowercase letters, digits and hyphens, starting with
a letter and ending with a letter or digit, and no other cluster in the
organisation may have it.`,
	Example: `  ankra cluster rename socloud-cheap socloud-dev
  ankra cluster rename 22de7a2c-a671-4e7b-b6db-b32d03490a08 socloud-dev -o json`,
	Args: cobra.ExactArgs(2),
	RunE: runClusterRename,
}

func runClusterRename(cmd *cobra.Command, args []string) error {
	newName := args[1]
	if refusal := clusterNameRefusal(newName); refusal != "" {
		return withExitCode(exitUsage, fmt.Errorf("new name %q: %s", newName, refusal))
	}
	if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
		return formatError
	}

	cluster, lookupError := lookupClusterByNameOrID(args[0])
	if lookupError != nil {
		return lookupError
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, renameError := apiClient.RenameCluster(ctx, cluster.ID, newName)
	if renameError != nil {
		return fmt.Errorf("renaming cluster %q: %w", cluster.Name, renameError)
	}
	selectionNote := followRenameInSelection(cluster.ID, result.Name)

	output := clusterRenameOutput{ClusterID: result.ClusterID, Name: result.Name, PreviousName: result.PreviousName,
		IacDirectory: result.IacDirectory, Changed: result.Changed}
	if rendered, renderError := renderStructured(cmd, output); rendered || renderError != nil {
		return renderError
	}

	out := cmd.OutOrStdout()
	if !result.Changed {
		_, _ = fmt.Fprintf(out, "Cluster '%s' already has that name; nothing changed.\n", result.Name)
		return nil
	}
	_, _ = fmt.Fprintf(out, "Cluster '%s' renamed to '%s'.\n", result.PreviousName, result.Name)
	_, _ = fmt.Fprintf(out, "Nodes, servers and the cluster's DNS zone keep their names; GitOps files stay in %s.\n",
		result.IacDirectory)
	_, _ = fmt.Fprintf(out, "Update metadata.name to '%s' in the cluster's ImportCluster file before the next 'ankra cluster apply'.\n",
		result.Name)
	_, _ = fmt.Fprintf(out, "Existing kubeconfig contexts keep working; 'ankra cluster kubeconfig add %s' adds one under the new name.\n",
		result.Name)
	if selectionNote != "" {
		_, _ = fmt.Fprintln(out, selectionNote)
	}
	return nil
}

// clusterNameRefusal answers why name cannot name a cluster, or "" when it
// can, with the platform's own rule so a bad name is a usage error before
// anything is sent.
func clusterNameRefusal(name string) string {
	if len(name) <= clusterNameLimit && clusterNamePattern.MatchString(name) {
		return ""
	}
	return fmt.Sprintf("must be 1 to %d lowercase letters, digits and hyphens, starting with a letter and ending with a letter or digit",
		clusterNameLimit)
}

// followRenameInSelection keeps the cluster chosen with 'ankra cluster select'
// pointing at the renamed cluster: several commands look the selection up by
// name, which the rename just retired. It answers a line to print when it
// updated the selection, and "" when the selection is another cluster, is
// absent, or could not be read or written (the rename itself succeeded).
func followRenameInSelection(clusterID string, newName string) string {
	selected, loadError := loadSelectedCluster()
	if loadError != nil || selected.ID != clusterID || selected.Name == newName {
		return ""
	}
	selected.Name = newName
	if saveError := saveSelectedCluster(selected); saveError != nil {
		return ""
	}
	return fmt.Sprintf("Your selected cluster now reads '%s'.", newName)
}

func init() {
	registerStructuredOutputFlags(clusterRenameCmd)
	clusterCmd.AddCommand(clusterRenameCmd)
}
