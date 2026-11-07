package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// creationProgressStepsSample is shaped as the platform writes it
// (CreationProgressStepResponse in the cluster openapi schema): every step up
// to the one just finished is completed, that one may carry a message, and the
// next step is active.
const creationProgressStepsSample = `{
	"analysis_status": null,
	"error_message": null,
	"pull_request_url": null,
	"creation_progress": [
		{"key": "agent_assigned", "status": "completed"},
		{"key": "resolve_github_token", "status": "completed"},
		{"key": "fetch_repo_tree", "status": "completed", "message": "Found 42 files"},
		{"key": "ai_generate_files", "status": "active"}
	]
}`

func TestApplicationAnalysisStateDecodesCreationProgress(t *testing.T) {
	testCases := []struct {
		name            string
		payload         string
		expectedSteps   int
		expectedSummary string
	}{
		{
			name:            "list of steps",
			payload:         creationProgressStepsSample,
			expectedSteps:   4,
			expectedSummary: "AI generate files...",
		},
		{
			name: "completed step with a message",
			payload: `{"creation_progress":[{"key":"agent_assigned","status":"completed"},` +
				`{"key":"fetch_repo_tree","status":"completed","message":"Found 42 files"}]}`,
			expectedSteps:   2,
			expectedSummary: "Fetch repository tree: Found 42 files",
		},
		{
			name:            "completed step without a message",
			payload:         `{"creation_progress":[{"key":"create_pull_request","status":"completed","message":null}]}`,
			expectedSteps:   1,
			expectedSummary: "Create pull request: done",
		},
		{
			name:            "null",
			payload:         `{"analysis_status":"pending","creation_progress":null}`,
			expectedSummary: "",
		},
		{
			name:            "absent",
			payload:         `{"analysis_status":"pending"}`,
			expectedSummary: "",
		},
		{
			name:            "empty list",
			payload:         `{"creation_progress":[]}`,
			expectedSummary: "",
		},
		{
			name:            "string from an older server",
			payload:         `{"creation_progress":"  Reading the repository "}`,
			expectedSummary: "Reading the repository",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var state applicationAnalysisState
			if decodeError := json.Unmarshal([]byte(testCase.payload), &state); decodeError != nil {
				t.Fatalf("decoding %s: %v", testCase.payload, decodeError)
			}
			if len(state.CreationProgress.Steps) != testCase.expectedSteps {
				t.Errorf("steps = %d, want %d", len(state.CreationProgress.Steps), testCase.expectedSteps)
			}
			if summary := state.CreationProgress.Summary(); summary != testCase.expectedSummary {
				t.Errorf("summary = %q, want %q", summary, testCase.expectedSummary)
			}
		})
	}
}

func TestApplicationAnalysisStateRejectsAnUnknownProgressShape(t *testing.T) {
	var state applicationAnalysisState
	decodeError := json.Unmarshal([]byte(`{"creation_progress":7}`), &state)
	if decodeError == nil || !strings.Contains(decodeError.Error(), "creation_progress") {
		t.Fatalf("decoding a number = %v, want a creation_progress error", decodeError)
	}
}

func TestApplicationAddWaitFollowsStepProgressToTheSetupPullRequest(t *testing.T) {
	repositoryPath := createTestGitRepository(t, "main", "https://github.com/acme/payments.git")
	resetApplicationAddFlags(t)
	t.Cleanup(func() { resetApplicationAddFlags(t) })
	serveApplicationAnalysis(t,
		`{"analysis_status":"pending","creation_progress":null}`,
		creationProgressStepsSample,
		`{"analysis_status":"complete","pull_request_url":"https://github.com/acme/payments/pull/7",`+
			`"creation_progress":[{"key":"application_create_complete","status":"completed"}]}`)

	output, executeError := executeCommand("application", "add", repositoryPath, "--wait", "--timeout", "1m")
	if executeError != nil {
		t.Fatalf("application add --wait = %v", executeError)
	}
	for _, expected := range []string{
		"AI generate files...",
		"Analysis complete.",
		"https://github.com/acme/payments/pull/7",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output does not contain %q:\n%s", expected, output)
		}
	}
}

func TestApplicationAddWaitReportsAFailureAfterStepProgress(t *testing.T) {
	repositoryPath := createTestGitRepository(t, "main", "https://github.com/acme/payments.git")
	resetApplicationAddFlags(t)
	t.Cleanup(func() { resetApplicationAddFlags(t) })
	serveApplicationAnalysis(t,
		`{"analysis_status":"failed","error_message":"no Dockerfile and no recipe could be generated",`+
			`"creation_progress":[{"key":"detect_language","status":"completed"},{"key":"ai_generate_files","status":"active"}]}`)

	_, executeError := executeCommand("application", "add", repositoryPath, "--wait", "--timeout", "30s")
	if executeError == nil || !strings.Contains(executeError.Error(), "no Dockerfile") {
		t.Fatalf("application add --wait = %v, want the platform's failure reason", executeError)
	}
}
