package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type helmRegistryGetPagedMock struct {
	baseMock
	lastPage     int
	lastPageSize int
	charts       []client.HelmChartVersionSummary
	totalCount   int
}

func (m *helmRegistryGetPagedMock) GetHelmRegistry(registryName string, page, pageSize int) (*client.GetHelmRegistryResponse, error) {
	m.lastPage = page
	m.lastPageSize = pageSize
	return &client.GetHelmRegistryResponse{
		Registry: client.HelmRegistryDetail{Name: registryName, URL: "https://charts.example.com"},
		Charts:   m.charts,
		Pagination: client.Pagination{
			TotalCount: m.totalCount,
			TotalPages: 3,
			Page:       page,
			PageSize:   pageSize,
		},
	}, nil
}

func TestHelmRegistriesGetForwardsChartPaging(t *testing.T) {
	mock := &helmRegistryGetPagedMock{
		charts: []client.HelmChartVersionSummary{
			{Name: "nginx", Version: "1.0.0"},
			{Name: "redis", Version: "2.0.0"},
		},
		totalCount: 120,
	}
	setMockClient(t, mock)
	t.Cleanup(func() {
		flags := helmRegistriesGetCmd.Flags()
		_ = flags.Set("page", "1")
		_ = flags.Set("page-size", "20")
	})

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("helm", "registries", "get", "my-registry", "--page", "2", "--page-size", "50")
	})

	if mock.lastPage != 2 || mock.lastPageSize != 50 {
		t.Errorf("expected page=2 page_size=50 forwarded to the client, got page=%d page_size=%d",
			mock.lastPage, mock.lastPageSize)
	}
	if !strings.Contains(stdoutOutput, "120 (showing 2 on this page)") {
		t.Errorf("expected the charts footer to reflect the fetched page, got: %s", stdoutOutput)
	}
}
