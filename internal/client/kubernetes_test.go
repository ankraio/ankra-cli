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
