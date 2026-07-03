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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type loginInitResponse struct {
	AuthURL     string `json:"auth_url"`
	State       string `json:"state"`
	Auth0Domain string `json:"auth0_domain"`
}

type tokenExchangeRequest struct {
	Code         string `json:"code"`
	State        string `json:"state"`
	CodeVerifier string `json:"code_verifier"`
	MachineID    string `json:"machine_id,omitempty"`
}

type tokenExchangeResponse struct {
	Token        string `json:"token"`
	ExpiresAt    string `json:"expires_at"`
	TokenID      string `json:"token_id"`
	TokenName    string `json:"token_name"`
	MfaRequired  bool   `json:"mfa_required"`
	MfaTicket    string `json:"mfa_ticket"`
	ChallengeURL string `json:"challenge_url"`
}

type mfaPollRequest struct {
	Ticket string `json:"ticket"`
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with the Ankra platform",
	Long: `Authenticate with the Ankra platform using your browser.

This command will:
1. Open your browser to the Ankra login page
2. After you authenticate, save your credentials locally
3. You can then use all ankra CLI commands

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
	Short: "Remove saved credentials",
	Long:  `Remove saved credentials from ~/.ankra.yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath := getConfigPath()

		// Read existing config
		viper.SetConfigFile(configPath)
		if err := viper.ReadInConfig(); err != nil {
			fmt.Println("No credentials found.")
			return nil
		}

		// Remove token and base-url (reset to default)
		viper.Set("token", "")
		viper.Set("base-url", "")
		viper.Set("token_id", "")
		viper.Set("token_name", "")
		viper.Set("machine_id", "")

		if err := viper.WriteConfig(); err != nil {
			return fmt.Errorf("clearing credentials: %w", err)
		}

		fmt.Println("Logged out successfully.")
		fmt.Println("Your credentials have been removed from", configPath)
		return nil
	},
}

func init() {
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

	// Generate PKCE code verifier and challenge
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return fmt.Errorf("generate code verifier: %w", err)
	}
	codeChallenge := generateCodeChallenge(codeVerifier)

	// Find an available port for the callback server. Bind to the IPv4
	// loopback explicitly and advertise the same 127.0.0.1 literal in the
	// redirect URI. Using "localhost" here is unsafe: on dual-stack hosts it
	// resolves to both 127.0.0.1 and ::1, so a browser that picks the IPv6
	// address reaches nothing (the server only listens on IPv4) and the login
	// callback silently fails. RFC 8252 §8.3 recommends the loopback IP literal.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// Initialize login with the backend
	loginURL := viper.GetString("base-url")
	if loginURL == "" {
		loginURL = "https://platform.ankra.app"
	}

	initURL := fmt.Sprintf("%s/api/v1/cli/login/init?redirect_uri=%s&code_challenge=%s&base_url=%s",
		strings.TrimRight(loginURL, "/"),
		url.QueryEscape(callbackURL),
		url.QueryEscape(codeChallenge),
		url.QueryEscape(loginURL))

	resp, err := loginHTTPClient.Get(initURL)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("initialize login: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		_ = listener.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024))
		msg := string(body)
		if len(msg) > 500 {
			msg = msg[:500] + "... (truncated)"
		}
		return fmt.Errorf("initialize login failed: %s", msg)
	}

	var initResp loginInitResponse
	if err := json.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		_ = listener.Close()
		return fmt.Errorf("parse login response: %w", err)
	}

	if initResp.Auth0Domain != "" {
		fmt.Printf("Using Auth0 domain: %s\n", initResp.Auth0Domain)
		fmt.Println()
	}

	// Channel to receive the authorization code
	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	// Start the callback server
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}

			code := r.URL.Query().Get("code")
			state := r.URL.Query().Get("state")

			if state != initResp.State {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, "Invalid state parameter")
				errChan <- fmt.Errorf("state mismatch")
				return
			}

			if code == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, "Missing authorization code")
				errChan <- fmt.Errorf("missing code")
				return
			}

			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, successHTML)

			codeChan <- code
		}),
	}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Open browser
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
	fmt.Println("Waiting for authentication...")
	fmt.Println("(Press Ctrl+C to cancel)")
	fmt.Println()

	// Wait for callback or timeout
	var authCode string
	select {
	case authCode = <-codeChan:
		// Success
	case err := <-errChan:
		_ = server.Close()
		return fmt.Errorf("callback error: %w", err)
	case <-time.After(10 * time.Minute):
		_ = server.Close()
		return fmt.Errorf("login timed out after 10 minutes")
	}

	_ = server.Close()

	// Exchange code for token
	fmt.Println("Exchanging authorization code for token...")

	// Generate or retrieve machine ID
	machineID := getOrCreateMachineID()

	tokenReq := tokenExchangeRequest{
		Code:         authCode,
		State:        initResp.State,
		CodeVerifier: codeVerifier,
		MachineID:    machineID,
	}

	tokenReqBody, _ := json.Marshal(tokenReq)
	tokenURL := fmt.Sprintf("%s/api/v1/cli/login/token", strings.TrimRight(loginURL, "/"))

	tokenResp, err := loginHTTPClient.Post(tokenURL, "application/json", strings.NewReader(string(tokenReqBody)))
	if err != nil {
		return fmt.Errorf("exchange token: %w", err)
	}
	defer func() {
		if closeErr := tokenResp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if tokenResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(tokenResp.Body, 10*1024))
		msg := string(body)
		if len(msg) > 500 {
			msg = msg[:500] + "... (truncated)"
		}
		return fmt.Errorf("token exchange failed: %s", msg)
	}

	var tokenData tokenExchangeResponse
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenData); err != nil {
		return fmt.Errorf("parse token response: %w", err)
	}

	// When the account has a second factor, the backend withholds the token and
	// hands back an MFA ticket plus a browser URL where the user completes the
	// passkey / authenticator / recovery-code challenge. We open that URL and
	// poll until the step-up succeeds and the token is released.
	if tokenData.MfaRequired {
		completed, err := completeMFAChallenge(loginURL, tokenData)
		if err != nil {
			return err
		}
		tokenData = completed
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

func completeMFAChallenge(loginURL string, pending tokenExchangeResponse) (tokenExchangeResponse, error) {
	fmt.Println()
	fmt.Println("Two-factor authentication required.")
	fmt.Println("Complete the second step in your browser:")
	fmt.Println()
	fmt.Printf("  %s\n", pending.ChallengeURL)
	fmt.Println()

	if err := openBrowser(pending.ChallengeURL); err != nil {
		fmt.Println("Could not open browser automatically. Please open the URL above manually.")
	}

	fmt.Println("Waiting for you to complete two-factor authentication...")
	fmt.Println("(Press Ctrl+C to cancel)")
	fmt.Println()

	pollURL := fmt.Sprintf("%s/api/v1/cli/login/mfa/poll", strings.TrimRight(loginURL, "/"))
	requestBody, _ := json.Marshal(mfaPollRequest{Ticket: pending.MfaTicket})

	deadline := time.After(10 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return tokenExchangeResponse{}, fmt.Errorf("two-factor authentication timed out after 10 minutes")
		case <-ticker.C:
			polled, done, err := pollMFAToken(pollURL, requestBody)
			if err != nil {
				return tokenExchangeResponse{}, err
			}
			if done {
				return polled, nil
			}
		}
	}
}

func pollMFAToken(pollURL string, requestBody []byte) (tokenExchangeResponse, bool, error) {
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

	if resp.StatusCode == http.StatusGone {
		return tokenExchangeResponse{}, false, fmt.Errorf("login session expired; run 'ankra login' again")
	}
	if resp.StatusCode != http.StatusOK {
		// Server hiccup - keep polling rather than failing the whole login.
		return tokenExchangeResponse{}, false, nil
	}

	var polled tokenExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&polled); err != nil {
		return tokenExchangeResponse{}, false, nil
	}
	if polled.Token != "" {
		return polled, true, nil
	}
	// Still awaiting the second factor.
	return tokenExchangeResponse{}, false, nil
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

const successHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Login Successful - Ankra CLI</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            background: #1f2937;
            color: #fff;
        }
        .container {
            text-align: center;
            padding: 40px;
        }
        .logo-wrapper {
            position: relative;
            display: inline-block;
            margin-bottom: 32px;
        }
        .logo-glow {
            position: absolute;
            inset: -20px;
            background: rgba(139, 92, 246, 0.3);
            border-radius: 50%;
            filter: blur(30px);
            animation: pulse 2s ease-in-out infinite;
        }
        @keyframes pulse {
            0%, 100% { opacity: 0.5; transform: scale(1); }
            50% { opacity: 0.8; transform: scale(1.1); }
        }
        .logo {
            position: relative;
            z-index: 1;
            width: 64px;
            height: 64px;
        }
        .card {
            background: #374151;
            border: 1px solid #4b5563;
            border-radius: 16px;
            padding: 40px 48px;
            max-width: 420px;
            box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
        }
        .success-icon {
            width: 56px;
            height: 56px;
            background: linear-gradient(135deg, #10b981 0%, #059669 100%);
            border-radius: 50%;
            display: flex;
            align-items: center;
            justify-content: center;
            margin: 0 auto 24px;
            box-shadow: 0 10px 40px rgba(16, 185, 129, 0.3);
        }
        .success-icon svg {
            width: 28px;
            height: 28px;
            color: white;
        }
        h1 {
            font-size: 24px;
            font-weight: 700;
            color: #fff;
            margin-bottom: 12px;
        }
        .subtitle {
            font-size: 16px;
            color: #9ca3af;
            margin-bottom: 24px;
            line-height: 1.5;
        }
        .config-path {
            background: #1f2937;
            border: 1px solid #4b5563;
            border-radius: 8px;
            padding: 12px 16px;
            font-size: 13px;
            color: #9ca3af;
        }
        .config-path code {
            color: #a78bfa;
            font-family: 'SF Mono', Monaco, 'Courier New', monospace;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="logo-wrapper">
            <div class="logo-glow"></div>
            <img src="https://platform.ankra.app/w-logo.png" alt="Ankra" class="logo">
        </div>
        <div class="card">
            <div class="success-icon">
                <svg fill="none" stroke="currentColor" stroke-width="3" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7"></path>
                </svg>
            </div>
            <h1>Login Successful!</h1>
            <p class="subtitle">You can close this window and return to your terminal.</p>
            <div class="config-path">
                Your credentials have been saved to <code>~/.ankra.yaml</code>
            </div>
        </div>
    </div>
</body>
</html>`
