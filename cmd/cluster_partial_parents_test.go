package cmd

import (
	"bytes"
	"testing"

	"ankra/internal/client"
)

func TestParseParentFlag(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      client.Parent
		wantError bool
	}{
		{name: "explicit kind", in: "name=infisical-ns,kind=manifest", want: client.Parent{Name: "infisical-ns", Kind: "manifest"}},
		{name: "addon kind", in: "name=infisical,kind=addon", want: client.Parent{Name: "infisical", Kind: "addon"}},
		{name: "kind defaults to manifest", in: "name=infisical-ns", want: client.Parent{Name: "infisical-ns", Kind: "manifest"}},
		{name: "whitespace tolerated", in: " name = web , kind = addon ", want: client.Parent{Name: "web", Kind: "addon"}},
		{name: "missing name", in: "kind=manifest", wantError: true},
		{name: "bad kind", in: "name=web,kind=service", wantError: true},
		{name: "empty", in: "", wantError: true},
		{name: "non key value", in: "web", wantError: true},
		{name: "unknown field", in: "name=web,foo=bar", wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseParentFlag(tc.in)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for %q, got %+v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseParentFlag(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMergeParents_Add(t *testing.T) {
	orig := []client.Parent{{Name: "a", Kind: "manifest"}}
	got, err := mergeParents(orig, []string{"name=b,kind=manifest"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []client.Parent{{Name: "a", Kind: "manifest"}, {Name: "b", Kind: "manifest"}}
	if !equalParents(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMergeParents_Remove(t *testing.T) {
	orig := []client.Parent{{Name: "a", Kind: "manifest"}, {Name: "b", Kind: "addon"}}
	got, err := mergeParents(orig, nil, []string{"name=a,kind=manifest"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []client.Parent{{Name: "b", Kind: "addon"}}
	if !equalParents(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMergeParents_RemoveLastClears(t *testing.T) {
	orig := []client.Parent{{Name: "a", Kind: "manifest"}}
	got, err := mergeParents(orig, nil, []string{"name=a,kind=manifest"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty parents, got %+v", got)
	}
}

func TestMergeParents_SetReplaces(t *testing.T) {
	orig := []client.Parent{{Name: "a", Kind: "manifest"}}
	got, err := mergeParents(orig, nil, nil, []string{"name=x,kind=addon", "name=y,kind=manifest"})
	if err != nil {
		t.Fatal(err)
	}
	// sorted by kind then name: addon/x, manifest/y
	want := []client.Parent{{Name: "x", Kind: "addon"}, {Name: "y", Kind: "manifest"}}
	if !equalParents(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMergeParents_SetAndAddConflict(t *testing.T) {
	_, err := mergeParents(nil, []string{"name=a"}, nil, []string{"name=b"})
	if err == nil {
		t.Fatal("expected conflict error combining --set-parent with --add-parent")
	}
}

func TestMergeParents_Dedup(t *testing.T) {
	orig := []client.Parent{{Name: "a", Kind: "manifest"}}
	got, err := mergeParents(orig, []string{"name=a,kind=manifest"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("expected dedup to 1 parent, got %+v", got)
	}
}

func TestWriteDecodedDoc(t *testing.T) {
	t.Run("yaml default adds newline", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeDecodedDoc(&buf, "image: nginx", ""); err != nil {
			t.Fatal(err)
		}
		if buf.String() != "image: nginx\n" {
			t.Errorf("got %q", buf.String())
		}
	})
	t.Run("raw base64 encodes", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeDecodedDoc(&buf, "hello", "raw"); err != nil {
			t.Fatal(err)
		}
		if buf.String() != "aGVsbG8=\n" {
			t.Errorf("got %q", buf.String())
		}
	})
	t.Run("invalid format errors", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeDecodedDoc(&buf, "x", "json"); err == nil {
			t.Fatal("expected error for unsupported format")
		}
	})
}

func equalParents(a, b []client.Parent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
