package cmd

// Node-level editing for ImportCluster YAML files.
//
// `cluster encrypt -f` and `cluster clone` rewrite cluster.yaml files that are
// often a GitOps source of truth. Round-tripping them through the
// ImportClusterConfig structs silently deletes everything the structs do not
// model (stack deploy_wave, spec.prometheus_metrics, any field added after the
// structs were written) along with comments, anchors, and key ordering. The
// helpers here instead mutate the parsed yaml.Node tree so only the touched
// nodes change; the structs remain read-side lookups only.

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// parseClusterYAMLDoc parses raw cluster YAML into a document node, preserving
// comments and ordering for round-trip re-encoding.
func parseClusterYAMLDoc(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse cluster YAML: %w", err)
	}
	return &doc, nil
}

// clusterDocRoot returns the root mapping of a parsed cluster document.
func clusterDocRoot(doc *yaml.Node) (*yaml.Node, error) {
	node := doc
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil, fmt.Errorf("cluster YAML document is empty")
		}
		node = node.Content[0]
	}
	node = resolveYAMLAlias(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("cluster YAML root is not a mapping")
	}
	return node, nil
}

// resolveYAMLAlias follows alias nodes to the anchored node they reference.
func resolveYAMLAlias(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return node
}

// yamlMapValue returns the value node for key in a mapping (aliases resolved),
// or nil when the mapping or key is absent.
func yamlMapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	idx := mapIndexOf(mapping, key)
	if idx < 0 {
		return nil
	}
	return resolveYAMLAlias(mapping.Content[idx+1])
}

// yamlMapString returns the scalar string value for key, or "".
func yamlMapString(mapping *yaml.Node, key string) string {
	value := yamlMapValue(mapping, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}

// yamlMapEnsure returns the value node for key, appending an empty node of the
// requested kind when the key is absent. A null placeholder value (e.g. a bare
// `stacks:` line) is converted in place, mirroring ApplySetAssignments.
func yamlMapEnsure(mapping *yaml.Node, key string, kind yaml.Kind, tag string) (*yaml.Node, error) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("cannot add %q: parent is not a mapping", key)
	}
	idx := mapIndexOf(mapping, key)
	if idx < 0 {
		value := &yaml.Node{Kind: kind, Tag: tag}
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			value)
		return value, nil
	}
	value := resolveYAMLAlias(mapping.Content[idx+1])
	if value.Kind == yaml.ScalarNode && (value.Tag == "!!null" || value.Value == "") {
		value.Kind = kind
		value.Tag = tag
		value.Value = ""
		value.Content = nil
	}
	if value.Kind != kind {
		return nil, fmt.Errorf("%q is a %s, expected a %s", key, kindName(value.Kind), kindName(kind))
	}
	return value, nil
}

// clusterStacksNode returns the spec.stacks sequence of doc. With create set,
// missing spec/stacks keys are added; otherwise nil is returned when the path
// does not resolve to a sequence.
func clusterStacksNode(doc *yaml.Node, create bool) (*yaml.Node, error) {
	root, err := clusterDocRoot(doc)
	if err != nil {
		return nil, err
	}
	if !create {
		stacks := yamlMapValue(yamlMapValue(root, "spec"), "stacks")
		if stacks == nil || stacks.Kind != yaml.SequenceNode {
			return nil, nil
		}
		return stacks, nil
	}
	spec, err := yamlMapEnsure(root, "spec", yaml.MappingNode, "!!map")
	if err != nil {
		return nil, err
	}
	return yamlMapEnsure(spec, "stacks", yaml.SequenceNode, "!!seq")
}

// stackNodeName returns the name field of a spec.stacks[] item.
func stackNodeName(item *yaml.Node) string {
	return yamlMapString(resolveYAMLAlias(item), "name")
}

// findClusterListEntry walks spec.stacks[*].<listKey> for the first mapping
// entry whose name equals name — the same first-match order as the
// struct-based lookups in cluster_encrypt.go.
func findClusterListEntry(doc *yaml.Node, listKey, name string) (*yaml.Node, error) {
	stacks, err := clusterStacksNode(doc, false)
	if err != nil {
		return nil, err
	}
	if stacks == nil {
		return nil, nil
	}
	for _, stackItem := range stacks.Content {
		list := yamlMapValue(resolveYAMLAlias(stackItem), listKey)
		if list == nil || list.Kind != yaml.SequenceNode {
			continue
		}
		for _, item := range list.Content {
			entry := resolveYAMLAlias(item)
			if entry != nil && entry.Kind == yaml.MappingNode && yamlMapString(entry, "name") == name {
				return entry, nil
			}
		}
	}
	return nil, nil
}

// appendManifestEncryptedPath records leafKey in the encrypted_paths list of
// the named manifest, creating the list when missing. Nothing else in the
// document is touched.
func appendManifestEncryptedPath(doc *yaml.Node, manifestName, leafKey string) error {
	manifest, err := findClusterListEntry(doc, "manifests", manifestName)
	if err != nil {
		return err
	}
	if manifest == nil {
		return fmt.Errorf("manifest %q not found in cluster YAML", manifestName)
	}
	return appendYAMLStringListEntry(manifest, "encrypted_paths", leafKey)
}

// appendAddonEncryptedPath records leafKey in the configuration.encrypted_paths
// list of the named addon.
func appendAddonEncryptedPath(doc *yaml.Node, addonName, leafKey string) error {
	addon, err := findClusterListEntry(doc, "addons", addonName)
	if err != nil {
		return err
	}
	if addon == nil {
		return fmt.Errorf("addon %q not found in cluster YAML", addonName)
	}
	configuration := yamlMapValue(addon, "configuration")
	if configuration == nil || configuration.Kind != yaml.MappingNode {
		return fmt.Errorf("addon %q has no configuration mapping in cluster YAML", addonName)
	}
	return appendYAMLStringListEntry(configuration, "encrypted_paths", leafKey)
}

func appendYAMLStringListEntry(mapping *yaml.Node, key, value string) error {
	list, err := yamlMapEnsure(mapping, key, yaml.SequenceNode, "!!seq")
	if err != nil {
		return err
	}
	list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	return nil
}

// deepCopyYAMLNode returns a copy of n that is safe to graft into another
// document. Aliases whose anchor lives inside the copied subtree keep pointing
// at the copied anchor; aliases to anchors outside the subtree are expanded
// inline, since their definition would not exist in the destination document.
func deepCopyYAMLNode(n *yaml.Node) *yaml.Node {
	return copyYAMLNode(n, map[*yaml.Node]*yaml.Node{})
}

func copyYAMLNode(n *yaml.Node, copies map[*yaml.Node]*yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if copied, ok := copies[n]; ok {
		return copied
	}
	if n.Kind == yaml.AliasNode {
		// Anchors are defined before their aliases in document order, so an
		// in-subtree anchor has already been copied by the time its alias is
		// visited.
		if target, ok := copies[n.Alias]; ok {
			aliasCopy := *n
			aliasCopy.Alias = target
			copies[n] = &aliasCopy
			return &aliasCopy
		}
		expanded := copyYAMLNode(n.Alias, copies)
		copies[n] = expanded
		return expanded
	}
	nodeCopy := *n
	copies[n] = &nodeCopy
	if len(n.Content) > 0 {
		nodeCopy.Content = make([]*yaml.Node, len(n.Content))
		for i, child := range n.Content {
			nodeCopy.Content[i] = copyYAMLNode(child, copies)
		}
	}
	return &nodeCopy
}

// newImportClusterDoc builds a fresh ImportCluster document for a clone
// target, carrying over the source's spec.git_repository node (comments,
// unknown keys and all) when present.
func newImportClusterDoc(name, description string, sourceDoc *yaml.Node) (*yaml.Node, error) {
	metadata := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	appendYAMLMapEntry(metadata, "name", yamlStringNode(name))
	if description != "" {
		appendYAMLMapEntry(metadata, "description", yamlStringNode(description))
	}

	sourceRoot, err := clusterDocRoot(sourceDoc)
	if err != nil {
		return nil, err
	}
	spec := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if gitRepository := yamlMapValue(yamlMapValue(sourceRoot, "spec"), "git_repository"); gitRepository != nil {
		appendYAMLMapEntry(spec, "git_repository", deepCopyYAMLNode(gitRepository))
	}
	appendYAMLMapEntry(spec, "stacks", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})

	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	appendYAMLMapEntry(root, "apiVersion", yamlStringNode("v1"))
	appendYAMLMapEntry(root, "kind", yamlStringNode("ImportCluster"))
	appendYAMLMapEntry(root, "metadata", metadata)
	appendYAMLMapEntry(root, "spec", spec)

	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}, nil
}

func appendYAMLMapEntry(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value)
}

func yamlStringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}
