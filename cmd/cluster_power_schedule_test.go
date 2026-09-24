package cmd

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"ankra/internal/client"
)

type powerScheduleCall struct {
	ClusterID  string
	ScheduleID string
	Request    client.PowerScheduleRequest
}

type powerScheduleMock struct {
	baseMock

	lists   []string
	creates []powerScheduleCall
	updates []powerScheduleCall
	deletes []powerScheduleCall

	// nodeLocal and nodeLocalError answer the node-local read; both nil is
	// a reading that could not be made. nodeLocalReads counts the reads.
	nodeLocal      *client.PowerScheduleNodeLocalStorage
	nodeLocalError error
	nodeLocalReads int
}

func (m *powerScheduleMock) GetPowerScheduleNodeLocalStorage(clusterID string) (*client.PowerScheduleNodeLocalStorage, error) {
	m.nodeLocalReads++
	return m.nodeLocal, m.nodeLocalError
}

func (m *powerScheduleMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "cluster-1", Name: name, Kind: "hetzner"}, nil
}

func (m *powerScheduleMock) listing() *client.PowerScheduleListResult {
	cronExpression := "0 19 * * 1-5"
	return &client.PowerScheduleListResult{Schedules: []client.PowerSchedule{
		{ID: "sched-1", Action: "stop", ScheduleKind: "cron",
			CronExpression: &cronExpression, Timezone: "Europe/Stockholm", Enabled: true},
	}}
}

func (m *powerScheduleMock) ListPowerSchedules(clusterID string) (*client.PowerScheduleListResult, error) {
	m.lists = append(m.lists, clusterID)
	return m.listing(), nil
}

func (m *powerScheduleMock) CreatePowerSchedule(clusterID string, request client.PowerScheduleRequest) (*client.PowerScheduleListResult, error) {
	m.creates = append(m.creates, powerScheduleCall{ClusterID: clusterID, Request: request})
	return m.listing(), nil
}

func (m *powerScheduleMock) UpdatePowerSchedule(clusterID, scheduleID string, request client.PowerScheduleRequest) (*client.PowerScheduleListResult, error) {
	m.updates = append(m.updates, powerScheduleCall{ClusterID: clusterID, ScheduleID: scheduleID, Request: request})
	return m.listing(), nil
}

func (m *powerScheduleMock) DeletePowerSchedule(clusterID, scheduleID string) (*client.DeletePowerScheduleResult, error) {
	m.deletes = append(m.deletes, powerScheduleCall{ClusterID: clusterID, ScheduleID: scheduleID})
	return &client.DeletePowerScheduleResult{Deleted: true}, nil
}

// executePowerScheduleCommand runs args against rootCmd with stdin wired for
// the delete [y/N] prompt, resetting the family's flag state around the run
// (rootCmd is a process global).
func executePowerScheduleCommand(t *testing.T, stdinContent string, args ...string) error {
	t.Helper()
	_, err := executePowerScheduleCommandCapturingStderr(t, stdinContent, args...)
	return err
}

// executePowerScheduleCommandCapturingStderr is executePowerScheduleCommand
// that also answers what the command wrote to stderr.
func executePowerScheduleCommandCapturingStderr(t *testing.T, stdinContent string, args ...string) (string, error) {
	t.Helper()
	resetPowerScheduleFlags()
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetPowerScheduleFlags()
	})
	stderr := new(strings.Builder)
	rootCmd.SetOut(new(strings.Builder))
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(stdinContent))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return stderr.String(), err
}

func resetPowerScheduleFlags() {
	reset := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
	reset(clusterPowerSchedulesListCmd.Flags())
	reset(clusterPowerSchedulesCreateCmd.Flags())
	reset(clusterPowerSchedulesUpdateCmd.Flags())
	reset(clusterPowerSchedulesDeleteCmd.Flags())
	reset(clusterCmd.PersistentFlags())
}

func TestClusterPowerSchedulesList_UsesResolvedCluster(t *testing.T) {
	mock := &powerScheduleMock{}
	setMockClient(t, mock)

	if err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "list",
		"--cluster", "my-cluster"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mock.lists) != 1 || mock.lists[0] != "cluster-1" {
		t.Fatalf("expected one list call for cluster-1, got %+v", mock.lists)
	}
}

func TestClusterPowerSchedulesCreate_CronSendsTimezoneAndEnabled(t *testing.T) {
	mock := &powerScheduleMock{}
	setMockClient(t, mock)

	if err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "create",
		"--cluster", "my-cluster", "--action", "stop",
		"--cron", "0 19 * * 1-5", "--timezone", "Europe/Stockholm"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(mock.creates) != 1 {
		t.Fatalf("expected one create call, got %d", len(mock.creates))
	}
	request := mock.creates[0].Request
	if request.Action != "stop" || request.ScheduleKind != "cron" ||
		request.CronExpression == nil || *request.CronExpression != "0 19 * * 1-5" ||
		request.Timezone == nil || *request.Timezone != "Europe/Stockholm" ||
		!request.Enabled || request.RunAt != nil {
		t.Fatalf("unexpected request: %+v", request)
	}
}

func TestClusterPowerSchedulesCreate_CronDefaultsTimezoneUTC(t *testing.T) {
	mock := &powerScheduleMock{}
	setMockClient(t, mock)

	if err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "create",
		"--cluster", "my-cluster", "--action", "start", "--cron", "0 7 * * 1-5"); err != nil {
		t.Fatalf("create: %v", err)
	}
	request := mock.creates[0].Request
	if request.Timezone == nil || *request.Timezone != "UTC" {
		t.Fatalf("cron schedules must restate an explicit timezone (UTC default), got %+v", request.Timezone)
	}
}

func TestClusterPowerSchedulesCreate_RequiresExactlyOneCadence(t *testing.T) {
	mock := &powerScheduleMock{}
	setMockClient(t, mock)

	err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "create",
		"--cluster", "my-cluster", "--action", "stop")
	if err == nil || !strings.Contains(err.Error(), "exactly one of --at") {
		t.Fatalf("expected the cadence requirement, got %v", err)
	}

	err = executePowerScheduleCommand(t, "", "cluster", "power-schedules", "create",
		"--cluster", "my-cluster", "--action", "stop",
		"--at", "2030-01-01T00:00:00Z", "--cron", "0 19 * * 1-5")
	if err == nil || !strings.Contains(err.Error(), "exactly one of --at") {
		t.Fatalf("expected the cadence exclusivity refusal, got %v", err)
	}
	if len(mock.creates) != 0 {
		t.Fatalf("invalid flag combinations must not reach the API, got %+v", mock.creates)
	}
}

func TestClusterPowerSchedulesCreate_RejectsTimezoneWithAt(t *testing.T) {
	mock := &powerScheduleMock{}
	setMockClient(t, mock)

	err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "create",
		"--cluster", "my-cluster", "--action", "stop",
		"--at", "2030-01-01T00:00:00Z", "--timezone", "Europe/Stockholm")
	if err == nil || !strings.Contains(err.Error(), "--timezone only applies to --cron") {
		t.Fatalf("expected the timezone/at refusal, got %v", err)
	}
}

func TestClusterPowerSchedulesUpdate_SendsPausedFullReplace(t *testing.T) {
	mock := &powerScheduleMock{}
	setMockClient(t, mock)

	if err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "update", "sched-1",
		"--cluster", "my-cluster", "--action", "stop",
		"--cron", "0 19 * * 1-5", "--timezone", "Europe/Stockholm", "--enabled=false"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(mock.updates) != 1 {
		t.Fatalf("expected one update call, got %d", len(mock.updates))
	}
	call := mock.updates[0]
	if call.ScheduleID != "sched-1" || call.ClusterID != "cluster-1" || call.Request.Enabled {
		t.Fatalf("unexpected update call: %+v", call)
	}
}

func TestClusterPowerSchedulesDelete_PromptDeclineSkipsAPI(t *testing.T) {
	mock := &powerScheduleMock{}
	setMockClient(t, mock)

	err := executePowerScheduleCommand(t, "n\n", "cluster", "power-schedules", "delete", "sched-1",
		"--cluster", "my-cluster")
	if err == nil {
		t.Fatal("expected the cancelled error when declining the prompt")
	}
	if len(mock.deletes) != 0 {
		t.Fatalf("a declined prompt must not reach the API, got %+v", mock.deletes)
	}
}

func TestClusterPowerSchedulesDelete_YesSkipsPrompt(t *testing.T) {
	mock := &powerScheduleMock{}
	setMockClient(t, mock)

	if err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "delete", "sched-1",
		"--cluster", "my-cluster", "--yes"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(mock.deletes) != 1 || mock.deletes[0].ScheduleID != "sched-1" {
		t.Fatalf("expected one delete call for sched-1, got %+v", mock.deletes)
	}
}

// --stop-mode and --preserve-state ride the stop schedule request: the mode
// as given, preserve-state three-state (absent leaves the backend default),
// and both refused on a start schedule.
func TestClusterPowerSchedulesCreate_SendsStopModeAndPreserveState(t *testing.T) {
	mock := &powerScheduleMock{}
	setMockClient(t, mock)
	t.Cleanup(func() {
		_ = clusterPowerSchedulesCreateCmd.Flags().Set("stop-mode", "")
		_ = clusterPowerSchedulesCreateCmd.Flags().Set("preserve-state", "")
	})

	if err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "create",
		"--cluster", "my-cluster", "--action", "stop", "--cron", "0 19 * * 1-5",
		"--stop-mode", "delete_resources", "--preserve-state=false"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(mock.creates) != 1 {
		t.Fatalf("expected one create call, got %d", len(mock.creates))
	}
	request := mock.creates[0].Request
	if request.StopMode != "delete_resources" || request.PreserveState == nil || *request.PreserveState {
		t.Fatalf("unexpected request: %+v", request)
	}

	_ = clusterPowerSchedulesCreateCmd.Flags().Set("stop-mode", "")
	_ = clusterPowerSchedulesCreateCmd.Flags().Set("preserve-state", "")
	if err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "create",
		"--cluster", "my-cluster", "--action", "stop", "--cron", "0 19 * * 1-5"); err != nil {
		t.Fatalf("create without state flags: %v", err)
	}
	request = mock.creates[1].Request
	if request.StopMode != "" || request.PreserveState != nil {
		t.Fatalf("absent flags must leave the backend defaults: %+v", request)
	}

	_ = clusterPowerSchedulesCreateCmd.Flags().Set("stop-mode", "")
	_ = clusterPowerSchedulesCreateCmd.Flags().Set("preserve-state", "")
	err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "create",
		"--cluster", "my-cluster", "--action", "start", "--cron", "0 7 * * 1-5", "--preserve-state=true")
	if err == nil || !strings.Contains(err.Error(), "only apply to stop schedules") {
		t.Fatalf("a start schedule must refuse the state flags, got %v", err)
	}
}

// TestClusterPowerSchedulesHelp_SaysPauseSavesNoComputeOnFullBillingProviders
// pins the pause billing note (ankra-u3jsj.27): Hetzner, DigitalOcean and
// UpCloud bill a powered-off server in full, so the --stop-mode help on both
// create and update, and the command's long help, must say a pause saves no
// compute cost there and name the modes that do.
func TestClusterPowerSchedulesHelp_SaysPauseSavesNoComputeOnFullBillingProviders(t *testing.T) {
	for _, command := range []struct {
		name  string
		usage string
	}{
		{"create", clusterPowerSchedulesCreateCmd.Flags().Lookup("stop-mode").Usage},
		{"update", clusterPowerSchedulesUpdateCmd.Flags().Lookup("stop-mode").Usage},
		{"power-schedules", clusterPowerSchedulesCmd.Long},
	} {
		for _, want := range []string{"saves no compute cost", "Hetzner, DigitalOcean or UpCloud", "scale_to_zero or delete_resources"} {
			if !strings.Contains(command.usage, want) {
				t.Errorf("%s help does not say %q: %q", command.name, want, command.usage)
			}
		}
	}
}

func presentNodeLocal(names ...string) *client.PowerScheduleNodeLocalStorage {
	count := len(names)
	return &client.PowerScheduleNodeLocalStorage{State: "present", PVCCount: &count, PVCNames: names, ConsentRequired: true}
}

// answerPromptInteractively makes the acknowledgement prompt treat the test's
// stdin as a terminal.
func answerPromptInteractively(t *testing.T) {
	t.Helper()
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return true }
	t.Cleanup(func() { promptIsInteractive = previous })
}

var scaleToZeroCreateArgs = []string{"cluster", "power-schedules", "create", "--cluster", "my-cluster",
	"--action", "stop", "--cron", "0 19 * * 1-5", "--stop-mode", "scale_to_zero"}

// TestClusterPowerSchedulesCreate_ScaleToZeroOnNodeLocalDataNeedsTheFlag pins
// the non-interactive refusal (ankra-u3jsj.30): a scale_to_zero stop deletes
// the worker servers, so on a cluster whose volumes keep data on worker
// disks the command says so, names the volumes, and fails without writing
// unless --accept-node-local-data-loss is passed.
func TestClusterPowerSchedulesCreate_ScaleToZeroOnNodeLocalDataNeedsTheFlag(t *testing.T) {
	mock := &powerScheduleMock{nodeLocal: presentNodeLocal("apps/cache", "db/pg")}
	setMockClient(t, mock)

	stderr, err := executePowerScheduleCommandCapturingStderr(t, "", scaleToZeroCreateArgs...)
	if err == nil || !strings.Contains(err.Error(), "apps/cache, db/pg") ||
		!strings.Contains(err.Error(), "--accept-node-local-data-loss") {
		t.Fatalf("create without the flag = %v, want a refusal naming the volumes and the flag", err)
	}
	if exitCodeFor(err) != exitUsage {
		t.Fatalf("exit code = %d, want %d", exitCodeFor(err), exitUsage)
	}
	if len(mock.creates) != 0 {
		t.Fatalf("nothing may be written without the acknowledgement, got %+v", mock.creates)
	}
	for _, want := range []string{"deletes the worker servers", "Data on the workers' own disks is lost", "apps/cache, db/pg"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not say %q: %q", want, stderr)
		}
	}

	if err := executePowerScheduleCommand(t, "", append(scaleToZeroCreateArgs, "--accept-node-local-data-loss")...); err != nil {
		t.Fatalf("create with the flag: %v", err)
	}
	if len(mock.creates) != 1 || !mock.creates[0].Request.AcceptNodeLocalDataLoss {
		t.Fatalf("the acknowledgement must reach the backend: %+v", mock.creates)
	}
}

// TestClusterPowerSchedulesCreate_ScaleToZeroPromptsOnATerminal pins the
// interactive path: a yes sends the acknowledgement, anything else cancels
// without writing.
func TestClusterPowerSchedulesCreate_ScaleToZeroPromptsOnATerminal(t *testing.T) {
	mock := &powerScheduleMock{nodeLocal: presentNodeLocal("apps/cache")}
	setMockClient(t, mock)
	answerPromptInteractively(t)

	if err := executePowerScheduleCommand(t, "n\n", scaleToZeroCreateArgs...); !errors.Is(err, errCancelled) {
		t.Fatalf("declined prompt = %v, want errCancelled", err)
	}
	if len(mock.creates) != 0 {
		t.Fatalf("a declined prompt must not write: %+v", mock.creates)
	}
	if err := executePowerScheduleCommand(t, "y\n", scaleToZeroCreateArgs...); err != nil {
		t.Fatalf("accepted prompt: %v", err)
	}
	if len(mock.creates) != 1 || !mock.creates[0].Request.AcceptNodeLocalDataLoss {
		t.Fatalf("an accepted prompt must send the acknowledgement: %+v", mock.creates)
	}
}

// TestClusterPowerSchedulesCreate_ScaleToZeroWithoutNodeLocalData covers the
// states that need no acknowledgement: none writes without it, and a
// reading that could not be made warns and writes, like the backend.
func TestClusterPowerSchedulesCreate_ScaleToZeroWithoutNodeLocalData(t *testing.T) {
	zero := 0
	for name, mock := range map[string]*powerScheduleMock{
		"none":       {nodeLocal: &client.PowerScheduleNodeLocalStorage{State: "none", PVCCount: &zero, PVCNames: []string{}}},
		"unknown":    {nodeLocal: &client.PowerScheduleNodeLocalStorage{State: "unknown"}},
		"read error": {nodeLocalError: errors.New("404 Not Found")},
	} {
		t.Run(name, func(t *testing.T) {
			setMockClient(t, mock)
			stderr, err := executePowerScheduleCommandCapturingStderr(t, "", scaleToZeroCreateArgs...)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if len(mock.creates) != 1 || mock.creates[0].Request.AcceptNodeLocalDataLoss {
				t.Fatalf("expected one create without the acknowledgement: %+v", mock.creates)
			}
			couldNotCheck := strings.Contains(stderr, "could not check")
			if couldNotCheck != (name != "none") {
				t.Fatalf("could-not-check warning = %t for %s: %q", couldNotCheck, name, stderr)
			}
		})
	}
}

// TestClusterPowerSchedulesNodeLocalCheck_OnlyForAnEnabledScaleToZeroStop
// keeps the read off every other schedule, and on update applies it to a
// scale_to_zero mode carried over from the schedule as it is now.
func TestClusterPowerSchedulesNodeLocalCheck_OnlyForAnEnabledScaleToZeroStop(t *testing.T) {
	mock := &powerScheduleMock{nodeLocal: presentNodeLocal("apps/cache")}
	setMockClient(t, mock)

	for _, args := range [][]string{
		{"cluster", "power-schedules", "create", "--cluster", "my-cluster", "--action", "stop", "--cron", "0 19 * * 1-5", "--stop-mode", "delete_resources"},
		{"cluster", "power-schedules", "create", "--cluster", "my-cluster", "--action", "start", "--cron", "0 7 * * 1-5"},
		append(append([]string{}, scaleToZeroCreateArgs...), "--enabled=false"),
	} {
		if err := executePowerScheduleCommand(t, "", args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if mock.nodeLocalReads != 0 {
		t.Fatalf("only an enabled scale_to_zero stop reads the node-local volumes, got %d reads", mock.nodeLocalReads)
	}

	err := executePowerScheduleCommand(t, "", "cluster", "power-schedules", "update", "sched-1",
		"--cluster", "my-cluster", "--action", "stop", "--cron", "0 20 * * 1-5", "--timezone", "UTC", "--stop-mode", "scale_to_zero")
	if err == nil || !strings.Contains(err.Error(), "--accept-node-local-data-loss") || len(mock.updates) != 0 {
		t.Fatalf("update to scale_to_zero without the flag = %v (updates %d)", err, len(mock.updates))
	}
}
