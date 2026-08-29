package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
	"ankra/internal/migrate"
)

// 'ankra migrate up' is the one-command migration: everything the separate
// verbs do, in the order a careful operator would run them, with every
// check that can be made up front made before anything is written.

var (
	migrateUpModule     string
	migrateUpOut        string
	migrateUpNamespace  string
	migrateUpStack      string
	migrateUpCluster    string
	migrateUpVault      string
	migrateUpOptions    []string
	migrateUpForce      bool
	migrateUpNoData     bool
	migrateUpStopSource bool
	migrateUpYes        bool
	migrateUpPlanOnly   bool
)

// migrateUpPodPollInterval is how often the database workloads are checked
// for readiness; tests shorten it.
var migrateUpPodPollInterval = 5 * time.Second

var migrateUpCmd = &cobra.Command{
	Use:   "up [dir]",
	Short: "Move a deployment into a cluster - convert, deploy and carry its data over - in one command",
	Long: `Move the deployment described in a directory (default: the current one)
into a cluster that Ankra runs, end to end:

  1. Plan.    Detect the source, find every database it runs and its size,
              resolve the cluster, the stack and the backup vault, and say
              what the migration will and will not carry. Nothing is
              touched until the plan checks out; --plan stops here.
  2. Convert. Turn the deployment into a stack (cluster.yaml plus the
              manifests) under <out>/stack, as 'ankra migrate convert' does.
  3. Deploy.  Apply the stack to the cluster and wait for its database
              workloads to be running.
  4. Export.  Dump every database from the source, as 'ankra migrate
              export' does, into <out>/data. With --stop-source the
              source's other services are stopped first, so the dump is
              the last word on the data: that is the cutover.
  5. Restore. Upload the dumps through the backup vault and load them
              into the cluster, as 'ankra migrate restore' does, and wait.

The command is safe to run more than once: the stack is re-applied, the
databases are dumped and restored again. Rehearse while the source runs,
then run it once more with --stop-source when you are ready to switch.
--timeout bounds each waiting step (deploy, readiness, restore).

Not carried: files kept in the volumes of non-database workloads, and data
in engines the export does not dump (Redis, MongoDB, search indexes). The
plan names every such item so nothing is left behind unnoticed.`,
	Example: `  ankra migrate up ./app --cluster shop --plan
  ankra migrate up ./app --cluster shop --option ingress.app=shop.example.com --option cluster-issuer=letsencrypt-prod
  ankra migrate up ./app --cluster shop --stop-source --yes
  ankra migrate up ./app --cluster shop --option docker-host=ssh://root@203.0.113.7`,
	Args:        cobra.MaximumNArgs(1),
	Annotations: map[string]string{annotationRequiresAuth: "true"},
	RunE:        runMigrateUp,
}

func init() {
	migrateUpCmd.Flags().StringVar(&migrateUpCluster, "cluster", "", "Cluster to migrate into, by name or id (default: the selected cluster)")
	migrateUpCmd.Flags().StringVar(&migrateUpVault, "vault", "", "Backup vault the data goes through, by name or id (default: the organisation's only ready vault)")
	migrateUpCmd.Flags().StringVar(&migrateUpModule, "module", "", "Module to use (default: the most confident detection)")
	migrateUpCmd.Flags().StringVar(&migrateUpOut, "out", "ankra-migration", "Output directory: <out>/stack for the conversion, <out>/data for the dumps")
	migrateUpCmd.Flags().StringVar(&migrateUpStack, "stack", "", "Name of the stack on the cluster (default: the directory name)")
	migrateUpCmd.Flags().StringVar(&migrateUpNamespace, "namespace", "", "Namespace the workloads run in (default: the stack name)")
	migrateUpCmd.Flags().StringArrayVar(&migrateUpOptions, "option", nil, "Module option as key=value (repeatable); convert and export options both apply")
	migrateUpCmd.Flags().BoolVar(&migrateUpForce, "force", false, "Overwrite an output directory that is not empty")
	migrateUpCmd.Flags().BoolVar(&migrateUpNoData, "no-data", false, "Deploy the workloads only; carry no data over")
	migrateUpCmd.Flags().BoolVar(&migrateUpStopSource, "stop-source", false, "Stop the source's non-database services before the export, so the dump is final (the cutover)")
	migrateUpCmd.Flags().BoolVarP(&migrateUpYes, "yes", "y", false, "Skip the confirmation prompt")
	migrateUpCmd.Flags().BoolVar(&migrateUpPlanOnly, "plan", false, "Print the plan and stop; change nothing")
	registerAsyncWriteFlags(migrateUpCmd)
	_ = migrateUpCmd.Flags().MarkHidden("wait")
	registerStructuredOutputFlags(migrateUpCmd)
	migrateCmd.AddCommand(migrateUpCmd)
}

// migrateUpPlan is everything the command established before changing
// anything.
type migrateUpPlan struct {
	Module      string              `json:"module" yaml:"module"`
	Dir         string              `json:"dir" yaml:"dir"`
	Out         string              `json:"out" yaml:"out"`
	ClusterID   string              `json:"cluster_id" yaml:"cluster_id"`
	ClusterName string              `json:"cluster_name" yaml:"cluster_name"`
	Stack       string              `json:"stack" yaml:"stack"`
	StackExists bool                `json:"stack_exists" yaml:"stack_exists"`
	Namespace   string              `json:"namespace" yaml:"namespace"`
	VaultID     string              `json:"vault_id,omitempty" yaml:"vault_id,omitempty"`
	Data        *migrate.ExportPlan `json:"data,omitempty" yaml:"data,omitempty"`
	// EstimatedBytes is the source's own size of everything the export
	// carries; the dumps compress, so it is a ceiling.
	EstimatedBytes int64 `json:"estimated_bytes" yaml:"estimated_bytes"`
	// FreeBytes is the space left where the dumps go; zero when unknown.
	FreeBytes int64    `json:"free_bytes" yaml:"free_bytes"`
	Warnings  []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// migrateUpSummary is the structured shape of a completed migration.
type migrateUpSummary struct {
	Plan    migrateUpPlan             `json:"plan" yaml:"plan"`
	Convert migrateConvertSummary     `json:"convert" yaml:"convert"`
	Quiesce *migrate.Quiesce          `json:"quiesce,omitempty" yaml:"quiesce,omitempty"`
	Import  *client.BackupVaultImport `json:"import,omitempty" yaml:"import,omitempty"`
	URLs    []string                  `json:"urls,omitempty" yaml:"urls,omitempty"`
}

func runMigrateUp(cmd *cobra.Command, args []string) error {
	_ = cmd.Flags().Set("wait", "true")
	dir, err := migrateSourceDir(args)
	if err != nil {
		return err
	}
	options, err := parseMigrateOptions(migrateUpOptions)
	if err != nil {
		return err
	}
	module, err := selectMigrateModule(cmd, newMigrateRegistry(), dir, migrateUpModule)
	if err != nil {
		return err
	}
	_, hasExport := migrate.ExporterFor(module)
	carryData := !migrateUpNoData && hasExport
	if migrateUpStopSource && !carryData {
		return withExitCode(exitUsage, errors.New("--stop-source only makes sense when data is carried over"))
	}
	quiescer, canQuiesce := module.(migrate.SourceQuiescer)
	if migrateUpStopSource && !canQuiesce {
		return withExitCode(exitUsage, fmt.Errorf("the %s module cannot stop the source's services; stop them yourself, then run without --stop-source", module.Describe().Name))
	}

	plan, err := planMigrateUp(cmd, dir, module, options, carryData)
	if err != nil {
		return err
	}
	if migrateUpPlanOnly {
		if handled, err := renderStructured(cmd, plan); handled || err != nil {
			return err
		}
		printMigrateUpPlan(cmd, plan, carryData)
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Plan only; nothing was changed. Run again without --plan to migrate.")
		return nil
	}
	if !structuredOutputRequested(cmd) {
		printMigrateUpPlan(cmd, plan, carryData)
	}
	if confirmError := confirmMigrateUp(cmd, plan, carryData); confirmError != nil {
		return confirmError
	}

	progress := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(progress, "\n==> Converting %s\n", dir)
	convertSummary, _, err := performMigrateConvert(dir, migrateConvertRequest{
		Module: module, ClusterName: plan.ClusterName, Namespace: plan.Namespace, Options: options,
		Out: filepath.Join(plan.Out, "stack"), Force: true,
	})
	if err != nil {
		return err
	}
	if len(convertSummary.Cluster.Spec.Stacks) > 0 {
		plan.Stack = convertSummary.Cluster.Spec.Stacks[0].Name
	}
	_, _ = fmt.Fprintf(progress, "Wrote %d file(s) to %s\n", len(convertSummary.Files), convertSummary.Out)
	printMigrateWarnings(cmd, convertSummary.Warnings)

	_, _ = fmt.Fprintf(progress, "\n==> Deploying stack %s to cluster %s\n", plan.Stack, plan.ClusterName)
	if applyError := deployMigrateUpStack(cmd, filepath.Join(convertSummary.Out, "cluster.yaml")); applyError != nil {
		return applyError
	}

	summary := migrateUpSummary{Plan: plan, Convert: convertSummary, URLs: migrateUpURLs(options)}
	if carryData {
		workloads := make([]string, 0, len(plan.Data.Databases))
		for _, server := range plan.Data.Databases {
			workloads = append(workloads, server.Workload)
		}
		_, _ = fmt.Fprintf(progress, "\n==> Waiting for %s to run in namespace %s\n", strings.Join(workloads, ", "), plan.Namespace)
		if waitError := waitForMigrateUpWorkloads(cmd, plan.ClusterID, plan.Namespace, workloads); waitError != nil {
			return waitError
		}

		exportRequest := migrate.ExportRequest{Dir: dir, Namespace: plan.Namespace, Options: options}
		if migrateUpStopSource {
			_, _ = fmt.Fprintln(progress, "\n==> Stopping the source's services")
			quiesce, quiesceError := quiescer.QuiesceSource(cmd.Context(), exportRequest)
			if quiesceError != nil {
				return quiesceError
			}
			summary.Quiesce = &quiesce
			if len(quiesce.Stopped) == 0 {
				_, _ = fmt.Fprintln(progress, "No running service besides the databases; nothing to stop.")
			} else {
				_, _ = fmt.Fprintf(progress, "Stopped %s. To bring them back: %s\n", strings.Join(quiesce.Stopped, ", "), quiesce.Resume)
			}
		}

		_, _ = fmt.Fprintf(progress, "\n==> Exporting the data of %s\n", dir)
		exported, exportError := performMigrateExport(cmd, dir, migrateExportRequest{
			Module: module.Describe().Name, Out: filepath.Join(plan.Out, "data"), Namespace: plan.Namespace,
			Options: migrateUpOptions, Force: true,
		})
		if exportError != nil {
			return exportError
		}
		_, _ = fmt.Fprintf(progress, "Exported %d database server(s) to %s\n", len(exported.Manifest.Databases), exported.Out)

		_, _ = fmt.Fprintf(progress, "\n==> Restoring into cluster %s\n", plan.ClusterName)
		imported, restoreError := performMigrateRestore(cmd, exported.Out, migrateRestoreTargets{
			clusterID: plan.ClusterID, clusterName: plan.ClusterName, vaultID: plan.VaultID, stackName: plan.Stack,
		}, true)
		if restoreError != nil {
			return restoreError
		}
		summary.Import = imported
	}

	if handled, err := renderStructured(cmd, summary); handled || err != nil {
		return err
	}
	printMigrateUpOutcome(cmd, summary, carryData)
	return nil
}

// planMigrateUp establishes everything the migration needs before it
// changes anything: the source's databases and their sizes, the space to
// dump them into, and the cluster, stack and vault they go to.
func planMigrateUp(cmd *cobra.Command, dir string, module migrate.Module, options map[string]string, carryData bool) (migrateUpPlan, error) {
	out, err := filepath.Abs(migrateUpOut)
	if err != nil {
		return migrateUpPlan{}, withExitCode(exitUsage, err)
	}
	stackName := migrateUpStack
	if stackName == "" {
		stackName = migrateResourceName(filepath.Base(dir))
	}
	namespace := migrateUpNamespace
	if namespace == "" {
		namespace = stackName
	}
	plan := migrateUpPlan{Module: module.Describe().Name, Dir: dir, Out: out, Stack: stackName, Namespace: namespace}

	clusterID, clusterName, err := resolveClusterForCmd(migrateUpCluster)
	if err != nil {
		return migrateUpPlan{}, err
	}
	if isUUIDLike(clusterName) {
		clusterName, err = clusterNameForID(clusterID)
		if err != nil {
			return migrateUpPlan{}, err
		}
	}
	plan.ClusterID, plan.ClusterName = clusterID, clusterName
	stacks, err := apiClient.ListClusterStacks(clusterID)
	if err != nil {
		return migrateUpPlan{}, fmt.Errorf("listing the stacks of cluster %s: %w", clusterName, err)
	}
	for _, stack := range stacks {
		plan.StackExists = plan.StackExists || stack.Name == stackName
	}

	if carryData {
		vaultID, vaultError := resolveImportVaultID(migrateUpVault)
		if vaultError != nil {
			return migrateUpPlan{}, vaultError
		}
		plan.VaultID = vaultID
		if planner, ok := module.(migrate.ExportPlanner); ok {
			exportPlan, planError := planner.PlanExport(cmd.Context(), migrate.ExportRequest{Dir: dir, Namespace: namespace, Options: options})
			if planError != nil {
				return migrateUpPlan{}, planError
			}
			plan.Data = &exportPlan
			for _, server := range exportPlan.Databases {
				plan.EstimatedBytes += server.EstimatedBytes()
				for _, database := range server.Databases {
					if database.SizeBytes > client.PresignedUploadMaximumBytes {
						plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: database %s is %s on the source; a dump above %s cannot be uploaded in one piece and would be refused",
							server.Workload, database.Name, formatByteSize(database.SizeBytes), formatByteSize(client.PresignedUploadMaximumBytes)))
					}
				}
			}
			plan.Warnings = append(plan.Warnings, exportPlan.Warnings...)
		} else {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("the %s module does not describe its export up front; sizes are unknown until the dump runs", module.Describe().Name))
		}
		if free, ok := freeDiskBytes(nearestExistingDir(out)); ok {
			plan.FreeBytes = free
			if plan.EstimatedBytes > free {
				return migrateUpPlan{}, withExitCode(exitUsage, fmt.Errorf("the source holds about %s of data but only %s is free at %s; pass --out to a larger volume",
					formatByteSize(plan.EstimatedBytes), formatByteSize(free), out))
			}
		}
	}
	// The output directory is the first thing the migration changes, so it
	// is created only once every check above has passed.
	if !migrateUpPlanOnly {
		if outError := ensureMigrateOutDir(out, migrateUpForce); outError != nil {
			return migrateUpPlan{}, outError
		}
	}
	return plan, nil
}

// nearestExistingDir walks up from path to the first directory that exists,
// which is where a not-yet-created output directory would take its space.
func nearestExistingDir(path string) string {
	for current := path; ; current = filepath.Dir(current) {
		if _, statError := os.Stat(current); statError == nil {
			return current
		}
		if filepath.Dir(current) == current {
			return current
		}
	}
}

func isUUIDLike(value string) bool {
	return len(value) == 36 && strings.Count(value, "-") == 4
}

// clusterNameForID finds the name of a cluster given by id: the stack is
// applied under the cluster's name, so an id on the command line is not
// enough.
func clusterNameForID(clusterID string) (string, error) {
	const pageSize = 100
	for page := 1; page <= 50; page++ {
		response, listError := apiClient.ListClusters(page, pageSize)
		if listError != nil {
			return "", fmt.Errorf("listing clusters: %w", listError)
		}
		for _, cluster := range response.Result {
			if cluster.ID == clusterID {
				return cluster.Name, nil
			}
		}
		if len(response.Result) < pageSize {
			break
		}
	}
	return "", withExitCode(exitNotFound, fmt.Errorf("no cluster with id %s in this organisation", clusterID))
}

func printMigrateUpPlan(cmd *cobra.Command, plan migrateUpPlan, carryData bool) {
	out := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(out, "Migration plan for %s (%s module)\n", plan.Dir, plan.Module)
	stackState := "new"
	if plan.StackExists {
		stackState = "exists, will be updated"
	}
	_, _ = fmt.Fprintf(out, "  cluster    %s\n", plan.ClusterName)
	_, _ = fmt.Fprintf(out, "  stack      %s (%s), namespace %s\n", plan.Stack, stackState, plan.Namespace)
	_, _ = fmt.Fprintf(out, "  output     %s\n", plan.Out)
	if !carryData {
		_, _ = fmt.Fprintln(out, "  data       not carried (--no-data, or the module cannot export)")
	} else if plan.Data != nil {
		for _, server := range plan.Data.Databases {
			names := make([]string, 0, len(server.Databases))
			for _, database := range server.Databases {
				if database.SizeBytes > 0 {
					names = append(names, fmt.Sprintf("%s (%s)", database.Name, formatByteSize(database.SizeBytes)))
				} else {
					names = append(names, database.Name)
				}
			}
			version := server.ServerVersion
			if version != "" {
				version = " " + version
			}
			_, _ = fmt.Fprintf(out, "  data       %s: %s%s as %s - %s\n", server.Workload, server.Engine, version, server.Username, strings.Join(names, ", "))
		}
		free := "free space unknown"
		if plan.FreeBytes > 0 {
			free = formatByteSize(plan.FreeBytes) + " free"
		}
		_, _ = fmt.Fprintf(out, "  disk       about %s to dump, %s at %s\n", formatByteSize(plan.EstimatedBytes), free, plan.Out)
		if len(plan.Data.NotCarried) > 0 {
			_, _ = fmt.Fprintln(out, "  not carried:")
			for _, item := range plan.Data.NotCarried {
				_, _ = fmt.Fprintf(out, "    - %s\n", item)
			}
		}
	}
	if len(plan.Warnings) > 0 {
		_, _ = fmt.Fprintln(out, "  warnings:")
		for _, warning := range plan.Warnings {
			_, _ = fmt.Fprintf(out, "    - %s\n", warning)
		}
	}
}

// confirmMigrateUp asks before the first change, naming what stopping the
// source will do when that was asked for.
func confirmMigrateUp(cmd *cobra.Command, plan migrateUpPlan, carryData bool) error {
	message := fmt.Sprintf("\nMigrate %s into cluster %s as stack %s", plan.Dir, plan.ClusterName, plan.Stack)
	if carryData {
		message += " and carry its data over"
	}
	if migrateUpStopSource {
		message += "; the source's non-database services will be STOPPED before the final export"
	}
	return confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), message+"? [y/N] ", migrateUpYes)
}

// deployMigrateUpStack applies the converted cluster.yaml and waits for the
// write; a refused configuration is reported the way 'ankra cluster apply'
// reports it.
func deployMigrateUpStack(cmd *cobra.Command, clusterFile string) error {
	importRequest, err := loadImportCluster(clusterFile, false, false)
	if err != nil {
		return err
	}
	requestContext, cancel, err := asyncWriteRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	response, _, err := applyImportCluster(requestContext, importRequest, true)
	if err != nil {
		return err
	}
	if len(response.Errors) > 0 {
		var lines []string
		for _, resourceError := range response.Errors {
			for _, detail := range resourceError.Errors {
				lines = append(lines, fmt.Sprintf("%s %q: %s: %s", resourceError.Kind, resourceError.Name, detail.Key, detail.Message))
			}
		}
		return fmt.Errorf("the platform refused the stack:\n  %s", strings.Join(lines, "\n  "))
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Stack applied to cluster %s; the agent is deploying it.\n", response.Name)
	return nil
}

// waitForMigrateUpWorkloads polls the cluster until every named workload
// has a running, ready pod in the namespace: the restore connects to them,
// so a database still pulling its image must not be handed a dump.
func waitForMigrateUpWorkloads(cmd *cobra.Command, clusterID string, namespace string, workloads []string) error {
	waitContext, cancel, err := asyncWriteRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	progress := cmd.ErrOrStderr()
	lastState := map[string]string{}
	for {
		pending := 0
		for _, workload := range workloads {
			state, isReady, readError := migrateUpWorkloadState(clusterID, namespace, workload)
			if readError != nil {
				return readError
			}
			if lastState[workload] != state {
				lastState[workload] = state
				_, _ = fmt.Fprintf(progress, "  %s: %s\n", workload, state)
			}
			if !isReady {
				pending++
			}
		}
		if pending == 0 {
			return nil
		}
		select {
		case <-waitContext.Done():
			return asyncWriteError("waiting for the database workloads", true, waitContext.Err())
		case <-time.After(migrateUpPodPollInterval):
		}
	}
}

// migrateUpWorkloadState reads the pods of one workload and folds them into
// a state line; ready means at least one pod is running with every
// container ready.
func migrateUpWorkloadState(clusterID string, namespace string, workload string) (string, bool, error) {
	response, listError := apiClient.ListPods(clusterID, &client.ListPodsOptions{Namespace: namespace, NameContains: workload, PageSize: 100})
	if listError != nil {
		return "", false, fmt.Errorf("listing the pods of %s: %w", workload, listError)
	}
	var states []string
	for _, pod := range response.Pods {
		if pod.Name != workload && !strings.HasPrefix(pod.Name, workload+"-") {
			continue
		}
		if pod.Phase == "Running" && podIsReady(pod.Ready) {
			return "running", true, nil
		}
		states = append(states, pod.Phase+" "+pod.Ready)
	}
	if len(states) == 0 {
		return "no pod yet", false, nil
	}
	sort.Strings(states)
	return strings.Join(states, ", "), false, nil
}

// podIsReady reads the "ready/total" column: every container ready, and at
// least one.
func podIsReady(ready string) bool {
	readyCount, total, found := strings.Cut(ready, "/")
	return found && readyCount == total && readyCount != "0" && readyCount != ""
}

// migrateUpURLs lists the hosts the conversion exposed, from the ingress
// options, as URLs.
func migrateUpURLs(options map[string]string) []string {
	scheme := "http://"
	if options["cluster-issuer"] != "" {
		scheme = "https://"
	}
	var urls []string
	for key, value := range options {
		if strings.HasPrefix(key, "ingress.") && value != "" {
			urls = append(urls, scheme+value)
		}
	}
	sort.Strings(urls)
	return urls
}

func printMigrateUpOutcome(cmd *cobra.Command, summary migrateUpSummary, carryData bool) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "\n%s is running on cluster %s as stack %s (namespace %s).\n", summary.Plan.Dir, summary.Plan.ClusterName, summary.Plan.Stack, summary.Plan.Namespace)
	if carryData && summary.Import != nil {
		_, _ = fmt.Fprintf(out, "Data: %d database server(s) restored (import %s).\n", len(summary.Import.Databases), summary.Import.ID)
	}
	if len(summary.URLs) > 0 {
		_, _ = fmt.Fprintf(out, "Reachable at: %s - point the DNS records at the cluster's ingress.\n", strings.Join(summary.URLs, ", "))
	}
	if summary.Plan.Data != nil && len(summary.Plan.Data.NotCarried) > 0 {
		_, _ = fmt.Fprintln(out, "Still on the source (not carried):")
		for _, item := range summary.Plan.Data.NotCarried {
			_, _ = fmt.Fprintf(out, "  - %s\n", item)
		}
	}
	switch {
	case summary.Quiesce != nil && len(summary.Quiesce.Stopped) > 0:
		_, _ = fmt.Fprintf(out, "The source's services are stopped; this was the cutover. To bring them back: %s\n", summary.Quiesce.Resume)
	case carryData:
		_, _ = fmt.Fprintln(out, "This was a rehearsal: the source kept running. When you are ready to switch, run the same command with --stop-source for the final sync.")
	}
}

// structuredOutputRequested reports whether -o json|yaml is in effect, so
// human progress that would pollute the document is skipped.
func structuredOutputRequested(cmd *cobra.Command) bool {
	format, _ := cmd.Flags().GetString("output")
	return format != ""
}
