package client

import "testing"

func TestStopClusterOptionsQuery(t *testing.T) {
	yes, no := true, false
	for name, testCase := range map[string]struct {
		options StopClusterOptions
		want    string
	}{
		"empty":          {StopClusterOptions{}, ""},
		"force":          {StopClusterOptions{Force: true}, "?force=true"},
		"preserve false": {StopClusterOptions{PreserveState: &no}, "?preserve_state=false"},
		"preserve true":  {StopClusterOptions{PreserveState: &yes}, "?preserve_state=true"},
		"both":           {StopClusterOptions{Force: true, PreserveState: &no}, "?force=true&preserve_state=false"},
	} {
		if got := testCase.options.query(); got != testCase.want {
			t.Errorf("%s: query = %q, want %q", name, got, testCase.want)
		}
	}
}

func TestStartClusterOptionsQuery(t *testing.T) {
	no := false
	for name, testCase := range map[string]struct {
		options StartClusterOptions
		want    string
	}{
		"empty":   {StartClusterOptions{}, ""},
		"scope":   {StartClusterOptions{Scope: "control_plane"}, "?scope=control_plane"},
		"restore": {StartClusterOptions{RestoreState: &no}, "?restore_state=false"},
		"both":    {StartClusterOptions{Scope: "all", RestoreState: &no}, "?restore_state=false&scope=all"},
	} {
		if got := testCase.options.query(); got != testCase.want {
			t.Errorf("%s: query = %q, want %q", name, got, testCase.want)
		}
	}
}
