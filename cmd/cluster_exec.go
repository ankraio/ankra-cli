package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// podExecOutputLimitBytes bounds how much of each stream -o json holds in
// memory; the text output streams and is not bounded.
const podExecOutputLimitBytes = 8 << 20

var clusterExecCmd = &cobra.Command{
	Use:   "exec <pod> -- <command> [args...]",
	Short: "Run one command in a pod's container and exit with its exit code",
	Long: `Run a single command in a container of the named pod, through the platform
- no kubeconfig and no port-forward - and exit with the command's own exit
code, so a script or CI job can branch on it.

Everything after -- is the command and its arguments, passed to the
container exactly as given: no shell re-parses it, so quote once for your
local shell and not again. To use shell features (pipes, globs, variables)
run a shell explicitly: -- sh -c 'echo "$HOSTNAME" | tr a-z A-Z'.

stdout and stderr stream to your stdout and stderr as the command runs. The
command gets no TTY and, unless --stdin is given, no input; with --stdin your
input is forwarded and the command sees end-of-file when it ends.

Like "ankra cluster terminal", every run is recorded by the platform and
linked from the cluster's audit log as an open_pod_terminal event carrying
the command; "ankra org terminal-session" replays it. Needs kubernetes.exec
on the cluster, and a cluster agent recent enough to run one-off commands.

Exit codes: the command's own exit code when it ran; otherwise the CLI's
usual codes (6 for a refused credential, 7 for a missing kubernetes.exec,
1 when the command could not be started).

With -o json nothing is streamed; one document is printed when the command
ends: {"exit_code", "stdout", "stderr", "stdout_truncated",
"stderr_truncated"}, each stream capped at 8 MiB.`,
	Example: `  ankra cluster exec cnpg-cluster-1 -n cnpg-database -c postgres -- psql -U postgres -d appdb -Atc 'select count(*) from users'
  ankra cluster exec api-6d8f9c7b5-x2kq9 -n payments -- cat /etc/app/config.yaml
  ankra cluster exec api-6d8f9c7b5-x2kq9 -n payments -o json -- curl -sf localhost:8080/healthz
  ankra cluster exec cnpg-cluster-1 -n cnpg-database -c postgres --stdin -- psql -U postgres -d appdb < query.sql`,
	Args: cobra.MinimumNArgs(2),
	RunE: runClusterExec,
}

// podExecResult is the -o json document.
type podExecResult struct {
	ExitCode        int    `json:"exit_code" yaml:"exit_code"`
	Stdout          string `json:"stdout" yaml:"stdout"`
	Stderr          string `json:"stderr" yaml:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated" yaml:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated" yaml:"stderr_truncated"`
}

// boundedBuffer keeps the first limit bytes written to it and records
// whether anything past that was dropped.
type boundedBuffer struct {
	buffer      bytes.Buffer
	limit       int
	isTruncated bool
}

func (bounded *boundedBuffer) Write(data []byte) (int, error) {
	remaining := bounded.limit - bounded.buffer.Len()
	if remaining <= 0 {
		bounded.isTruncated = len(data) > 0 || bounded.isTruncated
		return len(data), nil
	}
	if len(data) > remaining {
		bounded.buffer.Write(data[:remaining])
		bounded.isTruncated = true
		return len(data), nil
	}
	bounded.buffer.Write(data)
	return len(data), nil
}

func runClusterExec(cmd *cobra.Command, args []string) error {
	if cmd.ArgsLenAtDash() != 1 {
		return withExitCode(exitUsage, errors.New(
			"put the command after --: ankra cluster exec <pod> -n <namespace> -- <command> [args...]"))
	}
	podName := args[0]
	commandArguments := args[1:]
	namespace, _ := cmd.Flags().GetString("namespace")
	container, _ := cmd.Flags().GetString("container")
	shouldForwardStdin, _ := cmd.Flags().GetBool("stdin")
	format, formatError := structuredFormatFromFlags(cmd)
	if formatError != nil {
		return formatError
	}
	if namespace == "" {
		return withExitCode(exitUsage, errors.New("--namespace (-n) is required"))
	}
	if commandArguments[0] == "" {
		return withExitCode(exitUsage, errors.New("the command after -- must not be empty"))
	}
	cluster, resolveError := resolveActiveCluster(cmd)
	if resolveError != nil {
		return resolveError
	}
	if container == "" {
		defaultContainer, containerError := defaultPodContainer(cluster.ID, namespace, podName)
		if containerError != nil {
			return containerError
		}
		container = defaultContainer
	}
	return runPodExec(cmd, cluster.ID, client.PodTerminalRequest{
		Namespace:     namespace,
		PodName:       podName,
		ContainerName: container,
		Command:       commandArguments,
		Stdin:         shouldForwardStdin,
	}, format)
}

// runPodExec runs the command over the platform relay and turns its exit
// code into the CLI's. A non-zero remote exit is not an error message: the
// command already said what it had to on stderr, so the CLI exits with the
// same code and prints nothing of its own.
func runPodExec(cmd *cobra.Command, clusterID string, request client.PodTerminalRequest, format outputFormat) error {
	isStructured := format != outputDefault
	stdoutBuffer := &boundedBuffer{limit: podExecOutputLimitBytes}
	stderrBuffer := &boundedBuffer{limit: podExecOutputLimitBytes}
	stdoutWriter := cmd.OutOrStdout()
	stderrWriter := cmd.ErrOrStderr()
	if isStructured {
		stdoutWriter, stderrWriter = stdoutBuffer, stderrBuffer
	}

	parentContext := cmd.Context()
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := context.WithCancel(parentContext)
	defer cancel()

	session, openError := apiClient.OpenPodTerminal(ctx, clusterID, request)
	if openError != nil {
		return openError
	}
	defer func() { _ = session.Close() }()

	if request.Stdin {
		go forwardExecInput(ctx, cmd.InOrStdin(), session)
	}

	var exitCode *int
	var errorMessages []string
	for frame := range session.Frames() {
		switch frame.Type {
		case "stdout", "stderr":
			payload, decodeError := frame.Payload()
			if decodeError != nil {
				continue
			}
			if frame.Type == "stdout" {
				_, _ = stdoutWriter.Write(payload)
			} else {
				_, _ = stderrWriter.Write(payload)
			}
		case "error":
			errorMessages = append(errorMessages, frame.Message)
		case "exit":
			exitCode = frame.Code
		}
	}
	cancel()

	closeError := session.Err()
	var closed *client.PodTerminalClosedError
	if errors.As(closeError, &closed) && closed.IsAuthentication() {
		return withExitCode(exitAuth, closeError)
	}
	if closeError != nil {
		return closeError
	}
	if exitCode == nil {
		if len(errorMessages) > 0 {
			return errors.New(strings.Join(errorMessages, "; "))
		}
		return errors.New("the command ended without reporting an exit code")
	}

	if isStructured {
		result := podExecResult{
			ExitCode:        *exitCode,
			Stdout:          stdoutBuffer.buffer.String(),
			Stderr:          stderrBuffer.buffer.String(),
			StdoutTruncated: stdoutBuffer.isTruncated,
			StderrTruncated: stderrBuffer.isTruncated,
		}
		if encodeError := encodeStructured(cmd.OutOrStdout(), format, result); encodeError != nil {
			return encodeError
		}
	}
	if *exitCode != 0 {
		cmd.SilenceErrors = true
		return withExitCode(*exitCode, fmt.Errorf("the command exited with code %d", *exitCode))
	}
	return nil
}

// forwardExecInput sends the local input to the command and, once it ends,
// tells the command so it sees end-of-file.
func forwardExecInput(ctx context.Context, input io.Reader, session client.PodTerminal) {
	buffer := make([]byte, 32*1024)
	for {
		count, readError := input.Read(buffer)
		if count > 0 {
			if sendError := session.SendInput(buffer[:count]); sendError != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		if readError != nil {
			_ = session.CloseStdin()
			return
		}
	}
}

func init() {
	clusterExecCmd.Flags().StringP("namespace", "n", "", "Namespace of the pod (required)")
	clusterExecCmd.Flags().StringP("container", "c", "", "Container to run the command in (default: the pod's only container)")
	clusterExecCmd.Flags().Bool("stdin", false, "Forward your stdin to the command; it sees end-of-file when your input ends")
	registerStructuredOutputFlags(clusterExecCmd)
	clusterCmd.AddCommand(clusterExecCmd)
}
