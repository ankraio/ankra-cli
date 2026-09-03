package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestListPods(t *testing.T) {
	tests := []struct {
		name    string
		opts    *ListPodsOptions
		handler http.HandlerFunc
		wantErr bool
		wantLen int
	}{
		{
			name: "success without options",
			opts: nil,
			handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "/kubernetes/pods") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, ListPodsResponse{
					Pods:       []PodSummary{{UID: "pod-1", Name: "nginx", Phase: "Running", Ready: "1/1"}},
					TotalCount: 1,
					Page:       1,
					PageSize:   25,
					TotalPages: 1,
				})
			},
			wantErr: false,
			wantLen: 1,
		},
		{
			name: "success with filters",
			opts: &ListPodsOptions{Page: 1, PageSize: 10, Namespace: "kube-system", NameContains: "coredns", NodeName: "node-1"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				if query.Get("namespace") != "kube-system" {
					t.Errorf("expected namespace=kube-system, got %s", query.Get("namespace"))
				}
				if query.Get("name_contains") != "coredns" {
					t.Errorf("expected name_contains=coredns, got %s", query.Get("name_contains"))
				}
				if query.Get("node_name") != "node-1" {
					t.Errorf("expected node_name=node-1, got %s", query.Get("node_name"))
				}
				if query.Get("page") != "1" {
					t.Errorf("expected page=1, got %s", query.Get("page"))
				}
				if query.Get("page_size") != "10" {
					t.Errorf("expected page_size=10, got %s", query.Get("page_size"))
				}
				jsonResponse(t, w, http.StatusOK, ListPodsResponse{
					Pods:       []PodSummary{{UID: "pod-2", Name: "coredns-abc", Phase: "Running", Ready: "1/1"}},
					TotalCount: 1,
					Page:       1,
					PageSize:   10,
					TotalPages: 1,
				})
			},
			wantErr: false,
			wantLen: 1,
		},
		{
			name: "401 unauthorized",
			opts: nil,
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
			got, err := testClient.ListPods("cluster-id", tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("ListPods() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got.Pods) != tt.wantLen {
				t.Errorf("ListPods() got %d pods, want %d", len(got.Pods), tt.wantLen)
			}
		})
	}
}

func TestGetResources(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
		errType string
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var reqBody GetResourcesRequest
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				kind := "Deployment"
				jsonResponse(t, w, http.StatusOK, GetResourcesResponse{
					ResourceResponses: []ResourceResponseItem{
						{Status: "ok", Kind: kind, Version: "apps/v1", Items: []interface{}{}},
					},
				})
			},
			wantErr: false,
		},
		{
			name: "503 cluster unavailable",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error_code":"CLUSTER_OFFLINE","detail":"Cluster is offline"}`))
			},
			wantErr: true,
			errType: "ClusterUnavailableError",
		},
		{
			name: "500 server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("internal error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			req := GetResourcesRequest{
				ResourceRequests: []ResourceRequestItem{
					{Kind: "Deployment", Version: "apps/v1"},
				},
			}
			got, err := testClient.GetResources("cluster-id", req)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetResources() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.errType == "ClusterUnavailableError" {
				var clusterErr *ClusterUnavailableError
				if err == nil {
					t.Errorf("GetResources() expected ClusterUnavailableError, got nil")
				} else {
					if !isClusterUnavailableError(err, &clusterErr) {
						t.Errorf("GetResources() expected ClusterUnavailableError, got %T", err)
					}
				}
			}
			if !tt.wantErr && len(got.ResourceResponses) != 1 {
				t.Errorf("GetResources() got %d responses, want 1", len(got.ResourceResponses))
			}
		})
	}
}

func isClusterUnavailableError(err error, target **ClusterUnavailableError) bool {
	return errors.As(err, target)
}

func TestStreamPodLogs(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantErr    bool
		wantOutput string
	}{
		{
			name: "success with SSE data lines",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if r.Header.Get("Accept") != "text/event-stream" {
					t.Errorf("expected Accept: text/event-stream")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintln(w, "data: log line one")
				_, _ = fmt.Fprintln(w, "data: log line two")
				_, _ = fmt.Fprintln(w, "data:log line three")
			},
			wantErr:    false,
			wantOutput: "log line one\nlog line two\nlog line three\n",
		},
		{
			name: "503 cluster offline",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error_code":"CLUSTER_OFFLINE","detail":"offline"}`))
			},
			wantErr: true,
		},
		{
			name: "500 error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("server error"))
			},
			wantErr: true,
		},
		{
			name: "SSE error event returns error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintln(w, "event: error")
				_, _ = fmt.Fprintln(w, "data: Log stream failed: HTTP 400 - container bad-name is not valid for pod my-pod")
			},
			wantErr: true,
		},
		{
			name: "empty container name omits query param",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("container_name") {
					t.Errorf("expected container_name to be absent, got %q", r.URL.Query().Get("container_name"))
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintln(w, "data: auto-selected container log")
			},
			wantErr:    false,
			wantOutput: "auto-selected container log\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			var buf bytes.Buffer
			opts := PodLogOptions{
				Namespace: "default",
				PodName:   "nginx-abc",
				TailLines: 100,
			}
			if tt.name != "empty container name omits query param" {
				opts.ContainerName = "nginx"
			}
			err := testClient.StreamPodLogs(context.Background(), "cluster-id", opts, &buf)
			if (err != nil) != tt.wantErr {
				t.Errorf("StreamPodLogs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && buf.String() != tt.wantOutput {
				t.Errorf("StreamPodLogs() output = %q, want %q", buf.String(), tt.wantOutput)
			}
		})
	}
}

func TestListHelmReleases(t *testing.T) {
	tests := []struct {
		name    string
		opts    *HelmReleasesOptions
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success all namespaces",
			opts: nil,
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, HelmReleasesResponse{
					ResourceResponses: []HelmReleasesResponseItem{
						{Status: "ok", AllNamespaces: true, Items: []interface{}{}},
					},
				})
			},
			wantErr: false,
		},
		{
			name: "success with namespace filter",
			opts: &HelmReleasesOptions{Namespace: "monitoring"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var reqBody HelmReleasesRequest
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if len(reqBody.ResourceRequests) != 1 || reqBody.ResourceRequests[0].Namespace != "monitoring" {
					t.Errorf("expected namespace=monitoring in request body")
				}
				ns := "monitoring"
				jsonResponse(t, w, http.StatusOK, HelmReleasesResponse{
					ResourceResponses: []HelmReleasesResponseItem{
						{Status: "ok", Namespace: &ns, Items: []interface{}{}},
					},
				})
			},
			wantErr: false,
		},
		{
			name: "503 cluster unavailable",
			opts: nil,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error_code":"NO_AGENT","detail":"no agent"}`))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.ListHelmReleases("cluster-id", tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("ListHelmReleases() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got.ResourceResponses) != 1 {
				t.Errorf("ListHelmReleases() got %d responses, want 1", len(got.ResourceResponses))
			}
		})
	}
}

func TestUninstallHelmRelease(t *testing.T) {
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
				var reqBody UninstallHelmReleaseRequest
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if reqBody.ReleaseName != "my-release" || reqBody.Namespace != "default" {
					t.Errorf("expected release_name=my-release namespace=default, got %s %s", reqBody.ReleaseName, reqBody.Namespace)
				}
				jsonResponse(t, w, http.StatusOK, UninstallHelmReleaseResponse{Status: "ok"})
			},
			wantErr: false,
		},
		{
			name: "503 cluster unavailable",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error_code":"AGENT_TIMEOUT","detail":"timeout"}`))
			},
			wantErr: true,
		},
		{
			name: "500 error",
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
			got, err := testClient.UninstallHelmRelease("cluster-id", "my-release", "default")
			if (err != nil) != tt.wantErr {
				t.Errorf("UninstallHelmRelease() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Status != "ok" {
				t.Errorf("UninstallHelmRelease() got.Status = %v, want ok", got.Status)
			}
		})
	}
}

func TestDeleteResource(t *testing.T) {
	gracePeriod := 0
	tests := []struct {
		name       string
		request    DeleteResourceRequest
		handler    http.HandlerFunc
		wantStatus string
		wantErr    bool
	}{
		{
			name:    "success forwards the pod address and grace period",
			request: DeleteResourceRequest{Kind: "pods", Version: "v1", Namespace: "default", Name: "web-0", GracePeriodSeconds: &gracePeriod},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/kubernetes/resources/delete") {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				var reqBody map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if reqBody["kind"] != "pods" || reqBody["name"] != "web-0" || reqBody["namespace"] != "default" || reqBody["version"] != "v1" {
					t.Errorf("unexpected body %v", reqBody)
				}
				if reqBody["grace_period_seconds"] != float64(0) {
					t.Errorf("grace_period_seconds should be 0 on the wire, got %v", reqBody["grace_period_seconds"])
				}
				if reqBody["dry_run"] != false {
					t.Errorf("dry_run should be false on the wire, got %v", reqBody["dry_run"])
				}
				if r.Header.Get(csrfHeaderName) == "" || r.Header.Get("Authorization") != "Bearer "+testToken {
					t.Error("a relay delete must carry the CSRF header alongside the bearer token")
				}
				jsonResponse(t, w, http.StatusOK, ResourceMutationResponse{Status: "success"})
			},
			wantStatus: "success",
		},
		{
			name:    "unset grace period stays off the wire",
			request: DeleteResourceRequest{Kind: "pods", Version: "v1", Namespace: "default", Name: "web-0"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				var reqBody map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if _, isPresent := reqBody["grace_period_seconds"]; isPresent {
					t.Errorf("grace_period_seconds must be omitted when unset, got %v", reqBody["grace_period_seconds"])
				}
				message := "already gone"
				jsonResponse(t, w, http.StatusOK, ResourceMutationResponse{Status: "not_found", Message: &message})
			},
			wantStatus: "not_found",
		},
		{
			name:    "503 cluster unavailable",
			request: DeleteResourceRequest{Kind: "pods", Version: "v1", Namespace: "default", Name: "web-0"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error_code":"CLUSTER_OFFLINE","detail":"offline"}`))
			},
			wantErr: true,
		},
		{
			name:    "403 sandbox mode",
			request: DeleteResourceRequest{Kind: "pods", Version: "v1", Namespace: "default", Name: "web-0"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error_code":"SANDBOX_MODE","detail":"writes are disabled"}`))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.DeleteResource("cluster-id", tt.request)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteResource() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Status != tt.wantStatus {
				t.Errorf("DeleteResource() got.Status = %v, want %v", got.Status, tt.wantStatus)
			}
		})
	}
}

func TestPatchResource(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/kubernetes/resources/patch") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if reqBody["kind"] != "Node" || reqBody["name"] != "worker-3" || reqBody["patch_type"] != "strategic" {
			t.Errorf("unexpected body %v", reqBody)
		}
		if _, hasNamespace := reqBody["namespace"]; hasNamespace {
			t.Errorf("a cluster-scoped patch must omit namespace, got %v", reqBody["namespace"])
		}
		patch, _ := reqBody["patch"].(map[string]interface{})
		spec, _ := patch["spec"].(map[string]interface{})
		if spec["unschedulable"] != true {
			t.Errorf("patch should carry spec.unschedulable=true, got %v", reqBody["patch"])
		}
		if r.Header.Get(csrfHeaderName) == "" {
			t.Error("a relay patch must carry the CSRF header alongside the bearer token")
		}
		jsonResponse(t, w, http.StatusOK, ResourceMutationResponse{Status: "success"})
	})
	got, err := testClient.PatchResource("cluster-id", PatchResourceRequest{
		Kind: "Node", Version: "v1", Name: "worker-3", PatchType: "strategic",
		Patch: map[string]interface{}{"spec": map[string]interface{}{"unschedulable": true}},
	})
	if err != nil {
		t.Fatalf("PatchResource() error = %v", err)
	}
	if got.Status != "success" {
		t.Errorf("PatchResource() status = %q, want success", got.Status)
	}
}

func TestGetHelmReleaseDetail(t *testing.T) {
	t.Run("success addresses namespace then release", func(t *testing.T) {
		chart := "traefik-30.0.0"
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/kubernetes/helm/releases/traefik-ns/traefik") {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				t.Errorf("a relay read must carry the bearer token, got %q", r.Header.Get("Authorization"))
			}
			jsonResponse(t, w, http.StatusOK, HelmReleaseDetail{
				Metadata:   HelmReleaseMetadata{Name: "traefik", Namespace: "traefik-ns", Revision: 4, Status: "deployed", Chart: &chart},
				UserValues: map[string]interface{}{"replicas": float64(2)},
			})
		})
		got, err := testClient.GetHelmReleaseDetail("cluster-id", "traefik-ns", "traefik")
		if err != nil {
			t.Fatalf("GetHelmReleaseDetail() error = %v", err)
		}
		if got.Metadata.Revision != 4 || got.UserValues["replicas"] != float64(2) {
			t.Errorf("unexpected detail %+v", got)
		}
	})
	t.Run("404 keeps the backend detail", func(t *testing.T) {
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"release traefik not found in namespace traefik-ns"}`))
		})
		_, err := testClient.GetHelmReleaseDetail("cluster-id", "traefik-ns", "traefik")
		var unexpected *UnexpectedResponseError
		if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
			t.Fatalf("expected a 404 UnexpectedResponseError, got %v", err)
		}
		if !strings.Contains(err.Error(), "release traefik not found") {
			t.Errorf("error should carry the backend detail, got %v", err)
		}
	})
	t.Run("503 cluster offline", func(t *testing.T) {
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error_code":"CLUSTER_OFFLINE","detail":"offline"}`))
		})
		_, err := testClient.GetHelmReleaseDetail("cluster-id", "traefik-ns", "traefik")
		var unavailable *ClusterUnavailableError
		if !errors.As(err, &unavailable) || unavailable.ErrorCode != "CLUSTER_OFFLINE" {
			t.Fatalf("expected ClusterUnavailableError, got %v", err)
		}
	})
}

func TestGetHelmReleaseHistory(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/kubernetes/helm/releases/traefik-ns/traefik/history") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("limit should be forwarded, got %q", r.URL.Query().Get("limit"))
		}
		jsonResponse(t, w, http.StatusOK, HelmReleaseHistory{Revisions: []HelmReleaseHistoryEntry{{Revision: 4, Status: "deployed"}}})
	})
	got, err := testClient.GetHelmReleaseHistory("cluster-id", "traefik-ns", "traefik", 10)
	if err != nil {
		t.Fatalf("GetHelmReleaseHistory() error = %v", err)
	}
	if len(got.Revisions) != 1 || got.Revisions[0].Revision != 4 {
		t.Errorf("unexpected history %+v", got)
	}
}

func TestRollbackHelmRelease(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/kubernetes/helm/releases/traefik-ns/traefik/rollback") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get(csrfHeaderName) == "" {
			t.Error("rollback must carry the CSRF header the backend requires")
		}
		var reqBody RollbackHelmReleaseRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if reqBody.Revision != 3 || !reqBody.Wait || reqBody.TimeoutSeconds != 600 {
			t.Errorf("unexpected body %+v", reqBody)
		}
		jsonResponse(t, w, http.StatusOK, HelmReleaseMutationResult{ReleaseName: "traefik", Namespace: "traefik-ns", Revision: 5, ElapsedMS: 1200})
	})
	got, err := testClient.RollbackHelmRelease("cluster-id", "traefik-ns", "traefik", RollbackHelmReleaseRequest{Revision: 3, Wait: true, TimeoutSeconds: 600})
	if err != nil {
		t.Fatalf("RollbackHelmRelease() error = %v", err)
	}
	if got.Revision != 5 {
		t.Errorf("RollbackHelmRelease() revision = %d, want 5", got.Revision)
	}
}

func TestUpgradeHelmRelease(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/kubernetes/helm/releases/traefik-ns/traefik/upgrade") {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
			if r.Header.Get(csrfHeaderName) == "" {
				t.Error("upgrade must carry the CSRF header the backend requires")
			}
			var reqBody UpgradeHelmReleaseRequest
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if reqBody.ChartRef != "traefik/traefik" || reqBody.ValuesYAML != "replicas: 3\n" || reqBody.ChartVersion != "" {
				t.Errorf("unexpected body %+v", reqBody)
			}
			jsonResponse(t, w, http.StatusOK, HelmReleaseMutationResult{ReleaseName: "traefik", Namespace: "traefik-ns", Revision: 6, ElapsedMS: 3000})
		})
		got, err := testClient.UpgradeHelmRelease("cluster-id", "traefik-ns", "traefik", UpgradeHelmReleaseRequest{
			ChartRef: "traefik/traefik", ValuesYAML: "replicas: 3\n", Wait: true, TimeoutSeconds: 600,
		})
		if err != nil {
			t.Fatalf("UpgradeHelmRelease() error = %v", err)
		}
		if got.Revision != 6 {
			t.Errorf("UpgradeHelmRelease() revision = %d, want 6", got.Revision)
		}
	})
	t.Run("409 managed by addon keeps the detail", func(t *testing.T) {
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"release traefik is managed by the Ankra addon traefik"}`))
		})
		_, err := testClient.UpgradeHelmRelease("cluster-id", "traefik-ns", "traefik", UpgradeHelmReleaseRequest{ChartRef: "traefik/traefik", ValuesYAML: ""})
		if err == nil || !strings.Contains(err.Error(), "managed by the Ankra addon") {
			t.Fatalf("expected the 409 detail to surface, got %v", err)
		}
	})
}

func TestClusterUnavailableError(t *testing.T) {
	tests := []struct {
		errorCode string
		wantMsg   string
	}{
		{"CLUSTER_OFFLINE", "Cluster is offline. Check that the Ankra agent is running."},
		{"NO_AGENT", "No agent available for this cluster. Install the Ankra agent first."},
		{"AGENT_TIMEOUT", "Agent is not responding. The cluster may be temporarily unreachable."},
		{"UNKNOWN_CODE", "some detail"},
	}
	for _, tt := range tests {
		t.Run(tt.errorCode, func(t *testing.T) {
			err := &ClusterUnavailableError{ErrorCode: tt.errorCode, Detail: "some detail"}
			if err.Error() != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", err.Error(), tt.wantMsg)
			}
		})
	}
}
