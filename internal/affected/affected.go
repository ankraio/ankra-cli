// Package affected runs Ankra Pipelines' dependency-graph analysis (the
// affected: stage field) against a local checkout, so `ankra pipeline
// validate --changed-file` can say which stages a change reaches without a
// run.
//
// graph.awk and analyze.sh are copies of the platform's own program
// (ankraio/cluster enginekit/pipelineaffected). The CLI carries them rather
// than running a script the API sends it: the dry run reports the digest of
// the platform's copy (program_sha256), and the caller compares it with
// ProgramDigest to tell a reader when this copy is out of date. To update,
// copy both files from that package and nothing else.
package affected

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed graph.awk
var graphProgram string

//go:embed analyze.sh
var driverScript string

// outputPrefix starts every answer the analysis writes, followed by the
// stage name.
const outputPrefix = "ankra.affected."

// Timeout bounds one local analysis, the same bound the checkout step uses.
const Timeout = 120 * time.Second

// ProgramDigest identifies this copy of the analysis exactly as the platform
// identifies its own: the hex SHA-256 of graph.awk, a NUL, and analyze.sh.
func ProgramDigest() string {
	digest := sha256.Sum256([]byte(graphProgram + "\x00" + driverScript))
	return hex.EncodeToString(digest[:])
}

// Target is one stage's resolved targets, in the analysis' pattern form as
// the dry run returns them ("dir:go/...", "import:<path>", "name:@acme/web").
type Target struct {
	Stage string
	Go    []string
	Pnpm  []string
}

// ErrUnsupportedPlatform refuses a platform without a POSIX shell to run the
// analysis in.
var ErrUnsupportedPlatform = errors.New("the dependency-graph analysis needs a POSIX shell, git and awk, which Windows does not provide")

// Analyse runs the analysis over the repository at root for the given change
// and answers each stage it reached a verdict for: true when a changed file
// is in the stage's dependency graph, false when none is. A stage missing
// from the answer got no verdict - a pattern that matched nothing, or a file
// name the analysis cannot read - which the platform runs. log is what the
// analysis printed about each stage.
func Analyse(ctx context.Context, root string, changedFiles []string, targets []Target) (map[string]bool, string, error) {
	if runtime.GOOS == "windows" {
		return nil, "", ErrUnsupportedPlatform
	}
	for _, tool := range []string{"sh", "git", "awk"} {
		if _, lookError := exec.LookPath(tool); lookError != nil {
			return nil, "", fmt.Errorf("the dependency-graph analysis needs %s on PATH: %w", tool, lookError)
		}
	}
	data, tempError := os.MkdirTemp("", "ankra-affected-")
	if tempError != nil {
		return nil, "", fmt.Errorf("creating the analysis directory: %w", tempError)
	}
	defer func() { _ = os.RemoveAll(data) }()

	var changed strings.Builder
	for _, file := range changedFiles {
		if strings.ContainsAny(file, "\n\r\t\x00") {
			return nil, "", fmt.Errorf("the changed file %q cannot be handed to the analysis", file)
		}
		changed.WriteString(file + "\n")
	}
	var targetLines strings.Builder
	for _, target := range targets {
		for _, pattern := range target.Go {
			targetLines.WriteString(target.Stage + "\tgo\t" + pattern + "\n")
		}
		for _, pattern := range target.Pnpm {
			targetLines.WriteString(target.Stage + "\tpnpm\t" + pattern + "\n")
		}
	}
	files := map[string]string{
		"graph.awk":   graphProgram,
		"analyze.sh":  driverScript,
		"changed":     changed.String(),
		"targets":     targetLines.String(),
		"outputs.env": "",
	}
	for name, content := range files {
		if writeError := os.WriteFile(filepath.Join(data, name), []byte(content), 0o600); writeError != nil {
			return nil, "", fmt.Errorf("writing the analysis' %s: %w", name, writeError)
		}
	}

	analysisContext, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	outputs := filepath.Join(data, "outputs.env")
	command := exec.CommandContext(analysisContext, "sh", filepath.Join(data, "analyze.sh"), root, data, outputs)
	var log bytes.Buffer
	command.Stdout = &log
	command.Stderr = &log
	if runError := command.Run(); runError != nil {
		return nil, log.String(), fmt.Errorf("the dependency-graph analysis did not answer: %w", runError)
	}
	content, readError := os.ReadFile(outputs)
	if readError != nil {
		return nil, log.String(), fmt.Errorf("reading the analysis' answers: %w", readError)
	}
	return parseAnswers(string(content)), log.String(), nil
}

// parseAnswers reads the outputs file: "ankra.affected.<stage>=true|false"
// lines. Any other value is no verdict.
func parseAnswers(content string) map[string]bool {
	answers := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		name, value, isPair := strings.Cut(strings.TrimSpace(line), "=")
		if !isPair || !strings.HasPrefix(name, outputPrefix) {
			continue
		}
		stage := strings.TrimPrefix(name, outputPrefix)
		switch value {
		case "true":
			answers[stage] = true
		case "false":
			answers[stage] = false
		}
	}
	return answers
}
