package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"
)

// metricsErrorServer answers the metrics read with the agent's failure and
// the APIService probe with probeBody (raw JSON for one resource response),
// or a 500 when probeBody is empty.
func metricsErrorServer(t *testing.T, probeBody string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestBody, _ := io.ReadAll(request.Body)
		var decoded client.GetResourcesRequest
		if decodeError := json.Unmarshal(requestBody, &decoded); decodeError != nil || len(decoded.ResourceRequests) != 1 {
			t.Errorf("unexpected request body %s", requestBody)
			responseWriter.WriteHeader(http.StatusBadRequest)
			return
		}
		item := decoded.ResourceRequests[0]
		if !decoded.SkipCache {
			t.Errorf("the %s read must be live, got skip_cache=false", item.Kind)
		}
		switch item.Kind {
		case "NodeMetrics":
			_, _ = responseWriter.Write([]byte(`{"resource_responses":[{"status":"error","kind":"NodeMetrics","version":"v1beta1","items":[],"error_response":"Resource not found"}]}`))
		case "APIService":
			if item.Name != "v1beta1.metrics.k8s.io" || item.Group != "apiregistration.k8s.io" {
				t.Errorf("the probe must read the metrics APIService by name, got %+v", item)
			}
			if probeBody == "" {
				responseWriter.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = responseWriter.Write([]byte(`{"resource_responses":[` + probeBody + `]}`))
		default:
			t.Errorf("unexpected kind %q", item.Kind)
		}
	}))
	previousClient := apiClient
	apiClient = client.New("test-token", server.URL)
	t.Cleanup(func() {
		apiClient = previousClient
		server.Close()
	})
}

func TestTopSaysMetricsServerIsNotInstalledWhenTheAPIServiceIsAbsent(t *testing.T) {
	metricsErrorServer(t, `{"status":"error","kind":"APIService","version":"v1","items":[],"not_found":true,"error_response":"Resource not found"}`)
	_, fetchError := fetchLiveMetrics("cluster-id", "NodeMetrics", "nodes", "")
	if fetchError == nil {
		t.Fatal("a failed metrics read must be an error")
	}
	message := fetchError.Error()
	for _, expected := range []string{"metrics-server is not installed on this cluster", "ankra stack-profiles apply metrics-server"} {
		if !strings.Contains(message, expected) {
			t.Errorf("error %q should contain %q", message, expected)
		}
	}
	if strings.Contains(message, "healthy") {
		t.Errorf("an absent metrics-server is not an unhealthy one: %q", message)
	}
}

func TestTopDoesNotTreatASuccessfulEmptyAPIServiceReadAsNotInstalled(t *testing.T) {
	metricsErrorServer(t, `{"status":"success","kind":"APIService","version":"v1","items":[]}`)
	_, fetchError := fetchLiveMetrics("cluster-id", "NodeMetrics", "nodes", "")
	if fetchError == nil {
		t.Fatal("a failed metrics read must be an error")
	}
	message := fetchError.Error()
	if strings.Contains(message, "not installed on this cluster") {
		t.Errorf("a by-name read the apiserver did not answer with a 404 is unknown, not absent: %q", message)
	}
	if !strings.Contains(message, "could not answer") {
		t.Errorf("the error should say the read failed, got %q", message)
	}
}

func TestTopSaysMetricsServerIsUnhealthyWhenTheAPIServiceIsRegistered(t *testing.T) {
	metricsErrorServer(t, `{"status":"success","kind":"APIService","version":"v1","items":[{"metadata":{"name":"v1beta1.metrics.k8s.io"}}]}`)
	_, fetchError := fetchLiveMetrics("cluster-id", "NodeMetrics", "nodes", "")
	if fetchError == nil {
		t.Fatal("a failed metrics read must be an error")
	}
	message := fetchError.Error()
	for _, expected := range []string{"metrics-server is installed", "Resource not found", "Ready"} {
		if !strings.Contains(message, expected) {
			t.Errorf("error %q should contain %q", message, expected)
		}
	}
}

func TestTopDoesNotClaimMetricsServerIsMissingWhenTheProbeFails(t *testing.T) {
	cases := map[string]string{
		"probe request failed":                      "",
		"probe answered an error that is not a 404": `{"status":"error","kind":"APIService","version":"v1","items":[],"error_response":"forbidden"}`,
		"probe answered from the cache":             `{"status":"success","kind":"APIService","version":"v1","items":[],"cache_metadata":{"served_from_cache":true,"stale":true,"sync_status":"agent_offline"}}`,
	}
	for name, probeBody := range cases {
		t.Run(name, func(subtest *testing.T) {
			metricsErrorServer(subtest, probeBody)
			_, fetchError := fetchLiveMetrics("cluster-id", "NodeMetrics", "nodes", "")
			if fetchError == nil {
				subtest.Fatal("a failed metrics read must be an error")
			}
			message := fetchError.Error()
			if strings.Contains(message, "not installed on this cluster") {
				subtest.Errorf("an unreadable APIService is unknown, not absent: %q", message)
			}
			for _, expected := range []string{"could not answer the metrics read", "Resource not found"} {
				if !strings.Contains(message, expected) {
					subtest.Errorf("error %q should contain %q", message, expected)
				}
			}
		})
	}
}
