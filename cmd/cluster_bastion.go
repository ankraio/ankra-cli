package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type bastionOps struct {
	provider string
	resize   func(ctx context.Context, clusterID, instanceType string, wait bool) (*client.UpdateBastionInstanceTypeResult, bool, error)
}

func hetznerBastionOps() bastionOps {
	return bastionOps{provider: "hetzner", resize: apiClient.UpdateHetznerBastionInstanceType}
}

func ovhBastionOps() bastionOps {
	return bastionOps{provider: "ovh", resize: apiClient.UpdateOvhBastionInstanceType}
}

func upcloudBastionOps() bastionOps {
	return bastionOps{provider: "upcloud", resize: apiClient.UpdateUpcloudBastionInstanceType}
}

func digitaloceanBastionOps() bastionOps {
	return bastionOps{provider: "digitalocean", resize: apiClient.UpdateDigitaloceanBastionInstanceType}
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

func newBastionCmd(opsFn func() bastionOps, provider string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bastion",
		Short: fmt.Sprintf("Manage the bastion/gateway node for a %s cluster", provider),
		Long:  `Resize the cluster's single bastion/gateway node. Find its node ID with 'nodes list'.`,
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

	registerAsyncWriteFlags(resizeCmd)
	registerStructuredOutputFlags(resizeCmd)
	cmd.AddCommand(resizeCmd)
	return cmd
}

func init() {
	hetznerCmd.AddCommand(newBastionCmd(hetznerBastionOps, "Hetzner"))
	ovhCmd.AddCommand(newBastionCmd(ovhBastionOps, "OVH"))
	upcloudCmd.AddCommand(newBastionCmd(upcloudBastionOps, "UpCloud"))
	digitaloceanCmd.AddCommand(newBastionCmd(digitaloceanBastionOps, "DigitalOcean"))
}
