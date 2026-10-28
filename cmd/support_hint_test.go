package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestSupportHintPrintedForUnexpectedResponse(t *testing.T) {
	var out bytes.Buffer
	unexpectedResponse := fmt.Errorf("reconcile failed: %w", &client.UnexpectedResponseError{StatusCode: 500})

	printSupportHintForUnexpectedError(&out, clusterCmd, unexpectedResponse)

	if !strings.Contains(out.String(), "ankra support create --category bug") {
		t.Errorf("expected support hint, got %q", out.String())
	}
}

func TestSupportHintSkippedForOrdinaryErrors(t *testing.T) {
	var out bytes.Buffer

	printSupportHintForUnexpectedError(&out, clusterCmd, errors.New("cluster \"prod\" not found"))

	if out.Len() != 0 {
		t.Errorf("expected no hint for ordinary error, got %q", out.String())
	}
}

func TestSupportHintSkippedForClientErrorResponses(t *testing.T) {
	for _, statusCode := range []int{400, 401, 403, 404, 409, 422} {
		var out bytes.Buffer
		err := fmt.Errorf("create access grant failed: %w", &client.UnexpectedResponseError{StatusCode: statusCode})

		printSupportHintForUnexpectedError(&out, clusterCmd, err)

		if out.Len() != 0 {
			t.Errorf("expected no hint for status %d, got %q", statusCode, out.String())
		}
	}
}

func TestSupportHintSkippedForSupportCommands(t *testing.T) {
	var out bytes.Buffer
	unexpectedResponse := fmt.Errorf("create support ticket: %w", &client.UnexpectedResponseError{StatusCode: 500})

	printSupportHintForUnexpectedError(&out, supportCreateCmd, unexpectedResponse)

	if out.Len() != 0 {
		t.Errorf("expected no hint for support command, got %q", out.String())
	}
}

func TestSupportHintPrintedWhenCommandUnknown(t *testing.T) {
	var out bytes.Buffer

	printSupportHintForUnexpectedError(&out, nil, &client.UnexpectedResponseError{StatusCode: 502})

	if !strings.Contains(out.String(), "ankra support create") {
		t.Errorf("expected support hint, got %q", out.String())
	}
}
