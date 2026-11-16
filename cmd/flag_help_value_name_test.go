package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// A backquoted phrase in a flag's help is read as the NAME of the flag's
// value, so "see `ankra cluster kubeadm-versions`" made --help print
// "--kubernetes-version ankra cluster kubeadm-versions" where it should print
// "--kubernetes-version string". A value name is one word, and a boolean flag
// has none: anything else is a sentence that meant to quote a command, which
// takes plain quotes.
func TestNoFlagHelpNamesItsValueWithAPhrase(t *testing.T) {
	checked := 0
	var walk func(command *cobra.Command)
	walk = func(command *cobra.Command) {
		check := func(flag *pflag.Flag) {
			checked++
			if !strings.Contains(flag.Usage, "`") {
				return
			}
			name, _ := pflag.UnquoteUsage(flag)
			switch {
			case strings.ContainsAny(name, " \t") || name == "-":
				t.Errorf("%s --%s: help prints %q as the value name; quote the phrase with '...', not backquotes",
					command.CommandPath(), flag.Name, name)
			case name != "" && flag.Value.Type() == "bool":
				t.Errorf("%s --%s: a boolean flag takes no value, but its help names one (%q); quote the word with '...', not backquotes",
					command.CommandPath(), flag.Name, name)
			}
		}
		command.Flags().VisitAll(check)
		command.PersistentFlags().VisitAll(check)
		for _, subcommand := range command.Commands() {
			walk(subcommand)
		}
	}
	walk(rootCmd)
	if checked == 0 {
		t.Fatal("no flag was checked: the walk found no command")
	}
}
