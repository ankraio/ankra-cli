package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestLoginGitHubOIDCExchangesAndSavesTheToken(t *testing.T) {
	withFreshViperAndEnv(t)
	home := withTempHome(t)
	t.Cleanup(func() {
		_ = loginCmd.Flags().Set("github-oidc", "false")
		_ = loginCmd.Flags().Set("organisation-id", "")
	})
	const organisationID = "3b7dccca-0788-4470-9910-19478ae345ae"

	var requestedAudience, runnerBearer string
	runner := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestedAudience = request.URL.Query().Get("audience")
		runnerBearer = request.Header.Get("Authorization")
		_, _ = writer.Write([]byte(`{"value":"gh.id.token"}`))
	}))
	defer runner.Close()

	var exchanged map[string]string
	platform := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/oidc/github/token" {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewDecoder(request.Body).Decode(&exchanged)
		_, _ = writer.Write([]byte(`{"access_token":"ankra_exchanged","token_type":"Bearer","expires_in":900,"token_id":"tid"}`))
	}))
	defer platform.Close()

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", runner.URL+"/token?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-secret")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("ANKRA_ORG", "")

	// withFreshViperAndEnv resets viper, which drops the --base-url flag
	// binding, so the base URL is set on viper the way the other login tests do.
	viper.Set("base-url", platform.URL)
	output, executeError := executeCommand("login", "--github-oidc", "--organisation-id", organisationID)
	if executeError != nil {
		t.Fatalf("login --github-oidc: %v\n%s", executeError, output)
	}
	if requestedAudience != "https://ankra.ai/oidc/github" || runnerBearer != "Bearer runner-secret" {
		t.Errorf("ID token request: audience %q bearer %q", requestedAudience, runnerBearer)
	}
	if exchanged["subject_token"] != "gh.id.token" || exchanged["organisation_id"] != organisationID ||
		exchanged["grant_type"] != "urn:ietf:params:oauth:grant-type:token-exchange" {
		t.Errorf("exchange body: %v", exchanged)
	}
	if !strings.Contains(output, "::add-mask::ankra_exchanged") || !strings.Contains(output, "::add-mask::gh.id.token") {
		t.Errorf("both tokens must be masked in the Actions log:\n%s", output)
	}
	saved, readError := os.ReadFile(home + "/.ankra.yaml")
	if readError != nil || !strings.Contains(string(saved), "ankra_exchanged") {
		t.Fatalf("token not saved: %v", readError)
	}
	if info, _ := os.Stat(home + "/.ankra.yaml"); info.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v, want 0600", info.Mode().Perm())
	}
}

func TestLoginGitHubOIDCRefusesOutsideActionsAndWithoutOrganisation(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	t.Cleanup(func() {
		_ = loginCmd.Flags().Set("github-oidc", "false")
		_ = loginCmd.Flags().Set("organisation-id", "")
	})
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	t.Setenv("ANKRA_ORG", "")
	_, executeError := executeCommand("login", "--github-oidc")
	if executeError == nil || exitCodeFor(executeError) != exitUsage ||
		!strings.Contains(executeError.Error(), "id-token: write") {
		t.Fatalf("outside Actions: %v", executeError)
	}
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://example.invalid/t")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "x")
	_, executeError = executeCommand("login", "--github-oidc", "--organisation-id", "not-a-uuid")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("bad organisation id: %v", executeError)
	}
}

func TestLoginGitHubOIDCExplainsARefusedExchange(t *testing.T) {
	withFreshViperAndEnv(t)
	withTempHome(t)
	t.Cleanup(func() { _ = loginCmd.Flags().Set("github-oidc", "false") })
	runner := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"value":"gh.id.token"}`))
	}))
	defer runner.Close()
	platform := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer platform.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", runner.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "x")
	t.Setenv("ANKRA_ORG", "")
	viper.Set("base-url", platform.URL)
	_, executeError := executeCommand("login", "--github-oidc",
		"--organisation-id", "3b7dccca-0788-4470-9910-19478ae345ae")
	if executeError == nil || !strings.Contains(executeError.Error(), "no trust policy") {
		t.Fatalf("want an explanation of the refusal, got %v", executeError)
	}
	if _, statError := os.Stat(os.Getenv("HOME") + "/.ankra.yaml"); statError == nil {
		t.Error("a refused exchange must not write credentials")
	}
}

func TestRequestGitHubIDTokenRefusesNonHTTPSRunnerURL(t *testing.T) {
	if _, requestError := requestGitHubIDToken(t.Context(), http.DefaultClient,
		"http://runner.example/token", "x", "aud"); requestError == nil {
		t.Fatal("a plaintext runner URL must be refused")
	}
}
