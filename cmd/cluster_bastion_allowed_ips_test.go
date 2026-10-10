package cmd

// Tests for `ankra cluster <provider> bastion allowed-ips` and the
// --bastion-allowed-ips create flag (an omitted flag is an empty list, which
// the request's omitempty keeps off the wire, so older platforms see the
// same body as before). The update replaces the whole list, so
// the command must refuse to send an empty one unless --clear says so: an
// accidental empty list would reopen the bastion.

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// resetAllowedIPsFlags clears --clear and the output flags on every
// provider's allowed-ips command: the command tree is package-level, so a
// flag one test set would otherwise leak into the next.
func resetAllowedIPsFlags(t *testing.T) {
	t.Helper()
	var commands []*cobra.Command
	for _, provider := range []string{"hetzner", "ovh", "upcloud", "digitalocean"} {
		if command, _, findError := rootCmd.Find([]string{"cluster", provider, "bastion", "allowed-ips"}); findError == nil {
			commands = append(commands, command)
		}
	}
	resetTreeFlags(t, commands...)
	t.Cleanup(func() { resetTreeFlags(t, commands...) })
}

type bastionAllowedIPsMock struct {
	baseMock

	clusterIDs []string
	lists      [][]string
	result     *client.UpdateBastionAllowedIPsResult
}

func (m *bastionAllowedIPsMock) record(clusterID string, allowedIPs []string) (*client.UpdateBastionAllowedIPsResult, error) {
	m.clusterIDs = append(m.clusterIDs, clusterID)
	m.lists = append(m.lists, allowedIPs)
	return m.result, nil
}

func (m *bastionAllowedIPsMock) UpdateHetznerBastionAllowedIPs(_ context.Context, clusterID string, allowedIPs []string) (*client.UpdateBastionAllowedIPsResult, error) {
	return m.record(clusterID, allowedIPs)
}

func (m *bastionAllowedIPsMock) UpdateOvhBastionAllowedIPs(_ context.Context, clusterID string, allowedIPs []string) (*client.UpdateBastionAllowedIPsResult, error) {
	return m.record(clusterID, allowedIPs)
}

func TestClusterBastionAllowedIPsSendsTheWholeList(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetAllowedIPsFlags(t)
	operationID := "op-9"
	mock := &bastionAllowedIPsMock{result: &client.UpdateBastionAllowedIPsResult{
		Name: "bastion", BastionAllowedIPs: []string{"203.0.113.7/32", "198.51.100.0/24"}, OperationID: &operationID,
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "bastion", "allowed-ips", testClusterID,
			"203.0.113.7", "198.51.100.0/24,192.0.2.1")
	})

	want := []string{"203.0.113.7", "198.51.100.0/24", "192.0.2.1"}
	if len(mock.lists) != 1 || !reflect.DeepEqual(mock.lists[0], want) || mock.clusterIDs[0] != testClusterID {
		t.Fatalf("expected one call with %v, got %v for %v", want, mock.lists, mock.clusterIDs)
	}
	for _, expected := range []string{"SSH restricted to: 203.0.113.7/32, 198.51.100.0/24",
		"platform's own egress", "ankra cluster operations list op-9"} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected %q in the output, got: %s", expected, stdoutOutput)
		}
	}
}

func TestClusterBastionAllowedIPsClearSendsAnEmptyList(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetAllowedIPsFlags(t)
	mock := &bastionAllowedIPsMock{result: &client.UpdateBastionAllowedIPsResult{Name: "gateway", BastionAllowedIPs: []string{}}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "ovh", "bastion", "allowed-ips", testClusterID, "--clear")
	})
	if len(mock.lists) != 1 || mock.lists[0] == nil || len(mock.lists[0]) != 0 {
		t.Fatalf("--clear must send one empty (not nil) list, got %#v", mock.lists)
	}
	if !strings.Contains(stdoutOutput, "SSH allowlist cleared") {
		t.Errorf("expected the cleared confirmation, got: %s", stdoutOutput)
	}
}

func TestClusterBastionAllowedIPsRefusesAnAmbiguousRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"nothing listed and no --clear": {"cluster", "hetzner", "bastion", "allowed-ips", testClusterID},
		"a list together with --clear":  {"cluster", "hetzner", "bastion", "allowed-ips", testClusterID, "203.0.113.7", "--clear"},
	} {
		t.Run(name, func(t *testing.T) {
			writeSelectedClusterJSON(t)
			resetAllowedIPsFlags(t)
			mock := &bastionAllowedIPsMock{}
			setMockClient(t, mock)
			if _, executeError := executeCommand(args...); executeError == nil {
				t.Fatal("expected the command to refuse")
			}
			if len(mock.lists) != 0 {
				t.Fatalf("a refused request must not reach the platform, got %v", mock.lists)
			}
		})
	}
}

func TestBastionAllowedIPsSubcommandExistsOnlyWhereThePlatformEnforcesIt(t *testing.T) {
	for _, provider := range []string{"hetzner", "ovh", "upcloud", "digitalocean", "ankracloud", "aws", "scaleway", "proxmox", "morpheus"} {
		command, _, findError := rootCmd.Find([]string{"cluster", provider, "bastion", "allowed-ips"})
		mounted := findError == nil && command != nil && command.Name() == "allowed-ips"
		want := provider == "hetzner" || provider == "ovh" || provider == "upcloud" || provider == "digitalocean"
		if mounted != want {
			t.Errorf("%s bastion allowed-ips mounted = %v, want %v", provider, mounted, want)
		}
	}
}

func TestHetznerCreateSendsTheBastionAllowlist(t *testing.T) {
	for name, testCase := range map[string]struct {
		args []string
		want []string
	}{
		"omitted": {nil, nil},
		"listed":  {[]string{"--bastion-allowed-ips", "203.0.113.7,198.51.100.0/24"}, []string{"203.0.113.7", "198.51.100.0/24"}},
	} {
		t.Run(name, func(t *testing.T) {
			resetTreeFlags(t, hetznerCreateCmd)
			t.Cleanup(func() { resetTreeFlags(t, hetznerCreateCmd) })
			mock := &hetznerCreateMock{}
			args := append([]string{"cluster", "hetzner", "create", "--name", "allowlist-test",
				"--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--location", "fsn1"}, testCase.args...)
			if output, runError := runWithInput(t, mock, "", args...); runError != nil {
				t.Fatalf("execute failed: %v\noutput: %s", runError, output)
			}
			if len(testCase.want) == 0 && len(mock.gotRequest.BastionAllowedIPs) == 0 {
				return
			}
			if !reflect.DeepEqual(mock.gotRequest.BastionAllowedIPs, testCase.want) {
				t.Fatalf("bastion_allowed_ips = %#v, want %#v", mock.gotRequest.BastionAllowedIPs, testCase.want)
			}
		})
	}
}
