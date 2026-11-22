package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stream is a trimmed `go test -json` run: a passing package with one slow,
// one fast and one parallel test and one whose subtests are parallel, a
// package whose test failed, and a package that timed out with TestHang
// still running.
const stream = `{"Action":"start","Package":"ex/a"}
{"Action":"run","Package":"ex/a","Test":"TestSlow"}
{"Action":"output","Package":"ex/a","Test":"TestSlow","Output":"=== RUN   TestSlow\n"}
{"Action":"output","Package":"ex/a","Test":"TestSlow","Output":"--- PASS: TestSlow (2.50s)\n"}
{"Action":"pass","Package":"ex/a","Test":"TestSlow","Elapsed":2.5}
{"Action":"run","Package":"ex/a","Test":"TestFast"}
{"Action":"run","Package":"ex/a","Test":"TestFast/sub"}
{"Action":"pass","Package":"ex/a","Test":"TestFast/sub","Elapsed":0.001}
{"Action":"pass","Package":"ex/a","Test":"TestFast","Elapsed":0.002}
{"Action":"run","Package":"ex/a","Test":"TestPar"}
{"Action":"pause","Package":"ex/a","Test":"TestPar"}
{"Action":"cont","Package":"ex/a","Test":"TestPar"}
{"Action":"pass","Package":"ex/a","Test":"TestPar","Elapsed":0.05}
{"Time":"2026-11-11T12:00:00Z","Action":"run","Package":"ex/a","Test":"TestRows"}
{"Time":"2026-11-11T12:00:00.1Z","Action":"run","Package":"ex/a","Test":"TestRows/one"}
{"Action":"pause","Package":"ex/a","Test":"TestRows/one"}
{"Action":"cont","Package":"ex/a","Test":"TestRows/one"}
{"Time":"2026-11-11T12:00:03Z","Action":"pass","Package":"ex/a","Test":"TestRows/one","Elapsed":2.9}
{"Time":"2026-11-11T12:00:03Z","Action":"pass","Package":"ex/a","Test":"TestRows","Elapsed":0}
{"Action":"output","Package":"ex/a","Output":"ok  \tex/a\t5.600s\n"}
{"Action":"pass","Package":"ex/a","Elapsed":5.6}
{"ImportPath":"ex/b","Action":"build-output","Output":"# ex/b\n"}
{"Action":"run","Package":"ex/b","Test":"TestBroken"}
{"Action":"output","Package":"ex/b","Test":"TestBroken","Output":"    b_test.go:9: want 1, got 2\n"}
{"Action":"output","Package":"ex/b","Test":"TestBroken","Output":"--- FAIL: TestBroken (0.20s)\n"}
{"Action":"fail","Package":"ex/b","Test":"TestBroken","Elapsed":0.2}
{"Action":"output","Package":"ex/b","Output":"FAIL\tex/b\t0.300s\n"}
{"Action":"fail","Package":"ex/b","Elapsed":0.3}
{"Action":"run","Package":"ex/c","Test":"TestDone"}
{"Action":"output","Package":"ex/c","Test":"TestDone","Output":"quiet on pass\n"}
{"Action":"pass","Package":"ex/c","Test":"TestDone","Elapsed":12}
{"Action":"run","Package":"ex/c","Test":"TestHang"}
{"Action":"output","Package":"ex/c","Test":"TestHang","Output":"panic: test timed out after 5m0s\n"}
{"Action":"output","Package":"ex/c","Output":"FAIL\tex/c\t300.100s\n"}
{"Action":"fail","Package":"ex/c","Elapsed":300.1}
`

func TestFilterKeepsFailuresAndResultsOnly(t *testing.T) {
	var out bytes.Buffer
	if err := runFilter(strings.NewReader("go: downloading x\n"+stream), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"go: downloading x\n",
		"ok  \tex/a\t5.600s\n",
		"# ex/b\n",
		"    b_test.go:9: want 1, got 2\n--- FAIL: TestBroken (0.20s)\n",
		"FAIL\tex/b\t0.300s\n",
		"--- NO RESULT: TestHang (still running when ex/c ended)\npanic: test timed out after 5m0s\n",
		"FAIL\tex/c\t300.100s\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("filtered output lacks %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"=== RUN   TestSlow", "--- PASS: TestSlow", "quiet on pass"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("filtered output has a passing test's output %q:\n%s", unwanted, got)
		}
	}
	// The timeout panic comes before the package's FAIL line, as go test
	// prints it.
	if strings.Index(got, "panic: test timed out") > strings.Index(got, "FAIL\tex/c") {
		t.Errorf("unfinished test flushed after its package result:\n%s", got)
	}
}

func TestCollectCountsTopLevelTests(t *testing.T) {
	rep, err := collect(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tests) != 6 {
		t.Fatalf("want 6 top-level results (subtests excluded), got %d: %+v", len(rep.Tests), rep.Tests)
	}
	if first := rep.Tests[0]; first.Test != "TestDone" || first.Elapsed != 12*time.Second {
		t.Errorf("slowest test = %+v, want TestDone at 12s", first)
	}
	if rep.Packages[0].Package != "ex/c" || rep.Packages[0].Result != "fail" {
		t.Errorf("slowest package = %+v, want ex/c failed", rep.Packages[0])
	}
	var a packageResult
	for _, p := range rep.Packages {
		if p.Package == "ex/a" {
			a = p
		}
	}
	if a.Tests != 4 || a.Parallel != 1 || a.TestTime != 5552*time.Millisecond || a.Elapsed != 5600*time.Millisecond {
		t.Errorf("ex/a = %+v, want 4 tests, 1 parallel, 5.552s of test time in 5.6s", a)
	}
	// go test reports TestRows as 0s; it held the package for 3s while its
	// parallel subtest ran.
	for _, result := range rep.Tests {
		if result.Test == "TestRows" && result.Elapsed != 3*time.Second {
			t.Errorf("TestRows = %s, want the 3s from its run to its result", result.Elapsed)
		}
	}
	if len(rep.Unfinished) != 1 || rep.Unfinished[0].Test != "TestHang" {
		t.Errorf("unfinished = %+v, want TestHang", rep.Unfinished)
	}
}

func TestDistribution(t *testing.T) {
	got := distribution([]testResult{
		{Elapsed: time.Millisecond},
		{Elapsed: 50 * time.Millisecond},
		{Elapsed: 60 * time.Millisecond},
		{Elapsed: 20 * time.Second},
	})
	want := []struct {
		count int
		total time.Duration
	}{{1, time.Millisecond}, {2, 110 * time.Millisecond}, {0, 0}, {0, 0}, {1, 20 * time.Second}}
	for i, w := range want {
		if got[i].Count != w.count || got[i].Total != w.total {
			t.Errorf("bucket %s = %d tests / %s, want %d / %s", got[i].Label, got[i].Count, got[i].Total, w.count, w.total)
		}
	}
}

func TestSummaryWritesTextAndMarkdown(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "events.json")
	if err := os.WriteFile(events, []byte(stream), 0o600); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(dir, "summary.md")
	var out bytes.Buffer
	if err := runSummary([]string{events}, 2, md, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"Slowest 2 top-level tests:", "12.00s  pass  ex/c TestDone", "3.00s  pass  ex/a TestRows", "ex/c TestHang"} {
		if !strings.Contains(text, want) {
			t.Errorf("text summary lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "TestSlow") {
		t.Errorf("-top 2 listed a third test:\n%s", text)
	}
	written, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| `ex/a` | pass | 5.60s | 4 | 1 | 5.55s |", "| 12.00s | pass | `ex/c` | `TestDone` |", "- `ex/c` `TestHang`"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("markdown summary lacks %q:\n%s", want, written)
		}
	}
}

func TestSummaryToleratesANegativeTop(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "events.json")
	if err := os.WriteFile(events, []byte(stream), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runSummary([]string{events}, -1, filepath.Join(dir, "summary.md"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Slowest 0 top-level tests:") {
		t.Errorf("-top -1 should list no test:\n%s", out.String())
	}
}
