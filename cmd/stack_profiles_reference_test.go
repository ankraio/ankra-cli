package cmd

// Regression cover for ankra-ql3t: `stack-profiles list` prints NAME and
// LATEST columns, so those are the values a user reaches for next. Before
// this, a name produced a raw pydantic uuid_parsing dump from the API and
// `--version v1` was rejected by strconv even though every Ankra surface
// prints versions as v1.

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// profileLookupMock answers the profile listing used to resolve a name.
type profileLookupMock struct {
	baseMock
	profiles  []client.StackProfileSummary
	listError error
	searched  string
}

func (mock *profileLookupMock) ListStackProfiles(_, _ int, search string, _ string) (*client.StackProfileListResponse, error) {
	mock.searched = search
	if mock.listError != nil {
		return nil, mock.listError
	}
	return &client.StackProfileListResponse{Result: mock.profiles}, nil
}

func TestResolveStackProfileIDPassesAUUIDStraightThrough(t *testing.T) {
	mock := &profileLookupMock{listError: errors.New("listing must not be called for a uuid")}
	const profileID = "3435f700-7f47-475c-9f69-864dcba502bc"

	resolved, resolveError := resolveStackProfileID(mock, profileID)

	if resolveError != nil {
		t.Fatalf("a uuid must resolve to itself: %v", resolveError)
	}
	if resolved != profileID {
		t.Fatalf("expected %q, got %q", profileID, resolved)
	}
	if mock.searched != "" {
		t.Fatal("resolving a uuid must not cost a listing round-trip")
	}
}

func TestResolveStackProfileIDResolvesAName(t *testing.T) {
	mock := &profileLookupMock{profiles: []client.StackProfileSummary{
		{ID: "3cd57498-062c-4dd8-878a-cd0372e5fcf6", Name: "llm-d"},
		{ID: "3435f700-7f47-475c-9f69-864dcba502bc", Name: "gpu-inference"},
	}}

	resolved, resolveError := resolveStackProfileID(mock, "gpu-inference")

	if resolveError != nil {
		t.Fatalf("resolving a name printed by `list`: %v", resolveError)
	}
	if resolved != "3435f700-7f47-475c-9f69-864dcba502bc" {
		t.Fatalf("resolved to the wrong profile: %q", resolved)
	}
}

func TestResolveStackProfileIDExplainsAnUnknownName(t *testing.T) {
	mock := &profileLookupMock{profiles: []client.StackProfileSummary{{ID: "id-1", Name: "gpu-inference"}}}

	_, resolveError := resolveStackProfileID(mock, "gpu-inferenc")

	if resolveError == nil {
		t.Fatal("an unknown name must be reported, not passed to the API as a bogus uuid")
	}
	if !strings.Contains(resolveError.Error(), "stack-profiles list") {
		t.Fatalf("the error must point at how to find the right name, got: %v", resolveError)
	}
}

func TestResolveStackProfileIDFallsBackWhenLookupIsUnavailable(t *testing.T) {
	// The listing is a convenience. If it fails, the reference still has to
	// reach the API - failing here would break a working id because an
	// unrelated endpoint was down.
	mock := &profileLookupMock{listError: errors.New("listing unavailable")}

	resolved, resolveError := resolveStackProfileID(mock, "profile-1")

	if resolveError != nil {
		t.Fatalf("a failed lookup must not fail the command: %v", resolveError)
	}
	if resolved != "profile-1" {
		t.Fatalf("the reference must pass through unchanged, got %q", resolved)
	}
}

func TestParseProfileVersionFlagAcceptsBothForms(t *testing.T) {
	for _, accepted := range []struct {
		raw    string
		expect int
	}{
		{"1", 1},
		{"v1", 1},
		{"V2", 2},
		{" v3 ", 3},
		{"", 0},  // unset - use the profile's current version
		{"0", 0}, // the old int flag's "unset" value
	} {
		version, parseError := parseProfileVersionFlag(accepted.raw)
		if parseError != nil {
			t.Fatalf("--version %q must be accepted: %v", accepted.raw, parseError)
		}
		if version != accepted.expect {
			t.Fatalf("--version %q gave %d, want %d", accepted.raw, version, accepted.expect)
		}
	}
}

func TestParseProfileVersionFlagRejectsNonsense(t *testing.T) {
	for _, rejected := range []string{"latest", "v", "-1", "1.2"} {
		if _, parseError := parseProfileVersionFlag(rejected); parseError == nil {
			t.Fatalf("--version %q must be rejected with an explanation", rejected)
		}
	}
}
