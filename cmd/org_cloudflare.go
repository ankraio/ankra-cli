package cmd

// The Cloudflare domain commands: connect a scoped API token, list the
// domains it reaches, and manage the DNS records inside them.
//
// Records live in the customer's own Cloudflare account and take effect
// immediately, so the destructive commands confirm by default and the delete
// prompt names the record it is about to remove.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var orgCloudflareCmd = &cobra.Command{
	Use:   "cloudflare",
	Short: "Manage the DNS records of your Cloudflare-hosted domains",
	Long: `Manage the domains your Cloudflare account holds, from Ankra.

Connect a scoped Cloudflare API token once, then list your domains and manage
their DNS records. Records are written into your own Cloudflare account and
take effect immediately - there is no pending state to wait for.

  ankra org cloudflare connect production
  ankra org cloudflare domains
  ankra org cloudflare records example.com
  ankra org cloudflare add example.com app A 203.0.113.10 --ttl 300
  ankra org cloudflare update example.com app 203.0.113.11
  ankra org cloudflare delete example.com app

Connecting a credential and changing records requires organisation admin.

Ankra needs a scoped API token, never a Global API Key: a key carries every
permission on every zone in your account. Create a token with Zone:Read on the
zones Ankra should see and DNS:Edit on the zones it should write to.`,
}

var orgCloudflareCredentialsCmd = &cobra.Command{
	Use:   "credentials",
	Short: "List the organisation's connected Cloudflare credentials",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		list, err := apiClient.ListCloudflareCredentials(ctx)
		if err != nil {
			return cloudflareError(err, "list Cloudflare credentials")
		}
		return renderCloudflareCredentials(cmd.OutOrStdout(), list.Items, out)
	},
}

var orgCloudflareConnectCmd = &cobra.Command{
	Use:   "connect <name>",
	Short: "Connect a Cloudflare API token to the organisation",
	Long: `Connect a scoped Cloudflare API token under the given credential name.

The token is read from the ANKRA_CLOUDFLARE_API_TOKEN environment variable, or
from stdin with --token-stdin - never from a flag, so it does not land in your
shell history or the process list. Ankra verifies it with Cloudflare before
storing anything, and refuses a token that reaches no zones: such a token
authenticates perfectly and manages nothing.

Pass --verify-only to check a token without storing it.

  ANKRA_CLOUDFLARE_API_TOKEN=... ankra org cloudflare connect production
  cat token.txt | ankra org cloudflare connect production --token-stdin
  ANKRA_CLOUDFLARE_API_TOKEN=... ankra org cloudflare connect --verify-only`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		verifyOnly, _ := cmd.Flags().GetBool("verify-only")
		accountID, _ := cmd.Flags().GetString("account-id")

		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		if !verifyOnly && name == "" {
			return errors.New("a credential name is required; pass --verify-only to check a token without storing it")
		}
		// --verify-only stores nothing, so a name would be discarded. Saying
		// so beats accepting it and leaving the operator believing they
		// connected a credential.
		if verifyOnly && name != "" {
			return fmt.Errorf("--verify-only stores nothing, so the credential name %q would be ignored; "+
				"drop the name to check the token, or drop --verify-only to connect it", name)
		}

		apiToken, err := readCloudflareToken(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
		defer cancel()

		if verifyOnly {
			verification, err := apiClient.VerifyCloudflareToken(ctx, apiToken, accountID)
			if err != nil {
				return cloudflareError(err, "verify the Cloudflare token")
			}
			renderCloudflareVerification(cmd.OutOrStdout(), verification, "")
			return nil
		}

		verification, err := apiClient.ConnectCloudflareCredential(ctx, name, apiToken, accountID)
		if err != nil {
			return cloudflareError(err, "connect Cloudflare")
		}
		renderCloudflareVerification(cmd.OutOrStdout(), verification, name)
		return nil
	},
}

var orgCloudflareDomainsCmd = &cobra.Command{
	Use:   "domains [name]",
	Short: "List the Cloudflare domains the organisation can reach",
	Long: `List the domains (zones) the connected Cloudflare credential reaches.

Pass a domain name to resolve just that one, which costs a single lookup
rather than a walk through every domain in the account. An empty result means
Ankra does not reach that domain - either it is not in Cloudflare, or the
connected token is scoped away from it.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		credentialName, _ := cmd.Flags().GetString("credential")
		domainName := ""
		if len(args) == 1 {
			domainName = args[0]
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		list, err := apiClient.ListCloudflareDomains(ctx, credentialName, domainName)
		if err != nil {
			return cloudflareError(err, "list Cloudflare domains")
		}
		if list.IsTruncated {
			// Said out loud rather than dropped: a partial page read as the
			// whole account is how "that domain is not in Cloudflare" gets
			// concluded wrongly.
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"Note: more domains exist than were listed. Pass a domain name to resolve one directly.")
		}
		return renderCloudflareDomains(cmd.OutOrStdout(), list.Items, out)
	},
}

var orgCloudflareRecordsCmd = &cobra.Command{
	Use:   "records <domain>",
	Short: "List the DNS records in a Cloudflare domain",
	Long: `List a domain's DNS records. The domain is referenced by name or by its
Cloudflare zone id.

Each record reports whether Ankra created it (MANAGED "ankra") or it was made
elsewhere (MANAGED "external") - which is how to tell a platform-published
record from one written by hand, by Terraform, or by a cluster controller.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		credentialName, _ := cmd.Flags().GetString("credential")
		nameFilter, _ := cmd.Flags().GetString("name")
		typeFilter, _ := cmd.Flags().GetString("type")

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		domainID, err := resolveCloudflareDomainID(ctx, credentialName, args[0])
		if err != nil {
			return err
		}
		list, err := apiClient.ListCloudflareRecords(ctx, credentialName, domainID, nameFilter, strings.ToUpper(typeFilter))
		if err != nil {
			return cloudflareError(err, "list DNS records")
		}
		if list.IsTruncated {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"Note: more records exist than were listed. Narrow with --name or --type.")
		}
		return renderCloudflareRecords(cmd.OutOrStdout(), list.Items, out)
	},
}

var orgCloudflareAddCmd = &cobra.Command{
	Use:   "add <domain> <name> <type> <content>",
	Short: "Add a DNS record to a Cloudflare domain",
	Long: `Add a DNS record. The name is a bare label inside the domain, a
fully-qualified name inside it, or '@' for the domain itself.

  ankra org cloudflare add example.com app A 203.0.113.10 --ttl 300
  ankra org cloudflare add example.com www CNAME app.example.com --proxied
  ankra org cloudflare add example.com @ TXT "v=spf1 -all"
  ankra org cloudflare add example.com @ MX mail.example.com --priority 10

Only A, AAAA and CNAME records can be proxied through Cloudflare. A proxied
record's TTL is managed by Cloudflare, so --ttl is refused with --proxied.`,
	Args: cobra.ExactArgs(4),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialName, _ := cmd.Flags().GetString("credential")
		domainReference, name, recordType, content := args[0], args[1], strings.ToUpper(args[2]), args[3]

		if err := refuseTTLWithProxy(cmd); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		domainID, err := resolveCloudflareDomainID(ctx, credentialName, domainReference)
		if err != nil {
			return err
		}

		input := client.CreateCloudflareRecordInput{
			Name:       name,
			RecordType: recordType,
			Content:    content,
			TTL:        cloudflareTTLFromFlags(cmd),
			Priority:   cloudflareIntFlag(cmd, "priority"),
		}
		if proxied, _ := cmd.Flags().GetBool("proxied"); proxied {
			input.Proxied = &proxied
		}
		if comment, _ := cmd.Flags().GetString("comment"); comment != "" {
			input.Comment = comment
		}

		record, err := apiClient.CreateCloudflareRecord(ctx, credentialName, domainID, input)
		if err != nil {
			return cloudflareError(err, "add the DNS record")
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "DNS record %s %s -> %s created%s.\n",
			record.RecordType, record.Name, record.Content, proxiedSuffix(record.Proxied))
		return nil
	},
}

var orgCloudflareUpdateCmd = &cobra.Command{
	Use:   "update <domain> <record> <content>",
	Short: "Re-point a DNS record at new content",
	Long: `Re-point a record at new content. The record is referenced by its id or by
its name; a record's name and type never change - delete and re-add instead.

Only the fields you pass change: an omitted --ttl, --proxied or --priority
keeps the record's current value.`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialName, _ := cmd.Flags().GetString("credential")
		domainReference, recordReference, content := args[0], args[1], args[2]
		recordType, _ := cmd.Flags().GetString("type")

		if err := refuseTTLWithProxy(cmd); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		domainID, err := resolveCloudflareDomainID(ctx, credentialName, domainReference)
		if err != nil {
			return err
		}
		recordID, err := resolveCloudflareRecordID(ctx, credentialName, domainID, recordReference, recordType)
		if err != nil {
			return err
		}

		input := client.UpdateCloudflareRecordInput{
			Content:  content,
			TTL:      cloudflareTTLFromFlags(cmd),
			Priority: cloudflareIntFlag(cmd, "priority"),
		}
		if cmd.Flags().Changed("proxied") {
			proxied, _ := cmd.Flags().GetBool("proxied")
			input.Proxied = &proxied
		}
		if comment, _ := cmd.Flags().GetString("comment"); comment != "" {
			input.Comment = comment
		}

		record, err := apiClient.UpdateCloudflareRecord(ctx, credentialName, domainID, recordID, input)
		if err != nil {
			return cloudflareError(err, "update the DNS record")
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "DNS record %s %s -> %s updated%s.\n",
			record.RecordType, record.Name, record.Content, proxiedSuffix(record.Proxied))
		return nil
	},
}

var orgCloudflareDeleteCmd = &cobra.Command{
	Use:   "delete <domain> <record>",
	Short: "Delete a DNS record from a Cloudflare domain",
	Long: `Delete a DNS record, referenced by its id or by its name. When several
records share a name, disambiguate with --type.

The record lives in your own Cloudflare account: Ankra cannot restore it, and
removing a record live traffic depends on takes the hostname down immediately.
The prompt names the record before removing it.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialName, _ := cmd.Flags().GetString("credential")
		domainReference, recordReference := args[0], args[1]
		recordType, _ := cmd.Flags().GetString("type")
		yes, _ := cmd.Flags().GetBool("yes")

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		domainID, err := resolveCloudflareDomainID(ctx, credentialName, domainReference)
		if err != nil {
			return err
		}
		record, err := resolveCloudflareRecord(ctx, credentialName, domainID, recordReference, recordType)
		if err != nil {
			return err
		}

		// The prompt names the record's type, name and content: "delete app"
		// is not enough to approve removing live DNS. A record Ankra did not
		// create is called out, because something outside the platform put it
		// there.
		prompt := fmt.Sprintf("Delete %s %s -> %s from %s?",
			record.RecordType, record.Name, record.Content, record.ZoneName)
		if record.ManagedBy != "ankra" {
			prompt += " This record was not created through Ankra."
		}
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), prompt+" [y/N]: ", yes); err != nil {
			return err
		}

		if err := apiClient.DeleteCloudflareRecord(ctx, credentialName, domainID, record.ID); err != nil {
			return cloudflareError(err, "delete the DNS record")
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "DNS record %s %s deleted.\n", record.RecordType, record.Name)
		return nil
	},
}

// --- helpers ---

// refuseTTLWithProxy enforces what the help text promises: Cloudflare owns a
// proxied record's TTL and rejects an explicit one, so the combination is
// refused here with a message naming both flags rather than forwarded for the
// API to reject with a code nobody reads.
func refuseTTLWithProxy(cmd *cobra.Command) error {
	if !cmd.Flags().Changed("ttl") || !cmd.Flags().Changed("proxied") {
		return nil
	}
	proxied, _ := cmd.Flags().GetBool("proxied")
	if !proxied {
		return nil
	}
	return errors.New("--ttl cannot be combined with --proxied: Cloudflare manages a proxied record's TTL")
}

// cloudflareTokenEnvName is where the token is read from, so it never lands in
// shell history the way a --token flag would.
const cloudflareTokenEnvName = "ANKRA_CLOUDFLARE_API_TOKEN"

// readCloudflareToken takes the token from the environment or from stdin,
// never from a flag.
//
// A --token flag would put a live credential in shell history and in the
// process list. Prompting interactively is not offered either: without a
// terminal-control dependency the input cannot be read with echo disabled,
// and a token echoed into scrollback is no better than one in history. Env
// var and --token-stdin both work in scripts and CI, which is where this
// command mostly runs.
func readCloudflareToken(cmd *cobra.Command) (string, error) {
	fromStdin, _ := cmd.Flags().GetBool("token-stdin")
	if fromStdin {
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read the API token from stdin: %w", err)
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return "", errors.New("no API token was read from stdin")
		}
		return token, nil
	}
	if fromEnvironment := strings.TrimSpace(os.Getenv(cloudflareTokenEnvName)); fromEnvironment != "" {
		return fromEnvironment, nil
	}
	return "", fmt.Errorf("no API token supplied. Set %s, or pipe the token in with --token-stdin:\n"+
		"  %s=... ankra org cloudflare connect <name>\n"+
		"  cat token.txt | ankra org cloudflare connect <name> --token-stdin",
		cloudflareTokenEnvName, cloudflareTokenEnvName)
}

// cloudflareError renders the backend's own guidance where it has some, and
// falls back to a wrapped error otherwise. The two sentinels carry the next
// step, which a bare 404 would not.
func cloudflareError(err error, action string) error {
	if errors.Is(err, client.ErrCloudflareNotConnected) {
		return errors.New("no Cloudflare credential is connected for this organisation.\n" +
			"Connect one with 'ankra org cloudflare connect <name>'")
	}
	if errors.Is(err, client.ErrCloudflareNotFound) {
		return err
	}
	return fmt.Errorf("%s: %w", action, err)
}

func proxiedSuffix(proxied bool) string {
	if proxied {
		return " (proxied through Cloudflare)"
	}
	return ""
}

// cloudflareTTLFromFlags returns --ttl, nil when unset so the backend keeps
// Cloudflare's automatic TTL on create and the record's current TTL on update.
func cloudflareTTLFromFlags(cmd *cobra.Command) *int {
	return cloudflareIntFlag(cmd, "ttl")
}

// cloudflareIntFlag returns a flag the caller actually passed, or nil.
//
// A parse failure is impossible for a flag declared as an Int - cobra rejects
// a non-integer at parse time - so it is reported rather than swallowed. The
// previous shape returned nil on error, which would have turned a malformed
// value into a silent "leave this field as it is".
func cloudflareIntFlag(cmd *cobra.Command, name string) *int {
	flag := cmd.Flags().Lookup(name)
	if flag == nil || !flag.Changed {
		return nil
	}
	value, err := cmd.Flags().GetInt(name)
	if err != nil {
		panic(fmt.Sprintf("flag --%s is declared as an int but did not read back as one: %v", name, err))
	}
	return &value
}

// resolveCloudflareDomainID resolves a domain reference - a Cloudflare zone id
// or a domain name - to the zone id.
//
// A name is resolved through the server-side exact-name filter, which is one
// lookup rather than a walk through every domain in the account.
func resolveCloudflareDomainID(ctx context.Context, credentialName, reference string) (string, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(reference), ".")
	if trimmed == "" {
		return "", errors.New("a domain name or zone id is required")
	}
	// A Cloudflare zone id is 32 hex characters and carries no dot; a domain
	// name always has one. Treating a dotted reference as a name avoids a
	// pointless lookup for the common case.
	if !strings.Contains(trimmed, ".") {
		return trimmed, nil
	}
	list, err := apiClient.ListCloudflareDomains(ctx, credentialName, trimmed)
	if err != nil {
		return "", cloudflareError(err, "resolve the domain")
	}
	if len(list.Items) == 0 {
		return "", fmt.Errorf("no Cloudflare domain named %q is reachable; run 'ankra org cloudflare domains'", reference)
	}
	return list.Items[0].ID, nil
}

func resolveCloudflareRecordID(ctx context.Context, credentialName, domainID, reference, recordType string) (string, error) {
	record, err := resolveCloudflareRecord(ctx, credentialName, domainID, reference, recordType)
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

// resolveCloudflareRecord resolves a record reference - an id, a full name, or
// a bare label - to the record, listing candidates when it is ambiguous.
//
// The exact name is tried as a server-side filter first. That keeps the lookup
// correct on a zone with more records than one page holds: scanning only the
// first page would report an existing record as missing, and for delete that
// wrong negative is the dangerous direction - the user re-adds a duplicate of
// a record that was there all along.
func resolveCloudflareRecord(ctx context.Context, credentialName, domainID, reference, recordType string) (*client.CloudflareRecord, error) {
	normalisedReference := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(reference), "."))
	wantedType := strings.ToUpper(recordType)

	list, err := apiClient.ListCloudflareRecords(ctx, credentialName, domainID, normalisedReference, wantedType)
	if err != nil {
		return nil, cloudflareError(err, "list DNS records")
	}
	if len(list.Items) == 0 {
		// The reference may be a bare label or a record id, neither of which
		// the exact-name filter matches. Fall back to the full listing.
		list, err = apiClient.ListCloudflareRecords(ctx, credentialName, domainID, "", wantedType)
		if err != nil {
			return nil, cloudflareError(err, "list DNS records")
		}
	}

	var matches []client.CloudflareRecord
	for _, record := range list.Items {
		isMatch := record.ID == reference ||
			strings.ToLower(record.Name) == normalisedReference ||
			strings.HasPrefix(strings.ToLower(record.Name), normalisedReference+".")
		if !isMatch {
			continue
		}
		if wantedType != "" && record.RecordType != wantedType {
			continue
		}
		matches = append(matches, record)
	}
	switch len(matches) {
	case 0:
		// A capped listing has not been fully observed, so "no match" is not a
		// fact about the zone. Say what was actually seen rather than
		// declaring the record absent.
		if list.IsTruncated {
			return nil, fmt.Errorf("could not resolve %q: the record listing for that domain was truncated, "+
				"so this is not proof the record is absent. Pass the record's id instead, "+
				"or narrow with --type", reference)
		}
		return nil, fmt.Errorf("no DNS record matches %q in that domain; run 'ankra org cloudflare records <domain>'", reference)
	case 1:
		return &matches[0], nil
	default:
		candidates := make([]string, 0, len(matches))
		for _, record := range matches {
			candidates = append(candidates, fmt.Sprintf("%s %s -> %s (%s)",
				record.RecordType, record.Name, record.Content, record.ID))
		}
		return nil, fmt.Errorf("%q is ambiguous, disambiguate with --type or use an id:\n  %s",
			reference, strings.Join(candidates, "\n  "))
	}
}

// --- rendering ---

func renderCloudflareVerification(out io.Writer, verification *client.CloudflareVerification, connectedName string) {
	if verification == nil {
		if connectedName != "" {
			_, _ = fmt.Fprintf(out, "Cloudflare credential %q connected.\n", connectedName)
		}
		return
	}
	if connectedName != "" {
		_, _ = fmt.Fprintf(out, "Cloudflare credential %q connected.\n", connectedName)
	}
	if !verification.IsActive {
		_, _ = fmt.Fprintf(out, "Cloudflare reports this token as %s. Renew or re-enable it in Cloudflare.\n",
			verification.Status)
		return
	}
	account := verification.AccountName
	if account == "" {
		account = verification.AccountID
	}
	if account != "" {
		_, _ = fmt.Fprintf(out, "Account: %s\n", account)
	}
	_, _ = fmt.Fprintf(out, "Reaches %d domain%s", verification.ZoneCount, plural(verification.ZoneCount))
	if len(verification.ZoneNames) > 0 {
		_, _ = fmt.Fprintf(out, ": %s", strings.Join(verification.ZoneNames, ", "))
		if verification.ZoneCount > len(verification.ZoneNames) {
			_, _ = fmt.Fprint(out, ", ...")
		}
	}
	_, _ = fmt.Fprintln(out)
	if verification.ExpiresOn != "" {
		_, _ = fmt.Fprintf(out, "Token expires: %s\n", verification.ExpiresOn)
	}
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func renderCloudflareCredentials(out io.Writer, credentials []client.CloudflareCredential, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(client.CloudflareCredentialsListResult{Items: credentials})
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(client.CloudflareCredentialsListResult{Items: credentials})
	}
	if len(credentials) == 0 {
		_, _ = fmt.Fprintln(out, "No Cloudflare credentials connected. Connect one with 'ankra org cloudflare connect <name>'.")
		return nil
	}
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"NAME", "ACCOUNT", "TOKEN", "EXPIRES", "STATUS"})
	for _, credential := range credentials {
		account := credential.AccountName
		if account == "" {
			account = credential.AccountID
		}
		expires := credential.TokenExpiresAt
		if expires == "" {
			expires = "never"
		} else if len(expires) >= 10 {
			expires = expires[:10]
		}
		status := credential.State
		if credential.IsExpired {
			status = "expired"
		}
		t.AppendRow(table.Row{credential.Name, account, credential.TokenID, expires, status})
	}
	t.Render()
	return nil
}

func renderCloudflareDomains(out io.Writer, domains []client.CloudflareDomain, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(client.CloudflareDomainsListResult{Items: domains})
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(client.CloudflareDomainsListResult{Items: domains})
	}
	if len(domains) == 0 {
		_, _ = fmt.Fprintln(out, "No Cloudflare domains are reachable with the connected credential.")
		return nil
	}
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"NAME", "ZONE ID", "STATUS", "TYPE", "NAMESERVERS"})
	for _, domain := range domains {
		t.AppendRow(table.Row{
			domain.Name, domain.ID, domain.Status, domain.Type,
			truncateForDisplay(strings.Join(domain.NameServers, ", "), 44),
		})
	}
	t.Render()
	return nil
}

func renderCloudflareRecords(out io.Writer, records []client.CloudflareRecord, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(client.CloudflareRecordsListResult{Items: records})
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(client.CloudflareRecordsListResult{Items: records})
	}
	if len(records) == 0 {
		_, _ = fmt.Fprintln(out, "No DNS records in that domain.")
		return nil
	}
	t := table.NewWriter()
	t.SetOutputMirror(out)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"NAME", "TYPE", "CONTENT", "TTL", "PROXY", "MANAGED"})
	for _, record := range records {
		ttl := "Auto"
		if !record.IsTTLAutomatic {
			ttl = fmt.Sprintf("%d", record.TTL)
		}
		proxy := "direct"
		if record.Proxied {
			proxy = "proxied"
		}
		content := record.Content
		if record.Priority != nil {
			content = fmt.Sprintf("%s (priority %d)", content, *record.Priority)
		}
		t.AppendRow(table.Row{
			record.Name, record.RecordType, truncateForDisplay(content, 46), ttl, proxy, record.ManagedBy,
		})
	}
	t.Render()
	return nil
}

func init() {
	registerStructuredOutputFlags(orgCloudflareCredentialsCmd, orgCloudflareDomainsCmd, orgCloudflareRecordsCmd)

	// --credential selects among several connected credentials; with one
	// connected the backend resolves it, so it stays optional everywhere.
	for _, command := range []*cobra.Command{
		orgCloudflareDomainsCmd, orgCloudflareRecordsCmd, orgCloudflareAddCmd,
		orgCloudflareUpdateCmd, orgCloudflareDeleteCmd,
	} {
		command.Flags().String("credential", "",
			"Which connected Cloudflare credential to use (only needed with several)")
	}

	orgCloudflareConnectCmd.Flags().Bool("verify-only", false, "Check the token without storing it")
	orgCloudflareConnectCmd.Flags().Bool("token-stdin", false,
		"Read the API token from stdin instead of "+cloudflareTokenEnvName)
	orgCloudflareConnectCmd.Flags().String("account-id", "",
		"Pin every domain listing to one Cloudflare account (only needed for a token spanning several)")

	orgCloudflareRecordsCmd.Flags().String("name", "", "Filter to this exact record name")
	orgCloudflareRecordsCmd.Flags().String("type", "", "Filter to this record type")

	orgCloudflareAddCmd.Flags().Int("ttl", 0, "TTL in seconds (60..86400); omitted means Cloudflare's Auto")
	orgCloudflareAddCmd.Flags().Bool("proxied", false, "Route the record through Cloudflare's proxy (A, AAAA and CNAME only)")
	orgCloudflareAddCmd.Flags().Int("priority", 0, "Priority for MX and SRV records")
	orgCloudflareAddCmd.Flags().String("comment", "", "Comment stored on the record")

	orgCloudflareUpdateCmd.Flags().Int("ttl", 0, "TTL in seconds (60..86400); omitted keeps the record's current TTL")
	orgCloudflareUpdateCmd.Flags().Bool("proxied", false, "Route the record through Cloudflare's proxy; omitted keeps the current setting")
	orgCloudflareUpdateCmd.Flags().Int("priority", 0, "Priority for MX and SRV records; omitted keeps the current priority")
	orgCloudflareUpdateCmd.Flags().String("comment", "", "Comment stored on the record")
	orgCloudflareUpdateCmd.Flags().String("type", "", "Record type to disambiguate a name reference")

	orgCloudflareDeleteCmd.Flags().String("type", "", "Record type to disambiguate a name reference")
	orgCloudflareDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	orgCloudflareCmd.AddCommand(orgCloudflareCredentialsCmd)
	orgCloudflareCmd.AddCommand(orgCloudflareConnectCmd)
	orgCloudflareCmd.AddCommand(orgCloudflareDomainsCmd)
	orgCloudflareCmd.AddCommand(orgCloudflareRecordsCmd)
	orgCloudflareCmd.AddCommand(orgCloudflareAddCmd)
	orgCloudflareCmd.AddCommand(orgCloudflareUpdateCmd)
	orgCloudflareCmd.AddCommand(orgCloudflareDeleteCmd)
	orgCmd.AddCommand(orgCloudflareCmd)
}
