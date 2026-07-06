package cmd

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type controlPlaneOps struct {
	provider        string
	get             func(clusterID string) (*client.ControlPlaneInfo, error)
	setCount        func(clusterID string, count int) (*client.ChangeControlPlaneCountResult, error)
	setInstanceType func(clusterID, instanceType string) (*client.ChangeControlPlaneInstanceTypeResult, error)
}

func hetznerControlPlaneOps() controlPlaneOps {
	return controlPlaneOps{
		provider:        "hetzner",
		get:             apiClient.GetHetznerControlPlane,
		setCount:        apiClient.ChangeHetznerControlPlaneCount,
		setInstanceType: apiClient.ChangeHetznerControlPlaneInstanceType,
	}
}

func ovhControlPlaneOps() controlPlaneOps {
	return controlPlaneOps{
		provider:        "ovh",
		get:             apiClient.GetOvhControlPlane,
		setCount:        apiClient.ChangeOvhControlPlaneCount,
		setInstanceType: apiClient.ChangeOvhControlPlaneInstanceType,
	}
}

func upcloudControlPlaneOps() controlPlaneOps {
	return controlPlaneOps{
		provider:        "upcloud",
		get:             apiClient.GetUpcloudControlPlane,
		setCount:        apiClient.ChangeUpcloudControlPlaneCount,
		setInstanceType: apiClient.ChangeUpcloudControlPlaneInstanceType,
	}
}

func digitaloceanControlPlaneOps() controlPlaneOps {
	return controlPlaneOps{
		provider:        "digitalocean",
		get:             apiClient.GetDigitaloceanControlPlane,
		setCount:        apiClient.ChangeDigitaloceanControlPlaneCount,
		setInstanceType: apiClient.ChangeDigitaloceanControlPlaneInstanceType,
	}
}

func runControlPlaneGet(cmd *cobra.Command, opsFn func() controlPlaneOps, clusterID string) error {
	ops := opsFn()
	info, err := ops.get(clusterID)
	if err != nil {
		return err
	}
	if handled, err := renderStructured(cmd, info); err != nil {
		return err
	} else if handled {
		return nil
	}
	fmt.Printf("Control plane (%s)\n", ops.provider)
	fmt.Printf("  Count:            %d\n", info.Count)
	fmt.Printf("  Instance type:    %s\n", info.InstanceType)
	if len(info.SupportedCounts) > 0 {
		fmt.Printf("  Supported counts: %v\n", info.SupportedCounts)
	}
	fmt.Printf("  Editable:         %t\n", info.CanChange)
	if info.Reason != nil && *info.Reason != "" {
		fmt.Printf("  Note:             %s\n", *info.Reason)
	}
	return nil
}

func runControlPlaneSetCount(cmd *cobra.Command, opsFn func() controlPlaneOps, clusterID string, count int) error {
	ops := opsFn()
	res, err := ops.setCount(clusterID, count)
	if err != nil {
		return err
	}
	if handled, err := renderStructured(cmd, res); err != nil {
		return err
	} else if handled {
		return nil
	}
	fmt.Printf("Control plane count changed from %d to %d. Start the cluster to apply.\n",
		res.PreviousCount, res.NewCount)
	return nil
}

func runControlPlaneSetInstanceType(cmd *cobra.Command, opsFn func() controlPlaneOps, clusterID, instanceType string) error {
	ops := opsFn()
	res, err := ops.setInstanceType(clusterID, instanceType)
	if err != nil {
		return err
	}
	if handled, err := renderStructured(cmd, res); err != nil {
		return err
	} else if handled {
		return nil
	}
	fmt.Printf("Controller instance type changed from '%s' to '%s'. %d controller(s) updated. Start the cluster to apply.\n",
		res.PreviousInstanceType, res.NewInstanceType, res.Updated)
	return nil
}

func newControlPlaneCmd(opsFn func() controlPlaneOps, provider string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control-plane",
		Short: fmt.Sprintf("Manage the control plane for a %s cluster", provider),
		Long: `Inspect and change the control plane configuration. The cluster must be stopped
to change the controller count or instance type. Only 1 or 3 controllers are
allowed (etcd needs an odd number of voting members for quorum). Changes apply
the next time the cluster is started.`,
	}

	getCmd := &cobra.Command{
		Use:   "get <cluster_id>",
		Short: "Show the current control plane configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runControlPlaneGet(cmd, opsFn, args[0])
		},
	}

	setCountCmd := &cobra.Command{
		Use:   "set-count <cluster_id> <count>",
		Short: "Change the controller count (1 or 3)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			count, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid count: %w", err)
			}
			if count != 1 && count != 3 {
				return errors.New("count must be 1 or 3 (etcd quorum)")
			}
			return runControlPlaneSetCount(cmd, opsFn, args[0], count)
		},
	}

	setInstanceTypeCmd := &cobra.Command{
		Use:   "set-instance-type <cluster_id> <instance_type>",
		Short: "Change the controller instance type",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runControlPlaneSetInstanceType(cmd, opsFn, args[0], args[1])
		},
	}

	registerStructuredOutputFlags(getCmd, setCountCmd, setInstanceTypeCmd)
	cmd.AddCommand(getCmd, setCountCmd, setInstanceTypeCmd)
	return cmd
}

func init() {
	hetznerCmd.AddCommand(newControlPlaneCmd(hetznerControlPlaneOps, "Hetzner"))
	ovhCmd.AddCommand(newControlPlaneCmd(ovhControlPlaneOps, "OVH"))
	upcloudCmd.AddCommand(newControlPlaneCmd(upcloudControlPlaneOps, "UpCloud"))
	digitaloceanCmd.AddCommand(newControlPlaneCmd(digitaloceanControlPlaneOps, "DigitalOcean"))
}
