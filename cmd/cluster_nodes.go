package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type clusterNodesOps struct {
	provider string
	list     func(clusterID string) (*client.NodeListResult, error)
	get      func(clusterID, nodeID string) (*client.NodeDetail, error)
	restart  func(clusterID, nodeID string) (*client.RestartNodeResult, error)
	// cloudInitLog is nil for providers whose node-remediation SSH lane is
	// not wired (Proxmox, Morpheus); the subcommand is only registered when
	// it is set.
	cloudInitLog func(clusterID, nodeID string) (*client.NodeCloudInitLogResult, error)
	// diagnose rides the same node SSH lane as cloudInitLog, so it is set
	// for exactly the same providers.
	diagnose func(ctx context.Context, clusterID, nodeID string) (*client.NodeDiagnoseResult, error)
	// consoleLog reads the VM serial console from the provider's API rather
	// than over SSH. It is set only for providers the platform implements it
	// for (OVH), and its subcommand is registered only for them.
	consoleLog func(ctx context.Context, clusterID, nodeID string) (*client.NodeConsoleLogResult, error)
}

// defaultNodeConsoleLogTimeout bounds the client's wait for a console-log
// read. The platform waits up to a minute for the provider before handing
// back the operation to poll, so the client budget sits past that.
const defaultNodeConsoleLogTimeout = 2 * time.Minute

// defaultNodeDiagnoseTimeout bounds the client's wait for a node diagnosis.
// The platform waits up to two minutes for the job before handing back the
// operation to poll, so the client budget sits past that.
const defaultNodeDiagnoseTimeout = 3 * time.Minute

// nodeDiagnoser binds the provider-generic client call to one provider's
// route segment.
func nodeDiagnoser(provider string) func(ctx context.Context, clusterID, nodeID string) (*client.NodeDiagnoseResult, error) {
	return func(ctx context.Context, clusterID, nodeID string) (*client.NodeDiagnoseResult, error) {
		return apiClient.DiagnoseClusterNode(ctx, provider, clusterID, nodeID)
	}
}

func hetznerNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "hetzner",
		list:         apiClient.ListHetznerClusterNodes,
		get:          apiClient.GetHetznerClusterNode,
		restart:      apiClient.RestartHetznerClusterNode,
		cloudInitLog: apiClient.HetznerNodeCloudInitLog,
		diagnose:     nodeDiagnoser("hetzner"),
	}
}

func ovhNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "ovh",
		list:         apiClient.ListOvhClusterNodes,
		get:          apiClient.GetOvhClusterNode,
		restart:      apiClient.RestartOvhClusterNode,
		cloudInitLog: apiClient.OvhNodeCloudInitLog,
		diagnose:     nodeDiagnoser("ovh"),
		consoleLog:   apiClient.OvhNodeConsoleLog,
	}
}

func upcloudNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "upcloud",
		list:         apiClient.ListUpcloudClusterNodes,
		get:          apiClient.GetUpcloudClusterNode,
		restart:      apiClient.RestartUpcloudClusterNode,
		cloudInitLog: apiClient.UpcloudNodeCloudInitLog,
		diagnose:     nodeDiagnoser("upcloud"),
	}
}

func digitaloceanNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "digitalocean",
		list:         apiClient.ListDigitaloceanClusterNodes,
		get:          apiClient.GetDigitaloceanClusterNode,
		restart:      apiClient.RestartDigitaloceanClusterNode,
		cloudInitLog: apiClient.DigitaloceanNodeCloudInitLog,
		diagnose:     nodeDiagnoser("digitalocean"),
	}
}

func scalewayNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "scaleway",
		list:         apiClient.ListScalewayClusterNodes,
		get:          apiClient.GetScalewayClusterNode,
		restart:      apiClient.RestartScalewayClusterNode,
		cloudInitLog: apiClient.ScalewayNodeCloudInitLog,
		diagnose:     nodeDiagnoser("scaleway"),
	}
}

func ankraCloudNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "ankracloud",
		list:         apiClient.ListAnkraCloudClusterNodes,
		get:          apiClient.GetAnkraCloudClusterNode,
		restart:      apiClient.RestartAnkraCloudClusterNode,
		cloudInitLog: apiClient.AnkraCloudNodeCloudInitLog,
		diagnose:     nodeDiagnoser("ankracloud"),
	}
}

func awsNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "aws",
		list:         apiClient.ListAwsClusterNodes,
		get:          apiClient.GetAwsClusterNode,
		restart:      apiClient.RestartAwsClusterNode,
		cloudInitLog: apiClient.AwsNodeCloudInitLog,
		diagnose:     nodeDiagnoser("aws"),
	}
}

func proxmoxNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider: "proxmox",
		list:     apiClient.ListProxmoxClusterNodes,
		get:      apiClient.GetProxmoxClusterNode,
		restart:  apiClient.RestartProxmoxClusterNode,
	}
}

func morpheusNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider: "morpheus",
		list:     apiClient.ListMorpheusClusterNodes,
		get:      apiClient.GetMorpheusClusterNode,
	}
}

func runNodesList(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID string) error {
	ops := opsFn()
	result, err := ops.list(clusterID)
	if err != nil {
		return err
	}

	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if handled {
		return nil
	}

	if len(result.Nodes) == 0 {
		fmt.Println("No nodes found.")
		return nil
	}

	fmt.Printf("%-36s  %-22s  %-13s  %-14s  %-16s  %-12s  %-15s  %-15s\n",
		"ID", "NAME", "ROLE", "NODE_GROUP", "INSTANCE_TYPE", "STATE", "PRIVATE_IP", "PROVIDER_STATUS")
	for _, n := range result.Nodes {
		state := n.State
		if n.IsDeleted {
			state = state + " (soft-deleted)"
		}
		fmt.Printf("%-36s  %-22s  %-13s  %-14s  %-16s  %-12s  %-15s  %-15s\n",
			n.ID,
			truncate(n.Name, 22),
			truncate(stringValue(n.Role), 13),
			truncate(stringValue(n.NodeGroup), 14),
			truncate(n.InstanceType, 16),
			truncate(state, 12),
			truncate(stringValue(n.PrivateIP), 15),
			truncate(providerStatusDisplay(n.ProviderStatus, n.ProviderPowerState), 15),
		)
	}
	return nil
}

// providerStatusDisplay combines the cloud provider's live status and power
// state (e.g. OVH's ACTIVE/SHUTOFF plus its power state) into one column;
// "-" when the provider has no read job or none has run yet.
func providerStatusDisplay(status, powerState *string) string {
	switch {
	case status != nil && powerState != nil:
		return *status + "/" + *powerState
	case status != nil:
		return *status
	case powerState != nil:
		return *powerState
	default:
		return "-"
	}
}

func runNodesRestart(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID, nodeID string) error {
	ops := opsFn()
	result, err := ops.restart(clusterID, nodeID)
	if err != nil {
		return err
	}

	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if handled {
		return nil
	}

	fmt.Printf("Restart scheduled for node %s (operation %s, job %s).\n", result.NodeID, result.OperationID, result.JobName)
	fmt.Println("The node will reboot (or power-cycle); workloads on it are briefly unavailable until it comes back up.")
	fmt.Printf("Track progress with: ankra cluster operations list %s\n", result.OperationID)
	return nil
}

func runNodesCloudInitLog(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID, nodeID string) error {
	ops := opsFn()
	result, err := ops.cloudInitLog(clusterID, nodeID)
	if err != nil {
		return err
	}

	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if handled {
		return nil
	}

	if !result.Completed {
		fmt.Printf("Cloud-init log fetch dispatched (operation %s, status %s) - the SSH round trip is still running.\n",
			result.OperationID, result.Status)
		fmt.Printf("Re-run this command to attach to the in-flight fetch, or track it with: ankra cluster operations list %s\n",
			result.OperationID)
		return nil
	}
	if result.ErrorExcerpt != "" {
		fmt.Printf("Cloud-init log fetch failed: %s\n", result.ErrorExcerpt)
		return nil
	}
	if status, ok := result.Report["cloud_init_status"].(string); ok && status != "" {
		fmt.Println("cloud-init status:")
		fmt.Printf("  %s\n\n", strings.ReplaceAll(status, "\n", "\n  "))
	}
	if logTail, ok := result.Report["log_tail"].(string); ok {
		if truncated, _ := result.Report["truncated"].(bool); truncated {
			fmt.Println("cloud-init output log (tail, truncated to the last 64 KiB):")
		} else {
			fmt.Println("cloud-init output log:")
		}
		fmt.Println(logTail)
	}
	return nil
}

// runNodesConsoleLog reads the node's serial console and prints it: the log
// on stdout, the heading and the notes on what the answer leaves out on
// stderr, so the log pipes cleanly into grep or a file. --lines tails what
// the platform returned. A read that finished but failed exits non-zero after
// its output, like diagnose.
func runNodesConsoleLog(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID, nodeID string) error {
	lines, linesFlagError := cmd.Flags().GetInt("lines")
	if linesFlagError != nil {
		return fmt.Errorf("reading --lines: %w", linesFlagError)
	}
	if lines < 0 {
		return withExitCode(exitUsage, errors.New("--lines must be a positive number, or 0 for every line the platform returned"))
	}
	timeout, timeoutFlagError := cmd.Flags().GetDuration("timeout")
	if timeoutFlagError != nil {
		return fmt.Errorf("reading --timeout: %w", timeoutFlagError)
	}
	requestContext, cancelRequestContext := context.WithTimeout(context.Background(), timeout)
	defer cancelRequestContext()

	result, readError := opsFn().consoleLog(requestContext, clusterID, nodeID)
	if readError != nil {
		wrapped := fmt.Errorf("reading the node console log: %w", readError)
		if errors.Is(readError, context.DeadlineExceeded) {
			return withExitCode(exitWaitTimeout, wrapped)
		}
		return wrapped
	}
	tailConsoleLogReport(result, lines)

	if handled, renderError := renderStructured(cmd, result); renderError != nil {
		return renderError
	} else if !handled {
		printNodeConsoleLog(result)
	}
	return nodeConsoleLogFailure(result)
}

// tailConsoleLogReport keeps the newest lines of the report's console output
// when --lines asks for fewer than the platform returned, and records that it
// did (tailed_to_lines), so -o json|yaml carry the same answer the human
// output shows and say it was shortened here.
func tailConsoleLogReport(result *client.NodeConsoleLogResult, lines int) {
	if lines == 0 || !result.Completed || result.Report == nil {
		return
	}
	consoleOutput, isText := result.Report["console_output"].(string)
	if !isText {
		return
	}
	kept, isShortened := tailLines(consoleOutput, lines)
	if !isShortened {
		return
	}
	result.Report["console_output"] = kept
	result.Report["console_output_bytes"] = len(kept)
	result.Report["line_count"] = lines
	result.Report["tailed_to_lines"] = lines
}

// tailLines keeps the last count lines of text, a final newline kept as it
// was, and reports whether any line was dropped.
func tailLines(text string, count int) (string, bool) {
	trimmed := strings.TrimSuffix(text, "\n")
	if count <= 0 || trimmed == "" {
		return text, false
	}
	allLines := strings.Split(trimmed, "\n")
	if len(allLines) <= count {
		return text, false
	}
	kept := strings.Join(allLines[len(allLines)-count:], "\n")
	if strings.HasSuffix(text, "\n") {
		kept += "\n"
	}
	return kept, true
}

// printNodeConsoleLog renders a console-log read: the poll hint while it is
// still running, otherwise the heading and notes on stderr and the console
// text on stdout. A failed read prints nothing here; its error is the
// command's.
func printNodeConsoleLog(result *client.NodeConsoleLogResult) {
	if !result.Completed {
		verb := "dispatched"
		if result.Attached {
			verb = "attached to the read already in flight"
		}
		fmt.Printf("Console log read %s (operation %s, status %s); the provider read is still running.\n",
			verb, result.OperationID, result.Status)
		fmt.Printf("Re-run this command to attach to it, or track it with: ankra cluster operations list %s\n",
			result.OperationID)
		return
	}
	if nodeConsoleLogFailure(result) != nil {
		return
	}
	node, _ := result.Report["node"].(map[string]interface{})
	nodeName, _ := node["name"].(string)
	instanceID, _ := node["ovh_instance_id"].(string)
	region, _ := node["region"].(string)
	lineCount := reportInt(result.Report, "line_count")
	fmt.Fprintf(os.Stderr, "Console log of %s (instance %s, %s): the newest %d lines.\n",
		firstNonEmptyString(nodeName, result.NodeID), instanceID, region, lineCount)
	if limitReached, _ := result.Report["line_limit_reached"].(bool); limitReached {
		fmt.Fprintf(os.Stderr, "  Older console output may exist: the platform returns at most the newest %d lines.\n",
			reportInt(result.Report, "line_limit"))
	}
	if truncated, _ := result.Report["truncated"].(bool); truncated {
		fmt.Fprintln(os.Stderr, "  The platform's 96 KiB cap dropped the oldest of the lines it read.")
	}
	if redacted := reportInt(result.Report, "redacted_values"); redacted > 0 {
		fmt.Fprintf(os.Stderr, "  The platform replaced %d values that looked like secrets with [REDACTED:...] markers.\n", redacted)
	}
	if tailedTo := reportInt(result.Report, "tailed_to_lines"); tailedTo > 0 {
		fmt.Fprintf(os.Stderr, "  Shortened here to the newest %d (--lines).\n", tailedTo)
	}
	consoleOutput, _ := result.Report["console_output"].(string)
	if consoleOutput == "" {
		fmt.Fprintln(os.Stderr, "  The console holds no output.")
		return
	}
	fmt.Print(consoleOutput)
	if !strings.HasSuffix(consoleOutput, "\n") {
		fmt.Println()
	}
}

// nodeConsoleLogFailure is the error a console-log read that finished but did
// not succeed exits with: the provider refused the credential, the instance
// is gone, or its state has no console to read. A read still in flight is not
// a failure.
func nodeConsoleLogFailure(result *client.NodeConsoleLogResult) error {
	if !result.Completed {
		return nil
	}
	if result.ErrorExcerpt != "" {
		return fmt.Errorf("the console log read failed (operation %s): %s", result.OperationID, result.ErrorExcerpt)
	}
	if result.Status != "success" {
		return fmt.Errorf("the console log read ended %s without a report (operation %s)", result.Status, result.OperationID)
	}
	return nil
}

// reportInt reads a whole number out of a decoded JSON report, where numbers
// arrive as float64 (or int, when the CLI set them itself).
func reportInt(report map[string]interface{}, key string) int {
	switch value := report[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

// firstNonEmptyString returns the first argument that is not empty.
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// runNodesDiagnose dispatches the read-only node diagnosis and blocks for its
// report, like `bastion diagnose`: the endpoint has no submit-and-return
// half, so the command carries --timeout without a --wait twin, and a client
// budget that expires exits with exitWaitTimeout because the job itself keeps
// running.
func runNodesDiagnose(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID, nodeID string) error {
	ops := opsFn()
	timeout, timeoutFlagError := cmd.Flags().GetDuration("timeout")
	if timeoutFlagError != nil {
		return fmt.Errorf("reading --timeout: %w", timeoutFlagError)
	}
	requestContext, cancelRequestContext := context.WithTimeout(context.Background(), timeout)
	defer cancelRequestContext()

	result, diagnoseError := ops.diagnose(requestContext, clusterID, nodeID)
	if diagnoseError != nil {
		wrapped := fmt.Errorf("diagnosing node: %w", diagnoseError)
		if errors.Is(diagnoseError, context.DeadlineExceeded) {
			return withExitCode(exitWaitTimeout, wrapped)
		}
		return wrapped
	}

	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if !handled {
		if printError := printNodeDiagnosis(result); printError != nil {
			return printError
		}
	}
	return nodeDiagnosisFailure(result)
}

// nodeDiagnosisFailure is the error a diagnose run that finished but did
// not succeed exits with, after its output: the job ended failed, timed out
// or cancelled (the node unreachable over SSH, the snapshot failing), so a
// script must not read it as a diagnosis. A completed run is a success only
// when its step status says so; an error excerpt is not guaranteed on every
// other terminal status. -o json|yaml still print the result document first.
// A run still in flight is not a failure.
func nodeDiagnosisFailure(result *client.NodeDiagnoseResult) error {
	if !result.Completed {
		return nil
	}
	if result.ErrorExcerpt != "" {
		return fmt.Errorf("the diagnose job failed (operation %s): %s", result.OperationID, result.ErrorExcerpt)
	}
	if result.Status != "success" {
		return fmt.Errorf("the diagnose job ended %s without a report (operation %s)", result.Status, result.OperationID)
	}
	return nil
}

// printNodeDiagnosis renders the dispatched diagnosis: the operation handle
// always, then the report when the job finished inside the platform's wait
// budget and the poll hint when it did not.
func printNodeDiagnosis(result *client.NodeDiagnoseResult) error {
	verb := "dispatched"
	if result.Attached {
		verb = "attached to the run already in flight"
	}
	fmt.Printf("Node diagnosis %s (%s).\n", verb, result.JobName)
	fmt.Printf("  Operation ID: %s\n", result.OperationID)
	fmt.Printf("  Node ID:      %s\n", result.NodeID)
	fmt.Printf("  Status:       %s\n", result.Status)

	if !result.Completed {
		fmt.Println()
		fmt.Println("The diagnosis is still running. Poll it with:")
		fmt.Printf("  ankra cluster operations list %s\n", result.OperationID)
		return nil
	}
	if nodeDiagnosisFailure(result) != nil {
		// The failure itself is the command's error, printed on stderr once
		// runNodesDiagnose returns it.
		return nil
	}
	if len(result.Report) == 0 {
		fmt.Println()
		fmt.Println("The diagnose job completed without a report.")
		return nil
	}
	fmt.Println()
	fmt.Println("Report:")
	return printJSONBlock(result.Report)
}

func runNodesGet(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID, nodeID string) error {
	ops := opsFn()
	detail, err := ops.get(clusterID, nodeID)
	if err != nil {
		return err
	}

	if handled, err := renderStructured(cmd, detail); err != nil {
		return err
	} else if handled {
		return nil
	}

	fmt.Printf("Node: %s\n", detail.Name)
	fmt.Printf("  ID:           %s\n", detail.ID)
	fmt.Printf("  Kind:         %s\n", detail.Kind)
	fmt.Printf("  Role:         %s\n", stringValue(detail.Role))
	fmt.Printf("  Node group:   %s\n", stringValue(detail.NodeGroup))
	state := detail.State
	if detail.IsDeleted {
		state += " (soft-deleted)"
	}
	fmt.Printf("  State:        %s\n", state)
	fmt.Printf("  Created at:   %s\n", detail.CreatedAt)
	fmt.Printf("  Updated at:   %s\n", detail.UpdatedAt)

	fmt.Println()
	fmt.Println("Definition:")
	if err := printJSONBlock(detail.Definition); err != nil {
		return err
	}

	if len(detail.Info) > 0 {
		fmt.Println()
		fmt.Println("Provider info (latest):")
		if err := printJSONBlock(detail.Info); err != nil {
			return err
		}
	}
	if len(detail.Data) > 0 {
		fmt.Println()
		fmt.Println("Reconciler data (latest):")
		if err := printJSONBlock(detail.Data); err != nil {
			return err
		}
	}

	printEdges("Dependencies", detail.Dependencies)
	printEdges("Relationships", detail.Relationships)
	printEdges("Groups", detail.Groups)
	return nil
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func printJSONBlock(v interface{}) error {
	encoded, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Printf("  %s\n", string(encoded))
	return nil
}

func printEdges(title string, edges map[string][]string) {
	fmt.Println()
	fmt.Printf("%s:\n", title)
	if len(edges) == 0 {
		fmt.Println("  (none)")
		return
	}
	kinds := make([]string, 0, len(edges))
	for k := range edges {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		ids := edges[kind]
		fmt.Printf("  %s ×%d\n", kind, len(ids))
		for _, id := range ids {
			fmt.Printf("    - %s\n", id)
		}
	}
}

// newNodesCmd builds a provider's nodes group. supportsNodeSSH says the
// platform reaches this provider's nodes over its bastion SSH lane, which
// both cloud-init-log and diagnose ride. supportsConsoleLog says the platform
// reads this provider's VM serial console from its API.
func newNodesCmd(opsFn func() clusterNodesOps, provider string, supportsRestart bool, supportsNodeSSH bool, supportsConsoleLog bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: fmt.Sprintf("List and inspect %s cluster nodes", provider),
		Long: `Inspect every server Ankra manages for the cluster (control plane, workers,
and bastion or gateway). Soft-deleted entries from a stopped cluster are
included so the saved topology is visible before re-provisioning.`,
	}

	listCmd := &cobra.Command{
		Use:   "list <cluster_id|name>",
		Short: "List all nodes for the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			return runNodesList(cmd, opsFn, clusterID)
		},
	}

	getCmd := &cobra.Command{
		Use:   "get <cluster_id|name> <node_id>",
		Short: "Show full spec and metadata for a single node",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			return runNodesGet(cmd, opsFn, clusterID, args[1])
		},
	}

	registerStructuredOutputFlags(listCmd, getCmd)
	cmd.AddCommand(listCmd, getCmd)

	if supportsRestart {
		restartCmd := &cobra.Command{
			Use:   "restart <cluster_id|name> <node_id>",
			Short: "Restart a node (control plane, worker, or bastion/gateway)",
			Long: `Schedule a native reboot (falling back to a power cycle) of the node as a
tracked operation. The node must be in the 'up' state and have no restart
already in flight. Workloads on the node are briefly unavailable while it
reboots. Works for any node returned by 'nodes list', including the
bastion/gateway.`,
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				clusterID, resolveError := resolveClusterArg(args[0])
				if resolveError != nil {
					return resolveError
				}
				return runNodesRestart(cmd, opsFn, clusterID, args[1])
			},
		}
		registerStructuredOutputFlags(restartCmd)
		cmd.AddCommand(restartCmd)
	}

	if supportsNodeSSH {
		cloudInitLogCmd := &cobra.Command{
			Use:   "cloud-init-log <cluster_id|name> <node_id>",
			Short: "Read the node's cloud-init status and output-log tail",
			Long: `Fetch 'cloud-init status --long' and the tail of
/var/log/cloud-init-output.log from the node over the platform's bastion SSH
lane, as a tracked read-only operation. This is the debug surface for
node-group user-data: it shows whether the first-boot document ran and what
it printed, without needing the cluster's SSH key. Repeated calls attach to
an in-flight fetch instead of dispatching duplicates. Find node ids with
'nodes list'.`,
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				clusterID, resolveError := resolveClusterArg(args[0])
				if resolveError != nil {
					return resolveError
				}
				return runNodesCloudInitLog(cmd, opsFn, clusterID, args[1])
			},
		}
		registerStructuredOutputFlags(cloudInitLogCmd)
		cmd.AddCommand(cloudInitLogCmd)

		diagnoseCmd := &cobra.Command{
			Use:   "diagnose <cluster_id|name> <node_id>",
			Short: "Run a read-only SSH diagnosis of a node",
			Long: `Dispatch the platform's read-only node diagnose job and wait for its report:
over the bastion SSH lane it snapshots the node's OS-level state - the
Kubernetes service unit and its journal, disk, memory, uptime, the kernel log
and the firewall path to the API server - and names the symptoms it finds.
Nothing on the node is changed. This is what to reach for when a node is
NotReady or its kubelet stopped posting status, below what 'cluster get' can
see. The same diagnosis the Ankra assistant runs as ssh_diagnose_node.

The platform waits up to two minutes for the job; a slower run hands back the
operation id to poll with 'cluster operations list', and repeated calls attach
to the run already in flight. Find node ids with 'nodes list'. Needs the
clusters.operate permission on the cluster.`,
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				clusterID, resolveError := resolveClusterArg(args[0])
				if resolveError != nil {
					return resolveError
				}
				return runNodesDiagnose(cmd, opsFn, clusterID, args[1])
			},
		}
		diagnoseCmd.Flags().Duration("timeout", defaultNodeDiagnoseTimeout,
			"Maximum time to wait for the diagnosis report")
		registerStructuredOutputFlags(diagnoseCmd)
		cmd.AddCommand(diagnoseCmd)
	}

	if supportsConsoleLog {
		consoleLogCmd := &cobra.Command{
			Use:   "console-log <cluster_id|name> <node_id>",
			Short: "Read the newest lines of the node's VM serial console",
			Long: `Read the newest lines of the node's serial console from the cloud
provider's API, as a tracked read-only operation. It goes through neither the
node over SSH nor the in-cluster agent, so it answers for a node that hung: a
kernel soft lockup, hung-task warnings, an out-of-memory kill or a root
filesystem gone read-only is often the last thing the console printed.
Nothing on the node, the instance or the cluster's credentials changes.

The platform returns at most the newest 1000 lines (96 KiB); --lines shows
fewer. The log goes to stdout and the heading and notes to stderr, so the
output pipes cleanly into grep or a file. Repeated calls attach to a read in
flight or a recent one. Find node ids with 'nodes list'. Needs the
clusters.operate permission on the cluster.`,
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				clusterID, resolveError := resolveClusterArg(args[0])
				if resolveError != nil {
					return resolveError
				}
				return runNodesConsoleLog(cmd, opsFn, clusterID, args[1])
			},
		}
		consoleLogCmd.Flags().Int("lines", 0, "Show only the newest N lines (0 shows every line the platform returned)")
		consoleLogCmd.Flags().Duration("timeout", defaultNodeConsoleLogTimeout,
			"Maximum time to wait for the console read")
		registerStructuredOutputFlags(consoleLogCmd)
		cmd.AddCommand(consoleLogCmd)
	}
	return cmd
}

func init() {
	hetznerCmd.AddCommand(newNodesCmd(hetznerNodesOps, "Hetzner", true, true, false))
	ovhCmd.AddCommand(newNodesCmd(ovhNodesOps, "OVH", true, true, true))
	upcloudCmd.AddCommand(newNodesCmd(upcloudNodesOps, "UpCloud", true, true, false))
	digitaloceanCmd.AddCommand(newNodesCmd(digitaloceanNodesOps, "DigitalOcean", true, true, false))
	scalewayCmd.AddCommand(newNodesCmd(scalewayNodesOps, "Scaleway", true, true, false))
	awsCmd.AddCommand(newNodesCmd(awsNodesOps, "AWS", true, true, false))
	ankraCloudCmd.AddCommand(newNodesCmd(ankraCloudNodesOps, "Ankra Cloud", true, true, false))
	proxmoxCmd.AddCommand(newNodesCmd(proxmoxNodesOps, "Proxmox VE", true, false, false))
	morpheusCmd.AddCommand(newNodesCmd(morpheusNodesOps, "HPE Morpheus", false, false, false))
}
