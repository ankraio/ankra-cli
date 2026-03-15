package cmd

import (
	"testing"
	"time"
)

func TestFormatTimeAgo(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectOriginal bool
	}{
		{
			name:           "valid recent timestamp",
			input:          time.Now().Add(-5 * time.Minute).Format(time.RFC3339),
			expectOriginal: false,
		},
		{
			name:           "valid old timestamp",
			input:          "2020-01-01T00:00:00Z",
			expectOriginal: false,
		},
		{
			name:           "invalid timestamp returns original",
			input:          "not-a-timestamp",
			expectOriginal: true,
		},
		{
			name:           "empty string returns original",
			input:          "",
			expectOriginal: true,
		},
		{
			name:           "partial date returns original",
			input:          "2024-01-01",
			expectOriginal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTimeAgo(tt.input)
			if tt.expectOriginal {
				if result != tt.input {
					t.Errorf("formatTimeAgo(%q) = %q, want original input", tt.input, result)
				}
			} else {
				if result == tt.input {
					t.Errorf("formatTimeAgo(%q) returned original instead of human-readable time", tt.input)
				}
				if result == "" {
					t.Errorf("formatTimeAgo(%q) returned empty string", tt.input)
				}
			}
		})
	}
}
