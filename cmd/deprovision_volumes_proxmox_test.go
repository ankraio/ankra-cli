package cmd

import "testing"

// Proxmox VE deletes the disks its CSI driver provisioned with the cluster
// (they sit on a placeholder VM, not the cluster's), so a deprovision names
// them and needs the acknowledgement like the cloud providers do.
func TestProxmoxDeprovisionAsksAboutItsVolumes(t *testing.T) {
	if !volumeDeletingKinds[cloudClusterKindProxmox] {
		t.Fatal("a Proxmox VE deprovision must read and acknowledge its persistent volumes")
	}
	if volumeDeletingKinds[cloudClusterKindMorpheus] {
		t.Fatal("Morpheus disks go with their instances; it has no volumes to acknowledge")
	}
}
