package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// The metrics API is aggregated by metrics-server and is not part of the
// platform's Kubernetes cache: a measurement is only meaningful live. Its
// plurals also do not follow the kind (PodMetrics is served as "pods"), so
// the resource is stated explicitly instead of derived.
const (
	metricsAPIGroup   = "metrics.k8s.io"
	metricsAPIVersion = "v1beta1"
)

// metricsUnavailableHint is the guidance printed when the cluster answers
// with no metrics objects, which almost always means metrics-server is not
// installed rather than that nothing is running.
const metricsUnavailableHint = "No live metrics returned. metrics-server provides this API; " +
	"install it (or check that it is healthy) before using 'cluster top'. " +
	"For historical usage from Prometheus, use 'ankra cluster metrics'."

var clusterTopCmd = &cobra.Command{
	Use:   "top",
	Short: "Show live CPU and memory usage from the metrics API",
	Long: `Show live resource usage measured by metrics-server.

This is the "which container just got OOMKilled" view. It reads the
aggregated metrics API directly, so it works on clusters where Prometheus
was never installed. For trends over time use 'ankra cluster metrics',
which queries Prometheus.`,
	Annotations: map[string]string{"group": "kubernetes"},
}

var clusterTopPodsCmd = &cobra.Command{
	Use:   "pods",
	Short: "Show live CPU and memory usage per pod",
	Long: `Show live CPU and memory usage per pod, measured by metrics-server.

Without -n or --all-namespaces this reads every namespace, unlike kubectl
top, which defaults to the current one.

Examples:
  ankra cluster top pods -n default
  ankra cluster top pods --all-namespaces --sort-by memory
  ankra cluster top pods -n default --containers
  ankra cluster top pods --all-namespaces -o json`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{"group": "kubernetes"},
	RunE:        runTopPods,
}

var clusterTopNodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "Show live CPU and memory usage per node",
	Long: `Show live CPU and memory usage per node, measured by metrics-server.

Percentages are computed against each node's allocatable capacity. If the
node list cannot be read the percentage columns are omitted rather than
guessed.

Examples:
  ankra cluster top nodes
  ankra cluster top nodes --sort-by cpu
  ankra cluster top nodes -o json`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{"group": "kubernetes"},
	RunE:        runTopNodes,
}

func runTopPods(cmd *cobra.Command, _ []string) error {
	outputFormat, _ := cmd.Flags().GetString("output")
	if err := validateK8sOutputFormat(outputFormat); err != nil {
		return err
	}
	sortBy, _ := cmd.Flags().GetString("sort-by")
	if err := validateTopSortBy(sortBy); err != nil {
		return err
	}
	namespace, _ := cmd.Flags().GetString("namespace")
	allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
	showContainers, _ := cmd.Flags().GetBool("containers")
	if allNamespaces {
		namespace = ""
	}

	cluster, clusterError := resolveActiveCluster(cmd)
	if clusterError != nil {
		return clusterError
	}
	items, fetchError := fetchLiveMetrics(cluster.ID, "PodMetrics", "pods", namespace)
	if fetchError != nil {
		return fetchError
	}
	if handled, structuredError := renderStructuredMetrics(items, outputFormat); handled {
		return structuredError
	}
	if len(items) == 0 {
		fmt.Println(metricsUnavailableHint)
		return nil
	}

	if showContainers {
		renderContainerUsageTable(items, sortBy)
		return nil
	}
	renderPodUsageTable(items, sortBy)
	return nil
}

func runTopNodes(cmd *cobra.Command, _ []string) error {
	outputFormat, _ := cmd.Flags().GetString("output")
	if err := validateK8sOutputFormat(outputFormat); err != nil {
		return err
	}
	sortBy, _ := cmd.Flags().GetString("sort-by")
	if err := validateTopSortBy(sortBy); err != nil {
		return err
	}

	cluster, clusterError := resolveActiveCluster(cmd)
	if clusterError != nil {
		return clusterError
	}
	items, fetchError := fetchLiveMetrics(cluster.ID, "NodeMetrics", "nodes", "")
	if fetchError != nil {
		return fetchError
	}
	if handled, structuredError := renderStructuredMetrics(items, outputFormat); handled {
		return structuredError
	}
	if len(items) == 0 {
		fmt.Println(metricsUnavailableHint)
		return nil
	}
	renderNodeUsageTable(items, nodeAllocatable(cluster.ID), sortBy)
	return nil
}

func validateTopSortBy(sortBy string) error {
	switch sortBy {
	case "", "name", "cpu", "memory":
		return nil
	default:
		return withExitCode(exitUsage, fmt.Errorf(
			"unsupported --sort-by %q: use name, cpu or memory", sortBy))
	}
}

// renderStructuredMetrics writes the -o json|yaml form. handled reports
// that the command is finished.
func renderStructuredMetrics(items []map[string]interface{}, outputFormat string) (bool, error) {
	switch outputFormat {
	case "json":
		encoded, marshalError := json.MarshalIndent(items, "", "  ")
		if marshalError != nil {
			return true, fmt.Errorf("marshalling to JSON: %w", marshalError)
		}
		fmt.Println(string(encoded))
		return true, nil
	case "yaml":
		encoded, marshalError := yaml.Marshal(items)
		if marshalError != nil {
			return true, fmt.Errorf("marshalling to YAML: %w", marshalError)
		}
		fmt.Print(string(encoded))
		return true, nil
	}
	return false, nil
}

// fetchLiveMetrics reads the aggregated metrics API. skip_cache keeps the
// platform from answering out of its Kubernetes cache, which is keyed by
// plural and would hand back plain Pods or Nodes for these requests; a
// cached answer that arrives anyway (the platform degrades to stale cache
// when a cluster is offline) is refused rather than rendered as a live
// measurement.
func fetchLiveMetrics(clusterID string, kind string, resource string, namespace string) ([]map[string]interface{}, error) {
	request := client.ResourceRequestItem{
		Kind:     kind,
		Group:    metricsAPIGroup,
		Version:  metricsAPIVersion,
		Resource: resource,
	}
	if namespace != "" {
		request.Namespace = namespace
	}
	response, requestError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{request},
		SkipCache:        true,
	})
	if requestError != nil {
		return nil, requestError
	}
	if len(response.ResourceResponses) == 0 {
		return []map[string]interface{}{}, nil
	}
	responseItem := response.ResourceResponses[0]
	// Fix 5: an errored response carries no items, which would otherwise be
	// diagnosed as "metrics-server is not installed" and exit zero. The
	// platform reports the agent's message here.
	if responseItem.Status != "" && responseItem.Status != "success" {
		return nil, fmt.Errorf(
			"the cluster could not answer the metrics read (status %q): check that metrics-server is healthy and that the agent can reach the aggregated API",
			responseItem.Status)
	}
	// Deliberately the pointer, not CacheMetadata.ServedFromCache: the
	// platform attaches cache metadata on three paths, and the sandbox one
	// sets ServedFromCache false while still returning synthesised objects.
	// Gating on the flag would let a sandbox cluster's fabricated pods render
	// as live measurements.
	if responseItem.CacheMetadata != nil {
		if responseItem.CacheMetadata.SyncStatus == "sandbox" {
			return nil, fmt.Errorf(
				"live metrics are not available on a sandbox cluster: import a real cluster to read the metrics API")
		}
		return nil, fmt.Errorf(
			"live metrics unavailable: the platform answered from its cached inventory (%s). "+
				"Check that the cluster is online and its agent is connected",
			responseItem.CacheMetadata.SyncStatus)
	}
	measurements := make([]map[string]interface{}, 0, len(responseItem.Items))
	for _, item := range responseItem.Items {
		measurement, isMap := item.(map[string]interface{})
		if !isMap {
			continue
		}
		// A cached Pod and a PodMetrics both carry metadata.name; only the
		// measurement carries usage, so that is the discriminator.
		if !hasUsageMeasurement(measurement) {
			continue
		}
		measurements = append(measurements, measurement)
	}
	return measurements, nil
}

// hasUsageMeasurement distinguishes a metrics object from a plain Pod or
// Node, which the platform's cache would return for the same request. A
// NodeMetrics carries usage directly; a PodMetrics carries it per container,
// and a containers list whose entries have no usage block is not a
// measurement however much it looks like one.
func hasUsageMeasurement(measurement map[string]interface{}) bool {
	if _, hasUsage := getNestedMap(measurement, "usage"); hasUsage {
		return true
	}
	containers, isList := measurement["containers"].([]interface{})
	if !isList || len(containers) == 0 {
		return false
	}
	for _, rawContainer := range containers {
		container, isMap := rawContainer.(map[string]interface{})
		if !isMap {
			return false
		}
		if _, hasUsage := getNestedMap(container, "usage"); !hasUsage {
			return false
		}
	}
	return true
}

// podUsage totals a PodMetrics across its containers. isMeasured is false if
// any container's usage failed to parse: a partial total would under-report
// the pod while looking like a real measurement.
func podUsage(measurement map[string]interface{}) (int64, int64, bool) {
	containers, isList := measurement["containers"].([]interface{})
	if !isList || len(containers) == 0 {
		return 0, 0, false
	}
	var cpuNanocores int64
	var memoryBytes int64
	for _, rawContainer := range containers {
		container, isMap := rawContainer.(map[string]interface{})
		if !isMap {
			return 0, 0, false
		}
		containerCPU, containerMemory, isContainerMeasured := usageQuantities(container)
		if !isContainerMeasured {
			return 0, 0, false
		}
		cpuNanocores += containerCPU
		memoryBytes += containerMemory
	}
	return cpuNanocores, memoryBytes, true
}

// usageQuantities reads the usage block of a metrics object, returning CPU
// in nanocores and memory in bytes. isMeasured is false when either quantity
// is missing or unparseable, so the caller renders "-" rather than a
// confident zero that is indistinguishable from an idle container.
func usageQuantities(object map[string]interface{}) (int64, int64, bool) {
	usage, hasUsage := getNestedMap(object, "usage")
	if !hasUsage {
		return 0, 0, false
	}
	cpuCores, isCPUParsed := parseKubernetesQuantity(fmt.Sprintf("%v", usage["cpu"]))
	memoryBytes, isMemoryParsed := parseKubernetesQuantity(fmt.Sprintf("%v", usage["memory"]))
	if !isCPUParsed || !isMemoryParsed {
		return 0, 0, false
	}
	// CPU quantities are cores; nanocores keeps the millicore rendering
	// exact for the "123456789n" values metrics-server emits. Round rather
	// than truncate so "100u" is 100000n, not 99999n.
	return int64(math.Round(cpuCores * 1e9)), int64(math.Round(memoryBytes)), true
}

type usageRow struct {
	name         string
	namespace    string
	container    string
	cpuNanocores int64
	memoryBytes  int64
	isMeasured   bool
	cpuCapacity  int64
	memCapacity  int64
	hasCapacity  bool
}

// formatUsage renders a measurement, or "-" when the cluster did not give one
// that parses. A zero would read as an idle container.
func formatUsage(value int64, isMeasured bool, render func(int64) string) string {
	if !isMeasured {
		return "-"
	}
	return render(value)
}

func sortUsageRows(rows []usageRow, sortBy string) {
	switch sortBy {
	case "cpu":
		sort.SliceStable(rows, func(first int, second int) bool {
			return rows[first].cpuNanocores > rows[second].cpuNanocores
		})
	case "memory":
		sort.SliceStable(rows, func(first int, second int) bool {
			return rows[first].memoryBytes > rows[second].memoryBytes
		})
	default:
		sort.SliceStable(rows, func(first int, second int) bool {
			if rows[first].namespace != rows[second].namespace {
				return rows[first].namespace < rows[second].namespace
			}
			if rows[first].name != rows[second].name {
				return rows[first].name < rows[second].name
			}
			return rows[first].container < rows[second].container
		})
	}
}

func renderPodUsageTable(items []map[string]interface{}, sortBy string) {
	rows := make([]usageRow, 0, len(items))
	for _, measurement := range items {
		cpuNanocores, memoryBytes, isMeasured := podUsage(measurement)
		rows = append(rows, usageRow{
			name:         getNestedString(measurement, "metadata", "name"),
			namespace:    getNestedString(measurement, "metadata", "namespace"),
			cpuNanocores: cpuNanocores,
			memoryBytes:  memoryBytes,
			isMeasured:   isMeasured,
		})
	}
	sortUsageRows(rows, sortBy)

	usageTable := table.NewWriter()
	usageTable.SetOutputMirror(os.Stdout)
	usageTable.SetStyle(table.StyleRounded)
	usageTable.AppendHeader(table.Row{"Name", "Namespace", "CPU", "Memory"})
	for _, row := range rows {
		usageTable.AppendRow(table.Row{
			row.name, row.namespace,
			formatUsage(row.cpuNanocores, row.isMeasured, formatMillicores),
			formatUsage(row.memoryBytes, row.isMeasured, formatMebibytes),
		})
	}
	usageTable.Render()
}

func renderContainerUsageTable(items []map[string]interface{}, sortBy string) {
	rows := []usageRow{}
	for _, measurement := range items {
		containers, isList := measurement["containers"].([]interface{})
		if !isList {
			continue
		}
		for _, rawContainer := range containers {
			container, isMap := rawContainer.(map[string]interface{})
			if !isMap {
				continue
			}
			cpuNanocores, memoryBytes, isMeasured := usageQuantities(container)
			rows = append(rows, usageRow{
				name:         getNestedString(measurement, "metadata", "name"),
				namespace:    getNestedString(measurement, "metadata", "namespace"),
				container:    getNestedString(container, "name"),
				cpuNanocores: cpuNanocores,
				memoryBytes:  memoryBytes,
				isMeasured:   isMeasured,
			})
		}
	}
	sortUsageRows(rows, sortBy)

	usageTable := table.NewWriter()
	usageTable.SetOutputMirror(os.Stdout)
	usageTable.SetStyle(table.StyleRounded)
	usageTable.AppendHeader(table.Row{"Pod", "Namespace", "Container", "CPU", "Memory"})
	for _, row := range rows {
		usageTable.AppendRow(table.Row{
			row.name, row.namespace, row.container,
			formatUsage(row.cpuNanocores, row.isMeasured, formatMillicores),
			formatUsage(row.memoryBytes, row.isMeasured, formatMebibytes),
		})
	}
	usageTable.Render()
}

func renderNodeUsageTable(items []map[string]interface{}, capacity map[string]nodeCapacity, sortBy string) {
	rows := make([]usageRow, 0, len(items))
	for _, measurement := range items {
		cpuNanocores, memoryBytes, isMeasured := usageQuantities(measurement)
		name := getNestedString(measurement, "metadata", "name")
		row := usageRow{
			name: name, cpuNanocores: cpuNanocores,
			memoryBytes: memoryBytes, isMeasured: isMeasured,
		}
		if allocatable, isKnown := capacity[name]; isKnown {
			row.cpuCapacity = allocatable.cpuNanocores
			row.memCapacity = allocatable.memoryBytes
			row.hasCapacity = true
		}
		rows = append(rows, row)
	}
	sortUsageRows(rows, sortBy)

	usageTable := table.NewWriter()
	usageTable.SetOutputMirror(os.Stdout)
	usageTable.SetStyle(table.StyleRounded)
	usageTable.AppendHeader(table.Row{"Name", "CPU", "CPU %", "Memory", "Memory %"})
	for _, row := range rows {
		usageTable.AppendRow(table.Row{
			row.name,
			formatUsage(row.cpuNanocores, row.isMeasured, formatMillicores),
			formatUsagePercent(row.cpuNanocores, row.cpuCapacity, row.hasCapacity && row.isMeasured),
			formatUsage(row.memoryBytes, row.isMeasured, formatMebibytes),
			formatUsagePercent(row.memoryBytes, row.memCapacity, row.hasCapacity && row.isMeasured),
		})
	}
	usageTable.Render()
}

type nodeCapacity struct {
	cpuNanocores int64
	memoryBytes  int64
}

// nodeAllocatable reads each node's allocatable capacity so the node view
// can show percentages. A failure here is not fatal: the percentage columns
// render as "-" rather than the command failing over a decoration.
//
// Nodes are one of the kinds the platform keeps in its resource cache, so
// this read has to ask for a live answer explicitly - otherwise a percentage
// would pair a live measurement with a cached capacity, which is the kind of
// half-fresh number that reads as authoritative and is not. A cached answer
// arriving anyway drops the capacities, and the column renders "-".
func nodeAllocatable(clusterID string) map[string]nodeCapacity {
	capacities := map[string]nodeCapacity{}
	response, requestError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{{Kind: "Node", Version: "v1"}},
		SkipCache:        true,
	})
	if requestError != nil || len(response.ResourceResponses) == 0 {
		return capacities
	}
	if response.ResourceResponses[0].CacheMetadata != nil {
		return capacities
	}
	for _, item := range response.ResourceResponses[0].Items {
		node, isMap := item.(map[string]interface{})
		if !isMap {
			continue
		}
		allocatable, hasAllocatable := getNestedMap(node, "status")
		if !hasAllocatable {
			continue
		}
		quantities, isMap := allocatable["allocatable"].(map[string]interface{})
		if !isMap {
			continue
		}
		cpuCores, cpuOK := parseKubernetesQuantity(fmt.Sprintf("%v", quantities["cpu"]))
		memoryBytes, memoryOK := parseKubernetesQuantity(fmt.Sprintf("%v", quantities["memory"]))
		if !cpuOK || !memoryOK {
			continue
		}
		capacities[getNestedString(node, "metadata", "name")] = nodeCapacity{
			cpuNanocores: int64(math.Round(cpuCores * 1e9)),
			memoryBytes:  int64(math.Round(memoryBytes)),
		}
	}
	return capacities
}

func formatMillicores(cpuNanocores int64) string {
	return fmt.Sprintf("%dm", cpuNanocores/1_000_000)
}

func formatMebibytes(memoryBytes int64) string {
	return fmt.Sprintf("%dMi", memoryBytes/(1024*1024))
}

func formatUsagePercent(used int64, capacity int64, hasCapacity bool) string {
	if !hasCapacity || capacity <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d%%", used*100/capacity)
}

// parseKubernetesQuantity converts the resource.Quantity spellings the
// metrics and node APIs emit into a plain number - cores for CPU, bytes for
// memory. It covers the decimal SI and binary suffixes plus the "n"/"u"/"m"
// sub-unit suffixes metrics-server uses; anything else reports false rather
// than silently reading as zero.
func parseKubernetesQuantity(raw string) (float64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || value == "<nil>" {
		return 0, false
	}
	binarySuffixes := map[string]float64{
		"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30,
		"Ti": 1 << 40, "Pi": 1 << 50, "Ei": 1 << 60,
	}
	for suffix, multiplier := range binarySuffixes {
		if strings.HasSuffix(value, suffix) {
			return parseQuantityNumber(strings.TrimSuffix(value, suffix), multiplier)
		}
	}
	decimalSuffixes := map[string]float64{
		"n": 1e-9, "u": 1e-6, "m": 1e-3,
		"k": 1e3, "M": 1e6, "G": 1e9,
		"T": 1e12, "P": 1e15, "E": 1e18,
	}
	lastCharacter := value[len(value)-1:]
	if multiplier, isSuffix := decimalSuffixes[lastCharacter]; isSuffix {
		return parseQuantityNumber(value[:len(value)-1], multiplier)
	}
	return parseQuantityNumber(value, 1)
}

func parseQuantityNumber(digits string, multiplier float64) (float64, bool) {
	// ParseFloat rather than Sscanf: Sscanf("%g") stops at the first
	// character it cannot use and reports success, so "12abc" would read as
	// 12 and a malformed quantity would render as a plausible measurement.
	parsed, parseError := strconv.ParseFloat(strings.TrimSpace(digits), 64)
	if parseError != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
		return 0, false
	}
	return parsed * multiplier, true
}

func init() {
	clusterTopPodsCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace")
	clusterTopPodsCmd.Flags().BoolP("all-namespaces", "A", false, "Show pods across all namespaces")
	clusterTopPodsCmd.Flags().Bool("containers", false, "Break usage down per container")
	clusterTopPodsCmd.Flags().String("sort-by", "name", "Sort by: name, cpu or memory")
	clusterTopPodsCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")

	clusterTopNodesCmd.Flags().String("sort-by", "name", "Sort by: name, cpu or memory")
	clusterTopNodesCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")

	clusterTopCmd.AddCommand(clusterTopPodsCmd)
	clusterTopCmd.AddCommand(clusterTopNodesCmd)
	clusterCmd.AddCommand(clusterTopCmd)
}
