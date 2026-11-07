package cmd

// `ankra application previews`: the per-application "Preview every pull
// request" switch. It is the repository's rule for the previews lane of the
// application's AI configuration, the same write Next's Settings > Deploys
// makes: that one switch, leaving every other lane and setting as it is.

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

const applicationPreviewsLane = "previews"

type applicationAIConfigView struct {
	Binding struct {
		Connected    bool   `json:"connected"`
		Reason       string `json:"reason"`
		RepoFullName string `json:"repo_full_name"`
	} `json:"binding"`
	Lanes []struct {
		Lane                     string `json:"lane"`
		Enabled                  bool   `json:"enabled"`
		OverriddenForApplication bool   `json:"overridden_for_application"`
		OrganisationEnabled      bool   `json:"organisation_enabled"`
	} `json:"lanes"`
}

func newApplicationPreviewsCommand() *cobra.Command {
	previewsCommand := &cobra.Command{
		Use:   "previews <application>",
		Short: "Show or switch whether every pull request gets a preview",
		Long: `Show whether Ankra deploys each of this application's pull requests to its own
temporary address and posts the link on the pull request.

Previews follow the source-control connection's default until the repository
has its own rule. 'previews enable' and 'previews disable' set that rule and
change nothing else; 'previews follow' removes it again. Previews also need the
organisation's staging cluster, which 'ankra org ai-environment set --staging-cluster' configures.`,
		Example: `  ankra application previews my-app
  ankra application previews enable my-app
  ankra application previews disable my-app
  ankra application previews follow my-app`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, getError := apiClient.GetApplicationAIConfig(command.Context(), applicationID)
			if getError != nil {
				return getError
			}
			return renderApplicationPreviews(command, format, payload)
		},
	}
	registerStructuredOutputFlags(previewsCommand)
	previewsCommand.AddCommand(
		newApplicationPreviewsSetCommand("enable", "Preview every pull request of this repository", map[string]any{"override": true, "enabled": true}),
		newApplicationPreviewsSetCommand("disable", "Stop previewing this repository's pull requests", map[string]any{"override": true, "enabled": false}),
		newApplicationPreviewsSetCommand("follow", "Remove the repository's rule and follow the connection's default", map[string]any{"override": false}),
	)
	return previewsCommand
}

func newApplicationPreviewsSetCommand(verb string, short string, patch map[string]any) *cobra.Command {
	setCommand := &cobra.Command{
		Use:   verb + " <application>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			lane := map[string]any{"lane": applicationPreviewsLane}
			for key, value := range patch {
				lane[key] = value
			}
			body, marshalError := json.Marshal(map[string]any{"lanes": []any{lane}})
			if marshalError != nil {
				return fmt.Errorf("encoding the rule: %w", marshalError)
			}
			payload, updateError := apiClient.UpdateApplicationAIConfig(command.Context(), applicationID, body)
			if updateError != nil {
				return updateError
			}
			return renderApplicationPreviews(command, format, payload)
		},
	}
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func renderApplicationPreviews(command *cobra.Command, format outputFormat, payload json.RawMessage) error {
	var config applicationAIConfigView
	if decodeError := json.Unmarshal(payload, &config); decodeError != nil {
		return fmt.Errorf("parsing the AI configuration: %w", decodeError)
	}
	out := command.OutOrStdout()
	if !config.Binding.Connected {
		reason := config.Binding.Reason
		if reason == "" {
			reason = "this application's repository has no source-control connection"
		}
		return errors.New("previews cannot be read: " + reason)
	}
	for _, lane := range config.Lanes {
		if lane.Lane != applicationPreviewsLane {
			continue
		}
		if format != outputDefault {
			_, renderError := renderStructured(command, map[string]any{
				"repository":                 config.Binding.RepoFullName,
				"enabled":                    lane.Enabled,
				"overridden_for_application": lane.OverriddenForApplication,
				"connection_default":         lane.OrganisationEnabled,
			})
			return renderError
		}
		state := "off"
		if lane.Enabled {
			state = "on"
		}
		connectionDefault := "off"
		if lane.OrganisationEnabled {
			connectionDefault = "on"
		}
		source := "follows the connection's default"
		if lane.OverriddenForApplication {
			source = "set by this repository's rule; the connection's default is " + connectionDefault
		}
		_, _ = fmt.Fprintf(out, "Previews for %s: %s (%s).\n", config.Binding.RepoFullName, state, source)
		return nil
	}
	return errors.New("this platform offers no previews switch for this application")
}
