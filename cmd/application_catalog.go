package cmd

import (
	"encoding/json"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The application catalogue commands: publish an application's manifests as
// an organisation add-on other clusters can install, and read the published
// state back. Every publish field is optional and defaults from the
// application's descriptor.

func newApplicationPublishAddonCommand() *cobra.Command {
	publishCommand := &cobra.Command{
		Use:   "publish-addon <application-id>",
		Short: "Publish the application's manifests as an organisation add-on",
		Long: `Publish the application's generated manifests to the organisation
catalogue as a manifest add-on. Publishing again with a new --version adds
a version; other clusters install the add-on from the catalogue.`,
		Example: `  ankra application publish-addon <application-id> \
    --version 1.2.0 --display-name "Website" --category web --changelog "TLS defaults"`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			version, _ := command.Flags().GetString("version")
			displayName, _ := command.Flags().GetString("display-name")
			description, _ := command.Flags().GetString("description")
			category, _ := command.Flags().GetString("category")
			changelog, _ := command.Flags().GetString("changelog")
			payload, publishError := apiClient.PublishApplicationAddon(command.Context(),
				strings.TrimSpace(arguments[0]), client.PublishApplicationAddonRequest{
					Version:     strings.TrimSpace(version),
					DisplayName: strings.TrimSpace(displayName),
					Description: description,
					Category:    strings.TrimSpace(category),
					Changelog:   changelog,
				})
			if publishError != nil {
				return publishError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	publishCommand.Flags().String("version", "", "Add-on version to publish (defaults from the descriptor)")
	publishCommand.Flags().String("display-name", "", "Catalogue display name")
	publishCommand.Flags().String("description", "", "Catalogue description")
	publishCommand.Flags().String("category", "", "Catalogue category")
	publishCommand.Flags().String("changelog", "", "Changelog note for this version")
	registerStructuredOutputFlags(publishCommand)
	return publishCommand
}

func newApplicationPublishedAddonCommand() *cobra.Command {
	return newApplicationSubresourceCommand("published-addon",
		"Show the add-on this application published to the catalogue",
		func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationPublishedAddon(command.Context(), applicationID)
		})
}
