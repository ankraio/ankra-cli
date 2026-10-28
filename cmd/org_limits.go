package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var orgLimitsCmd = &cobra.Command{
	Use:   "limits",
	Short: "See and request increases to your organisation's limits",
	Long: "Some limits cannot be raised self-serve: the playground memory budget, the free " +
		"monthly AI allowance and the registry storage limit. Requests go to the Ankra team for review; you are notified when " +
		"one is decided.",
}

var orgLimitsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show your latest limit-increase request per kind",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		list, err := apiClient.ListLimitRequests()
		if err != nil {
			return fmt.Errorf("listing limit requests: %w", err)
		}
		if list.Requests == nil {
			list.Requests = []client.LimitRequest{}
		}
		if rendered, renderError := renderStructured(cmd, list); rendered || renderError != nil {
			return renderError
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
			case client.RegistryStorageLimitKind:
				value = fmt.Sprintf("%d GiB per registry project", request.RequestedValue)
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
		"  --kind ai-tokens --usd <n>           a bigger free monthly AI allowance\n" +
		"  --kind registry-storage --gb <n>     a bigger registry storage limit, in GiB per registry project\n" +
		"                                       (same as 'ankra registry storage request')\n",
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
		case "registry-storage", client.RegistryStorageLimitKind:
			if orgLimitsRequestGB <= 0 {
				return fmt.Errorf("--gb is required for --kind registry-storage (the limit you need, in GiB per registry project)")
			}
			request, err := submitRegistryStorageRequest(cmd, orgLimitsRequestGB, orgLimitsRequestJustification)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"Storage request submitted (%s). The Ankra team reviews it; you are notified when it is decided.\n",
				request.Status)
			return nil
		default:
			return fmt.Errorf("--kind must be playground-memory, ai-tokens or registry-storage")
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
	orgLimitsRequestCmd.Flags().StringVar(&orgLimitsRequestKind, "kind", "", "playground-memory, ai-tokens or registry-storage")
	orgLimitsRequestCmd.Flags().Int64Var(&orgLimitsRequestGB, "gb", 0,
		"requested playground memory budget in GB, or registry storage limit in GiB per registry project")
	orgLimitsRequestCmd.Flags().Float64Var(&orgLimitsRequestUSD, "usd", 0, "requested monthly AI allowance, in USD")
	orgLimitsRequestCmd.Flags().StringVar(&orgLimitsRequestJustification, "justification", "",
		"a sentence or two on why (required)")
	registerStructuredOutputFlags(orgLimitsListCmd)
	orgLimitsCmd.AddCommand(orgLimitsListCmd)
	orgLimitsCmd.AddCommand(orgLimitsRequestCmd)
	orgCmd.AddCommand(orgLimitsCmd)
}
