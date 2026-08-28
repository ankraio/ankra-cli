package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The platform spells encrypted_paths section-qualified ("stringData.PASSWORD")
// and reads the bare key name as the same entry. The CLI used to compare the
// two as strings, find nothing, and append every key again as a bare name -
// then re-encode the whole cluster file in its own style on the way out.
// These tests hold a platform-written file to what it deserves: the CLI
// recognises the entries it already has, spells a new one the way the file
// does, and changes nothing else.

// platformStyleClusterYAML is a cluster file as the platform writes it:
// indentless sequences, a folded long scalar, alphabetical keys, and an
// encrypted_paths entry in the platform's section-qualified spelling.
const platformStyleClusterYAML = `apiVersion: v1
kind: ImportCluster
metadata:
  name: prod
spec:
  stacks:
  - addons: []
    description: a description long enough that the platform's emitter folds it
      onto a second line, which must come back exactly as it was
    manifests:
    - encrypted_paths:
      - data.username
      force: false
      from_file: manifests/secret.yaml
      name: db-secret
    name: core
`

const bothKeysEncryptedManifestYAML = `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: web
type: Opaque
data:
  username: ENC[AES256_GCM,data:abc,iv:def,tag:ghi,type:str]
  password: ENC[AES256_GCM,data:jkl,iv:mno,tag:pqr,type:str]
sops:
  age:
    - recipient: age1example
  lastmodified: "2026-06-11T00:00:00Z"
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(username|password)$
`

// writePlatformStyleFixture lays out a cluster file and its plaintext Secret
// and returns the cluster path.
func writePlatformStyleFixture(t *testing.T, clusterYAML string) string {
	t.Helper()
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(clusterPath, []byte(clusterYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifests", "secret.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(plainSecretManifestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return clusterPath
}

func runEncryptManifestCommand(t *testing.T, mock *upgradeMock, args ...string) string {
	t.Helper()
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)
	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(append([]string{"cluster", "encrypt", "manifest", "db-secret"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	return out.String()
}

func TestRunEncryptManifest_PlatformStyleFileChangesByExactlyOneLine(t *testing.T) {
	clusterPath := writePlatformStyleFixture(t, platformStyleClusterYAML)
	mock := &upgradeMock{encryptResult: encryptedSecretManifestYAML}

	runEncryptManifestCommand(t, mock, "--key", "password", "-f", clusterPath)

	written, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	// One line, in the file's own indentless style and section-qualified
	// spelling; the folded description and every other sequence untouched.
	want := strings.Replace(platformStyleClusterYAML,
		"      - data.username\n",
		"      - data.username\n      - data.password\n", 1)
	if string(written) != want {
		t.Errorf("cluster file changed beyond the one entry.\n--- got ---\n%s\n--- want ---\n%s", written, want)
	}
}

func TestRunEncryptManifest_SectionQualifiedEntryCountsAsPresent(t *testing.T) {
	clusterPath := writePlatformStyleFixture(t, platformStyleClusterYAML)
	mock := &upgradeMock{encryptResult: bothKeysEncryptedManifestYAML}

	runEncryptManifestCommand(t, mock, "--key", "username", "-f", clusterPath)

	// The key is still encrypted; only the cluster file is left alone.
	if len(mock.encryptCalls) != 1 || !reflect.DeepEqual(mock.encryptCalls[0].EncryptedPaths, []string{"username"}) {
		t.Errorf("encrypt calls = %+v, want one call for username", mock.encryptCalls)
	}
	written, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != platformStyleClusterYAML {
		t.Errorf("cluster file should be byte-identical.\n--- got ---\n%s", written)
	}
}

// The case seen in the field: --all-data on a Secret whose every key the
// platform had already recorded. 15 entries became 31.
func TestRunEncryptManifest_AllDataDoesNotDuplicateSectionQualifiedEntries(t *testing.T) {
	source := strings.Replace(platformStyleClusterYAML,
		"      - data.username\n",
		"      - data.username\n      - data.password\n", 1)
	clusterPath := writePlatformStyleFixture(t, source)
	mock := &upgradeMock{encryptResult: bothKeysEncryptedManifestYAML}

	runEncryptManifestCommand(t, mock, "--all-data", "-f", clusterPath)

	if len(mock.encryptCalls) != 1 || !reflect.DeepEqual(mock.encryptCalls[0].EncryptedPaths, []string{"username", "password"}) {
		t.Errorf("encrypt calls = %+v, want one call for username and password", mock.encryptCalls)
	}
	written, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != source {
		t.Errorf("cluster file should be byte-identical.\n--- got ---\n%s", written)
	}
}

func TestRunEncryptManifest_PlatformStyleFileWithoutTheListGetsOneInItsStyle(t *testing.T) {
	source := strings.Replace(platformStyleClusterYAML,
		"    - encrypted_paths:\n      - data.username\n      force: false\n",
		"    - force: false\n", 1)
	clusterPath := writePlatformStyleFixture(t, source)
	mock := &upgradeMock{encryptResult: encryptedSecretManifestYAML}

	runEncryptManifestCommand(t, mock, "--key", "password", "-f", clusterPath)

	written, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}
	// No existing entry to copy a spelling from, so the CLI's own bare form;
	// dashes level with the key, as every other sequence in the file.
	want := strings.Replace(source,
		"      name: db-secret\n",
		"      name: db-secret\n      encrypted_paths:\n      - password\n", 1)
	if string(written) != want {
		t.Errorf("cluster file changed beyond the new list.\n--- got ---\n%s\n--- want ---\n%s", written, want)
	}
}

func TestEncryptedPathLeaf_StripsTheSectionsThePlatformStrips(t *testing.T) {
	cases := map[string]string{
		"PASSWORD":             "PASSWORD",
		"data.PASSWORD":        "PASSWORD",
		"stringData.PASSWORD":  "PASSWORD",
		"glob:stringData.DB_*": "glob:stringData.DB_*",
		"other.PASSWORD":       "other.PASSWORD",
	}
	for entry, want := range cases {
		if got := encryptedPathLeaf(entry); got != want {
			t.Errorf("encryptedPathLeaf(%q) = %q, want %q", entry, got, want)
		}
	}
}

func TestContainsEncryptedPath_MatchesAcrossSpellings(t *testing.T) {
	if !containsEncryptedPath([]string{"stringData.PASSWORD"}, "PASSWORD") {
		t.Error("a section-qualified entry should count for the bare key")
	}
	if !containsEncryptedPath([]string{"PASSWORD"}, "stringData.PASSWORD") {
		t.Error("a bare entry should count for the section-qualified key")
	}
	if containsEncryptedPath([]string{"glob:PASS*"}, "PASSWORD") {
		t.Error("a glob is compared verbatim, never expanded")
	}
	if containsEncryptedPath([]string{"stringData.PASSWORD"}, "USERNAME") {
		t.Error("a different key must not match")
	}
}

func TestEncryptedPathEntry_FollowsTheFilesSpelling(t *testing.T) {
	cases := []struct {
		name     string
		existing []string
		leaf     string
		section  string
		want     string
	}{
		{"section-qualified file, section known", []string{"data.username"}, "password", "data", "data.password"},
		{"bare file", []string{"username"}, "password", "data", "password"},
		{"empty list", nil, "password", "data", "password"},
		{"globs do not vote", []string{"glob:stringData.DB_*", "stringData.A"}, "B", "stringData", "stringData.B"},
		{"section unknown but the file agrees on one", []string{"stringData.A", "stringData.B"}, "C", "", "stringData.C"},
		{"section unknown and the file mixes sections", []string{"data.A", "stringData.B"}, "C", "", "C"},
		{"mixed spellings fall back to bare", []string{"stringData.A", "B"}, "C", "stringData", "C"},
	}
	for _, tc := range cases {
		if got := encryptedPathEntry(tc.existing, tc.leaf, tc.section); got != tc.want {
			t.Errorf("%s: encryptedPathEntry(%v, %q, %q) = %q, want %q", tc.name, tc.existing, tc.leaf, tc.section, got, tc.want)
		}
	}
}

func TestSecretKeySection_FindsTheSectionHoldingTheKey(t *testing.T) {
	manifest := []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: s\ndata:\n  username: dXNlcg==\nstringData:\n  password: hunter2\n")
	if got := secretKeySection(manifest, "username"); got != "data" {
		t.Errorf("username section = %q, want data", got)
	}
	if got := secretKeySection(manifest, "password"); got != "stringData" {
		t.Errorf("password section = %q, want stringData", got)
	}
	if got := secretKeySection(manifest, "missing"); got != "" {
		t.Errorf("missing section = %q, want empty", got)
	}
}

func TestUnionEncryptedPaths_KeepsTheFirstSpellingOfEachKey(t *testing.T) {
	got := unionEncryptedPaths([]string{"stringData.A"}, []string{"A", "B"}, []string{"glob:X*", "stringData.B"})
	want := []string{"stringData.A", "B", "glob:X*"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unionEncryptedPaths = %v, want %v", got, want)
	}
}
