package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestListClusterAddons(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/addons") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, ListClusterAddonsResponse{
			Result: []ClusterAddonListItem{
				{ID: "addon1", Name: "ingress", ChartName: "ingress-nginx", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			},
			Pagination: Pagination{TotalCount: 1, Page: 1, PageSize: 25, TotalPages: 1},
		})
	})
	got, err := testClient.ListClusterAddons("cluster-id")
	if err != nil {
		t.Fatalf("ListClusterAddons() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "ingress" {
		t.Errorf("ListClusterAddons() got = %v", got)
	}
}

func TestListAvailableAddons(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/addons/available") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, ListAvailableAddonsResponse{
			Result: []AvailableAddon{
				{ID: "addon-1", Name: "ingress-nginx", ChartName: "ingress-nginx", Version: "4.7.0"},
				{ID: "addon-2", Name: "cert-manager", ChartName: "cert-manager", Version: "1.12.0"},
			},
		})
	})
	got, err := testClient.ListAvailableAddons("cluster-id")
	if err != nil {
		t.Fatalf("ListAvailableAddons() error = %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListAvailableAddons() got %d addons, want 2", len(got))
	}
	if got[0].Name != "ingress-nginx" {
		t.Errorf("ListAvailableAddons() got[0].Name = %v, want ingress-nginx", got[0].Name)
	}
}

func TestGetAddonSettings(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/addons/ingress/settings") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, GetAddonSettingsResponse{
			AddonName: "ingress",
			Settings:  AddonSettings{},
		})
	})
	got, err := testClient.GetAddonSettings("cluster-id", "ingress")
	if err != nil {
		t.Fatalf("GetAddonSettings() error = %v", err)
	}
	if got.AddonName != "ingress" {
		t.Errorf("GetAddonSettings() got.AddonName = %v, want ingress", got.AddonName)
	}
}

func TestUpdateAddonSettings(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.Contains(r.URL.Path, "/addons/ingress/settings") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	err := testClient.UpdateAddonSettings(context.Background(), "cluster-id", "ingress", AddonSettings{})
	if err != nil {
		t.Fatalf("UpdateAddonSettings() error = %v", err)
	}
}

func TestUninstallAddon(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	got, err := testClient.UninstallAddon(context.Background(), "cluster-id", "addon-resource-id", false)
	if err != nil {
		t.Fatalf("UninstallAddon() error = %v", err)
	}
	if !got.Success {
		t.Errorf("UninstallAddon() got.Success = %v, want true", got.Success)
	}
}

func TestGetAddonByName(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, ListClusterAddonsResponse{
					Result: []ClusterAddonListItem{
						{ID: "addon1", Name: "ingress", ChartName: "ingress-nginx", CreatedAt: time.Now(), UpdatedAt: time.Now()},
					},
					Pagination: Pagination{TotalCount: 1, Page: 1, PageSize: 25, TotalPages: 1},
				})
			},
			wantErr: false,
		},
		{
			name: "not found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, ListClusterAddonsResponse{
					Result:     []ClusterAddonListItem{},
					Pagination: Pagination{TotalCount: 0, Page: 1, PageSize: 25, TotalPages: 0},
				})
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.GetAddonByName("cluster-id", "ingress")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAddonByName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Name != "ingress" {
				t.Errorf("GetAddonByName() got.Name = %v, want ingress", got.Name)
			}
		})
	}
}
