package client

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestListHelmRegistries(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
		wantLen int
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/org/helm/registries" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, ListHelmRegistriesResponse{
					Result: []HelmRegistryListItem{
						{Name: "docker-hub", Type: "oci", URL: "https://registry-1.docker.io", Status: "active", CreatedAt: "2025-01-01T00:00:00Z"},
						{Name: "bitnami", Type: "http", URL: "https://charts.bitnami.com/bitnami", Status: "active", CreatedAt: "2025-02-01T00:00:00Z"},
					},
					Pagination: Pagination{TotalCount: 2, Page: 1, PageSize: 25, TotalPages: 1},
				})
			},
			wantErr: false,
			wantLen: 2,
		},
		{
			name: "401 unauthorized",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErr: true,
			wantLen: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.ListHelmRegistries()
			if (err != nil) != tt.wantErr {
				t.Errorf("ListHelmRegistries() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got.Result) != tt.wantLen {
				t.Errorf("ListHelmRegistries() got %d results, want %d", len(got.Result), tt.wantLen)
			}
		})
	}
}

func TestGetHelmRegistry(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/helm/registries/my-registry") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, GetHelmRegistryResponse{
					Name:      "my-registry",
					Type:      "oci",
					URL:       "https://ghcr.io",
					Status:    "active",
					CreatedAt: "2025-01-01T00:00:00Z",
				})
			},
			wantErr: false,
		},
		{
			name: "401 unauthorized",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.GetHelmRegistry("my-registry")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetHelmRegistry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Name != "my-registry" {
				t.Errorf("GetHelmRegistry() got.Name = %v, want my-registry", got.Name)
			}
		})
	}
}

func TestCreateHelmRegistry(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success 201",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusCreated, CreateHelmRegistryResponse{
					Errors: nil,
				})
			},
			wantErr: false,
		},
		{
			name: "success 200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, CreateHelmRegistryResponse{
					Errors: nil,
				})
			},
			wantErr: false,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			req := CreateHelmRegistryRequest{
				Spec: json.RawMessage(`{"name":"test","type":"oci","url":"https://ghcr.io"}`),
			}
			got, err := testClient.CreateHelmRegistry(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateHelmRegistry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got == nil {
				t.Errorf("CreateHelmRegistry() got nil response")
			}
		})
	}
}

func TestDeleteHelmRegistry(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/helm/registries/my-registry") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, DeleteHelmRegistryResponse{Success: true})
			},
			wantErr: false,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.DeleteHelmRegistry("my-registry")
			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteHelmRegistry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Success {
				t.Errorf("DeleteHelmRegistry() got.Success = false, want true")
			}
		})
	}
}

func TestListHelmRegistryCredentials(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
		wantLen int
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/org/helm/credentials" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, ListHelmCredentialsResponse{
					Result: []HelmCredentialListItem{
						{Name: "docker-cred", CreatedAt: "2025-01-01T00:00:00Z"},
					},
					Pagination: Pagination{TotalCount: 1, Page: 1, PageSize: 25, TotalPages: 1},
				})
			},
			wantErr: false,
			wantLen: 1,
		},
		{
			name: "401 unauthorized",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErr: true,
			wantLen: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.ListHelmRegistryCredentials()
			if (err != nil) != tt.wantErr {
				t.Errorf("ListHelmRegistryCredentials() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got.Result) != tt.wantLen {
				t.Errorf("ListHelmRegistryCredentials() got %d results, want %d", len(got.Result), tt.wantLen)
			}
		})
	}
}

func TestCreateHelmRegistryCredential(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var reqBody CreateHelmCredentialRequest
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if reqBody.Name != "my-cred" || reqBody.Username != "user" {
					t.Errorf("unexpected request body: %+v", reqBody)
				}
				jsonResponse(t, w, http.StatusCreated, CreateHelmCredentialResponse{Success: true})
			},
			wantErr: false,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.CreateHelmRegistryCredential(CreateHelmCredentialRequest{
				Name:     "my-cred",
				Username: "user",
				Password: "pass",
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateHelmRegistryCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Success {
				t.Errorf("CreateHelmRegistryCredential() got.Success = false, want true")
			}
		})
	}
}

func TestDeleteHelmRegistryCredential(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/helm/credentials/my-cred") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, DeleteHelmCredentialResponse{Success: true})
			},
			wantErr: false,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.DeleteHelmRegistryCredential("my-cred")
			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteHelmRegistryCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Success {
				t.Errorf("DeleteHelmRegistryCredential() got.Success = false, want true")
			}
		})
	}
}
