package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `package cmd
type APIClient interface { Add(int,int) int; Notify(...string) }
type baseMock struct{}
func (baseMock) Add(a,b int) int { return a+b }
func (baseMock) Notify(...string) {}
type customMock struct{}
func (*customMock) Add(a,b int) int { return a-b }
`

func writeFixture(t *testing.T, directory, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, "fixture_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func typeCheck(t *testing.T, directory string) error {
	t.Helper()
	fset := token.NewFileSet()
	files := []*ast.File{}
	for _, name := range []string{"fixture_test.go", outputName} {
		file, err := parser.ParseFile(fset, filepath.Join(directory, name), nil, 0)
		if err != nil {
			return err
		}
		files = append(files, file)
	}
	_, err := new(types.Config).Check("cmd", fset, files, nil)
	return err
}

func TestGeneratedForwardingAndExplicitOverrideContracts(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, fixture)
	if err := generate(directory, false); err != nil {
		t.Fatal(err)
	}
	if err := typeCheck(t, directory); err != nil {
		t.Fatal(err)
	}
	if err := generate(directory, true); err != nil {
		t.Fatal(err)
	}
	// The narrow mock still has the complete signature guarantee it had when
	// passed as APIClient, without compiling all unimplemented methods on it.
	writeFixture(t, directory, strings.Replace(fixture, "func (*customMock) Add(a,b int) int { return a-b }", "func (*customMock) Add(a,b string) int { return len(a)+len(b) }", 1))
	if err := typeCheck(t, directory); err == nil {
		t.Fatal("wrong override signature must fail compilation")
	}
}

func TestAnonymousEmbeddingAndStaleGeneratedOutputAreRefused(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, strings.Replace(fixture, "type customMock struct{}", "type customMock struct{ baseMock }", 1))
	if err := generate(directory, false); err == nil || !strings.Contains(err.Error(), "embeds baseMock") {
		t.Fatalf("anonymous embedding was not refused: %v", err)
	}
	writeFixture(t, directory, fixture)
	if err := generate(directory, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, outputName), []byte("package cmd\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := generate(directory, true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale generated code was not refused: %v", err)
	}
}

func TestStatefulFallbackIsRefused(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, strings.Replace(fixture, "type baseMock struct{}", "type baseMock struct{ state int }", 1))
	if err := generate(directory, false); err == nil || !strings.Contains(err.Error(), "stateless") {
		t.Fatalf("stateful fallback must require an explicit design: %v", err)
	}
}
