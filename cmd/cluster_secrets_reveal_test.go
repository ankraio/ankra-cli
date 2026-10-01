package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"
)

const revealTestClusterID = "4f0b7c1e-2d3a-4b5c-8d9e-0a1b2c3d4e5f"

// revealTestServer answers the cluster lookup and resources/get, recording
// every resources/get body. marker is the secret_values marker it puts on
// the answer; data is the Secret's data section; empty answers with no item.
type revealTestServer struct {
	marker string
	data   map[string]interface{}
	empty  bool
	bodies []map[string]interface{}
}

func (s *revealTestServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters":
			_ = json.NewEncoder(writer).Encode(client.ClusterListResponse{
				Result:     []client.ClusterListItem{{ID: revealTestClusterID, Name: "reveal-test"}},
				Pagination: client.Pagination{TotalPages: 1},
			})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/api/v1/clusters/"+revealTestClusterID+"/kubernetes/resources/get":
			raw, _ := io.ReadAll(request.Body)
			body := map[string]interface{}{}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("resources/get body is not JSON: %v", err)
			}
			s.bodies = append(s.bodies, body)
			item := client.ResourceResponseItem{Status: "success", Kind: "Secret", Version: "v1", SecretValues: s.marker}
			if !s.empty {
				item.Items = []interface{}{map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata":   map[string]interface{}{"name": "db-credentials", "namespace": "default"},
					"type":       "Opaque",
					"data":       s.data,
				}}
			} else {
				item.SecretValues = ""
			}
			_ = json.NewEncoder(writer).Encode(client.GetResourcesResponse{
				ResourceResponses: []client.ResourceResponseItem{item},
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func runClusterGetSecrets(t *testing.T, server *httptest.Server, args ...string) (string, string, error) {
	t.Helper()
	useTestClient(t, server.URL)
	secretsCommand, _, findError := rootCmd.Find([]string{"cluster", "get", "secrets"})
	if findError != nil {
		t.Fatalf("find cluster get secrets: %v", findError)
	}
	resetCommandFlags(t, secretsCommand)
	t.Cleanup(func() {
		resetCommandFlags(t, secretsCommand)
		_ = clusterCmd.PersistentFlags().Set(activeClusterFlagName, "")
		clusterCmd.PersistentFlags().Lookup(activeClusterFlagName).Changed = false
	})
	var stdout string
	var runError error
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			_, runError = executeCommand(append([]string{"cluster", "get", "secrets", "--cluster", revealTestClusterID}, args...)...)
		})
	})
	return stdout, stderr, runError
}

func TestClusterGetSecretRevealSendsRevealOnlyForNamedRead(t *testing.T) {
	fake := &revealTestServer{marker: client.SecretValuesRevealed, data: map[string]interface{}{"password": "aHVudGVyMg=="}}
	server := fake.start(t)

	stdout, _, err := runClusterGetSecrets(t, server, "db-credentials", "-n", "default", "--reveal")
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if len(fake.bodies) != 1 {
		t.Fatalf("expected one resources/get call, got %d", len(fake.bodies))
	}
	if reveal, _ := fake.bodies[0]["reveal_secret_values"].(bool); !reveal {
		t.Errorf("--reveal must send reveal_secret_values: true, body: %v", fake.bodies[0])
	}
	requests, _ := fake.bodies[0]["resource_requests"].([]interface{})
	if len(requests) != 1 {
		t.Fatalf("expected one resource request, got %v", fake.bodies[0])
	}
	requestItem, _ := requests[0].(map[string]interface{})
	if requestItem["name"] != "db-credentials" || requestItem["namespace"] != "default" {
		t.Errorf("reveal must be a named, namespaced read, got %v", requestItem)
	}
	if !strings.Contains(stdout, "aHVudGVyMg==") {
		t.Errorf("revealed value missing from output:\n%s", stdout)
	}
}

func TestClusterGetSecretWithoutRevealOmitsTheFieldAndNotesDigests(t *testing.T) {
	fake := &revealTestServer{marker: client.SecretValuesWithheld, data: map[string]interface{}{"password": "sha256:b073aefd7c92"}}
	server := fake.start(t)

	_, stderr, err := runClusterGetSecrets(t, server, "db-credentials", "-n", "default")
	if err != nil {
		t.Fatalf("named read: %v", err)
	}
	if _, present := fake.bodies[0]["reveal_secret_values"]; present {
		t.Errorf("a read without --reveal must not send reveal_secret_values, body: %v", fake.bodies[0])
	}
	if !strings.Contains(stderr, "sha256 digests") || !strings.Contains(stderr, "--reveal") {
		t.Errorf("digest note missing from stderr:\n%s", stderr)
	}
}

func TestClusterGetSecretsListingInTableModeHasNoDigestNote(t *testing.T) {
	fake := &revealTestServer{marker: client.SecretValuesWithheld, data: map[string]interface{}{"password": "sha256:b073aefd7c92"}}
	server := fake.start(t)

	_, stderr, err := runClusterGetSecrets(t, server, "-n", "default")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if _, present := fake.bodies[0]["reveal_secret_values"]; present {
		t.Errorf("a listing must not send reveal_secret_values, body: %v", fake.bodies[0])
	}
	if strings.Contains(stderr, "sha256 digests") {
		t.Errorf("table listing shows no values, so no digest note:\n%s", stderr)
	}
}

func TestClusterGetSecretsRevealRefusedOnListings(t *testing.T) {
	cases := map[string][]string{
		"no name":        {"-n", "default", "--reveal"},
		"all namespaces": {"db-credentials", "-A", "--reveal"},
		"selector":       {"db-credentials", "-n", "default", "-l", "app=web", "--reveal"},
		"no namespace":   {"db-credentials", "--reveal"},
		"name flag only": {"--name", "db-credentials", "-n", "default", "--reveal"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &revealTestServer{marker: client.SecretValuesRevealed}
			server := fake.start(t)
			_, _, err := runClusterGetSecrets(t, server, args...)
			if err == nil {
				t.Fatal("expected --reveal to be refused")
			}
			if code := exitCodeFor(err); code != exitUsage {
				t.Errorf("exit code = %d, want %d (usage): %v", code, exitUsage, err)
			}
			if len(fake.bodies) != 0 {
				t.Errorf("a refused reveal must not call the platform, got %v", fake.bodies)
			}
		})
	}
}

func TestClusterGetSecretRevealPermissionRequired(t *testing.T) {
	fake := &revealTestServer{marker: client.SecretValuesPermissionRequired, data: map[string]interface{}{"password": "sha256:b073aefd7c92"}}
	server := fake.start(t)

	stdout, _, err := runClusterGetSecrets(t, server, "db-credentials", "-n", "default", "--reveal")
	if err == nil {
		t.Fatal("expected an error when the role lacks kubernetes.secrets_reveal")
	}
	if code := exitCodeFor(err); code != exitForbidden {
		t.Errorf("exit code = %d, want %d (forbidden)", code, exitForbidden)
	}
	if !strings.Contains(err.Error(), "kubernetes.secrets_reveal") {
		t.Errorf("error must name the permission: %v", err)
	}
	if strings.Contains(stdout, "sha256:") {
		t.Errorf("digests must not be printed as the answer to --reveal:\n%s", stdout)
	}
}

func TestClusterGetSecretRevealUnavailable(t *testing.T) {
	fake := &revealTestServer{marker: client.SecretValuesUnavailable, data: map[string]interface{}{"password": "sha256:b073aefd7c92"}}
	server := fake.start(t)

	stdout, _, err := runClusterGetSecrets(t, server, "db-credentials", "-n", "default", "--reveal")
	if err == nil {
		t.Fatal("expected an error when the platform could not reveal")
	}
	if code := exitCodeFor(err); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if stdout != "" {
		t.Errorf("nothing may be printed when the reveal failed:\n%s", stdout)
	}
}

func TestClusterGetSecretRevealNotFound(t *testing.T) {
	fake := &revealTestServer{empty: true}
	server := fake.start(t)

	_, _, err := runClusterGetSecrets(t, server, "missing", "-n", "default", "--reveal")
	if err == nil {
		t.Fatal("expected not found")
	}
	if code := exitCodeFor(err); code != exitNotFound {
		t.Errorf("exit code = %d, want %d (not found): %v", code, exitNotFound, err)
	}
}

func TestDescribeRendersDigestAsWithheld(t *testing.T) {
	object := map[string]interface{}{
		"kind": "Secret",
		"data": map[string]interface{}{"password": "sha256:b073aefd7c92", "plain": "aHVudGVyMg=="},
	}
	redactSecretData("Secret", object)
	data := object["data"].(map[string]interface{})
	if data["password"] != "<redacted: value withheld by the platform>" {
		t.Errorf("digest rendered as %v", data["password"])
	}
	if data["plain"] != "<redacted: 12 bytes>" {
		t.Errorf("value rendered as %v", data["plain"])
	}
}
