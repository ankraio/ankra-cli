package cmd

import (
	"github.com/spf13/cobra"
)

// registerStructuredOutputFlags adds the shared -o/--output flag (json or
// yaml) to commands whose default rendering is human-readable.
func registerStructuredOutputFlags(cmds ...*cobra.Command) {
	for _, command := range cmds {
		if command.Flags().Lookup("output") == nil {
			command.Flags().StringP("output", "o", "", "Output format: json or yaml (default: human-readable)")
		}
	}
}

// structuredFormatFromFlags resolves the requested structured output format
// from -o/--output. Commands without the flag resolve to outputDefault. A
// rejected value is an invocation mistake, so it is tagged exitUsage; callers
// pass the error straight through to Execute.
func structuredFormatFromFlags(cmd *cobra.Command) (outputFormat, error) {
	outputRaw := ""
	if cmd.Flags().Lookup("output") != nil {
		outputRaw, _ = cmd.Flags().GetString("output")
	}
	format, err := parseOutputFormat(outputRaw)
	if err != nil {
		return outputDefault, withExitCode(exitUsage, err)
	}
	return format, nil
}

// renderStructured writes value as JSON or YAML when requested via -o. It
// reports true when structured output was written, so the caller skips its
// human-readable rendering.
func renderStructured(cmd *cobra.Command, value interface{}) (bool, error) {
	format, err := structuredFormatFromFlags(cmd)
	if err != nil {
		return false, err
	}
	if format == outputDefault {
		return false, nil
	}
	return true, encodeStructured(cmd.OutOrStdout(), format, value)
}

// asyncSubmittedResult is the structured shape emitted when an asynchronous
// write was submitted without --wait, so scripted callers get a stable JSON
// document instead of the human guidance text.
type asyncSubmittedResult struct {
	Submitted   bool    `json:"submitted" yaml:"submitted"`
	Status      string  `json:"status" yaml:"status"`
	Operation   string  `json:"operation" yaml:"operation"`
	OperationID *string `json:"operation_id,omitempty" yaml:"operation_id,omitempty"`
	Hint        string  `json:"hint" yaml:"hint"`
}

func newAsyncSubmittedResult(operationLabel string) asyncSubmittedResult {
	return asyncSubmittedResult{
		Submitted: true,
		Status:    "accepted",
		Operation: operationLabel,
		Hint:      "re-run with --wait to block until completion and see the full result",
	}
}
