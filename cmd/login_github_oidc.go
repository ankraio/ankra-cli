package cmd

// `ankra login --github-oidc` (ankra-4pigj8): inside a GitHub Actions job,
// request the job's OIDC ID token with the Ankra audience, exchange it for a
// short-lived Ankra token and save it for the run. No static token is stored
// anywhere.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	envActionsIDTokenRequestURL   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	envActionsIDTokenRequestToken = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
	maxIDTokenResponseBytes       = 64 * 1024
)

// requestGitHubIDToken asks the Actions runtime for an ID token carrying
// audience. The bearer is the runner's own request token.
func requestGitHubIDToken(ctx context.Context, httpClient *http.Client, requestURL string,
	requestToken string, audience string) (string, error) {
	parsed, parseError := url.Parse(requestURL)
	if parseError != nil || (parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1") {
		return "", errors.New("ACTIONS_ID_TOKEN_REQUEST_URL is not an https URL")
	}
	query := parsed.Query()
	query.Set("audience", audience)
	parsed.RawQuery = query.Encode()
	request, requestError := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if requestError != nil {
		return "", requestError
	}
	request.Header.Set("Authorization", "Bearer "+requestToken)
	request.Header.Set("Accept", "application/json; api-version=2.0")
	response, sendError := httpClient.Do(request)
	if sendError != nil {
		return "", fmt.Errorf("request the GitHub ID token: %w", sendError)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxIDTokenResponseBytes))
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub refused the ID token request (status %d); does the job have 'permissions: id-token: write'?",
			response.StatusCode)
	}
	var parsedBody struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(body, &parsedBody) != nil || parsedBody.Value == "" {
		return "", errors.New("GitHub answered without an ID token")
	}
	return parsedBody.Value, nil
}

var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func resolveOIDCOrganisationID(cmd *cobra.Command) (string, error) {
	value, _ := cmd.Flags().GetString("organisation-id")
	if value == "" {
		value = organisationOverrideValue(cmd)
	}
	if !uuidShape.MatchString(value) {
		return "", withExitCode(exitUsage, errors.New(
			"--github-oidc needs the organisation id (a UUID): pass --organisation-id <id> or set ANKRA_ORG to it"))
	}
	return value, nil
}

func runGitHubOIDCLogin(cmd *cobra.Command) error {
	requestURL := os.Getenv(envActionsIDTokenRequestURL)
	requestToken := os.Getenv(envActionsIDTokenRequestToken)
	if requestURL == "" || requestToken == "" {
		return withExitCode(exitUsage, errors.New(
			"--github-oidc only works inside a GitHub Actions job with 'permissions: id-token: write' "+
				"(ACTIONS_ID_TOKEN_REQUEST_URL and ACTIONS_ID_TOKEN_REQUEST_TOKEN are not set)"))
	}
	organisationID, orgError := resolveOIDCOrganisationID(cmd)
	if orgError != nil {
		return orgError
	}
	audience, _ := cmd.Flags().GetString("audience")
	if audience == "" {
		audience = client.GitHubOIDCAudience
	}
	baseURL := viper.GetString("base-url")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL, normaliseError := client.NormalizeBaseURL(baseURL, os.Getenv(envAllowInsecureHTTP) == "1")
	if normaliseError != nil {
		return fmt.Errorf("invalid base URL: %w", normaliseError)
	}

	idToken, idTokenError := requestGitHubIDToken(cmd.Context(), loginHTTPClient, requestURL, requestToken, audience)
	if idTokenError != nil {
		return idTokenError
	}
	// The ID token is itself a credential for the length of its life.
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "::add-mask::%s\n", idToken)
	}
	result, exchangeError := client.ExchangeGitHubOIDCToken(cmd.Context(), loginHTTPClient, baseURL, organisationID, idToken)
	if exchangeError != nil {
		return exchangeError
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "::add-mask::%s\n", result.AccessToken)
	}

	configPath := getConfigPath()
	if mkdirError := os.MkdirAll(filepath.Dir(configPath), 0o700); mkdirError != nil {
		return fmt.Errorf("create config directory: %w", mkdirError)
	}
	if secureError := ensureSecureConfigFile(configPath); secureError != nil {
		return secureError
	}
	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")
	_ = viper.ReadInConfig()
	viper.Set("token", result.AccessToken)
	viper.Set("base-url", baseURL)
	if result.TokenID != "" {
		viper.Set("token_id", result.TokenID)
	}
	viper.SetConfigPermissions(0o600)
	if writeError := viper.WriteConfig(); writeError != nil {
		if safeError := viper.SafeWriteConfig(); safeError != nil {
			return fmt.Errorf("save config: %w", writeError)
		}
	}
	if chmodError := os.Chmod(configPath, 0o600); chmodError != nil {
		return fmt.Errorf("secure config file: %w", chmodError)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Logged in with the GitHub Actions identity of this job. The token is valid for %d minutes; nothing is stored beyond %s.\n",
		(result.ExpiresIn+59)/60, configPath)
	return nil
}

func init() {
	loginCmd.Flags().Bool("github-oidc", false,
		"authenticate with this GitHub Actions job's OIDC identity (needs 'permissions: id-token: write')")
	loginCmd.Flags().String("audience", "",
		"audience to request for the GitHub ID token (default "+client.GitHubOIDCAudience+")")
	loginCmd.Flags().String("organisation-id", "",
		"organisation whose OIDC trust policies apply (UUID; defaults to --org / ANKRA_ORG)")
}
