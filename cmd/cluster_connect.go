package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ankra/internal/client"
	"github.com/spf13/cobra"
)

// clusterConnectDefaultTimeout bounds the one write a connect makes.
// Registering a cluster with nothing declared on it is answered in a few
// seconds, so the default is far shorter than the ten minutes an apply with
// Stacks is given.
const clusterConnectDefaultTimeout = 2 * time.Minute

// clusterConnectOutput is the structured (-o json/yaml) shape of a connect.
type clusterConnectOutput struct {
	ClusterID      string `json:"cluster_id" yaml:"cluster_id"`
	Name           string `json:"name" yaml:"name"`
	InstallCommand string `json:"install_command" yaml:"install_command"`
}

// `ankra cluster connect` is the portal's "Connect a cluster" page as one
// command. Before it, the only way to connect a cluster from the CLI without
// a cloud credential was to write an ImportCluster file with an empty
// spec.stacks and apply it with --wait (ankra-pd7f35).
var clusterConnectCmd = &cobra.Command{
	Use:     "connect <name>",
	Aliases: []string{"import"},
	Short:   "Connect a cluster you already run: register it and print the agent install command",
	Long: `Connect a Kubernetes cluster you already run (AKS, EKS, GKE, k3s,
on-premises, minikube) to Ankra, without writing a cluster file.

The command registers the cluster under <name> and prints one Helm command.
Run that command where kubectl and Helm reach the cluster: it installs the
Ankra agent in the 'ankra' namespace, and the agent connects the cluster to
this organisation. The agent only makes outbound connections, so nothing is
opened inbound.

The Helm command carries the cluster's agent token, which is a credential: do
not share it or commit it to Git. 'ankra cluster agent token --cluster <name>'
prints the command again. With --generate it replaces the token, and an agent
installed with the old one must then be installed again.

--environment says what the cluster is for; without it the platform labels
the cluster development. The reporting level follows the environment, and
'ankra cluster update' changes either later.

A name another cluster in the organisation already has is refused, and
nothing is changed. To declare Stacks, add-ons or a GitOps repository on the
cluster, use 'ankra cluster apply' with an ImportCluster file.

To connect a cluster your cloud provider runs through a cloud credential
instead, with no command to run against the cluster, see 'ankra cluster
managed import'.

The name must be 1 to 53 lowercase letters, digits and hyphens, starting with
a letter and ending with a letter or digit.`,
	Example: `  ankra cluster connect my-cluster
  ankra cluster connect payments-prod --environment production
  ankra cluster connect edge-lab --description "Lab cluster in rack 4" -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runClusterConnect,
}

func runClusterConnect(cmd *cobra.Command, args []string) error {
	name := args[0]
	if refusal := clusterNameRefusal(name); refusal != "" {
		return withExitCode(exitUsage, fmt.Errorf("cluster name %q: %s", name, refusal))
	}
	if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
		return formatError
	}
	// Only --environment is declared on this command: the import the platform
	// offers the CLI carries no reporting level, so the second value is unset.
	environment, _, classificationError := clusterClassificationFromFlags(cmd)
	if classificationError != nil {
		return classificationError
	}
	description, _ := cmd.Flags().GetString("description")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	if timeout <= 0 {
		return withExitCode(exitUsage, errors.New("--timeout must be longer than zero, for example 2m"))
	}

	// The platform's import updates a cluster that already has the name, so a
	// taken name is refused here: a connect only ever registers a new cluster.
	existing, lookupError := apiClient.GetCluster(name)
	if lookupError == nil {
		return withExitCode(exitBlocked, fmt.Errorf(
			"a cluster named %q already exists (id %s); nothing was changed. To print its agent install command: ankra cluster agent token --cluster %s",
			name, existing.ID, shellWordOrQuoted(name)))
	}
	if !errors.Is(lookupError, client.ErrClusterNotFound) {
		return fmt.Errorf("checking whether a cluster named %q already exists: %w", name, lookupError)
	}

	request := client.CreateImportClusterRequest{
		Name:        name,
		Description: strings.TrimSpace(description),
		Environment: environment,
		Spec:        client.CreateResourceSpec{Stacks: []client.Stack{}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Waited for: the platform returns the install command only with the
	// answer to a write it has applied.
	response, submitted, connectError := apiClient.ApplyCluster(ctx, request, true)
	if connectError != nil {
		return asyncWriteError(fmt.Sprintf("connecting cluster %q", name), true, connectError)
	}
	if response == nil {
		response = &client.ImportResponse{}
	}
	installCommand := strings.Join(strings.Fields(response.ImportCommand), " ")
	if installCommand == "" {
		return clusterConnectWithoutCommandError(name, submitted, response.OperationID)
	}

	output := clusterConnectOutput{ClusterID: response.ClusterId, Name: name, InstallCommand: installCommand}
	if response.Name != "" {
		output.Name = response.Name
	}
	if rendered, renderError := renderStructured(cmd, output); rendered || renderError != nil {
		return renderError
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Cluster '%s' is registered. Install the Ankra agent to connect it.\n\n", output.Name)
	_, _ = fmt.Fprintln(out, "Check that kubectl points at the cluster you mean:")
	_, _ = fmt.Fprintln(out, "  kubectl config current-context")
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Then install the agent:")
	_, _ = fmt.Fprintf(out, "  %s\n\n", installCommand)
	_, _ = fmt.Fprintln(out, "The command carries this cluster's agent token: do not share it or commit it to Git.")
	_, _ = fmt.Fprintf(out, "The cluster usually connects within a minute. Check it with: ankra cluster agent status --cluster %s\n",
		shellWordOrQuoted(output.Name))
	_, _ = fmt.Fprintf(out, "\nView it in the UI:\n  %s\n", portalClusterURLByID(output.ClusterID))
	printValidationWarnings(cmd.ErrOrStderr(), response.Warnings)
	return nil
}

// clusterConnectWithoutCommandError is the answer to a connect the platform
// took without returning the install command: it queued the write instead of
// applying it (submitted), or its reply left the command out. Either way the
// cluster's own token command prints it once the cluster is registered.
func clusterConnectWithoutCommandError(name string, submitted bool, operationID string) error {
	tokenCommand := "ankra cluster agent token --cluster " + shellWordOrQuoted(name)
	if !submitted {
		return fmt.Errorf("cluster %q was registered, but the platform returned no install command; print it with: %s",
			name, tokenCommand)
	}
	follow := ""
	if operationID != "" {
		follow = " (follow it with: " + followOperationCommand(operationID) + ")"
	}
	return fmt.Errorf("the platform is still registering cluster %q%s, so there is no install command yet; once it is registered, print it with: %s",
		name, follow, tokenCommand)
}

func init() {
	clusterConnectCmd.Flags().String("environment", "",
		"Environment: production, staging, development, testing, local, or a name of your own (server default: development)")
	clusterConnectCmd.Flags().String("description", "", "Description for the cluster in Ankra (optional)")
	clusterConnectCmd.Flags().Duration("timeout", clusterConnectDefaultTimeout,
		"Maximum time to wait for the platform to register the cluster")
	registerStructuredOutputFlags(clusterConnectCmd)
	clusterCmd.AddCommand(clusterConnectCmd)
}
