package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The census reads the annotation by its literal value, so it compiles
// against a tree that predates withClusterArgument (its red-proof did).
func TestClusterArgumentCensusLiteralsMatch(t *testing.T) {
	if clusterArgumentAnnotation != censusClusterArgumentAnnotation ||
		string(clusterSelectionAllowed) != censusSelectionAllowed || string(clusterMustBeNamed) != censusMustBeNamed {
		t.Fatalf("the census literals (%q, %q, %q) no longer match withClusterArgument's (%q, %q, %q)",
			censusClusterArgumentAnnotation, censusSelectionAllowed, censusMustBeNamed,
			clusterArgumentAnnotation, clusterSelectionAllowed, clusterMustBeNamed)
	}
}

// clusterArgumentTestCommand is a command registered the way
// withClusterArgument registers one, under a parent that carries --cluster
// the way `ankra cluster` does. use is its usage line (a command ending in a
// list shows "..."); isListItem is that list's item check.
func clusterArgumentTestCommand(t *testing.T, use string, position int, access clusterArgumentAccess,
	validate cobra.PositionalArgs, isListItem func(string) bool) *cobra.Command {
	t.Helper()
	parent := &cobra.Command{Use: "parent"}
	parent.PersistentFlags().String(activeClusterFlagName, "", "")
	child := registerClusterArgument(position, access, isListItem, &cobra.Command{
		Use:  use,
		Args: validate,
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	parent.AddCommand(child)
	t.Cleanup(func() { delete(clusterArgumentSpecs, child) })
	return child
}

func resolveTestClusterArguments(t *testing.T, command *cobra.Command, flagged string, args ...string) (clusterArgumentResolution, error) {
	t.Helper()
	var flags []string
	if flagged != "" {
		flags = []string{"--" + activeClusterFlagName, flagged}
	}
	if parseError := command.ParseFlags(flags); parseError != nil {
		t.Fatalf("parsing %v: %v", flags, parseError)
	}
	t.Cleanup(func() { resetTreeFlags(t, command, command.Parent()) })
	return clusterArgumentSpecs[command].resolve(command, args)
}

func TestClusterArgumentResolution(t *testing.T) {
	withTempHome(t)
	writeSelectedClusterJSON(t)
	exact := func(count int) cobra.PositionalArgs { return cobra.ExactArgs(count) }
	for _, testCase := range []struct {
		name     string
		use      string
		position int
		access   clusterArgumentAccess
		validate cobra.PositionalArgs
		listItem func(string) bool
		flagged  string
		args     []string
		want     []string
		source   clusterArgumentSource
		// failure is a substring of the expected usage error; "" expects none.
		failure string
	}{
		{name: "argument", validate: exact(1), args: []string{"prod"}, want: []string{"prod"}, source: clusterFromArgument},
		{name: "flag stands in", validate: exact(1), flagged: "prod", want: []string{"prod"}, source: clusterFromFlag},
		{name: "flag before the other arguments", validate: exact(3), flagged: "prod", args: []string{"workers", "3"},
			want: []string{"prod", "workers", "3"}, source: clusterFromFlag},
		{name: "same cluster twice", validate: exact(3), flagged: "prod", args: []string{"prod", "workers", "3"},
			want: []string{"prod", "workers", "3"}, source: clusterFromArgument},
		{name: "two clusters", validate: exact(1), flagged: "staging", args: []string{"prod"}, failure: "name different clusters"},
		{name: "a name and an id", validate: exact(1), flagged: testClusterID, args: []string{"prod"},
			failure: "name different clusters"},
		{name: "too many beside the flag", validate: exact(3), flagged: "prod", args: []string{"a", "b", "c", "d"},
			failure: "counting --cluster"},
		{name: "nothing named", validate: exact(1), failure: "missing the cluster"},
		{name: "nothing named, with other arguments", validate: exact(2), args: []string{"node-1"},
			failure: "--cluster <name|id>"},
		{name: "a read never uses the selection", validate: exact(1), failure: "not use the cluster selected"},
		{name: "the count missing, not the cluster", validate: exact(3), args: []string{"prod", "workers"},
			failure: "accepts 3 arg(s), received 2"},
		{name: "cluster second", position: 1, validate: exact(2), flagged: "prod",
			args: []string{"mesh-1"}, want: []string{"mesh-1", "prod"}, source: clusterFromFlag},
		{name: "cluster second, nothing named", position: 1, validate: exact(2), args: []string{"mesh-1"},
			failure: "missing the cluster"},
		{name: "cluster second, nothing before it", position: 1, validate: exact(2),
			flagged: "prod", failure: "accepts 2 arg(s), received 0"},
		// The same cluster in both places is the argument as given, so the
		// command's own count applies: the group is missing, and the flag is
		// not put in a second time to make up for it (#744).
		{name: "same cluster twice, the count short", validate: exact(2), flagged: "prod", args: []string{"prod"},
			failure: "accepts 2 arg(s), received 1"},
		// `mesh up <mesh> <cluster> [cluster...]`: the flag is the first
		// cluster, not left for the command to find (ankra AI review, #760).
		{name: "list after the cluster, flag only", use: "up <mesh> <cluster_id|name> [cluster_id|name...]", position: 1,
			validate: cobra.MinimumNArgs(2), flagged: "prod",
			args: []string{"mesh-1"}, want: []string{"mesh-1", "prod"}, source: clusterFromFlag},
		// A cluster already where the cluster goes, and not the one
		// --cluster names: two clusters, whatever the list's count allows.
		{name: "list after the cluster, another cluster there", use: "up <mesh> <cluster_id|name> [cluster_id|name...]",
			position: 1, validate: cobra.MinimumNArgs(2), flagged: "prod",
			args: []string{"mesh-1", "staging"}, failure: "name different clusters"},
		{name: "list of clusters, another first", use: "readiness <cluster_id|name> [cluster_id|name...]",
			validate: cobra.MinimumNArgs(1), flagged: "prod", args: []string{"staging"}, failure: "name different clusters"},
		{name: "list of clusters, the flag repeating the first", use: "readiness <cluster_id|name> [cluster_id|name...]",
			validate: cobra.MinimumNArgs(1), flagged: "prod", args: []string{"prod", "staging"},
			want: []string{"prod", "staging"}, source: clusterFromArgument},
		{name: "list of items: the flag adds the cluster", use: "allowed-ips <cluster_id|name> [ip-or-cidr ...]",
			validate: cobra.MinimumNArgs(1), listItem: isAllowedIPsEntry, flagged: "prod", args: []string{"10.0.0.1"},
			want: []string{"prod", "10.0.0.1"}, source: clusterFromFlag},
		{name: "list of items: a CIDR list first", use: "allowed-ips <cluster_id|name> [ip-or-cidr ...]",
			validate: cobra.MinimumNArgs(1), listItem: isAllowedIPsEntry, flagged: "prod", args: []string{"10.0.0.0/8,10.1.0.1"},
			want: []string{"prod", "10.0.0.0/8,10.1.0.1"}, source: clusterFromFlag},
		{name: "list of items: another cluster first", use: "allowed-ips <cluster_id|name> [ip-or-cidr ...]",
			validate: cobra.MinimumNArgs(1), listItem: isAllowedIPsEntry, flagged: "prod", args: []string{"staging", "10.0.0.1"},
			failure: "name different clusters"},
		{name: "list of items: the flag repeating the first", use: "allowed-ips <cluster_id|name> [ip-or-cidr ...]",
			validate: cobra.MinimumNArgs(1), listItem: isAllowedIPsEntry, flagged: "prod",
			args: []string{"prod", "10.0.0.1"}, want: []string{"prod", "10.0.0.1"}, source: clusterFromArgument},
		// An optional cluster argument left out: the command reads --cluster
		// itself, by name or id, as it did before --cluster could stand in.
		{name: "optional: the command reads the flag", access: clusterSelectionAllowed, validate: cobra.MaximumNArgs(1),
			flagged: "prod", want: []string{}, source: clusterLeftToCommand},
		{name: "optional and must be named: the command reads the flag", validate: cobra.MaximumNArgs(1),
			flagged: "prod", want: []string{}, source: clusterLeftToCommand},
		{name: "optional: the command reads the selection", access: clusterSelectionAllowed, validate: cobra.MaximumNArgs(1),
			want: []string{}, source: clusterLeftToCommand},
		{name: "optional, but must be named", validate: cobra.MaximumNArgs(1), failure: "missing the cluster"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			access := testCase.access
			if access == "" {
				access = clusterMustBeNamed
			}
			use := testCase.use
			if use == "" {
				use = "child"
			}
			command := clusterArgumentTestCommand(t, use, testCase.position, access, testCase.validate, testCase.listItem)
			resolution, resolveError := resolveTestClusterArguments(t, command, testCase.flagged, testCase.args...)
			if testCase.failure != "" {
				if resolveError == nil || exitCodeFor(resolveError) != exitUsage || !strings.Contains(resolveError.Error(), testCase.failure) {
					t.Fatalf("error = %v (exit %d), want a usage error containing %q", resolveError, exitCodeFor(resolveError), testCase.failure)
				}
				return
			}
			if resolveError != nil {
				t.Fatalf("resolve: %v", resolveError)
			}
			if !slices.Equal(resolution.args, testCase.want) {
				t.Errorf("args = %q, want %q", resolution.args, testCase.want)
			}
			if resolution.source != testCase.source {
				t.Errorf("source %d, want %d", resolution.source, testCase.source)
			}
		})
	}
}
