package cmd

import (
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// `ankra backup vaults contents` (bead ankra-0xsdd.72).
//
// Every other backup command reports what Ankra believes. This one reports
// what the bucket holds, which is the only way to check that a delete deleted
// anything: on 2026-09-16 deleting a complete restore point reported success,
// said its objects were being swept, and left all nine of them and 36 MB in
// the bucket. The only reason anyone found out is that the verification went
// around the product and listed the bucket by hand.

var backupVaultsContentsCmd = &cobra.Command{
	Use:   "contents [vault-name|vault-id]",
	Short: "List what a backup vault's bucket actually holds",
	Long: `List what a backup vault's bucket actually holds, per restore point.

This reads the object store, not Ankra's rows, so it can disagree with the
platform - which is the point. Use it to confirm that deleting a restore point
removed its objects, and to find objects no restore point accounts for.

Two prefixes are reported per restore point because they differ:

  declared  the prefix the restore point records, and that a delete sweeps
  located   where the objects actually are, written by the backup data plane

Volume data lives in a SHARED repository per cluster and namespace. Its bytes
are reported separately and never counted into a single restore point: the
blocks are deduplicated across every backup of that namespace, so no restore
point owns them and deleting the prefix would destroy other restore points'
data.

A vault whose bucket cannot be read fails with an error rather than printing an
empty listing. An unreadable vault and an empty vault are different facts.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prefix, _ := cmd.Flags().GetString("prefix")
		restorePoint, _ := cmd.Flags().GetString("restore-point")
		orphansOnly, _ := cmd.Flags().GetBool("orphans-only")
		showObjects, _ := cmd.Flags().GetBool("objects")

		vaultID, resolveError := resolveBackupVaultID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}

		contents, contentsError := apiClient.GetBackupVaultContents(vaultID, client.BackupVaultContentsRequest{
			Prefix: prefix, RestorePointID: restorePoint, IncludeObjects: showObjects,
		})
		if contentsError != nil {
			return backupLaneError("reading backup vault contents", contentsError)
		}
		if rendered, renderError := renderStructured(cmd, contents); rendered || renderError != nil {
			return renderError
		}

		printBackupVaultContents(contents, orphansOnly, showObjects)
		return nil
	},
}

func printBackupVaultContents(contents *client.BackupVaultContents, orphansOnly bool, showObjects bool) {
	fmt.Println("Backup Vault Contents:")
	fmt.Printf("  Vault:    %s\n", contents.VaultName)
	fmt.Printf("  Bucket:   %s\n", contents.Bucket)
	fmt.Printf("  Endpoint: %s\n", contents.Endpoint)
	if contents.ScannedPrefix != "" {
		fmt.Printf("  Prefix:   %s\n", contents.ScannedPrefix)
	}
	fmt.Printf("  Objects:  %d (%s)\n", contents.ObjectCount, formatByteSize(int64(contents.TotalBytes)))
	// The totals above are the headline number a reader takes away, so a
	// partial listing has to qualify them right here rather than rely on a
	// warning further down. Keying this off Complete instead of off Warnings
	// is deliberate: a partial read whose cause the server did not put into
	// words would otherwise print as a full inventory, which is the exact
	// conflation this command exists to remove.
	if !contents.Complete {
		fmt.Println("  PARTIAL:  this listing stopped short - the counts above are a FLOOR, not a total.")
	}

	for _, warning := range contents.Warnings {
		fmt.Printf("\n  ! %s\n", warning)
	}

	if !orphansOnly {
		printVaultRestorePoints(contents.RestorePoints)
		printVaultRepositories(contents.SharedRepositories)
		printVaultOther(contents.Other)
	}
	printVaultOrphans(contents.Orphans, contents.OrphansDetermined)

	if showObjects {
		printVaultObjects(contents.Objects)
	}
}

func printVaultRestorePoints(restorePoints []client.VaultRestorePointContents) {
	fmt.Println("\nRestore points:")
	if len(restorePoints) == 0 {
		fmt.Println("  (none recorded in this vault)")
		return
	}
	idWidth := restorePointIDWidth(restorePoints)
	writer := table.NewWriter()
	writer.SetOutputMirror(os.Stdout)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Restore Point", "Status", "Stacks", "Objects", "Size", "Located Under"})
	for _, restorePoint := range restorePoints {
		writer.AppendRow(table.Row{
			shortRestorePointID(restorePoint.RestorePointID, idWidth),
			restorePoint.Status,
			strings.Join(restorePoint.StackNames, ","),
			restorePoint.ObjectCount,
			formatByteSize(int64(restorePoint.TotalBytes)),
			locatedSummary(restorePoint),
		})
	}
	writer.Render()

	for _, restorePoint := range restorePoints {
		if len(restorePoint.Notes) == 0 {
			continue
		}
		fmt.Printf("\n  %s:\n", shortRestorePointID(restorePoint.RestorePointID, idWidth))
		for _, note := range restorePoint.Notes {
			fmt.Printf("    - %s\n", note)
		}
	}
}

// locatedSummary renders where the objects are, and says so plainly when they
// are nowhere: an empty cell would read as "small", not as "missing".
func locatedSummary(restorePoint client.VaultRestorePointContents) string {
	if len(restorePoint.LocatedPrefixes) == 0 {
		return "(nothing found)"
	}
	prefixes := make([]string, 0, len(restorePoint.LocatedPrefixes))
	for _, usage := range restorePoint.LocatedPrefixes {
		prefixes = append(prefixes, usage.Prefix)
	}
	return strings.Join(prefixes, ", ")
}

func printVaultRepositories(repositories []client.VaultSharedRepository) {
	if len(repositories) == 0 {
		return
	}
	fmt.Println("\nShared repositories (volume data; bytes are not attributable to one restore point):")
	writer := table.NewWriter()
	writer.SetOutputMirror(os.Stdout)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Prefix", "Namespace", "Objects", "Size", "Referenced By"})
	for _, repository := range repositories {
		referencedBy := fmt.Sprintf("%d restore point(s)", len(repository.ReferencedByRestorePoints))
		if repository.Unreferenced {
			referencedBy = "NOTHING"
		}
		writer.AppendRow(table.Row{
			repository.Prefix, repository.Namespace, repository.ObjectCount,
			formatByteSize(int64(repository.TotalBytes)), referencedBy,
		})
	}
	writer.Render()
	for _, repository := range repositories {
		if repository.Note != "" {
			fmt.Printf("\n  %s:\n    - %s\n", repository.Prefix, repository.Note)
		}
	}
}

func printVaultOther(other []client.VaultPrefixUsage) {
	if len(other) == 0 {
		return
	}
	fmt.Println("\nOther contents (not restore points):")
	for _, usage := range other {
		fmt.Printf("  %-56s %5d objects  %s\n", usage.Prefix, usage.ObjectCount,
			formatByteSize(int64(usage.TotalBytes)))
	}
}

func printVaultOrphans(orphans []client.VaultOrphanGroup, determined bool) {
	fmt.Println("\nOrphans (objects no restore point accounts for):")
	if !determined {
		fmt.Println("  (not determined - the listing was partial, see the warnings above)")
		return
	}
	if len(orphans) == 0 {
		fmt.Println("  None. Every object in this vault is accounted for.")
		return
	}
	for _, orphan := range orphans {
		fmt.Printf("\n  %s\n", orphan.Prefix)
		fmt.Printf("    kind:    %s\n", orphan.Kind)
		fmt.Printf("    holds:   %d objects, %s\n", orphan.ObjectCount, formatByteSize(int64(orphan.TotalBytes)))
		if orphan.RestorePointID != "" {
			fmt.Printf("    from:    restore point %s\n", orphan.RestorePointID)
		}
		if orphan.RowDeletedAt != nil {
			fmt.Printf("    deleted: %s\n", formatTimeAgo(orphan.RowDeletedAt.Format("2006-01-02T15:04:05Z07:00")))
		}
		fmt.Printf("    why:     %s\n", orphan.Reason)
	}
}

func printVaultObjects(objects []client.VaultObject) {
	fmt.Println("\nObjects:")
	if len(objects) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, object := range objects {
		fmt.Printf("  %12s  %s\n", formatByteSize(int64(object.SizeBytes)), object.Key)
	}
}

// restorePointIDWidth is the shortest prefix length that tells every restore
// point in THIS listing apart, starting at 8.
//
// A fixed 8 characters is not safe to print beside an invitation to pass the
// value back to --restore-point: two restore points sharing those characters
// render identically, and the id the reader copies is then ambiguous. The
// platform refuses an ambiguous prefix rather than resolving it to the newest
// match, so the failure is loud rather than wrong - but a table that cannot
// distinguish two of its own rows is no good to read either.
func restorePointIDWidth(restorePoints []client.VaultRestorePointContents) int {
	longest := 0
	for _, restorePoint := range restorePoints {
		if len(restorePoint.RestorePointID) > longest {
			longest = len(restorePoint.RestorePointID)
		}
	}
	for width := 8; width < longest; width++ {
		seen := make(map[string]bool, len(restorePoints))
		collided := false
		for _, restorePoint := range restorePoints {
			short := shortRestorePointID(restorePoint.RestorePointID, width)
			if seen[short] {
				collided = true
				break
			}
			seen[short] = true
		}
		if !collided {
			return width
		}
	}
	return longest
}

// shortRestorePointID keeps the table narrow at the width the listing proved
// unambiguous.
func shortRestorePointID(restorePointID string, width int) string {
	if len(restorePointID) <= width {
		return restorePointID
	}
	return restorePointID[:width]
}

func init() {
	backupVaultsContentsCmd.Flags().String("prefix", "",
		"Only read objects under this prefix (narrows a large vault; orphan detection is withheld for a partial read)")
	backupVaultsContentsCmd.Flags().String("restore-point", "",
		"Report only this restore point (accepts an id prefix)")
	backupVaultsContentsCmd.Flags().Bool("orphans-only", false,
		"Print only the objects no restore point accounts for")
	backupVaultsContentsCmd.Flags().Bool("objects", false,
		"List every object key the listing read")

	registerStructuredOutputFlags(backupVaultsContentsCmd)
	backupVaultsCmd.AddCommand(backupVaultsContentsCmd)
}
