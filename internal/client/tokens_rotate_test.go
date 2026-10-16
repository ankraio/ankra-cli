package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func rotateFixture(t *testing.T, w http.ResponseWriter, extra map[string]any) {
	t.Helper()
	body := map[string]any{
		"id":                    "new-id",
		"token":                 "ankra_new_secret",
		"name":                  "ci-deploy",
		"expires_at":            "2027-01-13T10:00:00Z",
		"type":                  "Bearer",
		"scopes":                []string{"mcp:read"},
		"replaced_token_id":     "old-id",
		"old_token_valid_until": "2026-10-16T10:00:00Z",
	}
	for key, value := range extra {
		body[key] = value
	}
	jsonResponse(t, w, http.StatusOK, body)
}

func TestRotateAPITokenSendsNoBodyWithoutOverlap(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/account/tokens/old-id/rotate" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if body, _ := io.ReadAll(r.Body); len(body) != 0 {
			t.Errorf("expected no body, got %q", body)
		}
		rotateFixture(t, w, nil)
	})
	got, err := testClient.RotateAPIToken("old-id", nil)
	if err != nil {
		t.Fatalf("RotateAPIToken() error = %v", err)
	}
	if got.ID != "new-id" || got.Token != "ankra_new_secret" || got.ReplacedTokenID != "old-id" ||
		got.OldTokenValidUntil != "2026-10-16T10:00:00Z" || len(got.Scopes) != 1 {
		t.Errorf("RotateAPIToken() got = %+v", got)
	}
}

func TestRotateAPITokenSendsOverlapHours(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var sent map[string]int
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if hours, ok := sent["overlap_hours"]; !ok || hours != 0 {
			t.Errorf("expected overlap_hours 0 in body, got %v", sent)
		}
		rotateFixture(t, w, nil)
	})
	zero := 0
	if _, err := testClient.RotateAPIToken("old-id", &zero); err != nil {
		t.Fatalf("RotateAPIToken() error = %v", err)
	}
}

func TestRotateServiceTokenUsesServiceRouteAndReadsExtraFields(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/organisation/service-tokens/old-id/rotate" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rotateFixture(t, w, map[string]any{"account_name": "svc-ci", "label": "deploys"})
	})
	hours := 72
	got, err := testClient.RotateServiceToken("old-id", &hours)
	if err != nil {
		t.Fatalf("RotateServiceToken() error = %v", err)
	}
	if got.AccountName != "svc-ci" || got.Label != "deploys" {
		t.Errorf("RotateServiceToken() got = %+v", got)
	}
}

func TestRotateTokenErrorsCarryTheDetailAndNeverTheToken(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict} {
		detail := map[int]string{404: "Token not found", 409: "Token is already replaced"}[status]
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, status, map[string]string{"detail": detail})
		})
		_, err := testClient.RotateAPIToken("old-id", nil)
		if err == nil || err.Error() != detail {
			t.Fatalf("status %d: error = %v, want %q", status, err, detail)
		}
		var unexpected *UnexpectedResponseError
		if !errors.As(err, &unexpected) || unexpected.StatusCode != status {
			t.Errorf("status %d: want UnexpectedResponseError carrying the status, got %v", status, err)
		}
	}
}

func TestRotateTokenRedactsTheTokenFromAnUnparsedErrorBody(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusInternalServerError, map[string]string{"token": "ankra_leaked_value"})
	})
	_, err := testClient.RotateAPIToken("old-id", nil)
	if err == nil || strings.Contains(err.Error(), "ankra_leaked_value") {
		t.Fatalf("error must exist and not echo the token, got %v", err)
	}
}
