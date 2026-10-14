package systemtest

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestCreateFailureReasonReportsThePlatformsRefusal pins ankra-ews9qk: a create
// that printed no "Cluster ID:" line was reported as "could not resolve cluster
// id" for three lifecycle runs, while the platform had answered a 400 naming
// the cause (a Hetzner server type the location no longer offers, an UpCloud
// account with no credits). The FAIL line now carries that refusal.
func TestCreateFailureReasonReportsThePlatformsRefusal(t *testing.T) {
	bash, lookError := exec.LookPath("bash")
	if lookError != nil {
		t.Skip("bash not available")
	}
	script, readError := os.ReadFile("lifecycle_systemtest.sh")
	if readError != nil {
		t.Fatal(readError)
	}
	definition := regexp.MustCompile(`(?ms)^create_failure_reason\(\) \{.*?^\}`).Find(script)
	if definition == nil {
		t.Fatal("create_failure_reason is not defined in lifecycle_systemtest.sh")
	}
	if regexp.MustCompile(`fail "[^"]*could not resolve cluster id`).Match(script) {
		t.Error("a create site still fails with the bare 'could not resolve cluster id'")
	}

	cases := []struct {
		name   string
		output string
		want   string
	}{
		{"hetzner server type no longer offered",
			"creating systest-hetzner-k3s ...\nError: creating Hetzner cluster: create failed: status 400: " +
				`{"detail":"Server type 'cpx32' is not available in location 'nbg1' (not currently available for provisioning in this location)."}`,
			"refused by the platform, status 400: Server type 'cpx32' is not available in location 'nbg1' (not currently available for provisioning in this location)."},
		{"upcloud account without credits",
			"Error: creating UpCloud cluster: create failed: status 400, body: " +
				`{"detail":"The UpCloud account behind this credential has no credits left and no running server"}`,
			"refused by the platform, status 400: The UpCloud account behind this credential has no credits left and no running server"},
		{"a detail with an escaped quote", `Error: create failed: status 422: {"detail":"name \"x\" is taken"}`,
			`refused by the platform, status 422: name \"x\" is taken`},
		{"a CLI error without a platform body", "Error: unknown flag: --zone", "unknown flag: --zone"},
		{"no error at all", "creating x ...\nsomething unexpected", "the create printed neither a Cluster ID nor an error"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(bash, "-c", "set -euo pipefail\n"+string(definition)+"\ncreate_failure_reason \"$1\"", "bash", testCase.output)
			output, runError := command.Output()
			if runError != nil {
				t.Fatalf("create_failure_reason failed under set -euo pipefail: %v", runError)
			}
			if got := strings.TrimSpace(string(output)); got != testCase.want {
				t.Errorf("create_failure_reason(%q)\n got %q\nwant %q", testCase.output, got, testCase.want)
			}
		})
	}

	long := "Error: create failed: status 400: {\"detail\":\"" + strings.Repeat("x", 500) + "\"}"
	output, runError := exec.Command(bash, "-c", string(definition)+"\ncreate_failure_reason \"$1\"", "bash", long).Output()
	if runError != nil {
		t.Fatal(runError)
	}
	if got := strings.TrimSpace(string(output)); len(got) > len("refused by the platform, status 400: ")+300 {
		t.Errorf("a long detail must be cut at 300 characters, got %d", len(got))
	}
}

// TestHetznerLanePicksALocationThatOffersItsTypesNow pins ankra-ews9qk's
// second half: cpx32 left nbg1, then fsn1, then came back to nbg1 within the
// hour on 2026-10-14, so no fixed location keeps the lane creatable. The lane
// picks the first candidate that offers every type it uses right now.
func TestHetznerLanePicksALocationThatOffersItsTypesNow(t *testing.T) {
	bash, lookError := exec.LookPath("bash")
	if lookError != nil {
		t.Skip("bash not available")
	}
	script, readError := os.ReadFile("lifecycle_systemtest.sh")
	if readError != nil {
		t.Fatal(readError)
	}
	if !regexp.MustCompile(`(?m)^HETZNER_LOCATION="\$\{HETZNER_LOCATION:-\}"$`).Match(script) {
		t.Error("HETZNER_LOCATION must default to empty, so the lane picks its location at run time")
	}
	extract := func(name string) string {
		definition := regexp.MustCompile(`(?ms)^` + name + `\(\) \{.*?^\}`).Find(script)
		if definition == nil {
			t.Fatalf("%s is not defined in lifecycle_systemtest.sh", name)
		}
		return string(definition)
	}
	tableParser := extract("hetzner_types_from_table")
	picker := extract("pick_hetzner_location")

	rows := "  cax11        2      4     GB 40   GB arm    5.99       yes\n" +
		"  cpx22        2      4     GB 80   GB x86    19.49      yes\n" +
		"  cpx32        4      8     GB 160  GB x86    35.49      no\n"
	header := "  NAME         CORES  MEMORY   DISK    ARCH   PRICE/MO   AVAILABLE\n"
	for _, table := range []string{"Server types for location fsn1:\n" + header + rows, header + rows, rows} {
		parsed, parseError := exec.Command(bash, "-c", tableParser+"\nprintf '%s' \"$1\" | hetzner_types_from_table", "bash", table).Output()
		if parseError != nil {
			t.Fatal(parseError)
		}
		if got := strings.Fields(string(parsed)); strings.Join(got, " ") != "cax11 cpx22" {
			t.Errorf("hetzner_types_from_table = %v, want [cax11 cpx22] for a table with %d preamble lines", got, strings.Count(table, "\n")-3)
		}
	}

	// The stub plays the minute the run began: fsn1 lacks cpx32, nbg1 has
	// everything, hel1 would too.
	stub := `hetzner_available_types() { case "$1" in fsn1) printf 'cpx22\ncpx12\n';; nbg1|hel1) printf 'cpx22\ncpx32\n';; broken) return 7;; empty) return 0;; esac; }`
	types := "HETZNER_CP_TYPE=cpx32 HETZNER_WORKER_TYPE=cpx22 HETZNER_BASTION_TYPE=cpx22 HETZNER_BIGGER_TYPE=cpx32"
	cases := []struct {
		name       string
		candidates string
		types      string
		want       string
		wantFailed bool
	}{
		{"skips a location missing a type", "fsn1 nbg1 hel1", types, "nbg1", false},
		{"keeps the candidate order", "hel1 nbg1", types, "hel1", false},
		{"skips a location it could not read", "broken nbg1", types, "nbg1", false},
		{"no candidate offers every type", "fsn1", types,
			"no Hetzner location in 'fsn1' offers cpx32, cpx22, cpx22 and cpx32 right now", true},
		{"a location it could not read is never read as lacking", "fsn1 broken empty", types,
			"no readable Hetzner location offers cpx32, cpx22, cpx22 and cpx32 right now (lacking: fsn1; could not read: broken empty)", true},
		{"nothing could be read at all", "broken empty", types,
			"could not list Hetzner server types for any of 'broken empty' (a credential or API problem, not Hetzner's stock)", true},
		{"an empty type is a configuration error", "nbg1",
			"HETZNER_CP_TYPE= HETZNER_WORKER_TYPE=cpx22 HETZNER_BASTION_TYPE=cpx22 HETZNER_BIGGER_TYPE=cpx32",
			"configuration error: HETZNER_CP_TYPE, HETZNER_WORKER_TYPE, HETZNER_BASTION_TYPE and HETZNER_BIGGER_TYPE must all be set", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			program := "set -euo pipefail\n" + stub + "\n" + picker + "\n" + testCase.types +
				"\nHETZNER_LOCATION_CANDIDATES=\"$1\"\npick_hetzner_location"
			output, runError := exec.Command(bash, "-c", program, "bash", testCase.candidates).Output()
			failed := runError != nil
			if failed != testCase.wantFailed {
				t.Fatalf("pick_hetzner_location(%q) failed=%v, want %v (output %q)", testCase.candidates, failed, testCase.wantFailed, output)
			}
			if got := strings.TrimSpace(string(output)); got != testCase.want {
				t.Errorf("pick_hetzner_location(%q) = %q, want %q", testCase.candidates, got, testCase.want)
			}
		})
	}
}
