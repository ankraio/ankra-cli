package cmd

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// ankra org terminal-session: a recorded pod-terminal session, read behind
// audit.read. The session id comes from an open_pod_terminal audit row
// (details.terminal_session_id).

const terminalTranscriptPageLimit = 500

var orgTerminalSessionCmd = &cobra.Command{
	Use:   "terminal-session <session_id>",
	Short: "Read a recorded pod-terminal session from the audit log",
	Long: `Every pod terminal session opened through Ankra is recorded. The
open_pod_terminal audit row carries the session id in its details; this
command prints the session's facts and, with --transcript, replays the
recorded output as text (everything the container printed, in order).
--show-input lists what was typed as well, with control characters named.

Needs the audit.read permission. A recording contains whatever the shell
printed, secrets included.

Examples:
  ankra org terminal-session 11111111-2222-4333-8444-555555555555
  ankra org terminal-session 11111111-2222-4333-8444-555555555555 --transcript
  ankra org terminal-session 11111111-2222-4333-8444-555555555555 --transcript -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		withTranscript, _ := cmd.Flags().GetBool("transcript")
		showInput, _ := cmd.Flags().GetBool("show-input")

		session, err := apiClient.GetTerminalSession(args[0])
		if err != nil {
			return err
		}
		var chunks []client.TerminalTranscriptChunk
		if withTranscript || showInput {
			chunks, err = collectTerminalTranscript(args[0])
			if err != nil {
				return err
			}
		}
		if handled, renderError := renderStructured(cmd, map[string]any{
			"session":    session,
			"transcript": chunks,
		}); handled || renderError != nil {
			return renderError
		}

		out := cmd.OutOrStdout()
		renderTerminalSessionFacts(out, session)
		if withTranscript {
			_, _ = fmt.Fprintln(out, "\n--- recorded output ---")
			for _, chunk := range chunks {
				if chunk.Direction != "output" {
					continue
				}
				decoded, decodeError := base64.StdEncoding.DecodeString(chunk.Data)
				if decodeError != nil {
					continue
				}
				_, _ = out.Write(decoded)
			}
			_, _ = fmt.Fprintln(out, "\n--- end of recording ---")
		}
		if showInput {
			_, _ = fmt.Fprintln(out, "\n--- typed input ---")
			_, _ = fmt.Fprint(out, describeTypedInput(chunks))
			_, _ = fmt.Fprintln(out, "--- end of input ---")
		}
		return nil
	},
}

func collectTerminalTranscript(sessionID string) ([]client.TerminalTranscriptChunk, error) {
	chunks := []client.TerminalTranscriptChunk{}
	afterSequence := 0
	for {
		page, err := apiClient.GetTerminalTranscript(sessionID, afterSequence, terminalTranscriptPageLimit)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, page.Chunks...)
		if !page.HasMore || len(page.Chunks) == 0 {
			return chunks, nil
		}
		afterSequence = page.NextAfter
	}
}

func renderTerminalSessionFacts(out io.Writer, session *client.TerminalSession) {
	ended := "still open"
	if session.EndedAt != nil {
		ended = *session.EndedAt
		if session.EndReason != nil {
			ended += " (" + *session.EndReason + ")"
		}
	}
	flags := []string{}
	if session.IsTruncated {
		flags = append(flags, "truncated")
	}
	if session.RecordingDegraded {
		flags = append(flags, "recording degraded")
	}
	_, _ = fmt.Fprintf(out, "Session:    %s\n", session.ID)
	_, _ = fmt.Fprintf(out, "User:       %s\n", session.UserEmail)
	_, _ = fmt.Fprintf(out, "Container:  %s/%s (%s), shell %s\n", session.Namespace, session.PodName, session.ContainerName, session.Shell)
	_, _ = fmt.Fprintf(out, "Cluster:    %s\n", session.ClusterID)
	_, _ = fmt.Fprintf(out, "Started:    %s\n", session.StartedAt)
	_, _ = fmt.Fprintf(out, "Ended:      %s\n", ended)
	_, _ = fmt.Fprintf(out, "Recorded:   %d bytes", session.RecordedBytes)
	if len(flags) > 0 {
		_, _ = fmt.Fprintf(out, " [%s]", strings.Join(flags, ", "))
	}
	_, _ = fmt.Fprintln(out)
}

// describeTypedInput renders the input chunks as text with control
// characters named, so an edited command reads differently from a typed one.
func describeTypedInput(chunks []client.TerminalTranscriptChunk) string {
	var described strings.Builder
	for _, chunk := range chunks {
		if chunk.Direction != "input" {
			continue
		}
		decoded, decodeError := base64.StdEncoding.DecodeString(chunk.Data)
		if decodeError != nil {
			continue
		}
		for _, character := range string(decoded) {
			switch {
			case character == '\r' || character == '\n':
				described.WriteString("⏎\n")
			case character == 0x7f:
				described.WriteString("⌫")
			case character < 0x20:
				described.WriteString("^" + string(rune(character+0x40)))
			default:
				described.WriteRune(character)
			}
		}
	}
	return described.String()
}

func init() {
	orgTerminalSessionCmd.Flags().Bool("transcript", false, "Print the recorded output")
	orgTerminalSessionCmd.Flags().Bool("show-input", false, "List what was typed, with control characters named")
	registerStructuredOutputFlags(orgTerminalSessionCmd)
	orgCmd.AddCommand(orgTerminalSessionCmd)
}
