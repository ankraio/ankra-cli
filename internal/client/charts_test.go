package client

import (
	"net/http"
	"strings"
	"testing"
)

func TestListCharts(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/charts") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, ListChartsResponse{
			Charts: []ChartItem{
				{ChartID: "chart1", Name: "nginx", Description: "Web server", Version: "1.0.0"},
			},
			Pagination: ChartsPagination{Page: 1, PageSize: 25, TotalPages: 1},
		})
	})
	got, err := testClient.ListCharts(1, 25, false)
	if err != nil {
		t.Fatalf("ListCharts() error = %v", err)
	}
	if len(got.Charts) != 1 || got.Charts[0].Name != "nginx" {
		t.Errorf("ListCharts() got = %v", got)
	}
}

func TestGetChartDetails(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/stacks/charts/details" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, ChartDetails{
			Name:           "nginx",
			Icon:           "icon.png",
			RepositoryName: "bitnami",
			RepositoryURL:  "https://charts.bitnami.com",
			Versions:       []string{"1.0.0", "0.9.0"},
			Profiles:       []ChartProfile{},
		})
	})
	got, err := testClient.GetChartDetails("nginx", "https://charts.bitnami.com")
	if err != nil {
		t.Fatalf("GetChartDetails() error = %v", err)
	}
	if got.Name != "nginx" || len(got.Versions) != 2 {
		t.Errorf("GetChartDetails() got = %v", got)
	}
}

func TestSearchCharts(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		handler   http.HandlerFunc
		wantCount int
		wantErr   bool
	}{
		{
			name:  "matching name",
			query: "nginx",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, ListChartsResponse{
					Charts: []ChartItem{
						{ChartID: "chart1", Name: "nginx", Description: "Web server", Version: "1.0.0"},
						{ChartID: "chart2", Name: "nginx-ingress", Description: "Ingress", Version: "1.0.0"},
					},
					Pagination: ChartsPagination{Page: 1, PageSize: 100, TotalPages: 1},
				})
			},
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:  "no match",
			query: "nonexistent-chart-xyz",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, ListChartsResponse{
					Charts: []ChartItem{
						{ChartID: "chart1", Name: "nginx", Description: "Web server", Version: "1.0.0"},
					},
					Pagination: ChartsPagination{Page: 1, PageSize: 100, TotalPages: 1},
				})
			},
			wantCount: 0,
			wantErr:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.SearchCharts(tt.query)
			if (err != nil) != tt.wantErr {
				t.Errorf("SearchCharts() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got) != tt.wantCount {
				t.Errorf("SearchCharts() got %d results, want %d", len(got), tt.wantCount)
			}
		})
	}
}
