package cmd

// Profile authentication (two-factor settings, passkeys) is managed in the
// browser: enrollment and removal are session-authenticated browser flows
// (passkeys additionally need a WebAuthn ceremony no terminal can run), so
// the CLI's job is to land the user on the right page rather than mirror
// those flows over the API-token lane.

import (
	"fmt"
	"strings"

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

var profileAuthOpenCmd = &cobra.Command{
	Use:   "open",
	Short: "Open Profile Authentication in your browser",
	Long: `Open Profile Authentication in your browser, where two-factor
authentication, authenticator apps, recovery codes, passkeys, and security
keys are managed.`,
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

func init() {
	profileAuthCmd.AddCommand(profileAuthOpenCmd)
	deprecateAndForward(profileAuthCmd, "passkeys", "profile auth open", "v0.10.0", func(args []string) []string {
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			return args[1:]
		}
		return args
	})
	profileCmd.AddCommand(profileAuthCmd)
	rootCmd.AddCommand(profileCmd)
}
