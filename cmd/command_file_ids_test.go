package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// An id in a help example is made up. Examples used to be pasted from live
// output, so --help, the generated reference and this repository showed ids
// that were not example values. Every UUID in a command file is therefore
// one of the made-up family below, or one of the older made-up ids. The family varies in its first segment's last byte, so
// short ids stay different, and in its very last byte, so an example can
// show several ids. A new example takes its id from the family.
var madeUpExampleID = regexp.MustCompile(`^0b7c4d[0-9a-f]{2}-5f6a-4b8c-9d0e-1f2a3b4c5d[0-9a-f]{2}$`)

var olderMadeUpIDs = map[string]bool{
	"00000000-0000-0000-0000-000000000000": true,
	"5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3": true,
	"6f1f9aca-2c3d-4e5f-8a9b-0c1d2e3f4a5b": true,
}

func TestEveryIDInACommandFileIsMadeUp(t *testing.T) {
	anyID := regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	files, globError := filepath.Glob("*.go")
	if globError != nil {
		t.Fatal(globError)
	}
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, readError := os.ReadFile(file)
		if readError != nil {
			t.Fatalf("%s: %v", file, readError)
		}
		for number, line := range strings.Split(string(source), "\n") {
			for _, id := range anyID.FindAllString(line, -1) {
				checked++
				lowered := strings.ToLower(id)
				if madeUpExampleID.MatchString(lowered) || olderMadeUpIDs[lowered] {
					continue
				}
				t.Errorf("%s:%d: %s is not a made-up id; use one of the form 0b7c4dNN-5f6a-4b8c-9d0e-1f2a3b4c5d6e",
					file, number+1, id)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no id in any command file: the scan is not reading the package")
	}
}
