package main

import (
	"strings"
	"testing"
)

func TestRenderHelpTextFencesIndentedExamples(t *testing.T) {
	long := `Upgrade a manifest by patching just the fields you supply.

At least one mutating flag is required. Examples:

  # Patch a single path in-place, e.g. bump a Deployment image tag
  ankra cluster manifests upgrade web \
    --set 'spec.template.spec.containers[name=app].image=nginx:1.27' \
    --cluster website-demo

  # Read the manifest from stdin
  cat manifest.yaml | ankra cluster manifests upgrade demo-namespace \
    --manifest - --cluster website-demo

--set/--set-string MUTATE the existing manifest YAML.`

	want := "Upgrade a manifest by patching just the fields you supply.\n\n" +
		"At least one mutating flag is required. Examples:\n\n" +
		"```bash\n" +
		"# Patch a single path in-place, e.g. bump a Deployment image tag\n" +
		"ankra cluster manifests upgrade web \\\n" +
		"  --set 'spec.template.spec.containers[name=app].image=nginx:1.27' \\\n" +
		"  --cluster website-demo\n" +
		"\n" +
		"# Read the manifest from stdin\n" +
		"cat manifest.yaml | ankra cluster manifests upgrade demo-namespace \\\n" +
		"  --manifest - --cluster website-demo\n" +
		"```\n\n" +
		"`--set`/`--set-string` MUTATE the existing manifest YAML."

	if got := renderHelpText(long); got != want {
		t.Fatalf("renderHelpText mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderHelpTextFencesVerbatim(t *testing.T) {
	// Fenced content is not entity-escaped: MDX shows it literally.
	got := renderHelpText("Save it:\n\n  ankra cluster manifests get web > web.yaml")
	if !strings.Contains(got, "```bash\nankra cluster manifests get web > web.yaml\n```") {
		t.Fatalf("redirect not fenced verbatim:\n%s", got)
	}

	got = renderHelpText("Body:\n\n  {\n    \"limit\": 3\n  }")
	if !strings.Contains(got, "```json\n{\n  \"limit\": 3\n}\n```") {
		t.Fatalf("json snippet not fenced:\n%s", got)
	}

	got = renderHelpText("Installs:\n\n  skills     the SKILL.md files\n  rule       an always-applied instruction")
	if !strings.Contains(got, "```text\nskills     the SKILL.md files\n") {
		t.Fatalf("aligned table not fenced as text:\n%s", got)
	}

	got = renderHelpText("Options:\n\n  --option source=compose   which source to read\n  --option host=ssh://h    remote daemon")
	if !strings.Contains(got, "```text\n--option source=compose   which source to read\n") {
		t.Fatalf("flag table not fenced as text:\n%s", got)
	}
}

func TestRenderHelpTextKeepsListsAndParagraphsAsProse(t *testing.T) {
	long := "Deprovisioning means:\n\n" +
		"  - all cloud resources are released;\n" +
		"  - volumes are deleted unless you\n" +
		"    pass --accept-volume-data-loss.\n\n" +
		"Two modes:\n\n" +
		"  Cluster mode (default): fetch the manifest,\n" +
		"    decrypt it, and print to stdout."
	got := renderHelpText(long)
	if strings.Contains(got, "```") {
		t.Fatalf("prose list or paragraph was fenced:\n%s", got)
	}
	for _, want := range []string{
		"- all cloud resources are released;",
		"  pass `--accept-volume-data-loss`.",
		"Cluster mode (default): fetch the manifest,",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestEscapeMDXCodeSpansFlags(t *testing.T) {
	cases := map[string]string{
		"(--wait and --watch always exit this way)": "(`--wait` and `--watch` always exit this way)",
		"e.g. --set 'spec.replicas=3' (repeatable)": "e.g. `--set 'spec.replicas=3'` (repeatable)",
		"--output=json or -o yaml":                  "`--output=json` or -o yaml",
		"use `--cluster <id>` here":                 "use `--cluster <id>` here",
		"--from-file <path> replaces":               "`--from-file` &lt;path&gt; replaces",
		"pre-existing well-known names":             "pre-existing well-known names",
		"exclusive with --set*.":                    "exclusive with `--set*`.",
		"pass --output=json.":                       "pass `--output=json`.",
		"try --image=nginx:1.27:":                   "try `--image=nginx:1.27`:",
	}
	for in, want := range cases {
		if got := escapeMDX(in); got != want {
			t.Errorf("escapeMDX(%q) = %q, want %q", in, got, want)
		}
	}
}
