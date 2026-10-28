package cmd

import (
	"encoding/base64"
	"testing"
)

func TestExtractKindFromBase64(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "valid deployment manifest",
			input:    base64.StdEncoding.EncodeToString([]byte("kind: Deployment\napiVersion: apps/v1")),
			expected: "Deployment",
		},
		{
			name:     "valid service manifest",
			input:    base64.StdEncoding.EncodeToString([]byte("kind: Service\napiVersion: v1")),
			expected: "Service",
		},
		{
			name:     "valid configmap manifest",
			input:    base64.StdEncoding.EncodeToString([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test")),
			expected: "ConfigMap",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "unknown",
		},
		{
			name:     "invalid base64",
			input:    "not-valid-base64!@#$",
			expected: "unknown",
		},
		{
			name:     "valid base64 but invalid yaml",
			input:    base64.StdEncoding.EncodeToString([]byte("{{invalid yaml")),
			expected: "unknown",
		},
		{
			name:     "valid yaml without kind field",
			input:    base64.StdEncoding.EncodeToString([]byte("apiVersion: v1\nmetadata:\n  name: test")),
			expected: "unknown",
		},
		{
			name:     "valid yaml with empty kind",
			input:    base64.StdEncoding.EncodeToString([]byte("kind: \"\"\napiVersion: v1")),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractKindFromBase64(tt.input)
			if result != tt.expected {
				t.Errorf("extractKindFromBase64(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
