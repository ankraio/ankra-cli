package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// operationCommand describes a command that sends one request and renders
// the platform's answer: as it was sent under -o json|yaml, through human
// otherwise.
type operationCommand struct {
	use       string
	aliases   []string
	short     string
	long      string
	example   string
	arguments cobra.PositionalArgs
	invoke    func(command *cobra.Command, arguments []string) (json.RawMessage, error)
	human     func(out io.Writer, document json.RawMessage) error
}

func newOperationCommand(operation operationCommand) *cobra.Command {
	command := &cobra.Command{
		Use:     operation.use,
		Aliases: operation.aliases,
		Short:   operation.short,
		Long:    operation.long,
		Example: operation.example,
		Args:    operation.arguments,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			document, invokeError := operation.invoke(command, arguments)
			if invokeError != nil {
				return invokeError
			}
			return renderDocument(command, document, func(out io.Writer) error { return operation.human(out, document) })
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

// acknowledgementDocument is the answer of a write the platform answers
// with no body, so -o json|yaml still prints a document.
func acknowledgementDocument(members map[string]any) (json.RawMessage, error) {
	document, marshalError := json.Marshal(members)
	if marshalError != nil {
		return nil, fmt.Errorf("encoding the result: %w", marshalError)
	}
	return document, nil
}

// requiredArgument reads a positional argument that names one thing.
func requiredArgument(arguments []string, index int, meaning string) (string, error) {
	value := strings.TrimSpace(arguments[index])
	if value == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("%s cannot be blank", meaning))
	}
	return value, nil
}

// readDocumentFile reads a file a flag names, or stdin for "-".
func readDocumentFile(command *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		content, readError := io.ReadAll(command.InOrStdin())
		if readError != nil {
			return nil, fmt.Errorf("reading stdin: %w", readError)
		}
		return content, nil
	}
	content, readError := os.ReadFile(path)
	if readError != nil {
		return nil, withExitCode(exitUsage, fmt.Errorf("reading %s: %w", path, readError))
	}
	return content, nil
}

// readJSONObjectFile reads a JSON object from a file a flag names, or from
// stdin for "-".
func readJSONObjectFile(command *cobra.Command, path string) (map[string]any, error) {
	content, readError := readDocumentFile(command, path)
	if readError != nil {
		return nil, readError
	}
	object := map[string]any{}
	if decodeError := json.Unmarshal(content, &object); decodeError != nil {
		return nil, withExitCode(exitUsage, fmt.Errorf("%s is not a JSON object: %w", path, decodeError))
	}
	return object, nil
}
