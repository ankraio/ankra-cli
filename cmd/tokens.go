package cmd

import (
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
	Long:    "Commands to list, create, rotate, revoke, and delete API tokens.",
}

var tokenSortFields = []sortField[client.APIToken]{
	{"id", func(a, b client.APIToken) int { return compareFold(a.ID, b.ID) }},
	{"name", func(a, b client.APIToken) int { return compareFold(a.Name, b.Name) }},
	{"status", func(a, b client.APIToken) int { return compareBools(a.Revoked, b.Revoked) }},
	{"created", func(a, b client.APIToken) int { return compareTimeStrings(a.CreatedAt, b.CreatedAt) }},
	{"expires", func(a, b client.APIToken) int { return compareTimeStrings(a.ExpiresAt, b.ExpiresAt) }},
	{"last-used", func(a, b client.APIToken) int { return compareTimeStringPtrs(a.LastUsedAt, b.LastUsedAt) }},
}

var tokensListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all API tokens",
	Long: `List your API tokens with when each one expires.

A live token that expires within 7 days reads "Expiring" in the Status
column and an expired one "Expired"; the Expires column shows the date and
how many days are left. Renew an expiring token before it stops working with
"ankra tokens rotate <id>".`,
	RunE: func(cmd *cobra.Command, args []string) error {
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

var tokensCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new API token",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		expiresAt, _ := cmd.Flags().GetString("expires")
		scopes, _ := cmd.Flags().GetStringSlice("scopes")

		var expiresAtPtr *string
		if expiresAt != "" {
			expiresAtPtr = &expiresAt
		}

		result, err := apiClient.CreateAPIToken(name, expiresAtPtr, scopes)
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

		if rendered, err := renderStructured(cmd, rotated); rendered || err != nil {
			return err
		}
		printRotatedToken(rotated, stdoutIsTerminal())
		return nil
	},
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
	tokensCreateCmd.Flags().String("expires", "", "Token expiration date (ISO 8601 format)")
	tokensCreateCmd.Flags().StringSlice("scopes", nil,
		"MCP access scopes: mcp:read or mcp:read,mcp:write; omitted creates a REST-only token")

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
