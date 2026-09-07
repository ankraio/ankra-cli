package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// playgroundTestClusterID is id-shaped: the playground commands resolve
// anything else as a cluster name (resolvePlaygroundClusterID).
const playgroundTestClusterID = "8f6a4d2e-1c3b-4a5d-9e7f-0123456789ab"

type playgroundMock struct {
	baseMock

	// clusters answers ListClusters for the name-resolution tests; nil
	// keeps the base mock's "not implemented".
	clusters []client.ClusterListItem

	createResult    *client.CreatePlaygroundResult
	createError     error
	status          *client.PlaygroundStatus
	statusError     error
	statusRequested string

	destroyResult    *client.DestroyPlaygroundResult
	destroyError     error
	destroyRequested string

	createRequestedPlan string
	plansCatalog        *client.PlaygroundPlanCatalog
	plansError          error
	resizeRequested     string
	resizeResult        *client.ResizePlaygroundResult
	resizeError         error
}

func (m *playgroundMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	if m.clusters == nil {
		return m.baseMock.ListClusters(page, pageSize)
	}
	return &client.ClusterListResponse{Result: m.clusters, Pagination: client.Pagination{TotalPages: 1, Page: page, PageSize: pageSize}}, nil
}

func (m *playgroundMock) CreatePlayground(planID string) (*client.CreatePlaygroundResult, error) {
	m.createRequestedPlan = planID
	return m.createResult, m.createError
}

func (m *playgroundMock) ListPlaygroundPlans() (*client.PlaygroundPlanCatalog, error) {
	return m.plansCatalog, m.plansError
}

func (m *playgroundMock) ResizePlayground(clusterID string, planID string) (*client.ResizePlaygroundResult, error) {
	m.resizeRequested = clusterID + ":" + planID
	return m.resizeResult, m.resizeError
}

func (m *playgroundMock) ListLimitRequests() (*client.LimitRequestList, error) {
	return &client.LimitRequestList{Requests: []client.LimitRequest{}}, nil
}

func (m *playgroundMock) SubmitLimitRequest(string, int64, string) (*client.LimitRequest, error) {
	return nil, errors.New("not implemented")
}

func (m *playgroundMock) GetPlaygroundStatus(clusterID string) (*client.PlaygroundStatus, error) {
	m.statusRequested = clusterID
	return m.status, m.statusError
}

func (m *playgroundMock) DestroyPlayground(clusterID string) (*client.DestroyPlaygroundResult, error) {
	m.destroyRequested = clusterID
	return m.destroyResult, m.destroyError
}

func withPlaygroundMock(t *testing.T, mock *playgroundMock) {
	t.Helper()
	previous := apiClient
	apiClient = mock
	t.Cleanup(func() { apiClient = previous })
}

func TestPlaygroundCreatePrintsTheClusterIDAndTheFollowUpCommand(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{
		createResult: &client.CreatePlaygroundResult{ClusterID: playgroundTestClusterID, Success: true},
	})
	output := captureStdout(t, func() {
		if err := clusterPlaygroundCreateCmd.RunE(clusterPlaygroundCreateCmd, nil); err != nil {
			t.Fatalf("create failed: %v", err)
		}
	})
	if !strings.Contains(output, playgroundTestClusterID) {
		t.Errorf("expected the cluster id in the output, got: %s", output)
	}
	// Provisioning is asynchronous, so the command has to tell the user how
	// to follow it rather than implying the playground is ready.
	if !strings.Contains(output, "ankra cluster playground status "+playgroundTestClusterID) {
		t.Errorf("expected the follow-up command in the output, got: %s", output)
	}
}

// The --size flag is the order: it must reach the client verbatim, and its
// absence must order the free trial (an empty plan on the wire).
func TestPlaygroundCreatePassesTheOrderedSizeThrough(t *testing.T) {
	mock := &playgroundMock{
		createResult: &client.CreatePlaygroundResult{ClusterID: playgroundTestClusterID, Success: true},
	}
	withPlaygroundMock(t, mock)
	clusterPlaygroundCreateSize = "medium"
	t.Cleanup(func() { clusterPlaygroundCreateSize = "" })
	captureStdout(t, func() {
		if err := clusterPlaygroundCreateCmd.RunE(clusterPlaygroundCreateCmd, nil); err != nil {
			t.Fatalf("create failed: %v", err)
		}
	})
	if mock.createRequestedPlan != "medium" {
		t.Errorf("ordered size %q did not reach the client, got %q", "medium", mock.createRequestedPlan)
	}
}

func TestPlaygroundPlansListsSizesWithPricesAndAvailability(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{
		plansCatalog: &client.PlaygroundPlanCatalog{
			DefaultPlanID: "trial",
			Currency:      "eur",
			// No paid billing plan: the listing must say so instead of
			// letting the order fail at the end.
			OrganisationHasPaidPlan: false,
			Plans: []client.PlaygroundPlan{
				{ID: "trial", DisplayName: "Trial", Vcpus: 1, MemoryGB: 2, StorageGB: 20, PriceMonthlyCents: 0, Currency: "eur", Available: true},
				{ID: "small", DisplayName: "Small", Vcpus: 2, MemoryGB: 4, StorageGB: 50, PriceMonthlyCents: 1350, Currency: "eur", Available: true},
				{ID: "large", DisplayName: "Large", Vcpus: 8, MemoryGB: 16, StorageGB: 200, PriceMonthlyCents: 7200, Currency: "eur", Available: false},
			},
		},
	})
	buffer := &strings.Builder{}
	clusterPlaygroundPlansCmd.SetOut(buffer)
	t.Cleanup(func() { clusterPlaygroundPlansCmd.SetOut(nil) })
	if err := clusterPlaygroundPlansCmd.RunE(clusterPlaygroundPlansCmd, nil); err != nil {
		t.Fatalf("plans failed: %v", err)
	}
	output := buffer.String()
	for _, expected := range []string{"trial", "free", "small", "€13.50/mo", "large", "at capacity right now",
		"create --size", "billing plan"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in the listing, got: %s", expected, output)
		}
	}
}

func TestPlaygroundResizePassesTheSizeAndPrintsTheOutcome(t *testing.T) {
	mock := &playgroundMock{
		resizeResult: &client.ResizePlaygroundResult{
			ClusterID: playgroundTestClusterID,
			Plan: client.PlaygroundOrderedPlan{
				ID: "medium", DisplayName: "Medium", Vcpus: 4, MemoryGB: 8,
				PriceMonthlyCents: 2880, Currency: "eur",
			},
		},
	}
	withPlaygroundMock(t, mock)
	clusterPlaygroundResizeSize = "medium"
	t.Cleanup(func() { clusterPlaygroundResizeSize = "" })
	buffer := &strings.Builder{}
	clusterPlaygroundResizeCmd.SetOut(buffer)
	t.Cleanup(func() { clusterPlaygroundResizeCmd.SetOut(nil) })
	if err := clusterPlaygroundResizeCmd.RunE(clusterPlaygroundResizeCmd, []string{playgroundTestClusterID}); err != nil {
		t.Fatalf("resize failed: %v", err)
	}
	if mock.resizeRequested != playgroundTestClusterID+":medium" {
		t.Errorf("resize call = %q, want "+playgroundTestClusterID+":medium", mock.resizeRequested)
	}
	output := buffer.String()
	for _, expected := range []string{"Medium", "€28.80/mo", "pro-rata"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in output, got: %s", expected, output)
		}
	}
}

func TestPlaygroundResizeRequiresTheSizeFlag(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{})
	clusterPlaygroundResizeSize = ""
	if err := clusterPlaygroundResizeCmd.RunE(clusterPlaygroundResizeCmd, []string{playgroundTestClusterID}); err == nil {
		t.Fatal("resize without --size must refuse")
	}
}

func TestPlaygroundCreateSurfacesTheServerError(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{createError: errors.New("A playground already exists for this organisation.")})
	runError := clusterPlaygroundCreateCmd.RunE(clusterPlaygroundCreateCmd, nil)
	if runError == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(runError.Error(), "already exists") {
		t.Errorf("expected the server detail to survive, got: %v", runError)
	}
}

func TestPlaygroundStatusPrintsThePhaseAndMessage(t *testing.T) {
	message := "waiting for the agent"
	mock := &playgroundMock{status: &client.PlaygroundStatus{
		ClusterID:     playgroundTestClusterID,
		Phase:         "provisioning",
		StatusMessage: &message,
		ExpiresAt:     "2026-08-14T09:00:00Z",
	}}
	withPlaygroundMock(t, mock)
	output := captureStdout(t, func() {
		if err := clusterPlaygroundStatusCmd.RunE(clusterPlaygroundStatusCmd, []string{playgroundTestClusterID}); err != nil {
			t.Fatalf("status failed: %v", err)
		}
	})
	if mock.statusRequested != playgroundTestClusterID {
		t.Errorf("expected the cluster id to be passed through, got %q", mock.statusRequested)
	}
	for _, expected := range []string{"provisioning", "2026-08-14T09:00:00Z", "waiting for the agent"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in the output, got: %s", expected, output)
		}
	}
}

// An absent status_message must not print an empty Message line.
func TestPlaygroundStatusOmitsAnAbsentMessage(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{status: &client.PlaygroundStatus{
		ClusterID: playgroundTestClusterID,
		Phase:     "ready",
		ExpiresAt: "2026-08-14T09:00:00Z",
	}})
	output := captureStdout(t, func() {
		if err := clusterPlaygroundStatusCmd.RunE(clusterPlaygroundStatusCmd, []string{playgroundTestClusterID}); err != nil {
			t.Fatalf("status failed: %v", err)
		}
	})
	if strings.Contains(output, "Message:") {
		t.Errorf("expected no Message line, got: %s", output)
	}
}

func TestPlaygroundDestroyPrintsTheClusterIDAndPhase(t *testing.T) {
	mock := &playgroundMock{
		destroyResult: &client.DestroyPlaygroundResult{ClusterID: playgroundTestClusterID, Phase: "deprovisioning"},
	}
	withPlaygroundMock(t, mock)
	output := new(bytes.Buffer)
	clusterPlaygroundDestroyCmd.SetOut(output)
	clusterPlaygroundDestroyCmd.SetErr(output)
	t.Cleanup(func() {
		clusterPlaygroundDestroyCmd.SetOut(nil)
		clusterPlaygroundDestroyCmd.SetErr(nil)
	})

	if runError := clusterPlaygroundDestroyCmd.RunE(
		clusterPlaygroundDestroyCmd, []string{playgroundTestClusterID}); runError != nil {
		t.Fatalf("destroy failed: %v", runError)
	}
	if mock.destroyRequested != playgroundTestClusterID {
		t.Errorf("destroy asked for %q, want "+playgroundTestClusterID, mock.destroyRequested)
	}
	// The phase is what tells the caller teardown was scheduled rather than
	// finished, so it has to be in the output, not just the cluster id.
	for _, expected := range []string{playgroundTestClusterID, "deprovisioning"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("expected %q in the output, got: %s", expected, output.String())
		}
	}
}

func TestPlaygroundDestroySurfacesTheServerError(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{destroyError: errors.New("Playground not found.")})
	runError := clusterPlaygroundDestroyCmd.RunE(clusterPlaygroundDestroyCmd, []string{playgroundTestClusterID})
	if runError == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(runError.Error(), "Playground not found.") {
		t.Errorf("the server detail must survive: %v", runError)
	}
}

// A custom plan is invoiced against its agreement rather than collected by
// Stripe, so the order gate never asks for a card and the listing must not
// either. Every other plan still says so - including against a server too old
// to send the field, where an absent answer is not an exemption.
func TestPlaygroundPlansAsksForACardOnlyWhereStripeCollects(t *testing.T) {
	requiresCard := true
	exemptFromCard := false

	for _, testCase := range []struct {
		name          string
		requiresField *bool
		wantCardLine  bool
	}{
		{name: "custom plan", requiresField: &exemptFromCard, wantCardLine: false},
		{name: "Stripe-collected plan", requiresField: &requiresCard, wantCardLine: true},
		{name: "server predating the field", requiresField: nil, wantCardLine: true},
	} {
		withPlaygroundMock(t, &playgroundMock{
			plansCatalog: &client.PlaygroundPlanCatalog{
				DefaultPlanID:                   "trial",
				Currency:                        "eur",
				OrganisationHasPaidPlan:         true,
				OrganisationHasPaymentCard:      false,
				OrganisationRequiresPaymentCard: testCase.requiresField,
				Plans: []client.PlaygroundPlan{
					{ID: "small", DisplayName: "Small", Vcpus: 2, MemoryGB: 4, StorageGB: 50, PriceMonthlyCents: 1350, Currency: "eur", Available: true},
				},
			},
		})
		buffer := &strings.Builder{}
		clusterPlaygroundPlansCmd.SetOut(buffer)
		if err := clusterPlaygroundPlansCmd.RunE(clusterPlaygroundPlansCmd, nil); err != nil {
			clusterPlaygroundPlansCmd.SetOut(nil)
			t.Fatalf("%s: plans failed: %v", testCase.name, err)
		}
		output := buffer.String()
		clusterPlaygroundPlansCmd.SetOut(nil)
		if hasCardLine := strings.Contains(output, "payment card on file"); hasCardLine != testCase.wantCardLine {
			t.Errorf("%s: card line present = %v, want %v; output: %s",
				testCase.name, hasCardLine, testCase.wantCardLine, output)
		}
	}
}

// The playground routes take the cluster id; the CLI used to pass a name
// straight through and relay the route's bare 404 (ankra-y8l44.35). A name
// from `ankra cluster list` now resolves to the id for status, destroy and
// resize alike.
func TestPlaygroundCommandsResolveAClusterName(t *testing.T) {
	mock := &playgroundMock{
		clusters: []client.ClusterListItem{
			{ID: "11111111-2222-4333-8444-555555555555", Name: "other"},
			{ID: playgroundTestClusterID, Name: "playground"},
		},
		status:        &client.PlaygroundStatus{ClusterID: playgroundTestClusterID, Phase: "ready", ExpiresAt: "2026-08-14T09:00:00Z"},
		destroyResult: &client.DestroyPlaygroundResult{ClusterID: playgroundTestClusterID, Phase: "deprovisioning"},
		resizeResult:  &client.ResizePlaygroundResult{ClusterID: playgroundTestClusterID},
	}
	withPlaygroundMock(t, mock)

	captureStdout(t, func() {
		if err := clusterPlaygroundStatusCmd.RunE(clusterPlaygroundStatusCmd, []string{"Playground"}); err != nil {
			t.Fatalf("status by name failed: %v", err)
		}
	})
	if mock.statusRequested != playgroundTestClusterID {
		t.Errorf("status must resolve the name to the id, requested %q", mock.statusRequested)
	}
	captureStdout(t, func() {
		if err := clusterPlaygroundDestroyCmd.RunE(clusterPlaygroundDestroyCmd, []string{"playground"}); err != nil {
			t.Fatalf("destroy by name failed: %v", err)
		}
	})
	if mock.destroyRequested != playgroundTestClusterID {
		t.Errorf("destroy must resolve the name to the id, requested %q", mock.destroyRequested)
	}
	clusterPlaygroundResizeSize = "small"
	t.Cleanup(func() { clusterPlaygroundResizeSize = "" })
	captureStdout(t, func() {
		if err := clusterPlaygroundResizeCmd.RunE(clusterPlaygroundResizeCmd, []string{"playground"}); err != nil {
			t.Fatalf("resize by name failed: %v", err)
		}
	})
	if !strings.HasPrefix(mock.resizeRequested, playgroundTestClusterID+":") {
		t.Errorf("resize must resolve the name to the id, requested %q", mock.resizeRequested)
	}
}

func TestPlaygroundCommandsNameAnUnknownClusterInsteadOfA404(t *testing.T) {
	mock := &playgroundMock{clusters: []client.ClusterListItem{{ID: playgroundTestClusterID, Name: "playground"}}}
	withPlaygroundMock(t, mock)

	err := clusterPlaygroundStatusCmd.RunE(clusterPlaygroundStatusCmd, []string{"sandbox"})
	if err == nil {
		t.Fatal("an unknown name must be refused before the request")
	}
	for _, expected := range []string{`cluster "sandbox" not found`, "ankra cluster list"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("expected %q in the error, got %q", expected, err.Error())
		}
	}
	if mock.statusRequested != "" {
		t.Errorf("no request must be made for an unresolved name, got %q", mock.statusRequested)
	}
}
