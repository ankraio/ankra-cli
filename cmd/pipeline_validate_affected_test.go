package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ankra/internal/affected"
	"ankra/internal/client"
)

// affectedValidation is a dry run whose push leaves two stages to the
// dependency graph: test-api (Go, ./api/...) and test-web (pnpm, @acme/web).
func affectedValidation() *client.PipelineValidation {
	return &client.PipelineValidation{
		Severity: "ok",
		Events: []client.PipelineEventPlan{{
			Event: "push",
			Run:   true,
			Steps: []client.PipelinePlannedStep{
				{StepKey: "checkout", Stage: "checkout", Kind: "checkout"},
				{StepKey: "test-api", Stage: "test-api", Kind: "run", AffectedByGraph: true},
				{StepKey: "test-web", Stage: "test-web", Kind: "run", AffectedByGraph: true},
				{StepKey: "build", Stage: "build", Kind: "build"},
			},
			AffectedAnalysis: &client.PipelineAffectedAnalysis{
				ChangedFiles: []string{"web/ui/index.ts"},
				Targets: []client.PipelineAffectedTarget{
					{Stage: "test-api", Go: []string{"dir:api/..."}, Pnpm: []string{}},
					{Stage: "test-web", Go: []string{}, Pnpm: []string{"name:@acme/web"}},
				},
				ProgramDigest: affected.ProgramDigest(),
			},
		}},
	}
}

// affectedCheckout writes a repository where web depends on ui, and api is
// a Go package of its own, and makes it the working directory.
func affectedCheckout(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the analysis needs a POSIX shell")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":               "module example.com/mono\n",
		"api/api.go":           "package api\n",
		"web/app/package.json": `{"name": "@acme/web", "dependencies": {"@acme/ui": "workspace:*"}}`,
		"web/ui/package.json":  `{"name": "@acme/ui"}`,
		"web/ui/index.ts":      "export {}\n",
	}
	for name, content := range files {
		target := filepath.Join(root, filepath.FromSlash(name))
		if mkdirError := os.MkdirAll(filepath.Dir(target), 0o755); mkdirError != nil {
			t.Fatal(mkdirError)
		}
		if writeError := os.WriteFile(target, []byte(content), 0o644); writeError != nil {
			t.Fatal(writeError)
		}
	}
	for _, arguments := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if output, runError := command.CombinedOutput(); runError != nil {
			t.Fatalf("git %v: %v\n%s", arguments, runError, output)
		}
	}
	t.Chdir(root)
}

// With --changed-file inside a checkout, validate reads the graph locally:
// the ui change reaches test-web through its workspace dependency and not
// test-api, and the table says so.
func TestPipelineValidateReadsTheDependencyGraphLocally(t *testing.T) {
	affectedCheckout(t)
	mockClient := &pipelineLaneMock{validateResult: affectedValidation()}
	output, executeError := runPipelineCommand(t, mockClient, "validate", writePipelineFixture(t),
		"--application", testApplicationID, "--changed-file", "web/ui/index.ts")
	if executeError != nil {
		t.Fatalf("validate error = %v\n%s", executeError, output)
	}
	for _, wanted := range []string{
		"test-api (test-api, run) [graph: not affected, skipped; the stages that need it still run]",
		"test-web (test-web, run) [graph: affected]",
		"skip: not affected (graph)",
		"run (graph: affected)",
	} {
		if !strings.Contains(output, wanted) {
			t.Errorf("output does not contain %q:\n%s", wanted, output)
		}
	}
	if strings.Contains(output, "differs from the platform's") {
		t.Errorf("a matching program digest raised a note:\n%s", output)
	}
}

// --no-local-graph, or a validate with no --changed-file, leaves the graph to
// the run and says so; nothing is analysed.
func TestPipelineValidateLeavesTheGraphToTheRunWhenAsked(t *testing.T) {
	affectedCheckout(t)
	mockClient := &pipelineLaneMock{validateResult: affectedValidation()}
	output, executeError := runPipelineCommand(t, mockClient, "validate", writePipelineFixture(t),
		"--application", testApplicationID, "--changed-file", "web/ui/index.ts", "--no-local-graph")
	if executeError != nil {
		t.Fatalf("validate error = %v\n%s", executeError, output)
	}
	if !strings.Contains(output, "[graph: decided by the run's checkout]") ||
		!strings.Contains(output, "graph: decided at run time") {
		t.Fatalf("the graph must be shown as decided at run time:\n%s", output)
	}
	if mockClient.validateResult.Events[0].LocalAffected != nil {
		t.Fatalf("--no-local-graph analysed anyway: %v", mockClient.validateResult.Events[0].LocalAffected)
	}
}

// A platform whose program differs from this CLI's copy still gets the local
// answer, with a note that the copy may be out of date.
func TestPipelineValidateNotesAProgramThatDiffersFromThePlatforms(t *testing.T) {
	affectedCheckout(t)
	validation := affectedValidation()
	validation.Events[0].AffectedAnalysis.ProgramDigest = strings.Repeat("0", 64)
	mockClient := &pipelineLaneMock{validateResult: validation}
	output, executeError := runPipelineCommand(t, mockClient, "validate", writePipelineFixture(t),
		"--application", testApplicationID, "--changed-file", "web/ui/index.ts")
	if executeError != nil {
		t.Fatalf("validate error = %v\n%s", executeError, output)
	}
	if !strings.Contains(output, "differs from the platform's") {
		t.Fatalf("no note about the differing program:\n%s", output)
	}
}

// --changed-file names are cleaned to the form a provider reports, and a
// name outside the repository is a usage error rather than a change that
// matches nothing.
func TestPipelineValidateChangedFilesAreCleanRepositoryPaths(t *testing.T) {
	cleaned, cleanError := pipelineValidateChangedFiles([]string{" ./api/x.go ", "web//ui/../ui/index.ts", ""})
	if cleanError != nil || strings.Join(cleaned, ",") != "api/x.go,web/ui/index.ts" {
		t.Fatalf("cleaned = %v, %v", cleaned, cleanError)
	}
	for _, refused := range []string{"/etc/passwd", "../outside.go", ".", ".."} {
		if _, refusedError := pipelineValidateChangedFiles([]string{refused}); refusedError == nil {
			t.Errorf("%q must be refused", refused)
		}
	}
	if none, noneError := pipelineValidateChangedFiles(nil); none != nil || noneError != nil {
		t.Errorf("no flag must stay nil, got %v, %v", none, noneError)
	}
}
