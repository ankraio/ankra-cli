package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// defaultBastionDiagnoseTimeout bounds the client's wait for the diagnose
// endpoint. The platform waits up to two minutes for the SSH job's report
// before handing back the operation handle with completed=false, so the
// client budget has to sit above that or it would cancel a diagnosis that was
// about to answer.
const defaultBastionDiagnoseTimeout = 3 * time.Minute

// bastionOps binds one provider's bastion calls. Not every provider carries
// every one of them: resize needs an Update<Provider>BastionInstanceType
// client method, and diagnose needs a bastion job lane on the platform, so
// resize and diagnose are nil for the providers that have neither. The
// capability flags newBastionCmd takes decide which subcommands exist, so a
// nil field is never reached.
type bastionOps struct {
	provider string
	resize   func(ctx context.Context, clusterID, instanceType string, wait bool) (*client.UpdateBastionInstanceTypeResult, bool, error)
	health   func(clusterID string) (*client.BastionHealthResult, error)
	diagnose func(ctx context.Context, clusterID string) (*client.BastionDiagnoseResult, error)
}

func hetznerBastionOps() bastionOps {
	return bastionOps{
		provider: "hetzner",
		resize:   apiClient.UpdateHetznerBastionInstanceType,
		health:   apiClient.GetHetznerBastionHealth,
		diagnose: apiClient.DiagnoseHetznerBastion,
	}
}

func ovhBastionOps() bastionOps {
	return bastionOps{
		provider: "ovh",
		resize:   apiClient.UpdateOvhBastionInstanceType,
		health:   apiClient.GetOvhBastionHealth,
		diagnose: apiClient.DiagnoseOvhBastion,
	}
}

func upcloudBastionOps() bastionOps {
	return bastionOps{
		provider: "upcloud",
		resize:   apiClient.UpdateUpcloudBastionInstanceType,
		health:   apiClient.GetUpcloudBastionHealth,
		diagnose: apiClient.DiagnoseUpcloudBastion,
	}
}

func digitaloceanBastionOps() bastionOps {
	return bastionOps{
		provider: "digitalocean",
		resize:   apiClient.UpdateDigitaloceanBastionInstanceType,
		health:   apiClient.GetDigitaloceanBastionHealth,
		diagnose: apiClient.DiagnoseDigitaloceanBastion,
	}
}

func scalewayBastionOps() bastionOps {
	return bastionOps{
		provider: "scaleway",
		health:   apiClient.GetScalewayBastionHealth,
	}
}

func proxmoxBastionOps() bastionOps {
	return bastionOps{
		provider: "proxmox",
		health:   apiClient.GetProxmoxBastionHealth,
		diagnose: apiClient.DiagnoseProxmoxBastion,
	}
}

func morpheusBastionOps() bastionOps {
	return bastionOps{
		provider: "morpheus",
		health:   apiClient.GetMorpheusBastionHealth,
		diagnose: apiClient.DiagnoseMorpheusBastion,
	}
}

func runBastionResize(cmd *cobra.Command, opsFn func() bastionOps, clusterID, instanceType string) error {
	ops := opsFn()
	requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
	if err != nil {
		return err
	}
	defer cancelRequestContext()

	result, submitted, resizeError := ops.resize(requestContext, clusterID, instanceType, wait)
	if resizeError != nil {
		return asyncWriteError("resizing bastion", wait, resizeError)
	}
	if submitted {
		if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Bastion instance-type update")); err != nil {
			return err
		} else if handled {
			return nil
		}
		printAsyncWriteSubmitted("Bastion instance-type update")
		return nil
	}

	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if handled {
		return nil
	}
	printBastionResized(result)
	return nil
}

// printBastionResized reports the write, not the cloud resize: the platform
// records the new instance type and dispatches the provider's bastion update
// job, which powers the node off, resizes it and powers it back on long after
// this command has returned. The operation it hands back is the only thing
// that says when that has finished, so the confirmation names it and points at
// the poller instead of claiming the node is already resized.
func printBastionResized(result *client.UpdateBastionInstanceTypeResult) {
	if result.OperationID != nil && *result.OperationID != "" {
		fmt.Printf("Bastion/gateway '%s' instance type set to '%s' (operation %s).\n", result.Name, result.InstanceType, *result.OperationID)
		fmt.Println("The provider powers the node off, resizes it and powers it back on; SSH and NAT for the cluster are briefly unavailable until it is back.")
		fmt.Printf("Track progress with: ankra cluster operations list %s\n", *result.OperationID)
		return
	}
	fmt.Printf("Bastion/gateway '%s' instance type set to '%s'.\n", result.Name, result.InstanceType)
	fmt.Println("The platform returned no operation to track for this write - a stopped cluster applies the new type on start, and a resize already running keeps its own operation.")
	fmt.Println("List what is running with: ankra cluster operations list")
}

// runBastionStatus reports the verdict the platform's bastion health loop
// last recorded. It is a plain read - nothing is probed on the way - so it
// answers instantly even when the bastion is the thing that is down.
func runBastionStatus(cmd *cobra.Command, opsFn func() bastionOps, clusterID string) error {
	ops := opsFn()
	health, healthError := ops.health(clusterID)
	if healthError != nil {
		return healthError
	}

	if handled, err := renderStructured(cmd, health); err != nil {
		return err
	} else if handled {
		return nil
	}
	printBastionHealth(ops.provider, clusterID, health)
	return nil
}

// printBastionHealth renders the recorded verdict. The state is the
// load-bearing line and the rest is context for it, so a bastion the loop has
// not reached yet says so instead of printing an empty field, and the fields
// the loop only writes on a failed probe stay out of a healthy report. A
// provider with no diagnose job lane is told the diagnosis is unavailable
// rather than being pointed at a command that would only refuse.
func printBastionHealth(provider, clusterID string, health *client.BastionHealthResult) {
	fmt.Printf("Bastion/gateway for cluster '%s'\n", clusterID)
	fmt.Printf("  Resource ID:  %s\n", health.ResourceID)
	fmt.Printf("  Kind:         %s\n", health.Kind)
	fmt.Printf("  Provider:     %s\n", health.Provider)
	if health.State == "" {
		fmt.Println("  State:        not probed yet")
	} else {
		fmt.Printf("  State:        %s\n", health.State)
	}
	if health.Hop != "" {
		fmt.Printf("  Failed hop:   %s\n", health.Hop)
	}
	if health.Detail != "" {
		fmt.Printf("  Detail:       %s\n", health.Detail)
	}
	if health.ConsecutiveFailures > 0 {
		fmt.Printf("  Failures:     %d probe(s) in a row\n", health.ConsecutiveFailures)
	}
	if health.VMStatus != "" {
		fmt.Printf("  VM status:    %s\n", health.VMStatus)
	}
	if health.CheckedAt != "" {
		fmt.Printf("  Checked at:   %s\n", health.CheckedAt)
	}

	fmt.Println()
	if !health.DiagnoseSupported {
		fmt.Println("Diagnosis is not available for this provider.")
		return
	}
	fmt.Printf("Run 'ankra cluster %s bastion diagnose %s' to SSH in and collect a report.\n", provider, clusterID)
}

// runBastionDiagnose dispatches the read-only diagnose job and blocks for its
// report. The endpoint has no submit-and-return half - the platform always
// waits, within its own budget - so this command carries --timeout without a
// --wait twin, and a client budget that expires exits with exitWaitTimeout
// because the job itself keeps running.
func runBastionDiagnose(cmd *cobra.Command, opsFn func() bastionOps, clusterID string) error {
	ops := opsFn()
	timeout, timeoutFlagError := cmd.Flags().GetDuration("timeout")
	if timeoutFlagError != nil {
		return fmt.Errorf("reading --timeout: %w", timeoutFlagError)
	}
	requestContext, cancelRequestContext := context.WithTimeout(context.Background(), timeout)
	defer cancelRequestContext()

	result, diagnoseError := ops.diagnose(requestContext, clusterID)
	if diagnoseError != nil {
		wrapped := fmt.Errorf("diagnosing bastion: %w", diagnoseError)
		if errors.Is(diagnoseError, context.DeadlineExceeded) {
			return withExitCode(exitWaitTimeout, wrapped)
		}
		return wrapped
	}

	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if handled {
		return nil
	}
	return printBastionDiagnosis(result)
}

// printBastionDiagnosis renders the dispatched diagnosis: the operation
// handle always, then the report when the job finished inside the platform's
// wait budget and the poll hint when it did not.
func printBastionDiagnosis(result *client.BastionDiagnoseResult) error {
	fmt.Printf("Bastion diagnosis dispatched (%s).\n", result.JobName)
	fmt.Printf("  Operation ID: %s\n", result.OperationID)
	fmt.Printf("  Resource ID:  %s\n", result.ResourceID)
	fmt.Printf("  Status:       %s\n", result.Status)

	if len(result.Health) > 0 {
		fmt.Println()
		fmt.Println("Recorded health at dispatch:")
		if err := printJSONBlock(result.Health); err != nil {
			return err
		}
	}

	if !result.Completed {
		fmt.Println()
		fmt.Println("The diagnosis is still running. Poll it with:")
		fmt.Printf("  ankra cluster operations list %s\n", result.OperationID)
		return nil
	}
	if result.ErrorExcerpt != "" {
		fmt.Println()
		fmt.Printf("The diagnose job failed: %s\n", result.ErrorExcerpt)
		return nil
	}
	if len(result.Report) == 0 {
		fmt.Println()
		fmt.Println("The diagnose job completed without a report.")
		return nil
	}
	fmt.Println()
	fmt.Println("Report:")
	return printJSONBlock(result.Report)
}

// bastionGroupLong describes the group by what it can actually do for this
// provider, so a group without resize does not advertise it in its own help.
func bastionGroupLong(supportsResize, supportsDiagnose bool) string {
	switch {
	case supportsResize && supportsDiagnose:
		return `Inspect, diagnose, and resize the cluster's single bastion/gateway node.
Find its node ID with 'nodes list'.`
	case supportsDiagnose:
		return `Inspect and diagnose the cluster's single bastion/gateway node.
Find its node ID with 'nodes list'.`
	default:
		return `Inspect the cluster's single bastion/gateway node.
Find its node ID with 'nodes list'.`
	}
}

// bastionStatusLong ends on where to go next, which is the diagnose command
// only where there is one; a provider with no diagnose lane is told the
// verdict is the whole answer rather than being pointed at a command its
// group does not carry.
func bastionStatusLong(supportsDiagnose bool) string {
	const shared = `Report the verdict the platform's bastion health loop last recorded for the
cluster's bastion/gateway: whether it is reachable, which hop a failed probe
stopped at, and when it was last checked. Nothing is probed by this command,
so it answers even while the bastion is unreachable`
	if supportsDiagnose {
		return shared + ` - use 'bastion diagnose'
to SSH in and collect a fresh report.`
	}
	return shared + `. This provider has no bastion
diagnose job lane, so the recorded verdict is all there is to read.`
}

// newBastionCmd builds one provider's bastion group. The capability flags
// mirror newNodesCmd's: resize exists only where internal/client carries an
// Update<Provider>BastionInstanceType method, and diagnose only where the
// platform carries a <provider>_bastion_diagnose job lane. status is mounted
// for every provider on the node-group subrouter, which is all seven.
func newBastionCmd(opsFn func() bastionOps, provider string, supportsResize, supportsDiagnose bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bastion",
		Short: fmt.Sprintf("Manage the bastion/gateway node for a %s cluster", provider),
		Long:  bastionGroupLong(supportsResize, supportsDiagnose),
	}

	statusCmd := &cobra.Command{
		Use:   "status <cluster_id>",
		Short: "Show the recorded bastion/gateway health verdict",
		Long:  bastionStatusLong(supportsDiagnose),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBastionStatus(cmd, opsFn, args[0])
		},
	}
	registerStructuredOutputFlags(statusCmd)
	cmd.AddCommand(statusCmd)

	if supportsResize {
		resizeCmd := &cobra.Command{
			Use:   "resize <cluster_id> <instance_type>",
			Short: "Change the bastion/gateway instance type",
			Long: `Resize the bastion/gateway node. The provider's bastion/gateway update job
powers it off, resizes it, and powers it back on, causing brief SSH/NAT
downtime for the cluster.

--wait covers the platform write only; the cloud resize runs as the operation
reported on success. Follow it with 'ankra cluster operations list <operation_id>'.`,
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runBastionResize(cmd, opsFn, args[0], args[1])
			},
		}
		registerAsyncWriteFlags(resizeCmd)
		registerStructuredOutputFlags(resizeCmd)
		cmd.AddCommand(resizeCmd)
	}

	if supportsDiagnose {
		diagnoseCmd := &cobra.Command{
			Use:   "diagnose <cluster_id>",
			Short: "Run a read-only SSH diagnosis of the bastion/gateway",
			Long: `Dispatch the provider's read-only bastion diagnose job and wait for its
report: sshd configuration, failed-login volume, disk, failed units, journal
errors, listening ports, and pending security updates. Nothing on the host is
changed. The platform waits up to two minutes for the job; a slower run hands
back the operation id to poll with 'cluster operations list'.`,
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runBastionDiagnose(cmd, opsFn, args[0])
			},
		}
		diagnoseCmd.Flags().Duration("timeout", defaultBastionDiagnoseTimeout,
			"Maximum time to wait for the diagnosis report")
		registerStructuredOutputFlags(diagnoseCmd)
		cmd.AddCommand(diagnoseCmd)
	}

	return cmd
}

func init() {
	hetznerCmd.AddCommand(newBastionCmd(hetznerBastionOps, "Hetzner", true, true))
	ovhCmd.AddCommand(newBastionCmd(ovhBastionOps, "OVH", true, true))
	upcloudCmd.AddCommand(newBastionCmd(upcloudBastionOps, "UpCloud", true, true))
	digitaloceanCmd.AddCommand(newBastionCmd(digitaloceanBastionOps, "DigitalOcean", true, true))
	// The remaining three carry the bastion routes but not the resize client
	// method, and Scaleway's managed Public Gateway has no diagnose job lane
	// at all - its health verdict answers diagnose_supported=false.
	scalewayCmd.AddCommand(newBastionCmd(scalewayBastionOps, "Scaleway", false, false))
	proxmoxCmd.AddCommand(newBastionCmd(proxmoxBastionOps, "Proxmox VE", false, true))
	morpheusCmd.AddCommand(newBastionCmd(morpheusBastionOps, "HPE Morpheus", false, true))
}
