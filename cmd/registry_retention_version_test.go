package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"ankra/internal/client"
)

const retentionChangedText = "Retention changed since you read it. Reload it and make your change again."

// versionedRetentionMock stores a retention with a version and checks
// expected_version the way the platform does: a mismatch is a 409 and
// nothing is written. concurrentWrite, when set, is another session's
// write landing just before each of the CLI's writes, for the first
// concurrentWrites writes.
type versionedRetentionMock struct {
	registryStorageMock
	stored           client.RegistryRetention
	revision         int
	gets             int
	concurrentWrite  func(stored *client.RegistryRetention)
	concurrentWrites int
}

func newVersionedRetentionMock() *versionedRetentionMock {
	mock := &versionedRetentionMock{stored: *registryRetentionFixture(), revision: 1}
	mock.stored.Version = "v1"
	return mock
}

func copyRetention(retention client.RegistryRetention) *client.RegistryRetention {
	copied := retention
	copied.Rules = slices.Clone(retention.Rules)
	return &copied
}

func (mock *versionedRetentionMock) bump() {
	mock.revision++
	mock.stored.Version = fmt.Sprintf("v%d", mock.revision)
}

func (mock *versionedRetentionMock) GetRegistryRetention(context.Context) (*client.RegistryRetention, error) {
	mock.gets++
	return copyRetention(mock.stored), nil
}

func (mock *versionedRetentionMock) UpdateRegistryRetention(_ context.Context, update client.RegistryRetentionUpdate) (*client.RegistryRetentionUpdateResult, error) {
	mock.updates = append(mock.updates, update)
	if mock.concurrentWrite != nil && mock.concurrentWrites > 0 {
		mock.concurrentWrites--
		mock.concurrentWrite(&mock.stored)
		mock.bump()
	}
	if update.ExpectedVersion != "" && update.ExpectedVersion != mock.stored.Version {
		return nil, client.NewUnexpectedResponseError(409, retentionChangedText)
	}
	if update.DefaultSet {
		if update.Default == nil {
			mock.stored.Default = mock.stored.PlatformDefault
		} else {
			mock.stored.Default = *update.Default
		}
	}
	if update.RulesSet {
		mock.stored.Rules = nil
		for _, rule := range update.Rules {
			mock.stored.Rules = append(mock.stored.Rules, client.RegistryRetentionRule{
				ID: "id-" + rule.RepositoryPattern, RepositoryPattern: rule.RepositoryPattern,
				ApplicationID: rule.ApplicationID, KeepDays: rule.KeepDays, KeepLatest: rule.KeepLatest,
			})
		}
	}
	mock.bump()
	return &client.RegistryRetentionUpdateResult{Retention: *copyRetention(mock.stored), HarborApplied: true}, nil
}

func rulePatterns(rules []client.RegistryRetentionRuleInput) []string {
	patterns := make([]string, 0, len(rules))
	for _, rule := range rules {
		patterns = append(patterns, rule.RepositoryPattern)
	}
	return patterns
}

// The write carries the version the change was computed from, and the
// structured answer carries the new one.
func TestRetentionWriteCarriesTheReadVersion(t *testing.T) {
	mock := newVersionedRetentionMock()
	stdout, _, runError := runRegistryCommandSplit(t, mock, "", "storage", "retention", "rule", "add", "mobile/**",
		"--keep-days", "45", "-o", "json")
	if runError != nil {
		t.Fatalf("rule add: %v", runError)
	}
	if len(mock.updates) != 1 || mock.updates[0].ExpectedVersion != "v1" {
		t.Fatalf("the write must carry the read version: %+v", mock.updates)
	}
	encoded, _ := json.Marshal(mock.updates[0])
	if !strings.Contains(string(encoded), `"expected_version":"v1"`) {
		t.Fatalf("body = %s", encoded)
	}
	var decoded client.RegistryRetentionUpdateResult
	if json.Unmarshal([]byte(stdout), &decoded) != nil || decoded.Retention.Version != "v2" {
		t.Fatalf("-o json must carry the new version:\n%s", stdout)
	}
}

// Someone else adds a rule between the read and the write: the platform
// refuses, the CLI reads again and adds its rule to the list as it is now,
// so neither change is lost.
func TestRetentionRuleAddReappliesOnceAfterAConcurrentChange(t *testing.T) {
	mock := newVersionedRetentionMock()
	mock.concurrentWrites = 1
	mock.concurrentWrite = func(stored *client.RegistryRetention) {
		stored.Rules = append(stored.Rules, client.RegistryRetentionRule{ID: "other", RepositoryPattern: "docs/*", KeepDays: 10, KeepLatest: 2})
	}
	stdout, stderr, runError := runRegistryCommandSplit(t, mock, "", "storage", "retention", "rule", "add", "mobile/**", "--keep-days", "45")
	if runError != nil {
		t.Fatalf("rule add: %v\n%s", runError, stderr)
	}
	if mock.gets != 2 || len(mock.updates) != 2 {
		t.Fatalf("one re-read and one re-write expected: gets=%d writes=%d", mock.gets, len(mock.updates))
	}
	if mock.updates[0].ExpectedVersion != "v1" || mock.updates[1].ExpectedVersion != "v2" {
		t.Fatalf("each write carries the version it was computed from: %q, %q",
			mock.updates[0].ExpectedVersion, mock.updates[1].ExpectedVersion)
	}
	if got := rulePatterns(mock.updates[1].Rules); !slices.Equal(got, []string{"ankra-ci/**", "web/*", "docs/*", "mobile/**"}) {
		t.Fatalf("the re-applied write must keep the other session's rule: %v", got)
	}
	mustContain(t, stderr, "changed since it was read")
	mustContain(t, stdout, "Rule for mobile/** added.")
}

// A second refusal fails with the platform's words, exit 1, after exactly
// two reads and two writes.
func TestRetentionWriteFailsWithThePlatformTextAfterTheSecondConflict(t *testing.T) {
	for _, arguments := range [][]string{
		{"storage", "retention", "rule", "add", "mobile/**", "--keep-days", "45"},
		{"storage", "retention", "rule", "remove", "ankra-ci/**"},
		{"storage", "retention", "set", "--keep-days", "60"},
	} {
		mock := newVersionedRetentionMock()
		mock.concurrentWrites = 5
		mock.concurrentWrite = func(stored *client.RegistryRetention) {}
		_, _, runError := runRegistryCommandSplit(t, mock, "", arguments...)
		if runError == nil || runError.Error() != retentionChangedText || exitCodeFor(runError) != exitError {
			t.Fatalf("%v: want the platform's text verbatim with exit 1, got %v (exit %d)", arguments, runError, exitCodeFor(runError))
		}
		if mock.gets != 2 || len(mock.updates) != 2 {
			t.Fatalf("%v: one retry only: gets=%d writes=%d", arguments, mock.gets, len(mock.updates))
		}
	}
}

// 'retention set' is a delta too: it changes only the flags given, so a
// concurrent change to the other field survives the re-apply.
func TestRetentionSetReappliesOnlyItsOwnField(t *testing.T) {
	mock := newVersionedRetentionMock()
	mock.concurrentWrites = 1
	mock.concurrentWrite = func(stored *client.RegistryRetention) { stored.Default.KeepLatest = 9 }
	if _, _, runError := runRegistryCommandSplit(t, mock, "", "storage", "retention", "set", "--keep-days", "60"); runError != nil {
		t.Fatalf("set: %v", runError)
	}
	last := mock.updates[len(mock.updates)-1]
	if last.Default == nil || last.Default.KeepDays != 60 || last.Default.KeepLatest != 9 || last.RulesSet {
		t.Fatalf("the re-applied set must keep the concurrent keep_latest and never send rules: %+v", last)
	}
}

// Removing a rule re-applies against the fresh list; a rule someone else
// already removed is not found (exit 3), not written.
func TestRetentionRuleRemoveReappliesAgainstTheFreshList(t *testing.T) {
	mock := newVersionedRetentionMock()
	mock.concurrentWrites = 1
	mock.concurrentWrite = func(stored *client.RegistryRetention) {
		stored.Rules = append(stored.Rules, client.RegistryRetentionRule{ID: "other", RepositoryPattern: "docs/*", KeepDays: 10, KeepLatest: 2})
	}
	if _, _, runError := runRegistryCommandSplit(t, mock, "", "storage", "retention", "rule", "remove", "ankra-ci/**"); runError != nil {
		t.Fatalf("rule remove: %v", runError)
	}
	if got := rulePatterns(mock.updates[1].Rules); !slices.Equal(got, []string{"web/*", "docs/*"}) {
		t.Fatalf("the re-applied removal must keep the other session's rule: %v", got)
	}

	gone := newVersionedRetentionMock()
	gone.concurrentWrites = 1
	gone.concurrentWrite = func(stored *client.RegistryRetention) { stored.Rules = stored.Rules[1:] }
	_, _, runError := runRegistryCommandSplit(t, gone, "", "storage", "retention", "rule", "remove", "ankra-ci/**")
	if exitCodeFor(runError) != exitNotFound || len(gone.updates) != 1 {
		t.Fatalf("a rule removed meanwhile is not found and not written again: %v writes=%d", runError, len(gone.updates))
	}
}

// A platform that answers no version gets no expected_version, and a 409
// from it is not retried: there is no version to re-read against.
func TestRetentionWriteWithoutAVersionSendsNoCheck(t *testing.T) {
	mock := &registryStorageMock{}
	if _, runError := runRegistryCommand(t, mock, "", "storage", "retention", "rule", "add", "mobile/**", "--keep-days", "45"); runError != nil {
		t.Fatalf("rule add: %v", runError)
	}
	encoded, _ := json.Marshal(mock.updates[0])
	if strings.Contains(string(encoded), "expected_version") {
		t.Fatalf("no version read, no check sent: %s", encoded)
	}

	refusing := &registryStorageRefusingMock{refusal: client.NewUnexpectedResponseError(409, "something else conflicts")}
	_, runError := runRegistryCommand(t, refusing, "", "storage", "retention", "rule", "add", "mobile/**", "--keep-days", "45")
	if runError == nil || runError.Error() != "something else conflicts" {
		t.Fatalf("a 409 without a version is relayed once: %v", runError)
	}
}

// The structured reads carry the version.
func TestRetentionReadsCarryTheVersion(t *testing.T) {
	mock := newVersionedRetentionMock()
	stdout, _, runError := runRegistryCommandSplit(t, mock, "", "storage", "retention", "-o", "json")
	var shown client.RegistryRetention
	if runError != nil || json.Unmarshal([]byte(stdout), &shown) != nil || shown.Version != "v1" {
		t.Fatalf("retention -o json: %v\n%s", runError, stdout)
	}
	stdout, _, runError = runRegistryCommandSplit(t, mock, "", "storage", "retention", "rule", "list", "-o", "json")
	var listed struct {
		Version string `json:"version"`
	}
	if runError != nil || json.Unmarshal([]byte(stdout), &listed) != nil || listed.Version != "v1" {
		t.Fatalf("rule list -o json: %v\n%s", runError, stdout)
	}
}
