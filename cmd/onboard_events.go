package cmd

import (
	"bytes"
	"encoding/json"
	"io"
)

// `ankra onboard --json` prints one JSON object per line (NDJSON), one per
// step, so an AI coding agent can drive the onboarding:
//
//	{"event":"<name>", ...the step's data..., "instruction":"...", "next_command":"..."}
//
// "event" always comes first and "instruction" and "next_command" last.
// "instruction" says, in plain words, what the agent should tell or ask the
// user; "next_command" is the exact command to run next, present only when
// there is one. The events are onboarding_start, signed_in, login_url,
// organisation, clusters, playground_offer, playground_status, agent_setup,
// done and error.

// onboardField is one key of an event, in the order it is written.
type onboardField struct {
	key   string
	value any
}

func onboardValue(key string, value any) onboardField {
	return onboardField{key: key, value: value}
}

// onboardEvents writes the NDJSON stream.
type onboardEvents struct {
	writer io.Writer
}

// emit writes one event. Fields whose value is nil or an empty string are
// left out, so an absent optional value (a user code an older platform did
// not send) is absent rather than "".
func (events *onboardEvents) emit(name string, instruction string, nextCommand string, fields ...onboardField) {
	all := make([]onboardField, 0, len(fields)+3)
	all = append(all, onboardValue("event", name))
	all = append(all, fields...)
	all = append(all, onboardValue("instruction", instruction), onboardValue("next_command", nextCommand))

	var line bytes.Buffer
	line.WriteByte('{')
	written := 0
	for _, field := range all {
		if field.value == nil {
			continue
		}
		if text, isText := field.value.(string); isText && text == "" {
			continue
		}
		encodedValue, encodeError := onboardMarshal(field.value)
		if encodeError != nil {
			continue
		}
		encodedKey, _ := onboardMarshal(field.key)
		if written > 0 {
			line.WriteByte(',')
		}
		line.Write(encodedKey)
		line.WriteByte(':')
		line.Write(encodedValue)
		written++
	}
	line.WriteString("}\n")
	_, _ = events.writer.Write(line.Bytes())
}

// onboardMarshal encodes a value without HTML escaping, so an instruction
// that names `ankra org switch <id>` reads as written.
func onboardMarshal(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if encodeError := encoder.Encode(value); encodeError != nil {
		return nil, encodeError
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}
