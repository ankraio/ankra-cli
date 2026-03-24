package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func getNestedString(obj map[string]interface{}, keys ...string) string {
	current := obj
	for i, key := range keys {
		val, ok := current[key]
		if !ok {
			return ""
		}
		if i == len(keys)-1 {
			switch v := val.(type) {
			case string:
				return v
			case float64:
				if v == float64(int(v)) {
					return fmt.Sprintf("%d", int(v))
				}
				return fmt.Sprintf("%g", v)
			case bool:
				return fmt.Sprintf("%t", v)
			case nil:
				return ""
			default:
				return fmt.Sprintf("%v", v)
			}
		}
		nested, ok := val.(map[string]interface{})
		if !ok {
			return ""
		}
		current = nested
	}
	return ""
}

func getNestedMap(obj map[string]interface{}, key string) (map[string]interface{}, bool) {
	val, ok := obj[key]
	if !ok {
		return nil, false
	}
	m, ok := val.(map[string]interface{})
	return m, ok
}

func getNestedInt(obj map[string]interface{}, keys ...string) int {
	current := obj
	for i, key := range keys {
		val, ok := current[key]
		if !ok {
			return 0
		}
		if i == len(keys)-1 {
			switch v := val.(type) {
			case float64:
				return int(v)
			default:
				return 0
			}
		}
		nested, ok := val.(map[string]interface{})
		if !ok {
			return 0
		}
		current = nested
	}
	return 0
}

func formatK8sAge(timestamp string) string {
	if timestamp == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return timestamp
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

type kindConfig struct {
	commandName   string
	kind          string
	group         string
	version       string
	clusterScoped bool
	short         string
	headers       table.Row
	formatRow     func(obj map[string]interface{}) table.Row
}

var kindConfigs = []kindConfig{
	{
		commandName: "deployments", kind: "Deployment", group: "apps", version: "v1",
		short: "List deployments in the cluster",
		headers: table.Row{"Name", "Namespace", "Ready", "Up-to-date", "Available", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			readyCount := getNestedString(obj, "status", "readyReplicas")
			if readyCount == "" {
				readyCount = "0"
			}
			desired := getNestedString(obj, "spec", "replicas")
			if desired == "" {
				desired = "0"
			}
			updatedReplicas := getNestedString(obj, "status", "updatedReplicas")
			if updatedReplicas == "" {
				updatedReplicas = "0"
			}
			availableReplicas := getNestedString(obj, "status", "availableReplicas")
			if availableReplicas == "" {
				availableReplicas = "0"
			}
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "metadata", "namespace"),
				fmt.Sprintf("%s/%s", readyCount, desired),
				updatedReplicas,
				availableReplicas,
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "services", kind: "Service", group: "", version: "v1",
		short: "List services in the cluster",
		headers: table.Row{"Name", "Namespace", "Type", "Cluster-IP", "External-IP", "Ports", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			ports := ""
			if specMap, ok := getNestedMap(obj, "spec"); ok {
				if specPorts, ok := specMap["ports"]; ok {
					if portList, ok := specPorts.([]interface{}); ok {
						portStrs := make([]string, 0, len(portList))
						for _, p := range portList {
							if pm, ok := p.(map[string]interface{}); ok {
								portStr := fmt.Sprintf("%s/%s",
									getNestedString(pm, "port"),
									getNestedString(pm, "protocol"))
								portStrs = append(portStrs, portStr)
							}
						}
						ports = strings.Join(portStrs, ",")
					}
				}
			}
			externalIP := getNestedString(obj, "spec", "externalIP")
			if externalIP == "" {
				externalIP = "<none>"
			}
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "metadata", "namespace"),
				getNestedString(obj, "spec", "type"),
				getNestedString(obj, "spec", "clusterIP"),
				externalIP,
				ports,
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "nodes", kind: "Node", group: "", version: "v1", clusterScoped: true,
		short: "List nodes in the cluster",
		headers: table.Row{"Name", "Status", "Roles", "Version", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			status := "Unknown"
			if statusMap, ok := getNestedMap(obj, "status"); ok {
				if conditions, ok := statusMap["conditions"]; ok {
					if condList, ok := conditions.([]interface{}); ok {
						for _, c := range condList {
							if cm, ok := c.(map[string]interface{}); ok {
								if getNestedString(cm, "type") == "Ready" {
									if getNestedString(cm, "status") == "True" {
										status = text.FgGreen.Sprint("Ready")
									} else {
										status = text.FgRed.Sprint("NotReady")
									}
								}
							}
						}
					}
				}
			}
			roles := ""
			if metaMap, ok := getNestedMap(obj, "metadata"); ok {
				if labelMap, ok := metaMap["labels"].(map[string]interface{}); ok {
					roleList := []string{}
					for key := range labelMap {
						if strings.HasPrefix(key, "node-role.kubernetes.io/") {
							roleList = append(roleList, strings.TrimPrefix(key, "node-role.kubernetes.io/"))
						}
					}
					if len(roleList) > 0 {
						roles = strings.Join(roleList, ",")
					} else {
						roles = "<none>"
					}
				}
			}
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				status,
				roles,
				getNestedString(obj, "status", "nodeInfo", "kubeletVersion"),
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "namespaces", kind: "Namespace", group: "", version: "v1", clusterScoped: true,
		short: "List namespaces in the cluster",
		headers: table.Row{"Name", "Status", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "status", "phase"),
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "ingresses", kind: "Ingress", group: "networking.k8s.io", version: "v1",
		short: "List ingresses in the cluster",
		headers: table.Row{"Name", "Namespace", "Hosts", "Address", "Ports", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			hosts := ""
			if specMap, ok := getNestedMap(obj, "spec"); ok {
				if rules, ok := specMap["rules"]; ok {
					if ruleList, ok := rules.([]interface{}); ok {
						hostList := []string{}
						for _, r := range ruleList {
							if rm, ok := r.(map[string]interface{}); ok {
								if h := getNestedString(rm, "host"); h != "" {
									hostList = append(hostList, h)
								}
							}
						}
						hosts = strings.Join(hostList, ",")
					}
				}
			}
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "metadata", "namespace"),
				hosts, "", "",
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "events", kind: "Event", group: "", version: "v1",
		short: "List events in the cluster",
		headers: table.Row{"Last Seen", "Type", "Reason", "Object", "Message"},
		formatRow: func(obj map[string]interface{}) table.Row {
			lastSeen := getNestedString(obj, "lastTimestamp")
			if lastSeen == "" {
				lastSeen = getNestedString(obj, "metadata", "creationTimestamp")
			}
			object := fmt.Sprintf("%s/%s",
				strings.ToLower(getNestedString(obj, "involvedObject", "kind")),
				getNestedString(obj, "involvedObject", "name"))
			return table.Row{
				formatK8sAge(lastSeen),
				getNestedString(obj, "type"),
				getNestedString(obj, "reason"),
				object,
				getNestedString(obj, "message"),
			}
		},
	},
	{
		commandName: "configmaps", kind: "ConfigMap", group: "", version: "v1",
		short: "List configmaps in the cluster",
		headers: table.Row{"Name", "Namespace", "Data", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			dataCount := 0
			if data, ok := obj["data"]; ok {
				if dm, ok := data.(map[string]interface{}); ok {
					dataCount = len(dm)
				}
			}
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "metadata", "namespace"),
				dataCount,
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "secrets", kind: "Secret", group: "", version: "v1",
		short: "List secrets in the cluster",
		headers: table.Row{"Name", "Namespace", "Type", "Data", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			dataCount := 0
			if data, ok := obj["data"]; ok {
				if dm, ok := data.(map[string]interface{}); ok {
					dataCount = len(dm)
				}
			}
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "metadata", "namespace"),
				getNestedString(obj, "type"),
				dataCount,
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "statefulsets", kind: "StatefulSet", group: "apps", version: "v1",
		short: "List statefulsets in the cluster",
		headers: table.Row{"Name", "Namespace", "Ready", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			readyCount := getNestedString(obj, "status", "readyReplicas")
			if readyCount == "" {
				readyCount = "0"
			}
			desired := getNestedString(obj, "spec", "replicas")
			if desired == "" {
				desired = "0"
			}
			ready := fmt.Sprintf("%s/%s", readyCount, desired)
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "metadata", "namespace"),
				ready,
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "daemonsets", kind: "DaemonSet", group: "apps", version: "v1",
		short: "List daemonsets in the cluster",
		headers: table.Row{"Name", "Namespace", "Desired", "Current", "Ready", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "metadata", "namespace"),
				getNestedString(obj, "status", "desiredNumberScheduled"),
				getNestedString(obj, "status", "currentNumberScheduled"),
				getNestedString(obj, "status", "numberReady"),
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "k8s-jobs", kind: "Job", group: "batch", version: "v1",
		short: "List Kubernetes jobs in the cluster",
		headers: table.Row{"Name", "Namespace", "Completions", "Duration", "Age"},
		formatRow: func(obj map[string]interface{}) table.Row {
			completions := fmt.Sprintf("%s/%s",
				getNestedString(obj, "status", "succeeded"),
				getNestedString(obj, "spec", "completions"))
			duration := ""
			startTime := getNestedString(obj, "status", "startTime")
			completionTime := getNestedString(obj, "status", "completionTime")
			if startTime != "" && completionTime != "" {
				st, _ := time.Parse(time.RFC3339, startTime)
				ct, _ := time.Parse(time.RFC3339, completionTime)
				if !st.IsZero() && !ct.IsZero() {
					duration = ct.Sub(st).Round(time.Second).String()
				}
			}
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "metadata", "namespace"),
				completions,
				duration,
				formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
			}
		},
	},
	{
		commandName: "cronjobs", kind: "CronJob", group: "batch", version: "v1",
		short: "List cronjobs in the cluster",
		headers: table.Row{"Name", "Namespace", "Schedule", "Suspend", "Active", "Last Schedule"},
		formatRow: func(obj map[string]interface{}) table.Row {
			activeCount := 0
			if statusMap, ok := getNestedMap(obj, "status"); ok {
				if active, ok := statusMap["active"]; ok {
					if al, ok := active.([]interface{}); ok {
						activeCount = len(al)
					}
				}
			}
			return table.Row{
				getNestedString(obj, "metadata", "name"),
				getNestedString(obj, "metadata", "namespace"),
				getNestedString(obj, "spec", "schedule"),
				getNestedString(obj, "spec", "suspend"),
				activeCount,
				formatK8sAge(getNestedString(obj, "status", "lastScheduleTime")),
			}
		},
	},
}

func renderSingleResource(item interface{}, outputFormat string) {
	switch outputFormat {
	case "json":
		jsonData, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshalling to JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(jsonData))
	default:
		yamlData, err := yaml.Marshal(item)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshalling to YAML: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(string(yamlData))
	}
}

func fetchAndRenderResources(clusterID, namespace, nameFilter, labelSelector, outputFormat string, cfg kindConfig) {
	reqItem := client.ResourceRequestItem{
		Kind:    cfg.kind,
		Version: cfg.version,
	}
	if cfg.group != "" {
		reqItem.Group = cfg.group
	}
	if namespace != "" && !cfg.clusterScoped {
		reqItem.Namespace = namespace
	}
	if nameFilter != "" {
		reqItem.Name = nameFilter
	}
	if labelSelector != "" {
		reqItem.LabelSelector = labelSelector
	}

	response, err := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{reqItem},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(response.ResourceResponses) == 0 || len(response.ResourceResponses[0].Items) == 0 {
		fmt.Printf("No %s found.\n", cfg.commandName)
		return
	}

	items := response.ResourceResponses[0].Items

	isSingleResourceLookup := nameFilter != "" && len(items) == 1
	if isSingleResourceLookup {
		renderSingleResource(items[0], outputFormat)
		return
	}

	if outputFormat == "json" {
		jsonData, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshalling to JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(jsonData))
		return
	}
	if outputFormat == "yaml" {
		yamlData, err := yaml.Marshal(response)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshalling to YAML: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(string(yamlData))
		return
	}

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(cfg.headers)

	for _, item := range items {
		if obj, ok := item.(map[string]interface{}); ok {
			t.AppendRow(cfg.formatRow(obj))
		}
	}
	t.Render()
}

func registerKindCommand(cfg kindConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cfg.commandName + " [name]",
		Short: cfg.short,
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{"group": "kubernetes"},
		Run: func(cmd *cobra.Command, args []string) {
			cluster, err := loadSelectedCluster()
			if err != nil {
				fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
				return
			}
			namespace, _ := cmd.Flags().GetString("namespace")
			allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
			nameFilter, _ := cmd.Flags().GetString("name")
			labelSelector, _ := cmd.Flags().GetString("selector")
			outputFormat, _ := cmd.Flags().GetString("output")

			if len(args) == 1 {
				nameFilter = args[0]
				if outputFormat == "table" {
					outputFormat = "yaml"
				}
			}

			if allNamespaces {
				namespace = ""
			}

			fetchAndRenderResources(cluster.ID, namespace, nameFilter, labelSelector, outputFormat, cfg)
		},
	}

	if !cfg.clusterScoped {
		cmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace")
		cmd.Flags().BoolP("all-namespaces", "A", false, "List across all namespaces")
	}
	cmd.Flags().String("name", "", "Filter by resource name")
	cmd.Flags().StringP("selector", "l", "", "Label selector")
	cmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")

	return cmd
}

var clusterPodsCmd = &cobra.Command{
	Use:   "pods [name]",
	Short: "List pods or get a specific pod's manifest",
	Args:  cobra.MaximumNArgs(1),
	Annotations: map[string]string{"group": "kubernetes"},
	Run: func(cmd *cobra.Command, args []string) {
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}
		namespace, _ := cmd.Flags().GetString("namespace")
		allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
		nodeName, _ := cmd.Flags().GetString("node")
		nameContains, _ := cmd.Flags().GetString("name")
		outputFormat, _ := cmd.Flags().GetString("output")

		if len(args) == 1 {
			podName := args[0]
			if outputFormat == "table" {
				outputFormat = "yaml"
			}
			podCfg := kindConfig{
				commandName: "pods",
				kind:        "Pod",
				version:     "v1",
			}
			fetchAndRenderResources(cluster.ID, namespace, podName, "", outputFormat, podCfg)
			return
		}

		if allNamespaces {
			namespace = ""
		}

		allPods := []client.PodSummary{}
		page := 1
		for {
			opts := &client.ListPodsOptions{
				Page:         page,
				PageSize:     100,
				Namespace:    namespace,
				NameContains: nameContains,
				NodeName:     nodeName,
			}

			response, err := apiClient.ListPods(cluster.ID, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			allPods = append(allPods, response.Pods...)

			if outputFormat == "json" && page == 1 && response.TotalPages <= 1 {
				jsonData, _ := json.MarshalIndent(response, "", "  ")
				fmt.Println(string(jsonData))
				return
			}

			if page >= response.TotalPages {
				break
			}
			page++
		}

		if outputFormat == "json" {
			jsonData, err := json.MarshalIndent(allPods, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshalling to JSON: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(string(jsonData))
			return
		}
		if outputFormat == "yaml" {
			yamlData, err := yaml.Marshal(allPods)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshalling to YAML: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(string(yamlData))
			return
		}

		if len(allPods) == 0 {
			fmt.Println("No pods found.")
			return
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Namespace", "Status", "Ready", "Restarts", "Node", "Age"})

		for _, pod := range allPods {
			ns := ""
			if pod.Namespace != nil {
				ns = *pod.Namespace
			}
			node := ""
			if pod.NodeName != nil {
				node = *pod.NodeName
			}
			status := pod.Phase
			switch strings.ToLower(status) {
			case "running":
				status = text.FgGreen.Sprint(status)
			case "failed", "error":
				status = text.FgRed.Sprint(status)
			case "pending":
				status = text.FgYellow.Sprint(status)
			}
			age := ""
			if pod.StartTime != nil {
				age = formatK8sAge(*pod.StartTime)
			}
			t.AppendRow(table.Row{pod.Name, ns, status, pod.Ready, pod.Restarts, node, age})
		}
		t.Render()

		fmt.Printf("\n%d pods total.\n", len(allPods))
	},
}

var clusterLogsCmd = &cobra.Command{
	Use:   "logs <pod_name>",
	Short: "Stream logs from a pod",
	Long: `Stream log output from a pod in the active cluster.

Example:
  ankra cluster logs my-pod -n default -c my-container --tail 100`,
	Args: cobra.ExactArgs(1),
	Annotations: map[string]string{"group": "kubernetes"},
	Run: func(cmd *cobra.Command, args []string) {
		podName := args[0]
		namespace, _ := cmd.Flags().GetString("namespace")
		container, _ := cmd.Flags().GetString("container")
		tailLines, _ := cmd.Flags().GetInt("tail")
		sinceSeconds, _ := cmd.Flags().GetInt("since")

		if namespace == "" {
			fmt.Fprintln(os.Stderr, "Error: --namespace (-n) is required for logs")
			os.Exit(1)
		}
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}

		opts := client.PodLogOptions{
			Namespace:     namespace,
			PodName:       podName,
			ContainerName: container,
			TailLines:     tailLines,
			SinceSeconds:  sinceSeconds,
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		if err := apiClient.StreamPodLogs(ctx, cluster.ID, opts, os.Stdout); err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var clusterGenericResourcesCmd = &cobra.Command{
	Use:   "resources <kind>",
	Short: "Get any Kubernetes resource by kind",
	Long: `Fetch any Kubernetes resource type. Use for kinds not covered by dedicated commands.

Example:
  ankra cluster resources PersistentVolumeClaim -n default
  ankra cluster resources NetworkPolicy --all-namespaces`,
	Args: cobra.ExactArgs(1),
	Annotations: map[string]string{"group": "kubernetes"},
	Run: func(cmd *cobra.Command, args []string) {
		kind := args[0]
		cluster, err := loadSelectedCluster()
		if err != nil {
			fmt.Println("No active cluster selected. Run 'ankra cluster select <name>' or 'ankra cluster select' to pick one.")
			return
		}
		namespace, _ := cmd.Flags().GetString("namespace")
		allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
		nameFilter, _ := cmd.Flags().GetString("name")
		labelSelector, _ := cmd.Flags().GetString("selector")
		outputFormat, _ := cmd.Flags().GetString("output")
		apiVersion, _ := cmd.Flags().GetString("api-version")
		apiGroup, _ := cmd.Flags().GetString("group")

		if allNamespaces {
			namespace = ""
		}

		genericCfg := kindConfig{
			commandName: kind,
			kind:        kind,
			group:       apiGroup,
			version:     apiVersion,
			headers:     table.Row{"Name", "Namespace", "Age"},
			formatRow: func(obj map[string]interface{}) table.Row {
				return table.Row{
					getNestedString(obj, "metadata", "name"),
					getNestedString(obj, "metadata", "namespace"),
					formatK8sAge(getNestedString(obj, "metadata", "creationTimestamp")),
				}
			},
		}

		fetchAndRenderResources(cluster.ID, namespace, nameFilter, labelSelector, outputFormat, genericCfg)
	},
}

var clusterGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get Kubernetes resources from the active cluster",
	Long: `Get Kubernetes resources from the active cluster.

Examples:
  ankra cluster get pods
  ankra cluster get deployments -n kube-system
  ankra cluster get nodes
  ankra cluster get services --all-namespaces`,
}

func init() {
	clusterPodsCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace")
	clusterPodsCmd.Flags().BoolP("all-namespaces", "A", false, "List across all namespaces")
	clusterPodsCmd.Flags().String("node", "", "Filter by node name")
	clusterPodsCmd.Flags().String("name", "", "Filter by pod name (contains)")
	clusterPodsCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")

	clusterLogsCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required)")
	clusterLogsCmd.Flags().StringP("container", "c", "", "Container name (defaults to pod name)")
	clusterLogsCmd.Flags().Int("tail", 0, "Number of lines from the end of the logs")
	clusterLogsCmd.Flags().Int("since", 0, "Seconds of logs to retrieve")

	clusterGenericResourcesCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace")
	clusterGenericResourcesCmd.Flags().BoolP("all-namespaces", "A", false, "List across all namespaces")
	clusterGenericResourcesCmd.Flags().String("name", "", "Filter by resource name")
	clusterGenericResourcesCmd.Flags().StringP("selector", "l", "", "Label selector")
	clusterGenericResourcesCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")
	clusterGenericResourcesCmd.Flags().String("api-version", "v1", "API version (e.g. v1, v1beta1)")
	clusterGenericResourcesCmd.Flags().String("group", "", "API group (e.g. apps, networking.k8s.io)")

	clusterGetCmd.AddCommand(clusterPodsCmd)
	clusterGetCmd.AddCommand(clusterGenericResourcesCmd)

	for _, cfg := range kindConfigs {
		clusterGetCmd.AddCommand(registerKindCommand(cfg))
	}

	clusterCmd.AddCommand(clusterGetCmd)
	clusterCmd.AddCommand(clusterLogsCmd)
}
