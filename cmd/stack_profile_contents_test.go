package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type stackProfileContentsMock struct {
	baseMock
	payload json.RawMessage
	err     error
}

func (m *stackProfileContentsMock) GetStackProfileVersion(ctx context.Context, profileID string, version int) (json.RawMessage, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.payload, nil
}

func encodeForProfile(content string) string {
	return base64.StdEncoding.EncodeToString([]byte(content))
}

const clusterManifestYAML = `apiVersion: opensearch.org/v1
kind: OpenSearchCluster
metadata:
  name: logs
  namespace: opensearch
spec:
  general:
    serviceName: logs
`

const namespaceManifestYAML = `apiVersion: v1
kind: Namespace
metadata:
  name: opensearch
`

const fluentBitValuesYAML = `config:
  pipeline:
    outputs:
      - name: opensearch
        http_user: fluentbit
`

func profileVersionFixture() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
  "version": 2,
  "channel": "stable",
  "spec": {
    "stacks": [
      {
        "name": "opensearch",
        "manifests": [
          {"name": "opensearch-namespace", "manifest_base64": %q},
          {"name": "opensearch-cluster", "manifest_base64": %q}
        ],
        "addons": [
          {
            "name": "fluent-bit",
            "chart_name": "fluent-bit-collector",
            "chart_version": "1.1.1",
            "namespace": "opensearch",
            "configuration": {"values_base64": %q}
          }
        ]
      }
    ]
  }
}`,
		encodeForProfile(namespaceManifestYAML),
		encodeForProfile(clusterManifestYAML),
		encodeForProfile(fluentBitValuesYAML)))
}

func resetStackProfileContentsFlags(t *testing.T) {
	t.Helper()
	flags := stackProfilesContentsCmd.Flags()
	_ = flags.Set("resource", "")
	_ = flags.Set("all", "false")
	_ = flags.Set("output", "")
}

func TestStackProfileContentsListsResources(t *testing.T) {
	resetStackProfileContentsFlags(t)
	setMockClient(t, &stackProfileContentsMock{payload: profileVersionFixture()})

	stdout, err := executeCommand("stack-profiles", "contents", "profile-1", "2")
	if err != nil {
		t.Fatalf("contents failed: %v", err)
	}

	for _, expected := range []string{
		"fluent-bit",
		"fluent-bit-collector",
		"1.1.1",
		"opensearch-cluster",
		// The kind is read out of the decoded manifest, which is the whole
		// point: the encoded spec cannot tell you what a resource is.
		"OpenSearchCluster",
		"Namespace",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("expected %q in inventory output, got:\n%s", expected, stdout)
		}
	}
}

func TestStackProfileContentsPrintsOneResourceDecoded(t *testing.T) {
	resetStackProfileContentsFlags(t)
	setMockClient(t, &stackProfileContentsMock{payload: profileVersionFixture()})

	stdout, err := executeCommand("stack-profiles", "contents", "profile-1", "2", "--resource", "opensearch-cluster")
	if err != nil {
		t.Fatalf("contents failed: %v", err)
	}

	if !strings.Contains(stdout, "kind: OpenSearchCluster") {
		t.Errorf("expected decoded manifest YAML, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "manifest_base64") {
		t.Errorf("expected decoded content, not the encoded envelope, got:\n%s", stdout)
	}
}

func TestStackProfileContentsPrintsAddonValues(t *testing.T) {
	resetStackProfileContentsFlags(t)
	setMockClient(t, &stackProfileContentsMock{payload: profileVersionFixture()})

	stdout, err := executeCommand("stack-profiles", "contents", "profile-1", "2", "--resource", "fluent-bit")
	if err != nil {
		t.Fatalf("contents failed: %v", err)
	}

	if !strings.Contains(stdout, "http_user: fluentbit") {
		t.Errorf("expected decoded add-on values, got:\n%s", stdout)
	}
}

func TestStackProfileContentsRawKeepsBase64(t *testing.T) {
	resetStackProfileContentsFlags(t)
	setMockClient(t, &stackProfileContentsMock{payload: profileVersionFixture()})

	stdout, err := executeCommand("stack-profiles", "contents", "profile-1", "2", "--resource", "opensearch-cluster", "-o", "raw")
	if err != nil {
		t.Fatalf("contents failed: %v", err)
	}

	if !strings.Contains(stdout, encodeForProfile(clusterManifestYAML)) {
		t.Errorf("expected base64 content for -o raw, got:\n%s", stdout)
	}
}

func TestStackProfileContentsAllStreamsEveryResource(t *testing.T) {
	resetStackProfileContentsFlags(t)
	setMockClient(t, &stackProfileContentsMock{payload: profileVersionFixture()})

	stdout, err := executeCommand("stack-profiles", "contents", "profile-1", "2", "--all")
	if err != nil {
		t.Fatalf("contents failed: %v", err)
	}

	for _, expected := range []string{
		"# manifest: opensearch-namespace",
		"# manifest: opensearch-cluster",
		"# addon: fluent-bit",
		"kind: OpenSearchCluster",
		"http_user: fluentbit",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("expected %q in --all output, got:\n%s", expected, stdout)
		}
	}
}

func TestStackProfileContentsUnknownResourceListsAvailable(t *testing.T) {
	resetStackProfileContentsFlags(t)
	setMockClient(t, &stackProfileContentsMock{payload: profileVersionFixture()})

	_, err := executeCommand("stack-profiles", "contents", "profile-1", "2", "--resource", "nope")
	if err == nil {
		t.Fatal("expected an error for an unknown resource")
	}
	if !strings.Contains(err.Error(), "opensearch-cluster") {
		t.Errorf("expected the error to list available resources, got: %v", err)
	}
}

func TestStackProfileContentsRejectsResourceWithAll(t *testing.T) {
	resetStackProfileContentsFlags(t)
	setMockClient(t, &stackProfileContentsMock{payload: profileVersionFixture()})

	_, err := executeCommand("stack-profiles", "contents", "profile-1", "2", "--resource", "fluent-bit", "--all")
	if err == nil {
		t.Fatal("expected an error when --resource and --all are combined")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected a mutual-exclusion error, got: %v", err)
	}
}

func TestManifestKindAndNamespaceHandlesMultiDocAndJunk(t *testing.T) {
	kind, namespace := manifestKindAndNamespace(namespaceManifestYAML + "---\n" + clusterManifestYAML)
	if !strings.HasPrefix(kind, "Namespace") || !strings.Contains(kind, "+1") {
		t.Errorf("expected the first kind plus a count for a multi-doc manifest, got %q", kind)
	}
	if namespace != "opensearch" {
		t.Errorf("expected the first namespace found across the documents, got %q", namespace)
	}

	// An unparseable manifest must degrade rather than break the listing.
	if kind, namespace := manifestKindAndNamespace("\t not: [valid"); kind != "-" || namespace != "-" {
		t.Errorf("expected placeholders for unparseable content, got %q/%q", kind, namespace)
	}
}
