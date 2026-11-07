package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// A mistyped -o used to be read only when the command rendered its answer,
// so `ankra cluster hetzner create -o jsn` created the cluster and then
// exited 2 (ankra-v3qdto.6). The shared -o flag is now validated before the
// command runs. These cases carry no required flags on purpose: the -o
// refusal has to come first, ahead of every other check and every API call.
func TestInvalidOutputFormatIsRefusedBeforeTheCommandRuns(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		command *cobra.Command
		args    []string
	}{
		{name: "hetzner create", command: hetznerCreateCmd, args: []string{"cluster", "hetzner", "create"}},
		{name: "ovh create", command: ovhCreateCmd, args: []string{"cluster", "ovh", "create"}},
		{name: "upcloud create", command: upcloudCreateCmd, args: []string{"cluster", "upcloud", "create"}},
		{name: "digitalocean create", command: digitaloceanCreateCmd, args: []string{"cluster", "digitalocean", "create"}},
		{name: "scaleway create", command: scalewayCreateCmd, args: []string{"cluster", "scaleway", "create"}},
		{name: "aws create", command: awsCreateCmd, args: []string{"cluster", "aws", "create"}},
		{name: "ankracloud create", command: ankraCloudCreateCmd, args: []string{"cluster", "ankracloud", "create"}},
		{name: "proxmox create", command: proxmoxCreateCmd, args: []string{"cluster", "proxmox", "create"}},
		{name: "morpheus create", command: morpheusCreateCmd, args: []string{"cluster", "morpheus", "create"}},
		{name: "managed create", command: managedCreateCmd, args: []string{"cluster", "managed", "create"}},
		{name: "aws start", command: awsStartCmd, args: []string{"cluster", "aws", "start", testClusterID}},
		{name: "aws stop", command: awsStopCmd, args: []string{"cluster", "aws", "stop", testClusterID}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetTreeFlags(t, testCase.command)
			_, runError := runConfirmCommand(t, baseMock{}, "", []*cobra.Command{testCase.command},
				append(testCase.args, "-o", "jsn")...)
			if exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), `invalid -o format "jsn"`) {
				t.Fatalf("error = %v, want the -o usage error before anything else", runError)
			}
		})
	}
}

// With every required flag present the create itself would run, so this is
// the ordering the bug was about: no create request for a bad -o.
func TestInvalidOutputFormatSendsNoCreate(t *testing.T) {
	resetTreeFlags(t, hetznerCreateCmd)
	mock := &hetznerCreateMock{}
	_, runError := runConfirmCommand(t, mock, "", []*cobra.Command{hetznerCreateCmd},
		"cluster", "hetzner", "create", "--name", "demo", "--credential-id", "cred-1",
		"--location", "fsn1", "-o", "jsn")
	if exitCodeFor(runError) != exitUsage {
		t.Fatalf("error = %v, want a usage error", runError)
	}
	if mock.called {
		t.Fatal("the cluster was created before the -o value was refused")
	}
}

// A valid -o still renders as before, and commands with their own -o
// vocabulary (table, wide, raw...) are not held to json|yaml.
func TestValidOutputFormatStillRuns(t *testing.T) {
	resetTreeFlags(t, hetznerCreateCmd)
	mock := &hetznerCreateMock{}
	output, runError := runConfirmCommand(t, mock, "", []*cobra.Command{hetznerCreateCmd},
		"cluster", "hetzner", "create", "--name", "demo", "--credential-id", "cred-1",
		"--location", "fsn1", "-o", "json")
	if runError != nil {
		t.Fatalf("create -o json: %v\n%s", runError, output)
	}
	if !mock.called {
		t.Fatal("expected the create to run")
	}
}

func TestOwnOutputVocabularyIsNotAnnotated(t *testing.T) {
	command := &cobra.Command{Use: "own", Run: func(*cobra.Command, []string) {}}
	command.Flags().StringP("output", "o", "table", "table or wide")
	if validationError := validateStructuredOutputFlag(command); validationError != nil {
		t.Fatalf("a command-owned -o was validated as json|yaml: %v", validationError)
	}
	shared := newStructuredOutputTestCommand()
	_ = shared.Flags().Set("output", "wide")
	if validationError := validateStructuredOutputFlag(shared); exitCodeFor(validationError) != exitUsage {
		t.Fatalf("shared -o wide: error = %v, want a usage error", validationError)
	}
}
