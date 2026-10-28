package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
)

func newStructuredOutputTestCommand() *cobra.Command {
	command := &cobra.Command{Use: "test", Run: func(*cobra.Command, []string) {}}
	registerStructuredOutputFlags(command)
	return command
}

func TestStructuredFormatFromFlags(t *testing.T) {
	cases := map[string]struct {
		outputFlag string
		want       outputFormat
		wantErr    bool
	}{
		"default":              {want: outputDefault},
		"output json":          {outputFlag: "json", want: outputJSON},
		"output yaml":          {outputFlag: "yaml", want: outputYAML},
		"output yml":           {outputFlag: "yml", want: outputYAML},
		"invalid output value": {outputFlag: "xml", wantErr: true},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			command := newStructuredOutputTestCommand()
			if testCase.outputFlag != "" {
				if err := command.Flags().Set("output", testCase.outputFlag); err != nil {
					t.Fatalf("set --output: %v", err)
				}
			}
			got, err := structuredFormatFromFlags(command)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected error, got format %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("structuredFormatFromFlags: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("got %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestStructuredFormatFromFlagsRejectsWithUsageExit asserts an invalid -o value
// is tagged exitUsage so scripts see exit 2 rather than the generic 1.
func TestStructuredFormatFromFlagsRejectsWithUsageExit(t *testing.T) {
	command := newStructuredOutputTestCommand()
	if err := command.Flags().Set("output", "xml"); err != nil {
		t.Fatalf("set --output: %v", err)
	}
	_, err := structuredFormatFromFlags(command)
	if err == nil {
		t.Fatal("expected an error for an invalid -o value")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("invalid -o value should classify as exitUsage (%d), got %d", exitUsage, got)
	}
}

// TestInvalidOutputOnStructuredCommandExitsUsage drives a real structured
// command (org list) end-to-end with a bad -o value and asserts exit 2.
func TestInvalidOutputOnStructuredCommandExitsUsage(t *testing.T) {
	setMockClient(t, &orgListMock{organisations: nil})
	_, err := executeCommand("org", "list", "-o", "xml")
	if err == nil {
		t.Fatal("expected an error for an invalid -o value")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("invalid -o on a structured command should exit %d, got %d", exitUsage, got)
	}
}

func TestStructuredFormatFromFlagsWithoutFlags(t *testing.T) {
	command := &cobra.Command{Use: "bare", Run: func(*cobra.Command, []string) {}}
	got, err := structuredFormatFromFlags(command)
	if err != nil {
		t.Fatalf("structuredFormatFromFlags: %v", err)
	}
	if got != outputDefault {
		t.Fatalf("got %q, want outputDefault", got)
	}
}

func TestRenderStructuredJSON(t *testing.T) {
	command := newStructuredOutputTestCommand()
	if err := command.Flags().Set("output", "json"); err != nil {
		t.Fatalf("set --output: %v", err)
	}
	buf := new(bytes.Buffer)
	command.SetOut(buf)

	value := map[string]string{"cluster": "production"}
	rendered, err := renderStructured(command, value)
	if err != nil {
		t.Fatalf("renderStructured: %v", err)
	}
	if !rendered {
		t.Fatal("expected structured output to be rendered")
	}
	var decoded map[string]string
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if decoded["cluster"] != "production" {
		t.Fatalf("unexpected decoded payload: %#v", decoded)
	}
}

func TestRenderStructuredDefaultIsNoop(t *testing.T) {
	command := newStructuredOutputTestCommand()
	buf := new(bytes.Buffer)
	command.SetOut(buf)

	rendered, err := renderStructured(command, map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("renderStructured: %v", err)
	}
	if rendered {
		t.Fatal("expected no structured output for default format")
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no output, got %q", buf.String())
	}
}

// TestNoCommandHasJSONFlag walks the full command tree and asserts that no
// command defines a --json flag: the CLI convention is a single -o/--output
// flag with json|yaml values.
func TestNoCommandHasJSONFlag(t *testing.T) {
	var walk func(command *cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Flags().Lookup("json") != nil {
			t.Errorf("command %q defines a --json flag; use -o/--output json|yaml instead", command.CommandPath())
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)
}
