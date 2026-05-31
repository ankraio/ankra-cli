package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// setKind selects how the RHS of a `key=value` pair is interpreted.
type setKind int

const (
	setKindCoerce  setKind = iota // --set:        auto-coerce true/false/null/int/float, else string
	setKindString                 // --set-string: always string
	setKindFile                   // --set-file:   value is a file path; its contents become the string value
)

// pathSegment is one component of a dotted path.
//
//   - isIndex:    a numeric array index (`[N]`); value is empty, index carries N.
//   - isSelector: a key/value list match (`[key=value]`); selKey/selVal carry
//     the field to match. Used to address list items by a stable field (e.g. a
//     Kubernetes container by name) instead of a fragile positional index.
//   - otherwise:  a plain map key in `value`.
type pathSegment struct {
	isIndex    bool
	value      string
	index      int
	isSelector bool
	selKey     string
	selVal     string
}

// SetAssignment is one parsed `key=value` from --set/--set-string/--set-file.
type SetAssignment struct {
	Path  []pathSegment
	Raw   string // value text after =
	Kind  setKind
	Value string // resolved final string value (for setKindFile this is the file contents)
}

// ParseSetAssignments parses one or more --set raw entries into individual
// dotted-path assignments. Each raw entry may contain comma-separated
// assignments (Helm-style). For setKindFile, the value is read from the path
// named after `=` (e.g. `key=@path`, also supports `key=path` for brevity).
//
// Quoting/escaping rules (subset of Helm strvals):
//   - "\," and "\." are literal commas / dots in keys and values.
//   - Within values, '...' and "..." preserve commas/dots literally (no
//     coercion to bool/int/float is applied if quoted).
//   - "name={a,b,c}" brace-literal lists are NOT supported in v1; use repeated
//     --set or --set 'arr[0]=a,arr[1]=b' instead.
func ParseSetAssignments(raws []string, kind setKind) ([]SetAssignment, error) {
	var assignments []SetAssignment
	for _, raw := range raws {
		entries := splitTopLevel(raw, ',')
		for _, entry := range entries {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			eqIdx := findKeyValueSep(entry)
			if eqIdx < 0 {
				return nil, fmt.Errorf("invalid --set entry %q: expected key=value", entry)
			}
			rawKey := entry[:eqIdx]
			rawVal := entry[eqIdx+1:]

			path, err := parseDottedPath(rawKey)
			if err != nil {
				return nil, fmt.Errorf("invalid --set key %q: %w", rawKey, err)
			}
			if len(path) == 0 {
				return nil, fmt.Errorf("invalid --set entry %q: empty key", entry)
			}

			value := rawVal
			if kind == setKindFile {
				filePath := strings.TrimPrefix(rawVal, "@")
				contents, readErr := os.ReadFile(filePath)
				if readErr != nil {
					return nil, fmt.Errorf("--set-file: read %q: %w", filePath, readErr)
				}
				value = string(contents)
			} else {
				value = unescapeValue(rawVal)
			}

			assignments = append(assignments, SetAssignment{
				Path:  path,
				Raw:   rawVal,
				Kind:  kind,
				Value: value,
			})
		}
	}
	return assignments, nil
}

// ApplySetAssignments mutates a YAML document node in place. The root may be
// a *yaml.DocumentNode (as returned by yaml.Decoder) or a MappingNode/empty
// node. Empty or null roots are initialised to an empty mapping so
// `--set image.tag=...` works on a fresh values doc.
//
// Only the nodes on the dotted path are rewritten; comments and key ordering
// on untouched lines round-trip with zero diff.
func ApplySetAssignments(root *yaml.Node, assignments []SetAssignment) error {
	if root == nil {
		return fmt.Errorf("cannot apply --set to nil node")
	}

	// Resolve the actual content node, handling DocumentNode wrappers.
	contentNode := root
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
		}
		contentNode = root.Content[0]
	}

	// Treat null / empty mapping / no-tag-no-content as an empty map.
	if contentNode.Kind == yaml.ScalarNode && (contentNode.Tag == "!!null" || (contentNode.Value == "" && contentNode.Tag == "")) {
		contentNode.Kind = yaml.MappingNode
		contentNode.Tag = "!!map"
		contentNode.Value = ""
		contentNode.Content = nil
	}
	if contentNode.Kind == 0 {
		contentNode.Kind = yaml.MappingNode
		contentNode.Tag = "!!map"
	}

	for _, assign := range assignments {
		if err := applyOne(contentNode, assign); err != nil {
			return err
		}
	}
	if root.Kind == yaml.DocumentNode {
		root.Content[0] = contentNode
	}
	return nil
}

func applyOne(root *yaml.Node, assign SetAssignment) error {
	cursor := root
	for i, seg := range assign.Path {
		isLast := i == len(assign.Path)-1
		if seg.isSelector {
			if cursor.Kind == yaml.ScalarNode && (cursor.Tag == "!!null" || cursor.Value == "") {
				return fmt.Errorf("--set %s: no list item with %s=%s in %s (list is empty or missing)", renderPath(assign.Path), seg.selKey, seg.selVal, renderPath(assign.Path[:i]))
			}
			if cursor.Kind != yaml.SequenceNode {
				return fmt.Errorf("--set %s: expected sequence at selector [%s=%s], got %s", renderPath(assign.Path), seg.selKey, seg.selVal, kindName(cursor.Kind))
			}
			childIdx := -1
			for ci, item := range cursor.Content {
				if item.Kind != yaml.MappingNode {
					continue
				}
				ki := mapIndexOf(item, seg.selKey)
				if ki >= 0 && item.Content[ki+1].Value == seg.selVal {
					childIdx = ci
					break
				}
			}
			if childIdx < 0 {
				return fmt.Errorf("--set %s: no list item with %s=%s in %s", renderPath(assign.Path), seg.selKey, seg.selVal, renderPath(assign.Path[:i]))
			}
			child := cursor.Content[childIdx]
			if isLast {
				next, err := makeScalarNode(assign)
				if err != nil {
					return err
				}
				cursor.Content[childIdx] = preserveComments(child, next)
				return nil
			}
			cursor = child
			continue
		}
		if seg.isIndex {
			if cursor.Kind == yaml.ScalarNode && (cursor.Tag == "!!null" || cursor.Value == "") {
				cursor.Kind = yaml.SequenceNode
				cursor.Tag = "!!seq"
				cursor.Value = ""
				cursor.Content = nil
			}
			if cursor.Kind != yaml.SequenceNode {
				return fmt.Errorf("--set %s: expected sequence at index [%d], got %s", renderPath(assign.Path), seg.index, kindName(cursor.Kind))
			}
			for len(cursor.Content) <= seg.index {
				cursor.Content = append(cursor.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
			}
			child := cursor.Content[seg.index]
			if isLast {
				next, err := makeScalarNode(assign)
				if err != nil {
					return err
				}
				cursor.Content[seg.index] = preserveComments(child, next)
				return nil
			}
			cursor = child
			continue
		}

		if cursor.Kind == yaml.ScalarNode && (cursor.Tag == "!!null" || cursor.Value == "") {
			cursor.Kind = yaml.MappingNode
			cursor.Tag = "!!map"
			cursor.Value = ""
			cursor.Content = nil
		}
		if cursor.Kind != yaml.MappingNode {
			return fmt.Errorf("--set %s: expected mapping at key %q, got %s", renderPath(assign.Path), seg.value, kindName(cursor.Kind))
		}

		idx := mapIndexOf(cursor, seg.value)
		if idx < 0 {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg.value}
			var valNode *yaml.Node
			if isLast {
				n, err := makeScalarNode(assign)
				if err != nil {
					return err
				}
				valNode = n
			} else {
				next := assign.Path[i+1]
				if next.isIndex {
					valNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
				} else {
					valNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				}
			}
			cursor.Content = append(cursor.Content, keyNode, valNode)
			if isLast {
				return nil
			}
			cursor = valNode
			continue
		}

		valNode := cursor.Content[idx+1]
		if isLast {
			next, err := makeScalarNode(assign)
			if err != nil {
				return err
			}
			cursor.Content[idx+1] = preserveComments(valNode, next)
			return nil
		}
		cursor = valNode
	}
	return nil
}

// makeScalarNode converts the assignment's resolved value into a YAML scalar
// node with the appropriate tag and quoting style based on kind.
func makeScalarNode(assign SetAssignment) (*yaml.Node, error) {
	switch assign.Kind {
	case setKindString, setKindFile:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: assign.Value, Style: stringStyleFor(assign.Value)}, nil
	case setKindCoerce:
		return coerceScalarNode(assign.Value), nil
	default:
		return nil, fmt.Errorf("unknown set kind %d", assign.Kind)
	}
}

// stringStyleFor returns a quoting style that preserves leading zeros, special
// chars, and variable references such as ${VAR}. Helm-equivalent behavior is
// to emit `--set-string` values as quoted strings.
func stringStyleFor(s string) yaml.Style {
	if s == "" {
		return yaml.DoubleQuotedStyle
	}
	if looksNumeric(s) || looksBool(s) || looksNull(s) || strings.ContainsAny(s, ":#'\"\n\r\t") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return yaml.DoubleQuotedStyle
	}
	return 0
}

// coerceScalarNode mirrors Helm's `--set` coercion: bool/null/integer/float
// scalars get typed tags; everything else is treated as a string. Quoted RHS
// values (single or double quotes) preserve the literal as a string.
func coerceScalarNode(value string) *yaml.Node {
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
			inner := value[1 : len(value)-1]
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: inner, Style: yaml.DoubleQuotedStyle}
		}
	}
	if value == "null" || value == "~" || value == "" {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
	if value == "true" || value == "false" {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: value}
	}
	if _, err := strconv.ParseInt(value, 10, 64); err == nil && !looksLikeLeadingZero(value) {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: value}
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil && looksFloatish(value) {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: value}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: stringStyleFor(value)}
}

func looksLikeLeadingZero(s string) bool {
	if len(s) < 2 {
		return false
	}
	if s[0] == '-' || s[0] == '+' {
		s = s[1:]
	}
	return len(s) > 1 && s[0] == '0'
}

// looksFloatish ensures we only coerce to !!float when there's at least one
// indicator (decimal point, exponent). Plain integers must remain !!int.
func looksFloatish(s string) bool {
	return strings.ContainsAny(s, ".eE")
}

func looksNumeric(s string) bool {
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	return false
}

func looksBool(s string) bool {
	return s == "true" || s == "false"
}

func looksNull(s string) bool {
	return s == "null" || s == "~"
}

func preserveComments(old, fresh *yaml.Node) *yaml.Node {
	if old == nil {
		return fresh
	}
	if fresh.HeadComment == "" {
		fresh.HeadComment = old.HeadComment
	}
	if fresh.LineComment == "" {
		fresh.LineComment = old.LineComment
	}
	if fresh.FootComment == "" {
		fresh.FootComment = old.FootComment
	}
	return fresh
}

func mapIndexOf(mappingNode *yaml.Node, key string) int {
	for i := 0; i < len(mappingNode.Content); i += 2 {
		k := mappingNode.Content[i]
		if k.Value == key {
			return i
		}
	}
	return -1
}

func kindName(k yaml.Kind) string {
	switch k {
	case yaml.DocumentNode:
		return "document"
	case yaml.MappingNode:
		return "mapping"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return "unknown"
	}
}

func renderPath(segments []pathSegment) string {
	var b strings.Builder
	for i, seg := range segments {
		if seg.isIndex {
			fmt.Fprintf(&b, "[%d]", seg.index)
			continue
		}
		if seg.isSelector {
			fmt.Fprintf(&b, "[%s=%s]", seg.selKey, seg.selVal)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg.value)
	}
	return b.String()
}

// splitTopLevel splits s on the separator rune, honoring \-escapes.
// Quoted regions (single or double) are NOT special at this level — we only
// care about commas inside values, which Helm forbids unless escaped.
func splitTopLevel(s string, sep rune) []string {
	var out []string
	var b strings.Builder
	escape := false
	for _, r := range s {
		if escape {
			b.WriteRune(r)
			escape = false
			continue
		}
		if r == '\\' {
			b.WriteRune(r)
			escape = true
			continue
		}
		if r == sep {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	out = append(out, b.String())
	return out
}

// findKeyValueSep returns the index of the `=` that separates the dotted key
// from the value, ignoring any `=` that appears inside a `[...]` selector
// (e.g. `containers[name=app].image=nginx`). Honors \-escapes. Returns -1 when
// no top-level `=` exists.
func findKeyValueSep(s string) int {
	escape := false
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		switch c {
		case '\\':
			escape = true
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '=':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// findUnescaped returns the index of the first unescaped occurrence of c, or -1.
func findUnescaped(s string, c byte) int {
	escape := false
	for i := 0; i < len(s); i++ {
		if escape {
			escape = false
			continue
		}
		if s[i] == '\\' {
			escape = true
			continue
		}
		if s[i] == c {
			return i
		}
	}
	return -1
}

// unescapeValue strips one level of backslash from \, \. and \\ so user input
// like `key=a\,b` produces the literal string "a,b".
func unescapeValue(s string) string {
	var b strings.Builder
	escape := false
	for _, r := range s {
		if escape {
			b.WriteRune(r)
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// parseDottedPath parses a key like `a.b[0].c` into ordered segments.
// Escapes (`\.`) keep the dot inside a segment; `[N]` opens an array index.
func parseDottedPath(key string) ([]pathSegment, error) {
	var segments []pathSegment
	var cur strings.Builder
	escape := false
	i := 0
	flushKey := func() {
		if cur.Len() == 0 {
			return
		}
		segments = append(segments, pathSegment{value: cur.String()})
		cur.Reset()
	}
	for i < len(key) {
		r := key[i]
		if escape {
			cur.WriteByte(r)
			escape = false
			i++
			continue
		}
		switch r {
		case '\\':
			escape = true
			i++
		case '.':
			flushKey()
			i++
		case '[':
			flushKey()
			end := strings.IndexByte(key[i+1:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated bracket in %q", key)
			}
			body := key[i+1 : i+1+end]
			if eq := strings.IndexByte(body, '='); eq >= 0 {
				selKey := strings.TrimSpace(body[:eq])
				if selKey == "" {
					return nil, fmt.Errorf("invalid selector %q: empty key", body)
				}
				segments = append(segments, pathSegment{isSelector: true, selKey: selKey, selVal: body[eq+1:]})
			} else {
				n, err := strconv.Atoi(body)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("invalid array index %q", body)
				}
				segments = append(segments, pathSegment{isIndex: true, index: n})
			}
			i = i + 1 + end + 1
			if i < len(key) && key[i] == '.' {
				i++
			}
		default:
			cur.WriteByte(r)
			i++
		}
	}
	flushKey()
	return segments, nil
}
