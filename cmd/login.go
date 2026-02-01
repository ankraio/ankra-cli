package cmd

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	TokenID   string `json:"token_id"`
	TokenName string `json:"token_name"`
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
	Run: func(cmd *cobra.Command, args []string) {
		if err := runLogin(); err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			os.Exit(1)
		}
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove saved credentials",
	Long:  `Remove saved credentials from ~/.ankra.yaml`,
	Run: func(cmd *cobra.Command, args []string) {
		configPath := getConfigPath()

		// Read existing config
		viper.SetConfigFile(configPath)
		if err := viper.ReadInConfig(); err != nil {
			fmt.Println("No credentials found.")
			return
		}

		// Remove token and base-url (reset to default)
		viper.Set("token", "")
		viper.Set("base-url", "")
		viper.Set("token_id", "")
		viper.Set("token_name", "")

		if err := viper.WriteConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Error clearing credentials: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Logged out successfully.")
		fmt.Println("Your credentials have been removed from", configPath)
	},
}

func init() {
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

	// Find an available port for the callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	callbackURL := fmt.Sprintf("http://localhost:%d/callback", port)

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

	resp, err := http.Get(initURL)
	if err != nil {
		listener.Close()
		return fmt.Errorf("initialize login: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		listener.Close()
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("initialize login failed: %s", string(body))
	}

	var initResp loginInitResponse
	if err := json.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		listener.Close()
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
				fmt.Fprint(w, "Invalid state parameter")
				errChan <- fmt.Errorf("state mismatch")
				return
			}

			if code == "" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, "Missing authorization code")
				errChan <- fmt.Errorf("missing code")
				return
			}

			// Return success page
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, successHTML)

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
		server.Close()
		return fmt.Errorf("callback error: %w", err)
	case <-time.After(5 * time.Minute):
		server.Close()
		return fmt.Errorf("login timed out after 5 minutes")
	}

	server.Close()

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

	tokenResp, err := http.Post(tokenURL, "application/json", strings.NewReader(string(tokenReqBody)))
	if err != nil {
		return fmt.Errorf("exchange token: %w", err)
	}
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tokenResp.Body)
		return fmt.Errorf("token exchange failed: %s", string(body))
	}

	var tokenData tokenExchangeResponse
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenData); err != nil {
		return fmt.Errorf("parse token response: %w", err)
	}

	// Save token to config
	configPath := getConfigPath()

	// Ensure config directory exists
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")

	// Try to read existing config
	_ = viper.ReadInConfig()

	// Set the token and metadata
	viper.Set("token", tokenData.Token)
	viper.Set("base-url", loginURL)
	if tokenData.TokenID != "" {
		viper.Set("token_id", tokenData.TokenID)
	}
	if tokenData.TokenName != "" {
		viper.Set("token_name", tokenData.TokenName)
	}

	if err := viper.WriteConfig(); err != nil {
		// If config doesn't exist, create it
		if err := viper.SafeWriteConfig(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
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

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
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

	machineID = fmt.Sprintf("%s-%s", sanitizeIdentifier(hostname), sanitizeIdentifier(username))

	viper.Set("machine_id", machineID)
	_ = viper.WriteConfig()

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
