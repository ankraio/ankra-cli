package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
	// The notice itself is written by encodeStructuredCounting, so that the
	// call sites which reach it directly emit one too; this passes the
	// command's own error writer so SetErr redirection is honoured.
	_, err = encodeStructuredCounting(cmd.OutOrStdout(), format, value, cmd.ErrOrStderr())
	if err != nil {
		return true, err
	}
	return true, nil
}

// rawDocumentCarrier is a value decoded from a platform response that kept
// the response body beside the decode (client.RawDocument).
type rawDocumentCarrier interface {
	RawJSON() json.RawMessage
}

// structuredDocumentFor is what -o json|yaml prints for value. A value that
// carries the platform's own response document prints that document: the
// struct declares only the fields the human rendering reads, so encoding it
// would silently drop every field the platform added since (ankra-bn86k4.8.5).
// JSON is the body itself, re-indented; YAML is the same document converted
// node by node, so key order and number literals survive. Any other value,
// or a carrier that holds no document (built in code, a test mock), prints
// as the struct, as before.
func structuredDocumentFor(format outputFormat, value interface{}) interface{} {
	carrier, isCarrier := value.(rawDocumentCarrier)
	if !isCarrier {
		return value
	}
	if reflected := reflect.ValueOf(value); reflected.Kind() == reflect.Pointer && reflected.IsNil() {
		return value
	}
	document := carrier.RawJSON()
	if len(bytes.TrimSpace(document)) == 0 || !json.Valid(document) {
		return value
	}
	switch format {
	case outputJSON:
		return document
	case outputYAML:
		node, convertError := yamlNodeFromJSON(document)
		if convertError != nil {
			return value
		}
		return node
	default:
		return value
	}
}

// yamlNodeFromJSON converts a JSON document to a YAML node tree in block
// style. It walks the JSON tokens rather than decoding into Go maps, so the
// platform's key order is kept, and numbers keep their literal: a JSON
// integer above 2^53 is not respelled as a float.
func yamlNodeFromJSON(document []byte) (*yaml.Node, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	node, err := yamlNodeFromJSONTokens(decoder)
	if err != nil {
		return nil, err
	}
	if _, trailingError := decoder.Token(); !errors.Is(trailingError, io.EOF) {
		return nil, errors.New("trailing data after the JSON document")
	}
	return node, nil
}

func yamlNodeFromJSONTokens(decoder *json.Decoder) (*yaml.Node, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for decoder.More() {
				keyToken, keyError := decoder.Token()
				if keyError != nil {
					return nil, keyError
				}
				key, isString := keyToken.(string)
				if !isString {
					return nil, fmt.Errorf("object key %v is not a string", keyToken)
				}
				value, valueError := yamlNodeFromJSONTokens(decoder)
				if valueError != nil {
					return nil, valueError
				}
				mapping.Content = append(mapping.Content, yamlStringNode(key), value)
			}
			if _, closeError := decoder.Token(); closeError != nil {
				return nil, closeError
			}
			return mapping, nil
		case '[':
			sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for decoder.More() {
				item, itemError := yamlNodeFromJSONTokens(decoder)
				if itemError != nil {
					return nil, itemError
				}
				sequence.Content = append(sequence.Content, item)
			}
			if _, closeError := decoder.Token(); closeError != nil {
				return nil, closeError
			}
			return sequence, nil
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %q", typed)
		}
	case string:
		// yamlStringNode's !!str tag makes the encoder quote a value that
		// would otherwise read back as another type ("true", "12", "").
		return yamlStringNode(typed), nil
	case json.Number:
		// No tag: the plain literal resolves to the number it spells, and
		// a tag would print as "!!int" on an integer too large for int64.
		return &yaml.Node{Kind: yaml.ScalarNode, Value: typed.String()}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(typed)}, nil
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	default:
		return nil, fmt.Errorf("unexpected JSON token %v", token)
	}
}

// asyncSubmittedResult is the structured shape emitted when an asynchronous
// write was submitted without --wait, so scripted callers get a stable JSON
// document instead of the human guidance text.
type asyncSubmittedResult struct {
	Submitted bool   `json:"submitted" yaml:"submitted"`
	Operation string `json:"operation" yaml:"operation"`
	Hint      string `json:"hint" yaml:"hint"`
	// OperationID names the execution recording the write's outcome, when
	// the platform answered one.
	OperationID string `json:"operation_id,omitempty" yaml:"operation_id,omitempty"`
}

func newAsyncSubmittedResult(operationLabel string) asyncSubmittedResult {
	return asyncSubmittedResult{
		Submitted: true,
		Operation: operationLabel,
		Hint:      "re-run with --wait to block until completion and see the full result",
	}
}

// newAsyncSubmittedOperationResult is newAsyncSubmittedResult for a write
// whose outcome the platform records on an operation: the hint names the
// command that reads it.
func newAsyncSubmittedOperationResult(operationLabel string, operationID string) asyncSubmittedResult {
	result := newAsyncSubmittedResult(operationLabel)
	if operationID != "" {
		result.OperationID = operationID
		result.Hint = "follow it with '" + followOperationCommand(operationID) + "'; it records whether the platform applied or refused the write"
	}
	return result
}

// followOperationCommand is the command that reads one operation.
func followOperationCommand(operationID string) string {
	return "ankra cluster operations list " + operationID
}
