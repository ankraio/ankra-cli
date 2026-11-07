package systemtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// flowEnvironment holds every setting flow_regression.sh requires, so a test changes only the one it is about.
func flowEnvironment(directory string) map[string]string {
	return map[string]string{
		"PATH":                       directory + ":" + os.Getenv("PATH"),
		"HOME":                       directory,
		"TMPDIR":                     directory,
		"ANKRA_BIN":                  filepath.Join(directory, "ankra"),
		"FLOW_REGRESSION_CONFIRM":    "yes",
		"ANKRA_BASE_URL":             "https://platform.ankra.dev",
		"ANKRA_API_TOKEN":            "token",
		"FLOW_ORGANISATION_ID":       "org",
		"FLOW_CREDENTIAL_ID":         "credential",
		"FLOW_SSH_KEY_CREDENTIAL_ID": "ssh",
		"FLOW_ZONE":                  "zone",
		"FLOW_PLAN":                  "plan",
		"FLOW_FIXTURE_REPOSITORY":    "ankra-demos/flow-regression-fixture",
		"GH_TOKEN":                   "token",
		"FLOW_ALERT_DESTINATION_ID":  "destination",
		"FLOW_SLACK_CHANNEL_ID":      "channel",
		"SLACK_BOT_TOKEN":            "token",
		"FLOW_RUN_ID":                "test",
		"FLOW_POLL_SECONDS":          "0",
		"FLOW_LEAK_WAIT_SECONDS":     "0",
	}
}

// writeFake installs an executable script of the given name. Every call it receives is appended to calls.log.
func writeFake(t *testing.T, directory, name, body string) {
	t.Helper()
	script := fmt.Sprintf("#!/usr/bin/env bash\necho \"%s $*\" >> %q\n%s\n", name, filepath.Join(directory, "calls.log"), body)
	if writeError := os.WriteFile(filepath.Join(directory, name), []byte(script), 0o755); writeError != nil {
		t.Fatal(writeError)
	}
}

func runFlow(t *testing.T, environment map[string]string) (string, int) {
	t.Helper()
	bash, lookupError := exec.LookPath("bash")
	if lookupError != nil {
		t.Skip("bash not available")
	}
	command := exec.Command(bash, "flow_regression.sh")
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	output, runError := command.CombinedOutput()
	if runError == nil {
		return string(output), 0
	}
	exitError, isExit := runError.(*exec.ExitError)
	if !isExit {
		t.Fatal(runError)
	}
	return string(output), exitError.ExitCode()
}

// fakeTools puts a harmless ankra, gh, git and curl on PATH; a test replaces the one it is about.
func fakeTools(t *testing.T, directory string) {
	t.Helper()
	writeFake(t, directory, "ankra", "exit 0")
	writeFake(t, directory, "gh", "exit 0")
	writeFake(t, directory, "git", "exit 0")
	writeFake(t, directory, "curl", "exit 0")
	if _, jqError := exec.LookPath("jq"); jqError != nil {
		writeFake(t, directory, "jq", "exit 0")
	}
}

// requireJq skips a test that reads real JSON, where a fake jq would prove nothing. The refusals need no jq.
func requireJq(t *testing.T) {
	t.Helper()
	if _, jqError := exec.LookPath("jq"); jqError != nil {
		t.Skip("jq not available")
	}
}

func readCalls(t *testing.T, directory string) string {
	t.Helper()
	calls, readError := os.ReadFile(filepath.Join(directory, "calls.log"))
	if readError != nil {
		if os.IsNotExist(readError) {
			return ""
		}
		t.Fatal(readError)
	}
	return string(calls)
}

func TestFlowRegressionRefusesProductionBeforeTouchingAnything(t *testing.T) {
	for _, address := range []string{"https://platform.ankra.app", "https://platform.ankra.app/", "https://platform.ankra.ai"} {
		directory := t.TempDir()
		fakeTools(t, directory)
		environment := flowEnvironment(directory)
		environment["ANKRA_BASE_URL"] = address
		output, exitCode := runFlow(t, environment)
		if exitCode != 2 || !strings.Contains(output, "is production") {
			t.Fatalf("%s: exit %d, output %s", address, exitCode, output)
		}
		if calls := readCalls(t, directory); calls != "" {
			t.Fatalf("%s: called %s", address, calls)
		}
	}
}

func TestFlowRegressionRefusesAMissingPlatformAndConfirmation(t *testing.T) {
	directory := t.TempDir()
	fakeTools(t, directory)
	environment := flowEnvironment(directory)
	delete(environment, "ANKRA_BASE_URL")
	delete(environment, "FLOW_REGRESSION_CONFIRM")
	output, exitCode := runFlow(t, environment)
	if exitCode != 2 || !strings.Contains(output, "ANKRA_BASE_URL is required") || !strings.Contains(output, "FLOW_REGRESSION_CONFIRM=yes") {
		t.Fatalf("exit %d, output %s", exitCode, output)
	}
}

// The run force-pushes to the fixture, so a repository that is not plainly a fixture is refused.
func TestFlowRegressionRefusesARepositoryThatIsNotAFixture(t *testing.T) {
	directory := t.TempDir()
	fakeTools(t, directory)
	environment := flowEnvironment(directory)
	environment["FLOW_FIXTURE_REPOSITORY"] = "ankraio/cluster"
	output, exitCode := runFlow(t, environment)
	if exitCode != 2 || !strings.Contains(output, "must end in -fixture") {
		t.Fatalf("exit %d, output %s", exitCode, output)
	}
	if calls := readCalls(t, directory); strings.Contains(calls, "git ") {
		t.Fatalf("touched git: %s", calls)
	}
}

func TestFlowRegressionPlanOnlyCreatesNothing(t *testing.T) {
	directory := t.TempDir()
	fakeTools(t, directory)
	environment := flowEnvironment(directory)
	environment["FLOW_PLAN_ONLY"] = "1"
	output, exitCode := runFlow(t, environment)
	if exitCode != 0 || !strings.Contains(output, "plan only") {
		t.Fatalf("exit %d, output %s", exitCode, output)
	}
	if calls := readCalls(t, directory); calls != "" {
		t.Fatalf("called %s", calls)
	}
}

// A run that dies after creating a cluster still deprovisions it, and fails.
func TestFlowRegressionTearsDownAfterAFailure(t *testing.T) {
	requireJq(t)
	directory := t.TempDir()
	fakeTools(t, directory)
	writeFake(t, directory, "ankra", `case "$*" in
  *"ankracloud create --name flow-staging-test"*) echo '{"cluster_id":"staging-id"}' ;;
  *"ankracloud create"*) exit 1 ;;
  "cluster list -o json") echo '{"result":[]}' ;;
  "application list -o json") echo '[]' ;;
esac
exit 0`)
	output, exitCode := runFlow(t, flowEnvironment(directory))
	if exitCode != 1 || !strings.Contains(output, "RESULT: FAILED") {
		t.Fatalf("exit %d, output %s", exitCode, output)
	}
	calls := readCalls(t, directory)
	if !strings.Contains(calls, "ankra cluster deprovision staging-id --yes --accept-volume-data-loss") {
		t.Fatalf("staging was not deprovisioned: %s", calls)
	}
}

// A leak fails the run and says what leaked, even with nothing else wrong.
func TestFlowRegressionFailsOnALeak(t *testing.T) {
	requireJq(t)
	directory := t.TempDir()
	fakeTools(t, directory)
	writeFake(t, directory, "ankra", `case "$*" in
  *"ankracloud create"*) exit 1 ;;
  "cluster list -o json") echo '[{"name":"flow-staging-test","id":"staging-id"}]' ;;
  "application list -o json") echo '[]' ;;
esac
exit 0`)
	output, exitCode := runFlow(t, flowEnvironment(directory))
	if exitCode != 1 || !strings.Contains(output, "LEAK") || !strings.Contains(output, "cluster flow-staging-test") {
		t.Fatalf("exit %d, output %s", exitCode, output)
	}
}

// Another run's resources are not this run's leak.
func TestFlowRegressionLeakCheckReadsOnlyItsOwnRun(t *testing.T) {
	requireJq(t)
	directory := t.TempDir()
	fakeTools(t, directory)
	writeFake(t, directory, "ankra", `case "$*" in
  *"ankracloud create"*) exit 1 ;;
  "cluster list -o json") echo '[{"name":"flow-staging-other","id":"other"},{"name":"staging","id":"keep"}]' ;;
  "application list -o json") echo '[]' ;;
esac
exit 0`)
	output, _ := runFlow(t, flowEnvironment(directory))
	if strings.Contains(output, "LEAK") {
		t.Fatalf("read another run's cluster as a leak: %s", output)
	}
	if !strings.Contains(output, "nothing left behind by run test") {
		t.Fatalf("output %s", output)
	}
}

// A list that cannot be read proves nothing, so the run must not say nothing leaked.
func TestFlowRegressionAnUnreadableListIsNotClean(t *testing.T) {
	requireJq(t)
	directory := t.TempDir()
	fakeTools(t, directory)
	writeFake(t, directory, "ankra", `case "$*" in
  *"ankracloud create"*) exit 1 ;;
  "cluster list -o json") exit 1 ;;
  "application list -o json") echo '[]' ;;
esac
exit 0`)
	output, exitCode := runFlow(t, flowEnvironment(directory))
	if exitCode != 1 || !strings.Contains(output, "LEAK") || !strings.Contains(output, "clusters: could not read the list") {
		t.Fatalf("exit %d, output %s", exitCode, output)
	}
}

// A create that failed on its way back may still have made the cluster: teardown finds it by name.
func TestFlowRegressionTearsDownAClusterItOnlyKnowsByName(t *testing.T) {
	requireJq(t)
	directory := t.TempDir()
	fakeTools(t, directory)
	writeFake(t, directory, "ankra", `case "$*" in
  *"ankracloud create"*) exit 1 ;;
  "cluster list -o json") echo '{"result":[{"name":"flow-staging-test","id":"orphan-id"}]}' ;;
  "application list -o json") echo '[]' ;;
esac
exit 0`)
	runFlow(t, flowEnvironment(directory))
	if calls := readCalls(t, directory); !strings.Contains(calls, "ankra cluster deprovision orphan-id --yes") {
		t.Fatalf("orphan was not deprovisioned: %s", calls)
	}
}
