package cmd

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// manifestDoc is one Kubernetes document parsed out of a (possibly multi-doc)
// manifest. node is the parsed *yaml.Node (DocumentNode) used for in-place
// mutation and round-trip re-encoding.
type manifestDoc struct {
	node *yaml.Node
	kind string
	name string
}

// parseManifestDocs decodes a base64 manifest and splits it into its
// individual YAML documents, preserving comments and key ordering so untouched
// documents round-trip with minimal diff.
func parseManifestDocs(manifestBase64 string) ([]manifestDoc, error) {
	raw, err := base64.StdEncoding.DecodeString(manifestBase64)
	if err != nil {
		return nil, fmt.Errorf("decode manifest base64: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var docs []manifestDoc
	for {
		var node yaml.Node
		decErr := dec.Decode(&node)
		if decErr == io.EOF {
			break
		}
		if decErr != nil {
			return nil, fmt.Errorf("parse manifest YAML: %w", decErr)
		}
		// Skip empty documents (e.g. a stray trailing `---`), including ones
		// that decode to a single null scalar.
		content := &node
		if node.Kind == yaml.DocumentNode {
			if len(node.Content) == 0 {
				continue
			}
			content = node.Content[0]
		}
		if content.Kind == 0 || (content.Kind == yaml.ScalarNode && (content.Tag == "!!null" || content.Value == "")) {
			continue
		}
		kind, name := docKindName(&node)
		copied := node
		docs = append(docs, manifestDoc{node: &copied, kind: kind, name: name})
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("manifest is empty")
	}
	return docs, nil
}

// docKindName reads the top-level `kind` and `metadata.name` from a parsed
// document node. Missing fields come back as empty strings.
func docKindName(doc *yaml.Node) (kind, name string) {
	content := doc
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return "", ""
		}
		content = doc.Content[0]
	}
	if content.Kind != yaml.MappingNode {
		return "", ""
	}
	for i := 0; i+1 < len(content.Content); i += 2 {
		key := content.Content[i].Value
		val := content.Content[i+1]
		switch key {
		case "kind":
			kind = val.Value
		case "metadata":
			if val.Kind == yaml.MappingNode {
				if ni := mapIndexOf(val, "name"); ni >= 0 {
					name = val.Content[ni+1].Value
				}
			}
		}
	}
	return kind, name
}

// selectManifestDoc picks the document to mutate.
//
//   - With no selector: requires exactly one document, otherwise errors and
//     lists the available kind/name pairs so the user can disambiguate.
//   - With --target-kind / --target-name: filters and requires exactly one
//     match. Kind matching is case-insensitive; name matching is exact.
func selectManifestDoc(docs []manifestDoc, targetKind, targetName string) (int, error) {
	hasFilter := targetKind != "" || targetName != ""
	if !hasFilter {
		if len(docs) == 1 {
			return 0, nil
		}
		return -1, fmt.Errorf("manifest contains %d documents; use --target-kind/--target-name to pick one (available: %s)",
			len(docs), describeManifestDocs(docs))
	}

	var matches []int
	for i, d := range docs {
		if targetKind != "" && !strings.EqualFold(d.kind, targetKind) {
			continue
		}
		if targetName != "" && d.name != targetName {
			continue
		}
		matches = append(matches, i)
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return -1, fmt.Errorf("no document in the manifest matches %s (available: %s)",
			describeTarget(targetKind, targetName), describeManifestDocs(docs))
	default:
		return -1, fmt.Errorf("%s matches %d documents; narrow it with both --target-kind and --target-name (available: %s)",
			describeTarget(targetKind, targetName), len(matches), describeManifestDocs(docs))
	}
}

// applyManifestSet decodes the manifest, applies the --set assignments to the
// selected document, and returns the re-encoded base64 manifest. All other
// documents are preserved unchanged.
func applyManifestSet(manifestBase64 string, assignments []SetAssignment, targetKind, targetName string) (string, error) {
	docs, err := parseManifestDocs(manifestBase64)
	if err != nil {
		return "", err
	}
	idx, err := selectManifestDoc(docs, targetKind, targetName)
	if err != nil {
		return "", err
	}
	if err := ApplySetAssignments(docs[idx].node, assignments); err != nil {
		return "", err
	}
	return encodeManifestDocs(docs)
}

// encodeManifestDocs re-encodes each document into its own buffer and joins
// them with explicit `---` separators, then base64-encodes the result. We do
// not rely on yaml.Encoder's implicit multi-document separators.
func encodeManifestDocs(docs []manifestDoc) (string, error) {
	parts := make([]string, 0, len(docs))
	for _, d := range docs {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(d.node); err != nil {
			_ = enc.Close()
			return "", fmt.Errorf("re-encode manifest document: %w", err)
		}
		_ = enc.Close()
		parts = append(parts, buf.String())
	}
	joined := strings.Join(parts, "---\n")
	return base64.StdEncoding.EncodeToString([]byte(joined)), nil
}

func describeManifestDocs(docs []manifestDoc) string {
	seen := make([]string, 0, len(docs))
	for _, d := range docs {
		kind := d.kind
		if kind == "" {
			kind = "?"
		}
		name := d.name
		if name == "" {
			name = "?"
		}
		seen = append(seen, kind+"/"+name)
	}
	sort.Strings(seen)
	return strings.Join(seen, ", ")
}

func describeTarget(kind, name string) string {
	switch {
	case kind != "" && name != "":
		return fmt.Sprintf("--target-kind %q --target-name %q", kind, name)
	case kind != "":
		return fmt.Sprintf("--target-kind %q", kind)
	default:
		return fmt.Sprintf("--target-name %q", name)
	}
}
