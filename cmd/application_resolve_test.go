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
	listPayload  json.RawMessage
	pagePayloads []json.RawMessage
	listError    error
	searched     string
	pageSize     int
	requested    bool
	pagesAsked   []int

	branchesID string
}

// ListApplicationsRaw records the paging the caller asked for and refuses
// anything the real endpoint would reject, so a page size the API caps out is
// a test failure here rather than a silent fallback in production (see the
// stack-profiles lookup for the history).
func (mock *applicationLookupMock) ListApplicationsRaw(_ context.Context, page int, pageSize int, search string) (json.RawMessage, error) {
	mock.requested = true
	mock.pageSize = pageSize
	mock.searched = search
	mock.pagesAsked = append(mock.pagesAsked, page)
	if pageSize > maxApplicationLookupPageSize {
		return nil, fmt.Errorf("list applications failed (422): page_size %d exceeds the API maximum of %d",
			pageSize, maxApplicationLookupPageSize)
	}
	if mock.listError != nil {
		return nil, mock.listError
	}
	if len(mock.pagePayloads) > 0 {
		if page < 1 || page > len(mock.pagePayloads) {
			return nil, fmt.Errorf("page %d is outside the %d pages this listing has", page, len(mock.pagePayloads))
		}
		return mock.pagePayloads[page-1], nil
	}
	return mock.listPayload, nil
}

func (mock *applicationLookupMock) GetApplicationBranches(_ context.Context, applicationID string) (json.RawMessage, error) {
	mock.branchesID = applicationID
	return json.RawMessage(`{"branches":[],"default_branch":null}`), nil
}

func applicationListingPayload(applications ...[2]string) json.RawMessage {
	return applicationListingPagePayload(0, applications...)
}

// applicationListingPagePayload renders one page of the listing the way the
// applications endpoint does, including the pagination block the lookup reads
// to decide whether another page is waiting. A totalPages of 0 stands for the
// single-page listings the other tests use.
func applicationListingPagePayload(totalPages int, applications ...[2]string) json.RawMessage {
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type pagination struct {
		TotalPages int `json:"total_pages"`
	}
	listing := struct {
		Result     []item     `json:"result"`
		Pagination pagination `json:"pagination"`
	}{Result: []item{}, Pagination: pagination{TotalPages: totalPages}}
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

func TestResolveApplicationIDStopsAfterOnePageWhenThatIsTheWholeListing(t *testing.T) {
	// Paging past the exact match would spend a round-trip per command on
	// every org that fits in one page, which is nearly all of them.
	mock := &applicationLookupMock{pagePayloads: []json.RawMessage{
		applicationListingPagePayload(1, [2]string{"23298741-6a5a-401a-a681-66f31fbdebe1", "commerce"}),
	}}

	if _, resolveError := resolveApplicationID(context.Background(), mock, "commerce"); resolveError != nil {
		t.Fatalf("resolving a name: %v", resolveError)
	}
	if len(mock.pagesAsked) != 1 {
		t.Fatalf("a single-page listing must cost one request, got pages %v", mock.pagesAsked)
	}
}

func TestResolveApplicationIDPagesUntilItFindsTheExactMatch(t *testing.T) {
	// The server-side `search` filter is a case-insensitive substring match
	// ordered by creation date, and the endpoint caps page_size at 100. In an
	// org with more than a page of applications whose names contain the
	// reference, the exact match can sit on a later page - a single-page
	// lookup would tell the user it does not exist, which is the PLA-786
	// symptom returning for exactly the orgs most likely to hit it.
	firstPage := make([][2]string, 0, maxApplicationLookupPageSize)
	for index := 0; index < maxApplicationLookupPageSize; index++ {
		firstPage = append(firstPage,
			[2]string{fmt.Sprintf("3cd57498-062c-4dd8-878a-cd0372e5fc%02d", index), fmt.Sprintf("commerce-%d", index)})
	}
	mock := &applicationLookupMock{pagePayloads: []json.RawMessage{
		applicationListingPagePayload(2, firstPage...),
		applicationListingPagePayload(2, [2]string{"23298741-6a5a-401a-a681-66f31fbdebe1", "commerce"}),
	}}

	resolved, resolveError := resolveApplicationID(context.Background(), mock, "commerce")

	if resolveError != nil {
		t.Fatalf("an exact match on a later page must still resolve: %v", resolveError)
	}
	if resolved != "23298741-6a5a-401a-a681-66f31fbdebe1" {
		t.Fatalf("resolved to the wrong application: %q", resolved)
	}
	if len(mock.pagesAsked) != 2 || mock.pagesAsked[0] != 1 || mock.pagesAsked[1] != 2 {
		t.Fatalf("the lookup must walk the pages in order, got %v", mock.pagesAsked)
	}
}

func TestResolveApplicationIDReportsAnAmbiguousNameFoundAcrossPages(t *testing.T) {
	// Ambiguity is only knowable once the whole listing has been read: two
	// applications sharing a name can land on different pages, and picking
	// the first one seen would act on an application the user did not name.
	mock := &applicationLookupMock{pagePayloads: []json.RawMessage{
		applicationListingPagePayload(2, [2]string{"aaaaaaaa-062c-4dd8-878a-cd0372e5fcf6", "commerce"}),
		applicationListingPagePayload(2, [2]string{"23298741-6a5a-401a-a681-66f31fbdebe1", "commerce"}),
	}}

	_, resolveError := resolveApplicationID(context.Background(), mock, "commerce")

	if resolveError == nil {
		t.Fatal("a name shared across pages must be reported as ambiguous")
	}
	if !strings.Contains(resolveError.Error(), "aaaaaaaa-062c-4dd8-878a-cd0372e5fcf6") ||
		!strings.Contains(resolveError.Error(), "23298741-6a5a-401a-a681-66f31fbdebe1") {
		t.Fatalf("the error must list both candidate ids, got: %v", resolveError)
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

func TestResolveApplicationIDReportsAFailedLookupRatherThanPassingTheNameOn(t *testing.T) {
	// Only a name gets this far - a uuid returned at the top - so passing the
	// reference through on a failed listing would put a name in the backend's
	// uuid-typed path parameter and reproduce the bare 500 this resolver
	// exists to prevent. The safe fallback for an id is not the safe fallback
	// for a name.
	listingFailure := errors.New("listing unavailable")
	mock := &applicationLookupMock{listError: listingFailure}

	resolved, resolveError := resolveApplicationID(context.Background(), mock, "commerce")

	if resolveError == nil {
		t.Fatalf("a failed lookup must be reported, not handed to the uuid-typed API as %q", resolved)
	}
	if !errors.Is(resolveError, listingFailure) {
		t.Fatalf("the underlying failure must survive for exit-code classification, got: %v", resolveError)
	}
	if !strings.Contains(resolveError.Error(), "commerce") {
		t.Fatalf("the error must name the reference that could not be resolved, got: %v", resolveError)
	}
	if !strings.Contains(resolveError.Error(), "application list") ||
		!strings.Contains(resolveError.Error(), "application id") {
		t.Fatalf("the error must say what to do next, got: %v", resolveError)
	}
}

func TestResolveApplicationIDReportsAnUnreadableListing(t *testing.T) {
	mock := &applicationLookupMock{listPayload: json.RawMessage(`"not-a-listing"`)}

	resolved, resolveError := resolveApplicationID(context.Background(), mock, "commerce")

	if resolveError == nil {
		t.Fatalf("an unreadable listing must be reported, not handed to the uuid-typed API as %q", resolved)
	}
	if !strings.Contains(resolveError.Error(), "application list") ||
		!strings.Contains(resolveError.Error(), "application id") {
		t.Fatalf("the error must say what to do next, got: %v", resolveError)
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

func TestApplicationBranchesDoesNotSendANameWhenTheLookupFails(t *testing.T) {
	// The end of the PLA-786 path: a name in the request path is what the
	// backend answers 500 to, so a lookup that could not run must stop the
	// command rather than send the name anyway.
	mock := &applicationLookupMock{listError: errors.New("listing unavailable")}

	_, executeError := runApplicationCommand(t, mock, "branches", "commerce")

	if executeError == nil {
		t.Fatal("a failed lookup must fail the command instead of sending a name to the API")
	}
	if mock.branchesID != "" {
		t.Fatalf("a name must never reach the uuid-typed request path, got %q", mock.branchesID)
	}
}
