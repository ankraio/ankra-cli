package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// gitTestConfiguration is Git configuration every git process the package's
// tests start runs with. `git fetch`, `pull`, `merge`, `commit` and `clone`
// end by starting `git maintenance run --auto --detach` (or `gc --auto` on
// older Git), a background child that can still be writing into the
// repository's objects/ directory after the command returned. When the
// repository sits in a t.TempDir, that child races the directory's cleanup,
// and the test fails with "TempDir RemoveAll cleanup: ... directory not empty"
// (ankra-vdvwod). Tests never need automatic maintenance, so it is off.
var gitTestConfiguration = [][2]string{
	{"maintenance.auto", "false"},
	{"gc.auto", "0"},
}

// TestMain adds gitTestConfiguration to the process environment before any
// test runs. Git reads GIT_CONFIG_COUNT / GIT_CONFIG_KEY_<n> /
// GIT_CONFIG_VALUE_<n> as command-line configuration, and gitEnvironment
// passes them through, so it reaches the git processes the CLI code starts as
// well as the tests' own.
func TestMain(m *testing.M) {
	if configurationError := addGitTestConfiguration(); configurationError != nil {
		fmt.Fprintln(os.Stderr, configurationError)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// addGitTestConfiguration appends gitTestConfiguration after any
// GIT_CONFIG_COUNT entries the environment already carries.
func addGitTestConfiguration() error {
	existingCount := 0
	if rawCount := os.Getenv("GIT_CONFIG_COUNT"); rawCount != "" {
		parsedCount, parseError := strconv.Atoi(rawCount)
		if parseError != nil || parsedCount < 0 {
			return fmt.Errorf("GIT_CONFIG_COUNT=%q is not a count", rawCount)
		}
		existingCount = parsedCount
	}
	for offset, entry := range gitTestConfiguration {
		index := strconv.Itoa(existingCount + offset)
		if setError := os.Setenv("GIT_CONFIG_KEY_"+index, entry[0]); setError != nil {
			return setError
		}
		if setError := os.Setenv("GIT_CONFIG_VALUE_"+index, entry[1]); setError != nil {
			return setError
		}
	}
	return os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(existingCount+len(gitTestConfiguration)))
}

// TestGitAutomaticMaintenanceIsOffForTests pins the TestMain wiring: a git
// process started the way the CLI starts one reads the configuration, so no
// detached maintenance outlives a test's repository.
func TestGitAutomaticMaintenanceIsOffForTests(t *testing.T) {
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		t.Skip("git is not installed")
	}
	repository := t.TempDir()
	if _, initError := executeGit(t.Context(), repository, "init", "-q"); initError != nil {
		t.Fatalf("git init: %v", initError)
	}
	for _, entry := range gitTestConfiguration {
		value, getError := executeGit(t.Context(), repository, "config", "--get", entry[0])
		if getError != nil {
			t.Fatalf("git config --get %s: %v", entry[0], getError)
		}
		if value != entry[1] {
			t.Fatalf("git config %s = %q, want %q", entry[0], value, entry[1])
		}
	}
}
