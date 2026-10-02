package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	fakeHostAgentBinary = "#!/bin/sh\necho fake ankra-host-agent\n"
	fakeHostAgentUnit   = "[Unit]\nDescription=Ankra host agent\n[Service]\nExecStart=/usr/local/bin/ankra-host-agent run\n"
)

// fakeAgentRelease serves ankra-host-agent release assets the way a GitHub
// release does, and counts what was downloaded.
type fakeAgentRelease struct {
	mutex     sync.Mutex
	assets    map[string]string
	downloads []string
	url       string
}

func newFakeAgentRelease(t *testing.T, checksums string) *fakeAgentRelease {
	t.Helper()
	release := &fakeAgentRelease{assets: map[string]string{
		"/latest/download/ankra-host-agent-linux-amd64": fakeHostAgentBinary,
		"/latest/download/ankra-host-agent.service":     fakeHostAgentUnit,
		"/latest/download/SHA256SUMS":                   checksums,
	}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		release.mutex.Lock()
		release.downloads = append(release.downloads, request.URL.Path)
		content, isPublished := release.assets[request.URL.Path]
		release.mutex.Unlock()
		if !isPublished {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(writer, content)
	}))
	t.Cleanup(server.Close)
	release.url = server.URL
	return release
}

func (release *fakeAgentRelease) downloaded() []string {
	release.mutex.Lock()
	defer release.mutex.Unlock()
	return append([]string{}, release.downloads...)
}

func sha256Hex(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func honestChecksums() string {
	return fmt.Sprintf("%s  ankra-host-agent-linux-amd64\n%s  ankra-host-agent-linux-arm64\n%s  ankra-host-agent.service\n",
		sha256Hex(fakeHostAgentBinary), sha256Hex("arm64 build"), sha256Hex(fakeHostAgentUnit))
}

// fakeHost records every process register starts and what it got on stdin.
type fakeHost struct {
	mutex       sync.Mutex
	invocations []recordedInvocation
	failPath    string
}

type recordedInvocation struct {
	path        string
	arguments   []string
	environment []string
	stdin       string
}

func (host *fakeHost) run(_ context.Context, invocation hostCommandInvocation) error {
	stdin := ""
	if invocation.stdin != nil {
		content, _ := io.ReadAll(invocation.stdin)
		stdin = string(content)
	}
	host.mutex.Lock()
	host.invocations = append(host.invocations, recordedInvocation{
		path: invocation.path, arguments: invocation.arguments, environment: invocation.environment, stdin: stdin,
	})
	host.mutex.Unlock()
	if host.failPath != "" && invocation.path == host.failPath {
		return errors.New("exit status 1")
	}
	return nil
}

func (host *fakeHost) recorded() []recordedInvocation {
	host.mutex.Lock()
	defer host.mutex.Unlock()
	return append([]recordedInvocation{}, host.invocations...)
}

// useFakeHost points register at a Linux host rooted in a temporary
// directory, running as userID.
func useFakeHost(t *testing.T, userID int, operatingSystem string) (*fakeHost, string, string) {
	t.Helper()
	withTempHome(t)
	root := t.TempDir()
	host := &fakeHost{}
	binaryPath := filepath.Join(root, "usr", "local", "bin", "ankra-host-agent")
	unitPath := filepath.Join(root, "etc", "systemd", "system", "ankra-host-agent.service")
	previous := currentHostAgentHost
	currentHostAgentHost = func() hostAgentHost {
		return hostAgentHost{
			operatingSystem: operatingSystem,
			architecture:    "amd64",
			effectiveUserID: func() int { return userID },
			httpClient:      http.DefaultClient,
			runCommand:      host.run,
			binaryPath:      binaryPath,
			unitPath:        unitPath,
		}
	}
	t.Cleanup(func() { currentHostAgentHost = previous })
	return host, binaryPath, unitPath
}

func registerArguments(releaseURL string, extra ...string) []string {
	arguments := []string{"register", "--environment", "production", "--name", "web-1",
		"--label", "zone=a", "--label", "role=web", "--token-stdin", "--release-url", releaseURL}
	return append(arguments, extra...)
}

func TestTargetsRegisterInstallsVerifiesRegistersAndStarts(t *testing.T) {
	t.Setenv(envAnkraBaseURL, "https://platform.example.test")
	release := newFakeAgentRelease(t, honestChecksums())
	host, binaryPath, unitPath := useFakeHost(t, 0, "linux")

	stdout, stderr, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret+"\n", registerArguments(release.url)...)
	if executeError != nil {
		t.Fatalf("register failed: %v\nstderr:\n%s", executeError, stderr)
	}
	installedBinary, _ := os.ReadFile(binaryPath)
	if string(installedBinary) != fakeHostAgentBinary {
		t.Fatalf("binary not installed at %s", binaryPath)
	}
	if info, statError := os.Stat(binaryPath); statError != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("binary mode = %v (%v)", info.Mode(), statError)
	}
	installedUnit, _ := os.ReadFile(unitPath)
	if string(installedUnit) != fakeHostAgentUnit {
		t.Fatalf("unit not installed at %s", unitPath)
	}

	invocations := host.recorded()
	if len(invocations) != 4 {
		t.Fatalf("register, daemon-reload, enable and restart expected, got %+v", invocations)
	}
	agentRegister := invocations[0]
	wantArguments := "register --environment production --name web-1 --label role=web --label zone=a --token-stdin"
	if agentRegister.path != binaryPath || strings.Join(agentRegister.arguments, " ") != wantArguments {
		t.Fatalf("agent invocation = %s %v", agentRegister.path, agentRegister.arguments)
	}
	if agentRegister.stdin != testJoinTokenSecret+"\n" {
		t.Fatalf("the join token goes to the agent on stdin, got %q", agentRegister.stdin)
	}
	if strings.Join(agentRegister.environment, " ") != "ANKRA_URL=https://platform.example.test" {
		t.Fatalf("agent environment = %v", agentRegister.environment)
	}
	for _, invocation := range invocations {
		if strings.Contains(strings.Join(invocation.arguments, " "), testJoinTokenSecret) {
			t.Fatalf("the join token must never be an argument: %v", invocation.arguments)
		}
	}
	if strings.Join(invocations[1].arguments, " ") != "daemon-reload" ||
		strings.Join(invocations[2].arguments, " ") != "enable ankra-host-agent.service" ||
		strings.Join(invocations[3].arguments, " ") != "restart ankra-host-agent.service" ||
		invocations[1].path != "systemctl" || invocations[3].path != "systemctl" {
		t.Fatalf("systemctl calls = %+v", invocations[1:])
	}
	if strings.Contains(stdout+stderr, testJoinTokenSecret) {
		t.Fatalf("the join token must not be printed:\n%s\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "Registered web-1 in environment production") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestTargetsRegisterJSONKeepsStdoutParseable(t *testing.T) {
	release := newFakeAgentRelease(t, honestChecksums())
	useFakeHost(t, 0, "linux")
	stdout, _, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret, registerArguments(release.url, "-o", "json")...)
	if executeError != nil {
		t.Fatalf("register -o json failed: %v", executeError)
	}
	var document struct {
		DryRun       bool              `json:"dry_run"`
		BinarySHA256 string            `json:"binary_sha256"`
		Labels       map[string]string `json:"labels"`
		Steps        []string          `json:"steps"`
	}
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if document.DryRun || document.BinarySHA256 != sha256Hex(fakeHostAgentBinary) || document.Labels["role"] != "web" || len(document.Steps) != 9 {
		t.Fatalf("document = %+v", document)
	}
}

func TestTargetsRegisterRefusesAChecksumMismatch(t *testing.T) {
	tampered := strings.Replace(honestChecksums(), sha256Hex(fakeHostAgentBinary), sha256Hex("something else"), 1)
	release := newFakeAgentRelease(t, tampered)
	host, binaryPath, unitPath := useFakeHost(t, 0, "linux")
	_, _, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret, registerArguments(release.url)...)
	if executeError == nil || !strings.Contains(executeError.Error(), "refusing it") {
		t.Fatalf("a mismatching binary must be refused, got %v", executeError)
	}
	assertNothingInstalled(t, host, binaryPath, unitPath)
}

func TestTargetsRegisterRefusesAnAssetMissingFromTheChecksums(t *testing.T) {
	release := newFakeAgentRelease(t, fmt.Sprintf("%s  ankra-host-agent-linux-amd64\n", sha256Hex(fakeHostAgentBinary)))
	host, binaryPath, unitPath := useFakeHost(t, 0, "linux")
	_, _, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret, registerArguments(release.url)...)
	if executeError == nil || !strings.Contains(executeError.Error(), "lists no checksum for ankra-host-agent.service") {
		t.Fatalf("an unlisted asset must be refused, got %v", executeError)
	}
	assertNothingInstalled(t, host, binaryPath, unitPath)
}

func TestTargetsRegisterRefusesWhenTheSignatureCheckFails(t *testing.T) {
	release := newFakeAgentRelease(t, honestChecksums())
	host, binaryPath, unitPath := useFakeHost(t, 0, "linux")
	previous := verifyHostAgentReleaseSignature
	verifyHostAgentReleaseSignature = func(context.Context, hostAgentRelease) error { return errors.New("signature invalid") }
	t.Cleanup(func() { verifyHostAgentReleaseSignature = previous })
	_, _, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret, registerArguments(release.url)...)
	if executeError == nil || !strings.Contains(executeError.Error(), "signature invalid") {
		t.Fatalf("a failed signature check must refuse the release, got %v", executeError)
	}
	assertNothingInstalled(t, host, binaryPath, unitPath)
}

func TestTargetsRegisterRefusesANonRootUser(t *testing.T) {
	release := newFakeAgentRelease(t, honestChecksums())
	host, binaryPath, unitPath := useFakeHost(t, 1000, "linux")
	_, _, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret, registerArguments(release.url)...)
	if executeError == nil || !strings.Contains(executeError.Error(), "must run as root") {
		t.Fatalf("a non-root user must be refused, got %v", executeError)
	}
	if downloads := release.downloaded(); len(downloads) != 0 {
		t.Fatalf("nothing may be downloaded for a non-root user, got %v", downloads)
	}
	assertNothingInstalled(t, host, binaryPath, unitPath)
}

func TestTargetsRegisterDoesNotInstallTheServiceWhenTheAgentRefuses(t *testing.T) {
	release := newFakeAgentRelease(t, honestChecksums())
	host, binaryPath, unitPath := useFakeHost(t, 0, "linux")
	host.failPath = binaryPath
	_, _, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret, registerArguments(release.url)...)
	if executeError == nil || !strings.Contains(executeError.Error(), "the service was not installed") {
		t.Fatalf("a failed agent register must stop the install, got %v", executeError)
	}
	if _, statError := os.Stat(unitPath); !os.IsNotExist(statError) {
		t.Fatalf("the unit must not be installed after a failed register")
	}
	if invocations := host.recorded(); len(invocations) != 1 {
		t.Fatalf("systemctl must not run after a failed register, got %+v", invocations)
	}
}

func TestTargetsRegisterNoInstallOnlyPrintsThePlan(t *testing.T) {
	release := newFakeAgentRelease(t, honestChecksums())
	host, binaryPath, unitPath := useFakeHost(t, 1000, "linux")
	stdout, _, executeError := runDeployCommand(t, newTargetsCommand(), "", registerArguments(release.url, "--no-install")...)
	if executeError != nil {
		t.Fatalf("--no-install failed: %v", executeError)
	}
	for _, want := range []string{
		"nothing was changed",
		"download " + release.url + "/latest/download/ankra-host-agent-linux-amd64",
		"download " + release.url + "/latest/download/ankra-host-agent.service",
		release.url + "/latest/download/SHA256SUMS",
		"register --environment production --name web-1 --label role=web --label zone=a --token-stdin (join token on stdin)",
		"systemctl enable ankra-host-agent.service",
		"systemctl restart ankra-host-agent.service",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("plan misses %q:\n%s", want, stdout)
		}
	}
	if downloads := release.downloaded(); len(downloads) != 0 {
		t.Fatalf("--no-install must not download, got %v", downloads)
	}
	assertNothingInstalled(t, host, binaryPath, unitPath)

	stdout, _, executeError = runDeployCommand(t, newTargetsCommand(), "",
		registerArguments(release.url, "--no-install", "--agent-version", "2.1.40", "-o", "json")...)
	if executeError != nil {
		t.Fatalf("--no-install -o json failed: %v", executeError)
	}
	var document struct {
		DryRun  bool `json:"dry_run"`
		Release struct {
			Version   string `json:"version"`
			BinaryURL string `json:"binary_url"`
		} `json:"release"`
	}
	if decodeError := json.Unmarshal([]byte(stdout), &document); decodeError != nil || !document.DryRun ||
		document.Release.Version != "v2.1.40" ||
		document.Release.BinaryURL != release.url+"/download/v2.1.40/ankra-host-agent-linux-amd64" {
		t.Fatalf("plan JSON = %s (%v)", stdout, decodeError)
	}
}

func TestTargetsRegisterRefusesBadInvocations(t *testing.T) {
	release := newFakeAgentRelease(t, honestChecksums())
	cases := []struct {
		name            string
		operatingSystem string
		stdin           string
		arguments       []string
		want            string
	}{
		{name: "no --token-stdin", operatingSystem: "linux", stdin: testJoinTokenSecret,
			arguments: []string{"register", "--environment", "production", "--name", "web-1", "--release-url", release.url},
			want:      "--token-stdin is required"},
		{name: "empty stdin", operatingSystem: "linux", stdin: "\n",
			arguments: registerArguments(release.url), want: "no join token on stdin"},
		{name: "not Linux", operatingSystem: "darwin", stdin: testJoinTokenSecret,
			arguments: registerArguments(release.url), want: "runs on Linux only"},
		{name: "bad label", operatingSystem: "linux", stdin: testJoinTokenSecret,
			arguments: append(registerArguments(release.url), "--label", "novalue"), want: "must be key=value"},
		{name: "label key with a space", operatingSystem: "linux", stdin: testJoinTokenSecret,
			arguments: append(registerArguments(release.url), "--label", "tier =web"), want: "must be key=value"},
		{name: "label key ending in a dot", operatingSystem: "linux", stdin: testJoinTokenSecret,
			arguments: append(registerArguments(release.url), "--label", "tier.=web"), want: "must be key=value"},
		{name: "label value too long", operatingSystem: "linux", stdin: testJoinTokenSecret,
			arguments: append(registerArguments(release.url), "--label", "tier="+strings.Repeat("v", 64)),
			want:      "longer than 63 characters"},
		{name: "too many labels", operatingSystem: "linux", stdin: testJoinTokenSecret,
			arguments: append(registerArguments(release.url), manyLabels(31)...), want: "at most 32 labels"},
		{name: "plain http release URL", operatingSystem: "linux", stdin: testJoinTokenSecret,
			arguments: append(registerArguments(release.url), "--release-url", "http://mirror.example.test/releases"),
			want:      "--release-url: refusing to download the agent over plaintext http://"},
		{name: "bad name", operatingSystem: "linux", stdin: testJoinTokenSecret,
			arguments: []string{"register", "--environment", "production", "--name", "-web", "--token-stdin"},
			want:      "not a valid host target name"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			host, binaryPath, unitPath := useFakeHost(t, 0, testCase.operatingSystem)
			_, _, executeError := runDeployCommand(t, newTargetsCommand(), testCase.stdin, testCase.arguments...)
			if exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), testCase.want) {
				t.Fatalf("got %v (exit %d), want a usage error containing %q", executeError, exitCodeFor(executeError), testCase.want)
			}
			// No API token is ever sent to a release URL, so no refusal may
			// claim one would be (ankra-0bm15).
			if strings.Contains(executeError.Error(), "API token") {
				t.Fatalf("refusal mentions an API token: %v", executeError)
			}
			assertNothingInstalled(t, host, binaryPath, unitPath)
		})
	}
}

func assertNothingInstalled(t *testing.T, host *fakeHost, binaryPath string, unitPath string) {
	t.Helper()
	for _, path := range []string{binaryPath, unitPath} {
		if _, statError := os.Stat(path); !os.IsNotExist(statError) {
			t.Fatalf("%s must not be installed", path)
		}
	}
	if invocations := host.recorded(); len(invocations) != 0 {
		t.Fatalf("no process may run, got %+v", invocations)
	}
}

func manyLabels(count int) []string {
	arguments := make([]string, 0, 2*count)
	for index := 0; index < count; index++ {
		arguments = append(arguments, "--label", fmt.Sprintf("key-%d=value", index))
	}
	return arguments
}

func TestTargetsRegisterForwardsTheValidatedLabels(t *testing.T) {
	release := newFakeAgentRelease(t, honestChecksums())
	host, _, _ := useFakeHost(t, 0, "linux")
	arguments := append(registerArguments(release.url), "--label", "topology.example.com/rack=r1", "--label", "empty=")
	if _, stderr, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret, arguments...); executeError != nil {
		t.Fatalf("register failed: %v\n%s", executeError, stderr)
	}
	want := "register --environment production --name web-1 --label empty= --label role=web " +
		"--label topology.example.com/rack=r1 --label zone=a --token-stdin"
	if got := strings.Join(host.recorded()[0].arguments, " "); got != want {
		t.Fatalf("agent arguments = %s, want %s", got, want)
	}
}

func TestTargetsRegisterExplainsAMissingRelease(t *testing.T) {
	cases := []struct {
		name      string
		assets    map[string]string
		extra     []string
		want      []string
		downloads int
	}{
		{name: "no release published", assets: map[string]string{},
			want: []string{"no published ankra-host-agent release was found at", "--agent-version <tag>",
				"Nothing was installed", "/latest/download/SHA256SUMS answered 404"}},
		{name: "pinned tag missing", assets: map[string]string{}, extra: []string{"--agent-version", "2.1.5"},
			want: []string{"ankra-host-agent release v2.1.5 was not found at", "/download/v2.1.5/SHA256SUMS answered 404",
				"omit --agent-version"}},
		{name: "release without this architecture", assets: map[string]string{
			"/latest/download/SHA256SUMS": honestChecksums(),
		}, want: []string{"ankra-host-agent release latest has no ankra-host-agent-linux-amd64", "Nothing was installed"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			release := newFakeAgentRelease(t, honestChecksums())
			release.assets = testCase.assets
			host, binaryPath, unitPath := useFakeHost(t, 0, "linux")
			_, _, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret,
				append(registerArguments(release.url), testCase.extra...)...)
			if exitCodeFor(executeError) != exitNotFound {
				t.Fatalf("a missing release should exit %d, got %v (exit %d)", exitNotFound, executeError, exitCodeFor(executeError))
			}
			for _, want := range testCase.want {
				if !strings.Contains(executeError.Error(), want) {
					t.Fatalf("error misses %q: %v", want, executeError)
				}
			}
			assertNothingInstalled(t, host, binaryPath, unitPath)
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	useFakeHost(t, 0, "linux")
	_, _, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret, registerArguments(server.URL)...)
	if executeError == nil || exitCodeFor(executeError) == exitNotFound || !strings.Contains(executeError.Error(), "unexpected status 502") {
		t.Fatalf("a server error is not a missing release, got %v (exit %d)", executeError, exitCodeFor(executeError))
	}
}

func TestTargetsRegisterAgainRestartsAndNeverFollowsAPlantedSymlink(t *testing.T) {
	release := newFakeAgentRelease(t, honestChecksums())
	host, binaryPath, _ := useFakeHost(t, 0, "linux")
	if directoryError := os.MkdirAll(filepath.Dir(binaryPath), 0o755); directoryError != nil {
		t.Fatal(directoryError)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if writeError := os.WriteFile(victim, []byte("untouched"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	if symlinkError := os.Symlink(victim, binaryPath+".ankra-new"); symlinkError != nil {
		t.Fatal(symlinkError)
	}
	if writeError := os.WriteFile(binaryPath, []byte("old agent"), 0o755); writeError != nil {
		t.Fatal(writeError)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, stderr, executeError := runDeployCommand(t, newTargetsCommand(), testJoinTokenSecret, registerArguments(release.url)...); executeError != nil {
			t.Fatalf("register attempt %d failed: %v\n%s", attempt, executeError, stderr)
		}
	}
	if content, _ := os.ReadFile(victim); string(content) != "untouched" {
		t.Fatalf("the install followed a symlink and overwrote %s: %q", victim, content)
	}
	if content, _ := os.ReadFile(binaryPath); string(content) != fakeHostAgentBinary {
		t.Fatalf("the binary was not replaced: %q", content)
	}
	entries, _ := os.ReadDir(filepath.Dir(binaryPath))
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".ankra-new-") {
			t.Fatalf("a temporary install file was left behind: %s", entry.Name())
		}
	}
	restarts := 0
	for _, invocation := range host.recorded() {
		if invocation.path == "systemctl" && strings.Join(invocation.arguments, " ") == "restart ankra-host-agent.service" {
			restarts++
		}
	}
	if restarts != 2 {
		t.Fatalf("every register must restart the service onto the new binary, got %d restarts", restarts)
	}
}
