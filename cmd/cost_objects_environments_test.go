package cmd

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
)

const costObjectNamedEnvironmentID = "0b7c4d15-5f6a-4b8c-9d0e-1f2a3b4c5d6e"

// applicationCostDocument is an application's projection on one cluster with
// one namespace. byEnvironment is the by_environment value as JSON; empty
// leaves the key out, as a platform that predates it does.
func applicationCostDocument(byEnvironment string) string {
	block := ""
	if byEnvironment != "" {
		block = `,
  "by_environment": ` + byEnvironment
	}
	return `{
  "kind": "application",
  "object": {"id": "` + costObjectApplicationID + `", "name": "checkout", "cluster_id": null, "cluster_name": null},
  "currency": "eur",
  "priced": true, "unpriced_reason": null,
  "monthly_cents": 903, "idle_pct": null, "share_of_fleet_pct": 1.5, "confidence": "estimated", "coverage_incomplete": false,
  "open_waste": {"available": false, "reason": "not per namespace", "count": 0, "monthly_cents": null, "unpriced_count": 0},
  "trend_30d": ` + objectCostTrendJSON(steadyTrend(30), 1, nil) + `,
  "clusters": [{"cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu", "priced": true, "monthly_cents": 903, "confidence": "estimated"}],
  "namespaces": [{"cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu", "namespace": "shop", "monthly_cents": 903, "shared": false}],
  "snapshot_stale_after_hours": 26` + block + `
}`
}

// costObjectEnvironmentBlock is an available by_environment block holding
// the given environment lines, with nothing uncounted.
func costObjectEnvironmentBlock(environments ...string) string {
	return `{"state": "available", "reason": null, "environments": [` + strings.Join(environments, ",") + `],
  "environments_truncated": false, "uncounted": [], "uncounted_truncated": false, "uncounted_known_from": "2026-08-01"}`
}

// costObjectEnvironmentLine is the unnamed environment that is cluster
// prod-eu, with the given parts.
func costObjectEnvironmentLine(parts string) string {
	return `{"environment_id": "` + costObjectClusterID + `", "named": false, "shared": false,
  "trend_30d": ` + objectCostTrendJSON(steadyTrend(30), 1, nil) + `, "parts": ` + parts + `}`
}

// costObjectRow is one row of a rate; every field the contract names is
// sent, the ones a kind does not use as null.
func costObjectRow(kind string, name string, namespace string, cents string, counted bool, detail string) string {
	clusterID, namespaceValue := `"`+costObjectClusterID+`"`, `"`+namespace+`"`
	if namespace == "" {
		clusterID, namespaceValue = "null", "null"
	}
	detailValue := "null"
	if detail != "" {
		detailValue = `"` + detail + `"`
	}
	countedValue := "false"
	if counted {
		countedValue = "true"
	}
	return `{"kind": "` + kind + `", "name": "` + name + `", "detail": ` + detailValue + `, "cluster_id": ` + clusterID +
		`, "namespace": ` + namespaceValue + `, "monthly_cents": ` + cents + `, "shared": false, "counted_in_rate": ` + countedValue +
		`, "role": null, "database": null, "engine": null, "image_version": null, "instances": null, "copies": 2,
  "reserved_cpu_millicores": 500, "reserved_memory_bytes": 1073741824, "copies_alike": true,
  "claim_count": null, "claim_bytes": null, "claim_size_known": null}`
}

const costObjectBackupsDetail = "Backup storage is not priced by Ankra."

// costObjectShapeA is the live shape A: partial on split_rounded, nine rows,
// every amount null, eight counted in the rate and backups not, and the
// whole rate on no row.
func costObjectShapeA() string {
	rows := []string{
		costObjectRow("workload", "web", "shop", "null", true, "2 copies, each reserving 250m CPU and 512 MiB memory."),
		costObjectRow("workload", "worker", "shop", "null", true, ""),
		costObjectRow("workload", "api", "shop", "null", true, ""),
		costObjectRow("workload", "scheduler", "shop", "null", true, ""),
		costObjectRow("workload", "cache", "shop", "null", true, ""),
		costObjectRow("database_instance", "orders-db, primary", "shop", "null", true, ""),
		costObjectRow("database_instance", "orders-db, standby", "shop", "null", true, ""),
		costObjectRow("storage", "Storage", "shop", "null", true, "3 volume claims, 30 GiB."),
		costObjectRow("backups", "Backups", "", "null", false, costObjectBackupsDetail),
	}
	return costObjectEnvironmentLine(`{"state": "partial", "gaps": ["split_rounded"], "monthly_cents": 903,
  "rows": [` + strings.Join(rows, ",") + `], "rows_truncated": false, "truncated_monthly_cents": 0, "unattributed_monthly_cents": 903}`)
}

// costObjectShapeB is the live shape B: partial on split_unknown, one
// backups row, the whole rate on no row.
func costObjectShapeB() string {
	return costObjectEnvironmentLine(`{"state": "partial", "gaps": ["split_unknown"], "monthly_cents": 120,
  "rows": [` + costObjectRow("backups", "Backups", "", "null", false, costObjectBackupsDetail) + `],
  "rows_truncated": false, "truncated_monthly_cents": 0, "unattributed_monthly_cents": 120}`)
}

// costObjectShapeC is the live shape C: available, one backups row, a rate
// of zero with nothing on no row.
func costObjectShapeC() string {
	return costObjectEnvironmentLine(`{"state": "available", "gaps": [], "monthly_cents": 0,
  "rows": [` + costObjectRow("backups", "Backups", "", "null", false, costObjectBackupsDetail) + `],
  "rows_truncated": false, "truncated_monthly_cents": 0, "unattributed_monthly_cents": 0}`)
}

// costObjectShapeD is the live shape D: unknown on namespace_not_priced, no
// rows, the totals null.
func costObjectShapeD() string {
	return costObjectEnvironmentLine(`{"state": "unknown", "gaps": ["namespace_not_priced"], "monthly_cents": null,
  "rows": [], "rows_truncated": false, "truncated_monthly_cents": 0, "unattributed_monthly_cents": null}`)
}

func runApplicationCost(t *testing.T, document string) string {
	t.Helper()
	output, runError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "application", costObjectApplicationID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	return output
}

// costObjectEnvironmentSection is the output from the by-environment
// heading on.
func costObjectEnvironmentSection(t *testing.T, output string) string {
	t.Helper()
	index := strings.Index(output, "Cost by environment")
	if index < 0 {
		t.Fatalf("no cost by environment in:\n%s", output)
	}
	return output[index:]
}

func requireLines(t *testing.T, output string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
}

func refuseLines(t *testing.T, output string, unwanted ...string) {
	t.Helper()
	for _, text := range unwanted {
		if strings.Contains(output, text) {
			t.Fatalf("output must not say %q:\n%s", text, output)
		}
	}
}

// expectedCostTable draws rows the way the cost tables are drawn, straight
// from the table library, so the golden below does not lean on the command's
// own helpers.
func expectedCostTable(header table.Row, rows ...table.Row) string {
	var buffer bytes.Buffer
	writer := table.NewWriter()
	writer.SetOutputMirror(&buffer)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(header)
	for _, row := range rows {
		writer.AppendRow(row)
	}
	writer.Render()
	return buffer.String()
}

// TestCostObjectApplicationWithoutByEnvironmentPrintsAsBefore pins that the
// change is additive: an answer without by_environment (an older platform)
// prints exactly what the command printed before the block existed, line
// for line, and an answer whose by_environment is null (every other kind)
// prints the same.
func TestCostObjectApplicationWithoutByEnvironmentPrintsAsBefore(t *testing.T) {
	want := "Cost of application checkout (" + costObjectApplicationID + "), in EUR\n" +
		"\n" +
		"  Monthly run rate:  €9.03\n" +
		"  Idle share:        unknown (idle capacity belongs to the cluster, not to its namespaces)\n" +
		"  Share of fleet:    1.5%\n" +
		"  Confidence:        estimated\n" +
		"  Coverage:          complete\n" +
		"  Open waste:        unknown: not per namespace\n" +
		"\n" +
		"Daily spend (2026-08-27 to 2026-09-25, UTC), what each day cost, one mark per day scaled to the peak whole day:\n" +
		"  " + strings.Repeat("█", 30) + "\n" +
		"  · no cluster metered (unknown, not zero)   _ metered, cost nothing\n" +
		"  30 of 30 days metered · peak €0.30 on 2026-08-27 · latest whole day €0.30 on 2026-09-25\n" +
		"\n" +
		"Clusters behind it:\n" +
		expectedCostTable(table.Row{"CLUSTER", "PRICED", "MONTHLY", "CONFIDENCE", "CLUSTER ID"},
			table.Row{"prod-eu", "yes", "€9.03", "estimated", costObjectClusterID}) +
		"\n" +
		"Namespaces behind it:\n" +
		expectedCostTable(table.Row{"CLUSTER", "NAMESPACE", "MONTHLY", "SHARED"},
			table.Row{"prod-eu", "shop", "€9.03", "no"})
	absent := runApplicationCost(t, applicationCostDocument(""))
	if absent != want {
		t.Fatalf("an answer without by_environment changed its output:\nwant:\n%s\ngot:\n%s", want, absent)
	}
	if null := runApplicationCost(t, applicationCostDocument("null")); null != want {
		t.Fatalf("by_environment null must print as an absent block:\nwant:\n%s\ngot:\n%s", want, null)
	}
}

// TestCostObjectByEnvironmentShapeA pins the live shape A: the rate is
// partial and says why in words, every counted row with no amount reads
// "not known" and the backups row "not priced", never an amount, and the
// whole rate is on its own line as on no row.
func TestCostObjectByEnvironmentShapeA(t *testing.T) {
	section := costObjectEnvironmentSection(t, runApplicationCost(t,
		applicationCostDocument(costObjectEnvironmentBlock(costObjectShapeA()))))
	requireLines(t, section,
		"Cost by environment:\n",
		"Environment on cluster prod-eu ("+costObjectClusterID+"):\n",
		"  Monthly rate:    €9.03, partial\n",
		"  Why partial:     The metering stores compute and storage to the whole cent an hour, so up to 2 cents an hour is on no row.\n",
		"PART", "NAME", "NAMESPACE", "MONTHLY", "DETAIL",
		"* not known: the row is part of the rate, but the platform cannot say how much of it is this row's.",
		"* not priced: the row is shown beside the rate and is not part of it. Ankra does not price backups.\n",
		"  On no row:       €9.03 of the rate. No row explains it; the reasons above say why.\n",
	)
	refuseLines(t, section, "€0.00", "at least", "More rows", "Spend no environment line counts")
	notKnown := 0
	for _, line := range strings.Split(section, "\n") {
		switch {
		case strings.Contains(line, "│ backups"):
			if !strings.Contains(line, "not priced") || !strings.Contains(line, "whole environment") || !strings.Contains(line, costObjectBackupsDetail) {
				t.Fatalf("the backups row must read not priced, for the whole environment: %q", line)
			}
		case strings.Contains(line, "│ workload"), strings.Contains(line, "│ database instance"), strings.Contains(line, "│ storage"):
			if !strings.Contains(line, "not known") || !strings.Contains(line, "shop") {
				t.Fatalf("a counted row with no amount must read not known: %q", line)
			}
			notKnown++
		}
	}
	if notKnown != 8 {
		t.Fatalf("want 8 counted rows reading not known, got %d:\n%s", notKnown, section)
	}
}

// TestCostObjectByEnvironmentShapeB pins the live shape B: one backups row
// not priced, and the whole rate on no row with the reason in words.
func TestCostObjectByEnvironmentShapeB(t *testing.T) {
	section := costObjectEnvironmentSection(t, runApplicationCost(t,
		applicationCostDocument(costObjectEnvironmentBlock(costObjectShapeB()))))
	requireLines(t, section,
		"  Monthly rate:    €1.20, partial\n",
		"  Why partial:     The metering did not split a namespace's compute from its storage, so that figure is on no row.\n",
		"* not priced: the row is shown beside the rate and is not part of it. Ankra does not price backups.\n",
		"  On no row:       €1.20 of the rate.",
	)
	refuseLines(t, section, "* not known", "€0.00")
}

// TestCostObjectByEnvironmentShapeC pins the live shape C: an available rate
// of zero is a real zero and says nothing is partial, and the backups row
// beside it is not priced.
func TestCostObjectByEnvironmentShapeC(t *testing.T) {
	section := costObjectEnvironmentSection(t, runApplicationCost(t,
		applicationCostDocument(costObjectEnvironmentBlock(costObjectShapeC()))))
	requireLines(t, section, "  Monthly rate:    €0.00\n", "not priced")
	refuseLines(t, section, "partial", "Why", "On no row", "* not known")
}

// TestCostObjectByEnvironmentShapeD pins the live shape D: an unknown rate
// is unknown, never zero, says why, and lists no rows.
func TestCostObjectByEnvironmentShapeD(t *testing.T) {
	section := costObjectEnvironmentSection(t, runApplicationCost(t,
		applicationCostDocument(costObjectEnvironmentBlock(costObjectShapeD()))))
	requireLines(t, section,
		"  Monthly rate:    unknown, not zero\n",
		"  Why unknown:     A namespace has no cost figure (no usable snapshot, or the latest pass attributed none), so the rate leaves it out.\n",
	)
	refuseLines(t, section, "€", "PART", "Rows:", "at least", "On no row")
}

// TestCostObjectByEnvironmentEmptyAndUnknownBlocks pins the block's own
// states: available with no environment line says there is no line and why,
// never a zero; unknown says unknown, not none, with the platform's reason.
func TestCostObjectByEnvironmentEmptyAndUnknownBlocks(t *testing.T) {
	empty := runApplicationCost(t, applicationCostDocument(costObjectEnvironmentBlock()))
	requireLines(t, empty, "Cost by environment: no line. No environment has an installation behind this cost on the clusters you can read.\n")
	unknown := runApplicationCost(t, applicationCostDocument(`{"state": "unknown", "reason": "The installations could not be read.",
  "environments": [], "environments_truncated": false, "uncounted": [], "uncounted_truncated": false, "uncounted_known_from": null}`))
	requireLines(t, unknown, "Cost by environment: unknown, not none: The installations could not be read.\n")
	refuseLines(t, unknown, "no line")
}

// TestCostObjectByEnvironmentSaysEveryGapAndKeepsAnUnknownOne pins the eight
// gaps' sentences, each said once in the platform's order, and a gap this
// CLI does not know said once, generally, never dropped.
func TestCostObjectByEnvironmentSaysEveryGapAndKeepsAnUnknownOne(t *testing.T) {
	gaps := []string{"namespace_not_priced", "no_pods_seen", "requests_unreadable", "split_unknown", "split_rounded",
		"pods_capped", "nodes_not_all_priced", "unreadable", "a_new_gap", "another_new_gap"}
	encoded, _ := json.Marshal(gaps)
	section := costObjectEnvironmentSection(t, runApplicationCost(t, applicationCostDocument(costObjectEnvironmentBlock(
		costObjectEnvironmentLine(`{"state": "partial", "gaps": `+string(encoded)+`, "monthly_cents": 5000,
  "rows": [`+costObjectRow("workload", "web", "shop", "4000", true, "")+`], "rows_truncated": false,
  "truncated_monthly_cents": 0, "unattributed_monthly_cents": 1000}`)))))
	requireLines(t, section, "  Monthly rate:    at least €50.00, partial\n",
		"The platform also gave a reason this version of the CLI does not know: a_new_gap, another_new_gap.",
		"  On no row:       €10.00 of the rate.")
	for _, gap := range gaps[:8] {
		if strings.Count(section, costObjectGapSentences[gap]) != 1 {
			t.Fatalf("gap %s must be said once:\n%s", gap, section)
		}
	}
	if strings.Count(section, "does not know") != 1 {
		t.Fatalf("the unknown gaps must be said once, together:\n%s", section)
	}
	for _, line := range strings.Split(section, "\n") {
		if strings.Contains(line, "│ workload") && !strings.Contains(line, "€40.00") {
			t.Fatalf("a row with an amount prints it: %q", line)
		}
	}
}

// TestCostObjectByEnvironmentSaysWhatWasCutShort pins that a capped read is
// never shown as none: rows the cap left out, environments past the cap and
// uncounted entries past theirs each get a line, uncounted spend is listed
// with why, a named environment and a shared one are said as such, and an
// environment from a platform that does not send parts says so.
func TestCostObjectByEnvironmentSaysWhatWasCutShort(t *testing.T) {
	named := `{"environment_id": "` + costObjectNamedEnvironmentID + `", "named": true, "shared": true,
  "trend_30d": [], "parts": {"state": "available", "gaps": [], "monthly_cents": 9000,
  "rows": [` + costObjectRow("workload", "web", "shop", "4000", true, "") + `], "rows_truncated": true,
  "truncated_monthly_cents": 5000, "unattributed_monthly_cents": 0}}`
	withoutParts := `{"environment_id": "` + costObjectStagingID + `", "named": false, "shared": false, "trend_30d": []}`
	block := `{"state": "available", "reason": null, "environments": [` + named + `,` + withoutParts + `],
  "environments_truncated": true,
  "uncounted": [
    {"reason": "installation_removed", "environment_id": null, "named": false, "cluster_id": "` + costObjectClusterID + `",
     "namespace": "old-shop", "dates": ["2026-09-01", "2026-09-02", "2026-09-04"]},
    {"reason": "environment_not_listed", "environment_id": "` + costObjectStagingID + `", "named": false,
     "cluster_id": "` + costObjectStagingID + `", "namespace": "shop", "dates": ["2026-09-03"]}
  ],
  "uncounted_truncated": true, "uncounted_known_from": "2026-08-30"}`
	section := costObjectEnvironmentSection(t, runApplicationCost(t, applicationCostDocument(block)))
	requireLines(t, section,
		"Environment "+costObjectNamedEnvironmentID+" (a named environment):\n",
		"  Shared:          a namespace of it also holds another application, so that namespace is counted whole here, as in the total.\n",
		"  Monthly rate:    €90.00\n",
		"  More rows:       the platform found more rows than it lists. Those left out add up to €50.00 of the rate.\n",
		"Environment on cluster "+costObjectStagingID+":\n",
		"  Monthly rate:    not reported: this platform does not say what an environment's rate is made of.\n",
		"More environments: the platform lists 50. The rest have no line here; their spend is listed below as not counted by any line.\n",
		"Spend no environment line counts:\n",
		"installation removed, not in the total", "3 days, 2026-09-01 to 2026-09-04",
		"environment not listed, in the total", "1 day, 2026-09-03",
		"  More entries: the platform found more than it lists, and the rest are not shown.\n",
		"  Spend records start on 2026-08-30, so spend before that day may be missing here.\n",
	)
	for _, line := range strings.Split(section, "\n") {
		if strings.Contains(line, "old-shop") && !strings.Contains(line, "not known") {
			t.Fatalf("a removed installation's environment is not known: %q", line)
		}
	}
}

// TestCostObjectByEnvironmentStructuredOutputIsTheBlock pins -o json and
// -o yaml: the block comes back as the platform sent it, nulls as nulls,
// both from the response document the client keeps and from the struct
// when there is none.
func TestCostObjectByEnvironmentStructuredOutputIsTheBlock(t *testing.T) {
	document := applicationCostDocument(costObjectEnvironmentBlock(costObjectShapeA(), costObjectShapeD()))
	var want map[string]any
	if err := json.Unmarshal([]byte(document), &want); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	for _, keepDocument := range []bool{false, true} {
		projection := decodeObjectCost(t, document)
		if keepDocument {
			projection.RawDocument = client.NewRawDocument([]byte(document))
		}
		output, runError := runCostObjectCommand(t, &costObjectMock{projection: projection},
			"cost", "object", "application", costObjectApplicationID, "-o", "json")
		if runError != nil {
			t.Fatalf("run: %v", runError)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(output), &got); err != nil {
			t.Fatalf("decoding %s: %v", output, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("-o json (document kept: %v) is not the projection:\nwant %v\ngot  %v", keepDocument, want, got)
		}
		resetTreeFlags(t, costObjectApplicationCmd)
	}
	yamlOutput, yamlError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "application", costObjectApplicationID, "-o", "yaml")
	if yamlError != nil {
		t.Fatalf("yaml: %v", yamlError)
	}
	requireLines(t, yamlOutput, "by_environment:", "unattributed_monthly_cents: null", "counted_in_rate: false",
		"gaps:", "- split_rounded", "uncounted_known_from:", "monthly_cents: null")
}
