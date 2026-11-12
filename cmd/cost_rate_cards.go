package cmd

// `ankra cost rate-cards ...`, `ankra cost waste ...` and `ankra cost estimate`
// (ankra-hq41xb.23): the rates that price private capacity, the cleanup of
// cloud waste, and what a cluster would cost with different node groups.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type costRateCard struct {
	Currency               string `json:"currency"`
	VCPUMonthlyAmount      string `json:"vcpu_monthly_amount"`
	MemoryGBMonthlyAmount  string `json:"memory_gb_monthly_amount"`
	StorageGBMonthlyAmount string `json:"storage_gb_monthly_amount"`
}

func (card costRateCard) row(label string, source string, clusters any) table.Row {
	return table.Row{label, source, strings.ToUpper(card.Currency), orDash(card.VCPUMonthlyAmount),
		orDash(card.MemoryGBMonthlyAmount), orDash(card.StorageGBMonthlyAmount), clusters}
}

// resolveRateCardTarget reads the argument that names a rate card: the
// organisation fallback card, or a private credential by name or id.
func resolveRateCardTarget(reference string) (string, error) {
	trimmed := strings.TrimSpace(reference)
	if trimmed == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("name a credential, or 'organisation' for the fallback card"))
	}
	if trimmed == client.CostRateCardOrganisation {
		return trimmed, nil
	}
	return resolveCredentialID(trimmed)
}

func newCostRateCardsCommand() *cobra.Command {
	rateCardsCommand := &cobra.Command{
		Use:     "rate-cards",
		Aliases: []string{"rate-card"},
		Short:   "State what private capacity costs per vCPU, GB of memory and GB of storage",
		Long: `Private capacity (Proxmox, Morpheus, imported clusters on your own hardware)
has no price book, so Ankra prices it from a rate card: a monthly amount per
vCPU, per GB of memory and per GB of storage. A card is stated for one private
credential, or as the organisation's fallback card; a credential without
either falls back to the provider default, and is unpriced when the provider
ships none.`,
	}

	listCommand := newOperationCommand(operationCommand{
		use:       "list",
		aliases:   []string{"ls"},
		short:     "List the private credentials with the rates that price them",
		example:   "  ankra cost rate-cards list\n  ankra cost rate-cards list -o json",
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			return apiClient.ListCostRateCards(command.Context())
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var listing struct {
				Credentials []struct {
					costRateCard
					CredentialID   string `json:"credential_id"`
					CredentialName string `json:"credential_name"`
					Source         string `json:"source"`
					ClusterCount   int    `json:"cluster_count"`
				} `json:"credentials"`
				OrganisationCard     *costRateCard `json:"organisation_card"`
				UnpricedClusterCount int           `json:"unpriced_cluster_count"`
			}
			if decodeError := decodeDocument(document, &listing); decodeError != nil {
				return decodeError
			}
			rows := []table.Row{}
			if listing.OrganisationCard != nil {
				rows = append(rows, listing.OrganisationCard.row("organisation (fallback)", "stated", "-"))
			}
			for _, credential := range listing.Credentials {
				rows = append(rows, credential.row(credential.CredentialName+" ("+credential.CredentialID+")", credential.Source, credential.ClusterCount))
			}
			if len(rows) == 0 {
				_, _ = fmt.Fprintln(out, "No private credentials and no organisation fallback card.")
			} else {
				renderRoundedTable(out, table.Row{"Card", "Source", "Currency", "vCPU / month", "Memory GB / month", "Storage GB / month", "Clusters"}, rows)
			}
			if listing.UnpricedClusterCount > 0 {
				_, _ = fmt.Fprintf(out, "Nothing prices %s yet. State a card with 'ankra cost rate-cards set'.\n",
					pluralCount(listing.UnpricedClusterCount, "private cluster"))
			}
			return nil
		},
	})

	setCommand := newOperationCommand(operationCommand{
		use:   "set <credential|organisation> --currency <code>",
		short: "State the monthly rates of a private credential, or the organisation fallback card",
		long: `State a rate card for one private credential (name or id), or for the
organisation's fallback card with 'organisation'. Amounts are decimal, per
month, in --currency. Needs billing.manage. A token pinned to a project writes
the cards of its own project's credentials only. It asks first; --yes skips
the question and is required when there is no terminal to ask on.`,
		example: `  ankra cost rate-cards set homelab-proxmox --currency EUR --vcpu 4.50 --memory-gb 1.20 --storage-gb 0.04
  ankra cost rate-cards set organisation --currency USD --vcpu 5 --memory-gb 1.5 --storage-gb 0.05 --yes`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			currency, currencyError := requiredTextFlag(command, "currency", "the currency the amounts are stated in, such as EUR")
			if currencyError != nil {
				return nil, currencyError
			}
			vcpuAmount, _ := command.Flags().GetString("vcpu")
			memoryAmount, _ := command.Flags().GetString("memory-gb")
			storageAmount, _ := command.Flags().GetString("storage-gb")
			request := client.CostRateCardRequest{Currency: currency, VCPUMonthlyAmount: strings.TrimSpace(vcpuAmount),
				MemoryGBMonthlyAmount: strings.TrimSpace(memoryAmount), StorageGBMonthlyAmount: strings.TrimSpace(storageAmount)}
			if request.VCPUMonthlyAmount == "" && request.MemoryGBMonthlyAmount == "" && request.StorageGBMonthlyAmount == "" {
				return nil, withExitCode(exitUsage, fmt.Errorf("state at least one rate: --vcpu, --memory-gb or --storage-gb"))
			}
			target, targetError := resolveRateCardTarget(arguments[0])
			if targetError != nil {
				return nil, targetError
			}
			request.CredentialID = target
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Stating the rate card of %s changes what its capacity is priced at in every cost view.",
				strings.TrimSpace(arguments[0]))); confirmError != nil {
				return nil, confirmError
			}
			return apiClient.SetCostRateCard(command.Context(), request)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var card costRateCard
			if decodeError := decodeDocument(document, &card); decodeError != nil {
				return decodeError
			}
			_, _ = fmt.Fprintf(out, "Rate card stated in %s: %s per vCPU, %s per GB of memory, %s per GB of storage, a month.\n",
				strings.ToUpper(card.Currency), orDash(card.VCPUMonthlyAmount), orDash(card.MemoryGBMonthlyAmount), orDash(card.StorageGBMonthlyAmount))
			return nil
		},
	})
	setCommand.Flags().String("currency", "", "Currency the amounts are stated in (required)")
	setCommand.Flags().String("vcpu", "", "Decimal amount per vCPU per month")
	setCommand.Flags().String("memory-gb", "", "Decimal amount per GB of memory per month")
	setCommand.Flags().String("storage-gb", "", "Decimal amount per GB of storage per month")
	setCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")

	deleteCommand := newOperationCommand(operationCommand{
		use:   "delete <credential|organisation>",
		short: "Clear a stated rate card",
		long: `Clear a stated card, so its capacity falls back to the provider default, or
becomes unpriced when the provider ships none. Needs billing.manage. It asks
first; --yes skips the question and is required when there is no terminal to
ask on.`,
		example:   "  ankra cost rate-cards delete homelab-proxmox --yes",
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			target, targetError := resolveRateCardTarget(arguments[0])
			if targetError != nil {
				return nil, targetError
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Clearing the rate card of %s prices its capacity from the provider default, or not at all when there is none.",
				strings.TrimSpace(arguments[0]))); confirmError != nil {
				return nil, confirmError
			}
			if deleteError := apiClient.DeleteCostRateCard(command.Context(), target); deleteError != nil {
				return nil, deleteError
			}
			return acknowledgementDocument(map[string]any{"credential_id": target, "deleted": true})
		},
		human: func(out io.Writer, _ json.RawMessage) error {
			_, _ = fmt.Fprintln(out, "Rate card cleared.")
			return nil
		},
	})
	deleteCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")

	rateCardsCommand.AddCommand(listCommand, setCommand, deleteCommand)
	return rateCardsCommand
}

const cloudWasteConsentValues = "no_snapshot_acknowledged, delete_unattached_volume_acknowledged, delete_released_pv_acknowledged, " +
	"delete_stopped_server_acknowledged, release_unassigned_ip_acknowledged or delete_idle_load_balancer_acknowledged"

func registerCloudWasteResolveFlags(command *cobra.Command) {
	command.Flags().String("consent", "", "Written consent a cleanup that cannot be recovered needs: "+cloudWasteConsentValues)
	command.Flags().Int("min-unattached-days", 0, "The age a volume must have been seen unattached (the platform's 30 when omitted)")
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
}

func cloudWasteResolveRequestFromFlags(command *cobra.Command) (client.CloudWasteResolveRequest, error) {
	consent, _ := command.Flags().GetString("consent")
	request := client.CloudWasteResolveRequest{Consent: strings.TrimSpace(consent)}
	if command.Flags().Changed("min-unattached-days") {
		days, _ := command.Flags().GetInt("min-unattached-days")
		if days < 0 || days > 3650 {
			return request, withExitCode(exitUsage, fmt.Errorf("--min-unattached-days is between 0 and 3650"))
		}
		request.MinUnattachedDays = &days
	}
	return request, nil
}

func newCostWasteCommand() *cobra.Command {
	wasteCommand := &cobra.Command{
		Use:   "waste",
		Short: "Clean up cloud waste and see what the cleanup saved",
		Long: `Cloud waste is what the scan finds billing with nothing using it: a stopped
server, an unattached volume, an unassigned IP, an idle load balancer. 'ankra
cost savings' lists the open findings with their ids.

What a cleanup does is decided by the kind of waste, never by the caller: a
stopped server gets a snapshot kept for seven days before it is deleted; an
unassigned IP or an idle load balancer is released. What cannot be recovered,
such as an unattached volume, is deleted only with written consent
(--consent) and only once it has been unattached long enough.`,
	}

	resolveCommand := newOperationCommand(operationCommand{
		use:   "resolve <finding-id>",
		short: "Start the cleanup of one cloud-waste finding",
		long: `Start the cleanup of one open cloud-waste finding and print the operation
that does it. Needs billing.manage. It asks first; --yes skips the question
and is required when there is no terminal to ask on.`,
		example: `  ankra cost waste resolve 4f1c0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c --yes
  ankra cost waste resolve <finding-id> --consent delete_unattached_volume_acknowledged --yes`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			findingID, argumentError := requiredArgument(arguments, 0, "the finding id")
			if argumentError != nil {
				return nil, argumentError
			}
			request, requestError := cloudWasteResolveRequestFromFlags(command)
			if requestError != nil {
				return nil, requestError
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Resolving waste finding %s deletes or releases the cloud resource it names.", findingID)); confirmError != nil {
				return nil, confirmError
			}
			return apiClient.ResolveCloudWasteFinding(command.Context(), findingID, request)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var resolution struct {
				Action               string `json:"action"`
				Kind                 string `json:"kind"`
				OperationID          string `json:"operation_id"`
				ResourceName         string `json:"resource_name"`
				SnapshotRetainedDays *int   `json:"snapshot_retained_days"`
			}
			if decodeError := decodeDocument(document, &resolution); decodeError != nil {
				return decodeError
			}
			_, _ = fmt.Fprintf(out, "Cleanup started: %s of %s %s.\n", resolution.Action, resolution.Kind, orDash(resolution.ResourceName))
			if resolution.SnapshotRetainedDays != nil {
				_, _ = fmt.Fprintf(out, "A snapshot is kept for %d days.\n", *resolution.SnapshotRetainedDays)
			}
			_, _ = fmt.Fprintf(out, "Follow it with: %s\n", followOperationCommand(resolution.OperationID))
			return nil
		},
	})
	registerCloudWasteResolveFlags(resolveCommand)

	resolveBatchCommand := newOperationCommand(operationCommand{
		use:   "resolve-batch <finding-id>...",
		short: "Start the cleanup of up to 100 cloud-waste findings",
		long: `Start the cleanup of up to 100 findings at once, one operation per finding,
and print each finding's outcome: started, refused with the platform's reason,
or failed. --consent and --min-unattached-days apply to every finding that
needs them. The platform refuses the call when no cleanup started. It asks
first; --yes skips the question and is required when there is no terminal to
ask on.`,
		example:   "  ankra cost waste resolve-batch <finding-id> <finding-id> --yes",
		arguments: cobra.RangeArgs(1, 100),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			request, requestError := cloudWasteResolveRequestFromFlags(command)
			if requestError != nil {
				return nil, requestError
			}
			for index := range arguments {
				findingID, argumentError := requiredArgument(arguments, index, "a finding id")
				if argumentError != nil {
					return nil, argumentError
				}
				request.FindingIDs = append(request.FindingIDs, findingID)
			}
			if confirmError := confirmOrganisationChange(command, fmt.Sprintf(
				"Resolving %s deletes or releases the cloud resources they name.",
				pluralCount(len(request.FindingIDs), "waste finding"))); confirmError != nil {
				return nil, confirmError
			}
			return apiClient.ResolveCloudWasteBatch(command.Context(), request)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var batch struct {
				Results []struct {
					FindingID   string  `json:"finding_id"`
					Status      string  `json:"status"`
					OperationID *string `json:"operation_id"`
					Detail      *string `json:"detail"`
				} `json:"results"`
				Started int `json:"started"`
				Refused int `json:"refused"`
				Failed  int `json:"failed"`
			}
			if decodeError := decodeDocument(document, &batch); decodeError != nil {
				return decodeError
			}
			rows := make([]table.Row, 0, len(batch.Results))
			for _, result := range batch.Results {
				rows = append(rows, table.Row{result.FindingID, result.Status, valueOrDash(result.OperationID), valueOrDash(result.Detail)})
			}
			renderRoundedTable(out, table.Row{"Finding", "Status", "Operation", "Detail"}, rows)
			_, _ = fmt.Fprintf(out, "%d started, %d refused, %d failed.\n", batch.Started, batch.Refused, batch.Failed)
			return nil
		},
	})
	registerCloudWasteResolveFlags(resolveBatchCommand)

	resolvedCommand := newOperationCommand(operationCommand{
		use:   "resolved",
		short: "Total what Ankra's cleanup resolved, by kind",
		long: `Total the monthly cost of the findings Ankra's cleanup resolved in the last
--days days (30 by default, at most 90), by kind, in your display currency.
Only findings the cleanup resolved count; unpriced findings are counted and
never summed as zero.`,
		example:   "  ankra cost waste resolved\n  ankra cost waste resolved --days 90 --all-projects -o json",
		arguments: cobra.NoArgs,
		invoke: func(command *cobra.Command, _ []string) (json.RawMessage, error) {
			days, _ := command.Flags().GetInt("days")
			if days < 0 || days > 90 {
				return nil, withExitCode(exitUsage, fmt.Errorf("--days is between 1 and 90, or 0 for the platform's 30"))
			}
			applyCostProjectScope(command)
			return apiClient.GetResolvedCloudWaste(command.Context(), days)
		},
		human: func(out io.Writer, document json.RawMessage) error {
			var resolved struct {
				Days             int    `json:"days"`
				Currency         string `json:"currency"`
				Count            int    `json:"count"`
				MonthlyCostCents *int64 `json:"monthly_cost_cents"`
				UnpricedCount    int    `json:"unpriced_count"`
				Kinds            []struct {
					Kind             string `json:"kind"`
					Count            int    `json:"count"`
					MonthlyCostCents *int64 `json:"monthly_cost_cents"`
				} `json:"kinds"`
			}
			if decodeError := decodeDocument(document, &resolved); decodeError != nil {
				return decodeError
			}
			if resolved.Count == 0 {
				_, _ = fmt.Fprintf(out, "Ankra's cleanup resolved no cloud waste in the last %d days.\n", resolved.Days)
				return nil
			}
			_, _ = fmt.Fprintf(out, "Resolved in the last %d days: %s that were billing %s a month.\n", resolved.Days,
				pluralCount(resolved.Count, "finding"), formatOptionalCostCents(resolved.MonthlyCostCents, resolved.Currency))
			rows := make([]table.Row, 0, len(resolved.Kinds))
			for _, kind := range resolved.Kinds {
				rows = append(rows, table.Row{kind.Kind, kind.Count, formatOptionalCostCents(kind.MonthlyCostCents, resolved.Currency)})
			}
			renderRoundedTable(out, table.Row{"Kind", "Findings", "Monthly cost"}, rows)
			if resolved.UnpricedCount > 0 {
				_, _ = fmt.Fprintf(out, "%s could not be priced and is not in the total.\n", pluralCount(resolved.UnpricedCount, "finding"))
			}
			return nil
		},
	})
	resolvedCommand.Flags().Int("days", 0, "How many days back to total (1 to 90; 30 when omitted)")
	registerCostProjectScopeFlag(resolvedCommand)

	wasteCommand.AddCommand(resolveCommand, resolveBatchCommand, resolvedCommand)
	return wasteCommand
}

func newCostEstimateCommand() *cobra.Command {
	estimateCommand := newOperationCommand(operationCommand{
		use:   "estimate <cluster>",
		short: "Price a cluster's configuration, or a what-if draft of its node groups",
		long: `Price what the cluster runs today, its node groups and load balancers, from
the same price book or rate card the metering bills from.

With --draft-file, price a draft beside it and predict the monthly bill as the
metered run rate plus the difference. The draft is a JSON object: node_groups
(the full set the cluster would run, each with count, vcpus, memory_gb,
disk_gb and optionally name, spot, gpus, gpu_model), and optionally network
(load_balancers, nat_gateways, gateways, flexible_ips), provider (price it on
another provider) and match (cheapest or like_for_like). '-' reads the draft
from stdin. Nothing is stored. -o json carries every priced line.`,
		example: `  ankra cost estimate prod-eu
  ankra cost estimate prod-eu --draft-file draft.json
  echo '{"node_groups":[{"name":"workers","count":5,"vcpus":4,"memory_gb":16,"disk_gb":80}]}' | ankra cost estimate prod-eu --draft-file -`,
		arguments: cobra.ExactArgs(1),
		invoke: func(command *cobra.Command, arguments []string) (json.RawMessage, error) {
			var draft map[string]any
			if draftPath, _ := command.Flags().GetString("draft-file"); strings.TrimSpace(draftPath) != "" {
				readDraft, readError := readJSONObjectFile(command, strings.TrimSpace(draftPath))
				if readError != nil {
					return nil, readError
				}
				if _, hasNodeGroups := readDraft["node_groups"].([]any); !hasNodeGroups {
					return nil, withExitCode(exitUsage, fmt.Errorf("the draft needs node_groups: the full list of node groups the cluster would run"))
				}
				draft = readDraft
			}
			clusterID, resolveError := resolveClusterID(arguments[0])
			if resolveError != nil {
				return nil, resolveError
			}
			if draft == nil {
				return apiClient.GetClusterCostEstimate(command.Context(), clusterID)
			}
			return apiClient.EstimateClusterCost(command.Context(), clusterID, draft)
		},
		human: printClusterCostEstimate,
	})
	estimateCommand.Flags().String("draft-file", "", "A JSON draft of the node groups to price ('-' for stdin); omitted prices what runs today")
	return estimateCommand
}

func printClusterCostEstimate(out io.Writer, document json.RawMessage) error {
	type configuration struct {
		TotalNodeCount     int   `json:"total_node_count"`
		MonthlyCents       int64 `json:"monthly_cents"`
		CoverageIncomplete bool  `json:"coverage_incomplete"`
	}
	var estimate struct {
		Currency string `json:"currency"`
		Pricing  struct {
			Mode   string `json:"mode"`
			Source string `json:"source"`
			Reason string `json:"reason"`
		} `json:"pricing"`
		Baseline              configuration `json:"baseline"`
		Estimate              configuration `json:"estimate"`
		MeteredMonthlyCents   *int64        `json:"metered_monthly_cents"`
		PredictedMonthlyCents *int64        `json:"predicted_monthly_cents"`
		DeltaMonthlyCents     *int64        `json:"delta_monthly_cents"`
	}
	if decodeError := decodeDocument(document, &estimate); decodeError != nil {
		return decodeError
	}
	_, _ = fmt.Fprintf(out, "Pricing:   %s (%s)\n", orDash(estimate.Pricing.Mode), orDash(estimate.Pricing.Source))
	if estimate.Pricing.Reason != "" {
		_, _ = fmt.Fprintf(out, "           %s\n", estimate.Pricing.Reason)
	}
	_, _ = fmt.Fprintf(out, "Today:     %s a month for %s\n", formatCostCents(estimate.Baseline.MonthlyCents, estimate.Currency),
		pluralCount(estimate.Baseline.TotalNodeCount, "node"))
	_, _ = fmt.Fprintf(out, "Estimate:  %s a month for %s\n", formatCostCents(estimate.Estimate.MonthlyCents, estimate.Currency),
		pluralCount(estimate.Estimate.TotalNodeCount, "node"))
	_, _ = fmt.Fprintf(out, "Metered:   %s a month\n", formatOptionalCostCents(estimate.MeteredMonthlyCents, estimate.Currency))
	_, _ = fmt.Fprintf(out, "Predicted: %s a month\n", formatOptionalCostCents(estimate.PredictedMonthlyCents, estimate.Currency))
	_, _ = fmt.Fprintf(out, "Change:    %s a month\n", formatOptionalCostCents(estimate.DeltaMonthlyCents, estimate.Currency))
	if estimate.Baseline.CoverageIncomplete || estimate.Estimate.CoverageIncomplete {
		_, _ = fmt.Fprintln(out, "Some lines could not be priced, so the figures are a floor.")
	}
	return nil
}

func init() {
	costCmd.AddCommand(newCostRateCardsCommand(), newCostWasteCommand(), newCostEstimateCommand())
}
