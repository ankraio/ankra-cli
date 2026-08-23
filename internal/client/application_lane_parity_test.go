package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// These pin the method, path and body of the bearer-lane application
// operations the CLI reached for the first time. A path typo here fails as a
// 404 at runtime against a real platform and passes every unit test that only
// checks the command wiring, so the assertions are on the request itself.

func TestListApplicationEnvSecretsTargetsTheEnvSecretsRoute(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != "/api/v1/org/applications/app-1/env-secrets" {
			t.Errorf("path = %s", request.URL.Path)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"secrets": []any{}})
	})

	if _, listError := testClient.ListApplicationEnvSecrets(context.Background(), "app-1"); listError != nil {
		t.Fatalf("ListApplicationEnvSecrets error = %v", listError)
	}
}

func TestSetApplicationEnvSecretSendsOnlyTheValue(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", request.Method)
		}
		if request.URL.Path != "/api/v1/org/applications/app-1/env-secrets/DATABASE_URL" {
			t.Errorf("path = %s", request.URL.Path)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		if body["value"] != "postgres://example" {
			t.Errorf("value = %v", body["value"])
		}
		// The value is the only inbound secret on this surface; anything
		// else in the body is a field that could be echoed back.
		if len(body) != 1 {
			t.Errorf("body carries more than the value: %v", body)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"key": "DATABASE_URL", "has_value": true})
	})

	if _, setError := testClient.SetApplicationEnvSecret(context.Background(),
		"app-1", "DATABASE_URL", "postgres://example"); setError != nil {
		t.Fatalf("SetApplicationEnvSecret error = %v", setError)
	}
}

// A key is a path segment, so one carrying a character that means something in
// a URL must not be able to reshape the request.
func TestSetApplicationEnvSecretEscapesTheKey(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/applications/app-1/env-secrets/A/B" {
			t.Errorf("decoded path = %s", request.URL.Path)
		}
		if request.URL.EscapedPath() != "/api/v1/org/applications/app-1/env-secrets/A%2FB" {
			t.Errorf("escaped path = %s, want the key escaped", request.URL.EscapedPath())
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"key": "A/B"})
	})

	if _, setError := testClient.SetApplicationEnvSecret(context.Background(),
		"app-1", "A/B", "value"); setError != nil {
		t.Fatalf("SetApplicationEnvSecret error = %v", setError)
	}
}

func TestDeleteApplicationEnvSecretTargetsTheKey(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", request.Method)
		}
		if request.URL.Path != "/api/v1/org/applications/app-1/env-secrets/API_TOKEN" {
			t.Errorf("path = %s", request.URL.Path)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"deleted": true})
	})

	if _, deleteError := testClient.DeleteApplicationEnvSecret(context.Background(),
		"app-1", "API_TOKEN"); deleteError != nil {
		t.Fatalf("DeleteApplicationEnvSecret error = %v", deleteError)
	}
}

// The apply is a POST with no body on purpose: the values it applies are the
// ones already stored, so it is the one route here that needs nothing inbound.
func TestApplyApplicationEnvSecretsSendsNoBody(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/org/applications/app-1/env-secrets/apply" {
			t.Errorf("path = %s", request.URL.Path)
		}
		if request.ContentLength > 0 {
			t.Errorf("content length = %d, want no body", request.ContentLength)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"applied_count": 2})
	})

	if _, applyError := testClient.ApplyApplicationEnvSecrets(context.Background(), "app-1"); applyError != nil {
		t.Fatalf("ApplyApplicationEnvSecrets error = %v", applyError)
	}
}

func TestGetApplicationAutoDeployTargetsTheAutoDeployRoute(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != "/api/v1/org/applications/app-1/auto-deploy" {
			t.Errorf("path = %s", request.URL.Path)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"enabled": true})
	})

	if _, getError := testClient.GetApplicationAutoDeploy(context.Background(), "app-1"); getError != nil {
		t.Fatalf("GetApplicationAutoDeploy error = %v", getError)
	}
}

// The switch is a required boolean, so "off" has to ride as an explicit false
// rather than being omitted - an absent key is a 422 from the endpoint, not a
// request to turn it off.
func TestSetApplicationAutoDeploySendsOffAsAnExplicitFalse(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", request.Method)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		enabled, present := body["enabled"]
		if !present {
			t.Fatalf("enabled is absent: %v", body)
		}
		if enabled != false {
			t.Errorf("enabled = %v, want false", enabled)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"enabled": false})
	})

	if _, setError := testClient.SetApplicationAutoDeploy(context.Background(), "app-1", false); setError != nil {
		t.Fatalf("SetApplicationAutoDeploy error = %v", setError)
	}
}

// The settings routes sit on a static segment under the applications
// collection, not under an application id.
func TestGetApplicationSettingsTargetsTheCollectionSettingsRoute(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/applications/settings" {
			t.Errorf("path = %s", request.URL.Path)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"ci_runner_label": "self-hosted"})
	})

	if _, getError := testClient.GetApplicationSettings(context.Background()); getError != nil {
		t.Fatalf("GetApplicationSettings error = %v", getError)
	}
}

// Clearing the organisation's runner choice sends an explicit null. An absent
// key is a 422 missing-field error from the endpoint, so omitempty here would
// make "clear" fail rather than clear.
func TestUpdateApplicationSettingsClearSendsAnExplicitNull(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		body := map[string]json.RawMessage{}
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		raw, present := body["ci_runner_label"]
		if !present {
			t.Fatalf("ci_runner_label is absent: %v", body)
		}
		if string(raw) != "null" {
			t.Errorf("ci_runner_label = %s, want null", raw)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"ci_runner_label": nil})
	})

	if _, updateError := testClient.UpdateApplicationSettings(context.Background(), nil); updateError != nil {
		t.Fatalf("UpdateApplicationSettings error = %v", updateError)
	}
}

func TestUpdateApplicationSettingsSendsTheLabel(t *testing.T) {
	label := "self-hosted"
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		if body["ci_runner_label"] != "self-hosted" {
			t.Errorf("ci_runner_label = %v", body["ci_runner_label"])
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"ci_runner_label": "self-hosted"})
	})

	if _, updateError := testClient.UpdateApplicationSettings(context.Background(), &label); updateError != nil {
		t.Fatalf("UpdateApplicationSettings error = %v", updateError)
	}
}

// The manifest add-on operations key on a catalog id and hang off their own
// path root, not off /org/applications.
func TestManifestAddonOperationsTargetTheCatalogRoot(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		method string
		path   string
		invoke func(*Client) error
	}{
		{
			name: "get", method: http.MethodGet, path: "/api/v1/org/manifest-addons/addon-1",
			invoke: func(testClient *Client) error {
				_, callError := testClient.GetManifestAddon(context.Background(), "addon-1")
				return callError
			},
		},
		{
			name: "unpublish", method: http.MethodPost, path: "/api/v1/org/manifest-addons/addon-1/unpublish",
			invoke: func(testClient *Client) error {
				_, callError := testClient.UnpublishManifestAddon(context.Background(), "addon-1")
				return callError
			},
		},
		{
			name: "delete", method: http.MethodDelete, path: "/api/v1/org/manifest-addons/addon-1",
			invoke: func(testClient *Client) error {
				_, callError := testClient.DeleteManifestAddon(context.Background(), "addon-1")
				return callError
			},
		},
	} {
		t.Run(testCase.name, func(subTest *testing.T) {
			testClient := newTestClient(subTest, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != testCase.method {
					subTest.Errorf("method = %s, want %s", request.Method, testCase.method)
				}
				if request.URL.Path != testCase.path {
					subTest.Errorf("path = %s, want %s", request.URL.Path, testCase.path)
				}
				jsonResponse(subTest, writer, http.StatusOK, map[string]any{"success": true})
			})
			if callError := testCase.invoke(testClient); callError != nil {
				subTest.Fatalf("%s error = %v", testCase.name, callError)
			}
		})
	}
}

// 'to' is required by the endpoint; 'from' and 'paths' are only sent when
// given, so an unset 'from' keeps the backend's own default rather than
// pinning the comparison to an empty version.
func TestDiffManifestAddonSendsOnlyTheSelectorsGiven(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/manifest-addons/addon-1/diff" {
			t.Errorf("path = %s", request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("to") != "1.4.0" {
			t.Errorf("to = %q", query.Get("to"))
		}
		if _, present := query["from"]; present {
			t.Errorf("from must be omitted when unset: %q", request.URL.RawQuery)
		}
		if _, present := query["paths"]; present {
			t.Errorf("paths must be omitted when unset: %q", request.URL.RawQuery)
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"changes": []any{}})
	})

	if _, diffError := testClient.DiffManifestAddon(context.Background(),
		"addon-1", "1.4.0", "", nil); diffError != nil {
		t.Fatalf("DiffManifestAddon error = %v", diffError)
	}
}

// Repeated paths ride as repeated query keys, which is what the endpoint reads
// (query["paths"]); a comma-joined single value would be read as one path.
func TestDiffManifestAddonRepeatsEveryPath(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		paths := request.URL.Query()["paths"]
		if len(paths) != 2 || paths[0] != "deployment.yaml" || paths[1] != "service.yaml" {
			t.Errorf("paths = %v, want two repeated keys", paths)
		}
		if request.URL.Query().Get("from") != "1.2.0" {
			t.Errorf("from = %q", request.URL.Query().Get("from"))
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"changes": []any{}})
	})

	if _, diffError := testClient.DiffManifestAddon(context.Background(), "addon-1", "1.4.0", "1.2.0",
		[]string{"deployment.yaml", "service.yaml"}); diffError != nil {
		t.Fatalf("DiffManifestAddon error = %v", diffError)
	}
}

func TestInstallManifestAddonSendsTheClusterAndOmitsUnsetDefaults(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/manifest-addons/addon-1/install" {
			t.Errorf("path = %s", request.URL.Path)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		if body["cluster_id"] != "cluster-1" {
			t.Errorf("cluster_id = %v", body["cluster_id"])
		}
		// Namespace and version default from the add-on's own descriptor,
		// so an unset flag must not send an empty string over the default.
		for _, omitted := range []string{"namespace", "version", "inputs"} {
			if _, present := body[omitted]; present {
				t.Errorf("%s must be omitted when unset: %v", omitted, body)
			}
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{"installed": true})
	})

	if _, installError := testClient.InstallManifestAddon(context.Background(), "addon-1",
		InstallManifestAddonRequest{ClusterID: "cluster-1"}); installError != nil {
		t.Fatalf("InstallManifestAddon error = %v", installError)
	}
}
