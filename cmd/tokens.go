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
	Run: func(cmd *cobra.Command, args []string) {
		tokens, err := client.ListAPITokens(apiToken, baseURL)
		if err != nil {
			fmt.Printf("Error listing tokens: %v\n", err)
			return
		}

		if len(tokens) == 0 {
			fmt.Println("No API tokens found.")
			return
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
	},
}

var tokensCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new API token",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		expiresAt, _ := cmd.Flags().GetString("expires")

		var expiresAtPtr *string
		if expiresAt != "" {
			expiresAtPtr = &expiresAt
		}

		result, err := client.CreateAPIToken(apiToken, baseURL, name, expiresAtPtr)
		if err != nil {
			fmt.Printf("Error creating token: %v\n", err)
			return
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
	},
}

var tokensRevokeCmd = &cobra.Command{
	Use:   "revoke <token_id>",
	Short: "Revoke an API token (can be deleted after)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tokenID := args[0]

		result, err := client.RevokeAPIToken(apiToken, baseURL, tokenID)
		if err != nil {
			fmt.Printf("Error revoking token: %v\n", err)
			return
		}

		if result.Success {
			fmt.Println("Token revoked successfully!")
			fmt.Println("You can now delete it with: ankra tokens delete", tokenID)
		}
	},
}

var tokensDeleteCmd = &cobra.Command{
	Use:   "delete <token_id>",
	Short: "Delete a revoked API token",
	Long:  "Delete an API token. The token must be revoked first.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tokenID := args[0]

		result, err := client.DeleteAPIToken(apiToken, baseURL, tokenID)
		if err != nil {
			fmt.Printf("Error deleting token: %v\n", err)
			fmt.Println("Note: Tokens must be revoked before they can be deleted.")
			return
		}

		if result.Success {
			fmt.Println("Token deleted successfully!")
		}
	},
}

func init() {
	tokensCreateCmd.Flags().String("expires", "", "Token expiration date (ISO 8601 format)")

	tokensCmd.AddCommand(tokensListCmd)
	tokensCmd.AddCommand(tokensCreateCmd)
	tokensCmd.AddCommand(tokensRevokeCmd)
	tokensCmd.AddCommand(tokensDeleteCmd)

	rootCmd.AddCommand(tokensCmd)
}
