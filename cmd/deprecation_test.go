package cmd

import (
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
