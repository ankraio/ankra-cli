package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// markDeprecatedForGenericVerb attaches a cobra deprecation notice to a
// provider-specific command, pointing users at the cloud-agnostic generic verb
// that replaces it. Cobra prints "Command %q is deprecated, <notice>" before
// the command runs, so the warning surfaces on every invocation. Replacements
// are tracked in DEPRECATIONS.md.
func markDeprecatedForGenericVerb(replacement string, cmds ...*cobra.Command) {
	notice := fmt.Sprintf("use `%s` instead; the cloud provider is detected automatically.", replacement)
	for _, command := range cmds {
		command.Deprecated = notice
	}
}

// deprecateAndForward registers, under parent, a hidden command at the old
// spelling that forwards every invocation to newPath (a space-separated
// command path below root, e.g. "cluster delete"). Unlike
// markDeprecatedForGenericVerb, the old implementation is not kept: the
// forwarder rewrites arguments (rewrite may be nil for passthrough — it must
// handle flags interleaved with positionals, since flag parsing is disabled
// on the forwarder) and re-dispatches through the root command, so the
// target's flags, validation, and hooks all apply exactly once.
//
// Two deprecation signals are emitted before forwarding: cobra's standard
// human-facing notice, and a machine-readable stderr line
// `ANKRA_DEPRECATED=<old>=><new> removal=<version>` for scripts and agents
// (stderr keeps `-o json` output parseable). Each forwarder must have a
// matching entry in DEPRECATIONS.md.
//
// The forwarder itself is auth-free: with DisableFlagParsing, the persistent
// pre-run would otherwise resolve credentials before --token/--base-url are
// parsed and wrongly reject `ankra <old-cmd> --token ...`. Authentication is
// enforced on the target during re-dispatch, after its flags are parsed.
// The nested dispatch runs with errors silenced so a failing target prints
// its error exactly once (from the outer Execute).
func deprecateAndForward(parent *cobra.Command, use, newPath, removalVersion string, rewrite func([]string) []string) *cobra.Command {
	forwarder := &cobra.Command{
		Use:                use,
		Short:              fmt.Sprintf("Deprecated: use `ankra %s` instead", newPath),
		Hidden:             true,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Command %q is deprecated and will be removed in %s, use `ankra %s` instead\n",
				cmd.CommandPath(), removalVersion, newPath)
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "ANKRA_DEPRECATED=%s=>ankra %s removal=%s\n",
				cmd.CommandPath(), newPath, removalVersion)
			if rewrite != nil {
				args = rewrite(args)
			}
			root := cmd.Root()
			root.SetArgs(append(strings.Split(newPath, " "), args...))
			previousSilenceErrors := root.SilenceErrors
			root.SilenceErrors = true
			defer func() { root.SilenceErrors = previousSilenceErrors }()
			return root.Execute()
		},
	}
	setRequiresAuth(forwarder, false)
	parent.AddCommand(forwarder)
	return forwarder
}
