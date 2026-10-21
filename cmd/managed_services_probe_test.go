package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

// TestServicesPackagesProposeWritesTheBestCompleteProposalIntoThePackage
// covers propose: the answer is printed, and with -f the best complete
// proposal replaces the package file's connection, keeping every other field.
func TestServicesPackagesProposeWritesTheBestCompleteProposalIntoThePackage(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	packagePath := filepath.Join(t.TempDir(), "dragonfly-cache.json")
	initial := client.ServicePackageContract{SchemaVersion: 2, Name: "dragonfly-cache", Version: "1.0.0", Capability: "cache",
		Connection: &client.ServicePackageConnection{Protocol: "redis"}}
	encoded, _ := json.Marshal(initial)
	if writeError := os.WriteFile(packagePath, encoded, 0o644); writeError != nil {
		t.Fatal(writeError)
	}
	stdout, stderr, runError := runServicesCommand(t, platform, "", "packages", "propose", "--profile", fakeMemberProfileID,
		"--demo", "demo-ready", "-f", packagePath)
	if runError != nil {
		t.Fatalf("%v\n%s", runError, stderr)
	}
	if !strings.Contains(stdout, "redis through dragonfly:6379, secret dragonfly-auth (complete, high confidence)") ||
		!strings.Contains(stdout, "Port 6379 is Redis's own.") {
		t.Fatalf("the proposal is printed:\n%s", stdout)
	}
	written, _ := os.ReadFile(packagePath)
	var contract client.ServicePackageContract
	if decodeError := json.Unmarshal(written, &contract); decodeError != nil || contract.Name != "dragonfly-cache" ||
		contract.Connection == nil || contract.Connection.SecretName != "dragonfly-auth" || len(contract.Connection.Endpoints) != 1 ||
		contract.Connection.Endpoints[0].Port != 6379 {
		t.Fatalf("the package keeps its fields and takes the proposal's connection: %v\n%s", decodeError, written)
	}
	if info, statError := os.Stat(packagePath); statError != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("the rewrite keeps the file's permissions: %v %v", info.Mode(), statError)
	}
	if !strings.Contains(stderr, "ankra services packages probe --profile "+fakeMemberProfileID+" --demo demo-ready -f "+packagePath) {
		t.Fatalf("the next step is named:\n%s", stderr)
	}
}

// TestServicesPackagesProposeRefusesAnUnobservedDemo covers the three-state
// guard: an unobserved namespace prints its reason and exits non-zero, and
// leaves the package file as it was.
func TestServicesPackagesProposeRefusesAnUnobservedDemo(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	packagePath := filepath.Join(t.TempDir(), "package.json")
	if writeError := os.WriteFile(packagePath, []byte(`{"schema_version":2,"name":"x"}`), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	stdout, _, runError := runServicesCommand(t, platform, "", "packages", "propose", "--profile", fakeMemberProfileID,
		"--demo", "demo-dark", "-f", packagePath, "-o", "json")
	if runError == nil || !strings.Contains(runError.Error(), "could not be observed") {
		t.Fatalf("an unobserved demo must not succeed: %v", runError)
	}
	// The services command runs here without the root command's
	// SilenceUsage, so cobra's usage text follows the JSON on stdout; the
	// document itself is the first value.
	var answer client.StackProfileDemoConnectionProposal
	if decodeError := json.NewDecoder(strings.NewReader(stdout)).Decode(&answer); decodeError != nil || answer.Observable {
		t.Fatalf("-o json stays parseable and says unobserved: %v\n%s", decodeError, stdout)
	}
	if unchanged, _ := os.ReadFile(packagePath); string(unchanged) != `{"schema_version":2,"name":"x"}` {
		t.Fatalf("the package file is untouched: %s", unchanged)
	}
	if _, _, standardInput := runServicesCommand(t, platform, "", "packages", "propose", "--profile", fakeMemberProfileID,
		"--demo", "demo-ready", "-f", "-"); standardInput == nil {
		t.Fatal("standard input cannot be rewritten")
	}
}

// TestServicesPackagesProbeExitsZeroOnlyWhenTheProbePassed covers the probe
// verdicts under --wait: passed exits 0, failed and unknown both exit 1 with
// the reason, a probe still queued at --timeout exits 5, and without --wait
// the probe is only queued.
func TestServicesPackagesProbeExitsZeroOnlyWhenTheProbePassed(t *testing.T) {
	packagePath := filepath.Join(t.TempDir(), "package.json")
	document := `{"schema_version":2,"name":"dragonfly-cache","connection":{"protocol":"redis","secret_name":"dragonfly-auth",` +
		`"secret_keys":["password"],"endpoints":[{"name":"primary","scheme":"redis","service":"dragonfly","port":6379}],` +
		`"fields":{"password":{"kind":"secret_key","value":"password"}}}}`
	if writeError := os.WriteFile(packagePath, []byte(document), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	probe := func(t *testing.T, verdict string, extra ...string) (string, error) {
		platform := newFakeServicesPlatform(t)
		platform.probeVerdict = verdict
		arguments := append([]string{"packages", "probe", "--profile", fakeMemberProfileID, "--demo", "demo-ready", "-f", packagePath}, extra...)
		stdout, _, runError := runServicesCommand(t, platform, "", arguments...)
		posts := platform.requests("POST", "/demo-ready/connection-probes")
		if len(posts) != 1 || !strings.Contains(posts[0].body, `"secret_name":"dragonfly-auth"`) {
			t.Fatalf("the package's connection is posted once: %+v", posts)
		}
		return stdout, runError
	}
	if stdout, runError := probe(t, "passed", "--wait"); runError != nil || !strings.Contains(stdout, "probed, verdict passed") {
		t.Fatalf("passed: %v\n%s", runError, stdout)
	}
	for verdict, reason := range map[string]string{"failed": "WRONGPASS", "unknown": "The demo stopped"} {
		if _, runError := probe(t, verdict, "--wait"); runError == nil || !strings.Contains(runError.Error(), reason) {
			t.Fatalf("%s must fail with its reason: %v", verdict, runError)
		}
	}
	_, timedOut := probe(t, "", "--wait", "--timeout", "1ms")
	if timedOut == nil || exitCodeFor(timedOut) != exitWaitTimeout {
		t.Fatalf("a probe still queued at --timeout exits %d: %v", exitWaitTimeout, timedOut)
	}
	if stdout, runError := probe(t, "passed"); runError != nil || !strings.Contains(stdout, "Probe probe-1: queued") {
		t.Fatalf("without --wait the probe is only queued: %v\n%s", runError, stdout)
	}
}

// TestServicesPackagesCollisionsExitsZeroOnlyWithoutACollision covers the
// three verdicts: no_collision exits 0, collisions exit 1 naming the object,
// and unknown exits 1 with the reason, never reading as no collision.
func TestServicesPackagesCollisionsExitsZeroOnlyWithoutACollision(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	if stdout, _, runError := runServicesCommand(t, platform, "", "packages", "collisions", "--profile", fakeMemberProfileID,
		"--demo", "demo-clean"); runError != nil || !strings.Contains(stdout, "no collision") {
		t.Fatalf("no_collision: %v\n%s", runError, stdout)
	}
	stdout, _, runError := runServicesCommand(t, platform, "", "packages", "collisions", "--profile", fakeMemberProfileID, "--demo", "demo-ready")
	if runError == nil || !strings.Contains(stdout, "ClusterRole dragonfly-operator") {
		t.Fatalf("collisions must fail and name the object: %v\n%s", runError, stdout)
	}
	stdout, _, runError = runServicesCommand(t, platform, "", "packages", "collisions", "--profile", fakeMemberProfileID,
		"--demo", "demo-dark", "-o", "json")
	var check client.StackProfileDemoCollisionCheck
	if runError == nil || !strings.Contains(runError.Error(), "did not answer the ClusterRole listing") ||
		json.NewDecoder(strings.NewReader(stdout)).Decode(&check) != nil || check.Verdict != "unknown" {
		t.Fatalf("unknown must fail with its reason and keep -o json parseable: %v\n%s", runError, stdout)
	}
}
