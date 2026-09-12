package cmd

import (
	"context"
	"errors"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// Scaleway was absent from every cloud-kind list in reconcile.go, so
// `ankra cluster deprovision <scaleway-cluster>` fell through to the generic
// imported lane. The platform refuses that lane for a cloud cluster, so the
// command printed "Deprovisioning cluster: X" and then failed with a 409
// naming the provider endpoint, and the operator had to fall back to
// `ankra scaleway cluster deprovision <id>` (ankra-e3pa7).
//
// Nothing caught it: the four lists are hand-maintained, and every sibling
// test enumerated providers by hand too, so all of them omitted Scaleway in
// the same way. This test enumerates from allCloudClusterKinds instead, so a
// provider added to that list and nowhere else fails here.
//
// What this test structurally CANNOT catch is a kind missing from
// allCloudClusterKinds itself - it iterates that list. The seven managed
// kinds are missing from it today and do still reach the generic lane
// (ankra-menqa); see the comment on allCloudClusterKinds.

type cloudDeprovisionDispatchMock struct {
	baseMock
	cluster client.ClusterListItem

	calledProvider string
	genericCalls   int
}

func (m *cloudDeprovisionDispatchMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.cluster.Name == name || m.cluster.ID == name {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *cloudDeprovisionDispatchMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.cluster.ID == clusterID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

// The generic lane. Reaching it for a cloud kind IS the defect.
func (m *cloudDeprovisionDispatchMock) DeprovisionCluster(_ context.Context, _ string) (*client.DeprovisionClusterResult, error) {
	m.genericCalls++
	return &client.DeprovisionClusterResult{}, nil
}

func (m *cloudDeprovisionDispatchMock) DeprovisionHetznerCluster(clusterID string, _ bool) (*client.DeprovisionHetznerClusterResponse, error) {
	m.calledProvider = "hetzner"
	return &client.DeprovisionHetznerClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (m *cloudDeprovisionDispatchMock) DeprovisionOvhCluster(clusterID string, _ bool) (*client.DeprovisionOvhClusterResponse, error) {
	m.calledProvider = "ovh"
	return &client.DeprovisionOvhClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (m *cloudDeprovisionDispatchMock) DeprovisionUpcloudCluster(clusterID string, _ bool) (*client.DeprovisionUpcloudClusterResponse, error) {
	m.calledProvider = "upcloud"
	return &client.DeprovisionUpcloudClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (m *cloudDeprovisionDispatchMock) DeprovisionDigitaloceanCluster(clusterID string, _ bool) (*client.DeprovisionDigitaloceanClusterResponse, error) {
	m.calledProvider = "digitalocean"
	return &client.DeprovisionDigitaloceanClusterResponse{Success: true, ClusterID: clusterID}, nil
}

func (m *cloudDeprovisionDispatchMock) DeprovisionScalewayCluster(clusterID string) (*client.ProviderDeprovisionClusterResponse, error) {
	m.calledProvider = "scaleway"
	return &client.ProviderDeprovisionClusterResponse{ClusterID: clusterID}, nil
}

func (m *cloudDeprovisionDispatchMock) DeprovisionAwsCluster(clusterID string) (*client.ProviderDeprovisionClusterResponse, error) {
	m.calledProvider = "aws"
	return &client.ProviderDeprovisionClusterResponse{ClusterID: clusterID}, nil
}

func (m *cloudDeprovisionDispatchMock) DeprovisionProxmoxCluster(clusterID string) (*client.ProviderDeprovisionClusterResponse, error) {
	m.calledProvider = "proxmox"
	return &client.ProviderDeprovisionClusterResponse{ClusterID: clusterID}, nil
}

func (m *cloudDeprovisionDispatchMock) DeprovisionMorpheusCluster(clusterID string) (*client.ProviderDeprovisionClusterResponse, error) {
	m.calledProvider = "morpheus"
	return &client.ProviderDeprovisionClusterResponse{ClusterID: clusterID}, nil
}

func TestEveryCloudClusterKindHasADeprovisionDispatch(t *testing.T) {
	for _, cloudKind := range allCloudClusterKinds {
		t.Run(string(cloudKind), func(subtest *testing.T) {
			mock := &cloudDeprovisionDispatchMock{
				cluster: client.ClusterListItem{ID: "c-1", Name: "demo", Kind: string(cloudKind)},
			}
			_, executeError := runConfirmCommand(subtest, mock, "",
				[]*cobra.Command{clusterDeprovisionCmd},
				"cluster", "deprovision", "demo", "--yes")
			if executeError != nil {
				subtest.Fatalf("deprovision failed: %v", executeError)
			}

			if mock.genericCalls != 0 {
				subtest.Fatalf(
					"a %s cluster was deprovisioned through the GENERIC imported lane.\n"+
						"For a self-hosted kind the platform refuses that lane with a 409 naming the "+
						"provider endpoint, so the command fails and the operator has to fall back to "+
						"`ankra %s cluster deprovision <id>`. Add a `case cloudClusterKind%s:` to the "+
						"dispatch switch in reconcile.go.",
					cloudKind, cloudKind, cloudKind)
			}
			if mock.calledProvider != string(cloudKind) {
				subtest.Fatalf("dispatched to %q, want the %s provider endpoint", mock.calledProvider, cloudKind)
			}
		})
	}
}

// isCloudClusterKind decides whether the operator is warned that the cluster
// RECORD is deleted and cannot be provisioned again. A cloud kind missing
// from it gets a teardown with the wrong warning, so it is derived from the
// same list rather than restating it.
func TestIsCloudClusterKindCoversEveryCloudKind(t *testing.T) {
	for _, cloudKind := range allCloudClusterKinds {
		if !isCloudClusterKind(cloudKind) {
			t.Errorf("isCloudClusterKind(%q) = false, want true", cloudKind)
		}
	}
	if isCloudClusterKind("imported") {
		t.Error(`isCloudClusterKind("imported") = true, want false: the generic lane keeps the record`)
	}
	if isCloudClusterKind("") {
		t.Error(`isCloudClusterKind("") = true, want false: an unknown kind must not take the cloud lane`)
	}
}
