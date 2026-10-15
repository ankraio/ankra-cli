package systemtest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The autoscaling step's CA-identity assertion is the only systemtest
// assertion covering cluster#3557's identity contract. Until
// ankra-t5jf5.34.8.3.2 it could end as a SKIP - on the first failed log read,
// or on a log without two main-loop markers - and a SKIP never touched the
// exit code, so a run that never made the assertion read the same as one in
// which it passed. These tests drive the script's own functions with the
// CLI, the pod lookup and jq stubbed out.

// shellDefinitions pulls the named functions out of lifecycle_systemtest.sh,
// one-liners (`pass() { ...; }`) and multi-line ones alike. A name the script
// does not define is reported as missing rather than failing here, so a test
// can say which behaviour is absent.
func shellDefinitions(t *testing.T, names ...string) (string, []string) {
	t.Helper()
	script, err := os.ReadFile("lifecycle_systemtest.sh")
	if err != nil {
		t.Fatal(err)
	}
	var definitions []string
	var missing []string
	for _, name := range names {
		quoted := regexp.QuoteMeta(name)
		oneLiner := regexp.MustCompile(`(?m)^` + quoted + `\(\)[ \t]*\{[^\n]*\}[ \t]*$`)
		multiLine := regexp.MustCompile(`(?ms)^` + quoted + `\(\) \{\n.*?^\}`)
		if found := oneLiner.Find(script); found != nil {
			definitions = append(definitions, string(found))
		} else if found := multiLine.Find(script); found != nil {
			definitions = append(definitions, string(found))
		} else {
			missing = append(missing, name)
		}
	}
	return strings.Join(definitions, "\n"), missing
}

// The stubs: one Running autoscaler pod; `ankra cluster logs` failing for
// the first STUB_LOG_FAILURES reads and then printing $STUB_DIR/log; jq
// answering the group's nodes without a providerID with $STUB_NOID. The
// grace window is 1s polled every 0.2s, so a retried read has room.
const identityPrelude = `set -u -o pipefail
NL=$'\n'
LANE_SKIP_RESULTS=""
FAILURES=0
AS_CA_NAME=cluster-autoscaler
AS_CA_NAMESPACE=ankra
AS_LOG_WINDOW=60
AS_JQ_LIB=''
POLL_INTERVAL=0.2
AUTOSCALING_IDENTITY_GRACE=1
lane_headline() { echo "All 1 provider lanes configured"; }
as_ca_pod() { echo cluster-autoscaler-abc; }
as_nodes_json() { echo '{}'; }
jq() { cat >/dev/null; printf '%s\n' "$STUB_NOID"; }
ank_rc() {
  local reads
  reads=$(cat "$STUB_DIR/reads" 2>/dev/null || echo 0)
  reads=$((reads + 1))
  echo "$reads" > "$STUB_DIR/reads"
  if [ "$reads" -le "$STUB_LOG_FAILURES" ]; then
    echo "Error: forbidden: this token cannot read pod logs"
    return 1
  fi
  cat "$STUB_DIR/log"
}
`

const (
	loopMarker = "I1015 10:00:00.000000       1 static_autoscaler.go:270] Starting main loop\n"
	quietLoop  = "I1015 10:00:01.000000       1 filter_out_schedulable.go:66] Filtering out schedulables\n"
)

type scriptRun struct {
	output      string
	exitCode    int
	results     []string
	jobSummary  string
	readsMade   string
	missingDefs []string
}

func runIdentityScript(t *testing.T, body, log, noid string, logFailures string, names ...string) scriptRun {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	definitions, missing := shellDefinitions(t, names...)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	summaryFile := filepath.Join(dir, "step_summary")
	if err := os.WriteFile(summaryFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bash, "-c", identityPrelude+definitions+"\n"+body)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"STUB_DIR=" + dir,
		"STUB_NOID=" + noid,
		"STUB_LOG_FAILURES=" + logFailures,
		"RESULT_FILE=" + filepath.Join(dir, "results"),
		"GITHUB_STEP_SUMMARY=" + summaryFile,
	}
	out, err := command.CombinedOutput()
	run := scriptRun{output: string(out), missingDefs: missing}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		run.exitCode = exitErr.ExitCode()
	default:
		t.Fatalf("running the script functions: %v", err)
	}
	if results, err := os.ReadFile(filepath.Join(dir, "results")); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(results)), "\n") {
			if line != "" {
				run.results = append(run.results, line)
			}
		}
	}
	summary, _ := os.ReadFile(summaryFile)
	run.jobSummary = string(summary)
	reads, _ := os.ReadFile(filepath.Join(dir, "reads"))
	run.readsMade = strings.TrimSpace(string(reads))
	return run
}

var identityFunctions = []string{"log", "record", "pass", "fail", "skip", "unasserted", "as_assert_ca_identity"}

func TestCAIdentityAssertionIsNeverSilentlySkipped(t *testing.T) {
	cleanLoops := loopMarker + quietLoop + loopMarker + quietLoop + loopMarker
	cases := []struct {
		name        string
		log         string
		noid        string
		logFailures string
		wantPrefix  string
		wantText    []string
		wantRetry   bool
	}{
		{
			// The one-shot read: a single transient refusal used to skip
			// the whole assertion with return 0.
			name: "a log read that fails once is retried inside the grace window",
			log:  cleanLoops, logFailures: "1",
			wantPrefix: "PASS  ", wantText: []string{"autoscaler registers every worker (newest loop: none unregistered)"},
			wantRetry: true,
		},
		{
			name: "a log that stays unreadable for the whole window is NOT ASSERTED, not skipped",
			log:  cleanLoops, logFailures: "1000",
			wantPrefix: "UNASSERTED  ",
			wantText:   []string{"log stayed unreadable for 1s", "ankra cluster logs exit 1", "this token cannot read pod logs"},
		},
		{
			// Fewer than two markers bound no loop, and the absence of a
			// count proves nothing without one.
			name: "a log without two main-loop markers is NOT ASSERTED, not skipped",
			log:  loopMarker + quietLoop, logFailures: "0",
			wantPrefix: "UNASSERTED  ",
			wantText:   []string{"fewer than two 'Starting main loop' markers", "chart's default v4"},
		},
		{
			name: "a count past the tolerance still fails without loop markers",
			log:  quietLoop + "W1015 10:00:02.000000       1 static_autoscaler.go:405] 2 unregistered nodes present\n", logFailures: "0",
			wantPrefix: "FAIL  ", wantText: []string{"reports 2 unregistered nodes with 0 node(s) lacking a providerID"},
		},
		{
			name: "a worker the newest loop reports unregistered fails",
			log:  loopMarker + quietLoop + loopMarker + "I1015 10:00:11.000000       1 static_autoscaler.go:405] 1 unregistered nodes present\n" + loopMarker,
			noid: "", logFailures: "0",
			wantPrefix: "FAIL  ", wantText: []string{"still reports registered workers as unregistered"},
		},
		{
			name: "nodes without a providerID are the tolerated fallback",
			log:  loopMarker + quietLoop + loopMarker + "I1015 10:00:11.000000       1 static_autoscaler.go:405] 1 unregistered nodes present\n" + loopMarker,
			noid: "worker-a", logFailures: "0",
			wantPrefix: "PASS  ", wantText: []string{"the documented fallback: worker-a"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			run := runIdentityScript(t, `as_assert_ca_identity cluster-id default "hetzner/k3s autoscaling at 1 worker"`,
				testCase.log, testCase.noid, testCase.logFailures, identityFunctions...)
			if run.exitCode != 0 {
				t.Fatalf("as_assert_ca_identity exited %d\n%s", run.exitCode, run.output)
			}
			if len(run.results) != 1 {
				t.Fatalf("want exactly one result line, got %q (missing definitions: %v)\n%s", run.results, run.missingDefs, run.output)
			}
			result := run.results[0]
			if !strings.HasPrefix(result, testCase.wantPrefix) {
				t.Errorf("result = %q, want it to start %q (missing definitions: %v)\n%s", result, testCase.wantPrefix, run.missingDefs, run.output)
			}
			if !strings.Contains(result, "hetzner/k3s autoscaling at 1 worker") {
				t.Errorf("result %q does not name the step", result)
			}
			for _, want := range testCase.wantText {
				if !strings.Contains(result, want) {
					t.Errorf("result %q lacks %q", result, want)
				}
			}
			if testCase.wantRetry && run.readsMade != "2" {
				t.Errorf("log reads = %s, want 2 (one refused, one retried)", run.readsMade)
			}
		})
	}
}

var reportFunctions = []string{"log", "section", "report_results"}

func TestRunSummaryCountsNotAssertedApartAndFailsOnIt(t *testing.T) {
	cases := []struct {
		name         string
		lines        []string
		laneSkips    string
		wantExit     bool
		wantSummary  string
		wantJob      []string
		rejectJob    []string
		wantInOutput []string
	}{
		{
			// Other skips keep their meaning: an opt-in step left off, or a
			// lane without credentials, is listed and counted, never failed.
			name:        "skips alone pass the run",
			lines:       []string{"PASS  hetzner/k3s create", "SKIP  hetzner/k3s autoscaling (opt-in: ANKRA_SYSTEMTEST_AUTOSCALING=1)"},
			laneSkips:   "SKIP  ovh lane (missing OVH_CREDENTIAL_ID)",
			wantExit:    true,
			wantSummary: "1 passed, 0 failed, 0 not asserted, 2 skipped",
			rejectJob:   []string{"NOT ASSERTED"},
		},
		{
			name: "a requested assertion left unmade fails the run and is listed apart",
			lines: []string{
				"PASS  hetzner/k3s create",
				"UNASSERTED  hetzner/k3s autoscaling at 1 worker: autoscaler registers every worker - the cluster-autoscaler log stayed unreadable for 300s",
			},
			wantExit:    false,
			wantSummary: "1 passed, 0 failed, 1 not asserted, 0 skipped",
			wantJob: []string{
				"**1 passed, 0 failed, 1 not asserted, 0 skipped**",
				"**1 requested assertion(s) NOT ASSERTED - outcome unknown, so the run fails:**",
				"- UNASSERTED  hetzner/k3s autoscaling at 1 worker: autoscaler registers every worker",
			},
			wantInOutput: []string{"the run fails: 1 requested assertion(s) NOT ASSERTED"},
		},
		{
			name:        "a failure still fails the run",
			lines:       []string{"FAIL  hetzner/k3s upgrade", "PASS  hetzner/k3s create"},
			wantExit:    false,
			wantSummary: "1 passed, 1 failed, 0 not asserted, 0 skipped",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			body := `printf '%s\n' "$@" > "$RESULT_FILE"
LANE_SKIP_RESULTS=` + shellQuote(testCase.laneSkips) + `
report_results "$RESULT_FILE" "$STUB_DIR/a-target-that-wrote-no-results"`
			got := runReport(t, body, testCase.lines)
			if len(got.missingDefs) > 0 {
				t.Fatalf("lifecycle_systemtest.sh does not define %v", got.missingDefs)
			}
			if (got.exitCode == 0) != testCase.wantExit {
				t.Errorf("exit code = %d, want success=%v\n%s", got.exitCode, testCase.wantExit, got.output)
			}
			if !strings.Contains(got.output, testCase.wantSummary+" (All 1 provider lanes configured)") {
				t.Errorf("summary line lacks %q\n%s", testCase.wantSummary, got.output)
			}
			for _, want := range testCase.wantInOutput {
				if !strings.Contains(got.output, want) {
					t.Errorf("output lacks %q\n%s", want, got.output)
				}
			}
			for _, want := range testCase.wantJob {
				if !strings.Contains(got.jobSummary, want) {
					t.Errorf("job summary lacks %q\n%s", want, got.jobSummary)
				}
			}
			for _, reject := range testCase.rejectJob {
				if strings.Contains(got.jobSummary, reject) {
					t.Errorf("job summary unexpectedly contains %q\n%s", reject, got.jobSummary)
				}
			}
		})
	}
}

// End to end through both halves: an identity assertion the CLI could never
// read for fails the run and is named in the job summary.
func TestUnreadableAutoscalerLogFailsTheRun(t *testing.T) {
	names := append(append([]string{}, identityFunctions...), "section", "report_results")
	run := runIdentityScript(t, `as_assert_ca_identity cluster-id default "hetzner/k3s autoscaling at 2 workers"
report_results "$RESULT_FILE"`, "", "", "1000", names...)
	if len(run.missingDefs) > 0 {
		t.Fatalf("lifecycle_systemtest.sh does not define %v", run.missingDefs)
	}
	if run.exitCode == 0 {
		t.Errorf("the run passed with the identity assertion never made\n%s", run.output)
	}
	if !strings.Contains(run.output, "0 passed, 0 failed, 1 not asserted, 0 skipped") {
		t.Errorf("summary line does not count the unmade assertion\n%s", run.output)
	}
	if !strings.Contains(run.jobSummary, "NOT ASSERTED") || !strings.Contains(run.jobSummary, "hetzner/k3s autoscaling at 2 workers") {
		t.Errorf("job summary does not name the unmade assertion\n%s", run.jobSummary)
	}
}

func runReport(t *testing.T, body string, lines []string) scriptRun {
	t.Helper()
	quoted := make([]string, 0, len(lines))
	for _, line := range lines {
		quoted = append(quoted, shellQuote(line))
	}
	return runIdentityScript(t, "set -- "+strings.Join(quoted, " ")+"\n"+body, "", "", "0", reportFunctions...)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
