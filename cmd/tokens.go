package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var tokensCmd = &cobra.Command{
	Use:     "tokens",
	Aliases: []string{"token"},
	Short:   "Manage API tokens",
	Long: `Commands to list, create, rotate, revoke, and delete API tokens.

By default they act on your personal tokens. --service on list, create and
rotate acts on the organisation's service tokens instead: tokens that belong
to a service account rather than a person, for pipelines and other
automation. Listing and creating them needs the members.manage permission.`,
}

var tokenSortFields = []sortField[client.APIToken]{
	{"id", func(a, b client.APIToken) int { return compareFold(a.ID, b.ID) }},
	{"name", func(a, b client.APIToken) int { return compareFold(a.Name, b.Name) }},
	{"status", func(a, b client.APIToken) int { return compareBools(a.Revoked, b.Revoked) }},
	{"created", func(a, b client.APIToken) int { return compareTimeStrings(a.CreatedAt, b.CreatedAt) }},
	{"expires", func(a, b client.APIToken) int { return compareTimeStrings(a.ExpiresAt, b.ExpiresAt) }},
	{"last-used", func(a, b client.APIToken) int { return compareTimeStringPtrs(a.LastUsedAt, b.LastUsedAt) }},
}

var serviceTokenSortFields = []sortField[client.ServiceToken]{
	{"id", func(a, b client.ServiceToken) int { return compareFold(a.ID, b.ID) }},
	{"name", func(a, b client.ServiceToken) int {
		if byAccount := compareFold(a.AccountName, b.AccountName); byAccount != 0 {
			return byAccount
		}
		return compareFold(a.Label, b.Label)
	}},
	{"status", func(a, b client.ServiceToken) int { return compareBools(a.Revoked, b.Revoked) }},
	{"created", func(a, b client.ServiceToken) int { return compareTimeStrings(a.CreatedAt, b.CreatedAt) }},
	{"expires", func(a, b client.ServiceToken) int { return compareTimeStrings(a.ExpiresAt, b.ExpiresAt) }},
	{"last-used", func(a, b client.ServiceToken) int { return compareTimeStringPtrs(a.LastUsedAt, b.LastUsedAt) }},
}

var tokensListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all API tokens",
	Long: `List your API tokens with when each one expires.

A live token that expires within 7 days reads "Expiring" in the Status
column and an expired one "Expired"; the Expires column shows the date and
how many days are left. Renew an expiring token before it stops working with
"ankra tokens rotate <id>".

--service lists the organisation's service tokens instead, with the service
account each belongs to and the role it holds; renew one with
"ankra tokens rotate <id> --service". It needs members.manage (exit 7
without it).`,
	Example: `  ankra tokens list
  ankra tokens list --service
  ankra tokens list --service -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if isService, _ := cmd.Flags().GetBool("service"); isService {
			return listServiceTokens(cmd)
		}
		sortTokens, err := resolveSort(cmd, tokenSortFields)
		if err != nil {
			return err
		}
		tokens, err := apiClient.ListAPITokens()
		if err != nil {
			return fmt.Errorf("listing tokens: %w", err)
		}

		if tokens == nil {
			tokens = []client.APIToken{}
		}
		sortTokens(tokens)
		if rendered, err := renderStructured(cmd, tokens); rendered || err != nil {
			return err
		}

		if len(tokens) == 0 {
			fmt.Println("No API tokens found.")
			return nil
		}

		now := time.Now()
		expiringSoon := 0
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Status", "Created", "Expires", "Last Used"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 36},
			{Number: 2, WidthMin: 15},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 12},
			{Number: 5, WidthMin: 12},
			{Number: 6, WidthMin: 12},
		})

		for _, tok := range tokens {
			expiry := describeTokenExpiry(tok, now)
			status := text.FgGreen.Sprint("Active")
			switch {
			case tok.Revoked:
				status = text.FgRed.Sprint("Revoked")
			case expiry.isExpired:
				status = text.FgRed.Sprint("Expired")
				expiringSoon++
			case expiry.isExpiringSoon:
				status = text.FgYellow.Sprint("Expiring")
				expiringSoon++
			}

			lastUsed := "Never"
			if tok.LastUsedAt != nil {
				lastUsed = formatTimeAgo(*tok.LastUsedAt)
			}

			t.AppendRow(table.Row{
				tok.ID,
				tok.Name,
				status,
				formatTimeAgo(tok.CreatedAt),
				expiry.text,
				lastUsed,
			})
		}
		t.Render()
		if expiringSoon > 0 {
			fmt.Fprintf(os.Stderr, "%d token(s) expired or expire within 7 days. Renew one with "+
				"`ankra tokens rotate <id>`; the old token keeps working for a day while you update where it is used.\n", expiringSoon)
		}
		return nil
	},
}

// listServiceTokens is 'ankra tokens list --service'.
func listServiceTokens(cmd *cobra.Command) error {
	sortTokens, err := resolveSort(cmd, serviceTokenSortFields)
	if err != nil {
		return err
	}
	tokens, err := apiClient.ListServiceTokens()
	if err != nil {
		return fmt.Errorf("listing service tokens: %w", err)
	}
	if tokens == nil {
		tokens = []client.ServiceToken{}
	}
	sortTokens(tokens)
	if rendered, err := renderStructured(cmd, tokens); rendered || err != nil {
		return err
	}
	if len(tokens) == 0 {
		fmt.Println("No service tokens found. Create one with: ankra tokens create <service-account> --service --role <role>")
		return nil
	}

	now := time.Now()
	expiringSoon := 0
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"ID", "Service account", "Label", "Role", "Status", "Expires", "Last Used"})
	for _, tok := range tokens {
		expiry := describeTokenExpiry(client.APIToken{ExpiresAt: tok.ExpiresAt, DaysLeft: tok.DaysLeft, Revoked: tok.Revoked}, now)
		status := text.FgGreen.Sprint("Active")
		switch {
		case tok.Revoked:
			status = text.FgRed.Sprint("Revoked")
		case expiry.isExpired:
			status = text.FgRed.Sprint("Expired")
			expiringSoon++
		case expiry.isExpiringSoon:
			status = text.FgYellow.Sprint("Expiring")
			expiringSoon++
		}
		// No live role assignment means the account was stood down: it
		// still authenticates, at the viewer floor.
		role := tok.RoleSlug
		if role == "" {
			role = "none"
		}
		lastUsed := "Never"
		if tok.LastUsedAt != nil {
			lastUsed = formatTimeAgo(*tok.LastUsedAt)
		}
		t.AppendRow(table.Row{tok.ID, tok.AccountName, tok.Label, role, status, expiry.text, lastUsed})
	}
	t.Render()
	if expiringSoon > 0 {
		fmt.Fprintf(os.Stderr, "%d service token(s) expired or expire within 7 days. Renew one with "+
			"`ankra tokens rotate <id> --service`; the old token keeps working for a day while you update where it is used.\n", expiringSoon)
	}
	return nil
}

var tokensCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new API token",
	Long: `Create a personal API token named <name>.

With --service, <name> is a service account of the organisation instead, and
the token belongs to that account rather than to you: it keeps working when
you leave the organisation, and acts with the account's role, not yours.
The account is created with --role when the organisation has none of that
name; for an existing account --role must be the role it already holds.
--label names the token itself (default: a timestamp). The role may not
carry permissions you lack, and creating one needs members.manage.

The global --project <slug|name|id> pins the token to that project: it then
acts only there and cannot be pointed elsewhere. A token with MCP scopes is
always pinned - to the project you pass, else to the project you are acting
in; a REST token created without --project follows its owner's current
project and honours --project / ANKRA_PROJECT per command.

The token value is shown once.`,
	Example: `  ankra tokens create laptop --expires 2026-12-31T00:00:00Z
  ankra tokens create ci-deployer --service --role member --label github-actions
  ankra tokens create ci-deployer --service --role member --expires 2027-01-31T00:00:00Z -o json
  ankra tokens create agent-mcp --scopes mcp:read --project platform-team`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		expiresAt, _ := cmd.Flags().GetString("expires")
		scopes, _ := cmd.Flags().GetStringSlice("scopes")
		isService, _ := cmd.Flags().GetBool("service")
		projectID, projectError := tokenProjectPin(cmd)
		if projectError != nil {
			return projectError
		}
		if isService {
			return createServiceToken(cmd, name, expiresAt, scopes, projectID)
		}
		for _, serviceOnly := range []string{"role", "label"} {
			if cmd.Flags().Changed(serviceOnly) {
				return withExitCode(exitUsage, fmt.Errorf("--%s applies only to a service token: add --service", serviceOnly))
			}
		}

		var expiresAtPtr *string
		if expiresAt != "" {
			expiresAtPtr = &expiresAt
		}

		result, err := apiClient.CreateAPIToken(client.CreateAPITokenRequest{
			Name: name, ExpiresAt: expiresAtPtr, Scopes: scopes, ProjectID: projectID,
		})
		if err != nil {
			return fmt.Errorf("creating token: %w", err)
		}

		if rendered, err := renderStructured(cmd, result); rendered || err != nil {
			return err
		}

		fmt.Println("API token created successfully!")
		fmt.Println()
		fmt.Printf("  ID:      %s\n", result.ID)
		fmt.Printf("  Expires: %s\n", formatTimeAgo(result.ExpiresAt))
		fmt.Println()
		fmt.Println("Token (save this, it won't be shown again):")
		fmt.Printf("  %s\n", result.Token)
		fmt.Println()
		if len(scopes) > 0 {
			fmt.Println("Use this token only with the Ankra MCP server:")
			fmt.Println("  URL: https://platform.ankra.app/api/v1/mcp")
			fmt.Printf("  Authorization: Bearer %s\n", result.Token)
			return nil
		}
		fmt.Println("To use this token, set it as ANKRA_API_TOKEN environment variable:")
		fmt.Printf("  export ANKRA_API_TOKEN='%s'\n", result.Token)
		return nil
	},
}

// tokenProjectPin resolves the global --project flag to the project id a new
// token is pinned to, or nil when the flag was not passed. It is the same
// flag that scopes the invocation, so the token is pinned to the project the
// request acts in; ANKRA_PROJECT alone does not pin, the platform decides.
func tokenProjectPin(cmd *cobra.Command) (*string, error) {
	flag := cmd.Root().PersistentFlags().Lookup("project")
	if flag == nil || !flag.Changed {
		return nil, nil
	}
	reference := strings.TrimSpace(flag.Value.String())
	if reference == "" {
		return nil, withExitCode(exitUsage, errors.New("--project needs a project slug, name or id"))
	}
	if resolved := strings.TrimSpace(apiClient.ProjectOverride()); resolved != "" {
		return &resolved, nil
	}
	projectID, err := resolveProjectReferenceToID(reference)
	if err != nil {
		return nil, err
	}
	return &projectID, nil
}

// createServiceToken is 'ankra tokens create <account> --service'.
func createServiceToken(cmd *cobra.Command, accountName string, expiresAt string, scopes []string, projectID *string) error {
	role, _ := cmd.Flags().GetString("role")
	label, _ := cmd.Flags().GetString("label")
	if strings.TrimSpace(role) == "" {
		return withExitCode(exitUsage, errors.New("--role is required with --service: the role the service account acts with (owner, admin, operator, member, viewer, or a custom role's slug)"))
	}
	request := client.CreateServiceTokenRequest{
		Name:      accountName,
		Label:     strings.TrimSpace(label),
		RoleSlug:  strings.TrimSpace(role),
		Scopes:    scopes,
		ProjectID: projectID,
	}
	if trimmed := strings.TrimSpace(expiresAt); trimmed != "" {
		if _, parseError := time.Parse(time.RFC3339, trimmed); parseError != nil {
			return withExitCode(exitUsage, fmt.Errorf("invalid --expires %q: a service token takes an RFC 3339 time, such as 2027-01-31T00:00:00Z", expiresAt))
		}
		request.ExpiresAt = &trimmed
	}

	created, err := apiClient.CreateServiceToken(request)
	if err != nil {
		return fmt.Errorf("creating service token: %w", err)
	}
	if rendered, err := renderStructured(cmd, created); rendered || err != nil {
		return err
	}

	fmt.Printf("Service token created for service account %q (role %s).\n", created.AccountName, created.RoleSlug)
	fmt.Println()
	fmt.Printf("  ID:      %s\n", created.ID)
	fmt.Printf("  Label:   %s\n", created.Label)
	fmt.Printf("  Expires: %s\n", formatTokenDate(created.ExpiresAt))
	fmt.Println()
	fmt.Println("Token (save this, it won't be shown again):")
	fmt.Printf("  %s\n", created.Token)
	fmt.Println()
	if len(scopes) > 0 {
		fmt.Println("Use this token only with the Ankra MCP server:")
		fmt.Println("  URL: https://platform.ankra.app/api/v1/mcp")
		fmt.Printf("  Authorization: Bearer %s\n", created.Token)
		return nil
	}
	fmt.Println("To use it, set it as ANKRA_API_TOKEN where the automation runs:")
	fmt.Printf("  export ANKRA_API_TOKEN='%s'\n", created.Token)
	fmt.Println("List and renew it with: ankra tokens list --service / ankra tokens rotate <id> --service")
	return nil
}

var tokensRevokeCmd = &cobra.Command{
	Use:   "revoke <token_id>",
	Short: "Revoke an API token (can be deleted after)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tokenID := args[0]

		result, err := apiClient.RevokeAPIToken(tokenID)
		if err != nil {
			return fmt.Errorf("revoking token: %w", err)
		}

		if result.Success {
			fmt.Println("Token revoked successfully!")
			fmt.Println("You can now delete it with: ankra tokens delete", tokenID)
		}
		return nil
	},
}

var tokensDeleteCmd = &cobra.Command{
	Use:   "delete <token_id>",
	Short: "Delete a revoked API token",
	Long:  "Delete an API token. The token must be revoked first.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tokenID := args[0]

		result, err := apiClient.DeleteAPIToken(tokenID)
		if err != nil {
			return fmt.Errorf("deleting token (tokens must be revoked before they can be deleted): %w", err)
		}

		if result.Success {
			fmt.Println("Token deleted successfully!")
		}
		return nil
	},
}

const (
	rotateDefaultOverlapHours = 24
	rotateMaxOverlapHours     = 168
)

// stdoutIsTerminal reports whether stdout is a terminal. It is a variable so
// tests can pose as a terminal.
var stdoutIsTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

var tokensRotateCmd = &cobra.Command{
	Use:   "rotate <token_id>",
	Short: "Renew an API token before it expires",
	Long: `Issue a new token to replace one that is about to expire, with the same
name and scopes. The old token keeps working for an overlap window so you can
update every place it is used, then it stops by itself.

The new token value is shown once. When stdout is not a terminal the command
prints only the value on stdout, so this works in scripts:

  TOKEN=$(ankra tokens rotate <token_id>)

All other text goes to stderr. With -o json the whole response is printed.

Use --service to rotate an organisation service token instead of a personal
one. --overlap-hours sets how long the old token keeps working, from 0 (stops
at once) to 168; the platform default is 24 hours.`,
	Example: `  ankra tokens rotate 5b1c0d52-0000-0000-0000-000000000000
  ankra tokens rotate <token_id> --overlap-hours 72
  ankra tokens rotate <token_id> --service -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var overlapHours *int
		if cmd.Flags().Changed("overlap-hours") {
			hours, _ := cmd.Flags().GetInt("overlap-hours")
			if hours < 0 || hours > rotateMaxOverlapHours {
				return withExitCode(exitUsage, fmt.Errorf(
					"--overlap-hours must be between 0 and %d, got %d", rotateMaxOverlapHours, hours))
			}
			overlapHours = &hours
		}

		isService, _ := cmd.Flags().GetBool("service")
		var rotated *client.RotateTokenResponse
		var err error
		if isService {
			rotated, err = apiClient.RotateServiceToken(args[0], overlapHours)
		} else {
			rotated, err = apiClient.RotateAPIToken(args[0], overlapHours)
		}
		if err != nil {
			return fmt.Errorf("rotating token: %w", err)
		}
		if err := requireRotatedTokenValue(rotated); err != nil {
			return err
		}

		if rendered, err := renderStructured(cmd, rotated); rendered || err != nil {
			return err
		}
		printRotatedToken(rotated, stdoutIsTerminal())
		return nil
	},
}

// requireRotatedTokenValue refuses a rotate answer that carries no new token
// value. Printing "Renewed" there would hide that nothing usable came back
// while the old token's overlap is already counting down, so the error names
// when the old token stops and how to recover.
func requireRotatedTokenValue(rotated *client.RotateTokenResponse) error {
	if rotated != nil && rotated.Token != "" {
		return nil
	}
	validUntil := "the end of its overlap"
	if rotated != nil && rotated.OldTokenValidUntil != "" {
		validUntil = formatTokenDate(rotated.OldTokenValidUntil)
	}
	return fmt.Errorf("the platform answered the renewal without the new token value; the old token keeps "+
		"working until %s. Run `ankra tokens list` and renew again, or create a new token", validUntil)
}

// printRotatedToken shows a rotated token. On a terminal it prints one block
// to stdout. Otherwise stdout carries the bare token value and the
// explanation goes to stderr, so command substitution captures only the value.
func printRotatedToken(rotated *client.RotateTokenResponse, isTerminal bool) {
	name := rotated.Name
	if name == "" {
		name = "the token"
	}
	renewed := fmt.Sprintf("Renewed %s.", name)
	expires := ""
	if rotated.ExpiresAt != "" {
		expires = "Expires " + formatTokenDate(rotated.ExpiresAt) + "."
	}
	oldToken := "The old token keeps working until " + formatTokenDate(rotated.OldTokenValidUntil) +
		". Update where it is used, then it stops by itself."
	if rotated.OldTokenValidUntil == "" {
		oldToken = ""
	}

	if isTerminal {
		fmt.Printf("%s New token (shown once): %s\n", renewed, rotated.Token)
		if expires != "" {
			fmt.Println(expires)
		}
		if oldToken != "" {
			fmt.Println(oldToken)
		}
		return
	}
	fmt.Println(rotated.Token)
	fmt.Fprintln(os.Stderr, renewed+" The new token value is on stdout.")
	if expires != "" {
		fmt.Fprintln(os.Stderr, expires)
	}
	if oldToken != "" {
		fmt.Fprintln(os.Stderr, oldToken)
	}
}

// formatTokenDate renders a token timestamp as a UTC date and time, or as
// the platform sent it when it cannot be read.
func formatTokenDate(raw string) string {
	parsed, ok := parseTokenTimestamp(raw)
	if !ok {
		return raw
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}

// tokenExpiry is how `ankra tokens list` shows one token's expiry.
type tokenExpiry struct {
	text           string
	isExpired      bool
	isExpiringSoon bool
}

// describeTokenExpiry renders a token's expiry as its date plus how far off
// it is, and flags a live token that expires within
// client.TokenExpiryWarningWindow. It reads the platform's days_left when
// the platform sends it, and computes the same reading from expires_at when
// it does not. An expiry it cannot parse is shown as the platform sent it
// and flags nothing: an unreadable date says nothing about the token.
func describeTokenExpiry(token client.APIToken, now time.Time) tokenExpiry {
	expiresAt, isParsed := parseTokenTimestamp(token.ExpiresAt)
	if !isParsed && token.DaysLeft == nil {
		return tokenExpiry{text: token.ExpiresAt}
	}

	// The platform's days_left is the one source when it sends it; expires_at
	// is read only when it does not, so the text and the flags never come
	// from different subjects.
	var daysLeft int
	var isExpired, isInsideWindow bool
	if token.DaysLeft != nil {
		daysLeft = *token.DaysLeft
		isExpired = daysLeft < 0
		isInsideWindow = daysLeft < int(client.TokenExpiryWarningWindow/(24*time.Hour))
	} else {
		remaining := expiresAt.Sub(now)
		daysLeft = client.TokenDaysLeft(remaining)
		isExpired = remaining <= 0
		isInsideWindow = remaining <= client.TokenExpiryWarningWindow
	}

	text := describeDaysLeft(daysLeft, isExpired)
	if isParsed {
		text = expiresAt.UTC().Format("2006-01-02") + " (" + text + ")"
	} else {
		text = token.ExpiresAt + " (" + text + ")"
	}
	return tokenExpiry{
		text:           text,
		isExpired:      isExpired,
		isExpiringSoon: !token.Revoked && !isExpired && isInsideWindow,
	}
}

// describeDaysLeft is the parenthesised half of the Expires column.
func describeDaysLeft(daysLeft int, isExpired bool) string {
	switch {
	case isExpired:
		return "expired"
	case daysLeft == 0:
		return "within a day"
	case daysLeft == 1:
		return "in 1 day"
	default:
		return fmt.Sprintf("in %d days", daysLeft)
	}
}

// parseTokenTimestamp reads a token timestamp: RFC 3339, or the zone-less
// form the token listing answers, which the platform stores in UTC.
func parseTokenTimestamp(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if parsed, parseError := time.Parse(time.RFC3339Nano, trimmed); parseError == nil {
		return parsed, true
	}
	if parsed, parseError := time.Parse("2006-01-02T15:04:05.999999999", trimmed); parseError == nil {
		return parsed.UTC(), true
	}
	return time.Time{}, false
}

func init() {
	tokensCreateCmd.Flags().String("expires", "", "Token expiration date (ISO 8601 format; RFC 3339 with --service)")
	tokensCreateCmd.Flags().StringSlice("scopes", nil,
		"MCP access scopes: mcp:read or mcp:read,mcp:write; omitted creates a REST-only token")
	tokensCreateCmd.Flags().Bool("service", false, "Create an organisation service token for the service account <name> instead of a personal token")
	tokensCreateCmd.Flags().String("role", "", "Role the service account acts with (required with --service)")
	tokensCreateCmd.Flags().String("label", "", "Name of the service token itself (with --service; default: a timestamp)")

	tokensListCmd.Flags().Bool("service", false, "List the organisation's service tokens instead of your personal tokens")

	tokensRotateCmd.Flags().Bool("service", false, "Rotate an organisation service token instead of a personal token")
	tokensRotateCmd.Flags().Int("overlap-hours", rotateDefaultOverlapHours,
		"Hours the old token keeps working (0-168)")

	registerStructuredOutputFlags(tokensListCmd, tokensCreateCmd, tokensRotateCmd)
	registerSortFlags(tokensListCmd, tokenSortFields)

	tokensCmd.AddCommand(tokensListCmd)
	tokensCmd.AddCommand(tokensCreateCmd)
	tokensCmd.AddCommand(tokensRotateCmd)
	tokensCmd.AddCommand(tokensRevokeCmd)
	tokensCmd.AddCommand(tokensDeleteCmd)

	rootCmd.AddCommand(tokensCmd)
}
