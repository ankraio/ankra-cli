package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type applicationPipelineConvertMock struct {
	baseMock

	conversion    *client.PipelineConversion
	convertError  error
	applicationID string
	keepWorkflows bool
	calls         int
}

func (mock *applicationPipelineConvertMock) ConvertApplicationPipeline(requestContext context.Context,
	applicationID string, keepWorkflows bool) (*client.PipelineConversion, error) {
	mock.calls++
	mock.applicationID = applicationID
	mock.keepWorkflows = keepWorkflows
	return mock.conversion, mock.convertError
}

func convertedPipeline() *client.PipelineConversion {
	return &client.PipelineConversion{
		Outcome:        "started",
		Message:        "Converted to an Ankra pipeline: it builds from the next push.",
		PullRequestURL: "https://github.com/acme/shop/pull/42",
		PipelineSource: "ankra_pipeline",
		RemovedPaths:   []string{".github/workflows/build-and-publish.yml"},
		DisabledWorkflows: []string{
			".github/workflows/build-and-publish.yml",
		},
	}
}

// The default call is the one-click conversion: nothing but the application
// id, and the answer names the pull request and what left the repository.
func TestApplicationPipelineConvertIsOneCall(t *testing.T) {
	mockClient := &applicationPipelineConvertMock{conversion: convertedPipeline()}
	output, executeError := runApplicationCommand(t, mockClient, "pipeline", "convert", testApplicationID)
	if executeError != nil {
		t.Fatalf("convert error = %v", executeError)
	}
	if mockClient.calls != 1 || mockClient.applicationID != testApplicationID {
		t.Fatalf("calls = %d application id = %q", mockClient.calls, mockClient.applicationID)
	}
	if mockClient.keepWorkflows {
		t.Fatal("the default conversion removes the generated workflow; keepWorkflows was sent")
	}
	for _, expected := range []string{
		"Converted to an Ankra pipeline: it builds from the next push.",
		"Pull request: https://github.com/acme/shop/pull/42",
		"Removed by the pull request: .github/workflows/build-and-publish.yml",
		"Switched off on GitHub: .github/workflows/build-and-publish.yml",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "Warning:") {
		t.Errorf("no warning was owed:\n%s", output)
	}
}

func TestApplicationPipelineConvertKeepsWorkflowsWhenAsked(t *testing.T) {
	conversion := convertedPipeline()
	conversion.RemovedPaths = nil
	conversion.DisableWorkflowsMessage = "Could not switch off the generated workflow on GitHub (build-and-publish.yml: " +
		"GitHub answered Forbidden); it stops building when the pull request that removes it merges."
	mockClient := &applicationPipelineConvertMock{conversion: conversion}
	output, executeError := runApplicationCommand(t, mockClient, "pipeline", "convert", testApplicationID,
		"--keep-workflows")
	if executeError != nil {
		t.Fatalf("convert error = %v", executeError)
	}
	if !mockClient.keepWorkflows {
		t.Fatal("--keep-workflows was not sent")
	}
	if strings.Contains(output, "Removed by the pull request") {
		t.Errorf("nothing was removed:\n%s", output)
	}
	if !strings.Contains(output, "Warning: Could not switch off the generated workflow on GitHub") {
		t.Errorf("the refusal to switch the workflow off must be said:\n%s", output)
	}
}

func TestApplicationPipelineConvertRendersJSON(t *testing.T) {
	mockClient := &applicationPipelineConvertMock{conversion: convertedPipeline()}
	output, executeError := runApplicationCommand(t, mockClient, "pipeline", "convert", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("convert error = %v", executeError)
	}
	if !strings.Contains(output, `"pipeline_source": "ankra_pipeline"`) ||
		!strings.Contains(output, `"pull_request_url": "https://github.com/acme/shop/pull/42"`) {
		t.Errorf("json output = %s", output)
	}
}

// The platform's refusal is the sentence the user reads; the command adds
// nothing over it and fails.
func TestApplicationPipelineConvertSurfacesTheRefusal(t *testing.T) {
	mockClient := &applicationPipelineConvertMock{
		convertError: errors.New("This application already runs on an Ankra pipeline."),
	}
	_, executeError := runApplicationCommand(t, mockClient, "pipeline", "convert", testApplicationID)
	if executeError == nil || !strings.Contains(executeError.Error(), "already runs on an Ankra pipeline") {
		t.Fatalf("error = %v", executeError)
	}
}
