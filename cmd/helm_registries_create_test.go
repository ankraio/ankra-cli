package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestNormalizeHelmRegistrySpec(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		wantSpec string // canonical JSON the API should receive; "" means error expected
		wantNote bool
		wantErr  string
	}{
		{
			name:     "nested OCI under spec envelope",
			file:     `{"spec": {"helm_oci_registry": {"name": "acme", "url": "oci://ghcr.io/acme/charts"}}}`,
			wantSpec: `{"helm_oci_registry":{"name":"acme","url":"oci://ghcr.io/acme/charts"}}`,
		},
		{
			name:     "nested HTTP without envelope",
			file:     `{"helm_http_registry": {"name": "acme", "url": "https://charts.example.com"}}`,
			wantSpec: `{"helm_http_registry":{"name":"acme","url":"https://charts.example.com"}}`,
		},
		{
			name:     "flat OCI spec is auto-nested",
			file:     `{"name": "acme", "url": "oci://ghcr.io/acme/charts"}`,
			wantSpec: `{"helm_oci_registry":{"name":"acme","url":"oci://ghcr.io/acme/charts"}}`,
			wantNote: true,
		},
		{
			name:     "flat HTTP spec is auto-nested with extra fields kept",
			file:     `{"name": "acme", "url": "https://charts.example.com", "credential_name": "cred"}`,
			wantSpec: `{"helm_http_registry":{"credential_name":"cred","name":"acme","url":"https://charts.example.com"}}`,
			wantNote: true,
		},
		{
			name:     "flat spec under spec envelope is auto-nested",
			file:     `{"spec": {"name": "acme", "url": "oci://ghcr.io/acme/charts"}}`,
			wantSpec: `{"helm_oci_registry":{"name":"acme","url":"oci://ghcr.io/acme/charts"}}`,
			wantNote: true,
		},
		{
			name:    "both registry types rejected",
			file:    `{"spec": {"helm_oci_registry": {"name": "a", "url": "oci://x"}, "helm_http_registry": {"name": "b", "url": "https://y"}}}`,
			wantErr: "found both",
		},
		{
			name:    "flat spec without url rejected",
			file:    `{"name": "acme"}`,
			wantErr: "helm_oci_registry",
		},
		{
			name:    "non-object file rejected",
			file:    `[{"name": "acme"}]`,
			wantErr: "must contain a JSON object",
		},
		{
			name:    "non-object spec value rejected",
			file:    `{"spec": "acme"}`,
			wantErr: `"spec" must be a JSON object`,
		},
		{
			name:    "invalid JSON rejected",
			file:    `{"name": `,
			wantErr: "parsing JSON",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, note, err := normalizeHelmRegistrySpec([]byte(tc.file))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got spec %s", tc.wantErr, spec)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := mustCanonicalJSON(t, spec)
			want := mustCanonicalJSON(t, json.RawMessage(tc.wantSpec))
			if got != want {
				t.Errorf("spec mismatch:\n  got:  %s\n  want: %s", got, want)
			}
			if tc.wantNote != (note != "") {
				t.Errorf("note presence mismatch: wantNote=%v, note=%q", tc.wantNote, note)
			}
		})
	}
}

func mustCanonicalJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

type helmRegistryCreateMock struct {
	baseMock
	lastReq *client.CreateHelmRegistryRequest
}

func (m *helmRegistryCreateMock) CreateHelmRegistry(req client.CreateHelmRegistryRequest) (*client.CreateHelmRegistryResponse, error) {
	m.lastReq = &req
	return &client.CreateHelmRegistryResponse{}, nil
}

func writeHelmSpecFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry-spec.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing spec file: %v", err)
	}
	return path
}

func TestHelmRegistriesCreateAutoNestsFlatSpec(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)
	path := writeHelmSpecFile(t, `{"name": "acme", "url": "oci://ghcr.io/acme/charts"}`)

	_, err := executeCommand("helm", "registries", "create", "-f", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastReq == nil {
		t.Fatal("expected CreateHelmRegistry to be called")
	}
	got := mustCanonicalJSON(t, mock.lastReq.Spec)
	want := `{"helm_oci_registry":{"name":"acme","url":"oci://ghcr.io/acme/charts"}}`
	if got != want {
		t.Errorf("posted spec mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestHelmRegistriesCreateRejectsUnrecognizedSpecBeforePost(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)
	path := writeHelmSpecFile(t, `{"registry": {"name": "acme", "url": "oci://x"}}`)

	_, err := executeCommand("helm", "registries", "create", "-f", path)
	if err == nil {
		t.Fatal("expected an error for an unrecognized spec shape")
	}
	if !strings.Contains(err.Error(), "helm_oci_registry") {
		t.Errorf("expected actionable error naming helm_oci_registry, got: %v", err)
	}
	if code := exitCodeFor(err); code != exitUsage {
		t.Errorf("expected usage exit code %d, got %d", exitUsage, code)
	}
	if mock.lastReq != nil {
		t.Error("expected no API call for an invalid spec file")
	}
}
