package cmd

import (
	"context"
	"strings"
	"testing"
)

func cleanUpExecProjectScope(t *testing.T) {
	t.Helper()
	resetGlobalProjectFlag(t)
	t.Cleanup(func() {
		resetGlobalProjectFlag(t)
		projectPinnedExplicitly = false
	})
}

func assertEveryRequestCarriesTheProject(t *testing.T, api *fakeWorkspaceAPI, projectID string) {
	t.Helper()
	if len(api.projectHeaders) == 0 {
		t.Fatal("no API request reached the platform")
	}
	for index, header := range api.projectHeaders {
		if header != projectID {
			t.Fatalf("API request %d carried project %q, want %q", index, header, projectID)
		}
	}
}

func TestExecSendsTheProjectFlagOnEveryRequest(t *testing.T) {
	environment := newExecTestEnv(t)
	cleanUpExecProjectScope(t)
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))
	if _, stderr, code := runExecCommand(t, context.Background(), "--project", projectTestEdgeID, "--", "true"); code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	assertEveryRequestCarriesTheProject(t, environment.api, projectTestEdgeID)
	if !projectPinnedExplicitly {
		t.Fatal("--project must count as an explicit pin")
	}
}

func TestExecSendsTheProjectEnvironmentOnEveryRequest(t *testing.T) {
	environment := newExecTestEnv(t)
	cleanUpExecProjectScope(t)
	t.Setenv(envAnkraProject, projectTestEdgeID)
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))
	if _, stderr, code := runExecCommand(t, context.Background(), "--", "true"); code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	assertEveryRequestCarriesTheProject(t, environment.api, projectTestEdgeID)
}

func TestExecWithAnUnknownProjectDoesNotRun(t *testing.T) {
	newExecTestEnv(t)
	cleanUpExecProjectScope(t)
	mock := &projectsMock{projects: twoProjects()}
	setMockClient(t, mock)
	_, stderr, code := runExecCommand(t, context.Background(), "--project", "nowhere", "--", "true")
	if code != execExitNotRun {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, execExitNotRun, stderr)
	}
	if !strings.Contains(stderr, "nowhere") {
		t.Fatalf("stderr = %q, want it to name the project", stderr)
	}
}

func TestExecCheckCachesEachProjectSeparately(t *testing.T) {
	environment := newExecTestEnv(t)
	cleanUpExecProjectScope(t)
	if _, _, code := runExecCommand(t, context.Background(), "--check", "--project", projectTestDefaultID); code != 0 {
		t.Fatalf("--check = %d", code)
	}
	requests := environment.api.requests
	if _, _, code := runExecCommand(t, context.Background(), "--check", "--project", projectTestDefaultID); code != 0 {
		t.Fatalf("cached --check = %d", code)
	}
	if environment.api.requests != requests {
		t.Fatalf("the cached --check asked the platform again (%d -> %d requests)", requests, environment.api.requests)
	}
	environment.api.repositories = nil
	if _, _, code := runExecCommand(t, context.Background(), "--check", "--project", projectTestEdgeID); code != 1 {
		t.Fatalf("--check in another project = %d, want 1 from its own answer", code)
	}
	if environment.api.requests == requests {
		t.Fatal("--check in another project reused the first project's cached answer")
	}
}

func TestEnsureExecClientAppliesTheProjectAfterTheExecOrganisation(t *testing.T) {
	withTempHome(t)
	cleanUpExecProjectScope(t)
	t.Setenv(envAnkraOrg, "")
	t.Setenv("ANKRA_EXEC_ORG", projectTestOrganisationID)
	t.Setenv(envAnkraProject, "edge")
	mock := &organisationSwitchMock{projectsMock: projectsMock{projects: twoProjects()}}
	setMockClient(t, mock)
	if err := ensureExecClient(execCmd); err != nil {
		t.Fatal(err)
	}
	if mock.organisation != projectTestOrganisationID || mock.project != projectTestEdgeID {
		t.Fatalf("organisation %q, project %q", mock.organisation, mock.project)
	}
}

func TestEnsureExecClientAppliesTheSavedSelectionOnlyToTheSavedLogin(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		savedToken string
		envToken   string
		want       string
	}{
		{name: "the saved login", savedToken: "saved-token", want: projectTestEdgeID},
		{name: "an environment token", envToken: "environment-token"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			withFreshViperAndEnv(t)
			withTempHome(t)
			cleanUpExecProjectScope(t)
			t.Setenv(envAnkraOrg, "")
			t.Setenv("ANKRA_EXEC_ORG", "")
			t.Setenv(envAnkraProject, "")
			if testCase.savedToken != "" {
				writeAnkraConfig(t, map[string]string{"token": testCase.savedToken, "base-url": "https://saved.example.com"})
			}
			if err := saveSelectedProject(SelectedProject{ProjectID: projectTestEdgeID}); err != nil {
				t.Fatal(err)
			}
			setMockClient(t, &projectsMock{})
			apiClient = nil
			t.Setenv("ANKRA_API_TOKEN", testCase.envToken)
			if err := ensureExecClient(execCmd); err != nil {
				t.Fatal(err)
			}
			if got := apiClient.ProjectOverride(); got != testCase.want {
				t.Fatalf("project = %q, want %q", got, testCase.want)
			}
			if projectPinnedExplicitly {
				t.Fatal("a saved selection is not an explicit pin")
			}
		})
	}
}
