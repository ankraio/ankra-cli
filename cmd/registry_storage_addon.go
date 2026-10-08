package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// Paid registry storage: blocks of storage an organisation buys self-serve
// on top of its base limit, billed monthly by the days each block is held.
// Amounts above the self-serve maximum go through 'registry storage
// request' instead.

// registryStorageView is what 'registry storage -o json' prints: the usage
// answer as the platform gives it, with the paid storage beside it when the
// platform has paid storage.
type registryStorageView struct {
	client.RegistryStorage `yaml:",inline"`
	Addon                  *client.RegistryStorageAddon `json:"addon,omitempty" yaml:"addon,omitempty"`
	// AddonError is set when the paid storage could not be read, so a
	// script can tell "no paid storage on this platform" (both empty) from
	// "paid storage unknown" (this set).
	AddonError string `json:"addon_error,omitempty" yaml:"addon_error,omitempty"`
}

// registryStorageAddonUnserved reports a platform without paid storage: the
// add-on route answers a 404 that names nothing.
func registryStorageAddonUnserved(err error) bool {
	var unexpected *client.UnexpectedResponseError
	return errors.As(err, &unexpected) && unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == ""
}

// readRegistryStorageAddonForShow reads the paid storage for the usage
// view: nil and no error on a platform without it, so the view shows no
// add-on line rather than an error.
func readRegistryStorageAddonForShow(command *cobra.Command) (*client.RegistryStorageAddon, error) {
	addon, getError := apiClient.GetRegistryStorageAddon(command.Context())
	if getError != nil {
		if registryStorageAddonUnserved(getError) {
			return nil, nil
		}
		return nil, getError
	}
	if addon == nil {
		return nil, errors.New("the platform answered nothing")
	}
	return addon, nil
}

// formatRegistryMoney writes cents in a currency as "EUR 10.00".
func formatRegistryMoney(cents int64, currency string) string {
	code := strings.ToUpper(strings.TrimSpace(currency))
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	if code == "" {
		return fmt.Sprintf("%s%d.%02d (currency not stated)", sign, cents/100, cents%100)
	}
	return fmt.Sprintf("%s %s%d.%02d", code, sign, cents/100, cents%100)
}

// renderRegistryStorageAddonLines writes the add-on lines of the usage view.
func renderRegistryStorageAddonLines(out io.Writer, addon *client.RegistryStorageAddon, addonError error) {
	switch {
	case addonError != nil:
		_, _ = fmt.Fprintf(out, "  Add-on: unknown - paid storage could not be read (%v).\n", addonError)
		return
	case addon == nil:
		return
	case addon.Blocks > 0:
		_, _ = fmt.Fprintf(out, "  Add-on: %d x %d GiB, %s/month (change it: ankra registry storage buy --blocks <n>)\n",
			addon.Blocks, addon.BlockGiB, formatRegistryMoney(addon.MonthlyCents, addon.Currency))
	case addon.Available:
		_, _ = fmt.Fprintf(out, "  Add-on: none. Buy more in %d GiB blocks at %s/month each: ankra registry storage buy --blocks <n>\n",
			addon.BlockGiB, formatRegistryMoney(addon.PriceCentsPerBlockMonth, addon.Currency))
	}
	if !addon.Available {
		reason := "paid storage is not offered to this organisation right now"
		if addon.UnavailableReason != nil && strings.TrimSpace(*addon.UnavailableReason) != "" {
			reason = strings.TrimSpace(*addon.UnavailableReason)
		}
		_, _ = fmt.Fprintf(out, "  Buying more: not available - %s\n", reason)
	}
	if addon.BillingBlocker != nil && strings.TrimSpace(*addon.BillingBlocker) != "" {
		_, _ = fmt.Fprintf(out, "  Billing: %s\n", strings.TrimSpace(*addon.BillingBlocker))
	}
}

// registryStorageAddonChange is what a buy is about to do, as the
// confirmation shows it. Two reads that would show the same lines need only
// one confirmation.
type registryStorageAddonChange struct {
	lines []string
}

func (change registryStorageAddonChange) key() string {
	return strings.Join(change.lines, "\n")
}

func registryStorageAddonChangeFor(addon *client.RegistryStorageAddon, blocks int) registryStorageAddonChange {
	newLimit := addon.BaseLimitBytes + int64(blocks)*addon.BlockGiB*bytesPerGiB
	newMonthly := int64(blocks) * addon.PriceCentsPerBlockMonth
	lines := []string{
		fmt.Sprintf("Paid registry storage: %d -> %d blocks of %d GiB", addon.Blocks, blocks, addon.BlockGiB),
		fmt.Sprintf("  Limit:  %s -> %s per registry project", formatRegistryLimit(addon.EffectiveLimitBytes), formatRegistryLimit(newLimit)),
		fmt.Sprintf("  Price:  %s/month -> %s/month (billed for the days held, on the monthly invoice)",
			formatRegistryMoney(addon.MonthlyCents, addon.Currency), formatRegistryMoney(newMonthly, addon.Currency)),
	}
	if blocks > addon.Blocks && addon.BillingBlocker != nil && strings.TrimSpace(*addon.BillingBlocker) != "" {
		lines = append(lines, "  Billing: "+strings.TrimSpace(*addon.BillingBlocker))
	}
	return registryStorageAddonChange{lines: lines}
}

func newRegistryStorageBuyCommand() *cobra.Command {
	buyCommand := &cobra.Command{
		Use:   "buy",
		Short: "Buy more registry storage in blocks, billed monthly",
		Long: `Set how many paid storage blocks the organisation holds on top of its included
registry storage. Each block adds its size to the limit of every registry
project and is billed monthly for the days it is held. --blocks is the number
to hold, not the number to add; 0 stops paying for extra storage.

It shows the limit and the monthly price before and after, and asks first;
--yes skips the prompt. Lowering it below what the registry already holds is
refused: free space first. For more than the self-serve maximum, ask Ankra
with 'ankra registry storage request'.`,
		Example: `  ankra registry storage buy --blocks 2
  ankra registry storage buy --blocks 0 --yes
  ankra registry storage buy --blocks 4 --yes -o json`,
		Args: cobra.NoArgs,
		RunE: runRegistryStorageBuy,
	}
	buyCommand.Flags().Int("blocks", 0, "How many paid storage blocks to hold (0 stops paying for extra storage)")
	buyCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(buyCommand)
	return buyCommand
}

// registryStorageAddonBlocksCeiling bounds --blocks before any limit or
// price arithmetic, so a platform that reports no maximum (0) cannot turn a
// mistyped number into an overflowed limit on the confirmation.
const registryStorageAddonBlocksCeiling = 100000

func registryStorageAddonRouteError(routeError error) error {
	if registryStorageAddonUnserved(routeError) {
		return withExitCode(exitError, errors.New("paid registry storage is not available on this platform yet; "+
			"ask Ankra for more with 'ankra registry storage request --size <GiB> --reason \"...\"'"))
	}
	return routeError
}

func runRegistryStorageBuy(command *cobra.Command, _ []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	if !command.Flags().Changed("blocks") {
		return withExitCode(exitUsage, errors.New("say how many blocks to hold with --blocks <n> (0 stops paying for extra storage)"))
	}
	blocks, _ := command.Flags().GetInt("blocks")
	if blocks < 0 {
		return withExitCode(exitUsage, fmt.Errorf("--blocks must be 0 or more, got %d", blocks))
	}
	if blocks > registryStorageAddonBlocksCeiling {
		return withExitCode(exitUsage, fmt.Errorf("--blocks %d is far more than paid storage offers; for more than the self-serve maximum, "+
			"ask Ankra: ankra registry storage request --size <GiB> --reason \"...\"", blocks))
	}
	yes, _ := command.Flags().GetBool("yes")
	var confirmed []string
	for attempt := 0; ; attempt++ {
		addon, getError := apiClient.GetRegistryStorageAddon(command.Context())
		if getError != nil {
			return registryStorageAddonRouteError(getError)
		}
		if addon == nil {
			return errors.New("reading paid registry storage: the platform answered nothing")
		}
		if blocks == addon.Blocks {
			if rendered, renderError := renderStructured(command, map[string]any{"addon": addon, "changed": false}); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "The organisation already holds %d paid storage blocks (%s/month); nothing changed.\n",
				addon.Blocks, formatRegistryMoney(addon.MonthlyCents, addon.Currency))
			return nil
		}
		if addon.MaxBlocks > 0 && blocks > addon.MaxBlocks {
			return withExitCode(exitUsage, fmt.Errorf("--blocks %d is more than the %d blocks (%d GiB) you can buy here; for more, "+
				"ask Ankra: ankra registry storage request --size <GiB> --reason \"...\"",
				blocks, addon.MaxBlocks, int64(addon.MaxBlocks)*addon.BlockGiB))
		}
		if !addon.Available && blocks > addon.Blocks {
			reason := "paid storage is not offered to this organisation right now"
			if addon.UnavailableReason != nil && strings.TrimSpace(*addon.UnavailableReason) != "" {
				reason = strings.TrimSpace(*addon.UnavailableReason)
			}
			return withExitCode(exitError, fmt.Errorf("buying registry storage is not available: %s", reason))
		}
		change := registryStorageAddonChangeFor(addon, blocks)
		if !slices.Contains(confirmed, change.key()) {
			errOut := command.ErrOrStderr()
			for _, line := range change.lines {
				_, _ = fmt.Fprintln(errOut, line)
			}
			if confirmError := confirmPrompt(command.InOrStdin(), errOut, "Change it? [y/N]: ", yes); confirmError != nil {
				return confirmError
			}
			confirmed = append(confirmed, change.key())
		}
		result, updateError := apiClient.UpdateRegistryStorageAddon(command.Context(), blocks, addon.Version)
		if updateError == nil {
			if result == nil {
				return errors.New("changing paid registry storage: the platform answered nothing")
			}
			return renderRegistryStorageAddonUpdate(command, result)
		}
		if attempt == 0 && addon.Version != "" && registryStorageConflict(updateError) {
			_, _ = fmt.Fprintln(command.ErrOrStderr(),
				"Paid storage changed since it was read; checking it again.")
			continue
		}
		return registryStorageAddonRouteError(updateError)
	}
}

// registryStorageConflict reports a write refused because what it was
// computed from changed since it was read (409).
func registryStorageConflict(err error) bool {
	var unexpected *client.UnexpectedResponseError
	return errors.As(err, &unexpected) && unexpected.StatusCode == http.StatusConflict
}

func renderRegistryStorageAddonUpdate(command *cobra.Command, result *client.RegistryStorageAddonUpdateResult) error {
	if rendered, renderError := renderStructured(command, result); rendered || renderError != nil {
		return renderError
	}
	out := command.OutOrStdout()
	addon := result.Addon
	limit := formatRegistryLimit(addon.EffectiveLimitBytes)
	if addon.Blocks == 0 {
		_, _ = fmt.Fprintf(out, "Paid registry storage removed. The limit is now %s per registry project.\n", limit)
	} else {
		_, _ = fmt.Fprintf(out, "Paid registry storage set to %d x %d GiB, %s/month. The limit is now %s per registry project.\n",
			addon.Blocks, addon.BlockGiB, formatRegistryMoney(addon.MonthlyCents, addon.Currency), limit)
	}
	if result.HarborApplied {
		_, _ = fmt.Fprintln(out, "The registry has the new limit now.")
	} else {
		_, _ = fmt.Fprintln(out, "The registry gets the new limit automatically within about 5 minutes.")
		if result.HarborApplyError != nil && strings.TrimSpace(*result.HarborApplyError) != "" {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Applying it straight away did not work: %s\n", strings.TrimSpace(*result.HarborApplyError))
		}
	}
	return nil
}
