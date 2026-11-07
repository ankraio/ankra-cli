package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type aiKeysMock struct {
	baseMock
	listing      *client.PersonalAIKeyListing
	added        *client.AddPersonalAIKeyRequest
	addCalls     int
	addError     error
	reorderedIDs []string
	reorderCalls int
	renamedID    string
	renamedLabel string
	renameCalls  int
	deletedID    string
	deleteCalls  int
	testedID     string
	testResult   *client.PersonalAIKeyTestResult
	policy       client.PersonalAIKeysPolicy
	setAllowed   []bool
}

func (m *aiKeysMock) ListPersonalAIKeys() (*client.PersonalAIKeyListing, error) {
	if m.listing == nil {
		return &client.PersonalAIKeyListing{OrganisationPolicy: client.PersonalAIKeysPolicyAllowed}, nil
	}
	return m.listing, nil
}

func (m *aiKeysMock) AddPersonalAIKey(request client.AddPersonalAIKeyRequest) (*client.PersonalAIKey, error) {
	m.addCalls++
	m.added = &request
	if m.addError != nil {
		return nil, m.addError
	}
	preview := "sk-ant-...CRET"
	if request.Provider == "openrouter" {
		preview = "sk-or-...CRET"
	}
	return &client.PersonalAIKey{ID: "new-key", Provider: request.Provider, Label: request.Label,
		KeyPreview: preview, Position: 1, Status: client.PersonalAIKeyStatusActive}, nil
}

func (m *aiKeysMock) ReorderPersonalAIKeys(keyIDs []string) (*client.PersonalAIKeyList, error) {
	m.reorderCalls++
	m.reorderedIDs = keyIDs
	byID := map[string]client.PersonalAIKey{}
	for _, key := range m.listing.Keys {
		byID[key.ID] = key
	}
	list := &client.PersonalAIKeyList{}
	for position, keyID := range keyIDs {
		key := byID[keyID]
		key.Position = position
		list.Keys = append(list.Keys, key)
	}
	return list, nil
}

func (m *aiKeysMock) RenamePersonalAIKey(keyID string, label string) (*client.PersonalAIKey, error) {
	m.renameCalls++
	m.renamedID, m.renamedLabel = keyID, label
	return &client.PersonalAIKey{ID: keyID, Provider: "anthropic", Label: label, KeyPreview: "sk-ant-...1111"}, nil
}

func (m *aiKeysMock) DeletePersonalAIKey(keyID string) error {
	m.deleteCalls++
	m.deletedID = keyID
	return nil
}

func (m *aiKeysMock) TestPersonalAIKey(keyID string) (*client.PersonalAIKeyTestResult, error) {
	m.testedID = keyID
	return m.testResult, nil
}

func (m *aiKeysMock) GetPersonalAIKeysPolicy() (*client.PersonalAIKeysPolicy, error) {
	return &client.PersonalAIKeysPolicy{Allowed: m.policy.Allowed}, nil
}

func (m *aiKeysMock) SetPersonalAIKeysPolicy(allowed bool) (*client.PersonalAIKeysPolicy, error) {
	m.setAllowed = append(m.setAllowed, allowed)
	return &client.PersonalAIKeysPolicy{Allowed: allowed}, nil
}

// threeKeys is a profile with an active labelled key, an unlabelled key that
// is out of credit for a while, and a rejected key.
func threeKeys(policy string) *client.PersonalAIKeyListing {
	retryAfter := time.Now().Add(45 * time.Minute).UTC().Format(time.RFC3339)
	lastUsed := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	return &client.PersonalAIKeyListing{
		OrganisationPolicy: policy,
		Keys: []client.PersonalAIKey{
			{ID: "key-a", Provider: "anthropic", Label: "work", KeyPreview: "sk-ant-...1111", Position: 0,
				Status: client.PersonalAIKeyStatusActive, LastUsedAt: &lastUsed},
			{ID: "key-b", Provider: "openrouter", Label: "", KeyPreview: "sk-or-...2222", Position: 1,
				Status: client.PersonalAIKeyStatusOutOfCredit, RetryAfter: &retryAfter},
			{ID: "key-c", Provider: "anthropic", Label: "personal", KeyPreview: "sk-ant-...3333", Position: 2,
				Status: client.PersonalAIKeyStatusRejected},
		},
	}
}

// resetAIKeysCommandState puts every flag of the ai keys commands back to its
// default and stdin back to the process's own, before and after the test:
// the commands are package-level, so a flag one test set is otherwise still
// set in the next.
func resetAIKeysCommandState(t *testing.T) {
	t.Helper()
	reset := func() {
		var visit func(command *cobra.Command)
		visit = func(command *cobra.Command) {
			command.LocalFlags().VisitAll(func(flag *pflag.Flag) {
				_ = flag.Value.Set(flag.DefValue)
				flag.Changed = false
			})
			for _, child := range command.Commands() {
				visit(child)
			}
		}
		visit(aiKeysCmd)
		rootCmd.SetIn(nil)
	}
	reset()
	t.Cleanup(reset)
}

// runAIKeysSplit runs `ankra ai keys <args>` with stdin and returns what it
// wrote to stdout and to stderr, apart, and its error.
func runAIKeysSplit(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	var stdoutBuffer, stderrBuffer bytes.Buffer
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetOut(&stdoutBuffer)
	rootCmd.SetErr(&stderrBuffer)
	rootCmd.SetArgs(append([]string{"ai", "keys"}, args...))
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	var executeError error
	processStdout := captureStdout(t, func() {
		executeError = rootCmd.Execute()
	})
	return stdoutBuffer.String() + processStdout, stderrBuffer.String(), executeError
}

// runAIKeys is runAIKeysSplit with stdout and stderr together.
func runAIKeys(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	stdout, stderr, executeError := runAIKeysSplit(t, stdin, args...)
	return stdout + stderr, executeError
}

func TestAIKeysListShowsEachKeyAndWhetherTheOrganisationUsesThem(t *testing.T) {
	resetAIKeysCommandState(t)
	setMockClient(t, &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicyAllowed)})

	output, executeError := runAIKeys(t, "", "list")
	if executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	for _, expected := range []string{
		"work", "Anthropic", "sk-ant-...1111", "active", "2 hours ago", "key-a",
		"OpenRouter", "sk-or-...2222", "out of credit, tried again", "from now", "never",
		"rejected by the provider", "Your organisation runs the chats and pull request reviews you start on these keys",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in output, got:\n%s", expected, output)
		}
	}
	if strings.Index(output, "sk-ant-...1111") > strings.Index(output, "sk-ant-...3333") {
		t.Errorf("keys should be listed in the order they are tried, got:\n%s", output)
	}
}

func TestAIKeysListSaysKeysAreNotInUseWhenAnAdministratorTurnedThemOff(t *testing.T) {
	resetAIKeysCommandState(t)
	setMockClient(t, &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicyDisabled)})

	output, executeError := runAIKeys(t, "", "list")
	if executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	if !strings.Contains(output, "not in use") || !strings.Contains(output, "An administrator turned members' own AI keys off") {
		t.Errorf("expected the keys shown as not in use, got:\n%s", output)
	}
	if strings.Contains(output, "rejected by the provider") {
		t.Errorf("a key the organisation does not use should read as not in use, got:\n%s", output)
	}
}

func TestAIKeysListWithNoKeysSaysWhereToAddOne(t *testing.T) {
	resetAIKeysCommandState(t)
	setMockClient(t, &aiKeysMock{})

	output, executeError := runAIKeys(t, "", "list")
	if executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	for _, expected := range []string{"You have no AI keys", "ankra ai keys add", "Profile, Connected accounts, Your AI keys"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in output, got:\n%s", expected, output)
		}
	}
}

func TestAIKeysListJSONPrintsTheListing(t *testing.T) {
	resetAIKeysCommandState(t)
	setMockClient(t, &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicySelfHosted)})

	output, stderr, executeError := runAIKeysSplit(t, "", "list", "-o", "json")
	if executeError != nil || stderr != "" {
		t.Fatalf("unexpected error: %v\n%s%s", executeError, output, stderr)
	}
	var decoded struct {
		Keys               []map[string]any `json:"keys"`
		OrganisationPolicy string           `json:"organisation_policy"`
	}
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("-o json output is not one JSON document: %v\n%s", decodeError, output)
	}
	if decoded.OrganisationPolicy != "self_hosted" || len(decoded.Keys) != 3 || decoded.Keys[1]["status"] != "out_of_credit" {
		t.Errorf("decoded = %+v", decoded)
	}
}

// The key is never a flag: one would land in shell history and the process
// list. Only a switch saying where to read it from exists.
func TestAIKeysAddHasNoFlagThatTakesTheKey(t *testing.T) {
	aiKeysAddCmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Value.Type() == "bool" {
			return
		}
		switch flag.Name {
		case "provider", "label", "output":
		default:
			t.Errorf("add has a %s flag --%s; the key must never be a flag value", flag.Value.Type(), flag.Name)
		}
	})
	if aiKeysAddCmd.Flags().Lookup("key-stdin") == nil {
		t.Error("add should read a piped key with --key-stdin")
	}
}

func TestAIKeysAddReadsThePipedKeyAndNeverPrintsIt(t *testing.T) {
	const rawKey = "sk-ant-api03-PIPED-SECRET"
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{}
	setMockClient(t, mock)

	output, executeError := runAIKeys(t, rawKey+"\n", "add", "--key-stdin", "--label", "work")
	if executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	if mock.added == nil || mock.added.APIKey != rawKey || mock.added.Provider != "anthropic" || mock.added.Label != "work" {
		t.Fatalf("AddPersonalAIKey(%+v), want the piped key as an Anthropic key labelled work", mock.added)
	}
	if strings.Contains(output, rawKey) || strings.Contains(output, "PIPED") {
		t.Errorf("the key must never be printed, got:\n%s", output)
	}
	if !strings.Contains(output, "sk-ant-...CRET") || !strings.Contains(output, "number 2") {
		t.Errorf("expected the masked preview and the key's place, got:\n%s", output)
	}
}

func TestAIKeysAddJSONPrintsOnlyWhatThePlatformReturned(t *testing.T) {
	const rawKey = "sk-or-v1-JSON-SECRET"
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{}
	setMockClient(t, mock)

	output, stderr, executeError := runAIKeysSplit(t, rawKey+"\n", "add", "--key-stdin", "-o", "json")
	if executeError != nil || strings.Contains(stderr, rawKey) {
		t.Fatalf("unexpected error: %v\n%s%s", executeError, output, stderr)
	}
	if mock.added == nil || mock.added.Provider != "openrouter" {
		t.Fatalf("AddPersonalAIKey(%+v), want the provider worked out as openrouter", mock.added)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("-o json output is not one JSON document: %v\n%s", decodeError, output)
	}
	if decoded["key_preview"] != "sk-or-...CRET" || strings.Contains(output, rawKey) {
		t.Errorf("-o json should print the masked key only, got:\n%s", output)
	}
}

func TestAIKeysAddAsksForTheKeyWithHiddenInputOnATerminal(t *testing.T) {
	const rawKey = "sk-ant-api03-TYPED-SECRET"
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{}
	setMockClient(t, mock)
	originalTerminal, originalPrompt := personalAIKeyTerminal, readHiddenPersonalAIKey
	t.Cleanup(func() { personalAIKeyTerminal, readHiddenPersonalAIKey = originalTerminal, originalPrompt })
	personalAIKeyTerminal = func(io.Reader) (int, bool) { return 7, true }
	prompts := 0
	readHiddenPersonalAIKey = func(descriptor int, prompt io.Writer, label string) (string, error) {
		prompts++
		if descriptor != 7 {
			t.Errorf("prompted on descriptor %d, want the terminal's", descriptor)
		}
		_, _ = io.WriteString(prompt, label+": ")
		return rawKey, nil
	}

	output, executeError := runAIKeys(t, "", "add")
	if executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	if prompts != 1 || mock.added == nil || mock.added.APIKey != rawKey {
		t.Errorf("prompts = %d, added = %+v; want one hidden prompt and its key sent", prompts, mock.added)
	}
	if !strings.Contains(output, "Paste a Claude Console API key (it starts with sk-ant-) or an OpenRouter API key (it starts with sk-or-)") {
		t.Errorf("the prompt should name both kinds of key by their prefix, got:\n%s", output)
	}
	if strings.Contains(output, rawKey) {
		t.Errorf("the key must never be printed, got:\n%s", output)
	}
}

// The Claude Console issues more than sk-ant-api keys: a personal key tied to
// the member's identity starts with sk-ant-usr-. Every sk-ant- API key is an
// Anthropic key; only subscription and admin keys are refused.
func TestAIKeysAddTakesEveryClaudeConsoleAPIKeyAsAnthropic(t *testing.T) {
	for _, rawKey := range []string{"sk-ant-api03-CONSOLE-SECRET", "sk-ant-usr01-PERSONAL-SECRET"} {
		t.Run(rawKey[:10], func(t *testing.T) {
			resetAIKeysCommandState(t)
			mock := &aiKeysMock{}
			setMockClient(t, mock)

			output, executeError := runAIKeys(t, rawKey+"\n", "add", "--key-stdin")
			if executeError != nil {
				t.Fatalf("unexpected error: %v\n%s", executeError, output)
			}
			if mock.added == nil || mock.added.APIKey != rawKey || mock.added.Provider != "anthropic" {
				t.Fatalf("AddPersonalAIKey(%+v), want the key sent as an Anthropic key", mock.added)
			}
			if strings.Contains(output, "SECRET") {
				t.Errorf("the key must never be printed, got:\n%s", output)
			}
		})
	}
}

func TestAIKeysAddRefusesKeyStdinOnATerminal(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{}
	setMockClient(t, mock)
	originalTerminal := personalAIKeyTerminal
	t.Cleanup(func() { personalAIKeyTerminal = originalTerminal })
	personalAIKeyTerminal = func(io.Reader) (int, bool) { return 7, true }

	_, executeError := runAIKeys(t, "sk-ant-api03-x\n", "add", "--key-stdin")
	if executeError == nil || exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "input hidden") {
		t.Errorf("error = %v, want a usage error pointing at the hidden prompt", executeError)
	}
	if mock.addCalls != 0 {
		t.Errorf("AddPersonalAIKey called %d times", mock.addCalls)
	}
}

func TestAIKeysAddWithoutATerminalOrKeyStdinIsAUsageError(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{}
	setMockClient(t, mock)

	_, executeError := runAIKeys(t, "sk-ant-api03-NOT-READ\n", "add")
	if executeError == nil || exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "--key-stdin") {
		t.Errorf("error = %v, want a usage error naming --key-stdin", executeError)
	}
	if mock.addCalls != 0 {
		t.Errorf("AddPersonalAIKey called %d times", mock.addCalls)
	}
}

// What Ankra will never store is refused before anything leaves the machine,
// and the refusal names the kind of key, never the key.
func TestAIKeysAddRefusesWhatIsNotAnAPIKeyBeforeSending(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		rawKey   string
		flags    []string
		expected string
	}{
		{"subscription token", "sk-ant-oat01-SUBSCRIPTION-SECRET", nil, "Claude subscription token"},
		{"subscription refresh token", "sk-ant-ort01-SUBSCRIPTION-SECRET", nil, "Claude subscription token"},
		{"admin key", "sk-ant-admin01-ADMIN-SECRET", nil, "admin key"},
		{"no known provider", "AKIA-NOT-A-MODEL-SECRET", nil, "does not look like a Claude Console API key (it starts with sk-ant-)"},
		{"provider that does not match", "sk-or-v1-MISMATCH-SECRET", []string{"--provider", "anthropic"}, "is an OpenRouter key"},
		{"unknown provider flag", "sk-ant-api03-FLAG-SECRET", []string{"--provider", "openai"}, "anthropic or openrouter"},
		{"two words", "sk-ant-api03-ONE SECOND-SECRET", nil, "holds a space"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetAIKeysCommandState(t)
			mock := &aiKeysMock{}
			setMockClient(t, mock)

			output, executeError := runAIKeys(t, testCase.rawKey+"\n", append([]string{"add", "--key-stdin"}, testCase.flags...)...)
			if executeError == nil || exitCodeFor(executeError) != exitUsage {
				t.Fatalf("error = %v (exit %d), want a usage error", executeError, exitCodeFor(executeError))
			}
			if !strings.Contains(executeError.Error(), testCase.expected) {
				t.Errorf("error = %q, want it to say %q", executeError.Error(), testCase.expected)
			}
			if mock.addCalls != 0 {
				t.Errorf("AddPersonalAIKey called %d times; nothing should be sent", mock.addCalls)
			}
			if strings.Contains(executeError.Error(), "SECRET") || strings.Contains(output, "SECRET") {
				t.Errorf("the key must never be printed, got error %q and output:\n%s", executeError.Error(), output)
			}
		})
	}
}

func TestAIKeysAddRefusesALabelOverTheLimitBeforeAskingForTheKey(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{}
	setMockClient(t, mock)

	_, executeError := runAIKeys(t, "sk-ant-api03-x\n", "add", "--key-stdin", "--label", strings.Repeat("l", 101))
	if executeError == nil || exitCodeFor(executeError) != exitUsage || mock.addCalls != 0 {
		t.Errorf("error = %v, add calls %d; want a usage error and nothing sent", executeError, mock.addCalls)
	}
}

// Whatever the platform (or a proxy in between) answers, the key cannot reach
// the terminal through the error, and the exit code still follows the status.
func TestAIKeysAddPlatformRefusalNeverEchoesTheKey(t *testing.T) {
	const rawKey = "sk-ant-api03-ECHOED-SECRET"
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{addError: client.NewUnexpectedResponseError(409,
		"This key is already on your profile: "+rawKey)}
	setMockClient(t, mock)

	output, executeError := runAIKeys(t, rawKey+"\n", "add", "--key-stdin")
	if executeError == nil {
		t.Fatal("expected the platform refusal as an error")
	}
	if strings.Contains(executeError.Error(), rawKey) || strings.Contains(output, rawKey) {
		t.Errorf("the key must never be printed, got error %q and output:\n%s", executeError.Error(), output)
	}
	if !strings.Contains(executeError.Error(), "This key is already on your profile") {
		t.Errorf("error = %q, want the platform's reason", executeError.Error())
	}
	var unexpected *client.UnexpectedResponseError
	if !errors.As(executeError, &unexpected) || exitCodeFor(executeError) != exitError {
		t.Errorf("error = %#v (exit %d), want the platform error kept for the exit code", executeError, exitCodeFor(executeError))
	}
}

func TestAIKeysOrderSendsEveryKeyIDWithTheNamedKeysFirst(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicyAllowed)}
	setMockClient(t, mock)

	output, executeError := runAIKeys(t, "", "order", "personal", "key-b")
	if executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	if strings.Join(mock.reorderedIDs, ",") != "key-c,key-b,key-a" {
		t.Errorf("ReorderPersonalAIKeys(%v), want [key-c key-b key-a]: the named keys first, then the rest", mock.reorderedIDs)
	}
	if !strings.Contains(output, "now tried in this order") ||
		strings.Index(output, "sk-ant-...3333") > strings.Index(output, "sk-ant-...1111") {
		t.Errorf("expected the new order printed, got:\n%s", output)
	}
}

func TestAIKeysOrderFillsInTheKeysLeftOut(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicyAllowed)}
	setMockClient(t, mock)

	if output, executeError := runAIKeys(t, "", "order", "PERSONAL"); executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	if strings.Join(mock.reorderedIDs, ",") != "key-c,key-a,key-b" {
		t.Errorf("ReorderPersonalAIKeys(%v), want every key: [key-c key-a key-b]", mock.reorderedIDs)
	}
}

func TestAIKeysOrderRefusesAKeyNamedTwice(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicyAllowed)}
	setMockClient(t, mock)

	_, executeError := runAIKeys(t, "", "order", "work", "key-a")
	if executeError == nil || exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "same key") {
		t.Errorf("error = %v, want a usage error saying the same key was named twice", executeError)
	}
	if mock.reorderCalls != 0 {
		t.Errorf("ReorderPersonalAIKeys called %d times", mock.reorderCalls)
	}
}

func TestAIKeysOrderRefusesAnUnknownKey(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicyAllowed)}
	setMockClient(t, mock)

	_, executeError := runAIKeys(t, "", "order", "work", "holiday")
	if executeError == nil || exitCodeFor(executeError) != exitNotFound || !strings.Contains(executeError.Error(), `"holiday"`) {
		t.Errorf("error = %v, want a not-found error naming holiday", executeError)
	}
	if mock.reorderCalls != 0 {
		t.Errorf("ReorderPersonalAIKeys called %d times", mock.reorderCalls)
	}
}

func TestAIKeysRenameFindsTheKeyByLabel(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicyAllowed)}
	setMockClient(t, mock)

	output, executeError := runAIKeys(t, "", "rename", "work", "Personal Max plan")
	if executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	if mock.renamedID != "key-a" || mock.renamedLabel != "Personal Max plan" {
		t.Errorf("RenamePersonalAIKey(%q, %q), want key-a renamed", mock.renamedID, mock.renamedLabel)
	}
	if !strings.Contains(output, `"Personal Max plan"`) {
		t.Errorf("expected the new label in output, got:\n%s", output)
	}
}

func TestAIKeysRenameRefusesALabelTwoKeysShare(t *testing.T) {
	resetAIKeysCommandState(t)
	listing := threeKeys(client.PersonalAIKeysPolicyAllowed)
	listing.Keys[2].Label = "Work"
	mock := &aiKeysMock{listing: listing}
	setMockClient(t, mock)

	_, executeError := runAIKeys(t, "", "rename", "work", "new")
	if executeError == nil || exitCodeFor(executeError) != exitUsage ||
		!strings.Contains(executeError.Error(), "key-a") || !strings.Contains(executeError.Error(), "key-c") {
		t.Errorf("error = %v, want a usage error listing both ids", executeError)
	}
	if mock.renameCalls != 0 {
		t.Errorf("RenamePersonalAIKey called %d times", mock.renameCalls)
	}
}

func TestAIKeysRemoveAsksFirst(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicyAllowed)}
	setMockClient(t, mock)

	output, executeError := runAIKeys(t, "n\n", "remove", "work")
	if !errors.Is(executeError, errCancelled) || exitCodeFor(executeError) != exitCancelled {
		t.Errorf("error = %v, want errCancelled after a declined prompt", executeError)
	}
	if mock.deleteCalls != 0 {
		t.Errorf("DeletePersonalAIKey called %d times after a declined prompt", mock.deleteCalls)
	}
	if !strings.Contains(output, `Remove your Anthropic key "work" (sk-ant-...1111)?`) {
		t.Errorf("expected the prompt to name the key, got:\n%s", output)
	}
}

func TestAIKeysRemoveWithYesRemovesTheNamedKey(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{listing: threeKeys(client.PersonalAIKeysPolicyAllowed)}
	setMockClient(t, mock)

	output, executeError := runAIKeys(t, "", "remove", "key-b", "--yes")
	if executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	if mock.deletedID != "key-b" || !strings.Contains(output, "Removed your OpenRouter key sk-or-...2222") {
		t.Errorf("deleted %q, output:\n%s", mock.deletedID, output)
	}
}

func TestAIKeysTestReportsAWorkingKey(t *testing.T) {
	resetAIKeysCommandState(t)
	listing := threeKeys(client.PersonalAIKeysPolicyAllowed)
	mock := &aiKeysMock{listing: listing, testResult: &client.PersonalAIKeyTestResult{
		Key: listing.Keys[2], IsValid: true,
	}}
	mock.testResult.Key.Status = client.PersonalAIKeyStatusActive
	setMockClient(t, mock)

	output, executeError := runAIKeys(t, "", "test", "personal")
	if executeError != nil {
		t.Fatalf("unexpected error: %v\n%s", executeError, output)
	}
	if mock.testedID != "key-c" || !strings.Contains(output, "works. Status: active") {
		t.Errorf("tested %q, output:\n%s", mock.testedID, output)
	}
}

// A failed test is a non-zero exit, so a script can act on it, and -o json
// still prints the outcome.
func TestAIKeysTestExitsNonZeroWhenTheKeyFails(t *testing.T) {
	resetAIKeysCommandState(t)
	listing := threeKeys(client.PersonalAIKeysPolicyAllowed)
	mock := &aiKeysMock{listing: listing, testResult: &client.PersonalAIKeyTestResult{
		Key: listing.Keys[0], IsValid: false, Message: "Anthropic refused this key: it was revoked.",
	}}
	mock.testResult.Key.Status = client.PersonalAIKeyStatusRejected
	setMockClient(t, mock)

	output, stderr, executeError := runAIKeysSplit(t, "", "test", "work", "-o", "json")
	if executeError == nil || exitCodeFor(executeError) != exitError ||
		!strings.Contains(executeError.Error(), "it was revoked (status now: rejected by the provider)") {
		t.Errorf("error = %v, want exit 1 with the provider's reason", executeError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil || decoded["is_valid"] != false {
		t.Errorf("-o json should still print the outcome alone on stdout, got %v:\n%s", decodeError, output)
	}
	if !strings.Contains(stderr, "failed the test") {
		t.Errorf("the failure should be reported on stderr, got:\n%s", stderr)
	}
}

func TestAIKeysPolicyReadsAndSwitchesTheOrganisationPolicy(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{policy: client.PersonalAIKeysPolicy{Allowed: true}}
	setMockClient(t, mock)

	output, executeError := runAIKeys(t, "", "policy")
	if executeError != nil || !strings.Contains(output, "can run the chats and pull request reviews they start on their own AI keys") {
		t.Errorf("policy: error %v, output:\n%s", executeError, output)
	}

	output, executeError = runAIKeys(t, "", "policy", "disable")
	if executeError != nil || !strings.Contains(output, "turned off for this organisation") {
		t.Errorf("policy disable: error %v, output:\n%s", executeError, output)
	}
	output, _, executeError = runAIKeysSplit(t, "", "policy", "allow", "-o", "json")
	if executeError != nil || strings.TrimSpace(output) != "{\n  \"allowed\": true\n}" {
		t.Errorf("policy allow -o json: error %v, output:\n%s", executeError, output)
	}
	if len(mock.setAllowed) != 2 || mock.setAllowed[0] || !mock.setAllowed[1] {
		t.Errorf("SetPersonalAIKeysPolicy calls = %v, want [false true]", mock.setAllowed)
	}
}

func TestAIKeysPolicyRefusesAnUnknownSwitch(t *testing.T) {
	resetAIKeysCommandState(t)
	mock := &aiKeysMock{}
	setMockClient(t, mock)

	_, executeError := runAIKeys(t, "", "policy", "off")
	if executeError == nil || len(mock.setAllowed) != 0 {
		t.Errorf("error = %v, set calls %v; want an unknown-command error and no change", executeError, mock.setAllowed)
	}
}

// TestPersonalAIKeysPolicyNoteReportsAnUnsaidPolicyAsUnknown pins the note
// under the keys listing: each of the three policies gets its own sentence, and
// a listing that carried no policy says the answer is not known instead of
// printing an empty value as if it were one.
func TestPersonalAIKeysPolicyNoteReportsAnUnsaidPolicyAsUnknown(t *testing.T) {
	cases := map[string]string{
		client.PersonalAIKeysPolicyAllowed:    "runs the chats and pull request reviews you start on these keys",
		client.PersonalAIKeysPolicyDisabled:   "turned members' own AI keys off",
		client.PersonalAIKeysPolicySelfHosted: "runs AI on its own endpoint",
		"":                                    "did not say whether this organisation uses your keys",
	}
	for policy, want := range cases {
		note := personalAIKeysPolicyNote(policy)
		if !strings.Contains(note, want) {
			t.Errorf("policy %q: note %q does not contain %q", policy, note, want)
		}
		if strings.Contains(note, ": .") {
			t.Errorf("policy %q: note %q prints an empty value", policy, note)
		}
	}
}
