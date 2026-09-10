package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

const testClusterUUID = "11111111-2222-3333-4444-555555555555"

type stackProfileMock struct {
	baseMock
	instantiateRequest    *client.InstantiateStackProfileRequest
	instantiateResult     *client.InstantiateStackProfileResult
	instantiateClusterIDs []string
	failClusterID         string
	detail                *client.StackProfileDetail
}

func (m *stackProfileMock) InstantiateStackProfile(ctx context.Context, clusterID string, request client.InstantiateStackProfileRequest) (*client.InstantiateStackProfileResult, error) {
	m.instantiateRequest = &request
	m.instantiateClusterIDs = append(m.instantiateClusterIDs, clusterID)
	if m.failClusterID != "" && clusterID == m.failClusterID {
		return nil, errors.New("cluster is offline")
	}
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
		"stack-name": "",
		"version":    "0",
		"deploy":     "false",
		"dry-run":    "false",
		"output":     "",
	} {
		_ = flags.Set(name, value)
	}
	for _, name := range []string{"cluster", "set", "set-file", "set-env"} {
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

func TestStackProfilesApplyToSeveralClusters(t *testing.T) {
	resetStackProfileApplyFlags(t)
	second := "22222222-2222-3333-4444-555555555555"
	mock := &stackProfileMock{instantiateResult: &client.InstantiateStackProfileResult{
		DraftID: "draft-1", StackName: "hello-fleet", ProfileVersion: 2, ManifestsCount: 4, Deployed: true, JobCount: 6,
	}}
	setMockClient(t, mock)

	stdout := captureStdout(t, func() {
		_, _ = executeCommand("stack-profiles", "apply", "profile-1", "--cluster", testClusterUUID, "--cluster", second, "--deploy")
	})

	if len(mock.instantiateClusterIDs) != 2 || mock.instantiateClusterIDs[0] != testClusterUUID || mock.instantiateClusterIDs[1] != second {
		t.Fatalf("clusters applied = %v", mock.instantiateClusterIDs)
	}
	if !mock.instantiateRequest.Deploy {
		t.Errorf("expected --deploy to carry through to every cluster")
	}
	for _, want := range []string{"== " + testClusterUUID, "== " + second, "CLUSTER", "hello-fleet", "v2", "deployed"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestStackProfilesApplyToSeveralClustersKeepsGoingPastAFailure(t *testing.T) {
	resetStackProfileApplyFlags(t)
	second := "22222222-2222-3333-4444-555555555555"
	mock := &stackProfileMock{failClusterID: testClusterUUID}
	setMockClient(t, mock)

	var executeError error
	stdout := captureStdout(t, func() {
		_, executeError = executeCommand("stack-profiles", "apply", "profile-1", "--cluster", testClusterUUID, "--cluster", second)
	})

	if executeError == nil {
		t.Fatal("expected the command to report the failed cluster")
	}
	if len(mock.instantiateClusterIDs) != 2 {
		t.Fatalf("the second cluster should still be applied after the first failed: %v", mock.instantiateClusterIDs)
	}
	if !strings.Contains(stdout, "failed") || !strings.Contains(stdout, "cluster is offline") || !strings.Contains(stdout, "draft") {
		t.Errorf("stdout = %s", stdout)
	}
}

func TestStackProfilesApplyToSeveralClustersStructuredOutputStillFails(t *testing.T) {
	resetStackProfileApplyFlags(t)
	second := "22222222-2222-3333-4444-555555555555"
	mock := &stackProfileMock{failClusterID: testClusterUUID}
	setMockClient(t, mock)

	var executeError error
	var output string
	captured := captureStdout(t, func() {
		output, executeError = executeCommand("stack-profiles", "apply", "profile-1", "--cluster", testClusterUUID, "--cluster", second, "-o", "json")
	})
	if executeError == nil {
		t.Fatal("-o json must still exit non-zero when a cluster failed")
	}
	if !strings.Contains(output+captured, `"status": "failed"`) || !strings.Contains(output+captured, `"status": "draft"`) {
		t.Errorf("the JSON payload should still be emitted before the error:\n%s%s", output, captured)
	}
}
