package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var securityNamespacesCmd = &cobra.Command{
	Use:   "namespaces",
	Short: "Security posture by cluster and namespace: workloads, pods and actionable findings",
	Long: `Break the fleet's findings down the way a platform team owns it: cluster,
then namespace. Each row carries the scanned workloads and images in the
namespace, the pods the cluster runs there right now, the actionable
findings by severity and the known-exploited count. Cluster-scoped reports
(node and control-plane images) keep a row of their own so the rows still
add up to the cluster.

Drill further with 'ankra security pods --cluster <cluster> --namespace <ns>'.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		search, _ := cmd.Flags().GetString("search")
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		sort, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		options := client.SecurityNamespacesOptions{Page: page, PageSize: pageSize, Search: search, Sort: sort, Order: order}
		if clusterFlag != "" {
			clusterID, err := resolveClusterID(clusterFlag)
			if err != nil {
				return err
			}
			options.ClusterID = clusterID
		}
		list, err := apiClient.ListSecurityNamespaces(options)
		if err != nil {
			return fmt.Errorf("listing namespace security posture: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecurityNamespaces(cmd, list)
		return nil
	},
}

var securityPodsCmd = &cobra.Command{
	Use:   "pods",
	Short: "The pods of one namespace, each container joined to its scanned workload's findings",
	Long: `List the pods a namespace runs right now with the findings of the scanned
workload container behind each of their containers. The owner chain is
resolved one level up (Pod -> ReplicaSet -> Deployment, Pod -> Job -> CronJob),
so narrow to one workload with --workload-uid or --workload-kind/--workload-name.

A container the scanner has no report for is marked "not scanned", which is
not the same as clean.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		namespace, _ := cmd.Flags().GetString("namespace")
		workloadUID, _ := cmd.Flags().GetString("workload-uid")
		workloadKind, _ := cmd.Flags().GetString("workload-kind")
		workloadName, _ := cmd.Flags().GetString("workload-name")
		if strings.TrimSpace(clusterFlag) == "" || strings.TrimSpace(namespace) == "" {
			return withExitCode(exitUsage, fmt.Errorf("--cluster and --namespace are required"))
		}
		clusterID, err := resolveClusterID(clusterFlag)
		if err != nil {
			return err
		}
		list, err := apiClient.ListSecurityPods(client.SecurityPodsOptions{
			ClusterID:    clusterID,
			Namespace:    namespace,
			WorkloadUID:  workloadUID,
			WorkloadKind: workloadKind,
			WorkloadName: workloadName,
			Page:         page,
			PageSize:     pageSize,
		})
		if err != nil {
			return fmt.Errorf("listing namespace pods: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecurityPods(cmd, list)
		return nil
	},
}

var securitySbomCmd = &cobra.Command{
	Use:   "sbom",
	Short: "The fleet's software bill of materials: every package in every image, and where it runs",
	Long: `List every package the scanner found in the fleet's container images,
grouped by name, version and ecosystem, with how many images, workloads and
clusters carry it and the findings that name that exact package and version.
Search a package to answer "where do we run this" without a rescan.

The bill of materials is opt-in per cluster: set the security baseline input
trivy_sbom_generation_enabled to true on a cluster with control-plane
headroom. The coverage line says how many scanned clusters publish one.

Subcommands list the images ('ankra security sbom images') and open one
image's full component list ('ankra security sbom image <digest or reference>').`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		options, err := securitySbomComponentsOptionsFromFlags(cmd)
		if err != nil {
			return err
		}
		list, err := apiClient.ListSecuritySBOMComponents(options)
		if err != nil {
			return fmt.Errorf("listing the software bill of materials: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecuritySbomComponents(cmd, list)
		return nil
	},
}

var securitySbomImagesCmd = &cobra.Command{
	Use:   "images",
	Short: "Images with a bill of materials: OS, component count, where they run and their findings",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		search, _ := cmd.Flags().GetString("search")
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		namespace, _ := cmd.Flags().GetString("namespace")
		workloadKind, _ := cmd.Flags().GetString("workload-kind")
		workloadName, _ := cmd.Flags().GetString("workload-name")
		sort, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		options := client.SecuritySBOMImagesOptions{
			Page: page, PageSize: pageSize, Search: search, Namespace: namespace,
			WorkloadKind: workloadKind, WorkloadName: workloadName, Sort: sort, Order: order,
		}
		if clusterFlag != "" {
			clusterID, err := resolveClusterID(clusterFlag)
			if err != nil {
				return err
			}
			options.ClusterID = clusterID
		}
		list, err := apiClient.ListSecuritySBOMImages(options)
		if err != nil {
			return fmt.Errorf("listing images with a bill of materials: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecuritySbomImages(cmd, list)
		return nil
	},
}

var securitySbomImageCmd = &cobra.Command{
	Use:   "image <digest or reference>",
	Short: "One image's bill of materials: its identity, the workloads running it and every component",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		search, _ := cmd.Flags().GetString("search")
		packageTypes, _ := cmd.Flags().GetStringSlice("type")
		sort, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		detail, err := apiClient.GetSecuritySBOMImage(client.SecuritySBOMImageOptions{
			ImageIdentity: strings.TrimSpace(args[0]),
			Page:          page,
			PageSize:      pageSize,
			Search:        search,
			PackageTypes:  packageTypes,
			Sort:          sort,
			Order:         order,
		})
		if err != nil {
			return fmt.Errorf("reading the image's bill of materials: %w", err)
		}
		if rendered, err := renderStructured(cmd, detail); rendered || err != nil {
			return err
		}
		renderSecuritySbomImageDetail(cmd, detail)
		return nil
	},
}

var securitySbomContainersCmd = &cobra.Command{
	Use:   "containers",
	Short: "Every running container with or without a bill of materials, absent first",
	Long: `List every container the resource cache says is running on the scoped
clusters, init containers included, each joined to the bill of materials of
the image it runs - or marked ABSENT when the scanner has none for it.

An absent bill of materials is a state to act on, not an empty image: the
cluster may have SBOM generation switched off, the scanner may not be able
to pull from the image's registry, or the tag rolled since the last scan.
Absent rows sort first by default; --status absent lists only them.

The inventory line counts the scope (cluster, namespace, workload) before
--status and --search narrow the rows, so it holds while you drill.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		search, _ := cmd.Flags().GetString("search")
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		namespace, _ := cmd.Flags().GetString("namespace")
		workloadKind, _ := cmd.Flags().GetString("workload-kind")
		workloadName, _ := cmd.Flags().GetString("workload-name")
		status, _ := cmd.Flags().GetString("status")
		sort, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "", "any", "present", "absent":
		default:
			return withExitCode(exitUsage, fmt.Errorf("--status must be present, absent or any, got %q", status))
		}
		options := client.SecuritySBOMContainersOptions{
			Page: page, PageSize: pageSize, Search: search, Namespace: namespace,
			WorkloadKind: workloadKind, WorkloadName: workloadName, Status: status, Sort: sort, Order: order,
		}
		if strings.EqualFold(strings.TrimSpace(status), "any") {
			options.Status = ""
		}
		if clusterFlag != "" {
			clusterID, err := resolveClusterID(clusterFlag)
			if err != nil {
				return err
			}
			options.ClusterID = clusterID
		}
		list, err := apiClient.ListSecuritySBOMContainers(options)
		if err != nil {
			return fmt.Errorf("listing running containers and their bills of materials: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecuritySbomContainers(cmd, list)
		return nil
	},
}

var securitySbomExportCmd = &cobra.Command{
	Use:   "export <digest or reference>",
	Short: "Download one image's bill of materials as CycloneDX JSON or CSV",
	Long: `Download one image's bill of materials, rebuilt from the components the
platform stores: a CycloneDX 1.5 JSON document by default (what
Dependency-Track, Grype and licence scanners ingest) or a flat CSV with
--format csv. Every component is included, unpaged.

The document goes to the file the platform names (repository_tag.cdx.json)
in the current directory, to --output-file, or to standard output with
--output-file -.

Example:
  ankra security sbom export sha256:18ed3a...  --format csv --output-file backend.csv`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")
		outputFile, _ := cmd.Flags().GetString("output-file")
		force, _ := cmd.Flags().GetBool("force")
		normalizedFormat, formatError := normalizeSbomExportFormat(format)
		if formatError != nil {
			return formatError
		}
		export, err := apiClient.ExportSecuritySBOMImage(client.SecuritySBOMExportOptions{
			ImageIdentity: strings.TrimSpace(args[0]),
			Format:        normalizedFormat,
		})
		if err != nil {
			return fmt.Errorf("downloading the image's bill of materials: %w", err)
		}
		if outputFile == "-" {
			_, writeError := cmd.OutOrStdout().Write(export.Body)
			return writeError
		}
		target := strings.TrimSpace(outputFile)
		if target == "" {
			target = sbomExportLocalFileName(export.FileName, normalizedFormat)
		}
		if !force {
			if _, statError := os.Stat(target); statError == nil {
				return withExitCode(exitUsage, fmt.Errorf("%s already exists; pass --force to overwrite it or --output-file to write elsewhere", target))
			}
		}
		if writeError := os.WriteFile(target, export.Body, 0o600); writeError != nil {
			return fmt.Errorf("writing %s: %w", target, writeError)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%d bytes, %s)\n", target, len(export.Body), export.ContentType)
		return nil
	},
}

// normalizeSbomExportFormat maps the accepted spellings to the two formats
// the platform serves, so a typo is a usage error here rather than a
// server error or a silently defaulted document.
func normalizeSbomExportFormat(requested string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "", "cyclonedx", "cdx", "json":
		return "cyclonedx", nil
	case "csv":
		return "csv", nil
	}
	return "", withExitCode(exitUsage, fmt.Errorf("--format must be cyclonedx or csv, got %q", requested))
}

// sbomExportLocalFileName is the file the download lands in when the caller
// named none: the platform's suggested name reduced to its last path element
// (a Content-Disposition is server input, never a path), or a name derived
// from the format when the platform sent none.
func sbomExportLocalFileName(suggested string, format string) string {
	base := filepath.Base(strings.TrimSpace(suggested))
	if base == "" || base == "." || base == string(filepath.Separator) || strings.HasPrefix(base, ".") {
		if format == "csv" {
			return "sbom.csv"
		}
		return "sbom.cdx.json"
	}
	return base
}

// securitySbomComponentsOptionsFromFlags maps the component flags. --vulnerable
// accepts true, false or any so the inventory can be narrowed to packages a
// finding names, or to the ones nothing names.
func securitySbomComponentsOptionsFromFlags(cmd *cobra.Command) (client.SecuritySBOMComponentsOptions, error) {
	page, _ := cmd.Flags().GetInt("page")
	pageSize, _ := cmd.Flags().GetInt("page-size")
	search, _ := cmd.Flags().GetString("search")
	packageTypes, _ := cmd.Flags().GetStringSlice("type")
	clusterFlag, _ := cmd.Flags().GetString("cluster")
	namespace, _ := cmd.Flags().GetString("namespace")
	workloadKind, _ := cmd.Flags().GetString("workload-kind")
	workloadName, _ := cmd.Flags().GetString("workload-name")
	image, _ := cmd.Flags().GetString("image")
	vulnerableRaw, _ := cmd.Flags().GetString("vulnerable")
	sort, _ := cmd.Flags().GetString("sort")
	order, _ := cmd.Flags().GetString("order")
	options := client.SecuritySBOMComponentsOptions{
		Page:          page,
		PageSize:      pageSize,
		Search:        search,
		PackageTypes:  packageTypes,
		Namespace:     namespace,
		WorkloadKind:  workloadKind,
		WorkloadName:  workloadName,
		ImageIdentity: image,
		Sort:          sort,
		Order:         order,
	}
	switch strings.ToLower(strings.TrimSpace(vulnerableRaw)) {
	case "", "any":
	case "true", "yes":
		vulnerable := true
		options.Vulnerable = &vulnerable
	case "false", "no":
		vulnerable := false
		options.Vulnerable = &vulnerable
	default:
		return options, withExitCode(exitUsage, fmt.Errorf("--vulnerable must be true, false or any, got %q", vulnerableRaw))
	}
	if clusterFlag != "" {
		clusterID, err := resolveClusterID(clusterFlag)
		if err != nil {
			return options, err
		}
		options.ClusterID = clusterID
	}
	return options, nil
}

func namespaceCell(namespace client.SecurityNamespace) string {
	if namespace.ReportScope == "cluster" {
		return "(cluster-scoped)"
	}
	return namespace.Namespace
}

func redIfPositive(value int) string {
	rendered := fmt.Sprintf("%d", value)
	if value > 0 {
		return text.FgRed.Sprint(rendered)
	}
	return rendered
}

func renderSecurityNamespaces(cmd *cobra.Command, list *client.SecurityNamespaceList) {
	out := cmd.OutOrStdout()
	if len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No scanned namespaces match these filters.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Cluster", "Namespace", "Workloads", "Images", "Pods", "Actionable", "Critical", "High", "Known exploited", "Fixable severe", "Last scan"})
	for _, namespace := range list.Result {
		writer.AppendRow(table.Row{
			namespace.ClusterName,
			namespaceCell(namespace),
			namespace.Workloads,
			namespace.Images,
			namespace.Pods,
			namespace.ActionableTotal,
			namespace.Actionable.Critical,
			namespace.Actionable.High,
			redIfPositive(namespace.KnownExploited),
			namespace.FixableSevere,
			formatTimeAgo(namespace.LastScan),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d namespaces\n", list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
}

func podContainerCell(container client.SecurityPodContainer) string {
	parts := []string{container.Name + ": not scanned"}
	if container.Scanned {
		parts = []string{fmt.Sprintf("%s: %d critical, %d high", container.Name, container.Actionable.Critical, container.Actionable.High)}
		if container.KnownExploited > 0 {
			parts = append(parts, text.FgRed.Sprintf("%d known exploited", container.KnownExploited))
		}
	}
	if !container.Ready {
		parts = append(parts, "not ready")
	}
	return strings.Join(parts, ", ")
}

func renderSecurityPods(cmd *cobra.Command, list *client.SecurityPodList) {
	out := cmd.OutOrStdout()
	if len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No running pods match this workload.")
		return
	}
	if list.Capped {
		_, _ = fmt.Fprintln(out, text.FgYellow.Sprint("The namespace holds more pods than one read loads; this listing is capped."))
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Pod", "Workload", "Node", "Phase", "Priority", "Observed", "Critical", "High", "Known exploited", "Containers"})
	for _, pod := range list.Result {
		workload := "-"
		if pod.WorkloadName != nil {
			workload = stringOrEmpty(pod.WorkloadKind) + " " + *pod.WorkloadName
		}
		containers := make([]string, 0, len(pod.Containers))
		for _, container := range pod.Containers {
			containers = append(containers, podContainerCell(container))
		}
		priority := pod.Priority
		if priority == "unscanned" {
			priority = text.FgYellow.Sprint("not scanned")
		}
		writer.AppendRow(table.Row{
			pod.Name,
			strings.TrimSpace(workload),
			stringOrEmpty(pod.Node),
			stringOrEmpty(pod.Phase),
			priority,
			pod.Observed,
			pod.Actionable.Critical,
			pod.Actionable.High,
			redIfPositive(pod.KnownExploited),
			strings.Join(containers, "\n"),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d pods\n", list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
}

func renderSbomCoverage(cmd *cobra.Command, coverage client.SecuritySBOMCoverage) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Bill of materials: %d images, %d components, %d workloads · %d of %d scanned clusters publish one",
		coverage.Images, coverage.Components, coverage.Workloads, coverage.ClustersWithSBOM, coverage.ScannedClusters)
	if coverage.LatestGeneratedAt != nil && *coverage.LatestGeneratedAt != "" {
		_, _ = fmt.Fprintf(out, " · latest %s", formatTimeAgo(*coverage.LatestGeneratedAt))
	}
	_, _ = fmt.Fprintln(out)
	if coverage.ClustersWithSBOM < coverage.ScannedClusters {
		_, _ = fmt.Fprintln(out, text.FgYellow.Sprint("Clusters without a bill of materials have SBOM generation off: set the security baseline input trivy_sbom_generation_enabled to true to publish one."))
	}
}

func componentFindingsCell(component client.SecuritySBOMComponent) string {
	if component.VulnerableFindings == 0 {
		return "-"
	}
	rendered := fmt.Sprintf("%d (%d actionable)", component.VulnerableFindings, component.ActionableFindings)
	if component.KnownExploited > 0 {
		rendered += " " + text.FgRed.Sprint("KEV")
	}
	return rendered
}

func renderSecuritySbomComponents(cmd *cobra.Command, list *client.SecuritySBOMComponentList) {
	out := cmd.OutOrStdout()
	renderSbomCoverage(cmd, list.Coverage)
	if len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No components match these filters.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Package", "Version", "Type", "Licence", "Images", "Workloads", "Clusters", "Findings"})
	for _, component := range list.Result {
		writer.AppendRow(table.Row{
			component.Name,
			component.Version,
			component.PackageType,
			strings.Join(component.Licenses, ", "),
			component.Images,
			component.Workloads,
			component.Clusters,
			componentFindingsCell(component),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d components\n", list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
}

func renderSecuritySbomImages(cmd *cobra.Command, list *client.SecuritySBOMImageList) {
	out := cmd.OutOrStdout()
	renderSbomCoverage(cmd, list.Coverage)
	if len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No images match these filters.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Image", "OS", "Components", "Workloads", "Clusters", "Namespaces", "Critical", "High", "Known exploited", "Generated"})
	for _, image := range list.Result {
		writer.AppendRow(table.Row{
			image.ImageRef,
			stringOrEmpty(image.OSName),
			image.ComponentCount,
			image.Workloads,
			image.Clusters,
			strings.Join(image.Namespaces, ", "),
			image.Actionable.Critical,
			image.Actionable.High,
			redIfPositive(image.KnownExploited),
			optionalTimeAgo(image.GeneratedAt),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d images\n", list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
}

func renderSecuritySbomContainers(cmd *cobra.Command, list *client.SecuritySBOMContainerList) {
	out := cmd.OutOrStdout()
	renderSbomCoverage(cmd, list.Coverage)
	inventory := list.Inventory
	if inventory.Containers > 0 {
		without := fmt.Sprintf("%d", inventory.WithoutSBOM)
		if inventory.WithoutSBOM > 0 {
			without = text.FgRed.Sprint(without)
		}
		_, _ = fmt.Fprintf(out, "Inventory: %d containers in %d pods, %d with a bill of materials, %s without\n",
			inventory.Containers, inventory.Pods, inventory.WithSBOM, without)
	}
	if len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No running containers match these filters.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Cluster", "Namespace", "Workload", "Pod", "Container", "Image", "Bill of materials", "Components", "OS", "Generated"})
	for _, container := range list.Result {
		workload := "(bare pod)"
		if container.WorkloadName != nil {
			workload = strings.TrimSpace(stringOrEmpty(container.WorkloadKind) + " " + *container.WorkloadName)
		}
		containerLabel := container.ContainerName
		if container.ContainerKind == "init" {
			containerLabel += " (init)"
		}
		status := text.FgGreen.Sprint("present")
		components := "-"
		if container.SBOMStatus != "present" {
			status = text.FgRed.Sprint("ABSENT")
		} else if container.ComponentCount != nil {
			components = fmt.Sprintf("%d", *container.ComponentCount)
		}
		writer.AppendRow(table.Row{
			container.ClusterName,
			container.Namespace,
			workload,
			container.PodName,
			containerLabel,
			container.Image,
			status,
			components,
			stringOrEmpty(container.OSName),
			optionalTimeAgo(container.GeneratedAt),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d containers\n", list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
}

func renderSecuritySbomImageDetail(cmd *cobra.Command, detail *client.SecuritySBOMImageDetail) {
	out := cmd.OutOrStdout()
	image := detail.Image
	_, _ = fmt.Fprintln(out, text.Bold.Sprint(image.ImageRef))
	if image.ImageDigest != nil {
		_, _ = fmt.Fprintf(out, "Digest:      %s\n", *image.ImageDigest)
	}
	if image.Registry != nil {
		_, _ = fmt.Fprintf(out, "Registry:    %s\n", *image.Registry)
	}
	_, _ = fmt.Fprintf(out, "OS:          %s\n", stringOrEmpty(image.OSName))
	if image.BomFormat != nil {
		_, _ = fmt.Fprintf(out, "Format:      %s %s\n", *image.BomFormat, stringOrEmpty(image.SpecVersion))
	}
	_, _ = fmt.Fprintf(out, "Components:  %d (%d dependencies)\n", image.ComponentCount, image.DependencyCount)
	_, _ = fmt.Fprintf(out, "Findings:    %d observed, %d critical, %d high actionable, %s known exploited\n",
		image.Observed, image.Actionable.Critical, image.Actionable.High, redIfPositive(image.KnownExploited))
	_, _ = fmt.Fprintf(out, "Generated:   %s\n", optionalTimeAgo(image.GeneratedAt))

	_, _ = fmt.Fprintln(out)
	if len(detail.Workloads) == 0 {
		_, _ = fmt.Fprintln(out, "No workload currently runs this image; the bill of materials is kept from the last scan that saw it.")
	} else {
		_, _ = fmt.Fprintf(out, "Running in %d workload container(s) on %d cluster(s):\n", image.Workloads, image.Clusters)
		for _, workload := range detail.Workloads {
			label := "cluster-scoped image"
			if workload.WorkloadName != nil {
				label = strings.TrimSpace(stringOrEmpty(workload.WorkloadKind) + " " + *workload.WorkloadName)
				if workload.WorkloadNamespace != nil {
					label += " in " + *workload.WorkloadNamespace
				}
			}
			if workload.ContainerName != nil {
				label += " (container " + *workload.ContainerName + ")"
			}
			_, _ = fmt.Fprintf(out, "  - %s on %s\n", label, workload.ClusterName)
		}
	}

	_, _ = fmt.Fprintln(out)
	if len(detail.Components) == 0 {
		_, _ = fmt.Fprintln(out, "No components match these filters.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Package", "Version", "Type", "Licence", "Findings"})
	for _, component := range detail.Components {
		writer.AppendRow(table.Row{
			component.Name,
			component.Version,
			component.PackageType,
			strings.Join(component.Licenses, ", "),
			componentFindingsCell(component),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d components\n", detail.Pagination.Page, detail.Pagination.TotalPages, detail.Pagination.TotalCount)
}

func init() {
	securityCmd.AddCommand(securityNamespacesCmd, securityPodsCmd, securitySbomCmd)
	securitySbomCmd.AddCommand(securitySbomImagesCmd, securitySbomImageCmd, securitySbomContainersCmd, securitySbomExportCmd)
	registerStructuredOutputFlags(securityNamespacesCmd, securityPodsCmd, securitySbomCmd,
		securitySbomImagesCmd, securitySbomImageCmd, securitySbomContainersCmd)

	securityNamespacesCmd.Flags().String("search", "", "Match namespace or cluster name")
	securityNamespacesCmd.Flags().String("cluster", "", "Only namespaces on one cluster (name or id)")
	securityNamespacesCmd.Flags().String("sort", "actionable", "Sort key: actionable, severity, known_exploited, fixable_severe, workloads, images, pods, observed, namespace, cluster_name, last_scan")
	securityNamespacesCmd.Flags().String("order", "desc", "Sort order: asc or desc")
	securityNamespacesCmd.Flags().Int("page", 1, "Page number")
	securityNamespacesCmd.Flags().Int("page-size", 50, "Namespaces per page (max 100)")

	securityPodsCmd.Flags().String("cluster", "", "The cluster (name or id); required")
	securityPodsCmd.Flags().String("namespace", "", "The namespace; required")
	securityPodsCmd.Flags().String("workload-uid", "", "Only pods owned by the workload with this uid")
	securityPodsCmd.Flags().String("workload-kind", "", "With --workload-name: only pods owned by this workload kind")
	securityPodsCmd.Flags().String("workload-name", "", "Only pods owned by the workload with this name")
	securityPodsCmd.Flags().Int("page", 1, "Page number")
	securityPodsCmd.Flags().Int("page-size", 50, "Pods per page (max 100)")

	securitySbomCmd.Flags().String("search", "", "Match package name or package URL")
	securitySbomCmd.Flags().StringSlice("type", nil, "Ecosystem filter, repeatable: deb, apk, rpm, npm, pypi, golang, maven, ...")
	securitySbomCmd.Flags().String("cluster", "", "Only packages in images running on one cluster (name or id)")
	securitySbomCmd.Flags().String("namespace", "", "Only packages in images running in one namespace")
	securitySbomCmd.Flags().String("workload-kind", "", "Only packages in images run by workloads of this kind (Deployment, StatefulSet, DaemonSet, CronJob, ...)")
	securitySbomCmd.Flags().String("workload-name", "", "Only packages in images run by the workload with this name; combine with --workload-kind to pin one workload")
	securitySbomCmd.Flags().String("image", "", "Only packages in one image (digest or reference)")
	securitySbomCmd.Flags().String("vulnerable", "any", "Findings filter: true (a finding names the package), false or any")
	securitySbomCmd.Flags().String("sort", "images", "Sort key: images, workloads, clusters, vulnerable, actionable, name, version, package_type")
	securitySbomCmd.Flags().String("order", "desc", "Sort order: asc or desc")
	securitySbomCmd.Flags().Int("page", 1, "Page number")
	securitySbomCmd.Flags().Int("page-size", 50, "Components per page (max 100)")

	securitySbomImagesCmd.Flags().String("search", "", "Match image reference, digest or OS")
	securitySbomImagesCmd.Flags().String("cluster", "", "Only images running on one cluster (name or id)")
	securitySbomImagesCmd.Flags().String("namespace", "", "Only images running in one namespace")
	securitySbomImagesCmd.Flags().String("workload-kind", "", "Only images run by workloads of this kind (Deployment, StatefulSet, DaemonSet, CronJob, ...)")
	securitySbomImagesCmd.Flags().String("workload-name", "", "Only images run by the workload with this name; combine with --workload-kind to pin one workload")
	securitySbomImagesCmd.Flags().String("sort", "actionable", "Sort key: actionable, known_exploited, workloads, clusters, components, image_ref, generated_at, last_seen_at")
	securitySbomImagesCmd.Flags().String("order", "desc", "Sort order: asc or desc")
	securitySbomImagesCmd.Flags().Int("page", 1, "Page number")
	securitySbomImagesCmd.Flags().Int("page-size", 50, "Images per page (max 100)")

	securitySbomImageCmd.Flags().String("search", "", "Match package name or package URL")
	securitySbomImageCmd.Flags().StringSlice("type", nil, "Ecosystem filter, repeatable")
	securitySbomImageCmd.Flags().String("sort", "vulnerable", "Sort key: vulnerable, actionable, name, version, package_type")
	securitySbomImageCmd.Flags().String("order", "desc", "Sort order: asc or desc")
	securitySbomImageCmd.Flags().Int("page", 1, "Page number")
	securitySbomImageCmd.Flags().Int("page-size", 100, "Components per page (max 100)")

	securitySbomContainersCmd.Flags().String("search", "", "Match image, container, pod, workload or namespace")
	securitySbomContainersCmd.Flags().String("cluster", "", "One cluster (name or id); omit for every live cluster")
	securitySbomContainersCmd.Flags().String("namespace", "", "Only containers in one namespace")
	securitySbomContainersCmd.Flags().String("workload-kind", "", "Match the resolved workload, its direct owner, or 'pod' for bare pods")
	securitySbomContainersCmd.Flags().String("workload-name", "", "Match the resolved workload, its direct owner, or the pod name")
	securitySbomContainersCmd.Flags().String("status", "any", "Bill of materials filter: present, absent or any")
	securitySbomContainersCmd.Flags().String("sort", "status", "Sort key: status (absent first), image, container, pod, namespace, workload, cluster_name, components, last_seen_at")
	securitySbomContainersCmd.Flags().String("order", "desc", "Sort order: asc or desc")
	securitySbomContainersCmd.Flags().Int("page", 1, "Page number")
	securitySbomContainersCmd.Flags().Int("page-size", 50, "Containers per page (max 100)")

	securitySbomExportCmd.Flags().String("format", "cyclonedx", "Document format: cyclonedx or csv")
	securitySbomExportCmd.Flags().String("output-file", "", "Where to write the document; the platform's file name in the current directory by default, - for standard output")
	securitySbomExportCmd.Flags().Bool("force", false, "Overwrite the target file if it already exists")
}
