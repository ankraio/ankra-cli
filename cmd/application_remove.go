package cmd

// `ankra application remove` (ankra-dnytjg.2): take an application off one
// cluster and leave it, and everywhere else it runs, in place. The deployment
// is found among the application's own installations, so a refusal can say
// where it does run, and a cluster where it runs in two namespaces asks which.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// removableInstallation is the part of an installation row this command reads.
type removableInstallation struct {
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

// installationsOnCluster parses the installations read and keeps the rows on
// one cluster, optionally in one namespace.
func installationsOnCluster(payload json.RawMessage, clusterID string, namespace string) ([]removableInstallation, []removableInstallation, error) {
	var parsed struct {
		Installations []removableInstallation `json:"installations"`
	}
	if parseError := json.Unmarshal(payload, &parsed); parseError != nil {
		return nil, nil, fmt.Errorf("reading the application's installations: %w", parseError)
	}
	matched := []removableInstallation{}
	for _, installation := range parsed.Installations {
		if !strings.EqualFold(installation.ClusterID, clusterID) {
			continue
		}
		if namespace != "" && installation.Namespace != namespace {
			continue
		}
		matched = append(matched, installation)
	}
	return matched, parsed.Installations, nil
}

// describePlacements lists where the application runs, for a refusal.
func describePlacements(installations []removableInstallation) string {
	placements := make([]string, 0, len(installations))
	for _, installation := range installations {
		placements = append(placements, installation.ClusterID+" / "+installation.Namespace)
	}
	sort.Strings(placements)
	return strings.Join(placements, ", ")
}

func newApplicationRemoveCommand() *cobra.Command {
	removeCommand := &cobra.Command{
		Use:   "remove <application> --cluster <cluster>",
		Short: "Remove an application from one cluster",
		Long: `Remove an application from one cluster, leaving the application and its other
deployments in place.

Ankra uninstalls the application's workloads in that cluster and namespace -
and with them any data in that deployment's database and volumes - and
deletes the installation once the cluster has it. The application, its
settings, secrets, images and release history, and its deployments on other
clusters stay, so you can deploy it there again.

The removal runs in the background: 'ankra application installations' shows the
deployment as "removing" until it is gone, or "failed" with the reason.

To stop it for a while without losing anything, park it instead. To delete the
application everywhere, use 'ankra application delete'.`,
		Example: `  ankra application remove shop --cluster staging
  ankra application remove shop --cluster production --namespace shop-prod --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			clusterReference := strings.TrimSpace(mustFlagString(command, "cluster"))
			if clusterReference == "" {
				return withExitCode(exitUsage, errors.New("--cluster is required: the cluster to remove the application from"))
			}
			namespace := strings.TrimSpace(mustFlagString(command, "namespace"))
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			clusterID, clusterError := resolveClusterArg(clusterReference)
			if clusterError != nil {
				return clusterError
			}
			payload, readError := apiClient.GetApplicationInstallations(command.Context(), applicationID)
			if readError != nil {
				return readError
			}
			matched, all, parseError := installationsOnCluster(payload, clusterID, namespace)
			if parseError != nil {
				return parseError
			}
			applicationName := strings.TrimSpace(arguments[0])
			switch {
			case len(matched) == 0 && len(all) == 0:
				return withExitCode(exitNotFound, fmt.Errorf("application %q is not deployed to any cluster", applicationName))
			case len(matched) == 0:
				where := "cluster " + clusterReference
				if namespace != "" {
					where += " in namespace " + namespace
				}
				return withExitCode(exitNotFound, fmt.Errorf("application %q is not deployed on %s; it runs on: %s",
					applicationName, where, describePlacements(all)))
			case len(matched) > 1:
				return withExitCode(exitUsage, fmt.Errorf(
					"application %q runs in %d namespaces on cluster %s (%s) - pass --namespace",
					applicationName, len(matched), clusterReference, describePlacements(matched)))
			}
			target := matched[0]
			if target.Status == "removing" {
				return fmt.Errorf("application %q is already being removed from cluster %s (namespace %s)",
					applicationName, clusterReference, target.Namespace)
			}
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.OutOrStdout(), fmt.Sprintf(
				"Remove application %q from cluster %s (namespace %s)? Its workloads there are uninstalled, "+
					"including data in that deployment's database and volumes. The application and its other "+
					"deployments stay. [y/N]: ", applicationName, clusterReference, target.Namespace), yes); confirmError != nil {
				return confirmError
			}
			removed, removeError := apiClient.RemoveApplicationDeployment(command.Context(), applicationID,
				client.RemoveApplicationDeploymentRequest{ClusterID: clusterID, Namespace: target.Namespace})
			if removeError != nil {
				return removeRefusal(removeError)
			}
			if rendered, renderError := renderStructured(command, removed); rendered || renderError != nil {
				return renderError
			}
			printRemoveFollowUp(command.OutOrStdout(), applicationName, clusterReference, removed)
			return nil
		},
	}
	removeCommand.Flags().String("cluster", "", "Cluster to remove the application from (name or id, required)")
	removeCommand.Flags().String("namespace", "",
		"Namespace of the deployment (needed only when the application runs in more than one namespace on that cluster)")
	removeCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(removeCommand)
	return removeCommand
}

// removeRefusal gives the platform's two refusals their exit codes: nothing
// deployed there is a miss (3); a removal already running stays a plain
// failure (1) with the platform's sentence.
func removeRefusal(removeError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(removeError, &unexpected) && unexpected.StatusCode == 422 {
		return withExitCode(exitNotFound, removeError)
	}
	return removeError
}

func printRemoveFollowUp(out io.Writer, applicationName string, clusterReference string,
	removed *client.RemoveApplicationDeploymentResult) {
	_, _ = fmt.Fprintf(out, "Removing %s from cluster %s (namespace %s).\n", applicationName, clusterReference,
		removed.Namespace)
	_, _ = fmt.Fprintln(out, "The application and its other deployments stay.")
	_, _ = fmt.Fprintf(out, "Follow it with 'ankra application installations %s': the deployment reads \"removing\" "+
		"until it is gone, or \"failed\" with the reason.\n", applicationName)
}
