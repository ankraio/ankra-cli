package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// secretStdinLimit bounds what a --*-stdin flag reads. The largest secret a
// route here takes is 65536 bytes.
const secretStdinLimit = 1 << 20

// maskedSecretPrompt reads a secret with a masked prompt; a variable so tests
// can replace the terminal interaction.
var maskedSecretPrompt = func(label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Mask:  '*',
		Validate: func(input string) error {
			if input == "" {
				return errors.New("it cannot be empty")
			}
			return nil
		},
	}
	secret, promptError := prompt.Run()
	if promptError != nil {
		if isPromptCancellation(promptError) {
			return "", errCancelled
		}
		return "", fmt.Errorf("reading the %s: %w", label, promptError)
	}
	return secret, nil
}

// readSecretFromStdinOrPrompt takes a secret from stdin when the named
// --*-stdin flag is set, and from a masked prompt otherwise. There is no flag
// that carries the secret itself: an argument lands in shell history and in
// the process list. Without a terminal to prompt on, the stdin flag is
// required. Only the trailing newline a pipe or heredoc adds is dropped,
// since a secret may legitimately carry other whitespace. The secret is
// never printed back.
func readSecretFromStdinOrPrompt(command *cobra.Command, stdinFlagName string, label string) (string, error) {
	fromStdin, _ := command.Flags().GetBool(stdinFlagName)
	if fromStdin {
		raw, readError := io.ReadAll(io.LimitReader(command.InOrStdin(), secretStdinLimit))
		if readError != nil {
			return "", fmt.Errorf("read the %s from stdin: %w", label, readError)
		}
		secret := strings.TrimRight(string(raw), "\r\n")
		if secret == "" {
			return "", withExitCode(exitUsage, fmt.Errorf("no %s was read from stdin", label))
		}
		return secret, nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return "", withExitCode(exitUsage, fmt.Errorf(
			"the %s is never taken as an argument: pipe it on stdin with --%s, or run in a terminal to be prompted",
			label, stdinFlagName))
	}
	return maskedSecretPrompt(label)
}

// confirmOrganisationChange prints what a change does and asks for it to be
// confirmed. --yes skips the question; without a terminal to ask on, --yes
// is required. Everything goes to stderr so -o json|yaml stays parseable.
func confirmOrganisationChange(command *cobra.Command, consequence string) error {
	yes, _ := command.Flags().GetBool("yes")
	if yes {
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"%s This needs confirmation, but there is no terminal to ask on; re-run with --yes to confirm", consequence))
	}
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprintln(errOut, consequence)
	if promptError := confirmPrompt(command.InOrStdin(), errOut, "Continue? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// requiredTextFlag reads a flag that has to carry a value.
func requiredTextFlag(command *cobra.Command, flagName string, meaning string) (string, error) {
	value, _ := command.Flags().GetString(flagName)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("--%s is required: %s", flagName, meaning))
	}
	return value, nil
}

// renderRoundedTable prints rows under header in the CLI's table style.
func renderRoundedTable(out io.Writer, header table.Row, rows []table.Row) {
	listing := table.NewWriter()
	listing.SetOutputMirror(out)
	listing.SetStyle(table.StyleRounded)
	listing.AppendHeader(header)
	listing.AppendRows(rows)
	listing.Render()
}
