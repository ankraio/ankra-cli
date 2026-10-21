package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

	got = renderHelpText("Kubeconfig:\n\n  users:\n  - name: ankra")
	if !strings.Contains(got, "```yaml\nusers:\n- name: ankra\n```") {
		t.Fatalf("yaml mapping not fenced:\n%s", got)
	}

	got = renderHelpText("Parents:\n\n  - name: infisical-ns\n    kind: manifest")
	if !strings.Contains(got, "```yaml\n- name: infisical-ns\n  kind: manifest\n```") {
		t.Fatalf("yaml sequence not fenced:\n%s", got)
	}

	got = renderHelpText("A manifest:\n\n  apiVersion: apps/v1\n  kind: Deployment\n  metadata:\n    name: web")
	if !strings.Contains(got, "```yaml\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n```") {
		t.Fatalf("inline-value yaml not fenced:\n%s", got)
	}

	// Groups merged across a blank line keep their relative indentation.
	got = renderHelpText("Values:\n\n  spec:\n    replicas: 2\n\n    template: {}")
	if !strings.Contains(got, "```yaml\nspec:\n  replicas: 2\n\n  template: {}\n```") {
		t.Fatalf("merged group lost its nesting:\n%s", got)
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
		"Then:\n\n" +
		"  - export the kubeconfig before deleting\n" +
		"  - cat nothing into it\n\n" +
		"  Examples:\n" +
		"    see the pipeline guide.\n\n" +
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
		"an empty --tag= clears it":                 "an empty `--tag`= clears it",
		`e.g. --note "before the upgrade" here`:     "e.g. `--note \"before the upgrade\"` here",
	}
	for in, want := range cases {
		if got := escapeMDX(in); got != want {
			t.Errorf("escapeMDX(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderExampleDedentsTheWholeBlock(t *testing.T) {
	// Cobra examples indent every line for the terminal. Trimming the whole
	// string left the first line flush and the rest two spaces in, so every
	// multi-line fence on docs.ankra.ai rendered ragged (ankra-7qk1wu).
	example := `
  # Bump the image tag
  ankra cluster manifests upgrade web \
    --set 'spec.image=nginx:1.27'

  ankra cluster manifests get web -o yaml
`
	want := "# Bump the image tag\n" +
		"ankra cluster manifests upgrade web \\\n" +
		"  --set 'spec.image=nginx:1.27'\n" +
		"\n" +
		"ankra cluster manifests get web -o yaml"
	if got := renderExample(example); got != want {
		t.Fatalf("renderExample mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	if got := renderExample("ankra version"); got != "ankra version" {
		t.Fatalf("an unindented one-liner changed: %q", got)
	}
	if got := renderExample("  \n\n  "); got != "" {
		t.Fatalf("a blank example rendered %q, want empty", got)
	}
}

func TestRenderCommandFencesDedentedExample(t *testing.T) {
	cmd := &cobra.Command{
		Use:     "lanes",
		Short:   "Set a lane",
		Run:     func(*cobra.Command, []string) {},
		Example: "  ankra ai lanes set chat z-ai/glm-5.2\n  ankra ai lanes list",
	}
	var b strings.Builder
	renderCommand(&b, cmd)
	want := "**Examples**\n\n```bash\nankra ai lanes set chat z-ai/glm-5.2\nankra ai lanes list\n```"
	if !strings.Contains(b.String(), want) {
		t.Fatalf("example fence not dedented:\n%s", b.String())
	}
}
