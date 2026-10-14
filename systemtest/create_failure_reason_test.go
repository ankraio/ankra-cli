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

// TestHetznerLaneDefaultsToALocationThatOffersItsServerTypes keeps the
// Hetzner lane off nbg1, where Hetzner stopped offering cpx32 in 2026-10 and
// every create was refused (ankra-ews9qk).
func TestHetznerLaneDefaultsToALocationThatOffersItsServerTypes(t *testing.T) {
	script, readError := os.ReadFile("lifecycle_systemtest.sh")
	if readError != nil {
		t.Fatal(readError)
	}
	if !regexp.MustCompile(`(?m)^HETZNER_LOCATION="\$\{HETZNER_LOCATION:-fsn1\}"$`).Match(script) {
		t.Error("HETZNER_LOCATION must default to fsn1 (cpx22 and cpx32 are offered there)")
	}
}
