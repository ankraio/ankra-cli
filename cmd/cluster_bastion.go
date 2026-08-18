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
	fmt.Printf("Bastion/gateway '%s' resized to '%s'.\n", result.Name, result.InstanceType)
	return nil
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

func newBastionCmd(opsFn func() bastionOps, provider string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bastion",
		Short: fmt.Sprintf("Manage the bastion/gateway node for a %s cluster", provider),
		Long: `Inspect, diagnose, and resize the cluster's single bastion/gateway node.
Find its node ID with 'nodes list'.`,
	}

	resizeCmd := &cobra.Command{
		Use:   "resize <cluster_id> <instance_type>",
		Short: "Change the bastion/gateway instance type",
		Long: `Resize the bastion/gateway node. The provider's bastion/gateway update job
powers it off, resizes it, and powers it back on, causing brief SSH/NAT
downtime for the cluster.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBastionResize(cmd, opsFn, args[0], args[1])
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status <cluster_id>",
		Short: "Show the recorded bastion/gateway health verdict",
		Long: `Report the verdict the platform's bastion health loop last recorded for the
cluster's bastion/gateway: whether it is reachable, which hop a failed probe
stopped at, and when it was last checked. Nothing is probed by this command,
so it answers even while the bastion is unreachable - use 'bastion diagnose'
to SSH in and collect a fresh report.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBastionStatus(cmd, opsFn, args[0])
		},
	}

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

	registerAsyncWriteFlags(resizeCmd)
	registerStructuredOutputFlags(resizeCmd, statusCmd, diagnoseCmd)
	cmd.AddCommand(resizeCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(diagnoseCmd)
	return cmd
}

func init() {
	hetznerCmd.AddCommand(newBastionCmd(hetznerBastionOps, "Hetzner"))
	ovhCmd.AddCommand(newBastionCmd(ovhBastionOps, "OVH"))
	upcloudCmd.AddCommand(newBastionCmd(upcloudBastionOps, "UpCloud"))
	digitaloceanCmd.AddCommand(newBastionCmd(digitaloceanBastionOps, "DigitalOcean"))
}
