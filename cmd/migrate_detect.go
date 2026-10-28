package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var migrateDetectCmd = &cobra.Command{
	Use:   "detect [dir]",
	Short: "Report which modules recognise a directory, most confident first",
	Long: `Ask every module whether it recognises the directory (default: the current
one). Use it to see what 'ankra migrate convert' would pick, or why it picks
nothing.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runMigrateDetect,
}

func init() {
	registerStructuredOutputFlags(migrateDetectCmd)
	migrateCmd.AddCommand(migrateDetectCmd)
}

// migrateDetectRow is the structured shape of one module's verdict.
type migrateDetectRow struct {
	Module     string   `json:"module" yaml:"module"`
	Confidence float64  `json:"confidence" yaml:"confidence"`
	Files      []string `json:"files,omitempty" yaml:"files,omitempty"`
	Reason     string   `json:"reason,omitempty" yaml:"reason,omitempty"`
	Error      string   `json:"error,omitempty" yaml:"error,omitempty"`
}

func runMigrateDetect(cmd *cobra.Command, args []string) error {
	dir, err := migrateSourceDir(args)
	if err != nil {
		return err
	}

	candidates, notes := newMigrateRegistry().Detect(cmd.Context(), dir)
	for _, note := range notes {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), note)
	}

	rows := make([]migrateDetectRow, 0, len(candidates))
	for _, candidate := range candidates {
		row := migrateDetectRow{
			Module:     candidate.Module.Describe().Name,
			Confidence: candidate.Detection.Confidence,
			Files:      candidate.Detection.Files,
			Reason:     candidate.Detection.Reason,
		}
		if candidate.Err != nil {
			row.Error = candidate.Err.Error()
		}
		rows = append(rows, row)
	}

	if handled, err := renderStructured(cmd, rows); handled || err != nil {
		return err
	}

	writer := table.NewWriter()
	writer.SetOutputMirror(cmd.OutOrStdout())
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"MODULE", "CONFIDENCE", "FILES", "REASON"})
	for _, row := range rows {
		reason := row.Reason
		if row.Error != "" {
			reason = "error: " + row.Error
		}
		writer.AppendRow(table.Row{row.Module, fmt.Sprintf("%.2f", row.Confidence), strings.Join(row.Files, ", "), reason})
	}
	writer.Render()
	return nil
}

// migrateSourceDir resolves the optional directory argument to an absolute
// path that exists.
func migrateSourceDir(args []string) (string, error) {
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", withExitCode(exitUsage, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", withExitCode(exitNotFound, fmt.Errorf("directory %s: %w", dir, err))
	}
	if !info.IsDir() {
		return "", withExitCode(exitUsage, fmt.Errorf("%s is not a directory", dir))
	}
	return absolute, nil
}
