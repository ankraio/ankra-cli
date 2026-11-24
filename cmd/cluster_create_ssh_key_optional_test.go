package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Every self-managed create takes --ssh-key-credential-id as an option, not a
// requirement: the platform generates each cluster's own key pair and reaches
// the servers with it, and the owner adds a personal key later with
// `ankra cluster ssh-keys set`.
func TestSelfManagedCreatesLeaveThePersonalSSHKeyOptional(t *testing.T) {
	for name, command := range map[string]*cobra.Command{
		"ovh": ovhCreateCmd, "upcloud": upcloudCreateCmd, "digitalocean": digitaloceanCreateCmd,
		"scaleway": scalewayCreateCmd, "scaleway preflight": scalewayPreflightCmd,
		"proxmox": proxmoxCreateCmd, "morpheus": morpheusCreateCmd,
		"aws": awsCreateCmd, "aws preflight": awsPreflightCmd,
	} {
		flag := command.Flags().Lookup("ssh-key-credential-id")
		if flag == nil {
			t.Fatalf("%s must still take --ssh-key-credential-id", name)
		}
		if _, isRequired := flag.Annotations[cobra.BashCompOneRequiredFlag]; isRequired {
			t.Errorf("%s must not require --ssh-key-credential-id", name)
		}
		if !strings.Contains(flag.Usage, "ankra cluster ssh-keys set") {
			t.Errorf("%s --ssh-key-credential-id help must say how to add a key later, got %q", name, flag.Usage)
		}
	}
}

// An AWS create without the flag runs and sends no ssh_key_credential_id, so
// the platform reads it as "no personal key" rather than refusing "".
func TestAwsCreateWithoutAPersonalSSHKeySendsNone(t *testing.T) {
	mock := &awsClusterMock{}
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, awsCreateCmd) })
	var args []string
	for index := 0; index < len(awsCreateArgs); index += 2 {
		if awsCreateArgs[index] == "--ssh-key-credential-id" {
			continue
		}
		args = append(args, awsCreateArgs[index], awsCreateArgs[index+1])
	}
	var runError error
	captureStdout(t, func() {
		_, runError = executeCommand(append([]string{"cluster", "aws", "create"}, args...)...)
	})
	if runError != nil {
		t.Fatalf("a create without --ssh-key-credential-id must run, got %v", runError)
	}
	if mock.createRequest == nil || mock.createRequest.SSHKeyCredentialID != "" {
		t.Fatalf("the create must name no personal key, got %+v", mock.createRequest)
	}
}
