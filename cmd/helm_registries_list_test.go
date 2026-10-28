package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// helmRegistriesPagedMock serves a fixed page layout so the --all loop can
// be observed walking every page with the expected options.
type helmRegistriesPagedMock struct {
	baseMock
	pages      map[int][]client.HelmRegistryListItem
	totalCount int
	totalPages int
	calls      []client.ListHelmRegistriesOptions
}

func (m *helmRegistriesPagedMock) ListHelmRegistries(opts *client.ListHelmRegistriesOptions) (*client.ListHelmRegistriesResponse, error) {
	m.calls = append(m.calls, *opts)
	return &client.ListHelmRegistriesResponse{
		Result: m.pages[opts.Page],
		Pagination: client.Pagination{
			TotalCount: m.totalCount,
			TotalPages: m.totalPages,
			Page:       opts.Page,
			PageSize:   opts.PageSize,
		},
	}, nil
}

// resetHelmRegistriesListFlags restores the list command's flag values and
// their Changed markers both before and after the test: the cobra tree is
// shared across the test binary, and the --all/--page exclusivity check
// reads Changed, which any earlier --page invocation would leave set.
func resetHelmRegistriesListFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		flags := helmRegistriesListCmd.Flags()
		for name, value := range map[string]string{
			"page":       "1",
			"page-size":  "20",
			"all":        "false",
			"search":     "",
			"sort-by":    "",
			"sort-order": "",
			"output":     "",
		} {
			_ = flags.Set(name, value)
			flags.Lookup(name).Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

func TestHelmRegistriesListAllWalksEveryPage(t *testing.T) {
	mock := &helmRegistriesPagedMock{
		pages: map[int][]client.HelmRegistryListItem{
			1: {
				{Name: "registry-a", Kind: "http", URL: "https://a.example.com"},
				{Name: "registry-b", Kind: "http", URL: "https://b.example.com"},
			},
			2: {
				{Name: "registry-c", Kind: "oci", URL: "oci://c.example.com"},
				{Name: "registry-d", Kind: "http", URL: "https://d.example.com"},
			},
			3: {
				{Name: "registry-e", Kind: "http", URL: "https://e.example.com"},
			},
		},
		totalCount: 5,
		totalPages: 3,
	}
	setMockClient(t, mock)
	resetHelmRegistriesListFlags(t)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("helm", "registries", "list", "--all")
	})

	for _, expected := range []string{"registry-a", "registry-b", "registry-c", "registry-d", "registry-e"} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected output to contain %q, got: %s", expected, stdoutOutput)
		}
	}
	if !strings.Contains(stdoutOutput, "Page 1 of 1 (total 5)") {
		t.Errorf("expected merged pagination footer, got: %s", stdoutOutput)
	}
	if len(mock.calls) != 3 {
		t.Fatalf("expected 3 page fetches, got %d: %+v", len(mock.calls), mock.calls)
	}
	for index, call := range mock.calls {
		if call.Page != index+1 || call.PageSize != 100 {
			t.Errorf("call %d: expected page %d with page_size 100, got %+v", index, index+1, call)
		}
	}
}

func TestHelmRegistriesListAllRejectsExplicitPage(t *testing.T) {
	mock := &helmRegistriesPagedMock{totalPages: 1}
	setMockClient(t, mock)
	resetHelmRegistriesListFlags(t)

	_, err := executeCommand("helm", "registries", "list", "--all", "--page", "2")
	if err == nil {
		t.Fatal("expected an error for --all combined with --page")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("--all with --page should exit %d, got %d", exitUsage, got)
	}
	if len(mock.calls) != 0 {
		t.Errorf("expected no API calls for the rejected flag combination, got %d", len(mock.calls))
	}
}

func TestHelmRegistriesListAllJSONEmitsMergedEnvelope(t *testing.T) {
	mock := &helmRegistriesPagedMock{
		pages: map[int][]client.HelmRegistryListItem{
			1: {{Name: "registry-a", Kind: "http", URL: "https://a.example.com"}},
			2: {{Name: "registry-b", Kind: "oci", URL: "oci://b.example.com"}},
		},
		totalCount: 2,
		totalPages: 2,
	}
	setMockClient(t, mock)
	resetHelmRegistriesListFlags(t)

	output, err := executeCommand("helm", "registries", "list", "--all", "-o", "json")
	if err != nil {
		t.Fatalf("executeCommand() error = %v", err)
	}

	var decoded client.ListHelmRegistriesResponse
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("expected valid JSON envelope, got error %v for output: %s", err, output)
	}
	if len(decoded.Result) != 2 {
		t.Errorf("expected 2 merged registries, got %d", len(decoded.Result))
	}
	if decoded.Pagination.TotalCount != 2 || decoded.Pagination.TotalPages != 1 || decoded.Pagination.Page != 1 {
		t.Errorf("expected merged pagination, got %+v", decoded.Pagination)
	}
	if decoded.Result[1].Kind != "oci" {
		t.Errorf("expected the kind field to survive structured output, got %+v", decoded.Result[1])
	}
}
