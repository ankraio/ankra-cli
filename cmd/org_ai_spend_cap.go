package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var orgAISpendCapCmd = &cobra.Command{
	Use:   "ai-spend-cap",
	Short: "Show or set the organisation's daily AI money and token caps",
	Long: `The organisation's daily AI caps. The money cap bounds what chat may spend
per UTC day; the token caps bound the unattended runs: past the soft cap
every run degrades to the quick tier for the rest of the day, past the hard
cap new runs are refused until midnight UTC.`,
}

var orgAISpendCapShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the caps and today's spend",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		spendCap, getError := apiClient.GetAISpendCap()
		if getError != nil {
			return fmt.Errorf("reading the AI spend cap: %w", getError)
		}
		if rendered, renderError := renderStructured(cmd, spendCap); rendered || renderError != nil {
			return renderError
		}
		renderAISpendCap(cmd, spendCap)
		return nil
	},
}

var orgAISpendCapSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set or clear a cap; a cap not named is kept",
	Long: `Set one or more caps. Each flag takes a number or "none" to clear that cap;
a cap whose flag is not passed is left as it is. The hard token cap can
never be set below the soft one.

  ankra org ai-spend-cap set --daily-usd 25
  ankra org ai-spend-cap set --token-soft-cap 2000000 --token-hard-cap 5000000
  ankra org ai-spend-cap set --token-hard-cap none`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		update := client.AISpendCapUpdate{}
		if raw := changedStringFlag(cmd, "daily-usd"); raw != nil {
			value, parseError := parseOptionalFloat(*raw)
			if parseError != nil {
				return withExitCode(exitUsage, fmt.Errorf("--daily-usd: %w", parseError))
			}
			update.DailyChatUSDCapSet, update.DailyChatUSDCap = true, value
		}
		if raw := changedStringFlag(cmd, "token-soft-cap"); raw != nil {
			value, parseError := parseOptionalTokens(*raw)
			if parseError != nil {
				return withExitCode(exitUsage, fmt.Errorf("--token-soft-cap: %w", parseError))
			}
			update.DailyTokenSoftCapSet, update.DailyTokenSoftCap = true, value
		}
		if raw := changedStringFlag(cmd, "token-hard-cap"); raw != nil {
			value, parseError := parseOptionalTokens(*raw)
			if parseError != nil {
				return withExitCode(exitUsage, fmt.Errorf("--token-hard-cap: %w", parseError))
			}
			update.DailyTokenHardCapSet, update.DailyTokenHardCap = true, value
		}
		if !update.DailyChatUSDCapSet && !update.DailyTokenSoftCapSet && !update.DailyTokenHardCapSet {
			return withExitCode(exitUsage, errors.New("nothing to set: pass --daily-usd, --token-soft-cap or --token-hard-cap"))
		}
		spendCap, updateError := apiClient.UpdateAISpendCap(update)
		if updateError != nil {
			return fmt.Errorf("updating the AI spend cap: %w", updateError)
		}
		if rendered, renderError := renderStructured(cmd, spendCap); rendered || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "AI caps updated.")
		renderAISpendCap(cmd, spendCap)
		return nil
	},
}

// parseOptionalFloat reads a money flag: "none" clears, otherwise a
// positive number.
func parseOptionalFloat(raw string) (*float64, error) {
	if strings.EqualFold(strings.TrimSpace(raw), "none") {
		return nil, nil
	}
	value, parseError := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if parseError != nil {
		return nil, fmt.Errorf("expected a number or none, got %q", raw)
	}
	return &value, nil
}

// parseOptionalTokens reads a token flag: "none" clears, otherwise a whole
// number of tokens, with an optional k or m suffix (thousands, millions).
func parseOptionalTokens(raw string) (*int64, error) {
	text := strings.ToLower(strings.TrimSpace(raw))
	if text == "none" {
		return nil, nil
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(text, "m"):
		multiplier, text = 1_000_000, strings.TrimSuffix(text, "m")
	case strings.HasSuffix(text, "k"):
		multiplier, text = 1_000, strings.TrimSuffix(text, "k")
	}
	value, parseError := strconv.ParseInt(text, 10, 64)
	if parseError != nil {
		return nil, fmt.Errorf("expected a whole number of tokens (optionally with k or m) or none, got %q", raw)
	}
	value *= multiplier
	return &value, nil
}

func renderAISpendCap(cmd *cobra.Command, spendCap *client.AISpendCap) {
	writer := table.NewWriter()
	writer.SetOutputMirror(cmd.OutOrStdout())
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Cap", "Value", "Today"})
	writer.AppendRow(table.Row{"Daily money cap (USD)", formatOptionalFloat(spendCap.DailyChatUSDCap, spendCap.PlanDefaultUSD),
		fmt.Sprintf("%.2f spent", spendCap.SpentTodayUSD)})
	writer.AppendRow(table.Row{"Daily token soft cap", formatOptionalTokens(spendCap.DailyTokenSoftCap), formatTokensToday(spendCap)})
	writer.AppendRow(table.Row{"Daily token hard cap", formatOptionalTokens(spendCap.DailyTokenHardCap), "budget " + spendCap.TokenBudgetState})
	if spendCap.MonthlyFreeUSD != nil {
		writer.AppendRow(table.Row{"Monthly free allowance (USD)", fmt.Sprintf("%.2f", *spendCap.MonthlyFreeUSD),
			fmt.Sprintf("%.2f used, resets %s", spendCap.MonthlyPlatformSpentUSD, spendCap.MonthlyResetsAt)})
	}
	writer.Render()
}

func formatOptionalFloat(value *float64, planDefault *float64) string {
	if value != nil {
		return fmt.Sprintf("%.2f", *value)
	}
	if planDefault != nil {
		return fmt.Sprintf("plan default (%.2f)", *planDefault)
	}
	return "none"
}

func formatOptionalTokens(value *int64) string {
	if value == nil {
		return "none"
	}
	return strconv.FormatInt(*value, 10)
}

func formatTokensToday(spendCap *client.AISpendCap) string {
	if spendCap.TokensToday == nil {
		return "usage unreadable"
	}
	return strconv.FormatInt(*spendCap.TokensToday, 10) + " tokens"
}

func init() {
	orgAISpendCapSetCmd.Flags().String("daily-usd", "", "daily money cap in USD, or none to clear")
	orgAISpendCapSetCmd.Flags().String("token-soft-cap", "", "daily token soft cap (runs degrade to quick past it), or none to clear")
	orgAISpendCapSetCmd.Flags().String("token-hard-cap", "", "daily token hard cap (runs are refused past it), or none to clear")
	orgAISpendCapCmd.AddCommand(orgAISpendCapShowCmd)
	orgAISpendCapCmd.AddCommand(orgAISpendCapSetCmd)
	orgCmd.AddCommand(orgAISpendCapCmd)
}
