package cmd

import (
	"bytes"
	"strings"
	"testing"

	"ankra/internal/client"
)

// TestCICapacityHintNamesOnlyTheFullPoolMember reproduces an idle primary with
// waiting steps on another member, without compiling the entire command graph.
func TestCICapacityHintNamesOnlyTheFullPoolMember(t *testing.T) {
	capacity := &client.OrganisationCICapacity{
		IsPooled: true, CIWorkerCount: 64, OrganisationStepsWaitingOnSlots: 4,
		PoolMembers: []client.OrganisationCICapacityMember{
			{ClusterName: "build-01", IsPrimary: true, CIWorkerCount: 64, CanRunSteps: true},
			{ClusterName: "build-02", CIWorkerCount: 24, StepsInFlight: 24, CanRunSteps: true, IsFull: true},
		},
	}
	var output bytes.Buffer
	renderOrganisationCICapacityHints(&output, capacity, "build-01")
	if strings.Contains(output.String(), "Every slot") || strings.Contains(output.String(), "--cluster build-01") {
		t.Fatalf("an idle primary must not be described as full: %s", output.String())
	}
	if !strings.Contains(output.String(), "--cluster build-02") {
		t.Fatalf("hint must name the full member: %s", output.String())
	}
}

// TestSingleClusterCapacityHintRequiresMeasuredExhaustion keeps a pending count
// alone from claiming worker exhaustion and still explains a genuinely full cluster.
func TestSingleClusterCapacityHintRequiresMeasuredExhaustion(t *testing.T) {
	for _, busy := range []bool{false, true} {
		capacity := &client.OrganisationCICapacity{CIWorkerCount: 4, OrganisationStepsWaitingOnSlots: 1}
		if busy {
			capacity.StepsInFlightOnCluster = 4
		}
		var output bytes.Buffer
		renderOrganisationCICapacityHints(&output, capacity, "build-01")
		if strings.Contains(output.String(), "--cluster build-01") != busy {
			t.Fatalf("busy=%v: %s", busy, output.String())
		}
	}
}
