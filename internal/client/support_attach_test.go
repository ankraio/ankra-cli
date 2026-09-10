package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The platform admits a support attachment on the multipart part's own
// Content-Type header and answers 415 for anything outside its image
// allowlist. These tests pin that the CLI sends the file's real type: on the
// old CreateFormFile path every part read application/octet-stream and a
// valid PNG was refused (PLA-836).
func TestUploadSupportAttachmentSendsDetectedContentType(t *testing.T) {
	// The signatures http.DetectContentType recognises for the four allowed
	// types; the tail is arbitrary so only the magic bytes decide.
	cases := []struct {
		name     string
		filename string
		content  []byte
		want     string
	}{
		{"png", "screenshot.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...), "image/png"},
		{"jpeg", "photo.jpg", append([]byte("\xff\xd8\xff"), make([]byte, 16)...), "image/jpeg"},
		{"gif", "anim.gif", append([]byte("GIF89a"), make([]byte, 16)...), "image/gif"},
		{"webp", "pic.webp", append([]byte("RIFF\x00\x00\x00\x00WEBPVP"), make([]byte, 16)...), "image/webp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPartType, gotFilename, gotField string
			var gotBytes int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				file, header, err := r.FormFile("file")
				if err != nil {
					t.Errorf("FormFile(file) error = %v", err)
					w.WriteHeader(http.StatusUnprocessableEntity)
					return
				}
				defer file.Close()
				content, _ := io.ReadAll(file)
				gotBytes = len(content)
				gotPartType = header.Header.Get("Content-Type")
				gotFilename = header.Filename
				gotField = "file"
				jsonResponse(t, w, http.StatusOK, SupportTicket{ID: "ticket-1"})
			}))
			t.Cleanup(server.Close)

			path := filepath.Join(t.TempDir(), tc.filename)
			if err := os.WriteFile(path, tc.content, 0o600); err != nil {
				t.Fatal(err)
			}

			c := New(testToken, server.URL)
			ticket, err := c.UploadSupportAttachment(context.Background(), "ticket-1", path)
			if err != nil {
				t.Fatalf("UploadSupportAttachment() error = %v", err)
			}
			if ticket == nil || ticket.ID != "ticket-1" {
				t.Fatalf("UploadSupportAttachment() ticket = %+v, want id ticket-1", ticket)
			}
			if gotField != "file" {
				t.Errorf("multipart field = %q, want file", gotField)
			}
			if gotPartType != tc.want {
				t.Errorf("part Content-Type = %q, want %q", gotPartType, tc.want)
			}
			if gotFilename != tc.filename {
				t.Errorf("part filename = %q, want %q", gotFilename, tc.filename)
			}
			if gotBytes != len(tc.content) {
				t.Errorf("part bytes = %d, want %d", gotBytes, len(tc.content))
			}
		})
	}
}

// A file the sniffer cannot place is still sent, with whatever type the
// bytes suggest, so the platform's own 415 and its allowlist message reach
// the user unchanged rather than a client-side guess at the same rule.
func TestUploadSupportAttachmentUnknownTypeReachesThePlatform(t *testing.T) {
	var gotPartType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile(file) error = %v", err)
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		gotPartType = header.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_, _ = w.Write([]byte(`{"detail":"Unsupported attachment type. Allowed types: PNG, JPEG, WebP, GIF."}`))
	}))
	t.Cleanup(server.Close)

	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("plain text, not an image\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := New(testToken, server.URL)
	_, err := c.UploadSupportAttachment(context.Background(), "ticket-1", path)
	if err == nil {
		t.Fatal("UploadSupportAttachment() error = nil, want the platform's 415")
	}
	if gotPartType != "text/plain" {
		t.Errorf("part Content-Type = %q, want text/plain (parameters stripped)", gotPartType)
	}
}

// attachmentPartHeader must write the same Content-Disposition
// CreateFormFile does, so a filename with a quote or backslash still parses
// on the platform side.
func TestAttachmentPartHeaderEscapesFilename(t *testing.T) {
	header := attachmentPartHeader(`we"ird\name.png`, "image/png")
	got := header.Get("Content-Disposition")
	want := `form-data; name="file"; filename="we\"ird\\name.png"`
	if got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
	if header.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", header.Get("Content-Type"))
	}
}
