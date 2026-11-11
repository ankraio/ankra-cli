package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The sign-in step of `ankra onboard` when nobody is at a terminal: a coding
// agent, CI, or the installer piped into sh. It cannot wait for a person it
// cannot talk to, so it starts the browser sign-in, saves what is needed to
// finish it in ~/.ankra/onboard-login.json, prints the link and exits.
// `ankra onboard --wait` then collects the token from that saved sign-in.
//
// The saved sign-in is never replaced while it is still valid (only --new
// does that), so an agent that runs the command twice does not hand the
// person a second link that makes the first one useless.

// onboardPendingLoginLifetime is how long a saved sign-in is offered again.
// The platform keeps a login ticket for the same ten minutes.
const onboardPendingLoginLifetime = 10 * time.Minute

// onboardPendingLogin is the saved sign-in. The code verifier is what proves
// this machine started it, so the file is readable only by its owner.
type onboardPendingLogin struct {
	BaseURL      string    `json:"base_url"`
	MachineID    string    `json:"machine_id"`
	Ticket       string    `json:"ticket"`
	CodeVerifier string    `json:"code_verifier"`
	AuthURL      string    `json:"auth_url"`
	UserCode     string    `json:"user_code,omitempty"`
	Signup       bool      `json:"signup,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func onboardPendingLoginPath() (string, error) {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return "", homeError
	}
	return filepath.Join(home, ".ankra", "onboard-login.json"), nil
}

// loadOnboardPendingLogin returns the saved sign-in, or nil when there is
// none or the file cannot be used (a file that does not parse is as good as
// absent: the next step replaces it).
func loadOnboardPendingLogin() *onboardPendingLogin {
	path, pathError := onboardPendingLoginPath()
	if pathError != nil {
		return nil
	}
	content, readError := os.ReadFile(path)
	if readError != nil {
		return nil
	}
	var pending onboardPendingLogin
	if decodeError := json.Unmarshal(content, &pending); decodeError != nil {
		return nil
	}
	if pending.Ticket == "" || pending.CodeVerifier == "" || pending.AuthURL == "" || pending.ExpiresAt.IsZero() {
		return nil
	}
	return &pending
}

// saveOnboardPendingLogin writes the file through a temporary file created
// 0600 in the same directory, so the verifier is never readable by anyone
// else, not even while it is being written, and a reader never sees half a
// file.
func saveOnboardPendingLogin(pending *onboardPendingLogin) error {
	path, pathError := onboardPendingLoginPath()
	if pathError != nil {
		return pathError
	}
	directory := filepath.Dir(path)
	if mkdirError := os.MkdirAll(directory, 0o700); mkdirError != nil {
		return fmt.Errorf("create %s: %w", directory, mkdirError)
	}
	content, encodeError := json.MarshalIndent(pending, "", "  ")
	if encodeError != nil {
		return encodeError
	}
	temporary, createError := os.CreateTemp(directory, ".onboard-login-*.json")
	if createError != nil {
		return fmt.Errorf("save the pending sign-in: %w", createError)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if chmodError := temporary.Chmod(0o600); chmodError != nil {
		_ = temporary.Close()
		return fmt.Errorf("save the pending sign-in: %w", chmodError)
	}
	if _, writeError := temporary.Write(append(content, '\n')); writeError != nil {
		_ = temporary.Close()
		return fmt.Errorf("save the pending sign-in: %w", writeError)
	}
	if closeError := temporary.Close(); closeError != nil {
		return fmt.Errorf("save the pending sign-in: %w", closeError)
	}
	if renameError := os.Rename(temporaryPath, path); renameError != nil {
		return fmt.Errorf("save the pending sign-in: %w", renameError)
	}
	return nil
}

func removeOnboardPendingLogin() {
	if path, pathError := onboardPendingLoginPath(); pathError == nil {
		_ = os.Remove(path)
	}
}

// signInWithLink is the sign-in without a terminal. It returns true once the
// person is signed in (only with --wait), false when the link is waiting for
// them.
func (run *onboardRun) signInWithLink() (bool, error) {
	platformURL, resolveError := resolveLoginBaseURL()
	if resolveError != nil {
		return false, resolveError
	}
	pending := loadOnboardPendingLogin()
	if pending != nil && pending.BaseURL != platformURL {
		// Started against another platform (a different --base-url): it
		// cannot sign in to this one.
		pending = nil
	}
	if pending != nil && !run.flags.wait && !time.Now().Before(pending.ExpiresAt) {
		// Expired and nobody is waiting on it: hand out a new link.
		pending = nil
	}
	if pending != nil && !run.flags.wait && (run.flags.signup || run.flags.login) && pending.Signup != run.flags.signup {
		// An explicit --signup or --login that the saved link was not started
		// with: hand out a link that opens the page asked for. With --wait the
		// saved link stays, because it is the one the person already has.
		pending = nil
	}

	isFresh := false
	if pending == nil {
		session, startError := startLoginSession(loginOptions{Signup: run.flags.signup, Source: "onboard"})
		if startError != nil {
			return false, startError
		}
		now := time.Now().UTC()
		pending = &onboardPendingLogin{
			BaseURL:      session.BaseURL,
			MachineID:    session.MachineID,
			Ticket:       session.Ticket,
			CodeVerifier: session.CodeVerifier,
			AuthURL:      session.AuthURL,
			UserCode:     session.UserCode,
			Signup:       run.flags.signup,
			CreatedAt:    now,
			ExpiresAt:    now.Add(onboardPendingLoginLifetime),
		}
		if saveError := saveOnboardPendingLogin(pending); saveError != nil {
			return false, saveError
		}
		isFresh = true
	}

	if !run.flags.wait {
		run.reportLoginLink(pending, isFresh, false)
		return false, nil
	}
	if isFresh {
		run.reportLoginLink(pending, true, true)
	}
	return run.waitForPendingLogin(pending)
}

// reportLoginLink shows the link the person must open.
func (run *onboardRun) reportLoginLink(pending *onboardPendingLogin, isFresh bool, isWaiting bool) {
	expiresAt := pending.ExpiresAt.UTC().Format(time.RFC3339)
	if run.events != nil {
		instruction := "Ask the user to open this URL and sign in or create a free Ankra account with GitHub, Google or email."
		if pending.UserCode != "" {
			instruction += " If the page shows a code it must match " + pending.UserCode + "."
		}
		nextCommand := ""
		if isWaiting {
			instruction += " This command waits for the approval, saves the session and then continues."
		} else {
			instruction += " Then run next_command: it waits for the approval and saves the session. Re-running it never replaces this link."
			nextCommand = "ankra onboard --wait --json"
		}
		run.events.emit("login_url", instruction, nextCommand,
			onboardValue("url", pending.AuthURL),
			onboardValue("user_code", pending.UserCode),
			onboardValue("expires_at", expiresAt),
			onboardValue("reused", !isFresh))
		return
	}
	out := run.out
	_, _ = fmt.Fprintln(out, "Sign in to Ankra, or create a free account, by opening this link:")
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "  %s\n", pending.AuthURL)
	_, _ = fmt.Fprintln(out)
	if pending.UserCode != "" {
		_, _ = fmt.Fprintf(out, "Your code: %s (the browser shows the same code)\n", pending.UserCode)
	}
	_, _ = fmt.Fprintf(out, "The link works until %s.\n", formatPlaygroundDeadline(expiresAt, time.Now()))
	if isWaiting {
		return
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Then finish setting up with:")
	_, _ = fmt.Fprintln(out, "  ankra onboard --wait")
}

// waitForPendingLogin polls the saved sign-in until the browser releases the
// token or the link expires. The first poll runs at once, so a link that was
// approved before this command started is collected without a delay, even
// when its ten minutes are already up.
func (run *onboardRun) waitForPendingLogin(pending *onboardPendingLogin) (bool, error) {
	hints := run.out
	if run.events != nil {
		hints = run.errOut
	} else {
		_, _ = fmt.Fprintln(run.out, "Waiting for the sign-in to be approved in the browser...")
	}
	pollURL := strings.TrimRight(pending.BaseURL, "/") + "/api/v1/cli/login/poll"
	requestBody, _ := json.Marshal(loginPollRequest{Ticket: pending.Ticket, CodeVerifier: pending.CodeVerifier})
	printedHints := map[string]bool{}
	for {
		tokenData, isDone, pollError := pollLoginOnce(hints, pollURL, requestBody, printedHints)
		if pollError != nil {
			var refused *loginRefusedError
			if errors.As(pollError, &refused) && refused.statusCode == http.StatusGone {
				return false, run.pendingLoginExpired()
			}
			// A refused verifier or a CLI that must be upgraded: the saved
			// sign-in can never succeed, so it goes.
			removeOnboardPendingLogin()
			return false, pollError
		}
		if isDone {
			removeOnboardPendingLogin()
			if issuedError := ensureTokenIssued(tokenData); issuedError != nil {
				return false, issuedError
			}
			return true, run.completeSignIn(pending.BaseURL, pending.MachineID, tokenData)
		}
		remaining := time.Until(pending.ExpiresAt)
		if remaining <= 0 {
			return false, run.pendingLoginExpired()
		}
		time.Sleep(min(loginPollInterval, remaining))
	}
}

// pendingLoginExpired drops the saved sign-in and says how to start again.
func (run *onboardRun) pendingLoginExpired() error {
	removeOnboardPendingLogin()
	return &onboardStepError{
		err: withExitCode(exitWaitTimeout, errors.New(
			"the sign-in link expired before it was approved; start a new one with: "+run.command("ankra onboard"))),
		instruction: "The sign-in link expired before the user approved it. Run next_command to get a new link, then ask the user to open it.",
		nextCommand: run.command("ankra onboard"),
	}
}
