package cmd

import (
	"context"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

const testClusterUUID = "11111111-2222-3333-4444-555555555555"

type stackProfileMock struct {
	baseMock
	instantiateRequest *client.InstantiateStackProfileRequest
	instantiateResult  *client.InstantiateStackProfileResult
	detail             *client.StackProfileDetail
}

func (m *stackProfileMock) InstantiateStackProfile(ctx context.Context, clusterID string, request client.InstantiateStackProfileRequest) (*client.InstantiateStackProfileResult, error) {
	m.instantiateRequest = &request
	if m.instantiateResult != nil {
		return m.instantiateResult, nil
	}
	return &client.InstantiateStackProfileResult{
		DraftID:        "draft-1",
		StackName:      "observability",
		ProfileVersion: 2,
		AddonsCount:    1,
		ManifestsCount: 0,
	}, nil
}

func (m *stackProfileMock) GetStackProfile(profileID string) (*client.StackProfileDetail, error) {
	return m.detail, nil
}

func resetStackProfileApplyFlags(t *testing.T) {
	t.Helper()
	flags := stackProfilesApplyCmd.Flags()
	for name, value := range map[string]string{
		"cluster":    "",
		"stack-name": "",
		"version":    "0",
		"deploy":     "false",
		"output":     "",
	} {
		_ = flags.Set(name, value)
	}
	for _, name := range []string{"set", "set-file", "set-env"} {
		if sliceValue, ok := flags.Lookup(name).Value.(pflag.SliceValue); ok {
			_ = sliceValue.Replace([]string{})
		}
	}
}

func TestStackProfilesApplyCreatesDraft(t *testing.T) {
	resetStackProfileApplyFlags(t)
	mock := &stackProfileMock{}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "apply", "profile-1", "--cluster", testClusterUUID)
	})

	if mock.instantiateRequest == nil {
		t.Fatal("expected InstantiateStackProfile to be called")
	}
	if mock.instantiateRequest.ProfileID != "profile-1" {
		t.Errorf("profile id = %q, want profile-1", mock.instantiateRequest.ProfileID)
	}
	if mock.instantiateRequest.Deploy {
		t.Errorf("expected deploy=false by default")
	}
	if mock.instantiateRequest.Version != nil {
		t.Errorf("expected nil version by default, got %v", *mock.instantiateRequest.Version)
	}
	if !strings.Contains(stdout, "created as a draft") {
		t.Errorf("expected draft guidance in output, got: %s", stdout)
	}
}

func TestStackProfilesApplyBindsParameters(t *testing.T) {
	resetStackProfileApplyFlags(t)
	t.Setenv("ANKRA_TEST_SECRET", "s3cr3t-value")
	mock := &stackProfileMock{}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "apply", "profile-1",
			"--cluster", testClusterUUID,
			"--set", "replicas=3",
			"--set-env", "api_token=ANKRA_TEST_SECRET",
		)
	})

	bindings := map[string]string{}
	for _, binding := range mock.instantiateRequest.Parameters {
		bindings[binding.Name] = binding.Value
	}
	if bindings["replicas"] != "3" {
		t.Errorf("replicas binding = %q, want 3", bindings["replicas"])
	}
	if bindings["api_token"] != "s3cr3t-value" {
		t.Errorf("api_token binding = %q, want s3cr3t-value", bindings["api_token"])
	}
	if strings.Contains(stdout, "s3cr3t-value") {
		t.Errorf("secret value must not be echoed to output, got: %s", stdout)
	}
}

func TestStackProfilesApplyMissingEnvVarErrors(t *testing.T) {
	resetStackProfileApplyFlags(t)
	mock := &stackProfileMock{}
	setMockClient(t, mock)

	_, err := executeCommand("stack-profiles", "apply", "profile-1",
		"--cluster", testClusterUUID,
		"--set-env", "api_token=ANKRA_DEFINITELY_UNSET_VARIABLE",
	)
	if err == nil {
		t.Fatal("expected an error when the referenced environment variable is unset")
	}
	if mock.instantiateRequest != nil {
		t.Errorf("expected no API call when parameter resolution fails")
	}
}

func TestStackProfilesApplyDeploy(t *testing.T) {
	resetStackProfileApplyFlags(t)
	operationID := "operation-789"
	mock := &stackProfileMock{instantiateResult: &client.InstantiateStackProfileResult{
		DraftID:        "draft-1",
		StackName:      "observability",
		ProfileVersion: 2,
		AddonsCount:    1,
		Deployed:       true,
		OperationID:    &operationID,
		JobCount:       4,
	}}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "apply", "profile-1",
			"--cluster", testClusterUUID, "--deploy", "--version", "3")
	})

	if !mock.instantiateRequest.Deploy {
		t.Errorf("expected deploy=true")
	}
	if mock.instantiateRequest.Version == nil || *mock.instantiateRequest.Version != 3 {
		t.Errorf("expected version=3 in request, got %v", mock.instantiateRequest.Version)
	}
	if !strings.Contains(stdout, "deployed") {
		t.Errorf("expected deployed message, got: %s", stdout)
	}
	if !strings.Contains(stdout, "4 job") {
		t.Errorf("expected job count in output, got: %s", stdout)
	}
}

func TestStackProfilesApplyJSONOutput(t *testing.T) {
	resetStackProfileApplyFlags(t)
	mock := &stackProfileMock{}
	setMockClient(t, mock)

	output, err := executeCommand("stack-profiles", "apply", "profile-1",
		"--cluster", testClusterUUID, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "\"draft_id\"") {
		t.Errorf("expected json with draft_id, got: %s", output)
	}
}
