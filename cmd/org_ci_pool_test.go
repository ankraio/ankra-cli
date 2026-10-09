package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// orgCIPoolMock records the pool writes and answers a fixed pool.
type orgCIPoolMock struct {
	orgCISettingsMock

	pool          client.OrganisationCIPool
	poolError     error
	setClusterIDs []string
	setWeights    []*int
	removed       []string
}

func (m *orgCIPoolMock) GetOrganisationCIPool(ctx context.Context) (*client.OrganisationCIPool, error) {
	if m.poolError != nil {
		return nil, m.poolError
	}
	pool := m.pool
	return &pool, nil
}

func (m *orgCIPoolMock) SetOrganisationCIPoolMember(ctx context.Context, clusterID string,
	weight *int) (*client.OrganisationCIPool, error) {
	if m.poolError != nil {
		return nil, m.poolError
	}
	m.setClusterIDs = append(m.setClusterIDs, clusterID)
	m.setWeights = append(m.setWeights, weight)
	pool := m.pool
	return &pool, nil
}

func (m *orgCIPoolMock) RemoveOrganisationCIPoolMember(ctx context.Context,
	clusterID string) (*client.OrganisationCIPool, error) {
	if m.poolError != nil {
		return nil, m.poolError
	}
	m.removed = append(m.removed, clusterID)
	pool := m.pool
	return &pool, nil
}

const (
	poolPrimaryID = "4b1f0f8e-9c1a-4c2f-9e6f-2a1d8b3c4d5e"
	poolSecondID  = "7c2a1b3d-1111-4c2f-9e6f-2a1d8b3c4d5e"
)

func twoMemberPool() client.OrganisationCIPool {
	return client.OrganisationCIPool{IsPooled: true, Members: []client.OrganisationCIPoolMember{
		{ClusterID: poolPrimaryID, ClusterName: "ci-hel1", Weight: 100, IsPrimary: true},
		{ClusterID: poolSecondID, ClusterName: "ci-hel1-b", Weight: 200, IsListed: true},
	}}
}

// runOrgCIPoolWith installs the pool mock and drives rootCmd with it.
func runOrgCIPoolWith(t *testing.T, mock *orgCIPoolMock, args ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	return runOrgCIPoolInstalled(t, mock, args...)
}

func runOrgCIPoolInstalled(t *testing.T, mock *orgCIPoolMock, args ...string) (string, error) {
	t.Helper()
	resetOrgCISettingsFlags(t)
	for _, command := range []*cobra.Command{orgCIPoolListCmd, orgCIPoolAddCmd, orgCIPoolRemoveCmd} {
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	}
	output := new(strings.Builder)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs(args)
	executeError := rootCmd.Execute()
	return output.String(), executeError
}

func TestRunOrgCIPoolList_ShowsEveryMemberAndThePrimary(t *testing.T) {
	mock := &orgCIPoolMock{pool: twoMemberPool()}
	output, executeError := runOrgCIPoolWith(t, mock, "org", "ci-settings", "pool", "list")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	for _, fragment := range []string{"ci-hel1", "ci-hel1-b", "primary", "200"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in %s", fragment, output)
		}
	}
}

func TestRunOrgCIPoolList_SaysWhenNoMemberIsListed(t *testing.T) {
	mock := &orgCIPoolMock{pool: client.OrganisationCIPool{Members: []client.OrganisationCIPoolMember{
		{ClusterID: poolPrimaryID, ClusterName: "ci-hel1", Weight: 100, IsPrimary: true},
	}}}
	output, executeError := runOrgCIPoolWith(t, mock, "org", "ci-settings", "pool", "list")
	if executeError != nil {
		t.Fatalf("execute failed: %v", executeError)
	}
	if !strings.Contains(output, "every run goes to the pipeline cluster") {
		t.Errorf("an unpooled organisation is told how runs are placed, got %s", output)
	}
}

func TestRunOrgCIPoolList_JSONIsThePool(t *testing.T) {
	mock := &orgCIPoolMock{pool: twoMemberPool()}
	output, executeError := runOrgCIPoolWith(t, mock, "org", "ci-settings", "pool", "list", "-o", "json")
	if executeError != nil {
		t.Fatalf("execute failed: %v", executeError)
	}
	var decoded client.OrganisationCIPool
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("stdout is not the pool as JSON: %v\n%s", decodeError, output)
	}
	if !decoded.IsPooled || len(decoded.Members) != 2 || decoded.Members[1].Weight != 200 {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestRunOrgCIPoolAdd_ResolvesTheClusterNameAndSendsTheWeight(t *testing.T) {
	mock := &orgCIPoolMock{pool: twoMemberPool()}
	mock.clusters = []client.ClusterListItem{{ID: poolSecondID, Name: "ci-hel1-b"}}
	output, executeError := runOrgCIPoolWith(t, mock, "org", "ci-settings", "pool", "add", "ci-hel1-b",
		"--weight", "200")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.setClusterIDs) != 1 || mock.setClusterIDs[0] != poolSecondID {
		t.Fatalf("expected the resolved cluster id, got %v", mock.setClusterIDs)
	}
	if mock.setWeights[0] == nil || *mock.setWeights[0] != 200 {
		t.Errorf("expected weight 200, got %v", mock.setWeights[0])
	}
}

// An add without --weight must not send one: a member already listed keeps
// its weight, and a default sent by the CLI would reset it.
func TestRunOrgCIPoolAdd_WithoutAWeightSendsNone(t *testing.T) {
	mock := &orgCIPoolMock{pool: twoMemberPool()}
	_, executeError := runOrgCIPoolWith(t, mock, "org", "ci-settings", "pool", "add", poolSecondID)
	if executeError != nil {
		t.Fatalf("execute failed: %v", executeError)
	}
	if len(mock.setWeights) != 1 || mock.setWeights[0] != nil {
		t.Errorf("expected no weight sent, got %v", mock.setWeights)
	}
}

func TestRunOrgCIPoolRemove_AsksFirstAndRemovesWithYes(t *testing.T) {
	mock := &orgCIPoolMock{pool: twoMemberPool()}
	_, executeError := runOrgCIPoolWith(t, mock, "org", "ci-settings", "pool", "remove", poolSecondID, "--yes")
	if executeError != nil {
		t.Fatalf("execute failed: %v", executeError)
	}
	if len(mock.removed) != 1 || mock.removed[0] != poolSecondID {
		t.Errorf("expected the member removed, got %v", mock.removed)
	}
}

func TestRunOrgCIPool_APlatformWithoutPoolsExitsNotFound(t *testing.T) {
	mock := &orgCIPoolMock{poolError: client.ErrCIPoolUnavailable}
	_, executeError := runOrgCIPoolWith(t, mock, "org", "ci-settings", "pool", "list")
	if !errors.Is(executeError, client.ErrCIPoolUnavailable) {
		t.Fatalf("expected the unavailable sentence, got %v", executeError)
	}
	if exitCode := exitCodeFor(executeError); exitCode != exitNotFound {
		t.Errorf("exit code = %d, want %d", exitCode, exitNotFound)
	}
}

func TestRunOrgCISettingsGet_ShowsEveryPoolMembersLoad(t *testing.T) {
	clusterName := "ci-hel1"
	clusterID := poolPrimaryID
	mock := &orgCIPoolMock{}
	mock.settings = defaultCISettings()
	mock.capacity = &client.OrganisationCICapacity{
		ClusterID: &clusterID, ClusterName: &clusterName, CIWorkerCount: 4, StepsInFlightOnCluster: 4,
		IsPooled: true,
		PoolMembers: []client.OrganisationCICapacityMember{
			{ClusterID: poolPrimaryID, ClusterName: "ci-hel1", Weight: 100, IsPrimary: true, CIWorkerCount: 4,
				StepsInFlight: 4, CanRunSteps: true, IsFull: true},
			{ClusterID: poolSecondID, ClusterName: "ci-hel1-b", Weight: 200, IsListed: true, CIWorkerCount: 8,
				StepsInFlight: 1, CanRunSteps: true},
		},
	}
	output, executeError := runOrgCIPoolWith(t, mock, "org", "ci-settings", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v", executeError)
	}
	for _, fragment := range []string{"CI pool:", "2 clusters", "4 of 4 in use", "(full)", "1 of 8 in use"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in %s", fragment, output)
		}
	}
}
