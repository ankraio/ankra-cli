package cmd

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
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
	if runError == nil || len(platform.requests("GET", "/profile-pins/")) != 0 {
		t.Fatalf("an unknown protocol is refused before any request: %v", runError)
	}
	want := `invalid --protocol "amqp": must be one of nats, opensearch, postgres, redis, s3, victorialogs, victoriametrics`
	if runError.Error() != want || exitCodeFor(runError) != exitUsage {
		t.Fatalf("an unknown protocol is a usage error (exit 2) naming the allowed values:\n got %q (exit %d)\nwant %q (exit 2)",
			runError, exitCodeFor(runError), want)
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

func TestServicesPackagesInitFromAnAddonCreatesItsProfileAndPinsIt(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	values := filepath.Join(t.TempDir(), "values.yaml")
	if writeError := os.WriteFile(values, []byte("storage:\n  enabled: true\n"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	stdout, stderr, runError := runServicesCommand(t, platform, "", "packages", "init", "--addon", "dragonfly", "--registry", "dragonfly",
		"--chart-version", "v1.34.1", "--values", values, "--name", "dragonfly-cache", "--protocol", "redis",
		"--generate-secret", "dragonfly-auth", "--generate-key", "username=default", "--generate-key", "password:48")
	if runError != nil {
		t.Fatalf("%v\n%s", runError, stderr)
	}
	created := platform.requests("POST", "/stack-profiles/from-chart")
	if len(created) != 1 || len(platform.requests("POST", "/stack-profiles/import")) != 0 {
		t.Fatalf("one from-chart request and no client-built import, got %d", len(created))
	}
	var request client.ChartServiceProfileRequest
	if decodeError := json.Unmarshal([]byte(created[0].body), &request); decodeError != nil {
		t.Fatal(decodeError)
	}
	if request.Name != "dragonfly-cache" || request.ChartName != "dragonfly" || request.ChartVersion != "v1.34.1" ||
		request.RegistryName != "dragonfly" || request.ValuesYAML != "storage:\n  enabled: true\n" || request.GeneratedSecret == nil ||
		request.GeneratedSecret.Name != "dragonfly-auth" || len(request.GeneratedSecret.Keys) != 2 ||
		request.GeneratedSecret.Keys[0] != (client.GeneratedServiceSecretKey{Key: "username", Value: "default"}) ||
		request.GeneratedSecret.Keys[1] != (client.GeneratedServiceSecretKey{Key: "password", Length: 48}) {
		t.Fatalf("from-chart request %s", created[0].body)
	}
	var skeleton client.ServicePackageContract
	if decodeError := json.Unmarshal([]byte(stdout), &skeleton); decodeError != nil || skeleton.Connection == nil ||
		skeleton.Connection.SecretName != "dragonfly-auth" {
		t.Fatalf("the skeleton names the generated Secret: %v\n%s", decodeError, stdout)
	}
	if len(platform.requests("GET", "/profile-pins/"+fakeMemberProfileID+"/"+fakeMemberProfileVersionID)) != 1 ||
		!strings.Contains(stdout, fakeMemberProfileVersionID) || !strings.Contains(stderr, "Created stack profile dragonfly-cache") {
		t.Fatalf("the package must be pinned to the profile the platform created\n%s\n%s", stdout, stderr)
	}
}

func TestParseGeneratedKeysReadsRandomSizedAndFixedKeys(t *testing.T) {
	keys, parseError := parseGeneratedKeys([]string{"password", "token:32", "username=app"})
	if parseError != nil || len(keys) != 3 || keys[0] != (client.GeneratedServiceSecretKey{Key: "password"}) ||
		keys[1] != (client.GeneratedServiceSecretKey{Key: "token", Length: 32}) ||
		keys[2] != (client.GeneratedServiceSecretKey{Key: "username", Value: "app"}) {
		t.Fatalf("%+v %v", keys, parseError)
	}
	for _, refused := range []string{"", ":32", "password:x", "password:0", "=app", "username=", "password:48=secret"} {
		if _, parseError := parseGeneratedKeys([]string{refused}); parseError == nil {
			t.Fatalf("%q must be refused", refused)
		}
	}
}

func TestServicesPackagesInitRefusesAnAddonAndAProfileTogetherBeforeCreatingAnything(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	for _, arguments := range [][]string{
		{"--addon", "dragonfly", "--registry", "dragonfly", "--chart-version", "1", "--profile", fakeMemberProfileID, "--profile-version", fakeMemberProfileVersionID},
		{"--addon", "dragonfly", "--registry", "dragonfly"},
		{"--registry", "dragonfly"},
		{"--profile", fakeMemberProfileID, "--profile-version", fakeMemberProfileVersionID, "--generate-secret", "auth", "--generate-key", "password"},
		{"--addon", "dragonfly", "--registry", "dragonfly", "--chart-version", "1", "--generate-key", "password"},
		{"--addon", "dragonfly", "--registry", "dragonfly", "--chart-version", "1", "--generate-secret", "auth"},
		{},
	} {
		_, _, runError := runServicesCommand(t, platform, "", append([]string{"packages", "init", "--name", "dragonfly-cache", "--protocol", "redis"}, arguments...)...)
		if runError == nil {
			t.Fatalf("%v must be refused", arguments)
		}
		if exitCodeFor(runError) != exitUsage {
			t.Fatalf("%v is a flag mistake and must exit 2, got exit %d: %v", arguments, exitCodeFor(runError), runError)
		}
	}
	existing := filepath.Join(t.TempDir(), "package.json")
	if writeError := os.WriteFile(existing, []byte("{}"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	_, _, runError := runServicesCommand(t, platform, "", "packages", "init", "--addon", "dragonfly", "--registry", "dragonfly",
		"--chart-version", "1", "--name", "dragonfly-cache", "--protocol", "redis", "-f", existing)
	if runError == nil || !strings.Contains(runError.Error(), "already exists") {
		t.Fatalf("an existing output file is refused: %v", runError)
	}
	unwritable := filepath.Join(existing, "package.json")
	_, _, runError = runServicesCommand(t, platform, "", "packages", "init", "--addon", "dragonfly", "--registry", "dragonfly",
		"--chart-version", "1", "--name", "dragonfly-cache", "--protocol", "redis", "-f", unwritable)
	if runError == nil || !strings.Contains(runError.Error(), "before creating anything") {
		t.Fatalf("a path that cannot be checked is refused: %v", runError)
	}
	if len(platform.requests("POST", "/stack-profiles/from-chart")) != 0 {
		t.Fatal("nothing is created before the flags and the output file are known to be usable")
	}
	reservedPath := filepath.Join(t.TempDir(), "package.json")
	_, _, runError = runServicesCommand(t, platform, "", "packages", "init", "--addon", "dragonfly", "--registry", "unknown",
		"--chart-version", "1", "--name", "dragonfly-cache", "--protocol", "redis", "-f", reservedPath)
	if runError == nil {
		t.Fatal("an unknown registry is refused")
	}
	if _, statError := os.Stat(reservedPath); !errors.Is(statError, fs.ErrNotExist) {
		t.Fatalf("the output file reserved before a refused run is removed again: %v", statError)
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

func TestReadServicePackageDocumentRefusesWhatThePlatformWouldNotRead(t *testing.T) {
	for name, input := range map[string]string{
		"null":      "null",
		"an array":  "[]",
		"too large": `{"padding":"` + strings.Repeat("x", maxServicePackageDocumentBytes) + `"}`,
	} {
		if _, readError := readServicePackageDocument(strings.NewReader(input), "-"); readError == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
	oversized := filepath.Join(t.TempDir(), "large.json")
	if writeError := os.WriteFile(oversized, []byte(`{"padding":"`+strings.Repeat("x", maxServicePackageDocumentBytes)+`"}`), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if _, readError := readServicePackageDocument(nil, oversized); readError == nil || !strings.Contains(readError.Error(), "256 KiB") {
		t.Fatalf("an oversized file is refused with the limit: %v", readError)
	}
	if document, readError := readServicePackageDocument(strings.NewReader(`{"name":"x"}`), "-"); readError != nil || string(document) != `{"name":"x"}` {
		t.Fatalf("%s %v", document, readError)
	}
}
