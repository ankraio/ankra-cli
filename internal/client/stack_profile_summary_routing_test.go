package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSummaryRoutingPreviewConfirmsExactDestination(t *testing.T) {
	for _, scenario := range []string{"europe", "global", "any eligible", "old backend", "missing upstream", "null upstream", "wrong ingress", "wrong upstream", "wrong provider", "wrong endpoint", "unavailable", "invalid digest"} {
		t.Run(scenario, func(t *testing.T) {
			routing := ProfileSummaryRouting{Ingress: "europe", Upstream: "google-vertex/europe"}
			endpoint := "https://eu.openrouter.ai/api/v1"
			if scenario == "global" {
				routing.Ingress = "global"
				endpoint = "https://openrouter.ai/api/v1"
			}
			if scenario == "any eligible" || scenario == "missing upstream" || scenario == "null upstream" {
				routing.Upstream = ""
			}
			calls := 0
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				query := r.URL.Query()
				if r.Method != "GET" || r.URL.Path != profileSummaryPolicyPath || len(query) != 2 || len(query["openrouter_upstream"]) != 1 || query.Get("openrouter_upstream") != routing.Upstream || query.Get("openrouter_ingress") != routing.Ingress || r.Header.Get("Authorization") == "" {
					t.Errorf("preview changed route or authority: %s %s", r.Method, r.URL)
				}
				echo := routing
				route := map[string]any{"provider": "openrouter", "endpoint_url": endpoint, "consent_digest": strings.Repeat("a", 64), "openrouter_routing": &echo}
				body := map[string]any{"route_state": "available", "route": route}
				switch scenario {
				case "old backend":
					delete(route, "openrouter_routing")
				case "missing upstream":
					route["openrouter_routing"] = map[string]any{"ingress": routing.Ingress}
				case "null upstream":
					route["openrouter_routing"] = map[string]any{"ingress": routing.Ingress, "upstream": nil}
				case "wrong ingress":
					echo.Ingress = "global"
				case "wrong upstream":
					echo.Upstream = ""
				case "wrong provider":
					route["provider"] = "anthropic"
				case "wrong endpoint":
					route["endpoint_url"] = "https://example.invalid/api/v1"
				case "unavailable":
					body["route_state"] = "unavailable"
					body["route"] = nil
				case "invalid digest":
					route["consent_digest"] = "not-a-digest"
				}
				jsonResponse(t, w, 200, body)
			})
			payload, requestError := c.PreviewProfileSummaryPolicy(context.Background(), routing)
			success := scenario == "europe" || scenario == "global" || scenario == "any eligible"
			if calls != 1 || (requestError == nil) != success || (!success && payload != nil) || (success && !json.Valid(payload)) {
				t.Fatalf("preview accepted unconfirmed route: %v calls=%d", requestError, calls)
			}
		})
	}
}

func TestSummaryRoutingApprovalSerializesExactChoice(t *testing.T) {
	for _, routing := range []*ProfileSummaryRouting{nil, {Ingress: "global", Upstream: ""}, {Ingress: "europe", Upstream: "google-vertex/europe"}} {
		calls := 0
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil || r.Method != "PUT" || r.URL.Path != profileSummaryPolicyPath {
				t.Fatal("invalid approval request")
			}
			if routing == nil {
				if _, present := body["openrouter_routing"]; present {
					t.Error("omitted routing changed saved choice")
				}
			} else {
				var actual ProfileSummaryRouting
				if json.Unmarshal(body["openrouter_routing"], &actual) != nil || actual != *routing {
					t.Error("approval lost exact routing")
				}
			}
			jsonResponse(t, w, 200, map[string]any{"revision": 1, "state": "approved"})
		})
		if _, requestError := c.ApproveProfileSummaryPolicy(context.Background(), ProfileSummaryPolicyApprovalRequest{OpenRouterRouting: routing}); requestError != nil || calls != 1 {
			t.Fatalf("approval failed: %v", requestError)
		}
	}
}

func TestSummaryRoutingInvalidChoiceNeverReachesNetwork(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid route reached server") })
	for _, routing := range []ProfileSummaryRouting{{}, {Ingress: "custom"}, {Ingress: "europe", Upstream: "a/b/c"}, {Ingress: "europe", Upstream: strings.Repeat("a", 129)}} {
		if _, requestError := c.PreviewProfileSummaryPolicy(context.Background(), routing); requestError == nil {
			t.Fatal("invalid preview accepted")
		}
		if _, requestError := c.ApproveProfileSummaryPolicy(context.Background(), ProfileSummaryPolicyApprovalRequest{OpenRouterRouting: &routing}); requestError == nil {
			t.Fatal("invalid approval accepted")
		}
	}
}
