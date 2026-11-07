package cmd

import (
	"os"
	"testing"
)

// chdirOutsideGitDiscovery isolates no-checkout tests from repositories above
// their temporary directory. CI can put that directory inside its checkout.
// The parent belongs to the caller's temporary fixture and owns its cleanup.
func chdirOutsideGitDiscovery(t *testing.T, parent string) {
	t.Helper()
	directory, directoryError := os.MkdirTemp(parent, "outside-discovery-")
	if directoryError != nil {
		t.Fatal(directoryError)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", parent)
	t.Chdir(directory)
}
