package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

// imageReadsMock answers the two image reads with a decoded platform
// document, so -o json prints that document as the real client does.
type imageReadsMock struct {
	fixDocument         string
	fixError            error
	fixOptions          *client.SecurityImageFixCandidatesOptions
	declarationDocument string
	declarationError    error
	declarationOptions  *client.SecurityImageDeclarationsOptions
}

func (m *imageReadsMock) FindSecurityImageFixCandidates(_ context.Context,
	options client.SecurityImageFixCandidatesOptions) (*client.SecurityImageFixCandidates, error) {
	m.fixOptions = &options
	if m.fixError != nil {
		return nil, m.fixError
	}
	var answer client.SecurityImageFixCandidates
	if decodeError := json.Unmarshal([]byte(m.fixDocument), &answer); decodeError != nil {
		return nil, decodeError
	}
	answer.RawDocument = client.NewRawDocument([]byte(m.fixDocument))
	return &answer, nil
}

func (m *imageReadsMock) FindSecurityImageDeclarations(_ context.Context,
	options client.SecurityImageDeclarationsOptions) (*client.SecurityImageDeclarations, error) {
	m.declarationOptions = &options
	if m.declarationError != nil {
		return nil, m.declarationError
	}
	var answer client.SecurityImageDeclarations
	if decodeError := json.Unmarshal([]byte(m.declarationDocument), &answer); decodeError != nil {
		return nil, decodeError
	}
	answer.RawDocument = client.NewRawDocument([]byte(m.declarationDocument))
	return &answer, nil
}

func (m *imageReadsMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{
		Result:     []client.ClusterListItem{{ID: testClusterID, Name: "production"}},
		Pagination: client.Pagination{TotalPages: 1, Page: page, PageSize: pageSize},
	}, nil
}

// notScannedFixCandidates is an answer whose only candidate nothing Ankra
// scanned decides: no tag is verified to carry the fix.
const notScannedFixCandidates = `{"image":"docker:29.0.2-dind","cve_id":"CVE-2026-123158",
"running":{"tag":"29.0.2-dind","digest":"","packages":[{"finding_id":"f1","package":"openssh-client-common",
"package_type":"apk","installed_version":"10.0_p1-r9","fixed_version":"10.0_p1-r11","occurrences":2}],"detail":""},
"answer":{"smallest_step_with_fix":null,"newest_release":{"tag":"29.1.0-dind","pin":"29.1.0-dind@sha256:bb","verdict":"not_scanned"},
"summary":"No newer tag is verified to carry the fix: 29.1.0-dind is not scanned."},
"candidates":[{"tag":"29.1.0-dind","line":"29.1","same_line":false,"index_digest":"sha256:bb","pin":"29.1.0-dind@sha256:bb",
"verdict":"not_scanned","evidence":[{"package":"openssh-client-common","installed_version":"","fixed_version":"10.0_p1-r11",
"verdict":"not_scanned","source":"none","detail":"no scan or bill of materials of this tag"}],"registry_detail":""}],
"registry":{"registry":"index.docker.io","repository":"library/docker","state":"read","detail":"","tags_listed":40,
"listing_complete":false,"newer_tags":1,"manifests_read":1},
"unexamined_tags":["29.2.0-dind"],"how_to_read":"Only carries_fix is a fix; not_scanned is unknown.",
"a_field_the_cli_does_not_declare":7}`

func TestSecurityFixCandidatesNeverRendersAnUnscannedTagAsFixed(t *testing.T) {
	mock := &imageReadsMock{fixDocument: notScannedFixCandidates}
	output, executeError := runSecurityCommand(t, mock, "security", "fix-candidates", "docker:29.0.2-dind", "--cve", "cve-2026-123158")
	if executeError != nil {
		t.Fatalf("security fix-candidates failed: %v", executeError)
	}
	if mock.fixOptions == nil || mock.fixOptions.Image != "docker:29.0.2-dind" || mock.fixOptions.CVEID != "CVE-2026-123158" ||
		mock.fixOptions.FindingID != "" {
		t.Fatalf("options = %+v, want the image and the upper-cased CVE", mock.fixOptions)
	}
	for _, expected := range []string{
		"openssh-client-common", "10.0_p1-r11",
		"Smallest step with the fix: none verified",
		"Newest release: 29.1.0-dind@sha256:bb (not scanned (unknown, not fixed))",
		"no scan or bill of materials of this tag",
		"The listing stopped at its page budget, so a newer tag missing from it was not read.",
		"Newer tags not examined (1), which may carry the fix: 29.2.0-dind",
		"Only carries_fix is a fix; not_scanned is unknown.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "carries the fix\n") || strings.Contains(output, "(carries the fix)") {
		t.Fatalf("an unscanned tag must never read as carrying the fix:\n%s", output)
	}
}

func TestSecurityFixCandidatesSaysAnUnreadRegistryOffersNoCandidateBecauseNothingWasListed(t *testing.T) {
	document := strings.NewReplacer(`"state":"read"`, `"state":"unreadable"`,
		`"detail":"","tags_listed":40`, `"detail":"the registry refused the anonymous read (401)","tags_listed":0`).
		Replace(notScannedFixCandidates)
	output, executeError := runSecurityCommand(t, &imageReadsMock{fixDocument: document},
		"security", "fix-candidates", "docker:29.0.2-dind", "--finding", "632d93d6-f07b-4618-ad37-ae453409e928")
	if executeError != nil {
		t.Fatalf("security fix-candidates failed: %v", executeError)
	}
	if !strings.Contains(output, "no candidate is offered because nothing was listed, not because none exists") ||
		!strings.Contains(output, "the registry refused the anonymous read (401)") {
		t.Fatalf("an unread registry must be said as the reason, with its detail:\n%s", output)
	}
}

func TestSecurityFixCandidatesNamesTheVerifiedStep(t *testing.T) {
	document := strings.NewReplacer(`"smallest_step_with_fix":null`,
		`"smallest_step_with_fix":{"tag":"29.0.3-dind","pin":"29.0.3-dind@sha256:aa","verdict":"carries_fix"}`).
		Replace(notScannedFixCandidates)
	output, executeError := runSecurityCommand(t, &imageReadsMock{fixDocument: document},
		"security", "fix-candidates", "docker:29.0.2-dind", "--cve", "CVE-2026-123158")
	if executeError != nil {
		t.Fatalf("security fix-candidates failed: %v", executeError)
	}
	if !strings.Contains(output, "Smallest step with the fix: 29.0.3-dind@sha256:aa (carries the fix)") {
		t.Fatalf("the verified step must be named with its pin:\n%s", output)
	}
}

func TestSecurityFixCandidatesNeedsAVulnerability(t *testing.T) {
	mock := &imageReadsMock{fixDocument: notScannedFixCandidates}
	_, executeError := runSecurityCommand(t, mock, "security", "fix-candidates", "docker:29.0.2-dind")
	if exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "--cve") {
		t.Fatalf("no --cve or --finding must be a usage error naming the flags, got %v (exit %d)", executeError, exitCodeFor(executeError))
	}
	if mock.fixOptions != nil {
		t.Fatalf("nothing must be asked without a vulnerability, asked %+v", mock.fixOptions)
	}
}

func TestSecurityImageReadsClassifyThePlatformsRefusals(t *testing.T) {
	testCases := map[string]struct {
		refusal  error
		wantExit int
	}{
		"image not accepted":  {&client.UnexpectedResponseError{StatusCode: 400, Detail: "'::' is not an image reference."}, exitUsage},
		"malformed parameter": {&client.UnexpectedResponseError{StatusCode: 422}, exitUsage},
		"finding not found":   {&client.UnexpectedResponseError{StatusCode: 404, Detail: "Finding not found"}, exitNotFound},
		"no security.read":    {&client.PermissionDeniedError{Permission: "security.read"}, exitForbidden},
		"platform failure":    {&client.UnexpectedResponseError{StatusCode: 500}, exitError},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			_, fixError := runSecurityCommand(t, &imageReadsMock{fixError: testCase.refusal},
				"security", "fix-candidates", "docker:29.0.2-dind", "--cve", "CVE-2026-123158")
			if exitCodeFor(fixError) != testCase.wantExit {
				t.Fatalf("fix-candidates exit = %d (%v), want %d", exitCodeFor(fixError), fixError, testCase.wantExit)
			}
			_, declaredError := runSecurityCommand(t, &imageReadsMock{declarationError: testCase.refusal},
				"security", "images", "declared", "docker:29.0.2-dind")
			if exitCodeFor(declaredError) != testCase.wantExit {
				t.Fatalf("images declared exit = %d (%v), want %d", exitCodeFor(declaredError), declaredError, testCase.wantExit)
			}
		})
	}
}

func TestSecurityImageReadsPrintThePlatformsWholeAnswerAsJSON(t *testing.T) {
	output, executeError := runSecurityCommand(t, &imageReadsMock{fixDocument: notScannedFixCandidates},
		"security", "fix-candidates", "docker:29.0.2-dind", "--cve", "CVE-2026-123158", "-o", "json")
	if executeError != nil {
		t.Fatalf("security fix-candidates -o json failed: %v", executeError)
	}
	var printed map[string]any
	if decodeError := json.Unmarshal([]byte(output), &printed); decodeError != nil {
		t.Fatalf("-o json must print one JSON document, got %v:\n%s", decodeError, output)
	}
	if printed["a_field_the_cli_does_not_declare"] != float64(7) {
		t.Fatalf("-o json must print the platform's answer unchanged, got:\n%s", output)
	}
}

const incompleteEmptyDeclarations = `{"image":{"input":"moby/buildkit","repository":"docker.io/moby/buildkit","tag":"","digest":""},
"completeness":{"is_complete":false,"incomplete_reasons":[{"code":"members_not_searched","count":2}]},
"clusters_searched":2,"clusters":[],"clusters_excluded":[],"declarations":[],"unresolved_mentions":[],
"running_undeclared":[],"running_by_application":[],"running_match_unknown":[],"running_inventory":"read",
"not_searched":[{"cluster_name":"production","stack_name":"observability","member_kind":"addon","member_name":"loki",
"reason":"member_content_too_large"}]}`

func TestSecurityImagesDeclaredNeverReadsAnIncompleteEmptySearchAsUsedNowhere(t *testing.T) {
	mock := &imageReadsMock{declarationDocument: incompleteEmptyDeclarations}
	output, executeError := runSecurityCommand(t, mock, "security", "images", "declared", "moby/buildkit")
	if executeError != nil {
		t.Fatalf("security images declared failed: %v", executeError)
	}
	for _, expected := range []string{
		"docker.io/moby/buildkit (as written: moby/buildkit)",
		"Searched 2 cluster(s). The search is incomplete: members not searched (2).",
		"No declaration was found, but the search is incomplete, so this does not mean no stack declares the image.",
		"Not searched (1)", "production/observability: 1 addon, member content too large",
		"Pass -o json for every part not searched.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "No stack on the searched clusters declares this image.") {
		t.Fatalf("an incomplete search must not claim the image is declared nowhere:\n%s", output)
	}
	if mock.declarationOptions == nil || len(mock.declarationOptions.ClusterIDs) != 0 {
		t.Fatalf("no --cluster must search every readable cluster, got %+v", mock.declarationOptions)
	}
}

// The 2026-11-23 smoke test printed 538 not-searched lines after the answer,
// each sealed manifest twice (ankra-oq7jks.9.16): the table output counts
// them per cluster, stack and reason instead.
func TestSecurityImagesDeclaredCountsNotSearchedPartsPerStackAndReason(t *testing.T) {
	parts := []string{}
	for index := 0; index < 38; index++ {
		for _, reason := range []string{"sealed_keys_not_searched", "secret_documents_not_searched"} {
			parts = append(parts, fmt.Sprintf(`{"cluster_name":"control","stack_name":"control-plane","member_kind":"manifest",`+
				`"member_name":"sealed-%02d","reason":"%s"}`, index, reason))
		}
	}
	parts = append(parts,
		`{"cluster_name":"control","stack_name":"observability","member_kind":"addon","member_name":"loki","reason":"sealed_keys_not_searched"}`,
		`{"cluster_name":"control","stack_name":"observability","member_kind":"manifest","member_name":"creds","reason":"sealed_keys_not_searched"}`)
	document := strings.Replace(incompleteEmptyDeclarations, `"not_searched":[{"cluster_name":"production","stack_name":"observability","member_kind":"addon","member_name":"loki",
"reason":"member_content_too_large"}]`, `"not_searched":[`+strings.Join(parts, ",")+`]`, 1)
	output, executeError := runSecurityCommand(t, &imageReadsMock{declarationDocument: document},
		"security", "images", "declared", "moby/buildkit")
	if executeError != nil {
		t.Fatalf("security images declared failed: %v", executeError)
	}
	for _, expected := range []string{
		"No declaration was found, but the search is incomplete, so this does not mean no stack declares the image.",
		"Not searched (78)\n" +
			"  control/control-plane: 38 manifests, sealed keys not searched\n" +
			"  control/control-plane: 38 manifests, secret documents not searched\n" +
			"  control/observability: 2 members, sealed keys not searched\n" +
			"  Pass -o json for every part not searched.\n",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "sealed-07") {
		t.Fatalf("the table output must count not-searched parts, not list each one:\n%s", output)
	}
}

func TestSecurityImagesDeclaredSaysACompleteEmptySearchFoundNoStack(t *testing.T) {
	document := strings.NewReplacer(`"is_complete":false,"incomplete_reasons":[{"code":"members_not_searched","count":2}]`,
		`"is_complete":true,"incomplete_reasons":[]`, `"not_searched":[{"cluster_name":"production","stack_name":"observability","member_kind":"addon","member_name":"loki",
"reason":"member_content_too_large"}]`, `"not_searched":[]`).Replace(incompleteEmptyDeclarations)
	output, executeError := runSecurityCommand(t, &imageReadsMock{declarationDocument: document},
		"security", "images", "declared", "moby/buildkit")
	if executeError != nil {
		t.Fatalf("security images declared failed: %v", executeError)
	}
	if !strings.Contains(output, "The search is complete.") ||
		!strings.Contains(output, "No stack on the searched clusters declares this image.") {
		t.Fatalf("a complete empty search must say no stack declares the image:\n%s", output)
	}
}

func TestSecurityImagesDeclaredListsTheFileAndLineToChangeAndWhatRuns(t *testing.T) {
	document := `{"image":{"input":"docker:29.0.2-dind","repository":"docker.io/library/docker","tag":"29.0.2-dind","digest":""},
"completeness":{"is_complete":true,"incomplete_reasons":[]},"clusters_searched":1,
"declarations":[{"cluster_id":"` + testClusterID + `","cluster_name":"production","stack_name":"ci","member_kind":"addon",
"member_name":"actions-runner","resource_kind":"","resource_name":"","key_path":"dind.image","reference":"docker:29.0.2-dind",
"stored_line":12,"change_via":"gitops_pull_request","gitops":{"repository":"acme/platform-gitops","branch":"main",
"file_path":"clusters/production/stacks/ci/actions-runner.yaml","line":14,"line_unknown_reason":"","file_url":""},
"stack_has_open_draft":true,"running_workloads":[]}],
"running_undeclared":[{"cluster_name":"production","namespace":"build","workload_kind":"Deployment","workload_name":"builder",
"container_name":"dind","image":"docker:29.0.2-dind","pods":2,"match_unknown_reasons":[]}],
"running_match_unknown":[{"cluster_name":"production","namespace":"ops","workload_kind":"Job","workload_name":"nightly",
"container_name":"dind","image":"docker:29.0.2-dind","pods":1,"match_unknown_reasons":["owner_not_resolved"]}],
"running_inventory":"capped"}`
	mock := &imageReadsMock{declarationDocument: document}
	output, executeError := runSecurityCommand(t, mock, "security", "images", "declared", "docker:29.0.2-dind",
		"--cluster", "production")
	if executeError != nil {
		t.Fatalf("security images declared failed: %v", executeError)
	}
	if mock.declarationOptions == nil || len(mock.declarationOptions.ClusterIDs) != 1 ||
		mock.declarationOptions.ClusterIDs[0] != testClusterID {
		t.Fatalf("--cluster production must be sent as its id, got %+v", mock.declarationOptions)
	}
	for _, expected := range []string{
		"Declarations (1)", "actions-runner", "dind.image, line 12", "gitops pull request (the stack has an open draft)",
		"acme/platform-gitops clusters/production/stacks/ci/actions-runner.yaml:14",
		"Running, declared by no stack (1)", "production/build Deployment builder, container dind, 2 pod(s)",
		"Running, declaration not known (1)", "(not known because: owner not resolved)",
		"The running-container read was cut at its cap",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}
