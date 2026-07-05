package cmd

import "github.com/spf13/cobra"

// Root exposes the assembled command tree for documentation generation
// (tools/gendocs). It must not be used to execute commands; Execute()
// remains the only entry point for running the CLI.
func Root() *cobra.Command {
	rootCmd.Version = version
	return rootCmd
}
