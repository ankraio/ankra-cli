package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The catalogue and the two declarations a setup needs before it can be
// reviewed: where the service cluster is (its placement policy) and which
// application namespace may consume the service (a consumer binding).

func newServicesPackagesCommand() *cobra.Command {
	packagesCommand := &cobra.Command{
		Use:     "packages",
		Aliases: []string{"package", "catalog", "catalogue"},
		Short:   "Browse the service catalogue",
		Long: `Browse the service catalogue: the immutable package versions this organisation
may set up, Ankra's own (PostgreSQL, Valkey, OpenSearch, ...) and any shared
with it.

Being listed is not being deployable: setup re-checks access and the pinned
profile when the review is prepared.`,
	}
	packagesCommand.AddCommand(newServicesPackagesListCommand())
	packagesCommand.AddCommand(newServicesPackagesGetCommand())
	return packagesCommand
}

func newServicesPackagesListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the service packages this organisation can set up",
		Long: `List the service packages this organisation can set up: each version's name,
capability (database, cache, search, metrics, logs, ...), publisher and id.
Pass the name, name@version or id to 'ankra services setup --package'.`,
		Example: "  ankra services packages list\n  ankra services packages list -o json",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			packages, listError := listAllServicePackages(command.Context())
			if listError != nil {
				return listError
			}
			sort.SliceStable(packages, func(left, right int) bool {
				if packages[left].Name != packages[right].Name {
					return packages[left].Name < packages[right].Name
				}
				return packages[left].Version < packages[right].Version
			})
			if rendered, renderError := renderStructured(command, packages); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(packages) == 0 {
				_, _ = fmt.Fprintln(out, "No service packages are available to this organisation.")
				return nil
			}
			packageTable := table.NewWriter()
			packageTable.SetOutputMirror(out)
			packageTable.SetStyle(table.StyleRounded)
			packageTable.AppendHeader(table.Row{"Name", "Version", "Capability", "Publisher", "ID"})
			for _, servicePackage := range packages {
				packageTable.AppendRow(table.Row{servicePackage.Name, servicePackage.Version, servicePackage.Capability,
					servicePackagePublisher(servicePackage), servicePackage.ID})
			}
			packageTable.Render()
			return nil
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func servicePackagePublisher(servicePackage client.ServicePackageSummary) string {
	if servicePackage.FirstParty {
		return "Ankra"
	}
	return servicePackage.PublisherID
}

func newServicesPackagesGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <package>",
		Short: "Show a package's contract: modes, parameters, secret inputs and outputs",
		Long: `Show a package version's contract: the modes it can run in, the parameters a
setup may choose (with their bounds and defaults), the secret inputs it needs
and the outputs it publishes. The package is a name, name@version or id.`,
		Example: "  ankra services packages get postgresql\n  ankra services packages get postgresql@1.0.0 -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			versionID, resolveError := resolveServicePackageID(command.Context(), arguments[0])
			if resolveError != nil {
				return resolveError
			}
			detail, getError := apiClient.GetServicePackage(command.Context(), versionID)
			if getError != nil {
				return getError
			}
			if rendered, renderError := renderStructured(command, detail); rendered || renderError != nil {
				return renderError
			}
			printServicePackage(command.OutOrStdout(), *detail)
			return nil
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func servicePackageModes(contract client.ServicePackageContract) []string {
	modes := make([]string, 0, len(contract.Profiles))
	for mode := range contract.Profiles {
		modes = append(modes, mode)
	}
	sort.Strings(modes)
	return modes
}

func printServicePackage(out io.Writer, detail client.ServicePackageDetail) {
	_, _ = fmt.Fprintf(out, "Package %s %s\n", detail.Name, detail.Version)
	_, _ = fmt.Fprintf(out, "  ID:          %s\n", detail.ID)
	_, _ = fmt.Fprintf(out, "  Capability:  %s\n", detail.Capability)
	_, _ = fmt.Fprintf(out, "  Publisher:   %s\n", servicePackagePublisher(detail.ServicePackageSummary))
	_, _ = fmt.Fprintf(out, "  Digest:      %s\n", detail.Digest)
	_, _ = fmt.Fprintf(out, "  Modes:       %s\n", strings.Join(servicePackageModes(detail.Contract), ", "))
	if len(detail.Contract.Parameters) == 0 {
		_, _ = fmt.Fprintln(out, "  Parameters:  none")
	} else {
		_, _ = fmt.Fprintln(out, "  Parameters (--param name=value):")
		for _, parameter := range detail.Contract.Parameters {
			_, _ = fmt.Fprintf(out, "    %-16s default %d, %d-%d %s\n", parameter.Name, parameter.Default,
				parameter.Minimum, parameter.Maximum, parameter.Unit)
		}
	}
	if len(detail.Contract.Secrets) == 0 {
		_, _ = fmt.Fprintln(out, "  Secret inputs: none")
	} else {
		_, _ = fmt.Fprintln(out, "  Secret inputs (--secret-reference name=<credential grant id>):")
		for _, secret := range detail.Contract.Secrets {
			_, _ = fmt.Fprintf(out, "    %s (modes: %s)\n", secret.Name, strings.Join(secret.Modes, ", "))
		}
	}
	if len(detail.Contract.Outputs) > 0 {
		_, _ = fmt.Fprintln(out, "  Outputs:")
		for _, output := range detail.Contract.Outputs {
			_, _ = fmt.Fprintf(out, "    %s (%s)\n", output.Name, output.Kind)
		}
	}
}

// resolveServicePackageID turns a package reference into a version id: an
// id as given, or a name / name@version matched against the catalogue. A
// name with several matching versions is refused rather than guessed.
func resolveServicePackageID(ctx context.Context, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", withExitCode(exitUsage, errors.New("a package name, name@version or id is required"))
	}
	if looksLikeUUID(reference) {
		return reference, nil
	}
	name, version, hasVersion := strings.Cut(reference, "@")
	packages, listError := listAllServicePackages(ctx)
	if listError != nil {
		return "", listError
	}
	var matches []client.ServicePackageSummary
	for _, servicePackage := range packages {
		if servicePackage.Name == name && (!hasVersion || servicePackage.Version == version) {
			matches = append(matches, servicePackage)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return "", withExitCode(exitNotFound, fmt.Errorf(
			"no service package %q - run 'ankra services packages list' to see the catalogue", reference))
	default:
		candidates := make([]string, 0, len(matches))
		for _, servicePackage := range matches {
			candidates = append(candidates, fmt.Sprintf("%s@%s by %s (%s)", servicePackage.Name, servicePackage.Version,
				servicePackagePublisher(servicePackage), servicePackage.ID))
		}
		return "", withExitCode(exitUsage, fmt.Errorf("%d packages match %q - pass name@version or the id instead: %s",
			len(matches), reference, strings.Join(candidates, "; ")))
	}
}

func newServicesPolicyCommand() *cobra.Command {
	policyCommand := &cobra.Command{
		Use:   "policy",
		Short: "Read or declare where a cluster is, for service placement",
		Long: `Read or declare a cluster's service placement policy: the physical region it
runs in, the data boundary its data may stay within, and whether services on
it are local-only.

This is the organisation's declaration, not verified location evidence. Setup
refuses a cluster without one, and a setup's region and data boundary default
to it.`,
	}
	policyCommand.AddCommand(newServicesPolicyGetCommand())
	policyCommand.AddCommand(newServicesPolicySetCommand())
	return policyCommand
}

func newServicesPolicyGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:     "get",
		Short:   "Show a cluster's service placement policy",
		Example: "  ankra services policy get --cluster prod",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			clusterFlag, _ := command.Flags().GetString("cluster")
			clusterID, clusterName, resolveError := resolveClusterForCmd(strings.TrimSpace(clusterFlag))
			if resolveError != nil {
				return resolveError
			}
			policy, getError := apiClient.GetServiceClusterPolicy(command.Context(), clusterID)
			if getError != nil {
				if isServicePolicyMissing(getError) {
					return withExitCode(exitNotFound, fmt.Errorf("cluster %s has no service placement policy: "+
						"declare one with 'ankra services policy set --cluster %s --region <region> --data-boundary <boundary>'",
						clusterName, clusterName))
				}
				return getError
			}
			if rendered, renderError := renderStructured(command, policy); rendered || renderError != nil {
				return renderError
			}
			printServiceClusterPolicy(command.OutOrStdout(), clusterName, *policy)
			return nil
		},
	}
	getCommand.Flags().String("cluster", "", "The cluster (name or id; default: the selected cluster)")
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

// servicePolicyRequiredDetail is the platform's answer (409) for a cluster
// that has no placement policy yet, on a policy read and on anything that
// needs one.
const servicePolicyRequiredDetail = "Configure the cluster data policy before connecting services"

// isServicePolicyMissing reports the platform's refusal for a cluster
// without a placement policy.
func isServicePolicyMissing(lookupError error) bool {
	var unexpected *client.UnexpectedResponseError
	return errors.As(lookupError, &unexpected) && unexpected.StatusCode == http.StatusConflict &&
		strings.Contains(unexpected.Detail, servicePolicyRequiredDetail)
}

func printServiceClusterPolicy(out io.Writer, clusterName string, policy client.ServiceClusterPolicy) {
	_, _ = fmt.Fprintf(out, "Service placement policy of cluster %s\n", clusterName)
	_, _ = fmt.Fprintf(out, "  Cluster:        %s\n", policy.ClusterID)
	_, _ = fmt.Fprintf(out, "  Region:         %s\n", policy.Region)
	_, _ = fmt.Fprintf(out, "  Data boundary:  %s\n", policy.DataBoundary)
	_, _ = fmt.Fprintf(out, "  Local only:     %t\n", policy.LocalOnly)
	_, _ = fmt.Fprintf(out, "  Revision:       %d\n", policy.Revision)
	_, _ = fmt.Fprintf(out, "  Source:         %s\n", policy.Source)
	_, _ = fmt.Fprintf(out, "  Updated:        %s\n", policy.UpdatedAt)
}

func newServicesPolicySetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set",
		Short: "Declare a cluster's region and data boundary for service placement",
		Long: `Declare a cluster's service placement policy: the physical region it runs in
(for example eu-north-1), the data boundary its data may stay within (for
example eu) and whether services on it are local-only.

The first declaration creates the policy. Replacing one needs --revision with
the revision you reviewed ('ankra services policy get' shows it), so two
people cannot overwrite each other's declaration unseen. Nothing is deployed.`,
		Example: "  ankra services policy set --cluster prod --region eu-north-1 --data-boundary eu\n" +
			"  ankra services policy set --cluster prod --region eu-north-1 --data-boundary eu --local-only --revision 1",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			region, _ := command.Flags().GetString("region")
			dataBoundary, _ := command.Flags().GetString("data-boundary")
			localOnly, _ := command.Flags().GetBool("local-only")
			revision, _ := command.Flags().GetInt64("revision")
			region, dataBoundary = strings.TrimSpace(region), strings.TrimSpace(dataBoundary)
			if region == "" || dataBoundary == "" {
				return withExitCode(exitUsage, errors.New("--region and --data-boundary are required"))
			}
			if revision < 0 {
				return withExitCode(exitUsage, errors.New("--revision must be the policy's current revision (0 creates one)"))
			}
			clusterFlag, _ := command.Flags().GetString("cluster")
			clusterID, clusterName, resolveError := resolveClusterForCmd(strings.TrimSpace(clusterFlag))
			if resolveError != nil {
				return resolveError
			}
			if revision == 0 {
				current, getError := apiClient.GetServiceClusterPolicy(command.Context(), clusterID)
				switch {
				case getError == nil:
					return withExitCode(exitUsage, fmt.Errorf("cluster %s already has a placement policy (region %s, data boundary %s, "+
						"local only %t) at revision %d: pass --revision %d to replace it",
						clusterName, current.Region, current.DataBoundary, current.LocalOnly, current.Revision, current.Revision))
				case !isServicePolicyMissing(getError):
					return getError
				}
			}
			policy, setError := apiClient.SetServiceClusterPolicy(command.Context(), clusterID, client.ServiceClusterPolicyRequest{
				ExpectedRevision: revision, Region: region, DataBoundary: dataBoundary, LocalOnly: localOnly,
			})
			if setError != nil {
				return setError
			}
			if rendered, renderError := renderStructured(command, policy); rendered || renderError != nil {
				return renderError
			}
			printServiceClusterPolicy(command.OutOrStdout(), clusterName, *policy)
			return nil
		},
	}
	setCommand.Flags().String("cluster", "", "The cluster (name or id; default: the selected cluster)")
	setCommand.Flags().String("region", "", "Physical region the cluster runs in, for example eu-north-1")
	setCommand.Flags().String("data-boundary", "", "Boundary the data may stay within, for example eu")
	setCommand.Flags().Bool("local-only", false, "Services on this cluster serve only consumers on this cluster")
	setCommand.Flags().Int64("revision", 0, "The current revision of the policy being replaced (omit to create one)")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func newServicesConsumersCommand() *cobra.Command {
	consumersCommand := &cobra.Command{
		Use:     "consumers",
		Aliases: []string{"consumer"},
		Short:   "Bind and list the application namespaces that may consume a service",
		Long: `Bind and list consumers: the application namespaces a service is set up for.

A consumer is one application's namespace on one cluster, with a stable id.
Setup takes one or more consumer ids (--consumer), and the plan records each
with the cluster policy it was resolved against. A binding normally resolves
to the application's installation on that cluster; --allow-planned binds a
namespace the application is not installed in yet.`,
	}
	consumersCommand.AddCommand(newServicesConsumersListCommand())
	consumersCommand.AddCommand(newServicesConsumersGetCommand())
	consumersCommand.AddCommand(newServicesConsumersBindCommand())
	return consumersCommand
}

func newServicesConsumersListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List an application's consumer bindings",
		Long: `List an application's consumer bindings as last saved: each binding's id (what
setup's --consumer takes), cluster, namespace and revision. Bindings on
clusters you cannot read are not listed.`,
		Example: "  ankra services consumers list --application orders",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationReference, _ := command.Flags().GetString("application")
			applicationID, resolveError := resolveApplicationID(command.Context(), apiClient, applicationReference)
			if resolveError != nil {
				return resolveError
			}
			bindings, listError := collectServicePages(func(after string) ([]client.ServiceConsumerBinding, *string, error) {
				page, pageError := apiClient.ListServiceConsumers(command.Context(), applicationID,
					client.ServicePageOptions{Limit: servicePageLimit, After: after})
				if pageError != nil {
					return nil, nil, pageError
				}
				return page.Items, page.NextCursor, nil
			})
			if listError != nil {
				return listError
			}
			if rendered, renderError := renderStructured(command, bindings); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(bindings) == 0 {
				_, _ = fmt.Fprintln(out, "No consumer bindings for this application. Bind one with 'ankra services consumers bind'.")
				return nil
			}
			names := newServiceNames(command)
			bindingTable := table.NewWriter()
			bindingTable.SetOutputMirror(out)
			bindingTable.SetStyle(table.StyleRounded)
			bindingTable.AppendHeader(table.Row{"ID", "Cluster", "Namespace", "Planned allowed", "Local only", "Revision", "Updated"})
			for _, binding := range bindings {
				bindingTable.AppendRow(table.Row{binding.ID, names.cluster(binding.ClusterID), binding.Namespace,
					binding.AllowPlanned, binding.LocalOnly, binding.Revision, binding.UpdatedAt})
			}
			bindingTable.Render()
			return nil
		},
	}
	listCommand.Flags().String("application", "", "The application (name or id)")
	_ = listCommand.MarkFlagRequired("application")
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newServicesConsumersGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <consumer-id>",
		Short: "Show a consumer binding, resolved against the cluster policy and installation",
		Long: `Show a consumer binding resolved as setup would resolve it: the cluster's
declared region and data boundary, the policy and binding revisions, and the
application installation it binds (none for a planned binding).`,
		Example: "  ankra services consumers get 5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c11",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			consumer, getError := apiClient.GetServiceConsumer(command.Context(), strings.TrimSpace(arguments[0]))
			if getError != nil {
				return getError
			}
			if rendered, renderError := renderStructured(command, consumer); rendered || renderError != nil {
				return renderError
			}
			printServiceConsumer(command.OutOrStdout(), newServiceNames(command), *consumer)
			return nil
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func printServiceConsumer(out io.Writer, names *serviceNames, consumer client.ServiceConsumer) {
	_, _ = fmt.Fprintf(out, "Consumer %s\n", consumer.ID)
	_, _ = fmt.Fprintf(out, "  Application:  %s\n", consumer.ApplicationID)
	_, _ = fmt.Fprintf(out, "  Cluster:      %s\n", names.clusterWithID(consumer.ClusterID))
	_, _ = fmt.Fprintf(out, "  Namespace:    %s\n", consumer.Namespace)
	_, _ = fmt.Fprintf(out, "  Location:     region %s, data boundary %s\n", consumer.Region, consumer.DataBoundary)
	_, _ = fmt.Fprintf(out, "  Local only:   %t\n", consumer.LocalOnly)
	if consumer.BindingKind == "planned" || consumer.InstallationID == nil {
		_, _ = fmt.Fprintln(out, "  Binding:      planned (the application is not installed in this namespace yet)")
	} else {
		_, _ = fmt.Fprintf(out, "  Binding:      installation %s\n", *consumer.InstallationID)
	}
	_, _ = fmt.Fprintf(out, "  Revisions:    binding %d, cluster policy %d\n", consumer.BindingRevision, consumer.PolicyRevision)
}

// findServiceConsumerBinding returns the application's stored binding of
// the namespace on the cluster, if it has one, and whether the bindings
// could be read at all: an unreadable listing is "not known", never "not
// bound". A binding exists once per application, cluster and namespace, and
// the platform answers a create of an existing one with a bare
// "configuration changed" conflict, so bind looks first and names the
// binding instead. The failure is said on stderr.
func findServiceConsumerBinding(command *cobra.Command, applicationID string, clusterID string, namespace string) (*client.ServiceConsumerBinding, bool) {
	ctx := command.Context()
	bindings, listError := collectServicePages(func(after string) ([]client.ServiceConsumerBinding, *string, error) {
		page, pageError := apiClient.ListServiceConsumers(ctx, applicationID, client.ServicePageOptions{Limit: servicePageLimit, After: after})
		if pageError != nil {
			return nil, nil, pageError
		}
		return page.Items, page.NextCursor, nil
	})
	if listError != nil {
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "Note: the application's existing bindings could not be read (%v).\n", listError)
		return nil, false
	}
	for index := range bindings {
		if bindings[index].ClusterID == clusterID && bindings[index].Namespace == namespace {
			return &bindings[index], true
		}
	}
	return nil, true
}

func newServicesConsumersBindCommand() *cobra.Command {
	bindCommand := &cobra.Command{
		Use:   "bind",
		Short: "Bind an application namespace as a service consumer",
		Long: `Bind an application's namespace on a cluster as a service consumer and print
its consumer id, which 'ankra services setup --consumer' takes.

The cluster needs a placement policy first ('ankra services policy set'). The
application must be installed in that namespace unless --allow-planned is
given. A binding is created once per application, cluster and namespace;
changing an existing one needs --revision with its current revision
('ankra services consumers list --application <app>' shows it); a setting
not given on the command line keeps the binding's current value. Binding
deploys nothing and delivers no credentials.`,
		Example: "  ankra services consumers bind --application orders --cluster prod --namespace orders\n" +
			"  ankra services consumers bind --application orders --cluster prod --namespace orders --allow-planned",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			namespace, _ := command.Flags().GetString("namespace")
			namespace = strings.TrimSpace(namespace)
			if namespace == "" {
				return withExitCode(exitUsage, errors.New("--namespace is required"))
			}
			allowPlanned, _ := command.Flags().GetBool("allow-planned")
			localOnly, _ := command.Flags().GetBool("local-only")
			revision, _ := command.Flags().GetInt64("revision")
			if revision < 0 {
				return withExitCode(exitUsage, errors.New("--revision must be the binding's current revision (0 creates one)"))
			}
			applicationReference, _ := command.Flags().GetString("application")
			applicationID, resolveError := resolveApplicationID(command.Context(), apiClient, applicationReference)
			if resolveError != nil {
				return resolveError
			}
			clusterFlag, _ := command.Flags().GetString("cluster")
			clusterID, _, clusterError := resolveClusterForCmd(strings.TrimSpace(clusterFlag))
			if clusterError != nil {
				return clusterError
			}
			existing, bindingsRead := findServiceConsumerBinding(command, applicationID, clusterID, namespace)
			bothSettingsGiven := command.Flags().Changed("allow-planned") && command.Flags().Changed("local-only")
			if revision > 0 && !bindingsRead && !bothSettingsGiven {
				// A change replaces both settings; with the current ones
				// unknown, one left unnamed would be reset, not kept.
				return fmt.Errorf("the binding's current settings could not be read, so a change could reset one you did not name: " +
					"pass both --allow-planned=<true|false> and --local-only=<true|false>")
			}
			if revision == 0 && !bindingsRead {
				_, _ = fmt.Fprintln(command.ErrOrStderr(), "Whether this namespace is already bound was not checked; binding anyway.")
			}
			if revision == 0 && existing != nil {
				return withExitCode(exitUsage, fmt.Errorf("namespace %s on that cluster is already bound for this application as consumer %s "+
					"(revision %d): pass --consumer %s to setup as it is, or --revision %d to change the binding",
					namespace, existing.ID, existing.Revision, existing.ID, existing.Revision))
			}
			// A change replaces both settings, so one the command line did not
			// name keeps the binding's current value instead of resetting it.
			if revision > 0 && existing != nil {
				if !command.Flags().Changed("allow-planned") {
					allowPlanned = existing.AllowPlanned
				}
				if !command.Flags().Changed("local-only") {
					localOnly = existing.LocalOnly
				}
			}
			consumer, bindError := apiClient.BindServiceConsumer(command.Context(), client.ServiceConsumerRequest{
				ApplicationID: applicationID, ClusterID: clusterID, Namespace: namespace,
				AllowPlanned: allowPlanned, LocalOnly: localOnly, ExpectedRevision: revision,
			})
			if bindError != nil {
				return bindError
			}
			if rendered, renderError := renderStructured(command, consumer); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			printServiceConsumer(out, newServiceNames(command), *consumer)
			_, _ = fmt.Fprintf(out, "Set a service up for it with 'ankra services setup <name> --package <package> --consumer %s'.\n", consumer.ID)
			return nil
		},
	}
	bindCommand.Flags().String("application", "", "The application that will consume the service (name or id)")
	bindCommand.Flags().String("cluster", "", "The cluster the application runs on (name or id; default: the selected cluster)")
	bindCommand.Flags().String("namespace", "", "The application's namespace on that cluster")
	bindCommand.Flags().Bool("allow-planned", false, "Bind even though the application is not installed in the namespace yet")
	bindCommand.Flags().Bool("local-only", false, "The consumer may only use a service on its own cluster")
	bindCommand.Flags().Int64("revision", 0, "The current revision of the binding being changed (omit to create one)")
	_ = bindCommand.MarkFlagRequired("application")
	registerStructuredOutputFlags(bindCommand)
	return bindCommand
}
