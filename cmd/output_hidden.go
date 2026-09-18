package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"ankra/internal/hiddenunicode"

	"gopkg.in/yaml.v3"
)

// Hidden Unicode in structured output (ankra-4r75g.12).
//
// The human-facing rendering strips hidden characters before printing, but
// `-o json|yaml` returns earlier, through encodeStructured, so a scripted
// caller still received the raw payload. That is the same threat with an extra
// hop: machine output is what gets piped into another tool, including an AI
// one, and a Tag-block run that a person would never see travels intact.
//
// Two decisions worth reviewing, because both are judgement rather than
// mechanics:
//
// The strip happens at encodeStructured rather than in the three chat
// commands the bead named. That function is the single seam every structured
// payload passes through (25 files call it), so one change covers agent
// outcomes, remediation views, alert text and cluster names too, and none of
// it needs the field-by-field walk the bead was wary of. The narrower fix
// would have left every other command exposed.
//
// A marker is added rather than stripping silently, mirroring what the
// human-facing surfaces do: a machine consumer that is handed quietly altered
// bytes has no way to know the payload was hostile. For an object payload the
// marker is an extra field; for an array or a scalar it cannot be added
// without changing the document's shape, so those get the stderr notice
// (written by renderStructured) and nothing in band. A reviewer who would
// rather keep the payload byte-exact and warn only on stderr should say so:
// that is a contract preference, not a correctness question.
//
// When nothing is hidden the original typed value is encoded, untouched, so
// field order and every existing output stay byte-identical. Only a payload
// that actually carried hidden characters takes the generic path, where map
// key order follows the encoder rather than the struct's field order.
const hiddenRemovedKey = "hidden_characters_removed"

// stripStats is what a pass over a document found. Key collisions are
// counted separately from characters because they mean something different:
// the document became ambiguous, not merely dirty.
type stripStats struct {
	removed       int
	keyCollisions int
}

// sanitizeStructured round-trips value through its own encoder so tags are
// honoured per format, strips every string it finds, and reports what it
// found. It returns a nil value when there was nothing to strip, so the
// caller knows to encode the original.
func sanitizeStructured(format outputFormat, value interface{}) (interface{}, stripStats, error) {
	raw, err := marshalFor(format, value)
	if err != nil {
		return nil, stripStats{}, err
	}
	generic, err := decodeGeneric(format, raw)
	if err != nil {
		return nil, stripStats{}, err
	}
	cleaned, stats := stripStructured(generic)
	if stats.removed == 0 {
		return nil, stripStats{}, nil
	}
	if object, ok := cleaned.(map[string]interface{}); ok {
		// Never overwrite a field the payload already owns.
		if _, taken := object[hiddenRemovedKey]; !taken {
			object[hiddenRemovedKey] = stats.removed
		}
		return object, stats, nil
	}
	return cleaned, stats, nil
}

func marshalFor(format outputFormat, value interface{}) ([]byte, error) {
	switch format {
	case outputYAML:
		return yaml.Marshal(value)
	default:
		return json.Marshal(value)
	}
}

// decodeGeneric decodes an encoded document into plain maps and slices.
//
// JSON goes through a decoder with UseNumber, so a number keeps its literal
// instead of becoming a float64: without it, re-encoding a stripped payload
// would round an integer above 2^53 (a resource quantity, a nanosecond
// timestamp) and respell large or small floats. The payload that was hostile
// would also have been the payload whose numbers stopped being exact.
// json.Number is a distinct type, so the strip walk passes it through
// untouched and it marshals back as the literal it came in as.
func decodeGeneric(format outputFormat, raw []byte) (interface{}, error) {
	var generic interface{}
	if format == outputYAML {
		if err := yaml.Unmarshal(raw, &generic); err != nil {
			return nil, err
		}
		return generic, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	return generic, nil
}

// stripStructured walks a decoded document, cleaning every string in it, keys
// included: a hidden character in a key hides the key itself from whoever
// reads the output.
//
// Keys are visited in sorted order rather than Go's randomised map order, so
// that when two keys clean to the same name the outcome is the same on every
// run. The first key in sorted order keeps the name and the later one is
// dropped, counted as a collision and named on stderr: a document that
// carries both "cluster" and "clu<ZWSP>ster" is ambiguous by construction,
// and silently letting whichever value happened to be visited last win is the
// kind of ambiguity this package exists to remove.
func stripStructured(value interface{}) (interface{}, stripStats) {
	switch typed := value.(type) {
	case string:
		cleaned, removed := hiddenunicode.Strip(typed)
		return cleaned, stripStats{removed: removed}
	case []interface{}:
		var stats stripStats
		for index, element := range typed {
			cleaned, elementStats := stripStructured(element)
			typed[index] = cleaned
			stats.add(elementStats)
		}
		return typed, stats
	case map[string]interface{}:
		var stats stripStats
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		rebuilt := make(map[string]interface{}, len(typed))
		for _, key := range keys {
			cleaned, elementStats := stripStructured(typed[key])
			stats.add(elementStats)
			cleanKey, keyRemoved := hiddenunicode.Strip(key)
			stats.removed += keyRemoved
			if _, taken := rebuilt[cleanKey]; taken {
				// Two keys that render identically. Keep the first and say so.
				stats.keyCollisions++
				continue
			}
			rebuilt[cleanKey] = cleaned
		}
		return rebuilt, stats
	case map[interface{}]interface{}:
		// yaml.v3 decodes into map[string]interface{} for string keys, but a
		// document with non-string keys still lands here.
		var stats stripStats
		for key, element := range typed {
			cleaned, elementStats := stripStructured(element)
			typed[key] = cleaned
			stats.add(elementStats)
		}
		return typed, stats
	default:
		return value, stripStats{}
	}
}

func (s *stripStats) add(other stripStats) {
	s.removed += other.removed
	s.keyCollisions += other.keyCollisions
}

// structuredHiddenNotice is the stderr line for a structured payload that was
// carrying hidden characters. It goes to stderr so a script parsing stdout is
// unaffected by it.
func structuredHiddenNotice(stats stripStats) string {
	if stats.removed <= 0 {
		return ""
	}
	notice := fmt.Sprintf(
		"warning: %d invisible character(s) were removed from this output before it was written. "+
			"Text that hides characters is hostile input: do not feed this payload to another tool "+
			"without looking at what it was hiding.", stats.removed)
	if stats.keyCollisions > 0 {
		notice += fmt.Sprintf(
			" %d field name(s) became identical once cleaned; the first was kept and the rest dropped, "+
				"so this document was ambiguous before it was cleaned.", stats.keyCollisions)
	}
	return notice
}
