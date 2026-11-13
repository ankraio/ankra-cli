package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func readSecretThroughStdinFlag(t *testing.T, input string, isTerminal bool) (string, error) {
	t.Helper()
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return isTerminal }
	t.Cleanup(func() { promptIsInteractive = previous })
	command := &cobra.Command{Use: "secret-stdin-test"}
	command.Flags().Bool("secret-stdin", false, "")
	if setError := command.Flags().Set("secret-stdin", "true"); setError != nil {
		t.Fatalf("setting --secret-stdin: %v", setError)
	}
	command.SetIn(strings.NewReader(input))
	return readSecretFromStdinOrPrompt(command, "secret-stdin", "token")
}

func TestReadSecretFromStdinKeepsEveryByteButOneTrailingNewline(t *testing.T) {
	pem := "-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----\n"
	for _, testCase := range []struct {
		name  string
		input string
		want  string
	}{
		{"no newline", "s3cret", "s3cret"},
		{"one LF from a pipe", "s3cret\n", "s3cret"},
		{"one CRLF from a Windows pipe", "s3cret\r\n", "s3cret"},
		{"a deliberate trailing newline survives the pipe's", "s3cret\n\n", "s3cret\n"},
		{"a second CRLF survives", "s3cret\r\n\r\n", "s3cret\r\n"},
		{"a PEM key keeps its final newline", pem + "\n", pem},
		{"a lone trailing CR is part of the secret", "s3cret\r", "s3cret\r"},
		{"inner and edge whitespace is kept", "  s3cret token\t\n", "  s3cret token\t"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			secret, readError := readSecretThroughStdinFlag(t, testCase.input, false)
			if readError != nil {
				t.Fatalf("read failed: %v", readError)
			}
			if secret != testCase.want {
				t.Fatalf("stored %q, want %q", secret, testCase.want)
			}
		})
	}
}

func TestReadSecretFromStdinRefusesWhatItCannotStoreUnaltered(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		input      string
		isTerminal bool
		mentions   string
	}{
		{"a terminal would echo the secret", "s3cret\n", true, "stdin is a terminal"},
		{"nothing piped", "", false, "no token was read"},
		{"only the pipe's newline", "\n", false, "no token was read"},
		{"only whitespace", " \t\r\n\n", false, "no token was read"},
		{"invalid UTF-8 would be replaced in the JSON body", "s3cr\xffet\n", false, "not valid UTF-8"},
		{"over the limit would be cut short", strings.Repeat("x", secretStdinLimit+1), false, "larger than"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			secret, readError := readSecretThroughStdinFlag(t, testCase.input, testCase.isTerminal)
			if readError == nil || exitCodeFor(readError) != exitUsage || !strings.Contains(readError.Error(), testCase.mentions) {
				t.Fatalf("want a usage error mentioning %q, got %q, %v", testCase.mentions, secret, readError)
			}
			if strings.Contains(readError.Error(), "s3cr") {
				t.Fatalf("the refusal must not print the secret back: %v", readError)
			}
		})
	}
}
