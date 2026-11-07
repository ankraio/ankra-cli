package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveGatewayAPI(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		includeNetworking bool
		want              bool
		wantUsageError    bool
	}{
		{name: "off by default", includeNetworking: true, want: false},
		{name: "on with the networking stack", args: []string{"--gateway-api"}, includeNetworking: true, want: true},
		{name: "explicitly off without the networking stack", args: []string{"--gateway-api=false"}, includeNetworking: false, want: false},
		{name: "on without the networking stack is a usage error", args: []string{"--gateway-api"}, includeNetworking: false, wantUsageError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "create"}
			registerGatewayAPIFlag(cmd)
			if err := cmd.Flags().Parse(tt.args); err != nil {
				t.Fatalf("parsing flags: %v", err)
			}
			got, err := resolveGatewayAPI(cmd, tt.includeNetworking)
			if tt.wantUsageError {
				if err == nil {
					t.Fatal("expected a usage error, got none")
				}
				if code := exitCodeFor(err); code != exitUsage {
					t.Errorf("exit code = %d, want %d (usage)", code, exitUsage)
				}
				if !strings.Contains(err.Error(), "--gateway-api requires the networking stack") {
					t.Errorf("error = %q, want it to name the networking requirement", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("gatewayAPI = %v, want %v", got, tt.want)
			}
		})
	}
}

// Every create command that can install the networking stack must offer the
// Gateway API option on it, off by default, so the option cannot quietly go
// missing from one provider.
func TestEveryCreateCommandWithNetworkingOffersGatewayAPI(t *testing.T) {
	var withNetworking []string
	var walk func(command *cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Flags().Lookup("include-networking") != nil {
			withNetworking = append(withNetworking, command.CommandPath())
			gatewayAPI := command.Flags().Lookup("gateway-api")
			if gatewayAPI == nil {
				t.Errorf("%s offers --include-networking but not --gateway-api", command.CommandPath())
			} else if gatewayAPI.DefValue != "false" {
				t.Errorf("%s --gateway-api defaults to %s, want false", command.CommandPath(), gatewayAPI.DefValue)
			}
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)
	if len(withNetworking) < 9 {
		t.Fatalf("found %d create commands with --include-networking (%v), want the nine self-managed providers", len(withNetworking), withNetworking)
	}
}
