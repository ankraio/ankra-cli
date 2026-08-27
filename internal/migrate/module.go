// Package migrate turns a project's existing deployment description - a
// docker-compose file, and whatever formats people write modules for - into
// Ankra resources: an ImportCluster manifest plus the Kubernetes manifests its
// stack refers to.
//
// A module is either compiled in (see the compose subpackage) or an executable
// named ankra-module-<name> found on PATH. Both answer the same three verbs, so
// a module written in Bash is indistinguishable from a built-in one to the rest
// of the CLI.
package migrate

import "context"

// Module converts one source format. Implementations must be safe to call
// concurrently and must not write to the source directory.
type Module interface {
	// Describe reports what this module is and which files it recognises. It
	// is called for `ankra migrate modules` and must not touch the filesystem.
	Describe() Description

	// Detect reports whether dir looks like this module's source format.
	// A module that finds nothing returns a zero-confidence result, not an
	// error: "not mine" is an answer, not a failure.
	Detect(ctx context.Context, dir string) (Detection, error)

	// Convert reads the source in dir and returns the Ankra resources for it.
	// It reports problems it could not resolve through Result.Warnings rather
	// than failing, so a partial conversion a human can finish beats no
	// conversion at all.
	Convert(ctx context.Context, request ConvertRequest) (Result, error)
}

// Description is a module's self-report.
type Description struct {
	// Name is the token users type: `ankra migrate convert --module <name>`.
	Name string `json:"name"`
	// Version is the module's own version, not the CLI's.
	Version string `json:"version"`
	// Protocol is the wire protocol version an external module implements
	// (ProtocolVersion). The registry sets it for built-ins.
	Protocol int `json:"protocol"`
	// Summary is one line, shown in the module list.
	Summary string `json:"summary"`
	// FilePatterns are the globs this module looks for, shown to users so an
	// empty detection is explainable rather than mysterious.
	FilePatterns []string `json:"file_patterns,omitempty"`
	// Builtin is set by the registry, not by external modules.
	Builtin bool `json:"builtin,omitempty"`
	// Path is where an external module was found. Empty for built-ins.
	Path string `json:"path,omitempty"`
}

// Detection is the answer to "does this directory look like yours?".
type Detection struct {
	// Confidence runs 0 (not mine) to 1 (certain). Modules should reserve 1
	// for an unambiguous marker file and use lower values for heuristics, so
	// that a specific module wins over a general one.
	Confidence float64 `json:"confidence"`
	// Files are the source files found, relative to the scanned directory.
	Files []string `json:"files,omitempty"`
	// Reason explains the verdict in one line, including for a zero score.
	Reason string `json:"reason,omitempty"`
}

// ConvertRequest is the input to a conversion.
type ConvertRequest struct {
	// Dir is the directory holding the source files.
	Dir string `json:"dir"`
	// ClusterName names the generated ImportCluster.
	ClusterName string `json:"cluster_name"`
	// Namespace is where the generated workloads are placed.
	Namespace string `json:"namespace"`
	// Options carries module-specific settings from --option key=value, so a
	// module can take input the CLI knows nothing about.
	Options map[string]string `json:"options,omitempty"`
}

// Result is a conversion's output: the cluster manifest, the files its stack
// refers to, and anything the module could not translate faithfully.
type Result struct {
	// Cluster is the ImportCluster the CLI serialises to cluster.yaml.
	Cluster Cluster `json:"cluster"`
	// Files maps a path relative to the output directory to its content.
	// Manifest entries in Cluster reference these by the same path.
	Files map[string]string `json:"files,omitempty"`
	// Warnings are things a human must resolve: an unmappable construct, a
	// value that has to be supplied, a secret that cannot be carried over.
	Warnings []string `json:"warnings,omitempty"`
}
