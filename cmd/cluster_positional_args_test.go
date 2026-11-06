package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// walkClusterPositionalCommands visits every command under `ankra cluster` that
// takes the cluster as its first argument.
func walkClusterPositionalCommands(command *cobra.Command, visit func(*cobra.Command)) {
	if clusterPositionalKind(command) != "" {
		visit(command)
	}
	for _, subcommand := range command.Commands() {
		walkClusterPositionalCommands(subcommand, visit)
	}
}

func withWrappedClusterPositionals(t *testing.T) {
	t.Helper()
	originals := map[*cobra.Command]cobra.PositionalArgs{}
	walkClusterPositionalCommands(clusterCmd, func(command *cobra.Command) {
		originals[command] = command.Args
	})
	wrapClusterPositionalArgs(clusterCmd)
	t.Cleanup(func() {
		for command, validate := range originals {
			command.Args = validate
		}
	})
}

func setClusterFlag(t *testing.T, command *cobra.Command, value string) {
	t.Helper()
	if parseError := command.ParseFlags([]string{"--cluster", value}); parseError != nil {
		t.Fatalf("%s: %v", command.CommandPath(), parseError)
	}
	t.Cleanup(func() { _ = command.Flags().Set(activeClusterFlagName, "") })
}

// The audit: every command with a leading cluster positional, found from the
// tree rather than a hand list, names the right form when given only the flag,
// and never takes a required cluster from it or from the selection.
func TestEveryClusterPositionalCommandNamesItsForm(t *testing.T) {
	withWrappedClusterPositionals(t)
	required := 0
	walkClusterPositionalCommands(clusterCmd, func(command *cobra.Command) {
		if clusterPositionalKind(command) != "<" {
			return
		}
		required++
		t.Run(command.CommandPath(), func(t *testing.T) {
			noFlag := command.Args(command, nil)
			if noFlag == nil || !strings.Contains(noFlag.Error(), command.UseLine()) {
				t.Errorf("with no argument: got %v, want the usage form", noFlag)
			}
			if exitCodeFor(noFlag) != exitUsage {
				t.Errorf("with no argument: exit %d, want %d", exitCodeFor(noFlag), exitUsage)
			}

			setClusterFlag(t, command, "prod")
			withFlag := command.Args(command, nil)
			if withFlag == nil || !strings.Contains(withFlag.Error(), "first argument, not --cluster") ||
				!strings.Contains(withFlag.Error(), command.UseLine()) {
				t.Errorf("with only --cluster: got %v, want the right form named", withFlag)
			}
		})
	})
	if required < 100 {
		t.Errorf("walked %d commands, want the 100+ the tree holds: the Use pattern stopped matching", required)
	}
}

func TestClusterPositionalArgsOnNodeGroupAdd(t *testing.T) {
	withWrappedClusterPositionals(t)
	command := clusterNodeGroupAddCmd

	if err := command.Args(command, []string{"prod"}); err != nil {
		t.Errorf("the positional alone must pass: %v", err)
	}

	setClusterFlag(t, command, "prod")
	if err := command.Args(command, []string{"prod"}); err != nil {
		t.Errorf("a flag agreeing with the positional must pass: %v", err)
	}

	err := command.Args(command, []string{"staging"})
	if err == nil || !strings.Contains(err.Error(), "name different clusters") {
		t.Fatalf("a flag that disagrees must be refused, got %v", err)
	}
	if exitCodeFor(err) != exitUsage {
		t.Errorf("exit %d, want %d", exitCodeFor(err), exitUsage)
	}
}

func TestClusterPositionalArgsKeepsAnOptionalPositionalOptional(t *testing.T) {
	validate := clusterPositionalArgs(cobra.MaximumNArgs(1), false)
	command := &cobra.Command{Use: "domain [cluster]"}
	command.Flags().String(activeClusterFlagName, "", "")

	if err := validate(command, nil); err != nil {
		t.Errorf("no argument and no flag must pass: %v", err)
	}
	setClusterFlag(t, command, "prod")
	if err := validate(command, nil); err != nil {
		t.Errorf("the flag alone must pass on an optional positional: %v", err)
	}
	if err := validate(command, []string{"staging"}); err == nil {
		t.Error("a flag that disagrees with the positional must be refused")
	}
}

func TestClusterPositionalArgsKeepsTheValidatorsOwnError(t *testing.T) {
	validate := clusterPositionalArgs(cobra.ExactArgs(2), true)
	command := &cobra.Command{Use: "scale <cluster_id|name> <count>"}
	command.Flags().String(activeClusterFlagName, "", "")

	err := validate(command, []string{"prod"})
	if err == nil || !strings.Contains(err.Error(), "accepts 2 arg(s)") || !strings.Contains(err.Error(), "scale <cluster_id|name> <count>") {
		t.Errorf("got %v, want cobra's count error and the form", err)
	}

	setClusterFlag(t, command, "prod")
	err = validate(command, []string{"5"})
	if err == nil || !strings.Contains(err.Error(), "first argument, not --cluster") {
		t.Errorf("--cluster prod with only the count: got %v, want the right form named", err)
	}
}

func TestClusterPositionalKindIgnoresOtherFirstArguments(t *testing.T) {
	cases := map[string]string{
		"add <cluster_id|name>":             "<",
		"list <cluster>":                    "<",
		"namespaces <cluster-name-or-id>":   "<",
		"lift <cluster_id> --reason <text>": "<",
		"taints <cluster_id|name> <group>":  "<",
		"domain [cluster]":                  "[",
		"deprovision [cluster_name]":        "[",
		"get <group_name>":                  "",
		"cluster":                           "",
		"logs <pod>":                        "",
	}
	for use, want := range cases {
		if got := clusterPositionalKind(&cobra.Command{Use: use}); got != want {
			t.Errorf("%q: got %q, want %q", use, got, want)
		}
	}
}
