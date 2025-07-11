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
