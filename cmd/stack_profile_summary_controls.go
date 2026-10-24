package cmd

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"ankra/internal/client"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func addSummaryControlCommands(parent *cobra.Command) {
	for _, spec := range []struct {
		name, use, short string
		count            int
	}{
		{"status", "status <profile-id|profile-name> <version>", "Read generation and approval state for an exact version", 2},
		{"draft-status", "draft-status <draft-id>", "Read generation and approval state for the draft revision you inspected", 1},
		{"generate", "generate <profile-id|profile-name> <version>", "Request generation for an exact version using a saved request key", 2},
		{"generate-draft", "generate-draft <draft-id>", "Request generation for an exact draft revision using a saved request key", 1},
		{"cancel", "cancel <generation-id>", "Cancel one generation without withdrawing an approval", 1},
	} {
		command := &cobra.Command{Use: spec.use, Short: spec.short, Args: cobra.ExactArgs(spec.count), RunE: runSummaryControl}
		registerStructuredOutputFlags(command)
		switch spec.name {
		case "draft-status":
			command.Flags().Int("source-revision", 0, "Draft revision you inspected (required)")
			_ = command.MarkFlagRequired("source-revision")
			command.Example = "  ankra stack-profiles summary draft-status <draft-id> --source-revision 3"
		case "generate", "generate-draft":
			command.Long = `Request one private candidate. The platform must have generation enabled, a configured
model, current organisation consent and sufficient budget. This command grants none
of those prerequisites and does not publish a summary or change a workload.

First read status (or draft-status). Create and save a request-key before admission.
Supply the exact source and summary revisions you inspected, including summary
revision zero. After an uncertain response, retry with the SAME key, source and
revisions. Do not create a new key or refresh expectations for that retry. The server
rejects conflicting reuse. Inspect generation <id> for progress and review separately.`
			command.Flags().Int("source-revision", 0, "Exact source revision you inspected (required; must match version for generate)")
			command.Flags().Int("summary-revision", 0, "Approval pointer revision you inspected, including zero (required)")
			command.Flags().String("request-key", "", "Saved canonical UUIDv7 identifying this request; reuse unchanged for retries (required)")
			for _, name := range []string{"source-revision", "summary-revision", "request-key"} {
				_ = command.MarkFlagRequired(name)
			}
			target := "opensearch 3"
			if spec.name == "generate-draft" {
				target = "<draft-id>"
			}
			command.Example = "  ankra stack-profiles summary " + spec.name + " " + target + " --source-revision 3 --summary-revision 0 --request-key <saved-uuidv7>"
		case "cancel":
			command.Flags().Bool("yes", false, "Confirm cancelling this generation")
			command.Example = "  ankra stack-profiles summary cancel <generation-id>"
		default:
			command.Example = "  ankra stack-profiles summary status opensearch 3"
		}
		parent.AddCommand(command)
	}
	key := &cobra.Command{Use: "request-key", Short: "Create a UUIDv7 to save before a new generation request", Args: cobra.NoArgs,
		Long:        "Create a request key locally. Save it before submitting generation. This command does not contact the platform. Never create a new key to retry an uncertain request.",
		Example:     "  ankra stack-profiles summary request-key -o json",
		Annotations: map[string]string{annotationRequiresAuth: "false"},
		RunE: func(command *cobra.Command, _ []string) error {
			if _, err := structuredFormatFromFlags(command); err != nil {
				return err
			}
			key, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("create summary request key: %w", err)
			}
			payload, err := json.Marshal(map[string]string{"idempotency_key": key.String()})
			if err != nil {
				return err
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(key)
	parent.AddCommand(key, newSummaryPolicyCommand())
}

func runSummaryControl(command *cobra.Command, args []string) error {
	if _, err := structuredFormatFromFlags(command); err != nil {
		return err
	}
	var payload json.RawMessage
	var err error
	if command.Name() == "cancel" {
		id, err := summaryUUID(args[0])
		if err != nil {
			return err
		}
		yes, _ := command.Flags().GetBool("yes")
		if err := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), "Cancel this summary generation? [y/N] ", yes); err != nil {
			return err
		}
		payload, err = apiClient.CancelProfileSummaryGeneration(command.Context(), id)
		return renderSummaryControl(command, payload, err)
	}
	draft := command.Name() == "draft-status" || command.Name() == "generate-draft"
	generating := command.Name() == "generate" || command.Name() == "generate-draft"
	var source, version int
	var id string
	if draft {
		id, err = summaryUUID(args[0])
		if err != nil {
			return err
		}
		source, err = boundedSummaryRevision(command, "source-revision", 1)
		if err != nil {
			return err
		}
	} else {
		version, err = parseRequiredProfileVersionArgument(args[1])
		if err != nil {
			return err
		}
		if version > math.MaxInt32 {
			return summaryUsage("version exceeds the supported revision range")
		}
	}
	request := client.ProfileSummaryAdmissionRequest{}
	if generating {
		source, err = boundedSummaryRevision(command, "source-revision", 1)
		if err != nil {
			return err
		}
		if !draft && source != version {
			return summaryUsage("--source-revision must match the requested version")
		}
		revision, err := boundedSummaryRevision(command, "summary-revision", 0)
		if err != nil {
			return err
		}
		key, _ := command.Flags().GetString("request-key")
		parsed, err := uuid.Parse(key)
		if err != nil || parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 || parsed.String() != key {
			return summaryUsage("--request-key must be a canonical lowercase UUIDv7; save it and reuse it unchanged for retries")
		}
		request = client.ProfileSummaryAdmissionRequest{ExpectedSourceRevision: source, ExpectedSummaryRevision: revision, IdempotencyKey: key}
	}
	if !draft {
		id, err = resolveStackProfileID(apiClient, args[0])
		if err != nil {
			return err
		}
	}
	switch command.Name() {
	case "status":
		payload, err = apiClient.GetProfileSummaryStatus(command.Context(), id, version)
	case "draft-status":
		payload, err = apiClient.GetDraftSummaryStatus(command.Context(), id, source)
	case "generate":
		payload, err = apiClient.GenerateProfileSummary(command.Context(), id, version, request)
	case "generate-draft":
		payload, err = apiClient.GenerateDraftSummary(command.Context(), id, request)
	}
	return renderSummaryControl(command, payload, err)
}

func summaryUUID(raw string) (string, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil || !stackProfileIDPattern.MatchString(strings.ToLower(raw)) {
		return "", summaryUsage("ID must be a nonzero UUID")
	}
	return id.String(), nil
}

func boundedSummaryRevision(command *cobra.Command, name string, minimum int) (int, error) {
	value, err := summaryRevision(command, name, minimum)
	if err != nil {
		return 0, err
	}
	if value > math.MaxInt32 {
		return 0, summaryUsage("--" + name + " exceeds the supported revision range")
	}
	return value, nil
}

func renderSummaryControl(command *cobra.Command, payload json.RawMessage, err error) error {
	if err != nil {
		return fmt.Errorf("profile summary %s: %w", command.Name(), err)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(payload, &object) != nil || len(object) == 0 {
		return fmt.Errorf("profile summary response was invalid; read back before retrying a write, and retain the same request key for generation")
	}
	return renderApplicationPayload(command, payload)
}

func newSummaryPolicyCommand() *cobra.Command {
	parent := &cobra.Command{Use: "policy", Short: "Inspect and explicitly approve or revoke organisation summary consent", Long: `Read policy get first: inspect the data-use notice, provider destination, provider
terms, consent digest and current policy revision. Approval requires a human identity with ai.manage and
explicit limits and acknowledgements. It does not enable generation or select a model.
After an uncertain policy write, read back before deciding what to do next.`}
	for _, name := range []string{"get", "approve", "revoke"} {
		command := &cobra.Command{Use: name, Args: cobra.NoArgs, RunE: runSummaryPolicy}
		registerStructuredOutputFlags(command)
		switch name {
		case "get":
			command.Short = "Read the data-use notice, provider route and current policy"
			command.Example = "  ankra stack-profiles summary policy get"
		case "revoke":
			command.Short = "Revoke the exact consent revision you inspected"
			command.Flags().Bool("yes", false, "Confirm revoking summary generation consent")
			command.Example = "  ankra stack-profiles summary policy revoke --policy-revision 2"
		case "approve":
			command.Short = "Approve the inspected data use and provider terms with explicit limits"
			command.Long = parent.Long
			command.Flags().String("consent-digest", "", "Exact route consent_digest from policy get (required)")
			command.Flags().Bool("accept-data-use", false, "Acknowledge the data-use notice you inspected (required)")
			command.Flags().Bool("confirm-provider-terms", false, "Confirm you reviewed the provider terms (required)")
			command.Flags().Int("content-retention-days", 0, "Content retention, 1 to 90 days (required)")
			command.Flags().Int("metadata-retention-days", 0, "History retention, at least content retention and at most 3650 days (required)")
			command.Flags().Int64("daily-tokens", 0, "Daily token ceiling, 1 to 1000000000 (required)")
			command.Flags().Int64("request-tokens", 0, "Per-request token ceiling, at most daily ceiling and 1000000 (required)")
			command.Flags().String("daily-usd", "", "Daily USD ceiling, positive and at most 1000000; up to six decimal places (required)")
			command.Flags().String("request-usd", "", "Per-request USD ceiling, positive and at most daily ceiling and 1000; up to six decimal places (required)")
			for _, flag := range []string{"consent-digest", "accept-data-use", "confirm-provider-terms", "content-retention-days", "metadata-retention-days", "daily-tokens", "request-tokens", "daily-usd", "request-usd"} {
				_ = command.MarkFlagRequired(flag)
			}
			command.Example = "  # Example limits only: choose ceilings and retention for your organisation.\n  ankra stack-profiles summary policy approve --policy-revision 0 --consent-digest <inspected-digest> --accept-data-use --confirm-provider-terms --content-retention-days 7 --metadata-retention-days 30 --daily-tokens 100000 --request-tokens 1000 --daily-usd 1 --request-usd 0.01"
		}
		if name != "get" {
			command.Flags().Int64("policy-revision", 0, "Exact policy revision you inspected; zero only for first approval (required)")
			_ = command.MarkFlagRequired("policy-revision")
		}
		parent.AddCommand(command)
	}
	return parent
}

func runSummaryPolicy(command *cobra.Command, _ []string) error {
	if _, err := structuredFormatFromFlags(command); err != nil {
		return err
	}
	if command.Name() == "get" {
		payload, err := apiClient.GetProfileSummaryPolicy(command.Context())
		return renderSummaryControl(command, payload, err)
	}
	revision, _ := command.Flags().GetInt64("policy-revision")
	if !command.Flags().Changed("policy-revision") || revision < 0 || revision == math.MaxInt64 || (command.Name() == "revoke" && revision == 0) {
		return summaryUsage("supply the exact valid --policy-revision you inspected")
	}
	if command.Name() == "revoke" {
		yes, _ := command.Flags().GetBool("yes")
		if err := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), "Revoke organisation consent for summary generation? [y/N] ", yes); err != nil {
			return err
		}
		payload, err := apiClient.RevokeProfileSummaryPolicy(command.Context(), client.ProfileSummaryPolicyRevocationRequest{ExpectedRevision: revision})
		return renderSummaryControl(command, payload, err)
	}
	request, err := summaryPolicyApproval(command, revision)
	if err != nil {
		return err
	}
	payload, err := apiClient.ApproveProfileSummaryPolicy(command.Context(), request)
	return renderSummaryControl(command, payload, err)
}

func summaryPolicyApproval(command *cobra.Command, revision int64) (client.ProfileSummaryPolicyApprovalRequest, error) {
	request := client.ProfileSummaryPolicyApprovalRequest{ExpectedRevision: revision}
	request.ExpectedConsentDigest, _ = command.Flags().GetString("consent-digest")
	digest, err := hex.DecodeString(request.ExpectedConsentDigest)
	if err != nil || len(digest) != 32 || strings.ToLower(request.ExpectedConsentDigest) != request.ExpectedConsentDigest {
		return request, summaryUsage("--consent-digest must be the lowercase SHA-256 digest from policy get")
	}
	request.AcceptDataUse, _ = command.Flags().GetBool("accept-data-use")
	request.ConfirmProviderTerms, _ = command.Flags().GetBool("confirm-provider-terms")
	if !request.AcceptDataUse || !request.ConfirmProviderTerms {
		return request, summaryUsage("both data-use and provider-terms acknowledgements must be explicitly true")
	}
	limits := &request.Limits
	limits.ContentRetentionDays, _ = command.Flags().GetInt("content-retention-days")
	limits.MetadataRetentionDays, _ = command.Flags().GetInt("metadata-retention-days")
	limits.DailyTokenLimit, _ = command.Flags().GetInt64("daily-tokens")
	limits.RequestTokenLimit, _ = command.Flags().GetInt64("request-tokens")
	daily, _ := command.Flags().GetString("daily-usd")
	perRequest, _ := command.Flags().GetString("request-usd")
	limits.DailyCostMicrousd, err = summaryUSDMicros(daily, 1_000_000_000_000)
	if err != nil {
		return request, summaryUsage("--daily-usd must be a positive decimal at most 1000000 with up to six decimal places")
	}
	limits.RequestCostMicrousd, err = summaryUSDMicros(perRequest, 1_000_000_000)
	if err != nil {
		return request, summaryUsage("--request-usd must be a positive decimal at most 1000 with up to six decimal places")
	}
	if limits.ContentRetentionDays < 1 || limits.ContentRetentionDays > 90 {
		return request, summaryUsage("--content-retention-days must be between 1 and 90")
	}
	if limits.MetadataRetentionDays < limits.ContentRetentionDays || limits.MetadataRetentionDays > 3650 {
		return request, summaryUsage("--metadata-retention-days must be at least content retention and at most 3650")
	}
	if limits.DailyTokenLimit < 1 || limits.DailyTokenLimit > 1_000_000_000 {
		return request, summaryUsage("--daily-tokens must be between 1 and 1000000000")
	}
	if limits.RequestTokenLimit < 1 || limits.RequestTokenLimit > 1_000_000 || limits.RequestTokenLimit > limits.DailyTokenLimit {
		return request, summaryUsage("--request-tokens must be positive and at most daily tokens and 1000000")
	}
	if limits.RequestCostMicrousd > limits.DailyCostMicrousd {
		return request, summaryUsage("--request-usd must not exceed --daily-usd")
	}
	return request, nil
}

// summaryUSDMicros parses decimal money without floats, exponents or rounding.
func summaryUSDMicros(raw string, maximum int64) (int64, error) {
	if len(raw) == 0 || len(raw) > 14 {
		return 0, fmt.Errorf("invalid USD amount")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || len(parts[0]) == 0 {
		return 0, fmt.Errorf("invalid USD amount")
	}
	for _, part := range parts {
		if len(part) == 0 {
			return 0, fmt.Errorf("invalid USD amount")
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return 0, fmt.Errorf("invalid USD amount")
			}
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 6 {
		return 0, fmt.Errorf("invalid USD amount")
	}
	value, err := strconv.ParseInt(parts[0]+fraction+strings.Repeat("0", 6-len(fraction)), 10, 64)
	if err != nil || value < 1 || value > maximum {
		return 0, fmt.Errorf("invalid USD amount")
	}
	return value, nil
}
