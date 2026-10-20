package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// maxAnkraCloudDiskGibibytes mirrors the platform's upper bound on a server
// root disk; checking client-side turns a round-trip rejection into an
// immediate usage error. The floor (the plan's and the template's minimum)
// is the Cloud's to enforce.
const maxAnkraCloudDiskGibibytes = 4096

// ankraCloudDiskFlags are the per-role root disk flags `create` and
// `preflight` take.
var ankraCloudDiskFlags = []string{"bastion-disk-gib", "control-plane-disk-gib", "etcd-disk-gib", "worker-disk-gib"}

// ankraCloudDiskFlag reads a root disk size flag: zero when the flag was not
// set, so the request omits it and the plan's included disk applies, and a
// usage error for a set value outside 1..maxAnkraCloudDiskGibibytes.
func ankraCloudDiskFlag(cmd *cobra.Command, flagName string) (int, error) {
	if !cmd.Flags().Changed(flagName) {
		return 0, nil
	}
	diskGibibytes, readError := cmd.Flags().GetInt(flagName)
	if readError != nil {
		return 0, withExitCode(exitUsage, fmt.Errorf("reading --%s: %w", flagName, readError))
	}
	if diskGibibytes < 1 || diskGibibytes > maxAnkraCloudDiskGibibytes {
		return 0, withExitCode(exitUsage, fmt.Errorf("invalid --%s %d: must be between 1 and %d GiB",
			flagName, diskGibibytes, maxAnkraCloudDiskGibibytes))
	}
	return diskGibibytes, nil
}
