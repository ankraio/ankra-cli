package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// leadingClusterPositional matches a Use line whose first argument is the
// cluster: submatch 1 is "<" for a required one and "[" for an optional one.
var leadingClusterPositional = regexp.MustCompile(`^\S+\s+([<\[])cluster[^>\]\s]*[>\]]`)

// clusterPositionalKind answers "<" or "[" for a command that takes the cluster
// as its first argument, and "" for any other.
func clusterPositionalKind(command *cobra.Command) string {
	match := leadingClusterPositional.FindStringSubmatch(command.Use)
	if match == nil {
		return ""
	}
	return match[1]
}

// clusterPositionalArgs wraps a command's argument validator for a command that
// takes the cluster as its first argument. Every command under `ankra cluster`
// inherits --cluster, so cobra accepts the flag on these too, and it was never
// read: a caller who passed only the flag heard "accepts 1 arg(s), received 0",
// and one who passed both got the positional with no word about the flag.
//
// The flag never stands in for a required positional. These commands include
// stop, delete and destroy, which must name their target rather than fall back
// on anything shared between sessions.
func clusterPositionalArgs(validate cobra.PositionalArgs, isRequired bool) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		flagValue := clusterFlagOverride(command)
		validateError := error(nil)
		if validate != nil {
			validateError = validate(command, args)
		}
		isMissing := isRequired && len(args) == 0
		isFlagAgreeing := flagValue != "" && len(args) > 0 && strings.TrimSpace(args[0]) == flagValue

		switch {
		case flagValue != "" && !isFlagAgreeing && (isMissing || validateError != nil):
			return withExitCode(exitUsage, fmt.Errorf(
				"this command takes the cluster as its first argument, not --cluster; usage: %s", command.UseLine()))
		case isMissing:
			return withExitCode(exitUsage, fmt.Errorf("missing the cluster argument; usage: %s", command.UseLine()))
		case validateError != nil:
			return withExitCode(exitUsage, fmt.Errorf("%w; usage: %s", validateError, command.UseLine()))
		case flagValue != "" && len(args) > 0 && strings.TrimSpace(args[0]) != flagValue:
			return withExitCode(exitUsage, fmt.Errorf(
				"--cluster %q and the argument %q name different clusters; pass one, usage: %s",
				flagValue, args[0], command.UseLine()))
		}
		return nil
	}
}

// wrapClusterPositionalArgs gives every command under `ankra cluster` that
// takes the cluster as its first argument the validator above. It works from
// the Use line, so a command added later is covered without asking for it.
func wrapClusterPositionalArgs(command *cobra.Command) {
	if kind := clusterPositionalKind(command); kind != "" {
		command.Args = clusterPositionalArgs(command.Args, kind == "<")
	}
	for _, subcommand := range command.Commands() {
		wrapClusterPositionalArgs(subcommand)
	}
}
