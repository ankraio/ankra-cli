package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// leadingClusterPositional matches a Use line whose first argument is the
// cluster: submatch 1 is "<" for a required one and "[" for an optional one.
// The audit reads the usage line itself rather than trusting the
// registration it audits.
var leadingClusterPositional = regexp.MustCompile(`^\S+\s+([<\[])cluster[^>\]\s]*[>\]]`)

// clusterPositionalKind answers "<" or "[" for a command that takes the cluster
// as its first argument, and "" for any other.
func clusterPositionalKind(command *cobra.Command) string {
	match := leadingClusterPositional.FindStringSubmatch(command.Use)
	if match == nil {
		return ""
	}
	return match[1]
}

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

func setClusterFlag(t *testing.T, command *cobra.Command, value string) {
	t.Helper()
	if parseError := command.ParseFlags([]string{"--cluster", value}); parseError != nil {
		t.Fatalf("%s: %v", command.CommandPath(), parseError)
	}
	t.Cleanup(func() { _ = command.Flags().Set(activeClusterFlagName, "") })
}

// The audit: every command with a leading required cluster positional, found
// from the tree rather than a hand list, names the right form when given no
// cluster, takes the cluster from --cluster alone (ankra-v3qdto.19), and
// never takes it from the selection, which is there and must not be used.
func TestEveryClusterPositionalCommandNamesItsForm(t *testing.T) {
	withTempHome(t)
	writeSelectedClusterJSON(t)
	required := 0
	walkClusterPositionalCommands(clusterCmd, func(command *cobra.Command) {
		if clusterPositionalKind(command) != "<" {
			return
		}
		required++
		t.Run(command.CommandPath(), func(t *testing.T) {
			noFlag := command.Args(command, nil)
			if noFlag == nil || !strings.Contains(noFlag.Error(), command.UseLine()) ||
				!strings.Contains(noFlag.Error(), "--cluster") {
				t.Errorf("with no argument and a cluster selected: got %v, want the usage form naming --cluster", noFlag)
			}
			if exitCodeFor(noFlag) != exitUsage {
				t.Errorf("with no argument: exit %d, want %d", exitCodeFor(noFlag), exitUsage)
			}

			// --cluster stands in for the cluster; the command's other
			// arguments are still its own.
			_, others, _, _ := clusterArgumentUse(command.Use)
			setClusterFlag(t, command, otherClusterID)
			if withFlag := command.Args(command, others); withFlag != nil {
				t.Errorf("with --cluster in place of the cluster argument %v: got %v, want it accepted", others, withFlag)
			}
		})
	})
	if required < 100 {
		t.Errorf("walked %d commands, want the 100+ the tree holds: the Use pattern stopped matching", required)
	}
}

func TestClusterPositionalArgsOnNodeGroupAdd(t *testing.T) {
	command := clusterNodeGroupAddCmd

	if err := command.Args(command, []string{"prod"}); err != nil {
		t.Errorf("the positional alone must pass: %v", err)
	}

	setClusterFlag(t, command, "prod")
	if err := command.Args(command, []string{"prod"}); err != nil {
		t.Errorf("a flag agreeing with the positional must pass: %v", err)
	}
	if err := command.Args(command, nil); err != nil {
		t.Errorf("the flag alone must pass: %v", err)
	}

	err := command.Args(command, []string{"staging"})
	if err == nil || !strings.Contains(err.Error(), "name different clusters") {
		t.Fatalf("a flag that disagrees must be refused, got %v", err)
	}
	if exitCodeFor(err) != exitUsage {
		t.Errorf("exit %d, want %d", exitCodeFor(err), exitUsage)
	}
}

// positionalTestCommand is a command registered with withClusterArgument,
// carrying --cluster itself, and dropped from the registry after the test.
func positionalTestCommand(t *testing.T, access clusterArgumentAccess, use string, validate cobra.PositionalArgs) *cobra.Command {
	t.Helper()
	command := withClusterArgument(access, &cobra.Command{
		Use:  use,
		Args: validate,
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	command.Flags().String(activeClusterFlagName, "", "")
	t.Cleanup(func() { delete(clusterArgumentSpecs, command) })
	return command
}

func TestClusterPositionalArgsKeepsAnOptionalPositionalOptional(t *testing.T) {
	command := positionalTestCommand(t, clusterSelectionAllowed, "domain [cluster]", cobra.MaximumNArgs(1))

	if err := command.Args(command, nil); err != nil {
		t.Errorf("no argument and no flag must pass: %v", err)
	}
	setClusterFlag(t, command, "prod")
	if err := command.Args(command, nil); err != nil {
		t.Errorf("the flag alone must pass on an optional positional: %v", err)
	}
	if err := command.Args(command, []string{"staging"}); err == nil {
		t.Error("a flag that disagrees with the positional must be refused")
	}
}

// A command that must name its cluster refuses to be left without one even
// when its own argument check would let the cluster go.
func TestClusterPositionalArgsNeverLetsAnExplicitClusterGo(t *testing.T) {
	command := positionalTestCommand(t, clusterMustBeNamed, "deprovision [cluster]", cobra.MaximumNArgs(1))

	err := command.Args(command, nil)
	if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "--cluster") {
		t.Errorf("got %v (exit %d), want a usage error naming --cluster", err, exitCodeFor(err))
	}
}

func TestClusterPositionalArgsKeepsTheValidatorsOwnError(t *testing.T) {
	command := positionalTestCommand(t, clusterMustBeNamed, "scale <cluster_id|name> <count>", cobra.ExactArgs(2))

	err := command.Args(command, []string{"prod"})
	if err == nil || !strings.Contains(err.Error(), "accepts 2 arg(s)") || !strings.Contains(err.Error(), "scale <cluster_id|name> <count>") {
		t.Errorf("got %v, want cobra's count error and the form", err)
	}

	setClusterFlag(t, command, "prod")
	if err = command.Args(command, []string{"5"}); err != nil {
		t.Errorf("--cluster prod with only the count: got %v, want it accepted", err)
	}

	err = command.Args(command, []string{"5", "6", "7"})
	if err == nil || !strings.Contains(err.Error(), "accepts 2 arg(s)") || !strings.Contains(err.Error(), "counting --cluster") {
		t.Errorf("--cluster prod with three more arguments: got %v, want cobra's count error counting the flag", err)
	}

	// Two arguments fit the command with the cluster among them, so the
	// first is a cluster, and not the one --cluster names.
	err = command.Args(command, []string{"staging", "5"})
	if err == nil || !strings.Contains(err.Error(), "name different clusters") {
		t.Errorf("--cluster prod beside staging 5: got %v, want the different-clusters refusal", err)
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
		"reconcile [cluster_name]":          "[",
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
