package cmd

import (
	"encoding/base64"
	"gopkg.in/yaml.v3"
)

func extractKindFromBase64(manifestBase64 string) string {
	if manifestBase64 == "" {
		return "unknown"
	}

	decoded, err := base64.StdEncoding.DecodeString(manifestBase64)
	if err != nil {
		return "unknown"
	}

	var manifest struct {
		Kind string `yaml:"kind"`
	}

	if err := yaml.Unmarshal(decoded, &manifest); err != nil {
		return "unknown"
	}

	if manifest.Kind == "" {
		return "unknown"
	}

	return manifest.Kind
}

// extractNamespaceFromBase64 pulls metadata.namespace out of a base64
// manifest document; the manifest listing itself carries no namespace.
func extractNamespaceFromBase64(manifestBase64 string) string {
	if manifestBase64 == "" {
		return ""
	}

	decoded, err := base64.StdEncoding.DecodeString(manifestBase64)
	if err != nil {
		return ""
	}

	var manifest struct {
		Metadata struct {
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}

	if err := yaml.Unmarshal(decoded, &manifest); err != nil {
		return ""
	}

	return manifest.Metadata.Namespace
}
