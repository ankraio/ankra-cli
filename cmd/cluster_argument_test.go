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

// clusterArgumentTestCommand is a command registered by withClusterArgumentAt
// under a parent that carries --cluster the way `ankra cluster` does.
func clusterArgumentTestCommand(t *testing.T, position int, access clusterArgumentAccess, validate cobra.PositionalArgs) *cobra.Command {
	t.Helper()
	parent := &cobra.Command{Use: "parent"}
	parent.PersistentFlags().String(activeClusterFlagName, "", "")
	child := withClusterArgumentAt(position, access, &cobra.Command{
		Use:  "child",
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
		position int
		access   clusterArgumentAccess
		validate cobra.PositionalArgs
		flagged  string
		args     []string
		want     []string
		source   clusterArgumentSource
		also     string
		// failure is a substring of the expected usage error; "" expects none.
		failure string
	}{
		{name: "argument", validate: exact(1), args: []string{"prod"}, want: []string{"prod"}, source: clusterFromArgument},
		{name: "flag stands in", validate: exact(1), flagged: "prod", want: []string{"prod"}, source: clusterFromFlag},
		{name: "flag before the other arguments", validate: exact(3), flagged: "prod", args: []string{"workers", "3"},
			want: []string{"prod", "workers", "3"}, source: clusterFromFlag},
		{name: "same cluster twice", validate: exact(3), flagged: "prod", args: []string{"prod", "workers", "3"},
			want: []string{"prod", "workers", "3"}, source: clusterFromArgument},
		{name: "two spellings, confirmed by RunE", validate: exact(1), flagged: testClusterID, args: []string{"prod"},
			want: []string{"prod"}, source: clusterFromArgument, also: testClusterID},
		{name: "two ids", validate: exact(1), flagged: otherClusterID, args: []string{testClusterID}, failure: "named twice"},
		{name: "one id in two cases", validate: exact(1), flagged: strings.ToUpper(testClusterID), args: []string{testClusterID},
			want: []string{testClusterID}, source: clusterFromArgument},
		{name: "too many beside the flag", validate: exact(3), flagged: "prod", args: []string{"a", "b", "c", "d"},
			failure: "with --cluster, leave the cluster argument out"},
		{name: "selection for a read", validate: exact(2), args: []string{"node-1"},
			want: []string{testClusterID, "node-1"}, source: clusterFromSelection},
		{name: "a write ignores the selection", access: clusterMustBeNamed, validate: exact(2), args: []string{"node-1"},
			failure: "never falls back to the cluster selected"},
		{name: "cluster second", position: 1, access: clusterMustBeNamed, validate: exact(2), flagged: "prod",
			args: []string{"mesh-1"}, want: []string{"mesh-1", "prod"}, source: clusterFromFlag},
		{name: "cluster second, nothing before it", position: 1, access: clusterMustBeNamed, validate: exact(2),
			flagged: "prod", failure: "accepts 2 arg(s), received 0"},
		{name: "list: the flag adds the first", validate: cobra.MinimumNArgs(1), flagged: "prod", args: []string{"10.0.0.1"},
			want: []string{"prod", "10.0.0.1"}, source: clusterFromFlag},
		{name: "list: the flag repeating the first", validate: cobra.MinimumNArgs(1), flagged: "prod",
			args: []string{"prod", "10.0.0.1"}, want: []string{"prod", "10.0.0.1"}, source: clusterFromArgument},
		{name: "optional: the command reads the flag", validate: cobra.MaximumNArgs(1), flagged: "prod",
			want: []string{}, source: clusterLeftToCommand},
		{name: "optional: the command reads the selection", validate: cobra.MaximumNArgs(1),
			want: []string{}, source: clusterLeftToCommand},
		{name: "optional write named nowhere", access: clusterMustBeNamed, validate: cobra.MaximumNArgs(1),
			failure: "never falls back to the cluster selected"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			access := testCase.access
			if access == "" {
				access = clusterSelectionAllowed
			}
			command := clusterArgumentTestCommand(t, testCase.position, access, testCase.validate)
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
			if resolution.source != testCase.source || resolution.alsoFlagged != testCase.also {
				t.Errorf("source %d, also %q; want %d, %q", resolution.source, resolution.alsoFlagged, testCase.source, testCase.also)
			}
		})
	}
}
