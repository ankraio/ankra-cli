package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func stubControlPlaneOps(info *client.ControlPlaneInfo, changed *client.ChangeControlPlaneInstanceTypeResult) func() controlPlaneOps {
	return func() controlPlaneOps {
		return controlPlaneOps{
			provider: "digitalocean",
			get: func(string) (*client.ControlPlaneInfo, error) {
				return info, nil
			},
			setInstanceType: func(string, string) (*client.ChangeControlPlaneInstanceTypeResult, error) {
				return changed, nil
			},
		}
	}
}

// A running three-controller cluster can have its instance type rolled live
// while its count still needs a stopped cluster. Rendering one "Editable" line
// from the count's answer told operators to stop a cluster that did not need
// stopping, so the two answers must stay apart.
func TestControlPlaneGetRendersTheTwoControlsSeparately(t *testing.T) {
	countReason := "Stop the cluster to change the control plane count"
	info := &client.ControlPlaneInfo{
		Count:           3,
		SupportedCounts: []int{1, 3},
		InstanceType:    "s-2vcpu-4gb",
		CanChange:       false,
		Reason:          &countReason,
		CountChange: &client.ControlPlaneChangeCapability{
			Allowed: false,
			Mode:    client.ControlPlaneChangeModeOffline,
			Reason:  &countReason,
		},
		InstanceTypeChange: &client.ControlPlaneChangeCapability{
			Allowed: true,
			Mode:    client.ControlPlaneChangeModeRolling,
		},
	}

	stdoutOutput := captureStdout(t, func() {
		if err := runControlPlaneGet(&cobra.Command{}, stubControlPlaneOps(info, nil), "cluster-1"); err != nil {
			t.Fatalf("runControlPlaneGet returned an error: %v", err)
		}
	})

	if !strings.Contains(stdoutOutput, "Change count:     no - "+countReason) {
		t.Errorf("expected the count refusal to name the count's reason, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Change type:      yes - live, one controller at a time") {
		t.Errorf("expected the instance type to be offered as a live rolling resize, got: %s", stdoutOutput)
	}
	// The whole point of the split: the count's reason must not be rendered
	// against the instance type.
	typeLine := lineContaining(t, stdoutOutput, "Change type:")
	if strings.Contains(typeLine, countReason) {
		t.Errorf("the count's reason must not appear on the instance-type line, got: %s", typeLine)
	}
	if strings.Contains(stdoutOutput, "Editable:") {
		t.Errorf("a server that answers both controls must not be collapsed into one Editable line, got: %s", stdoutOutput)
	}
}

// A platform older than the release that split the answer sends neither
// capability. Nil means "this server did not say", not "not allowed", so the
// legacy rendering has to survive intact.
func TestControlPlaneGetFallsBackToLegacyFields(t *testing.T) {
	reason := "Stop the cluster to change the control plane count"
	info := &client.ControlPlaneInfo{
		Count:           1,
		SupportedCounts: []int{1, 3},
		InstanceType:    "s-2vcpu-4gb",
		CanChange:       false,
		Reason:          &reason,
	}

	stdoutOutput := captureStdout(t, func() {
		if err := runControlPlaneGet(&cobra.Command{}, stubControlPlaneOps(info, nil), "cluster-1"); err != nil {
			t.Fatalf("runControlPlaneGet returned an error: %v", err)
		}
	})

	if !strings.Contains(stdoutOutput, "Editable:         false") {
		t.Errorf("expected the legacy Editable line, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Note:             "+reason) {
		t.Errorf("expected the legacy Note line, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "Change count:") || strings.Contains(stdoutOutput, "Change type:") {
		t.Errorf("nothing may be invented for a server that did not answer, got: %s", stdoutOutput)
	}
}

// On the rolling lane the resize is already under way against a running
// cluster. "Start the cluster to apply" would be an instruction to stop it
// first, which is the opposite of what happened.
func TestSetInstanceTypeRollingReportsTheOperationAndNoStart(t *testing.T) {
	changed := &client.ChangeControlPlaneInstanceTypeResult{
		PreviousInstanceType: "s-2vcpu-4gb",
		NewInstanceType:      "s-4vcpu-8gb",
		Updated:              3,
		Mode:                 client.ControlPlaneChangeModeRolling,
		OperationID:          stringPointer("op-42"),
	}

	stdoutOutput := captureStdout(t, func() {
		err := runControlPlaneSetInstanceType(&cobra.Command{}, stubControlPlaneOps(nil, changed), "cluster-1", "s-4vcpu-8gb")
		if err != nil {
			t.Fatalf("runControlPlaneSetInstanceType returned an error: %v", err)
		}
	})

	if !strings.Contains(stdoutOutput, "resized one at a time") {
		t.Errorf("expected the rolling lane to be named, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Operation: op-42") {
		t.Errorf("expected the operation to poll, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "Start the cluster") {
		t.Errorf("a running cluster must not be told to start, got: %s", stdoutOutput)
	}
}

// The offline lane rewrites a stopped cluster's plan, so the next start is
// genuinely what applies it. That wording has to stay.
func TestSetInstanceTypeOfflineStillSaysStartTheCluster(t *testing.T) {
	changed := &client.ChangeControlPlaneInstanceTypeResult{
		PreviousInstanceType: "s-2vcpu-4gb",
		NewInstanceType:      "s-4vcpu-8gb",
		Updated:              1,
		Mode:                 client.ControlPlaneChangeModeOffline,
	}

	stdoutOutput := captureStdout(t, func() {
		err := runControlPlaneSetInstanceType(&cobra.Command{}, stubControlPlaneOps(nil, changed), "cluster-1", "s-4vcpu-8gb")
		if err != nil {
			t.Fatalf("runControlPlaneSetInstanceType returned an error: %v", err)
		}
	})

	if !strings.Contains(stdoutOutput, "Start the cluster to apply.") {
		t.Errorf("expected the offline lane to keep its wording, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "Operation:") {
		t.Errorf("the offline lane dispatches nothing, so there is no operation to print, got: %s", stdoutOutput)
	}
}

// A platform that predates the split sends no mode. It can only have been the
// offline lane, so the original wording is the safe answer.
func TestSetInstanceTypeWithoutModeKeepsLegacyWording(t *testing.T) {
	changed := &client.ChangeControlPlaneInstanceTypeResult{
		PreviousInstanceType: "s-2vcpu-4gb",
		NewInstanceType:      "s-4vcpu-8gb",
		Updated:              1,
	}

	stdoutOutput := captureStdout(t, func() {
		err := runControlPlaneSetInstanceType(&cobra.Command{}, stubControlPlaneOps(nil, changed), "cluster-1", "s-4vcpu-8gb")
		if err != nil {
			t.Fatalf("runControlPlaneSetInstanceType returned an error: %v", err)
		}
	})

	if !strings.Contains(stdoutOutput, "Start the cluster to apply.") {
		t.Errorf("expected the legacy wording, got: %s", stdoutOutput)
	}
}

// Asking for the type the controllers already run updates nothing on either
// lane. Reporting a change "from s-2vcpu-4gb to s-2vcpu-4gb" reads as though
// something happened.
func TestSetInstanceTypeNoOpSaysNothingChanged(t *testing.T) {
	changed := &client.ChangeControlPlaneInstanceTypeResult{
		PreviousInstanceType: "s-2vcpu-4gb",
		NewInstanceType:      "s-2vcpu-4gb",
		Updated:              0,
		Mode:                 client.ControlPlaneChangeModeRolling,
	}

	stdoutOutput := captureStdout(t, func() {
		err := runControlPlaneSetInstanceType(&cobra.Command{}, stubControlPlaneOps(nil, changed), "cluster-1", "s-2vcpu-4gb")
		if err != nil {
			t.Fatalf("runControlPlaneSetInstanceType returned an error: %v", err)
		}
	})

	if !strings.Contains(stdoutOutput, "is already 's-2vcpu-4gb'. Nothing to apply.") {
		t.Errorf("expected the no-op to say so, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "changed from") {
		t.Errorf("nothing changed, so nothing may be reported as changed, got: %s", stdoutOutput)
	}
}

// A server can answer "0 controller(s) updated" while the two type names
// differ. Nothing was staged and nothing was dispatched, so announcing a change
// and then telling the operator to start the cluster describes work that will
// never happen. (Ankra AI review finding on #181.)
func TestSetInstanceTypeZeroUpdatedNeverReportsAChange(t *testing.T) {
	changed := &client.ChangeControlPlaneInstanceTypeResult{
		PreviousInstanceType: "s-2vcpu-4gb",
		NewInstanceType:      "s-4vcpu-8gb",
		Updated:              0,
		Mode:                 client.ControlPlaneChangeModeOffline,
	}

	stdoutOutput := captureStdout(t, func() {
		err := runControlPlaneSetInstanceType(&cobra.Command{}, stubControlPlaneOps(nil, changed), "cluster-1", "s-4vcpu-8gb")
		if err != nil {
			t.Fatalf("runControlPlaneSetInstanceType returned an error: %v", err)
		}
	})

	if !strings.Contains(stdoutOutput, "No controllers were updated. The instance type is still 's-2vcpu-4gb'.") {
		t.Errorf("expected the zero-update outcome to be reported as such, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "changed from") {
		t.Errorf("no controller changed, so nothing may be reported as changed, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "Start the cluster to apply.") {
		t.Errorf("nothing was staged, so the next start applies nothing, got: %s", stdoutOutput)
	}
}

// A wrong instance type is accepted here and only rejected by the provider,
// which on the offline lane is at the next start - inside the maintenance
// window, with the cluster already stopped. The help has to say so and name the
// command that lists the real ones. (Linear PLA-805: a customer asked three
// times to resize to "s-2vcpu-8gb", a DigitalOcean size that does not exist.)
func TestSetInstanceTypeHelpWarnsAndNamesTheCatalogCommand(t *testing.T) {
	long := setInstanceTypeLong("DigitalOcean", "digitalocean sizes")

	if !strings.Contains(long, "it is not checked against DigitalOcean's catalog") {
		t.Errorf("expected the help to say the name is not validated, got: %s", long)
	}
	if !strings.Contains(long, "at the next start") {
		t.Errorf("expected the help to say when the rejection lands, got: %s", long)
	}
	if !strings.Contains(long, "ankra cluster digitalocean sizes") {
		t.Errorf("expected the help to name the catalog command, got: %s", long)
	}
}

// OVH and UpCloud have no instance-type listing command in the CLI. Pointing at
// one that does not exist is worse than saying nothing, so the warning stands
// alone for them.
func TestSetInstanceTypeHelpOmitsTheCatalogLineWhenThereIsNoCommand(t *testing.T) {
	long := setInstanceTypeLong("UpCloud", "")

	if !strings.Contains(long, "it is not checked against UpCloud's catalog") {
		t.Errorf("expected the warning regardless of catalog support, got: %s", long)
	}
	if strings.Contains(long, "List the instance types") || strings.Contains(long, "ankra cluster ") {
		t.Errorf("no catalog command exists for this provider, so none may be suggested, got: %s", long)
	}
}

func lineContaining(t *testing.T, output, needle string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line containing %q in: %s", needle, output)
	return ""
}
