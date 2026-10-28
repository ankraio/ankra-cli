package cmd

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

type chartsTemplateMock struct {
	baseMock
	templateResult  *client.TemplateChartResult
	templateError   error
	capturedRequest client.TemplateChartRequest
}

func (m *chartsTemplateMock) TemplateChart(request client.TemplateChartRequest) (*client.TemplateChartResult, error) {
	m.capturedRequest = request
	if m.templateError != nil {
		return nil, m.templateError
	}
	return m.templateResult, nil
}

func encodeManifest(content string) string {
	return base64.StdEncoding.EncodeToString([]byte(content))
}

func TestChartsTemplatePrintsManifestStreamWithSourceHeaders(t *testing.T) {
	resetCommandFlags(t, chartsTemplateCmd)
	notes := "Deployed nginx.\n"
	mock := &chartsTemplateMock{
		templateResult: &client.TemplateChartResult{
			Rendered: []client.RenderedChartManifest{
				{Path: "nginx/templates/deployment.yaml", ContentBase64: encodeManifest("kind: Deployment\n")},
				{Path: "nginx/templates/service.yaml", ContentBase64: encodeManifest("kind: Service")},
			},
			Notes: &notes,
		},
	}
	setMockClient(t, mock)

	output, err := executeCommand("charts", "template", "nginx", "--version", "1.2.3", "--repository", "repo-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, expected := range []string{
		"---\n# Source: nginx/templates/deployment.yaml\nkind: Deployment\n",
		"---\n# Source: nginx/templates/service.yaml\nkind: Service\n",
		"NOTES:\nDeployed nginx.\n",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output missing %q:\n%s", expected, output)
		}
	}
	if mock.capturedRequest.RepositoryName != "repo-a" || mock.capturedRequest.ChartName != "nginx" ||
		mock.capturedRequest.ChartVersion != "1.2.3" {
		t.Errorf("unexpected request: %+v", mock.capturedRequest)
	}
	if mock.capturedRequest.ValuesBase64 != nil || mock.capturedRequest.ReleaseName != nil ||
		mock.capturedRequest.Namespace != nil {
		t.Errorf("optional fields unexpectedly set: %+v", mock.capturedRequest)
	}
}

func TestChartsTemplateSendsValuesFileAndRenderOptions(t *testing.T) {
	resetCommandFlags(t, chartsTemplateCmd)
	valuesContent := "replicaCount: 5\n"
	valuesPath := filepath.Join(t.TempDir(), "values.yaml")
	if writeError := os.WriteFile(valuesPath, []byte(valuesContent), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	mock := &chartsTemplateMock{
		templateResult: &client.TemplateChartResult{
			Rendered: []client.RenderedChartManifest{
				{Path: "nginx/templates/deployment.yaml", ContentBase64: encodeManifest("kind: Deployment\n")},
			},
		},
	}
	setMockClient(t, mock)

	_, err := executeCommand("charts", "template", "nginx", "--version", "1.2.3", "--repository", "repo-a",
		"-f", valuesPath, "--release-name", "preview", "--namespace", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.capturedRequest.ValuesBase64 == nil ||
		*mock.capturedRequest.ValuesBase64 != base64.StdEncoding.EncodeToString([]byte(valuesContent)) {
		t.Errorf("values not forwarded: %+v", mock.capturedRequest.ValuesBase64)
	}
	if mock.capturedRequest.ReleaseName == nil || *mock.capturedRequest.ReleaseName != "preview" {
		t.Errorf("release name not forwarded: %+v", mock.capturedRequest.ReleaseName)
	}
	if mock.capturedRequest.Namespace == nil || *mock.capturedRequest.Namespace != "staging" {
		t.Errorf("namespace not forwarded: %+v", mock.capturedRequest.Namespace)
	}
}

func TestChartsTemplateSurfacesHelmErrorNonZero(t *testing.T) {
	resetCommandFlags(t, chartsTemplateCmd)
	mock := &chartsTemplateMock{
		templateError: &client.ChartTemplateError{Message: "values don't meet the specifications of the schema(s): replicaCount must be integer"},
	}
	setMockClient(t, mock)

	_, err := executeCommand("charts", "template", "nginx", "--version", "1.2.3", "--repository", "repo-a")
	if err == nil || !strings.Contains(err.Error(), "replicaCount must be integer") {
		t.Fatalf("expected the helm error text, got: %v", err)
	}
	if exitCodeFor(err) == exitOK {
		t.Error("helm error must exit non-zero")
	}
}

func TestChartsTemplateUnknownChartExitsNotFound(t *testing.T) {
	resetCommandFlags(t, chartsTemplateCmd)
	mock := &chartsTemplateMock{
		templateError: fmt.Errorf("chart %q version %q in repository %q: %w",
			"nginx", "9.9.9", "repo-a", client.ErrChartNotFound),
	}
	setMockClient(t, mock)

	_, err := executeCommand("charts", "template", "nginx", "--version", "9.9.9", "--repository", "repo-a")
	if err == nil || !errors.Is(err, client.ErrChartNotFound) {
		t.Fatalf("expected a not-found error, got: %v", err)
	}
	if exitCodeFor(err) != exitNotFound {
		t.Errorf("exit code = %d, want %d", exitCodeFor(err), exitNotFound)
	}
}
