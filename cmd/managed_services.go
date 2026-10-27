package cmd

import (
	"context"
	"encoding/json"
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

A service is set up in three steps:

  1. Pick a package from the catalogue:   ankra services packages list
  2. Bind the application namespace that
     will use the service:                 ankra services consumers bind --application <a> --cluster <c> --namespace <ns>
  3. Review and confirm the setup:         ankra services setup <name> --package postgresql --cluster <c> --consumer <id>

Declaring where a cluster is ('ankra services policy set') is optional: do it
when services must keep data within a data boundary.

Setup and retirement are two-step on the platform. A review is prepared
first: the platform resolves a plan, stores it with a digest for ten minutes,
and changes nothing. The plan is shown, and only confirming it by its digest
sets the service up or removes it. Every confirmation asks first; --yes skips
the question for scripts. Retiring a service deletes its data, so delete also
needs --acknowledge-data-loss.

An application namespace on the service's own cluster receives the connection
once the service is running: a <service>-connection Secret in its namespace,
copied inside the cluster, which its workloads read as environment variables
(DATABASE_URL, REDIS_URL, NATS_URL, ...). While none of the application's
running pods reads the Secret, Ankra writes it without a redeploy; otherwise
Ankra leaves it to the application's next deploy, which rolls the pods that
read it together. Workloads deployed before the service was connected usually read
it after their next deploy; 'ankra services get' says when they do not. An
application on another cluster is not connected for you.

'ankra services list' and 'ankra services get <name>' show each service's
deployment state, health, readiness (a consumer-path canary connects with the
generated credentials, for every engine) and connection: its endpoints, the
Secret holding its credentials, and how each consumer receives them.
Credential values are never shown; they stay in the cluster.`,
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
	return listServiceInstances(ctx, client.ServiceInstanceListOptions{})
}

// listServiceInstances walks the inventory with the platform's own filters
// carried on every page: a filter that only reached the first page would
// let the rest of the walk answer the whole organisation.
func listServiceInstances(ctx context.Context, filter client.ServiceInstanceListOptions) ([]client.ServiceInstance, error) {
	return collectServicePages(func(after string) ([]client.ServiceInstance, *string, error) {
		options := filter
		options.ServicePageOptions = client.ServicePageOptions{Limit: servicePageLimit, After: after}
		page, listError := apiClient.ListServiceInstances(ctx, options)
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
	ctx                context.Context
	notes              io.Writer
	clusters           map[string]string
	packages           map[string]string
	applications       map[string]string
	clustersLoaded     bool
	packagesLoaded     bool
	applicationsLoaded bool
}

func newServiceNames(command *cobra.Command) *serviceNames {
	return &serviceNames{ctx: command.Context(), notes: command.ErrOrStderr(),
		clusters: map[string]string{}, packages: map[string]string{}, applications: map[string]string{}}
}

func (names *serviceNames) cluster(clusterID string) string {
	if !names.clustersLoaded {
		names.clustersLoaded = true
		const pageSize, maxPages = 100, 20
		readAll, failed := false, false
		for page := 1; page <= maxPages; page++ {
			response, listError := apiClient.ListClusters(page, pageSize)
			if listError != nil || response == nil {
				names.lookupFailed("cluster", listError)
				failed = true
				break
			}
			for _, cluster := range response.Result {
				names.clusters[cluster.ID] = cluster.Name
			}
			// A full page means there may be more even when the total is
			// not reported; a short one is the end.
			if len(response.Result) < pageSize || (response.Pagination.TotalPages > 0 && response.Pagination.TotalPages <= page) {
				readAll = true
				break
			}
		}
		// Stopping at the page cap is a lookup that did not finish: say so,
		// so an id shown for want of a name is not read as a cluster gone.
		if !readAll && !failed {
			_, _ = fmt.Fprintf(names.notes, "Note: only the first %d clusters were read for names; others are shown by id.\n", maxPages*pageSize)
		}
	}
	if name := names.clusters[clusterID]; name != "" {
		return name
	}
	return clusterID
}

// application is the name of an application, read once from the
// organisation's application listing. A record that already carries the name
// (an instance consumer) goes through consumerApplication instead.
func (names *serviceNames) application(applicationID string) string {
	if names.applications == nil {
		names.applications = map[string]string{}
	}
	if !names.applicationsLoaded && apiClient != nil {
		names.applicationsLoaded = true
		readAll, failed := false, false
		for page := 1; page <= maxApplicationLookupPages; page++ {
			payload, listError := apiClient.ListApplicationsRaw(names.ctx, page, maxApplicationLookupPageSize, "")
			if listError != nil {
				names.lookupFailed("application", listError)
				failed = true
				break
			}
			var listing applicationListingPage
			if unmarshalError := json.Unmarshal(payload, &listing); unmarshalError != nil {
				names.lookupFailed("application", unmarshalError)
				failed = true
				break
			}
			for _, application := range listing.Result {
				names.applications[application.ID] = application.Name
			}
			// A full page means there may be more even when the total is not
			// reported; a short one is the end.
			if len(listing.Result) < maxApplicationLookupPageSize ||
				(listing.Pagination.TotalPages > 0 && listing.Pagination.TotalPages <= page) {
				readAll = true
				break
			}
		}
		if !readAll && !failed {
			_, _ = fmt.Fprintf(names.notes, "Note: only the first %d applications were read for names; others are shown by id.\n",
				maxApplicationLookupPages*maxApplicationLookupPageSize)
		}
	}
	if name := names.applications[applicationID]; name != "" {
		return name
	}
	return applicationID
}

// consumerApplication names a consumer's application: the name the record
// carries, else the listing's, else the id.
func (names *serviceNames) consumerApplication(applicationID string, applicationName *string) string {
	if applicationName != nil && *applicationName != "" {
		return *applicationName
	}
	return names.application(applicationID)
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

// serviceClusterName is the name of the cluster a service runs on: the one
// the instance read carries, or the cluster listing's when it carries none.
func serviceClusterName(names *serviceNames, instance client.ServiceInstance) string {
	name := instance.ClusterName
	if name == "" {
		name = names.cluster(instance.ClusterID)
	}
	if name == "" {
		name = instance.ClusterID
	}
	if instance.ClusterDeleted {
		name += " (deleted)"
	}
	return name
}

// serviceServes reports whether a service's reviewed plan names a consumer
// of the application, the platform's own test for its application filter.
func serviceServes(instance client.ServiceInstance, applicationID string) bool {
	for _, consumer := range instance.Consumers {
		if consumer.ApplicationID == applicationID {
			return true
		}
	}
	return false
}

// serviceUsedBy names what of one application a service serves: each of
// the application's consumers in the reviewed plan, by the environment
// deployed in its namespace, or by the namespace while none is.
func serviceUsedBy(instance client.ServiceInstance, applicationID string) string {
	var usedBy []string
	for _, consumer := range instance.Consumers {
		if consumer.ApplicationID != applicationID {
			continue
		}
		if consumer.Environment != nil && consumer.Environment.Name != "" {
			usedBy = append(usedBy, consumer.Environment.Name)
			continue
		}
		usedBy = append(usedBy, "namespace "+consumer.Namespace)
	}
	if len(usedBy) == 0 {
		return "-"
	}
	return strings.Join(usedBy, ", ")
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
degraded, unknown). Readiness says whether a consumer can use the service: a
canary connects with the generated credentials (ready, verifying, degraded,
stale); a service no canary covers, or whose cluster's agent is too old to
run one, reads unverified.

--application keeps the services an application uses: those whose reviewed
plan names one of the application's namespaces as a consumer. The
application is given by name or id, and a Used by column says which of its
environments each service serves (or the namespace, where the application is
not deployed yet).

Retired services keep their record; --include-released lists them too.`,
		Example: "  ankra services list\n  ankra services list --cluster prod\n  ankra services list --application storefront\n  ankra services list -o json",
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
			// Both filters travel to the platform, which applies them on
			// every page. Both are also checked below, so a platform that
			// ignores a filter still lists only what was asked for.
			filter := client.ServiceInstanceListOptions{ClusterID: clusterID}
			applicationReference, _ := command.Flags().GetString("application")
			applicationReference = strings.TrimSpace(applicationReference)
			if command.Flags().Changed("application") {
				// The platform filters by id; a name is resolved the way
				// every per-application command resolves its argument.
				applicationID, resolveError := resolveApplicationID(command.Context(), apiClient, applicationReference)
				if resolveError != nil {
					return resolveError
				}
				filter.ApplicationID = applicationID
			}
			includeReleased, _ := command.Flags().GetBool("include-released")
			instances, listError := listServiceInstances(command.Context(), filter)
			if listError != nil {
				return listError
			}
			shown := make([]client.ServiceInstance, 0, len(instances))
			hiddenReleased := 0
			for _, instance := range instances {
				if clusterID != "" && instance.ClusterID != clusterID {
					continue
				}
				if filter.ApplicationID != "" && !serviceServes(instance, filter.ApplicationID) {
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
			switch {
			case len(shown) == 0 && filter.ApplicationID != "":
				_, _ = fmt.Fprintf(out, "No managed services serve application %s. Add one from the application's Overview in the portal, or with 'ankra services setup'.\n", applicationReference)
			case len(shown) == 0:
				_, _ = fmt.Fprintln(out, "No managed services. Set one up with 'ankra services setup'; 'ankra services --help' walks through it.")
			default:
				names := newServiceNames(command)
				instanceTable := table.NewWriter()
				instanceTable.SetOutputMirror(out)
				instanceTable.SetStyle(table.StyleRounded)
				header := table.Row{"Name", "Cluster", "Package", "Deployment", "Health", "Readiness"}
				if filter.ApplicationID != "" {
					header = append(header, "Used by")
				}
				instanceTable.AppendHeader(append(header, "ID"))
				for _, instance := range shown {
					row := table.Row{instance.Name, serviceClusterName(names, instance),
						names.packageVersion(instance.PackageVersionID), serviceDeploymentView(instance),
						serviceHealthSummary(instance), instance.Readiness}
					if filter.ApplicationID != "" {
						row = append(row, serviceUsedBy(instance, filter.ApplicationID))
					}
					instanceTable.AppendRow(append(row, instance.ID))
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
	listCommand.Flags().String("application", "", "Only services this application uses (name or id)")
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
it was set up for with how each receives the connection, and the latest
retirement if one was confirmed.

A consumer on the service's cluster receives the connection as a
<service>-connection Secret in its namespace, read as environment variables,
whose names are listed. Its delivery reads delivered, pending (not delivered
yet; Pickup says what it waits for), unsupported (for example a consumer on
another cluster, which needs the values set by hand) or unknown.

Pickup says how far the application is from running with the connection:
  waiting_for_service  the service has no credentials yet; Ankra delivers the
                       connection once it has
  delivering           no running pod reads it, so Ankra is writing it without
                       a redeploy or restart
  holder_gone          waits for you to choose which service provides the
                       standard names (DATABASE_URL, ...)
  redeploy_needed      Ankra leaves it to a deploy; a deploy usually writes
                       it and connects the workloads to it, but not always
                       (the delivery reason says what is needed)
  restart_needed       running pods started before it arrived; restart them
                       to pick it up (Ankra does not restart them)
  in_use               every running pod that reads it has it
A value not listed here comes from a newer platform and is shown as sent.
"Defined twice" lists the connection's variables that the application's
workloads also get from another source. Ankra holds back a connection that
is not delivered yet until that is resolved; the delivery reason says how.

The connection names the Secret and its keys only. Credential values are never
shown here; they stay in the cluster.

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
	if instance.ClusterDeleted {
		deletedCluster := instance.ClusterID
		if instance.ClusterName != "" {
			deletedCluster = fmt.Sprintf("%s (%s)", instance.ClusterName, instance.ClusterID)
		}
		_, _ = fmt.Fprintf(out, "  Cluster:     %s, deleted; retire the service with 'ankra services delete %s' to release its name\n",
			deletedCluster, instance.Name)
	} else {
		_, _ = fmt.Fprintf(out, "  Cluster:     %s\n", names.clusterWithID(instance.ClusterID))
	}
	_, _ = fmt.Fprintf(out, "  Mode:        %s\n", instance.Mode)
	_, _ = fmt.Fprintf(out, "  Location:    region %s, data boundary %s\n", instance.Region, instance.DataBoundary)
	_, _ = fmt.Fprintf(out, "  Generation:  %d\n", instance.Generation)
	_, _ = fmt.Fprintf(out, "  Created:     %s\n", instance.CreatedAt)
	if instance.ReleasedAt != nil {
		_, _ = fmt.Fprintf(out, "  Released:    %s (retired; the name is free again)\n", *instance.ReleasedAt)
	}

	deployment := serviceDeploymentView(instance)
	if deployment != instance.DeploymentState {
		deployment += " - " + serviceDeploymentViewReason(deployment)
	}
	if instance.EvidenceUpdatedAt != nil {
		deployment += " (recorded " + *instance.EvidenceUpdatedAt + ")"
	}
	_, _ = fmt.Fprintf(out, "  Deployment:  %s\n", deployment)
	if instance.DeploymentState == "deployment_failed" {
		printServiceDeploymentFailure(out, instance)
	}

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
			clusterName := consumer.ClusterName
			if clusterName == "" {
				clusterName = names.cluster(consumer.ClusterID)
			}
			_, _ = fmt.Fprintf(out, "    %s  application %s, cluster %s, namespace %s\n", consumer.ID,
				names.consumerApplication(consumer.ApplicationID, consumer.ApplicationName), clusterName, consumer.Namespace)
			printServiceConnectionDelivery(out, consumer.ConnectionDelivery)
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
		if retirement.Outcome != nil {
			printServiceKeptData(out, *retirement.Outcome, retirement.DataDisposal, retirement.KeptData, "  ")
		}
	}
}

// printServiceConnectionDelivery prints how one consumer receives the
// service's connection, under its consumer line. A consumer the platform
// reported no delivery for (a platform from before delivery) prints nothing:
// that is not an answer either way.
func printServiceConnectionDelivery(out io.Writer, delivery *client.ServiceConnectionDelivery) {
	if delivery == nil || delivery.State == "" {
		return
	}
	line := "      Delivery: " + delivery.State
	if delivery.SecretName != "" {
		line += ", Secret " + delivery.SecretName
	}
	if delivery.Reason != "" {
		line += " - " + delivery.Reason
	}
	_, _ = fmt.Fprintln(out, line)
	if delivery.Pickup != "" {
		pickup := "      Pickup:   " + delivery.Pickup
		if meaning, known := serviceConnectionPickupMeanings[delivery.Pickup]; known {
			pickup += " (" + meaning + ")"
		}
		_, _ = fmt.Fprintln(out, pickup)
	}
	if len(delivery.Variables) > 0 {
		_, _ = fmt.Fprintf(out, "      Variables: %s\n", strings.Join(delivery.Variables, ", "))
	}
	if len(delivery.NameCollisions) > 0 {
		_, _ = fmt.Fprintf(out, "      Defined twice: %s (the application's workloads also get these from another source)\n",
			strings.Join(delivery.NameCollisions, ", "))
	}
}

// serviceConnectionPickupMeanings says in a few words what each pickup the
// platform answers today means. A pickup not listed here, added by a later
// platform, prints as the platform sent it, without a meaning.
var serviceConnectionPickupMeanings = map[string]string{
	client.ServiceConnectionPickupWaitingForService: "the service has no credentials yet; Ankra delivers the connection once it has",
	client.ServiceConnectionPickupDelivering:        "Ankra is writing the connection into the namespace; nothing is redeployed or restarted",
	client.ServiceConnectionPickupHolderGone:        "waits for a person to choose which service provides the standard names",
	client.ServiceConnectionPickupRedeployNeeded:    "left to a deploy, which usually connects the workloads to it; the reason says what is needed",
	client.ServiceConnectionPickupRestartNeeded:     "running pods started before the connection arrived; restart them to pick it up",
	client.ServiceConnectionPickupInUse:             "every running pod that reads it has it",
}

func init() {
	rootCmd.AddCommand(newServicesCommand())
}

// serviceDeploymentView is the deployment state as a person reads it, the
// same rule as ankra-frontend's deploymentView: a released service whose
// retirement left its data, or could not confirm it gone, reads "released,
// data may remain"; a deployment the platform
// still records as verification_pending reads "deployed" once a readiness
// check has passed, and "running" when the platform observes the workload
// serving and no check found it failing or could not reach it. Every other
// state reads as the platform sent it, and -o json/yaml keep the platform's
// value untouched.
func serviceDeploymentView(instance client.ServiceInstance) string {
	if instance.DeploymentState == "released" && instance.Retirement != nil && instance.Retirement.Outcome != nil &&
		client.ServiceRetirementDataMayRemain(*instance.Retirement.Outcome, instance.Retirement.DataDisposal) {
		// Released, but never read as a plain retirement while the data may
		// remain (cluster ankra-otrxtu.2).
		return "released, data may remain"
	}
	if instance.DeploymentState != "verification_pending" {
		return instance.DeploymentState
	}
	if instance.Readiness == "ready" {
		return "deployed"
	}
	if instance.Health != nil && instance.Health.State == "serving" &&
		instance.Readiness != "degraded" && instance.Readiness != "stale" {
		return "running"
	}
	return instance.DeploymentState
}

// serviceDeploymentViewReason says why a deployment reads as view.
func serviceDeploymentViewReason(view string) string {
	switch view {
	case "deployed":
		return "the deployment finished and a readiness check connected to the service"
	case "running":
		return "the deployment finished and Ankra observes the service serving in its cluster"
	default:
		return ""
	}
}

// serviceDeploymentFailureExcerptLimit bounds how many characters of a failing
// step's error the summary prints; the full text is one command away.
const serviceDeploymentFailureExcerptLimit = 400

// serviceRetryableOperationStatuses are the execution states the platform
// retries (cluster usecase/executions retryableStatuses); any other state
// answers 409, so the CLI does not suggest a retry for it.
var serviceRetryableOperationStatuses = map[string]bool{"failed": true, "cancelled": true, "critical": true}

// printServiceDeploymentFailure prints why a deployment failed and what to do
// next: the error of each failed step of the deployment operation, or the
// operation's own error when no step carries one, then the commands that show
// every step, retry it when the platform can, and retire the service. A
// failure that cannot be read is said to be unreadable, never left out.
func printServiceDeploymentFailure(out io.Writer, instance client.ServiceInstance) {
	if instance.DeploymentOperationID == nil || *instance.DeploymentOperationID == "" {
		_, _ = fmt.Fprintln(out, "  Failure:     the platform recorded no deployment operation for this service")
		_, _ = fmt.Fprintf(out, "  Next:        retire the service with 'ankra services delete %s' and set it up again\n", instance.Name)
		return
	}
	operationID := *instance.DeploymentOperationID
	detail, readError := apiClient.GetExecution(operationID)
	if readError != nil {
		_, _ = fmt.Fprintf(out, "  Failure:     the failing step could not be read (%s)\n", readError)
		_, _ = fmt.Fprintf(out, "  Next:        see every step with 'ankra cluster operations steps %s'\n", operationID)
		_, _ = fmt.Fprintf(out, "               or retire the service with 'ankra services delete %s'\n", instance.Name)
		return
	}
	printed := false
	for _, step := range detail.Steps {
		if excerpt := serviceFailureExcerpt(step.ErrorExcerpt); excerpt != "" {
			_, _ = fmt.Fprintf(out, "  Failure:     step %s (%s): %s\n", step.Name, step.Status, excerpt)
			printed = true
		}
	}
	if !printed {
		if excerpt := serviceFailureExcerpt(detail.Execution.ErrorExcerpt); excerpt != "" {
			_, _ = fmt.Fprintf(out, "  Failure:     operation %s: %s\n", detail.Execution.Status, excerpt)
		} else {
			_, _ = fmt.Fprintf(out, "  Failure:     the operation ended %s and recorded no error message\n", detail.Execution.Status)
		}
	}
	_, _ = fmt.Fprintf(out, "  Next:        see every step with 'ankra cluster operations steps %s'\n", operationID)
	if serviceRetryableOperationStatuses[detail.Execution.Status] {
		_, _ = fmt.Fprintf(out, "               retry it with 'ankra cluster operations retry %s', or retire the service with 'ankra services delete %s'\n",
			operationID, instance.Name)
	} else {
		_, _ = fmt.Fprintf(out, "               a %s operation cannot be retried: retire the service with 'ankra services delete %s' and set it up again\n",
			detail.Execution.Status, instance.Name)
	}
}

// serviceFailureExcerpt is an error excerpt on one line, cut on a character
// boundary at serviceDeploymentFailureExcerptLimit; empty when there is none.
func serviceFailureExcerpt(errorExcerpt *string) string {
	if errorExcerpt == nil {
		return ""
	}
	excerpt := strings.Join(strings.Fields(*errorExcerpt), " ")
	if runes := []rune(excerpt); len(runes) > serviceDeploymentFailureExcerptLimit {
		excerpt = string(runes[:serviceDeploymentFailureExcerptLimit]) + "..."
	}
	return excerpt
}
