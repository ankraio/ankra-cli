package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type deprovisionRolesMock struct {
	deprovisionVolumesMock
	ciClusterID      *string
	stagingClusterID *string
	settingsRead     error
}

func (m *deprovisionRolesMock) GetOrganisationCISettings(context.Context) (*client.OrganisationCISettings, error) {
	if m.settingsRead != nil {
		return nil, m.settingsRead
	}
	return &client.OrganisationCISettings{ClusterID: m.ciClusterID}, nil
}

func (m *deprovisionRolesMock) GetOrganisationPreviewSettings(context.Context) (*client.OrganisationPreviewSettings, error) {
	if m.settingsRead != nil {
		return nil, m.settingsRead
	}
	return &client.OrganisationPreviewSettings{StagingClusterID: m.stagingClusterID}, nil
}

func deprovisionRolesFixture(ciClusterID *string, stagingClusterID *string, settingsRead error) *deprovisionRolesMock {
	return &deprovisionRolesMock{
		deprovisionVolumesMock: *hetznerVolumesMock(&client.DeprovisionVolumes{State: "none"}, nil),
		ciClusterID:            ciClusterID, stagingClusterID: stagingClusterID, settingsRead: settingsRead,
	}
}

func TestDeprovisionWarnsWhenTheClusterIsThePipelineAndAIStagingCluster(t *testing.T) {
	clusterID := "c-1"
	mock := deprovisionRolesFixture(&clusterID, &clusterID, nil)
	output, executeError := runConfirmCommand(t, mock, "", deprovisionResets, "cluster", "deprovision", "demo", "--yes")
	if executeError != nil {
		t.Fatalf("the warning must not stop the deprovision: %v", executeError)
	}
	for _, want := range []string{"pipeline cluster", "ankra org ci-settings set --cluster",
		"AI staging cluster", "ankra org ai-environment set --staging-cluster"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q: %q", want, output)
		}
	}
	if !mock.deprovisioned {
		t.Error("the cluster must still be deprovisioned")
	}
}

func TestDeprovisionSaysNothingForAnotherCluster(t *testing.T) {
	otherClusterID := "c-2"
	mock := deprovisionRolesFixture(&otherClusterID, &otherClusterID, nil)
	output, executeError := runConfirmCommand(t, mock, "", deprovisionResets, "cluster", "deprovision", "demo", "--yes")
	if executeError != nil {
		t.Fatal(executeError)
	}
	if strings.Contains(output, "pipeline cluster") || strings.Contains(output, "AI staging cluster") {
		t.Errorf("no role of this cluster to warn about: %q", output)
	}
}

func TestDeprovisionIsNotHeldUpByASettingsReadThatFails(t *testing.T) {
	mock := deprovisionRolesFixture(nil, nil, errors.New("forbidden"))
	if _, executeError := runConfirmCommand(t, mock, "", deprovisionResets, "cluster", "deprovision", "demo", "--yes"); executeError != nil {
		t.Fatalf("a failed settings read must not stop the deprovision: %v", executeError)
	}
	if !mock.deprovisioned {
		t.Error("the cluster must still be deprovisioned")
	}
}
