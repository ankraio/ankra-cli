package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// `ankra login status` is a pure read. It exists because `ankra login status`
// used to fall through to plain `ankra login` and silently start a browser
// auth flow - a status probe must never open a browser or mutate saved
// credentials.

// loginStatusOutput is the -o json document.
type loginStatusOutput struct {
	LoggedIn     bool                        `json:"logged_in" yaml:"logged_in"`
	BaseURL      string                      `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	TokenSource  string                      `json:"token_source,omitempty" yaml:"token_source,omitempty"`
	TokenName    string                      `json:"token_name,omitempty" yaml:"token_name,omitempty"`
	Organisation *client.OrganisationSummary `json:"organisation,omitempty" yaml:"organisation,omitempty"`
	Detail       string                      `json:"detail,omitempty" yaml:"detail,omitempty"`
}

var loginStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report whether you are logged in, and as what",
	Long: `Report the login state without any browser interaction: whether
credentials are configured, the API base URL in use, where the token came
from (the saved config file, the environment, or a flag), and the currently
selected organisation.

Exit code 6 when not logged in or the token is rejected, so scripts can probe
the login state without parsing output.`,
	Args: cobra.NoArgs,
	RunE: runLoginStatus,
}

func runLoginStatus(command *cobra.Command, _ []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}

	resolved, resolveError := resolveCredentials(command)
	if resolveError != nil {
		if exitCodeFor(resolveError) != exitAuth {
			return resolveError
		}
		if renderError := renderLoginStatus(command, loginStatusOutput{
			LoggedIn: false,
			Detail:   "no credentials configured",
		}); renderError != nil {
			return renderError
		}
		return withExitCode(exitAuth,
			errors.New("not logged in: run `ankra login`, or provide a token via --token or ANKRA_API_TOKEN"))
	}

	status := loginStatusOutput{
		LoggedIn:    true,
		BaseURL:     resolved.baseURL,
		TokenSource: string(resolved.source),
	}
	if resolved.source == sourceConfigFile {
		status.TokenName = savedTokenName()
	}

	// The same wiring persistentPreRunE would have done for an
	// auth-requiring command: status is annotated auth-free so a missing
	// token reports instead of erroring, which means the client is not set
	// up yet when credentials do exist.
	apiToken = resolved.token
	baseURL = resolved.baseURL
	if apiClient == nil {
		apiClient = newAPIClient()
	}

	organisation, organisationSource, organisationError := resolveTargetOrganisation(command)
	if organisationError != nil {
		if errors.Is(organisationError, client.ErrUnauthorized) {
			status.LoggedIn = false
			status.Detail = "the token was rejected by the platform"
			if renderError := renderLoginStatus(command, status); renderError != nil {
				return renderError
			}
			return withExitCode(exitAuth, errors.New("the token was rejected by the platform; run `ankra login` again"))
		}
		return organisationError
	}
	status.Organisation = &organisation
	status.Detail = "organisation selection source: " + organisationSource
	return renderLoginStatus(command, status)
}

func renderLoginStatus(command *cobra.Command, status loginStatusOutput) error {
	if rendered, renderError := renderStructured(command, status); rendered || renderError != nil {
		return renderError
	}
	output := command.OutOrStdout()
	loggedIn := "no"
	if status.LoggedIn {
		loggedIn = "yes"
	}
	_, _ = fmt.Fprintf(output, "Logged in:    %s\n", loggedIn)
	if status.BaseURL != "" {
		_, _ = fmt.Fprintf(output, "Base URL:     %s\n", status.BaseURL)
	}
	if status.TokenSource != "" {
		sourceLabel := status.TokenSource
		if status.TokenSource == string(sourceConfigFile) {
			sourceLabel = "config (" + statusConfigPath() + ")"
		}
		_, _ = fmt.Fprintf(output, "Token source: %s\n", sourceLabel)
	}
	if status.TokenName != "" {
		_, _ = fmt.Fprintf(output, "Token name:   %s\n", status.TokenName)
	}
	if status.Organisation != nil {
		organisationName := status.Organisation.OrganisationID
		if status.Organisation.Name != nil && strings.TrimSpace(*status.Organisation.Name) != "" {
			organisationName = fmt.Sprintf("%s (%s)", *status.Organisation.Name, status.Organisation.OrganisationID)
		}
		_, _ = fmt.Fprintf(output, "Organisation: %s\n", organisationName)
	}
	if status.Organisation == nil && status.Detail != "" {
		_, _ = fmt.Fprintf(output, "Detail:       %s\n", status.Detail)
	}
	return nil
}

// savedTokenName reads the token_name `ankra login` saved, straight from the
// config file (honouring an explicit --config) so an ANKRA_API_TOKEN in the
// environment cannot shadow it.
func savedTokenName() string {
	fileConfiguration := viper.New()
	if cfgFile != "" {
		fileConfiguration.SetConfigFile(cfgFile)
		if !configExtSupported(cfgFile) {
			fileConfiguration.SetConfigType("yaml")
		}
	} else {
		fileConfiguration.SetConfigFile(getConfigPath())
		fileConfiguration.SetConfigType("yaml")
	}
	if readError := fileConfiguration.ReadInConfig(); readError != nil {
		return ""
	}
	return strings.TrimSpace(fileConfiguration.GetString("token_name"))
}

// statusConfigPath names the config file the status report points at: the
// explicit --config file when one was given, else the default.
func statusConfigPath() string {
	if cfgFile != "" {
		return cfgFile
	}
	return getConfigPath()
}

func init() {
	setRequiresAuth(loginStatusCmd, false)
	registerStructuredOutputFlags(loginStatusCmd)
	loginCmd.AddCommand(loginStatusCmd)
}
