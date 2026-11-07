package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ankra/internal/client"

	"github.com/dustin/go-humanize"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// personalAIKeyLabelMaxLength is the longest label the platform stores.
const personalAIKeyLabelMaxLength = 100

// personalAIKeyReferenceHelp is how every command that takes a key says what
// to type for it.
const personalAIKeyReferenceHelp = `<key> is the key's id or its label, as 'ankra ai keys list' shows them.`

var aiKeysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Run the chats and pull request reviews you start on your own AI keys",
	Long: `Add your own Anthropic or OpenRouter API keys to your profile. The chats you
send and the reviews of your GitHub pull requests then run on your keys, in the
order you choose, and your provider bills your account instead of your
organisation's. Background work, scheduled runs and alert analysis keep running
on the organisation's AI.

Your keys are yours alone: they never serve another member's work, and after
you add a key Ankra only ever shows its last four characters. You can keep up
to 10 keys.

Only API keys are accepted: an Anthropic key from the Claude Console (it
starts with sk-ant-api) or an OpenRouter key (it starts with sk-or-). Claude
subscription tokens (sk-ant-oat...) and Anthropic admin keys are refused,
because Anthropic does not allow other services to store them.

Ankra tries your keys from the top. A key that runs out of credit is tried
again an hour later; a key the provider rejects is not used until
'ankra ai keys test' passes.

An organisation administrator can turn members' own keys off for the
organisation ('ankra ai keys policy disable'). Your keys then stay on your
profile, but are not in use there.

You can also manage your keys in the Ankra portal: Profile, Connected
accounts, Your AI keys.`,
}

// --- list ---

var aiKeysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your AI keys, in the order they are tried",
	Long: `List the AI keys on your profile in the order your chats and pull request
reviews try them, with each key's status, and say whether your current
organisation uses them.

A key is active, out of credit (tried again at the time shown), or rejected
by its provider (not used until 'ankra ai keys test' passes).`,
	Example: `  ankra ai keys list
  ankra ai keys list -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		listing, listError := apiClient.ListPersonalAIKeys()
		if listError != nil {
			return fmt.Errorf("listing your AI keys: %w", listError)
		}
		if rendered, renderError := renderStructured(cmd, listing); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		keys := personalAIKeysInOrder(listing.Keys)
		if len(keys) == 0 {
			_, _ = fmt.Fprintln(out, "You have no AI keys on your profile.")
			_, _ = fmt.Fprintln(out, "Add one with 'ankra ai keys add', or in the portal: Profile, Connected accounts, Your AI keys.")
			_, _ = fmt.Fprintln(out, personalAIKeysPolicyNote(listing.OrganisationPolicy))
			return nil
		}
		renderPersonalAIKeys(out, keys, listing.OrganisationPolicy)
		_, _ = fmt.Fprintln(out, personalAIKeysPolicyNote(listing.OrganisationPolicy))
		return nil
	},
}

// --- add ---

var aiKeysAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an Anthropic or OpenRouter API key to your profile",
	Long: `Add an Anthropic or OpenRouter API key to your profile. Ankra checks the key
with the provider before saving it, so a mistyped or revoked key is refused
straight away, and adds it to the end of your order.

The key is never a flag: a flag value lands in your shell history and in the
process list. Run the command in a terminal and paste the key when asked (the
input is hidden), or pipe it in with --key-stdin.

The provider is worked out from the key: sk-ant-api... is Anthropic and
sk-or-... is OpenRouter. --provider is only needed to check that the key is
the kind you meant. A Claude subscription token (sk-ant-oat...) or an
Anthropic admin key is refused before anything is sent.

Only the key's masked preview is ever printed.`,
	Example: `  # Paste the key when asked; the input is hidden.
  ankra ai keys add --label "Personal Max plan"

  # Pipe it in, for example from a password manager.
  op read op://Private/anthropic/credential | ankra ai keys add --key-stdin --label work`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		requestedProvider, providerError := personalAIKeyProviderFlag(cmd)
		if providerError != nil {
			return providerError
		}
		label := mustFlagString(cmd, "label")
		if labelError := checkPersonalAIKeyLabel(label); labelError != nil {
			return labelError
		}
		apiKey, readError := readPersonalAIKey(cmd)
		if readError != nil {
			return readError
		}
		provider, shapeError := personalAIKeyProvider(apiKey, requestedProvider)
		if shapeError != nil {
			return shapeError
		}
		key, addError := apiClient.AddPersonalAIKey(client.AddPersonalAIKeyRequest{
			Provider: provider, APIKey: apiKey, Label: label,
		})
		if addError != nil {
			return redactSecret(fmt.Errorf("adding your AI key: %w", addError), apiKey)
		}
		if rendered, renderError := renderStructured(cmd, key); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Added your %s.\n", describePersonalAIKey(*key))
		if key.Position == 0 {
			_, _ = fmt.Fprintln(out, "It is the first key your chats and pull request reviews try.")
			return nil
		}
		_, _ = fmt.Fprintf(out, "It is number %d in the order your chats and pull request reviews try your keys; "+
			"change that with 'ankra ai keys order'.\n", key.Position+1)
		return nil
	},
}

// --- test ---

var aiKeysTestCmd = &cobra.Command{
	Use:   "test <key>",
	Short: "Check a key with its provider again",
	Long: `Ask the provider about one of your keys and record the answer. A key that
works is put back in service, a refused key is marked rejected, and a key with
no balance left is marked out of credit for an hour. When the provider cannot
be reached, the key is left as it was.

The command exits 1 when the key fails the test.

` + personalAIKeyReferenceHelp,
	Example: `  ankra ai keys test work`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, resolveError := findPersonalAIKey(args[0])
		if resolveError != nil {
			return resolveError
		}
		result, testError := apiClient.TestPersonalAIKey(key.ID)
		if testError != nil {
			return fmt.Errorf("testing your AI key: %w", testError)
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			if renderError != nil {
				return renderError
			}
			return personalAIKeyTestFailure(result)
		}
		if failure := personalAIKeyTestFailure(result); failure != nil {
			return failure
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Your %s works. Status: %s.\n",
			describePersonalAIKey(result.Key), personalAIKeyStatusText(result.Key))
		return nil
	},
}

// personalAIKeyTestFailure is the error a failed test exits with, or nil when
// the provider accepted the key.
func personalAIKeyTestFailure(result *client.PersonalAIKeyTestResult) error {
	if result.IsValid {
		return nil
	}
	reason := strings.TrimSpace(result.Message)
	if reason == "" {
		reason = "the provider did not accept it"
	}
	return fmt.Errorf("your %s failed the test: %s (status now: %s)",
		describePersonalAIKey(result.Key), strings.TrimSuffix(reason, "."), personalAIKeyStatusText(result.Key))
}

// --- rename ---

var aiKeysRenameCmd = &cobra.Command{
	Use:   "rename <key> <label>",
	Short: "Change the label shown beside a key",
	Long: `Change the label shown beside one of your keys. An empty label ("") clears
it. A label is at most 100 characters.

` + personalAIKeyReferenceHelp,
	Example: `  ankra ai keys rename work "Personal Max plan"
  ankra ai keys rename "Personal Max plan" ""`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		label := strings.TrimSpace(args[1])
		if labelError := checkPersonalAIKeyLabel(label); labelError != nil {
			return labelError
		}
		key, resolveError := findPersonalAIKey(args[0])
		if resolveError != nil {
			return resolveError
		}
		renamed, renameError := apiClient.RenamePersonalAIKey(key.ID, label)
		if renameError != nil {
			return fmt.Errorf("renaming your AI key: %w", renameError)
		}
		if rendered, renderError := renderStructured(cmd, renamed); rendered || renderError != nil {
			return renderError
		}
		if renamed.Label == "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cleared the label of your %s key %s.\n",
				personalAIKeyProviderName(renamed.Provider), renamed.KeyPreview)
			return nil
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Your %s key %s is now labelled %q.\n",
			personalAIKeyProviderName(renamed.Provider), renamed.KeyPreview, renamed.Label)
		return nil
	},
}

// --- order ---

var aiKeysOrderCmd = &cobra.Command{
	Use:   "order <key> [<key>...]",
	Short: "Set the order your keys are tried in",
	Long: `Set the order your chats and pull request reviews try your keys in. Name
the keys you want first, first; the keys you leave out keep their current
order after them. So 'ankra ai keys order work' moves the key labelled work to
the top.

` + personalAIKeyReferenceHelp,
	Example: `  ankra ai keys order work
  ankra ai keys order personal work`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		listing, listError := apiClient.ListPersonalAIKeys()
		if listError != nil {
			return fmt.Errorf("reading your AI keys: %w", listError)
		}
		keyIDs, orderError := personalAIKeyOrder(personalAIKeysInOrder(listing.Keys), args)
		if orderError != nil {
			return orderError
		}
		list, reorderError := apiClient.ReorderPersonalAIKeys(keyIDs)
		if reorderError != nil {
			return fmt.Errorf("ordering your AI keys: %w", reorderError)
		}
		if rendered, renderError := renderStructured(cmd, list); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "Your keys are now tried in this order:")
		renderPersonalAIKeys(out, personalAIKeysInOrder(list.Keys), "")
		return nil
	},
}

// personalAIKeyOrder is the full order to send: the named keys first, in the
// order given, then every other key in its current order. The platform
// refuses an order that does not name every key exactly once, so the keys
// left out are filled in rather than dropped.
func personalAIKeyOrder(keys []client.PersonalAIKey, references []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, withExitCode(exitNotFound, errors.New(
			"you have no AI keys on your profile to order; add one with 'ankra ai keys add'"))
	}
	named := make(map[string]string, len(references))
	keyIDs := make([]string, 0, len(keys))
	for _, reference := range references {
		key, resolveError := resolvePersonalAIKey(keys, reference)
		if resolveError != nil {
			return nil, resolveError
		}
		if earlier, isNamed := named[key.ID]; isNamed {
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"%q and %q name the same key; name each key once", earlier, reference))
		}
		named[key.ID] = reference
		keyIDs = append(keyIDs, key.ID)
	}
	for _, key := range keys {
		if _, isNamed := named[key.ID]; !isNamed {
			keyIDs = append(keyIDs, key.ID)
		}
	}
	return keyIDs, nil
}

// --- remove ---

var aiKeysRemoveCmd = &cobra.Command{
	Use:     "remove <key>",
	Aliases: []string{"delete"},
	Short:   "Remove a key from your profile",
	Long: `Remove one of your keys. Ankra deletes the stored key, so work you start
afterwards no longer runs on it. The key itself stays valid at the provider:
revoke it there too if you no longer need it.

Asks for confirmation unless --yes is passed.

` + personalAIKeyReferenceHelp,
	Example: `  ankra ai keys remove work
  ankra ai keys remove 3f0c9e4e-5b7a-4d0e-9a51-2a6a1f0b8c11 --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, resolveError := findPersonalAIKey(args[0])
		if resolveError != nil {
			return resolveError
		}
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), fmt.Sprintf(
			"Remove your %s? Work you start afterwards no longer runs on it. [y/N]: ",
			describePersonalAIKey(key)), yes); confirmError != nil {
			return confirmError
		}
		if deleteError := apiClient.DeletePersonalAIKey(key.ID); deleteError != nil {
			return fmt.Errorf("removing your AI key: %w", deleteError)
		}
		if rendered, renderError := renderStructured(cmd, map[string]any{"id": key.ID, "deleted": true}); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed your %s.\n", describePersonalAIKey(key))
		return nil
	},
}

// --- policy (organisation switch) ---

var aiKeysPolicyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Show whether this organisation runs members' work on their own AI keys",
	Long: `Show whether this organisation's work may run on its members' own AI keys.
Members can use their own keys until an administrator turns that off.

'ankra ai keys policy disable' turns it off and 'ankra ai keys policy allow'
turns it back on; both need permission to manage the organisation's AI.
Members keep their keys either way. An organisation that runs AI on its own
OpenAI-compatible endpoint never uses members' keys, whatever this says.`,
	Example: `  ankra ai keys policy
  ankra ai keys policy disable`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		policy, policyError := apiClient.GetPersonalAIKeysPolicy()
		if policyError != nil {
			return fmt.Errorf("reading the personal AI keys policy: %w", policyError)
		}
		if rendered, renderError := renderStructured(cmd, policy); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, personalAIKeysAllowedNote(policy.Allowed))
		if policy.Allowed {
			_, _ = fmt.Fprintln(out, "An administrator can turn this off with 'ankra ai keys policy disable'.")
		} else {
			_, _ = fmt.Fprintln(out, "An administrator can turn this back on with 'ankra ai keys policy allow'.")
		}
		return nil
	},
}

var aiKeysPolicyAllowCmd = &cobra.Command{
	Use:   "allow",
	Short: "Let members run their own work on their own AI keys",
	Long: `Let this organisation's members run the chats and pull request reviews they
start on their own AI keys. Needs permission to manage the organisation's AI.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return setPersonalAIKeysPolicy(cmd, true)
	},
}

var aiKeysPolicyDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Stop this organisation's work running on members' own AI keys",
	Long: `Stop this organisation's work running on its members' own AI keys. Their
chats and reviews run on the organisation's AI instead. Members keep their
keys, shown as not in use, until the policy is allowed again. Needs permission
to manage the organisation's AI.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return setPersonalAIKeysPolicy(cmd, false)
	},
}

func setPersonalAIKeysPolicy(cmd *cobra.Command, allowed bool) error {
	policy, policyError := apiClient.SetPersonalAIKeysPolicy(allowed)
	if policyError != nil {
		return fmt.Errorf("changing the personal AI keys policy: %w", policyError)
	}
	if rendered, renderError := renderStructured(cmd, policy); rendered || renderError != nil {
		return renderError
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), personalAIKeysAllowedNote(policy.Allowed))
	return nil
}

func personalAIKeysAllowedNote(allowed bool) string {
	if allowed {
		return "Members of this organisation can run the chats and pull request reviews they start on their own AI keys."
	}
	return "Members' own AI keys are turned off for this organisation: their keys stay on their profiles but are not used for its work."
}

// --- reading the key ---

// personalAIKeyTerminal reports whether in is an interactive terminal, and its
// file descriptor. A variable so tests can stand in for a terminal.
var personalAIKeyTerminal = func(in io.Reader) (int, bool) {
	file, isFile := in.(*os.File)
	if !isFile {
		return 0, false
	}
	descriptor := int(file.Fd())
	return descriptor, term.IsTerminal(descriptor)
}

// readHiddenPersonalAIKey asks for the key on the terminal with echo off. A
// variable so tests can replace the terminal.
var readHiddenPersonalAIKey = readHiddenTerminalLine

// readPersonalAIKey takes the key from stdin when --key-stdin is passed, and
// otherwise asks for it on the terminal with the input hidden. It never takes
// the key from a flag, and never prints it.
func readPersonalAIKey(cmd *cobra.Command) (string, error) {
	in := cmd.InOrStdin()
	descriptor, isTerminal := personalAIKeyTerminal(in)
	fromStdin, _ := cmd.Flags().GetBool("key-stdin")
	var apiKey string
	switch {
	case fromStdin && isTerminal:
		return "", withExitCode(exitUsage, errors.New(
			"--key-stdin reads a key piped into the command, but stdin is a terminal, where the key would show as you type it; "+
				"leave --key-stdin out to be asked for it with the input hidden"))
	case fromStdin:
		line, readError := readLineWithin(in, secretStdinWait)
		if errors.Is(readError, errStdinSilent) {
			return "", withExitCode(exitUsage, fmt.Errorf(
				"--key-stdin was passed, but stdin sent nothing within %s; pipe the key into the command", secretStdinWait))
		}
		if readError != nil && !errors.Is(readError, io.EOF) {
			return "", fmt.Errorf("reading the API key from stdin: %w", readError)
		}
		apiKey = line
	case isTerminal:
		line, promptError := readHiddenPersonalAIKey(descriptor, cmd.ErrOrStderr(),
			"Paste your Anthropic (sk-ant-api...) or OpenRouter (sk-or-...) API key")
		if promptError != nil {
			if errors.Is(promptError, errCancelled) {
				return "", errCancelled
			}
			return "", fmt.Errorf("reading the API key: %w", promptError)
		}
		apiKey = line
	default:
		return "", withExitCode(exitUsage, errors.New(
			"stdin is not a terminal, so the API key cannot be asked for: pipe it in with --key-stdin, "+
				"for example 'ankra ai keys add --key-stdin < key.txt'. The key is never taken as a flag"))
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", withExitCode(exitUsage, errors.New("no API key was given; nothing was sent"))
	}
	if strings.IndexFunc(apiKey, unicode.IsSpace) >= 0 {
		return "", withExitCode(exitUsage, errors.New(
			"what was given holds a space, so it is not a single API key; give only the key. Nothing was sent"))
	}
	return apiKey, nil
}

// readHiddenTerminalLine prints label to prompt and reads one line from the
// terminal with echo off. An interrupt puts the terminal back the way it was
// before giving up, so the shell is not left typing blind.
func readHiddenTerminalLine(descriptor int, prompt io.Writer, label string) (string, error) {
	state, stateError := term.GetState(descriptor)
	if stateError != nil {
		return "", stateError
	}
	_, _ = fmt.Fprintf(prompt, "%s (input hidden): ", label)
	defer func() { _, _ = fmt.Fprintln(prompt) }()

	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	type readResult struct {
		line      []byte
		readError error
	}
	results := make(chan readResult, 1)
	go func() {
		line, readError := term.ReadPassword(descriptor)
		results <- readResult{line: line, readError: readError}
	}()
	select {
	case result := <-results:
		return string(result.line), result.readError
	case <-interrupts:
		_ = term.Restore(descriptor, state)
		return "", errCancelled
	}
}

// --- checking the key before it is sent ---

// personalAIKeyProviderFlag reads --provider, which is optional.
func personalAIKeyProviderFlag(cmd *cobra.Command) (string, error) {
	provider := strings.ToLower(mustFlagString(cmd, "provider"))
	switch provider {
	case "", "anthropic", "openrouter":
		return provider, nil
	default:
		return "", withExitCode(exitUsage, fmt.Errorf("--provider %q is not one Ankra takes: use anthropic or openrouter", provider))
	}
}

func checkPersonalAIKeyLabel(label string) error {
	if utf8.RuneCountInString(label) > personalAIKeyLabelMaxLength {
		return withExitCode(exitUsage, fmt.Errorf("the label is %d characters; it can be at most %d",
			utf8.RuneCountInString(label), personalAIKeyLabelMaxLength))
	}
	return nil
}

// personalAIKeyProvider works out the key's provider from its prefix and
// refuses what Ankra will never store before anything is sent: a Claude
// subscription token, an Anthropic admin key, or something that is not an
// API key of either provider. Messages name the prefix, never the key.
func personalAIKeyProvider(apiKey string, requested string) (string, error) {
	for _, subscriptionPrefix := range []string{"sk-ant-oat", "sk-ant-ort"} {
		if strings.HasPrefix(apiKey, subscriptionPrefix) {
			return "", withExitCode(exitUsage, fmt.Errorf(
				"that is a Claude subscription token (it starts with %s), not an API key. "+
					"Anthropic does not allow other services to store subscription credentials, so Ankra only takes API keys: "+
					"create one in the Claude Console (https://platform.claude.com) and add that instead. Nothing was sent",
				subscriptionPrefix))
		}
	}
	switch {
	case strings.HasPrefix(apiKey, "sk-ant-admin"):
		return "", withExitCode(exitUsage, errors.New(
			"that is an Anthropic admin key (it starts with sk-ant-admin), which manages an organisation rather than "+
				"running models. Create an API key in the Claude Console (https://platform.claude.com) and add that instead. "+
				"Nothing was sent"))
	}
	detected := ""
	switch {
	case strings.HasPrefix(apiKey, "sk-ant-"):
		detected = "anthropic"
	case strings.HasPrefix(apiKey, "sk-or-"):
		detected = "openrouter"
	}
	switch {
	case detected == "" && requested == "":
		return "", withExitCode(exitUsage, errors.New(
			"that does not look like an Anthropic API key (sk-ant-api...) or an OpenRouter key (sk-or-...); "+
				"check you copied the whole key. Nothing was sent"))
	case detected == "":
		return "", withExitCode(exitUsage, fmt.Errorf(
			"that does not look like an %s API key, which starts with %s; check you copied the whole key. Nothing was sent",
			personalAIKeyProviderName(requested), personalAIKeyPrefix(requested)))
	case requested != "" && requested != detected:
		return "", withExitCode(exitUsage, fmt.Errorf(
			"--provider is %s, but the key is an %s key (it starts with %s). Nothing was sent",
			requested, personalAIKeyProviderName(detected), personalAIKeyPrefix(detected)))
	}
	return detected, nil
}

func personalAIKeyPrefix(provider string) string {
	if provider == "openrouter" {
		return "sk-or-"
	}
	return "sk-ant-"
}

func personalAIKeyProviderName(provider string) string {
	switch provider {
	case "anthropic":
		return "Anthropic"
	case "openrouter":
		return "OpenRouter"
	default:
		return provider
	}
}

// secretRedactedError hides a secret wherever it appears in an error's
// message. It unwraps to the original, so the exit code is unchanged.
type secretRedactedError struct {
	err    error
	secret string
}

func (e *secretRedactedError) Error() string {
	return strings.ReplaceAll(e.err.Error(), e.secret, "[redacted]")
}

func (e *secretRedactedError) Unwrap() error { return e.err }

// redactSecret makes sure an error can never print secret, whatever the
// platform or a proxy put into its message.
func redactSecret(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	return &secretRedactedError{err: err, secret: secret}
}

// --- naming keys ---

// findPersonalAIKey reads the member's keys and finds the one reference names.
func findPersonalAIKey(reference string) (client.PersonalAIKey, error) {
	listing, listError := apiClient.ListPersonalAIKeys()
	if listError != nil {
		return client.PersonalAIKey{}, fmt.Errorf("reading your AI keys: %w", listError)
	}
	return resolvePersonalAIKey(listing.Keys, reference)
}

// resolvePersonalAIKey finds a key by its id or its label (letter case is
// ignored). A label two keys share is refused rather than guessed.
func resolvePersonalAIKey(keys []client.PersonalAIKey, reference string) (client.PersonalAIKey, error) {
	wanted := strings.TrimSpace(reference)
	if wanted == "" {
		return client.PersonalAIKey{}, withExitCode(exitUsage, errors.New(
			"name a key by its id or its label; 'ankra ai keys list' shows them"))
	}
	for _, key := range keys {
		if strings.EqualFold(key.ID, wanted) {
			return key, nil
		}
	}
	var labelled []client.PersonalAIKey
	for _, key := range keys {
		if key.Label != "" && strings.EqualFold(strings.TrimSpace(key.Label), wanted) {
			labelled = append(labelled, key)
		}
	}
	switch len(labelled) {
	case 1:
		return labelled[0], nil
	case 0:
		return client.PersonalAIKey{}, withExitCode(exitNotFound, fmt.Errorf(
			"no AI key on your profile has the id or label %q; 'ankra ai keys list' shows them", wanted))
	default:
		keyIDs := make([]string, 0, len(labelled))
		for _, key := range labelled {
			keyIDs = append(keyIDs, key.ID)
		}
		return client.PersonalAIKey{}, withExitCode(exitUsage, fmt.Errorf(
			"%d of your keys are labelled %q; name the one you mean by its id: %s",
			len(labelled), wanted, strings.Join(keyIDs, ", ")))
	}
}

// personalAIKeysInOrder returns the keys sorted by their place in the order.
func personalAIKeysInOrder(keys []client.PersonalAIKey) []client.PersonalAIKey {
	ordered := append([]client.PersonalAIKey(nil), keys...)
	sort.SliceStable(ordered, func(left, right int) bool { return ordered[left].Position < ordered[right].Position })
	return ordered
}

// describePersonalAIKey names a key in a sentence by its label, provider and
// masked preview, never the key.
func describePersonalAIKey(key client.PersonalAIKey) string {
	if key.Label == "" {
		return fmt.Sprintf("%s key %s", personalAIKeyProviderName(key.Provider), key.KeyPreview)
	}
	return fmt.Sprintf("%s key %q (%s)", personalAIKeyProviderName(key.Provider), key.Label, key.KeyPreview)
}

// personalAIKeyStatusText says a key's status in words, with when an out of
// credit key is tried again.
func personalAIKeyStatusText(key client.PersonalAIKey) string {
	switch key.Status {
	case client.PersonalAIKeyStatusActive:
		return "active"
	case client.PersonalAIKeyStatusOutOfCredit:
		retryAfter, hasRetry := parsePersonalAIKeyTime(key.RetryAfter)
		if !hasRetry {
			return "out of credit"
		}
		if retryAfter.After(time.Now()) {
			return "out of credit, tried again " + humanize.Time(retryAfter)
		}
		return "out of credit, tried again on next use"
	case client.PersonalAIKeyStatusRejected:
		return "rejected by the provider"
	default:
		return key.Status
	}
}

func parsePersonalAIKeyTime(value *string) (time.Time, bool) {
	if value == nil || *value == "" {
		return time.Time{}, false
	}
	parsed, parseError := time.Parse(time.RFC3339, *value)
	if parseError != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// personalAIKeysPolicyNote says what the current organisation does with the
// member's keys.
func personalAIKeysPolicyNote(policy string) string {
	switch policy {
	case client.PersonalAIKeysPolicyAllowed:
		return "Your organisation runs the chats and pull request reviews you start on these keys, trying them from the top."
	case client.PersonalAIKeysPolicyDisabled:
		return "An administrator turned members' own AI keys off for this organisation, so your keys are not in use here. " +
			"They stay on your profile."
	case client.PersonalAIKeysPolicySelfHosted:
		return "This organisation runs AI on its own endpoint, so your keys are never used for its work."
	default:
		return fmt.Sprintf("Organisation policy: %s.", policy)
	}
}

// renderPersonalAIKeys prints the keys table. policy is the organisation's
// policy from a listing, or empty when the answer carried none; keys an
// organisation does not use are shown as not in use.
func renderPersonalAIKeys(out io.Writer, keys []client.PersonalAIKey, policy string) {
	notInUse := policy != "" && policy != client.PersonalAIKeysPolicyAllowed
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"#", "Label", "Provider", "Key", "Status", "Last Used", "ID"})
	for _, key := range keys {
		label := key.Label
		if label == "" {
			label = "-"
		}
		status := personalAIKeyStatusText(key)
		if notInUse {
			status = "not in use"
		}
		lastUsed := "never"
		if usedAt, hasUse := parsePersonalAIKeyTime(key.LastUsedAt); hasUse {
			lastUsed = humanize.Time(usedAt)
		}
		writer.AppendRow(table.Row{key.Position + 1, label, personalAIKeyProviderName(key.Provider),
			key.KeyPreview, status, lastUsed, key.ID})
	}
	writer.Render()
}

func init() {
	aiKeysAddCmd.Flags().String("provider", "", "anthropic or openrouter; worked out from the key when left out")
	aiKeysAddCmd.Flags().String("label", "", "Your name for the key, shown beside it (at most 100 characters)")
	aiKeysAddCmd.Flags().Bool("key-stdin", false,
		"Read the API key from stdin instead of asking for it (the key is never taken as a flag)")
	aiKeysRemoveCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(aiKeysListCmd, aiKeysAddCmd, aiKeysTestCmd, aiKeysRenameCmd,
		aiKeysOrderCmd, aiKeysRemoveCmd, aiKeysPolicyCmd, aiKeysPolicyAllowCmd, aiKeysPolicyDisableCmd)

	aiKeysPolicyCmd.AddCommand(aiKeysPolicyAllowCmd, aiKeysPolicyDisableCmd)
	aiKeysCmd.AddCommand(aiKeysListCmd, aiKeysAddCmd, aiKeysTestCmd, aiKeysRenameCmd,
		aiKeysOrderCmd, aiKeysRemoveCmd, aiKeysPolicyCmd)
	aiCmd.AddCommand(aiKeysCmd)
}
