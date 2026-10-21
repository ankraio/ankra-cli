package cmd

import (
	"fmt"
	"slices"
	"strings"

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

// ankraCloudStorageTiers mirrors the storage tiers the platform accepts for
// a server root disk: local-nvme is the node's own disks, standard, maxiops
// and hdd are the zone's Ankra Storage (Ceph) tiers.
var ankraCloudStorageTiers = []string{"local-nvme", "standard", "maxiops", "hdd"}

// ankraCloudDiskTierFlags are the per-role root disk tier flags `create`
// and `preflight` take.
var ankraCloudDiskTierFlags = []string{"bastion-disk-tier", "control-plane-disk-tier", "etcd-disk-tier", "worker-disk-tier"}

// ankraCloudDiskTierUsage words the help of a role's root disk tier flag.
func ankraCloudDiskTierUsage(role string) string {
	return role + " root disk storage tier: local-nvme (the node's own disks), standard, maxiops or hdd " +
		"(the zone's Ankra Storage). Default: the zone's default tier"
}

// ankraCloudDiskTierFlag reads a root disk tier flag: "" when the flag was
// not set, so the request omits it and the zone's default tier applies, and
// a usage error for a value the platform does not accept.
func ankraCloudDiskTierFlag(cmd *cobra.Command, flagName string) (string, error) {
	if !cmd.Flags().Changed(flagName) {
		return "", nil
	}
	diskTier, readError := cmd.Flags().GetString(flagName)
	if readError != nil {
		return "", withExitCode(exitUsage, fmt.Errorf("reading --%s: %w", flagName, readError))
	}
	diskTier = strings.ToLower(strings.TrimSpace(diskTier))
	if !slices.Contains(ankraCloudStorageTiers, diskTier) {
		return "", withExitCode(exitUsage, fmt.Errorf("invalid --%s %q: must be one of %s",
			flagName, diskTier, strings.Join(ankraCloudStorageTiers, ", ")))
	}
	return diskTier, nil
}
