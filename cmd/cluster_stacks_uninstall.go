package cmd

// `ankra cluster stacks uninstall` and `ankra cluster stacks install` (bead
// ankra-vrujb7.2, over ankraio/cluster#6021).
//
// Uninstalling a stack takes every add-on, manifest and application in it off
// the cluster while the stack stays declared in Ankra and in Git; installing
// it deploys it again. Until now that was the portal's Uninstall stack and
// Install stack, and nothing else.
//
// Uninstall removes running workloads, and data goes as a delete treats it,
// so it states that and asks first: --yes answers, and without a terminal
// --yes is required. It also never acts on the cluster `ankra cluster select`
// saved, which every shell and agent session on the machine shares: a
// selection made in another session must not aim it at the wrong cluster
// (the rule withClusterArgument applies to cluster teardowns). Install only
// deploys what the stack declares and the platform refuses it for a stack
// that is not uninstalled, so it neither asks nor needs --cluster, like
// redeploy.
//
// Every 409 either route answers is a refusal that changed nothing, with the
// platform's sentence saying what to do first. It is printed as it came and
// exits 8, the code for a block that left everything as it was.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// stackInstallationVerb is one of the two directions.
type stackInstallationVerb struct {
	// name is the verb as typed and as the route spells it.
	name string
	// progressive opens the progress line: "Uninstalling", "Installing".
	progressive string
	// installedAfter is what the platform's installed member must read after
	// a successful call.
	installedAfter bool
}

var (
	stackUninstallVerb = stackInstallationVerb{name: "uninstall", progressive: "Uninstalling", installedAfter: false}
	stackInstallVerb   = stackInstallationVerb{name: "install", progressive: "Installing", installedAfter: true}
)

var clusterStacksUninstallCmd = &cobra.Command{
	Use:   "uninstall <stack name>",
	Short: "Take a stack's add-ons, manifests and applications off the cluster, keeping the stack declared",
	Long: "Uninstall a stack: take every add-on, manifest and application in it off the cluster. The stack itself " +
		"is not deleted. It stays declared in Ankra and in your GitOps repository, nothing is committed to Git, and " +
		"'ankra cluster stacks install <stack>' deploys it again.\n\n" +
		"Members come off in reverse dependency order. Data is treated exactly as a delete treats it: whatever a " +
		"chart or operator removes together with its workload is removed too, and installing the stack again does " +
		"not bring that back. Back the stack up first: 'ankra cluster stacks data list <stack>' lists what it " +
		"stores, and 'ankra cluster stacks restore-points create <stack>' takes a restore point.\n\n" +
		"While the stack is uninstalled its members stay off the cluster through cluster starts and Git syncs, and " +
		"edits to them are stored and deployed when the stack is installed again.\n\n" +
		"Name the cluster with --cluster. This command never uses the cluster 'ankra cluster select' saved, which " +
		"every shell and agent session on this machine shares. It says what it will do and asks first: --yes " +
		"answers, and without a terminal --yes is required.\n\n" +
		"The platform refuses an uninstall, and changes nothing, while the cluster is offline, while a member's " +
		"deployment is still running or has failed, while the stack has an open draft or is being deleted, and for " +
		"a stack a managed service owns. The command then prints the platform's reason and exits 8. It needs the " +
		"stacks.delete permission on the cluster (exit 7 without it), and a stack the cluster does not have exits 3.",
	Example: "  ankra cluster stacks uninstall web --cluster prod\n" +
		"  ankra cluster stacks uninstall web --cluster prod --yes\n" +
		"  ankra cluster stacks uninstall web --cluster prod --yes -o json",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStackInstallationChange(cmd, args[0], stackUninstallVerb)
	},
}

var clusterStacksInstallCmd = &cobra.Command{
	Use:   "install <stack name>",
	Short: "Deploy an uninstalled stack to the cluster again",
	Long: "Install a stack that was uninstalled: deploy its add-ons, manifests and applications to the cluster " +
		"again, in dependency order, with the definitions the stack holds now, including members added or edited " +
		"while it was uninstalled. A member that was already off the cluster when the stack was uninstalled, such " +
		"as an add-on uninstalled on its own, stays off it.\n\n" +
		"It acts on --cluster, or on the selected cluster when none is named.\n\n" +
		"The platform refuses an install, and changes nothing, for a stack that is not uninstalled, one whose " +
		"uninstall is still taking members off the cluster, one with an open draft or being deleted, and while the " +
		"cluster is offline. The command then prints the platform's reason and exits 8. It needs the stacks.deploy " +
		"permission on the cluster (exit 7 without it), and a stack the cluster does not have exits 3.",
	Example: "  ankra cluster stacks install web --cluster prod\n" +
		"  ankra cluster stacks install web -o json",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStackInstallationChange(cmd, args[0], stackInstallVerb)
	},
}

// runStackInstallationChange is the body both verbs share.
func runStackInstallationChange(cmd *cobra.Command, rawStackName string, verb stackInstallationVerb) error {
	stackName := strings.TrimSpace(rawStackName)
	if stackName == "" {
		return withExitCode(exitUsage, fmt.Errorf("a stack name is required"))
	}
	format, formatError := structuredFormatFromFlags(cmd)
	if formatError != nil {
		return formatError
	}
	uninstalling := verb == stackUninstallVerb
	if uninstalling && clusterFlagOverride(cmd) == "" {
		return withExitCode(exitUsage, fmt.Errorf(
			"name the cluster to uninstall stack '%s' from with --cluster <name|id>. Uninstall never acts on the "+
				"cluster 'ankra cluster select' saved, which every shell and agent session on this machine shares. "+
				"Nothing was changed", stackName))
	}
	cluster, clusterError := resolveActiveCluster(cmd)
	if clusterError != nil {
		return clusterError
	}
	if uninstalling {
		if confirmError := confirmStackUninstall(cmd, stackName, cluster); confirmError != nil {
			return confirmError
		}
	}
	if format == outputDefault {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s stack '%s' on cluster '%s'...\n", verb.progressive, stackName, cluster.Name)
	}
	var result *client.StackInstallationResult
	var callError error
	if uninstalling {
		result, callError = apiClient.UninstallStack(cmd.Context(), cluster.ID, stackName)
	} else {
		result, callError = apiClient.InstallStack(cmd.Context(), cluster.ID, stackName)
	}
	if callError != nil {
		return stackInstallationError(callError, verb, stackName, cluster)
	}
	if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
		return renderError
	}
	printStackInstallationResult(cmd.OutOrStdout(), verb, stackName, cluster, result)
	return nil
}

// stackUninstallConsequence is what an uninstall does, said before it is
// asked for. The platform documents every clause (openapi.json, POST
// .../stacks/{stack_name}/uninstall).
func stackUninstallConsequence(stackName string, cluster client.ClusterListItem) string {
	return fmt.Sprintf("Uninstalling stack '%s' on cluster '%s' takes every add-on, manifest and application in it "+
		"off the cluster, in reverse dependency order, and their workloads stop running.\n"+
		"Data is treated exactly as a delete treats it: whatever a chart or operator removes together with its "+
		"workload is removed too, and installing the stack again does not bring that back. Back the stack up "+
		"first: 'ankra cluster stacks data list %s' lists what it stores, and 'ankra cluster stacks "+
		"restore-points create %s' takes a restore point.\n"+
		"The stack stays declared in Ankra and in Git, nothing is committed to Git, and 'ankra cluster stacks "+
		"install %s' deploys it again.\n",
		stackName, cluster.Name, stackName, stackName, stackName)
}

// confirmStackUninstall states what the uninstall removes and asks for it to
// be confirmed, the CLI's counterpart of the portal's dialog. --yes skips the
// question; without a terminal to ask on, --yes is required, because a script
// that never saw the consequence must not take workloads off a cluster by
// accident. Everything goes to stderr so -o json|yaml stays parseable.
func confirmStackUninstall(command *cobra.Command, stackName string, cluster client.ClusterListItem) error {
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprint(errOut, stackUninstallConsequence(stackName, cluster))
	yes, _ := command.Flags().GetBool("yes")
	if yes {
		_, _ = fmt.Fprintln(errOut)
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"uninstalling stack '%s' takes its members off cluster '%s' and needs confirmation, but there is no "+
				"terminal to ask on; re-run with --yes to confirm. Nothing was changed", stackName, cluster.Name))
	}
	if promptError := confirmPrompt(command.InOrStdin(), errOut,
		fmt.Sprintf("\nUninstall stack '%s' from cluster '%s'? [y/N]: ", stackName, cluster.Name), false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			_, _ = fmt.Fprintf(errOut, "Nothing was changed: stack '%s' stays installed.\n", stackName)
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// stackInstallationError says what a failed uninstall or install means. A
// refusal (any 409) changed nothing and exits 8 with the platform's sentence
// as it came. Of the 404s, only the stack's and the cluster's mean the target
// does not exist: a route the platform does not serve (these are newer than
// the platform may be), or a 404 that gives no reason, must not exit as if
// the stack were missing. Everything else keeps its own class (exit 6 for a
// login, 7 for a role without the permission).
func stackInstallationError(callError error, verb stackInstallationVerb, stackName string, cluster client.ClusterListItem) error {
	operation := fmt.Sprintf("%s stack '%s' on cluster '%s'", verb.name, stackName, cluster.Name)
	var refused *client.StackInstallationRefusedError
	if errors.As(callError, &refused) {
		return withExitCode(exitBlocked, fmt.Errorf("the platform refused to %s stack '%s' on cluster '%s', and nothing "+
			"was changed: %s", verb.name, stackName, cluster.Name, refused.Detail))
	}
	// A timeout already names the operation and the read that settles
	// whether it landed.
	var unknown *client.WriteOutcomeUnknownError
	if errors.As(callError, &unknown) {
		return callError
	}
	var unexpected *client.UnexpectedResponseError
	if !errors.As(callError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		return fmt.Errorf("%s: %w", operation, callError)
	}
	switch unexpected.Detail {
	case fmt.Sprintf("Stack '%s' could not be found in this cluster.", stackName):
		return withExitCode(exitNotFound, fmt.Errorf("stack '%s' not found on cluster '%s'", stackName, cluster.Name))
	case hostedLogsClusterNotFoundDetail:
		return withExitCode(exitNotFound, fmt.Errorf(
			"cluster '%s' (%s) not found in this organisation. Check the selected organisation with "+
				"'ankra org current', and the cluster you named with --cluster", cluster.Name, cluster.ID))
	case routeAbsentDetail:
		return withExitCode(exitError, fmt.Errorf(
			"%s: this Ankra platform does not offer stack %s to API tokens yet (the route answered 404). "+
				"Nothing was changed", operation, verb.name))
	case "":
		return withExitCode(exitError, fmt.Errorf(
			"%s: the platform answered 404 without saying why, so it is not known whether the stack is missing "+
				"or the platform does not offer this yet. Nothing was changed", operation))
	default:
		return withExitCode(exitError, fmt.Errorf("%s: %w", operation, callError))
	}
}

// stackInstallationClusterFlag is the --cluster value the follow-up hints
// carry: the cluster's name, or its id when the platform reported none.
func stackInstallationClusterFlag(cluster client.ClusterListItem) string {
	if cluster.Name != "" {
		return cluster.Name
	}
	return cluster.ID
}

// printStackInstallationResult renders the outcome. The members leave or
// return in the background, so it says the work is under way rather than
// done, and where to watch it. resource_ids are the members this call moved;
// an empty list is "nothing was left to move", not a failure. An installed
// state that is not what the verb leaves is reported rather than smoothed
// over.
func printStackInstallationResult(out io.Writer, verb stackInstallationVerb, stackName string,
	cluster client.ClusterListItem, result *client.StackInstallationResult) {
	clusterFlag := stackInstallationClusterFlag(cluster)
	moved := len(result.ResourceIDs)
	switch {
	case verb == stackUninstallVerb && moved > 0:
		_, _ = fmt.Fprintf(out, "Stack '%s' is uninstalled on cluster '%s'. Ankra is taking its members off the cluster now.\n",
			stackName, cluster.Name)
		_, _ = fmt.Fprintf(out, "  Members being taken off: %d\n", moved)
	case verb == stackUninstallVerb:
		_, _ = fmt.Fprintf(out, "Stack '%s' is uninstalled on cluster '%s'. No member was left to take off the cluster.\n",
			stackName, cluster.Name)
	case moved > 0:
		_, _ = fmt.Fprintf(out, "Stack '%s' is installed again on cluster '%s'. Ankra is deploying its members now.\n",
			stackName, cluster.Name)
		_, _ = fmt.Fprintf(out, "  Members being deployed: %d\n", moved)
	default:
		_, _ = fmt.Fprintf(out, "Stack '%s' is installed again on cluster '%s'. No member needed to be deployed.\n",
			stackName, cluster.Name)
	}
	for _, resourceID := range result.ResourceIDs {
		_, _ = fmt.Fprintf(out, "    - %s\n", resourceID)
	}
	if result.Installed != verb.installedAfter {
		_, _ = fmt.Fprintf(out, "\nThe platform reports the stack as installed=%t, which is not what an %s leaves. "+
			"Check it with 'ankra cluster stacks list %s --cluster %s'.\n", result.Installed, verb.name, stackName, clusterFlag)
		return
	}
	if verb == stackUninstallVerb {
		_, _ = fmt.Fprintf(out, "\nThe stack stays declared in Ankra and in Git. 'ankra cluster stacks list %s --cluster %s' "+
			"shows each member's state, and 'ankra cluster stacks install %s --cluster %s' deploys the stack again.\n",
			stackName, clusterFlag, stackName, clusterFlag)
		return
	}
	_, _ = fmt.Fprintf(out, "\n'ankra cluster stacks list %s --cluster %s' shows each member's state.\n", stackName, clusterFlag)
}

func init() {
	clusterStacksUninstallCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(clusterStacksUninstallCmd, clusterStacksInstallCmd)
	clusterStacksCmd.AddCommand(clusterStacksUninstallCmd, clusterStacksInstallCmd)
}
