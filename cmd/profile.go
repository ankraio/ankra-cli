package cmd

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage your Ankra profile",
}

var profileAuthCmd = &cobra.Command{
	Use:     "auth",
	Aliases: []string{"authentication", "mfa", "2fa"},
	Short:   "Manage profile authentication and two-factor settings",
}

var profileAuthStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show two-factor authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := apiClient.GetMFAStatus()
		if err != nil {
			return fmt.Errorf("get authentication status: %w", err)
		}
		if renderStructuredOrExit(cmd, status) {
			return nil
		}
		renderMFAStatus(cmd.OutOrStdout(), status)
		return nil
	},
}

var profileAuthTotpCmd = &cobra.Command{
	Use:     "totp",
	Aliases: []string{"authenticator"},
	Short:   "Manage authenticator app setup",
}

var profileAuthTotpStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start authenticator app setup",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.StartTOTPEnrollment()
		if err != nil {
			return fmt.Errorf("start authenticator setup: %w", err)
		}
		if renderStructuredOrExit(cmd, result) {
			return nil
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Authenticator setup started.")
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Scan this otpauth URI with your authenticator app:")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", result.OtpAuthURI)
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Or enter this secret manually:")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", result.Secret)
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Then confirm with:")
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  ankra profile auth totp confirm <6-digit-code>")
		return nil
	},
}

var profileAuthTotpConfirmCmd = &cobra.Command{
	Use:   "confirm <code>",
	Short: "Confirm authenticator app setup",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.ConfirmTOTPEnrollment(args[0])
		if err != nil {
			return fmt.Errorf("confirm authenticator setup: %w", err)
		}
		if renderStructuredOrExit(cmd, result) {
			return nil
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Authenticator app enabled.")
		renderRecoveryCodes(cmd, result.RecoveryCodes)
		return nil
	},
}

var profileAuthTotpRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove the authenticator app second factor",
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), "Remove your authenticator app from two-factor authentication?", yes); err != nil {
			return err
		}
		result, err := apiClient.RemoveMFAMethod("totp")
		if err != nil {
			return fmt.Errorf("remove authenticator: %w", err)
		}
		if renderStructuredOrExit(cmd, result) {
			return nil
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Authenticator app removed.")
		return nil
	},
}

var profileAuthRecoveryCodesCmd = &cobra.Command{
	Use:     "recovery-codes",
	Aliases: []string{"recovery", "codes"},
	Short:   "Manage one-time recovery codes",
}

var profileAuthRecoveryCodesRegenerateCmd = &cobra.Command{
	Use:     "regenerate",
	Aliases: []string{"generate"},
	Short:   "Generate a fresh set of recovery codes",
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), "Generate a new recovery-code set? This invalidates all existing recovery codes.", yes); err != nil {
			return err
		}
		result, err := apiClient.RegenerateRecoveryCodes()
		if err != nil {
			return fmt.Errorf("regenerate recovery codes: %w", err)
		}
		if renderStructuredOrExit(cmd, result) {
			return nil
		}
		renderRecoveryCodes(cmd, result.RecoveryCodes)
		return nil
	},
}

var profileAuthPasskeysCmd = &cobra.Command{
	Use:     "passkeys",
	Aliases: []string{"passkey"},
	Short:   "Manage passkeys and security keys",
}

var profileAuthPasskeysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered passkeys and security keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := apiClient.GetMFAStatus()
		if err != nil {
			return fmt.Errorf("list passkeys: %w", err)
		}
		if renderStructuredOrExit(cmd, status.Passkeys) {
			return nil
		}
		renderPasskeys(cmd.OutOrStdout(), status.Passkeys)
		return nil
	},
}

var profileAuthPasskeysRemoveCmd = &cobra.Command{
	Use:   "remove <credential_id>",
	Short: "Remove a passkey or security key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), "Remove this passkey or security key from two-factor authentication?", yes); err != nil {
			return err
		}
		result, err := apiClient.RemovePasskey(args[0])
		if err != nil {
			return fmt.Errorf("remove passkey: %w", err)
		}
		if renderStructuredOrExit(cmd, result) {
			return nil
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Passkey removed.")
		return nil
	},
}

var profileAuthPasskeysOpenCmd = &cobra.Command{
	Use:   "open",
	Short: "Open Profile Authentication to add a passkey or security key",
	RunE: func(cmd *cobra.Command, args []string) error {
		profileAuthenticationURL := fmt.Sprintf("%s/organisation/profile/authentication", strings.TrimRight(baseURL, "/"))
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Opening Profile Authentication in your browser:")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", profileAuthenticationURL)
		if err := openBrowser(profileAuthenticationURL); err != nil {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Could not open browser automatically. Please open the URL above manually.")
		}
		return nil
	},
}

func renderMFAStatus(out io.Writer, status *client.MFAStatus) {
	enrolled := text.FgRed.Sprint("No")
	if status.Enrolled {
		enrolled = text.FgGreen.Sprint("Yes")
	}
	required := "No"
	if status.Required {
		required = text.FgYellow.Sprint("Yes")
	}
	_, _ = fmt.Fprintln(out, "Two-factor authentication:")
	_, _ = fmt.Fprintf(out, "  Required by organisation: %s\n", required)
	_, _ = fmt.Fprintf(out, "  Enrolled:                 %s\n", enrolled)
	_, _ = fmt.Fprintf(out, "  Recovery codes remaining: %d\n", status.RecoveryCodesRemaining)
	_, _ = fmt.Fprintf(out, "  Authenticator apps:       %d\n", len(status.Methods))
	_, _ = fmt.Fprintf(out, "  Passkeys/security keys:   %d\n", len(status.Passkeys))
	if len(status.Passkeys) > 0 {
		_, _ = fmt.Fprintln(out)
		renderPasskeys(out, status.Passkeys)
	}
}

func renderPasskeys(out io.Writer, passkeys []client.PasskeyInfo) {
	if len(passkeys) == 0 {
		_, _ = fmt.Fprintln(out, "No passkeys or security keys registered.")
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "Name", "Type", "Created"})
	for _, passkey := range passkeys {
		name := ""
		if passkey.Name != nil {
			name = *passkey.Name
		}
		createdAt := ""
		if passkey.CreatedAt != nil {
			createdAt = formatTimeAgo(*passkey.CreatedAt)
		}
		writer.AppendRow(table.Row{passkey.ID, name, passkey.Type, createdAt})
	}
	writer.Render()
}

func renderRecoveryCodes(cmd *cobra.Command, recoveryCodes []string) {
	if len(recoveryCodes) == 0 {
		return
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Recovery codes (save these now; they will not be shown again):")
	for _, recoveryCode := range recoveryCodes {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", recoveryCode)
	}
}

func init() {
	registerStructuredOutputFlags(
		profileAuthStatusCmd,
		profileAuthTotpStartCmd,
		profileAuthTotpConfirmCmd,
		profileAuthTotpRemoveCmd,
		profileAuthRecoveryCodesRegenerateCmd,
		profileAuthPasskeysListCmd,
		profileAuthPasskeysRemoveCmd,
	)
	profileAuthTotpRemoveCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	profileAuthRecoveryCodesRegenerateCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	profileAuthPasskeysRemoveCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	profileAuthTotpCmd.AddCommand(profileAuthTotpStartCmd)
	profileAuthTotpCmd.AddCommand(profileAuthTotpConfirmCmd)
	profileAuthTotpCmd.AddCommand(profileAuthTotpRemoveCmd)
	profileAuthRecoveryCodesCmd.AddCommand(profileAuthRecoveryCodesRegenerateCmd)
	profileAuthPasskeysCmd.AddCommand(profileAuthPasskeysListCmd)
	profileAuthPasskeysCmd.AddCommand(profileAuthPasskeysRemoveCmd)
	profileAuthPasskeysCmd.AddCommand(profileAuthPasskeysOpenCmd)
	profileAuthCmd.AddCommand(profileAuthStatusCmd)
	profileAuthCmd.AddCommand(profileAuthTotpCmd)
	profileAuthCmd.AddCommand(profileAuthRecoveryCodesCmd)
	profileAuthCmd.AddCommand(profileAuthPasskeysCmd)
	profileCmd.AddCommand(profileAuthCmd)
	rootCmd.AddCommand(profileCmd)
}
