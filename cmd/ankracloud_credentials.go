package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// ankraCloudTokenEnvironmentName is where `credentials ankracloud create`
// reads the API token from when it is not piped in, so the token never lands
// in shell history or the process list the way a flag would.
const ankraCloudTokenEnvironmentName = "ANKRA_CLOUD_API_TOKEN"

var ankraCloudCredentialCmd = &cobra.Command{
	Use:     "ankracloud",
	Aliases: []string{"ankra-cloud"},
	Short:   "Manage Ankra Cloud credentials",
	Long: `List and create Ankra Cloud API credentials.

One Ankra Cloud credential serves both self-managed clusters
('ankra cluster ankracloud create') and Ankra Cloud Kubernetes
('ankra cluster managed create --provider ankracloud_k8s').

Ankra Cloud servers take any SSH key credential of the organisation for
--ssh-key-credential-id; create one with, for example,
'ankra credentials hetzner ssh-key create --name my-key --generate'.`,
}

var ankraCloudCredentialListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Ankra Cloud API credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentials, listError := apiClient.ListAnkraCloudCredentials()
		if listError != nil {
			return fmt.Errorf("listing Ankra Cloud credentials: %w", listError)
		}
		if credentials == nil {
			credentials = []client.Credential{}
		}
		if handled, renderError := renderStructured(cmd, credentials); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(credentials) == 0 {
			fmt.Println("No Ankra Cloud credentials found.")
			return nil
		}

		credentialsTable := table.NewWriter()
		credentialsTable.SetOutputMirror(os.Stdout)
		credentialsTable.SetStyle(table.StyleRounded)
		credentialsTable.AppendHeader(table.Row{"ID", "Name", "Available", "Created"})
		credentialsTable.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 20},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 15},
		})
		for _, credential := range credentials {
			available := "yes"
			if !credential.Available {
				available = "no"
			}
			credentialsTable.AppendRow(table.Row{credential.ID, credential.Name, available, formatTimeAgo(credential.CreatedAt)})
		}
		credentialsTable.Render()
		return nil
	},
}

var ankraCloudCredentialCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an Ankra Cloud API credential",
	Long: `Create an Ankra Cloud API credential from an API token (act_...).

The token is never taken from a flag. It is read from stdin with
--token-stdin, else from the ANKRA_CLOUD_API_TOKEN environment variable,
else from a masked prompt on a terminal.

--endpoint names a non-default Ankra Cloud installation (https only); it
defaults to https://cloud.ankra.app.

Examples:
  ankra credentials ankracloud create --name ankra-cloud
  ANKRA_CLOUD_API_TOKEN=act_... ankra credentials ankracloud create --name ankra-cloud
  cat token.txt | ankra credentials ankracloud create --name dev --endpoint https://cloud.ankra.dev --token-stdin`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		endpoint, _ := cmd.Flags().GetString("endpoint")
		endpoint = strings.TrimSpace(endpoint)
		if endpointError := validateAnkraCloudEndpoint(endpoint); endpointError != nil {
			return withExitCode(exitUsage, endpointError)
		}

		apiToken, tokenError := readAnkraCloudToken(cmd)
		if tokenError != nil {
			return tokenError
		}

		result, createError := apiClient.CreateAnkraCloudCredential(client.CreateAnkraCloudCredentialRequest{
			Name:     name,
			APIToken: apiToken,
			Endpoint: endpoint,
		})
		if createError != nil {
			return fmt.Errorf("creating Ankra Cloud credential: %w", createError)
		}
		if !result.Success {
			message := "failed to create Ankra Cloud credential:"
			for _, resourceError := range result.Errors {
				message += fmt.Sprintf("\n  - %s: %s", resourceError.Key, resourceError.Message)
			}
			return errors.New(message)
		}

		fmt.Printf("Ankra Cloud credential '%s' created successfully!\n", name)
		return nil
	},
}

// validateAnkraCloudEndpoint accepts an empty endpoint (the server default)
// or an absolute https URL, the only scheme the platform stores.
func validateAnkraCloudEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	parsedEndpoint, parseError := url.Parse(endpoint)
	if parseError != nil || parsedEndpoint.Scheme != "https" || parsedEndpoint.Host == "" {
		return fmt.Errorf("invalid --endpoint %q: must be an https URL such as %s", endpoint, client.AnkraCloudDefaultEndpoint)
	}
	return nil
}

// ankraCloudTokenPrompt reads the token with a masked prompt; a variable so
// tests can replace the terminal interaction.
var ankraCloudTokenPrompt = func() (string, error) {
	prompt := promptui.Prompt{
		Label: "Ankra Cloud API Token",
		Mask:  '*',
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return errors.New("token cannot be empty")
			}
			return nil
		},
	}
	token, promptError := prompt.Run()
	if promptError != nil {
		return "", errors.New("prompt cancelled")
	}
	return strings.TrimSpace(token), nil
}

// readAnkraCloudToken takes the API token from stdin (--token-stdin), then
// the environment, then a masked prompt, and never echoes it.
func readAnkraCloudToken(cmd *cobra.Command) (string, error) {
	isFromStdin, _ := cmd.Flags().GetBool("token-stdin")
	if isFromStdin {
		raw, readError := io.ReadAll(cmd.InOrStdin())
		if readError != nil {
			return "", fmt.Errorf("read the API token from stdin: %w", readError)
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return "", withExitCode(exitUsage, errors.New("no API token was read from stdin"))
		}
		return token, nil
	}
	if fromEnvironment := strings.TrimSpace(os.Getenv(ankraCloudTokenEnvironmentName)); fromEnvironment != "" {
		return fromEnvironment, nil
	}
	return ankraCloudTokenPrompt()
}

func init() {
	ankraCloudCredentialCreateCmd.Flags().String("name", "", "Credential name (required)")
	ankraCloudCredentialCreateCmd.Flags().String("endpoint", "", "Ankra Cloud API endpoint, https only (default "+client.AnkraCloudDefaultEndpoint+")")
	ankraCloudCredentialCreateCmd.Flags().Bool("token-stdin", false, "Read the API token from stdin")
	_ = ankraCloudCredentialCreateCmd.MarkFlagRequired("name")

	registerStructuredOutputFlags(ankraCloudCredentialListCmd)

	ankraCloudCredentialCmd.AddCommand(ankraCloudCredentialListCmd)
	ankraCloudCredentialCmd.AddCommand(ankraCloudCredentialCreateCmd)
	credentialsCmd.AddCommand(ankraCloudCredentialCmd)
}
