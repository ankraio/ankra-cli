package client

import "encoding/json"

// RawDocument keeps the exact JSON document a response was decoded from, so
// structured output (-o json|yaml) can print what the platform answered
// rather than the subset the CLI's struct declares. A response type opts in
// by embedding it, tagged out of both encodings:
//
//	type FleetCloudCost struct {
//		RawDocument `json:"-" yaml:"-"`
//		...
//	}
//
// The shared decode helpers (sendJSON, the CSRF doJSON lane) fill it after a
// successful decode of the top-level document. A value built in code, a
// test mock's value, or a type nested inside another document carries none,
// and RawJSON reports nil: callers fall back to encoding the struct.
type RawDocument struct {
	document json.RawMessage
}

// RawJSON returns the response body the value was decoded from, or nil when
// it was not decoded from a response.
func (raw RawDocument) RawJSON() json.RawMessage {
	return raw.document
}

func (raw *RawDocument) keepRawJSON(body []byte) {
	raw.document = append(json.RawMessage(nil), body...)
}

// rawDocumentKeeper is implemented by any pointer to a type that embeds
// RawDocument.
type rawDocumentKeeper interface {
	keepRawJSON(body []byte)
}

// decodeKeepingRawDocument unmarshals body into target and, when target
// embeds RawDocument, records body beside the decode.
func decodeKeepingRawDocument(body []byte, target any) error {
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}
	if keeper, ok := target.(rawDocumentKeeper); ok {
		keeper.keepRawJSON(body)
	}
	return nil
}
