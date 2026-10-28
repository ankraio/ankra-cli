package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

func resetStackProfileGetFlags(t *testing.T) {
	t.Helper()
	flags := stackProfilesGetCmd.Flags()
	_ = flags.Set("version", "0")
	_ = flags.Set("output", "")
}

func TestStackProfilesGetShowsParameters(t *testing.T) {
	resetStackProfileGetFlags(t)
	title := "API Token"
	description := "Token used to authenticate"
	mock := &stackProfileMock{detail: &client.StackProfileDetail{
		Profile: client.StackProfileSummary{
			ID:             "profile-1",
			Name:           "observability",
			Category:       "general",
			Visibility:     "organisation",
			LatestVersion:  2,
			CurrentVersion: 2,
		},
		Versions: []client.StackProfileVersionSummary{
			{ID: "version-2", Version: 2, Channel: "stable", CreatedAt: "2026-06-01T12:00:00Z"},
		},
		CurrentVersionDetail: &client.StackProfileVersionDetail{
			Version: 2,
			Channel: "stable",
			Parameters: []client.StackProfileParameter{
				{Name: "api_token", Title: &title, Description: &description, Type: "secret", Required: true},
			},
		},
	}}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "get", "profile-1")
	})

	if !strings.Contains(stdout, "observability") {
		t.Errorf("expected profile name in output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "api_token") {
		t.Errorf("expected parameter name in output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Current version: v2") {
		t.Errorf("expected current version in output, got: %s", stdout)
	}
}

func TestStackProfilesGetJSONOutput(t *testing.T) {
	resetStackProfileGetFlags(t)
	mock := &stackProfileMock{detail: &client.StackProfileDetail{
		Profile: client.StackProfileSummary{ID: "profile-1", Name: "observability", CurrentVersion: 1, LatestVersion: 1},
	}}
	setMockClient(t, mock)

	output, err := executeCommand("stack-profiles", "get", "profile-1", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "\"profile\"") {
		t.Errorf("expected json with profile field, got: %s", output)
	}
}

func TestStackProfilesGetListsWhatEachChoiceSets(t *testing.T) {
	resetStackProfileGetFlags(t)
	large := "Qwen3 32B on one L40S"
	reasoning := "Needs a 120Gi model store."
	mock := &stackProfileMock{detail: &client.StackProfileDetail{
		Profile: client.StackProfileSummary{ID: "profile-1", Name: "gpu-chat", CurrentVersion: 1, LatestVersion: 1},
		CurrentVersionDetail: &client.StackProfileVersionDetail{
			Version: 1,
			Channel: "stable",
			Parameters: []client.StackProfileParameter{
				{
					Name: "model_size", Type: "enum", EnumValues: []string{"8b", "32b"},
					Options: []client.StackProfileParameterOption{
						{Value: "8b", Sets: map[string]string{"model_id": "Qwen/Qwen3-8B"}},
						{Value: "32b", Title: &large, Description: &reasoning, Sets: map[string]string{
							"model_store_size": "120Gi", "model_id": "Qwen/Qwen3-32B"}},
					},
				},
				{Name: "model_id", Type: "string"},
				{Name: "model_store_size", Type: "string"},
			},
		},
	}}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "get", "profile-1")
	})

	for _, expected := range []string{
		"Choices for model_size (--set model_size=<value>):",
		"  8b\n    sets model_id=Qwen/Qwen3-8B",
		"  32b  Qwen3 32B on one L40S\n    Needs a 120Gi model store.\n    sets model_id=Qwen/Qwen3-32B\n    sets model_store_size=120Gi",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("expected %q in output, got: %s", expected, stdout)
		}
	}
	if strings.Contains(stdout, "Choices for model_id") {
		t.Errorf("an input without options must not get a choices block, got: %s", stdout)
	}
}
