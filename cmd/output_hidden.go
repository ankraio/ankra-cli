package cmd

import (
	"encoding/json"
	"fmt"

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

// sanitizeStructured round-trips value through its own encoder so tags are
// honoured per format, strips every string it finds, and reports how many
// characters went. It returns a nil value when there was nothing to strip, so
// the caller knows to encode the original.
func sanitizeStructured(format outputFormat, value interface{}) (interface{}, int, error) {
	raw, err := marshalFor(format, value)
	if err != nil {
		return nil, 0, err
	}
	var generic interface{}
	if err := unmarshalFor(format, raw, &generic); err != nil {
		return nil, 0, err
	}
	cleaned, removed := stripStructured(generic)
	if removed == 0 {
		return nil, 0, nil
	}
	if object, ok := cleaned.(map[string]interface{}); ok {
		// Never overwrite a field the payload already owns.
		if _, taken := object[hiddenRemovedKey]; !taken {
			object[hiddenRemovedKey] = removed
		}
		return object, removed, nil
	}
	return cleaned, removed, nil
}

func marshalFor(format outputFormat, value interface{}) ([]byte, error) {
	switch format {
	case outputYAML:
		return yaml.Marshal(value)
	default:
		return json.Marshal(value)
	}
}

func unmarshalFor(format outputFormat, raw []byte, into interface{}) error {
	switch format {
	case outputYAML:
		return yaml.Unmarshal(raw, into)
	default:
		return json.Unmarshal(raw, into)
	}
}

// stripStructured walks a decoded document, cleaning every string in it, keys
// included: a hidden character in a key hides the key itself from whoever
// reads the output.
func stripStructured(value interface{}) (interface{}, int) {
	switch typed := value.(type) {
	case string:
		cleaned, removed := hiddenunicode.Strip(typed)
		return cleaned, removed
	case []interface{}:
		total := 0
		for index, element := range typed {
			cleaned, removed := stripStructured(element)
			typed[index] = cleaned
			total += removed
		}
		return typed, total
	case map[string]interface{}:
		total := 0
		for key, element := range typed {
			cleaned, removed := stripStructured(element)
			total += removed
			cleanKey, keyRemoved := hiddenunicode.Strip(key)
			if keyRemoved > 0 {
				delete(typed, key)
				total += keyRemoved
			}
			typed[cleanKey] = cleaned
		}
		return typed, total
	case map[interface{}]interface{}:
		// yaml.v3 decodes into map[string]interface{} for string keys, but a
		// document with non-string keys still lands here.
		total := 0
		for key, element := range typed {
			cleaned, removed := stripStructured(element)
			typed[key] = cleaned
			total += removed
		}
		return typed, total
	default:
		return value, 0
	}
}

// structuredHiddenNotice is the stderr line for a structured payload that was
// carrying hidden characters. It goes to stderr so a script parsing stdout is
// unaffected by it.
func structuredHiddenNotice(removed int) string {
	if removed <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"warning: %d invisible character(s) were removed from this output before it was written. "+
			"Text that hides characters is hostile input: do not feed this payload to another tool "+
			"without looking at what it was hiding.", removed)
}
