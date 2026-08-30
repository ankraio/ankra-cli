package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// `cluster deprovision` used to describe itself as "(stop)" and promise it
// "will shut down the cluster but not delete it", while its own confirmation
// prompt three lines later warned that it deletes every cloud resource. The
// backend lane it calls is the full teardown (delete_permanently on every
// resource plus the cloud-termination DAG that uninstalls the stack), NOT the
// power lane behind `ankra <provider> cluster stop`. A customer power-cycled a
// GPU cluster through it believing it was a stop/start and lost the addon
// values they had patched in place (ankra-54il0, ankra-dic4y). These tests pin
// the corrected wording so the "stop"/"start" labels cannot come back.

type provisionWordingMock struct {
	baseMock
	cluster        client.ClusterListItem
	provisionCalls int
}

func (m *provisionWordingMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.cluster.Name == name || m.cluster.ID == name {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *provisionWordingMock) ProvisionCluster(ctx context.Context, clusterID string) (*client.ProvisionClusterResult, error) {
	m.provisionCalls++
	return &client.ProvisionClusterResult{MarkedToStartAt: "2026-08-24T11:32:00Z"}, nil
}

func TestClusterDeprovisionHelpDoesNotClaimItIsAStop(t *testing.T) {
	help := clusterDeprovisionCmd.Short + "\n" + clusterDeprovisionCmd.Long
	for _, banned := range []string{"(stop)", "but not delete it"} {
		if strings.Contains(help, banned) {
			t.Errorf("deprovision help still contains %q; it is a teardown, not a power-off", banned)
		}
	}
	for _, required := range []string{"teardown", "power-schedules", "cluster stop"} {
		if !strings.Contains(help, required) {
			t.Errorf("deprovision help should mention %q so operators can find the real power lane", required)
		}
	}
}

// The help also used to promise, unconditionally, that "the cluster record is
// kept so it can be provisioned again later". That is true only for imported
// clusters. A cloud deprovision routes to the provider endpoint, which DELETEs
// the cluster: the record, its id and its stacks are gone, and no later
// "cluster provision" can bring them back. Following the documented
// deprovision/provision cycle on a Proxmox cluster destroyed it outright
// (ankra-8h0lk). These pin the corrected wording.
func TestClusterDeprovisionHelpDoesNotPromiseTheRecordSurvives(t *testing.T) {
	help := clusterDeprovisionCmd.Short + "\n" + clusterDeprovisionCmd.Long
	if strings.Contains(help, "the cluster record is kept so it can be provisioned") {
		t.Error("deprovision help still promises the record survives; a cloud deprovision deletes it")
	}
	for _, required := range []string{"DELETES", "imported clusters keep their record"} {
		if !strings.Contains(help, required) {
			t.Errorf("deprovision help should state %q so the two outcomes are not confused", required)
		}
	}
}

func TestClusterProvisionHelpSaysItCannotRebuildACloudCluster(t *testing.T) {
	help := clusterProvisionCmd.Short + "\n" + clusterProvisionCmd.Long
	if !strings.Contains(help, "cannot rebuild a deprovisioned cloud cluster") {
		t.Error("provision help should say it cannot rebuild a deprovisioned cloud cluster; that record is deleted")
	}
}

func TestCloudClusterKindsAreRecognisedAsRecordDeleting(t *testing.T) {
	for _, kind := range []cloudClusterKind{
		cloudClusterKindHetzner, cloudClusterKindOvh, cloudClusterKindUpcloud,
		cloudClusterKindDigitalocean, cloudClusterKindProxmox, cloudClusterKindMorpheus,
	} {
		if !isCloudClusterKind(kind) {
			t.Errorf("%q must be treated as record-deleting so the prompt warns about it", kind)
		}
	}
	for _, kind := range []cloudClusterKind{"imported", "", "k3s"} {
		if isCloudClusterKind(kind) {
			t.Errorf("%q is not a provider-deprovisioned kind; its record survives", kind)
		}
	}
}

func TestClusterProvisionHelpDoesNotClaimItIsAStart(t *testing.T) {
	help := clusterProvisionCmd.Short + "\n" + clusterProvisionCmd.Long
	if strings.Contains(help, "(start)") {
		t.Errorf("provision help still labels itself %q; it rebuilds the cluster, it does not power it on", "(start)")
	}
	for _, required := range []string{"stored stack definition", "power-schedules"} {
		if !strings.Contains(help, required) {
			t.Errorf("provision help should mention %q", required)
		}
	}
}

func TestClusterDeprovisionPromptSaysTeardownNotPowerOff(t *testing.T) {
	mock := &deprovisionMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo"}}
	output, err := runConfirmCommand(t, mock, "n\n",
		[]*cobra.Command{clusterDeprovisionCmd},
		"cluster", "deprovision", "demo")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if !strings.Contains(output, "uninstalls every stack resource") {
		t.Errorf("confirmation prompt should say the stack is uninstalled too, got %q", output)
	}
	if !strings.Contains(output, "not a power-off") {
		t.Errorf("confirmation prompt should say this is not a power-off, got %q", output)
	}
}

func TestClusterProvisionNotesStacksAreRedeployedFromStoredDefinition(t *testing.T) {
	mock := &provisionWordingMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo"}}
	output, err := runConfirmCommand(t, mock, "",
		[]*cobra.Command{clusterProvisionCmd},
		"cluster", "provision", "demo")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if mock.provisionCalls != 1 {
		t.Fatalf("expected one provision call, got %d", mock.provisionCalls)
	}
	if !strings.Contains(output, "stored stack definition") {
		t.Errorf("provision output should note stacks are redeployed from the stored definition, got %q", output)
	}
	if !strings.Contains(output, "ankra cluster addons list") {
		t.Errorf("provision output should point at the addon verification command, got %q", output)
	}
}

func TestClusterProvisionNoteIsSuppressedForStructuredOutput(t *testing.T) {
	// The note goes to stderr so `--output json` stays machine-parseable on
	// stdout; runConfirmCommand merges both streams, so assert on the JSON
	// payload being present rather than on stream separation.
	mock := &provisionWordingMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo"}}
	output, err := runConfirmCommand(t, mock, "",
		[]*cobra.Command{clusterProvisionCmd},
		"cluster", "provision", "demo", "--output", "json")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(output, "marked_to_start_at") {
		t.Errorf("structured output should carry the API payload, got %q", output)
	}
	if strings.Contains(output, "stored stack definition") {
		t.Errorf("structured output should not carry the human-readable note, got %q", output)
	}
}
