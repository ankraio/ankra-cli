package cmd

import (
	"bytes"
	"encoding/json"
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
	clusterPlaygroundDestroyCmd.SetIn(strings.NewReader("y\n"))
	t.Cleanup(func() {
		clusterPlaygroundDestroyCmd.SetOut(nil)
		clusterPlaygroundDestroyCmd.SetErr(nil)
		clusterPlaygroundDestroyCmd.SetIn(nil)
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

// Destroy is irreversible - the tenant's storage goes with it - so it has to
// ask like every other destructive verb does (ankra-stril). Declining is the
// shared cancelled error (exit 4) and must not reach the API at all.
func TestPlaygroundDestroyAsksFirstAndADeclineNeverReachesTheAPI(t *testing.T) {
	mock := &playgroundMock{
		destroyResult: &client.DestroyPlaygroundResult{ClusterID: playgroundTestClusterID, Phase: "deprovisioning"},
	}
	withPlaygroundMock(t, mock)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	clusterPlaygroundDestroyCmd.SetOut(stdout)
	clusterPlaygroundDestroyCmd.SetErr(stderr)
	clusterPlaygroundDestroyCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() {
		clusterPlaygroundDestroyCmd.SetOut(nil)
		clusterPlaygroundDestroyCmd.SetErr(nil)
		clusterPlaygroundDestroyCmd.SetIn(nil)
	})

	runError := clusterPlaygroundDestroyCmd.RunE(clusterPlaygroundDestroyCmd, []string{playgroundTestClusterID})
	if !errors.Is(runError, errCancelled) {
		t.Fatalf("a declined prompt must return errCancelled, got %v", runError)
	}
	if mock.destroyRequested != "" {
		t.Errorf("a declined destroy must not call the API, but it asked for %q", mock.destroyRequested)
	}
	// The prompt is human text, so it belongs on stderr: a `-o json` caller
	// must never find it mixed into the document on stdout.
	if !strings.Contains(stderr.String(), "Destroy playground") || !strings.Contains(stderr.String(), "[y/N]") {
		t.Errorf("expected the confirmation prompt on stderr, got: %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("a declined destroy must leave stdout empty, got: %s", stdout.String())
	}
}

// A name is resolved to an id before the request, so the prompt has to name
// the resolved cluster too: what the user confirms must be what the API is
// asked to destroy, not just the word they typed.
func TestPlaygroundDestroyPromptNamesTheResolvedClusterID(t *testing.T) {
	mock := &playgroundMock{
		clusters: []client.ClusterListItem{{ID: playgroundTestClusterID, Name: "playground"}},
	}
	withPlaygroundMock(t, mock)
	output := new(bytes.Buffer)
	clusterPlaygroundDestroyCmd.SetErr(output)
	clusterPlaygroundDestroyCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() {
		clusterPlaygroundDestroyCmd.SetErr(nil)
		clusterPlaygroundDestroyCmd.SetIn(nil)
	})

	runError := clusterPlaygroundDestroyCmd.RunE(clusterPlaygroundDestroyCmd, []string{"playground"})
	if !errors.Is(runError, errCancelled) {
		t.Fatalf("expected the declined prompt, got %v", runError)
	}
	for _, expected := range []string{`"playground"`, "cluster " + playgroundTestClusterID} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("expected %s in the prompt, got: %s", expected, output.String())
		}
	}
}

// --yes is the scripting escape hatch: no prompt is printed and stdin is
// never read, so a script with a closed stdin still tears down.
func TestPlaygroundDestroyYesSkipsThePrompt(t *testing.T) {
	mock := &playgroundMock{
		destroyResult: &client.DestroyPlaygroundResult{ClusterID: playgroundTestClusterID, Phase: "deprovisioning"},
	}
	withPlaygroundMock(t, mock)
	output := new(bytes.Buffer)
	clusterPlaygroundDestroyCmd.SetOut(output)
	// An empty stdin would read as a decline if the prompt ran.
	clusterPlaygroundDestroyCmd.SetIn(strings.NewReader(""))
	if err := clusterPlaygroundDestroyCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatalf("setting --yes: %v", err)
	}
	t.Cleanup(func() {
		clusterPlaygroundDestroyCmd.SetOut(nil)
		clusterPlaygroundDestroyCmd.SetIn(nil)
		_ = clusterPlaygroundDestroyCmd.Flags().Set("yes", "false")
	})

	if runError := clusterPlaygroundDestroyCmd.RunE(clusterPlaygroundDestroyCmd, []string{playgroundTestClusterID}); runError != nil {
		t.Fatalf("destroy --yes failed: %v", runError)
	}
	if mock.destroyRequested != playgroundTestClusterID {
		t.Errorf("destroy --yes asked for %q, want "+playgroundTestClusterID, mock.destroyRequested)
	}
	if strings.Contains(output.String(), "[y/N]") {
		t.Errorf("--yes must not print the prompt, got: %s", output.String())
	}
}

// Status is the command a script polls in a loop, so it is the one that most
// needs a parseable shape: -o json emits the API's status document verbatim
// and none of the human labels.
func TestPlaygroundStatusRendersJSONOnRequest(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{status: &client.PlaygroundStatus{
		ClusterID: playgroundTestClusterID,
		Phase:     "ready",
		ExpiresAt: "2026-08-14T09:00:00Z",
		Plan:      &client.PlaygroundOrderedPlan{ID: "trial", DisplayName: "Trial", Vcpus: 1, MemoryGB: 2},
	}})
	if err := clusterPlaygroundStatusCmd.Flags().Set("output", "json"); err != nil {
		t.Fatalf("setting -o json: %v", err)
	}
	// The structured renderer writes to the command's own writer, which
	// falls back to whatever a parent command last had set - so capture it
	// here rather than on os.Stdout, or the test depends on run order.
	buffer := &strings.Builder{}
	clusterPlaygroundStatusCmd.SetOut(buffer)
	t.Cleanup(func() {
		clusterPlaygroundStatusCmd.SetOut(nil)
		_ = clusterPlaygroundStatusCmd.Flags().Set("output", "")
	})

	if err := clusterPlaygroundStatusCmd.RunE(clusterPlaygroundStatusCmd, []string{playgroundTestClusterID}); err != nil {
		t.Fatalf("status -o json failed: %v", err)
	}
	output := buffer.String()
	var decoded struct {
		ClusterID string `json:"cluster_id"`
		Phase     string `json:"phase"`
		Plan      *struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("-o json must emit a JSON document, got %v from: %s", err, output)
	}
	if decoded.ClusterID != playgroundTestClusterID || decoded.Phase != "ready" || decoded.Plan == nil || decoded.Plan.ID != "trial" {
		t.Errorf("unexpected JSON document: %s", output)
	}
	if strings.Contains(output, "Cluster ID:") {
		t.Errorf("-o json must not mix in the human labels, got: %s", output)
	}
}

func TestPlaygroundDestroySurfacesTheServerError(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{destroyError: errors.New("Playground not found.")})
	clusterPlaygroundDestroyCmd.SetIn(strings.NewReader("y\n"))
	t.Cleanup(func() { clusterPlaygroundDestroyCmd.SetIn(nil) })
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
	clusterPlaygroundDestroyCmd.SetIn(strings.NewReader("y\n"))
	t.Cleanup(func() { clusterPlaygroundDestroyCmd.SetIn(nil) })
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
