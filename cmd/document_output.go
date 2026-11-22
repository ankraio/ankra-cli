package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// renderDocument prints the platform's answer as -o json|yaml asked for it,
// and otherwise hands it to human. A nil human prints the document as JSON.
func renderDocument(command *cobra.Command, document json.RawMessage, human func(out io.Writer) error) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	if format != outputDefault || human == nil {
		return renderApplicationPayload(command, document)
	}
	return human(command.OutOrStdout())
}

// decodeDocument reads the members a human rendering needs out of the
// platform's answer.
func decodeDocument(document json.RawMessage, target any) error {
	if decodeError := json.Unmarshal(document, target); decodeError != nil {
		return fmt.Errorf("parsing response: %w", decodeError)
	}
	return nil
}
