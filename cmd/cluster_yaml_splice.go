package cmd

// Text-level editing for cluster YAML files that must survive byte-for-byte.
//
// The yaml.Node helpers in cluster_yaml_edit.go keep comments, ordering and
// unmodelled fields, but re-encoding the tree still reflows the file into
// yaml.v3's house style: sequences indented under their key, long plain
// scalars unfolded, quoting normalised. The platform writes cluster files in
// a different style - indentless sequences, long lines folded - so
// re-encoding a platform-written file changes every sequence in it: a
// 600-line diff in a GitOps repository to record one encrypted path. The
// helpers here locate the node to change through the parsed tree and splice
// the new text into the original bytes, so the only lines that change are
// the ones asked for.

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// spliceManifestEncryptedPaths records entries in the encrypted_paths list of
// the named manifest, one text splice per entry. The tree is re-parsed between
// splices because every insertion shifts the line numbers below it.
func spliceManifestEncryptedPaths(source []byte, manifestName string, entries []string) ([]byte, error) {
	for _, entry := range entries {
		doc, err := parseClusterYAMLDoc(source)
		if err != nil {
			return nil, err
		}
		manifest, err := findClusterListEntry(doc, "manifests", manifestName)
		if err != nil {
			return nil, err
		}
		if manifest == nil {
			return nil, fmt.Errorf("manifest %q not found in cluster YAML", manifestName)
		}
		source, err = spliceListEntry(source, doc, manifest, "encrypted_paths", entry)
		if err != nil {
			return nil, fmt.Errorf("record %s in encrypted_paths of manifest %q: %w", entry, manifestName, err)
		}
	}
	return source, nil
}

// spliceAddonEncryptedPaths records entries in the configuration.encrypted_paths
// list of the named addon, one text splice per entry.
func spliceAddonEncryptedPaths(source []byte, addonName string, entries []string) ([]byte, error) {
	for _, entry := range entries {
		doc, err := parseClusterYAMLDoc(source)
		if err != nil {
			return nil, err
		}
		addon, err := findClusterListEntry(doc, "addons", addonName)
		if err != nil {
			return nil, err
		}
		if addon == nil {
			return nil, fmt.Errorf("addon %q not found in cluster YAML", addonName)
		}
		configuration := yamlMapValue(addon, "configuration")
		if configuration == nil || configuration.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("addon %q has no configuration mapping in cluster YAML", addonName)
		}
		source, err = spliceListEntry(source, doc, configuration, "encrypted_paths", entry)
		if err != nil {
			return nil, fmt.Errorf("record %s in encrypted_paths of addon %q: %w", entry, addonName, err)
		}
	}
	return source, nil
}

// spliceListEntry returns source with value appended to the sequence stored
// under key in mapping, creating the key when it is absent. mapping must
// belong to doc, and doc must have been parsed from source unchanged: the
// edit is placed by the line and column numbers the parser recorded.
//
// A block sequence gains one line whose prefix (indentation and dash) is
// copied from its last item, so the file's own style is kept whatever it is.
// A flow sequence is extended on its line. A missing key, or a key with no
// value, is given a block sequence in the document's own sequence style.
func spliceListEntry(source []byte, doc, mapping *yaml.Node, key, value string) ([]byte, error) {
	if mapping == nil || mapping.Kind != yaml.MappingNode || len(mapping.Content) == 0 {
		return nil, fmt.Errorf("cannot add %q: parent is not a mapping with entries", key)
	}
	if mapping.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("cannot add %q: the parent is a flow mapping on line %d; add the entry by hand", key, mapping.Line)
	}
	lines := strings.Split(string(source), "\n")
	newline := "\n"
	if strings.Contains(string(source), "\r\n") {
		newline = "\r\n"
	}

	index := mapIndexOf(mapping, key)
	if index < 0 {
		keyIndent := mapping.Content[0].Column - 1
		return insertLines(lines, newline, yamlNodeEndLine(mapping), []string{
			strings.Repeat(" ", keyIndent) + key + ":",
			strings.Repeat(" ", keyIndent+blockSequenceDashOffset(doc, lines)) + "- " + renderYAMLScalar(value, false),
		}), nil
	}

	keyNode := mapping.Content[index]
	valueNode := mapping.Content[index+1]
	switch {
	case valueNode.Kind == yaml.AliasNode:
		return nil, fmt.Errorf("%s is a YAML alias; add the entry to the anchored list by hand", key)

	case valueNode.Kind == yaml.SequenceNode && valueNode.Style&yaml.FlowStyle != 0:
		return spliceFlowSequenceEntry(lines, newline, valueNode, value)

	case valueNode.Kind == yaml.SequenceNode:
		// Block sequences always hold at least one item; an empty one can
		// only be written in flow style or as a bare key.
		last := valueNode.Content[len(valueNode.Content)-1]
		itemLine := lines[last.Line-1]
		if last.Column-1 > len(itemLine) {
			return nil, fmt.Errorf("%s: parser position is past the end of line %d", key, last.Line)
		}
		prefix := itemLine[:last.Column-1]
		return insertLines(lines, newline, yamlNodeEndLine(last), []string{prefix + renderYAMLScalar(value, false)}), nil

	case valueNode.Kind == yaml.ScalarNode && valueNode.Tag == "!!null":
		// `encrypted_paths:` with nothing after it.
		keyIndent := keyNode.Column - 1
		return insertLines(lines, newline, keyNode.Line, []string{
			strings.Repeat(" ", keyIndent+blockSequenceDashOffset(doc, lines)) + "- " + renderYAMLScalar(value, false),
		}), nil

	default:
		return nil, fmt.Errorf("%s is a %s, expected a list", key, kindName(valueNode.Kind))
	}
}

// spliceFlowSequenceEntry extends a single-line flow sequence such as
// `encrypted_paths: []` or `[a, b]` on its own line.
func spliceFlowSequenceEntry(lines []string, newline string, sequence *yaml.Node, value string) ([]byte, error) {
	line := lines[sequence.Line-1]
	open := sequence.Column - 1
	if open >= len(line) || line[open] != '[' {
		return nil, fmt.Errorf("flow list on line %d does not start where the parser reported", sequence.Line)
	}
	closeOffset := strings.Index(line[open:], "]")
	if closeOffset < 0 {
		return nil, fmt.Errorf("flow list on line %d spans several lines; add the entry by hand", sequence.Line)
	}
	close := open + closeOffset
	rendered := renderYAMLScalar(value, true)
	inner := strings.TrimRight(line[open+1:close], " ")
	if strings.TrimSpace(inner) == "" {
		inner = rendered
	} else {
		inner += ", " + rendered
	}
	lines[sequence.Line-1] = line[:open+1] + inner + line[close:]
	return []byte(strings.Join(lines, "\n")), nil
}

// blockSequenceDashOffset reports how far the document indents a block
// sequence's dashes past the key that owns it: 0 for the indentless style the
// platform writes, 2 for yaml.v3's. Measured from the first block sequence
// found; a document with none gets yaml.v3's style.
func blockSequenceDashOffset(doc *yaml.Node, lines []string) int {
	offset, found := findBlockSequenceDashOffset(doc, lines)
	if !found || offset < 0 {
		return 2
	}
	return offset
}

func findBlockSequenceDashOffset(node *yaml.Node, lines []string) (int, bool) {
	if node == nil {
		return 0, false
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if offset, found := findBlockSequenceDashOffset(child, lines); found {
				return offset, true
			}
		}
	case yaml.MappingNode:
		for entryIndex := 0; entryIndex+1 < len(node.Content); entryIndex += 2 {
			keyNode := node.Content[entryIndex]
			valueNode := node.Content[entryIndex+1]
			if valueNode.Kind == yaml.SequenceNode && valueNode.Style&yaml.FlowStyle == 0 && len(valueNode.Content) > 0 {
				item := valueNode.Content[0]
				if item.Line >= 1 && item.Line <= len(lines) && item.Column-1 <= len(lines[item.Line-1]) {
					dash := strings.LastIndex(lines[item.Line-1][:item.Column-1], "-")
					if dash >= 0 {
						return dash - (keyNode.Column - 1), true
					}
				}
			}
			if offset, found := findBlockSequenceDashOffset(valueNode, lines); found {
				return offset, true
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if offset, found := findBlockSequenceDashOffset(child, lines); found {
				return offset, true
			}
		}
	}
	return 0, false
}

// yamlNodeEndLine returns the last line (1-based) a node occupies. Block
// scalars are counted by their content; other scalars are taken as one line.
func yamlNodeEndLine(node *yaml.Node) int {
	if node == nil {
		return 0
	}
	end := node.Line
	if node.Kind == yaml.ScalarNode && (node.Style&yaml.LiteralStyle != 0 || node.Style&yaml.FoldedStyle != 0) {
		content := strings.TrimRight(node.Value, "\n")
		if content != "" {
			end += strings.Count(content, "\n") + 1
		}
	}
	for _, child := range node.Content {
		if childEnd := yamlNodeEndLine(child); childEnd > end {
			end = childEnd
		}
	}
	return end
}

// insertLines returns the document with newLines placed after line afterLine
// (1-based). Line endings match the file's.
func insertLines(lines []string, newline string, afterLine int, newLines []string) []byte {
	if newline == "\r\n" {
		for index := range newLines {
			newLines[index] += "\r"
		}
	}
	if afterLine > len(lines) {
		afterLine = len(lines)
	}
	result := make([]string, 0, len(lines)+len(newLines))
	result = append(result, lines[:afterLine]...)
	result = append(result, newLines...)
	result = append(result, lines[afterLine:]...)
	return []byte(strings.Join(result, "\n"))
}

// renderYAMLScalar spells value as yaml.v3 would, so a string that needs
// quoting gets it. Inside a flow sequence the characters that delimit the
// sequence also force quotes.
func renderYAMLScalar(value string, flow bool) string {
	encoded, err := yaml.Marshal(value)
	if err != nil {
		return strconv.Quote(value)
	}
	rendered := strings.TrimSuffix(string(encoded), "\n")
	if strings.Contains(rendered, "\n") {
		return strconv.Quote(value)
	}
	if flow && !strings.HasPrefix(rendered, "\"") && !strings.HasPrefix(rendered, "'") && strings.ContainsAny(rendered, ",[]{}:") {
		return strconv.Quote(value)
	}
	return rendered
}
