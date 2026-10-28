package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/migrate"
)

var (
	migrateExportModule    string
	migrateExportOut       string
	migrateExportNamespace string
	migrateExportOptions   []string
	migrateExportForce     bool
)

var migrateExportCmd = &cobra.Command{
	Use:   "export [dir]",
	Short: "Dump the databases behind a deployment into a directory ready to be restored into the cluster",
	Long: `Dump every database the deployment in a directory (default: the current one)
runs - PostgreSQL and MySQL/MariaDB today - into an output directory, with a
manifest.json that says where each dump restores to: the Service that
'ankra migrate convert' generated for the database and the Secret holding its
password.

The dumps are taken through the docker CLI from the running containers, so
the source must be up. A deployment on another host is reached the way docker
itself reaches it:

  --option docker-host=ssh://root@203.0.113.7  dump from a remote Docker daemon
  --option project=<name>                      the compose project name, when it
                                               differs from the directory name
  --option container.<workload>=<name>         dump from a specific container
  --option databases.<workload>=a,b            only these databases (default: all)
  --option profiles=app,dns                    compose profiles, as for convert

Each dump is a snapshot of a live server. Rehearse with the source running,
then stop its writers and export once more right before the final cutover.

The output directory is self-contained: manifest.json describes every
artifact and where it belongs, and SHA256SUMS lets 'sha256sum -c' verify it.
Load it into the cluster with 'ankra migrate restore <out>'.`,
	Example: `  ankra migrate export ./app --out ./app-data
  ankra migrate export ./app --option docker-host=ssh://root@203.0.113.7 --option project=aura-office
  ankra migrate export --option databases.postgres=office,pdns`,
	Args: cobra.MaximumNArgs(1),
	RunE: runMigrateExport,
}

func init() {
	migrateExportCmd.Flags().StringVar(&migrateExportModule, "module", "", "Module to use (default: the most confident detection)")
	migrateExportCmd.Flags().StringVar(&migrateExportOut, "out", "ankra-migration-data", "Output directory")
	migrateExportCmd.Flags().StringVar(&migrateExportNamespace, "namespace", "", "Namespace the converted workloads run in (default: the directory name, as for convert)")
	migrateExportCmd.Flags().StringArrayVar(&migrateExportOptions, "option", nil, "Module option as key=value (repeatable)")
	migrateExportCmd.Flags().BoolVar(&migrateExportForce, "force", false, "Overwrite an output directory that is not empty")
	registerStructuredOutputFlags(migrateExportCmd)
	migrateCmd.AddCommand(migrateExportCmd)
}

// migrateExportSummary is the structured shape of a completed export.
type migrateExportSummary struct {
	Module   string                 `json:"module" yaml:"module"`
	Out      string                 `json:"out" yaml:"out"`
	Manifest migrate.ExportManifest `json:"manifest" yaml:"manifest"`
}

// migrateExportRequest is what an export needs beyond the source directory.
type migrateExportRequest struct {
	Module    string
	Out       string
	Namespace string
	Options   []string
	Force     bool
}

func runMigrateExport(cmd *cobra.Command, args []string) error {
	dir, err := migrateSourceDir(args)
	if err != nil {
		return err
	}
	summary, err := performMigrateExport(cmd, dir, migrateExportRequest{
		Module:    migrateExportModule,
		Out:       migrateExportOut,
		Namespace: migrateExportNamespace,
		Options:   migrateExportOptions,
		Force:     migrateExportForce,
	})
	if err != nil {
		return err
	}
	if handled, err := renderStructured(cmd, summary); handled || err != nil {
		return err
	}
	printMigrateExportSummary(cmd, summary)
	return nil
}

// performMigrateExport dumps the databases behind dir into the output
// directory and writes the manifest; it is the step 'ankra migrate data'
// shares with 'ankra migrate export'.
func performMigrateExport(cmd *cobra.Command, dir string, request migrateExportRequest) (migrateExportSummary, error) {
	options, err := parseMigrateOptions(request.Options)
	if err != nil {
		return migrateExportSummary{}, err
	}
	module, err := selectMigrateModule(cmd, newMigrateRegistry(), dir, request.Module)
	if err != nil {
		return migrateExportSummary{}, err
	}
	moduleName := module.Describe().Name
	exporter, ok := migrate.ExporterFor(module)
	if !ok {
		return migrateExportSummary{}, withExitCode(exitUsage, fmt.Errorf("the %s module converts workloads but does not export data (run `ankra migrate modules` to see which modules do)", moduleName))
	}

	namespace := request.Namespace
	if namespace == "" {
		namespace = migrateResourceName(filepath.Base(dir))
	}
	out, err := filepath.Abs(request.Out)
	if err != nil {
		return migrateExportSummary{}, withExitCode(exitUsage, err)
	}
	if err := ensureMigrateOutDir(out, request.Force); err != nil {
		return migrateExportSummary{}, err
	}

	export, err := exporter.Export(cmd.Context(), migrate.ExportRequest{
		Dir:       dir,
		OutputDir: out,
		Namespace: namespace,
		Options:   options,
		Progress:  cmd.ErrOrStderr(),
	})
	if err != nil {
		return migrateExportSummary{}, fmt.Errorf("%s: %w", moduleName, err)
	}
	manifest, err := migrate.FinaliseExport(out, moduleName, dir, export, time.Now())
	if err != nil {
		return migrateExportSummary{}, fmt.Errorf("%s: %w", moduleName, err)
	}
	if err := migrate.WriteExportManifest(out, manifest); err != nil {
		return migrateExportSummary{}, err
	}
	return migrateExportSummary{Module: moduleName, Out: out, Manifest: manifest}, nil
}

func printMigrateExportSummary(cmd *cobra.Command, summary migrateExportSummary) {
	writer := table.NewWriter()
	writer.SetOutputMirror(cmd.OutOrStdout())
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"WORKLOAD", "ENGINE", "DATABASE", "FILE", "SIZE"})
	artifacts := 0
	for _, database := range summary.Manifest.Databases {
		for _, artifact := range database.Artifacts {
			artifacts++
			label := artifact.Database
			if artifact.Kind == migrate.ArtifactKindGlobals {
				label = "(roles and globals)"
			}
			writer.AppendRow(table.Row{database.Workload, database.Engine + " " + database.ServerVersion, label, artifact.Path, formatByteSize(artifact.SizeBytes)})
		}
	}
	writer.Render()

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote %d artifact(s), %s and %s to %s\n", artifacts, migrate.ExportManifestFileName, migrate.ExportChecksumsFileName, summary.Out)
	for _, database := range summary.Manifest.Databases {
		target := database.Target
		password := "no password"
		if target.PasswordSecret != "" {
			password = fmt.Sprintf("password from Secret %s key %s", target.PasswordSecret, target.PasswordKey)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s restores into %s:%d in namespace %s as %s, %s\n",
			database.Workload, target.Host, target.Port, target.Namespace, target.Username, password)
	}
	printMigrateWarnings(cmd, summary.Manifest.Warnings)
}

func formatByteSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	suffixes := "KMGTPE"
	exponent := 0
	for value >= unit && exponent < len(suffixes) {
		value /= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", value, suffixes[exponent-1])
}
