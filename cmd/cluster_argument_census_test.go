package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The census of every `ankra cluster ...` command whose arguments include
// the cluster it acts on (ankra-v3qdto.19). Each inherits --cluster,
// documented as the cluster the command acts on, so each must take the
// cluster from --cluster when the argument is left out, refuse an argument
// and a --cluster that name different clusters, and act on the cluster
// `ankra cluster select` saved only when it is one of the few reads (and
// reconcile and update) listed in clusterSelectionFallbacks. A command added
// later that takes a cluster argument without going through
// withClusterArgument fails here, and so does one that falls back to the
// selection without being listed.
//
// The literals mirror clusterArgumentAnnotation and its two
// clusterArgumentAccess values; TestClusterArgumentCensusLiteralsMatch pins
// them.
const (
	censusClusterArgumentAnnotation = "ankra.cluster_argument"
	censusSelectionAllowed          = "selection"
	censusMustBeNamed               = "explicit"
)

// clusterArgumentCensusExempt are the commands whose argument names a
// cluster but that do not act on it through withClusterArgument, and why.
var clusterArgumentCensusExempt = map[string]string{
	"ankra cluster select":            "the argument is the cluster to select, not one to act on",
	"ankra cluster kubeconfig add":    "folds its argument into its own --cluster flag (applyKubeconfigPositionalCluster), which already refuses two different clusters",
	"ankra cluster kubeconfig remove": "folds its argument into its own --cluster flag (applyKubeconfigPositionalCluster), which already refuses two different clusters",
}

// clusterSelectionFallbacks are the only commands whose cluster argument may
// be left out, the command then acting on the cluster `ankra cluster select`
// saved, and why each may. The selection is one file shared by every shell
// and agent session on the machine, so a command that stops, starts,
// deprovisions, deletes, moves, scales, upgrades or bills a cluster is never
// one. Listing a command here is the reviewed decision to let it use the
// selection; it is not keyed off the command's name.
var clusterSelectionFallbacks = map[string]string{
	"ankra cluster audit-logs":                   "reads the cluster's audit trail",
	"ankra cluster domain":                       "reads the cluster's domain; it never creates a zone",
	"ankra cluster gitops status":                "reads the cluster's GitOps wiring",
	"ankra cluster gitops conflicts list":        "reads the open merge conflicts",
	"ankra cluster gitops preserved list":        "reads the preserved paths",
	"ankra cluster node-group list":              "reads the node groups",
	"ankra cluster hetzner node-group list":      "reads the node groups",
	"ankra cluster ovh node-group list":          "reads the node groups",
	"ankra cluster upcloud node-group list":      "reads the node groups",
	"ankra cluster digitalocean node-group list": "reads the node groups",
	"ankra cluster reconcile":                    "re-applies the cluster's stored definition; it removes and bills nothing, and fell back to the selection before",
	"ankra cluster update":                       "sets the environment label and reporting level; it removes and bills nothing, and fell back to the selection before",
}

var censusPlaceholder = regexp.MustCompile(`<[^<>]+>|\[[^\[\]]+\]`)

// clusterArgumentUse reads a usage line: the index of the cluster among the
// command's arguments, the required arguments around it (placeholder
// values, in order, without the cluster), and whether the command takes a
// list of arguments ("..."). ok is false when no argument is a cluster. Flags in the usage
// line ("--reason <text>") are not arguments.
func clusterArgumentUse(use string) (position int, others []string, variadic, ok bool) {
	if flagStart := strings.Index(use, " -"); flagStart >= 0 {
		use = use[:flagStart]
	}
	fields := strings.SplitN(use, " ", 2)
	if len(fields) < 2 {
		return 0, nil, false, false
	}
	position = -1
	for _, token := range censusPlaceholder.FindAllString(fields[1], -1) {
		inner := strings.TrimSpace(token[1 : len(token)-1])
		if strings.Contains(inner, "...") {
			variadic = true
		}
		if position < 0 && strings.HasPrefix(inner, "cluster") {
			position = len(others)
			continue
		}
		if strings.HasPrefix(token, "<") {
			others = append(others, "census-argument-"+string(rune('a'+len(others))))
		}
	}
	return position, others, variadic, position >= 0
}

func withCensusCluster(others []string, position int, cluster string) []string {
	arguments := append([]string{}, others[:position]...)
	arguments = append(arguments, cluster)
	return append(arguments, others[position:]...)
}

// censusArgs runs a command's Args check the way cobra does after parsing
// flags: with --cluster set to flagged ("" leaves it unset).
func censusArgs(t *testing.T, command *cobra.Command, flagged string, arguments []string) error {
	t.Helper()
	resetTreeFlags(t, command)
	t.Cleanup(func() { resetTreeFlags(t, command) })
	var flags []string
	if flagged != "" {
		flags = []string{"--cluster", flagged}
	}
	if parseError := command.ParseFlags(flags); parseError != nil {
		t.Fatalf("parsing %v: %v", flags, parseError)
	}
	if command.Args == nil {
		return nil
	}
	return command.Args(command, arguments)
}

func clusterArgumentCommands(t *testing.T) []*cobra.Command {
	t.Helper()
	var found []*cobra.Command
	var walk func(command *cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Runnable() && !command.DisableFlagParsing {
			if _, _, _, ok := clusterArgumentUse(command.Use); ok {
				found = append(found, command)
			}
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(clusterCmd)
	return found
}

func TestEveryClusterArgumentCommandTakesTheClusterFlag(t *testing.T) {
	withTempHome(t)
	writeSelectedClusterJSON(t)
	commands := clusterArgumentCommands(t)
	if len(commands) < 150 {
		t.Fatalf("the census found %d commands taking a cluster argument; the walk is broken", len(commands))
	}
	fallbacksSeen := map[string]bool{}
	for _, command := range commands {
		path := command.CommandPath()
		position, others, variadic, _ := clusterArgumentUse(command.Use)
		if reason, exempt := clusterArgumentCensusExempt[path]; exempt {
			t.Run(path, func(t *testing.T) {
				if reason == "" {
					t.Fatal("an exemption needs its reason")
				}
				if command.Annotations[censusClusterArgumentAnnotation] != "" {
					t.Fatalf("%s is exempt from the census but registered with withClusterArgument; drop the exemption", path)
				}
			})
			continue
		}
		t.Run(path, func(t *testing.T) {
			access := command.Annotations[censusClusterArgumentAnnotation]
			if access != censusSelectionAllowed && access != censusMustBeNamed {
				t.Errorf("%s takes a cluster argument (%q) but is not registered with withClusterArgument, "+
					"so --cluster cannot stand in for it", path, command.Use)
			}
			reason, listed := clusterSelectionFallbacks[path]
			switch {
			case access == censusSelectionAllowed && !listed:
				t.Errorf("%s is registered with clusterSelectionAllowed, so it acts on the cluster `ankra cluster select` "+
					"saved when none is named; list it in clusterSelectionFallbacks with the reason it may, "+
					"or register it with clusterMustBeNamed", path)
			case access == censusMustBeNamed && listed:
				t.Errorf("%s is listed in clusterSelectionFallbacks (%s) but registered with clusterMustBeNamed; drop the entry",
					path, reason)
			}
			if listed {
				fallbacksSeen[path] = true
			}
			optional := strings.Contains(command.Use, "[cluster")
			if access == censusMustBeNamed && optional && !variadic {
				t.Errorf("%s must be named, so its usage line shows the cluster as required (<cluster_id|name>); got %q", path, command.Use)
			}
			if access == censusSelectionAllowed && !optional {
				t.Errorf("%s falls back to the selected cluster, so its usage line shows the cluster as optional "+
					"([cluster_id|name]); got %q", path, command.Use)
			}

			if err := censusArgs(t, command, otherClusterID, others); err != nil {
				t.Errorf("%s --cluster %s %v: %v; --cluster must stand in for the cluster argument",
					path, otherClusterID, others, err)
			}
			if err := censusArgs(t, command, otherClusterID, withCensusCluster(others, position, otherClusterID)); err != nil {
				t.Errorf("%s naming the same cluster as the argument and with --cluster: %v; that is allowed", path, err)
			}
			if !variadic {
				twice := censusArgs(t, command, otherClusterID, withCensusCluster(others, position, testClusterID))
				if twice == nil || exitCodeFor(twice) != exitUsage || !strings.Contains(twice.Error(), "name different clusters") {
					t.Errorf("%s with the argument and --cluster naming two clusters: got %v (exit %d), "+
						"want the different-clusters usage error (exit 2)", path, twice, exitCodeFor(twice))
				}
			}

			unnamed := censusArgs(t, command, "", others)
			switch access {
			case censusMustBeNamed:
				if unnamed == nil || exitCodeFor(unnamed) != exitUsage || !strings.Contains(unnamed.Error(), "--cluster") {
					t.Errorf("%s with no cluster named and one selected: got %v (exit %d), want a usage error (exit 2) "+
						"naming --cluster: it must not act on the selected cluster", path, unnamed, exitCodeFor(unnamed))
				}
			default:
				if unnamed != nil {
					t.Errorf("%s with no cluster named and one selected: %v; it falls back to the selected cluster", path, unnamed)
				}
			}
		})
	}
	for path := range clusterSelectionFallbacks {
		if !fallbacksSeen[path] {
			t.Errorf("clusterSelectionFallbacks lists %s, which the census did not find; drop the entry", path)
		}
	}
}

// The usage-line reader the census depends on.
func TestClusterArgumentUseReadsTheUsageLine(t *testing.T) {
	for _, testCase := range []struct {
		use      string
		position int
		others   int
		variadic bool
		ok       bool
	}{
		{use: "stop [cluster_id|name]", position: 0, ok: true},
		{use: "scale [cluster_id|name] <group_name> <count>", position: 0, others: 2, ok: true},
		{use: "leave <mesh_id> [cluster_id|name]", position: 1, others: 1, ok: true},
		{use: "up <mesh_name|mesh_id> [cluster_id|name...]", position: 1, others: 1, variadic: true, ok: true},
		{use: "allowed-ips [cluster_id|name] [ip-or-cidr ...]", position: 0, variadic: true, ok: true},
		{use: "rollback-record [cluster_id] <version> --reason <text>", position: 0, others: 1, ok: true},
		{use: "set [cluster_id] --for <duration> --reason <text>", position: 0, ok: true},
		{use: "list", ok: false},
		{use: "delete <name>", ok: false},
	} {
		position, others, variadic, ok := clusterArgumentUse(testCase.use)
		if ok != testCase.ok || (ok && (position != testCase.position || len(others) != testCase.others || variadic != testCase.variadic)) {
			t.Errorf("%q: position %d, %d others, variadic %v, ok %v; want %d, %d, %v, %v", testCase.use,
				position, len(others), variadic, ok, testCase.position, testCase.others, testCase.variadic, testCase.ok)
		}
	}
}
