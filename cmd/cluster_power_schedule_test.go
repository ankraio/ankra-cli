package cmd

import (
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
	resetPowerScheduleFlags()
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetPowerScheduleFlags()
	})
	rootCmd.SetOut(new(strings.Builder))
	rootCmd.SetErr(new(strings.Builder))
	rootCmd.SetIn(strings.NewReader(stdinContent))
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
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
