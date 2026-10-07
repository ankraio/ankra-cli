package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type registryStorageMock struct {
	baseMock
	storage      *client.RegistryStorage
	repositories *client.RegistryStorageRepositories
	retention    *client.RegistryRetention
	// updates is every retention write, in order.
	updates       []client.RegistryRetentionUpdate
	harborApplied bool
	run           *client.RegistryRetentionRun
	limitRequests *client.LimitRequestList
	// submitted is the storage request sent, when one was.
	submittedKind  string
	submittedValue int64
	submittedWhy   string
	submitError    error
}

func gib(n int64) int64 { return n * bytesPerGiB }

func int64Pointer(value int64) *int64 { return &value }

func registryStorageFixture(used *int64, status string) *client.RegistryStorage {
	return &client.RegistryStorage{
		OrganisationID: "org-1", LimitBytes: gib(50), DefaultLimitBytes: gib(50), Source: "default",
		UsedBytes: used, UsageStatus: status,
		Projects: []client.RegistryStorageProject{{Name: "default", Project: "org-1", LimitBytes: int64Pointer(gib(50)),
			UsedBytes: used, Status: "ok"}},
	}
}

func registryRetentionFixture() *client.RegistryRetention {
	updatedBy := "ops@example.com"
	return &client.RegistryRetention{
		OrganisationID:  "org-1",
		Default:         client.RegistryRetentionPolicy{KeepDays: 30, KeepLatest: 3},
		PlatformDefault: client.RegistryRetentionPolicy{KeepDays: 30, KeepLatest: 3},
		Source:          "organisation",
		UpdatedBy:       &updatedBy,
		Rules: []client.RegistryRetentionRule{
			{ID: "rule-ci", RepositoryPattern: "ankra-ci/**", KeepDays: 7, KeepLatest: 2},
			{ID: "rule-web", RepositoryPattern: "web/*", KeepDays: 60, KeepLatest: 5},
		},
		RecentlyPulledDays: 7,
		RunsDailyAt:        "14:37 UTC",
		SpaceFreedAt:       "weekly, Sunday 03:00 UTC",
	}
}

func (mock *registryStorageMock) GetRegistryStorage(context.Context) (*client.RegistryStorage, error) {
	if mock.storage == nil {
		return registryStorageFixture(int64Pointer(gib(20)), client.RegistryStorageUsageComplete), nil
	}
	return mock.storage, nil
}

func (mock *registryStorageMock) ListRegistryStorageRepositories(context.Context) (*client.RegistryStorageRepositories, error) {
	return mock.repositories, nil
}

func (mock *registryStorageMock) GetRegistryRetention(context.Context) (*client.RegistryRetention, error) {
	if mock.retention == nil {
		mock.retention = registryRetentionFixture()
	}
	return mock.retention, nil
}

func (mock *registryStorageMock) UpdateRegistryRetention(_ context.Context, update client.RegistryRetentionUpdate) (*client.RegistryRetentionUpdateResult, error) {
	mock.updates = append(mock.updates, update)
	retention := *registryRetentionFixture()
	return &client.RegistryRetentionUpdateResult{Retention: retention, HarborApplied: mock.harborApplied}, nil
}

func (mock *registryStorageMock) RunRegistryRetention(context.Context) (*client.RegistryRetentionRun, error) {
	return mock.run, nil
}

func (mock *registryStorageMock) ListLimitRequests() (*client.LimitRequestList, error) {
	if mock.limitRequests == nil {
		return &client.LimitRequestList{}, nil
	}
	return mock.limitRequests, nil
}

func (mock *registryStorageMock) SubmitLimitRequest(kind string, value int64, justification string) (*client.LimitRequest, error) {
	if mock.submitError != nil {
		return nil, mock.submitError
	}
	mock.submittedKind, mock.submittedValue, mock.submittedWhy = kind, value, justification
	return &client.LimitRequest{ID: "lr-1", LimitKind: kind, RequestedValue: value, Justification: justification, Status: "pending"}, nil
}

func mustContain(t *testing.T, output string, expected ...string) {
	t.Helper()
	for _, text := range expected {
		if !strings.Contains(output, text) {
			t.Fatalf("output lacks %q:\n%s", text, output)
		}
	}
}

func mustNotContain(t *testing.T, output string, forbidden ...string) {
	t.Helper()
	for _, text := range forbidden {
		if strings.Contains(output, text) {
			t.Fatalf("output must not contain %q:\n%s", text, output)
		}
	}
}

func TestRegistryStorageShowStates(t *testing.T) {
	cases := []struct {
		name      string
		storage   *client.RegistryStorage
		expected  []string
		forbidden []string
	}{
		{"healthy", registryStorageFixture(int64Pointer(gib(20)), client.RegistryStorageUsageComplete),
			[]string{"20 GiB of 50 GiB (40%)", "[############------------------]", "50 GiB per registry project (the platform default)"},
			[]string{"Free space", "lower bound"}},
		{"nearly full", registryStorageFixture(int64Pointer(gib(42)), client.RegistryStorageUsageComplete),
			[]string{"(84%)", "84% full", "ankra registry storage retention set", "ankra registry storage request --size"},
			nil},
		{"full", registryStorageFixture(int64Pointer(gib(52)+gib(1)/2), client.RegistryStorageUsageComplete),
			[]string{"52.5 GiB of 50 GiB (105%)", "every image push is refused", "Ask for more"}, nil},
		{"unknown", registryStorageFixture(nil, client.RegistryStorageUsageUnknown),
			[]string{"Used:   unknown", "does not mean empty"}, []string{"0 B", "Free space"}},
		{"partial", registryStorageFixture(int64Pointer(gib(41)), client.RegistryStorageUsagePartial),
			[]string{"at least 41 GiB of 50 GiB (82%)", "lower bound"}, nil},
		{"not provisioned", registryStorageFixture(int64Pointer(0), client.RegistryStorageUsageNotProvisioned),
			[]string{"no registry project"}, []string{"Free space"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			output, runError := runRegistryCommand(t, &registryStorageMock{storage: testCase.storage}, "", "storage")
			if runError != nil {
				t.Fatalf("storage: %v", runError)
			}
			mustContain(t, output, testCase.expected...)
			mustNotContain(t, output, testCase.forbidden...)
		})
	}
}

func TestRegistryStorageShowOrganisationLimitAndProjects(t *testing.T) {
	reason := "Approved storage request"
	updatedAt := "2026-10-01T08:00:00Z"
	storage := registryStorageFixture(int64Pointer(gib(30)), client.RegistryStorageUsageComplete)
	storage.LimitBytes, storage.Source, storage.Reason, storage.UpdatedAt = gib(200), "organisation", &reason, &updatedAt
	storage.Projects = []client.RegistryStorageProject{
		{Name: "default", Project: "org-1", UsedBytes: int64Pointer(gib(190)), Status: "ok"},
		{Name: "staging", Project: "org-1-staging", Status: "unknown"},
	}
	requestedAt := "2026-10-05T10:00:00Z"
	mock := &registryStorageMock{storage: storage, limitRequests: &client.LimitRequestList{Requests: []client.LimitRequest{
		{LimitKind: "ai_tokens", RequestedValue: 5000, Status: "approved"},
		{LimitKind: "registry_storage", RequestedValue: 400, Status: "pending", RequestedAt: &requestedAt},
	}}}
	output, runError := runRegistryCommand(t, mock, "", "storage", "show")
	if runError != nil {
		t.Fatalf("storage show: %v", runError)
	}
	mustContain(t, output, "200 GiB per registry project, set for this organisation by Ankra (Approved storage request, since 2026-10-01 08:00 UTC)",
		"staging", "unknown", "95%", "Storage request: pending, 400 GiB per project (asked 2026-10-05 10:00 UTC)",
		"The registry is 95% full")
}

func TestRegistryStorageShowJSONIsTheAPIAnswer(t *testing.T) {
	stdout, stderr, runError := runRegistryCommandSplit(t, &registryStorageMock{storage: registryStorageFixture(nil, client.RegistryStorageUsageUnknown)},
		"", "storage", "-o", "json")
	if runError != nil {
		t.Fatalf("storage -o json: %v", runError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", unmarshalError, stdout)
	}
	if decoded["used_bytes"] != nil || decoded["usage_status"] != "unknown" || decoded["limit_bytes"] != float64(gib(50)) {
		t.Fatalf("decoded = %v", decoded)
	}
	mustNotContain(t, stderr, "Free space")
}

func TestRegistryStorageRepositoriesTable(t *testing.T) {
	rule := "rule-ci"
	unknownRule := "rule-gone"
	mock := &registryStorageMock{repositories: &client.RegistryStorageRepositories{
		Status: "complete", SizesAreApproximate: true,
		Repositories: []client.RegistryStorageRepository{
			{Name: "ankra-ci/api", Project: "default", ArtifactCount: 412, SizeBytes: gib(31), LastPushedAt: "2026-10-06T22:10:00Z",
				Truncated: true, RetentionRuleID: &rule},
			{Name: "investor-update/api", Project: "default", ArtifactCount: 12, SizeBytes: gib(3)},
			{Name: "web/site", Project: "default", ArtifactCount: 3, SizeBytes: 300 << 20, RetentionRuleID: &unknownRule},
		},
	}}
	output, runError := runRegistryCommand(t, mock, "", "storage", "repositories", "--limit", "2")
	if runError != nil {
		t.Fatalf("repositories: %v", runError)
	}
	mustContain(t, output, "ankra-ci/api  (CI)", ">= 31 GiB", "rule ankra-ci/**", "org policy", "2026-10-06 22:10 UTC",
		"Showing the 2 largest of 3", "Sizes are approximate", "retention rule add 'ankra-ci/**'")
	mustNotContain(t, output, "web/site")

	output, runError = runRegistryCommand(t, mock, "", "storage", "repositories", "--limit", "0")
	if runError != nil {
		t.Fatalf("repositories --limit 0: %v", runError)
	}
	mustContain(t, output, "web/site", "300 MiB", "rule rule-gone")

	stdout, _, runError := runRegistryCommandSplit(t, mock, "", "storage", "repos", "--limit", "1", "-o", "json")
	if runError != nil {
		t.Fatalf("repositories -o json: %v", runError)
	}
	var decoded client.RegistryStorageRepositories
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil || len(decoded.Repositories) != 1 {
		t.Fatalf("stdout = %s (%v)", stdout, unmarshalError)
	}

	unknown := &registryStorageMock{repositories: &client.RegistryStorageRepositories{Status: "unknown"}}
	output, _ = runRegistryCommand(t, unknown, "", "storage", "repositories")
	mustContain(t, output, "could not be read")
}

func TestRegistryRetentionShow(t *testing.T) {
	output, runError := runRegistryCommand(t, &registryStorageMock{}, "", "storage", "retention")
	if runError != nil {
		t.Fatalf("retention: %v", runError)
	}
	mustContain(t, output, "set for this organisation",
		"keep images pushed in the last 30 days, and always the 3 newest images of each repository",
		"ankra-ci/**", "web/*", "the newest image of every repository", "pulled in the last 7 days",
		"daily at 14:37 UTC", "weekly, Sunday 03:00 UTC")

	stdout, _, runError := runRegistryCommandSplit(t, &registryStorageMock{}, "", "storage", "retention", "show", "-o", "json")
	if runError != nil {
		t.Fatalf("retention show -o json: %v", runError)
	}
	var decoded client.RegistryRetention
	if unmarshalError := json.Unmarshal([]byte(stdout), &decoded); unmarshalError != nil || decoded.RunsDailyAt != "14:37 UTC" {
		t.Fatalf("stdout = %s (%v)", stdout, unmarshalError)
	}
}

func TestRegistryRetentionSetSendsOnlyTheDefault(t *testing.T) {
	mock := &registryStorageMock{harborApplied: true}
	output, runError := runRegistryCommand(t, mock, "", "storage", "retention", "set", "--keep-days", "60")
	if runError != nil {
		t.Fatalf("set (looser, no prompt): %v", runError)
	}
	if len(mock.updates) != 1 {
		t.Fatalf("updates = %+v", mock.updates)
	}
	update := mock.updates[0]
	if !update.DefaultSet || update.RulesSet || update.Default == nil || update.Default.KeepDays != 60 || update.Default.KeepLatest != 3 {
		t.Fatalf("a set must keep the unnamed field and never touch the rules: %+v", update)
	}
	encoded, _ := json.Marshal(update)
	if string(encoded) != `{"default":{"keep_days":60,"keep_latest":3}}` {
		t.Fatalf("body = %s", encoded)
	}
	mustContain(t, output, "Retention policy updated.", "The registry has it now.")
}

func TestRegistryRetentionSetConfirmsBeforeTightening(t *testing.T) {
	mock := &registryStorageMock{}
	_, runError := runRegistryCommand(t, mock, "n\n", "storage", "retention", "set", "--keep-days", "7")
	if exitCodeFor(runError) != exitCancelled || len(mock.updates) != 0 {
		t.Fatalf("a declined tightening must not write: error=%v updates=%v", runError, mock.updates)
	}
	output, runError := runRegistryCommand(t, mock, "y\n", "storage", "retention", "set", "--keep-latest", "1")
	if runError != nil || len(mock.updates) != 1 {
		t.Fatalf("confirmed tightening: error=%v updates=%v", runError, mock.updates)
	}
	mustContain(t, output, "keeps fewer images", "Now: keep images pushed in the last 30 days", "New: keep images pushed in the last 30 days, and always the newest image",
		"Saved. The registry picks it up automatically within about 5 minutes.")
	if _, runError := runRegistryCommand(t, mock, "", "storage", "retention", "set", "--keep-days", "0", "--yes"); runError != nil || len(mock.updates) != 2 {
		t.Fatalf("--yes must skip the prompt: error=%v", runError)
	}
}

func TestRegistryRetentionSetResetAndUsage(t *testing.T) {
	mock := &registryStorageMock{}
	mock.retention = registryRetentionFixture()
	mock.retention.Default = client.RegistryRetentionPolicy{KeepDays: 90, KeepLatest: 10}
	if _, runError := runRegistryCommand(t, mock, "n\n", "storage", "retention", "set", "--reset"); exitCodeFor(runError) != exitCancelled {
		t.Fatalf("a reset that keeps fewer images must ask first: %v", runError)
	}
	output, runError := runRegistryCommand(t, mock, "", "storage", "retention", "set", "--reset", "--yes")
	if runError != nil || len(mock.updates) != 1 {
		t.Fatalf("reset: %v", runError)
	}
	encoded, _ := json.Marshal(mock.updates[0])
	if string(encoded) != `{"default":null}` {
		t.Fatalf("a reset sends default null and no rules, got %s", encoded)
	}
	mustContain(t, output, "reset to the platform default")

	for _, arguments := range [][]string{
		{"storage", "retention", "set"},
		{"storage", "retention", "set", "--reset", "--keep-days", "3"},
		{"storage", "retention", "set", "--keep-latest", "0"},
		{"storage", "retention", "set", "--keep-days", "4000"},
	} {
		if _, runError := runRegistryCommand(t, mock, "", arguments...); exitCodeFor(runError) != exitUsage {
			t.Fatalf("%v must be a usage error, got %v", arguments, runError)
		}
	}
	if len(mock.updates) != 1 {
		t.Fatalf("a usage error must not write: %v", mock.updates)
	}
}

func TestRegistryRetentionRuleAddSendsTheWholeList(t *testing.T) {
	mock := &registryStorageMock{}
	output, runError := runRegistryCommand(t, mock, "", "storage", "retention", "rule", "add", "Mobile/**", "--keep-days", "45",
		"--keep-latest", "4", "--application", "app-9")
	if runError != nil {
		t.Fatalf("rule add: %v", runError)
	}
	if len(mock.updates) != 1 {
		t.Fatalf("updates = %v", mock.updates)
	}
	encoded, _ := json.Marshal(mock.updates[0])
	want := `{"rules":[{"repository_pattern":"ankra-ci/**","keep_days":7,"keep_latest":2},` +
		`{"repository_pattern":"web/*","keep_days":60,"keep_latest":5},` +
		`{"repository_pattern":"mobile/**","application_id":"app-9","keep_days":45,"keep_latest":4}]}`
	if string(encoded) != want {
		t.Fatalf("a rule add must send every existing rule plus the new one, and never the default:\n got %s\nwant %s", encoded, want)
	}
	mustContain(t, output, "Rule for mobile/** added.")
}

func TestRegistryRetentionRuleAddUpdatesAndConfirms(t *testing.T) {
	mock := &registryStorageMock{}
	if _, runError := runRegistryCommand(t, mock, "n\n", "storage", "retention", "rule", "add", "web/*", "--keep-days", "14"); exitCodeFor(runError) != exitCancelled {
		t.Fatalf("a tighter rule must ask first: %v", runError)
	}
	output, runError := runRegistryCommand(t, mock, "", "storage", "retention", "rule", "add", "web/*", "--keep-latest", "8")
	if runError != nil || len(mock.updates) != 1 {
		t.Fatalf("looser rule update: %v", runError)
	}
	rules := mock.updates[0].Rules
	if len(rules) != 2 || rules[1].RepositoryPattern != "web/*" || rules[1].KeepDays != 60 || rules[1].KeepLatest != 8 {
		t.Fatalf("an update must keep the unnamed field and the list's order: %+v", rules)
	}
	mustContain(t, output, "Rule for web/* updated.")

	for _, pattern := range []string{"**", " "} {
		if _, runError := runRegistryCommand(t, mock, "", "storage", "retention", "rule", "add", pattern, "--keep-days", "3"); exitCodeFor(runError) != exitUsage {
			t.Fatalf("pattern %q must be refused: %v", pattern, runError)
		}
	}
}

func TestRegistryRetentionRuleRemove(t *testing.T) {
	mock := &registryStorageMock{}
	// Removing the CI rule (7 days) puts those repositories on the 30-day
	// policy, which keeps more: no prompt.
	output, runError := runRegistryCommand(t, mock, "", "storage", "retention", "rule", "remove", "ankra-ci/**")
	if runError != nil || len(mock.updates) != 1 {
		t.Fatalf("rule remove: %v", runError)
	}
	encoded, _ := json.Marshal(mock.updates[0])
	if string(encoded) != `{"rules":[{"repository_pattern":"web/*","keep_days":60,"keep_latest":5}]}` {
		t.Fatalf("body = %s", encoded)
	}
	mustContain(t, output, "Rule for ankra-ci/** removed.")

	// Removing the web rule (60 days) falls back to 30 days: it asks.
	mock = &registryStorageMock{}
	if _, runError := runRegistryCommand(t, mock, "n\n", "storage", "retention", "rule", "rm", "web/*"); exitCodeFor(runError) != exitCancelled || len(mock.updates) != 0 {
		t.Fatalf("a removal that keeps fewer images must ask: %v", runError)
	}

	// The last rule removed sends an empty list, not an absent one.
	mock = &registryStorageMock{}
	mock.retention = registryRetentionFixture()
	mock.retention.Rules = mock.retention.Rules[:1]
	if _, runError := runRegistryCommand(t, mock, "", "storage", "retention", "rule", "remove", "ankra-ci/**"); runError != nil {
		t.Fatalf("last rule remove: %v", runError)
	}
	encoded, _ = json.Marshal(mock.updates[0])
	if string(encoded) != `{"rules":[]}` {
		t.Fatalf("removing the last rule must send [], got %s", encoded)
	}

	if _, runError := runRegistryCommand(t, &registryStorageMock{}, "", "storage", "retention", "rule", "remove", "nope/*"); exitCodeFor(runError) != exitNotFound {
		t.Fatalf("an unknown rule must be not-found: %v", runError)
	}
}

func TestRegistryRetentionRuleList(t *testing.T) {
	output, runError := runRegistryCommand(t, &registryStorageMock{}, "", "storage", "retention", "rule", "list")
	if runError != nil {
		t.Fatalf("rule list: %v", runError)
	}
	mustContain(t, output, "ankra-ci/**", "web/*", "Every other repository follows the organisation policy")
	stdout, _, runError := runRegistryCommandSplit(t, &registryStorageMock{}, "", "storage", "retention", "rule", "list", "-o", "json")
	var decoded struct {
		Rules []client.RegistryRetentionRule `json:"rules"`
	}
	if runError != nil || json.Unmarshal([]byte(stdout), &decoded) != nil || len(decoded.Rules) != 2 {
		t.Fatalf("rule list -o json: %v\n%s", runError, stdout)
	}
}

func TestRegistryRetentionRun(t *testing.T) {
	reason := "a cleanup is already running"
	mock := &registryStorageMock{run: &client.RegistryRetentionRun{
		Projects: []client.RegistryRetentionRunProject{
			{Project: "org-1", Started: true},
			{Project: "org-1-staging", Started: false, Reason: &reason},
		},
		SpaceFreedAt: "weekly, Sunday 03:00 UTC",
	}}
	output, runError := runRegistryCommand(t, mock, "", "storage", "retention", "run")
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	mustContain(t, output, "org-1: cleanup started", "org-1-staging: not started: a cleanup is already running",
		"garbage collection (weekly, Sunday 03:00 UTC)")
	stdout, _, runError := runRegistryCommandSplit(t, mock, "", "storage", "retention", "run", "-o", "json")
	var decoded client.RegistryRetentionRun
	if runError != nil || json.Unmarshal([]byte(stdout), &decoded) != nil || len(decoded.Projects) != 2 {
		t.Fatalf("run -o json: %v\n%s", runError, stdout)
	}
}

func TestRegistryStorageRequest(t *testing.T) {
	mock := &registryStorageMock{}
	output, runError := runRegistryCommand(t, mock, "", "storage", "request", "--size", "100", "--reason", "nightly images")
	if runError != nil {
		t.Fatalf("request: %v", runError)
	}
	if mock.submittedKind != "registry_storage" || mock.submittedValue != 100 || mock.submittedWhy != "nightly images" {
		t.Fatalf("submitted %q %d %q", mock.submittedKind, mock.submittedValue, mock.submittedWhy)
	}
	mustContain(t, output, "Storage request submitted (pending): 100 GiB per registry project", "within about 5 minutes")

	for _, arguments := range [][]string{
		{"storage", "request", "--size", "50", "--reason", "x"}, // not more than today's 50 GiB
		{"storage", "request", "--size", "20000", "--reason", "x"},
		{"storage", "request", "--size", "100"},
	} {
		blocked := &registryStorageMock{}
		if _, runError := runRegistryCommand(t, blocked, "", arguments...); exitCodeFor(runError) != exitUsage || blocked.submittedKind != "" {
			t.Fatalf("%v must be a usage error without a request, got %v", arguments, runError)
		}
	}

	noLimit := &registryStorageMock{storage: registryStorageFixture(int64Pointer(gib(1)), "complete")}
	noLimit.storage.LimitBytes = client.RegistryStorageNoLimit
	if _, runError := runRegistryCommand(t, noLimit, "", "storage", "request", "--size", "100", "--reason", "x"); exitCodeFor(runError) != exitUsage {
		t.Fatalf("an organisation without a limit has nothing to raise: %v", runError)
	}

	pending := &registryStorageMock{submitError: client.NewUnexpectedResponseError(409, "A request is already pending.")}
	_, runError = runRegistryCommand(t, pending, "", "storage", "request", "--size", "100", "--reason", "x")
	if runError == nil || !strings.Contains(runError.Error(), "ankra org limits list") {
		t.Fatalf("a pending request must point at the list: %v", runError)
	}

	stdout, _, runError := runRegistryCommandSplit(t, &registryStorageMock{}, "", "storage", "request", "--size", "75", "--reason", "x", "-o", "json")
	var decoded client.LimitRequest
	if runError != nil || json.Unmarshal([]byte(stdout), &decoded) != nil || decoded.RequestedValue != 75 || decoded.LimitKind != "registry_storage" {
		t.Fatalf("request -o json: %v\n%s", runError, stdout)
	}
}

func TestRegistryStorageUnservedRoute(t *testing.T) {
	previousClient := apiClient
	t.Cleanup(func() { apiClient = previousClient })
	_, runError := runRegistryCommand(t, unservedRouteClient(t), "", "storage")
	if runError == nil || !strings.Contains(runError.Error(), "does not serve registry storage yet") {
		t.Fatalf("an unregistered route must say so: %v", runError)
	}
}

func TestFormatBytesGiB(t *testing.T) {
	for size, want := range map[int64]string{
		0: "0 B", 512: "512 B", 1536: "1.5 KiB", gib(50): "50 GiB", gib(52) + gib(1)/2: "52.5 GiB", gib(2048): "2 TiB",
	} {
		if got := formatBytesGiB(size); got != want {
			t.Errorf("formatBytesGiB(%d) = %q, want %q", size, got, want)
		}
	}
}

func TestRetentionTighter(t *testing.T) {
	policy := func(days, latest int) client.RegistryRetentionPolicy {
		return client.RegistryRetentionPolicy{KeepDays: days, KeepLatest: latest}
	}
	cases := []struct {
		current, next client.RegistryRetentionPolicy
		want          bool
	}{
		{policy(30, 3), policy(60, 3), false},
		{policy(30, 3), policy(7, 3), true},
		{policy(30, 3), policy(0, 3), true},
		{policy(0, 3), policy(7, 3), false},
		{policy(30, 3), policy(30, 2), true},
		{policy(30, 3), policy(30, 3), false},
	}
	for _, testCase := range cases {
		if got := retentionTighter(testCase.current, testCase.next); got != testCase.want {
			t.Errorf("retentionTighter(%+v, %+v) = %v, want %v", testCase.current, testCase.next, got, testCase.want)
		}
	}
}
