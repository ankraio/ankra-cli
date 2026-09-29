// Command gendocs renders the ankra CLI command tree as Mintlify MDX pages,
// one page per top-level command family, plus an index page.
//
// Usage:
//
//	go run ./tools/gendocs --out ../ankra-docs/reference/cli
//
// The output directory is wiped of *.mdx files before writing so removed
// commands disappear from the docs. Pages are deterministic (sorted) so the
// diff is reviewable.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"ankra/cmd"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func main() {
	out := flag.String("out", "", "directory to write MDX files into (required)")
	version := flag.String("version", "", "CLI version the docs are generated from (e.g. the release tag); defaults to the compiled-in fallback")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "gendocs: --out is required")
		os.Exit(2)
	}
	if *version != "" {
		cmd.SetVersion(*version)
	}

	root := cmd.Root()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	if err := run(root, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run(root *cobra.Command, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	stale, err := filepath.Glob(filepath.Join(outDir, "*.mdx"))
	if err != nil {
		return err
	}
	for _, f := range stale {
		if err := os.Remove(f); err != nil {
			return err
		}
	}

	families := visibleSubcommands(root)
	for _, family := range families {
		page := renderFamilyPage(family)
		name := family.Name() + ".mdx"
		if err := os.WriteFile(filepath.Join(outDir, name), []byte(page), 0o644); err != nil {
			return err
		}
	}

	index := renderIndexPage(root, families)
	if err := os.WriteFile(filepath.Join(outDir, "index.mdx"), []byte(index), 0o644); err != nil {
		return err
	}

	fmt.Printf("gendocs: wrote %d command pages + index to %s\n", len(families), outDir)
	return nil
}

func visibleSubcommands(c *cobra.Command) []*cobra.Command {
	var cmds []*cobra.Command
	for _, sub := range c.Commands() {
		// Deprecated commands are excluded to match cobra's own help
		// (IsAvailableCommand); publishing them would advertise grammar
		// the CLI is actively retiring.
		if sub.Hidden || sub.Deprecated != "" || sub.Name() == "help" {
			continue
		}
		cmds = append(cmds, sub)
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name() < cmds[j].Name() })
	return cmds
}

var mdxProse = strings.NewReplacer(
	"<", "&lt;",
	">", "&gt;",
	"{", "&#123;",
	"}", "&#125;",
)

// flagToken matches a long flag named in prose, with an =value or a directly
// following single-quoted argument ("--set 'spec.replicas=3'"). Mintlify's
// typographer turns an unfenced "--" into an em dash, so "--wait" rendered as
// "—wait" until these were code-spanned (ankra-ta04t).
var flagToken = regexp.MustCompile(`(^|[\s(\[/,;:"'])(--[A-Za-z][A-Za-z0-9-]*(?:=[^\s,;)'"]*| '[^'\n]*'|\*)?)`)

// escapeMDX makes arbitrary help text safe inside MDX prose: angle brackets
// and curly braces are JSX syntax to Mintlify, and flag names become code
// spans. Existing backtick code spans are left untouched — MDX renders
// character references inside them literally, so escaping there would
// corrupt placeholders like `--cluster <id>`.
func escapeMDX(s string) string {
	parts := strings.Split(s, "`")
	for i, part := range parts {
		// Odd indices sit between a backtick pair, unless the opening
		// backtick is unmatched (last part) — CommonMark renders an
		// unmatched backtick as literal prose, so escape that too.
		if i%2 == 1 && i < len(parts)-1 {
			continue
		}
		part = flagToken.ReplaceAllString(part, "$1\x00$2\x00")
		part = mdxProse.Replace(part)
		parts[i] = restoreCodeSpans(part)
	}
	return strings.Join(parts, "`")
}

// restoreCodeSpans turns the NUL-delimited spans marked before escaping into
// backtick code spans, undoing the entity escaping inside them: MDX renders
// references in a code span literally.
func restoreCodeSpans(s string) string {
	segs := strings.Split(s, "\x00")
	for i := 1; i < len(segs); i += 2 {
		segs[i] = "`" + unescapeProse.Replace(segs[i]) + "`"
	}
	return strings.Join(segs, "")
}

var unescapeProse = strings.NewReplacer(
	"&lt;", "<",
	"&gt;", ">",
	"&#123;", "{",
	"&#125;", "}",
)

// renderHelpText converts cobra Long text to MDX. Help text indents its
// examples, snippets and aligned tables by two spaces, which reads well in a
// terminal but is nothing in MDX (indented code blocks are disabled): an
// example's "# comment" became a heading and its "--flags" em dashes. So
// every indented group is classified: code and aligned tables are fenced
// verbatim, lists and hanging-indent paragraphs are dedented into prose.
func renderHelpText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\t", "    "), "\n")

	type chunk struct {
		lang  string // fence language; "" means markdown prose
		lines []string
	}
	var chunks []chunk
	emit := func(lang string, ls []string) {
		// Code groups separated only by blank lines share one fence.
		if n := len(chunks); n > 0 && lang != "" && chunks[n-1].lang == lang {
			chunks[n-1].lines = append(append(chunks[n-1].lines, ""), ls...)
			return
		}
		chunks = append(chunks, chunk{lang, ls})
	}

	for i := 0; i < len(lines); {
		if !isIndented(lines[i]) {
			j := i
			for j < len(lines) && !isIndented(lines[j]) {
				j++
			}
			prose := trimBlankLines(lines[i:j])
			if len(prose) > 0 {
				chunks = append(chunks, chunk{"", prose})
			}
			i = j
			continue
		}
		j := i
		for j < len(lines) && isIndented(lines[j]) {
			j++
		}
		group := dedent(lines[i:j])
		emit(classifyIndented(group), group)
		i = j
	}

	var b strings.Builder
	for i, c := range chunks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if c.lang == "" {
			b.WriteString(escapeMDX(strings.Join(c.lines, "\n")))
		} else {
			fmt.Fprintf(&b, "```%s\n%s\n```", c.lang, strings.Join(c.lines, "\n"))
		}
	}
	return b.String()
}

func isIndented(line string) bool {
	return strings.TrimSpace(line) != "" && (line[0] == ' ' || line[0] == '\t')
}

func trimBlankLines(ls []string) []string {
	for len(ls) > 0 && strings.TrimSpace(ls[0]) == "" {
		ls = ls[1:]
	}
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}

func dedent(ls []string) []string {
	indent := -1
	for _, l := range ls {
		if n := len(l) - len(strings.TrimLeft(l, " ")); indent < 0 || n < indent {
			indent = n
		}
	}
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = strings.TrimRight(l[indent:], " ")
	}
	return out
}

var (
	// commandLine is a shell line an example would start with.
	commandLine = regexp.MustCompile(`^(ankra\b|ankra-module-|#|\$ |cat |echo |kubectl |helm |curl |export |[A-Z][A-Z0-9_]*=\S*\s)`)
	// flagRow opens a flag table ("--option k=v   what it does").
	flagRow   = regexp.MustCompile(`^--?[A-Za-z]`)
	yamlBlock = regexp.MustCompile(`^[A-Za-z_][\w.-]*:$`)
	listItem  = regexp.MustCompile(`^(-|\*|\d+\.)\s`)
	// aligned spots a column layout: two or more spaces after text.
	aligned = regexp.MustCompile(`\S {2,}\S`)
)

// classifyIndented names the fence language for a dedented group, or ""
// when it is a list or a hanging-indent paragraph that reads as prose.
func classifyIndented(ls []string) string {
	first := ls[0]
	switch {
	case strings.HasPrefix(first, "{") || strings.HasPrefix(first, "["):
		return "json"
	case yamlBlock.MatchString(first):
		return "yaml"
	case listItem.MatchString(first):
		return ""
	}
	for _, l := range ls {
		if commandLine.MatchString(strings.TrimSpace(l)) {
			return "bash"
		}
	}
	if flagRow.MatchString(first) {
		return "text"
	}
	for _, l := range ls {
		if aligned.MatchString(l) {
			return "text"
		}
	}
	return ""
}

// escapeTableCell additionally escapes pipes so flag usage strings cannot
// break the markdown table layout.
func escapeTableCell(s string) string {
	return strings.ReplaceAll(escapeMDX(s), "|", "\\|")
}

func renderFamilyPage(family *cobra.Command) string {
	var b strings.Builder

	title := family.CommandPath()
	desc := strings.TrimSpace(family.Short)
	if desc == "" {
		desc = "Reference for the " + title + " command."
	}
	fmt.Fprintf(&b, "---\ntitle: \"%s\"\ndescription: \"%s\"\n---\n\n", title, strings.ReplaceAll(desc, `"`, `'`))
	b.WriteString("{/* Generated by ankra-cli tools/gendocs — do not edit by hand. */}\n\n")

	renderCommand(&b, family)

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range visibleSubcommands(c) {
			renderCommand(&b, sub)
			walk(sub)
		}
	}
	walk(family)

	return b.String()
}

// renderCommand writes one command section. Headings are a flat h2 at every
// depth on purpose: each heading carries the full CommandPath, and Mintlify's
// page TOC only shows h2/h3.
func renderCommand(b *strings.Builder, c *cobra.Command) {
	fmt.Fprintf(b, "## %s\n\n", c.CommandPath())

	long := strings.TrimSpace(c.Long)
	short := strings.TrimSpace(c.Short)
	switch {
	case long != "":
		fmt.Fprintf(b, "%s\n\n", renderHelpText(long))
	case short != "":
		fmt.Fprintf(b, "%s\n\n", escapeMDX(short))
	}

	if c.Runnable() || len(c.Commands()) == 0 {
		fmt.Fprintf(b, "```bash\n%s\n```\n\n", c.UseLine())
	}

	if example := strings.TrimSpace(c.Example); example != "" {
		b.WriteString("**Examples**\n\n")
		fmt.Fprintf(b, "```bash\n%s\n```\n\n", example)
	}

	if flags := collectFlags(c.NonInheritedFlags()); len(flags) > 0 {
		b.WriteString("**Flags**\n\n")
		b.WriteString("| Flag | Default | Description |\n|------|---------|-------------|\n")
		for _, f := range flags {
			b.WriteString(f)
		}
		b.WriteString("\n")
	}
}

func collectFlags(fs *pflag.FlagSet) []string {
	var rows []string
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		name := "`--" + f.Name + "`"
		if f.Shorthand != "" {
			name = "`-" + f.Shorthand + "`, " + name
		}
		def := f.DefValue
		if def == "" {
			def = " "
		} else {
			// The default is wrapped in a code span, where entities render
			// literally — only pipes (table syntax) need escaping.
			def = "`" + strings.ReplaceAll(def, "|", "\\|") + "`"
		}
		rows = append(rows, fmt.Sprintf("| %s | %s | %s |\n", name, def, escapeTableCell(f.Usage)))
	})
	return rows
}

func renderIndexPage(root *cobra.Command, families []*cobra.Command) string {
	var b strings.Builder
	b.WriteString(`---
title: "CLI Command Reference"
description: "Every ankra CLI command, flag, and default — generated from the CLI source."
---

`)
	b.WriteString("{/* Generated by ankra-cli tools/gendocs — do not edit by hand. */}\n\n")
	fmt.Fprintf(&b, "This reference is generated from ankra CLI **v%s**. For installation and authentication, see the [CLI overview](/integrations/ankra-cli).\n\n", strings.TrimPrefix(root.Version, "v"))

	b.WriteString("## Commands\n\n| Command | Description |\n|---------|-------------|\n")
	for _, f := range families {
		fmt.Fprintf(&b, "| [%s](/reference/cli/%s) | %s |\n", f.CommandPath(), f.Name(), escapeTableCell(strings.TrimSpace(f.Short)))
	}
	b.WriteString("\n## Global Flags\n\nThese flags are accepted by every command:\n\n")
	b.WriteString("| Flag | Default | Description |\n|------|---------|-------------|\n")
	for _, row := range collectFlags(root.PersistentFlags()) {
		b.WriteString(row)
	}
	b.WriteString("\n")
	return b.String()
}
