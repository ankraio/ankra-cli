package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
)

const fixCandidatesAnswer = `{"image":"docker:29.0.2-dind","cve_id":"CVE-2026-123158",
"running":{"tag":"29.0.2-dind","digest":"","packages":[{"finding_id":"f1","package":"openssh-client-common",
"package_type":"apk","installed_version":"10.0_p1-r9","fixed_version":"10.0_p1-r11","occurrences":2}],"detail":""},
"answer":{"smallest_step_with_fix":null,"newest_release":{"tag":"29.1.0-dind","pin":"29.1.0-dind@sha256:bb","verdict":"not_scanned"},
"summary":"No newer tag is verified to carry the fix."},
"candidates":[{"tag":"29.1.0-dind","line":"29.1","same_line":false,"index_digest":"sha256:bb","pin":"29.1.0-dind@sha256:bb",
"verdict":"not_scanned","evidence":[],"registry_detail":""}],
"registry":{"registry":"index.docker.io","repository":"library/docker","state":"read","detail":"","tags_listed":40,
"listing_complete":true,"newer_tags":1,"manifests_read":1},
"unexamined_tags":[],"how_to_read":"Only carries_fix is a fix.","a_field_the_cli_does_not_declare":7}`

func TestFindSecurityImageFixCandidatesSendsTheImageAndVulnerabilityAndKeepsTheWholeAnswer(t *testing.T) {
	var receivedMethod, receivedPath string
	var receivedQuery url.Values
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod, receivedPath, receivedQuery = r.Method, r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixCandidatesAnswer))
	})

	answer, readError := testClient.FindSecurityImageFixCandidates(context.Background(), SecurityImageFixCandidatesOptions{
		Image: " docker:29.0.2-dind ", CVEID: "CVE-2026-123158", FindingID: "632d93d6-f07b-4618-ad37-ae453409e928",
	})
	if readError != nil {
		t.Fatalf("FindSecurityImageFixCandidates: %v", readError)
	}
	if receivedMethod != http.MethodGet || receivedPath != "/api/v1/org/security/fix-candidates" ||
		receivedQuery.Get("image") != "docker:29.0.2-dind" || receivedQuery.Get("cve_id") != "CVE-2026-123158" ||
		receivedQuery.Get("finding_id") != "632d93d6-f07b-4618-ad37-ae453409e928" {
		t.Fatalf("request = %s %s?%s", receivedMethod, receivedPath, receivedQuery.Encode())
	}
	if answer.Answer.SmallestStepWithFix != nil || answer.Answer.NewestRelease == nil ||
		answer.Answer.NewestRelease.Verdict != FixCandidateVerdictNotScanned || answer.Registry.State != FixCandidateRegistryRead {
		t.Fatalf("answer decoded wrong: %+v", answer.Answer)
	}
	var kept map[string]any
	if decodeError := json.Unmarshal(answer.RawJSON(), &kept); decodeError != nil || kept["a_field_the_cli_does_not_declare"] != float64(7) {
		t.Fatalf("the platform's whole answer must be kept for -o json, got %s", answer.RawJSON())
	}
}

func TestFindSecurityImageFixCandidatesLeavesOutAVulnerabilityNotNamed(t *testing.T) {
	var receivedQuery url.Values
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query()
		_, _ = w.Write([]byte(fixCandidatesAnswer))
	})
	if _, readError := testClient.FindSecurityImageFixCandidates(context.Background(),
		SecurityImageFixCandidatesOptions{Image: "docker:29.0.2-dind", FindingID: "f1"}); readError != nil {
		t.Fatalf("FindSecurityImageFixCandidates: %v", readError)
	}
	if _, present := receivedQuery["cve_id"]; present || receivedQuery.Get("finding_id") != "f1" {
		t.Fatalf("an absent --cve must send no cve_id, got %s", receivedQuery.Encode())
	}
}

func TestSecurityImageReadsCarryThePlatformsRefusal(t *testing.T) {
	testCases := map[string]struct {
		status     int
		body       string
		wantStatus int
		wantDetail string
		wantDenied bool
	}{
		"invalid image":    {http.StatusBadRequest, `{"detail":"'::' is not an image reference."}`, http.StatusBadRequest, "'::' is not an image reference.", false},
		"unknown finding":  {http.StatusNotFound, `{"detail":"Finding not found"}`, http.StatusNotFound, "Finding not found", false},
		"no security.read": {http.StatusForbidden, `{"detail":"permission_denied","permission":"security.read","scope_type":"organisation"}`, 0, "", true},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			})
			for _, readError := range []error{
				func() error {
					_, readError := testClient.FindSecurityImageFixCandidates(context.Background(),
						SecurityImageFixCandidatesOptions{Image: "x", CVEID: "CVE-1"})
					return readError
				}(),
				func() error {
					_, readError := testClient.FindSecurityImageDeclarations(context.Background(),
						SecurityImageDeclarationsOptions{Image: "x"})
					return readError
				}(),
			} {
				if testCase.wantDenied {
					var denied *PermissionDeniedError
					if !errors.As(readError, &denied) {
						t.Fatalf("a 403 permission_denied must be a PermissionDeniedError, got %v", readError)
					}
					continue
				}
				var unexpected *UnexpectedResponseError
				if !errors.As(readError, &unexpected) || unexpected.StatusCode != testCase.wantStatus ||
					unexpected.Detail != testCase.wantDetail {
					t.Fatalf("refusal = %v, want status %d with the platform's detail %q", readError, testCase.wantStatus, testCase.wantDetail)
				}
			}
		})
	}
}

func TestFindSecurityImageDeclarationsRepeatsClusterIDsAndKeepsTheWholeAnswer(t *testing.T) {
	var receivedPath string
	var receivedQuery url.Values
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath, receivedQuery = r.URL.Path, r.URL.Query()
		_, _ = w.Write([]byte(`{"image":{"input":"moby/buildkit","repository":"docker.io/moby/buildkit"},
"completeness":{"is_complete":false,"incomplete_reasons":[{"code":"members_not_searched","count":2}]},
"clusters_searched":2,"declarations":[],"running_inventory":"read","member_notes":[{"agents_md":"kept"}]}`))
	})

	answer, readError := testClient.FindSecurityImageDeclarations(context.Background(), SecurityImageDeclarationsOptions{
		Image: "moby/buildkit", ClusterIDs: []string{"c1", " ", "c2"},
	})
	if readError != nil {
		t.Fatalf("FindSecurityImageDeclarations: %v", readError)
	}
	if receivedPath != "/api/v1/org/security/image-declarations" || receivedQuery.Get("image") != "moby/buildkit" ||
		len(receivedQuery["cluster_id"]) != 2 || receivedQuery["cluster_id"][0] != "c1" || receivedQuery["cluster_id"][1] != "c2" {
		t.Fatalf("request = %s?%s", receivedPath, receivedQuery.Encode())
	}
	if answer.Completeness.IsComplete || len(answer.Completeness.IncompleteReasons) != 1 ||
		answer.Completeness.IncompleteReasons[0].Code != "members_not_searched" {
		t.Fatalf("completeness decoded wrong: %+v", answer.Completeness)
	}
	var kept map[string]any
	if decodeError := json.Unmarshal(answer.RawJSON(), &kept); decodeError != nil || kept["member_notes"] == nil {
		t.Fatalf("the platform's whole answer must be kept for -o json, got %s", answer.RawJSON())
	}
}
