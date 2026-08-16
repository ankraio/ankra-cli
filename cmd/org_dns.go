package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var orgDnsCmd = &cobra.Command{
	Use:   "dns",
	Short: "Manage DNS records in the organisation's delegated zone",
	Long: `Manage CNAME/A/TXT records under the organisation's own delegated DNS
zone (shown by 'ankra org dns zone'). Records are reconciled asynchronously:
a new or edited record starts in state pending and turns active once it is
published to the authoritative nameservers.

  ankra org dns zone
  ankra org dns list
  ankra org dns add chat CNAME lb-1234.example-lb.com --ttl 300
  ankra org dns update chat target.example.com
  ankra org dns delete chat

Creating, editing, and deleting records requires organisation admin.`,
}

var orgDnsZoneCmd = &cobra.Command{
	Use:   "zone",
	Short: "Show the organisation's delegated DNS zone",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		zone, err := apiClient.GetOrganisationDnsZone(ctx)
		if err != nil {
			return fmt.Errorf("get organisation DNS zone: %w", err)
		}
		switch out {
		case outputJSON:
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(zone)
		case outputYAML:
			enc := yaml.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent(2)
			defer func() { _ = enc.Close() }()
			return enc.Encode(zone)
		}
		if zone.State == "none" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No DNS zone is provisioned for this organisation yet.")
			return nil
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Zone:  %s\nState: %s\n", zone.FQDN, zone.State)
		return nil
	},
}

var orgDnsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's DNS records",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		list, err := apiClient.ListOrganisationDnsRecords(ctx)
		if err != nil {
			return fmt.Errorf("list DNS records: %w", err)
		}
		return renderDnsRecords(cmd.OutOrStdout(), list.Items, out)
	},
}

var orgDnsAddCmd = &cobra.Command{
	Use:   "add <name> <type> <content>",
	Short: "Add a DNS record to the organisation's zone",
	Long: `Add a record to the organisation's delegated zone. The name is the label
inside the zone (the zone fqdn is appended server-side), the type is one of
CNAME, A, or TXT, and the content is the record target.

  ankra org dns add chat CNAME lb-1234.example-lb.com --ttl 300
  ankra org dns add app A 203.0.113.10
  ankra org dns add eu TXT "v=spf1 ~all"`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, recordType, content := args[0], strings.ToUpper(args[1]), args[2]
		ttl := ttlFromFlags(cmd)

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		record, err := apiClient.CreateOrganisationDnsRecord(ctx, name, recordType, content, ttl)
		if err != nil {
			return fmt.Errorf("add DNS record: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "DNS record %s %s -> %s created (state: %s).\n",
			record.RecordType, record.Name, record.Content, record.State)
		return nil
	},
}

var orgDnsUpdateCmd = &cobra.Command{
	Use:   "update <record> <content>",
	Short: "Re-point an existing DNS record at new content",
	Long: `Re-point a record at new content. The record is referenced by its id or
by its name (the label or the full fqdn); the name and type of a record
never change - delete and re-add to rename. Pass --ttl to change the ttl at
the same time.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		reference, content := args[0], args[1]
		recordType, _ := cmd.Flags().GetString("type")
		ttl := ttlFromFlags(cmd)

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		recordID, err := resolveDnsRecordID(ctx, reference, recordType)
		if err != nil {
			return err
		}
		record, err := apiClient.UpdateOrganisationDnsRecord(ctx, recordID, content, ttl)
		if err != nil {
			return fmt.Errorf("update DNS record: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "DNS record %s %s -> %s updated (state: %s).\n",
			record.RecordType, record.Name, record.Content, record.State)
		return nil
	},
}

var orgDnsDeleteCmd = &cobra.Command{
	Use:   "delete <record>",
	Short: "Delete a DNS record from the organisation's zone",
	Long: `Delete a record, referenced by its id or by its name (the label or the
full fqdn). When several records share a name, disambiguate with --type.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reference := args[0]
		recordType, _ := cmd.Flags().GetString("type")
		yes, _ := cmd.Flags().GetBool("yes")

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		recordID, err := resolveDnsRecordID(ctx, reference, recordType)
		if err != nil {
			return err
		}

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete DNS record %q? [y/N]: ", reference),
			yes,
		); err != nil {
			return err
		}

		if err := apiClient.DeleteOrganisationDnsRecord(ctx, recordID); err != nil {
			if errors.Is(err, client.ErrDnsRecordNotFound) {
				return fmt.Errorf("DNS record %q not found", reference)
			}
			return fmt.Errorf("delete DNS record: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "DNS record %q deleted.\n", reference)
		return nil
	},
}

// ttlFromFlags returns the --ttl value, nil when the flag was not passed so
// the backend keeps its Auto default (or an edited record's existing ttl).
func ttlFromFlags(cmd *cobra.Command) *int {
	flag := cmd.Flags().Lookup("ttl")
	if flag == nil || !flag.Changed {
		return nil
	}
	value, err := cmd.Flags().GetInt("ttl")
	if err != nil {
		return nil
	}
	return &value
}

// resolveDnsRecordID resolves a record reference - an id, the record's full
// fqdn, or its label inside the zone - to the record id, listing candidates
// when the reference is ambiguous.
func resolveDnsRecordID(ctx context.Context, reference string, recordType string) (string, error) {
	if looksLikeUUID(reference) {
		return reference, nil
	}

	list, err := apiClient.ListOrganisationDnsRecords(ctx)
	if err != nil {
		return "", fmt.Errorf("list DNS records: %w", err)
	}
	normalisedReference := strings.ToLower(strings.TrimSuffix(reference, "."))
	wantedType := strings.ToUpper(recordType)
	var matches []client.DnsRecord
	for _, record := range list.Items {
		if record.Name != normalisedReference && !strings.HasPrefix(record.Name, normalisedReference+".") {
			continue
		}
		if wantedType != "" && record.RecordType != wantedType {
			continue
		}
		matches = append(matches, record)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no DNS record matches %q; run 'ankra org dns list'", reference)
	case 1:
		return matches[0].ID, nil
	default:
		candidates := make([]string, 0, len(matches))
		for _, record := range matches {
			candidates = append(candidates, fmt.Sprintf("%s %s (%s)", record.RecordType, record.Name, record.ID))
		}
		return "", fmt.Errorf("%q is ambiguous, disambiguate with --type or use an id:\n  %s",
			reference, strings.Join(candidates, "\n  "))
	}
}

func renderDnsRecords(out io.Writer, records []client.DnsRecord, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(client.DnsRecordsListResult{Items: records})
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(client.DnsRecordsListResult{Items: records})
	}
	if len(records) == 0 {
		_, _ = fmt.Fprintln(out, "No DNS records.")
		return nil
	}
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"NAME", "TYPE", "CONTENT", "TTL", "STATE", "ERROR"})
	for _, record := range records {
		ttl := "Auto"
		if record.TTL != nil {
			ttl = fmt.Sprintf("%d", *record.TTL)
		}
		lastError := ""
		if record.LastError != nil {
			lastError = truncateForDisplay(*record.LastError, 40)
		}
		t.AppendRow(table.Row{record.Name, record.RecordType, truncateForDisplay(record.Content, 50), ttl, record.State, lastError})
	}
	t.Render()
	return nil
}

func init() {
	registerStructuredOutputFlags(orgDnsZoneCmd, orgDnsListCmd)
	orgDnsAddCmd.Flags().Int("ttl", 0, "TTL in seconds (30..86400); omitted means Auto")
	orgDnsUpdateCmd.Flags().Int("ttl", 0, "TTL in seconds (30..86400); omitted keeps the record's current ttl")
	orgDnsUpdateCmd.Flags().String("type", "", "Record type (CNAME, A, TXT) to disambiguate a name reference")
	orgDnsDeleteCmd.Flags().String("type", "", "Record type (CNAME, A, TXT) to disambiguate a name reference")
	orgDnsDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	orgDnsCmd.AddCommand(orgDnsZoneCmd)
	orgDnsCmd.AddCommand(orgDnsListCmd)
	orgDnsCmd.AddCommand(orgDnsAddCmd)
	orgDnsCmd.AddCommand(orgDnsUpdateCmd)
	orgDnsCmd.AddCommand(orgDnsDeleteCmd)
	orgCmd.AddCommand(orgDnsCmd)
}
