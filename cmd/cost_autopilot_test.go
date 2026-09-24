package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

type costAutopilotMock struct {
	baseMock
	policy       *client.CostAutopilotPolicy
	readError    error
	updates      []client.CostAutopilotPolicyUpdate
	updateError  error
	cluster      *client.CostAutopilotCluster
	overrideIDs  []string
	overrides    []client.CostAutopilotOverrideRequest
	clearedIDs   []string
	clusterError error
}

func (m *costAutopilotMock) GetCostAutopilot() (*client.CostAutopilotPolicy, error) {
	if m.readError != nil {
		return nil, m.readError
	}
	return m.policy, nil
}

func (m *costAutopilotMock) UpdateCostAutopilot(update client.CostAutopilotPolicyUpdate) (*client.CostAutopilotPolicy, error) {
	m.updates = append(m.updates, update)
	if m.updateError != nil {
		return nil, m.updateError
	}
	return m.policy, nil
}

func (m *costAutopilotMock) SetCostAutopilotOverride(clusterID string, request client.CostAutopilotOverrideRequest) (*client.CostAutopilotCluster, error) {
	m.overrideIDs = append(m.overrideIDs, clusterID)
	m.overrides = append(m.overrides, request)
	if m.clusterError != nil {
		return nil, m.clusterError
	}
	return m.cluster, nil
}

func (m *costAutopilotMock) ClearCostAutopilotOverride(clusterID string) (*client.CostAutopilotCluster, error) {
	m.clearedIDs = append(m.clearedIDs, clusterID)
	if m.clusterError != nil {
		return nil, m.clusterError
	}
	return m.cluster, nil
}

// runCostAutopilotCommand runs the command with stdin, returning stdout and
// stderr apart so a test can tell the parseable output from the prompt.
func runCostAutopilotCommand(t *testing.T, mock APIClient, input string, args ...string) (string, string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	commands := []*cobra.Command{costAutopilotGetCmd, costAutopilotSetCmd, costAutopilotOverrideCmd, costAutopilotClearCmd}
	// Reset before the run as well as after the test: a test that runs the
	// command more than once must not carry one run's flags into the next.
	resetTreeFlags(t, commands...)
	t.Cleanup(func() { resetTreeFlags(t, commands...) })
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

const (
	autopilotStagingID = "33333333-3333-4333-8333-333333333333"
	autopilotProdID    = "11111111-1111-4111-8111-111111111111"
	autopilotUserID    = "77777777-7777-4777-8777-777777777777"
)

func autopilotPointer[T any](value T) *T {
	return &value
}

// costAutopilotFixture is the policy as the platform sends it: set, with
// quiet hours and a pre-notice route, every tier described, a cluster on its
// environment's tier and one under an expiring override.
func costAutopilotFixture() *client.CostAutopilotPolicy {
	return &client.CostAutopilotPolicy{
		IsSet: true,
		Defaults: map[string]string{"production": "hands-off", "staging": "scheduled", "development": "managed",
			"preview": "ephemeral", "unknown": "hands-off"},
		RecommendedDefaults: map[string]string{"production": "hands-off", "staging": "scheduled", "development": "managed",
			"preview": "ephemeral", "unknown": "hands-off"},
		QuietHours:          &client.CostAutopilotQuietHours{Start: "22:00", End: "07:00", Timezone: "Europe/Stockholm"},
		NotificationRouteID: autopilotPointer("5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3"),
		UpdatedBy:           autopilotPointer(autopilotUserID),
		UpdatedAt:           autopilotPointer("2026-09-20T10:00:00Z"),
		EnvironmentKinds:    []string{"production", "staging", "development", "preview", "unknown"},
		Tiers: []client.CostAutopilotTier{
			{Tier: "hands-off", Name: "Hands-off", AppliesTo: "Production", Does: "Nothing on its own.",
				Proposes: "Every change, for a person to approve.", RollbackWindow: "-"},
			{Tier: "managed", Name: "Managed", AppliesTo: "Development", Does: "Right-sizes and stops idle capacity after a pre-notice.",
				Proposes: "Deletions and anything outside the rollback window.", RollbackWindow: "7 days"},
		},
		Clusters: []client.CostAutopilotCluster{
			{ClusterID: autopilotProdID, ClusterName: "eu-production", Environment: "production", EnvironmentKind: "production",
				Reason: "eu-production is Hands-off as a production cluster, so every change waits for a person.",
				Source: "environment", Tier: "hands-off"},
			{ClusterID: autopilotStagingID, ClusterName: "staging-1", Environment: "stage", EnvironmentKind: "staging",
				Override: &client.CostAutopilotOverride{Tier: "managed", Reason: "Load test week", SetBy: autopilotUserID,
					SetAt: "2026-09-21T09:00:00Z", ExpiresAt: autopilotPointer("2026-09-27T09:00:00Z")},
				Reason: "staging-1 is Managed by an override: Load test week.", Source: "override", Tier: "managed"},
		},
	}
}

// flattenAutopilotOutput joins the rendered lines with the table borders and
// padding stripped, so a note that wraps inside the table reads as one
// sentence.
func flattenAutopilotOutput(output string) string {
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		lines = append(lines, strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│")))
	}
	return strings.Join(lines, " ")
}

func autopilotRowLine(t *testing.T, output string, marker string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "│ "+marker) {
			return line
		}
	}
	t.Fatalf("no table row for %s:\n%s", marker, output)
	return ""
}

func TestCostAutopilotGetRendersPolicyTiersAndClusters(t *testing.T) {
	output, _, executeError := runCostAutopilotCommand(t, &costAutopilotMock{policy: costAutopilotFixture()}, "", "cost", "autopilot", "get")
	if executeError != nil {
		t.Fatalf("cost autopilot get failed: %v", executeError)
	}
	flat := flattenAutopilotOutput(output)
	for _, expected := range []string{
		"Cost autopilot policy, last changed by " + autopilotUserID + " at 2026-09-20T10:00:00Z",
		"Quiet hours: 22:00 to 07:00 Europe/Stockholm (the autopilot starts no change of its own then)",
		"Pre-notices go to notification route 5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3",
		"Tier by environment kind:", "Tiers:", "Clusters (2):",
		"↳ eu-production is Hands-off as a production cluster, so every change waits for a person.",
		`↳ Override "Load test week" set by ` + autopilotUserID + " at 2026-09-21T09:00:00Z.",
	} {
		if !strings.Contains(flat, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if line := autopilotRowLine(t, output, "development "); !strings.Contains(line, "managed") {
		t.Fatalf("the development kind's tier is shown: %s", line)
	}
	if line := autopilotRowLine(t, output, "staging-1 "); !strings.Contains(line, "stage (staging)") ||
		!strings.Contains(line, "managed") || !strings.Contains(line, "override until 2026-09-27T09:00:00Z") {
		t.Fatalf("an override names its expiry, and a label that is not its kind shows both: %s", line)
	}
	if line := autopilotRowLine(t, output, "eu-production "); !strings.Contains(line, "its environment") || strings.Contains(line, "(production)") {
		t.Fatalf("a tier the environment set says so: %s", line)
	}
	// The order of the kinds is the platform's, not the map's.
	if strings.Index(output, "│ production ") > strings.Index(output, "│ staging ") ||
		strings.Index(output, "│ staging ") > strings.Index(output, "│ development ") {
		t.Fatalf("environment kinds follow the platform's order:\n%s", output)
	}
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if width := text.StringWidthWithoutEscSequences(line); width > 100 {
			t.Fatalf("line is %d columns wide, over 100: %q\n%s", width, line, output)
		}
	}
}

func TestCostAutopilotGetForAPolicyNeverSet(t *testing.T) {
	policy := costAutopilotFixture()
	policy.IsSet = false
	policy.Defaults = map[string]string{"production": "hands-off", "staging": "hands-off", "development": "hands-off",
		"preview": "hands-off", "unknown": "hands-off"}
	policy.QuietHours, policy.NotificationRouteID, policy.UpdatedBy, policy.UpdatedAt = nil, nil, nil, nil
	policy.Clusters = []client.CostAutopilotCluster{}
	output, _, executeError := runCostAutopilotCommand(t, &costAutopilotMock{policy: policy}, "", "cost", "autopilot", "get")
	if executeError != nil {
		t.Fatalf("cost autopilot get failed: %v", executeError)
	}
	for _, expected := range []string{
		"Cost autopilot policy: never set, so every environment kind is hands-off (every change waits for a person).",
		"Quiet hours: none", "Pre-notices go through the organisation's default routing", "No live cluster yet.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if line := autopilotRowLine(t, output, "development "); !strings.Contains(line, "hands-off") || !strings.Contains(line, "managed") {
		t.Fatalf("an unset policy shows each kind's tier beside the recommended one: %s", line)
	}
}

func TestCostAutopilotGetStructuredOutputIsTheApiDocument(t *testing.T) {
	policy := costAutopilotFixture()
	policy.QuietHours, policy.NotificationRouteID = nil, nil
	output, _, executeError := runCostAutopilotCommand(t, &costAutopilotMock{policy: policy}, "", "cost", "autopilot", "get", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost autopilot get -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	for _, field := range []string{"quiet_hours", "notification_route_id"} {
		if value, present := decoded[field]; !present || value != nil {
			t.Fatalf("%s must stay null on the wire: %+v", field, decoded)
		}
	}
	clusters, _ := decoded["clusters"].([]any)
	if len(clusters) != 2 {
		t.Fatalf("clusters missing: %+v", decoded)
	}
	if value, present := clusters[0].(map[string]any)["override"]; !present || value != nil {
		t.Fatalf("a cluster on its environment's tier keeps a null override: %+v", clusters[0])
	}
	if decoded["defaults"].(map[string]any)["preview"] != "ephemeral" || decoded["is_set"] != true {
		t.Fatalf("the defaults must pass through: %+v", decoded)
	}
}

func TestCostAutopilotReportsAMissingRouteAsSuch(t *testing.T) {
	for _, routeError := range []error{
		client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found"),
		&client.UnexpectedResponseError{StatusCode: 404, Detail: "Not Found"},
		client.NewUnexpectedResponseError(405, "unexpected status: 405 Method Not Allowed"),
	} {
		_, _, getError := runCostAutopilotCommand(t, &costAutopilotMock{readError: routeError}, "", "cost", "autopilot", "get")
		if getError == nil || !strings.Contains(getError.Error(), "this platform does not serve the cost autopilot") ||
			!strings.Contains(getError.Error(), "GET /api/v1/org/cloud-cost/autopilot is not registered") || exitCodeFor(getError) != exitError {
			t.Fatalf("get error = %v (exit %d)", getError, exitCodeFor(getError))
		}
		_, _, setError := runCostAutopilotCommand(t, &costAutopilotMock{updateError: routeError}, "", "cost", "autopilot", "set",
			"--default", "development=managed")
		if setError == nil || !strings.Contains(setError.Error(), "PUT /api/v1/org/cloud-cost/autopilot is not registered") {
			t.Fatalf("set error = %v", setError)
		}
		// A cluster route that is missing, on a platform whose policy route is
		// missing too, is a platform that predates the autopilot.
		_, _, overrideError := runCostAutopilotCommand(t, &costAutopilotMock{clusterError: routeError, readError: routeError}, "",
			"cost", "autopilot", "override", autopilotStagingID, "--tier", "managed", "--reason", "Load test week")
		if overrideError == nil || !strings.Contains(overrideError.Error(), "PUT /api/v1/org/cloud-cost/autopilot/clusters/{cluster_id} is not registered") ||
			exitCodeFor(overrideError) != exitError {
			t.Fatalf("override error = %v (exit %d)", overrideError, exitCodeFor(overrideError))
		}
	}
}

// A cluster route's 404 is classified by asking the policy route, never by
// the wording of the 404's detail: whatever the detail says, a policy that
// answers means the cluster is not a live one (exit 3), and a policy that is
// missing too means the platform predates the autopilot.
func TestCostAutopilotClusterRoute404IsClassifiedByProbingThePolicy(t *testing.T) {
	for _, detail := range []string{"Cluster not found", "Not Found", ""} {
		clusterMissing := &client.UnexpectedResponseError{StatusCode: 404, Detail: detail}

		mock := &costAutopilotMock{clusterError: clusterMissing, policy: costAutopilotFixture()}
		_, _, clearError := runCostAutopilotCommand(t, mock, "", "cost", "autopilot", "clear", autopilotStagingID, "--yes")
		if clearError == nil || strings.Contains(clearError.Error(), "predates") ||
			!strings.Contains(clearError.Error(), "it is not a live cluster of this organisation") || exitCodeFor(clearError) != exitNotFound {
			t.Fatalf("detail %q, policy answers: error = %v (exit %d), want not a live cluster (exit 3)", detail, clearError, exitCodeFor(clearError))
		}

		mock = &costAutopilotMock{clusterError: clusterMissing, readError: client.NewUnexpectedResponseError(404, "request failed")}
		_, _, overrideError := runCostAutopilotCommand(t, mock, "", "cost", "autopilot", "override", autopilotStagingID,
			"--tier", "managed", "--reason", "Load test week")
		if overrideError == nil || !strings.Contains(overrideError.Error(), "this platform does not serve the cost autopilot") ||
			strings.Contains(overrideError.Error(), "not a live cluster of this organisation (") || exitCodeFor(overrideError) != exitError {
			t.Fatalf("detail %q, policy missing too: error = %v (exit %d), want predates (exit 1)", detail, overrideError, exitCodeFor(overrideError))
		}

		mock = &costAutopilotMock{clusterError: clusterMissing, readError: client.NewUnexpectedResponseError(502, "bad gateway")}
		_, _, unknownError := runCostAutopilotCommand(t, mock, "", "cost", "autopilot", "clear", autopilotStagingID, "--yes")
		if unknownError == nil || !strings.Contains(unknownError.Error(), "the autopilot policy could not be read to tell why (bad gateway)") ||
			!strings.Contains(unknownError.Error(), "so either the cluster is not a live one of this organisation or this platform predates the autopilot") ||
			exitCodeFor(unknownError) != exitError {
			t.Fatalf("detail %q, policy unreadable: error = %v (exit %d), want both causes named", detail, unknownError, exitCodeFor(unknownError))
		}
	}
	// A 405 on a cluster route is the route missing, with no probe needed.
	mock := &costAutopilotMock{clusterError: client.NewUnexpectedResponseError(405, "Method Not Allowed"), policy: costAutopilotFixture()}
	_, _, methodError := runCostAutopilotCommand(t, mock, "", "cost", "autopilot", "clear", autopilotStagingID, "--yes")
	if methodError == nil || !strings.Contains(methodError.Error(), "DELETE /api/v1/org/cloud-cost/autopilot/clusters/{cluster_id} is not registered") {
		t.Fatalf("a 405 on a cluster route is the route missing, got %v", methodError)
	}
}

// Omitted is not cleared: set sends the parts given and nothing else.
func TestCostAutopilotSetSendsOnlyThePartsGiven(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want map[string]string
	}{
		{[]string{"--default", "development=managed", "--default", "Preview=Ephemeral"},
			map[string]string{"defaults": `{"development":"managed","preview":"ephemeral"}`}},
		{[]string{"--clear-quiet-hours"}, map[string]string{"quiet_hours": "null"}},
		{[]string{"--quiet-hours", "22:00-07:00", "--timezone", "Europe/Stockholm"},
			map[string]string{"quiet_hours": `{"start":"22:00","end":"07:00","timezone":"Europe/Stockholm"}`}},
		{[]string{"--notification-route", "5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3"},
			map[string]string{"notification_route_id": `"5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3"`}},
		{[]string{"--clear-notification-route", "--default", "staging=managed"},
			map[string]string{"notification_route_id": "null", "defaults": `{"staging":"managed"}`}},
	} {
		mock := &costAutopilotMock{policy: costAutopilotFixture()}
		output, _, executeError := runCostAutopilotCommand(t, mock, "", append([]string{"cost", "autopilot", "set"}, testCase.args...)...)
		if executeError != nil {
			t.Fatalf("%v: cost autopilot set failed: %v", testCase.args, executeError)
		}
		if len(mock.updates) != 1 {
			t.Fatalf("%v: one write expected, got %d", testCase.args, len(mock.updates))
		}
		body := mock.updates[0].Body()
		if len(body) != len(testCase.want) {
			t.Fatalf("%v: body = %v, want exactly %v", testCase.args, body, testCase.want)
		}
		for field, want := range testCase.want {
			encoded, _ := json.Marshal(body[field])
			if string(encoded) != want {
				t.Fatalf("%v: %s = %s, want %s", testCase.args, field, encoded, want)
			}
		}
		if !strings.HasPrefix(output, "Autopilot policy updated.\n") {
			t.Fatalf("%v: the change is confirmed:\n%s", testCase.args, output)
		}
	}
}

func TestCostAutopilotSetRefusesAnEmptyOrMalformedChange(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{nil, "pass at least one of --default, --quiet-hours"},
		{[]string{"--default", "development"}, `--default "development" is not KIND=TIER`},
		{[]string{"--default", "staging=managed", "--default", "staging=scheduled"}, "--default names staging more than once"},
		{[]string{"--quiet-hours", "22:00", "--timezone", "UTC"}, `--quiet-hours "22:00" is not START-END`},
		{[]string{"--quiet-hours", "22:00-07:00"}, "flags in the group [quiet-hours timezone]"},
		{[]string{"--quiet-hours", "22:00-07:00", "--timezone", "UTC", "--clear-quiet-hours"}, "none of the others can be"},
	} {
		mock := &costAutopilotMock{policy: costAutopilotFixture()}
		_, _, executeError := runCostAutopilotCommand(t, mock, "", append([]string{"cost", "autopilot", "set"}, testCase.args...)...)
		if executeError == nil || !strings.Contains(executeError.Error(), testCase.want) || exitCodeFor(executeError) != exitUsage {
			t.Fatalf("%v: error = %v (exit %d), want usage error %q", testCase.args, executeError, exitCodeFor(executeError), testCase.want)
		}
		if len(mock.updates) != 0 {
			t.Fatalf("%v: a refused change must not be sent", testCase.args)
		}
	}
}

func TestCostAutopilotWritesSurfaceTheMissingPermission(t *testing.T) {
	_, _, setError := runCostAutopilotCommand(t, &costAutopilotMock{updateError: &client.PermissionDeniedError{Permission: "billing.manage"}},
		"", "cost", "autopilot", "set", "--default", "development=managed")
	if setError == nil || !strings.Contains(setError.Error(), `"billing.manage"`) || exitCodeFor(setError) != exitForbidden {
		t.Fatalf("set error = %v (exit %d)", setError, exitCodeFor(setError))
	}
	_, _, overrideError := runCostAutopilotCommand(t, &costAutopilotMock{clusterError: &client.PermissionDeniedError{Permission: "clusters.write"}},
		"", "cost", "autopilot", "override", autopilotStagingID, "--tier", "managed", "--reason", "Load test week")
	if overrideError == nil || !strings.Contains(overrideError.Error(), `"clusters.write"`) || exitCodeFor(overrideError) != exitForbidden {
		t.Fatalf("override error = %v (exit %d)", overrideError, exitCodeFor(overrideError))
	}
	detail := client.NewUnexpectedResponseError(400, "invalid autopilot policy: notification_route_id is not a route of this organisation")
	_, _, badError := runCostAutopilotCommand(t, &costAutopilotMock{updateError: detail}, "", "cost", "autopilot", "set",
		"--notification-route", "5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3")
	if badError == nil || !strings.Contains(badError.Error(), "notification_route_id is not a route of this organisation") {
		t.Fatalf("a 400 detail is relayed, got %v", badError)
	}
}

func TestCostAutopilotOverrideSendsTierReasonAndOnlyAGivenExpiry(t *testing.T) {
	cluster := costAutopilotFixture().Clusters[1]
	mock := &costAutopilotMock{cluster: &cluster}
	output, _, executeError := runCostAutopilotCommand(t, mock, "", "cost", "autopilot", "override", autopilotStagingID,
		"--tier", "Managed", "--reason", " Load test week ")
	if executeError != nil {
		t.Fatalf("cost autopilot override failed: %v", executeError)
	}
	if len(mock.overrides) != 1 || mock.overrideIDs[0] != autopilotStagingID {
		t.Fatalf("one override on the cluster expected, got %v", mock.overrideIDs)
	}
	request := mock.overrides[0]
	if request.Tier != "managed" || request.Reason != "Load test week" || request.ExpiresAt != nil {
		t.Fatalf("an override with no expiry sends none: %+v", request)
	}
	encoded, _ := json.Marshal(request)
	if strings.Contains(string(encoded), "expires_at") {
		t.Fatalf("an omitted expiry must not be on the wire: %s", encoded)
	}
	if !strings.HasPrefix(output, "staging-1 is now managed by override (until 2026-09-27T09:00:00Z).\n") {
		t.Fatalf("the override is confirmed with the cluster's tier as it now stands:\n%s", output)
	}

	mock = &costAutopilotMock{cluster: &cluster}
	if _, _, executeError = runCostAutopilotCommand(t, mock, "", "cost", "autopilot", "override", autopilotStagingID,
		"--tier", "managed", "--reason", "Quarter close", "--expires-at", "2026-10-01T02:00:00+02:00"); executeError != nil {
		t.Fatalf("cost autopilot override --expires-at failed: %v", executeError)
	}
	if expiresAt := mock.overrides[0].ExpiresAt; expiresAt == nil || *expiresAt != "2026-10-01T00:00:00Z" {
		t.Fatalf("--expires-at is sent in UTC, got %v", expiresAt)
	}

	mock = &costAutopilotMock{cluster: &cluster}
	before := time.Now().Add(72 * time.Hour).Add(-time.Minute)
	if _, _, executeError = runCostAutopilotCommand(t, mock, "", "cost", "autopilot", "override", autopilotStagingID,
		"--tier", "managed", "--reason", "Launch freeze", "--expires-in", "72h"); executeError != nil {
		t.Fatalf("cost autopilot override --expires-in failed: %v", executeError)
	}
	expiresAt, parseError := time.Parse(time.RFC3339, *mock.overrides[0].ExpiresAt)
	if parseError != nil || expiresAt.Before(before) || expiresAt.After(time.Now().Add(72*time.Hour).Add(time.Minute)) {
		t.Fatalf("--expires-in 72h is sent as now plus 72h, got %v", *mock.overrides[0].ExpiresAt)
	}
}

func TestCostAutopilotOverrideRefusals(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{[]string{autopilotStagingID, "--reason", "why not"}, `required flag(s) "tier" not set`},
		{[]string{autopilotStagingID, "--tier", "managed", "--reason", "why", "--expires-at", "next week"}, `--expires-at "next week" is not an RFC 3339 time`},
		{[]string{autopilotStagingID, "--tier", "managed", "--reason", "why", "--expires-in", "-1h"}, "--expires-in must be a positive duration"},
	} {
		mock := &costAutopilotMock{}
		_, _, executeError := runCostAutopilotCommand(t, mock, "", append([]string{"cost", "autopilot", "override"}, testCase.args...)...)
		if executeError == nil || !strings.Contains(executeError.Error(), testCase.want) || exitCodeFor(executeError) != exitUsage {
			t.Fatalf("%v: error = %v (exit %d), want usage error %q", testCase.args, executeError, exitCodeFor(executeError), testCase.want)
		}
		if len(mock.overrides) != 0 {
			t.Fatalf("%v: a refused override must not be sent", testCase.args)
		}
	}
	// The route's own 404, with the policy route answering, says the cluster
	// is not a live one: exit 3, not "this platform predates it".
	notLive := &client.UnexpectedResponseError{StatusCode: 404, Detail: "Cluster not found"}
	_, _, executeError := runCostAutopilotCommand(t, &costAutopilotMock{clusterError: notLive, policy: costAutopilotFixture()}, "",
		"cost", "autopilot", "override", autopilotStagingID, "--tier", "managed", "--reason", "why not")
	if executeError == nil || strings.Contains(executeError.Error(), "predates") ||
		!strings.Contains(executeError.Error(), "it is not a live cluster of this organisation") || exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("error = %v (exit %d)", executeError, exitCodeFor(executeError))
	}
}

func TestCostAutopilotClearAsksFirstOnStderr(t *testing.T) {
	cleared := client.CostAutopilotCluster{ClusterID: autopilotStagingID, ClusterName: "staging-1", Environment: "stage",
		EnvironmentKind: "staging", Reason: "staging-1 is Scheduled as a staging cluster.", Source: "environment", Tier: "scheduled"}
	mock := &costAutopilotMock{cluster: &cleared}
	stdout, stderr, executeError := runCostAutopilotCommand(t, mock, "n\n", "cost", "autopilot", "clear", autopilotStagingID)
	if executeError == nil || exitCodeFor(executeError) != exitCancelled || len(mock.clearedIDs) != 0 {
		t.Fatalf("a declined prompt clears nothing and exits %d, got %v (cleared %v)", exitCancelled, executeError, mock.clearedIDs)
	}
	if !strings.Contains(stderr, "Remove the autopilot override on "+autopilotStagingID+" and return it to its environment's tier? [y/N]: ") ||
		strings.Contains(stdout, "[y/N]") {
		t.Fatalf("the prompt goes to stderr, not stdout: stdout=%q stderr=%q", stdout, stderr)
	}

	mock = &costAutopilotMock{cluster: &cleared}
	stdout, _, executeError = runCostAutopilotCommand(t, mock, "y\n", "cost", "autopilot", "clear", autopilotStagingID)
	if executeError != nil || len(mock.clearedIDs) != 1 || mock.clearedIDs[0] != autopilotStagingID {
		t.Fatalf("a confirmed prompt clears the override, got %v (cleared %v)", executeError, mock.clearedIDs)
	}
	if !strings.HasPrefix(stdout, "staging-1 takes its environment's tier: scheduled.\n") {
		t.Fatalf("the clear is confirmed with the tier as it now stands:\n%s", stdout)
	}

	mock = &costAutopilotMock{cluster: &cleared}
	stdout, _, executeError = runCostAutopilotCommand(t, mock, "", "cost", "autopilot", "clear", autopilotStagingID, "--yes", "-o", "json")
	if executeError != nil || len(mock.clearedIDs) != 1 {
		t.Fatalf("--yes skips the prompt, got %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("-o json stays parseable: %v\n%s", unmarshalError, stdout)
	}
	if value, present := decoded["override"]; !present || value != nil || decoded["source"] != "environment" {
		t.Fatalf("the cleared cluster passes through: %+v", decoded)
	}
}
