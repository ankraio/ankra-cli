package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestSuggestAccessOnKubeTokenDenied(t *testing.T) {
	original := client.NewUnexpectedResponseError(403, `kube token request failed: status 403, body: {"detail":"You do not have access to this cluster"}`)
	err := suggestAccessOnKubeTokenDenied(original, "arm64-build")

	if !strings.Contains(err.Error(), "ankra cluster access grant <your-email> --cluster arm64-build --role view") {
		t.Fatalf("expected access-grant suggestion, got: %v", err)
	}
	// The original error must stay in the chain so exit-code classification
	// still sees the 403.
	var unexpected *client.UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != 403 {
		t.Fatalf("expected wrapped UnexpectedResponseError with status 403, got: %v", err)
	}
	if code := exitCodeFor(err); code != exitAuth {
		t.Fatalf("expected exit code %d, got %d", exitAuth, code)
	}
}

func TestSuggestAccessOnKubeTokenDeniedPassesOtherErrorsThrough(t *testing.T) {
	for _, err := range []error{
		client.NewUnexpectedResponseError(500, "kube token request failed: status 500"),
		errors.New("dial tcp: connection refused"),
	} {
		if got := suggestAccessOnKubeTokenDenied(err, "demo"); got != err {
			t.Fatalf("expected %v to pass through unchanged, got: %v", err, got)
		}
	}
}
