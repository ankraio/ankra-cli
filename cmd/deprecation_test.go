package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func findChildCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, child := range parent.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

func TestProviderCommandsDeprecatedForGenericVerbs(t *testing.T) {
	for _, provider := range []string{"hetzner", "ovh", "upcloud"} {
		providerCommand := findChildCommand(clusterCmd, provider)
		if providerCommand == nil {
			t.Fatalf("provider command %q not registered under cluster", provider)
		}

		expectations := map[string]string{
			"scale":       "ankra cluster scale",
			"deprovision": "ankra cluster deprovision",
		}
		for name, replacement := range expectations {
			command := findChildCommand(providerCommand, name)
			if command == nil {
				t.Fatalf("%s %s not registered", provider, name)
			}
			if !strings.Contains(command.Deprecated, replacement) {
				t.Errorf("%s %s should point at `%s`, got Deprecated=%q", provider, name, replacement, command.Deprecated)
			}
		}

		nodeGroupCommand := findChildCommand(providerCommand, "node-group")
		if nodeGroupCommand == nil {
			t.Fatalf("%s node-group not registered", provider)
		}
		for _, verb := range []string{"list", "add", "scale", "upgrade", "delete"} {
			leaf := findChildCommand(nodeGroupCommand, verb)
			if leaf == nil {
				t.Fatalf("%s node-group %s not registered", provider, verb)
			}
			if !strings.Contains(leaf.Deprecated, "ankra cluster node-group") {
				t.Errorf("%s node-group %s should be deprecated, got Deprecated=%q", provider, verb, leaf.Deprecated)
			}
		}
	}
}

func TestOvhNodeGroupLabelsAndTaintsNotDeprecated(t *testing.T) {
	ovhCommand := findChildCommand(clusterCmd, "ovh")
	if ovhCommand == nil {
		t.Fatal("ovh command not registered under cluster")
	}
	nodeGroupCommand := findChildCommand(ovhCommand, "node-group")
	if nodeGroupCommand == nil {
		t.Fatal("ovh node-group not registered")
	}
	for _, verb := range []string{"labels", "taints"} {
		leaf := findChildCommand(nodeGroupCommand, verb)
		if leaf == nil {
			t.Fatalf("ovh node-group %s not registered", verb)
		}
		if leaf.Deprecated != "" {
			t.Errorf("ovh node-group %s has no generic equivalent and must not be deprecated, got Deprecated=%q", verb, leaf.Deprecated)
		}
	}
}

func TestDeprecateAndForwardDispatchesToTarget(t *testing.T) {
	var gotArgs []string
	var gotFlag string
	root := &cobra.Command{Use: "ankra"}
	group := &cobra.Command{Use: "cluster"}
	target := &cobra.Command{
		Use: "delete",
		RunE: func(cmd *cobra.Command, args []string) error {
			gotArgs = args
			gotFlag, _ = cmd.Flags().GetString("note")
			return nil
		},
	}
	target.Flags().String("note", "", "")
	group.AddCommand(target)
	root.AddCommand(group)

	forwarder := deprecateAndForward(root, "delete-cluster", "cluster delete", "v0.7.0", nil)
	if !forwarder.Hidden {
		t.Error("forwarder should be hidden from help")
	}

	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetOut(io.Discard)
	root.SetArgs([]string{"delete-cluster", "prod", "--note", "bye"})
	if err := root.Execute(); err != nil {
		t.Fatalf("forwarded execution failed: %v", err)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "prod" {
		t.Errorf("target args = %v, want [prod]", gotArgs)
	}
	if gotFlag != "bye" {
		t.Errorf("target flag = %q, want %q (flags must pass through the forwarder)", gotFlag, "bye")
	}
	warnings := stderr.String()
	if !strings.Contains(warnings, "ANKRA_DEPRECATED=ankra delete-cluster=>ankra cluster delete removal=v0.7.0") {
		t.Errorf("missing machine-readable marker, stderr: %q", warnings)
	}
	if !strings.Contains(warnings, "deprecated") {
		t.Errorf("missing human-facing notice, stderr: %q", warnings)
	}
}

func TestDeprecateAndForwardRewritesArgs(t *testing.T) {
	var gotArgs []string
	root := &cobra.Command{Use: "ankra"}
	target := &cobra.Command{
		Use:  "cancel",
		RunE: func(cmd *cobra.Command, args []string) error { gotArgs = args; return nil },
	}
	target.Flags().String("step", "", "")
	root.AddCommand(target)

	deprecateAndForward(root, "cancel-step", "cancel", "v0.7.0", func(args []string) []string {
		if len(args) == 2 {
			return []string{args[0], "--step", args[1]}
		}
		return args
	})

	root.SetErr(io.Discard)
	root.SetOut(io.Discard)
	root.SetArgs([]string{"cancel-step", "exec-1", "step-2"})
	if err := root.Execute(); err != nil {
		t.Fatalf("forwarded execution failed: %v", err)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "exec-1" {
		t.Errorf("rewritten args = %v, want positional [exec-1] with step-2 moved to --step", gotArgs)
	}
}

func TestDeprecateAndForwardPropagatesTargetError(t *testing.T) {
	// Configured like the real rootCmd: SilenceUsage only, errors NOT silenced,
	// so this also guards against the forwarded error printing twice (once from
	// the nested Execute, once from the outer one).
	root := &cobra.Command{Use: "ankra", SilenceUsage: true}
	target := &cobra.Command{
		Use:  "fail",
		RunE: func(*cobra.Command, []string) error { return withExitCode(exitNotFound, errors.New("boom")) },
	}
	root.AddCommand(target)
	deprecateAndForward(root, "old-fail", "fail", "v0.7.0", nil)

	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetOut(io.Discard)
	root.SetArgs([]string{"old-fail"})
	err := root.Execute()
	if err == nil {
		t.Fatal("target error should propagate through the forwarder")
	}
	if got := exitCodeFor(err); got != exitNotFound {
		t.Errorf("exit code should survive forwarding, got %d", got)
	}
	if got := strings.Count(stderr.String(), "Error: boom"); got != 1 {
		t.Errorf("forwarded failure should print exactly once, got %d prints in: %q", got, stderr.String())
	}
	if root.SilenceErrors {
		t.Error("root.SilenceErrors must be restored after forwarding")
	}
}

func TestDeprecateAndForwardIsAuthFree(t *testing.T) {
	// With DisableFlagParsing the persistent pre-run would resolve credentials
	// before --token is parsed, so the forwarder itself must skip auth; the
	// target enforces it during re-dispatch with parsed flags.
	root := &cobra.Command{Use: "ankra"}
	forwarder := deprecateAndForward(root, "old", "new", "v0.7.0", nil)
	if commandRequiresAuth(forwarder) {
		t.Error("forwarder must be auth-free; auth is enforced on the target after flags are parsed")
	}
}
