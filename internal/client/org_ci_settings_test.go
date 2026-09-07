package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The organisation CI settings lane parity table. A path or method typo here
// fails as a 404 against a real platform and passes every command-wiring test
// that only checks the flag plumbing, so both routes are pinned on the request
// itself, the way the agent CI settings lane is.
func TestOrganisationCISettingsLaneParity(t *testing.T) {
	lanes := []struct {
		name       string
		wantMethod string
		wantBody   map[string]any
		call       func(testClient *Client) error
	}{
		{
			name:       "get reads the organisation's settings",
			wantMethod: http.MethodGet,
			call: func(testClient *Client) error {
				_, getError := testClient.GetOrganisationCISettings(context.Background())
				return getError
			},
		},
		{
			// The endpoint reads presence, so the body must carry exactly
			// the keys the caller named and nothing it did not.
			name:       "set sends only the named setting",
			wantMethod: http.MethodPut,
			wantBody:   map[string]any{"ci_build_fallback": "none"},
			call: func(testClient *Client) error {
				_, updateError := testClient.UpdateOrganisationCISettings(context.Background(),
					map[string]any{"ci_build_fallback": "none"})
				return updateError
			},
		},
		{
			// Clearing the pipeline cluster is a present key with a null
			// value; an absent key would leave the cluster untouched.
			name:       "set clears the cluster with an explicit null",
			wantMethod: http.MethodPut,
			wantBody:   map[string]any{"ci_cluster_id": nil},
			call: func(testClient *Client) error {
				_, updateError := testClient.UpdateOrganisationCISettings(context.Background(),
					map[string]any{"ci_cluster_id": nil})
				return updateError
			},
		},
	}

	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			var seenMethod, seenPath string
			var seenBody map[string]any
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				seenMethod, seenPath = request.Method, request.URL.Path
				if request.Method != http.MethodGet {
					if decodeError := json.NewDecoder(request.Body).Decode(&seenBody); decodeError != nil {
						t.Fatalf("decode request body: %v", decodeError)
					}
				}
				jsonResponse(t, writer, http.StatusOK, OrganisationCISettings{
					BuildFallback: CIBuildFallbackPlatformBuilders})
			})

			if callError := lane.call(testClient); callError != nil {
				t.Fatalf("call error = %v", callError)
			}
			if seenMethod != lane.wantMethod {
				t.Errorf("method = %s, want %s", seenMethod, lane.wantMethod)
			}
			if seenPath != "/api/v1/org/ci-settings" {
				t.Errorf("path = %s", seenPath)
			}
			if lane.wantBody == nil {
				return
			}
			if len(seenBody) != len(lane.wantBody) {
				t.Fatalf("body = %v, want exactly %v", seenBody, lane.wantBody)
			}
			for key, want := range lane.wantBody {
				got, isPresent := seenBody[key]
				if !isPresent {
					t.Errorf("body is missing %q: %v", key, seenBody)
					continue
				}
				if got != want {
					t.Errorf("body[%q] = %v, want %v", key, got, want)
				}
			}
		})
	}
}

// The wire record as production serves it today, every field included. A
// struct that dropped a field would still decode cleanly, so the only test
// that catches the omission is one that asserts the whole record.
func TestGetOrganisationCISettingsDecodesEveryField(t *testing.T) {
	const productionShape = `{
		"ci_cluster_id": "858f9fb3-b2d1-4568-86e4-600de3944130",
		"ci_cluster_name": "launch-week-2026",
		"ci_build_fallback": "platform_builders",
		"ci_max_parallel_runs": 4,
		"ci_max_parallel_steps": 8,
		"ci_allowed_image_prefixes": ["ghcr.io/ankraio"],
		"ci_artifact_retention_days": 30,
		"ci_cache_retention_days": 14,
		"ci_run_retention_days": 90,
		"ci_image_gate": "app",
		"ci_ignore_unfixed": true,
		"ci_egress_allowed_cidrs": ["10.0.0.0/8"],
		"is_default": false,
		"updated_at": "2026-09-06T19:45:53.034986Z"
	}`
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(productionShape))
	})

	settings, getError := testClient.GetOrganisationCISettings(context.Background())
	if getError != nil {
		t.Fatalf("GetOrganisationCISettings error = %v", getError)
	}
	if settings.ClusterID == nil || *settings.ClusterID != "858f9fb3-b2d1-4568-86e4-600de3944130" {
		t.Errorf("ClusterID = %v", settings.ClusterID)
	}
	if settings.ClusterName == nil || *settings.ClusterName != "launch-week-2026" {
		t.Errorf("ClusterName = %v", settings.ClusterName)
	}
	if settings.BuildFallback != CIBuildFallbackPlatformBuilders {
		t.Errorf("BuildFallback = %q", settings.BuildFallback)
	}
	if settings.MaxParallelRuns != 4 || settings.MaxParallelSteps != 8 {
		t.Errorf("parallelism = %d/%d", settings.MaxParallelRuns, settings.MaxParallelSteps)
	}
	if len(settings.AllowedImagePrefixes) != 1 || settings.AllowedImagePrefixes[0] != "ghcr.io/ankraio" {
		t.Errorf("AllowedImagePrefixes = %v", settings.AllowedImagePrefixes)
	}
	if len(settings.EgressAllowedCIDRs) != 1 || settings.EgressAllowedCIDRs[0] != "10.0.0.0/8" {
		t.Errorf("EgressAllowedCIDRs = %v", settings.EgressAllowedCIDRs)
	}
	if settings.ArtifactRetentionDays != 30 || settings.CacheRetentionDays != 14 || settings.RunRetentionDays != 90 {
		t.Errorf("retention = %d/%d/%d", settings.ArtifactRetentionDays,
			settings.CacheRetentionDays, settings.RunRetentionDays)
	}
	if settings.ImageGate != CIImageGateApplicationDependencies {
		t.Errorf("ImageGate = %q", settings.ImageGate)
	}
	if !settings.IgnoreUnfixed {
		t.Errorf("IgnoreUnfixed = false, want true")
	}
	if settings.IsDefault {
		t.Errorf("IsDefault = true, want false")
	}
	if settings.UpdatedAt == nil {
		t.Errorf("UpdatedAt was dropped")
	}
}

// A member changing the settings is refused by the admin gate. That is an RBAC
// refusal - the caller's identity is fine, their role is not - so it must be
// the exit-7 PermissionDeniedError, not the exit-6 "re-login" a bare 403
// earns, and the platform's own sentence must survive the trip.
func TestUpdateOrganisationCISettingsAdminGateIsAPermissionDenial(t *testing.T) {
	const gateSentence = "Changing the organisation's CI settings requires the admin role."
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusForbidden, map[string]string{"detail": gateSentence})
	})

	_, updateError := testClient.UpdateOrganisationCISettings(context.Background(),
		map[string]any{"ci_build_fallback": "none"})
	var denied *PermissionDeniedError
	if !errors.As(updateError, &denied) {
		t.Fatalf("error = %T %v, want *PermissionDeniedError", updateError, updateError)
	}
	if !strings.Contains(updateError.Error(), gateSentence) {
		t.Errorf("the gate's own sentence must be relayed, got %q", updateError.Error())
	}
}

// The platform's RBAC shape for a 403 keeps its existing handling: the
// permission it names is what the caller reads.
func TestUpdateOrganisationCISettingsKeepsTheRBACPermissionName(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusForbidden, map[string]string{
			"detail": "permission_denied", "permission": "organisation.settings.write"})
	})

	_, updateError := testClient.UpdateOrganisationCISettings(context.Background(),
		map[string]any{"ci_build_fallback": "none"})
	var denied *PermissionDeniedError
	if !errors.As(updateError, &denied) {
		t.Fatalf("error = %T %v, want *PermissionDeniedError", updateError, updateError)
	}
	if denied.Permission != "organisation.settings.write" {
		t.Errorf("Permission = %q", denied.Permission)
	}
}

// The endpoint writes two refusal shapes - a single sentence for the frozen
// refusals and a per-member list for validation - and the whole point of
// relaying them is that they name the setting and its bounds. Both shapes
// must reach the caller as words, not as a status code.
func TestOrganisationCISettingsRelaysBothRefusalShapes(t *testing.T) {
	shapes := []struct {
		name   string
		status int
		body   any
		want   string
	}{
		{
			name:   "single sentence",
			status: http.StatusUnprocessableEntity,
			body:   map[string]string{"detail": "Run retention must be between 7 and 365 days."},
			want:   "between 7 and 365 days",
		},
		{
			name:   "per-member list",
			status: http.StatusUnprocessableEntity,
			body: map[string]any{"detail": []map[string]string{
				{"msg": "Each allowed egress CIDR must sit inside private address space"},
				{"msg": "Run retention must be between 7 and 365 days."},
			}},
			want: "private address space; Run retention",
		},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				jsonResponse(t, writer, shape.status, shape.body)
			})
			_, updateError := testClient.UpdateOrganisationCISettings(context.Background(),
				map[string]any{"ci_run_retention_days": 1})
			if updateError == nil {
				t.Fatal("a refused write must return an error")
			}
			var unexpected *UnexpectedResponseError
			if !errors.As(updateError, &unexpected) || unexpected.StatusCode != shape.status {
				t.Fatalf("error = %T %v, want *UnexpectedResponseError with status %d",
					updateError, updateError, shape.status)
			}
			if !strings.Contains(updateError.Error(), shape.want) {
				t.Errorf("refusal vocabulary must be relayed, got %q", updateError.Error())
			}
		})
	}
}
