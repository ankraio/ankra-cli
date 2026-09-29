package hiddenunicode

import "testing"

// The joiner rule mirrors joinerIsLegitimate in cluster
// enginekit/hiddenunicode (ankra-4r75g.13). Two halves matter and they pull in
// opposite directions, which is why both are pinned here:
//
//   - A joiner between Latin letters is smuggling. It renders as nothing, so
//     it hides text from the reader and makes prod-1 and prod<ZWJ>-1 look
//     identical on screen. Stripped.
//   - A joiner between letters of a script that uses joiners in ordinary
//     writing is doing its job. Stripping Persian ZWNJ reshapes a customer's
//     own name in text the CLI prints back to them. Kept.
//
// The look-behind skips combining marks, which is what makes the real
// Devanagari conjunct work: the joiner follows the virama, not the consonant.
func TestJoinerRule(t *testing.T) {
	const (
		zwj  = rune(0x200D)
		zwnj = rune(0x200C)
	)
	// Persian: mi + ZWNJ + ravam, a ZWNJ inside an ordinary word.
	persian := string([]rune{0x0645, 0x06CC, zwnj, 0x0631, 0x0648, 0x0645})
	// Devanagari conjunct with an explicit half-form: ka, virama, ZWJ, sha.
	// This is the order real text uses, and the order the combining-mark
	// look-behind exists for.
	devanagariAfterVirama := string([]rune{0x0915, 0x094D, zwj, 0x0937})
	// The same characters with the joiner on the wrong side of the virama.
	// Not a sequence real text produces, and not one the rule keeps: the
	// look-ahead lands on the virama, which is a mark rather than a letter.
	devanagariBeforeVirama := string([]rune{0x0915, zwj, 0x094D, 0x0937})
	// Arabic and Hebrew, two more non-Latin scripts.
	arabic := string([]rune{0x0628, zwnj, 0x062A})
	hebrew := string([]rune{0x05D0, zwj, 0x05D1})
	// Emoji, the carve-out that already existed.
	family := string([]rune{0x1F468, zwj, 0x1F469, zwj, 0x1F467})

	cases := []struct {
		name    string
		input   string
		want    string
		removed int
	}{
		{"persian zwnj inside a word is kept", persian, persian, 0},
		{"devanagari zwj after the virama is kept", devanagariAfterVirama, devanagariAfterVirama, 0},
		{"arabic zwnj is kept", arabic, arabic, 0},
		{"hebrew zwj is kept", hebrew, hebrew, 0},
		{"emoji joiners are kept", family, family, 0},
		{"zwj between latin letters is stripped", "de" + string(zwj) + "lete", "delete", 1},
		{"zwnj between latin letters is stripped", "de" + string(zwnj) + "lete", "delete", 1},
		{"zwj in an identifier is stripped", "prod" + string(zwj) + "-1", "prod-1", 1},
		{"zwj between greek letters is stripped", string([]rune{0x03B1, zwj, 0x03B2}), string([]rune{0x03B1, 0x03B2}), 1},
		{"zwj between cyrillic letters is stripped", string([]rune{0x0430, zwj, 0x0431}), string([]rune{0x0430, 0x0431}), 1},
		{"zwj between digits is stripped", "1" + string(zwj) + "2", "12", 1},
		{"zwj at the start is stripped", string(zwj) + arabic, arabic, 1},
		{"zwj at the end is stripped", arabic + string(zwj), arabic, 1},
		{"devanagari zwj before the virama is stripped", devanagariBeforeVirama, string([]rune{0x0915, 0x094D, 0x0937}), 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, removed := Strip(testCase.input)
			if got != testCase.want {
				t.Errorf("Strip() = %q, want %q", got, testCase.want)
			}
			if removed != testCase.removed {
				t.Errorf("Strip() removed %d, want %d", removed, testCase.removed)
			}
		})
	}
}
