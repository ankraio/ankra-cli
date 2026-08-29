package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"ankra/internal/migrate"
)

var (
	migrateConvertModule      string
	migrateConvertOut         string
	migrateConvertClusterName string
	migrateConvertNamespace   string
	migrateConvertOptions     []string
	migrateConvertForce       bool
	migrateConvertDryRun      bool
)

var migrateConvertCmd = &cobra.Command{
	Use:   "convert [dir]",
	Short: "Convert a directory's deployment into an ImportCluster manifest and Kubernetes manifests",
	Long: `Convert the deployment described in a directory (default: the current one)
into an output directory holding cluster.yaml and the manifests it refers to.

The module is chosen by asking each one whether it recognises the directory;
pass --module to choose explicitly. Module-specific settings go through
--option key=value, repeated as needed. For the built-in docker module:

  --option source=compose|dockerfile|daemon   which source to read (default: detected)
  --option profiles=app,dns                   compose profiles to include
  --option all-profiles=true                  include every compose profile
  --option use-environment=true               let your shell satisfy ${VAR} references
  --option project=<name>                     compose project, for source=daemon
  --option containers=a,b                     specific containers, for source=daemon
  --option docker-host=ssh://root@host        a remote daemon, for source=daemon
  --option image.<workload>=<registry/repo:tag>  image for a locally built workload
  --option ingress.<workload>=<host>          expose a workload through an Ingress
  --option cluster-issuer=letsencrypt-prod    request TLS for every Ingress
  --option volume-size=20Gi                   size of every PersistentVolumeClaim
  --option storage-class=<name>               storageClassName for every claim

Read the warnings: they list what the module could not carry over - locally
built images, host directories, unresolved variables, credentials written in
plain text - and each one names the fix.

Then review the output and apply it:

  ankra cluster apply -f <out>/cluster.yaml`,
	Example: `  ankra migrate convert
  ankra migrate convert ./app --out ./app-k8s --option profiles=app
  ankra migrate convert --option source=daemon --option project=aura-office
  ankra migrate convert --option ingress.app=example.com --option cluster-issuer=letsencrypt-prod`,
	Args: cobra.MaximumNArgs(1),
	RunE: runMigrateConvert,
}

func init() {
	migrateConvertCmd.Flags().StringVar(&migrateConvertModule, "module", "", "Module to use (default: the most confident detection)")
	migrateConvertCmd.Flags().StringVar(&migrateConvertOut, "out", "ankra-migration", "Output directory")
	migrateConvertCmd.Flags().StringVar(&migrateConvertClusterName, "cluster-name", "", "Name for the generated ImportCluster (default: the directory name)")
	migrateConvertCmd.Flags().StringVar(&migrateConvertNamespace, "namespace", "", "Namespace for the generated workloads (default: the cluster name)")
	migrateConvertCmd.Flags().StringArrayVar(&migrateConvertOptions, "option", nil, "Module option as key=value (repeatable)")
	migrateConvertCmd.Flags().BoolVar(&migrateConvertForce, "force", false, "Overwrite an output directory that is not empty")
	migrateConvertCmd.Flags().BoolVar(&migrateConvertDryRun, "dry-run", false, "Print cluster.yaml and the files that would be written, without writing")
	registerStructuredOutputFlags(migrateConvertCmd)
	migrateCmd.AddCommand(migrateConvertCmd)
}

// migrateConvertSummary is the structured shape of a completed conversion.
type migrateConvertSummary struct {
	Module   string          `json:"module" yaml:"module"`
	Out      string          `json:"out,omitempty" yaml:"out,omitempty"`
	Cluster  migrate.Cluster `json:"cluster" yaml:"cluster"`
	Files    []string        `json:"files" yaml:"files"`
	Warnings []string        `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

func runMigrateConvert(cmd *cobra.Command, args []string) error {
	dir, err := migrateSourceDir(args)
	if err != nil {
		return err
	}
	options, err := parseMigrateOptions(migrateConvertOptions)
	if err != nil {
		return err
	}
	module, err := selectMigrateModule(cmd, newMigrateRegistry(), dir, migrateConvertModule)
	if err != nil {
		return err
	}
	clusterName := migrateConvertClusterName
	if clusterName == "" {
		clusterName = migrateResourceName(filepath.Base(dir))
	}
	namespace := migrateConvertNamespace
	if namespace == "" {
		namespace = clusterName
	}

	summary, clusterYAML, err := performMigrateConvert(dir, migrateConvertRequest{
		Module: module, ClusterName: clusterName, Namespace: namespace, Options: options,
		Out: migrateConvertOut, Force: migrateConvertForce, DryRun: migrateConvertDryRun,
	})
	if err != nil {
		return err
	}

	if migrateConvertDryRun {
		if handled, err := renderStructured(cmd, summary); handled || err != nil {
			return err
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), string(clusterYAML))
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nWould write %d file(s) to %s:\n", len(summary.Files), migrateConvertOut)
		for _, file := range summary.Files {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", file)
		}
		printMigrateWarnings(cmd, summary.Warnings)
		return nil
	}

	if handled, err := renderStructured(cmd, summary); handled || err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Converted %s with the %s module.\n", dir, summary.Module)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote %d file(s) to %s\n", len(summary.Files), summary.Out)
	printMigrateWarnings(cmd, summary.Warnings)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nReview the output, then apply it:\n  ankra cluster apply -f %s\n", filepath.Join(summary.Out, "cluster.yaml"))
	return nil
}

// migrateConvertRequest is one conversion: the module, the names the output
// carries, and where it goes. DryRun renders without writing.
type migrateConvertRequest struct {
	Module      migrate.Module
	ClusterName string
	Namespace   string
	Options     map[string]string
	Out         string
	Force       bool
	DryRun      bool
}

// performMigrateConvert runs the module, validates its result, and writes
// cluster.yaml plus every file it produced under Out unless DryRun. It
// returns the summary (Out set when written) and the rendered cluster.yaml.
func performMigrateConvert(dir string, request migrateConvertRequest) (migrateConvertSummary, []byte, error) {
	module := request.Module
	result, err := module.Convert(context.Background(), migrate.ConvertRequest{
		Dir:         dir,
		ClusterName: request.ClusterName,
		Namespace:   request.Namespace,
		Options:     request.Options,
	})
	if err != nil {
		return migrateConvertSummary{}, nil, fmt.Errorf("%s: %w", module.Describe().Name, err)
	}
	if err := migrate.Validate(result); err != nil {
		return migrateConvertSummary{}, nil, fmt.Errorf("%s: %w", module.Describe().Name, err)
	}
	clusterYAML, err := yaml.Marshal(result.Cluster)
	if err != nil {
		return migrateConvertSummary{}, nil, err
	}

	files := make([]string, 0, len(result.Files)+1)
	for file := range result.Files {
		files = append(files, file)
	}
	sort.Strings(files)
	files = append([]string{"cluster.yaml"}, files...)
	summary := migrateConvertSummary{
		Module:   module.Describe().Name,
		Cluster:  result.Cluster,
		Files:    files,
		Warnings: result.Warnings,
	}
	if request.DryRun {
		return summary, clusterYAML, nil
	}

	out, err := filepath.Abs(request.Out)
	if err != nil {
		return migrateConvertSummary{}, nil, withExitCode(exitUsage, err)
	}
	if err := ensureMigrateOutDir(out, request.Force); err != nil {
		return migrateConvertSummary{}, nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "cluster.yaml"), clusterYAML, 0o644); err != nil {
		return migrateConvertSummary{}, nil, err
	}
	for file, content := range result.Files {
		path := filepath.Join(out, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return migrateConvertSummary{}, nil, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return migrateConvertSummary{}, nil, err
		}
	}
	summary.Out = out
	return summary, clusterYAML, nil
}

func printMigrateWarnings(cmd *cobra.Command, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n%d thing(s) need your attention:\n", len(warnings))
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", warning)
	}
}

// parseMigrateOptions turns repeated key=value flags into a map. A bare key
// is a usage error, not an empty value: silently accepting it would hide a
// typo until the module ignored the option.
func parseMigrateOptions(raw []string) (map[string]string, error) {
	options := map[string]string{}
	for _, entry := range raw {
		key, value, found := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--option %q is not key=value", entry))
		}
		options[key] = value
	}
	return options, nil
}

// ensureMigrateOutDir creates the output directory, refusing to write into
// one that already has content unless --force was given.
func ensureMigrateOutDir(out string, force bool) error {
	entries, err := os.ReadDir(out)
	switch {
	case os.IsNotExist(err):
		return os.MkdirAll(out, 0o755)
	case err != nil:
		return err
	case len(entries) > 0 && !force:
		return withExitCode(exitUsage, fmt.Errorf("%s is not empty; pass --force to overwrite", out))
	}
	return nil
}
