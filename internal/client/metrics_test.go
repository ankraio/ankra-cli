package client

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestQueryPrometheusInstant(t *testing.T) {
	t.Run("success sends query and parses result", func(t *testing.T) {
		var receivedBody map[string]interface{}
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/kubernetes/metrics/query") {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				t.Errorf("missing bearer token")
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &receivedBody)
			resultType := "vector"
			jsonResponse(t, w, http.StatusOK, PrometheusQueryResult{
				Status:      "success",
				ResultType:  &resultType,
				Result:      []interface{}{map[string]interface{}{"metric": map[string]interface{}{}, "value": []interface{}{0, "1"}}},
				TotalSeries: 1,
			})
		})

		result, err := client.QueryPrometheusInstant("cluster-1", "up", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "success" || result.TotalSeries != 1 {
			t.Errorf("unexpected result: %+v", result)
		}
		if receivedBody["query"] != "up" {
			t.Errorf("expected query=up, got %v", receivedBody["query"])
		}
		if _, present := receivedBody["timeout_seconds"]; present {
			t.Errorf("timeout_seconds should be omitted when zero")
		}
	})

	t.Run("timeout is sent when provided", func(t *testing.T) {
		var receivedBody map[string]interface{}
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &receivedBody)
			jsonResponse(t, w, http.StatusOK, PrometheusQueryResult{Status: "success"})
		})

		if _, err := client.QueryPrometheusInstant("cluster-1", "up", 45); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receivedBody["timeout_seconds"] != float64(45) {
			t.Errorf("expected timeout_seconds=45, got %v", receivedBody["timeout_seconds"])
		}
	})

	t.Run("503 maps to cluster unavailable error", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, http.StatusServiceUnavailable, ApiErrorResponse{ErrorCode: "NO_AGENT", Detail: "no agent"})
		})

		_, err := client.QueryPrometheusInstant("cluster-1", "up", 0)
		var unavailable *ClusterUnavailableError
		if err == nil || !asClusterUnavailable(err, &unavailable) {
			t.Fatalf("expected ClusterUnavailableError, got %v", err)
		}
		if unavailable.ErrorCode != "NO_AGENT" {
			t.Errorf("expected NO_AGENT, got %s", unavailable.ErrorCode)
		}
	})

	t.Run("401 returns unauthorized error", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})

		_, err := client.QueryPrometheusInstant("cluster-1", "up", 0)
		if err == nil || !strings.Contains(err.Error(), "ankra login") {
			t.Fatalf("expected unauthorized error, got %v", err)
		}
	})

	t.Run("400 returns redacted error", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, http.StatusBadRequest, map[string]string{"detail": "bad query"})
		})

		_, err := client.QueryPrometheusInstant("cluster-1", "up{", 0)
		if err == nil || !strings.Contains(err.Error(), "status 400") {
			t.Fatalf("expected status 400 error, got %v", err)
		}
	})
}

func TestQueryPrometheusRange(t *testing.T) {
	t.Run("sends range and step", func(t *testing.T) {
		var receivedBody map[string]interface{}
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/kubernetes/metrics/query_range") {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &receivedBody)
			jsonResponse(t, w, http.StatusOK, PrometheusQueryResult{Status: "success"})
		})

		_, err := client.QueryPrometheusRange("cluster-1", PrometheusRangeOptions{Query: "up", Range: "1h", Step: "5m"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receivedBody["range"] != "1h" || receivedBody["step"] != "5m" {
			t.Errorf("unexpected body: %+v", receivedBody)
		}
		if _, present := receivedBody["start"]; present {
			t.Errorf("start should be omitted when empty")
		}
	})

	t.Run("parses explicit start and end as numbers", func(t *testing.T) {
		var receivedBody map[string]interface{}
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &receivedBody)
			jsonResponse(t, w, http.StatusOK, PrometheusQueryResult{Status: "success"})
		})

		_, err := client.QueryPrometheusRange("cluster-1", PrometheusRangeOptions{
			Query: "up", Start: "1000", End: "4600", Step: "60s",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receivedBody["start"] != float64(1000) || receivedBody["end"] != float64(4600) {
			t.Errorf("unexpected start/end: %+v", receivedBody)
		}
	})

	t.Run("invalid start is rejected before request", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("request should not be sent for invalid start")
		})

		_, err := client.QueryPrometheusRange("cluster-1", PrometheusRangeOptions{Query: "up", Start: "not-a-number"})
		if err == nil || !strings.Contains(err.Error(), "invalid --start") {
			t.Fatalf("expected invalid start error, got %v", err)
		}
	})
}

func asClusterUnavailable(err error, target **ClusterUnavailableError) bool {
	if cu, ok := err.(*ClusterUnavailableError); ok {
		*target = cu
		return true
	}
	return false
}
