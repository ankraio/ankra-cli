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
