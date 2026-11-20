package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"ankra/internal/client"

	"gopkg.in/yaml.v3"
)

func applicationBackupCount(value int) *int { return &value }

// knownApplicationBackupKeeping is a deployment whose policy keeps 28 days
// and which keeps eleven restore points now.
func knownApplicationBackupKeeping() *client.ApplicationBackupKeeping {
	return &client.ApplicationBackupKeeping{
		State:                 "known",
		Policy:                &client.BackupRetention{Daily: 7, Weekly: 4},
		KeepsDays:             applicationBackupCount(28),
		OldestKeptAt:          applicationBackupText("2026-10-22T02:17:00Z"),
		KeptCount:             applicationBackupCount(11),
		UnattributedKeptCount: applicationBackupCount(0),
		ProtectedSince:        applicationBackupText("2026-03-04T02:17:00Z"),
		ProtectedSinceState:   "known",
		LastRestoreTestState:  "none",
	}
}

func renderApplicationBackupsWithKeeping(t *testing.T, keeping *client.ApplicationBackupKeeping) string {
	t.Helper()
	backups := sampleApplicationBackups()
	backups.Deployments[0].Keeping = keeping
	mock := &applicationBackupsMock{backups: backups}
	output, executeError := runBackupCommand(t, mock, "",
		applicationBackupCommands(t), "application", "backups", applicationBackupsAppID)
	if executeError != nil {
		t.Fatalf("reading the application's backups: %v", executeError)
	}
	return stripANSICodes(output)
}

// requireKeepingLines fails the test unless the backups output holds each of
// lines as a whole line. Its name is the keeping tests' own, so no other
// test file of the package can declare it too.
func requireKeepingLines(t *testing.T, output string, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(output, line+"\n") {
			t.Errorf("expected the line %q, got:\n%s", line, output)
		}
	}
}

func TestApplicationBackupsSaysWhatTheBackupsKeep(t *testing.T) {
	output := renderApplicationBackupsWithKeeping(t, knownApplicationBackupKeeping())

	requireKeepingLines(t, output,
		"    Since:       2026-03-04T02:17:00Z",
		"    Kept for:    28 days (7 daily, 4 weekly)",
		"    Kept now:    11 backups",
		"    Oldest kept: 2026-10-22T02:17:00Z",
	)
}

func TestApplicationBackupsSaysAPolicyThatKeepsEverything(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.KeepsDays = nil
	keeping.KeepsEverything = true
	keeping.Policy = &client.BackupRetention{}

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output, "    Kept for:    everything (no backup is expired)")
}

func TestApplicationBackupsSaysAPolicyShorterThanADay(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.KeepsDays = nil
	keeping.Policy = &client.BackupRetention{Hourly: 6}

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output, "    Kept for:    less than a day (6 hourly)")
}

func TestApplicationBackupsSaysAPolicyThatKeepsANumberOfBackups(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.KeepsDays = nil
	keeping.Policy = &client.BackupRetention{MinimumCount: 5}

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output, "    Kept for:    a number of backups rather than days (never below 5)")
}

// A policy that names nothing, beside keeps_everything false and no days,
// says the platform did not say for how long: not "less than a day", and
// not "the platform default".
func TestApplicationBackupsSaysAPolicyThatNamesNoPeriod(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.KeepsDays = nil
	keeping.Policy = &client.BackupRetention{}

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output, "    Kept for:    the platform did not say for how long")
	for _, wrong := range []string{"less than a day", "rather than days", "the platform default"} {
		if strings.Contains(output, wrong) {
			t.Errorf("a policy that names no period was rendered with %q:\n%s", wrong, output)
		}
	}
}

func TestApplicationBackupsSaysAStackWithNoBackupPolicy(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.State = "no_policy"
	keeping.Policy = nil
	keeping.KeepsDays = nil

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output, "    Kept for:    no backup policy, so nothing is kept on a schedule")
}

// An unread block says so on every line. None of its null fields is printed
// as none or zero.
func TestApplicationBackupsNeverCallsAnUnreadKeepingBlockNone(t *testing.T) {
	output := renderApplicationBackupsWithKeeping(t, &client.ApplicationBackupKeeping{
		State: "unknown", ProtectedSinceState: "unknown", LastRestoreTestState: "unknown",
	})

	requireKeepingLines(t, output,
		"    Since:       could not be read",
		"    Kept for:    could not be read",
		"    Kept now:    could not be read",
	)
	for _, wrong := range []string{"Kept now:    none", "Kept now:    0", "Oldest kept:"} {
		if strings.Contains(output, wrong) {
			t.Errorf("an unread block was rendered as %q:\n%s", wrong, output)
		}
	}
}

func TestApplicationBackupsSaysAStackThatKeepsNoBackupYet(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.KeptCount = applicationBackupCount(0)
	keeping.OldestKeptAt = nil
	keeping.ProtectedSince = nil
	keeping.ProtectedSinceState = "none"

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output,
		"    Since:       no backup has completed yet",
		"    Kept now:    none",
	)
	if strings.Contains(output, "Oldest kept:") {
		t.Errorf("a stack that keeps no backup has no oldest one:\n%s", output)
	}
}

// protected_since_state unknown is "cannot be told", not "never".
func TestApplicationBackupsKeepsAnUntellableStartApartFromNever(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.ProtectedSince = nil
	keeping.ProtectedSinceState = "unknown"

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output,
		"    Since:       cannot be told (an older backup does not record which stack it was taken from)")
	if strings.Contains(output, "no backup has completed yet") {
		t.Errorf("an untellable start was rendered as never:\n%s", output)
	}
}

// A protected_since_state this version of the CLI does not know, or none at
// all, cannot be told, and gets no cause: the cause belongs to unknown.
func TestApplicationBackupsGivesNoCauseForAStartStateItDoesNotKnow(t *testing.T) {
	for _, state := range []string{"", "estimated"} {
		keeping := knownApplicationBackupKeeping()
		keeping.ProtectedSince = nil
		keeping.ProtectedSinceState = state

		output := renderApplicationBackupsWithKeeping(t, keeping)

		requireKeepingLines(t, output, "    Since:       cannot be told")
		if strings.Contains(output, "does not record which stack") {
			t.Errorf("protected_since_state %q was given the cause of unknown:\n%s", state, output)
		}
	}
}

// A keeping state this version of the CLI does not know was read: it is not
// "could not be read", and the line names the value.
func TestApplicationBackupsNamesAKeepingStateItDoesNotKnow(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.State = "partial"

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output,
		`    Keeping:     the platform answered a keeping state this version of the CLI does not know ("partial"); -o json shows it.`)
	for _, wrong := range []string{"could not be read", "Since:", "Kept for:", "Kept now:"} {
		if strings.Contains(output, wrong) {
			t.Errorf("a keeping state the CLI does not know was rendered with %q:\n%s", wrong, output)
		}
	}
}

func TestApplicationBackupsNamesBackupsThatDoNotRecordTheirStack(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.KeptCount = applicationBackupCount(1)
	keeping.UnattributedKeptCount = applicationBackupCount(2)

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output,
		"    Kept now:    1 backup, and 2 more with this stack's name that do not record which stack they came from")
}

// No backup of the stack's own beside some that carry its name says so in
// one sentence, not "none, and 2 more".
func TestApplicationBackupsSaysNoOwnBackupBesideOnesThatDoNotRecordTheirStack(t *testing.T) {
	keeping := knownApplicationBackupKeeping()
	keeping.KeptCount = applicationBackupCount(0)
	keeping.OldestKeptAt = nil
	keeping.UnattributedKeptCount = applicationBackupCount(2)

	output := renderApplicationBackupsWithKeeping(t, keeping)

	requireKeepingLines(t, output,
		"    Kept now:    no backup recorded as this stack's own, and 2 with this stack's name that do not record which stack they came from")
	if strings.Contains(output, "none, and") || strings.Contains(output, "2 more") {
		t.Errorf("no own backup beside unattributed ones contradicts itself:\n%s", output)
	}
}

// A deployment with no keeping block prints exactly what the section printed
// before the block existed.
func TestApplicationBackupsWithoutAKeepingBlockPrintsAsBefore(t *testing.T) {
	output := renderApplicationBackupsWithKeeping(t, nil)

	for _, added := range []string{"Since:", "Kept for:", "Kept now:", "Oldest kept:"} {
		if strings.Contains(output, added) {
			t.Errorf("a deployment with no keeping block printed %q:\n%s", added, output)
		}
	}
	requireKeepingLines(t, output,
		"    Last backup: 2026-09-14T02:17:00Z",
		"    Next backup: 2026-09-16T02:17:00Z",
	)
}

// applicationBackupsDocument is the platform's answer with one keeping block
// per deployment as given, null fields and a field this CLI does not declare
// included.
const applicationBackupsDocument = `{"application_id":"` + applicationBackupsAppID + `",` +
	`"application_name":"shop","database":{"status":"absent"},"database_backup":true,` +
	`"deployments":[{"deployment_id":"` + applicationBackupsClusterID + `","cluster_id":"` +
	applicationBackupsClusterID + `","cluster_name":"production","stack_name":"deploy-shop",` +
	`"namespace":"shop","has_database":false,"has_database_known":true,"posture":null,` +
	`"keeping":{"state":"known","policy":{"hourly":0,"daily":7,"weekly":4,"monthly":0,"yearly":0,` +
	`"minimum_count":0},"keeps_days":28,"keeps_everything":false,"oldest_kept_at":"2026-10-22T02:17:00Z",` +
	`"kept_count":11,"unattributed_kept_count":0,"protected_since":"2026-03-04T02:17:00Z",` +
	`"protected_since_state":"known","last_restore_test":{"tested_at":"2026-11-02T09:00:00Z",` +
	`"state":"passed","took_seconds":null,"bytes_back":null},"last_restore_test_state":"known",` +
	`"zz_added_by_a_newer_platform":3}}],"ready_vault_count":1,"ready_vault_known":true,"warnings":[]}`

// newApplicationBackupsServer answers the backups read with document through
// a real client, so the body travels the way it does against the platform.
func newApplicationBackupsServer(t *testing.T, document []byte) *client.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/applications/"+applicationBackupsAppID+"/backups" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(document)
	}))
	t.Cleanup(server.Close)
	return client.New("test-token", server.URL)
}

// runApplicationBackupsAgainst runs `application backups -o <format>` and
// returns stdout alone, which is what a script parses.
func runApplicationBackupsAgainst(t *testing.T, apiClient APIClient, format string) string {
	t.Helper()
	withTempHome(t)
	setMockClient(t, apiClient)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"application", "backups", applicationBackupsAppID, "-o", format})
	resets := applicationBackupCommands(t)
	resetTreeFlags(t, resets...)
	t.Cleanup(func() { resetTreeFlags(t, resets...) })
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("application backups -o %s: %v", format, executeError)
	}
	return stdout.String()
}

// -o json is the platform's document: the keeping block as sent, its nulls
// kept as null and a field the CLI does not know passed through.
func TestApplicationBackupsJSONPassesTheKeepingBlockThrough(t *testing.T) {
	output := runApplicationBackupsAgainst(t,
		newApplicationBackupsServer(t, []byte(applicationBackupsDocument)), "json")

	got := decodeJSONDocument(t, []byte(output))
	if want := decodeJSONDocument(t, []byte(applicationBackupsDocument)); !reflect.DeepEqual(want, got) {
		t.Fatalf("-o json is not the platform's document:\nwant %s\ngot  %s", applicationBackupsDocument, output)
	}
	keeping := got["deployments"].([]any)[0].(map[string]any)["keeping"].(map[string]any)
	test := keeping["last_restore_test"].(map[string]any)
	if value, present := test["took_seconds"]; !present || value != nil {
		t.Fatalf("took_seconds was sent as null and must stay null, got %v (present %v)", value, present)
	}
	if _, present := keeping["zz_added_by_a_newer_platform"]; !present {
		t.Fatalf("a keeping field the CLI does not declare was dropped:\n%s", output)
	}
}

func TestApplicationBackupsYAMLPassesTheKeepingBlockThrough(t *testing.T) {
	output := runApplicationBackupsAgainst(t,
		newApplicationBackupsServer(t, []byte(applicationBackupsDocument)), "yaml")

	decoded := map[string]any{}
	if decodeError := yaml.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("-o yaml is not YAML: %v\n%s", decodeError, output)
	}
	deployment := decoded["deployments"].([]any)[0].(map[string]any)
	keeping, isMap := deployment["keeping"].(map[string]any)
	if !isMap {
		t.Fatalf("-o yaml has no keeping block:\n%s", output)
	}
	if keeping["kept_count"] != 11 || keeping["keeps_days"] != 28 || keeping["state"] != "known" {
		t.Fatalf("the keeping block's values did not survive -o yaml:\n%s", output)
	}
	test := keeping["last_restore_test"].(map[string]any)
	if value, present := test["took_seconds"]; !present || value != nil {
		t.Fatalf("took_seconds was sent as null and must stay null in -o yaml:\n%s", output)
	}
	if _, present := keeping["zz_added_by_a_newer_platform"]; !present {
		t.Fatalf("a keeping field the CLI does not declare was dropped from -o yaml:\n%s", output)
	}
}

// A platform that sends no keeping block gets exactly the -o json and -o yaml
// it got before: the document matches what encoding the struct printed, byte
// for byte, and no keeping key is invented.
func TestApplicationBackupsStructuredOutputWithoutKeepingIsUnchanged(t *testing.T) {
	document, encodeError := json.Marshal(sampleApplicationBackups())
	if encodeError != nil {
		t.Fatalf("encoding the fixture: %v", encodeError)
	}
	if bytes.Contains(document, []byte(`"keeping"`)) {
		t.Fatalf("the fixture of an older platform carries a keeping key:\n%s", document)
	}
	for _, format := range []outputFormat{outputJSON, outputYAML} {
		output := runApplicationBackupsAgainst(t, newApplicationBackupsServer(t, document), string(format))

		before := new(bytes.Buffer)
		if previousError := encodeStructured(before, format, sampleApplicationBackups()); previousError != nil {
			t.Fatalf("encoding the struct as before: %v", previousError)
		}
		if output != before.String() {
			t.Fatalf("-o %s changed for an answer with no keeping block:\nbefore %s\nnow    %s",
				format, before.String(), output)
		}
		if strings.Contains(output, "keeping") {
			t.Fatalf("-o %s invented a keeping key:\n%s", format, output)
		}
	}
}
