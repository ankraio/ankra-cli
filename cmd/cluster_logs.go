package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"ankra/internal/client"

	"gopkg.in/yaml.v3"
)

// maxConcurrentLogFollows bounds how many streams a single `cluster logs`
// invocation holds open at once. Each target is one long-lived HTTP stream
// on the platform and one follow on the agent, so an unbounded selector
// match would open a connection per replica. kubectl draws the same line at
// five; beyond it the CLI asks the caller to narrow the selector rather
// than quietly truncating the match.
const maxConcurrentLogFollows = 5

// logTarget is one pod/container pair to read.
type logTarget struct {
	podName       string
	containerName string
}

// podLogGroup is the structured form of one target's output.
type podLogGroup struct {
	Pod       string   `json:"pod" yaml:"pod"`
	Container string   `json:"container,omitempty" yaml:"container,omitempty"`
	Lines     []string `json:"lines" yaml:"lines"`
}

// resolveLogTargets turns the command's pod argument or label selector into
// the concrete pod/container pairs to read. allContainers expands each pod
// to every container it declares, including init and ephemeral containers,
// which is where a failing init container's log lives.
func resolveLogTargets(
	clusterID string,
	namespace string,
	podName string,
	labelSelector string,
	containerName string,
	allContainers bool,
) ([]logTarget, error) {
	if podName != "" && !allContainers {
		return []logTarget{{podName: podName, containerName: containerName}}, nil
	}

	request := client.ResourceRequestItem{Kind: "Pod", Version: "v1", Namespace: namespace}
	if podName != "" {
		request.Name = podName
	}
	if labelSelector != "" {
		request.LabelSelector = labelSelector
	}
	response, requestError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{request},
	})
	if requestError != nil {
		return nil, requestError
	}
	if len(response.ResourceResponses) == 0 || len(response.ResourceResponses[0].Items) == 0 {
		if podName != "" {
			return nil, withExitCode(exitNotFound, fmt.Errorf(
				"pod %q not found in namespace %s", podName, namespace))
		}
		return nil, withExitCode(exitNotFound, fmt.Errorf(
			"no pods match selector %q in namespace %s", labelSelector, namespace))
	}

	targets := []logTarget{}
	for _, item := range response.ResourceResponses[0].Items {
		pod, isPod := item.(map[string]interface{})
		if !isPod {
			continue
		}
		name := getNestedString(pod, "metadata", "name")
		if name == "" {
			continue
		}
		if !allContainers {
			targets = append(targets, logTarget{podName: name, containerName: containerName})
			continue
		}
		for _, container := range podContainerNames(pod) {
			targets = append(targets, logTarget{podName: name, containerName: container})
		}
	}
	sort.SliceStable(targets, func(first int, second int) bool {
		if targets[first].podName != targets[second].podName {
			return targets[first].podName < targets[second].podName
		}
		return targets[first].containerName < targets[second].containerName
	})
	if len(targets) == 0 {
		return nil, withExitCode(exitNotFound, fmt.Errorf(
			"no readable containers found for the selected pods in namespace %s", namespace))
	}
	return targets, nil
}

// podContainerNames lists every container of a pod in the order a reader
// wants them: init containers first (they run first and are the usual
// cause of a stuck pod), then the app containers, then any ephemeral
// debug containers.
func podContainerNames(pod map[string]interface{}) []string {
	names := []string{}
	spec, hasSpec := getNestedMap(pod, "spec")
	if !hasSpec {
		return names
	}
	for _, key := range []string{"initContainers", "containers", "ephemeralContainers"} {
		containers, isList := spec[key].([]interface{})
		if !isList {
			continue
		}
		for _, rawContainer := range containers {
			container, isMap := rawContainer.(map[string]interface{})
			if !isMap {
				continue
			}
			if name := getNestedString(container, "name"); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// streamLogTargets reads every target. A single target streams straight to
// the writer; several targets are prefixed so the interleaved output stays
// attributable, and are read concurrently when following so a quiet replica
// does not block a noisy one.
func streamLogTargets(
	ctx context.Context,
	clusterID string,
	targets []logTarget,
	options client.PodLogOptions,
	showPrefix bool,
	writer io.Writer,
) error {
	if len(targets) == 1 && !showPrefix {
		options.PodName = targets[0].podName
		options.ContainerName = targets[0].containerName
		return apiClient.StreamPodLogs(ctx, clusterID, options, writer)
	}

	var outputLock sync.Mutex
	streamOne := func(target logTarget) error {
		targetOptions := options
		targetOptions.PodName = target.podName
		targetOptions.ContainerName = target.containerName
		prefixed := &prefixingWriter{
			prefix: logTargetPrefix(target),
			target: writer,
			lock:   &outputLock,
		}
		streamError := apiClient.StreamPodLogs(ctx, clusterID, targetOptions, prefixed)
		if flushError := prefixed.Flush(); flushError != nil && streamError == nil {
			streamError = flushError
		}
		if streamError != nil {
			return fmt.Errorf("%s: %w", logTargetPrefix(target), streamError)
		}
		return nil
	}

	if !options.Follow {
		for _, target := range targets {
			if streamError := streamOne(target); streamError != nil {
				return streamError
			}
		}
		return nil
	}

	var waitGroup sync.WaitGroup
	streamErrors := make([]error, len(targets))
	for index, target := range targets {
		waitGroup.Add(1)
		go func(slot int, streamTarget logTarget) {
			defer waitGroup.Done()
			streamErrors[slot] = streamOne(streamTarget)
		}(index, target)
	}
	waitGroup.Wait()
	for _, streamError := range streamErrors {
		if streamError != nil {
			return streamError
		}
	}
	return nil
}

func logTargetPrefix(target logTarget) string {
	if target.containerName == "" {
		return target.podName
	}
	return target.podName + "/" + target.containerName
}

// prefixingWriter stamps every complete line with its target and serialises
// writes across concurrent streams so two replicas cannot interleave inside
// a single line.
type prefixingWriter struct {
	prefix    string
	target    io.Writer
	lock      *sync.Mutex
	remainder []byte
}

func (writer *prefixingWriter) Write(payload []byte) (int, error) {
	writer.lock.Lock()
	defer writer.lock.Unlock()
	consumed := len(payload)
	buffer := append(writer.remainder, payload...)
	for {
		newlineIndex := bytes.IndexByte(buffer, '\n')
		if newlineIndex < 0 {
			break
		}
		line := buffer[:newlineIndex]
		buffer = buffer[newlineIndex+1:]
		if _, writeError := fmt.Fprintf(writer.target, "[%s] %s\n", writer.prefix, line); writeError != nil {
			return consumed, writeError
		}
	}
	writer.remainder = append([]byte(nil), buffer...)
	return consumed, nil
}

// Flush emits a trailing partial line, which a stream that ended without a
// newline would otherwise drop.
func (writer *prefixingWriter) Flush() error {
	writer.lock.Lock()
	defer writer.lock.Unlock()
	if len(writer.remainder) == 0 {
		return nil
	}
	_, writeError := fmt.Fprintf(writer.target, "[%s] %s\n", writer.prefix, writer.remainder)
	writer.remainder = nil
	return writeError
}

// collectLogTargets reads every target into memory for the -o json|yaml
// forms. It is only reachable on a bounded read, so the whole output is
// known to terminate.
func collectLogTargets(
	ctx context.Context,
	clusterID string,
	targets []logTarget,
	options client.PodLogOptions,
) ([]podLogGroup, error) {
	groups := make([]podLogGroup, 0, len(targets))
	for _, target := range targets {
		targetOptions := options
		targetOptions.PodName = target.podName
		targetOptions.ContainerName = target.containerName
		var buffer bytes.Buffer
		if streamError := apiClient.StreamPodLogs(ctx, clusterID, targetOptions, &buffer); streamError != nil {
			return nil, fmt.Errorf("%s: %w", logTargetPrefix(target), streamError)
		}
		groups = append(groups, podLogGroup{
			Pod:       target.podName,
			Container: target.containerName,
			Lines:     splitLogLines(buffer.String()),
		})
	}
	return groups, nil
}

func splitLogLines(output string) []string {
	trimmed := strings.TrimSuffix(output, "\n")
	if trimmed == "" {
		return []string{}
	}
	return strings.Split(trimmed, "\n")
}

func renderLogGroups(groups []podLogGroup, outputFormat string) error {
	switch outputFormat {
	case "json":
		encoded, marshalError := json.MarshalIndent(groups, "", "  ")
		if marshalError != nil {
			return fmt.Errorf("marshalling to JSON: %w", marshalError)
		}
		fmt.Println(string(encoded))
		return nil
	default:
		encoded, marshalError := yaml.Marshal(groups)
		if marshalError != nil {
			return fmt.Errorf("marshalling to YAML: %w", marshalError)
		}
		fmt.Print(string(encoded))
		return nil
	}
}
