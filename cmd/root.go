package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var apiClient APIClient

const (
	// annotationRequiresAuth controls whether the persistent pre-run hook
	// resolves API credentials before invoking a command's RunE. Commands
	// that do not call the Ankra API (login, logout, version, completion,
	// help) set this to "false" so users can invoke them without a token.
	annotationRequiresAuth = "ankra.requires_auth"

	envAnkraAPIToken     = "ANKRA_API_TOKEN"
	envAnkraBaseURL      = "ANKRA_BASE_URL"
	envAnkraOrg          = "ANKRA_ORG"
	envAllowInsecureHTTP = "ANKRA_ALLOW_INSECURE_HTTP"
	defaultBaseURL       = "https://platform.ankra.app"
)

func newAPIClient() APIClient {
	return client.New(apiToken, baseURL)
}

var (
	apiToken string
	baseURL  string
	cfgFile  string
	version  = "0.2.4"
)

var rootCmd = &cobra.Command{
	Use:   "ankra",
	Short: "CLI for the Ankra platform",
	Long: `Ankra CLI allows you to manage clusters, operations,
addons, persistent selection, and more.`,
	SilenceUsage:      true,
	PersistentPreRunE: persistentPreRunE,
}

// SetVersion lets the main package override the build-time version string,
// typically from a -ldflags injection in CI.
func SetVersion(v string) {
	if v == "" {
		return
	}
	version = v
	rootCmd.Version = v
}

func Execute() {
	rootCmd.Version = version
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().
		StringVar(&cfgFile, "config", "", "config file (default is $HOME/.ankra.yaml)")
	rootCmd.PersistentFlags().
		String("token", "", "API token for authentication (or set ANKRA_API_TOKEN)")
	rootCmd.PersistentFlags().
		String("base-url", "", "Base URL for the Ankra API (or set ANKRA_BASE_URL)")
	rootCmd.PersistentFlags().
		String("org", "", "Organisation name or ID to run this command against, overriding the selected organisation (or set ANKRA_ORG)")

	_ = viper.BindPFlag("token", rootCmd.PersistentFlags().Lookup("token"))
	_ = viper.BindPFlag("base-url", rootCmd.PersistentFlags().Lookup("base-url"))
	_ = viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config"))

	viper.SetDefault("base-url", defaultBaseURL)

	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

// setRequiresAuth attaches the auth requirement annotation to a command.
// Subcommands inherit the value via commandRequiresAuth walking up the
// parent chain. The annotation is consulted from persistentPreRunE before
// resolving credentials.
func setRequiresAuth(cmd *cobra.Command, required bool) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	if required {
		cmd.Annotations[annotationRequiresAuth] = "true"
	} else {
		cmd.Annotations[annotationRequiresAuth] = "false"
	}
}

// commandRequiresAuth resolves the auth requirement for a command by walking
// up the command tree. Defaults to true (auth required) so any new command
// is fail-safe. Cobra's built-in help command and hidden shell-completion
// helper are always treated as auth-free since they never call the API.
func commandRequiresAuth(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	switch cmd.Name() {
	case "help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return false
	}
	for current := cmd; current != nil; current = current.Parent() {
		if current.Annotations == nil {
			continue
		}
		if value, ok := current.Annotations[annotationRequiresAuth]; ok {
			return value != "false"
		}
	}
	return true
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else if home, err := os.UserHomeDir(); err == nil {
		viper.AddConfigPath(home)
		viper.SetConfigName(".ankra")
		viper.SetConfigType("yaml")
	}
	viper.AutomaticEnv()
	if err := viper.BindEnv("token", envAnkraAPIToken); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not bind token environment variable: %v\n", err)
	}
	if err := viper.BindEnv("base-url", envAnkraBaseURL); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not bind base-url environment variable: %v\n", err)
	}
	_ = viper.ReadInConfig()
}

// persistentPreRunE resolves API credentials for any command that requires
// authentication. It runs after Cobra has resolved the actual target
// command, so positional arguments cannot accidentally bypass auth (the
// previous implementation scanned os.Args[1:]).
func persistentPreRunE(cmd *cobra.Command, _ []string) error {
	if !commandRequiresAuth(cmd) {
		return nil
	}

	resolved, err := resolveCredentials(cmd)
	if err != nil {
		return err
	}

	apiToken = resolved.token
	baseURL = resolved.baseURL
	if apiClient == nil {
		apiClient = newAPIClient()
	}

	if err := applyOrganisationOverride(cmd); err != nil {
		return err
	}
	return nil
}

// applyOrganisationOverride resolves the global `--org` flag (or the ANKRA_ORG
// environment variable) to an organisation ID and scopes all subsequent API
// requests to it, without changing the persistently selected organisation. The
// value may be an organisation name or ID. The flag takes precedence over the
// environment variable.
func applyOrganisationOverride(cmd *cobra.Command) error {
	orgValue := ""
	if flag, _ := flagValue(cmd.Root().PersistentFlags().Lookup("org")); flag != "" {
		orgValue = flag
	} else {
		orgValue = os.Getenv(envAnkraOrg)
	}
	orgValue = strings.TrimSpace(orgValue)
	if orgValue == "" {
		return nil
	}
	orgID, err := resolveOrgFlagToID(orgValue)
	if err != nil {
		return err
	}
	apiClient.SetOrganisationOverride(orgID)
	return nil
}

type resolvedCredentials struct {
	token   string
	baseURL string
}

// credentialSource explains where a credential pair came from for diagnostics.
type credentialSource string

const (
	sourceFlag       credentialSource = "flag"
	sourceConfigFile credentialSource = "config"
	sourceEnv        credentialSource = "env"
)

// resolveCredentials picks the active token and base URL using a single
// source of truth. Token and base URL are resolved together so that a saved
// login token is never silently combined with an env-supplied base URL
// pointing at another platform.
//
// Precedence:
//  1. Explicit --token flag pairs with the explicit --base-url flag (or
//     viper default).
//  2. Saved config file (written by `ankra login`).
//  3. Environment variables (ANKRA_API_TOKEN + optional ANKRA_BASE_URL).
func resolveCredentials(cmd *cobra.Command) (resolvedCredentials, error) {
	tokenFlag := cmd.Root().PersistentFlags().Lookup("token")
	baseURLFlag := cmd.Root().PersistentFlags().Lookup("base-url")

	flagToken, flagTokenSet := flagValue(tokenFlag)
	flagBaseURL, flagBaseURLSet := flagValue(baseURLFlag)

	savedToken, savedBaseURL := readSavedCredentials()
	envToken := os.Getenv(envAnkraAPIToken)
	envBaseURL := os.Getenv(envAnkraBaseURL)

	allowInsecureHTTP := os.Getenv(envAllowInsecureHTTP) == "1"

	var (
		token       string
		rawBaseURL  string
		tokenOrigin credentialSource
	)

	switch {
	case flagTokenSet && flagToken != "":
		token = flagToken
		tokenOrigin = sourceFlag
		switch {
		case flagBaseURLSet && flagBaseURL != "":
			rawBaseURL = flagBaseURL
		case envBaseURL != "":
			rawBaseURL = envBaseURL
		case savedBaseURL != "":
			rawBaseURL = savedBaseURL
		default:
			rawBaseURL = defaultBaseURL
		}
	case savedToken != "":
		token = savedToken
		tokenOrigin = sourceConfigFile
		switch {
		case flagBaseURLSet && flagBaseURL != "":
			rawBaseURL = flagBaseURL
		case savedBaseURL != "":
			rawBaseURL = savedBaseURL
		default:
			rawBaseURL = defaultBaseURL
		}
		if envToken != "" && envToken != savedToken {
			fmt.Fprintln(os.Stderr,
				"Note: ANKRA_API_TOKEN env var is set but differs from saved login token. Using login token.")
			fmt.Fprintln(os.Stderr,
				"To use the env var instead, run `ankra logout` to clear saved credentials.")
		}
		if envBaseURL != "" && envBaseURL != savedBaseURL {
			fmt.Fprintln(os.Stderr,
				"Note: ANKRA_BASE_URL is set but ignored because a saved login token is in use.")
			fmt.Fprintln(os.Stderr,
				"Run `ankra logout` and re-authenticate, or override with --base-url to switch platforms.")
		}
	case envToken != "":
		token = envToken
		tokenOrigin = sourceEnv
		switch {
		case flagBaseURLSet && flagBaseURL != "":
			rawBaseURL = flagBaseURL
		case envBaseURL != "":
			rawBaseURL = envBaseURL
		default:
			rawBaseURL = defaultBaseURL
		}
	default:
		return resolvedCredentials{}, errors.New(
			"not logged in: run `ankra login`, or provide a token via --token or ANKRA_API_TOKEN")
	}

	normalized, err := client.NormalizeBaseURL(rawBaseURL, allowInsecureHTTP)
	if err != nil {
		return resolvedCredentials{}, fmt.Errorf("invalid Ankra base URL: %w", err)
	}

	if tokenOrigin == sourceEnv && envBaseURL == "" && (flagBaseURL == "" || !flagBaseURLSet) {
		fmt.Fprintf(os.Stderr,
			"Note: using default base URL %s because ANKRA_BASE_URL is not set.\n", normalized)
	}

	return resolvedCredentials{token: token, baseURL: normalized}, nil
}

func flagValue(flag *pflag.Flag) (string, bool) {
	if flag == nil {
		return "", false
	}
	return flag.Value.String(), flag.Changed
}

// readSavedCredentials returns the token and base URL stored in
// $HOME/.ankra.yaml (or the explicit --config file), if any. Errors are
// swallowed because the config file is optional, but a permission
// warning is emitted if the file is group- or world-readable.
func readSavedCredentials() (string, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	v := viper.New()
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName(".ankra")
		v.SetConfigType("yaml")
		v.AddConfigPath(home)
	}
	if err := v.ReadInConfig(); err != nil {
		return "", ""
	}

	if used := v.ConfigFileUsed(); used != "" {
		warnIfConfigFileLoose(used)
	}

	return v.GetString("token"), v.GetString("base-url")
}

// warnIfConfigFileLoose emits a one-line stderr warning if the config
// file at path is group- or world-accessible. The warning is rate-
// limited to a single emission per process so it does not spam scripts.
var loosePermsWarned = make(map[string]bool)

func warnIfConfigFileLoose(path string) {
	if loosePermsWarned[path] {
		return
	}
	loosePermsWarned[path] = true
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		fmt.Fprintf(os.Stderr,
			"Warning: %s has permissions %#o; expected 0600. Run `chmod 600 %s` or re-run `ankra login` to fix.\n",
			path, mode, path)
	}
}
