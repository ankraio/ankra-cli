package cmd

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

const (
	costObjectClusterID     = "1834920e-3001-4157-8938-33c447031033"
	costObjectStagingID     = "5d1c2b3a-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	costObjectApplicationID = "2b8c7c1e-7d6a-4f0e-9d3c-0c5a1b2c3d4e"
)

type costObjectMock struct {
	baseMock
	projection *client.ObjectCostProjection
	readError  error
	clusters   []client.ClusterListItem
	kinds      []string
	segments   [][]string
}

func (m *costObjectMock) GetObjectCost(kind string, pathSegments ...string) (*client.ObjectCostProjection, error) {
	m.kinds = append(m.kinds, kind)
	m.segments = append(m.segments, pathSegments)
	if m.readError != nil {
		return nil, m.readError
	}
	return m.projection, nil
}

func (m *costObjectMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{Result: m.clusters, Pagination: client.Pagination{TotalPages: 1}}, nil
}

func runCostObjectCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		resetTreeFlags(t, costObjectClusterCmd, costObjectNamespaceCmd, costObjectStackCmd,
			costObjectApplicationCmd, costObjectCredentialCmd)
	})
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

// decodeObjectCost decodes a projection the way the client does, so a
// fixture is the wire document itself.
func decodeObjectCost(t *testing.T, document string) *client.ObjectCostProjection {
	t.Helper()
	var projection client.ObjectCostProjection
	if err := json.Unmarshal([]byte(document), &projection); err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}
	return &projection
}

// objectCostTrendStart is the first of the fixtures' 30 days.
var objectCostTrendStart = time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)

// objectCostTrendJSON is 30 days, oldest first: cents[i] is the day's cents
// (nil for a day nobody metered), with one of clustersTotal metered on the
// days listed in partialDays.
func objectCostTrendJSON(cents []*int64, clustersTotal int, partialDays map[int]bool) string {
	days := make([]map[string]any, len(cents))
	for index, value := range cents {
		metered := 0
		if value != nil {
			metered = clustersTotal
			if partialDays[index] {
				metered = 1
			}
		}
		days[index] = map[string]any{"date": objectCostTrendStart.AddDate(0, 0, index).Format("2006-01-02"),
			"cents": value, "clusters_metered": metered, "clusters_total": clustersTotal}
	}
	encoded, _ := json.Marshal(days)
	return string(encoded)
}

// steadyTrend is 30 metered days of the given cents.
func steadyTrend(cents int64) []*int64 {
	values := make([]*int64, 30)
	for index := range values {
		values[index] = namespaceCents(cents)
	}
	return values
}

func pricedClusterProjection() string {
	return `{
  "kind": "cluster",
  "object": {"id": "` + costObjectClusterID + `", "name": "prod-eu", "cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu"},
  "currency": "usd",
  "priced": true,
  "unpriced_reason": null,
  "monthly_cents": 123456,
  "idle_pct": 12.5,
  "share_of_fleet_pct": 40.25,
  "confidence": "high",
  "coverage_incomplete": false,
  "open_waste": {"available": true, "reason": null, "count": 3, "monthly_cents": 12000, "unpriced_count": 1},
  "trend_30d": ` + objectCostTrendJSON(steadyTrend(4000), 1, nil) + `,
  "clusters": [{"cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu", "priced": true, "monthly_cents": 123456, "confidence": "high"}],
  "namespaces": [],
  "snapshot_stale_after_hours": 26
}`
}

// TestCostObjectClusterPriced pins a priced cluster: its figures, the
// resolved cluster reaching the cluster route, a whole figure (no floor),
// the waste line and a flat trend of top marks.
func TestCostObjectClusterPriced(t *testing.T) {
	mock := &costObjectMock{projection: decodeObjectCost(t, pricedClusterProjection()),
		clusters: []client.ClusterListItem{{ID: costObjectClusterID, Name: "prod-eu"}}}
	output, runError := runCostObjectCommand(t, mock, "cost", "object", "cluster", "prod-eu")
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	if len(mock.kinds) != 1 || mock.kinds[0] != "cluster" || !reflect.DeepEqual(mock.segments[0], []string{costObjectClusterID}) {
		t.Fatalf("asked for %v %v", mock.kinds, mock.segments)
	}
	for _, want := range []string{
		"Cost of cluster prod-eu (" + costObjectClusterID + "), in USD",
		"Monthly run rate:  $1234.56\n",
		"Idle share:        12.5%",
		"Share of fleet:    40.25%",
		"Confidence:        high",
		"Coverage:          complete",
		"Open waste:        3 findings open, priced at $120.00/mo, 1 unpriced (not in that figure)",
		"Trend (2026-08-27 to 2026-09-25, UTC)",
		strings.Repeat("█", 30),
		"30 of 30 days metered · peak $40.00 on 2026-08-27 · latest $40.00 on 2026-09-25",
		"CLUSTER", "prod-eu", "yes",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"at least", "Not priced", "Contributed nothing", "Namespaces behind it"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("a whole, priced cluster must not print %q:\n%s", unwanted, output)
		}
	}
	if strings.Count(output, "unknown") != 1 {
		t.Fatalf("only the trend legend may say unknown for a fully known cluster:\n%s", output)
	}
}

// TestCostObjectFloorNamesTheClusterThatContributedNothing pins a credential
// whose figure is a floor: "at least", and the unpriced cluster named with
// why, its own row unknown rather than $0.00.
func TestCostObjectFloorNamesTheClusterThatContributedNothing(t *testing.T) {
	document := `{
  "kind": "credential",
  "object": {"id": "9f1d2e3c-4b5a-4968-8776-655443322110", "name": "aws-prod", "cluster_id": null, "cluster_name": null},
  "currency": "eur",
  "priced": true, "unpriced_reason": null,
  "monthly_cents": 50000, "idle_pct": 20, "share_of_fleet_pct": 10, "confidence": "medium",
  "coverage_incomplete": true,
  "open_waste": {"available": true, "reason": null, "count": 0, "monthly_cents": 0, "unpriced_count": 0},
  "trend_30d": ` + objectCostTrendJSON(steadyTrend(1600), 2, map[int]bool{28: true, 29: true}) + `,
  "clusters": [
    {"cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu", "priced": true, "monthly_cents": 50000, "confidence": "medium"},
    {"cluster_id": "` + costObjectStagingID + `", "cluster_name": "staging-eu", "priced": false, "monthly_cents": null, "confidence": null}
  ],
  "namespaces": [],
  "snapshot_stale_after_hours": 26
}`
	output, runError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "credential", "9f1d2e3c-4b5a-4968-8776-655443322110")
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	for _, want := range []string{
		"Cost of credential aws-prod (9f1d2e3c-4b5a-4968-8776-655443322110), in EUR",
		"Monthly run rate:  at least €500.00 (a floor: coverage is incomplete)",
		"Coverage:          incomplete, so the monthly figure is a floor",
		"Contributed nothing: staging-eu (not priced: no cost snapshot in the last 26 hours)",
		"Open waste:        no open findings",
		"2 days metered only some of the clusters behind it, so those days are floors.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, costObjectStagingID) && (!strings.Contains(line, "unknown") || strings.Contains(line, "0.00")) {
			t.Fatalf("the unpriced cluster's row must read unknown, never a zero: %q", line)
		}
	}
}

// TestCostObjectUnpricedSaysWhyAndPrintsUnknown pins an unpriced object:
// the platform's reason, and every figure "unknown", never 0 or blank.
func TestCostObjectUnpricedSaysWhyAndPrintsUnknown(t *testing.T) {
	document := `{
  "kind": "application",
  "object": {"id": "` + costObjectApplicationID + `", "name": "checkout", "cluster_id": null, "cluster_name": null},
  "currency": "usd",
  "priced": false,
  "unpriced_reason": "The application is not installed in any namespace, so there is nothing to cost.",
  "monthly_cents": null, "idle_pct": null, "share_of_fleet_pct": null, "confidence": null, "coverage_incomplete": null,
  "open_waste": {"available": false, "reason": null, "count": 0, "monthly_cents": null, "unpriced_count": 0},
  "trend_30d": ` + objectCostTrendJSON(make([]*int64, 30), 0, nil) + `,
  "clusters": [], "namespaces": [],
  "snapshot_stale_after_hours": 26
}`
	output, runError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "application", costObjectApplicationID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	for _, want := range []string{
		"Not priced: The application is not installed in any namespace, so there is nothing to cost.",
		"Monthly run rate:  unknown\n",
		"Idle share:        unknown (idle capacity belongs to the cluster, not to its namespaces)",
		"Share of fleet:    unknown\n",
		"Confidence:        unknown\n",
		"Coverage:          unknown\n",
		"Open waste:        unknown (the platform gave no reason)",
		"no day was metered, so the trend is unknown, not zero.",
		"Clusters behind it: none.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"$0", "0%", "at least", ":  \n", ":   \n"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("an unpriced object printed %q, which reads as a known figure or a blank:\n%s", unwanted, output)
		}
	}
}

// TestCostObjectNamespaceBasedMarksSharedAndUnavailableWaste pins a stack
// on a named cluster: a shared namespace is marked and explained, a
// namespace with no figure reads unknown, and waste that is not attributed
// to stacks reads unknown with the platform's reason, never "none".
func TestCostObjectNamespaceBasedMarksSharedAndUnavailableWaste(t *testing.T) {
	wasteReason := "Cloud waste is found per cloud resource (a server, a volume, an address), not per namespace, so none is attributed to this stack; the cluster's own projection carries it."
	document := `{
  "kind": "stack",
  "object": {"id": "7a6b5c4d-3e2f-4a1b-9c8d-7e6f5a4b3c2d", "name": "observability", "cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu"},
  "currency": "usd",
  "priced": true, "unpriced_reason": null,
  "monthly_cents": 30000, "idle_pct": null, "share_of_fleet_pct": 3.5, "confidence": "estimated", "coverage_incomplete": false,
  "open_waste": {"available": false, "reason": "` + wasteReason + `", "count": 0, "monthly_cents": null, "unpriced_count": 0},
  "trend_30d": ` + objectCostTrendJSON(steadyTrend(1000), 1, nil) + `,
  "clusters": [{"cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu", "priced": true, "monthly_cents": 30000, "confidence": "estimated"}],
  "namespaces": [
    {"cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu", "namespace": "monitoring", "monthly_cents": 30000, "shared": true},
    {"cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu", "namespace": "logging", "monthly_cents": null, "shared": false}
  ],
  "snapshot_stale_after_hours": 26
}`
	mock := &costObjectMock{projection: decodeObjectCost(t, document)}
	output, runError := runCostObjectCommand(t, mock, "cost", "object", "stack", costObjectClusterID, "observability")
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	if mock.kinds[0] != "stack" || !reflect.DeepEqual(mock.segments[0], []string{costObjectClusterID, "observability"}) {
		t.Fatalf("asked for %v %v", mock.kinds, mock.segments)
	}
	for _, want := range []string{
		"Cost of stack observability on cluster prod-eu (7a6b5c4d-3e2f-4a1b-9c8d-7e6f5a4b3c2d), in USD",
		"Confidence:        estimated",
		"Open waste:        unknown: " + wasteReason,
		"Namespaces behind it:",
		"* Shared: another application also runs in that namespace",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.Contains(line, "monitoring") && (!strings.Contains(line, "yes *") || !strings.Contains(line, "$300.00")):
			t.Fatalf("the shared namespace must be marked: %q", line)
		case strings.Contains(line, "logging") && (!strings.Contains(line, "unknown") || strings.Contains(line, "$0.00")):
			t.Fatalf("a namespace with no figure must read unknown: %q", line)
		}
	}
	if strings.Contains(output, "no open findings") {
		t.Fatalf("waste not attributed to a stack is unknown, not none:\n%s", output)
	}
}

// TestCostObjectTrendTellsUnmeteredFromZero pins the trend's marks: a day
// nobody metered is '·', a metered day that cost nothing is '_', the rest
// scale to the peak day, and the latest day reads unknown when unmetered.
func TestCostObjectTrendTellsUnmeteredFromZero(t *testing.T) {
	cents := make([]*int64, 30)
	cents[10], cents[11], cents[12], cents[13] = namespaceCents(800), namespaceCents(0), namespaceCents(100), namespaceCents(400)
	document := `{
  "kind": "namespace",
  "object": {"id": "shop", "name": "shop", "cluster_id": "` + costObjectClusterID + `", "cluster_name": "prod-eu"},
  "currency": "gbp", "priced": true, "unpriced_reason": null,
  "monthly_cents": 100, "idle_pct": null, "share_of_fleet_pct": null, "confidence": "low", "coverage_incomplete": false,
  "open_waste": {"available": false, "reason": "not per namespace", "count": 0, "monthly_cents": null, "unpriced_count": 0},
  "trend_30d": ` + objectCostTrendJSON(cents, 1, nil) + `,
  "clusters": [], "namespaces": [],
  "snapshot_stale_after_hours": 26
}`
	mock := &costObjectMock{projection: decodeObjectCost(t, document)}
	output, runError := runCostObjectCommand(t, mock, "cost", "object", "namespace", costObjectClusterID, "shop")
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	if !reflect.DeepEqual(mock.segments[0], []string{costObjectClusterID, "shop"}) {
		t.Fatalf("asked for %v", mock.segments)
	}
	wantTrend := strings.Repeat("·", 10) + "█_▁▄" + strings.Repeat("·", 16)
	for _, want := range []string{
		"Cost of namespace shop on cluster prod-eu, in GBP",
		"  " + wantTrend + "\n",
		"4 of 30 days metered · peak £8.00 on 2026-09-06 · latest unknown on 2026-09-25",
		"Share of fleet:    unknown",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
}

// TestCostObjectKeepsThePlatformsWords pins the errors: the route's own
// not-founds are exit 3 naming what was missing, a refused namespace is a
// usage error in the platform's sentence, a malformed id is a usage error,
// and a platform without the route is named as predating it.
func TestCostObjectKeepsThePlatformsWords(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		err      error
		wantText string
		wantExit int
	}{
		{"object not found", []string{"cost", "object", "application", costObjectApplicationID},
			&client.UnexpectedResponseError{StatusCode: 404, Detail: "Object not found"},
			"the application was not found in this organisation", exitNotFound},
		{"stack not found", []string{"cost", "object", "stack", costObjectClusterID, "nope"},
			&client.UnexpectedResponseError{StatusCode: 404, Detail: "Object not found"},
			"the stack was not found on that cluster in this organisation", exitNotFound},
		{"cluster not found", []string{"cost", "object", "cluster", costObjectClusterID},
			&client.UnexpectedResponseError{StatusCode: 404, Detail: "Cluster not found"},
			"the cluster was not found in this organisation", exitNotFound},
		{"namespace refused", []string{"cost", "object", "namespace", costObjectClusterID, "Not_A_Name"},
			&client.UnexpectedResponseError{StatusCode: 400, Detail: "namespace must be a Kubernetes namespace name"},
			"namespace must be a Kubernetes namespace name", exitUsage},
		{"malformed id", []string{"cost", "object", "credential", "not-a-uuid"},
			client.NewUnexpectedResponseError(422, "request failed: status 422, body: {\"detail\":[...]}"),
			`the platform refused "not-a-uuid" as the credential id: it must be a UUID`, exitUsage},
		{"route missing", []string{"cost", "object", "application", costObjectApplicationID},
			&client.UnexpectedResponseError{StatusCode: 404, Detail: "Not Found."},
			"this platform does not serve the object cost projection: GET /api/v1/org/cloud-cost/objects/application/{application_id} is not registered", exitError},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, runError := runCostObjectCommand(t, &costObjectMock{readError: testCase.err}, testCase.args...)
			if runError == nil || !strings.Contains(runError.Error(), testCase.wantText) || exitCodeFor(runError) != testCase.wantExit {
				t.Fatalf("error = %v (exit %d), want %q (exit %d)", runError, exitCodeFor(runError), testCase.wantText, testCase.wantExit)
			}
		})
	}
	mock := &costObjectMock{}
	_, emptyError := runCostObjectCommand(t, mock, "cost", "object", "stack", costObjectClusterID, " ")
	if emptyError == nil || exitCodeFor(emptyError) != exitUsage || len(mock.kinds) != 0 {
		t.Fatalf("an empty stack name = %v (exit %d, %d reads), want a usage error before any read",
			emptyError, exitCodeFor(emptyError), len(mock.kinds))
	}
}

// TestCostObjectStructuredOutputIsTheProjection pins -o json: the document
// comes back field for field, nulls as nulls, and -o yaml keeps the wire
// names.
func TestCostObjectStructuredOutputIsTheProjection(t *testing.T) {
	document := pricedClusterProjection()
	document = strings.Replace(document, `"idle_pct": 12.5`, `"idle_pct": null`, 1)
	document = strings.Replace(document, `"confidence": "high",
  "coverage_incomplete"`, `"confidence": null,
  "coverage_incomplete"`, 1)
	output, runError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "cluster", costObjectClusterID, "-o", "json")
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	var want, got map[string]any
	if err := json.Unmarshal([]byte(document), &want); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("decoding %s: %v", output, err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("-o json is not the projection:\nwant %v\ngot  %v", want, got)
	}
	if got["idle_pct"] != nil || got["confidence"] != nil {
		t.Fatalf("a null must stay null: %s", output)
	}
	resetTreeFlags(t, costObjectClusterCmd)
	yamlOutput, yamlError := runCostObjectCommand(t, &costObjectMock{projection: decodeObjectCost(t, document)},
		"cost", "object", "cluster", costObjectClusterID, "-o", "yaml")
	if yamlError != nil || !strings.Contains(yamlOutput, "idle_pct: null") || !strings.Contains(yamlOutput, "trend_30d:") ||
		!strings.Contains(yamlOutput, "snapshot_stale_after_hours: 26") {
		t.Fatalf("-o yaml = %s (%v)", yamlOutput, yamlError)
	}
}

// TestCostObjectTrendScalesWithoutOverflow pins the float scaling at the
// top of the int64 range: a peak the integer form would have overflowed
// still draws the top mark, and a tiny day beside it the bottom one.
func TestCostObjectTrendScalesWithoutOverflow(t *testing.T) {
	huge := int64(math.MaxInt64 - 1)
	days := make([]client.ObjectCostDay, 4)
	for index, cents := range []*int64{namespaceCents(1), namespaceCents(huge), nil, namespaceCents(0)} {
		days[index] = client.ObjectCostDay{Date: objectCostTrendStart.AddDate(0, 0, index).Format("2006-01-02"),
			Cents: cents, ClustersMetered: 1, ClustersTotal: 1}
	}
	var out bytes.Buffer
	renderObjectCostTrend(&out, days, "usd")
	if !strings.Contains(out.String(), "  ▁█·_\n") {
		t.Fatalf("trend = %q, want the bottom mark, the top mark, unknown, zero", out.String())
	}
}
