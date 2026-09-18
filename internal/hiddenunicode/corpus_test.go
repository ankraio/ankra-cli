package hiddenunicode

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// The conformance corpus is the contract for what counts as hidden, shared
// with every other implementation of this strip: cluster
// enginekit/hiddenunicode (the source of truth, which carries the same file at
// enginekit/hiddenunicode/testdata/corpus.json), the portal TypeScript util,
// the agent's copy of this package, and the Python detector the support agents
// use. Six implementations in five repositories and three languages cannot
// share code, so they share vectors instead (ankra-4r75g.11).
//
// It exists because the classes drifted once already: the flag carve-out
// shipped as "any short lowercase tag run after U+1F3F4", which smuggles
// words, because "close" is five letters and a black flag is an ordinary
// character. One reviewer caught it on one side; nothing would have caught it
// in the other four.
//
// A case here is never edited to make an implementation pass. If this
// package disagrees with a vector, that disagreement is the finding.
const (
	// canonicalCorpusMD5 is the checksum of the canonical file this copy was
	// vendored from. It pins provenance: editing testdata/corpus.json in this
	// repository fails this test, which is the failure mode worth stopping,
	// because a fixture quietly adjusted to match the code protects nothing.
	//
	// What it cannot do, stated plainly: detect that the canonical file has
	// moved ahead. That comparison needs the canonical copy, which lives in
	// another repository, so it cannot run from inside this one. The
	// cross-repo gate is still open on ankra-4r75g.11.
	canonicalCorpusMD5 = "34a3bb79c70f03bd61cefc3411c06176"

	// canonicalCorpusVersion is the corpus schema version this loader
	// understands. A bump means the shape changed, so the loader is reviewed
	// rather than the file silently accepted.
	canonicalCorpusVersion = 1
)

type corpusCase struct {
	Name    string   `json:"name"`
	Note    string   `json:"note"`
	Input   string   `json:"input"`
	Output  string   `json:"output"`
	Removed int      `json:"removed"`
	Classes []string `json:"classes"`
}

type corpusFoldCase struct {
	Name  string `json:"name"`
	Note  string `json:"note"`
	Input string `json:"input"`
	Fold  string `json:"fold"`
}

type contractCorpus struct {
	Version   int              `json:"version"`
	Cases     []corpusCase     `json:"cases"`
	FoldCases []corpusFoldCase `json:"fold_cases"`
}

const corpusPath = "testdata/corpus.json"

func loadCorpus(t *testing.T) contractCorpus {
	t.Helper()
	raw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatalf("read %s: %v", corpusPath, err)
	}
	// The canonical file is plain JSON with \uXXXX escapes (surrogate pairs
	// for astral characters), so the standard decoder is enough and no
	// hand-rolled unescaper is needed: JSON defines no \U introducer, and a
	// custom one that accepted eight-digit escapes was a real bug on the
	// cluster side.
	var corpus contractCorpus
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatalf("parse %s: %v", corpusPath, err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatalf("%s carries no cases", corpusPath)
	}
	return corpus
}

func TestVendoredCorpusMatchesCanonical(t *testing.T) {
	raw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatalf("read %s: %v", corpusPath, err)
	}
	sum := md5.Sum(raw)
	if got := hex.EncodeToString(sum[:]); got != canonicalCorpusMD5 {
		t.Errorf("%s has md5 %s, want %s.\n"+
			"This copy is vendored from the canonical corpus and must be byte-for-byte "+
			"identical to it. If you changed it to make a test pass, that is the bug: "+
			"change the canonical file (cluster enginekit/hiddenunicode/testdata/corpus.json), "+
			"re-vendor, and update canonicalCorpusMD5 in the same commit.",
			corpusPath, got, canonicalCorpusMD5)
	}
	corpus := loadCorpus(t)
	if corpus.Version != canonicalCorpusVersion {
		t.Errorf("corpus version %d, loader understands %d: review the loader before bumping",
			corpus.Version, canonicalCorpusVersion)
	}
}

func TestContractCorpus(t *testing.T) {
	corpus := loadCorpus(t)
	for _, testCase := range corpus.Cases {
		t.Run(testCase.name(), func(t *testing.T) {
			got, removed := Strip(testCase.Input)
			if got != testCase.Output {
				t.Errorf("Strip(%q) = %q, want %q\ncase note: %s",
					testCase.Input, got, testCase.Output, testCase.Note)
			}
			if removed != testCase.Removed {
				t.Errorf("Strip(%q) removed %d, want %d\ncase note: %s",
					testCase.Input, removed, testCase.Removed, testCase.Note)
			}
			// The corpus's own invariant: a case that removes nothing names no
			// classes, and a case that removes something names at least one.
			if (testCase.Removed == 0) != (len(testCase.Classes) == 0) {
				t.Errorf("case %q has removed=%d and classes=%v, which disagree",
					testCase.Name, testCase.Removed, testCase.Classes)
			}
		})
	}
}

// TestCorpusFoldCasesAreNotAssertedHere records why the corpus's fold vectors
// are read but not asserted: Fold is the NFKC-normalised form an
// implementation matches marker literals against, and this package has no
// Fold because nothing in the CLI matches markers. The vectors are checked for
// presence so that a corpus which grows fold expectations relevant to us is
// noticed rather than silently skipped.
func TestCorpusFoldCasesAreNotAssertedHere(t *testing.T) {
	corpus := loadCorpus(t)
	if len(corpus.FoldCases) == 0 {
		t.Skip("corpus carries no fold vectors")
	}
	for _, foldCase := range corpus.FoldCases {
		t.Logf("fold vector %q not asserted: this package has no Fold (see the package doc)", foldCase.Name)
	}
}

// TestRepoLocalExtraCases holds the vectors from this repository's original
// 32-case table that the canonical corpus does not cover. Everything else in
// that table is covered by a canonical case, so keeping it would have meant
// maintaining two corpora, which is the drift this whole exercise is about.
//
// These two belong upstream; they are here until the canonical file absorbs
// them (noted on ankra-4r75g.11).
func TestRepoLocalExtraCases(t *testing.T) {
	firstStrongIsolate := string(rune(0x2068))
	startOfHeading := string(rune(0x0001))
	extra := []struct {
		name    string
		reason  string
		input   string
		want    string
		removed int
	}{
		{
			name:    "first strong isolate is removed",
			reason:  "the canonical bidi case covers U+202E/202C/2066/2069/200E/200F/061C but not U+2068",
			input:   "a" + firstStrongIsolate + "b",
			want:    "ab",
			removed: 1,
		},
		{
			name:    "start of heading is removed",
			reason:  "the canonical control cases use U+0007/001B/007F/000B/000C/0085/001C, not U+0001",
			input:   "a" + startOfHeading + "b",
			want:    "ab",
			removed: 1,
		},
	}
	for _, testCase := range extra {
		t.Run(testCase.name, func(t *testing.T) {
			got, removed := Strip(testCase.input)
			if got != testCase.want || removed != testCase.removed {
				t.Errorf("Strip() = %q (removed %d), want %q (removed %d)\nwhy this case is local: %s",
					got, removed, testCase.want, testCase.removed, testCase.reason)
			}
		})
	}
}

// name is the subtest name, falling back to the input when a case is
// unnamed so a failure still points somewhere.
func (c corpusCase) name() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Input
}
