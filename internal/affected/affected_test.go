package affected

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// fixtureRepository writes a small Go monorepo with a module wired in by a
// replace directive, and a pnpm workspace beside it, and stages it.
func fixtureRepository(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the analysis needs a POSIX shell")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":               "module example.com/mono\n\nreplace example.com/engine => ./engine\n",
		"engine/go.mod":        "module example.com/engine\n",
		"engine/plan/plan.go":  "package plan\n",
		"api/api.go":           "package api\n\nimport \"example.com/engine/plan\"\n",
		"lib/util/util.go":     "package util\n",
		"web/app/package.json": `{"name": "@acme/app", "dependencies": {"@acme/ui": "workspace:*"}}`,
		"web/ui/package.json":  `{"name": "@acme/ui"}`,
		"web/ui/index.ts":      "export {}\n",
		"docs/guide.md":        "# guide\n",
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
	return root
}

var fixtureTargets = []Target{
	{Stage: "api", Go: []string{"dir:api/..."}},
	{Stage: "util", Go: []string{"dir:lib/util"}},
	{Stage: "app", Pnpm: []string{"name:@acme/app"}},
	{Stage: "typo", Go: []string{"dir:lib/utill"}},
}

func TestAnalyseFollowsTheGraphAcrossModulesAndWorkspaces(t *testing.T) {
	root := fixtureRepository(t)
	cases := []struct {
		changed []string
		want    map[string]bool
	}{
		{[]string{"engine/plan/plan.go"}, map[string]bool{"api": true, "util": false, "app": false}},
		{[]string{"web/ui/index.ts"}, map[string]bool{"api": false, "util": false, "app": true}},
		{[]string{"docs/guide.md"}, map[string]bool{"api": false, "util": false, "app": false}},
	}
	for _, testCase := range cases {
		answers, log, analyseError := Analyse(context.Background(), root, testCase.changed, fixtureTargets)
		if analyseError != nil {
			t.Fatalf("%v: %v\n%s", testCase.changed, analyseError, log)
		}
		if !reflect.DeepEqual(answers, testCase.want) {
			t.Fatalf("%v: answers = %v, want %v (a typo gets no verdict)\n%s", testCase.changed, answers, testCase.want, log)
		}
	}
}

func TestAnalyseOutsideARepositoryAnswersNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the analysis needs a POSIX shell")
	}
	answers, _, analyseError := Analyse(context.Background(), t.TempDir(), []string{"a.go"}, fixtureTargets)
	if analyseError == nil || len(answers) != 0 {
		t.Fatalf("answers %v, error %v; want no verdict and an error", answers, analyseError)
	}
}

func TestProgramDigestIsAHexSHA256OfTheProgram(t *testing.T) {
	if digest := ProgramDigest(); len(digest) != 64 {
		t.Fatalf("ProgramDigest() = %q", digest)
	}
	if parsed := parseAnswers("ankra.affected.a=true\nankra.affected.b=false\nankra.affected.c=maybe\nsha=x\n"); !reflect.DeepEqual(parsed, map[string]bool{"a": true, "b": false}) {
		t.Fatalf("parseAnswers = %v", parsed)
	}
}
