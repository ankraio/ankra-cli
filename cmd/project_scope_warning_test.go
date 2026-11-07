package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// A name in the list that no command answers to would silently keep warning on
// the command it was meant to quiet.
func TestOrganisationLevelCommandsNameRealCommands(t *testing.T) {
	topLevel := map[string]bool{}
	for _, command := range rootCmd.Commands() {
		topLevel[command.Name()] = true
	}
	for name := range organisationLevelCommands {
		if name != "completion" && !topLevel[name] {
			t.Errorf("%q is not a top-level command", name)
		}
	}
}

func TestCommandIsOrganisationLevelFollowsTheTopLevelCommand(t *testing.T) {
	root := &cobra.Command{Use: "ankra"}
	organisation := &cobra.Command{Use: "org"}
	invite := &cobra.Command{Use: "invite"}
	security := &cobra.Command{Use: "security"}
	findings := &cobra.Command{Use: "findings"}
	root.AddCommand(organisation, security)
	organisation.AddCommand(invite)
	security.AddCommand(findings)
	for command, want := range map[*cobra.Command]bool{
		root: false, organisation: true, invite: true, security: false, findings: false,
	} {
		if got := commandIsOrganisationLevel(command); got != want {
			t.Errorf("%s = %t, want %t", command.CommandPath(), got, want)
		}
	}
}
