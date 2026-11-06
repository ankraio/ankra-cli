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

// cmdHTTPClients is every http.Client this package builds itself, keyed by
// the file and the declaration that builds it, and what it talks to. None
// talks to the API: a client that does comes from the client package
// (client.NewGuardedHTTPClient, or the API client itself), behind the check
// that refuses a path the platform's edge would rewrite. The client
// package's own scan (internal/client/request_path_guard_test.go) covers
// what it builds.
var cmdHTTPClients = map[string]string{
	"clone.go:httpDownloadClient":                        "a remote cluster YAML and its files, at URLs the user gives",
	"migrate_modules_install.go:newModuleDownloadClient": "migration module release downloads",
	"targets_register.go:currentHostAgentHost":           "ankra-host-agent release downloads (--release-url)",
	"upgrade.go:runUpgrade":                              "GitHub releases of the CLI",
}

// The scan that keeps cmdHTTPClients complete: every http.Client made here (a
// literal, new(http.Client) or a declared http.Client value) is listed, and
// nothing uses net/http's package-level client or transport or dials a
// websocket, all of which would reach the API around the client package.
func TestEveryHTTPClientInCmdIsListed(t *testing.T) {
	files, globError := filepath.Glob("*.go")
	if globError != nil {
		t.Fatal(globError)
	}
	fileSet := token.NewFileSet()
	seen := map[string]bool{}
	var problems []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseError := parser.ParseFile(fileSet, file, nil, parser.SkipObjectResolution)
		if parseError != nil {
			t.Fatalf("%s: %v", file, parseError)
		}
		for _, declaration := range parsed.Decls {
			key := file + ":" + cmdDeclarationOwner(declaration)
			builds := func(position token.Pos, form string) {
				seen[key] = true
				if _, listed := cmdHTTPClients[key]; !listed {
					problems = append(problems, fileSet.Position(position).String()+": "+form+" built in cmd ("+
						key+"): an API client comes from the client package; anything else is listed in "+
						"cmdHTTPClients with what it talks to")
				}
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.CompositeLit:
					if cmdIsPackageSelector(typed.Type, "http", "Client") {
						builds(typed.Pos(), "an http.Client")
					}
				case *ast.ValueSpec:
					if typed.Type != nil && cmdIsPackageSelector(typed.Type, "http", "Client") {
						builds(typed.Pos(), "a declared http.Client value")
					}
				case *ast.CallExpr:
					if builtin, isIdent := typed.Fun.(*ast.Ident); isIdent && builtin.Name == "new" &&
						len(typed.Args) == 1 && cmdIsPackageSelector(typed.Args[0], "http", "Client") {
						builds(typed.Pos(), "new(http.Client)")
					}
					if cmdIsPackageSelector(typed.Fun, "websocket", "Dial") {
						problems = append(problems, fileSet.Position(typed.Pos()).String()+
							": a websocket dial in cmd; the client package's terminal lane is the one dial")
					}
				case *ast.SelectorExpr:
					for _, name := range []string{"DefaultClient", "DefaultTransport", "Get", "Head", "Post", "PostForm"} {
						if cmdIsPackageSelector(typed, "http", name) {
							problems = append(problems, fileSet.Position(typed.Pos()).String()+
								": http."+name+" in cmd sends around the client package")
						}
					}
				}
				return true
			})
		}
	}
	for key := range cmdHTTPClients {
		if !seen[key] {
			problems = append(problems, key+" builds no http.Client any more: drop it from cmdHTTPClients")
		}
	}
	sort.Strings(problems)
	for _, problem := range problems {
		t.Error(problem)
	}
}

// cmdDeclarationOwner names a top-level declaration: a function or method
// by its name, a package-level variable by its first name.
func cmdDeclarationOwner(declaration ast.Decl) string {
	switch typed := declaration.(type) {
	case *ast.FuncDecl:
		return typed.Name.Name
	case *ast.GenDecl:
		for _, spec := range typed.Specs {
			if valueSpec, isValue := spec.(*ast.ValueSpec); isValue && len(valueSpec.Names) > 0 {
				return valueSpec.Names[0].Name
			}
		}
	}
	return ""
}

func cmdIsPackageSelector(expression ast.Expr, packageName, name string) bool {
	selector, isSelector := expression.(*ast.SelectorExpr)
	if !isSelector || selector.Sel.Name != name {
		return false
	}
	identifier, isIdentifier := selector.X.(*ast.Ident)
	return isIdentifier && identifier.Name == packageName
}
