package systemtest

import (
	"os"
	"os/exec"
	"regexp"
	"testing"
)

// TestWriteRefusedBusyNeedsAFailedWriteAndThePlatformsWords pins ankra-nz8f8:
// a day-2 write counts as refused-while-busy only when the CLI failed AND the
// output says the cluster is busy or carries an HTTP 409 status. The old check
// matched a bare "409" anywhere, so a successful stop or upgrade whose output
// printed a cluster id like 00a01a40-0124-4097-... was retried until the step
// failed, on every lane whose cluster id happened to contain "409".
func TestWriteRefusedBusyNeedsAFailedWriteAndThePlatformsWords(t *testing.T) {
	bash, lookError := exec.LookPath("bash")
	if lookError != nil {
		t.Skip("bash not available")
	}
	script, readError := os.ReadFile("lifecycle_systemtest.sh")
	if readError != nil {
		t.Fatal(readError)
	}
	definition := regexp.MustCompile(`(?ms)^write_refused_busy\(\) \{.*?^\}`).Find(script)
	if definition == nil {
		t.Fatal("write_refused_busy is not defined in lifecycle_systemtest.sh")
	}

	cases := []struct {
		name    string
		rc      string
		output  string
		refused bool
	}{
		{"success printing a cluster id with 409 in it", "0",
			"AWS cluster stop initiated.\n  Cluster ID: 00a01a40-0124-4097-98c5-da118d200ac2", false},
		{"success printing a hetzner id with 409 in it", "0",
			"Kubernetes upgrade submitted.\n  Cluster ID: 30067204-865a-4091-b40c-79c4bb112105", false},
		{"generic 409 from the CLI", "1", "stop failed: status 409, body: {\"detail\":\"busy\"}", true},
		{"platform busy wording", "1",
			"Cannot scale workers while operations are in progress: ['cluster_apply']. Please wait for them to complete.", true},
		{"cluster not in a modifiable state", "1", "Cluster is not in a state safe to modify", true},
		{"stop still running before a start", "1",
			"Cannot start cluster while a stop or terminate is still running (execution 1 \"stop\" is running)", true},
		{"a real failure that is not about being busy", "1",
			"upgrade failed: status 500, body: cluster 00a01a40-0124-4097-98c5-da118d200ac2", false},
		{"a 4090 status is not a 409", "1", "upgrade failed: status 4090, body: x", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(bash, "-c", string(definition)+"\nwrite_refused_busy \"$1\" \"$2\"", "bash", testCase.rc, testCase.output)
			runError := command.Run()
			refused := runError == nil
			if _, isExit := runError.(*exec.ExitError); runError != nil && !isExit {
				t.Fatal(runError)
			}
			if refused != testCase.refused {
				t.Errorf("write_refused_busy(rc=%s, %q) = %v, want %v", testCase.rc, testCase.output, refused, testCase.refused)
			}
		})
	}
}
