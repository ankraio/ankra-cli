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

// The managed services commands ("Services" in the portal): PostgreSQL,
// Valkey, OpenSearch, VictoriaMetrics and the other engines Ankra offers as
// reviewed, health-checked services on a customer's cluster.
//
// They mirror the platform's two-step safety. Setting a service up and
// retiring one each prepare a review first (a stored plan with a digest,
// which changes nothing), show it, and only then confirm it by that digest,
// which is the one call with an effect. Nothing here confirms on its own: a
// confirmation needs a yes at the prompt or --yes, and a retirement also
// needs --acknowledge-data-loss.

func newServicesCommand() *cobra.Command {
	servicesCommand := &cobra.Command{
		Use:     "services",
		Aliases: []string{"service"},
		Short:   "Set up, inspect and retire managed services (PostgreSQL, Valkey, OpenSearch, ...)",
		Long: `Set up, inspect and retire managed services: PostgreSQL, Valkey, OpenSearch,
VictoriaMetrics, VictoriaLogs and the other engines in the service catalogue,
run on your own cluster as reviewed, health-checked platform services.

A service is set up in four steps:

  1. Pick a package from the catalogue:   ankra services packages list
  2. Declare where the cluster is:         ankra services policy set --cluster <c> --region <r> --data-boundary <b>
  3. Bind the application namespace that
     will use the service:                 ankra services consumers bind --application <a> --cluster <c> --namespace <ns>
  4. Review and confirm the setup:         ankra services setup <name> --package postgresql --cluster <c> --consumer <id>

Setup and retirement are two-step on the platform. A review is prepared
first: the platform resolves a plan, stores it with a digest for ten minutes,
and changes nothing. The plan is shown, and only confirming it by its digest
sets the service up or removes it. Every confirmation asks first; --yes skips
the question for scripts. Retiring a service deletes its data, so delete also
needs --acknowledge-data-loss.

'ankra services list' and 'ankra services get <name>' show each service's
deployment state, health, readiness (for PostgreSQL, a consumer-path canary
logs in with the generated credentials) and connection: its endpoints and the
Secret holding its credentials. Credential values are never shown; they stay
in the cluster.`,
		Example: `  ankra services packages list
  ankra services setup orders-db --package postgresql --cluster prod --consumer 5b1f0c7e-...
  ankra services list
  ankra services get orders-db
  ankra services delete orders-db --acknowledge-data-loss`,
	}
	servicesCommand.AddCommand(newServicesListCommand())
	servicesCommand.AddCommand(newServicesGetCommand())
	servicesCommand.AddCommand(newServicesSetupCommand())
	servicesCommand.AddCommand(newServicesDeleteCommand())
	servicesCommand.AddCommand(newServicesPackagesCommand())
	servicesCommand.AddCommand(newServicesPolicyCommand())
	servicesCommand.AddCommand(newServicesConsumersCommand())
	servicesCommand.AddCommand(newServicesReviewsCommand())
	servicesCommand.AddCommand(newServicesRetirementsCommand())
	return servicesCommand
}

// servicePageLimit is the largest page every managed-services listing serves.
const servicePageLimit = 50

// serviceMaxPages bounds a walk over a keyset listing. The platform's
// listings may answer short or even empty pages that still carry a cursor
// (rows the caller may not read use the scan budget), so a walk follows the
// cursor until it is null; the bound only stops a platform that never ends.
const serviceMaxPages = 200

// collectServicePages follows a keyset listing's next_cursor until it is
// null and returns every item. An empty intermediate page is never read as
// the end of the listing.
func collectServicePages[T any](fetch func(after string) ([]T, *string, error)) ([]T, error) {
	var collected []T
	after := ""
	seen := map[string]bool{}
	for page := 0; page < serviceMaxPages; page++ {
		items, next, fetchError := fetch(after)
		if fetchError != nil {
			return nil, fetchError
		}
		collected = append(collected, items...)
		if next == nil || *next == "" {
			return collected, nil
		}
		if seen[*next] {
			return nil, fmt.Errorf("the platform answered the page cursor %s twice; stopping rather than reading the listing forever", *next)
		}
		seen[*next] = true
		after = *next
	}
	return nil, fmt.Errorf("the listing did not end within %d pages", serviceMaxPages)
}

func listAllServiceInstances(ctx context.Context) ([]client.ServiceInstance, error) {
	return collectServicePages(func(after string) ([]client.ServiceInstance, *string, error) {
		page, listError := apiClient.ListServiceInstances(ctx, client.ServicePageOptions{Limit: servicePageLimit, After: after})
		if listError != nil {
			return nil, nil, listError
		}
		return page.Items, page.NextCursor, nil
	})
}

func listAllServicePackages(ctx context.Context) ([]client.ServicePackageSummary, error) {
	return collectServicePages(func(after string) ([]client.ServicePackageSummary, *string, error) {
		page, listError := apiClient.ListServicePackages(ctx, client.ServicePageOptions{Limit: servicePageLimit, After: after})
		if listError != nil {
			return nil, nil, listError
		}
		return page.Items, page.NextCursor, nil
	})
}

// serviceHumanWriter is where a command's human-readable text goes: stdout,
// or stderr when -o json|yaml asked for a document on stdout.
func serviceHumanWriter(command *cobra.Command) io.Writer {
	if format, formatError := structuredFormatFromFlags(command); formatError == nil && format != outputDefault {
		return command.ErrOrStderr()
	}
	return command.OutOrStdout()
}

// isServiceNotFound reports a 404 the platform answered for the resource.
func isServiceNotFound(lookupError error) bool {
	var unexpected *client.UnexpectedResponseError
	return errors.As(lookupError, &unexpected) && unexpected.StatusCode == http.StatusNotFound
}

// resolveServiceInstance finds the service a command names, by id or by
// name. A name is matched against the whole inventory; --cluster narrows it
// when the same name runs on several clusters. A retired service keeps its
// record (deployment state released), so an active service wins over
// released ones of the same name.
func resolveServiceInstance(command *cobra.Command, reference string) (*client.ServiceInstance, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return nil, withExitCode(exitUsage, errors.New("a service name or id is required"))
	}
	ctx := command.Context()
	clusterID := ""
	if clusterReference, _ := command.Flags().GetString("cluster"); strings.TrimSpace(clusterReference) != "" {
		resolvedID, resolveError := resolveClusterID(strings.TrimSpace(clusterReference))
		if resolveError != nil {
			return nil, resolveError
		}
		clusterID = resolvedID
	}
	// An id-shaped reference is read as an id first. Service names may look
	// like ids too (lower-case words joined by hyphens), so a not-found falls
	// through to the name search rather than ending here. --cluster holds for
	// an id as it does for a name: a service on another cluster is not the
	// one asked for.
	if looksLikeUUID(reference) {
		instance, getError := apiClient.GetServiceInstance(ctx, reference)
		if getError == nil {
			if clusterID != "" && instance.ClusterID != clusterID {
				return nil, withExitCode(exitNotFound, fmt.Errorf("service %s runs on cluster %s, not on the cluster --cluster names (%s)",
					reference, instance.ClusterID, clusterID))
			}
			return instance, nil
		}
		if !isServiceNotFound(getError) {
			return nil, getError
		}
	}
	instances, listError := listAllServiceInstances(ctx)
	if listError != nil {
		return nil, listError
	}
	var active, released []client.ServiceInstance
	for _, instance := range instances {
		if instance.Name != reference || (clusterID != "" && instance.ClusterID != clusterID) {
			continue
		}
		if instance.ReleasedAt != nil {
			released = append(released, instance)
		} else {
			active = append(active, instance)
		}
	}
	matches := active
	if len(matches) == 0 {
		matches = released
	}
	switch len(matches) {
	case 1:
		return &matches[0], nil
	case 0:
		// A reference shaped like an id was read as one first (and answered
		// not found); it is also a valid service name, which is why the
		// inventory was searched too, so the answer names both.
		if looksLikeUUID(reference) {
			return nil, withExitCode(exitNotFound, fmt.Errorf(
				"no service with the id or name %q - run 'ankra services list' to see the organisation's services", reference))
		}
		return nil, withExitCode(exitNotFound, fmt.Errorf(
			"no service named %q - run 'ankra services list' to see the organisation's services", reference))
	default:
		candidates := make([]string, 0, len(matches))
		for _, instance := range matches {
			candidates = append(candidates, fmt.Sprintf("%s on cluster %s", instance.ID, instance.ClusterID))
		}
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"%d services are named %q - pass --cluster or the service id instead (%s)",
			len(matches), reference, strings.Join(candidates, "; ")))
	}
}

// serviceNames turns the ids a service record carries into names for human
// output. Every lookup is best effort: a name that cannot be read is shown
// as its id, never as an error. A lookup that failed says so once on stderr,
// so an id shown for want of a name is not mistaken for a resource that is
// gone.
type serviceNames struct {
	ctx            context.Context
	notes          io.Writer
	clusters       map[string]string
	packages       map[string]string
	clustersLoaded bool
	packagesLoaded bool
}

func newServiceNames(command *cobra.Command) *serviceNames {
	return &serviceNames{ctx: command.Context(), notes: command.ErrOrStderr(),
		clusters: map[string]string{}, packages: map[string]string{}}
}

func (names *serviceNames) cluster(clusterID string) string {
	if !names.clustersLoaded {
		names.clustersLoaded = true
		const pageSize = 100
		for page := 1; page <= 20; page++ {
			response, listError := apiClient.ListClusters(page, pageSize)
			if listError != nil || response == nil {
				names.lookupFailed("cluster", listError)
				break
			}
			for _, cluster := range response.Result {
				names.clusters[cluster.ID] = cluster.Name
			}
			// A full page means there may be more even when the total is
			// not reported; a short one is the end.
			if len(response.Result) < pageSize || (response.Pagination.TotalPages > 0 && response.Pagination.TotalPages <= page) {
				break
			}
		}
	}
	if name := names.clusters[clusterID]; name != "" {
		return name
	}
	return clusterID
}

func (names *serviceNames) lookupFailed(kind string, lookupError error) {
	reason := "no answer"
	if lookupError != nil {
		reason = lookupError.Error()
	}
	_, _ = fmt.Fprintf(names.notes, "Note: %s names could not be read (%s); %ss are shown by id.\n", kind, reason, kind)
}

func (names *serviceNames) clusterWithID(clusterID string) string {
	if name := names.cluster(clusterID); name != clusterID {
		return fmt.Sprintf("%s (%s)", name, clusterID)
	}
	return clusterID
}

func (names *serviceNames) packageVersion(versionID string) string {
	if !names.packagesLoaded {
		names.packagesLoaded = true
		packages, listError := listAllServicePackages(names.ctx)
		if listError != nil {
			names.lookupFailed("package", listError)
		}
		for _, servicePackage := range packages {
			names.packages[servicePackage.ID] = servicePackage.Name + " " + servicePackage.Version
		}
	}
	if name := names.packages[versionID]; name != "" {
		return name
	}
	return versionID
}

// serviceHealthSummary is the one-line health of an instance.
func serviceHealthSummary(instance client.ServiceInstance) string {
	if instance.Health == nil {
		return "not observed"
	}
	return instance.Health.State
}

func valueOr(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}

func newServicesListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the organisation's managed services with their state, health and readiness",
		Long: `List the organisation's managed services: each with its cluster, package,
deployment state, health and readiness.

Deployment state is the recorded operation progress (accepted, deploying,
verification_pending, deployment_failed, ...), not current health. Health is
what the platform last observed from the cluster (serving, progressing,
degraded, unknown). Readiness says whether a consumer can use the service:
for PostgreSQL a canary logs in with the generated credentials (ready,
verifying, degraded, stale); engines without a canary read unverified.

Retired services keep their record; --include-released lists them too.`,
		Example: "  ankra services list\n  ankra services list --cluster prod\n  ankra services list -o json",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			clusterID := ""
			if clusterReference, _ := command.Flags().GetString("cluster"); strings.TrimSpace(clusterReference) != "" {
				resolvedID, resolveError := resolveClusterID(strings.TrimSpace(clusterReference))
				if resolveError != nil {
					return resolveError
				}
				clusterID = resolvedID
			}
			includeReleased, _ := command.Flags().GetBool("include-released")
			instances, listError := listAllServiceInstances(command.Context())
			if listError != nil {
				return listError
			}
			shown := make([]client.ServiceInstance, 0, len(instances))
			hiddenReleased := 0
			for _, instance := range instances {
				if clusterID != "" && instance.ClusterID != clusterID {
					continue
				}
				if instance.ReleasedAt != nil && !includeReleased {
					hiddenReleased++
					continue
				}
				shown = append(shown, instance)
			}
			sort.SliceStable(shown, func(left, right int) bool { return shown[left].Name < shown[right].Name })
			if rendered, renderError := renderStructured(command, shown); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(shown) == 0 {
				_, _ = fmt.Fprintln(out, "No managed services. Set one up with 'ankra services setup'; 'ankra services --help' walks through it.")
			} else {
				names := newServiceNames(command)
				instanceTable := table.NewWriter()
				instanceTable.SetOutputMirror(out)
				instanceTable.SetStyle(table.StyleRounded)
				instanceTable.AppendHeader(table.Row{"Name", "Cluster", "Package", "Deployment", "Health", "Readiness", "ID"})
				for _, instance := range shown {
					instanceTable.AppendRow(table.Row{instance.Name, names.cluster(instance.ClusterID),
						names.packageVersion(instance.PackageVersionID), instance.DeploymentState,
						serviceHealthSummary(instance), instance.Readiness, instance.ID})
				}
				instanceTable.Render()
			}
			if hiddenReleased > 0 {
				_, _ = fmt.Fprintf(command.ErrOrStderr(), "%d retired service(s) not shown; --include-released lists them.\n", hiddenReleased)
			}
			return nil
		},
	}
	listCommand.Flags().String("cluster", "", "Only services on this cluster (name or id)")
	listCommand.Flags().Bool("include-released", false, "Also list retired services")
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newServicesGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <service>",
		Short: "Show one managed service: health, readiness, connection and consumers",
		Long: `Show one managed service by name or id: its package, cluster and location, its
deployment state, health and readiness with the evidence behind each, how it
is reached (endpoints and the Secret holding its credentials), the consumers
it was set up for, and the latest retirement if one was confirmed.

The connection names the Secret and its keys only. Credential values are never
shown here; they stay in the service's namespace on the cluster.

A name that runs on more than one cluster needs --cluster.`,
		Example: "  ankra services get orders-db\n  ankra services get orders-db --cluster prod -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			instance, resolveError := resolveServiceInstance(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			if rendered, renderError := renderStructured(command, instance); rendered || renderError != nil {
				return renderError
			}
			printServiceInstance(command.OutOrStdout(), newServiceNames(command), *instance)
			return nil
		},
	}
	getCommand.Flags().String("cluster", "", "The cluster the service runs on, when its name is not unique (name or id)")
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func printServiceInstance(out io.Writer, names *serviceNames, instance client.ServiceInstance) {
	_, _ = fmt.Fprintf(out, "Service %q\n", instance.Name)
	_, _ = fmt.Fprintf(out, "  ID:          %s\n", instance.ID)
	_, _ = fmt.Fprintf(out, "  Package:     %s\n", names.packageVersion(instance.PackageVersionID))
	_, _ = fmt.Fprintf(out, "  Cluster:     %s\n", names.clusterWithID(instance.ClusterID))
	_, _ = fmt.Fprintf(out, "  Mode:        %s\n", instance.Mode)
	_, _ = fmt.Fprintf(out, "  Location:    region %s, data boundary %s\n", instance.Region, instance.DataBoundary)
	_, _ = fmt.Fprintf(out, "  Generation:  %d\n", instance.Generation)
	_, _ = fmt.Fprintf(out, "  Created:     %s\n", instance.CreatedAt)
	if instance.ReleasedAt != nil {
		_, _ = fmt.Fprintf(out, "  Released:    %s (retired; the name is free again)\n", *instance.ReleasedAt)
	}

	deployment := instance.DeploymentState
	if instance.EvidenceUpdatedAt != nil {
		deployment += " (recorded " + *instance.EvidenceUpdatedAt + ")"
	}
	_, _ = fmt.Fprintf(out, "  Deployment:  %s\n", deployment)

	if instance.Health == nil {
		_, _ = fmt.Fprintln(out, "  Health:      not observed yet")
	} else {
		_, _ = fmt.Fprintf(out, "  Health:      %s - %s\n", instance.Health.State, instance.Health.Reason)
		_, _ = fmt.Fprintf(out, "               observed %s, valid until %s\n", instance.Health.ObservedAt, instance.Health.ExpiresAt)
	}

	if instance.ReadinessCheck == nil {
		readiness := instance.Readiness
		if readiness == "unverified" {
			readiness += " (no consumer canary covers this service)"
		}
		_, _ = fmt.Fprintf(out, "  Readiness:   %s\n", readiness)
	} else {
		check := instance.ReadinessCheck
		_, _ = fmt.Fprintf(out, "  Readiness:   %s - %s\n", instance.Readiness, check.Reason)
		_, _ = fmt.Fprintf(out, "               canary observed %s, valid until %s, last passed %s\n",
			check.ObservedAt, check.ExpiresAt, valueOr(check.LastPassedAt, "never"))
	}

	if instance.Connection == nil {
		_, _ = fmt.Fprintln(out, "  Connection:  none published")
	} else {
		connection := instance.Connection
		_, _ = fmt.Fprintln(out, "  Connection:")
		_, _ = fmt.Fprintf(out, "    Namespace: %s\n", connection.Namespace)
		for _, endpoint := range connection.Endpoints {
			_, _ = fmt.Fprintf(out, "    Endpoint:  %s  %s\n", endpoint.Name, endpoint.Address)
		}
		_, _ = fmt.Fprintf(out, "    Secret:    %s (keys: %s)\n", connection.Secret.Name, strings.Join(connection.Secret.Keys, ", "))
		_, _ = fmt.Fprintf(out, "               The values stay in the cluster. To read them: ankra cluster get secrets %s -n %s --cluster %s --reveal\n",
			connection.Secret.Name, connection.Namespace, names.cluster(instance.ClusterID))
	}

	if len(instance.Consumers) == 0 {
		_, _ = fmt.Fprintln(out, "  Consumers:   none")
	} else {
		_, _ = fmt.Fprintln(out, "  Consumers:")
		for _, consumer := range instance.Consumers {
			_, _ = fmt.Fprintf(out, "    %s  application %s, cluster %s, namespace %s\n",
				consumer.ID, consumer.ApplicationID, names.cluster(consumer.ClusterID), consumer.Namespace)
		}
	}

	if instance.Retirement != nil {
		retirement := instance.Retirement
		state := retirement.State
		if retirement.Phase != nil {
			state += ", phase " + *retirement.Phase
		}
		if retirement.Outcome != nil {
			state += ", outcome " + *retirement.Outcome
		}
		_, _ = fmt.Fprintf(out, "  Retirement:  %s (%s, requested %s)\n", state, retirement.ID, retirement.RequestedAt)
		if retirement.Reason != nil && *retirement.Reason != "" {
			_, _ = fmt.Fprintf(out, "               %s\n", *retirement.Reason)
		}
	}
}

func init() {
	rootCmd.AddCommand(newServicesCommand())
}
