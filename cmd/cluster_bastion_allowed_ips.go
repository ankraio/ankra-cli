package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// bastionAllowedIPsFlagUsage is the create-time help shared by the four
// providers whose bastion lanes enforce the allowlist.
const bastionAllowedIPsFlagUsage = "IPv4 addresses or CIDRs allowed to reach the bastion over SSH, comma-separated " +
	"(the platform's own egress is always allowed too; IPv6 and 0.0.0.0/0 are refused). " +
	"Omit to keep the bastion reachable from anywhere (key-only SSH, rate-limited). " +
	"Change it later with 'bastion allowed-ips'."

// withBastionAllowedIPs adds the allowed-ips subcommand to a provider's
// bastion group. Only Hetzner, OVHcloud, UpCloud and DigitalOcean carry it:
// Ankra Cloud and AWS take their allowlist at create time and enforce it at
// the provider firewall, and the remaining providers' bastions are not on
// the public internet in the same way.
func withBastionAllowedIPs(bastionCmd *cobra.Command, opsFn func() bastionOps) *cobra.Command {
	allowedIPsCmd := &cobra.Command{
		Use:   "allowed-ips <cluster_id|name> [ip-or-cidr ...]",
		Short: "Restrict which sources may SSH to the bastion/gateway",
		Long: `Replace the bastion/gateway's SSH source allowlist. Only the listed IPv4
addresses or CIDRs, the platform's own egress addresses and private network
sources can then reach port 22; every other source is dropped. The platform's
egress is always added by the platform itself, so it can never be locked out.

The list you give is the complete new list: entries may be separated by
spaces or commas. --clear removes the allowlist and makes the bastion
reachable from anywhere again (key-only SSH, rate-limited). IPv6 entries and
0.0.0.0/0 are refused; at most 64 entries.

The platform applies the list through the bastion update operation reported
on success. Follow it with 'ankra cluster operations list <operation_id>'.

Examples:
  ankra cluster hetzner bastion allowed-ips my-cluster 203.0.113.7 198.51.100.0/24
  ankra cluster ovh bastion allowed-ips my-cluster --clear`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shouldClear, _ := cmd.Flags().GetBool("clear")
			allowedIPs, parseError := bastionAllowedIPsArgs(args[1:], shouldClear)
			if parseError != nil {
				return parseError
			}
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			return runBastionAllowedIPs(cmd, opsFn, clusterID, allowedIPs)
		},
	}
	allowedIPsCmd.Flags().Bool("clear", false, "Remove the allowlist: SSH from anywhere again")
	registerStructuredOutputFlags(allowedIPsCmd)
	bastionCmd.AddCommand(allowedIPsCmd)
	return bastionCmd
}

// bastionAllowedIPsArgs turns the positional entries into the list to send.
// Entries are split on commas as well as spaces, so a pasted
// "a,b" works like "a b". Exactly one of a non-empty list or --clear is
// required: an empty list sent by accident would reopen the bastion.
func bastionAllowedIPsArgs(rawEntries []string, shouldClear bool) ([]string, error) {
	allowedIPs := []string{}
	for _, rawEntry := range rawEntries {
		for _, entry := range strings.Split(rawEntry, ",") {
			if trimmed := strings.TrimSpace(entry); trimmed != "" {
				allowedIPs = append(allowedIPs, trimmed)
			}
		}
	}
	switch {
	case shouldClear && len(allowedIPs) > 0:
		return nil, errors.New("--clear removes the allowlist; do not list addresses with it")
	case !shouldClear && len(allowedIPs) == 0:
		return nil, errors.New("list at least one IPv4 address or CIDR, or pass --clear to remove the allowlist")
	}
	return allowedIPs, nil
}

func runBastionAllowedIPs(cmd *cobra.Command, opsFn func() bastionOps, clusterID string, allowedIPs []string) error {
	ops := opsFn()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	result, updateError := ops.allowedIPs(ctx, clusterID, allowedIPs)
	if updateError != nil {
		return fmt.Errorf("updating the bastion SSH allowlist: %w", updateError)
	}
	if result == nil {
		return errors.New("updating the bastion SSH allowlist: the platform answered without a result")
	}
	if handled, renderError := renderStructured(cmd, result); renderError != nil {
		return renderError
	} else if handled {
		return nil
	}
	printBastionAllowedIPs(result)
	return nil
}

// printBastionAllowedIPs reports the stored list and the operation that
// applies it; like the resize, the write returns before the bastion has the
// new rules, so it points at the poller rather than claiming they are live.
func printBastionAllowedIPs(result *client.UpdateBastionAllowedIPsResult) {
	if len(result.BastionAllowedIPs) == 0 {
		fmt.Printf("Bastion/gateway '%s' SSH allowlist cleared: SSH is reachable from anywhere again (key-only, rate-limited).\n", result.Name)
	} else {
		fmt.Printf("Bastion/gateway '%s' SSH restricted to: %s\n", result.Name, strings.Join(result.BastionAllowedIPs, ", "))
		fmt.Println("The platform's own egress addresses and private network sources stay allowed.")
	}
	if result.OperationID != nil && *result.OperationID != "" {
		fmt.Printf("Track progress with: ankra cluster operations list %s\n", *result.OperationID)
		return
	}
	fmt.Println("No operation was scheduled: the list already matched, or a stopped cluster applies it on start.")
}
