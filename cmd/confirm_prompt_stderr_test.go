package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A confirmation prompt is a question to the person at the terminal, not
// output: written to stdout it lands inside the JSON a script reads from
// `-o json` without --yes, and in anything piped onward. Every prompt is
// written to stderr; this scan fails on one that may reach stdout.
func TestConfirmPromptsAreWrittenToStderr(t *testing.T) {
	files, globError := filepath.Glob("*.go")
	if globError != nil {
		t.Fatal(globError)
	}
	fileSet := token.NewFileSet()
	var parsed []*ast.File
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsedFile, parseError := parser.ParseFile(fileSet, file, nil, parser.SkipObjectResolution)
		if parseError != nil {
			t.Fatalf("%s: %v", file, parseError)
		}
		parsed = append(parsed, parsedFile)
	}
	problems, confirmCalls := promptsThatMayReachStdout(fileSet, parsed)
	if confirmCalls == 0 {
		t.Fatal("found no confirmPrompt call: the scan is not reading the package")
	}
	for _, problem := range problems {
		t.Error(problem)
	}
}

// The scan catches a prompt whose writer only may be stdout: a variable
// that one branch points at stdout, a helper's writer parameter that a
// caller fills with stdout, and a question printed straight to stdout. The
// stderr forms pass.
func TestThePromptScanFollowsVariablesAndParameters(t *testing.T) {
	const source = `package cmd

func narrated(cmd *cobra.Command, structured bool) error {
	narration := cmd.OutOrStdout()
	if structured {
		narration = cmd.ErrOrStderr()
	}
	return confirmPrompt(cmd.InOrStdin(), narration, "Go? [y/N]: ", false)
}

func askRefresh(in io.Reader, out io.Writer) {
	_, _ = fmt.Fprintf(out, "Also refresh? [Y/n]: ")
}

func callsTheHelperWithStdout(cmd *cobra.Command) {
	askRefresh(cmd.InOrStdin(), cmd.OutOrStdout())
}

func printsTheQuestion() {
	fmt.Print("Run this action? [y/N]: ")
}

var stderrOnly = func(cmd *cobra.Command) error {
	errOut := cmd.ErrOrStderr()
	askRefresh(cmd.InOrStdin(), errOut)
	return confirmPrompt(cmd.InOrStdin(), errOut, "Go? [y/N]: ", false)
}
`
	fileSet := token.NewFileSet()
	parsed, parseError := parser.ParseFile(fileSet, "fixture.go", source, parser.SkipObjectResolution)
	if parseError != nil {
		t.Fatal(parseError)
	}
	problems, confirmCalls := promptsThatMayReachStdout(fileSet, []*ast.File{parsed})
	if confirmCalls != 2 {
		t.Errorf("confirmPrompt calls = %d, want 2", confirmCalls)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"fixture.go:8:", "fixture.go:16:", "fixture.go:20:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the scan missed the prompt at %s; it reported:\n%s", want, joined)
		}
	}
	if len(problems) != 3 {
		t.Errorf("the scan reported %d prompts, want 3 (the stderr ones pass):\n%s", len(problems), joined)
	}
}

// promptsThatMayReachStdout reports every prompt in files whose writer may
// be stdout, and counts the confirmPrompt calls it read. A prompt is:
//
//   - a confirmPrompt call, or a call of a prompt helper: a function that
//     hands one of its writer parameters on to a prompt
//     (confirmNamespaceChange, decideSkillsRefresh), found here, not listed;
//   - an fmt.Fprint, Fprintf or Fprintln of a "[y/N]" or "[Y/n]" question;
//   - an fmt.Print, Printf or Println of one, which is always stdout.
//
// A writer may be stdout when it is os.Stdout or an OutOrStdout() call, or
// when it is a variable that any assignment in its function gives one of
// those (narration := cmd.OutOrStdout(), then stderr only for -o). A writer
// that is a parameter makes its function a prompt helper, and the helper's
// callers are checked in turn.
func promptsThatMayReachStdout(fileSet *token.FileSet, files []*ast.File) ([]string, int) {
	helpers := map[string]map[int]bool{"confirmPrompt": {1: true}}
	for {
		problems, confirmCalls, grew := scanPrompts(fileSet, files, helpers)
		if !grew {
			sort.Strings(problems)
			return problems, confirmCalls
		}
	}
}

// scanPrompts is one pass of promptsThatMayReachStdout. It adds to helpers
// every function found passing a parameter on as a prompt's writer, and
// reports whether it added one, so the caller passes again until the set
// is complete.
func scanPrompts(fileSet *token.FileSet, files []*ast.File, helpers map[string]map[int]bool) ([]string, int, bool) {
	var problems []string
	confirmCalls, grew := 0, false
	check := func(position token.Pos, what string, writer ast.Expr, scopes []promptScope) {
		mayBeStdout, parameter := promptWriter(writer, scopes)
		switch {
		case mayBeStdout:
			problems = append(problems, fileSet.Position(position).String()+": "+what+
				" may write its prompt to stdout; write it to the command's ErrOrStderr()")
		case parameter >= 0:
			name := scopes[len(scopes)-1].name
			if name == "" {
				problems = append(problems, fileSet.Position(position).String()+": "+what+
					" writes its prompt to a parameter of a function literal, whose callers the scan cannot "+
					"follow; pass the command's ErrOrStderr() there instead")
				return
			}
			if helpers[name] == nil {
				helpers[name] = map[int]bool{}
			}
			if !helpers[name][parameter] {
				helpers[name][parameter], grew = true, true
			}
		}
	}
	for _, file := range files {
		var scopes []promptScope
		var stack []ast.Node
		ast.Inspect(file, func(node ast.Node) bool {
			if node == nil {
				if _, isFunction := stack[len(stack)-1].(*ast.FuncDecl); isFunction {
					scopes = scopes[:len(scopes)-1]
				} else if _, isFunction := stack[len(stack)-1].(*ast.FuncLit); isFunction {
					scopes = scopes[:len(scopes)-1]
				}
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, node)
			switch typed := node.(type) {
			case *ast.FuncDecl:
				scopes = append(scopes, promptScope{name: typed.Name.Name, parameters: parameterIndexes(typed.Type),
					body: typed.Body})
			case *ast.FuncLit:
				scopes = append(scopes, promptScope{parameters: parameterIndexes(typed.Type), body: typed.Body})
			case *ast.CallExpr:
				if len(scopes) == 0 {
					return true
				}
				callee := calledName(typed)
				for index := range helpers[callee] {
					if index >= len(typed.Args) {
						continue
					}
					if callee == "confirmPrompt" {
						confirmCalls++
					}
					check(typed.Pos(), callee, typed.Args[index], scopes)
				}
				if function, isFmt := fmtCall(typed); isFmt && asksAQuestion(typed.Args) {
					switch function {
					case "Fprint", "Fprintf", "Fprintln":
						check(typed.Pos(), "fmt."+function+" of a question", typed.Args[0], scopes)
					case "Print", "Printf", "Println":
						problems = append(problems, fileSet.Position(typed.Pos()).String()+": fmt."+function+
							" prints a question to stdout; write it to the command's ErrOrStderr()")
					}
				}
			}
			return true
		})
	}
	return problems, confirmCalls, grew
}

// promptScope is one function enclosing a prompt: its name ("" for a
// function literal), its parameters by name, and its body.
type promptScope struct {
	name       string
	parameters map[string]int
	body       *ast.BlockStmt
}

func parameterIndexes(functionType *ast.FuncType) map[string]int {
	indexes := map[string]int{}
	index := 0
	for _, field := range functionType.Params.List {
		if len(field.Names) == 0 {
			index++
			continue
		}
		for _, name := range field.Names {
			indexes[name.Name] = index
			index++
		}
	}
	return indexes
}

// promptWriter says where a prompt's writer may lead: to stdout, or to a
// parameter of the innermost enclosing function (its index), or neither
// (-1). A variable is followed through every assignment in the outermost
// enclosing function, which holds every closure that could set it.
func promptWriter(writer ast.Expr, scopes []promptScope) (mayBeStdout bool, parameter int) {
	if namesStdout(writer) {
		return true, -1
	}
	identifier, isIdentifier := writer.(*ast.Ident)
	if !isIdentifier {
		return false, -1
	}
	if outermost := scopes[0].body; outermost != nil && assignsStdout(outermost, identifier.Name) {
		return true, -1
	}
	if index, isParameter := scopes[len(scopes)-1].parameters[identifier.Name]; isParameter {
		return false, index
	}
	return false, -1
}

// assignsStdout reports whether any assignment or declaration in body gives
// the variable name stdout.
func assignsStdout(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.AssignStmt:
			if len(typed.Lhs) == len(typed.Rhs) {
				for index, target := range typed.Lhs {
					if identifier, isIdentifier := target.(*ast.Ident); isIdentifier && identifier.Name == name &&
						namesStdout(typed.Rhs[index]) {
						found = true
					}
				}
			}
		case *ast.ValueSpec:
			if len(typed.Names) == len(typed.Values) {
				for index, declared := range typed.Names {
					if declared.Name == name && namesStdout(typed.Values[index]) {
						found = true
					}
				}
			}
		}
		return !found
	})
	return found
}

// calledName is the name a call calls: a function's, or a method's.
func calledName(call *ast.CallExpr) string {
	switch callee := call.Fun.(type) {
	case *ast.Ident:
		return callee.Name
	case *ast.SelectorExpr:
		return callee.Sel.Name
	}
	return ""
}

// fmtCall reports the fmt function a call calls, if it calls one.
func fmtCall(call *ast.CallExpr) (string, bool) {
	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	if !isSelector {
		return "", false
	}
	pkg, isIdentifier := selector.X.(*ast.Ident)
	if !isIdentifier || pkg.Name != "fmt" {
		return "", false
	}
	return selector.Sel.Name, true
}

// asksAQuestion reports whether a string literal among arguments ends a
// question the way every prompt here does: "[y/N]" or "[Y/n]".
func asksAQuestion(arguments []ast.Expr) bool {
	found := false
	for _, argument := range arguments {
		ast.Inspect(argument, func(node ast.Node) bool {
			if literal, isLiteral := node.(*ast.BasicLit); isLiteral && literal.Kind == token.STRING &&
				(strings.Contains(literal.Value, "[y/N]") || strings.Contains(literal.Value, "[Y/n]")) {
				found = true
			}
			return !found
		})
	}
	return found
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
