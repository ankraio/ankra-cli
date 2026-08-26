package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var orgLimitsCmd = &cobra.Command{
	Use:   "limits",
	Short: "See and request increases to your organisation's limits",
	Long: "Some limits cannot be raised self-serve: the playground memory budget and the free " +
		"monthly AI allowance. Requests go to the Ankra team for review; you are notified when " +
		"one is decided.",
}

var orgLimitsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show your latest limit-increase request per kind",
	RunE: func(cmd *cobra.Command, args []string) error {
		list, err := apiClient.ListLimitRequests()
		if err != nil {
			return fmt.Errorf("listing limit requests: %w", err)
		}
		writer := cmd.OutOrStdout()
		if len(list.Requests) == 0 {
			_, _ = fmt.Fprintf(writer, "No limit-increase requests yet.\n")
			_, _ = fmt.Fprintf(writer, "Submit one with: ankra org limits request --kind playground-memory --gb <n> --justification \"...\"\n")
			return nil
		}
		for _, request := range list.Requests {
			value := fmt.Sprintf("%d", request.RequestedValue)
			switch request.LimitKind {
			case "playground_memory":
				value = fmt.Sprintf("%d GB", request.RequestedValue/1024)
			case "ai_tokens":
				value = fmt.Sprintf("$%.2f/month", float64(request.RequestedValue)/100)
			}
			_, _ = fmt.Fprintf(writer, "%-18s  %-10s  requested %s\n", request.LimitKind, request.Status, value)
		}
		return nil
	},
}

var (
	orgLimitsRequestKind          string
	orgLimitsRequestGB            int64
	orgLimitsRequestUSD           float64
	orgLimitsRequestJustification string
)

var orgLimitsRequestCmd = &cobra.Command{
	Use:   "request",
	Short: "Request a limit increase",
	Long: "Request a higher limit, reviewed by the Ankra team.\n\n" +
		"  --kind playground-memory --gb <n>    a bigger playground memory budget\n" +
		"  --kind ai-tokens --usd <n>           a bigger free monthly AI allowance\n",
	RunE: func(cmd *cobra.Command, args []string) error {
		var limitKind string
		var requestedValue int64
		switch strings.ToLower(orgLimitsRequestKind) {
		case "playground-memory", "playground_memory":
			if orgLimitsRequestGB <= 0 {
				return fmt.Errorf("--gb is required for --kind playground-memory")
			}
			limitKind, requestedValue = "playground_memory", orgLimitsRequestGB*1024
		case "ai-tokens", "ai_tokens":
			if orgLimitsRequestUSD <= 0 {
				return fmt.Errorf("--usd is required for --kind ai-tokens")
			}
			limitKind, requestedValue = "ai_tokens", int64(orgLimitsRequestUSD*100)
		default:
			return fmt.Errorf("--kind must be playground-memory or ai-tokens")
		}
		request, err := apiClient.SubmitLimitRequest(limitKind, requestedValue, orgLimitsRequestJustification)
		if err != nil {
			return fmt.Errorf("submitting the limit request: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Limit request submitted (%s). The Ankra team reviews it; you are notified when it is decided.\n",
			request.Status)
		return nil
	},
}

func init() {
	orgLimitsRequestCmd.Flags().StringVar(&orgLimitsRequestKind, "kind", "", "playground-memory or ai-tokens")
	orgLimitsRequestCmd.Flags().Int64Var(&orgLimitsRequestGB, "gb", 0, "requested playground memory budget, in GB")
	orgLimitsRequestCmd.Flags().Float64Var(&orgLimitsRequestUSD, "usd", 0, "requested monthly AI allowance, in USD")
	orgLimitsRequestCmd.Flags().StringVar(&orgLimitsRequestJustification, "justification", "",
		"a sentence or two on why (required)")
	orgLimitsCmd.AddCommand(orgLimitsListCmd)
	orgLimitsCmd.AddCommand(orgLimitsRequestCmd)
	orgCmd.AddCommand(orgLimitsCmd)
}
