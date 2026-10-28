package cmd

import (
	"bytes"
	"strings"
	"testing"

	"ankra/internal/client"
)

// `ankra pipeline get` says whether each step started warm (CACHE), and
// --step prints why a cache did not restore (ankra-alhkef.4, PLA-900).
func TestPipelineRunDetailPrintsEachStepsCacheResult(t *testing.T) {
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 11, ID: "run-11", Status: "running"},
		Steps: []client.PipelineStep{
			{StepKey: "install", Status: "concluded", CacheResult: "restore_failed",
				Caches: []client.PipelineStepCache{{Path: "/workspace/.pnpm-store", Outcome: "failed"}}},
			{StepKey: "lint", Status: "concluded", CacheResult: "unknown"},
		},
	}, client.PipelineSelector{})
	rendered := output.String()
	if !strings.Contains(rendered, "CACHE") {
		t.Fatalf("the step table has no CACHE column:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "install") && !strings.Contains(line, "restore_failed") {
			t.Errorf("the install row does not say its restore failed: %q", line)
		}
		if strings.Contains(line, "lint") && strings.Contains(line, "unknown") {
			t.Errorf("a step with no cache report claims a cache answer: %q", line)
		}
	}
}

func TestPipelineStepCachesTableNamesWhyACacheDidNotRestore(t *testing.T) {
	detail := client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 12},
		Steps: []client.PipelineStep{
			{StepKey: "install", Attempt: 1, CacheResult: "restore_failed"},
			{StepKey: "install", Attempt: 2, CacheResult: "partial", Caches: []client.PipelineStepCache{
				{Path: "/workspace/.pnpm-store", Outcome: "failed", Reason: "download_failed",
					Error: "the download or extraction failed: HTTP 403", DurationMilliseconds: 1500},
				{Path: "/workspace/node_modules", Outcome: "hit", Reason: "none", Bytes: 3 << 20,
					Saved: true, SavedBytes: 2048},
			}},
		},
	}
	var output bytes.Buffer
	if printError := printPipelineStepCaches(&output, detail, "install"); printError != nil {
		t.Fatalf("printing: %v", printError)
	}
	rendered := output.String()
	for _, expected := range []string{
		"Caches of install (attempt 2): partial",
		"download_failed", "HTTP 403", "1.5s",
		"3.0 MiB", "yes (2.0 KiB)",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("output missing %q:\n%s", expected, rendered)
		}
	}

	var missing bytes.Buffer
	printError := printPipelineStepCaches(&missing, detail, "deploy")
	if printError == nil || exitCodeFor(printError) != exitUsage {
		t.Errorf("a step the run does not have is a usage error, got %v", printError)
	}
	var unreported bytes.Buffer
	detail.Steps = detail.Steps[:1]
	if printError := printPipelineStepCaches(&unreported, detail, "install"); printError != nil ||
		!strings.Contains(unreported.String(), "No per-cache report was recorded") {
		t.Errorf("a step without a report says so (%v):\n%s", printError, unreported.String())
	}
}

func TestFormatPipelineCacheBytes(t *testing.T) {
	for sizeBytes, expected := range map[int64]string{512: "512 B", 2048: "2.0 KiB", 5 << 30: "5.0 GiB"} {
		if formatted := formatPipelineCacheBytes(sizeBytes); formatted != expected {
			t.Errorf("formatPipelineCacheBytes(%d) = %q, want %q", sizeBytes, formatted, expected)
		}
	}
}
