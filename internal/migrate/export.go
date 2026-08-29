package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ExportRequest is the input to the export verb: dump the data behind the
// source in Dir into OutputDir.
type ExportRequest struct {
	// Dir is the directory holding the source files.
	Dir string `json:"dir"`
	// OutputDir is the absolute directory the module writes artifacts into.
	// Paths in the reply are relative to it.
	OutputDir string `json:"output_dir"`
	// Namespace is where convert placed the workloads, so the module can say
	// where each artifact restores to.
	Namespace string `json:"namespace"`
	// Options carries module-specific settings from --option key=value.
	Options map[string]string `json:"options,omitempty"`
	// Progress receives human-readable progress lines while a dump runs.
	// Built-in modules write to it directly; an external module's stderr is
	// relayed to it. It is never part of the wire format.
	Progress io.Writer `json:"-"`
}

// Export is a module's answer to the export verb: what it dumped, grouped by
// the database server each dump came from.
type Export struct {
	Databases []DatabaseExport `json:"databases"`
	// Warnings are things a human must know before restoring: a server that
	// was dumped while live, a database that was skipped.
	Warnings []string `json:"warnings,omitempty"`
}

// DatabaseExport is one database server's dumps plus where they restore to.
type DatabaseExport struct {
	// Workload is the source service the server ran as; convert turned it
	// into the workload of the same name.
	Workload string `json:"workload"`
	// Engine is EnginePostgres or EngineMySQL.
	Engine        string        `json:"engine"`
	Image         string        `json:"image,omitempty"`
	ServerVersion string        `json:"server_version,omitempty"`
	Target        RestoreTarget `json:"target"`
	Artifacts     []Artifact    `json:"artifacts"`
}

// RestoreTarget is the in-cluster server the dumps load into: the Service
// convert generated for the database and the Secret holding its password.
type RestoreTarget struct {
	Namespace string `json:"namespace"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username,omitempty"`
	// PasswordSecret and PasswordKey name the Secret entry the restore reads
	// the password from. Empty when the source ran without one.
	PasswordSecret string `json:"password_secret,omitempty"`
	PasswordKey    string `json:"password_key,omitempty"`
}

// Artifact is one file a module wrote into the output directory.
type Artifact struct {
	// Path is relative to the output directory.
	Path string `json:"path"`
	// Kind is ArtifactKindDatabase or ArtifactKindGlobals.
	Kind string `json:"kind"`
	// Format is ArtifactFormatPostgresCustom or ArtifactFormatSQL.
	Format string `json:"format"`
	// Database is the database a Kind=database artifact holds.
	Database string `json:"database,omitempty"`
	// SizeBytes and SHA256 are measured from the file by the CLI, not
	// reported by the module, so a restore can trust them.
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// Artifact kinds and formats.
const (
	ArtifactKindDatabase = "database"
	ArtifactKindGlobals  = "globals"

	ArtifactFormatPostgresCustom = "pg_custom"
	ArtifactFormatSQL            = "sql"
)

// Database engines an export can name.
const (
	EnginePostgres = "postgres"
	EngineMySQL    = "mysql"
)

// ExportManifest is the manifest.json written next to the artifacts. It is the
// whole description of an export: a restore needs nothing else.
type ExportManifest struct {
	Version   int              `json:"version"`
	Module    string           `json:"module"`
	SourceDir string           `json:"source_dir"`
	CreatedAt time.Time        `json:"created_at"`
	Databases []DatabaseExport `json:"databases"`
	Warnings  []string         `json:"warnings,omitempty"`
}

// Files an export directory always contains.
const (
	ExportManifestVersion   = 1
	ExportManifestFileName  = "manifest.json"
	ExportChecksumsFileName = "SHA256SUMS"
)

// FinaliseExport checks every artifact a module reported against the output
// directory and measures each file's size and checksum. A module that names a
// file it did not write, writes an empty one, or points outside the output
// directory is rejected here, before anything is described as restorable.
func FinaliseExport(outputDir, moduleName, sourceDir string, export Export, now time.Time) (ExportManifest, error) {
	if len(export.Databases) == 0 {
		return ExportManifest{}, fmt.Errorf("module returned no databases")
	}
	manifest := ExportManifest{
		Version:   ExportManifestVersion,
		Module:    moduleName,
		SourceDir: sourceDir,
		CreatedAt: now.UTC(),
		Warnings:  export.Warnings,
	}
	seen := map[string]bool{}
	for _, database := range export.Databases {
		if database.Workload == "" || database.Engine == "" {
			return ExportManifest{}, fmt.Errorf("module returned a database export without a workload or engine")
		}
		if len(database.Artifacts) == 0 {
			return ExportManifest{}, fmt.Errorf("module returned no artifacts for %s", database.Workload)
		}
		for i := range database.Artifacts {
			artifact := &database.Artifacts[i]
			if err := validateArtifactPath(artifact.Path); err != nil {
				return ExportManifest{}, err
			}
			if seen[artifact.Path] {
				return ExportManifest{}, fmt.Errorf("module returned artifact %s twice", artifact.Path)
			}
			seen[artifact.Path] = true
			if artifact.Kind == "" || artifact.Format == "" {
				return ExportManifest{}, fmt.Errorf("artifact %s has no kind or format", artifact.Path)
			}
			if artifact.Kind == ArtifactKindDatabase && artifact.Database == "" {
				return ExportManifest{}, fmt.Errorf("artifact %s is a database dump but names no database", artifact.Path)
			}
			size, sum, err := checksumFile(filepath.Join(outputDir, filepath.FromSlash(artifact.Path)))
			if err != nil {
				return ExportManifest{}, fmt.Errorf("artifact %s: %w", artifact.Path, err)
			}
			if size == 0 {
				return ExportManifest{}, fmt.Errorf("artifact %s is empty", artifact.Path)
			}
			artifact.SizeBytes, artifact.SHA256 = size, sum
		}
		manifest.Databases = append(manifest.Databases, database)
	}
	return manifest, nil
}

func validateArtifactPath(path string) error {
	switch {
	case path == "":
		return fmt.Errorf("module returned an artifact without a path")
	case filepath.IsAbs(path), strings.HasPrefix(path, ".."), strings.Contains(path, "/../"), strings.Contains(path, `\`):
		return fmt.Errorf("module returned an unsafe artifact path %q", path)
	case path == ExportManifestFileName, path == ExportChecksumsFileName:
		return fmt.Errorf("module returned an artifact named %s, which the CLI writes itself", path)
	}
	return nil
}

func checksumFile(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(digest.Sum(nil)), nil
}

// WriteExportManifest writes manifest.json and a SHA256SUMS file in the format
// `sha256sum -c` reads, so an export can be verified without the CLI.
func WriteExportManifest(outputDir string, manifest ExportManifest) error {
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, ExportManifestFileName), append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	var lines []string
	for _, database := range manifest.Databases {
		for _, artifact := range database.Artifacts {
			lines = append(lines, artifact.SHA256+"  "+artifact.Path)
		}
	}
	sort.Strings(lines)
	return os.WriteFile(filepath.Join(outputDir, ExportChecksumsFileName), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// ReadExportManifest reads the manifest an earlier export wrote.
func ReadExportManifest(outputDir string) (ExportManifest, error) {
	raw, err := os.ReadFile(filepath.Join(outputDir, ExportManifestFileName))
	if err != nil {
		return ExportManifest{}, err
	}
	var manifest ExportManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ExportManifest{}, fmt.Errorf("reading %s: %w", ExportManifestFileName, err)
	}
	if manifest.Version != ExportManifestVersion {
		return ExportManifest{}, fmt.Errorf("%s is version %d, this CLI reads version %d", ExportManifestFileName, manifest.Version, ExportManifestVersion)
	}
	return manifest, nil
}
