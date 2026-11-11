package cmd

// `ankra credentials datasource list|create` (ankra-hq41xb.15): the CLI
// surface over GET/POST /api/v1/credentials/datasource, the organisation
// credentials a cluster's metrics source and log source name by
// credential_name.
//
// The secret is never a flag: it comes from stdin (--secret-stdin) or a
// masked prompt, like the other credential commands, and it is never
// printed, including in an error.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var datasourceCredentialCmd = &cobra.Command{
	Use:   "datasource",
	Short: "Manage the credentials metrics and log data sources authenticate with",
	Long: `Data source credentials are what a cluster's metrics source
(spec.prometheus_metrics) and log source (spec.log_source) authenticate with:
a bearer token or API key, or a username and password. A source names its
credential by credential_name in the cluster YAML.

The secret is stored in the platform's secret store and never returned.`,
}

var datasourceCredentialListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List data source credentials",
	Long:    `List the organisation's data source credentials. Only metadata is returned, never a secret.`,
	Example: `  ankra credentials datasource list
  ankra credentials datasource list -o json`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		credentials, listError := apiClient.ListDatasourceCredentials(command.Context())
		if listError != nil {
			return fmt.Errorf("listing data source credentials: %w", listError)
		}
		if rendered, renderError := renderStructured(command, credentials); rendered || renderError != nil {
			return renderError
		}
		out := command.OutOrStdout()
		if len(credentials) == 0 {
			_, _ = fmt.Fprintln(out, "No data source credentials. Create one with 'ankra credentials datasource create'.")
			return nil
		}
		rows := make([]table.Row, 0, len(credentials))
		for _, credential := range credentials {
			state := "-"
			if credential.State != nil && *credential.State != "" {
				state = *credential.State
			}
			rows = append(rows, table.Row{credential.ID, credential.Name, state, credential.Available, formatTimeAgo(credential.CreatedAt)})
		}
		renderRoundedTable(out, table.Row{"ID", "Name", "State", "Available", "Created"}, rows)
		return nil
	},
}

var datasourceCredentialCreateCmd = &cobra.Command{
	Use:   "create --name <name>",
	Short: "Create a data source credential",
	Long: `Create a credential for metrics and log data sources.

Without --username the secret is a token: sent as a bearer token by default, or
as an API key with --auth-scheme api_key. With --username the secret is that
user's password and the credential is basic auth.

The secret is never taken as an argument. Pipe it on stdin with --secret-stdin,
or run in a terminal to be asked for it with the input hidden. It is stored in
the platform's secret store and never shown again.`,
	Example: `  ankra credentials datasource create --name grafana-cloud-metrics
  cat token.txt | ankra credentials datasource create --name grafana-cloud-metrics --secret-stdin
  cat password.txt | ankra credentials datasource create --name mimir-reader --username reader --secret-stdin`,
	Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		name, nameError := requiredTextFlag(command, "name", "the name data sources reference the credential by")
		if nameError != nil {
			return nameError
		}
		username, _ := command.Flags().GetString("username")
		username = strings.TrimSpace(username)
		authScheme, _ := command.Flags().GetString("auth-scheme")
		authScheme = strings.TrimSpace(authScheme)
		request := client.CreateDatasourceCredentialRequest{Name: name, AuthScheme: authScheme, Username: username}
		secretLabel := "token"
		switch {
		case username != "" && authScheme != "" && authScheme != "basic":
			return withExitCode(exitUsage, fmt.Errorf("--username makes a basic credential, so --auth-scheme %s cannot be combined with it", authScheme))
		case username == "" && authScheme == "basic":
			return withExitCode(exitUsage, errors.New("--auth-scheme basic needs --username"))
		case username == "" && authScheme != "" && authScheme != "bearer" && authScheme != "api_key":
			return withExitCode(exitUsage, fmt.Errorf("--auth-scheme must be bearer, api_key or basic, got %q", authScheme))
		case username != "":
			secretLabel = "password"
		}
		secret, secretError := readSecretFromStdinOrPrompt(command, "secret-stdin", secretLabel)
		if secretError != nil {
			return secretError
		}
		if username != "" {
			request.Password = secret
		} else {
			request.Token = secret
		}
		result, createError := apiClient.CreateDatasourceCredential(command.Context(), request)
		if createError != nil {
			return redactSecret(fmt.Errorf("creating data source credential %q: %w", name, createError), secret)
		}
		if !result.Success {
			message := fmt.Sprintf("the platform did not create data source credential %q:", name)
			for _, problem := range result.Errors {
				message += fmt.Sprintf("\n  - %v: %v", problem["key"], problem["message"])
			}
			return redactSecret(errors.New(message), secret)
		}
		if rendered, renderError := renderStructured(command, result); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(command.OutOrStdout(), "Data source credential '%s' created (%s). Name it as credential_name on a metrics or log source.\n",
			result.Name, result.ID)
		return nil
	},
}

func init() {
	registerStructuredOutputFlags(datasourceCredentialListCmd, datasourceCredentialCreateCmd)
	datasourceCredentialCreateCmd.Flags().String("name", "", "Credential name (required)")
	datasourceCredentialCreateCmd.Flags().String("username", "", "Username of a basic (username and password) credential")
	datasourceCredentialCreateCmd.Flags().String("auth-scheme", "", "bearer (the default) or api_key for a token; basic with --username")
	datasourceCredentialCreateCmd.Flags().Bool("secret-stdin", false, "Read the token or password from stdin")
	datasourceCredentialCmd.AddCommand(datasourceCredentialListCmd, datasourceCredentialCreateCmd)
	credentialsCmd.AddCommand(datasourceCredentialCmd)
}
