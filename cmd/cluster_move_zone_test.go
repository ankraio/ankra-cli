package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type zoneMoveMock struct {
	clusters      []client.ClusterListItem
	startRequests []client.ZoneMoveRequest
	startMove     *client.ZoneMove
	startError    error
	// readings are answered in turn by GetZoneMove; the last one repeats.
	readings      []*client.ZoneMove
	readError     error
	readCalls     int
	advanceCalls  []client.ZoneMoveAction
	advanceAnswer *client.ZoneMove
}

func (m *zoneMoveMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	for _, cluster := range m.clusters {
		if cluster.ID == clusterID {
			return cluster, nil
		}
	}
	return client.ClusterListItem{}, client.ErrClusterNotFound
}

func (m *zoneMoveMock) GetCluster(name string) (client.ClusterListItem, error) {
	for _, cluster := range m.clusters {
		if cluster.Name == name || cluster.ID == name {
			return cluster, nil
		}
	}
	return client.ClusterListItem{}, client.ErrClusterNotFound
}

func (m *zoneMoveMock) StartZoneMove(ctx context.Context, sourceClusterID string, request client.ZoneMoveRequest) (*client.ZoneMove, error) {
	m.startRequests = append(m.startRequests, request)
	if m.startError != nil {
		return nil, m.startError
	}
	move := *m.startMove
	if request.PlanOnly {
		move.ID = ""
		move.Status = client.ZoneMoveStatusPlanned
	}
	return &move, nil
}

func (m *zoneMoveMock) GetZoneMove(ctx context.Context, sourceClusterID string) (*client.ZoneMove, error) {
	m.readCalls++
	if m.readError != nil {
		return nil, m.readError
	}
	index := m.readCalls - 1
	if index >= len(m.readings) {
		index = len(m.readings) - 1
	}
	return m.readings[index], nil
}

func (m *zoneMoveMock) AdvanceZoneMove(ctx context.Context, sourceClusterID string, action client.ZoneMoveAction) (*client.ZoneMove, error) {
	m.advanceCalls = append(m.advanceCalls, action)
	return m.advanceAnswer, nil
}

func (m *zoneMoveMock) startCalls(planOnly bool) int {
	count := 0
	for _, request := range m.startRequests {
		if request.PlanOnly == planOnly {
			count++
		}
	}
	return count
}

func zoneMoveFixture(status string) *client.ZoneMove {
	targetOrigin := "lb-target"
	return &client.ZoneMove{
		ID: "move-1", SourceClusterID: "c-fr", Zone: "se-sto1", Status: status,
		Plan: client.ZoneMovePlan{
			Target: client.ZoneMoveTarget{
				Name: "avura-stockholm", Zone: "se-sto1", NetworkMode: "ipv6_only", StorageTier: "standard",
				ControlPlane: client.ZoneMoveControlPlane{Plan: "starter-4c-8g", Count: 3}, BastionPlan: "edge-small",
				NodeGroups: []client.ZoneMoveNodeGroup{{Name: "default", Plan: "cloud-native-4c-8g", Count: 3}},
			},
			Stacks: []client.ZoneMoveStack{
				{Name: "avura", Carried: true},
				{Name: "scratch", Carried: false, Reason: textPointer("manifests only reference the source")},
			},
			Data:       []client.ZoneMoveDataVolume{{Namespace: "avura", Name: "miniodata-minio-0", Kind: "pvc", RequestedBytes: 10737418240}},
			NotCarried: []client.ZoneMoveOmission{{What: "pvc avura/backups", Reason: "backup scratch space"}},
			DNS: []client.ZoneMoveHostname{{Hostname: "avura.work", Mechanism: client.ZoneMoveDNSMechanismAnkraCloudProxied,
				SourceOrigin: textPointer("lb-source"), TargetOrigin: &targetOrigin}},
		},
		Steps: []client.ZoneMoveStep{
			{Name: "provision", Status: client.ZoneMoveStepSucceeded, StartedAt: textPointer("2026-11-21T10:00:00Z")},
			{Name: "seed", Status: client.ZoneMoveStepRunning},
		},
	}
}

func newZoneMoveMock() *zoneMoveMock {
	return &zoneMoveMock{
		clusters: []client.ClusterListItem{
			{ID: "c-fr", Name: "avura-france", Kind: "ankracloud"},
			{ID: "c-se", Name: "avura-stockholm", Kind: "ankracloud"},
		},
		startMove: zoneMoveFixture(client.ZoneMoveStatusProvisioning),
	}
}

func fastZoneMovePolling(t *testing.T) {
	t.Helper()
	original := zoneMovePollInterval
	zoneMovePollInterval = time.Millisecond
	t.Cleanup(func() { zoneMovePollInterval = original })
}

func runZoneMoveCommand(t *testing.T, mock *zoneMoveMock, input string, args ...string) (string, error) {
	t.Helper()
	return runConfirmCommand(t, mock, input, []*cobra.Command{clusterMoveCmd}, append([]string{"cluster", "move"}, args...)...)
}

func TestClusterMoveZone_FlagCombinationsAreRefusedBeforeAnythingIsSent(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{[]string{"avura-france", "--organisation", "acme", "--zone", "se-sto1"}, "--organisation and --zone cannot be combined"},
		{[]string{"avura-france", "--organisation", "acme", "--status"}, "--organisation and --status cannot be combined"},
		{[]string{"avura-france", "--zone", "se-sto1", "--cutover"}, "--zone and --cutover cannot be combined"},
		{[]string{"avura-france", "--rollback", "--retire"}, "--rollback and --retire cannot be combined"},
		{[]string{"avura-france", "--status", "--plan"}, "--plan only applies when starting a zone move"},
		{[]string{"avura-france", "--cutover", "--target-name", "x"}, "--target-name only applies when starting"},
		{[]string{"avura-france", "--zone", "se-sto1", "--target-cluster", "avura-stockholm", "--worker-count", "2"},
			"--target-cluster moves into a cluster that already exists, so --worker-count"},
		{[]string{"avura-france", "--zone", "se-sto1", "--plan", "--wait"}, "--wait does not apply to --plan"},
		{[]string{"avura-france", "--zone", "se-sto1", "--control-plane-count", "0"}, "--control-plane-count must be at least 1"},
		{[]string{"avura-france", "--zone", " "}, "--zone needs a zone name"},
		{[]string{"avura-france"}, "a destination is required"},
		{[]string{"avura-france", "--wait"}, "--wait only applies to a zone move"},
	} {
		t.Run(strings.Join(testCase.args, " "), func(t *testing.T) {
			mock := newZoneMoveMock()
			_, runError := runZoneMoveCommand(t, mock, "", testCase.args...)
			if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), testCase.want) {
				t.Fatalf("error = %v (exit %d), want a usage error containing %q", runError, exitCodeFor(runError), testCase.want)
			}
			if len(mock.startRequests) != 0 || mock.readCalls != 0 || len(mock.advanceCalls) != 0 {
				t.Fatalf("expected no requests, got start=%d read=%d advance=%d", len(mock.startRequests), mock.readCalls, len(mock.advanceCalls))
			}
		})
	}
}

func TestClusterMoveZone_NeverFallsBackToTheSelectedCluster(t *testing.T) {
	mock := newZoneMoveMock()
	_, runError := runZoneMoveCommand(t, mock, "", "--zone", "se-sto1", "--plan")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--cluster") {
		t.Fatalf("error = %v, want a usage error naming --cluster", runError)
	}
	if len(mock.startRequests) != 0 {
		t.Fatalf("expected no request, got %d", len(mock.startRequests))
	}
}

func TestClusterMoveZone_PlanRendersEverythingAndWritesNothing(t *testing.T) {
	mock := newZoneMoveMock()
	output, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--zone", "se-sto1", "--plan", "--storage-tier", "standard")
	if runError != nil {
		t.Fatalf("plan failed: %v\n%s", runError, output)
	}
	if len(mock.startRequests) != 1 || !mock.startRequests[0].PlanOnly || mock.startRequests[0].Zone != "se-sto1" ||
		mock.startRequests[0].StorageTier != "standard" || mock.startRequests[0].ControlPlaneCount != nil {
		t.Fatalf("plan request = %+v", mock.startRequests)
	}
	for _, want := range []string{
		"Zone move plan: avura-france -> zone se-sto1", "Target cluster: avura-stockholm", "ipv6_only",
		"Control plane:  3 x starter-4c-8g", "Node group default: 3 x cloud-native-4c-8g", "Bastion:        edge-small",
		"Stacks carried (1)", "- avura", "scratch: manifests only reference the source",
		"Data volumes copied (1, 10.0 GiB requested)", "miniodata-minio-0",
		"pvc avura/backups: backup scratch space",
		"avura.work", "proxied record repointed to the target load balancer", "lb-source", "lb-target",
		"Nothing was changed.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("plan output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "[y/N]") {
		t.Fatalf("a plan must not prompt:\n%s", output)
	}
}

func TestClusterMoveZone_PlanAsJSON(t *testing.T) {
	mock := newZoneMoveMock()
	output, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--zone", "se-sto1", "--plan", "-o", "json")
	if runError != nil {
		t.Fatalf("plan failed: %v", runError)
	}
	var decoded client.ZoneMove
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, output)
	}
	if decoded.Plan.Target.Name != "avura-stockholm" || len(decoded.Plan.Data) != 1 {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestClusterMoveZone_StartDeclinedShowsThePlanAndStartsNothing(t *testing.T) {
	mock := newZoneMoveMock()
	output, runError := runZoneMoveCommand(t, mock, "n\n", "avura-france", "--zone", "se-sto1")
	if !errors.Is(runError, errCancelled) || exitCodeFor(runError) != exitCancelled {
		t.Fatalf("expected errCancelled, got %v", runError)
	}
	if mock.startCalls(true) != 1 || mock.startCalls(false) != 0 {
		t.Fatalf("expected one plan and no start, got %+v", mock.startRequests)
	}
	for _, want := range []string{"miniodata-minio-0", `Start moving cluster "avura-france" to zone "se-sto1" as cluster "avura-stockholm"?`, "until you run --cutover"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestClusterMoveZone_StartWithYesSendsTheOverridesAndPrintsTheNextCommand(t *testing.T) {
	mock := newZoneMoveMock()
	output, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--zone", "se-sto1", "--yes",
		"--target-name", "avura-stockholm", "--control-plane-count", "3", "--control-plane-plan", "starter-4c-8g",
		"--worker-plan", "cloud-native-4c-8g", "--worker-count", "3", "--storage-tier", "standard", "--soak-days", "7")
	if runError != nil {
		t.Fatalf("start failed: %v\n%s", runError, output)
	}
	if mock.startCalls(true) != 0 || mock.startCalls(false) != 1 {
		t.Fatalf("expected a single start with --yes, got %+v", mock.startRequests)
	}
	request := mock.startRequests[0]
	if request.Zone != "se-sto1" || request.TargetName != "avura-stockholm" || request.ControlPlaneCount == nil ||
		*request.ControlPlaneCount != 3 || request.WorkerCount == nil || *request.WorkerCount != 3 ||
		request.SoakDays == nil || *request.SoakDays != 7 || request.ControlPlanePlan != "starter-4c-8g" ||
		request.WorkerPlan != "cloud-native-4c-8g" || request.StorageTier != "standard" {
		t.Fatalf("request = %+v", request)
	}
	for _, want := range []string{"Zone move move-1", "Status:  provisioning", "seed", "ankra cluster move avura-france --status --wait"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestClusterMoveZone_StartIntoAnExistingTargetCluster(t *testing.T) {
	mock := newZoneMoveMock()
	_, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--target-cluster", "avura-stockholm", "--yes")
	if runError != nil {
		t.Fatalf("start failed: %v", runError)
	}
	if len(mock.startRequests) != 1 || mock.startRequests[0].TargetClusterID != "c-se" || mock.startRequests[0].Zone != "" {
		t.Fatalf("request = %+v", mock.startRequests)
	}

	itself := newZoneMoveMock()
	_, runError = runZoneMoveCommand(t, itself, "", "avura-france", "--target-cluster", "c-fr", "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("moving into the source itself must be a usage error, got %v", runError)
	}

	missing := newZoneMoveMock()
	_, runError = runZoneMoveCommand(t, missing, "", "avura-france", "--target-cluster", "nowhere", "--yes")
	if runError == nil || exitCodeFor(runError) != exitNotFound || len(missing.startRequests) != 0 {
		t.Fatalf("an unknown target must exit 3 before sending, got %v", runError)
	}
}

func TestClusterMoveZone_ActiveMoveConflictIsABlock(t *testing.T) {
	mock := newZoneMoveMock()
	mock.startError = client.NewUnexpectedResponseError(409, "a zone move of this cluster is already active")
	_, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--zone", "se-sto1", "--yes")
	if runError == nil || exitCodeFor(runError) != exitBlocked || !strings.Contains(runError.Error(), "already active") {
		t.Fatalf("error = %v (exit %d), want exit %d", runError, exitCodeFor(runError), exitBlocked)
	}
}

func TestClusterMoveZone_CutoverWithoutAMoveIsNotFound(t *testing.T) {
	mock := newZoneMoveMock()
	mock.readError = client.NewUnexpectedResponseError(404, "No zone move for this cluster")
	_, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--cutover", "--yes")
	if runError == nil || exitCodeFor(runError) != exitNotFound || !strings.Contains(runError.Error(), "avura-france has no zone move") {
		t.Fatalf("error = %v (exit %d), want exit 3", runError, exitCodeFor(runError))
	}
	if len(mock.advanceCalls) != 0 {
		t.Fatalf("expected no cutover, got %v", mock.advanceCalls)
	}
}

func TestClusterMoveZone_CutoverBeforeTheMoveIsReadyIsBlocked(t *testing.T) {
	mock := newZoneMoveMock()
	mock.readings = []*client.ZoneMove{zoneMoveFixture(client.ZoneMoveStatusSeeding)}
	_, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--cutover", "--yes")
	if runError == nil || exitCodeFor(runError) != exitBlocked || !strings.Contains(runError.Error(), "runs only from ready_for_cutover") {
		t.Fatalf("error = %v (exit %d), want exit %d", runError, exitCodeFor(runError), exitBlocked)
	}
	if len(mock.advanceCalls) != 0 {
		t.Fatalf("expected no cutover, got %v", mock.advanceCalls)
	}
}

func TestClusterMoveZone_EachStepConfirmsWithItsOwnSentence(t *testing.T) {
	for _, testCase := range []struct {
		flag   string
		status string
		want   string
	}{
		{"--cutover", client.ZoneMoveStatusReadyForCutover, "The maintenance page goes on, the source is frozen, the final copy is taken and traffic switches"},
		{"--rollback", client.ZoneMoveStatusSoaking, "writes made on the target \"avura-stockholm\" since the cutover are not copied back"},
		{"--retire", client.ZoneMoveStatusSoaking, "deprovisions the source cluster and cannot be undone"},
	} {
		t.Run(testCase.flag, func(t *testing.T) {
			mock := newZoneMoveMock()
			mock.readings = []*client.ZoneMove{zoneMoveFixture(testCase.status)}
			output, runError := runZoneMoveCommand(t, mock, "n\n", "avura-france", testCase.flag)
			if !errors.Is(runError, errCancelled) {
				t.Fatalf("expected errCancelled, got %v", runError)
			}
			if !strings.Contains(output, testCase.want) {
				t.Fatalf("prompt missing %q:\n%s", testCase.want, output)
			}
			if len(mock.advanceCalls) != 0 {
				t.Fatalf("declined, yet sent %v", mock.advanceCalls)
			}
		})
	}
}

func TestClusterMoveZone_RetireWithYesReadsTheMoveWhenTheAnswerHasNone(t *testing.T) {
	mock := newZoneMoveMock()
	mock.readings = []*client.ZoneMove{zoneMoveFixture(client.ZoneMoveStatusSoaking), zoneMoveFixture(client.ZoneMoveStatusRetiring)}
	output, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--retire", "--yes")
	if runError != nil {
		t.Fatalf("retire failed: %v", runError)
	}
	if len(mock.advanceCalls) != 1 || mock.advanceCalls[0] != client.ZoneMoveActionRetire || mock.readCalls != 2 {
		t.Fatalf("advance=%v reads=%d", mock.advanceCalls, mock.readCalls)
	}
	if !strings.Contains(output, "Status:  retiring") {
		t.Fatalf("output:\n%s", output)
	}
}

func TestClusterMoveZone_StatusWaitFollowsUntilTheMoveRests(t *testing.T) {
	fastZoneMovePolling(t)
	mock := newZoneMoveMock()
	ready := zoneMoveFixture(client.ZoneMoveStatusReadyForCutover)
	ready.Steps[1].Status = client.ZoneMoveStepSucceeded
	mock.readings = []*client.ZoneMove{
		zoneMoveFixture(client.ZoneMoveStatusSeeding), zoneMoveFixture(client.ZoneMoveStatusSeeding), ready,
	}
	output, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--status", "--wait")
	if runError != nil {
		t.Fatalf("wait failed: %v\n%s", runError, output)
	}
	if mock.readCalls != 3 {
		t.Fatalf("expected three reads, got %d", mock.readCalls)
	}
	for _, want := range []string{"Zone move move-1: seeding (seed)", "Zone move move-1: ready_for_cutover", "ankra cluster move avura-france --cutover"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Count(output, "seeding (seed)") != 1 {
		t.Fatalf("an unchanged state is reported once:\n%s", output)
	}
}

func TestClusterMoveZone_WaitOnAFailedMoveExitsNonZeroWithTheStep(t *testing.T) {
	fastZoneMovePolling(t)
	mock := newZoneMoveMock()
	failed := zoneMoveFixture(client.ZoneMoveStatusFailed)
	failed.Steps[1].Status = client.ZoneMoveStepFailed
	failed.Steps[1].Message = textPointer("volume copy timed out")
	mock.readings = []*client.ZoneMove{zoneMoveFixture(client.ZoneMoveStatusSeeding), failed}
	_, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--status", "--wait")
	if runError == nil || exitCodeFor(runError) != exitError || !strings.Contains(runError.Error(), "at step seed: volume copy timed out") {
		t.Fatalf("error = %v (exit %d)", runError, exitCodeFor(runError))
	}
}

func TestClusterMoveZone_WaitTimeoutExitsFive(t *testing.T) {
	fastZoneMovePolling(t)
	mock := newZoneMoveMock()
	mock.readings = []*client.ZoneMove{zoneMoveFixture(client.ZoneMoveStatusSeeding)}
	output, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--status", "--wait", "--timeout", "20ms", "-o", "json")
	if runError == nil || exitCodeFor(runError) != exitWaitTimeout {
		t.Fatalf("error = %v (exit %d), want exit %d", runError, exitCodeFor(runError), exitWaitTimeout)
	}
	if !strings.Contains(output, `"status": "seeding"`) {
		t.Fatalf("the last reading is still printed:\n%s", output)
	}
}

func TestClusterMoveZone_CutoverWaitMustReachSoaking(t *testing.T) {
	fastZoneMovePolling(t)
	mock := newZoneMoveMock()
	mock.readings = []*client.ZoneMove{
		zoneMoveFixture(client.ZoneMoveStatusReadyForCutover),
		zoneMoveFixture(client.ZoneMoveStatusFinalCopy),
		zoneMoveFixture(client.ZoneMoveStatusSoaking),
	}
	mock.advanceAnswer = zoneMoveFixture(client.ZoneMoveStatusFreezing)
	output, runError := runZoneMoveCommand(t, mock, "", "avura-france", "--cutover", "--yes", "--wait")
	if runError != nil {
		t.Fatalf("cutover wait failed: %v\n%s", runError, output)
	}
	if len(mock.advanceCalls) != 1 || mock.advanceCalls[0] != client.ZoneMoveActionCutover {
		t.Fatalf("advance = %v", mock.advanceCalls)
	}
	for _, want := range []string{"freezing", "final_copy", "Status:  soaking", "--rollback", "--retire"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
