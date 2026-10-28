package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// The API has always returned last_sync_error on both the list and the detail
// route. It was not modelled, so a registry whose index cannot be fetched
// decoded as healthy: chart_count populated, resource_state "up", no hint that
// its chart list was stale.
func TestHelmRegistryListItemDecodesLastSyncError(t *testing.T) {
	body := `{"name":"acme","kind":"http","url":"https://charts.example.com",` +
		`"indexing":false,"chart_count":6,"is_global":true,` +
		`"last_sync_error":"Failed to fetch index.yaml from https://charts.example.com: HTTP 404"}`

	var item client.HelmRegistryListItem
	if err := json.Unmarshal([]byte(body), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if item.LastSyncError == nil {
		t.Fatal("LastSyncError was dropped; a failing registry still looks healthy")
	}
	if !strings.Contains(*item.LastSyncError, "HTTP 404") {
		t.Errorf("LastSyncError = %q, want the fetch failure", *item.LastSyncError)
	}
}

func TestHelmRegistryListItemLeavesLastSyncErrorNilWhenHealthy(t *testing.T) {
	var item client.HelmRegistryListItem
	if err := json.Unmarshal([]byte(`{"name":"acme","chart_count":6,"last_sync_error":null}`), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if item.LastSyncError != nil {
		t.Errorf("LastSyncError = %q, want nil for a healthy registry", *item.LastSyncError)
	}
}

func TestGetHelmRegistryResponseDecodesLastSyncError(t *testing.T) {
	body := `{"registry":{"name":"acme","url":"https://charts.example.com"},"charts":[],` +
		`"indexing":false,"resource_state":"up",` +
		`"last_sync_error":"Failed to fetch index.yaml from https://charts.example.com: HTTP 500",` +
		`"pagination":{"page":1,"total_pages":1,"total_count":0}}`

	var response client.GetHelmRegistryResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if response.LastSyncError == nil {
		t.Fatal("LastSyncError was dropped from the detail response")
	}
	// resource_state stays "up" for a registry that cannot be indexed, which is
	// exactly why the error has to be surfaced separately.
	if response.ResourceState == nil || *response.ResourceState != "up" {
		t.Errorf("ResourceState = %v, want \"up\" alongside the sync error", response.ResourceState)
	}
}

func TestPrintRegistrySyncErrors(t *testing.T) {
	failing := "Failed to fetch index.yaml from https://charts.example.com: HTTP 404"
	empty := ""

	cases := []struct {
		name        string
		registries  []client.HelmRegistryListItem
		wantOutput  []string
		wantSilence bool
	}{
		{
			name:        "no registries",
			registries:  nil,
			wantSilence: true,
		},
		{
			name: "all healthy",
			registries: []client.HelmRegistryListItem{
				{Name: "acme"},
				{Name: "globex", LastSyncError: &empty},
			},
			wantSilence: true,
		},
		{
			name: "one failing is singular",
			registries: []client.HelmRegistryListItem{
				{Name: "acme", LastSyncError: &failing},
				{Name: "globex"},
			},
			wantOutput: []string{"1 registry is failing to sync", "acme: ", "HTTP 404"},
		},
		{
			name: "several failing are plural",
			registries: []client.HelmRegistryListItem{
				{Name: "acme", LastSyncError: &failing},
				{Name: "globex", LastSyncError: &failing},
			},
			wantOutput: []string{"2 registries are failing to sync", "acme", "globex"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			output := captureStdout(t, func() {
				printRegistrySyncErrors(testCase.registries)
			})
			if testCase.wantSilence {
				if strings.TrimSpace(output) != "" {
					t.Errorf("expected no output, got %q", output)
				}
				return
			}
			for _, want := range testCase.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output %q does not contain %q", output, want)
				}
			}
		})
	}
}
