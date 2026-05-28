package cmd

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func mustParseYAML(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(src), &root); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return &root
}

func encodeYAML(t *testing.T, node *yaml.Node) string {
	t.Helper()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		t.Fatalf("yaml.Encode: %v", err)
	}
	_ = enc.Close()
	return buf.String()
}

func TestParseSetAssignments_DottedPathsAndArrays(t *testing.T) {
	got, err := ParseSetAssignments([]string{"image.tag=1.0.146", "ingress.hosts[0].host=demo.ankra.io"}, setKindCoerce)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[1].Path[0].value != "ingress" || !got[1].Path[2].isIndex || got[1].Path[2].index != 0 || got[1].Path[3].value != "host" {
		t.Errorf("unexpected path: %+v", got[1].Path)
	}
}

func TestParseSetAssignments_CommaSeparated(t *testing.T) {
	got, err := ParseSetAssignments([]string{"a=1,b=2"}, setKindCoerce)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].Value != "1" || got[1].Value != "2" {
		t.Errorf("got %v", got)
	}
}

func TestParseSetAssignments_EscapedCommaInValue(t *testing.T) {
	got, err := ParseSetAssignments([]string{`note=hello\,world`}, setKindCoerce)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Value != "hello,world" {
		t.Errorf("got %q, want hello,world", got[0].Value)
	}
}

func TestParseSetAssignments_RejectsMissingEquals(t *testing.T) {
	_, err := ParseSetAssignments([]string{"image-tag"}, setKindCoerce)
	if err == nil {
		t.Error("expected error for missing =")
	}
}

func TestApplySetAssignments_SimpleScalar(t *testing.T) {
	node := mustParseYAML(t, "image:\n  tag: 1.0.0\n  repository: foo/bar\nreplicaCount: 2\n")
	a, err := ParseSetAssignments([]string{"image.tag=1.0.146"}, setKindCoerce)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ApplySetAssignments(node, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := encodeYAML(t, node)
	if !strings.Contains(out, "tag: 1.0.146") {
		t.Errorf("expected tag updated, got:\n%s", out)
	}
	if !strings.Contains(out, "repository: foo/bar") {
		t.Errorf("expected sibling preserved, got:\n%s", out)
	}
	if !strings.Contains(out, "replicaCount: 2") {
		t.Errorf("expected other top-level keys preserved, got:\n%s", out)
	}
}

func TestApplySetAssignments_NewArrayElement(t *testing.T) {
	node := mustParseYAML(t, "ingress:\n  enabled: true\n")
	a, err := ParseSetAssignments([]string{"ingress.hosts[0].host=demo.ankra.io"}, setKindCoerce)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ApplySetAssignments(node, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := encodeYAML(t, node)
	if !strings.Contains(out, "host: demo.ankra.io") {
		t.Errorf("expected host added, got:\n%s", out)
	}
	if !strings.Contains(out, "enabled: true") {
		t.Errorf("expected enabled preserved, got:\n%s", out)
	}
}

func TestApplySetAssignments_EmptyRoot(t *testing.T) {
	node := mustParseYAML(t, "")
	a, err := ParseSetAssignments([]string{"image.tag=1.0.146"}, setKindCoerce)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ApplySetAssignments(node, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := encodeYAML(t, node)
	if !strings.Contains(out, "tag: 1.0.146") {
		t.Errorf("expected tag set on empty root, got:\n%s", out)
	}
}

func TestApplySetAssignments_NullRoot(t *testing.T) {
	node := mustParseYAML(t, "null\n")
	a, err := ParseSetAssignments([]string{"image.tag=1.0.146"}, setKindCoerce)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ApplySetAssignments(node, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := encodeYAML(t, node)
	if !strings.Contains(out, "tag: 1.0.146") {
		t.Errorf("expected tag set on null root, got:\n%s", out)
	}
}

func TestApplySetAssignments_PreservesVariablePlaceholder(t *testing.T) {
	node := mustParseYAML(t, "config:\n  url: ${MY_URL}\n  hostname: ${HOST}\nimage:\n  tag: 1.0.0\n")
	a, err := ParseSetAssignments([]string{"image.tag=1.0.146"}, setKindCoerce)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ApplySetAssignments(node, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := encodeYAML(t, node)
	if !strings.Contains(out, "${MY_URL}") || !strings.Contains(out, "${HOST}") {
		t.Errorf("variable placeholders not preserved, got:\n%s", out)
	}
}

func TestApplySetAssignments_SetStringForcesStringScalar(t *testing.T) {
	node := mustParseYAML(t, "")
	a, err := ParseSetAssignments([]string{"image.tag=2.0"}, setKindString)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ApplySetAssignments(node, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := encodeYAML(t, node)
	// `2.0` would normally coerce to !!float (rendering as `2`), but
	// --set-string must keep it a string.
	if !strings.Contains(out, `tag: "2.0"`) {
		t.Errorf("expected `tag: \"2.0\"`, got:\n%s", out)
	}
}

func TestApplySetAssignments_CoerceBoolAndNumber(t *testing.T) {
	node := mustParseYAML(t, "")
	a, err := ParseSetAssignments([]string{"enabled=true,count=3"}, setKindCoerce)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ApplySetAssignments(node, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := encodeYAML(t, node)
	if !strings.Contains(out, "enabled: true") {
		t.Errorf("expected bool coercion, got:\n%s", out)
	}
	if !strings.Contains(out, "count: 3") {
		t.Errorf("expected int coercion, got:\n%s", out)
	}
}

func TestApplySetAssignments_LeadingZeroStaysString(t *testing.T) {
	node := mustParseYAML(t, "")
	a, err := ParseSetAssignments([]string{"port=007"}, setKindCoerce)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ApplySetAssignments(node, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := encodeYAML(t, node)
	if !strings.Contains(out, `port: "007"`) && !strings.Contains(out, "port: 007") {
		t.Errorf("expected leading-zero string preserved, got:\n%s", out)
	}
	// Must NOT have collapsed to `port: 7`
	if strings.Contains(out, "port: 7\n") {
		t.Errorf("leading-zero value was coerced to int, got:\n%s", out)
	}
}

func TestApplySetAssignments_DeeplyNestedCreatesPath(t *testing.T) {
	node := mustParseYAML(t, "existing:\n  key: val\n")
	a, err := ParseSetAssignments([]string{"a.b.c.d=1"}, setKindCoerce)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ApplySetAssignments(node, a); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := encodeYAML(t, node)
	if !strings.Contains(out, "a:") || !strings.Contains(out, "d: 1") {
		t.Errorf("expected nested path created, got:\n%s", out)
	}
}
