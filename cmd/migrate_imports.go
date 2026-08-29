package cmd

import (
	"fmt"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The imports a migration leaves in a backup vault are the dumps it
// uploaded: kept so a restore can be run again, removed when they are no
// longer wanted. These verbs list and delete them.

var (
	migrateImportsListVault   string
	migrateImportsDeleteVault string
	migrateImportsDeleteYes   bool
)

var migrateImportsCmd = &cobra.Command{
	Use:   "imports",
	Short: "List and delete the exports uploaded to a backup vault by 'ankra migrate restore'",
	Long: `Every 'ankra migrate restore' (and 'up', and 'data') uploads the export's
dumps into the organisation's backup vault under imports/<import-id>/ and
keeps them there, so the same upload can be restored again. These verbs
show what a vault holds and remove an import - its dumps in the vault and
the record - once it is no longer needed.`,
	Annotations: map[string]string{annotationRequiresAuth: "true"},
}

var migrateImportsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the imports a backup vault holds",
	Example: `  ankra migrate imports list
  ankra migrate imports list --vault backups -o json`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{annotationRequiresAuth: "true"},
	RunE:        runMigrateImportsList,
}

var migrateImportsDeleteCmd = &cobra.Command{
	Use:   "delete <import-id>",
	Short: "Remove an import's dumps from the vault and forget the import",
	Long: `Delete the dumps an import uploaded into the backup vault and hide the
import. An import whose restore is still running is refused: its job is
reading those dumps. Objects the vault cannot be asked to remove are noted
in the audit record; the import is gone either way.`,
	Example: `  ankra migrate imports delete 6f1c8e2a-... --yes
  ankra migrate imports delete 6f1c8e2a-... --vault backups`,
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{annotationRequiresAuth: "true"},
	RunE:        runMigrateImportsDelete,
}

func init() {
	migrateImportsListCmd.Flags().StringVar(&migrateImportsListVault, "vault", "", "Backup vault, by name or id (default: the organisation's only ready vault)")
	registerStructuredOutputFlags(migrateImportsListCmd)
	migrateImportsCmd.AddCommand(migrateImportsListCmd)

	migrateImportsDeleteCmd.Flags().StringVar(&migrateImportsDeleteVault, "vault", "", "Backup vault the import went through, by name or id (default: the organisation's only ready vault)")
	migrateImportsDeleteCmd.Flags().BoolVarP(&migrateImportsDeleteYes, "yes", "y", false, "Skip the confirmation prompt")
	migrateImportsCmd.AddCommand(migrateImportsDeleteCmd)

	migrateCmd.AddCommand(migrateImportsCmd)
}

func runMigrateImportsList(cmd *cobra.Command, _ []string) error {
	vaultID, err := resolveImportVaultID(migrateImportsListVault)
	if err != nil {
		return err
	}
	listing, err := apiClient.ListBackupVaultImports(vaultID)
	if err != nil {
		return backupLaneError("listing the imports", err)
	}
	if handled, err := renderStructured(cmd, listing); handled || err != nil {
		return err
	}
	if len(listing.Imports) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "The vault holds no imports.")
		return nil
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(cmd.OutOrStdout())
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"IMPORT", "STACK", "STATUS", "DATABASES", "SIZE", "CREATED"})
	for _, imported := range listing.Imports {
		workloads := make([]string, 0, len(imported.Databases))
		var totalBytes int64
		for _, database := range imported.Databases {
			workloads = append(workloads, database.Workload)
			for _, artifact := range database.Artifacts {
				totalBytes += artifact.SizeBytes
			}
		}
		writer.AppendRow(table.Row{imported.ID, imported.StackName, imported.Status, strings.Join(workloads, ", "), formatByteSize(totalBytes), imported.CreatedAt})
	}
	writer.Render()
	return nil
}

func runMigrateImportsDelete(cmd *cobra.Command, args []string) error {
	importID := args[0]
	vaultID, err := resolveImportVaultID(migrateImportsDeleteVault)
	if err != nil {
		return err
	}
	imported, err := apiClient.GetBackupVaultImport(vaultID, importID)
	if err != nil {
		return backupLaneError("reading the import", err)
	}
	if imported.Status == client.BackupVaultImportStatusRestoring {
		return withExitCode(exitUsage, fmt.Errorf("import %s is being restored right now; wait for it to finish (ankra migrate restore-status %s --wait) before deleting it", importID, importID))
	}
	message := fmt.Sprintf("Delete import %s (stack %s, %d database server(s)) and its dumps from the vault? [y/N] ", importID, imported.StackName, len(imported.Databases))
	if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), message, migrateImportsDeleteYes); confirmError != nil {
		return confirmError
	}
	if deleteError := apiClient.DeleteBackupVaultImport(vaultID, importID); deleteError != nil {
		return backupLaneError("deleting the import", deleteError)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Import %s deleted; its dumps are removed from the vault.\n", importID)
	return nil
}
