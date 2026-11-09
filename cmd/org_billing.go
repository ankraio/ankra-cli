package cmd

import (
	"fmt"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// newOrgBillingCommand builds `ankra org billing`: the organisation's
// billing standing and prepaid wallet, read with billing.read.
func newOrgBillingCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "billing",
		Short: "Show the organisation's billing standing and prepaid wallet",
		Long: `Read the organisation's billing state. Both need billing.read.

  ankra org billing standing   whether billing restricts what the organisation may do
  ankra org billing wallet     the prepaid credit balance`,
	}

	standing := &cobra.Command{
		Use:   "standing",
		Short: "Show whether overdue invoices, the wallet or the free tier restrict the organisation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			billingStanding, getError := apiClient.GetBillingStanding()
			if getError != nil {
				return fmt.Errorf("reading the billing standing: %w", getError)
			}
			if rendered, renderError := renderStructured(cmd, billingStanding); rendered || renderError != nil {
				return renderError
			}
			writer := table.NewWriter()
			writer.SetOutputMirror(cmd.OutOrStdout())
			writer.SetStyle(table.StyleRounded)
			writer.AppendRow(table.Row{"State", billingStanding.State})
			writer.AppendRow(table.Row{"Restricted", billingStanding.Restricted})
			if billingStanding.Reason != "" {
				writer.AppendRow(table.Row{"Reason", billingStanding.Reason})
			}
			if billingStanding.OverdueInvoices > 0 {
				writer.AppendRow(table.Row{"Overdue invoices", fmt.Sprintf("%d (%.2f, oldest %d days)",
					billingStanding.OverdueInvoices, float64(billingStanding.OverdueCents)/100, billingStanding.OldestOverdueDays)})
			}
			if billingStanding.WalletState != "" {
				writer.AppendRow(table.Row{"Wallet", billingStanding.WalletState})
			}
			if billingStanding.FreeTierStanding != "" {
				writer.AppendRow(table.Row{"Free tier", billingStanding.FreeTierStanding})
			}
			if billingStanding.CreditLimitCents != nil {
				writer.AppendRow(table.Row{"Credit limit", fmt.Sprintf("%.2f", float64(*billingStanding.CreditLimitCents)/100)})
			}
			if len(billingStanding.GatedActions) > 0 {
				writer.AppendRow(table.Row{"Gated actions", strings.Join(billingStanding.GatedActions, ", ")})
			}
			writer.Render()
			if billingStanding.Message != "" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), billingStanding.Message)
			}
			return nil
		},
	}

	wallet := &cobra.Command{
		Use:   "wallet",
		Short: "Show the prepaid credit wallet's balance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			creditWallet, getError := apiClient.GetWallet()
			if getError != nil {
				return fmt.Errorf("reading the wallet: %w", getError)
			}
			if rendered, renderError := renderStructured(cmd, creditWallet); rendered || renderError != nil {
				return renderError
			}
			out := cmd.OutOrStdout()
			if !creditWallet.Present {
				_, _ = fmt.Fprintln(out, "The organisation has no prepaid wallet.")
				return nil
			}
			writer := table.NewWriter()
			writer.SetOutputMirror(out)
			writer.SetStyle(table.StyleRounded)
			writer.AppendRow(table.Row{"Balance", creditWallet.BalanceDisplay})
			writer.AppendRow(table.Row{"Net position", creditWallet.NetPositionDisplay})
			writer.AppendRow(table.Row{"State", creditWallet.State})
			if creditWallet.FrozenReason != nil && *creditWallet.FrozenReason != "" {
				writer.AppendRow(table.Row{"Frozen", *creditWallet.FrozenReason})
			}
			writer.AppendRow(table.Row{"Top-ups", creditWallet.TopUpsEnabled})
			writer.Render()
			return nil
		},
	}

	registerStructuredOutputFlags(standing, wallet)
	command.AddCommand(standing, wallet)
	return command
}

func init() {
	orgCmd.AddCommand(newOrgBillingCommand())
}
