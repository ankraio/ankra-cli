package cmd

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type loginInitResponse struct {
	AuthURL     string `json:"auth_url"`
	State       string `json:"state"`
	Auth0Domain string `json:"auth0_domain"`
	Ticket      string `json:"ticket"`
}

// loginPollRequest proves possession of the PKCE code verifier: the
// verifier never leaves this machine, and presenting it is what authorises
// collecting the token the browser login parked on the ticket.
type loginPollRequest struct {
	Ticket       string `json:"ticket"`
	CodeVerifier string `json:"code_verifier"`
}

type tokenExchangeResponse struct {
	Token       string `json:"token"`
	ExpiresAt   string `json:"expires_at"`
	TokenID     string `json:"token_id"`
	TokenName   string `json:"token_name"`
	MfaRequired bool   `json:"mfa_required"`
	Status      string `json:"status"`
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with the Ankra platform",
	Long: `Authenticate with the Ankra platform using your browser.

This command will:
1. Open your browser to the Ankra login page
2. After you authenticate, ask you to approve the sign-in in the browser
3. Complete the two-factor challenge in the browser, if your account has one
4. Save your credentials locally so you can use all ankra CLI commands

The whole login happens between your browser and the platform; the CLI
never opens a local network port.

Your credentials will be saved to ~/.ankra.yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := runLogin(); err != nil {
			return fmt.Errorf("login failed: %w", err)
		}
		return nil
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Revoke the login token and remove saved credentials",
	Long: `Revoke the saved login token on the platform and remove the saved
credentials from ~/.ankra.yaml.

Revocation is best-effort: when the platform cannot be reached the local
credentials are still cleared, with a warning that the token may remain
valid until it expires. Use --local-only to skip the revocation call
entirely (for example on an air-gapped machine).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		localOnly, _ := cmd.Flags().GetBool("local-only")
		configPath := getConfigPath()

		viper.SetConfigFile(configPath)
		if err := viper.ReadInConfig(); err != nil {
			_, _ = fmt.Fprintln(out, "No credentials found.")
			return nil
		}

		if !localOnly {
			revokeSavedTokenBestEffort(cmd)
		}

		// Remove token and base-url (reset to default)
		viper.Set("token", "")
		viper.Set("base-url", "")
		viper.Set("token_id", "")
		viper.Set("token_name", "")
		viper.Set("machine_id", "")

		// Same secure-write discipline as login: 0600 before and after the
		// write, so a pre-existing loose file never keeps loose permissions.
		if err := ensureSecureConfigFile(configPath); err != nil {
			return err
		}
		viper.SetConfigPermissions(0o600)
		if err := viper.WriteConfig(); err != nil {
			return fmt.Errorf("clearing credentials: %w", err)
		}
		if err := os.Chmod(configPath, 0o600); err != nil {
			return fmt.Errorf("secure config file: %w", err)
		}

		_, _ = fmt.Fprintln(out, "Logged out successfully.")
		_, _ = fmt.Fprintln(out, "Your credentials have been removed from", configPath)
		return nil
	},
}

// revokeSavedTokenBestEffort revokes the saved login token on the platform
// before the local credentials are cleared, so "logout" actually invalidates
// the token instead of leaving a live credential behind. It reads the token
// pair straight from the config file (not the global viper, where
// ANKRA_API_TOKEN can shadow the saved token and pair it with the wrong
// token_id). Failures only warn: logout must still succeed offline.
func revokeSavedTokenBestEffort(cmd *cobra.Command) {
	fileConfig := viper.New()
	fileConfig.SetConfigFile(getConfigPath())
	fileConfig.SetConfigType("yaml")
	if err := fileConfig.ReadInConfig(); err != nil {
		return
	}

	savedToken := strings.TrimSpace(fileConfig.GetString("token"))
	savedTokenID := strings.TrimSpace(fileConfig.GetString("token_id"))
	if savedToken == "" {
		return
	}
	if savedTokenID == "" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
			"Warning: no token_id saved for this login; the token cannot be revoked remotely and stays valid until it expires.")
		return
	}

	savedBaseURL := fileConfig.GetString("base-url")
	if savedBaseURL == "" {
		savedBaseURL = defaultBaseURL
	}
	normalizedBaseURL, err := client.NormalizeBaseURL(savedBaseURL, os.Getenv(envAllowInsecureHTTP) == "1")
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: could not revoke the token on the platform (invalid saved base URL: %v). It stays valid until it expires.\n", err)
		return
	}

	revocationClient := apiClient
	if revocationClient == nil {
		revocationClient = client.New(savedToken, normalizedBaseURL)
	}
	if _, err := revocationClient.RevokeAPIToken(savedTokenID); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"Warning: could not revoke the token on the platform: %v\nThe token stays valid until it expires. Revoke it from the dashboard or with `ankra tokens revoke %s`.\n",
			err, savedTokenID)
		return
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Revoked the login token on the platform.")
}

func init() {
	logoutCmd.Flags().Bool("local-only", false, "clear saved credentials without revoking the token on the platform")

	setRequiresAuth(loginCmd, false)
	setRequiresAuth(logoutCmd, false)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
}

func getConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ankra.yaml"
	}
	return filepath.Join(home, ".ankra.yaml")
}

// ensureSecureConfigFile creates `path` with 0600 permissions if it does
// not exist, or repairs the permissions of an existing file. It is a
// no-op when the file already has 0600.
func ensureSecureConfigFile(path string) error {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		// File exists - tighten permissions if they are loose.
		mode := info.Mode().Perm()
		if mode&0o077 != 0 {
			fmt.Fprintf(os.Stderr,
				"Warning: %s currently has permissions %#o; tightening to 0600.\n",
				path, mode)
			if err := os.Chmod(path, 0o600); err != nil {
				return fmt.Errorf("tighten config permissions: %w", err)
			}
		}
		return nil
	case errors.Is(err, os.ErrNotExist):
		f, createErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if createErr != nil {
			return fmt.Errorf("create config file: %w", createErr)
		}
		return f.Close()
	default:
		return fmt.Errorf("stat config file: %w", err)
	}
}

var loginHTTPClient = &http.Client{Timeout: 30 * time.Second}

func runLogin() error {
	fmt.Println("Ankra CLI Login")
	fmt.Println("───────────────")
	fmt.Println()

	// Generate the PKCE pair. The verifier stays on this machine: no
	// localhost callback server is opened, and the browser part of the
	// login runs entirely against the platform. The verifier is presented
	// on /login/poll to collect the token parked for this login attempt.
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return fmt.Errorf("generate code verifier: %w", err)
	}
	codeChallenge := generateCodeChallenge(codeVerifier)

	loginURL := viper.GetString("base-url")
	if loginURL == "" {
		loginURL = "https://platform.ankra.app"
	}

	machineID := getOrCreateMachineID()
	machineName, err := os.Hostname()
	if err != nil {
		machineName = ""
	}

	initURL := fmt.Sprintf("%s/api/v1/cli/login/init?code_challenge=%s&machine_id=%s&machine_name=%s&base_url=%s",
		strings.TrimRight(loginURL, "/"),
		url.QueryEscape(codeChallenge),
		url.QueryEscape(machineID),
		url.QueryEscape(machineName),
		url.QueryEscape(loginURL))

	resp, err := loginHTTPClient.Get(initURL)
	if err != nil {
		return fmt.Errorf("initialize login: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024))
		// A platform that predates the ticket flow still requires the
		// legacy redirect_uri parameter and answers 422 for its absence.
		if resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(string(body), "redirect_uri") {
			return fmt.Errorf("the platform does not support this CLI's login flow yet; try again in a few minutes")
		}
		return fmt.Errorf("initialize login failed: %s", apiErrorDetail(body))
	}

	var initResp loginInitResponse
	if err := json.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		return fmt.Errorf("parse login response: %w", err)
	}
	if initResp.Ticket == "" {
		return fmt.Errorf("the platform did not start a login session; it may not support this CLI version yet")
	}

	if initResp.Auth0Domain != "" {
		fmt.Printf("Using Auth0 domain: %s\n", initResp.Auth0Domain)
		fmt.Println()
	}

	fmt.Println("Opening browser for authentication...")
	fmt.Println()
	fmt.Println("If the browser doesn't open, visit this URL:")
	fmt.Println()
	fmt.Printf("  %s\n", initResp.AuthURL)
	fmt.Println()

	if err := openBrowser(initResp.AuthURL); err != nil {
		fmt.Println("Could not open browser automatically. Please open the URL above manually.")
	}

	fmt.Println()
	fmt.Println("Sign in and approve the login in your browser.")
	fmt.Println("(Press Ctrl+C to cancel)")
	fmt.Println()

	tokenData, err := pollForToken(loginURL, initResp.Ticket, codeVerifier)
	if err != nil {
		return err
	}

	// Never persist an empty token. Older CLIs silently wrote "" to
	// ~/.ankra.yaml when the platform withheld the token (e.g. for a
	// two-factor step-up this CLI version did not understand) and then
	// reported "Login successful!", leaving every subsequent command
	// failing with "not logged in".
	if err := ensureTokenIssued(tokenData); err != nil {
		return err
	}

	configPath := getConfigPath()

	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	// Create the file with 0600 permissions before viper writes to it.
	// Viper's WriteConfig leaves the existing permissions in place on a
	// pre-existing file, so we need the file to be 0600 *before* the
	// token lands in it. The Chmod after WriteConfig acts as a belt-
	// and-suspenders for the rare case where the file already exists
	// with looser permissions.
	if err := ensureSecureConfigFile(configPath); err != nil {
		return err
	}

	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")
	_ = viper.ReadInConfig()

	viper.Set("token", tokenData.Token)
	viper.Set("base-url", loginURL)
	viper.Set("machine_id", machineID)
	if tokenData.TokenID != "" {
		viper.Set("token_id", tokenData.TokenID)
	}
	if tokenData.TokenName != "" {
		viper.Set("token_name", tokenData.TokenName)
	}

	// Second belt: if viper has to create the file (SafeWriteConfig path),
	// have it do so with 0600 rather than its default so the token never
	// briefly lands in a world-readable file.
	viper.SetConfigPermissions(0o600)

	if err := viper.WriteConfig(); err != nil {
		if safeErr := viper.SafeWriteConfig(); safeErr != nil {
			return fmt.Errorf("save config: %w", err)
		}
	}

	if err := os.Chmod(configPath, 0o600); err != nil {
		return fmt.Errorf("secure config file: %w", err)
	}

	fmt.Println()
	fmt.Println("✓ Login successful!")
	fmt.Println()
	fmt.Printf("  Credentials saved to: %s\n", configPath)
	if tokenData.TokenName != "" {
		fmt.Printf("  Token name: %s\n", tokenData.TokenName)
	}
	fmt.Printf("  Token expires: %s\n", formatExpiry(tokenData.ExpiresAt))
	fmt.Println()
	fmt.Println("You can now use ankra CLI commands. Try:")
	fmt.Println("  ankra cluster list")
	fmt.Println()

	return nil
}

func ensureTokenIssued(tokenData tokenExchangeResponse) error {
	if strings.TrimSpace(tokenData.Token) != "" {
		return nil
	}
	if tokenData.MfaRequired {
		return fmt.Errorf("two-factor authentication was not completed; run `ankra login` again")
	}
	return fmt.Errorf("the platform did not issue an API token; saved credentials were left unchanged. Upgrade the CLI (`ankra upgrade`) and run `ankra login` again")
}

// pollForToken collects the token the browser login parks on the ticket.
// The browser drives everything user-facing (sign-in, the approval click,
// and any two-factor challenge); this loop just waits for the release and
// prints a hint when the login is blocked on a browser step.
func pollForToken(loginURL string, ticket string, codeVerifier string) (tokenExchangeResponse, error) {
	pollURL := fmt.Sprintf("%s/api/v1/cli/login/poll", strings.TrimRight(loginURL, "/"))
	requestBody, _ := json.Marshal(loginPollRequest{Ticket: ticket, CodeVerifier: codeVerifier})

	deadline := time.After(10 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	printedHints := map[string]bool{}
	for {
		select {
		case <-deadline:
			return tokenExchangeResponse{}, fmt.Errorf("login timed out after 10 minutes")
		case <-ticker.C:
			polled, done, err := pollLoginOnce(pollURL, requestBody, printedHints)
			if err != nil {
				return tokenExchangeResponse{}, err
			}
			if done {
				return polled, nil
			}
		}
	}
}

func pollLoginOnce(pollURL string, requestBody []byte, printedHints map[string]bool) (tokenExchangeResponse, bool, error) {
	resp, err := loginHTTPClient.Post(pollURL, "application/json", strings.NewReader(string(requestBody)))
	if err != nil {
		// Transient network error - keep polling.
		return tokenExchangeResponse{}, false, nil
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode >= http.StatusInternalServerError {
		// Server hiccup - keep polling rather than failing the whole login.
		return tokenExchangeResponse{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		// Any 4xx is final: the session expired (410), the verifier was
		// refused (403), or this CLI must be upgraded (426). Retrying
		// cannot fix it, so surface the platform's message immediately.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024))
		return tokenExchangeResponse{}, false, fmt.Errorf("login failed: %s", apiErrorDetail(body))
	}

	var polled tokenExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&polled); err != nil {
		return tokenExchangeResponse{}, false, nil
	}
	if polled.Token != "" {
		return polled, true, nil
	}

	hint := ""
	switch {
	case polled.MfaRequired || polled.Status == "mfa_pending":
		hint = "Two-factor authentication required. Complete the challenge in your browser..."
	case polled.Status == "awaiting_approval":
		hint = "Approve the sign-in in your browser to continue..."
	}
	if hint != "" && !printedHints[hint] {
		printedHints[hint] = true
		fmt.Println(hint)
	}
	return tokenExchangeResponse{}, false, nil
}

// apiErrorDetail extracts the {"detail": "..."} message the platform sends
// with error statuses, falling back to the raw (truncated) body.
func apiErrorDetail(body []byte) string {
	var detailPayload struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &detailPayload); err == nil && detailPayload.Detail != "" {
		return detailPayload.Detail
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		return "the platform returned an error without details"
	}
	if len(message) > 500 {
		message = message[:500] + "... (truncated)"
	}
	return message
}

func generateCodeVerifier() (string, error) {
	// Generate 32 random bytes
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// Base64 URL encode without padding
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateCodeChallenge(verifier string) string {
	// SHA256 hash of the verifier
	h := sha256.Sum256([]byte(verifier))
	// Base64 URL encode without padding
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func openBrowser(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("refusing to open non-HTTP URL: %s", rawURL)
	}

	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{rawURL}
	case "linux":
		cmd = "xdg-open"
		args = []string{rawURL}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", rawURL}
	default:
		return fmt.Errorf("unsupported platform")
	}

	return exec.Command(cmd, args...).Start()
}

func formatExpiry(expiresAt string) string {
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return expiresAt
	}
	return t.Format("January 2, 2006")
}

// getOrCreateMachineID returns the machine ID for this host. It is read-only:
// when a machine_id is already saved it is returned as-is, otherwise the ID is
// derived in memory and returned WITHOUT writing anything to disk. Persisting
// it is the caller's job (see the save block in runLogin), which only happens
// after ensureSecureConfigFile has created the config file with 0600
// permissions. Writing here previously created ~/.ankra.yaml world-readable
// with the ANKRA_API_TOKEN env value bound into the global viper, leaking the
// token if login then failed or was cancelled.
func getOrCreateMachineID() string {
	configPath := getConfigPath()
	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")
	_ = viper.ReadInConfig()

	machineID := viper.GetString("machine_id")
	if machineID != "" {
		return machineID
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	currentUser, err := user.Current()
	username := "unknown"
	if err == nil {
		username = currentUser.Username
	}

	rawID := fmt.Sprintf("%s-%s", sanitizeIdentifier(hostname), sanitizeIdentifier(username))
	hash := sha256.Sum256([]byte(rawID))
	machineID = hex.EncodeToString(hash[:16])

	return machineID
}

func sanitizeIdentifier(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, ".", "-")
	result := ""
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result += string(r)
		}
	}
	if len(result) > 20 {
		result = result[:20]
	}
	return result
}
