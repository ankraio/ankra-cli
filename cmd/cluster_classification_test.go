package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// Every cluster create family accepts an environment and a reporting level,
// so every create (and preflight, where the family has one) command must
// declare both flags (ankra-lhy8e). A family added without them fails here.
func TestClusterClassificationFlagsOnEveryCreateCommand(t *testing.T) {
	paths := [][]string{
		{"cluster", "hetzner", "create"},
		{"cluster", "ovh", "create"},
		{"cluster", "upcloud", "create"},
		{"cluster", "digitalocean", "create"},
		{"cluster", "scaleway", "create"},
		{"cluster", "scaleway", "preflight"},
		{"cluster", "proxmox", "create"},
		{"cluster", "morpheus", "create"},
		{"cluster", "ankracloud", "create"},
		{"cluster", "ankracloud", "preflight"},
		{"cluster", "aws", "create"},
		{"cluster", "aws", "preflight"},
		{"cluster", "managed", "create"},
		{"cluster", "managed", "import"},
	}
	for _, path := range paths {
		command, _, findError := rootCmd.Find(path)
		if findError != nil || command.Name() != path[len(path)-1] {
			t.Errorf("%s: command not found (%v)", strings.Join(path, " "), findError)
			continue
		}
		for _, flagName := range []string{"environment", "criticality"} {
			if command.Flags().Lookup(flagName) == nil {
				t.Errorf("%s has no --%s", strings.Join(path, " "), flagName)
			}
		}
	}
}

func TestClusterClassificationFromFlags(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		wantEnvironment *string
		wantCriticality *string
		wantUsage       bool
	}{
		{name: "blank flags send nothing"},
		{name: "whitespace-only environment sends nothing", args: []string{"--environment", "   "}},
		{name: "environment alone leaves the level to the platform",
			args: []string{"--environment", "  production "}, wantEnvironment: stringPointer("production")},
		{name: "a name of your own is sent as typed",
			args: []string{"--environment", "QA-eu"}, wantEnvironment: stringPointer("QA-eu")},
		{name: "criticality is normalised",
			args:            []string{"--environment", "staging", "--criticality", " Disposable "},
			wantEnvironment: stringPointer("staging"), wantCriticality: stringPointer("disposable")},
		{name: "criticality alone", args: []string{"--criticality", "development"},
			wantCriticality: stringPointer("development")},
		{name: "unknown criticality", args: []string{"--criticality", "high"}, wantUsage: true},
		{name: "environment over the platform limit",
			args: []string{"--environment", strings.Repeat("e", clusterEnvironmentNameLimit+1)}, wantUsage: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := &cobra.Command{Use: "create"}
			registerClusterClassificationFlags(command)
			if parseError := command.ParseFlags(tt.args); parseError != nil {
				t.Fatalf("parsing %v: %v", tt.args, parseError)
			}
			environment, criticality, readError := clusterClassificationFromFlags(command)
			if tt.wantUsage {
				if readError == nil || exitCodeFor(readError) != exitUsage {
					t.Fatalf("error = %v (exit %d), want a usage error", readError, exitCodeFor(readError))
				}
				return
			}
			if readError != nil {
				t.Fatalf("unexpected error: %v", readError)
			}
			assertOptionalString(t, "environment", environment, tt.wantEnvironment)
			assertOptionalString(t, "criticality", criticality, tt.wantCriticality)
		})
	}
}

func assertOptionalString(t *testing.T, field string, got *string, want *string) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %v, want %v", field, describeOptionalString(got), describeOptionalString(want))
	case *got != *want:
		t.Errorf("%s = %q, want %q", field, *got, *want)
	}
}

func describeOptionalString(value *string) string {
	if value == nil {
		return "<omitted>"
	}
	return "\"" + *value + "\""
}

func TestHetznerCreateSendsTheClassification(t *testing.T) {
	resetConfirmFlag(t, hetznerCreateCmd)
	mock := &hetznerCreateMock{}
	out, runError := runWithInput(t, mock, "", "cluster", "hetzner", "create",
		"--name", "env-test", "--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--location", "fsn1",
		"--environment", "production", "--criticality", "Staging")
	if runError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", runError, out)
	}
	if !mock.called {
		t.Fatal("expected CreateHetznerCluster call")
	}
	assertOptionalString(t, "environment", mock.gotRequest.Environment, stringPointer("production"))
	assertOptionalString(t, "criticality", mock.gotRequest.Criticality, stringPointer("staging"))
}

func TestHetznerCreateRefusesAnUnknownCriticalityBeforeCreating(t *testing.T) {
	resetConfirmFlag(t, hetznerCreateCmd)
	mock := &hetznerCreateMock{}
	_, runError := runWithInput(t, mock, "", "cluster", "hetzner", "create",
		"--name", "env-test", "--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1", "--location", "fsn1",
		"--criticality", "high")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("error = %v, want a usage error", runError)
	}
	if mock.called {
		t.Fatal("an unknown criticality must be refused before the cluster is created")
	}
}

type scalewayClassificationMock struct {
	baseMock
	createCalled    bool
	preflightCalled bool
	gotRequest      client.CreateScalewayClusterRequest
}

func (m *scalewayClassificationMock) CreateScalewayCluster(request client.CreateScalewayClusterRequest) (*client.CreateScalewayClusterResponse, error) {
	m.createCalled = true
	m.gotRequest = request
	return &client.CreateScalewayClusterResponse{ClusterID: "scw-1", Name: request.Name}, nil
}

func (m *scalewayClassificationMock) PreflightScalewayCluster(request client.CreateScalewayClusterRequest) (*client.ScalewayPreflightResult, error) {
	m.preflightCalled = true
	m.gotRequest = request
	return &client.ScalewayPreflightResult{}, nil
}

func scalewayClassificationArgs(subcommand string, extra ...string) []string {
	return append([]string{"cluster", "scaleway", subcommand,
		"--name", "env-test", "--credential-id", "cred-1", "--ssh-key-credential-id", "ssh-1",
		"--region", "fr-par", "--zone", "fr-par-1"}, extra...)
}

// The Scaleway builder is shared by create and preflight and now returns an
// error, so both are pinned: the values travel, a blank flag is omitted, and
// a refused value stops before any call.
func TestScalewayCreateAndPreflightCarryTheClassification(t *testing.T) {
	resetConfirmFlag(t, scalewayCreateCmd, scalewayPreflightCmd)

	mock := &scalewayClassificationMock{}
	out, runError := runWithInput(t, mock, "", scalewayClassificationArgs("create", "--environment", "staging")...)
	if runError != nil || !mock.createCalled {
		t.Fatalf("create: called=%v error=%v\noutput: %s", mock.createCalled, runError, out)
	}
	assertOptionalString(t, "environment", mock.gotRequest.Environment, stringPointer("staging"))
	assertOptionalString(t, "criticality", mock.gotRequest.Criticality, nil)

	resetConfirmFlag(t, scalewayCreateCmd, scalewayPreflightCmd)
	mock = &scalewayClassificationMock{}
	if _, runError = runWithInput(t, mock, "", scalewayClassificationArgs("create")...); runError != nil {
		t.Fatalf("create without the flags: %v", runError)
	}
	assertOptionalString(t, "environment", mock.gotRequest.Environment, nil)

	resetConfirmFlag(t, scalewayCreateCmd, scalewayPreflightCmd)
	mock = &scalewayClassificationMock{}
	_, runError = runWithInput(t, mock, "", scalewayClassificationArgs("preflight", "--criticality", "high")...)
	if runError == nil || exitCodeFor(runError) != exitUsage || mock.preflightCalled {
		t.Fatalf("preflight with an unknown criticality: called=%v error=%v, want a usage error and no call",
			mock.preflightCalled, runError)
	}
}
