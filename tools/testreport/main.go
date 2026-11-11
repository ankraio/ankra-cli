// Command testreport reads the event stream of `go test -json` and reports
// where the time went.
//
// CI runs the suite under a fixed -timeout, and a package that creeps up to
// it fails with "panic: test timed out" and no assertion failing. Without
// per-test durations nobody can tell one hung test from a few thousand
// slightly slow ones (ankra-hq41xb.28), so CI keeps the JSON stream and this
// command turns it into numbers.
//
// Two modes:
//
//	go test -json ./... | tee events.json | go run ./tools/testreport -filter
//
// echoes the stream as a person wants to read it in a CI log: build errors,
// each package's result line, and the full output of every test that failed
// or was still running when its package ended (a timeout panic). Passing
// tests stay quiet, as without -v.
//
//	go run ./tools/testreport -summary events.json
//
// prints per-package totals, how the test time is distributed, and the
// slowest tests. With -markdown FILE (for example $GITHUB_STEP_SUMMARY) the
// same summary is appended to FILE as markdown.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// event is one line of `go test -json` (cmd/test2json), including the build
// events `go test -json` emits since Go 1.24.
type event struct {
	Time       time.Time
	Action     string
	Package    string
	ImportPath string
	Test       string
	Elapsed    float64
	Output     string
}

func main() {
	filter := flag.Bool("filter", false, "echo a go test -json stream from stdin the way go test without -v prints it")
	summary := flag.Bool("summary", false, "summarise the go test -json stream in the file argument (stdin if none)")
	top := flag.Int("top", 30, "how many of the slowest tests -summary lists")
	markdown := flag.String("markdown", "", "with -summary, also append the summary as markdown to this file")
	flag.Parse()

	var err error
	switch {
	case *filter == *summary:
		err = errors.New("give exactly one of -filter and -summary")
	case *filter:
		err = runFilter(os.Stdin, os.Stdout)
	default:
		err = runSummary(flag.Args(), *top, *markdown, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "testreport:", err)
		os.Exit(1)
	}
}

func runSummary(args []string, top int, markdownPath string, stdout io.Writer) error {
	in := io.Reader(os.Stdin)
	switch len(args) {
	case 0:
	case 1:
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		in = f
	default:
		return errors.New("-summary takes at most one file")
	}
	report, err := collect(in)
	if err != nil {
		return err
	}
	writeText(stdout, report, top)
	if markdownPath == "" {
		return nil
	}
	// Appended, not replaced: $GITHUB_STEP_SUMMARY collects every step's
	// summary for the job.
	f, err := os.OpenFile(markdownPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	writeMarkdown(f, report, top)
	return f.Close()
}

// decode reads events until EOF. A line that is not an event (go test can
// print a few plain lines before the stream starts) is passed to plain.
func decode(r io.Reader, handle func(event), plain func(string)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var e event
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &e) != nil {
			plain(string(line) + "\n")
			continue
		}
		handle(e)
	}
	return scanner.Err()
}

// runFilter echoes the stream without the output of tests that passed or
// were skipped. Like go test over several packages, it prints a package's
// lines once the package has finished, so packages do not interleave.
func runFilter(r io.Reader, w io.Writer) error {
	type key struct{ pkg, test string }
	held := map[key][]string{}
	order := map[string][]string{}      // per package, tests in the order they started
	packageOut := map[string][]string{} // per package, lines no test owns
	var packages []string
	seen := map[string]bool{}
	note := func(pkg string) {
		if !seen[pkg] {
			seen[pkg] = true
			packages = append(packages, pkg)
		}
	}
	finish := func(pkg string) {
		for _, test := range order[pkg] {
			k := key{pkg, test}
			lines, ok := held[k]
			if !ok {
				continue
			}
			_, _ = fmt.Fprintf(w, "--- NO RESULT: %s (still running when %s ended)\n", test, pkg)
			for _, line := range lines {
				_, _ = io.WriteString(w, line)
			}
			delete(held, k)
		}
		for _, line := range packageOut[pkg] {
			_, _ = io.WriteString(w, line)
		}
		delete(order, pkg)
		delete(packageOut, pkg)
		delete(seen, pkg)
	}
	err := decode(r, func(e event) {
		if e.Action == "build-output" {
			_, _ = io.WriteString(w, e.Output)
			return
		}
		if e.Package == "" {
			return
		}
		note(e.Package)
		k := key{e.Package, e.Test}
		switch {
		case e.Test == "" && e.Action == "output":
			packageOut[e.Package] = append(packageOut[e.Package], e.Output)
		case e.Test == "" && (e.Action == "pass" || e.Action == "fail" || e.Action == "skip"):
			finish(e.Package)
		case e.Test == "":
		case e.Action == "run":
			if _, ok := held[k]; !ok {
				held[k] = nil
				order[e.Package] = append(order[e.Package], e.Test)
			}
		case e.Action == "output":
			held[k] = append(held[k], e.Output)
		case e.Action == "fail":
			for _, line := range held[k] {
				_, _ = io.WriteString(w, line)
			}
			delete(held, k)
		case e.Action == "pass" || e.Action == "skip":
			delete(held, k)
		}
	}, func(line string) { _, _ = io.WriteString(w, line) })
	// A stream cut short (the job was cancelled) still shows what it had.
	for _, pkg := range packages {
		if seen[pkg] {
			finish(pkg)
		}
	}
	return err
}

type testResult struct {
	Package string
	Test    string
	Result  string
	// Elapsed is the time the test held the package. For a test that ran
	// on its own that includes its parallel subtests, which go test leaves
	// out of the test's own duration (a parent whose subtests all call
	// t.Parallel reports 0s).
	Elapsed time.Duration
}

type packageResult struct {
	Package string
	Result  string
	Elapsed time.Duration
	// Tests counts top-level tests; Parallel those that called t.Parallel.
	Tests    int
	Parallel int
	// TestTime sums the top-level tests' durations. Close to Elapsed means
	// the package runs its tests one after another.
	TestTime time.Duration
}

type report struct {
	Packages []packageResult
	// Tests holds every top-level test that reached a result.
	Tests []testResult
	// Unfinished names tests that were running when their package ended.
	Unfinished []testResult
}

func seconds(f float64) time.Duration {
	return time.Duration(f * float64(time.Second))
}

func collect(r io.Reader) (report, error) {
	type key struct{ pkg, test string }
	packages := map[string]*packageResult{}
	var packageOrder []string
	pkg := func(name string) *packageResult {
		p, ok := packages[name]
		if !ok {
			p = &packageResult{Package: name}
			packages[name] = p
			packageOrder = append(packageOrder, name)
		}
		return p
	}
	running := map[key]bool{}
	started := map[key]time.Time{}
	paused := map[key]bool{}
	var rep report
	err := decode(r, func(e event) {
		if e.Package == "" {
			return
		}
		p := pkg(e.Package)
		if e.Test == "" {
			switch e.Action {
			case "pass", "fail", "skip":
				p.Result = e.Action
				p.Elapsed = seconds(e.Elapsed)
			}
			return
		}
		if strings.Contains(e.Test, "/") {
			return
		}
		k := key{e.Package, e.Test}
		switch e.Action {
		case "run":
			running[k] = true
			started[k] = e.Time
		case "pause":
			paused[k] = true
		case "pass", "fail", "skip":
			delete(running, k)
			p.Tests++
			elapsed := seconds(e.Elapsed)
			if paused[k] {
				p.Parallel++
			} else if start := started[k]; !start.IsZero() && !e.Time.IsZero() && e.Time.Sub(start) > elapsed {
				// A test that did not pause held the package from its run
				// event to its result, its parallel subtests included.
				elapsed = e.Time.Sub(start)
			}
			p.TestTime += elapsed
			rep.Tests = append(rep.Tests, testResult{Package: e.Package, Test: e.Test, Result: e.Action, Elapsed: elapsed})
		}
	}, func(string) {})
	for _, name := range packageOrder {
		rep.Packages = append(rep.Packages, *packages[name])
	}
	for k := range running {
		rep.Unfinished = append(rep.Unfinished, testResult{Package: k.pkg, Test: k.test, Result: "running"})
	}
	sort.Slice(rep.Packages, func(i, j int) bool { return rep.Packages[i].Elapsed > rep.Packages[j].Elapsed })
	sort.SliceStable(rep.Tests, func(i, j int) bool { return rep.Tests[i].Elapsed > rep.Tests[j].Elapsed })
	sort.Slice(rep.Unfinished, func(i, j int) bool {
		if rep.Unfinished[i].Package != rep.Unfinished[j].Package {
			return rep.Unfinished[i].Package < rep.Unfinished[j].Package
		}
		return rep.Unfinished[i].Test < rep.Unfinished[j].Test
	})
	return rep, err
}

// bucket is one band of the duration distribution: how many tests took less
// than Below, and how much time they add up to.
type bucket struct {
	Label string
	Below time.Duration
	Count int
	Total time.Duration
}

func distribution(tests []testResult) []bucket {
	buckets := []bucket{
		{Label: "< 10ms", Below: 10 * time.Millisecond},
		{Label: "10-100ms", Below: 100 * time.Millisecond},
		{Label: "100ms-1s", Below: time.Second},
		{Label: "1-10s", Below: 10 * time.Second},
		{Label: ">= 10s", Below: 1<<63 - 1},
	}
	for _, t := range tests {
		for i := range buckets {
			if t.Elapsed < buckets[i].Below {
				buckets[i].Count++
				buckets[i].Total += t.Elapsed
				break
			}
		}
	}
	return buckets
}

func short(d time.Duration) string {
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func writeText(w io.Writer, rep report, top int) {
	_, _ = fmt.Fprintln(w, "Per-package totals (slowest first):")
	_, _ = fmt.Fprintf(w, "  %-10s %-6s %7s %9s %10s  %s\n", "elapsed", "result", "tests", "parallel", "test time", "package")
	for _, p := range rep.Packages {
		_, _ = fmt.Fprintf(w, "  %-10s %-6s %7d %9d %10s  %s\n", short(p.Elapsed), p.Result, p.Tests, p.Parallel, short(p.TestTime), p.Package)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Top-level test durations, all packages:")
	for _, b := range distribution(rep.Tests) {
		_, _ = fmt.Fprintf(w, "  %-9s %6d tests  %10s\n", b.Label, b.Count, short(b.Total))
	}
	_, _ = fmt.Fprintln(w)
	n := max(0, min(top, len(rep.Tests)))
	_, _ = fmt.Fprintf(w, "Slowest %d top-level tests:\n", n)
	for _, t := range rep.Tests[:n] {
		_, _ = fmt.Fprintf(w, "  %9s  %-4s  %s %s\n", short(t.Elapsed), t.Result, t.Package, t.Test)
	}
	if len(rep.Unfinished) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "Still running when their package ended (a timeout or a crash):")
		for _, t := range rep.Unfinished {
			_, _ = fmt.Fprintf(w, "  %s %s\n", t.Package, t.Test)
		}
	}
}

func writeMarkdown(w io.Writer, rep report, top int) {
	_, _ = fmt.Fprintln(w, "### Test time")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "| package | result | elapsed | tests | parallel | sum of test durations |")
	_, _ = fmt.Fprintln(w, "|---|---|---:|---:|---:|---:|")
	for _, p := range rep.Packages {
		_, _ = fmt.Fprintf(w, "| `%s` | %s | %s | %d | %d | %s |\n", p.Package, p.Result, short(p.Elapsed), p.Tests, p.Parallel, short(p.TestTime))
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "| top-level test duration | tests | total |")
	_, _ = fmt.Fprintln(w, "|---|---:|---:|")
	for _, b := range distribution(rep.Tests) {
		_, _ = fmt.Fprintf(w, "| %s | %d | %s |\n", b.Label, b.Count, short(b.Total))
	}
	_, _ = fmt.Fprintln(w)
	n := max(0, min(top, len(rep.Tests)))
	_, _ = fmt.Fprintf(w, "<details><summary>Slowest %d top-level tests</summary>\n\n", n)
	_, _ = fmt.Fprintln(w, "| elapsed | result | package | test |")
	_, _ = fmt.Fprintln(w, "|---:|---|---|---|")
	for _, t := range rep.Tests[:n] {
		_, _ = fmt.Fprintf(w, "| %s | %s | `%s` | `%s` |\n", short(t.Elapsed), t.Result, t.Package, t.Test)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "</details>")
	if len(rep.Unfinished) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "Still running when their package ended (a timeout or a crash):")
		_, _ = fmt.Fprintln(w)
		for _, t := range rep.Unfinished {
			_, _ = fmt.Fprintf(w, "- `%s` `%s`\n", t.Package, t.Test)
		}
	}
	_, _ = fmt.Fprintln(w)
}
