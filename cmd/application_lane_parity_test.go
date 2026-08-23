package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type applicationLaneMock struct {
	baseMock
	payload json.RawMessage

	envSecretKey     string
	envSecretValue   string
	envSecretSets    int
	envSecretDeletes int
	envSecretApplies int

	autoDeployEnabled bool
	autoDeploySets    int

	settingsLabel   *string
	settingsUpdates int

	addonID          string
	diffTo           string
	diffFrom         string
	diffPaths        []string
	installRequest   client.InstallManifestAddonRequest
	installCalls     int
	unpublishCalls   int
	addonDeleteCalls int

	fixBuildBranch string
	fixBuildCalls  int
}

func (mock *applicationLaneMock) FixApplicationBuild(requestContext context.Context, applicationID string, branch string) (json.RawMessage, error) {
	mock.fixBuildCalls++
	mock.fixBuildBranch = branch
	return mock.payload, nil
}

func (mock *applicationLaneMock) ListApplicationEnvSecrets(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return mock.payload, nil
}

func (mock *applicationLaneMock) SetApplicationEnvSecret(requestContext context.Context, applicationID string, secretKey string, value string) (json.RawMessage, error) {
	mock.envSecretSets++
	mock.envSecretKey = secretKey
	mock.envSecretValue = value
	return mock.payload, nil
}

func (mock *applicationLaneMock) DeleteApplicationEnvSecret(requestContext context.Context, applicationID string, secretKey string) (json.RawMessage, error) {
	mock.envSecretDeletes++
	mock.envSecretKey = secretKey
	return mock.payload, nil
}

func (mock *applicationLaneMock) ApplyApplicationEnvSecrets(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	mock.envSecretApplies++
	return mock.payload, nil
}

func (mock *applicationLaneMock) GetApplicationAutoDeploy(requestContext context.Context, applicationID string) (json.RawMessage, error) {
	return mock.payload, nil
}

func (mock *applicationLaneMock) SetApplicationAutoDeploy(requestContext context.Context, applicationID string, enabled bool) (json.RawMessage, error) {
	mock.autoDeploySets++
	mock.autoDeployEnabled = enabled
	return mock.payload, nil
}

func (mock *applicationLaneMock) GetApplicationSettings(requestContext context.Context) (json.RawMessage, error) {
	return mock.payload, nil
}

func (mock *applicationLaneMock) UpdateApplicationSettings(requestContext context.Context, ciRunnerLabel *string) (json.RawMessage, error) {
	mock.settingsUpdates++
	mock.settingsLabel = ciRunnerLabel
	return mock.payload, nil
}

func (mock *applicationLaneMock) GetManifestAddon(requestContext context.Context, addonID string) (json.RawMessage, error) {
	mock.addonID = addonID
	return mock.payload, nil
}

func (mock *applicationLaneMock) DiffManifestAddon(requestContext context.Context, addonID string, toVersion string, fromVersion string, paths []string) (json.RawMessage, error) {
	mock.addonID = addonID
	mock.diffTo = toVersion
	mock.diffFrom = fromVersion
	mock.diffPaths = paths
	return mock.payload, nil
}

func (mock *applicationLaneMock) InstallManifestAddon(requestContext context.Context, addonID string, installRequest client.InstallManifestAddonRequest) (json.RawMessage, error) {
	mock.installCalls++
	mock.addonID = addonID
	mock.installRequest = installRequest
	return mock.payload, nil
}

func (mock *applicationLaneMock) UnpublishManifestAddon(requestContext context.Context, addonID string) (json.RawMessage, error) {
	mock.unpublishCalls++
	mock.addonID = addonID
	return mock.payload, nil
}

func (mock *applicationLaneMock) DeleteManifestAddon(requestContext context.Context, addonID string) (json.RawMessage, error) {
	mock.addonDeleteCalls++
	mock.addonID = addonID
	return mock.payload, nil
}

func applicationSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, subcommand := range newApplicationCommand().Commands() {
		if subcommand.Name() == name {
			return subcommand
		}
	}
	t.Fatalf("the %q subcommand is not registered", name)
	return nil
}

func TestApplicationLaneParityCommandsRegistered(t *testing.T) {
	for group, expected := range map[string][]string{
		"env-secrets":    {"list", "set", "delete", "apply"},
		"auto-deploy":    {"get", "set"},
		"settings":       {"get", "set"},
		"manifest-addon": {"get", "diff", "install", "unpublish", "delete"},
	} {
		groupCommand := applicationSubcommand(t, group)
		registered := map[string]bool{}
		for _, subcommand := range groupCommand.Commands() {
			registered[subcommand.Name()] = true
		}
		for _, name := range expected {
			if !registered[name] {
				t.Errorf("%s subcommand %q is not registered", group, name)
			}
		}
	}
}

func TestApplicationEnvSecretSetReadsTheValueFromStdin(t *testing.T) {
	mockClient := &applicationLaneMock{payload: json.RawMessage(`{"key":"API_TOKEN","has_value":true}`)}
	output, executeError := runApplicationCommandWithInput(t, mockClient, "sk-live-abc\n",
		"env-secrets", "set", "app-1", "API_TOKEN")
	if executeError != nil {
		t.Fatalf("env-secrets set error = %v", executeError)
	}
	if mockClient.envSecretSets != 1 {
		t.Fatalf("SetApplicationEnvSecret calls = %d, want 1", mockClient.envSecretSets)
	}
	if mockClient.envSecretKey != "API_TOKEN" {
		t.Errorf("key = %q, want API_TOKEN", mockClient.envSecretKey)
	}
	// The trailing newline of a piped value is stripped; a secret that
	// arrives with one seals a value the application never matches.
	if mockClient.envSecretValue != "sk-live-abc" {
		t.Errorf("value = %q, want the piped value without its newline", mockClient.envSecretValue)
	}
	// Storing is not applying, and a user who stops here changes nothing
	// about the running workload - so the command says so.
	if !strings.Contains(output, "env-secrets apply") {
		t.Errorf("expected the apply hint, got %q", output)
	}
}

// The value must never reach stdout: a set inside a script with -o json would
// otherwise write the secret into whatever captured it.
func TestApplicationEnvSecretSetNeverEchoesTheValue(t *testing.T) {
	mockClient := &applicationLaneMock{payload: json.RawMessage(`{"key":"API_TOKEN","has_value":true}`)}
	output, executeError := runApplicationCommand(t, mockClient,
		"env-secrets", "set", "app-1", "API_TOKEN", "--value", "sk-live-secret")
	if executeError != nil {
		t.Fatalf("env-secrets set error = %v", executeError)
	}
	if mockClient.envSecretValue != "sk-live-secret" {
		t.Errorf("value = %q", mockClient.envSecretValue)
	}
	if strings.Contains(output, "sk-live-secret") {
		t.Errorf("the value was echoed back: %q", output)
	}
}

// An application's environment holds arbitrary values, so the resolver must
// take --value verbatim and strip only the line ending a pipe added. Trimming
// either would store a value the workload never matches, with nothing to see
// afterwards because no route hands a stored value back.
func TestApplicationEnvSecretSetPreservesSignificantWhitespace(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		piped     string
		arguments []string
		want      string
	}{
		{
			name:      "--value is taken verbatim",
			arguments: []string{"--value", "  padded secret  "},
			want:      "  padded secret  ",
		},
		{
			name:  "a piped value loses only its trailing newline",
			piped: "  padded secret  \n",
			want:  "  padded secret  ",
		},
		{
			name:  "a piped CRLF loses both bytes",
			piped: "windows-secret\r\n",
			want:  "windows-secret",
		},
		{
			name:  "an interior newline survives",
			piped: "-----BEGIN KEY-----\nabc\n-----END KEY-----\n",
			want:  "-----BEGIN KEY-----\nabc\n-----END KEY-----",
		},
	} {
		t.Run(testCase.name, func(subTest *testing.T) {
			mockClient := &applicationLaneMock{payload: json.RawMessage(`{"key":"K"}`)}
			arguments := append([]string{"env-secrets", "set", "app-1", "K"}, testCase.arguments...)
			_, executeError := runApplicationCommandWithInput(subTest, mockClient, testCase.piped, arguments...)
			if executeError != nil {
				subTest.Fatalf("env-secrets set error = %v", executeError)
			}
			if mockClient.envSecretValue != testCase.want {
				subTest.Errorf("value = %q, want %q", mockClient.envSecretValue, testCase.want)
			}
		})
	}
}

func TestApplicationEnvSecretSetRequiresAValue(t *testing.T) {
	mockClient := &applicationLaneMock{}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "", "env-secrets", "set", "app-1", "K")
	if executeError == nil {
		t.Fatal("expected an empty value to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.envSecretSets != 0 {
		t.Errorf("SetApplicationEnvSecret calls = %d, want 0", mockClient.envSecretSets)
	}
}

func TestApplicationEnvSecretDeleteRefusesWhenDeclined(t *testing.T) {
	mockClient := &applicationLaneMock{}
	_, executeError := runApplicationCommandWithInput(t, mockClient, "n\n",
		"env-secrets", "delete", "app-1", "API_TOKEN")
	if executeError == nil {
		t.Fatal("a declined confirmation must fail")
	}
	if exitCodeFor(executeError) != exitCancelled {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitCancelled)
	}
	if mockClient.envSecretDeletes != 0 {
		t.Errorf("DeleteApplicationEnvSecret calls = %d, want 0", mockClient.envSecretDeletes)
	}
}

func TestApplicationEnvSecretApplySendsTheApply(t *testing.T) {
	mockClient := &applicationLaneMock{payload: json.RawMessage(`{"applied_count":2,"failed_count":0}`)}
	output, executeError := runApplicationCommand(t, mockClient, "env-secrets", "apply", "app-1")
	if executeError != nil {
		t.Fatalf("env-secrets apply error = %v", executeError)
	}
	if mockClient.envSecretApplies != 1 {
		t.Fatalf("ApplyApplicationEnvSecrets calls = %d, want 1", mockClient.envSecretApplies)
	}
	if !strings.Contains(output, "\"applied_count\": 2") {
		t.Errorf("output is not the rendered payload: %q", output)
	}
}

// Turning unattended deployment on and turning it off are both deliberate, so
// a bare `set` is a usage error rather than defaulting to either.
func TestApplicationAutoDeploySetRequiresTheFlag(t *testing.T) {
	mockClient := &applicationLaneMock{}
	_, executeError := runApplicationCommand(t, mockClient, "auto-deploy", "set", "app-1")
	if executeError == nil {
		t.Fatal("expected a bare set to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.autoDeploySets != 0 {
		t.Errorf("SetApplicationAutoDeploy calls = %d, want 0", mockClient.autoDeploySets)
	}
}

func TestApplicationAutoDeploySetCarriesBothStates(t *testing.T) {
	for _, testCase := range []struct {
		argument string
		want     bool
	}{
		{"--enabled", true},
		{"--enabled=false", false},
	} {
		mockClient := &applicationLaneMock{payload: json.RawMessage(`{"enabled":true}`)}
		_, executeError := runApplicationCommand(t, mockClient, "auto-deploy", "set", "app-1", testCase.argument)
		if executeError != nil {
			t.Fatalf("auto-deploy set %s error = %v", testCase.argument, executeError)
		}
		if mockClient.autoDeploySets != 1 {
			t.Fatalf("SetApplicationAutoDeploy calls = %d, want 1", mockClient.autoDeploySets)
		}
		if mockClient.autoDeployEnabled != testCase.want {
			t.Errorf("%s sent enabled = %v, want %v", testCase.argument, mockClient.autoDeployEnabled, testCase.want)
		}
	}
}

// Clearing sends a nil label, which the client marshals as an explicit null;
// naming a label sends the label. Asking for both at once is a usage error
// rather than one silently winning.
func TestApplicationSettingsSetDistinguishesClearFromALabel(t *testing.T) {
	mockClient := &applicationLaneMock{payload: json.RawMessage(`{"ci_runner_label":null}`)}
	if _, executeError := runApplicationCommand(t, mockClient, "settings", "set", "--clear"); executeError != nil {
		t.Fatalf("settings set --clear error = %v", executeError)
	}
	if mockClient.settingsUpdates != 1 || mockClient.settingsLabel != nil {
		t.Errorf("clear sent label = %v after %d updates", mockClient.settingsLabel, mockClient.settingsUpdates)
	}

	mockClient = &applicationLaneMock{payload: json.RawMessage(`{"ci_runner_label":"self-hosted"}`)}
	if _, executeError := runApplicationCommand(t, mockClient,
		"settings", "set", "--ci-runner-label", " self-hosted "); executeError != nil {
		t.Fatalf("settings set error = %v", executeError)
	}
	if mockClient.settingsLabel == nil || *mockClient.settingsLabel != "self-hosted" {
		t.Errorf("label = %v, want it trimmed to self-hosted", mockClient.settingsLabel)
	}

	mockClient = &applicationLaneMock{}
	_, executeError := runApplicationCommand(t, mockClient,
		"settings", "set", "--clear", "--ci-runner-label", "self-hosted")
	if executeError == nil {
		t.Fatal("--clear with --ci-runner-label must fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.settingsUpdates != 0 {
		t.Errorf("UpdateApplicationSettings calls = %d, want 0", mockClient.settingsUpdates)
	}
}

func TestApplicationSettingsSetRequiresAChoice(t *testing.T) {
	mockClient := &applicationLaneMock{}
	_, executeError := runApplicationCommand(t, mockClient, "settings", "set")
	if executeError == nil {
		t.Fatal("expected a bare settings set to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
}

func TestManifestAddonDiffRequiresTo(t *testing.T) {
	mockClient := &applicationLaneMock{}
	_, executeError := runApplicationCommand(t, mockClient, "manifest-addon", "diff", "addon-1")
	if executeError == nil {
		t.Fatal("expected a missing --to to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.diffTo != "" {
		t.Errorf("DiffManifestAddon must not be called, got to = %q", mockClient.diffTo)
	}
}

func TestManifestAddonDiffPassesEveryRepeatedPath(t *testing.T) {
	mockClient := &applicationLaneMock{payload: json.RawMessage(`{"changes":[]}`)}
	_, executeError := runApplicationCommand(t, mockClient, "manifest-addon", "diff", "addon-1",
		"--to", "1.4.0", "--from", "1.2.0", "--path", "deployment.yaml", "--path", "service.yaml")
	if executeError != nil {
		t.Fatalf("manifest-addon diff error = %v", executeError)
	}
	if mockClient.diffTo != "1.4.0" || mockClient.diffFrom != "1.2.0" {
		t.Errorf("to = %q from = %q", mockClient.diffTo, mockClient.diffFrom)
	}
	if len(mockClient.diffPaths) != 2 {
		t.Errorf("paths = %v, want both", mockClient.diffPaths)
	}
}

func TestManifestAddonInstallRequiresACluster(t *testing.T) {
	mockClient := &applicationLaneMock{}
	_, executeError := runApplicationCommand(t, mockClient, "manifest-addon", "install", "addon-1")
	if executeError == nil {
		t.Fatal("expected a missing --cluster-id to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.installCalls != 0 {
		t.Errorf("InstallManifestAddon calls = %d, want 0", mockClient.installCalls)
	}
}

func TestManifestAddonInstallParsesInputs(t *testing.T) {
	mockClient := &applicationLaneMock{payload: json.RawMessage(`{"installed":true}`)}
	_, executeError := runApplicationCommand(t, mockClient, "manifest-addon", "install", "addon-1",
		"--cluster-id", "cluster-1", "--namespace", "commerce", "--version", "1.4.0",
		"--input", "replicas=3", "--input", "dsn=postgres://user@host/db?sslmode=require")
	if executeError != nil {
		t.Fatalf("manifest-addon install error = %v", executeError)
	}
	if mockClient.installRequest.ClusterID != "cluster-1" ||
		mockClient.installRequest.Namespace != "commerce" ||
		mockClient.installRequest.Version != "1.4.0" {
		t.Errorf("install request = %+v", mockClient.installRequest)
	}
	if mockClient.installRequest.Inputs["replicas"] != "3" {
		t.Errorf("replicas = %q", mockClient.installRequest.Inputs["replicas"])
	}
	// Only the first '=' separates, so a value that contains one survives.
	if mockClient.installRequest.Inputs["dsn"] != "postgres://user@host/db?sslmode=require" {
		t.Errorf("dsn = %q, want the whole value", mockClient.installRequest.Inputs["dsn"])
	}
}

// A key silently answered with "" is worse than a refusal: the add-on installs
// with an input nobody meant to set.
func TestManifestAddonInstallRejectsAnInputWithoutAValue(t *testing.T) {
	mockClient := &applicationLaneMock{}
	_, executeError := runApplicationCommand(t, mockClient, "manifest-addon", "install", "addon-1",
		"--cluster-id", "cluster-1", "--input", "replicas")
	if executeError == nil {
		t.Fatal("expected --input without '=' to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.installCalls != 0 {
		t.Errorf("InstallManifestAddon calls = %d, want 0", mockClient.installCalls)
	}
}

// Unpublish withdraws the catalog entry and leaves installations running;
// delete undeploys them. Both confirm, and each says which one it is.
func TestManifestAddonWithdrawalsBothConfirm(t *testing.T) {
	mockClient := &applicationLaneMock{}
	output, executeError := runApplicationCommandWithInput(t, mockClient, "n\n",
		"manifest-addon", "unpublish", "addon-1")
	if executeError == nil || exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("declined unpublish error = %v", executeError)
	}
	if !strings.Contains(output, "keep running") {
		t.Errorf("the unpublish prompt must say installations survive: %q", output)
	}
	if mockClient.unpublishCalls != 0 {
		t.Errorf("UnpublishManifestAddon calls = %d, want 0", mockClient.unpublishCalls)
	}

	mockClient = &applicationLaneMock{}
	output, executeError = runApplicationCommandWithInput(t, mockClient, "n\n",
		"manifest-addon", "delete", "addon-1")
	if executeError == nil || exitCodeFor(executeError) != exitCancelled {
		t.Fatalf("declined delete error = %v", executeError)
	}
	if !strings.Contains(output, "undeploys every installation") {
		t.Errorf("the delete prompt must say installations are undeployed: %q", output)
	}
	if mockClient.addonDeleteCalls != 0 {
		t.Errorf("DeleteManifestAddon calls = %d, want 0", mockClient.addonDeleteCalls)
	}
}

// fix-build is the remedy for the answer `demo build` gives, and the CLI had
// the check without the remedy: the bearer twin for it landed in cluster#1717
// (ankra-961wq). --branch is required because the lane repairs one branch's
// build, and a defaulted branch would repair the wrong one silently.
func TestApplicationDemoFixBuildRequiresABranch(t *testing.T) {
	mockClient := &applicationLaneMock{}
	_, executeError := runApplicationCommand(t, mockClient, "demo", "fix-build", "app-1")
	if executeError == nil {
		t.Fatal("expected a missing --branch to fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.fixBuildCalls != 0 {
		t.Errorf("FixApplicationBuild calls = %d, want 0", mockClient.fixBuildCalls)
	}
}

func TestApplicationDemoFixBuildSendsTheBranch(t *testing.T) {
	mockClient := &applicationLaneMock{payload: json.RawMessage(`{"status":"dispatched"}`)}
	_, executeError := runApplicationCommand(t, mockClient,
		"demo", "fix-build", "app-1", "--branch", " feature/checkout ")
	if executeError != nil {
		t.Fatalf("demo fix-build error = %v", executeError)
	}
	if mockClient.fixBuildCalls != 1 {
		t.Fatalf("FixApplicationBuild calls = %d, want 1", mockClient.fixBuildCalls)
	}
	if mockClient.fixBuildBranch != "feature/checkout" {
		t.Errorf("branch = %q, want it trimmed", mockClient.fixBuildBranch)
	}
}

// An explicitly empty --value is refused so the flag path agrees with the
// stdin and prompt paths. The script footgun is `--value "$UNSET_VAR"`:
// without this it stores a value the workload never matches, and no route
// hands a stored value back to show it afterwards.
func TestApplicationEnvSecretSetRefusesAnExplicitlyEmptyValue(t *testing.T) {
	for _, argument := range []string{"--value=", "--value"} {
		mockClient := &applicationLaneMock{}
		arguments := []string{"env-secrets", "set", "app-1", "K", argument}
		if argument == "--value" {
			arguments = append(arguments, "")
		}
		_, executeError := runApplicationCommand(t, mockClient, arguments...)
		if executeError == nil {
			t.Fatalf("%s: an empty value must be refused", argument)
		}
		if exitCodeFor(executeError) != exitUsage {
			t.Errorf("%s: exit code = %d, want %d", argument, exitCodeFor(executeError), exitUsage)
		}
		if !strings.Contains(executeError.Error(), "env-secrets delete") {
			t.Errorf("%s: the refusal should point at the tool that clears a value: %v", argument, executeError)
		}
		if mockClient.envSecretSets != 0 {
			t.Errorf("%s: SetApplicationEnvSecret calls = %d, want 0", argument, mockClient.envSecretSets)
		}
	}
}

// url.PathEscape does not escape dot segments, so a key of ".." would build
// ".../env-secrets/.." and a server or proxy that normalises request paths
// could resolve it onto the application resource - which on DELETE is the
// delete-application route. The key is validated to the platform's own
// environment-variable rule before it reaches a path.
func TestApplicationEnvSecretRefusesAKeyThatIsNotAnEnvironmentVariableName(t *testing.T) {
	for _, secretKey := range []string{"..", ".", "A/B", "1LEADING_DIGIT", "has space", "has-dash", ""} {
		for _, verb := range []string{"set", "delete"} {
			mockClient := &applicationLaneMock{}
			arguments := []string{"env-secrets", verb, "app-1", secretKey}
			if verb == "set" {
				arguments = append(arguments, "--value", "v")
			} else {
				arguments = append(arguments, "--yes")
			}
			_, executeError := runApplicationCommand(t, mockClient, arguments...)
			if executeError == nil {
				t.Errorf("%s %q was accepted", verb, secretKey)
				continue
			}
			if exitCodeFor(executeError) != exitUsage {
				t.Errorf("%s %q: exit code = %d, want %d", verb, secretKey, exitCodeFor(executeError), exitUsage)
			}
			if mockClient.envSecretSets != 0 || mockClient.envSecretDeletes != 0 {
				t.Errorf("%s %q reached the client", verb, secretKey)
			}
		}
	}
	// The rule must still accept an ordinary key.
	mockClient := &applicationLaneMock{payload: json.RawMessage(`{"key":"DATABASE_URL"}`)}
	if _, executeError := runApplicationCommand(t, mockClient,
		"env-secrets", "set", "app-1", "DATABASE_URL", "--value", "v"); executeError != nil {
		t.Fatalf("a valid key was refused: %v", executeError)
	}
	if mockClient.envSecretSets != 1 {
		t.Errorf("SetApplicationEnvSecret calls = %d, want 1", mockClient.envSecretSets)
	}
}

// There is one value per input, so last-one-wins can never be what someone
// meant: the usual cause is a variable expanded twice in a script, and
// installing with the second value silently hides it.
func TestManifestAddonInstallRejectsADuplicateInputKey(t *testing.T) {
	mockClient := &applicationLaneMock{}
	_, executeError := runApplicationCommand(t, mockClient, "manifest-addon", "install", "addon-1",
		"--cluster-id", "cluster-1", "--input", "replicas=2", "--input", "replicas=3")
	if executeError == nil {
		t.Fatal("a repeated --input key must be refused")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitUsage)
	}
	if !strings.Contains(executeError.Error(), "replicas") {
		t.Errorf("the refusal should name the duplicated key: %v", executeError)
	}
	if mockClient.installCalls != 0 {
		t.Errorf("InstallManifestAddon calls = %d, want 0", mockClient.installCalls)
	}
}
