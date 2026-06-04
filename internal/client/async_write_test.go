package client

import "testing"

func TestAppendWaitQuery(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wait     bool
		want     string
	}{
		{
			name:     "append false",
			endpoint: "https://platform.example/api/v1/clusters/import",
			wait:     false,
			want:     "https://platform.example/api/v1/clusters/import?wait=false",
		},
		{
			name:     "append true",
			endpoint: "https://platform.example/api/v1/clusters/hetzner/id/node-groups",
			wait:     true,
			want:     "https://platform.example/api/v1/clusters/hetzner/id/node-groups?wait=true",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := appendWaitQuery(testCase.endpoint, testCase.wait)
			if got != testCase.want {
				t.Fatalf("appendWaitQuery() = %q, want %q", got, testCase.want)
			}
		})
	}
}
