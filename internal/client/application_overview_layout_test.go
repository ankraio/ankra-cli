package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// These pin the method, path and body of the Overview layout routes, and how
// each refusal arrives: a stale base as the conflict with what the Overview
// shows now, a coded refusal as the platform's sentence with its code.

const overviewLayoutTestTeam = `{"on_page": "team", "revision": {"id": "11111111-2222-4333-8444-555555555555",
  "number": 3, "content": "{\"schema_version\": 2}", "author_user_id": "u-1", "author_name": "Ada",
  "created_at": "2026-11-18T09:30:00Z"}, "versions": 3}`

const overviewLayoutTestAnkra = `{"on_page": "ankra", "revision": null, "versions": 3}`

const overviewLayoutTestStaleDetail = "The Overview layout changed since base_revision_id; reload it and apply the change again."

func writeOverviewLayoutAnswer(writer http.ResponseWriter, status int, body string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(body))
}

// readOverviewLayoutBody decodes a request body, keeping null members.
func readOverviewLayoutBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	payload, _ := io.ReadAll(request.Body)
	var body map[string]any
	if decodeError := json.Unmarshal(payload, &body); decodeError != nil {
		t.Fatalf("request body is not a JSON object: %v: %s", decodeError, payload)
	}
	return body
}

func TestGetApplicationOverviewLayoutReadsTheTeamVersionAndKeepsTheDocument(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/org/applications/app-1/overview-layout" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		writeOverviewLayoutAnswer(writer, http.StatusOK, overviewLayoutTestTeam)
	})

	layout, readError := testClient.GetApplicationOverviewLayout(context.Background(), "app-1")

	if readError != nil {
		t.Fatalf("GetApplicationOverviewLayout error = %v", readError)
	}
	if layout.OnPage != OverviewLayoutOnPageTeam || layout.Revision == nil || layout.Revision.Number != 3 || layout.Versions != 3 {
		t.Errorf("layout = %+v", layout)
	}
	if string(layout.RawJSON()) != overviewLayoutTestTeam {
		t.Errorf("the platform's document was not kept: %s", layout.RawJSON())
	}
}

func TestGetApplicationOverviewLayoutReadsAnkrasLayoutAsNoRevision(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writeOverviewLayoutAnswer(writer, http.StatusOK, overviewLayoutTestAnkra)
	})

	layout, readError := testClient.GetApplicationOverviewLayout(context.Background(), "app-1")

	if readError != nil {
		t.Fatalf("GetApplicationOverviewLayout error = %v", readError)
	}
	if layout.OnPage != OverviewLayoutOnPageAnkra || layout.Revision != nil {
		t.Errorf("layout = %+v", layout)
	}
}

func TestListApplicationOverviewLayoutVersionsSendsTheCursorAndLimit(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/org/applications/app-1/overview-layout/versions" {
			t.Errorf("path = %s", request.URL.Path)
		}
		if request.URL.Query().Get("cursor") != "next-1" || request.URL.Query().Get("limit") != "10" {
			t.Errorf("query = %s", request.URL.RawQuery)
		}
		writeOverviewLayoutAnswer(writer, http.StatusOK, `{"items": [{"id": "v-1", "number": 1, "author_user_id": null,
		  "author_name": null, "created_at": "2026-11-17T08:00:00Z", "size_bytes": 812, "on_page": false}], "next_cursor": "next-2"}`)
	})

	versions, listError := testClient.ListApplicationOverviewLayoutVersions(context.Background(), "app-1", "next-1", 10)

	if listError != nil {
		t.Fatalf("ListApplicationOverviewLayoutVersions error = %v", listError)
	}
	if len(versions.Items) != 1 || versions.Items[0].SizeBytes != 812 || versions.NextCursor == nil || *versions.NextCursor != "next-2" {
		t.Errorf("versions = %+v", versions)
	}
}

func TestListApplicationOverviewLayoutVersionsFirstPageSendsNoQuery(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.RawQuery != "" {
			t.Errorf("query = %q, want none", request.URL.RawQuery)
		}
		writeOverviewLayoutAnswer(writer, http.StatusOK, `{"items": null, "next_cursor": null}`)
	})

	versions, listError := testClient.ListApplicationOverviewLayoutVersions(context.Background(), "app-1", "", 0)

	if listError != nil {
		t.Fatalf("ListApplicationOverviewLayoutVersions error = %v", listError)
	}
	if versions.Items == nil || versions.NextCursor != nil {
		t.Errorf("versions = %+v", versions)
	}
}

func TestSaveApplicationOverviewLayoutVersionSendsANullBaseForAnkrasLayout(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/applications/app-1/overview-layout/versions" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		body := readOverviewLayoutBody(t, request)
		base, hasBase := body["base_revision_id"]
		if !hasBase || base != nil {
			t.Errorf("base_revision_id = %v (sent %v), want null", base, hasBase)
		}
		if body["content"] != `{"schema_version": 2}` || len(body) != 2 {
			t.Errorf("body = %v", body)
		}
		writeOverviewLayoutAnswer(writer, http.StatusCreated, overviewLayoutTestTeam)
	})

	layout, saveError := testClient.SaveApplicationOverviewLayoutVersion(context.Background(), "app-1", `{"schema_version": 2}`, nil)

	if saveError != nil {
		t.Fatalf("SaveApplicationOverviewLayoutVersion error = %v", saveError)
	}
	if layout.Revision == nil || layout.Revision.Number != 3 {
		t.Errorf("layout = %+v", layout)
	}
}

func TestPutBackApplicationOverviewLayoutVersionNamesTheVersionAndTheBase(t *testing.T) {
	base := "11111111-2222-4333-8444-555555555555"
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/api/v1/org/applications/app-1/overview-layout/versions/v-1/put-back" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		body := readOverviewLayoutBody(t, request)
		if body["base_revision_id"] != base || len(body) != 1 {
			t.Errorf("body = %v", body)
		}
		writeOverviewLayoutAnswer(writer, http.StatusCreated, overviewLayoutTestTeam)
	})

	if _, putBackError := testClient.PutBackApplicationOverviewLayoutVersion(context.Background(), "app-1", "v-1", &base); putBackError != nil {
		t.Fatalf("PutBackApplicationOverviewLayoutVersion error = %v", putBackError)
	}
}

func TestUseAnkraApplicationOverviewLayoutPostsTheBase(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/applications/app-1/overview-layout/use-ankra" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		body := readOverviewLayoutBody(t, request)
		if base, hasBase := body["base_revision_id"]; !hasBase || base != nil {
			t.Errorf("body = %v", body)
		}
		writeOverviewLayoutAnswer(writer, http.StatusOK, overviewLayoutTestAnkra)
	})

	layout, useAnkraError := testClient.UseAnkraApplicationOverviewLayout(context.Background(), "app-1", nil)

	if useAnkraError != nil {
		t.Fatalf("UseAnkraApplicationOverviewLayout error = %v", useAnkraError)
	}
	if layout.OnPage != OverviewLayoutOnPageAnkra {
		t.Errorf("layout = %+v", layout)
	}
}

func TestOverviewLayoutStaleBaseIsTheConflictWithWhatTheOverviewShowsNow(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writeOverviewLayoutAnswer(writer, http.StatusConflict, `{"detail": "`+overviewLayoutTestStaleDetail+`",
		  "error_code": "canvas_revision_conflict", "current_revision_id": "11111111-2222-4333-8444-555555555555",
		  "layout": `+overviewLayoutTestTeam+`}`)
	})

	_, saveError := testClient.SaveApplicationOverviewLayoutVersion(context.Background(), "app-1", "{}", nil)

	var conflict *OverviewLayoutConflictError
	if !errors.As(saveError, &conflict) {
		t.Fatalf("error = %v (%T), want an *OverviewLayoutConflictError", saveError, saveError)
	}
	if conflict.Error() != overviewLayoutTestStaleDetail {
		t.Errorf("sentence = %q", conflict.Error())
	}
	if conflict.CurrentRevisionID == nil || *conflict.CurrentRevisionID != "11111111-2222-4333-8444-555555555555" ||
		conflict.Layout.Revision == nil || conflict.Layout.Revision.Number != 3 {
		t.Errorf("conflict = %+v", conflict)
	}
}

func TestOverviewLayoutCodedRefusalKeepsItsSentenceCodeAndStatus(t *testing.T) {
	cases := map[string]struct {
		status   int
		body     string
		code     string
		sentence string
	}{
		"version not found": {http.StatusNotFound,
			`{"detail": "Overview layout version not found.", "error_code": "overview_layout_version_not_found"}`,
			OverviewLayoutCodeVersionNotFound, "Overview layout version not found."},
		"layout refused": {http.StatusBadRequest,
			`{"detail": "element a: props.card must be one of health, environments.", "error_code": "invalid_canvas_spec"}`,
			"invalid_canvas_spec", "element a: props.card must be one of health, environments."},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				writeOverviewLayoutAnswer(writer, testCase.status, testCase.body)
			})

			_, putBackError := testClient.PutBackApplicationOverviewLayoutVersion(context.Background(), "app-1", "v-1", nil)

			var refusal *OverviewLayoutRefusalError
			if !errors.As(putBackError, &refusal) || refusal.Code != testCase.code || putBackError.Error() != testCase.sentence {
				t.Fatalf("error = %v (%T)", putBackError, putBackError)
			}
			var unexpected *UnexpectedResponseError
			if !errors.As(putBackError, &unexpected) || unexpected.StatusCode != testCase.status {
				t.Errorf("status = %+v, want %d", unexpected, testCase.status)
			}
		})
	}
}

func TestOverviewLayoutOtherRefusalsKeepTheSharedClasses(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		check  func(t *testing.T, requestError error)
	}{
		"application not found": {http.StatusNotFound, `{"detail": "Application not found"}`,
			func(t *testing.T, requestError error) {
				var unexpected *UnexpectedResponseError
				var refusal *OverviewLayoutRefusalError
				if !errors.As(requestError, &unexpected) || unexpected.StatusCode != http.StatusNotFound ||
					requestError.Error() != "Application not found" || errors.As(requestError, &refusal) {
					t.Errorf("error = %v (%T)", requestError, requestError)
				}
			}},
		"permission denied": {http.StatusForbidden, `{"detail": "permission_denied", "permission": "applications.write"}`,
			func(t *testing.T, requestError error) {
				var denied *PermissionDeniedError
				if !errors.As(requestError, &denied) || denied.Permission != "applications.write" {
					t.Errorf("error = %v", requestError)
				}
			}},
		"unauthorized": {http.StatusUnauthorized, `{"detail": "Not authenticated"}`,
			func(t *testing.T, requestError error) {
				if !errors.Is(requestError, ErrUnauthorized) {
					t.Errorf("error = %v", requestError)
				}
			}},
		"conflict of another shape": {http.StatusConflict, `{"detail": "Something else."}`,
			func(t *testing.T, requestError error) {
				var conflict *OverviewLayoutConflictError
				if errors.As(requestError, &conflict) || requestError.Error() != "Something else." {
					t.Errorf("error = %v (%T)", requestError, requestError)
				}
			}},
		"server error": {http.StatusInternalServerError, `Internal Server Error`,
			func(t *testing.T, requestError error) {
				if requestError == nil || !strings.Contains(requestError.Error(), "status 500") {
					t.Errorf("error = %v", requestError)
				}
			}},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				writeOverviewLayoutAnswer(writer, testCase.status, testCase.body)
			})

			_, useAnkraError := testClient.UseAnkraApplicationOverviewLayout(context.Background(), "app-1", nil)

			testCase.check(t, useAnkraError)
		})
	}
}
