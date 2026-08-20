package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newCloudProviderNetworkingCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "create"}
	cmd.Flags().Bool("external-cloud-provider", true, "")
	cmd.Flags().Bool("include-networking", true, "")
	return cmd
}

// The create flag trio (--external-cloud-provider, --include-networking,
// --include-dns) is the CLI's half of a contract the server defaults on: a
// provider create command that is missing one of them silently opts the user
// into it with no way to say no.
func TestProviderCreateCommandsExposeTheFlagTrio(t *testing.T) {
	tests := []struct {
		name                  string
		cmd                   *cobra.Command
		externalCloudProvider bool
	}{
		{name: "upcloud", cmd: upcloudCreateCmd, externalCloudProvider: true},
		{name: "digitalocean", cmd: digitaloceanCreateCmd, externalCloudProvider: true},
		{name: "hetzner", cmd: hetznerCreateCmd, externalCloudProvider: true},
		{name: "ovh", cmd: ovhCreateCmd, externalCloudProvider: true},
		{name: "proxmox", cmd: proxmoxCreateCmd},
		{name: "morpheus", cmd: morpheusCreateCmd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, flagName := range []string{"include-networking", "include-dns"} {
				flag := tt.cmd.Flags().Lookup(flagName)
				if flag == nil {
					t.Fatalf("%s create must expose --%s", tt.name, flagName)
				}
				if flag.DefValue != "true" {
					t.Errorf("--%s default = %q, want true (the server defaults it on)", flagName, flag.DefValue)
				}
			}
			// Proxmox and Morpheus have no CCM; the backend refuses the flag
			// for them, which TestProxmoxCreate_HasNoExternalCloudProviderFlag
			// pins for its own command.
			if got := tt.cmd.Flags().Lookup("external-cloud-provider") != nil; got != tt.externalCloudProvider {
				t.Errorf("--external-cloud-provider present = %v, want %v", got, tt.externalCloudProvider)
			}
		})
	}
}

func TestResolveCloudProviderNetworking(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		wantCloudProvider bool
		wantNetworking    bool
		wantErr           bool
		wantErrContains   string
	}{
		{name: "defaults both on", args: nil, wantCloudProvider: true, wantNetworking: true},
		{name: "networking off keeps cloud provider", args: []string{"--include-networking=false"}, wantCloudProvider: true, wantNetworking: false},
		{name: "cloud provider off disables networking implicitly", args: []string{"--external-cloud-provider=false"}, wantCloudProvider: false, wantNetworking: false},
		{name: "cloud provider off with networking off", args: []string{"--external-cloud-provider=false", "--include-networking=false"}, wantCloudProvider: false, wantNetworking: false},
		{name: "cloud provider off with explicit networking errors", args: []string{"--external-cloud-provider=false", "--include-networking=true"}, wantErr: true, wantErrContains: "requires --external-cloud-provider"},
		{name: "both explicitly on", args: []string{"--external-cloud-provider=true", "--include-networking=true"}, wantCloudProvider: true, wantNetworking: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newCloudProviderNetworkingCommand()
			if err := cmd.Flags().Parse(tt.args); err != nil {
				t.Fatalf("parsing flags: %v", err)
			}

			externalCloudProvider, includeNetworking, err := resolveCloudProviderNetworking(cmd)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got none")
				}
				if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if externalCloudProvider != tt.wantCloudProvider {
				t.Errorf("externalCloudProvider = %v, want %v", externalCloudProvider, tt.wantCloudProvider)
			}
			if includeNetworking != tt.wantNetworking {
				t.Errorf("includeNetworking = %v, want %v", includeNetworking, tt.wantNetworking)
			}
		})
	}
}
