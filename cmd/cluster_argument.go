package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Every `ankra cluster ...` command inherits --cluster, documented as the
// cluster the command acts on. Commands that take that cluster as a
// positional argument used to demand the argument and never read the flag:
// `ankra cluster ssh-keys get --cluster <id>` failed with "accepts 1 arg(s),
// received 0", and an argument beside a different --cluster silently won
// (ankra-v3qdto.19, measured on 121 command definitions). withClusterArgument
// makes the argument and the flag two spellings of the same thing for every
// such command, through one resolver:
//
//   - the cluster is the argument, else --cluster;
//   - the argument and --cluster naming two different clusters is a usage
//     error (exit 2); naming the same one twice is fine;
//   - with --cluster set, the cluster argument is left out, so a command with
//     further arguments takes one fewer (`node-group scale --cluster prod
//     workers 3`). A command whose argument count is fixed also accepts the
//     cluster in both places, which is never ambiguous;
//   - named in neither place, a read uses the cluster `ankra cluster select`
//     saved, and a command that changes or removes a cluster or its machines
//     refuses (exit 2). The selection is one file per machine, shared by
//     every shell and agent session on it, so it is never what a write acts
//     on unless the write already fell back to it before (see
//     clusterSelectionAllowed).
//
// The census in cluster_argument_test.go walks the command tree and fails a
// command that takes a cluster argument without going through here.

// clusterArgumentAnnotation marks a command registered by withClusterArgument,
// with its clusterArgumentAccess as the value.
const clusterArgumentAnnotation = "ankra.cluster_argument"

// clusterArgumentAccess says where a command's cluster may come from when
// neither its argument nor --cluster names it.
type clusterArgumentAccess string

const (
	// clusterSelectionAllowed: from the cluster `ankra cluster select` saved.
	// Reads, and the writes that already fell back to the selection before
	// --cluster could stand in for their argument (reconcile, provision,
	// update, domain), which keep doing so.
	clusterSelectionAllowed clusterArgumentAccess = "selection"
	// clusterMustBeNamed: from nowhere. The command changes or removes the
	// cluster or its machines, so the cluster has to be named on its own
	// command line.
	clusterMustBeNamed clusterArgumentAccess = "explicit"
)

// clusterArgumentPlaceholder stands in for a left-out cluster while a
// command's own argument check is asked whether the rest would fit.
const clusterArgumentPlaceholder = "cluster"

// clusterArgumentSpec is what withClusterArgument knows about a command.
type clusterArgumentSpec struct {
	// position is the index of the cluster among the command's arguments.
	position int
	access   clusterArgumentAccess
	// validate is the command's own argument check, of the whole argument
	// list with the cluster at position.
	validate cobra.PositionalArgs
}

// clusterArgumentSpecs holds the spec of every command registered by
// withClusterArgument, for typedClusterOf.
var clusterArgumentSpecs = map[*cobra.Command]clusterArgumentSpec{}

// withClusterArgument registers a command whose first argument is the
// cluster it acts on. command.Args stays the check of the whole argument
// list (cluster first) and command.RunE keeps reading the cluster from
// args[0]: both are wrapped so the cluster can come from --cluster (or, for
// clusterSelectionAllowed, the selection) instead. Returns command.
func withClusterArgument(access clusterArgumentAccess, command *cobra.Command) *cobra.Command {
	return withClusterArgumentAt(0, access, command)
}

// withClusterArgumentAt is withClusterArgument for a command whose cluster is
// the argument at position (`mesh leave <mesh_id> [cluster_id|name]`).
func withClusterArgumentAt(position int, access clusterArgumentAccess, command *cobra.Command) *cobra.Command {
	run := command.RunE
	if run == nil {
		panic(fmt.Sprintf("withClusterArgument: %q has no RunE to give the cluster to", command.Use))
	}
	validate := command.Args
	if validate == nil {
		validate = cobra.ArbitraryArgs
	}
	spec := clusterArgumentSpec{position: position, access: access, validate: validate}
	command.Args = func(cmd *cobra.Command, args []string) error {
		_, resolveError := spec.resolve(cmd, args)
		return resolveError
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		resolution, resolveError := spec.resolve(cmd, args)
		if resolveError != nil {
			return resolveError
		}
		if resolution.alsoFlagged != "" {
			argumentID, argumentError := resolveClusterArg(resolution.typed)
			if argumentError != nil {
				return argumentError
			}
			if differentError := refuseADifferentFlaggedCluster(resolution.typed, argumentID, resolution.alsoFlagged); differentError != nil {
				return differentError
			}
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
	// clusterFromSelection: the selected cluster's id, put in the same way.
	clusterFromSelection
	// clusterLeftToCommand: the command's cluster argument is optional and
	// was left out; the command reads --cluster or the selection itself.
	clusterLeftToCommand
)

// clusterArgumentResolution is the argument list a command's RunE gets.
type clusterArgumentResolution struct {
	args []string
	// typed is the cluster as it was named (the argument, the --cluster
	// value, or the selected cluster's id); "" for clusterLeftToCommand.
	typed  string
	source clusterArgumentSource
	// alsoFlagged is the --cluster value when the argument names the
	// cluster too, spelled differently; RunE confirms they are one cluster
	// before the command runs.
	alsoFlagged string
}

// resolve works out the argument list a command's RunE gets. It reads only
// the command line and the saved selection, never the platform, because it
// runs as the command's Args check, before credentials are read. Whether the
// cluster was left out is asked of the command's own check: the arguments
// fit it with the cluster put in, and do not as given.
func (spec clusterArgumentSpec) resolve(cmd *cobra.Command, args []string) (clusterArgumentResolution, error) {
	fits := func(candidate []string) bool { return spec.validate(cmd, candidate) == nil }
	position := spec.position
	named := len(args) > position
	if override := clusterFlagOverride(cmd); override != "" {
		if len(args) < position {
			if shortError := spec.validate(cmd, args); shortError != nil {
				return clusterArgumentResolution{}, withExitCode(exitUsage, shortError)
			}
			return clusterArgumentResolution{args: args, source: clusterLeftToCommand}, nil
		}
		if named && args[position] == override && fits(args) {
			return clusterArgumentResolution{args: args, typed: override, source: clusterFromArgument}, nil
		}
		if len(args) == position && fits(args) {
			return clusterArgumentResolution{args: args, source: clusterLeftToCommand}, nil
		}
		withFlag := insertClusterArgument(args, position, override)
		flagError := spec.validate(cmd, withFlag)
		if flagError == nil {
			return clusterArgumentResolution{args: withFlag, typed: override, source: clusterFromFlag}, nil
		}
		if named && fits(args) {
			argument := args[position]
			if isLikelyClusterID(argument) && isLikelyClusterID(override) {
				if strings.EqualFold(argument, override) {
					return clusterArgumentResolution{args: args, typed: argument, source: clusterFromArgument}, nil
				}
				return clusterArgumentResolution{}, clusterNamedTwiceError(argument, override)
			}
			return clusterArgumentResolution{args: args, typed: argument, source: clusterFromArgument, alsoFlagged: override}, nil
		}
		var coded *codedError
		if errors.As(flagError, &coded) {
			return clusterArgumentResolution{}, flagError
		}
		return clusterArgumentResolution{}, withExitCode(exitUsage, fmt.Errorf(
			"%d argument(s) beside --cluster %q do not fit `%s`: with --cluster, leave the cluster argument out",
			len(args), override, cmd.UseLine()))
	}
	ownError := spec.validate(cmd, args)
	if ownError == nil {
		if named {
			return clusterArgumentResolution{args: args, typed: args[position], source: clusterFromArgument}, nil
		}
		if spec.access == clusterMustBeNamed {
			return clusterArgumentResolution{}, clusterNotNamedError(cmd)
		}
		return clusterArgumentResolution{args: args, source: clusterLeftToCommand}, nil
	}
	if len(args) < position || !fits(insertClusterArgument(args, position, clusterArgumentPlaceholder)) {
		return clusterArgumentResolution{}, withExitCode(exitUsage, ownError)
	}
	if spec.access == clusterMustBeNamed {
		return clusterArgumentResolution{}, clusterNotNamedError(cmd)
	}
	_, selectedID, selectedError := selectedClusterArgument()
	if selectedError != nil {
		return clusterArgumentResolution{}, selectedError
	}
	return clusterArgumentResolution{
		args:   insertClusterArgument(args, position, selectedID),
		typed:  selectedID,
		source: clusterFromSelection,
	}, nil
}

// insertClusterArgument returns args with cluster put in at position, as a
// new list.
func insertClusterArgument(args []string, position int, cluster string) []string {
	withCluster := make([]string, 0, len(args)+1)
	withCluster = append(withCluster, args[:position]...)
	withCluster = append(withCluster, cluster)
	return append(withCluster, args[position:]...)
}

// clusterNamedTwiceError refuses an argument and a --cluster that name two
// different clusters, rather than quietly acting on one of them.
func clusterNamedTwiceError(argument, flagged string) error {
	return withExitCode(exitUsage, fmt.Errorf(
		"the cluster is named twice, %q as the argument and %q with --cluster; pass one", argument, flagged))
}

// clusterNotNamedError refuses a command that changes or removes a cluster
// when neither its argument nor --cluster names one.
func clusterNotNamedError(cmd *cobra.Command) error {
	return withExitCode(exitUsage, fmt.Errorf(
		"name the cluster `%s` acts on, as its argument or with --cluster <name|id>: a command that changes a cluster "+
			"never falls back to the cluster selected with `ankra cluster select`", cmd.CommandPath()))
}

// refuseADifferentFlaggedCluster refuses a --cluster that names another
// cluster than the argument did. argumentID is the argument, resolved.
func refuseADifferentFlaggedCluster(argument, argumentID, flagged string) error {
	if flagged == "" || flagged == argument {
		return nil
	}
	flaggedID, flaggedError := resolveClusterArg(flagged)
	if flaggedError != nil {
		return flaggedError
	}
	if !strings.EqualFold(flaggedID, argumentID) {
		return clusterNamedTwiceError(argument, flagged)
	}
	return nil
}

// selectedClusterArgument is the cluster `ankra cluster select` saved: its
// name for messages (its id when the selection has no name) and its id. No
// selection is a usage error naming --cluster.
func selectedClusterArgument() (typed, clusterID string, err error) {
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
		clusterID, err = resolveClusterArg(args[0])
		if err != nil {
			return "", "", err
		}
		if differentError := refuseADifferentFlaggedCluster(args[0], clusterID, override); differentError != nil {
			return "", "", differentError
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
	return selectedClusterArgument()
}
