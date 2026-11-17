package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

func completePreservedFixture(t *testing.T) *gitopsPreservedServer {
	t.Helper()
	entry := gitopsPreservedFixtures()[0]
	changes := make([]client.GitopsPreservedPathChange, 108)
	for index := range changes {
		changes[index] = client.GitopsPreservedPathChange{Path: fmt.Sprintf("spec.counter[%d]", index), Change: "changed", ValueMasked: true}
	}
	entry.StructuralDiff.Total, entry.StructuralDiff.Truncated = 108, true
	entry.StructuralDiff.Changes = changes[:50]
	recorder := newGitopsPreservedServer(t, []client.GitopsPreservedPath{entry})
	for offset := 0; offset < 108; offset += 50 {
		end := min(offset+50, 108)
		page := client.GitopsPreservedDiffPage{Path: entry.Path, HeadContentHash: entry.HeadContentHash,
			RenderContentHash: entry.RenderContentHash, HeadCommitSHA: entry.HeadCommitSHA, Status: "computed",
			Changes: changes[offset:end], Total: 108, Offset: offset, HasMore: end < 108, StructuralDiffValues: client.GitopsPreservedDiffValuesRevealed}
		if page.HasMore {
			page.NextCursor = preservedStringPointer(fmt.Sprintf("cursor-%d", end))
		}
		recorder.diffPages = append(recorder.diffPages, page)
	}
	return recorder
}

func TestPreservedShowAllPrintsCompleteMetadataAndLeavesDefaultPreview(t *testing.T) {
	for _, format := range []string{"json", "yaml", ""} {
		t.Run(format, func(t *testing.T) {
			recorder := completePreservedFixture(t)
			arguments := []string{"show", preservedIndexPath, "--all"}
			if format != "" {
				arguments = append(arguments, "-o", format)
			}
			stdout, _, runError := runGitopsPreserved(t, "", arguments...)
			if runError != nil {
				t.Fatal(runError)
			}
			if !strings.Contains(stdout, "spec.counter[107]") {
				t.Fatal("last difference was omitted")
			}
			if format == "json" {
				var page client.GitopsPreservedDiffPage
				if decodeError := json.Unmarshal([]byte(stdout), &page); decodeError != nil {
					t.Fatal(decodeError)
				}
				if len(page.Changes) != 108 || page.Total != 108 || page.HasMore || page.NextCursor != nil {
					t.Fatalf("incomplete output: %#v", page)
				}
			}
			if len(recorder.writes()) != 0 {
				t.Fatal("inspection sent a write")
			}
			for _, request := range recorder.seen() {
				if !strings.HasSuffix(request.path, "/diff") {
					t.Fatal("complete read depended on the capped path listing")
				}
			}
		})
	}
	t.Run("default remains preview", func(t *testing.T) {
		recorder := completePreservedFixture(t)
		stdout, _, runError := runGitopsPreserved(t, "", "show", preservedIndexPath, "-o", "json")
		if runError != nil {
			t.Fatal(runError)
		}
		var entry client.GitopsPreservedPath
		if decodeError := json.Unmarshal([]byte(stdout), &entry); decodeError != nil {
			t.Fatal(decodeError)
		}
		if len(entry.StructuralDiff.Changes) != 50 || !entry.StructuralDiff.Truncated || len(recorder.seen()) != 1 {
			t.Fatal("default preview changed")
		}
	})
}

func TestPreservedCompleteReadFailurePrintsNoPartialOutputAndSendsNoWrite(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		t.Run(fmt.Sprint(resolve), func(t *testing.T) {
			recorder := completePreservedFixture(t)
			recorder.diffPageFailure = 2
			arguments := []string{"show", preservedIndexPath, "--all", "-o", "json"}
			if resolve {
				arguments = []string{"resolve", preservedIndexPath, "--keep", "git", "--yes", "-o", "json"}
			}
			stdout, stderr, runError := runGitopsPreserved(t, "", arguments...)
			if runError == nil || stdout != "" || len(recorder.writes()) != 0 {
				t.Fatalf("partial output or write: stdout=%q stderr=%q error=%v", stdout, stderr, runError)
			}
			if strings.Contains(stderr, "Keep Git's version") {
				t.Fatal("confirmation was offered before the complete read succeeded")
			}
		})
	}
}

func TestPreservedResolveReadsCompleteMatchingSnapshotBeforeConfirmation(t *testing.T) {
	t.Run("all differences precede write", func(t *testing.T) {
		recorder := completePreservedFixture(t)
		_, stderr, runError := runGitopsPreserved(t, "", "resolve", preservedIndexPath, "--keep", "git", "--yes")
		if runError != nil {
			t.Fatal(runError)
		}
		if !strings.Contains(stderr, "spec.counter[107]") || strings.Contains(stderr, "not shown here") {
			t.Fatal("confirmation used only a capped preview")
		}
		if len(recorder.writes()) != 1 || len(recorder.seen()) != 5 {
			t.Fatal("expected list, three pages, then one resolve")
		}
	})
	t.Run("changed since listing prevents write", func(t *testing.T) {
		recorder := completePreservedFixture(t)
		for index := range recorder.diffPages {
			recorder.diffPages[index].HeadContentHash = "moved"
		}
		stdout, _, runError := runGitopsPreserved(t, "", "resolve", preservedIndexPath, "--keep", "git", "--yes")
		if runError == nil || !strings.Contains(runError.Error(), "changed") || stdout != "" || len(recorder.writes()) != 0 {
			t.Fatalf("changed snapshot accepted: %q %v", stdout, runError)
		}
	})
	t.Run("unsupported server prevents write", func(t *testing.T) {
		recorder := completePreservedFixture(t)
		recorder.diffPages = nil
		_, _, runError := runGitopsPreserved(t, "", "resolve", preservedIndexPath, "--keep", "git", "--yes")
		if runError == nil || len(recorder.writes()) != 0 {
			t.Fatal("missing complete route allowed an incomplete confirmation")
		}
	})
}
