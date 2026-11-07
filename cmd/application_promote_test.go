package cmd

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

type applicationPromoteMock struct {
	applicationAIConfigMock
	preview        string
	previewQueries []url.Values
	started        []map[string]string
	startAnswer    string
}

const promoteEnvironmentsDocument = `{"environments":[
	{"id":"cluster-staging","name":"staging-eu","role":"staging"},
	{"id":"cluster-production","name":"production-eu","role":"production"},
	{"id":"cluster-canary","name":"canary-eu","role":"production"}]}`

func (mock *applicationPromoteMock) GetApplicationEnvironments(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(promoteEnvironmentsDocument), nil
}

func (mock *applicationPromoteMock) PreviewApplicationPromotion(_ context.Context, _ string, query url.Values) (json.RawMessage, error) {
	mock.previewQueries = append(mock.previewQueries, query)
	return json.RawMessage(mock.preview), nil
}

func (mock *applicationPromoteMock) StartApplicationPromotion(_ context.Context, _ string, promotion map[string]string) (json.RawMessage, error) {
	mock.started = append(mock.started, promotion)
	return json.RawMessage(mock.startAnswer), nil
}

const readyPromotionPreview = `{"review_fingerprint":"fp-1","state":"ready",
	"source":{"id":"cluster-staging","name":"staging-eu","image_tag":"sha-abc1234"},
	"target":{"id":"cluster-canary","name":"canary-eu","image_tag":"sha-0000000"},
	"blockers":[],"config_differences":[{"key":"replicas","source_value":"1","target_value":"3"}]}`

func promoteArguments(extra ...string) []string {
	return append([]string{"application", "promote", testApplicationID, "--from", "staging", "--to", "canary-eu"}, extra...)
}

func resetPromoteFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		for _, command := range rootCmd.Commands() {
			if command.Name() != "application" {
				continue
			}
			for _, subcommand := range command.Commands() {
				if subcommand.Name() != "promote" {
					continue
				}
				for name, value := range map[string]string{"from": "", "to": "", "dry-run": "false", "yes": "false", "output": ""} {
					_ = subcommand.Flags().Set(name, value)
				}
			}
		}
	})
}

func runPromote(t *testing.T, mock *applicationPromoteMock, stdin string, arguments ...string) (string, error) {
	t.Helper()
	resetPromoteFlags(t)
	setMockClient(t, mock)
	stdout := &strings.Builder{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&strings.Builder{})
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(arguments)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
	})
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

func TestApplicationPromoteConfirmsThenCarriesTheFingerprint(t *testing.T) {
	mock := &applicationPromoteMock{preview: readyPromotionPreview,
		startAnswer: `{"state":"promoted","image_tag":"sha-abc1234","operation_id":"op-1"}`}
	output, executeError := runPromote(t, mock, "y\n", promoteArguments()...)
	if executeError != nil {
		t.Fatalf("promote failed: %v", executeError)
	}
	if len(mock.started) != 1 || mock.started[0]["expected_fingerprint"] != "fp-1" ||
		mock.started[0]["source_environment_id"] != "cluster-staging" ||
		mock.started[0]["target_environment_id"] != "cluster-canary" {
		t.Errorf("started = %+v", mock.started)
	}
	for _, want := range []string{"canary-eu runs sha-0000000", "Stays different: replicas", "op-1"} {
		if !strings.Contains(output, want) {
			t.Errorf("output must contain %q: %s", want, output)
		}
	}
}

func TestApplicationPromoteRefusesAStartStateItDoesNotKnow(t *testing.T) {
	for name, answer := range map[string]string{
		"blocked": `{"state":"blocked","image_tag":"sha-abc1234"}`,
		"empty":   `{"image_tag":"sha-abc1234"}`,
	} {
		t.Run(name, func(t *testing.T) {
			mock := &applicationPromoteMock{preview: readyPromotionPreview, startAnswer: answer}
			output, executeError := runPromote(t, mock, "y\n", promoteArguments()...)
			if executeError == nil {
				t.Fatalf("promote must fail on an unknown start state, output: %s", output)
			}
			if strings.Contains(output, "Promoting sha-abc1234") {
				t.Errorf("an unknown start state must not read as promoting: %s", output)
			}
		})
	}
}

func TestApplicationPromoteDeclinedDeploysNothing(t *testing.T) {
	mock := &applicationPromoteMock{preview: readyPromotionPreview}
	_, executeError := runPromote(t, mock, "n\n", promoteArguments()...)
	if exitCodeFor(executeError) != exitCancelled {
		t.Errorf("exit = %d (%v), want %d", exitCodeFor(executeError), executeError, exitCancelled)
	}
	if len(mock.started) != 0 {
		t.Errorf("a declined promotion must not start: %+v", mock.started)
	}
}

func TestApplicationPromoteDryRunDeploysNothing(t *testing.T) {
	mock := &applicationPromoteMock{preview: readyPromotionPreview}
	if _, executeError := runPromote(t, mock, "", promoteArguments("--dry-run")...); executeError != nil {
		t.Fatalf("dry run failed: %v", executeError)
	}
	if len(mock.started) != 0 {
		t.Errorf("a dry run must not start: %+v", mock.started)
	}
}

func TestApplicationPromoteBlockedExitsBlocked(t *testing.T) {
	mock := &applicationPromoteMock{preview: `{"state":"blocked","blockers":[{"reason":"unproven","detail":"staging is not healthy","fix_path":"/x"}]}`}
	output, executeError := runPromote(t, mock, "y\n", promoteArguments()...)
	if exitCodeFor(executeError) != exitBlocked || !strings.Contains(executeError.Error(), "staging is not healthy") {
		t.Errorf("exit = %d (%v), want %d naming the blocker", exitCodeFor(executeError), executeError, exitBlocked)
	}
	if !strings.Contains(output, "Blocked: staging is not healthy") || len(mock.started) != 0 {
		t.Errorf("output = %q, started = %+v", output, mock.started)
	}
}

func TestApplicationPromoteUnknownStateIsAFailureNotASuccess(t *testing.T) {
	mock := &applicationPromoteMock{preview: `{"state":"unknown","read_reason":"the source could not be read"}`}
	_, executeError := runPromote(t, mock, "", promoteArguments()...)
	if executeError == nil || !strings.Contains(executeError.Error(), "the source could not be read") {
		t.Errorf("got %v", executeError)
	}
}

func TestApplicationPromoteAlreadyCurrentIsNotAnError(t *testing.T) {
	mock := &applicationPromoteMock{preview: `{"state":"already_current"}`}
	output, executeError := runPromote(t, mock, "", promoteArguments()...)
	if executeError != nil || !strings.Contains(output, "already runs this build") {
		t.Errorf("output = %q, error = %v", output, executeError)
	}
}

func TestApplicationPromoteStructuredNeedsYesOrDryRun(t *testing.T) {
	mock := &applicationPromoteMock{preview: readyPromotionPreview}
	_, executeError := runPromote(t, mock, "", promoteArguments("-o", "json")...)
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit = %d (%v), want %d", exitCodeFor(executeError), executeError, exitUsage)
	}
}

func TestResolvePromotionEnvironment(t *testing.T) {
	environments := []promotionEnvironment{
		{ID: "a", Name: "staging-eu", Role: "staging"},
		{ID: "b", Name: "production-eu", Role: "production"},
		{ID: "c", Name: "canary-eu", Role: "production"},
	}
	cases := []struct {
		reference string
		wantID    string
		wantExit  int
	}{
		{reference: "a", wantID: "a"},
		{reference: "PRODUCTION-EU", wantID: "b"},
		{reference: "staging", wantID: "a"},
		{reference: "production", wantExit: exitUsage},
		{reference: "nowhere", wantExit: exitNotFound},
	}
	for _, testCase := range cases {
		environment, resolveError := resolvePromotionEnvironment(environments, testCase.reference)
		if testCase.wantExit != 0 {
			if exitCodeFor(resolveError) != testCase.wantExit {
				t.Errorf("%q: exit = %d (%v), want %d", testCase.reference, exitCodeFor(resolveError), resolveError, testCase.wantExit)
			}
			continue
		}
		if resolveError != nil || environment.ID != testCase.wantID {
			t.Errorf("%q: got %+v, %v", testCase.reference, environment, resolveError)
		}
	}
}

type applicationPreviewsMock struct {
	applicationAIConfigMock
	document string
	written  []string
}

func (mock *applicationPreviewsMock) GetApplicationAIConfig(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(mock.document), nil
}

func (mock *applicationPreviewsMock) UpdateApplicationAIConfig(_ context.Context, _ string, configuration json.RawMessage) (json.RawMessage, error) {
	mock.written = append(mock.written, string(configuration))
	return json.RawMessage(mock.document), nil
}

const previewsConfigDocument = `{"binding":{"connected":true,"repo_full_name":"acme/shop"},
	"lanes":[{"lane":"previews","enabled":true,"overridden_for_application":true,"organisation_enabled":false}]}`

func TestApplicationPreviewsSwitchWritesOnlyThatLane(t *testing.T) {
	cases := map[string]string{
		"enable":  `{"lanes":[{"enabled":true,"lane":"previews","override":true}]}`,
		"disable": `{"lanes":[{"enabled":false,"lane":"previews","override":true}]}`,
		"follow":  `{"lanes":[{"lane":"previews","override":false}]}`,
	}
	for verb, want := range cases {
		t.Run(verb, func(t *testing.T) {
			mock := &applicationPreviewsMock{document: previewsConfigDocument}
			setMockClient(t, mock)
			output, executeError := executeCommand("application", "previews", verb, testApplicationID)
			if executeError != nil {
				t.Fatalf("previews %s: %v", verb, executeError)
			}
			if len(mock.written) != 1 || mock.written[0] != want {
				t.Errorf("wrote %v, want %s", mock.written, want)
			}
			_ = output
		})
	}
}

func TestRenderApplicationPreviewsRefusesAnUnconnectedRepository(t *testing.T) {
	mock := &applicationPreviewsMock{document: `{"binding":{"connected":false,"reason":"connect the repository"},"lanes":[]}`}
	setMockClient(t, mock)
	_, executeError := executeCommand("application", "previews", testApplicationID)
	if executeError == nil || !strings.Contains(executeError.Error(), "connect the repository") {
		t.Errorf("got %v", executeError)
	}
}
