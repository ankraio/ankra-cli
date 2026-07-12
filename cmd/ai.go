package cmd

import (
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var aiCmd = &cobra.Command{
	Use:   "ai",
	Short: "Manage AI provider settings and the model catalog",
	Long: `Configure the organisation's AI provider, credentials, custom
OpenAI-compatible endpoints, and the model catalog the chat picker offers.

Mutations require the ai.manage permission (organisation admins by default);
the provider status and the model listing are available to every member.`,
}

// --- status ---

var aiStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active AI provider and configured credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := apiClient.GetAIProviderStatus()
		if err != nil {
			return fmt.Errorf("getting AI provider status: %w", err)
		}
		if rendered, err := renderStructured(cmd, status); rendered || err != nil {
			return err
		}
		fmt.Printf("Active provider: %s\n\n", text.FgCyan.Sprint(status.Provider))
		fmt.Println("Anthropic (custom key):")
		if status.Anthropic.Configured {
			fmt.Printf("  configured: %s\n", text.FgGreen.Sprint("yes"))
			if status.Anthropic.KeyPreview != nil {
				fmt.Printf("  key:        %s\n", *status.Anthropic.KeyPreview)
			}
		} else {
			fmt.Printf("  configured: %s\n", "no")
		}
		fmt.Println("\nOpenAI-compatible (legacy single endpoint):")
		if status.OpenAICompatible.Configured {
			fmt.Printf("  configured: %s\n", text.FgGreen.Sprint("yes"))
			if status.OpenAICompatible.BaseURL != nil {
				fmt.Printf("  base URL:   %s\n", *status.OpenAICompatible.BaseURL)
			}
			if status.OpenAICompatible.Model != nil {
				fmt.Printf("  model:      %s\n", *status.OpenAICompatible.Model)
			}
			if status.OpenAICompatible.KeyPreview != nil {
				fmt.Printf("  key:        %s\n", *status.OpenAICompatible.KeyPreview)
			}
		} else {
			fmt.Printf("  configured: %s\n", "no")
		}
		return nil
	},
}

// --- provider ---

var aiProviderCmd = &cobra.Command{
	Use:   "provider <ankra|anthropic|openai_compatible>",
	Short: "Set the active AI provider",
	Long: `Set the active AI provider for the organisation.

  ankra              Ankra-managed Claude models (the default).
  anthropic          Your own Anthropic API key (set it first with 'ankra ai anthropic set').
  openai_compatible  Your OpenAI-compatible endpoint (set it first with 'ankra ai openai set').`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := apiClient.SetAIProvider(args[0])
		if err != nil {
			return fmt.Errorf("setting AI provider: %w", err)
		}
		if rendered, err := renderStructured(cmd, status); rendered || err != nil {
			return err
		}
		fmt.Printf("Active AI provider set to %s.\n", text.FgGreen.Sprint(status.Provider))
		return nil
	},
}

// --- models ---

var aiModelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Manage the organisation model catalog",
}

var aiModelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the catalog models the chat picker offers",
	RunE: func(cmd *cobra.Command, args []string) error {
		models, err := apiClient.ListAIModels()
		if err != nil {
			return fmt.Errorf("listing AI models: %w", err)
		}
		if rendered, err := renderStructured(cmd, models); rendered || err != nil {
			return err
		}
		if len(models) == 0 {
			fmt.Println("No models in the catalog.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(os.Stdout)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Key", "Display Name", "Provider", "Model ID", "Tier", "Enabled", "Default"})
		for _, model := range models {
			tier := ""
			if model.Tier != nil {
				tier = *model.Tier
			}
			writer.AppendRow(table.Row{
				model.Key,
				model.DisplayName,
				model.Provider,
				model.ModelID,
				tier,
				yesNo(model.IsEnabled),
				yesNo(model.IsDefault),
			})
		}
		writer.Render()
		return nil
	},
}

var aiModelsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Add a model to the catalog",
	Long: `Add a model to the catalog.

For an Ankra-managed Claude model pass --model-id (e.g. claude-opus-4-8). For a
model served by a custom OpenAI-compatible endpoint, also pass --endpoint with
the endpoint id (see 'ankra ai endpoints list').`,
	RunE: func(cmd *cobra.Command, args []string) error {
		request := client.AIModelRequest{
			Key:                 mustFlagString(cmd, "key"),
			DisplayName:         mustFlagString(cmd, "name"),
			ModelID:             mustFlagString(cmd, "model-id"),
			Description:         mustFlagString(cmd, "description"),
			Tier:                mustFlagString(cmd, "tier"),
			ContextWindowTokens: mustFlagInt(cmd, "context-window"),
			MaxOutputTokens:     mustFlagInt(cmd, "max-output"),
			SupportsTools:       mustFlagBool(cmd, "supports-tools"),
			SupportsThinking:    mustFlagBool(cmd, "supports-thinking"),
			SupportsImages:      mustFlagBool(cmd, "supports-images"),
			IsEnabled:           mustFlagBool(cmd, "enabled"),
			SortOrder:           mustFlagInt(cmd, "sort-order"),
		}
		if endpoint := mustFlagString(cmd, "endpoint"); endpoint != "" {
			request.EndpointID = &endpoint
		}
		model, err := apiClient.CreateAIModel(request)
		if err != nil {
			return fmt.Errorf("creating AI model: %w", err)
		}
		if rendered, err := renderStructured(cmd, model); rendered || err != nil {
			return err
		}
		fmt.Printf("Model %s created.\n", text.FgGreen.Sprint(model.Key))
		return nil
	},
}

var aiModelsUpdateCmd = &cobra.Command{
	Use:   "update <id|key>",
	Short: "Update a catalog model",
	Long: `Update a catalog model addressed by its row id or catalog key.

Only the flags you pass are changed; the rest keep their current values.
Editing a built-in default the first time materialises the catalog.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reference := args[0]
		models, err := apiClient.ListAIModels()
		if err != nil {
			return fmt.Errorf("loading AI models: %w", err)
		}
		existing, found := findCatalogModel(models, reference)
		if !found {
			return client.NewUnexpectedResponseError(404,
				fmt.Sprintf("model %q not found in the catalog", reference))
		}
		request := modelRequestFromExisting(existing)
		if cmd.Flags().Changed("key") {
			request.Key = mustFlagString(cmd, "key")
		}
		if cmd.Flags().Changed("name") {
			request.DisplayName = mustFlagString(cmd, "name")
		}
		if cmd.Flags().Changed("model-id") {
			request.ModelID = mustFlagString(cmd, "model-id")
		}
		if cmd.Flags().Changed("description") {
			request.Description = mustFlagString(cmd, "description")
		}
		if cmd.Flags().Changed("tier") {
			request.Tier = mustFlagString(cmd, "tier")
		}
		if cmd.Flags().Changed("endpoint") {
			if endpoint := mustFlagString(cmd, "endpoint"); endpoint != "" {
				request.EndpointID = &endpoint
			} else {
				request.EndpointID = nil
			}
		}
		if cmd.Flags().Changed("context-window") {
			request.ContextWindowTokens = mustFlagInt(cmd, "context-window")
		}
		if cmd.Flags().Changed("max-output") {
			request.MaxOutputTokens = mustFlagInt(cmd, "max-output")
		}
		if cmd.Flags().Changed("supports-tools") {
			request.SupportsTools = mustFlagBool(cmd, "supports-tools")
		}
		if cmd.Flags().Changed("supports-thinking") {
			request.SupportsThinking = mustFlagBool(cmd, "supports-thinking")
		}
		if cmd.Flags().Changed("supports-images") {
			request.SupportsImages = mustFlagBool(cmd, "supports-images")
		}
		if cmd.Flags().Changed("enabled") {
			request.IsEnabled = mustFlagBool(cmd, "enabled")
		}
		if cmd.Flags().Changed("sort-order") {
			request.SortOrder = mustFlagInt(cmd, "sort-order")
		}
		model, err := apiClient.UpdateAIModel(reference, request)
		if err != nil {
			return fmt.Errorf("updating AI model: %w", err)
		}
		if rendered, err := renderStructured(cmd, model); rendered || err != nil {
			return err
		}
		fmt.Printf("Model %s updated.\n", text.FgGreen.Sprint(model.Key))
		return nil
	},
}

var aiModelsDeleteCmd = &cobra.Command{
	Use:   "delete <id|key>",
	Short: "Delete a catalog model",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reference := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete model %q? [y/N]: ", reference), yes); err != nil {
			return err
		}
		if err := apiClient.DeleteAIModel(reference); err != nil {
			return fmt.Errorf("deleting AI model: %w", err)
		}
		fmt.Println("Model deleted.")
		return nil
	},
}

var aiModelsResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Revert the catalog to the built-in defaults",
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			"Reset the model catalog to the built-in defaults? Custom models and edits are lost. [y/N]: ", yes); err != nil {
			return err
		}
		models, err := apiClient.ResetAIModels()
		if err != nil {
			return fmt.Errorf("resetting AI models: %w", err)
		}
		if rendered, err := renderStructured(cmd, models); rendered || err != nil {
			return err
		}
		fmt.Printf("Catalog reset to %d built-in models.\n", len(models))
		return nil
	},
}

// --- endpoints ---

var aiEndpointsCmd = &cobra.Command{
	Use:   "endpoints",
	Short: "Manage custom OpenAI-compatible endpoints",
}

var aiEndpointsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured OpenAI-compatible endpoints",
	RunE: func(cmd *cobra.Command, args []string) error {
		endpoints, err := apiClient.ListAIEndpoints()
		if err != nil {
			return fmt.Errorf("listing AI endpoints: %w", err)
		}
		if rendered, err := renderStructured(cmd, endpoints); rendered || err != nil {
			return err
		}
		if len(endpoints) == 0 {
			fmt.Println("No endpoints configured.")
			return nil
		}
		writer := table.NewWriter()
		writer.SetOutputMirror(os.Stdout)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"ID", "Name", "Base URL", "Key"})
		for _, endpoint := range endpoints {
			writer.AppendRow(table.Row{endpoint.ID, endpoint.Name, endpoint.BaseURL, endpoint.KeyPreview})
		}
		writer.Render()
		return nil
	},
}

var aiEndpointsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Add an OpenAI-compatible endpoint",
	Long: `Add a named OpenAI-API-standard endpoint (OpenRouter, vLLM, LiteLLM,
Ollama, ...). The server validates and probes the endpoint before saving; the
API key is stored server-side and never returned.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		endpoint, err := apiClient.CreateAIEndpoint(
			mustFlagString(cmd, "name"),
			mustFlagString(cmd, "base-url"),
			mustFlagString(cmd, "api-key"),
		)
		if err != nil {
			return fmt.Errorf("creating AI endpoint: %w", err)
		}
		if rendered, err := renderStructured(cmd, endpoint); rendered || err != nil {
			return err
		}
		fmt.Printf("Endpoint %s created (id %s).\n", text.FgGreen.Sprint(endpoint.Name), endpoint.ID)
		return nil
	},
}

var aiEndpointsUpdateCmd = &cobra.Command{
	Use:   "update <endpoint_id>",
	Short: "Update an OpenAI-compatible endpoint",
	Long: `Update an endpoint's name, base URL, and optionally rotate its key.
Omit --api-key to keep the stored key.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		endpointID := args[0]
		endpoints, err := apiClient.ListAIEndpoints()
		if err != nil {
			return fmt.Errorf("loading AI endpoints: %w", err)
		}
		existing, found := findEndpoint(endpoints, endpointID)
		if !found {
			return client.NewUnexpectedResponseError(404,
				fmt.Sprintf("endpoint %q not found", endpointID))
		}
		name := existing.Name
		if cmd.Flags().Changed("name") {
			name = mustFlagString(cmd, "name")
		}
		baseURL := existing.BaseURL
		if cmd.Flags().Changed("base-url") {
			baseURL = mustFlagString(cmd, "base-url")
		}
		endpoint, err := apiClient.UpdateAIEndpoint(endpointID, name, baseURL, mustFlagString(cmd, "api-key"))
		if err != nil {
			return fmt.Errorf("updating AI endpoint: %w", err)
		}
		if rendered, err := renderStructured(cmd, endpoint); rendered || err != nil {
			return err
		}
		fmt.Printf("Endpoint %s updated.\n", text.FgGreen.Sprint(endpoint.Name))
		return nil
	},
}

var aiEndpointsDeleteCmd = &cobra.Command{
	Use:   "delete <endpoint_id>",
	Short: "Delete an OpenAI-compatible endpoint",
	Long:  "Delete an endpoint and its stored key. Catalog models on it are removed too.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		endpointID := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete endpoint %q and its catalog models? [y/N]: ", endpointID), yes); err != nil {
			return err
		}
		if err := apiClient.DeleteAIEndpoint(endpointID); err != nil {
			return fmt.Errorf("deleting AI endpoint: %w", err)
		}
		fmt.Println("Endpoint deleted.")
		return nil
	},
}

var aiEndpointsDiscoverCmd = &cobra.Command{
	Use:   "discover <endpoint_id>",
	Short: "List the model ids an endpoint advertises",
	Long:  "Query the endpoint's /models list so you know which model ids to reference.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		modelIDs, err := apiClient.DiscoverEndpointModels(args[0])
		if err != nil {
			return fmt.Errorf("discovering endpoint models: %w", err)
		}
		if rendered, err := renderStructured(cmd, modelIDs); rendered || err != nil {
			return err
		}
		if len(modelIDs) == 0 {
			fmt.Println("The endpoint advertised no models on /models.")
			return nil
		}
		for _, modelID := range modelIDs {
			fmt.Println(modelID)
		}
		return nil
	},
}

// --- anthropic credentials ---

var aiAnthropicCmd = &cobra.Command{
	Use:   "anthropic",
	Short: "Manage the custom Anthropic API key",
}

var aiAnthropicSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Store a custom Anthropic API key",
	Long:  "Store and validate a custom Anthropic API key (must start with sk-ant-).",
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := apiClient.SaveAnthropicKey(mustFlagString(cmd, "api-key"))
		if err != nil {
			return fmt.Errorf("saving Anthropic key: %w", err)
		}
		if rendered, err := renderStructured(cmd, status); rendered || err != nil {
			return err
		}
		fmt.Println("Anthropic API key saved. Activate it with: ankra ai provider anthropic")
		return nil
	},
}

var aiAnthropicDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Remove the custom Anthropic API key",
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			"Remove the custom Anthropic API key? [y/N]: ", yes); err != nil {
			return err
		}
		if _, err := apiClient.DeleteAnthropicKey(); err != nil {
			return fmt.Errorf("deleting Anthropic key: %w", err)
		}
		fmt.Println("Anthropic API key removed.")
		return nil
	},
}

// --- openai-compatible (legacy single endpoint) credentials ---

var aiOpenAICmd = &cobra.Command{
	Use:   "openai",
	Short: "Manage the legacy single OpenAI-compatible endpoint",
	Long: `Manage the legacy single OpenAI-compatible endpoint configuration.

For multiple endpoints and per-model routing prefer 'ankra ai endpoints' and
'ankra ai models'; this command configures the single legacy endpoint the
'openai_compatible' provider uses.`,
}

var aiOpenAISetCmd = &cobra.Command{
	Use:   "set",
	Short: "Store the legacy OpenAI-compatible endpoint",
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := apiClient.SaveOpenAICompatible(
			mustFlagString(cmd, "base-url"),
			mustFlagString(cmd, "api-key"),
			mustFlagString(cmd, "model"),
		)
		if err != nil {
			return fmt.Errorf("saving OpenAI-compatible config: %w", err)
		}
		if rendered, err := renderStructured(cmd, status); rendered || err != nil {
			return err
		}
		fmt.Println("OpenAI-compatible endpoint saved. Activate it with: ankra ai provider openai_compatible")
		return nil
	},
}

var aiOpenAIDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Remove the legacy OpenAI-compatible endpoint",
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			"Remove the legacy OpenAI-compatible endpoint? [y/N]: ", yes); err != nil {
			return err
		}
		if _, err := apiClient.DeleteOpenAICompatible(); err != nil {
			return fmt.Errorf("deleting OpenAI-compatible config: %w", err)
		}
		fmt.Println("OpenAI-compatible endpoint removed.")
		return nil
	},
}

// --- helpers ---

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func mustFlagString(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(value)
}

func mustFlagInt(cmd *cobra.Command, name string) int {
	value, _ := cmd.Flags().GetInt(name)
	return value
}

func mustFlagBool(cmd *cobra.Command, name string) bool {
	value, _ := cmd.Flags().GetBool(name)
	return value
}

// findCatalogModel matches a catalog entry by row id or catalog key.
func findCatalogModel(models []client.AICatalogModel, reference string) (client.AICatalogModel, bool) {
	for _, model := range models {
		if model.Key == reference || (model.ID != nil && *model.ID == reference) {
			return model, true
		}
	}
	return client.AICatalogModel{}, false
}

// findEndpoint matches an endpoint by id.
func findEndpoint(endpoints []client.AIEndpoint, endpointID string) (client.AIEndpoint, bool) {
	for _, endpoint := range endpoints {
		if endpoint.ID == endpointID {
			return endpoint, true
		}
	}
	return client.AIEndpoint{}, false
}

// modelRequestFromExisting builds an update body carrying the model's current
// values so a partial update only changes the flags the caller set.
func modelRequestFromExisting(model client.AICatalogModel) client.AIModelRequest {
	request := client.AIModelRequest{
		Key:                 model.Key,
		DisplayName:         model.DisplayName,
		Description:         model.Description,
		EndpointID:          model.EndpointID,
		ModelID:             model.ModelID,
		ContextWindowTokens: model.ContextWindowTokens,
		MaxOutputTokens:     model.MaxOutputTokens,
		SupportsTools:       model.SupportsTools,
		SupportsThinking:    model.SupportsThinking,
		SupportsImages:      model.SupportsImages,
		IsEnabled:           model.IsEnabled,
		SortOrder:           model.SortOrder,
	}
	if model.Tier != nil {
		request.Tier = *model.Tier
	}
	return request
}

// addModelWriteFlags registers the shared catalog-entry flags on create and
// update.
func addModelWriteFlags(cmd *cobra.Command) {
	cmd.Flags().String("key", "", "Catalog key (lowercase slug; the model_mode value)")
	cmd.Flags().String("name", "", "Display name shown in the picker")
	cmd.Flags().String("model-id", "", "Provider model id (e.g. claude-opus-4-8 or gpt-4o)")
	cmd.Flags().String("description", "", "Short description")
	cmd.Flags().String("tier", "", "Auto-routing tier: expert, think, quick, or empty")
	cmd.Flags().String("endpoint", "", "OpenAI-compatible endpoint id (omit for Ankra-managed Claude)")
	cmd.Flags().Int("context-window", 200000, "Context window in tokens")
	cmd.Flags().Int("max-output", 8192, "Maximum output tokens")
	cmd.Flags().Bool("supports-tools", true, "Model supports tool calling")
	cmd.Flags().Bool("supports-thinking", false, "Model supports a thinking/reasoning channel")
	cmd.Flags().Bool("supports-images", false, "Model accepts image inputs")
	cmd.Flags().Bool("enabled", true, "Model is selectable in the picker")
	cmd.Flags().Int("sort-order", 0, "Sort order in the picker (lower first)")
}

func init() {
	addModelWriteFlags(aiModelsCreateCmd)
	_ = aiModelsCreateCmd.MarkFlagRequired("key")
	_ = aiModelsCreateCmd.MarkFlagRequired("name")
	_ = aiModelsCreateCmd.MarkFlagRequired("model-id")
	addModelWriteFlags(aiModelsUpdateCmd)
	aiModelsDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	aiModelsResetCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	aiEndpointsCreateCmd.Flags().String("name", "", "Endpoint name")
	aiEndpointsCreateCmd.Flags().String("base-url", "", "OpenAI-compatible base URL (e.g. https://openrouter.ai/api/v1)")
	aiEndpointsCreateCmd.Flags().String("api-key", "", "API key for the endpoint")
	_ = aiEndpointsCreateCmd.MarkFlagRequired("name")
	_ = aiEndpointsCreateCmd.MarkFlagRequired("base-url")
	_ = aiEndpointsCreateCmd.MarkFlagRequired("api-key")
	aiEndpointsUpdateCmd.Flags().String("name", "", "Endpoint name")
	aiEndpointsUpdateCmd.Flags().String("base-url", "", "OpenAI-compatible base URL")
	aiEndpointsUpdateCmd.Flags().String("api-key", "", "New API key (omit to keep the stored key)")
	aiEndpointsDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	aiAnthropicSetCmd.Flags().String("api-key", "", "Anthropic API key (starts with sk-ant-)")
	_ = aiAnthropicSetCmd.MarkFlagRequired("api-key")
	aiAnthropicDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	aiOpenAISetCmd.Flags().String("base-url", "", "OpenAI-compatible base URL")
	aiOpenAISetCmd.Flags().String("api-key", "", "API key for the endpoint")
	aiOpenAISetCmd.Flags().String("model", "", "Model id to use")
	_ = aiOpenAISetCmd.MarkFlagRequired("base-url")
	_ = aiOpenAISetCmd.MarkFlagRequired("api-key")
	_ = aiOpenAISetCmd.MarkFlagRequired("model")
	aiOpenAIDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(aiStatusCmd, aiProviderCmd,
		aiModelsListCmd, aiModelsCreateCmd, aiModelsUpdateCmd, aiModelsResetCmd,
		aiEndpointsListCmd, aiEndpointsCreateCmd, aiEndpointsUpdateCmd, aiEndpointsDiscoverCmd,
		aiAnthropicSetCmd, aiOpenAISetCmd)

	aiModelsCmd.AddCommand(aiModelsListCmd, aiModelsCreateCmd, aiModelsUpdateCmd, aiModelsDeleteCmd, aiModelsResetCmd)
	aiEndpointsCmd.AddCommand(aiEndpointsListCmd, aiEndpointsCreateCmd, aiEndpointsUpdateCmd, aiEndpointsDeleteCmd, aiEndpointsDiscoverCmd)
	aiAnthropicCmd.AddCommand(aiAnthropicSetCmd, aiAnthropicDeleteCmd)
	aiOpenAICmd.AddCommand(aiOpenAISetCmd, aiOpenAIDeleteCmd)

	aiCmd.AddCommand(aiStatusCmd, aiProviderCmd, aiModelsCmd, aiEndpointsCmd, aiAnthropicCmd, aiOpenAICmd)
	rootCmd.AddCommand(aiCmd)
}
