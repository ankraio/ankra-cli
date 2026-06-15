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
				createdA := "2025-01-01T00:00:00Z"
				createdB := "2025-02-01T00:00:00Z"
				jsonResponse(t, w, http.StatusOK, ListHelmRegistriesResponse{
					Result: []HelmRegistryListItem{
						{Name: "docker-hub", URL: "oci://registry-1.docker.io", CreatedAt: &createdA, Indexing: false, ChartCount: 42},
						{Name: "bitnami", URL: "https://charts.bitnami.com/bitnami", CreatedAt: &createdB, Indexing: true, ChartCount: 100, IsGlobal: true},
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
				created := "2025-01-01T00:00:00Z"
				jsonResponse(t, w, http.StatusOK, GetHelmRegistryResponse{
					Registry: HelmRegistryDetail{
						Name:      "my-registry",
						URL:       "oci://ghcr.io",
						CreatedAt: &created,
					},
					Pagination: Pagination{TotalCount: 0, Page: 1, PageSize: 25, TotalPages: 0},
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
			if !tt.wantErr && got.Registry.Name != "my-registry" {
				t.Errorf("GetHelmRegistry() got.Registry.Name = %v, want my-registry", got.Registry.Name)
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

func TestUpdateHelmRegistry(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/helm/registries/my-registry") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				var reqBody UpdateHelmRegistryRequest
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if reqBody.ReadJobInterval == nil || *reqBody.ReadJobInterval != 300 {
					t.Errorf("unexpected request body: %+v", reqBody)
				}
				jsonResponse(t, w, http.StatusOK, map[string]any{})
			},
			wantErr: false,
		},
		{
			name: "not found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantErr: true,
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
			interval := 300
			err := testClient.UpdateHelmRegistry("my-registry", &interval)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateHelmRegistry() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSyncHelmRegistry(t *testing.T) {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		wantErr  bool
		wantJobs int
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/helm/registries/my-registry/sync") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, SyncHelmRegistryResponse{CreatedJobs: 1})
			},
			wantErr:  false,
			wantJobs: 1,
		},
		{
			name: "already in progress surfaces detail",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusConflict, map[string]string{
					"detail": "A sync is already in progress for this registry.",
				})
			},
			wantErr: true,
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
			got, err := testClient.SyncHelmRegistry("my-registry")
			if (err != nil) != tt.wantErr {
				t.Errorf("SyncHelmRegistry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.CreatedJobs != tt.wantJobs {
				t.Errorf("SyncHelmRegistry() got.CreatedJobs = %d, want %d", got.CreatedJobs, tt.wantJobs)
			}
		})
	}
}

func TestListHelmRegistrySyncJobs(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
		wantLen int
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/helm/registries/my-registry/sync-jobs") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, ListRegistrySyncJobsResponse{
					Jobs: []SyncJob{
						{
							ID:           "job-1",
							Name:         "read_http_registry",
							Status:       "completed",
							ResourceKind: "http_registry",
							ResourceName: "my-registry",
							CreatedAt:    "2025-01-01T00:00:00Z",
						},
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
			got, err := testClient.ListHelmRegistrySyncJobs("my-registry", 1, 25)
			if (err != nil) != tt.wantErr {
				t.Errorf("ListHelmRegistrySyncJobs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got.Jobs) != tt.wantLen {
				t.Errorf("ListHelmRegistrySyncJobs() got %d jobs, want %d", len(got.Jobs), tt.wantLen)
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
					Credentials: []HelmCredentialListItem{
						{ID: "cred-id", Name: "docker-cred", CreatedAt: "2025-01-01T00:00:00Z"},
					},
					TotalCount: 1,
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
			if !tt.wantErr && len(got.Credentials) != tt.wantLen {
				t.Errorf("ListHelmRegistryCredentials() got %d credentials, want %d", len(got.Credentials), tt.wantLen)
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

func TestGetHelmRegistryCredential(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/helm/credentials/my-cred") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, GetHelmCredentialResponse{
					ID:        "cred-id",
					Name:      "my-cred",
					Username:  "user",
					Provider:  "helm",
					CreatedAt: "2025-01-01T00:00:00Z",
					UpdatedAt: "2025-01-02T00:00:00Z",
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
			got, err := testClient.GetHelmRegistryCredential("my-cred")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetHelmRegistryCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Name != "my-cred" {
				t.Errorf("GetHelmRegistryCredential() got.Name = %v, want my-cred", got.Name)
			}
		})
	}
}

func TestUpdateHelmRegistryCredential(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/helm/credentials/my-cred") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				var reqBody UpdateHelmCredentialRequest
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if reqBody.Username != "user" || reqBody.Password != "pass" {
					t.Errorf("unexpected request body: %+v", reqBody)
				}
				jsonResponse(t, w, http.StatusOK, map[string]any{"errors": []any{}})
			},
			wantErr: false,
		},
		{
			name: "not found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantErr: true,
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
			err := testClient.UpdateHelmRegistryCredential("my-cred", UpdateHelmCredentialRequest{
				Username: "user",
				Password: "pass",
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateHelmRegistryCredential() error = %v, wantErr %v", err, tt.wantErr)
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
