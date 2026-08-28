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

func proxmoxControlPlaneOps() controlPlaneOps {
	return controlPlaneOps{
		provider:        "proxmox",
		get:             apiClient.GetProxmoxControlPlane,
		setCount:        apiClient.ChangeProxmoxControlPlaneCount,
		setInstanceType: apiClient.ChangeProxmoxControlPlaneInstanceType,
	}
}

func morpheusControlPlaneOps() controlPlaneOps {
	return controlPlaneOps{
		provider:        "morpheus",
		get:             apiClient.GetMorpheusControlPlane,
		setCount:        apiClient.ChangeMorpheusControlPlaneCount,
		setInstanceType: apiClient.ChangeMorpheusControlPlaneInstanceType,
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
	// A server that answers the two controls separately gets them rendered
	// separately. Collapsing them back into one "Editable" line is what told
	// operators to stop a cluster to change an instance type that could be
	// rolled live.
	if info.CountChange != nil || info.InstanceTypeChange != nil {
		printControlPlaneChange("Change count", info.CountChange)
		printControlPlaneChange("Change type", info.InstanceTypeChange)
		return nil
	}
	fmt.Printf("  Editable:         %t\n", info.CanChange)
	if info.Reason != nil && *info.Reason != "" {
		fmt.Printf("  Note:             %s\n", *info.Reason)
	}
	return nil
}

// printControlPlaneChange renders one control's answer. The lane is worth
// naming: rolling is a live, one-controller-at-a-time resize that needs no
// maintenance window, while offline only takes effect at the next start.
func printControlPlaneChange(label string, change *client.ControlPlaneChangeCapability) {
	if change == nil {
		return
	}
	var answer string
	switch {
	case change.Allowed && change.Mode == client.ControlPlaneChangeModeRolling:
		answer = "yes - live, one controller at a time"
	case change.Allowed && change.Mode == client.ControlPlaneChangeModeOffline:
		answer = "yes - applied at the next start"
	case change.Allowed:
		answer = "yes"
	case change.Reason != nil && *change.Reason != "":
		answer = "no - " + *change.Reason
	default:
		answer = "no"
	}
	fmt.Printf("  %-18s%s\n", label+":", answer)
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
	if res.Updated == 0 && res.PreviousInstanceType == res.NewInstanceType {
		fmt.Printf("Controller instance type is already '%s'. Nothing to apply.\n", res.NewInstanceType)
		return nil
	}
	fmt.Printf("Controller instance type changed from '%s' to '%s'. %d controller(s) updated.\n",
		res.PreviousInstanceType, res.NewInstanceType, res.Updated)
	// Only the offline lane leaves work for the next start. On the rolling lane
	// the resize has already been dispatched against a running cluster, and
	// telling the operator to start it would be an instruction to stop it first.
	if res.Mode == client.ControlPlaneChangeModeRolling {
		fmt.Println("The cluster is running, so the controllers are being resized one at a time.")
		if res.OperationID != nil && *res.OperationID != "" {
			fmt.Printf("Operation: %s\n", *res.OperationID)
		}
		return nil
	}
	fmt.Println("Start the cluster to apply.")
	return nil
}

func newControlPlaneCmd(opsFn func() controlPlaneOps, provider string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control-plane",
		Short: fmt.Sprintf("Manage the control plane for a %s cluster", provider),
		Long: `Inspect and change the control plane configuration. Only 1 or 3 controllers
are allowed (etcd needs an odd number of voting members for quorum).

The controller count can only be changed while the cluster is stopped, and
applies the next time the cluster is started.

The instance type can also be changed on a running cluster that has three
controllers: they are then resized one at a time, keeping the Kubernetes API
up. With a single controller it still needs a stopped cluster, because
resizing the only controller takes the API down while it reboots.

Run "control-plane get" to see which of the two is available right now.`,
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
	proxmoxCmd.AddCommand(newControlPlaneCmd(proxmoxControlPlaneOps, "Proxmox VE"))
	morpheusCmd.AddCommand(newControlPlaneCmd(morpheusControlPlaneOps, "HPE Morpheus"))
}
