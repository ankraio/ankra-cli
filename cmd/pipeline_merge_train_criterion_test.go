package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// pipelineTrainStoredRule answers a settings write as a platform that knows the
// criterion does, with the rule the write left in force.
func pipelineTrainStoredRule(autoAdmit string, criterion string, paths []string,
	isDefault bool) *client.PipelineMergeTrainSettings {
	return &client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 1, AutoAdmit: autoAdmit,
		AutoAdmitCriterion: criterion, AutoAdmitProtectedPaths: &paths, AutoAdmitProtectedPathsIsDefault: &isDefault}
}

var pipelineTrainDefaultPaths = []string{"alembic/**", "deploy/charts/*/values.yaml",
	"deploy/charts/*/values.schema.json"}

func TestPipelineTrainEnableSendsTheCriterionOnlyWhenNamed(t *testing.T) {
	mock := &pipelineTrainMock{settingsAnswer: pipelineTrainStoredRule("on", "green", pipelineTrainDefaultPaths, true)}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--repository", testPipelineRepositoryID); runError != nil {
		t.Fatalf("enable: %v", runError)
	}
	if mock.settingsRequest.AutoAdmitCriterion != nil || mock.settingsRequest.AutoAdmitProtectedPaths != nil {
		t.Fatalf("an enable naming neither keeps both, got %+v", mock.settingsRequest)
	}
	for named, want := range map[string]string{"green": "green", " Ready_Shadow ": "ready_shadow", "READY": "ready"} {
		if _, runError := runPipelineTrainCommand(t, mock, "enable", "--auto-admit-criterion", named, "--repository",
			testPipelineRepositoryID); runError != nil {
			t.Fatalf("enable --auto-admit-criterion %q: %v", named, runError)
		}
		if got := mock.settingsRequest.AutoAdmitCriterion; got == nil || *got != want {
			t.Fatalf("--auto-admit-criterion %q sent %v, want %q", named, got, want)
		}
		if mock.settingsRequest.AutoAdmitProtectedPaths != nil || mock.settingsRequest.AutoAdmit != nil ||
			mock.settingsRequest.MaxCars != nil {
			t.Fatalf("naming the criterion sent something else too: %+v", mock.settingsRequest)
		}
	}
}

func TestPipelineTrainEnableRefusesABadCriterionAndSendsNothing(t *testing.T) {
	for _, named := range []string{"", "always", "ready-shadow", "on"} {
		mock := &pipelineTrainMock{}
		_, runError := runPipelineTrainCommand(t, mock, "enable", "--auto-admit-criterion", named, "--repository",
			testPipelineRepositoryID)
		if runError == nil || exitCodeFor(runError) != exitUsage {
			t.Fatalf("--auto-admit-criterion %q: want a usage error, got %v", named, runError)
		}
		if !strings.Contains(runError.Error(), "green, ready_shadow, ready") {
			t.Fatalf("the refusal does not name the values: %v", runError)
		}
		if mock.settingsRequest != nil {
			t.Fatalf("--auto-admit-criterion %q sent %+v", named, mock.settingsRequest)
		}
	}
}

func TestPipelineTrainEnableProtectedPathFlagsSendTheirThreeMeanings(t *testing.T) {
	mock := &pipelineTrainMock{settingsAnswer: pipelineTrainStoredRule("on", "ready", []string{"alembic/**"}, false)}

	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--auto-admit-protected-paths",
		" alembic/** ,!alembic/docs/**", "--auto-admit-protected-paths", "deploy/**", "--repository",
		testPipelineRepositoryID); runError != nil {
		t.Fatalf("enable with a list: %v", runError)
	}
	sent := mock.settingsRequest.AutoAdmitProtectedPaths
	if sent == nil || strings.Join(*sent, "|") != "alembic/**|!alembic/docs/**|deploy/**" {
		t.Fatalf("list sent %v: want it trimmed and in order, the last matching pattern wins", sent)
	}
	if mock.settingsRequest.AutoAdmitCriterion != nil {
		t.Fatalf("naming the paths sent a criterion too: %+v", mock.settingsRequest)
	}

	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--auto-admit-protected-paths-default", "--repository",
		testPipelineRepositoryID); runError != nil {
		t.Fatalf("enable --auto-admit-protected-paths-default: %v", runError)
	}
	sent = mock.settingsRequest.AutoAdmitProtectedPaths
	if sent == nil || *sent != nil {
		t.Fatalf("the default flag must send null, a pointer to a nil slice, got %v", sent)
	}

	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--auto-admit-protected-paths-none", "--repository",
		testPipelineRepositoryID); runError != nil {
		t.Fatalf("enable --auto-admit-protected-paths-none: %v", runError)
	}
	sent = mock.settingsRequest.AutoAdmitProtectedPaths
	if sent == nil || *sent == nil || len(*sent) != 0 {
		t.Fatalf("the none flag must send [], a pointer to an empty slice, got %v", sent)
	}
}

func TestPipelineTrainEnableRefusesBadProtectedPathsAndSendsNothing(t *testing.T) {
	tooMany := strings.TrimSuffix(strings.Repeat("a/**,", 51), ",")
	cases := map[string][]string{
		"an empty value":     {"--auto-admit-protected-paths", ""},
		"an empty pattern":   {"--auto-admit-protected-paths", "alembic/**,,deploy/**"},
		"a trailing comma":   {"--auto-admit-protected-paths", "alembic/**,"},
		"a blank pattern":    {"--auto-admit-protected-paths", "alembic/**, "},
		"only an exclusion":  {"--auto-admit-protected-paths", "!"},
		"51 patterns":        {"--auto-admit-protected-paths", tooMany},
		"257 characters":     {"--auto-admit-protected-paths", strings.Repeat("a", 257)},
		"a list and default": {"--auto-admit-protected-paths", "a", "--auto-admit-protected-paths-default"},
		"a list and none":    {"--auto-admit-protected-paths", "a", "--auto-admit-protected-paths-none"},
		"default and none":   {"--auto-admit-protected-paths-default", "--auto-admit-protected-paths-none"},
	}
	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			mock := &pipelineTrainMock{}
			arguments := append([]string{"enable", "--repository", testPipelineRepositoryID}, flags...)
			_, runError := runPipelineTrainCommand(t, mock, arguments...)
			if runError == nil || exitCodeFor(runError) != exitUsage {
				t.Fatalf("want a usage error, got %v", runError)
			}
			if mock.settingsRequest != nil {
				t.Fatalf("sent %+v", mock.settingsRequest)
			}
		})
	}
}

func TestPipelineTrainEnableAcceptsTheLimitsThePlatformAccepts(t *testing.T) {
	atTheLimit := strings.TrimSuffix(strings.Repeat("a/**,", 50), ",")
	for name, value := range map[string]string{"50 patterns": atTheLimit, "256 characters": strings.Repeat("a", 256)} {
		mock := &pipelineTrainMock{settingsAnswer: pipelineTrainStoredRule("on", "green", []string{"a"}, false)}
		if _, runError := runPipelineTrainCommand(t, mock, "enable", "--auto-admit-protected-paths", value,
			"--repository", testPipelineRepositoryID); runError != nil {
			t.Fatalf("%s: %v", name, runError)
		}
	}
}

func TestPipelineTrainEnableSaysPlainlyWhatReadyDoes(t *testing.T) {
	mock := &pipelineTrainMock{settingsAnswer: pipelineTrainStoredRule("on", "ready", pipelineTrainDefaultPaths, true)}
	output, runError := runPipelineTrainCommand(t, mock, "enable", "--auto-admit-criterion", "ready", "--repository",
		testPipelineRepositoryID)
	if runError != nil {
		t.Fatalf("enable: %v", runError)
	}
	for _, want := range []string{
		"Auto-admit: on; the train adds pull requests by itself when they are marked ready.",
		"a pull request enters the train when it is marked ready, before its own run has passed",
		"One that changes a protected path keeps the green rule",
		"Protected paths (the default list): alembic/**, deploy/charts/*/values.yaml, deploy/charts/*/values.schema.json",
		"At ready the car's run is the only run that has to pass before the merge",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "adds green pull requests") {
		t.Fatalf("at ready the train does not wait for green:\n%s", output)
	}

	notNamingReady, runError := runPipelineTrainCommand(t, mock, "enable", "--repository", testPipelineRepositoryID)
	if runError != nil {
		t.Fatalf("enable: %v", runError)
	}
	if strings.Contains(notNamingReady, "only run that has to pass") {
		t.Fatalf("the cost is said to a write that decides ready, not to every enable:\n%s", notNamingReady)
	}
}

func TestPipelineTrainCriterionSentenceNamesEachRuleAndWhenItDoesNothing(t *testing.T) {
	cases := []struct {
		name      string
		autoAdmit string
		criterion string
		wants     []string
		rejects   []string
	}{
		{name: "green", autoAdmit: "on", criterion: "green",
			wants: []string{"Auto-admit criterion: green", "once its own run has passed"}, rejects: []string{"Auto-admit is off"}},
		{name: "ready_shadow", autoAdmit: "on", criterion: "ready_shadow",
			wants: []string{"Auto-admit criterion: ready_shadow", "records which ones it would have added at ready"}},
		{name: "ready while off", autoAdmit: "off", criterion: "ready",
			wants: []string{"Auto-admit criterion: ready", "Auto-admit is off, so nothing enters this way until it is on."}},
		{name: "ready in shadow", autoAdmit: "shadow", criterion: "ready",
			wants: []string{"Auto-admit criterion: ready", "Auto-admit is in shadow, so this is only recorded until it is on."}},
		{name: "a value this CLI does not know", autoAdmit: "on", criterion: "later",
			wants: []string{"Auto-admit criterion: later."}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sentence := pipelineTrainCriterionSentence(client.PipelineMergeTrainSettings{AutoAdmit: testCase.autoAdmit,
				AutoAdmitCriterion: testCase.criterion})
			for _, want := range testCase.wants {
				if !strings.Contains(sentence, want) {
					t.Errorf("sentence lacks %q: %s", want, sentence)
				}
			}
			for _, reject := range testCase.rejects {
				if strings.Contains(sentence, reject) {
					t.Errorf("sentence has %q: %s", reject, sentence)
				}
			}
		})
	}
}

func TestPipelineTrainAutoAdmitSentenceInShadowAtReadyDoesNotSayGreen(t *testing.T) {
	sentence := pipelineTrainAutoAdmitSentence(client.PipelineMergeTrainSettings{AutoAdmit: "shadow",
		AutoAdmitCriterion: "ready"})
	if !strings.Contains(sentence, "which pull requests it would add at ready, and adds none") {
		t.Fatalf("sentence = %s", sentence)
	}
}

func TestPipelineTrainListShowsTheCriterionAndThePaths(t *testing.T) {
	mock := &pipelineTrainMock{train: &client.PipelineMergeTrain{
		Settings: *pipelineTrainStoredRule("on", "ready_shadow", pipelineTrainDefaultPaths, true),
	}}
	output, runError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID)
	if runError != nil {
		t.Fatalf("list: %v", runError)
	}
	for _, want := range []string{"Auto-admit criterion: ready_shadow",
		"Protected paths (the default list): alembic/**, deploy/charts/*/values.yaml"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}

	jsonOutput, jsonError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID,
		"-o", "json")
	if jsonError != nil {
		t.Fatalf("list -o json: %v", jsonError)
	}
	var document struct {
		Settings map[string]any `json:"settings"`
	}
	if decodeError := json.Unmarshal([]byte(jsonOutput), &document); decodeError != nil {
		t.Fatalf("list -o json is not parseable: %v\n%s", decodeError, jsonOutput)
	}
	if document.Settings["auto_admit_criterion"] != "ready_shadow" ||
		document.Settings["auto_admit_protected_paths_is_default"] != true {
		t.Fatalf("settings = %v", document.Settings)
	}
	if paths, isList := document.Settings["auto_admit_protected_paths"].([]any); !isList || len(paths) != 3 {
		t.Fatalf("auto_admit_protected_paths = %v", document.Settings["auto_admit_protected_paths"])
	}
	for _, kept := range []string{"enabled", "max_cars", "auto_admit"} {
		if _, isThere := document.Settings[kept]; !isThere {
			t.Fatalf("-o json lost the existing key %q: %v", kept, document.Settings)
		}
	}

	yamlOutput, yamlError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID,
		"-o", "yaml")
	if yamlError != nil {
		t.Fatalf("list -o yaml: %v", yamlError)
	}
	for _, want := range []string{"auto_admit_criterion: ready_shadow", "auto_admit_protected_paths:",
		"- alembic/**", "auto_admit_protected_paths_is_default: true"} {
		if !strings.Contains(yamlOutput, want) {
			t.Fatalf("yaml output lacks %q:\n%s", want, yamlOutput)
		}
	}
}

func TestPipelineTrainListTellsAnEmptyListFromAMissingOne(t *testing.T) {
	mock := &pipelineTrainMock{train: &client.PipelineMergeTrain{
		Settings: *pipelineTrainStoredRule("on", "ready", []string{}, false),
	}}
	output, runError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID)
	if runError != nil {
		t.Fatalf("list: %v", runError)
	}
	if !strings.Contains(output, "Protected paths: none; at ready, no path keeps the green rule.") {
		t.Fatalf("an empty list is not named:\n%s", output)
	}
	jsonOutput, jsonError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID,
		"-o", "json")
	if jsonError != nil {
		t.Fatalf("list -o json: %v", jsonError)
	}
	if !strings.Contains(jsonOutput, `"auto_admit_protected_paths": []`) {
		t.Fatalf("-o json dropped the empty list, which reads as an older platform:\n%s", jsonOutput)
	}
	yamlOutput, yamlError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID,
		"-o", "yaml")
	if yamlError != nil {
		t.Fatalf("list -o yaml: %v", yamlError)
	}
	if !strings.Contains(yamlOutput, "auto_admit_protected_paths: []") {
		t.Fatalf("-o yaml dropped the empty list, which reads as an older platform:\n%s", yamlOutput)
	}
}

func TestPipelineTrainListClaimsNothingOnAPlatformWithoutTheCriterion(t *testing.T) {
	mock := &pipelineTrainMock{train: &client.PipelineMergeTrain{
		Settings: client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 1, AutoAdmit: "on"},
	}}
	output, runError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID)
	if runError != nil {
		t.Fatalf("list: %v", runError)
	}
	for _, reject := range []string{"Auto-admit criterion", "Protected paths"} {
		if strings.Contains(output, reject) {
			t.Fatalf("a platform that sent neither is not described:\n%s", output)
		}
	}
	if !strings.Contains(output, "Auto-admit: on; the train adds green pull requests by itself.") {
		t.Fatalf("the mode sentence changed on an older platform:\n%s", output)
	}
	jsonOutput, jsonError := runPipelineTrainCommand(t, mock, "list", "--repository", testPipelineRepositoryID,
		"-o", "json")
	if jsonError != nil {
		t.Fatalf("list -o json: %v", jsonError)
	}
	for _, reject := range []string{"auto_admit_criterion", "auto_admit_protected_paths"} {
		if strings.Contains(jsonOutput, reject) {
			t.Fatalf("-o json invented %q:\n%s", reject, jsonOutput)
		}
	}
}

func TestPipelineTrainEnableFailsWhenThePlatformDidNotStoreIt(t *testing.T) {
	predates := func() *client.PipelineMergeTrainSettings {
		return &client.PipelineMergeTrainSettings{Enabled: true, MaxCars: 1, AutoAdmit: "on"}
	}
	cases := map[string]struct {
		flags []string
		want  string
	}{
		"the criterion": {flags: []string{"--auto-admit-criterion", "ready"},
			want: "did not store --auto-admit-criterion ready"},
		"a list":    {flags: []string{"--auto-admit-protected-paths", "a"}, want: "did not store the protected paths"},
		"a default": {flags: []string{"--auto-admit-protected-paths-default"}, want: "did not store the protected paths"},
		"none":      {flags: []string{"--auto-admit-protected-paths-none"}, want: "did not store the protected paths"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			mock := &pipelineTrainMock{settingsAnswer: predates()}
			arguments := append([]string{"enable", "--repository", testPipelineRepositoryID}, testCase.flags...)
			output, runError := runPipelineTrainCommand(t, mock, arguments...)
			if runError == nil || !strings.Contains(runError.Error(), testCase.want) {
				t.Fatalf("want %q, got %v", testCase.want, runError)
			}
			if exitCodeFor(runError) != exitError {
				t.Fatalf("exit code = %d, want %d", exitCodeFor(runError), exitError)
			}
			if strings.Contains(output, "Auto-admit criterion") || strings.Contains(output, "Protected paths") ||
				strings.Contains(output, "only run that has to pass") {
				t.Fatalf("output describes a rule the platform never stored:\n%s", output)
			}

			jsonOutput, jsonError := runPipelineTrainCommand(t, mock, append(arguments, "-o", "json")...)
			if jsonError == nil || !strings.Contains(jsonError.Error(), testCase.want) {
				t.Fatalf("-o json: want %q, got %v", testCase.want, jsonError)
			}
			// The harness writes stdout and stderr to one buffer, so read the
			// document stdout starts with and leave cobra's error text after it.
			var document map[string]any
			if decodeError := json.NewDecoder(strings.NewReader(jsonOutput)).Decode(&document); decodeError != nil {
				t.Fatalf("-o json is not parseable: %v\n%s", decodeError, jsonOutput)
			}
			if document["auto_admit"] != "on" {
				t.Fatalf("-o json lost what the platform did store: %v", document)
			}
		})
	}

	mock := &pipelineTrainMock{settingsAnswer: predates()}
	if _, runError := runPipelineTrainCommand(t, mock, "enable", "--repository", testPipelineRepositoryID); runError != nil {
		t.Fatalf("an enable naming neither is not refused on an older platform: %v", runError)
	}
}

func TestPipelineTrainEnableSurfacesThePlatformsRefusalInItsWords(t *testing.T) {
	sentence := "Merge train auto-admit can only be set to admit at ready by a human member, not a service account token."
	mock := &pipelineTrainMock{settingsError: errors.New(sentence)}
	output, runError := runPipelineTrainCommand(t, mock, "enable", "--auto-admit-criterion", "ready", "--repository",
		testPipelineRepositoryID)
	if runError == nil || runError.Error() != sentence {
		t.Fatalf("error = %v, want the platform's sentence as it reads", runError)
	}
	if strings.Contains(output, "The merge train is on") {
		t.Fatalf("a refused write is not reported as done:\n%s", output)
	}
}

func TestPipelineTrainDisableTakesNoCriterionOrPaths(t *testing.T) {
	command := newPipelineTrainSettingsCommand("disable", "off", false)
	for _, name := range []string{"auto-admit-criterion", "auto-admit-protected-paths",
		"auto-admit-protected-paths-default", "auto-admit-protected-paths-none"} {
		if command.Flags().Lookup(name) != nil {
			t.Fatalf("disable takes no --%s: switching the train off stops admission anyway", name)
		}
	}
	mock := &pipelineTrainMock{}
	if _, runError := runPipelineTrainCommand(t, mock, "disable", "--repository", testPipelineRepositoryID); runError != nil {
		t.Fatalf("disable: %v", runError)
	}
	if mock.settingsRequest.Enabled || mock.settingsRequest.AutoAdmitCriterion != nil ||
		mock.settingsRequest.AutoAdmitProtectedPaths != nil {
		t.Fatalf("settings request = %+v", mock.settingsRequest)
	}
}

func TestPipelineTrainEnableHelpNamesTheRuleAndWhoMaySetIt(t *testing.T) {
	command := newPipelineTrainSettingsCommand("enable", "on", true)
	for _, want := range []string{"--auto-admit-criterion green|ready_shadow|ready", "--auto-admit-protected-paths <glob,...>",
		"--auto-admit-protected-paths-default", "--auto-admit-protected-paths-none",
		"A service account token may set green or ready_shadow", "the only run that has to pass before the merge"} {
		if !strings.Contains(command.Long, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
