package cmd

import (
	"fmt"
	"os"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var tokensCmd = &cobra.Command{
	Use:     "tokens",
	Aliases: []string{"token"},
	Short:   "Manage API tokens",
	Long:    "Commands to list, create, revoke, and delete API tokens.",
}

var tokensListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all API tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		tokens, err := apiClient.ListAPITokens()
		if err != nil {
			return fmt.Errorf("listing tokens: %w", err)
		}

		if tokens == nil {
			tokens = []client.APIToken{}
		}
		if rendered, err := renderStructured(cmd, tokens); rendered || err != nil {
			return err
		}

		if len(tokens) == 0 {
			fmt.Println("No API tokens found.")
			return nil
		}

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
			status := text.FgGreen.Sprint("Active")
			if tok.Revoked {
				status = text.FgRed.Sprint("Revoked")
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
				formatTimeAgo(tok.ExpiresAt),
				lastUsed,
			})
		}
		t.Render()
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

func init() {
	tokensCreateCmd.Flags().String("expires", "", "Token expiration date (ISO 8601 format)")
	tokensCreateCmd.Flags().StringSlice("scopes", nil,
		"Permission allowlist for the token (e.g. clusters.read,stacks.deploy); omitted grants the user's full authority")

	registerStructuredOutputFlags(tokensListCmd, tokensCreateCmd)

	tokensCmd.AddCommand(tokensListCmd)
	tokensCmd.AddCommand(tokensCreateCmd)
	tokensCmd.AddCommand(tokensRevokeCmd)
	tokensCmd.AddCommand(tokensDeleteCmd)

	rootCmd.AddCommand(tokensCmd)
}
