package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeArtifactFile(t *testing.T, outputDir, relative, content string) {
	t.Helper()
	absolute := filepath.Join(outputDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFinaliseExportMeasuresArtifacts(t *testing.T) {
	out := t.TempDir()
	writeArtifactFile(t, out, "db/globals.sql", "CREATE ROLE app;\n")
	writeArtifactFile(t, out, "db/app.dump", "PGDMP\x01\x02payload")

	export := Export{
		Databases: []DatabaseExport{{
			Workload: "db", Engine: EnginePostgres, ServerVersion: "17.2",
			Target: RestoreTarget{Namespace: "shop", Host: "db", Port: 5432, Username: "app", PasswordSecret: "db-secrets", PasswordKey: "POSTGRES_PASSWORD"},
			Artifacts: []Artifact{
				{Path: "db/globals.sql", Kind: ArtifactKindGlobals, Format: ArtifactFormatSQL},
				{Path: "db/app.dump", Kind: ArtifactKindDatabase, Format: ArtifactFormatPostgresCustom, Database: "app"},
			},
		}},
		Warnings: []string{"dumped live"},
	}
	now := time.Date(2026, 8, 29, 1, 2, 3, 0, time.FixedZone("CEST", 2*3600))
	manifest, err := FinaliseExport(out, "docker", "/src/shop", export, now)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != ExportManifestVersion || manifest.Module != "docker" || manifest.SourceDir != "/src/shop" || !manifest.CreatedAt.Equal(now) || manifest.CreatedAt.Location() != time.UTC {
		t.Errorf("manifest header = %+v", manifest)
	}
	if len(manifest.Warnings) != 1 || len(manifest.Databases) != 1 || len(manifest.Databases[0].Artifacts) != 2 {
		t.Fatalf("manifest = %+v", manifest)
	}
	globals := manifest.Databases[0].Artifacts[0]
	want := sha256.Sum256([]byte("CREATE ROLE app;\n"))
	if globals.SizeBytes != int64(len("CREATE ROLE app;\n")) || globals.SHA256 != hex.EncodeToString(want[:]) {
		t.Errorf("globals measured as %+v", globals)
	}
	if manifest.Databases[0].Target.PasswordSecret != "db-secrets" {
		t.Errorf("target not carried over: %+v", manifest.Databases[0].Target)
	}

	if err := WriteExportManifest(out, manifest); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(out, ExportChecksumsFileName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "  db/app.dump") || !strings.HasSuffix(lines[1], "  db/globals.sql") || !strings.HasPrefix(lines[1], globals.SHA256) {
		t.Errorf("SHA256SUMS must list every artifact, sorted, in sha256sum format:\n%s", sums)
	}
	read, err := ReadExportManifest(out)
	if err != nil {
		t.Fatal(err)
	}
	if read.Module != "docker" || len(read.Databases) != 1 || read.Databases[0].Artifacts[1].SHA256 != manifest.Databases[0].Artifacts[1].SHA256 {
		t.Errorf("round trip lost data: %+v", read)
	}
}

func TestFinaliseExportRejects(t *testing.T) {
	out := t.TempDir()
	writeArtifactFile(t, out, "db/ok.sql", "SELECT 1;\n")
	writeArtifactFile(t, out, "db/empty.sql", "")

	database := func(artifacts ...Artifact) Export {
		return Export{Databases: []DatabaseExport{{Workload: "db", Engine: EnginePostgres, Artifacts: artifacts}}}
	}
	ok := Artifact{Path: "db/ok.sql", Kind: ArtifactKindGlobals, Format: ArtifactFormatSQL}
	cases := map[string]Export{
		"no databases":          {},
		"no artifacts":          database(),
		"no engine":             {Databases: []DatabaseExport{{Workload: "db", Artifacts: []Artifact{ok}}}},
		"absolute path":         database(Artifact{Path: "/etc/passwd", Kind: ArtifactKindGlobals, Format: ArtifactFormatSQL}),
		"parent escape":         database(Artifact{Path: "../ok.sql", Kind: ArtifactKindGlobals, Format: ArtifactFormatSQL}),
		"nested escape":         database(Artifact{Path: "db/../../ok.sql", Kind: ArtifactKindGlobals, Format: ArtifactFormatSQL}),
		"missing file":          database(Artifact{Path: "db/missing.sql", Kind: ArtifactKindGlobals, Format: ArtifactFormatSQL}),
		"empty file":            database(Artifact{Path: "db/empty.sql", Kind: ArtifactKindGlobals, Format: ArtifactFormatSQL}),
		"duplicate":             database(ok, ok),
		"claims manifest.json":  database(Artifact{Path: ExportManifestFileName, Kind: ArtifactKindGlobals, Format: ArtifactFormatSQL}),
		"no kind":               database(Artifact{Path: "db/ok.sql", Format: ArtifactFormatSQL}),
		"database without name": database(Artifact{Path: "db/ok.sql", Kind: ArtifactKindDatabase, Format: ArtifactFormatSQL}),
	}
	for name, export := range cases {
		if _, err := FinaliseExport(out, "docker", "/src", export, time.Now()); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestReadExportManifestRejectsOtherVersions(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, ExportManifestFileName), []byte(`{"version": 99, "module": "docker"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadExportManifest(out); err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Errorf("expected a version error, got %v", err)
	}
	if _, err := ReadExportManifest(t.TempDir()); err == nil {
		t.Error("a directory without a manifest must be an error")
	}
}

// claimsExport advertises the capability without implementing the verb.
type claimsExport struct{ fakeModule }

func (m claimsExport) Describe() Description {
	description := m.fakeModule.Describe()
	description.Capabilities = []string{CapabilityExport}
	return description
}

func TestExporterFor(t *testing.T) {
	if _, ok := ExporterFor(fakeModule{name: "plain"}); ok {
		t.Error("a module without the capability must not be offered as an exporter")
	}
	if _, ok := ExporterFor(claimsExport{fakeModule{name: "claims"}}); ok {
		t.Error("advertising the capability without implementing it must not be enough")
	}
	if !HasCapability(Description{Capabilities: []string{"other", CapabilityExport}}, CapabilityExport) || HasCapability(Description{}, CapabilityExport) {
		t.Error("HasCapability must find the capability among others and nowhere else")
	}
}

// dumperModule is an external module that answers export: it writes one dump
// under output_dir, narrates on stderr, and points the target at the
// namespace the request named.
const dumperModule = `#!/bin/sh
case "$1" in
  describe)
    printf '%s' '{"name":"dumper","version":"0.1","protocol":1,"summary":"dumper test module","capabilities":["export"]}'
    ;;
  detect)
    printf '%s' '{"confidence":0,"reason":"never"}'
    ;;
  export)
    request=$(cat)
    out=$(printf '%s' "$request" | sed -n 's/.*"output_dir":"\([^"]*\)".*/\1/p')
    ns=$(printf '%s' "$request" | sed -n 's/.*"namespace":"\([^"]*\)".*/\1/p')
    echo "dumping app" >&2
    mkdir -p "$out/db" && printf 'CREATE TABLE t (id int);\n' > "$out/db/app.sql"
    printf '{"databases":[{"workload":"db","engine":"mysql","target":{"namespace":"%s","host":"db","port":3306},"artifacts":[{"path":"db/app.sql","kind":"database","format":"sql","database":"app"}]}],"warnings":["from dumper"]}' "$ns"
    ;;
  *)
    echo "unknown verb $1" >&2
    exit 2
    ;;
esac
`

func TestExternalModuleExport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script modules")
	}
	dir := t.TempDir()
	writeModule(t, dir, "dumper", dumperModule, true)
	writeModule(t, dir, "echo", echoModule, true)
	registry := NewRegistry()
	registry.searchDirs = func() []string { return []string{dir} }

	plain, err := registry.Lookup(context.Background(), "echo")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ExporterFor(plain); ok {
		t.Error("an external module that does not list the capability must not be asked to export")
	}

	module, err := registry.Lookup(context.Background(), "dumper")
	if err != nil {
		t.Fatal(err)
	}
	exporter, ok := ExporterFor(module)
	if !ok {
		t.Fatal("dumper advertises export and must be offered as an exporter")
	}
	out := t.TempDir()
	var progress bytes.Buffer
	export, err := exporter.Export(context.Background(), ExportRequest{Dir: "/src", OutputDir: out, Namespace: "shop", Progress: &progress})
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Databases) != 1 || export.Databases[0].Target.Namespace != "shop" || export.Warnings[0] != "from dumper" {
		t.Errorf("export = %+v", export)
	}
	if !strings.Contains(progress.String(), "dumping app") {
		t.Errorf("the module's stderr must be relayed as progress, got %q", progress.String())
	}
	manifest, err := FinaliseExport(out, "dumper", "/src", export, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Databases[0].Artifacts[0].SizeBytes != int64(len("CREATE TABLE t (id int);\n")) {
		t.Errorf("artifact not measured from the file the module wrote: %+v", manifest.Databases[0].Artifacts[0])
	}
}
