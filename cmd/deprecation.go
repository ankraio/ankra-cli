package cmd

import (
	"fmt"

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
