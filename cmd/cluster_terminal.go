package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"ankra/internal/client"
)

const (
	podTerminalPingInterval   = 30 * time.Second
	podTerminalResizeInterval = 250 * time.Millisecond
	podTerminalDefaultShell   = "/bin/sh"
	podTerminalDefaultCols    = 80
	podTerminalDefaultRows    = 24
)

var clusterTerminalCmd = &cobra.Command{
	Use:   "terminal <pod>",
	Short: "Open an interactive shell in a pod's container",
	Long: `Open an interactive shell in a container of the named pod, through the
platform - no kubeconfig and no port-forward. The local terminal is put in
raw mode, so every keystroke (Ctrl-C included) reaches the remote shell;
leave with exit or Ctrl-D. Window resizes follow the local terminal.

Every session is recorded by the platform and linked from the cluster's
audit log as an open_pod_terminal event; "ankra org terminal-session"
replays it. Needs kubernetes.exec on the cluster.

With one container the shell opens there; a pod with several needs
--container. Input that is not a terminal (a pipe) is forwarded as typed
and should end with exit, since the remote shell cannot see the pipe close.`,
	Example: `  ankra cluster terminal api-6d8f9c7b5-x2kq9 -n payments
  ankra cluster terminal api-6d8f9c7b5-x2kq9 -n payments -c sidecar --shell /bin/bash`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace, _ := cmd.Flags().GetString("namespace")
		container, _ := cmd.Flags().GetString("container")
		shell, _ := cmd.Flags().GetString("shell")
		if namespace == "" {
			return withExitCode(exitUsage, errors.New("--namespace (-n) is required"))
		}
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		if container == "" {
			container, err = defaultPodContainer(cluster.ID, namespace, args[0])
			if err != nil {
				return err
			}
		}
		return runPodTerminal(cmd, cluster.ID, client.PodTerminalRequest{
			Namespace:     namespace,
			PodName:       args[0],
			ContainerName: container,
			Shell:         shell,
		})
	},
}

// defaultPodContainer picks the container a terminal opens in when the
// operator named none: the pod's only container. Several containers is a
// choice the operator has to make, and the refusal lists them.
func defaultPodContainer(clusterID string, namespace string, podName string) (string, error) {
	response, requestError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{{Kind: "Pod", Version: "v1", Namespace: namespace, Name: podName}},
	})
	if requestError != nil {
		return "", requestError
	}
	if len(response.ResourceResponses) == 0 || len(response.ResourceResponses[0].Items) == 0 {
		return "", withExitCode(exitNotFound, fmt.Errorf("pod %q not found in namespace %q", podName, namespace))
	}
	pod, isObject := response.ResourceResponses[0].Items[0].(map[string]interface{})
	if !isObject {
		return "", fmt.Errorf("unexpected pod payload for %q", podName)
	}
	names := terminalContainerNames(pod)
	switch len(names) {
	case 0:
		return "", fmt.Errorf("pod %q reports no containers", podName)
	case 1:
		return names[0], nil
	default:
		return "", withExitCode(exitUsage, fmt.Errorf("pod %q has several containers (%s); pick one with --container",
			podName, strings.Join(names, ", ")))
	}
}

// terminalContainerNames lists the pod's regular containers - the ones a
// shell can run in. Init and ephemeral containers are left out on purpose:
// they are what podContainerNames (logs) enumerates, and a pod with one app
// container and one init container still has exactly one place to open a
// shell.
func terminalContainerNames(pod map[string]interface{}) []string {
	names := []string{}
	spec, hasSpec := getNestedMap(pod, "spec")
	if !hasSpec {
		return names
	}
	containers, isList := spec["containers"].([]interface{})
	if !isList {
		return names
	}
	for _, rawContainer := range containers {
		container, isMap := rawContainer.(map[string]interface{})
		if !isMap {
			continue
		}
		if name, hasName := container["name"].(string); hasName && name != "" {
			names = append(names, name)
		}
	}
	return names
}

// runPodTerminal bridges the local terminal and the platform relay until the
// remote shell exits or the relay ends the session. A refused credential
// exits 6; the relay's other refusals arrive as the client's typed errors
// and keep their usual exit codes.
func runPodTerminal(cmd *cobra.Command, clusterID string, request client.PodTerminalRequest) error {
	input := cmd.InOrStdin()
	output := cmd.OutOrStdout()
	errorOutput := cmd.ErrOrStderr()
	if request.Shell == "" {
		request.Shell = podTerminalDefaultShell
	}

	inputFile, isInputFile := input.(*os.File)
	isInteractive := isInputFile && term.IsTerminal(int(inputFile.Fd()))
	request.Cols, request.Rows = podTerminalDefaultCols, podTerminalDefaultRows
	sizeDescriptor := -1
	if outputFile, isOutputFile := output.(*os.File); isOutputFile && term.IsTerminal(int(outputFile.Fd())) {
		sizeDescriptor = int(outputFile.Fd())
		if cols, rows, sizeError := term.GetSize(sizeDescriptor); sizeError == nil && cols > 0 && rows > 0 {
			request.Cols, request.Rows = cols, rows
		}
	}

	_, _ = fmt.Fprintf(errorOutput, "Opening %s in %s/%s - the session is recorded to the audit log; leave with exit.\n",
		request.Shell, request.PodName, request.ContainerName)

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

	if isInteractive {
		previousState, rawError := term.MakeRaw(int(inputFile.Fd()))
		if rawError != nil {
			return fmt.Errorf("putting the terminal in raw mode: %w", rawError)
		}
		defer func() { _ = term.Restore(int(inputFile.Fd()), previousState) }()
	}

	go forwardTerminalInput(ctx, input, session)
	go keepTerminalAlive(ctx, session, sizeDescriptor, request.Cols, request.Rows)

	for frame := range session.Frames() {
		switch frame.Type {
		case "stdout", "stderr":
			payload, decodeError := frame.Payload()
			if decodeError != nil {
				continue
			}
			_, _ = output.Write(payload)
		case "error":
			_, _ = fmt.Fprintf(errorOutput, "\r\n%s\r\n", frame.Message)
		}
	}
	cancel()

	closeError := session.Err()
	var closed *client.PodTerminalClosedError
	if errors.As(closeError, &closed) && closed.IsAuthentication() {
		return withExitCode(exitAuth, closeError)
	}
	return closeError
}

// forwardTerminalInput copies what is typed (or piped) to the remote shell
// until the input ends or the session does.
func forwardTerminalInput(ctx context.Context, input io.Reader, session client.PodTerminal) {
	buffer := make([]byte, 4096)
	for {
		count, readError := input.Read(buffer)
		if count > 0 {
			if sendError := session.SendInput(buffer[:count]); sendError != nil {
				return
			}
		}
		if readError != nil || ctx.Err() != nil {
			return
		}
	}
}

// keepTerminalAlive pings the relay on the portal's cadence and follows the
// local window size, sending a resize only when it changed.
func keepTerminalAlive(ctx context.Context, session client.PodTerminal, sizeDescriptor int, cols int, rows int) {
	pingTicker := time.NewTicker(podTerminalPingInterval)
	defer pingTicker.Stop()
	resizeTicker := time.NewTicker(podTerminalResizeInterval)
	defer resizeTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			if pingError := session.Ping(); pingError != nil {
				return
			}
		case <-resizeTicker.C:
			if sizeDescriptor < 0 {
				continue
			}
			newCols, newRows, sizeError := term.GetSize(sizeDescriptor)
			if sizeError != nil || (newCols == cols && newRows == rows) {
				continue
			}
			cols, rows = newCols, newRows
			if resizeError := session.Resize(cols, rows); resizeError != nil {
				return
			}
		}
	}
}

func init() {
	clusterTerminalCmd.Flags().StringP("namespace", "n", "", "Namespace of the pod (required)")
	clusterTerminalCmd.Flags().StringP("container", "c", "", "Container to open the shell in (default: the pod's only container)")
	clusterTerminalCmd.Flags().String("shell", podTerminalDefaultShell, "Shell to start in the container")
	clusterCmd.AddCommand(clusterTerminalCmd)
}
