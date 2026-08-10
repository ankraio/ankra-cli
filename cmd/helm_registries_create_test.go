package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
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
			name:    "flat spec with unknown url scheme rejected",
			file:    `{"name": "acme", "url": "ftp://charts.example.com"}`,
			wantErr: "URL scheme must be oci://",
		},
		{
			name:    "flat spec with scheme-less url rejected",
			file:    `{"name": "acme", "url": "charts.example.com"}`,
			wantErr: "http:// / https://",
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
	return writeHelmSpecFileNamed(t, "registry-spec.json", content)
}

func writeHelmSpecFileNamed(t *testing.T, fileName, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), fileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing spec file: %v", err)
	}
	return path
}

// resetHelmRegistriesCreateFlags clears the create command's flag state so
// the package-level command does not leak flag values (or their Changed
// bits, which drive the exactly-one-of group validation) between tests.
// Slice flags need Replace because pflag's Set appends to arrays.
func resetHelmRegistriesCreateFlags() {
	helmRegistriesCreateCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
			_ = sliceValue.Replace(nil)
		} else {
			_ = flag.Value.Set(flag.DefValue)
		}
		flag.Changed = false
	})
}

func runHelmRegistriesCreate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetHelmRegistriesCreateFlags()
	t.Cleanup(resetHelmRegistriesCreateFlags)
	return executeCommand(append([]string{"helm", "registries", "create"}, args...)...)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	originalStderr := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	os.Stderr = writer

	fn()

	_ = writer.Close()
	os.Stderr = originalStderr

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(reader)
	return buf.String()
}

func TestHelmRegistriesCreateAutoNestsFlatSpec(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)
	path := writeHelmSpecFile(t, `{"name": "acme", "url": "oci://ghcr.io/acme/charts"}`)

	_, err := runHelmRegistriesCreate(t, "-f", path)
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

	_, err := runHelmRegistriesCreate(t, "-f", path)
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

func TestHelmRegistriesCreateAcceptsNestedYAMLSpec(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)
	path := writeHelmSpecFileNamed(t, "registry-spec.yaml", `spec:
  helm_http_registry:
    name: acme
    url: https://charts.example.com
    exclude_charts:
      - legacy
`)

	_, err := runHelmRegistriesCreate(t, "-f", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastReq == nil {
		t.Fatal("expected CreateHelmRegistry to be called")
	}
	got := mustCanonicalJSON(t, mock.lastReq.Spec)
	want := `{"helm_http_registry":{"exclude_charts":["legacy"],"name":"acme","url":"https://charts.example.com"}}`
	if got != want {
		t.Errorf("posted spec mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestHelmRegistriesCreateAutoNestsFlatYAMLSpec(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)
	path := writeHelmSpecFileNamed(t, "registry-spec.yaml", "name: acme\nurl: oci://ghcr.io/acme/charts\n")

	_, err := runHelmRegistriesCreate(t, "-f", path)
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

func TestHelmRegistriesCreateRejectsMalformedSpecFile(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)
	path := writeHelmSpecFile(t, `{"name": `)

	_, err := runHelmRegistriesCreate(t, "-f", path)
	if err == nil {
		t.Fatal("expected an error for a file that is neither JSON nor YAML")
	}
	if !strings.Contains(err.Error(), "JSON and YAML are accepted") {
		t.Errorf("expected error naming the accepted formats, got: %v", err)
	}
	if code := exitCodeFor(err); code != exitUsage {
		t.Errorf("expected usage exit code %d, got %d", exitUsage, code)
	}
	if mock.lastReq != nil {
		t.Error("expected no API call for a malformed spec file")
	}
}

func TestHelmRegistriesCreateFromFlags(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)

	var err error
	stderrOutput := captureStderr(t, func() {
		_, err = runHelmRegistriesCreate(t,
			"--name", "acme",
			"--url", "https://charts.example.com",
			"--credential-name", "cred",
			"--exclude-charts", "legacy",
			"--exclude-charts", "internal")
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastReq == nil {
		t.Fatal("expected CreateHelmRegistry to be called")
	}
	got := mustCanonicalJSON(t, mock.lastReq.Spec)
	want := `{"helm_http_registry":{"credential_name":"cred","exclude_charts":["legacy","internal"],"name":"acme","url":"https://charts.example.com"}}`
	if got != want {
		t.Errorf("posted spec mismatch:\n  got:  %s\n  want: %s", got, want)
	}
	if !strings.Contains(stderrOutput, "helm registries sync acme") {
		t.Errorf("expected a sync hint on stderr, got: %q", stderrOutput)
	}
}

func TestHelmRegistriesCreateFromFlagsInfersOCI(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)

	_, err := runHelmRegistriesCreate(t, "--name", "acme", "--url", "oci://ghcr.io/acme/charts")
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

func TestHelmRegistriesCreateRejectsFileAndFlagsTogether(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)
	path := writeHelmSpecFile(t, `{"name": "acme", "url": "oci://ghcr.io/acme/charts"}`)

	_, err := runHelmRegistriesCreate(t, "-f", path, "--name", "acme", "--url", "oci://ghcr.io/acme/charts")
	if err == nil {
		t.Fatal("expected an error when both --file and --name are set")
	}
	if code := exitCodeFor(err); code != exitUsage {
		t.Errorf("expected usage exit code %d, got %d", exitUsage, code)
	}
	if mock.lastReq != nil {
		t.Error("expected no API call when --file and --name conflict")
	}
}

func TestHelmRegistriesCreateRequiresFileOrFlags(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)

	_, err := runHelmRegistriesCreate(t)
	if err == nil {
		t.Fatal("expected an error when neither --file nor --name is set")
	}
	if code := exitCodeFor(err); code != exitUsage {
		t.Errorf("expected usage exit code %d, got %d", exitUsage, code)
	}
	if mock.lastReq != nil {
		t.Error("expected no API call without --file or --name")
	}
}

func TestHelmRegistriesCreateRequiresURLWithName(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)

	_, err := runHelmRegistriesCreate(t, "--name", "acme")
	if err == nil {
		t.Fatal("expected an error when --name is set without --url")
	}
	if code := exitCodeFor(err); code != exitUsage {
		t.Errorf("expected usage exit code %d, got %d", exitUsage, code)
	}
	if mock.lastReq != nil {
		t.Error("expected no API call when --url is missing")
	}
}

func TestHelmRegistriesCreateRejectsUnknownURLScheme(t *testing.T) {
	mock := &helmRegistryCreateMock{}
	setMockClient(t, mock)

	_, err := runHelmRegistriesCreate(t, "--name", "acme", "--url", "ftp://charts.example.com")
	if err == nil {
		t.Fatal("expected an error for an unsupported url scheme")
	}
	if !strings.Contains(err.Error(), "oci://") {
		t.Errorf("expected error naming the accepted schemes, got: %v", err)
	}
	if code := exitCodeFor(err); code != exitUsage {
		t.Errorf("expected usage exit code %d, got %d", exitUsage, code)
	}
	if mock.lastReq != nil {
		t.Error("expected no API call for an unsupported url scheme")
	}
}
