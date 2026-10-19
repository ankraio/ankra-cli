package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServicesPackagesInitWritesAPinnedSchemaTwoSkeletonForTheProtocol(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "", "packages", "init", "--profile", fakeMemberProfileID,
		"--profile-version", fakeMemberProfileVersionID, "--name", "dragonfly-cache", "--protocol", "redis")
	if runError != nil {
		t.Fatal(runError)
	}
	var skeleton struct {
		SchemaVersion int    `json:"schema_version"`
		PublisherID   string `json:"publisher_id"`
		Capability    string `json:"capability"`
		Version       string `json:"version"`
		Profiles      map[string]struct {
			ID, VersionID, Digest string
		} `json:"profiles"`
		Lifecycle  map[string]string `json:"lifecycle"`
		Connection struct {
			Protocol   string                       `json:"protocol"`
			SecretName string                       `json:"secret_name"`
			SecretKeys []string                     `json:"secret_keys"`
			Endpoints  []map[string]any             `json:"endpoints"`
			Fields     map[string]map[string]string `json:"fields"`
		} `json:"connection"`
	}
	if decodeError := json.Unmarshal([]byte(stdout), &skeleton); decodeError != nil {
		t.Fatalf("%v\n%s", decodeError, stdout)
	}
	if skeleton.SchemaVersion != 2 || skeleton.PublisherID != fakeServiceOrganisation || skeleton.Capability != "cache" || skeleton.Version != "1.0.0" ||
		skeleton.Profiles["customer"].Digest != "sha256:"+strings.Repeat("9", 64) || skeleton.Lifecycle["verify"] != "stack-profile-verify" {
		t.Fatalf("skeleton %+v", skeleton)
	}
	if skeleton.Connection.Protocol != "redis" || skeleton.Connection.SecretName != "" ||
		strings.Join(skeleton.Connection.SecretKeys, ",") != "url,port,username,password" ||
		skeleton.Connection.Fields["host"]["kind"] != "primary_host" || skeleton.Connection.Fields["password"]["value"] != "password" ||
		len(skeleton.Connection.Endpoints) != 1 || skeleton.Connection.Endpoints[0]["service"] != "" {
		t.Fatalf("connection %+v", skeleton.Connection)
	}
	if len(platform.requests("GET", "/profile-pins/"+fakeMemberProfileID+"/"+fakeMemberProfileVersionID)) != 1 {
		t.Fatal("init must read the profile version's pin")
	}
}

func TestServicesPackagesInitRefusesAnUnknownProtocolAndAnExistingFile(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "", "packages", "init", "--profile", fakeMemberProfileID,
		"--profile-version", fakeMemberProfileVersionID, "--name", "broker", "--protocol", "amqp")
	if runError == nil || !strings.Contains(runError.Error(), `unknown protocol "amqp"`) || len(platform.requests("GET", "/profile-pins/")) != 0 {
		t.Fatalf("an unknown protocol is refused before any request: %v", runError)
	}
	existing := filepath.Join(t.TempDir(), "package.json")
	if writeError := os.WriteFile(existing, []byte("{}"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	_, _, runError = runServicesCommand(t, platform, "", "packages", "init", "--profile", fakeMemberProfileID,
		"--profile-version", fakeMemberProfileVersionID, "--name", "dragonfly-cache", "--protocol", "redis", "-f", existing)
	if runError == nil || !strings.Contains(runError.Error(), "already exists") {
		t.Fatalf("init must never overwrite a file: %v", runError)
	}
	written := filepath.Join(t.TempDir(), "dragonfly-cache.json")
	_, stderr, runError := runServicesCommand(t, platform, "", "packages", "init", "--profile", fakeMemberProfileID,
		"--profile-version", fakeMemberProfileVersionID, "--name", "dragonfly-cache", "--protocol", "s3", "-f", written)
	content, readError := os.ReadFile(written)
	if runError != nil || readError != nil || !strings.Contains(string(content), `"kind": "primary_address"`) ||
		!strings.Contains(string(content), `"capability": "object_storage"`) || !strings.Contains(stderr, "ankra services packages publish -f") {
		t.Fatalf("%v %v\n%s\n%s", runError, readError, content, stderr)
	}
}

func TestServicesPackagesPublishSendsTheDocumentAsWrittenAndReportsARefusal(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	document := `{"schema_version":2,"name":"dragonfly-cache","version":"1.0.0","capability":"cache",` +
		`"connection":{"protocol":"redis","secret_name":"dragonfly-auth"}}`
	path := filepath.Join(t.TempDir(), "package.json")
	if writeError := os.WriteFile(path, []byte(document), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	stdout, _, runError := runServicesCommand(t, platform, "", "packages", "publish", "-f", path, "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	published := platform.requests("POST", "/api/v1/org/service-packages")
	if len(published) != 1 || published[0].body != document {
		t.Fatalf("the document must be sent exactly as written: %+v", published)
	}
	if !strings.Contains(stdout, `"name": "dragonfly-cache"`) {
		t.Fatalf("stdout %s", stdout)
	}

	unfilled := strings.Replace(document, `"dragonfly-auth"`, `""`, 1)
	_, _, runError = runServicesCommand(t, platform, unfilled, "packages", "publish", "-f", "-")
	if runError == nil || !strings.Contains(runError.Error(), "connection.secret_name") {
		t.Fatalf("the platform's refusal path must reach the user: %v", runError)
	}
	_, _, runError = runServicesCommand(t, platform, "not json", "packages", "publish", "-f", "-")
	if runError == nil || !strings.Contains(runError.Error(), "not a JSON object") || len(platform.requests("POST", "/api/v1/org/service-packages")) != 2 {
		t.Fatalf("a document that is not a JSON object is refused before publishing: %v", runError)
	}
}
