package cmd

// Regression cover for PLA-786 (ankra-83336): `application list` prints a
// name column, so a name is what a user passes to the per-application
// commands next - and before this it travelled to the backend's uuid-typed
// lookup unchecked, which answered with a bare 500 Internal Server Error.
// The application commands now resolve a name the way stack-profiles,
// credentials and clusters already do.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// applicationLookupMock answers the application listing used to resolve a
// name, and records the per-application calls that follow so a test can see
// which id the command actually sent.
type applicationLookupMock struct {
	baseMock
	listPayload json.RawMessage
	listError   error
	searched    string
	pageSize    int
	requested   bool

	branchesID string
}

// ListApplicationsRaw records the paging the caller asked for and refuses
// anything the real endpoint would reject, so a page size the API caps out is
// a test failure here rather than a silent fallback in production (see the
// stack-profiles lookup for the history).
func (mock *applicationLookupMock) ListApplicationsRaw(_ context.Context, _ int, pageSize int, search string) (json.RawMessage, error) {
	mock.requested = true
	mock.pageSize = pageSize
	mock.searched = search
	if pageSize > maxApplicationLookupPageSize {
		return nil, fmt.Errorf("list applications failed (422): page_size %d exceeds the API maximum of %d",
			pageSize, maxApplicationLookupPageSize)
	}
	if mock.listError != nil {
		return nil, mock.listError
	}
	return mock.listPayload, nil
}

func (mock *applicationLookupMock) GetApplicationBranches(_ context.Context, applicationID string) (json.RawMessage, error) {
	mock.branchesID = applicationID
	return json.RawMessage(`{"branches":[],"default_branch":null}`), nil
}

func applicationListingPayload(applications ...[2]string) json.RawMessage {
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	listing := struct {
		Result []item `json:"result"`
	}{Result: []item{}}
	for _, application := range applications {
		listing.Result = append(listing.Result, item{ID: application[0], Name: application[1]})
	}
	encoded, marshalError := json.Marshal(listing)
	if marshalError != nil {
		panic(marshalError)
	}
	return encoded
}

func TestResolveApplicationIDPassesAUUIDStraightThrough(t *testing.T) {
	mock := &applicationLookupMock{listError: errors.New("listing must not be called for a uuid")}
	const applicationID = "23298741-6a5a-401a-a681-66f31fbdebe1"

	resolved, resolveError := resolveApplicationID(context.Background(), mock, applicationID)

	if resolveError != nil {
		t.Fatalf("a uuid must resolve to itself: %v", resolveError)
	}
	if resolved != applicationID {
		t.Fatalf("expected %q, got %q", applicationID, resolved)
	}
	if mock.requested {
		t.Fatal("resolving a uuid must not cost a listing round-trip")
	}
}

func TestResolveApplicationIDResolvesAName(t *testing.T) {
	mock := &applicationLookupMock{listPayload: applicationListingPayload(
		[2]string{"3cd57498-062c-4dd8-878a-cd0372e5fcf6", "commerce-api"},
		[2]string{"23298741-6a5a-401a-a681-66f31fbdebe1", "commerce"},
	)}

	resolved, resolveError := resolveApplicationID(context.Background(), mock, "commerce")

	if resolveError != nil {
		t.Fatalf("resolving a name printed by `list`: %v", resolveError)
	}
	if resolved != "23298741-6a5a-401a-a681-66f31fbdebe1" {
		t.Fatalf("resolved to the wrong application: %q", resolved)
	}
	if !mock.requested {
		t.Fatal("a non-uuid reference must consult the listing")
	}
	if mock.searched != "commerce" {
		t.Fatalf("the name must be pushed down as the server-side search filter, got %q", mock.searched)
	}
	if mock.pageSize > maxApplicationLookupPageSize {
		t.Fatalf("requested page_size %d exceeds the API maximum of %d", mock.pageSize, maxApplicationLookupPageSize)
	}
}

func TestResolveApplicationIDPrefersTheExactNameOverACaseVariant(t *testing.T) {
	mock := &applicationLookupMock{listPayload: applicationListingPayload(
		[2]string{"aaaaaaaa-062c-4dd8-878a-cd0372e5fcf6", "Commerce"},
		[2]string{"23298741-6a5a-401a-a681-66f31fbdebe1", "commerce"},
	)}

	resolved, resolveError := resolveApplicationID(context.Background(), mock, "commerce")

	if resolveError != nil {
		t.Fatalf("an exact name match must win outright: %v", resolveError)
	}
	if resolved != "23298741-6a5a-401a-a681-66f31fbdebe1" {
		t.Fatalf("expected the exact match, got %q", resolved)
	}
}

func TestResolveApplicationIDMatchesCaseInsensitivelyWhenNoExactMatchExists(t *testing.T) {
	// The server-side search filter is already case-insensitive, so a user
	// who types the case they remember should not be told the application
	// does not exist when exactly one case variant does.
	mock := &applicationLookupMock{listPayload: applicationListingPayload(
		[2]string{"23298741-6a5a-401a-a681-66f31fbdebe1", "Commerce"},
	)}

	resolved, resolveError := resolveApplicationID(context.Background(), mock, "commerce")

	if resolveError != nil {
		t.Fatalf("a unique case-insensitive match must resolve: %v", resolveError)
	}
	if resolved != "23298741-6a5a-401a-a681-66f31fbdebe1" {
		t.Fatalf("resolved to the wrong application: %q", resolved)
	}
}

func TestResolveApplicationIDExplainsAnUnknownName(t *testing.T) {
	mock := &applicationLookupMock{listPayload: applicationListingPayload(
		[2]string{"23298741-6a5a-401a-a681-66f31fbdebe1", "commerce"},
	)}

	_, resolveError := resolveApplicationID(context.Background(), mock, "commerc")

	if resolveError == nil {
		t.Fatal("an unknown name must be reported, not passed to the API as a bogus uuid")
	}
	if !strings.Contains(resolveError.Error(), "application list") {
		t.Fatalf("the error must point at how to find the right name, got: %v", resolveError)
	}
}

func TestResolveApplicationIDReportsAnAmbiguousName(t *testing.T) {
	mock := &applicationLookupMock{listPayload: applicationListingPayload(
		[2]string{"aaaaaaaa-062c-4dd8-878a-cd0372e5fcf6", "commerce"},
		[2]string{"23298741-6a5a-401a-a681-66f31fbdebe1", "commerce"},
	)}

	_, resolveError := resolveApplicationID(context.Background(), mock, "commerce")

	if resolveError == nil {
		t.Fatal("an ambiguous name must be reported, not resolved to an arbitrary application")
	}
	if !strings.Contains(resolveError.Error(), "aaaaaaaa-062c-4dd8-878a-cd0372e5fcf6") ||
		!strings.Contains(resolveError.Error(), "23298741-6a5a-401a-a681-66f31fbdebe1") {
		t.Fatalf("the error must list the candidate ids, got: %v", resolveError)
	}
}

func TestResolveApplicationIDFallsBackWhenLookupIsUnavailable(t *testing.T) {
	// The listing is a convenience. If it fails, the reference still has to
	// reach the API - failing here would break a working id because an
	// unrelated endpoint was down.
	mock := &applicationLookupMock{listError: errors.New("listing unavailable")}

	resolved, resolveError := resolveApplicationID(context.Background(), mock, "app-1")

	if resolveError != nil {
		t.Fatalf("a failed lookup must not fail the command: %v", resolveError)
	}
	if resolved != "app-1" {
		t.Fatalf("the reference must pass through unchanged, got %q", resolved)
	}
}

func TestResolveApplicationIDFallsBackOnAMalformedListing(t *testing.T) {
	mock := &applicationLookupMock{listPayload: json.RawMessage(`"not-a-listing"`)}

	resolved, resolveError := resolveApplicationID(context.Background(), mock, "app-1")

	if resolveError != nil {
		t.Fatalf("an unreadable listing must not fail the command: %v", resolveError)
	}
	if resolved != "app-1" {
		t.Fatalf("the reference must pass through unchanged, got %q", resolved)
	}
}

func TestApplicationBranchesResolvesAName(t *testing.T) {
	// The customer-visible shape of PLA-786: `application branches commerce`
	// answered 500 because "commerce" was interpolated into the request path.
	mock := &applicationLookupMock{listPayload: applicationListingPayload(
		[2]string{"23298741-6a5a-401a-a681-66f31fbdebe1", "commerce"},
	)}

	_, executeError := runApplicationCommand(t, mock, "branches", "commerce")

	if executeError != nil {
		t.Fatalf("branches by name: %v", executeError)
	}
	if mock.branchesID != "23298741-6a5a-401a-a681-66f31fbdebe1" {
		t.Fatalf("the request must carry the resolved id, got %q", mock.branchesID)
	}
}

func TestApplicationBranchesExplainsAnUnknownName(t *testing.T) {
	mock := &applicationLookupMock{listPayload: applicationListingPayload()}

	_, executeError := runApplicationCommand(t, mock, "branches", "no-such-app")

	if executeError == nil {
		t.Fatal("an unknown name must fail before any request is made")
	}
	if !strings.Contains(executeError.Error(), "application list") {
		t.Fatalf("the error must point at `application list`, got: %v", executeError)
	}
	if mock.branchesID != "" {
		t.Fatalf("no branches request may be made for an unresolvable name, got %q", mock.branchesID)
	}
}
