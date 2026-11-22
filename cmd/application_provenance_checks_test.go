package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"
)

// These drive `ankra application provenance checks` and `verify
// --environment` through the real client against a stub platform, so what is
// asserted is the request that leaves and what the command prints.

const (
	provenanceChecksTestProduction = "5a1d7c3e-9b2f-4e6a-8c1d-2f3e4a5b6c7d"
	provenanceChecksTestStaging    = "6b2e8d4f-0c3a-4f7b-9d2e-3a4b5c6d7e8f"
	provenanceChecksTestPreview    = "7c3f9e5a-1d4b-4a8c-8e3f-4b5c6d7e8f9a"
	provenanceChecksTestClusterA   = "c1a2b3c4-d5e6-4f7a-8b9c-0d1e2f3a4b5c"
	provenanceChecksTestClusterB   = "c2b3c4d5-e6f7-4a8b-9c0d-1e2f3a4b5c6d"
	provenanceChecksTestPath       = "/api/v1/org/applications/" + testApplicationID + "/provenance/checks"
	provenanceChecksTestVerifyPath = "/api/v1/org/applications/" + testApplicationID + "/provenance/verify"
	provenanceChecksTestEnvsPath   = "/api/v1/org/applications/" + testApplicationID + "/environments"
)

const provenanceChecksTestEnvironments = `{"environments": [
  {"id": "` + provenanceChecksTestProduction + `", "name": "production", "role": "production"},
  {"id": "` + provenanceChecksTestStaging + `", "name": "staging", "role": "staging"},
  {"id": "` + provenanceChecksTestPreview + `", "name": "preview", "role": "development"}]}`

// provenanceChecksTestAnswer has one environment of each kind: production
// with a passed check, a check that did not pass and one never checked;
// staging with a check that could not be made; preview with no installation.
const provenanceChecksTestAnswer = `{"application_id": "` + testApplicationID + `", "read_state": "ok",
  "kept_from": "2026-11-01T00:00:00Z", "environments": [
  {"environment_id": "` + provenanceChecksTestProduction + `", "placements": [
    {"installation_id": "i-1", "cluster_id": "` + provenanceChecksTestClusterA + `", "namespace": "shop", "last_check": {
      "checked_at": "2026-11-18T09:30:00Z", "source": "release", "by": {"kind": "ankra", "name": null}, "image_tag": "sha-1a2b",
      "mode": "warn", "decision": "deploy", "state": "verified", "message": null, "images": [], "unchecked_images": [],
      "images_capped": false, "is_current": true}},
    {"installation_id": "i-2", "cluster_id": "` + provenanceChecksTestClusterB + `", "namespace": "shop", "last_check": {
      "checked_at": "2026-11-19T07:05:00Z", "source": "check_now", "by": {"kind": "member", "name": "Ada"}, "image_tag": "sha-0f9e",
      "mode": "warn", "decision": "deploy_with_finding", "state": "refused", "message": "no attestation", "images": [],
      "unchecked_images": [], "images_capped": false, "is_current": false}},
    {"installation_id": "i-3", "cluster_id": "` + provenanceChecksTestClusterB + `", "namespace": null, "last_check": null}],
   "placements_capped": true, "placements_not_checkable": 2},
  {"environment_id": "` + provenanceChecksTestStaging + `", "placements": [
    {"installation_id": "i-4", "cluster_id": "` + provenanceChecksTestClusterA + `", "namespace": "shop-staging", "last_check": {
      "checked_at": "2026-11-17T12:00:00Z", "source": "check_now", "by": {"kind": "former_member", "name": null}, "image_tag": null,
      "mode": "warn", "decision": "not_checked", "state": "unknown", "message": "the registry did not answer", "images": [],
      "unchecked_images": [], "images_capped": false, "is_current": null}}],
   "placements_capped": false, "placements_not_checkable": 0},
  {"environment_id": "` + provenanceChecksTestPreview + `", "placements": [], "placements_capped": false, "placements_not_checkable": 0}]}`

type provenanceChecksStubAnswer struct {
	status int
	body   string
}

type provenanceChecksStubRequest struct {
	method string
	path   string
	body   map[string]any
}

// provenanceChecksStub is a fake platform: one answer per "METHOD path", and
// every request it received, in order.
type provenanceChecksStub struct {
	t        *testing.T
	answers  map[string]provenanceChecksStubAnswer
	mutex    sync.Mutex
	requests []provenanceChecksStubRequest
}

func (stub *provenanceChecksStub) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	recorded := provenanceChecksStubRequest{method: request.Method, path: request.URL.Path}
	if payload, _ := io.ReadAll(request.Body); len(payload) > 0 {
		if decodeError := json.Unmarshal(payload, &recorded.body); decodeError != nil {
			stub.t.Errorf("request body is not a JSON object: %v: %s", decodeError, payload)
		}
	}
	stub.mutex.Lock()
	stub.requests = append(stub.requests, recorded)
	stub.mutex.Unlock()
	answer, isKnown := stub.answers[request.Method+" "+request.URL.Path]
	if !isKnown {
		stub.t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		answer = provenanceChecksStubAnswer{http.StatusNotFound, `{"detail": "Not Found"}`}
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(answer.status)
	_, _ = writer.Write([]byte(answer.body))
}

func (stub *provenanceChecksStub) sent(method string, path string) []provenanceChecksStubRequest {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	matched := []provenanceChecksStubRequest{}
	for _, request := range stub.requests {
		if request.method == method && request.path == path {
			matched = append(matched, request)
		}
	}
	return matched
}

func runProvenanceChecks(t *testing.T, answers map[string]provenanceChecksStubAnswer, arguments ...string) (*provenanceChecksStub, string, error) {
	t.Helper()
	stub := &provenanceChecksStub{t: t, answers: answers}
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	output, executeError := runApplicationCommandWithInput(t, client.New("test-token", server.URL), "",
		append([]string{"provenance"}, arguments...)...)
	return stub, output, executeError
}

func TestProvenanceChecksPrintsOneRowForEachInstallationAndSaysEachAnswerAsItself(t *testing.T) {
	_, output, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
		"GET " + provenanceChecksTestPath:     {http.StatusOK, provenanceChecksTestAnswer},
		"GET " + provenanceChecksTestEnvsPath: {http.StatusOK, provenanceChecksTestEnvironments},
	}, "checks", testApplicationID)

	if executeError != nil {
		t.Fatalf("checks error = %v", executeError)
	}
	expected := strings.Join([]string{
		"ENVIRONMENT  CLUSTER                               NAMESPACE     CHECKED               BY                          TAG                                ANSWER",
		"production   " + provenanceChecksTestClusterA + "  shop          2026-11-18 09:30 UTC  Ankra, at release           sha-1a2b                           verified",
		"production   " + provenanceChecksTestClusterB + "  shop          2026-11-19 07:05 UTC  Ada, check now              sha-0f9e (not the tag stored now)  did not pass: no attestation",
		"production   " + provenanceChecksTestClusterB + "  -             -                     -                           -                                  never checked",
		"staging      " + provenanceChecksTestClusterA + "  shop-staging  2026-11-17 12:00 UTC  a former member, check now  -                                  could not be checked: the registry did not answer",
		"preview      -                                     -             -                     -                           -                                  no installation to check",
		"production holds more installations than these 3; the rest are not listed.",
		"production also runs 2 deployments with no installation record, which no check covers.",
		"Checks are kept from 2026-11-01 00:00 UTC: never checked means no check of that installation since then.",
		"",
	}, "\n")
	if output != expected {
		t.Errorf("output differs.\n got:\n%s\nwant:\n%s", output, expected)
	}
}

func TestProvenanceChecksJSONPrintsThePlatformsDocument(t *testing.T) {
	stub, output, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
		"GET " + provenanceChecksTestPath: {http.StatusOK, provenanceChecksTestAnswer},
	}, "checks", testApplicationID, "-o", "json")

	if executeError != nil {
		t.Fatalf("checks -o json error = %v", executeError)
	}
	var printed, answered any
	if decodeError := json.Unmarshal([]byte(output), &printed); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	_ = json.Unmarshal([]byte(provenanceChecksTestAnswer), &answered)
	if !reflect.DeepEqual(printed, answered) {
		t.Errorf("-o json is not the platform's document:\n%s", output)
	}
	if len(stub.sent(http.MethodGet, provenanceChecksTestEnvsPath)) != 0 {
		t.Errorf("-o json read the environment names it does not print")
	}
}

func TestProvenanceChecksUnreadIsNeverNothingChecked(t *testing.T) {
	unread := `{"application_id": "` + testApplicationID + `", "read_state": "unknown", "kept_from": "2026-11-01T00:00:00Z", "environments": []}`
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			arguments := []string{"checks", testApplicationID}
			if format == "json" {
				arguments = append(arguments, "-o", "json")
			}
			_, output, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
				"GET " + provenanceChecksTestPath: {http.StatusOK, unread},
			}, arguments...)

			if exitCodeFor(executeError) != exitError {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitError, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), "could not read the environments or the kept provenance checks") ||
				!strings.Contains(executeError.Error(), "this does not mean nothing was checked") {
				t.Errorf("error = %v", executeError)
			}
			if strings.Contains(output, "never checked") || strings.Contains(output, "no environment") {
				t.Errorf("an unread answer printed as a list:\n%s", output)
			}
		})
	}
}

func TestProvenanceChecksShowsIDsWhenTheEnvironmentNamesCannotBeRead(t *testing.T) {
	_, output, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
		"GET " + provenanceChecksTestPath:     {http.StatusOK, provenanceChecksTestAnswer},
		"GET " + provenanceChecksTestEnvsPath: {http.StatusInternalServerError, `Internal Server Error`},
	}, "checks", testApplicationID)

	if executeError != nil {
		t.Fatalf("checks error = %v", executeError)
	}
	if !strings.Contains(output, "The environment names could not be read") || !strings.Contains(output, provenanceChecksTestStaging+"  ") {
		t.Errorf("output:\n%s", output)
	}
}

func TestProvenanceChecksRefusalPrintsThePlatformsReason(t *testing.T) {
	cases := map[string]struct {
		answer           provenanceChecksStubAnswer
		expectedExitCode int
		expectedText     string
	}{
		"permission denied": {provenanceChecksStubAnswer{http.StatusForbidden,
			`{"detail": "You need the pipelines.read permission to read deploy provenance settings and keys."}`},
			exitForbidden, "pipelines.read"},
		"application not found": {provenanceChecksStubAnswer{http.StatusNotFound, `{"detail": "Application not found"}`},
			exitNotFound, "Application not found"},
		"not available yet": {provenanceChecksStubAnswer{http.StatusServiceUnavailable,
			`{"detail": "Kept provenance checks are not available yet; retry shortly"}`},
			exitError, "Kept provenance checks are not available yet; retry shortly"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, output, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
				"GET " + provenanceChecksTestPath: testCase.answer,
			}, "checks", testApplicationID)

			if exitCodeFor(executeError) != testCase.expectedExitCode {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), testCase.expectedExitCode, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expectedText) {
				t.Errorf("error = %v, want it to carry %q", executeError, testCase.expectedText)
			}
			if strings.Contains(output, "ENVIRONMENT") {
				t.Errorf("a refused read printed a table:\n%s", output)
			}
		})
	}
}

const provenanceChecksTestCheckNow = `{"application_id": "` + testApplicationID + `", "mode": "warn", "decision": "deploy_with_finding",
  "verdict": {"state": "refused", "images": [{"image": "registry.example/shop:sha-0f9e", "state": "refused", "reason": "unsigned",
  "message": "no attestation"}], "unchecked_images": ["docker.io/library/postgres:16"]},
  "checks": [
    {"installation_id": "i-1", "cluster_id": "` + provenanceChecksTestClusterA + `", "namespace": "shop", "checked_at": "2026-11-19T08:00:00Z", "image_tag": "sha-0f9e", "state": "refused", "message": "no attestation"},
    {"installation_id": "i-2", "cluster_id": "` + provenanceChecksTestClusterB + `", "namespace": "shop", "checked_at": "2026-11-19T08:00:00Z", "image_tag": null, "state": "unknown", "message": "its images could not be named"}],
  "checks_capped": true, "images_capped": false}`

func TestProvenanceVerifyEnvironmentChecksItByNameAndPrintsEachInstallation(t *testing.T) {
	stub, output, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
		"GET " + provenanceChecksTestEnvsPath:    {http.StatusOK, provenanceChecksTestEnvironments},
		"POST " + provenanceChecksTestVerifyPath: {http.StatusOK, provenanceChecksTestCheckNow},
	}, "verify", testApplicationID, "--environment", "Production")

	if executeError != nil {
		t.Fatalf("verify --environment error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, provenanceChecksTestVerifyPath)
	if len(requests) != 1 || !reflect.DeepEqual(requests[0].body, map[string]any{"environment_id": provenanceChecksTestProduction}) {
		t.Errorf("verify requests = %+v, want one naming only the environment", requests)
	}
	for _, fragment := range []string{
		"Checked what production runs. Each check is kept as the installation's last one.",
		"Decision: a deploy would go ahead and file a finding (warn)",
		"registry.example/shop:sha-0f9e",
		"not checked (outside the application's registry): docker.io/library/postgres:16",
		"CLUSTER                               NAMESPACE  TAG       ANSWER",
		provenanceChecksTestClusterA + "  shop       sha-0f9e  did not pass: no attestation",
		provenanceChecksTestClusterB + "  shop       -         could not be checked: its images could not be named",
		"production holds more than 2 installations; only these were checked.",
	} {
		if !strings.Contains(output, fragment) {
			t.Errorf("output lacks %q:\n%s", fragment, output)
		}
	}
}

func TestProvenanceVerifyEnvironmentRefusedDecisionExitsOne(t *testing.T) {
	refused := strings.Replace(strings.Replace(provenanceChecksTestCheckNow, `"decision": "deploy_with_finding"`, `"decision": "refuse"`, 1),
		`"mode": "warn"`, `"mode": "enforce"`, 1)
	_, _, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
		"GET " + provenanceChecksTestEnvsPath:    {http.StatusOK, provenanceChecksTestEnvironments},
		"POST " + provenanceChecksTestVerifyPath: {http.StatusOK, refused},
	}, "verify", testApplicationID, "--environment", provenanceChecksTestProduction)

	if exitCodeFor(executeError) != exitError {
		t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), exitError, executeError)
	}
	if executeError == nil || executeError.Error() != "a deploy of what production runs would be refused" {
		t.Errorf("error = %v", executeError)
	}
}

func TestProvenanceVerifyEnvironmentJSONPrintsThePlatformsDocument(t *testing.T) {
	_, output, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
		"GET " + provenanceChecksTestEnvsPath:    {http.StatusOK, provenanceChecksTestEnvironments},
		"POST " + provenanceChecksTestVerifyPath: {http.StatusOK, provenanceChecksTestCheckNow},
	}, "verify", testApplicationID, "--environment", "production", "-o", "json")

	if executeError != nil {
		t.Fatalf("verify --environment -o json error = %v", executeError)
	}
	var printed, answered any
	if decodeError := json.Unmarshal([]byte(output), &printed); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	_ = json.Unmarshal([]byte(provenanceChecksTestCheckNow), &answered)
	if !reflect.DeepEqual(printed, answered) {
		t.Errorf("-o json is not the platform's document:\n%s", output)
	}
}

func TestProvenanceVerifyEnvironmentRefusalPrintsThePlatformsSentence(t *testing.T) {
	cases := map[string]struct {
		answer           provenanceChecksStubAnswer
		expectedExitCode int
		expectedText     string
	}{
		"nothing to check": {provenanceChecksStubAnswer{http.StatusConflict,
			`{"detail": "This environment has no installation whose images Ankra can check"}`},
			exitError, "checking production: This environment has no installation whose images Ankra can check"},
		"environment gone": {provenanceChecksStubAnswer{http.StatusNotFound, `{"detail": "Environment not found"}`},
			exitNotFound, "checking production: Environment not found"},
		"environments unread": {provenanceChecksStubAnswer{http.StatusServiceUnavailable,
			`{"detail": "The application's environments could not be read; retry shortly"}`},
			exitError, "The application's environments could not be read; retry shortly"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, output, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
				"GET " + provenanceChecksTestEnvsPath:    {http.StatusOK, provenanceChecksTestEnvironments},
				"POST " + provenanceChecksTestVerifyPath: testCase.answer,
			}, "verify", testApplicationID, "--environment", "production")

			if exitCodeFor(executeError) != testCase.expectedExitCode {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), testCase.expectedExitCode, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expectedText) {
				t.Errorf("error = %v, want it to carry %q", executeError, testCase.expectedText)
			}
			if strings.Contains(output, "Checked what") {
				t.Errorf("a refused check was reported done:\n%s", output)
			}
		})
	}
}

func TestProvenanceVerifyEnvironmentRefusesBeforeSendingAnything(t *testing.T) {
	cases := map[string]struct {
		arguments        []string
		answers          map[string]provenanceChecksStubAnswer
		expectedExitCode int
		expectedText     string
	}{
		"with --image too": {[]string{"--environment", "production", "--image", "a/b:c"}, map[string]provenanceChecksStubAnswer{},
			exitUsage, "pass --image or --environment, not both"},
		"empty environment": {[]string{"--environment", " "}, map[string]provenanceChecksStubAnswer{},
			exitUsage, "--environment is empty"},
		"unknown environment": {[]string{"--environment", "qa"}, map[string]provenanceChecksStubAnswer{
			"GET " + provenanceChecksTestEnvsPath: {http.StatusOK, provenanceChecksTestEnvironments}},
			exitNotFound, `no environment is named "qa"`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			stub, _, executeError := runProvenanceChecks(t, testCase.answers,
				append([]string{"verify", testApplicationID}, testCase.arguments...)...)

			if exitCodeFor(executeError) != testCase.expectedExitCode {
				t.Errorf("exit code = %d, want %d: %v", exitCodeFor(executeError), testCase.expectedExitCode, executeError)
			}
			if executeError == nil || !strings.Contains(executeError.Error(), testCase.expectedText) {
				t.Errorf("error = %v, want it to carry %q", executeError, testCase.expectedText)
			}
			if requests := stub.sent(http.MethodPost, provenanceChecksTestVerifyPath); len(requests) != 0 {
				t.Errorf("a check was sent: %+v", requests)
			}
		})
	}
}

func TestProvenanceVerifyWithImagesStillSendsOnlyTheImages(t *testing.T) {
	stub, _, executeError := runProvenanceChecks(t, map[string]provenanceChecksStubAnswer{
		"POST " + provenanceChecksTestVerifyPath: {http.StatusOK,
			`{"application_id": "x", "mode": "warn", "decision": "deploy", "verdict": {"state": "verified", "images": [], "unchecked_images": []}}`},
	}, "verify", testApplicationID, "--image", "a/b:c")

	if executeError != nil {
		t.Fatalf("verify --image error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, provenanceChecksTestVerifyPath)
	if len(requests) != 1 || !reflect.DeepEqual(requests[0].body, map[string]any{"images": []any{"a/b:c"}}) {
		t.Errorf("verify requests = %+v", requests)
	}
}
