package cmd

// `ankra onboard`: the first command a new person runs, and the one the
// installer runs right after installing. It signs the person up or in in the
// browser, then does only what the account still needs: pick the
// organisation, offer a free playground when the organisation has no
// cluster, and set up the coding assistants on this machine. Every step
// checks before it acts, so running it again is safe. With --json every step
// is one NDJSON event (onboard_events.go) an AI coding agent can act on.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"
	"unicode/utf8"

	"ankra/internal/client"
	"ankra/internal/skills"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const onboardTagline = "Run applications as easily as you build them."

// onboardLogo is the Ankra horizontal lockup, the symbol beside the ANKRA
// wordmark, drawn from the brand artwork
// (https://ankra.ai/brand/png/ankra-horizontal-black-2400.png) in quadrant block
// characters, two pixels per cell each way. Block characters print solid in
// common terminal fonts and most terminals draw them natively; braille and
// sextant cells were tried and left out because many fonts render them faint or
// not at all. The logo is monochrome by the brand rules, so it is printed in the
// terminal's own text colour and reads on light and dark backgrounds alike.
var onboardLogo = []string{
	`    ▄▞▗█▙▄`,
	`▗▄█▀▀▗█▚▖▀▀█▄▖                  ▖   ▖    ▗  ▄▄▄▄▖      ▗`,
	`█▌  ▟█▘▝▜▙  ▐█     ▐▀▙   ▐▛▙    █  ▐▌ ▗▄▛▘  ▛   ▀▙    ▗▛▙`,
	`█▌ ▟▛    ▜▙ ▐█    ▗▛ ▜▖  ▐▌▝▚▖  █  ▐▙▟▛     ▙   ▄█   ▗▛ ▝▙`,
	`█▌▐▛      ▜▙▝█   ▗█▄▄▄█▖ ▐▌  ▜▄ █  ▐▌ ▀▙▖   ▛▀▀▜▙    █▙▄▄█▙`,
	`▀▀▀▀▀▀▀▀▀▀▘▜█▞   ▛     ▜▖▐▌   ▝▙█  ▐▌   ▀▙  ▌   ▝▚▖ ▟▘    ▐▌`,
	` ▀▜█▄▖  ▗▄█▛▀`,
	`    ▝▀██▀▘`,
}

// onboardSymbolColumns is how many columns of onboardLogo the symbol takes.
const onboardSymbolColumns = 14

// onboardBanner is the logo to print in a terminal width columns wide (0 when
// the width is unknown). A terminal too narrow for the whole lockup gets the
// symbol alone, so the lockup never wraps into a broken picture.
func onboardBanner(width int) []string {
	logoWidth := 0
	for _, line := range onboardLogo {
		logoWidth = max(logoWidth, utf8.RuneCountInString(line))
	}
	if width == 0 || width > logoWidth {
		return onboardLogo
	}
	banner := make([]string, 0, len(onboardLogo))
	for _, line := range onboardLogo {
		runes := []rune(line)
		banner = append(banner, strings.TrimRight(string(runes[:min(len(runes), onboardSymbolColumns)]), " "))
	}
	return banner
}

// The seams tests replace.
var (
	// onboardIsTerminal reports whether a stream is a terminal.
	onboardIsTerminal = func(stream any) bool {
		file, isFile := stream.(*os.File)
		return isFile && term.IsTerminal(int(file.Fd()))
	}
	// onboardAgentName names the coding agent the CLI runs under, if any.
	onboardAgentName = client.DetectedAgentName
	// onboardChoose asks the person to pick one of items.
	onboardChoose = chooseWithPrompt
	// onboardRunMCPConnect connects Claude Code to the Ankra MCP server.
	onboardRunMCPConnect = func(out io.Writer, errOut io.Writer) error {
		command := exec.Command(currentExecutable(), "mcp", "connect", "--client", mcpClientClaudeCode)
		command.Stdout = out
		command.Stderr = errOut
		return command.Run()
	}
	onboardPlaygroundPollInterval = 5 * time.Second
	onboardPlaygroundTimeout      = 10 * time.Minute
	onboardSpinnerInterval        = 120 * time.Millisecond
)

var onboardSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var onboardCmd = &cobra.Command{
	Use:   "onboard",
	Short: "Sign up or sign in, then set up what your account still needs",
	Long: `Get started with Ankra in one command. The installer runs it for you.

onboard signs you in, or creates a free account, in your browser, and then
does only what is still missing:

  1. picks the organisation to work in, asking when you belong to several
  2. offers a free playground when the organisation has no cluster yet: a real
     Kubernetes cluster on Ankra, ready in about 2 minutes, as a trial that
     expires
  3. offers to install the Ankra skills into the coding assistants on this
     machine, and to connect Claude Code to the Ankra MCP server

Every step checks first, so running it again is safe.

Without a terminal (CI, a coding agent) it does not wait for you: it saves a
pending sign-in, prints the link to open, and exits. 'ankra onboard --wait'
then waits for that link to be approved and carries on. Running it again
without --new shows the same link until it expires, after 10 minutes.

--json prints one JSON object per line, one per step, for an AI coding agent
to drive: "event" names the step, then its data, an "instruction" saying what
to tell or ask the user, and a "next_command" when there is one to run.`,
	Example: `  ankra onboard
  ankra onboard --signup
  ankra onboard --json             # an agent shows the user the link from the login_url event
  ankra onboard --wait --json      # then waits for the approval and continues
  ankra onboard --yes --no-agent-setup`,
	Args: cobra.NoArgs,
	RunE: runOnboard,
}

func init() {
	onboardCmd.Flags().Bool("json", false, "print one JSON event per line, for a coding agent to drive")
	onboardCmd.Flags().Bool("wait", false, "without a terminal, wait until the pending sign-in is approved")
	onboardCmd.Flags().Bool("signup", false, "create a new account, without asking")
	onboardCmd.Flags().Bool("login", false, "sign in to an existing account, without asking")
	onboardCmd.Flags().Bool("new", false, "discard a pending sign-in link and start a new one")
	onboardCmd.Flags().Bool("yes", false, "accept the offers (the playground and the skills install) without asking")
	onboardCmd.Flags().Bool("no-playground", false, "do not offer a playground")
	onboardCmd.Flags().Bool("no-agent-setup", false, "do not set up coding assistants")
	setRequiresAuth(onboardCmd, false)
	// The saved organisation selection may name an organisation this login
	// no longer belongs to; onboard lists the login's organisations without
	// it and makes the choice itself.
	setIgnoresLocalOrganisation(onboardCmd)
	rootCmd.AddCommand(onboardCmd)
}

type onboardFlags struct {
	json, wait, signup, login, fresh, yes, noPlayground, noAgentSetup bool
}

// onboardRun is one invocation's state.
type onboardRun struct {
	cmd         *cobra.Command
	flags       onboardFlags
	out         io.Writer
	errOut      io.Writer
	input       *bufio.Reader
	interactive bool
	agent       string
	events      *onboardEvents
	style       skillsStyle
	// ownsClient is true when onboard built the API client itself, so it
	// rebuilds it after a new sign-in; a client set up by someone else (a
	// test's mock) is left in place.
	ownsClient bool
	baseURL    string
	// organisations is the login's organisation list, read when the login
	// was checked.
	organisations []client.OrganisationSummary
	summary       []string
}

// onboardStepError is a failure that knows what the person, or the agent,
// should do next.
type onboardStepError struct {
	err         error
	instruction string
	nextCommand string
}

func (e *onboardStepError) Error() string { return e.err.Error() }
func (e *onboardStepError) Unwrap() error { return e.err }

func runOnboard(cmd *cobra.Command, _ []string) error {
	flags := onboardFlags{}
	flags.json, _ = cmd.Flags().GetBool("json")
	flags.wait, _ = cmd.Flags().GetBool("wait")
	flags.signup, _ = cmd.Flags().GetBool("signup")
	flags.login, _ = cmd.Flags().GetBool("login")
	flags.fresh, _ = cmd.Flags().GetBool("new")
	flags.yes, _ = cmd.Flags().GetBool("yes")
	flags.noPlayground, _ = cmd.Flags().GetBool("no-playground")
	flags.noAgentSetup, _ = cmd.Flags().GetBool("no-agent-setup")

	run := &onboardRun{
		cmd:        cmd,
		flags:      flags,
		out:        cmd.OutOrStdout(),
		errOut:     cmd.ErrOrStderr(),
		input:      bufio.NewReader(cmd.InOrStdin()),
		agent:      onboardAgentName(),
		ownsClient: apiClient == nil,
	}
	run.style = newSkillsStyle(run.out)
	if flags.json {
		run.events = &onboardEvents{writer: run.out}
	}
	// A coding agent's shell can still own a terminal; prompting there would
	// hang the agent, so under an agent onboard behaves as without one.
	run.interactive = !flags.json && run.agent == "" &&
		onboardIsTerminal(cmd.InOrStdin()) && onboardIsTerminal(cmd.OutOrStdout())

	runError := run.execute()
	if runError != nil && run.events != nil {
		run.emitError(runError)
	}
	return runError
}

func (run *onboardRun) execute() error {
	if run.flags.signup && run.flags.login {
		return withExitCode(exitUsage, errors.New("--signup and --login cannot be used together"))
	}
	run.greet()
	if run.flags.fresh {
		removeOnboardPendingLogin()
	}
	isSignedIn, signInError := run.signIn()
	if signInError != nil || !isSignedIn {
		return signInError
	}
	organisation, organisationError := run.chooseOrganisation(run.organisations)
	if organisationError != nil {
		return organisationError
	}
	if organisation != nil {
		run.offerPlayground()
	}
	run.setUpAgents()
	run.finish()
	return nil
}

// command returns a follow-up command in the form this run should name it:
// with --json when the caller is an agent reading JSON.
func (run *onboardRun) command(base string) string {
	if run.events != nil {
		return base + " --json"
	}
	return base
}

func (run *onboardRun) note(format string, arguments ...any) {
	run.summary = append(run.summary, fmt.Sprintf(format, arguments...))
}

func (run *onboardRun) greet() {
	if run.interactive {
		_, _ = fmt.Fprintln(run.out)
		for _, line := range onboardBanner(run.style.width) {
			_, _ = fmt.Fprintln(run.out, line)
		}
		_, _ = fmt.Fprintln(run.out)
		_, _ = fmt.Fprintln(run.out, run.style.bold(onboardTagline))
		_, _ = fmt.Fprintln(run.out)
	}
	if run.events != nil {
		run.events.emit("onboarding_start",
			"Ankra onboarding has started. If the user already has an Ankra account, any sign-in method with the same verified email address reaches it (GitHub, Google or email): Ankra links the method to the existing account. A different email address creates a new account.",
			"",
			onboardValue("version", version),
			onboardValue("agent", run.agent))
		return
	}
	if !run.interactive && run.agent != "" {
		_, _ = fmt.Fprintln(run.errOut, "Tip for coding agents: `ankra onboard --json` prints every step as a JSON event.")
	}
}

func (run *onboardRun) emitError(err error) {
	instruction := "Onboarding stopped with an error. Tell the user what the message says."
	nextCommand := ""
	var stepError *onboardStepError
	if errors.As(err, &stepError) {
		instruction = stepError.instruction
		nextCommand = stepError.nextCommand
	}
	run.events.emit("error", instruction, nextCommand,
		onboardValue("message", err.Error()),
		onboardValue("exit_code", exitCodeFor(err)))
}

// connect points the API client at the credentials in effect, the way
// persistentPreRunE does for commands that require a login.
func (run *onboardRun) connect(resolved resolvedCredentials) {
	apiToken = resolved.token
	baseURL = resolved.baseURL
	organisationScopeTokenSource = resolved.source
	run.baseURL = resolved.baseURL
	if run.ownsClient || apiClient == nil {
		apiClient = newAPIClient()
		run.ownsClient = true
	}
}

// currentLogin reports whether the credentials in effect work, reading the
// login's organisations on the way. A saved login
// or ANKRA_API_TOKEN the platform rejects counts as no login: signing in
// again replaces it. A rejected --token is an error, because a new sign-in
// cannot take precedence over it.
func (run *onboardRun) currentLogin() (resolvedCredentials, bool, error) {
	resolved, resolveError := resolveCredentials(run.cmd)
	if resolveError != nil {
		if exitCodeFor(resolveError) == exitAuth {
			return resolvedCredentials{}, false, nil
		}
		return resolvedCredentials{}, false, resolveError
	}
	run.connect(resolved)
	organisations, listError := apiClient.ListOrganisations()
	if listError != nil {
		if errors.Is(listError, client.ErrUnauthorized) {
			if resolved.source == sourceFlag {
				return resolved, false, withExitCode(exitAuth, errors.New("the token passed with --token was rejected by the platform"))
			}
			return resolved, false, nil
		}
		return resolved, false, fmt.Errorf("checking your login: %w", listError)
	}
	run.organisations = organisations
	return resolved, true, nil
}

func (run *onboardRun) signIn() (bool, error) {
	resolved, isSignedIn, checkError := run.currentLogin()
	if checkError != nil {
		return false, checkError
	}
	if isSignedIn {
		// A sign-in link still waiting is moot now.
		removeOnboardPendingLogin()
		run.reportSignedIn(resolved, true, nil)
		return true, nil
	}
	if run.interactive {
		return run.signInInteractively()
	}
	return run.signInWithLink()
}

// signInInteractively runs the browser sign-in with the person at the
// terminal, the way `ankra login` does.
func (run *onboardRun) signInInteractively() (bool, error) {
	isSignup := run.flags.signup
	if !run.flags.signup && !run.flags.login {
		choice, chooseError := onboardChoose(run.cmd.InOrStdin(), run.out, "Do you have an Ankra account?",
			[]string{"No, create a free account", "Yes, sign in"}, 0)
		if chooseError != nil {
			return false, chooseError
		}
		isSignup = choice == 0
	}
	session, startError := startLoginSession(loginOptions{Signup: isSignup, Source: "onboard"})
	if startError != nil {
		return false, startError
	}
	out := run.out
	_, _ = fmt.Fprintln(out)
	if isSignup {
		_, _ = fmt.Fprintln(out, "Opening your browser to create your free Ankra account. If it does not open, visit:")
	} else {
		_, _ = fmt.Fprintln(out, "Opening your browser to sign in. GitHub, Google or email all reach your account when they use")
		_, _ = fmt.Fprintln(out, "the same verified email address. If the browser does not open, visit:")
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "  %s\n", session.AuthURL)
	_, _ = fmt.Fprintln(out)
	if session.UserCode != "" {
		_, _ = fmt.Fprintf(out, "Your code: %s (the browser shows the same code)\n", session.UserCode)
		_, _ = fmt.Fprintln(out)
	}
	if browserError := launchBrowser(session.AuthURL); browserError != nil {
		_, _ = fmt.Fprintln(out, "Could not open the browser. Open the link above yourself.")
	}
	_, _ = fmt.Fprintln(out, "Waiting for you to finish in the browser. Press Ctrl+C to cancel.")

	tokenData, waitError := pollForToken(out, session.BaseURL, session.Ticket, session.CodeVerifier, loginWaitTimeout)
	if waitError != nil {
		var refused *loginRefusedError
		if errors.Is(waitError, errLoginWaitExpired) || (errors.As(waitError, &refused) && refused.statusCode == http.StatusGone) {
			return false, withExitCode(exitWaitTimeout, errors.New(
				"the sign-in was not finished in time; run 'ankra onboard' again for a new link"))
		}
		return false, waitError
	}
	if issuedError := ensureTokenIssued(tokenData); issuedError != nil {
		return false, issuedError
	}
	return true, run.completeSignIn(session.BaseURL, session.MachineID, tokenData)
}

// completeSignIn saves an issued token like `ankra login` does and points
// the client at it.
func (run *onboardRun) completeSignIn(platformURL string, machineID string, tokenData tokenExchangeResponse) error {
	if _, saveError := saveLoginCredentials(platformURL, machineID, tokenData); saveError != nil {
		return saveError
	}
	resolved, isSignedIn, checkError := run.currentLogin()
	if checkError != nil {
		return checkError
	}
	if !isSignedIn {
		return withExitCode(exitAuth, errors.New("the platform did not accept the new login; run 'ankra onboard' again"))
	}
	run.reportSignedIn(resolved, false, &tokenData)
	return nil
}

func (run *onboardRun) reportSignedIn(resolved resolvedCredentials, alreadySignedIn bool, tokenData *tokenExchangeResponse) {
	tokenName := ""
	tokenExpiresAt := ""
	if tokenData != nil {
		tokenName = tokenData.TokenName
		tokenExpiresAt = tokenData.ExpiresAt
	} else if resolved.source == sourceConfigFile {
		tokenName = savedTokenName()
	}
	if alreadySignedIn {
		run.note("Signed in to %s", resolved.baseURL)
	} else {
		run.note("Signed in to %s and saved the login in %s", resolved.baseURL, getConfigPath())
	}
	if run.events != nil {
		instruction := "The user is already signed in to Ankra, so no sign-in is needed."
		if !alreadySignedIn {
			instruction = "The user is now signed in to Ankra and the session is saved. Onboarding continues."
		}
		run.events.emit("signed_in", instruction, "",
			onboardValue("already_signed_in", alreadySignedIn),
			onboardValue("base_url", resolved.baseURL),
			onboardValue("token_source", string(resolved.source)),
			onboardValue("token_name", tokenName),
			onboardValue("token_expires_at", tokenExpiresAt))
		return
	}
	who := ""
	if tokenName != "" {
		who = fmt.Sprintf(" (login %q)", tokenName)
	}
	if alreadySignedIn {
		_, _ = fmt.Fprintf(run.out, "%s You are signed in to %s%s.\n", run.style.green("✓"), resolved.baseURL, who)
		return
	}
	_, _ = fmt.Fprintln(run.out)
	_, _ = fmt.Fprintf(run.out, "%s Signed in to %s%s.\n", run.style.green("✓"), resolved.baseURL, who)
	if tokenExpiresAt != "" {
		_, _ = fmt.Fprintf(run.out, "  The login is saved in %s and expires %s.\n", getConfigPath(), formatExpiry(tokenExpiresAt))
	}
}

// onboardOrganisation is one organisation as the JSON events show it.
type onboardOrganisation struct {
	OrganisationID string `json:"organisation_id"`
	Name           string `json:"name,omitempty"`
	Slug           string `json:"slug,omitempty"`
	Role           string `json:"role,omitempty"`
	Current        bool   `json:"current"`
}

func onboardOrganisationOf(organisation client.OrganisationSummary, isCurrent bool) onboardOrganisation {
	return onboardOrganisation{
		OrganisationID: organisation.OrganisationID,
		Name:           trimmedOptional(organisation.Name),
		Slug:           trimmedOptional(organisation.Slug),
		Role:           trimmedOptional(organisation.Role),
		Current:        isCurrent,
	}
}

// chooseOrganisation settles which organisation the rest of onboarding acts
// in. It returns nil when there is none to act in yet.
func (run *onboardRun) chooseOrganisation(organisations []client.OrganisationSummary) (*client.OrganisationSummary, error) {
	if len(organisations) == 0 {
		run.reportNoOrganisation()
		return nil, nil
	}
	current, _, resolveError := resolveTargetOrganisationFrom(run.cmd, organisations)
	hasCurrent := resolveError == nil
	if organisationOverrideValue(run.cmd) != "" {
		// --org or ANKRA_ORG decides, for this run only.
		if resolveError != nil {
			return nil, withExitCode(exitUsage, resolveError)
		}
		return run.useOrganisation(current, organisations, false)
	}

	chosen := current
	switch {
	case len(organisations) == 1:
		chosen = organisations[0]
	case run.interactive:
		defaultIndex := 0
		labels := make([]string, len(organisations))
		for index, organisation := range organisations {
			labels[index] = organisationDisplayName(organisation)
			if role := trimmedOptional(organisation.Role); role != "" {
				labels[index] += " (" + role + ")"
			}
			if hasCurrent && organisation.OrganisationID == current.OrganisationID {
				defaultIndex = index
			}
		}
		choice, chooseError := onboardChoose(run.cmd.InOrStdin(), run.out, "Which organisation do you want to work in?", labels, defaultIndex)
		if chooseError != nil {
			return nil, chooseError
		}
		chosen = organisations[choice]
	case !hasCurrent:
		run.reportOrganisationChoiceNeeded(organisations)
		return nil, nil
	}

	needsSwitch := !hasCurrent || chosen.OrganisationID != current.OrganisationID
	if needsSwitch {
		if switchError := run.switchOrganisation(chosen); switchError != nil {
			return nil, switchError
		}
	}
	return run.useOrganisation(chosen, organisations, len(organisations) > 1 && !run.interactive)
}

// switchOrganisation selects an organisation the way `ankra org switch`
// does: on the platform and in the saved local selection.
func (run *onboardRun) switchOrganisation(target client.OrganisationSummary) error {
	if _, switchError := apiClient.SwitchOrganisation(target.OrganisationID); switchError != nil {
		return fmt.Errorf("switching to organisation %s: %w", organisationDisplayName(target), switchError)
	}
	if saveError := saveSelectedOrganisation(SelectedOrganisation{
		OrganisationID: target.OrganisationID,
		Name:           target.Name,
		Slug:           target.Slug,
		Role:           target.Role,
	}); saveError != nil {
		_, _ = fmt.Fprintf(run.errOut, "Warning: switched on the platform but could not save the selection locally: %v\n", saveError)
	}
	if clearError := clearSelectedProject(); clearError != nil {
		_, _ = fmt.Fprintf(run.errOut, "Warning: could not forget the project selection of the previous organisation: %v\n", clearError)
	}
	return nil
}

// useOrganisation scopes the rest of the run to the organisation and reports
// it. isUnasked marks a choice made for an agent among several, which the
// event says how to change.
func (run *onboardRun) useOrganisation(chosen client.OrganisationSummary, organisations []client.OrganisationSummary, isUnasked bool) (*client.OrganisationSummary, error) {
	apiClient.SetOrganisationOverride(chosen.OrganisationID)
	name := organisationDisplayName(chosen)
	run.note("Organisation: %s", name)
	if run.events != nil {
		listed := make([]onboardOrganisation, 0, len(organisations))
		for _, organisation := range organisations {
			listed = append(listed, onboardOrganisationOf(organisation, organisation.OrganisationID == chosen.OrganisationID))
		}
		instruction := "Onboarding continues in the organisation " + name + "."
		if isUnasked {
			instruction = fmt.Sprintf("The user belongs to %d organisations and onboarding continues in %s. "+
				"If the user wants a different one, run `ankra org switch <organisation_id>` with its id, then run `ankra onboard --json` again.",
				len(organisations), name)
		}
		run.events.emit("organisation", instruction, "",
			onboardValue("organisation", onboardOrganisationOf(chosen, true)),
			onboardValue("organisations", listed))
		return &chosen, nil
	}
	_, _ = fmt.Fprintf(run.out, "%s Organisation: %s\n", run.style.green("✓"), name)
	if isUnasked {
		_, _ = fmt.Fprintf(run.out, "  You belong to %d organisations. To use another one: ankra org switch <organisation>\n", len(organisations))
	}
	return &chosen, nil
}

func (run *onboardRun) reportNoOrganisation() {
	if run.events != nil {
		run.events.emit("organisation",
			"The user does not belong to an Ankra organisation yet. Ask them for a name for one, run `ankra org create <name>`, then run `ankra onboard --json` again.",
			"",
			onboardValue("organisations", []onboardOrganisation{}))
		return
	}
	_, _ = fmt.Fprintln(run.out, "You do not belong to an organisation yet. Create one with:")
	_, _ = fmt.Fprintln(run.out, "  ankra org create <name>")
	_, _ = fmt.Fprintln(run.out, "then run 'ankra onboard' again.")
}

func (run *onboardRun) reportOrganisationChoiceNeeded(organisations []client.OrganisationSummary) {
	if run.events != nil {
		listed := make([]onboardOrganisation, 0, len(organisations))
		for _, organisation := range organisations {
			listed = append(listed, onboardOrganisationOf(organisation, false))
		}
		run.events.emit("organisation",
			fmt.Sprintf("The user belongs to %d organisations and none is selected. Ask which one to use, run `ankra org switch <organisation_id>` with its id, then run `ankra onboard --json` again.", len(organisations)),
			"",
			onboardValue("organisations", listed))
		return
	}
	_, _ = fmt.Fprintf(run.out, "You belong to %d organisations and none is selected:\n", len(organisations))
	for _, organisation := range organisations {
		_, _ = fmt.Fprintf(run.out, "  %s  %s\n", organisation.OrganisationID, organisationDisplayName(organisation))
	}
	_, _ = fmt.Fprintln(run.out, "Pick one with 'ankra org switch <organisation>', then run 'ankra onboard' again.")
}

// confirmYes asks a question whose default is yes. A closed input declines:
// nothing is created without an answer.
func (run *onboardRun) confirmYes(question string) bool {
	_, _ = fmt.Fprint(run.out, question)
	line, readError := run.input.ReadString('\n')
	if readError != nil && line == "" {
		_, _ = fmt.Fprintln(run.out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}

// offerPlayground offers a free playground when the organisation has no
// cluster at all.
func (run *onboardRun) offerPlayground() {
	listing, listError := apiClient.ListClusters(1, 1)
	if listError != nil {
		// Not fatal: the rest of onboarding does not depend on it. The event
		// keeps a --json caller from missing the step.
		run.note("Clusters: could not be checked")
		if run.events != nil {
			run.events.emit("clusters",
				"Could not check whether the organisation has clusters ("+listError.Error()+"), so no playground was offered. "+
					"Onboarding continues; run next_command later to see the clusters.",
				"ankra cluster list",
				onboardValue("error", listError.Error()))
			return
		}
		_, _ = fmt.Fprintf(run.errOut, "Warning: could not check the organisation's clusters: %v\n", listError)
		return
	}
	count := listing.Pagination.TotalCount
	if len(listing.Result) > count {
		count = len(listing.Result)
	}
	if count > 0 {
		run.note("Clusters: %s already", pluralize(count, "cluster", "clusters"))
		if run.events != nil {
			run.events.emit("clusters",
				fmt.Sprintf("The organisation already has %s, so no playground is offered.", pluralize(count, "cluster", "clusters")),
				"", onboardValue("count", count))
			return
		}
		_, _ = fmt.Fprintf(run.out, "%s Your organisation has %s. List them with: ankra cluster list\n",
			run.style.green("✓"), pluralize(count, "cluster", "clusters"))
		return
	}

	const createCommand = "ankra cluster playground create"
	if run.flags.noPlayground {
		if run.events != nil {
			run.events.emit("clusters",
				"The organisation has no cluster yet, and the playground offer was skipped (--no-playground).",
				"", onboardValue("count", 0))
			return
		}
		_, _ = fmt.Fprintln(run.out, "Your organisation has no clusters yet. Create a free playground any time with: "+createCommand)
		return
	}

	const offer = "Your organisation has no clusters yet. A free playground is a real Kubernetes cluster on Ankra, " +
		"ready in about 2 minutes. It is a trial and expires."
	isAccepted := run.flags.yes
	if !isAccepted {
		switch {
		case run.interactive:
			_, _ = fmt.Fprintln(run.out)
			_, _ = fmt.Fprintln(run.out, offer)
			isAccepted = run.confirmYes("Create a free playground now? [Y/n] ")
			if !isAccepted {
				_, _ = fmt.Fprintln(run.out, "Skipped. Create one later with: "+createCommand)
				return
			}
		case run.events != nil:
			run.events.emit("playground_offer",
				"The organisation has no cluster yet. Ask the user whether to create a free playground: a real Kubernetes "+
					"cluster on Ankra, ready in about 2 minutes. It is a trial and expires. If they agree, run next_command.",
				createCommand)
			return
		default:
			_, _ = fmt.Fprintln(run.out, offer)
			_, _ = fmt.Fprintln(run.out, "Create one with: "+createCommand)
			return
		}
	}
	run.createPlayground()
}

func (run *onboardRun) createPlayground() {
	const createCommand = "ankra cluster playground create"
	result, createError := apiClient.CreatePlayground("")
	clusterID := ""
	switch {
	case createError == nil && result != nil:
		clusterID = result.ClusterID
	case createError != nil && client.ExistingPlaygroundID(createError) != "":
		clusterID = client.ExistingPlaygroundID(createError)
		if run.events == nil {
			_, _ = fmt.Fprintln(run.out, "Your organisation already has a playground; following it.")
		}
	}
	if clusterID == "" {
		message := "the platform did not say which cluster it created"
		if createError != nil {
			message = playgroundRefusalMessage(createError)
		}
		run.note("Playground: not created (%s)", message)
		if run.events != nil {
			run.events.emit("playground_status",
				"The playground could not be created: "+message+". Tell the user; they can try again later with next_command.",
				createCommand,
				onboardValue("phase", "not_created"),
				onboardValue("message", message))
			return
		}
		_, _ = fmt.Fprintf(run.out, "Could not create the playground: %s\n", message)
		_, _ = fmt.Fprintln(run.out, "Try again later with: "+createCommand)
		return
	}
	run.waitForPlayground(clusterID)
}

// playgroundRefusalMessage says why a playground was not created, in the
// platform's words where it gave any.
func playgroundRefusalMessage(createError error) string {
	var unexpected *client.UnexpectedResponseError
	if errors.As(createError, &unexpected) {
		switch unexpected.StatusCode {
		case http.StatusServiceUnavailable:
			return "the playground pool is full right now, try again later"
		case http.StatusPaymentRequired, http.StatusForbidden, http.StatusUnprocessableEntity:
			if unexpected.Detail != "" {
				return unexpected.Detail
			}
		}
	}
	return createError.Error()
}

// onboardPlaygroundSettled reports whether a phase is final for the wait:
// ready, or a phase the playground does not come back from on its own.
func onboardPlaygroundSettled(phase string) bool {
	switch phase {
	case "ready", "failed", "deprovisioning", "removed":
		return true
	}
	return false
}

// waitForPlayground follows provisioning until the playground is ready, for
// at most onboardPlaygroundTimeout. Ctrl+C stops the waiting, not the
// playground, and onboarding carries on with its next step.
func (run *onboardRun) waitForPlayground(clusterID string) {
	statusCommand := "ankra cluster playground status " + clusterID
	interrupted, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()

	if run.events == nil {
		_, _ = fmt.Fprintln(run.out)
		if run.interactive {
			_, _ = fmt.Fprintln(run.out, "Creating your playground. It is usually ready in about 2 minutes.")
			_, _ = fmt.Fprintln(run.out, "Press Ctrl+C to stop waiting; the playground keeps going.")
		} else {
			_, _ = fmt.Fprintf(run.out, "Creating your playground (cluster %s). Waiting until it is ready...\n", clusterID)
		}
	}

	started := time.Now()
	lastPhase := ""
	var latest *client.PlaygroundStatus
	check := func() bool {
		status, statusError := apiClient.GetPlaygroundStatus(clusterID)
		if statusError != nil || status == nil {
			// A failed read is not a failed playground: try again next tick.
			return false
		}
		latest = status
		isSettled := onboardPlaygroundSettled(status.Phase)
		if status.Phase != lastPhase {
			lastPhase = status.Phase
			if run.events != nil && !isSettled {
				run.events.emit("playground_status",
					"The playground is being created; it is usually ready in about 2 minutes.", "",
					onboardValue("cluster_id", clusterID),
					onboardValue("phase", status.Phase),
					onboardValue("message", trimmedOptional(status.StatusMessage)))
			}
		}
		return isSettled
	}

	spinnerFrame := 0
	drawSpinner := func() {
		if !run.interactive {
			return
		}
		phase := lastPhase
		if phase == "" {
			phase = "starting"
		}
		elapsed := time.Since(started).Truncate(time.Second)
		_, _ = fmt.Fprintf(run.out, "\r\033[K%s Creating your playground: %s (%s)",
			onboardSpinnerFrames[spinnerFrame%len(onboardSpinnerFrames)], phase, elapsed)
		spinnerFrame++
	}
	clearSpinner := func() {
		if run.interactive {
			_, _ = fmt.Fprint(run.out, "\r\033[K")
		}
	}

	if !check() {
		poll := time.NewTicker(onboardPlaygroundPollInterval)
		defer poll.Stop()
		deadline := time.NewTimer(onboardPlaygroundTimeout)
		defer deadline.Stop()
		var spin <-chan time.Time
		if run.interactive {
			spinner := time.NewTicker(onboardSpinnerInterval)
			defer spinner.Stop()
			spin = spinner.C
		}
		drawSpinner()
	waiting:
		for {
			select {
			case <-interrupted.Done():
				clearSpinner()
				run.reportPlaygroundUnsettled(clusterID, lastPhase, statusCommand, "Stopped waiting.")
				return
			case <-deadline.C:
				clearSpinner()
				run.reportPlaygroundUnsettled(clusterID, lastPhase, statusCommand,
					fmt.Sprintf("The playground is not ready after %s.", describeLoginTimeout(onboardPlaygroundTimeout)))
				return
			case <-spin:
				drawSpinner()
			case <-poll.C:
				if check() {
					break waiting
				}
			}
		}
	}
	clearSpinner()
	run.reportPlaygroundSettled(clusterID, latest, statusCommand)
}

func (run *onboardRun) reportPlaygroundUnsettled(clusterID string, phase string, statusCommand string, reason string) {
	run.note("Playground: %s, still being created", clusterID)
	if run.events != nil {
		run.events.emit("playground_status",
			reason+" The playground is still being created. Check it later with next_command.",
			statusCommand,
			onboardValue("cluster_id", clusterID),
			onboardValue("phase", phase),
			onboardValue("timed_out", true))
		return
	}
	_, _ = fmt.Fprintf(run.out, "%s The playground keeps being created. Check it with:\n  %s\n", reason, statusCommand)
}

func (run *onboardRun) reportPlaygroundSettled(clusterID string, status *client.PlaygroundStatus, statusCommand string) {
	message := trimmedOptional(status.StatusMessage)
	if status.Phase != "ready" {
		run.note("Playground: %s (%s)", clusterID, status.Phase)
		if run.events != nil {
			run.events.emit("playground_status",
				"The playground did not become ready (phase "+status.Phase+"). Tell the user; next_command shows its state.",
				statusCommand,
				onboardValue("cluster_id", clusterID),
				onboardValue("phase", status.Phase),
				onboardValue("message", message))
			return
		}
		_, _ = fmt.Fprintf(run.out, "The playground did not become ready: it is %s.", status.Phase)
		if message != "" {
			_, _ = fmt.Fprintf(run.out, " %s", message)
		}
		_, _ = fmt.Fprintf(run.out, "\nCheck it with: %s\n", statusCommand)
		return
	}

	trialEnds := ""
	if status.IsTrial && status.TrialExpiresAt != nil {
		trialEnds = strings.TrimSpace(*status.TrialExpiresAt)
	}
	// A paid order does not idle-expire, so its expires_at is not shown.
	idleExpiry := ""
	if status.Plan == nil || status.Plan.OrderedAt == nil {
		idleExpiry = strings.TrimSpace(status.ExpiresAt)
	}
	run.note("Playground: %s is ready", clusterID)
	if run.events != nil {
		instruction := "The playground is ready."
		if trialEnds != "" {
			instruction += " Tell the user it is a free trial that ends at " + trialEnds + "."
		} else if idleExpiry != "" {
			instruction += " Tell the user it expires after a period of inactivity (next at " + idleExpiry + ")."
		}
		run.events.emit("playground_status", instruction, "",
			onboardValue("cluster_id", clusterID),
			onboardValue("phase", status.Phase),
			onboardValue("trial_expires_at", trialEnds),
			onboardValue("expires_at", idleExpiry))
		return
	}
	_, _ = fmt.Fprintf(run.out, "%s Your playground is ready (cluster %s).\n", run.style.green("✓"), clusterID)
	if trialEnds != "" {
		_, _ = fmt.Fprintf(run.out, "  It is a free trial that ends %s.\n", formatPlaygroundDeadline(trialEnds, time.Now()))
	} else if idleExpiry != "" {
		_, _ = fmt.Fprintf(run.out, "  It expires after a period of inactivity, next at %s.\n", formatPlaygroundDeadline(idleExpiry, time.Now()))
	}
}

// onboardAssistant is one coding assistant as the agent_setup event shows it.
type onboardAssistant struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	SkillsInstalled bool   `json:"skills_installed"`
}

// setUpAgents offers the Ankra skills to the coding assistants found on this
// machine that do not have them yet, and the MCP connection to Claude Code.
func (run *onboardRun) setUpAgents() {
	if run.flags.noAgentSetup {
		return
	}
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		_, _ = fmt.Fprintf(run.errOut, "Warning: skipped the coding assistant setup: %v\n", homeError)
		return
	}
	detected := skills.DetectClients(home, skills.ScopePersonal)
	installedIDs := map[string]bool{}
	if installed, installedError := skillsInstalledClients(home); installedError == nil {
		for _, installedClient := range installed {
			installedIDs[installedClient.ID] = true
		}
	}
	var missing []skills.Client
	for _, detectedClient := range detected {
		if !installedIDs[detectedClient.ID] {
			missing = append(missing, detectedClient)
		}
	}
	skillsCommand := "ankra skills install"
	for _, missingClient := range missing {
		skillsCommand += " --client " + missingClient.ID
	}
	const mcpCommand = "ankra mcp connect --client " + mcpClientClaudeCode
	_, claudeLookError := lookMCPClientCommand("claude")
	isMCPOffered := claudeLookError == nil && !onboardMCPConnected(mcpClientClaudeCode)

	report := run.out
	if run.events != nil {
		report = run.errOut
	}
	isInstalledNow := false
	if len(missing) > 0 {
		isAccepted := run.flags.yes
		if !isAccepted && run.interactive {
			_, _ = fmt.Fprintln(run.out)
			_, _ = fmt.Fprintf(run.out, "Coding assistants on this machine: %s.\n", clientNames(detected))
			_, _ = fmt.Fprintln(run.out, "The Ankra skills teach them to run your Kubernetes work through Ankra.")
			isAccepted = run.confirmYes("Install the Ankra skills for " + clientNames(missing) + "? [Y/n] ")
		}
		if isAccepted {
			if installError := onboardInstallSkills(report, home, missing); installError != nil {
				_, _ = fmt.Fprintf(run.errOut, "Warning: installing the Ankra skills failed: %v\nRetry with: %s\n", installError, skillsCommand)
			} else {
				for _, installedClient := range missing {
					installedIDs[installedClient.ID] = true
				}
				run.note("Ankra skills installed for %s", clientNames(missing))
				missing = nil
				isInstalledNow = true
			}
		}
	}

	if isMCPOffered && run.interactive && !run.flags.yes {
		_, _ = fmt.Fprintln(run.out)
		_, _ = fmt.Fprintln(run.out, "Claude Code can also read your Ankra resources through the Ankra MCP server.")
		_, _ = fmt.Fprintln(run.out, "Connecting creates a read-only token for it.")
		if run.confirmYes("Connect Ankra to Claude Code? [Y/n] ") {
			if connectError := onboardRunMCPConnect(run.out, run.errOut); connectError != nil {
				_, _ = fmt.Fprintf(run.errOut, "Warning: connecting Claude Code failed: %v\nRetry with: %s\n", connectError, mcpCommand)
			} else {
				run.note("Claude Code connected to the Ankra MCP server")
				isMCPOffered = false
			}
		} else {
			isMCPOffered = false
			_, _ = fmt.Fprintln(run.out, "Skipped. Connect it later with: "+mcpCommand)
		}
	}

	var commands []string
	if len(missing) > 0 {
		commands = append(commands, skillsCommand)
	}
	if isMCPOffered {
		commands = append(commands, mcpCommand)
	}

	if run.events != nil {
		assistants := make([]onboardAssistant, 0, len(detected))
		for _, detectedClient := range detected {
			assistants = append(assistants, onboardAssistant{
				ID: detectedClient.ID, Name: detectedClient.DisplayName, SkillsInstalled: installedIDs[detectedClient.ID],
			})
		}
		instruction := ""
		switch {
		case len(detected) == 0:
			instruction = "No AI coding assistant was found on this machine, so there is nothing to set up here. " +
				"If the user uses one, `ankra skills install --client <id>` installs the Ankra skills for it."
		case len(missing) > 0:
			instruction = "Offer to install the Ankra skills for " + clientNames(missing) +
				": they teach the assistant to use Ankra. If the user agrees, run next_command."
		default:
			instruction = "The Ankra skills are installed for " + clientNames(detected) + "."
		}
		if isMCPOffered {
			instruction += " Claude Code can also connect to the Ankra MCP server with `" + mcpCommand +
				"`. It creates a read-only token, so ask the user first."
		}
		nextCommand := ""
		if len(commands) > 0 {
			nextCommand = commands[0]
		}
		run.events.emit("agent_setup", instruction, nextCommand,
			onboardValue("detected_clients", assistants),
			onboardValue("skills_installed_now", isInstalledNow),
			onboardValue("commands", append([]string{}, commands...)))
		return
	}

	switch {
	case len(detected) == 0:
		_, _ = fmt.Fprintln(run.out, "No coding assistants found on this machine. Install the Ankra skills later with: ankra skills install --client <id>")
	case len(missing) == 0 && !isInstalledNow:
		_, _ = fmt.Fprintf(run.out, "%s Ankra skills are installed for %s.\n", run.style.green("✓"), clientNames(detected))
	}
	if !run.interactive || run.flags.yes {
		if len(missing) > 0 {
			_, _ = fmt.Fprintf(run.out, "Install the Ankra skills for %s with: %s\n", clientNames(missing), skillsCommand)
		}
		if isMCPOffered {
			_, _ = fmt.Fprintf(run.out, "Connect Claude Code to the Ankra MCP server with: %s\n", mcpCommand)
		}
	}
}

func clientNames(clients []skills.Client) string {
	names := make([]string, 0, len(clients))
	for _, assistant := range clients {
		names = append(names, assistant.DisplayName)
	}
	return strings.Join(names, ", ")
}

// onboardMCPConnected reports whether `ankra mcp connect` already stored a
// token for the client.
func onboardMCPConnected(clientName string) bool {
	stored, readError := readMCPStoredToken(clientName)
	return readError == nil && stored != nil && strings.TrimSpace(stored.Token) != ""
}

// onboardInstallSkills is `ankra skills install --client <id>...` for the
// given assistants, reporting to out.
func onboardInstallSkills(out io.Writer, home string, clients []skills.Client) error {
	fsys, sourceError := skills.EmbeddedFS()
	if sourceError != nil {
		return sourceError
	}
	targets := make([]skills.Target, 0, len(clients))
	for _, assistant := range clients {
		target, targetError := skills.ResolveTarget(assistant, skills.ScopePersonal, home)
		if targetError != nil {
			return targetError
		}
		targets = append(targets, target)
	}
	if refusal := refusePersonalInstallUnderSudo(targets, os.Geteuid(), os.Getenv("SUDO_USER")); refusal != nil {
		return refusal
	}
	options := skillsInstallOptions{InstallOptions: skills.DefaultInstallOptions()}
	writtenDirectories := make(map[string]string)
	var failures []error
	for index, target := range targets {
		if index > 0 {
			_, _ = fmt.Fprintln(out)
		}
		if installError := installForTargetTo(out, fsys, target, nil, options, writtenDirectories); installError != nil {
			failures = append(failures, explainInstallFailure(target, installError))
		}
	}
	return errors.Join(failures...)
}

// finish prints what this run did and where to go next.
func (run *onboardRun) finish() {
	portalURL := strings.TrimRight(run.baseURL, "/") + "/next"
	nextCommands := []string{"ankra cluster list", `ankra chat "What can you help me with?"`}
	if run.events != nil {
		run.events.emit("done",
			"Ankra is set up. Tell the user what was done (summary) and suggest next_commands, or the Ankra web app at portal_url.",
			"",
			onboardValue("summary", append([]string{}, run.summary...)),
			onboardValue("portal_url", portalURL),
			onboardValue("next_commands", nextCommands))
		return
	}
	out := run.out
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, run.style.bold("You are set up."))
	for _, line := range run.summary {
		_, _ = fmt.Fprintf(out, "  - %s\n", line)
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Next steps:")
	_, _ = fmt.Fprintf(out, "  %-42s %s\n", nextCommands[0], run.style.dim("see your clusters"))
	_, _ = fmt.Fprintf(out, "  %-42s %s\n", nextCommands[1], run.style.dim("ask Ankra AI"))
	_, _ = fmt.Fprintf(out, "  %-42s %s\n", portalURL, run.style.dim("the Ankra web app"))
}

// chooseWithPrompt is the arrow-key picker onboard asks its questions with.
func chooseWithPrompt(in io.Reader, out io.Writer, label string, items []string, defaultIndex int) (int, error) {
	size := len(items)
	if size > 10 {
		size = 10
	}
	prompt := promptui.Select{
		Label: label,
		Items: items,
		Size:  size,
		Templates: &promptui.SelectTemplates{
			Label:    "{{ . }}",
			Active:   "> {{ . | cyan }}",
			Inactive: "  {{ . }}",
			Selected: "{{ . | faint }}",
		},
	}
	if file, isFile := in.(*os.File); isFile {
		prompt.Stdin = file
	}
	if file, isFile := out.(*os.File); isFile {
		prompt.Stdout = file
	}
	scroll := 0
	if defaultIndex >= size {
		scroll = defaultIndex - size + 1
	}
	index, _, promptError := prompt.RunCursorAt(defaultIndex, scroll)
	if promptError != nil {
		if errors.Is(promptError, promptui.ErrInterrupt) || errors.Is(promptError, promptui.ErrEOF) ||
			errors.Is(promptError, promptui.ErrAbort) {
			return 0, errCancelled
		}
		return 0, promptError
	}
	return index, nil
}
