package cmd

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
)

// costObjectRateNotKnown is how a row that is part of the rate but has no
// amount prints: the platform cannot say how much of the rate is this row's,
// which is never zero.
const costObjectRateNotKnown = "not known"

// costObjectRateNotPriced is how a row shown beside the rate prints when it
// has no amount: Ankra does not price it (backups), so it is not in the rate.
const costObjectRateNotPriced = "not priced"

// costObjectGapSentences words each reason the platform gives for parts that
// are partial or unknown, from the contract's own descriptions.
var costObjectGapSentences = map[string]string{
	client.ObjectCostGapNamespaceNotPriced: "A namespace has no cost figure (no usable snapshot, or the latest pass attributed none), so the rate leaves it out.",
	client.ObjectCostGapNoPodsSeen:         "Compute was priced in a namespace where no running pod is seen, so it is on no row.",
	client.ObjectCostGapRequestsUnreadable: "Some pods ask for no CPU or memory, so part of the rate is on no row.",
	client.ObjectCostGapSplitUnknown:       "The metering did not split a namespace's compute from its storage, so that figure is on no row.",
	client.ObjectCostGapSplitRounded:       "The metering stores compute and storage to the whole cent an hour, so up to 2 cents an hour is on no row.",
	client.ObjectCostGapPodsCapped:         "A namespace holds more objects than one read takes, so its compute is on no row.",
	client.ObjectCostGapNodesNotAllPriced:  "Not every node of the cluster was priced, so the rows add up but are not exact.",
	client.ObjectCostGapUnreadable:         "A source could not be read.",
}

// costObjectUncountedReasons words why a namespace's spend is on no line.
var costObjectUncountedReasons = map[string]string{
	"installation_removed":   "installation removed, not in the total",
	"cluster_not_read":       "cluster no longer read",
	"environment_not_listed": "environment not listed, in the total",
}

// costObjectGapLines words a parts block's gaps, one sentence each in the
// platform's order. Gaps this CLI does not know are said once, together, and
// never dropped. No gap on a partial or unknown block says the platform gave
// no reason.
func costObjectGapLines(gaps []string) []string {
	var lines, unknown []string
	for _, gap := range gaps {
		if sentence, isKnown := costObjectGapSentences[gap]; isKnown {
			lines = append(lines, sentence)
			continue
		}
		unknown = append(unknown, gap)
	}
	if len(unknown) > 0 {
		lines = append(lines, "The platform also gave a reason this version of the CLI does not know: "+strings.Join(unknown, ", ")+".")
	}
	if len(lines) == 0 {
		lines = append(lines, "The platform gave no reason.")
	}
	return lines
}

// costObjectClusterNames maps each cluster id behind the projection to its
// name, so an environment that is a cluster can be named.
func costObjectClusterNames(projection *client.ObjectCostProjection) map[string]string {
	names := make(map[string]string, len(projection.Clusters)+len(projection.Namespaces))
	for _, namespace := range projection.Namespaces {
		if namespace.ClusterName != "" {
			names[namespace.ClusterID] = namespace.ClusterName
		}
	}
	for _, cluster := range projection.Clusters {
		if cluster.ClusterName != "" {
			names[cluster.ClusterID] = cluster.ClusterName
		}
	}
	return names
}

// costObjectEnvironmentTitle names one environment line: a named
// environment by its id (the projection carries no environment name), an
// unnamed one by the cluster it is.
func costObjectEnvironmentTitle(environment client.ObjectCostEnvironment, clusterNames map[string]string) string {
	if environment.Named {
		return "Environment " + environment.EnvironmentID + " (a named environment)"
	}
	if name, isKnown := clusterNames[environment.EnvironmentID]; isKnown {
		return "Environment on cluster " + name + " (" + environment.EnvironmentID + ")"
	}
	return "Environment on cluster " + environment.EnvironmentID
}

// renderObjectCostByEnvironment prints an application's cost by
// environment: each environment's monthly rate and the rows it is made of,
// then the spend no line counts. It prints nothing when the platform sent no
// block (an older platform) or null (every other kind), so their output is
// what it was.
func renderObjectCostByEnvironment(out io.Writer, projection *client.ObjectCostProjection) {
	block := projection.ByEnvironment.Value
	if block == nil {
		return
	}
	_, _ = fmt.Fprintln(out)
	switch block.State {
	case client.ObjectCostStateUnknown:
		reason := costObjectText(block.Reason)
		if reason == costObjectUnknown {
			reason = "the platform gave no reason"
		}
		_, _ = fmt.Fprintf(out, "Cost by environment: unknown, not none: %s\n", reason)
	case client.ObjectCostStateAvailable:
		if len(block.Environments) == 0 && !block.EnvironmentsTruncated {
			_, _ = fmt.Fprintln(out, "Cost by environment: no line. No environment has an installation behind this cost on the clusters you can read.")
		} else {
			_, _ = fmt.Fprintln(out, "Cost by environment:")
		}
	default:
		_, _ = fmt.Fprintf(out, "Cost by environment (state %q, which this version of the CLI does not know):\n", block.State)
	}
	clusterNames := costObjectClusterNames(projection)
	for _, environment := range block.Environments {
		renderObjectCostEnvironment(out, environment, clusterNames, projection.Currency)
	}
	if block.EnvironmentsTruncated {
		_, _ = fmt.Fprintln(out)
		// The number is the count the platform sent, not its cap: the answer
		// carries no cap, and a platform that changes it must not make this
		// line say a number that is no longer true.
		_, _ = fmt.Fprintf(out, "More environments: the platform found more than the %d it lists. The rest have no line here; their spend is listed below as not counted by any line.\n", len(block.Environments))
	}
	renderObjectCostUncounted(out, block, clusterNames)
}

// renderObjectCostEnvironment prints one environment: its monthly rate with
// partial and unknown said in words, its rows, and what no row explains.
func renderObjectCostEnvironment(out io.Writer, environment client.ObjectCostEnvironment, clusterNames map[string]string, currency string) {
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, costObjectEnvironmentTitle(environment, clusterNames)+":")
	if environment.Shared {
		_, _ = fmt.Fprintln(out, "  Shared:          a namespace of it also holds another application, so that namespace is counted whole here, as in the total.")
	}
	parts := environment.Parts
	if parts == nil {
		_, _ = fmt.Fprintln(out, "  Monthly rate:    not reported: this platform does not say what an environment's rate is made of.")
		return
	}
	isFloor := false
	for _, gap := range parts.Gaps {
		if gap == client.ObjectCostGapNamespaceNotPriced {
			isFloor = true
		}
	}
	rate := costObjectCents(parts.MonthlyCents, currency)
	if parts.MonthlyCents == nil {
		rate = "unknown, not zero"
	} else if isFloor {
		rate = "at least " + rate
	}
	why := ""
	switch parts.State {
	case client.ObjectCostStateAvailable:
	case client.ObjectCostStatePartial:
		rate += ", partial"
		why = "Why partial"
	case client.ObjectCostStateUnknown:
		why = "Why unknown"
	default:
		rate += fmt.Sprintf(" (state %q, which this version of the CLI does not know)", parts.State)
		if len(parts.Gaps) > 0 {
			why = "Why"
		}
	}
	_, _ = fmt.Fprintf(out, "  Monthly rate:    %s\n", rate)
	if why != "" {
		for index, sentence := range costObjectGapLines(parts.Gaps) {
			label := ""
			if index == 0 {
				label = why + ":"
			}
			_, _ = fmt.Fprintf(out, "  %-16s %s\n", label, sentence)
		}
	}
	renderObjectCostRateRows(out, parts, clusterNames, currency)
}

// renderObjectCostRateRows prints the rows of one environment's rate: a row
// counted in the rate with no amount is "not known" (its amount is on no
// row), a row beside the rate with no amount is "not priced", and a row
// beside the rate with an amount is marked as not in it. Then what no row
// explains, and what the cap on rows left out.
func renderObjectCostRateRows(out io.Writer, parts *client.ObjectCostRateParts, clusterNames map[string]string, currency string) {
	if len(parts.Rows) == 0 {
		if parts.State != client.ObjectCostStateUnknown {
			_, _ = fmt.Fprintln(out, "  Rows:            none listed, which does not mean it costs nothing.")
		}
	} else {
		clustersSeen := map[string]bool{}
		for _, row := range parts.Rows {
			if row.ClusterID != nil {
				clustersSeen[*row.ClusterID] = true
			}
		}
		writer := newCostTable(out)
		writer.AppendHeader(table.Row{"PART", "NAME", "NAMESPACE", "MONTHLY", "DETAIL"})
		anyNotKnown, anyNotPriced, anyBeside, anyBackups := false, false, false, false
		for _, row := range parts.Rows {
			var monthly string
			counted := row.IsCountedInRate()
			switch {
			case row.MonthlyCents != nil && counted:
				monthly = formatCostCents(*row.MonthlyCents, currency)
			case row.MonthlyCents != nil:
				monthly, anyBeside = formatCostCents(*row.MonthlyCents, currency)+", not in the rate", true
			case counted:
				monthly, anyNotKnown = costObjectRateNotKnown, true
			default:
				monthly, anyNotPriced = costObjectRateNotPriced, true
				anyBackups = anyBackups || row.Kind == client.ObjectCostRatePartKindBackups
			}
			detail := ""
			if row.Detail != nil {
				detail = *row.Detail
			}
			writer.AppendRow(table.Row{strings.ReplaceAll(row.Kind, "_", " "), costObjectText(&row.Name),
				costObjectRatePlace(row, len(clustersSeen) > 1, clusterNames), monthly, detail})
		}
		writer.Render()
		if anyNotKnown {
			_, _ = fmt.Fprintln(out, "* not known: the row is part of the rate, but the platform cannot say how much of it is this row's. Its amount is in the part on no row.")
		}
		if anyNotPriced {
			line := "* not priced: the row is shown beside the rate and is not part of it."
			if anyBackups {
				line += " Ankra does not price backups."
			}
			_, _ = fmt.Fprintln(out, line)
		}
		if anyBeside {
			_, _ = fmt.Fprintln(out, "* not in the rate: the row is shown beside the rate and is not added to it.")
		}
	}
	if parts.UnattributedMonthlyCents != nil && *parts.UnattributedMonthlyCents > 0 {
		_, _ = fmt.Fprintf(out, "  On no row:       %s of the rate. No row explains it; the reasons above say why.\n",
			formatCostCents(*parts.UnattributedMonthlyCents, currency))
	}
	if parts.RowsTruncated {
		leftOut := "an amount the platform did not give"
		if parts.TruncatedMonthlyCents != nil {
			leftOut = formatCostCents(*parts.TruncatedMonthlyCents, currency) + " of the rate"
		}
		_, _ = fmt.Fprintf(out, "  More rows:       the platform found more rows than it lists. Those left out add up to %s.\n", leftOut)
	}
}

// costObjectRatePlace is where a row runs: its namespace, with its cluster
// when the environment's rows run on more than one, or the whole
// environment for a row that is not one namespace's (backups, storage over
// several namespaces).
func costObjectRatePlace(row client.ObjectCostRatePart, severalClusters bool, clusterNames map[string]string) string {
	if row.Namespace == nil || strings.TrimSpace(*row.Namespace) == "" {
		return "whole environment"
	}
	if !severalClusters || row.ClusterID == nil {
		return *row.Namespace
	}
	cluster := *row.ClusterID
	if name, isKnown := clusterNames[cluster]; isKnown {
		cluster = name
	}
	return *row.Namespace + " on " + cluster
}

// renderObjectCostUncounted prints the spend the metering recorded for the
// application that no environment line counts, with why, and says when the
// list is cut short or cannot cover every day.
func renderObjectCostUncounted(out io.Writer, block *client.ObjectCostByEnvironment, clusterNames map[string]string) {
	if len(block.Uncounted) == 0 && !block.UncountedTruncated {
		return
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Spend no environment line counts:")
	if len(block.Uncounted) > 0 {
		writer := newCostTable(out)
		writer.AppendHeader(table.Row{"WHY", "CLUSTER", "NAMESPACE", "DAYS WITH SPEND", "ENVIRONMENT"})
		for _, spend := range block.Uncounted {
			why, isKnown := costObjectUncountedReasons[spend.Reason]
			if !isKnown {
				why = spend.Reason + " (a reason this version of the CLI does not know)"
			}
			cluster := spend.ClusterID
			if name, isNamed := clusterNames[cluster]; isNamed {
				cluster = name
			}
			environment := "not known"
			if spend.EnvironmentID != nil {
				environment = *spend.EnvironmentID
			}
			writer.AppendRow(table.Row{why, cluster, spend.Namespace, costObjectUncountedDays(spend.Dates), environment})
		}
		writer.Render()
	}
	if block.UncountedTruncated {
		_, _ = fmt.Fprintln(out, "  More entries: the platform found more than it lists, and the rest are not shown.")
	}
	if block.UncountedKnownFrom != nil {
		_, _ = fmt.Fprintf(out, "  Spend records start on %s, so spend before that day may be missing here.\n", *block.UncountedKnownFrom)
	}
}

// costObjectUncountedDays is how many days had spend, with the first and the
// last.
func costObjectUncountedDays(dates []string) string {
	switch len(dates) {
	case 0:
		return "none listed"
	case 1:
		return "1 day, " + dates[0]
	default:
		return fmt.Sprintf("%d days, %s to %s", len(dates), dates[0], dates[len(dates)-1])
	}
}
