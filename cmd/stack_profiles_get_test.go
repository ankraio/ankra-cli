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
