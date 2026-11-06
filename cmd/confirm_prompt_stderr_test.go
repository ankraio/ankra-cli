package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// A confirmation prompt is a question to the person at the terminal, not
// output: written to stdout it lands inside the JSON a script reads from
// `-o json` without --yes, and in anything piped onward. Every confirmPrompt
// call writes it to stderr; this scan fails on one that names stdout.
func TestConfirmPromptsAreWrittenToStderr(t *testing.T) {
	files, globError := filepath.Glob("*.go")
	if globError != nil {
		t.Fatal(globError)
	}
	fileSet := token.NewFileSet()
	var problems []string
	calls := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseError := parser.ParseFile(fileSet, file, nil, parser.SkipObjectResolution)
		if parseError != nil {
			t.Fatalf("%s: %v", file, parseError)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			if name, isIdent := call.Fun.(*ast.Ident); !isIdent || name.Name != "confirmPrompt" || len(call.Args) < 2 {
				return true
			}
			calls++
			if namesStdout(call.Args[1]) {
				problems = append(problems, fileSet.Position(call.Pos()).String()+
					": confirmPrompt writes its prompt to stdout; pass the command's ErrOrStderr()")
			}
			return true
		})
	}
	if calls == 0 {
		t.Fatal("found no confirmPrompt call: the scan is not reading the package")
	}
	for _, problem := range problems {
		t.Error(problem)
	}
}

// namesStdout reports whether writer is os.Stdout or a command's
// OutOrStdout().
func namesStdout(writer ast.Expr) bool {
	if call, isCall := writer.(*ast.CallExpr); isCall {
		selector, isSelector := call.Fun.(*ast.SelectorExpr)
		return isSelector && selector.Sel.Name == "OutOrStdout"
	}
	selector, isSelector := writer.(*ast.SelectorExpr)
	if !isSelector {
		return false
	}
	pkg, isIdent := selector.X.(*ast.Ident)
	return isIdent && pkg.Name == "os" && selector.Sel.Name == "Stdout"
}
