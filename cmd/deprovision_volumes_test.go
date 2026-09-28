package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// deprovisionVolumesMock answers the volume read and records what the
// deprovision was sent, for a Hetzner cluster and an AWS one.
type deprovisionVolumesMock struct {
	baseMock
	cluster     client.ClusterListItem
	volumes     *client.DeprovisionVolumes
	volumesRead error

	deprovisioned bool
	gotOptions    client.DeprovisionOptions
}

func (m *deprovisionVolumesMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.cluster.Name == name || m.cluster.ID == name {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *deprovisionVolumesMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.cluster.ID == clusterID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *deprovisionVolumesMock) GetDeprovisionVolumes(_ string, _ string) (*client.DeprovisionVolumes, error) {
	return m.volumes, m.volumesRead
}

func (m *deprovisionVolumesMock) DeprovisionHetznerCluster(clusterID string, options client.DeprovisionOptions) (*client.DeprovisionHetznerClusterResponse, error) {
	m.deprovisioned = true
	m.gotOptions = options
	return &client.DeprovisionHetznerClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (m *deprovisionVolumesMock) DeprovisionAwsCluster(clusterID string, options client.DeprovisionOptions) (*client.ProviderDeprovisionClusterResponse, error) {
	m.deprovisioned = true
	m.gotOptions = options
	return &client.ProviderDeprovisionClusterResponse{ClusterID: clusterID}, nil
}

func presentVolumes(claims ...string) *client.DeprovisionVolumes {
	volumes := &client.DeprovisionVolumes{State: "present", ConsentRequired: true}
	for index, claim := range claims {
		volumes.Volumes = append(volumes.Volumes, client.DeprovisionVolume{
			VolumeID: "vol-" + string(rune('a'+index)), Claim: stringPointer(claim), Capacity: stringPointer("20Gi"),
		})
	}
	return volumes
}

func hetznerVolumesMock(volumes *client.DeprovisionVolumes, readError error) *deprovisionVolumesMock {
	return &deprovisionVolumesMock{
		cluster: client.ClusterListItem{ID: "c-1", Name: "demo", Kind: string(cloudClusterKindHetzner)},
		volumes: volumes, volumesRead: readError,
	}
}

var deprovisionResets = []*cobra.Command{clusterDeprovisionCmd, awsDeprovisionCmd}

// TestDeprovisionOfAClusterWithVolumesNeedsTheFlag pins the non-interactive
// refusal (ankra-pzrgy): a deprovision deletes the cluster's persistent
// volumes, so without --accept-volume-data-loss and without a terminal to
// ask on it fails naming them, before anything is deleted. --yes skips the
// teardown confirmation, never this one.
func TestDeprovisionOfAClusterWithVolumesNeedsTheFlag(t *testing.T) {
	mock := hetznerVolumesMock(presentVolumes("payments/data-postgres-0"), nil)
	output, executeError := runConfirmCommand(t, mock, "", deprovisionResets,
		"cluster", "deprovision", "demo", "--yes")
	if executeError == nil || mock.deprovisioned {
		t.Fatalf("a deprovision that deletes volumes must be refused without the flag: %v (deprovisioned %v)",
			executeError, mock.deprovisioned)
	}
	for _, fragment := range []string{"payments/data-postgres-0 (20Gi)", "--accept-volume-data-loss"} {
		if !strings.Contains(executeError.Error(), fragment) {
			t.Errorf("the refusal must name %q: %v", fragment, executeError)
		}
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want the usage exit code", exitCodeFor(executeError))
	}
	if !strings.Contains(output, "deletes its persistent volumes and the data on them") {
		t.Errorf("the warning must be printed: %s", output)
	}
}

// TestDeprovisionSendsTheAcknowledgementGivenByTheFlag pins that the flag
// reaches the platform, alongside force.
func TestDeprovisionSendsTheAcknowledgementGivenByTheFlag(t *testing.T) {
	mock := hetznerVolumesMock(presentVolumes("payments/data-postgres-0"), nil)
	if _, executeError := runConfirmCommand(t, mock, "", deprovisionResets,
		"cluster", "deprovision", "demo", "--yes", "--force", "--accept-volume-data-loss"); executeError != nil {
		t.Fatalf("deprovision failed: %v", executeError)
	}
	if !mock.gotOptions.AcceptVolumeDataLoss || !mock.gotOptions.Force {
		t.Fatalf("options = %+v, want force and the acknowledgement", mock.gotOptions)
	}
}

// TestDeprovisionAsksForTheAcknowledgementOnATerminal pins the prompt: a yes
// accepts the loss, a no cancels before anything is deleted.
func TestDeprovisionAsksForTheAcknowledgementOnATerminal(t *testing.T) {
	answerPromptInteractively(t)
	accepting := hetznerVolumesMock(presentVolumes("payments/data-postgres-0"), nil)
	if _, executeError := runConfirmCommand(t, accepting, "y\n", deprovisionResets,
		"cluster", "deprovision", "demo", "--yes"); executeError != nil {
		t.Fatalf("deprovision failed: %v", executeError)
	}
	if !accepting.gotOptions.AcceptVolumeDataLoss {
		t.Fatalf("a yes at the prompt must send the acknowledgement: %+v", accepting.gotOptions)
	}

	declining := hetznerVolumesMock(presentVolumes("payments/data-postgres-0"), nil)
	_, executeError := runConfirmCommand(t, declining, "n\n", deprovisionResets,
		"cluster", "deprovision", "demo", "--yes")
	if !errors.Is(executeError, errCancelled) || declining.deprovisioned {
		t.Fatalf("a no must cancel: %v (deprovisioned %v)", executeError, declining.deprovisioned)
	}
}

// TestDeprovisionTreatsAnUnreadableInventoryAsUnknown pins the careful
// direction: volumes Ankra could not list may still be deleted, so the
// deprovision asks for the acknowledgement rather than reading "no volumes".
func TestDeprovisionTreatsAnUnreadableInventoryAsUnknown(t *testing.T) {
	mock := hetznerVolumesMock(nil, errors.New("status 500"))
	output, executeError := runConfirmCommand(t, mock, "", deprovisionResets,
		"cluster", "deprovision", "demo", "--yes")
	if executeError == nil || mock.deprovisioned || !strings.Contains(executeError.Error(), "could not list") {
		t.Fatalf("an unreadable inventory must still need the acknowledgement: %v", executeError)
	}
	if !strings.Contains(output, "could not list the persistent volumes") || !strings.Contains(output, "status 500") {
		t.Errorf("the unknown warning must be printed with the reason the read failed: %s", output)
	}
}

// TestDeprovisionOfAClusterWithoutVolumesAsksNothing pins that a cluster with
// no volumes, or one whose retention policy keeps them, needs no
// acknowledgement; the kept volumes are named as kept.
func TestDeprovisionOfAClusterWithoutVolumesAsksNothing(t *testing.T) {
	empty := hetznerVolumesMock(&client.DeprovisionVolumes{State: "none"}, nil)
	if _, executeError := runConfirmCommand(t, empty, "", deprovisionResets,
		"cluster", "deprovision", "demo", "--yes"); executeError != nil || empty.gotOptions.AcceptVolumeDataLoss {
		t.Fatalf("a cluster without volumes deprovisions without the acknowledgement: %v %+v",
			executeError, empty.gotOptions)
	}

	retained := &deprovisionVolumesMock{
		cluster: client.ClusterListItem{ID: testClusterID, Name: "demo-aws", Kind: string(cloudClusterKindAws)},
		volumes: &client.DeprovisionVolumes{State: "none", RetentionPolicy: stringPointer("retain"),
			KeptVolumes: []client.DeprovisionVolume{{VolumeID: "vol-0abc", Claim: stringPointer("payments/data-postgres-0")}}},
	}
	output, executeError := runConfirmCommand(t, retained, "", deprovisionResets,
		"cluster", "aws", "deprovision", testClusterID, "--yes")
	if executeError != nil || !retained.deprovisioned || retained.gotOptions.AcceptVolumeDataLoss {
		t.Fatalf("a retained cluster deprovisions without the acknowledgement: %v %+v", executeError, retained.gotOptions)
	}
	if !strings.Contains(output, "retention policy") || !strings.Contains(output, "payments/data-postgres-0") {
		t.Errorf("the kept volumes must be named: %s", output)
	}
}

// TestDeprovisionAsksWhenThePlatformRequiresConsent pins that the platform's
// consent_required wins over the CLI's reading of the state: an answer that
// says "none" but requires the consent still needs the acknowledgement,
// rather than a deprovision the platform then refuses.
func TestDeprovisionAsksWhenThePlatformRequiresConsent(t *testing.T) {
	mock := hetznerVolumesMock(&client.DeprovisionVolumes{State: "none", ConsentRequired: true}, nil)
	_, executeError := runConfirmCommand(t, mock, "", deprovisionResets,
		"cluster", "deprovision", "demo", "--yes")
	if executeError == nil || mock.deprovisioned || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("a deprovision the platform requires consent for must need the flag: %v (deprovisioned %v)",
			executeError, mock.deprovisioned)
	}
}

// TestDeprovisionVolumeListIsCapped pins the "and N more" of a long list.
func TestDeprovisionVolumeListIsCapped(t *testing.T) {
	claims := make([]string, 12)
	for index := range claims {
		claims[index] = "apps/data-" + string(rune('a'+index))
	}
	if named := deprovisionVolumeList(presentVolumes(claims...).Volumes); !strings.HasSuffix(named, "and 2 more") {
		t.Fatalf("list = %q", named)
	}
}
