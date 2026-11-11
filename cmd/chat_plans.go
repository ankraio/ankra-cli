package cmd

// `ankra chat plans ...` (ankra-hq41xb.22): the terminal half of plan review.
// A conversation that needs several writes proposes them as one plan and
// halts; the plan runs once it is approved, and never if it is rejected.
// `ankra chat actions` is the same decision for a single write.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
	"ankra/internal/hiddenunicode"
)

type chatPlanStep struct {
	StepOrder   int     `json:"step_order"`
	ToolName    string  `json:"tool_name"`
	Description string  `json:"description"`
	RiskLevel   string  `json:"risk_level"`
	Reversible  bool    `json:"reversible"`
	Status      string  `json:"status"`
	Error       *string `json:"error"`
}

type chatPlan struct {
	PlanID           string         `json:"plan_id"`
	Title            string         `json:"title"`
	Summary          string         `json:"summary"`
	RiskAggregate    string         `json:"risk_aggregate"`
	Status           string         `json:"status"`
	ExpiresInSeconds int            `json:"expires_in_seconds"`
	Steps            []chatPlanStep `json:"steps"`
}

// The plan_complete statuses that mean every step the plan was approved for
// is done, or handed to the session that proposed it.
const (
	chatPlanStatusCompleted = "completed"
	chatPlanStatusApproved  = "approved"
)

func newChatPlansCommand() *cobra.Command {
	plansCommand := &cobra.Command{
		Use:   "plans",
		Short: "Review, approve and reject AI plans awaiting approval",
		Long: `A chat conversation that needs several writes proposes them as one plan and
halts. Approving the plan runs its steps in order; rejecting it discards them.

Plans expire, so decide promptly. A plan with high-risk or irreversible steps
is only approved when each of those steps is named with --acknowledge.`,
	}
	listCommand := &cobra.Command{
		Use:   "list <conversation_id>",
		Short: "List your open plans in a conversation",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			payload, listError := apiClient.ListChatPlans(command.Context(), strings.TrimSpace(arguments[0]))
			if listError != nil {
				return listError
			}
			if format != outputDefault {
				_, renderError := renderStructured(command, json.RawMessage(payload))
				return renderError
			}
			var listing struct {
				Plans []chatPlan `json:"plans"`
			}
			if decodeError := json.Unmarshal(payload, &listing); decodeError != nil {
				return fmt.Errorf("parsing the plans: %w", decodeError)
			}
			output := command.OutOrStdout()
			if len(listing.Plans) == 0 {
				_, _ = fmt.Fprintln(output, "No plans awaiting approval.")
				return nil
			}
			for _, plan := range listing.Plans {
				_, _ = fmt.Fprintf(output, "%-38s %-10s %-8s %d step(s)  %s\n", chatPlanText(plan.PlanID),
					chatPlanText(plan.Status), chatPlanText(plan.RiskAggregate), len(plan.Steps), chatPlanText(plan.Title))
			}
			return nil
		},
	}
	getCommand := &cobra.Command{
		Use:   "get <plan_id>",
		Short: "Show one plan with its steps",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			payload, getError := apiClient.GetChatPlan(command.Context(), strings.TrimSpace(arguments[0]))
			if getError != nil {
				return getError
			}
			if format != outputDefault {
				_, renderError := renderStructured(command, json.RawMessage(payload))
				return renderError
			}
			var plan chatPlan
			if decodeError := json.Unmarshal(payload, &plan); decodeError != nil {
				return fmt.Errorf("parsing the plan: %w", decodeError)
			}
			printChatPlan(command.OutOrStdout(), plan)
			return nil
		},
	}
	approveCommand := &cobra.Command{
		Use:   "approve <plan_id>",
		Short: "Approve a plan so its steps run",
		Long: `Approve a proposed plan and follow its steps as they run.

Approving is the decision: there is no further prompt, exactly as with
'ankra chat actions confirm'. Read the plan first with
'ankra chat plans get <plan_id>'. A plan with high-risk or irreversible steps
is refused, with nothing run, until each of those steps is named with
--acknowledge <step-order>; the refusal lists them. It needs the ai.execute
permission.

Exit codes: 0 when every step completed (or the plan was handed to the chat
session that proposed it, which runs the steps), 8 when steps still need
--acknowledge, 3 when the plan is not found, 1 when the plan could not be
approved or a step failed.`,
		Example: `  ankra chat plans approve 6f1c2f9e-6d5a-4a1e-9f0b-6d4c2b8a1e77

  # Accept the two steps the refusal named as high-risk or irreversible
  ankra chat plans approve 6f1c2f9e-6d5a-4a1e-9f0b-6d4c2b8a1e77 --acknowledge 2,4`,
		Args: cobra.ExactArgs(1),
		RunE: runChatPlanApprove,
	}
	approveCommand.Flags().IntSlice("acknowledge", nil,
		"Step order of a high-risk or irreversible step you accept; repeat or separate with commas")
	rejectCommand := &cobra.Command{
		Use:   "reject <plan_id>",
		Short: "Reject a plan so none of its steps run",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			planID := strings.TrimSpace(arguments[0])
			payload, rejectError := apiClient.RejectChatPlan(command.Context(), planID, mustFlagString(command, "reason"))
			if rejectError != nil {
				if isNotFoundResponse(rejectError) {
					return withExitCode(exitNotFound, fmt.Errorf(
						"plan %s was not found or can no longer be rejected: 'ankra chat plans get %s' shows its status", planID, planID))
				}
				return rejectError
			}
			if format != outputDefault {
				_, renderError := renderStructured(command, json.RawMessage(payload))
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Plan %s rejected; none of its steps will run.\n", planID)
			return nil
		},
	}
	rejectCommand.Flags().String("reason", "", "Why the plan was rejected")

	registerStructuredOutputFlags(listCommand, getCommand, approveCommand, rejectCommand)
	plansCommand.AddCommand(listCommand, getCommand, approveCommand, rejectCommand)
	return plansCommand
}

// chatPlanApprovalOutput is `chat plans approve -o json|yaml`: the frames of
// the approval stream in the order the platform sent them.
type chatPlanApprovalOutput struct {
	PlanID string                   `json:"plan_id" yaml:"plan_id"`
	Status string                   `json:"status" yaml:"status"`
	Events []client.ChatStreamEvent `json:"events" yaml:"events"`
}

func runChatPlanApprove(command *cobra.Command, arguments []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	planID := strings.TrimSpace(arguments[0])
	acknowledged, _ := command.Flags().GetIntSlice("acknowledge")
	output := command.OutOrStdout()

	approval := chatPlanApprovalOutput{PlanID: planID, Events: []client.ChatStreamEvent{}}
	var refusal error
	approveError := apiClient.ApproveChatPlan(command.Context(), planID, acknowledged, func(event client.ChatStreamEvent) {
		approval.Events = append(approval.Events, event)
		data, _ := event.Data.(map[string]any)
		switch event.Type {
		case client.ChatPlanEventStepResult:
			if format == outputDefault {
				printChatPlanStepResult(output, data)
			}
		case client.ChatPlanEventComplete:
			approval.Status, _ = data["status"].(string)
		case client.ChatPlanEventError:
			approval.Status = "refused"
			refusal = chatPlanRefusalError(planID, data)
		}
	})
	if approveError != nil {
		var acknowledgementRequired *client.ChatPlanAcknowledgementRequiredError
		if errors.As(approveError, &acknowledgementRequired) {
			return chatPlanAcknowledgementError(planID, acknowledgementRequired)
		}
		if errors.Is(approveError, context.Canceled) {
			return approveError
		}
		if len(approval.Events) > 0 {
			return fmt.Errorf("the approval stream of plan %s was cut before it finished: %w; "+
				"'ankra chat plans get %s' shows what ran", planID, approveError, planID)
		}
		return approveError
	}
	if format != outputDefault {
		if encodeError := encodeStructured(output, format, approval); encodeError != nil {
			return encodeError
		}
	}
	switch {
	case refusal != nil:
		return refusal
	case approval.Status == chatPlanStatusCompleted:
		if format == outputDefault {
			_, _ = fmt.Fprintf(output, "Plan %s completed.\n", planID)
		}
		return nil
	case approval.Status == chatPlanStatusApproved:
		if format == outputDefault {
			_, _ = fmt.Fprintf(output, "Plan %s approved; the chat session that proposed it is running its steps.\n", planID)
		}
		return nil
	case approval.Status == "":
		return fmt.Errorf("the approval stream of plan %s ended without an outcome: 'ankra chat plans get %s' shows what ran",
			planID, planID)
	}
	return fmt.Errorf("plan %s ended %s: 'ankra chat plans get %s' shows each step",
		planID, strings.ReplaceAll(chatPlanText(approval.Status), "_", " "), planID)
}

// chatPlanRefusalError turns a plan_error frame into the command's error: a
// plan that is not there exits 3, every other refusal 1.
func chatPlanRefusalError(planID string, data map[string]any) error {
	message := chatPlanText(mapString(data, "message"))
	refusal := fmt.Errorf("plan %s was not approved: %s", planID, message)
	if mapString(data, "reason") == "not_found" {
		return withExitCode(exitNotFound, refusal)
	}
	return refusal
}

// chatPlanAcknowledgementError lists the steps an approval has to name and
// the command that names them. Nothing ran, so it exits 8.
func chatPlanAcknowledgementError(planID string, required *client.ChatPlanAcknowledgementRequiredError) error {
	lines := make([]string, 0, len(required.Steps))
	orders := make([]string, 0, len(required.Steps))
	for _, step := range required.Steps {
		orders = append(orders, strconv.Itoa(step.StepOrder))
		lines = append(lines, fmt.Sprintf("  %d. %s: %s (%s)", step.StepOrder, chatPlanText(step.ToolName),
			chatPlanText(step.Description), chatPlanText(step.Reason)))
	}
	return withExitCode(exitBlocked, fmt.Errorf(
		"plan %s was not approved and nothing ran: these steps are high-risk or irreversible and must be acknowledged\n%s\n"+
			"Approve them with: ankra chat plans approve %s --acknowledge %s",
		planID, strings.Join(lines, "\n"), planID, strings.Join(orders, ",")))
}

// chatPlanText removes invisible Unicode from server-authored text: a plan is
// what an operator reads before approving writes, so what is printed has to
// be what is there (ankra-4r75g.9).
func chatPlanText(value string) string {
	cleaned, _ := hiddenunicode.Strip(value)
	return cleaned
}

func printChatPlan(output io.Writer, plan chatPlan) {
	_, _ = fmt.Fprintf(output, "Plan:    %s\nTitle:   %s\nStatus:  %s\nRisk:    %s\n", chatPlanText(plan.PlanID),
		chatPlanText(plan.Title), chatPlanText(plan.Status), chatPlanText(plan.RiskAggregate))
	if plan.ExpiresInSeconds > 0 {
		_, _ = fmt.Fprintf(output, "Expires: in %ds\n", plan.ExpiresInSeconds)
	}
	if summary := strings.TrimSpace(chatPlanText(plan.Summary)); summary != "" {
		_, _ = fmt.Fprintf(output, "\n%s\n", summary)
	}
	_, _ = fmt.Fprintln(output, "\nSteps:")
	for _, step := range plan.Steps {
		reversibility := "reversible"
		if !step.Reversible {
			reversibility = "NOT reversible"
		}
		_, _ = fmt.Fprintf(output, "  %d. [%s] %s: %s (risk %s, %s)\n", step.StepOrder, chatPlanText(step.Status),
			chatPlanText(step.ToolName), chatPlanText(step.Description), chatPlanText(step.RiskLevel), reversibility)
		if step.Error != nil && strings.TrimSpace(*step.Error) != "" {
			_, _ = fmt.Fprintf(output, "     error: %s\n", chatPlanText(*step.Error))
		}
	}
}

func printChatPlanStepResult(output io.Writer, data map[string]any) {
	stepOrder := "?"
	if order, isNumber := data["step_order"].(float64); isNumber {
		stepOrder = strconv.Itoa(int(order))
	}
	_, _ = fmt.Fprintf(output, "Step %s %s: %s\n", stepOrder, chatPlanText(mapString(data, "tool_name")),
		chatPlanText(mapString(data, "status")))
	if stepError, hasError := data["error"].(string); hasError && stepError != "" {
		_, _ = fmt.Fprintf(output, "  error: %s\n", chatPlanText(stepError))
	}
}

func init() {
	chatCmd.AddCommand(newChatPlansCommand())
}
