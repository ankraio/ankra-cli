package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

// Red-first stub: the command exists with its flags, and does nothing.
var secretsAdoptCmd = &cobra.Command{
	Use: "adopt <label>",
	RunE: func(cmd *cobra.Command, args []string) error {
		return errors.New("not implemented")
	},
}

func init() {
	registerStructuredOutputFlags(secretsAdoptCmd)
	secretsAdoptCmd.Flags().String("cluster", "", "")
	secretsAdoptCmd.Flags().StringP("namespace", "n", "", "")
	secretsAdoptCmd.Flags().String("name", "", "")
	secretsAdoptCmd.Flags().String("key", "", "")
	secretsAdoptCmd.Flags().String("hint", "", "")
	secretsAdoptCmd.Flags().Bool("strip-trailing-newline", false, "")
	secretsCmd.AddCommand(secretsAdoptCmd)
}
