package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// costObjectUnknown is how every figure the platform does not know prints:
// absent is not zero, so it is never "0" and never blank.
const costObjectUnknown = "unknown"

const costObjectLong = `Read what one object costs now and over the last 30 days, from the same
hourly snapshots the fleet summary reads, so the object's figure and the
fleet's agree.

A cluster and a credential cost their clusters' run rate, idle capacity
included. A namespace, a stack and an application cost their namespaces'
allocations; idle capacity belongs to the cluster, so their idle share is
unknown. Open cloud waste is found per cloud resource, so only a cluster and a
credential carry it.

Every figure the platform cannot give prints as unknown, never as zero. An
object with no priced cluster behind it says why. When coverage is incomplete
the monthly figure is a floor ("at least"), and the clusters that contributed
nothing are named. The trend draws one mark per UTC day, scaled to the
object's own peak day: '·' is a day no cluster was metered (unknown), '_' a
metered day that cost nothing.

Pass -o json (or yaml) for the projection exactly as the platform serves it.`

var costObjectCmd = &cobra.Command{
	Use:   "object",
	Short: "What one cluster, namespace, stack, application or credential costs, now and over 30 days",
	Long:  costObjectLong,
}

var costObjectClusterCmd = &cobra.Command{
	Use:   "cluster <cluster-name-or-id>",
	Short: "What one cluster costs: run rate, idle share, share of the fleet, open waste and 30-day trend",
	Long:  costObjectLong,
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost object cluster prod-eu
  ankra cost object cluster 1834920e-3001-4157-8938-33c447031033 -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := costObjectClusterArgument(args[0])
		if err != nil {
			return err
		}
		return runCostObject(cmd, client.ObjectCostKindCluster, clusterID, clusterID)
	},
}

var costObjectNamespaceCmd = &cobra.Command{
	Use:   "namespace <cluster-name-or-id> <namespace>",
	Short: "What one namespace of a cluster costs: its allocation, share of the fleet and 30-day trend",
	Long:  costObjectLong,
	Args:  cobra.ExactArgs(2),
	Example: `  ankra cost object namespace prod-eu shop
  ankra cost object namespace prod-eu shop -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace, err := costObjectNameArgument("namespace", args[1])
		if err != nil {
			return err
		}
		clusterID, err := costObjectClusterArgument(args[0])
		if err != nil {
			return err
		}
		return runCostObject(cmd, client.ObjectCostKindNamespace, clusterID, clusterID, namespace)
	},
}

var costObjectStackCmd = &cobra.Command{
	Use:   "stack <cluster-name-or-id> <stack-name>",
	Short: "What one stack of a cluster costs: its namespaces' allocations, share of the fleet and 30-day trend",
	Long:  costObjectLong,
	Args:  cobra.ExactArgs(2),
	Example: `  ankra cost object stack prod-eu observability
  ankra cost object stack prod-eu observability -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName, err := costObjectNameArgument("stack name", args[1])
		if err != nil {
			return err
		}
		clusterID, err := costObjectClusterArgument(args[0])
		if err != nil {
			return err
		}
		return runCostObject(cmd, client.ObjectCostKindStack, clusterID, clusterID, stackName)
	},
}

var costObjectApplicationCmd = &cobra.Command{
	Use:   "application <application-id>",
	Short: "What one application costs across every namespace it is installed in",
	Long:  costObjectLong,
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost object application 2b8c7c1e-7d6a-4f0e-9d3c-0c5a1b2c3d4e
  ankra cost object application 2b8c7c1e-7d6a-4f0e-9d3c-0c5a1b2c3d4e -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		applicationID, err := costObjectNameArgument("application id", args[0])
		if err != nil {
			return err
		}
		return runCostObject(cmd, client.ObjectCostKindApplication, applicationID, applicationID)
	},
}

var costObjectCredentialCmd = &cobra.Command{
	Use:   "credential <credential-id>",
	Short: "What one cloud credential costs across every cluster it provisioned, with its open waste",
	Long:  costObjectLong,
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost object credential 9f1d2e3c-4b5a-4968-8776-655443322110
  ankra cost object credential 9f1d2e3c-4b5a-4968-8776-655443322110 -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, err := costObjectNameArgument("credential id", args[0])
		if err != nil {
			return err
		}
		return runCostObject(cmd, client.ObjectCostKindCredential, credentialID, credentialID)
	},
}

// costObjectClusterArgument resolves a cluster name or id the way every
// other cost command does.
func costObjectClusterArgument(reference string) (string, error) {
	if _, err := costObjectNameArgument("cluster", reference); err != nil {
		return "", err
	}
	return resolveClusterID(reference)
}

// costObjectNameArgument refuses an empty argument: it would drop a path
// segment and reach another route, which answers a 404 that reads as a
// platform without this read.
func costObjectNameArgument(what string, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("the %s must not be empty", what))
	}
	return value, nil
}

// runCostObject reads the projection and renders it. idArgument is the id
// the path carries that a 422 refuses as malformed.
func runCostObject(cmd *cobra.Command, kind string, idArgument string, pathSegments ...string) error {
	projection, err := apiClient.GetObjectCost(kind, pathSegments...)
	if err != nil {
		return costObjectReadError(err, kind, idArgument)
	}
	if rendered, err := renderStructured(cmd, projection); rendered || err != nil {
		return err
	}
	renderObjectCost(cmd.OutOrStdout(), projection)
	return nil
}

// The route's own not-found details: a cluster outside the organisation,
// and a stack, application or credential that is not the organisation's.
const (
	costObjectClusterNotFound = "Cluster not found"
	costObjectObjectNotFound  = "Object not found"
)

// costObjectRoutes names each kind's route for the message a platform
// without it earns.
var costObjectRoutes = map[string]string{
	client.ObjectCostKindCluster:     "GET /api/v1/org/cloud-cost/objects/cluster/{cluster_id}",
	client.ObjectCostKindNamespace:   "GET /api/v1/org/cloud-cost/objects/namespace/{cluster_id}/{namespace}",
	client.ObjectCostKindStack:       "GET /api/v1/org/cloud-cost/objects/stack/{cluster_id}/{stack_name}",
	client.ObjectCostKindApplication: "GET /api/v1/org/cloud-cost/objects/application/{application_id}",
	client.ObjectCostKindCredential:  "GET /api/v1/org/cloud-cost/objects/credential/{credential_id}",
}

// costObjectReadError keeps the platform's own words, as cost namespaces
// does: a namespace it refuses is a usage error in its sentence, a malformed
// id is a usage error, the route's own not-found names what was not found,
// and a platform with no such route predates it.
func costObjectReadError(readError error, kind string, idArgument string) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) {
		switch {
		case unexpected.StatusCode == http.StatusBadRequest && unexpected.Detail != "":
			return withExitCode(exitUsage, errors.New(unexpected.Detail))
		case unexpected.StatusCode == http.StatusUnprocessableEntity:
			idName := kind + " id"
			if kind == client.ObjectCostKindNamespace || kind == client.ObjectCostKindStack {
				idName = "cluster id"
			}
			return withExitCode(exitUsage, fmt.Errorf("the platform refused %q as the %s: it must be a UUID", idArgument, idName))
		case unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == costObjectClusterNotFound:
			// The route's own not-found. A platform without the route also
			// answers 404, sometimes with a detail ("Not Found."), which is
			// why only these sentences are read as a missing object.
			return withExitCode(exitNotFound, errors.New("the cluster was not found in this organisation"))
		case unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == costObjectObjectNotFound:
			if kind == client.ObjectCostKindStack {
				return withExitCode(exitNotFound, errors.New("the stack was not found on that cluster in this organisation"))
			}
			return withExitCode(exitNotFound, fmt.Errorf("the %s was not found in this organisation", kind))
		}
	}
	return costTrendReadError(readError, costObjectRoutes[kind], "the object cost projection", "reading the object cost")
}

// costObjectText is a string the platform may leave null or empty; either
// prints as unknown, never blank.
func costObjectText(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return costObjectUnknown
	}
	return *value
}

func costObjectCents(cents *int64, currency string) string {
	if cents == nil {
		return costObjectUnknown
	}
	return formatCostCents(*cents, currency)
}

func costObjectPercent(value *float64) string {
	if value == nil {
		return costObjectUnknown
	}
	return strconv.FormatFloat(*value, 'f', -1, 64) + "%"
}

// costObjectIsNamespaceBased says whether the kind costs its namespaces'
// allocations rather than its clusters' run rate.
func costObjectIsNamespaceBased(kind string) bool {
	switch kind {
	case client.ObjectCostKindNamespace, client.ObjectCostKindStack, client.ObjectCostKindApplication:
		return true
	default:
		return false
	}
}

// costObjectTitle names the object: its kind and name, the cluster it lives
// on, and its id when the name is not the id.
func costObjectTitle(projection *client.ObjectCostProjection) string {
	subject := projection.Object
	name := costObjectText(&subject.Name)
	title := projection.Kind + " " + name
	switch projection.Kind {
	case client.ObjectCostKindNamespace, client.ObjectCostKindStack:
		title += " on cluster " + costObjectText(subject.ClusterName)
	}
	if subject.ID != "" && subject.ID != subject.Name {
		title += " (" + subject.ID + ")"
	}
	return title
}

func renderObjectCost(out io.Writer, projection *client.ObjectCostProjection) {
	currency := projection.Currency
	_, _ = fmt.Fprintf(out, "Cost of %s, in %s\n", costObjectTitle(projection), strings.ToUpper(currency))
	if !projection.Priced {
		reason := costObjectText(projection.UnpricedReason)
		if reason == costObjectUnknown {
			reason = "unknown (the platform gave no reason)"
		}
		_, _ = fmt.Fprintf(out, "Not priced: %s\n", reason)
	}
	isFloor := projection.CoverageIncomplete != nil && *projection.CoverageIncomplete

	monthly := costObjectCents(projection.MonthlyCents, currency)
	if projection.MonthlyCents != nil && isFloor {
		monthly = "at least " + monthly + " (a floor: coverage is incomplete)"
	}
	idle := costObjectPercent(projection.IdlePct)
	if projection.IdlePct == nil && costObjectIsNamespaceBased(projection.Kind) {
		idle += " (idle capacity belongs to the cluster, not to its namespaces)"
	}
	coverage := costObjectUnknown
	if projection.CoverageIncomplete != nil {
		coverage = "complete"
		if isFloor {
			coverage = "incomplete, so the monthly figure is a floor"
		}
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "  Monthly run rate:  %s\n", monthly)
	_, _ = fmt.Fprintf(out, "  Idle share:        %s\n", idle)
	_, _ = fmt.Fprintf(out, "  Share of fleet:    %s\n", costObjectPercent(projection.ShareOfFleetPct))
	_, _ = fmt.Fprintf(out, "  Confidence:        %s\n", costObjectText(projection.Confidence))
	_, _ = fmt.Fprintf(out, "  Coverage:          %s\n", coverage)
	_, _ = fmt.Fprintf(out, "  Open waste:        %s\n", costObjectWaste(projection.OpenWaste, currency))
	if isFloor {
		_, _ = fmt.Fprintln(out, costObjectFloorLine(projection))
	}

	_, _ = fmt.Fprintln(out)
	renderObjectCostTrend(out, projection.Trend30d, currency)
	renderObjectCostClusters(out, projection)
	renderObjectCostNamespaces(out, projection)
}

// costObjectWaste is the open waste line: a count and its priced monthly
// figure, or why waste is not attributed to this kind (unknown, not none).
func costObjectWaste(waste client.ObjectCostWaste, currency string) string {
	if !waste.Available {
		if reason := costObjectText(waste.Reason); reason != costObjectUnknown {
			return "unknown: " + reason
		}
		return "unknown (the platform gave no reason)"
	}
	if waste.Count == 0 {
		return "no open findings"
	}
	line := fmt.Sprintf("%s open, priced at %s/mo", pluralCount(waste.Count, "finding"), costObjectCents(waste.MonthlyCents, currency))
	if waste.UnpricedCount > 0 {
		line += fmt.Sprintf(", %d unpriced (not in that figure)", waste.UnpricedCount)
	}
	return line
}

// costObjectFloorLine names why the figure is a floor: the clusters that
// contributed nothing, or, when every cluster contributed, that one of them
// could not price everything it runs.
func costObjectFloorLine(projection *client.ObjectCostProjection) string {
	var missing []string
	for _, cluster := range projection.Clusters {
		if cluster.MonthlyCents != nil {
			continue
		}
		why := fmt.Sprintf("not priced: no cost snapshot in the last %d hours", projection.SnapshotStaleAfterHours)
		if cluster.Priced {
			why = "priced, but contributed no figure"
			if costObjectIsNamespaceBased(projection.Kind) {
				why = "priced, but its latest snapshot attributed no namespace"
			}
		}
		missing = append(missing, fmt.Sprintf("%s (%s)", costObjectText(&cluster.ClusterName), why))
	}
	if len(missing) == 0 {
		return "  Floor:             every cluster behind it contributed, but at least one could not price every node or billed resource"
	}
	return "  Contributed nothing: " + strings.Join(missing, "; ")
}

// renderObjectCostTrend draws the 30 days with the marks and the scaling of
// ankra cost namespaces: one mark per day, scaled to the series' own peak,
// '·' for a day nobody metered and '_' for a metered day that cost nothing.
func renderObjectCostTrend(out io.Writer, days []client.ObjectCostDay, currency string) {
	if len(days) == 0 {
		_, _ = fmt.Fprintln(out, "Trend: the platform returned no day, so the trend is unknown.")
		return
	}
	values := make([]*int64, len(days))
	metered, partial := 0, 0
	var peak *client.ObjectCostDay
	for index := range days {
		day := &days[index]
		values[index] = day.Cents
		if day.Cents == nil {
			continue
		}
		metered++
		if day.ClustersMetered < day.ClustersTotal {
			partial++
		}
		if peak == nil || *day.Cents > *peak.Cents {
			peak = day
		}
	}
	span := fmt.Sprintf("%s to %s, UTC", days[0].Date, days[len(days)-1].Date)
	if metered == 0 {
		_, _ = fmt.Fprintf(out, "Trend (%s): no day was metered, so the trend is unknown, not zero.\n", span)
		return
	}
	_, _ = fmt.Fprintf(out, "Trend (%s), one mark per day scaled to the peak day:\n", span)
	_, _ = fmt.Fprintf(out, "  %s\n", costNamespacesTrend(values))
	_, _ = fmt.Fprintf(out, "  %c no cluster metered (unknown, not zero)   %c metered, cost nothing\n",
		costNamespacesUnknownMark, costNamespacesZeroMark)
	latest := days[len(days)-1]
	_, _ = fmt.Fprintf(out, "  %d of %d days metered · peak %s on %s · latest %s on %s\n", metered, len(days),
		formatCostCents(*peak.Cents, currency), peak.Date, costObjectCents(latest.Cents, currency), latest.Date)
	switch {
	case partial == 1:
		_, _ = fmt.Fprintln(out, "  1 day metered only some of the clusters behind it, so that day is a floor.")
	case partial > 1:
		_, _ = fmt.Fprintf(out, "  %d days metered only some of the clusters behind it, so those days are floors.\n", partial)
	}
}

func renderObjectCostClusters(out io.Writer, projection *client.ObjectCostProjection) {
	_, _ = fmt.Fprintln(out)
	if len(projection.Clusters) == 0 {
		_, _ = fmt.Fprintln(out, "Clusters behind it: none.")
		return
	}
	_, _ = fmt.Fprintln(out, "Clusters behind it:")
	writer := newCostTable(out)
	writer.AppendHeader(table.Row{"CLUSTER", "PRICED", "MONTHLY", "CONFIDENCE", "CLUSTER ID"})
	for _, cluster := range projection.Clusters {
		priced := "no"
		if cluster.Priced {
			priced = "yes"
		}
		writer.AppendRow(table.Row{costObjectText(&cluster.ClusterName), priced,
			costObjectCents(cluster.MonthlyCents, projection.Currency), costObjectText(cluster.Confidence), cluster.ClusterID})
	}
	writer.Render()
}

func renderObjectCostNamespaces(out io.Writer, projection *client.ObjectCostProjection) {
	if len(projection.Namespaces) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Namespaces behind it:")
	writer := newCostTable(out)
	writer.AppendHeader(table.Row{"CLUSTER", "NAMESPACE", "MONTHLY", "SHARED"})
	anyShared := false
	for _, namespace := range projection.Namespaces {
		shared := "no"
		if namespace.Shared {
			shared, anyShared = "yes *", true
		}
		writer.AppendRow(table.Row{costObjectText(&namespace.ClusterName), namespace.Namespace,
			costObjectCents(namespace.MonthlyCents, projection.Currency), shared})
	}
	writer.Render()
	if anyShared {
		_, _ = fmt.Fprintln(out, "* Shared: another application also runs in that namespace, so its whole cost is counted for each of them, not split.")
	}
}

func init() {
	registerStructuredOutputFlags(costObjectClusterCmd, costObjectNamespaceCmd, costObjectStackCmd,
		costObjectApplicationCmd, costObjectCredentialCmd)
	costObjectCmd.AddCommand(costObjectClusterCmd, costObjectNamespaceCmd, costObjectStackCmd,
		costObjectApplicationCmd, costObjectCredentialCmd)
	costCmd.AddCommand(costObjectCmd)
}
