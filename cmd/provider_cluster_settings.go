package cmd

// Provider-specific cluster settings (ankra-hq41xb.15):
//
//	ankra cluster k3s-disabled-components get|set <cluster_id|name>
//	ankra cluster upcloud gateway-plans | gateway-plan | load-balancer-plans |
//	                      load-balancer-plan get|set | available-zones
//	ankra cluster ovh gateway-model <cluster_id|name>
//	ankra cluster hetzner external-cloud-provider converge <cluster_id|name>
//
// Each is the CLI surface over a bearer route under
// /api/v1/clusters/<provider>/... that only the portal reached. Commands that
// act on a cluster take it as the first argument or with --cluster and never
// from the selection, like their siblings.

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var clusterK3sDisabledComponentsCmd = &cobra.Command{
	Use:   "k3s-disabled-components",
	Short: "Show or extend the k3s packaged components kept disabled on a cluster",
	Long: `The k3s packaged components the platform keeps disabled on a running k3s cluster
(k3s --disable), on top of the provider shape's own defaults: for example traefik
on a cluster that brings its own ingress. The components k3s packages are
coredns, servicelb, traefik, local-storage, metrics-server and runtimes.

The cloud provider is detected from the cluster. A kubeadm cluster has no k3s
components and the platform refuses it (409).`,
}

var clusterK3sDisabledComponentsGetCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "get <cluster_id|name>",
	Short: "Show the k3s components kept disabled on a cluster",
	Long: `Show the k3s packaged components stored as disabled on the cluster's Kubernetes
node definitions, how many control planes the list is rendered on, and how the
stored list reaches the nodes. An empty list means nothing is stored and the
provider shape's own defaults apply.`,
	Example: `  ankra cluster k3s-disabled-components get prod
  ankra cluster k3s-disabled-components get --cluster prod -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		clusterID, kind, resolveError := resolveK3sDisabledComponentsCluster(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		components, getError := apiClient.GetK3sDisabledComponents(ctx, kind, clusterID)
		if getError != nil {
			return fmt.Errorf("reading the k3s disabled components of cluster %s: %w", clusterTarget(arguments[0], clusterID), getError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, components)
		}
		printK3sDisabledComponents(command, components)
		return nil
	},
})

var clusterK3sDisabledComponentsSetCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "set <cluster_id|name> --components <components>",
	Short: "Store the complete list of k3s components to keep disabled",
	Long: `Replace the list of k3s packaged components the platform keeps disabled on the
cluster. --components is the complete list to store, and it has to contain
every component already stored: a component can be added, never taken off the
list, and a list that drops one is refused (409). Read the stored list first
with 'ankra cluster k3s-disabled-components get'.

Within about five minutes the platform flags the control planes whose k3s
server config is missing an entry, then patches the config and restarts k3s on
one control plane at a time. Control planes that already carry every entry are
not touched. The component's workloads stop being managed by k3s, so this asks
for confirmation; --yes skips the question and is required when there is no
terminal to ask on.`,
	Example: `  ankra cluster k3s-disabled-components set prod --components traefik
  ankra cluster k3s-disabled-components set prod --components traefik,servicelb --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		requested, _ := command.Flags().GetStringSlice("components")
		components := []string{}
		for _, component := range requested {
			if trimmed := strings.TrimSpace(component); trimmed != "" && !slices.Contains(components, trimmed) {
				components = append(components, trimmed)
			}
		}
		if len(components) == 0 {
			return withExitCode(exitUsage, errors.New(
				"--components is required: the complete list of k3s components to keep disabled, for example --components traefik"))
		}
		clusterID, kind, resolveError := resolveK3sDisabledComponentsCluster(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		if confirmError := confirmProviderSettingChange(command, fmt.Sprintf(
			"Keeping %s disabled on cluster %s restarts k3s on each control plane missing an entry, one at a time.",
			strings.Join(components, ", "), clusterTarget(arguments[0], clusterID))); confirmError != nil {
			return confirmError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		stored, setError := apiClient.SetK3sDisabledComponents(ctx, kind, clusterID, components)
		if setError != nil {
			return fmt.Errorf("storing the k3s disabled components of cluster %s: %w", clusterTarget(arguments[0], clusterID), setError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, stored)
		}
		printK3sDisabledComponents(command, stored)
		return nil
	},
})

// resolveK3sDisabledComponentsCluster resolves the cluster argument and reads
// the cluster's kind, which names the provider route. A cluster of a kind
// without the route is a usage error (exit 2) naming its kind.
func resolveK3sDisabledComponentsCluster(typed string) (string, string, error) {
	clusterID, resolveError := resolveClusterArg(typed)
	if resolveError != nil {
		return "", "", resolveError
	}
	cluster, lookupError := apiClient.GetClusterByID(clusterID)
	if lookupError != nil {
		return "", "", fmt.Errorf("looking up cluster %q: %w", clusterID, lookupError)
	}
	if !slices.Contains(client.K3sDisabledComponentsKinds, cluster.Kind) {
		return "", "", unsupportedClusterKindError(typed, cluster.Kind, "k3s disabled components")
	}
	return clusterID, cluster.Kind, nil
}

func printK3sDisabledComponents(command *cobra.Command, components *client.K3sDisabledComponents) {
	out := command.OutOrStdout()
	if len(components.K3sDisabledComponents) == 0 {
		_, _ = fmt.Fprintln(out, "Disabled k3s components: none stored (the provider shape's own defaults apply)")
	} else {
		_, _ = fmt.Fprintf(out, "Disabled k3s components: %s\n", strings.Join(components.K3sDisabledComponents, ", "))
	}
	_, _ = fmt.Fprintf(out, "Control planes:          %d\n", components.ControlPlaneCount)
	if components.Applies != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", components.Applies)
	}
}

// confirmProviderSettingChange prints what a change does and asks for it to
// be confirmed. --yes skips the question; without a terminal to ask on,
// --yes is required. Everything goes to stderr so -o json|yaml stays
// parseable.
func confirmProviderSettingChange(command *cobra.Command, consequence string) error {
	yes, _ := command.Flags().GetBool("yes")
	if yes {
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"%s This needs confirmation, but there is no terminal to ask on; re-run with --yes to confirm", consequence))
	}
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprintln(errOut, consequence)
	if promptError := confirmPrompt(command.InOrStdin(), errOut, "Continue? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// requiredProviderFlag reads a flag that has to carry a value.
func requiredProviderFlag(command *cobra.Command, flagName string, meaning string) (string, error) {
	value, _ := command.Flags().GetString(flagName)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("--%s is required: %s", flagName, meaning))
	}
	return value, nil
}

func highAvailabilityLabel(highlyAvailable bool) string {
	if highlyAvailable {
		return "yes"
	}
	return "no"
}

var upcloudGatewayPlansCmd = &cobra.Command{
	Use:   "gateway-plans --credential-id <id>",
	Short: "List the UpCloud NAT gateway plans a credential's account can create",
	Long: `List UpCloud's NAT gateway plans, read live with the given credential: bandwidth,
how many gateway servers the plan runs and whether it is highly available (two
or more servers, which keeps egress up while UpCloud replaces one). It also
names the plan a cluster created without one gets, and what a day-2 plan change
does to a cluster's outbound internet access.`,
	Example: `  ankra cluster upcloud gateway-plans --credential-id <id>
  ankra cluster upcloud gateway-plans --credential-id <id> -o json`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		credentialID, flagError := requiredProviderFlag(command, "credential-id", "the UpCloud credential whose account is read ('ankra credentials upcloud list')")
		if flagError != nil {
			return flagError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		plans, listError := apiClient.ListUpcloudGatewayPlans(ctx, credentialID)
		if listError != nil {
			return fmt.Errorf("listing UpCloud NAT gateway plans: %w", listError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, plans)
		}
		out := command.OutOrStdout()
		_, _ = fmt.Fprintf(out, "%-28s %-10s %-8s %-4s %s\n", "PLAN", "MBIT/S", "SERVERS", "HA", "MAX CONNECTIONS")
		for _, plan := range plans.Plans {
			name := plan.Name
			if name == plans.DefaultPlan {
				name += " (default)"
			}
			_, _ = fmt.Fprintf(out, "%-28s %-10d %-8d %-4s %d\n", name, plan.BandwidthMbps, plan.ServerNumber,
				highAvailabilityLabel(plan.HighAvailability), plan.MaxConnections)
		}
		if plans.PlanChangeNotice != "" {
			_, _ = fmt.Fprintf(out, "\n%s\n", plans.PlanChangeNotice)
		}
		return nil
	},
}

var upcloudGatewayPlanCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "gateway-plan <cluster_id|name> --plan <plan>",
	Short: "Change the plan of a cluster's UpCloud NAT gateways",
	Long: `Change the plan of the cluster's UpCloud NAT gateways in place. --plan is a plan
name from 'ankra cluster upcloud gateway-plans'.

This interrupts the cluster's outbound internet access while UpCloud rebuilds
the gateway: expect several minutes without outbound access, so choose a quiet
time. That is why it asks for confirmation; --yes skips the question and is
required when there is no terminal to ask on. The command returns once the
change is recorded; the platform carries it out and reports it complete only
after the gateway runs on the new plan and the cluster's agent has checked in
again. A stopped cluster's gateways take the plan at the next start.`,
	Example: `  ankra cluster upcloud gateway-plan prod --plan production
  ankra cluster upcloud gateway-plan prod --plan production --yes -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		plan, flagError := requiredProviderFlag(command, "plan", "a plan name from 'ankra cluster upcloud gateway-plans'")
		if flagError != nil {
			return flagError
		}
		clusterID, resolveError := resolveClusterArg(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		if confirmError := confirmProviderSettingChange(command, fmt.Sprintf(
			"Changing the NAT gateway plan of cluster %s to %s interrupts its outbound internet access for several minutes "+
				"while UpCloud rebuilds the gateway.", clusterTarget(arguments[0], clusterID), plan)); confirmError != nil {
			return confirmError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		change, setError := apiClient.SetUpcloudGatewayPlan(ctx, clusterID, plan)
		if setError != nil {
			return fmt.Errorf("changing the NAT gateway plan: %w", setError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, change)
		}
		out := command.OutOrStdout()
		_, _ = fmt.Fprintf(out, "NAT gateway plan change to %s recorded (request %s).\n", change.GatewayPlan, change.PlanChangeRequestID)
		_, _ = fmt.Fprintf(out, "  Gateways being changed now:     %d\n", len(change.ResourceIDs))
		_, _ = fmt.Fprintf(out, "  Gateways changed at next start: %d\n", len(change.DeferredResourceIDs))
		if change.Notice != "" {
			_, _ = fmt.Fprintf(out, "\n%s\n", change.Notice)
		}
		return nil
	},
})

func printUpcloudLoadBalancerPlans(command *cobra.Command, plans []client.UpcloudLoadBalancerPlan) {
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "%-28s %-8s %-4s %s\n", "PLAN", "NODES", "HA", "MAX SESSIONS PER NODE")
	for _, plan := range plans {
		_, _ = fmt.Fprintf(out, "%-28s %-8d %-4s %d\n", plan.Name, plan.ServerNumber,
			highAvailabilityLabel(plan.HighlyAvailable), plan.PerServerMaxSessions)
	}
}

var upcloudLoadBalancerPlansCmd = &cobra.Command{
	Use:   "load-balancer-plans --credential-id <id>",
	Short: "List UpCloud's managed load balancer plans",
	Long: `List UpCloud's managed load balancer plans, read live with the given credential:
the choices for a cluster's ingress load balancer. A plan that runs more than
one load balancer node is highly available.`,
	Example: `  ankra cluster upcloud load-balancer-plans --credential-id <id>`,
	Args:    cobra.NoArgs,
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		credentialID, flagError := requiredProviderFlag(command, "credential-id", "the UpCloud credential whose account is read ('ankra credentials upcloud list')")
		if flagError != nil {
			return flagError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		plans, listError := apiClient.ListUpcloudLoadBalancerPlans(ctx, credentialID)
		if listError != nil {
			return fmt.Errorf("listing UpCloud load balancer plans: %w", listError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, plans)
		}
		printUpcloudLoadBalancerPlans(command, plans)
		return nil
	},
}

var upcloudLoadBalancerPlanCmd = &cobra.Command{
	Use:   "load-balancer-plan",
	Short: "Show or change the plan of a cluster's ingress load balancer",
	Long: `The UpCloud plan of the load balancer in front of the cluster's Ankra-managed
ingress. 'get' shows the plan Ankra has recorded beside the plans the live load
balancers run on; 'set' records a new plan and the balancer moves onto it in
place.`,
}

var upcloudLoadBalancerPlanGetCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "get <cluster_id|name>",
	Short: "Show the recorded and live ingress load balancer plans",
	Long: `Show the plan Ankra has recorded for the cluster's ingress load balancer beside
the plans its live load balancers run on. No recorded plan means none was ever
set, and Ankra then leaves the balancer's plan alone. When UpCloud cannot be
read the live balancers are reported as not observed, never as none.`,
	Example: `  ankra cluster upcloud load-balancer-plan get prod
  ankra cluster upcloud load-balancer-plan get prod -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		clusterID, resolveError := resolveClusterArg(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		plan, getError := apiClient.GetUpcloudClusterLoadBalancerPlan(ctx, clusterID)
		if getError != nil {
			return fmt.Errorf("reading the ingress load balancer plan: %w", getError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, plan)
		}
		out := command.OutOrStdout()
		if plan.LoadBalancerPlan == nil {
			_, _ = fmt.Fprintln(out, "Recorded plan: none (Ankra leaves the live plan alone)")
		} else {
			_, _ = fmt.Fprintf(out, "Recorded plan: %s\n", *plan.LoadBalancerPlan)
		}
		if !plan.Managed {
			_, _ = fmt.Fprintln(out, "Ankra does not render this cluster's ingress load balancer.")
		}
		if !plan.LoadBalancersObserved {
			reason := "UpCloud could not be read"
			if plan.ObservationError != nil && *plan.ObservationError != "" {
				reason = *plan.ObservationError
			}
			_, _ = fmt.Fprintf(out, "Live load balancers: not observed (%s)\n", reason)
		} else {
			_, _ = fmt.Fprintf(out, "Live load balancers: %d\n", len(plan.LoadBalancers))
			for _, balancer := range plan.LoadBalancers {
				role := ""
				if balancer.Ingress {
					role = " (ingress)"
				}
				_, _ = fmt.Fprintf(out, "  %s: plan %s, %s%s\n", balancer.Name, balancer.Plan, balancer.OperationalState, role)
			}
		}
		if len(plan.AvailablePlans) > 0 {
			_, _ = fmt.Fprintln(out, "\nAvailable plans:")
			printUpcloudLoadBalancerPlans(command, plan.AvailablePlans)
		}
		return nil
	},
})

var upcloudLoadBalancerPlanSetCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "set <cluster_id|name> --plan <plan>",
	Short: "Record a new plan for the cluster's ingress load balancer",
	Long: `Record a new UpCloud plan for the cluster's ingress load balancer. Ankra
re-renders the ingress Service and UpCloud's cloud controller moves the existing
balancer onto the plan in place. --plan is a plan name from 'ankra cluster
upcloud load-balancer-plan get' or 'load-balancer-plans'.

The platform checks the plan against UpCloud's catalogue and refuses (409) an
unknown plan, a cluster whose ingress load balancer Ankra does not render, and
a cluster whose cloud provider stack has not settled. Naming the plan already
recorded changes nothing.`,
	Example: `  ankra cluster upcloud load-balancer-plan set prod --plan production-small`,
	Args:    cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		plan, flagError := requiredProviderFlag(command, "plan", "a plan name from 'ankra cluster upcloud load-balancer-plans'")
		if flagError != nil {
			return flagError
		}
		clusterID, resolveError := resolveClusterArg(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		change, setError := apiClient.SetUpcloudClusterLoadBalancerPlan(ctx, clusterID, plan)
		if setError != nil {
			return fmt.Errorf("changing the ingress load balancer plan: %w", setError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, change)
		}
		out := command.OutOrStdout()
		if !change.Changed {
			_, _ = fmt.Fprintf(out, "Ingress load balancer plan is already %s; nothing changed.\n", change.LoadBalancerPlan)
			return nil
		}
		previous := "none recorded"
		if change.PreviousLoadBalancerPlan != nil {
			previous = *change.PreviousLoadBalancerPlan
		}
		_, _ = fmt.Fprintf(out, "Ingress load balancer plan recorded: %s (was %s).\n", change.LoadBalancerPlan, previous)
		return nil
	},
})

var upcloudAvailableZonesCmd = &cobra.Command{
	Use:   "available-zones --credential-id <id>",
	Short: "List the UpCloud zones a credential's account can deploy in",
	Long: `List the public UpCloud zones the credential's account can deploy in, read live
from UpCloud: the values 'ankra cluster upcloud create --zone' and
'ankra cluster upcloud zones' take.`,
	Example: `  ankra cluster upcloud available-zones --credential-id <id>`,
	Args:    cobra.NoArgs,
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		credentialID, flagError := requiredProviderFlag(command, "credential-id", "the UpCloud credential whose account is read ('ankra credentials upcloud list')")
		if flagError != nil {
			return flagError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		zones, listError := apiClient.ListUpcloudZones(ctx, credentialID)
		if listError != nil {
			return fmt.Errorf("listing UpCloud zones: %w", listError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, zones)
		}
		out := command.OutOrStdout()
		_, _ = fmt.Fprintf(out, "%-12s %s\n", "ZONE", "DESCRIPTION")
		for _, zone := range zones {
			_, _ = fmt.Fprintf(out, "%-12s %s\n", zone.ID, zone.Description)
		}
		return nil
	},
}

var ovhGatewayModelCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "gateway-model <cluster_id|name> --model <model>",
	Short: "Change the model of a cluster's OVH managed gateway",
	Long: `Change the model (size) of the cluster's OVH managed gateway: s, m, l, xl, 2xl or
3xl. The platform refuses (409) an unsupported model, a cluster with no recorded
managed gateway and a cluster with no active network resource. Resizing a
gateway can interrupt the cluster's outbound internet access, so this asks for
confirmation; --yes skips the question and is required when there is no
terminal to ask on.`,
	Example: `  ankra cluster ovh gateway-model prod --model m
  ankra cluster ovh gateway-model prod --model l --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		model, flagError := requiredProviderFlag(command, "model", "the gateway model: s, m, l, xl, 2xl or 3xl")
		if flagError != nil {
			return flagError
		}
		clusterID, resolveError := resolveClusterArg(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		if confirmError := confirmProviderSettingChange(command, fmt.Sprintf(
			"Changing the gateway model of cluster %s to %s can interrupt its outbound internet access while OVH resizes the gateway.",
			clusterTarget(arguments[0], clusterID), model)); confirmError != nil {
			return confirmError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		change, setError := apiClient.SetOvhGatewayModel(ctx, clusterID, model)
		if setError != nil {
			return fmt.Errorf("changing the gateway model: %w", setError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, change)
		}
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Gateway model set to %s on %d gateway resource(s).\n",
			change.GatewayModel, len(change.ResourceIDs))
		return nil
	},
})

var hetznerExternalCloudProviderCmd = &cobra.Command{
	Use:   "external-cloud-provider",
	Short: "Converge a cluster's stored external cloud provider flag",
	Long: `Whether a Hetzner k3s cluster's Kubernetes node definitions say its nodes run
for the hcloud cloud controller manager (external_cloud_provider). The day-2
lanes read that flag, so a cluster whose nodes already run that way while its
definitions say otherwise is converged with 'converge'.`,
}

var hetznerExternalCloudProviderConvergeCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "converge <cluster_id|name>",
	Short: "Store external_cloud_provider=true where the nodes already run that way",
	Long: `Store external_cloud_provider=true on every Kubernetes node definition of a
Hetzner k3s cluster whose definitions say false while its nodes already run for
the hcloud cloud controller manager.

The flag is only written when every node the platform has seen in the last 30
minutes already carries the external shape (the kubelet argument
cloud-provider=external, on a k3s server also the disabled cloud controller and
servicelb, and an hcloud:// provider id). Only the stored definitions change: no
node is restarted, re-tainted or re-registered. A cluster that already stores
true everywhere answers with nothing converged.`,
	Example: `  ankra cluster hetzner external-cloud-provider converge prod
  ankra cluster hetzner external-cloud-provider converge prod -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		format, formatError := structuredFormatFromFlags(command)
		if formatError != nil {
			return formatError
		}
		clusterID, resolveError := resolveClusterArg(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		ctx, cancel := hostedLogsContext(command)
		defer cancel()
		convergence, convergeError := apiClient.ConvergeHetznerExternalCloudProvider(ctx, clusterID)
		if convergeError != nil {
			return fmt.Errorf("converging the external cloud provider flag: %w", convergeError)
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, convergence)
		}
		out := command.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Node definitions converged:     %d\n", len(convergence.ConvergedResourceIDs))
		_, _ = fmt.Fprintf(out, "Node definitions already true:  %d\n", len(convergence.AlreadyExternalResourceIDs))
		if convergence.VerifiedNodeCount != nil {
			_, _ = fmt.Fprintf(out, "Nodes verified as external:     %d\n", *convergence.VerifiedNodeCount)
		}
		if convergence.Applies != "" {
			_, _ = fmt.Fprintf(out, "\n%s\n", convergence.Applies)
		}
		return nil
	},
})

func init() {
	clusterK3sDisabledComponentsSetCmd.Flags().StringSlice("components", nil, "The complete list of k3s components to keep disabled (required), for example traefik,servicelb")
	clusterK3sDisabledComponentsSetCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	registerStructuredOutputFlags(clusterK3sDisabledComponentsGetCmd, clusterK3sDisabledComponentsSetCmd)
	clusterK3sDisabledComponentsCmd.AddCommand(clusterK3sDisabledComponentsGetCmd, clusterK3sDisabledComponentsSetCmd)
	clusterCmd.AddCommand(clusterK3sDisabledComponentsCmd)

	upcloudGatewayPlansCmd.Flags().String("credential-id", "", "UpCloud credential whose account is read (required)")
	upcloudLoadBalancerPlansCmd.Flags().String("credential-id", "", "UpCloud credential whose account is read (required)")
	upcloudAvailableZonesCmd.Flags().String("credential-id", "", "UpCloud credential whose account is read (required)")
	upcloudGatewayPlanCmd.Flags().String("plan", "", "NAT gateway plan name (required)")
	upcloudGatewayPlanCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	upcloudLoadBalancerPlanSetCmd.Flags().String("plan", "", "Load balancer plan name (required)")
	registerStructuredOutputFlags(upcloudGatewayPlansCmd, upcloudGatewayPlanCmd, upcloudLoadBalancerPlansCmd,
		upcloudLoadBalancerPlanGetCmd, upcloudLoadBalancerPlanSetCmd, upcloudAvailableZonesCmd)
	upcloudLoadBalancerPlanCmd.AddCommand(upcloudLoadBalancerPlanGetCmd, upcloudLoadBalancerPlanSetCmd)
	upcloudCmd.AddCommand(upcloudGatewayPlansCmd, upcloudGatewayPlanCmd, upcloudLoadBalancerPlansCmd,
		upcloudLoadBalancerPlanCmd, upcloudAvailableZonesCmd)

	ovhGatewayModelCmd.Flags().String("model", "", "Gateway model: s, m, l, xl, 2xl or 3xl (required)")
	ovhGatewayModelCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	registerStructuredOutputFlags(ovhGatewayModelCmd)
	ovhCmd.AddCommand(ovhGatewayModelCmd)

	registerStructuredOutputFlags(hetznerExternalCloudProviderConvergeCmd)
	hetznerExternalCloudProviderCmd.AddCommand(hetznerExternalCloudProviderConvergeCmd)
	hetznerCmd.AddCommand(hetznerExternalCloudProviderCmd)
}
