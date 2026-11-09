package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Every `ankra cluster ...` command inherits --cluster, documented as the
// cluster the command acts on. Commands that take that cluster as a
// positional argument used to demand the argument and never read the flag:
// `ankra cluster ssh-keys get --cluster <id>` failed with "accepts 1 arg(s),
// received 0", and an argument beside a different --cluster silently won
// (ankra-v3qdto.19, ankra-oznf7k.10). withClusterArgument makes the argument
// and the flag two spellings of the same thing for every such command,
// through one resolver:
//
//   - the cluster is the argument, else --cluster;
//   - the argument and --cluster naming different clusters is a usage error
//     (exit 2). The comparison is of the text, so a name beside the same
//     cluster's id is refused too: the safe side, and nothing is looked up
//     before the command runs;
//   - with --cluster set, the cluster argument is left out, so a command
//     with further arguments takes one fewer (`node-group scale --cluster
//     prod workers 3`). Naming the cluster in both places, the same way, is
//     the argument as given, counted by the command's own check;
//   - a command whose cluster argument may be left out (MaximumNArgs) and is
//     reads --cluster itself, by name or id, exactly as it did before
//     --cluster could stand in for the argument (resolveActiveCluster,
//     resolveOptionalClusterArg);
//   - named in neither place, a command whose usage line shows the cluster
//     as required (<cluster...>) refuses with exit 2, saying how to name it.
//     It never acts on the cluster `ankra cluster select` saved: that is one
//     file per machine, shared by every shell and agent session on it, so a
//     selection made elsewhere could aim a deprovision at the wrong cluster.
//     Only a command registered with clusterSelectionAllowed, whose usage
//     shows the cluster as optional ([cluster...]), falls back to the
//     selection, and the census names each one with its reason.
//
// The census in cluster_argument_census_test.go walks the command tree and
// fails a command that takes a cluster argument without going through here.

// clusterArgumentAnnotation marks a command registered by withClusterArgument,
// with its clusterArgumentAccess as the value.
const clusterArgumentAnnotation = "ankra.cluster_argument"

// clusterArgumentAccess says where a command's cluster may come from when
// neither its argument nor --cluster names it.
type clusterArgumentAccess string

const (
	// clusterMustBeNamed: from nowhere. The cluster is named on the command
	// line, as the argument or with --cluster. Every command whose usage
	// line shows the cluster as required (<cluster_id|name>), which takes in
	// every command that stops, starts, deprovisions, deletes, moves,
	// scales, upgrades, relabels or bills a cluster.
	clusterMustBeNamed clusterArgumentAccess = "explicit"
	// clusterSelectionAllowed: the cluster argument may be left out
	// ([cluster]), and the command then uses the cluster `ankra cluster
	// select` saved, as it did before --cluster could stand in for the
	// argument. Reads, and reconcile, which re-applies what is stored.
	clusterSelectionAllowed clusterArgumentAccess = "selection"
)

// clusterArgumentSpec is what withClusterArgument knows about a command.
type clusterArgumentSpec struct {
	// position is the index of the cluster among the command's arguments.
	position int
	access   clusterArgumentAccess
	// validate is the command's own argument check, of the whole argument
	// list with the cluster at position.
	validate cobra.PositionalArgs
	// list: the arguments end in a list of any length (the usage line
	// shows "..."), so their count cannot say whether the value at the
	// cluster's position is the cluster or the list's first item.
	list bool
	// isListItem reports a value at the cluster's position that is the
	// first item of the list rather than a cluster (an IP or CIDR for
	// `bastion allowed-ips`). nil: every value there is a cluster (`mesh
	// readiness <cluster_id|name> [cluster_id|name...]`).
	isListItem func(string) bool
}

// clusterArgumentSpecs holds the spec of every command registered by
// withClusterArgument, for typedClusterOf.
var clusterArgumentSpecs = map[*cobra.Command]clusterArgumentSpec{}

// withClusterArgument registers a command whose first argument is the
// cluster it acts on. command.Args stays the check of the whole argument
// list (cluster first) and command.RunE keeps reading the cluster from
// args[0]: both are wrapped so the cluster can come from --cluster instead.
// Returns command.
func withClusterArgument(access clusterArgumentAccess, command *cobra.Command) *cobra.Command {
	return registerClusterArgument(0, access, nil, command)
}

// withClusterArgumentAt is withClusterArgument for a command whose cluster is
// the argument at position (`mesh leave <mesh_id> <cluster_id|name>`).
func withClusterArgumentAt(position int, access clusterArgumentAccess, command *cobra.Command) *cobra.Command {
	return registerClusterArgument(position, access, nil, command)
}

// withClusterArgumentBeforeItems is withClusterArgument for a command whose
// cluster argument is followed by a list of other things (`bastion
// allowed-ips <cluster_id|name> [ip-or-cidr ...]`). isListItem tells such a
// thing from a cluster, so `--cluster prod 203.0.113.7` puts prod first
// while `staging --cluster prod` names two clusters.
func withClusterArgumentBeforeItems(access clusterArgumentAccess, isListItem func(string) bool, command *cobra.Command) *cobra.Command {
	return registerClusterArgument(0, access, isListItem, command)
}

func registerClusterArgument(position int, access clusterArgumentAccess, isListItem func(string) bool, command *cobra.Command) *cobra.Command {
	run := command.RunE
	if run == nil {
		panic(fmt.Sprintf("withClusterArgument: %q has no RunE to give the cluster to", command.Use))
	}
	validate := command.Args
	if validate == nil {
		validate = cobra.ArbitraryArgs
	}
	spec := clusterArgumentSpec{
		position:   position,
		access:     access,
		validate:   validate,
		list:       strings.Contains(command.Use, "..."),
		isListItem: isListItem,
	}
	command.Args = func(cmd *cobra.Command, args []string) error {
		_, resolveError := spec.resolve(cmd, args)
		return resolveError
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		resolution, resolveError := spec.resolve(cmd, args)
		if resolveError != nil {
			return resolveError
		}
		return run(cmd, resolution.args)
	}
	if command.Annotations == nil {
		command.Annotations = map[string]string{}
	}
	command.Annotations[clusterArgumentAnnotation] = string(access)
	clusterArgumentSpecs[command] = spec
	return command
}

// clusterArgumentSource is where a resolution found the cluster.
type clusterArgumentSource int

const (
	// clusterFromArgument: the argument, as given.
	clusterFromArgument clusterArgumentSource = iota
	// clusterFromFlag: --cluster, put in the argument list at the
	// cluster's position.
	clusterFromFlag
	// clusterLeftToCommand: the cluster argument may be left out and was.
	// The command finds the cluster itself: --cluster when it is set, by
	// name or id, as it did before --cluster could stand in for the
	// argument, else the selected cluster (clusterSelectionAllowed only).
	clusterLeftToCommand
)

// clusterArgumentResolution is the argument list a command's RunE gets.
type clusterArgumentResolution struct {
	args []string
	// typed is the cluster as it was named (the argument or the --cluster
	// value); "" when the command reads the selected cluster.
	typed  string
	source clusterArgumentSource
}

// holdsCluster reports whether value, at the cluster's position of a list
// command, is a cluster rather than the list's first item.
func (spec clusterArgumentSpec) holdsCluster(value string) bool {
	return spec.list && (spec.isListItem == nil || !spec.isListItem(value))
}

// resolve works out the argument list a command's RunE gets. It reads only
// the command line, never the platform or the saved selection, because it
// runs as the command's Args check, before credentials are read.
func (spec clusterArgumentSpec) resolve(cmd *cobra.Command, args []string) (clusterArgumentResolution, error) {
	fits := func(candidate []string) bool { return spec.validate(cmd, candidate) == nil }
	position := spec.position
	named := len(args) > position
	flagged := clusterFlagOverride(cmd)
	if flagged == "" {
		return spec.resolveUnflagged(cmd, args)
	}
	switch {
	case named && strings.TrimSpace(args[position]) == flagged:
		// The same cluster as the argument and with --cluster: the argument,
		// as given, counted by the command's own check as it always was, so
		// `node-group delete prod --cluster prod` lacks the group rather
		// than deleting a group called prod.
		if ownError := spec.validate(cmd, args); ownError != nil {
			return clusterArgumentResolution{}, usageError(cmd, ownError, "")
		}
		return clusterArgumentResolution{args: args, typed: flagged, source: clusterFromArgument}, nil
	case len(args) < position:
		return clusterArgumentResolution{}, usageError(cmd, spec.validate(cmd, args),
			"the arguments before the cluster are missing")
	case !named && fits(args):
		// The cluster argument may be left out, and is: the command reads
		// --cluster itself (resolveActiveCluster, resolveOptionalClusterArg),
		// by name or id, with the same lookups and exit codes as before
		// --cluster could stand in for the argument.
		return clusterArgumentResolution{args: args, typed: flagged, source: clusterLeftToCommand}, nil
	}
	// --cluster stands in for the argument: the arguments given are the
	// ones around it.
	withFlag := insertClusterArgument(args, position, flagged)
	flagError := spec.validate(cmd, withFlag)
	// The arguments are complete with a cluster at its position, and it is
	// another one than --cluster names. For a command ending in a list the
	// count cannot tell, so the value there decides.
	if named && fits(args) && (flagError != nil || spec.holdsCluster(args[position])) {
		return clusterArgumentResolution{}, differentClustersError(cmd, flagged, args[position])
	}
	if flagError != nil {
		return clusterArgumentResolution{}, withExitCode(exitUsage, fmt.Errorf(
			"%w, counting --cluster %q as the cluster argument; usage: %s", flagError, flagged, cmd.UseLine()))
	}
	return clusterArgumentResolution{args: withFlag, typed: flagged, source: clusterFromFlag}, nil
}

// resolveUnflagged is resolve without --cluster: the cluster is the argument,
// else the selection where the command may use it.
func (spec clusterArgumentSpec) resolveUnflagged(cmd *cobra.Command, args []string) (clusterArgumentResolution, error) {
	fits := func(candidate []string) bool { return spec.validate(cmd, candidate) == nil }
	position := spec.position
	named := len(args) > position
	ownError := spec.validate(cmd, args)
	switch {
	case ownError == nil && named:
		return clusterArgumentResolution{args: args, typed: args[position], source: clusterFromArgument}, nil
	case ownError == nil && spec.access == clusterSelectionAllowed:
		return clusterArgumentResolution{args: args, source: clusterLeftToCommand}, nil
	case ownError == nil, len(args) <= position:
		// What is missing is the cluster.
		return clusterArgumentResolution{}, clusterNotNamedError(cmd)
	}
	usage := usageError(cmd, ownError, "")
	// Arguments are read in order, so `scale prod` lacks its count, not a
	// cluster. When the cluster may be what is missing instead (`upgrade
	// v1.33.1`), the refusal also says how to name it.
	if fits(insertClusterArgument(args, position, "cluster")) {
		return clusterArgumentResolution{}, withExitCode(exitUsage, fmt.Errorf(
			"%w; if the cluster is what is missing, name it as the argument or with --cluster <name|id>: "+
				"the cluster selected with `ankra cluster select` is not used", usage))
	}
	return clusterArgumentResolution{}, usage
}

// usageError is a command's own argument check failing, with its usage
// line, as exit 2. fallback is the message when the check itself passed.
func usageError(cmd *cobra.Command, validateError error, fallback string) error {
	if validateError == nil {
		validateError = fmt.Errorf("%s", fallback)
	}
	return withExitCode(exitUsage, fmt.Errorf("%w; usage: %s", validateError, cmd.UseLine()))
}

// insertClusterArgument returns args with cluster put in at position, as a
// new list.
func insertClusterArgument(args []string, position int, cluster string) []string {
	withCluster := make([]string, 0, len(args)+1)
	withCluster = append(withCluster, args[:position]...)
	withCluster = append(withCluster, cluster)
	return append(withCluster, args[position:]...)
}

// differentClustersError refuses a --cluster that names a different cluster
// from the argument. resolveOptionalClusterArg answers with the same words,
// so the refusal reads the same whichever check meets it first.
func differentClustersError(command *cobra.Command, flagValue, argument string) error {
	return withExitCode(exitUsage, fmt.Errorf(
		"--cluster %q and the argument %q name different clusters; pass one, usage: %s",
		flagValue, argument, command.UseLine()))
}

// clusterArgumentBesideOwnFlag is the Args check of a command that reads its
// optional cluster argument and --cluster itself instead of through
// withClusterArgument (`cluster select`, `kubeconfig add|remove`). It keeps
// the checks ankra-oznf7k.10 gave every leading cluster argument: the
// command's own argument error comes with the usage line, and an argument
// beside a --cluster naming a different cluster is refused (exit 2).
func clusterArgumentBesideOwnFlag(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if validateError := validate(command, args); validateError != nil {
			return usageError(command, validateError, "")
		}
		if flagValue := clusterFlagOverride(command); flagValue != "" && len(args) > 0 &&
			strings.TrimSpace(args[0]) != flagValue {
			return differentClustersError(command, flagValue, args[0])
		}
		return nil
	}
}

// clusterNotNamedError refuses a command whose cluster neither its argument
// nor --cluster names. The selected cluster is not used: it is shared by
// every session on the machine.
func clusterNotNamedError(cmd *cobra.Command) error {
	return withExitCode(exitUsage, fmt.Errorf(
		"missing the cluster: name it as the argument or with --cluster <name|id>; `%s` does not use the cluster "+
			"selected with `ankra cluster select`, which every session on this machine shares; usage: %s",
		cmd.CommandPath(), cmd.UseLine()))
}

// resolveOptionalClusterArg resolves the cluster of a command whose
// <cluster_id|name> argument may be left out: the argument when given, else
// --cluster, else the cluster `ankra cluster select` saved. typed is what the
// user named it by, for messages. node-group list used to demand the
// argument and never read --cluster, so `node-group list --cluster <id>`
// failed with "accepts 1 arg(s), received 0" and an argument beside a
// different --cluster silently won (ankra-v3qdto.6). Both given and naming
// different clusters is refused rather than one quietly ignored.
func resolveOptionalClusterArg(cmd *cobra.Command, args []string) (typed, clusterID string, err error) {
	override := clusterFlagOverride(cmd)
	if len(args) > 0 {
		// The same check and words as withClusterArgument, which runs
		// first, so the refusal never depends on which check met it. A name
		// beside the same cluster's id is refused there too.
		if override != "" && strings.TrimSpace(args[0]) != override {
			return "", "", differentClustersError(cmd, override, args[0])
		}
		clusterID, err = resolveClusterArg(args[0])
		if err != nil {
			return "", "", err
		}
		return args[0], clusterID, nil
	}
	if override != "" {
		clusterID, err = resolveClusterArg(override)
		if err != nil {
			return "", "", err
		}
		return override, clusterID, nil
	}
	selected, selectedError := loadSelectedCluster()
	if selectedError != nil || strings.TrimSpace(selected.ID) == "" {
		return "", "", withExitCode(exitUsage, errNoClusterSelected{})
	}
	typed = selected.Name
	if typed == "" {
		typed = selected.ID
	}
	return typed, selected.ID, nil
}
