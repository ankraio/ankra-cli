package cmd

// Regression cover for PLA-824 / ankra-e28tp: `stack-profiles list` defaults
// to --page-size 25, so an organisation with 35 profiles saw a 25-row table
// with nothing saying the other 10 existed. Page 1 read as the whole
// catalogue - to the user, and to anything that shells out to the table.

import (
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

// profileListingMock answers one page of a larger catalogue.
type profileListingMock struct {
	baseMock
	response *client.StackProfileListResponse
}

func (mock *profileListingMock) ListStackProfiles(page, pageSize int, _ string, _ string) (*client.StackProfileListResponse, error) {
	return mock.response, nil
}

// runProfilesList drives the real command so the assertion covers what the
// user sees, not just the footer helper.
func runProfilesList(t *testing.T, page int, pageSize int, response *client.StackProfileListResponse) string {
	t.Helper()
	previousClient := apiClient
	apiClient = &profileListingMock{response: response}
	t.Cleanup(func() { apiClient = previousClient })

	if flagError := stackProfilesListCmd.Flags().Set("page", fmt.Sprintf("%d", page)); flagError != nil {
		t.Fatalf("setting --page: %v", flagError)
	}
	if flagError := stackProfilesListCmd.Flags().Set("page-size", fmt.Sprintf("%d", pageSize)); flagError != nil {
		t.Fatalf("setting --page-size: %v", flagError)
	}
	t.Cleanup(func() {
		_ = stackProfilesListCmd.Flags().Set("page", "1")
		_ = stackProfilesListCmd.Flags().Set("page-size", "25")
	})

	return captureStdout(t, func() {
		if runError := stackProfilesListCmd.RunE(stackProfilesListCmd, nil); runError != nil {
			t.Fatalf("listing stack profiles: %v", runError)
		}
	})
}

func profilePage(count int) []client.StackProfileSummary {
	profiles := make([]client.StackProfileSummary, 0, count)
	for index := 0; index < count; index++ {
		profiles = append(profiles, client.StackProfileSummary{
			ID:            fmt.Sprintf("profile-%d", index),
			Name:          fmt.Sprintf("tael-%d", index),
			Category:      "general",
			LatestVersion: 1,
		})
	}
	return profiles
}

func TestStackProfilesListSaysWhenThePageIsNotTheCatalogue(t *testing.T) {
	output := stripANSICodes(runProfilesList(t, 1, 25, &client.StackProfileListResponse{
		Result:     profilePage(25),
		TotalCount: 35,
		Page:       1,
		PageSize:   25,
	}))

	if !strings.Contains(output, "Showing 25 of 35 stack profiles") {
		t.Fatalf("a truncated page must say so; got:\n%s", output)
	}
	if !strings.Contains(output, "(page 1 of 2)") {
		t.Fatalf("the footer must locate the page in the catalogue; got:\n%s", output)
	}
	if !strings.Contains(output, "--page-size") {
		t.Fatalf("the footer must say how to see the rest; got:\n%s", output)
	}
}

func TestStackProfilesListStaysQuietWhenThePageIsTheCatalogue(t *testing.T) {
	output := stripANSICodes(runProfilesList(t, 1, 25, &client.StackProfileListResponse{
		Result:     profilePage(3),
		TotalCount: 3,
		Page:       1,
		PageSize:   25,
	}))

	if strings.Contains(output, "Showing") {
		t.Fatalf("a complete listing must not claim to be partial; got:\n%s", output)
	}
	if !strings.Contains(output, "tael-0") {
		t.Fatalf("the table itself must still render; got:\n%s", output)
	}
}

func TestStackProfileListingFooterCountsTheLastPage(t *testing.T) {
	// The last page shows fewer rows than the page size; it is still a page,
	// and saying "10 of 35" is what tells the reader they are not looking at
	// the whole catalogue.
	footer := stackProfileListingFooter(10, 35, 2, 25)

	if !strings.Contains(footer, "Showing 10 of 35 stack profiles (page 2 of 2)") {
		t.Fatalf("unexpected footer: %q", footer)
	}
}

func TestStackProfileListingFooterOmitsThePageCountWithoutAPageSize(t *testing.T) {
	footer := stackProfileListingFooter(10, 35, 1, 0)

	if !strings.Contains(footer, "Showing 10 of 35 stack profiles;") {
		t.Fatalf("the count must survive a missing page size: %q", footer)
	}
	if strings.Contains(footer, "page 1 of") {
		t.Fatalf("a page count cannot be computed without a page size: %q", footer)
	}
}
