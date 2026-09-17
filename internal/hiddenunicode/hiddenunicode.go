// Package hiddenunicode removes invisible Unicode from text the CLI is about
// to print to a terminal, write into a file an AI assistant reads, or send to
// the platform's AI.
//
// Unicode Tag characters (U+E0000-E007F) mirror ASCII one to one and render
// as nothing, so a proposal description, a suggested command or a cluster
// name can carry text the operator never sees while a model reads it in full.
// Bidirectional controls are worse on a terminal: they reorder what is
// displayed, so a printed command can read differently than the bytes it is
// made of, and the operator approves or pastes something other than what they
// read. C0/C1 controls are removed for the same reason: an ANSI escape inside
// server-authored text can repaint the screen around it.
//
// This duplicates the semantics of ankra.cloud/enginekit/hiddenunicode in the
// cluster repo (Strip). ankra-cli is a separate repository and Go module and
// deliberately has no dependency on it, so the two are kept in step by hand;
// if you change the classes here, change them there. Unlike the cluster copy
// this package has no Fold: nothing in the CLI matches marker literals, so
// there is no NFKC-folded comparison to make.
//
// What is kept, deliberately:
//   - U+FE0F, the emoji presentation selector, and zero-width joiners that
//     sit between emoji, so a family emoji (two joiners) and a warning sign
//     with its presentation selector survive intact.
//   - The tag runs of the three subdivision flags that actually exist
//     (gbeng, gbsct, gbwls). That is a closed list, not a shape: a review on
//     the cluster side found that accepting "any short lowercase tag run"
//     after U+1F3F4 smuggles words, because "close" is five letters and a
//     black flag is an ordinary character.
package hiddenunicode

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	flagBase        = 0x1F3F4
	tagCancel       = 0xE007F
	variationSel16  = 0xFE0F
	tagBlockStart   = 0xE0000
	tagBlockEnd     = 0xE007F
	skinToneStart   = 0x1F3FB
	skinToneEnd     = 0x1F3FF
	zeroWidthJoiner = 0x200D
)

// rgiSubdivisionTags are the only subdivision-flag tag sequences with
// recommended emoji presentation, so they are the only ones whose tag runs
// survive.
var rgiSubdivisionTags = map[string]bool{"gbeng": true, "gbsct": true, "gbwls": true}

// emojiRanges are the blocks a rune must fall in to count as an emoji
// neighbour for the joiner carve-out.
var emojiRanges = [][2]rune{
	{0x1F000, 0x1FAFF},
	{0x2190, 0x21FF},
	{0x2300, 0x23FF},
	{0x2600, 0x27BF},
	{0x2B00, 0x2BFF},
	{0x3030, 0x303D},
}

var emojiSingles = map[rune]bool{
	0x00A9: true, 0x00AE: true, 0x203C: true, 0x2049: true,
	0x2122: true, 0x2139: true, 0x20E3: true,
}

// hiddenSingles are the invisible runes that unicode.Cf and friends do not
// already cover, or that are worth naming for the reader.
var hiddenSingles = map[rune]bool{
	0x034F: true, // combining grapheme joiner
	0x115F: true, // hangul choseong filler
	0x1160: true, // hangul jungseong filler
	0x3164: true, // hangul filler
	0xFFA0: true, // halfwidth hangul filler
}

func inRanges(r rune, ranges [][2]rune) bool {
	for _, span := range ranges {
		if r >= span[0] && r <= span[1] {
			return true
		}
	}
	return false
}

func isEmoji(r rune) bool {
	return emojiSingles[r] || inRanges(r, emojiRanges)
}

func isEmojiModifier(r rune) bool {
	return r == variationSel16 || (r >= skinToneStart && r <= skinToneEnd)
}

// isVariationSelector covers the selectors that carry a byte each when abused
// as a smuggling channel. U+FE0F is excluded: it is load-bearing for emoji.
func isVariationSelector(r rune) bool {
	switch {
	case r >= 0xFE00 && r <= 0xFE0E:
		return true
	case r >= 0xE0100 && r <= 0xE01EF:
		return true
	case r >= 0x180B && r <= 0x180D, r == 0x180F:
		return true
	}
	return false
}

// isHidden reports whether r has no business in text a person is about to
// read. Tabs, newlines and carriage returns are layout, not hidden.
func isHidden(r rune) bool {
	switch {
	case r >= tagBlockStart && r <= tagBlockEnd:
		return true
	case isVariationSelector(r):
		return true
	case hiddenSingles[r]:
		return true
	case r == variationSel16:
		return false
	case r == '\t' || r == '\n' || r == '\r':
		return false
	case unicode.Is(unicode.Cf, r):
		return true // zero-width, bidi controls, soft hyphen, U+180E
	case unicode.Is(unicode.Co, r):
		return true // private use: renders as tofu or nothing
	case unicode.Is(unicode.Cc, r):
		return true // C0/C1, including ESC: no ANSI from server-authored text
	}
	return false
}

// joinsEmoji reports whether the joiner at runes[index] sits between two
// emoji, skipping the modifiers that may sit in between.
func joinsEmoji(runes []rune, index int) bool {
	left := index - 1
	for left >= 0 && isEmojiModifier(runes[left]) {
		left--
	}
	right := index + 1
	for right < len(runes) && runes[right] == variationSel16 {
		right++
	}
	if left < 0 || right >= len(runes) {
		return false
	}
	return isEmoji(runes[left]) && isEmoji(runes[right])
}

// flagTagEnd returns the index just past a real subdivision-flag tag run that
// starts after the U+1F3F4 at index, or -1 when what follows is not one of
// the three flags.
func flagTagEnd(runes []rune, index int) int {
	cursor := index + 1
	var spelled strings.Builder
	for cursor < len(runes) && cursor-index <= 7 {
		r := runes[cursor]
		if (r >= 0xE0030 && r <= 0xE0039) || (r >= 0xE0061 && r <= 0xE007A) {
			spelled.WriteRune(r - tagBlockStart)
			cursor++
			continue
		}
		break
	}
	if cursor > index+1 && cursor < len(runes) && runes[cursor] == tagCancel &&
		rgiSubdivisionTags[spelled.String()] {
		return cursor + 1
	}
	return -1
}

// Strip removes every hidden rune from text and reports how many it removed.
// The count is what the caller shows the operator: the point is not a silent
// cleanup but that the operator learns the text was hiding something.
func Strip(text string) (string, int) {
	if text == "" {
		return text, 0
	}
	runes := []rune(text)
	var cleaned strings.Builder
	cleaned.Grow(len(text))
	removed := 0
	for index := 0; index < len(runes); index++ {
		r := runes[index]
		if r == flagBase {
			if end := flagTagEnd(runes, index); end > 0 {
				cleaned.WriteString(string(runes[index:end]))
				index = end - 1
				continue
			}
		}
		if r == zeroWidthJoiner && joinsEmoji(runes, index) {
			cleaned.WriteRune(r)
			continue
		}
		if isHidden(r) {
			removed++
			continue
		}
		cleaned.WriteRune(r)
	}
	return cleaned.String(), removed
}

// Has reports whether text carries anything Strip would remove.
func Has(text string) bool {
	_, removed := Strip(text)
	return removed > 0
}

// Line strips text and flattens it to a single line, for values interpolated
// into a structured document (YAML frontmatter, a one-line field) where a
// newline would let server-authored text forge structure around itself.
func Line(text string) (string, int) {
	cleaned, removed := Strip(text)
	cleaned = strings.ReplaceAll(cleaned, "\r\n", " ")
	for _, breakRune := range []string{"\n", "\r", "\u2028", "\u2029"} {
		cleaned = strings.ReplaceAll(cleaned, breakRune, " ")
	}
	return strings.TrimSpace(cleaned), removed
}

// Notice is the operator-facing line for text that had hidden runes removed.
// Empty when nothing was removed, so callers can print it unconditionally.
func Notice(removed int) string {
	if removed <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"%d invisible character(s) were removed before display. Text that hides characters "+
			"from you can read differently than it is.", removed)
}
