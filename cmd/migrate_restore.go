package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
	"ankra/internal/migrate"
)

// The restore lane is the one part of 'ankra migrate' that talks to the
// platform: an export directory travels into the cluster through a backup
// vault. The CLI uploads each dump straight to the vault's bucket with the
// presigned URLs the platform mints, asks the platform to verify they
// arrived, and the platform runs the restore inside the cluster - so the
// data never passes through Ankra, and nothing on this machine needs
// kubectl or a database client.

var (
	migrateRestoreCluster string
	migrateRestoreStack   string
	migrateRestoreVault   string

	migrateRestoreStatusVault string

	migrateDataModule    string
	migrateDataOut       string
	migrateDataNamespace string
	migrateDataOptions   []string
	migrateDataForce     bool
	migrateDataCluster   string
	migrateDataStack     string
	migrateDataVault     string
)

// migrateRestorePollInterval is how often --wait re-reads the import; tests
// shorten it.
var migrateRestorePollInterval = 5 * time.Second

var migrateRestoreCmd = &cobra.Command{
	Use:   "restore <export-dir>",
	Short: "Load an exported deployment's databases into a cluster through a backup vault",
	Long: `Load the databases 'ankra migrate export' dumped into the cluster that now
runs the converted deployment. The export directory's manifest says where
each dump belongs - the Service and Secret 'ankra migrate convert' generated
for the database - so nothing has to be looked up by hand.

The dumps are uploaded straight to the organisation's backup vault with
presigned URLs, the platform verifies every object arrived intact, and the
cluster's agent runs the restore inside the cluster: roles and globals
first, then each database with the engine's own tools. Ankra never holds
the data, and this machine needs neither kubectl nor a database client.

Prerequisites: a backup vault in the organisation ('ankra backup vaults
provision' creates one), the converted stack applied to the cluster
('ankra cluster apply'), and a cluster agent that supports data restores.
The vault is picked automatically when the organisation has exactly one
ready vault; pass --vault otherwise.

Pass --wait to follow the restore to its end; --timeout bounds only that
wait, never the upload. Without --wait the command returns once the restore
is running, and 'ankra migrate restore-status <import-id>' reports progress.`,
	Example: `  ankra migrate restore ./app-data --cluster shop --wait
  ankra migrate restore ./app-data --cluster shop --vault backups --stack shop
  ankra migrate restore ./app-data -o json`,
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{annotationRequiresAuth: "true"},
	RunE:        runMigrateRestore,
}

var migrateRestoreStatusCmd = &cobra.Command{
	Use:   "restore-status <import-id>",
	Short: "Report the progress of a restore started by 'ankra migrate restore'",
	Long: `Read an import and the state of its restore jobs, one per database server.
Pass --wait to block until the restore has completed or failed.`,
	Example: `  ankra migrate restore-status 6f1c8e2a-... --vault backups
  ankra migrate restore-status 6f1c8e2a-... --wait`,
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{annotationRequiresAuth: "true"},
	RunE:        runMigrateRestoreStatus,
}

var migrateDataCmd = &cobra.Command{
	Use:   "data [dir]",
	Short: "Export a deployment's databases and restore them into a cluster in one go",
	Long: `Run 'ankra migrate export' on the directory (default: the current one) and
'ankra migrate restore' on the result, back to back: the one command that
moves a Docker deployment's data into the cluster once its workloads are
running there. Every flag of both commands applies. The cluster and the
vault are resolved before anything is dumped, so a wrong target fails fast.

Each dump is a snapshot of a live server. Rehearse while the source is
running, then stop its writers and run it once more for the real cutover.`,
	Example: `  ankra migrate data ./app --cluster shop --wait
  ankra migrate data ./app --option docker-host=ssh://root@203.0.113.7 --option project=aura-office --cluster shop --wait`,
	Args:        cobra.MaximumNArgs(1),
	Annotations: map[string]string{annotationRequiresAuth: "true"},
	RunE:        runMigrateData,
}

func init() {
	migrateRestoreCmd.Flags().StringVar(&migrateRestoreCluster, "cluster", "", "Cluster to restore into, by name or id (default: the selected cluster)")
	migrateRestoreCmd.Flags().StringVar(&migrateRestoreStack, "stack", "", "Stack the data belongs to (default: derived from the export)")
	migrateRestoreCmd.Flags().StringVar(&migrateRestoreVault, "vault", "", "Backup vault to upload through, by name or id (default: the organisation's only ready vault)")
	registerAsyncWriteFlags(migrateRestoreCmd)
	registerStructuredOutputFlags(migrateRestoreCmd)
	migrateCmd.AddCommand(migrateRestoreCmd)

	migrateRestoreStatusCmd.Flags().StringVar(&migrateRestoreStatusVault, "vault", "", "Backup vault the import went through, by name or id (default: the organisation's only ready vault)")
	registerAsyncWriteFlags(migrateRestoreStatusCmd)
	registerStructuredOutputFlags(migrateRestoreStatusCmd)
	migrateCmd.AddCommand(migrateRestoreStatusCmd)

	migrateDataCmd.Flags().StringVar(&migrateDataModule, "module", "", "Module to use (default: the most confident detection)")
	migrateDataCmd.Flags().StringVar(&migrateDataOut, "out", "ankra-migration-data", "Output directory for the export")
	migrateDataCmd.Flags().StringVar(&migrateDataNamespace, "namespace", "", "Namespace the converted workloads run in (default: the directory name, as for convert)")
	migrateDataCmd.Flags().StringArrayVar(&migrateDataOptions, "option", nil, "Module option as key=value (repeatable)")
	migrateDataCmd.Flags().BoolVar(&migrateDataForce, "force", false, "Overwrite an output directory that is not empty")
	migrateDataCmd.Flags().StringVar(&migrateDataCluster, "cluster", "", "Cluster to restore into, by name or id (default: the selected cluster)")
	migrateDataCmd.Flags().StringVar(&migrateDataStack, "stack", "", "Stack the data belongs to (default: derived from the export)")
	migrateDataCmd.Flags().StringVar(&migrateDataVault, "vault", "", "Backup vault to upload through, by name or id (default: the organisation's only ready vault)")
	registerAsyncWriteFlags(migrateDataCmd)
	registerStructuredOutputFlags(migrateDataCmd)
	migrateCmd.AddCommand(migrateDataCmd)
}

// migrateRestoreTargets is where a restore goes, resolved once up front so
// a wrong cluster or an ambiguous vault fails before any work is done.
type migrateRestoreTargets struct {
	clusterID   string
	clusterName string
	vaultID     string
	stackName   string
}

// resolveMigrateRestoreTargets turns the --cluster / --vault / --stack flags
// into ids; an empty stack is derived later from the export.
func resolveMigrateRestoreTargets(clusterFlag string, vaultFlag string, stackFlag string) (migrateRestoreTargets, error) {
	clusterID, clusterName, err := resolveClusterForCmd(clusterFlag)
	if err != nil {
		return migrateRestoreTargets{}, err
	}
	vaultID, err := resolveImportVaultID(vaultFlag)
	if err != nil {
		return migrateRestoreTargets{}, err
	}
	return migrateRestoreTargets{clusterID: clusterID, clusterName: clusterName, vaultID: vaultID, stackName: stackFlag}, nil
}

func runMigrateRestore(cmd *cobra.Command, args []string) error {
	exportDir, err := migrateSourceDir(args)
	if err != nil {
		return err
	}
	wait, err := asyncWriteWaitFlag(cmd)
	if err != nil {
		return err
	}
	targets, err := resolveMigrateRestoreTargets(migrateRestoreCluster, migrateRestoreVault, migrateRestoreStack)
	if err != nil {
		return err
	}
	imported, err := performMigrateRestore(cmd, exportDir, targets, wait)
	if err != nil {
		return err
	}
	if handled, err := renderStructured(cmd, imported); handled || err != nil {
		return err
	}
	printMigrateRestoreOutcome(cmd, imported, wait)
	return nil
}

func runMigrateRestoreStatus(cmd *cobra.Command, args []string) error {
	wait, err := asyncWriteWaitFlag(cmd)
	if err != nil {
		return err
	}
	vaultID, err := resolveImportVaultID(migrateRestoreStatusVault)
	if err != nil {
		return err
	}
	var imported *client.BackupVaultImport
	if wait {
		imported, err = waitForMigrateRestore(cmd, vaultID, args[0])
	} else {
		imported, err = apiClient.GetBackupVaultImport(vaultID, args[0])
		if err != nil {
			err = backupLaneError("reading the import", err)
		}
	}
	if err != nil {
		return err
	}
	if handled, err := renderStructured(cmd, imported); handled || err != nil {
		return err
	}
	printMigrateRestoreOutcome(cmd, imported, wait)
	return nil
}

func runMigrateData(cmd *cobra.Command, args []string) error {
	dir, err := migrateSourceDir(args)
	if err != nil {
		return err
	}
	wait, err := asyncWriteWaitFlag(cmd)
	if err != nil {
		return err
	}
	targets, err := resolveMigrateRestoreTargets(migrateDataCluster, migrateDataVault, migrateDataStack)
	if err != nil {
		return err
	}

	exported, err := performMigrateExport(cmd, dir, migrateExportRequest{
		Module: migrateDataModule, Out: migrateDataOut, Namespace: migrateDataNamespace,
		Options: migrateDataOptions, Force: migrateDataForce,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Exported %d database server(s) to %s\n", len(exported.Manifest.Databases), exported.Out)

	imported, err := performMigrateRestore(cmd, exported.Out, targets, wait)
	if err != nil {
		return err
	}
	if handled, err := renderStructured(cmd, imported); handled || err != nil {
		return err
	}
	printMigrateRestoreOutcome(cmd, imported, wait)
	return nil
}

// performMigrateRestore registers the export, uploads its artifacts, has the
// platform verify them, dispatches the restore, and - with wait - follows it
// to a terminal state. The uploads run under the command's own context: a
// dump takes as long as it takes, and only the wait is bounded by --timeout.
func performMigrateRestore(cmd *cobra.Command, exportDir string, targets migrateRestoreTargets, wait bool) (*client.BackupVaultImport, error) {
	manifest, err := migrate.ReadExportManifest(exportDir)
	if err != nil {
		return nil, withExitCode(exitUsage, fmt.Errorf("%s is not an export directory: %w", exportDir, err))
	}
	rawManifest, err := os.ReadFile(filepath.Join(exportDir, migrate.ExportManifestFileName))
	if err != nil {
		return nil, err
	}
	stackName := targets.stackName
	if stackName == "" {
		stackName = migrateResourceName(filepath.Base(exportSourceDir(manifest, exportDir)))
	}
	for _, database := range manifest.Databases {
		for _, artifact := range database.Artifacts {
			if artifact.SizeBytes > client.ImportArtifactMaximumBytes {
				return nil, withExitCode(exitUsage, fmt.Errorf("artifact %s is %s; an upload carries at most %s",
					artifact.Path, formatByteSize(artifact.SizeBytes), formatByteSize(client.ImportArtifactMaximumBytes)))
			}
		}
	}

	progress := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(progress, "Registering the import for cluster %s\n", targets.clusterName)
	created, err := apiClient.CreateBackupVaultImport(targets.vaultID, client.CreateBackupVaultImportRequest{
		ClusterID: targets.clusterID, StackName: stackName, Manifest: rawManifest,
	})
	if err != nil {
		return nil, backupLaneError("registering the import", err)
	}

	for _, upload := range created.Uploads {
		localPath := filepath.Join(exportDir, filepath.FromSlash(upload.Path))
		info, statError := os.Stat(localPath)
		if statError != nil {
			return nil, fmt.Errorf("artifact %s is missing from %s: %w", upload.Path, exportDir, statError)
		}
		if info.Size() != upload.SizeBytes {
			return nil, fmt.Errorf("artifact %s is %d bytes but the manifest recorded %d; run `ankra migrate export` again", upload.Path, info.Size(), upload.SizeBytes)
		}
		if upload.Method == client.BackupVaultImportUploadMethodMultipart {
			_, _ = fmt.Fprintf(progress, "Uploading %s (%s, %d parts)\n", upload.Path, formatByteSize(info.Size()), len(upload.Parts))
		} else {
			_, _ = fmt.Fprintf(progress, "Uploading %s (%s)\n", upload.Path, formatByteSize(info.Size()))
		}
		if uploadError := uploadMigrateArtifact(cmd.Context(), localPath, upload, info.Size()); uploadError != nil {
			return nil, fmt.Errorf("uploading %s: %w", upload.Path, uploadError)
		}
	}

	_, _ = fmt.Fprintln(progress, "Verifying the upload")
	completed, err := apiClient.CompleteBackupVaultImport(targets.vaultID, created.Import.ID)
	if err != nil {
		return nil, backupLaneError("verifying the import", err)
	}
	_, _ = fmt.Fprintf(progress, "Restoring %d database server(s) into cluster %s\n", len(completed.Databases), targets.clusterName)
	restoring, err := apiClient.RestoreBackupVaultImport(targets.vaultID, created.Import.ID)
	if err != nil {
		return nil, backupLaneError("starting the restore", err)
	}
	if !wait {
		return restoring, nil
	}
	return waitForMigrateRestore(cmd, targets.vaultID, restoring.ID)
}

func uploadMigrateArtifact(ctx context.Context, localPath string, upload client.BackupVaultImportUpload, size int64) error {
	file, openError := os.Open(localPath)
	if openError != nil {
		return openError
	}
	defer func() { _ = file.Close() }()
	return apiClient.UploadPresignedObject(ctx, upload, file, size)
}

// exportSourceDir is where the export was taken from, when the manifest
// remembers it; the export directory itself otherwise.
func exportSourceDir(manifest migrate.ExportManifest, exportDir string) string {
	if manifest.SourceDir != "" {
		return manifest.SourceDir
	}
	return exportDir
}

// resolveImportVaultID picks the vault by name or id, or - with nothing
// given - the organisation's only ready vault, which is the common case.
func resolveImportVaultID(reference string) (string, error) {
	if reference != "" {
		return resolveBackupVaultID(apiClient, reference)
	}
	listing, listError := apiClient.ListBackupVaults()
	if listError != nil {
		return "", backupLaneError("listing backup vaults", listError)
	}
	ready := []client.BackupVault{}
	for _, vault := range listing.Items {
		if vault.Status == "ready" {
			ready = append(ready, vault)
		}
	}
	switch len(ready) {
	case 1:
		return ready[0].ID, nil
	case 0:
		return "", withExitCode(exitUsage, fmt.Errorf("the organisation has no ready backup vault; create one with `ankra backup vaults provision`, or pass --vault"))
	default:
		names := make([]string, 0, len(ready))
		for _, vault := range ready {
			names = append(names, vault.Name)
		}
		return "", withExitCode(exitUsage, fmt.Errorf("the organisation has %d ready backup vaults (%s); pass --vault to choose one", len(ready), strings.Join(names, ", ")))
	}
}

// waitForMigrateRestore re-reads the import until it completes or fails,
// narrating each step's state change, within the --timeout budget.
func waitForMigrateRestore(cmd *cobra.Command, vaultID string, importID string) (*client.BackupVaultImport, error) {
	waitContext, cancel, contextError := asyncWriteRequestContext(cmd)
	if contextError != nil {
		return nil, contextError
	}
	defer cancel()
	progress := cmd.ErrOrStderr()
	lastSeen := map[string]string{}
	for {
		current, getError := apiClient.GetBackupVaultImport(vaultID, importID)
		if getError != nil {
			return nil, backupLaneError("reading the import", getError)
		}
		if current.Restore != nil {
			for _, step := range current.Restore.Steps {
				if lastSeen[step.StepID] != step.Status {
					lastSeen[step.StepID] = step.Status
					_, _ = fmt.Fprintf(progress, "  %s: %s\n", step.Workload, step.Status)
				}
			}
		}
		switch current.Status {
		case client.BackupVaultImportStatusCompleted:
			return current, nil
		case client.BackupVaultImportStatusFailed:
			return current, migrateRestoreFailure(current)
		case client.BackupVaultImportStatusRestoring:
		case client.BackupVaultImportStatusUploading, client.BackupVaultImportStatusUploaded:
			return current, fmt.Errorf("import %s is %s; no restore is running for it", current.ID, current.Status)
		default:
			return current, fmt.Errorf("import %s reports status %q, which this CLI does not know; upgrade the CLI", current.ID, current.Status)
		}
		select {
		case <-waitContext.Done():
			return current, asyncWriteError("waiting for the restore", true, waitContext.Err())
		case <-time.After(migrateRestorePollInterval):
		}
	}
}

func migrateRestoreFailure(imported *client.BackupVaultImport) error {
	reason := "see the restore job's log in the cluster"
	if imported.ErrorExcerpt != nil && *imported.ErrorExcerpt != "" {
		reason = *imported.ErrorExcerpt
	}
	return fmt.Errorf("the restore of import %s failed: %s", imported.ID, reason)
}

func printMigrateRestoreOutcome(cmd *cobra.Command, imported *client.BackupVaultImport, waited bool) {
	out := cmd.OutOrStdout()
	if !waited && imported.Status == client.BackupVaultImportStatusRestoring {
		_, _ = fmt.Fprintf(out, "Restore started (import %s).\n", imported.ID)
		_, _ = fmt.Fprintf(out, "The cluster's agent is loading the data in the background. Follow it with:\n  ankra migrate restore-status %s --wait\n", imported.ID)
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"WORKLOAD", "ENGINE", "TARGET", "STATUS"})
	stepStatus := map[string]client.BackupVaultImportRestoreStep{}
	if imported.Restore != nil {
		for _, step := range imported.Restore.Steps {
			stepStatus[step.Workload] = step
		}
	}
	for _, database := range imported.Databases {
		status := imported.Status
		if step, ok := stepStatus[database.Workload]; ok {
			status = step.Status
			if step.ErrorExcerpt != "" {
				status += ": " + step.ErrorExcerpt
			}
		}
		target := fmt.Sprintf("%s/%s:%d", database.Target.Namespace, database.Target.Host, database.Target.Port)
		writer.AppendRow(table.Row{database.Workload, database.Engine, target, status})
	}
	writer.Render()
	switch imported.Status {
	case client.BackupVaultImportStatusCompleted:
		_, _ = fmt.Fprintf(out, "Restore complete: %d database server(s) loaded (import %s).\n", len(imported.Databases), imported.ID)
		_, _ = fmt.Fprintf(out, "The dumps stay in the vault so the import can be restored again; remove them with:\n  ankra migrate imports delete %s\n", imported.ID)
	case client.BackupVaultImportStatusRestoring:
		_, _ = fmt.Fprintf(out, "Restore still running (import %s).\n", imported.ID)
	default:
		_, _ = fmt.Fprintf(out, "Import %s is %s.\n", imported.ID, imported.Status)
	}
}
