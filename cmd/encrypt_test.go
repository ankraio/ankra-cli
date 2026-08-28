package cmd

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEncryptManifestResolution(t *testing.T) {
	clusterYAML := `kind: ImportCluster
metadata:
  name: test-cluster
spec:
  stacks:
    - name: monitoring
      manifests:
        - name: db-secret
          from_file: manifests/secret.yaml
        - name: namespace
          from_file: manifests/ns.yaml
      addons:
        - name: prometheus
          chart_name: kube-prometheus-stack
          chart_version: "56.0.0"
    - name: networking
      manifests:
        - name: ingress-config
          from_file: manifests/ingress.yaml`

	var cluster ImportClusterConfig
	if err := yaml.Unmarshal([]byte(clusterYAML), &cluster); err != nil {
		t.Fatalf("failed to parse cluster YAML: %v", err)
	}

	if cluster.Kind != "ImportCluster" {
		t.Errorf("kind = %q, want %q", cluster.Kind, "ImportCluster")
	}

	t.Run("find manifest in first stack", func(t *testing.T) {
		var found *ManifestConfig
		for stackIdx := range cluster.Spec.Stacks {
			for manifestIdx := range cluster.Spec.Stacks[stackIdx].Manifests {
				if cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx].Name == "db-secret" {
					found = &cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx]
					break
				}
			}
			if found != nil {
				break
			}
		}
		if found == nil {
			t.Fatal("expected to find manifest 'db-secret'")
		} else if found.FromFile != "manifests/secret.yaml" {
			t.Errorf("from_file = %q, want %q", found.FromFile, "manifests/secret.yaml")
		}
	})

	t.Run("find manifest in second stack", func(t *testing.T) {
		var found *ManifestConfig
		for stackIdx := range cluster.Spec.Stacks {
			for manifestIdx := range cluster.Spec.Stacks[stackIdx].Manifests {
				if cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx].Name == "ingress-config" {
					found = &cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx]
					break
				}
			}
			if found != nil {
				break
			}
		}
		if found == nil {
			t.Fatal("expected to find manifest 'ingress-config'")
		}
	})

	t.Run("manifest not found", func(t *testing.T) {
		var found *ManifestConfig
		for stackIdx := range cluster.Spec.Stacks {
			for manifestIdx := range cluster.Spec.Stacks[stackIdx].Manifests {
				if cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx].Name == "nonexistent" {
					found = &cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx]
					break
				}
			}
		}
		if found != nil {
			t.Error("expected manifest to not be found")
		}
	})
}

func TestEncryptAddonResolution(t *testing.T) {
	clusterYAML := `kind: ImportCluster
metadata:
  name: test-cluster
spec:
  stacks:
    - name: monitoring
      addons:
        - name: grafana
          chart_name: grafana
          chart_version: "7.0.0"
          configuration:
            from_file: values/grafana.yaml
        - name: prometheus
          chart_name: kube-prometheus-stack
          chart_version: "56.0.0"`

	var cluster ImportClusterConfig
	if err := yaml.Unmarshal([]byte(clusterYAML), &cluster); err != nil {
		t.Fatalf("failed to parse cluster YAML: %v", err)
	}

	t.Run("find addon with from_file", func(t *testing.T) {
		var found *AddonConfig
		for stackIdx := range cluster.Spec.Stacks {
			for addonIdx := range cluster.Spec.Stacks[stackIdx].Addons {
				if cluster.Spec.Stacks[stackIdx].Addons[addonIdx].Name == "grafana" {
					found = &cluster.Spec.Stacks[stackIdx].Addons[addonIdx]
					break
				}
			}
			if found != nil {
				break
			}
		}
		if found == nil {
			t.Fatal("expected to find addon 'grafana'")
		} else {
			if found.Configuration == nil {
				t.Fatal("expected configuration to be present")
			}
			fromFile, ok := found.Configuration["from_file"].(string)
			if !ok || fromFile == "" {
				t.Error("expected from_file to be set in configuration")
			}
		}
	})

	t.Run("addon without configuration", func(t *testing.T) {
		var found *AddonConfig
		for stackIdx := range cluster.Spec.Stacks {
			for addonIdx := range cluster.Spec.Stacks[stackIdx].Addons {
				if cluster.Spec.Stacks[stackIdx].Addons[addonIdx].Name == "prometheus" {
					found = &cluster.Spec.Stacks[stackIdx].Addons[addonIdx]
					break
				}
			}
		}
		if found == nil {
			t.Fatal("expected to find addon 'prometheus'")
		} else if len(found.Configuration) > 0 {
			fromFile, ok := found.Configuration["from_file"].(string)
			if ok && fromFile != "" {
				t.Error("expected prometheus to not have from_file")
			}
		}
	})
}

func TestNormalizeEncryptKey(t *testing.T) {
	tests := []struct {
		name      string
		rawKey    string
		expected  string
		expectErr bool
	}{
		{"plain key", "password", "password", false},
		{"dotted path uses last segment", "data.password", "password", false},
		{"deeply dotted path", "spec.template.secret.apiKey", "apiKey", false},
		{"surrounding whitespace trimmed", "  password  ", "password", false},
		{"leading-dot key kept literally", ".dockerconfigjson", ".dockerconfigjson", false},
		{"leading-dot key with whitespace", "  .dockerconfigjson  ", ".dockerconfigjson", false},
		{"empty key", "", "", true},
		{"whitespace-only key", "   ", "", true},
		{"trailing dot", "data.", "", true},
		{"bare dot", ".", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := normalizeEncryptKey(tt.rawKey)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("normalizeEncryptKey(%q) expected error, got %q", tt.rawKey, result)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeEncryptKey(%q) unexpected error: %v", tt.rawKey, err)
			}
			if result != tt.expected {
				t.Errorf("normalizeEncryptKey(%q) = %q, want %q", tt.rawKey, result, tt.expected)
			}
		})
	}
}

func TestVerifyKeyEncrypted(t *testing.T) {
	encryptedSecret := `apiVersion: v1
kind: Secret
data:
  username: YWRtaW4=
  password: ENC[AES256_GCM,data:abc,iv:def,tag:ghi,type:str]
sops:
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(password)$
`

	plaintextWithSopsMetadata := `apiVersion: v1
kind: Secret
data:
  username: YWRtaW4=
  password: aHVudGVyMg==
sops:
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(data.password)$
`

	t.Run("encrypted value passes", func(t *testing.T) {
		if err := verifyKeyEncrypted(encryptedSecret, "password"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("encrypted leading-dot key passes", func(t *testing.T) {
		content := `apiVersion: v1
kind: Secret
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: ENC[AES256_GCM,data:abc,iv:def,tag:ghi,type:str]
sops:
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(\.dockerconfigjson)$
`
		if err := verifyKeyEncrypted(content, ".dockerconfigjson"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("plaintext leading-dot key fails", func(t *testing.T) {
		content := `apiVersion: v1
kind: Secret
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: eyJhdXRocyI6e319
sops:
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(\.dockerconfigjson)$
`
		err := verifyKeyEncrypted(content, ".dockerconfigjson")
		if err == nil {
			t.Fatal("expected error for plaintext value")
		}
		if !strings.Contains(err.Error(), "still plaintext") {
			t.Errorf("expected plaintext error, got: %v", err)
		}
	})

	t.Run("plaintext value under sops metadata fails", func(t *testing.T) {
		err := verifyKeyEncrypted(plaintextWithSopsMetadata, "password")
		if err == nil {
			t.Fatal("expected error for plaintext value")
		}
		if !strings.Contains(err.Error(), "still plaintext") {
			t.Errorf("expected plaintext error, got: %v", err)
		}
	})

	t.Run("missing key fails", func(t *testing.T) {
		err := verifyKeyEncrypted(encryptedSecret, "token")
		if err == nil {
			t.Fatal("expected error for missing key")
		}
		if !strings.Contains(err.Error(), "SOPS encrypted nothing") {
			t.Errorf("expected missing-key error, got: %v", err)
		}
	})

	t.Run("sops metadata key does not count as a match", func(t *testing.T) {
		if err := verifyKeyEncrypted(encryptedSecret, "mac"); err == nil {
			t.Error("expected error: 'mac' only exists inside the sops metadata block")
		}
	})

	t.Run("nested mapping under matched key must be fully encrypted", func(t *testing.T) {
		content := `credentials:
  username: ENC[AES256_GCM,data:abc]
  password: plain
`
		if err := verifyKeyEncrypted(content, "credentials"); err == nil {
			t.Error("expected error for partially encrypted subtree")
		}
	})

	t.Run("multi-document YAML finds the key in any document", func(t *testing.T) {
		content := `kind: Namespace
metadata:
  name: web
---
kind: Secret
data:
  password: ENC[AES256_GCM,data:abc]
`
		if err := verifyKeyEncrypted(content, "password"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("leading fingerprint comment is tolerated", func(t *testing.T) {
		content := "# ankra_content_fingerprint: abc123\ndata:\n  password: ENC[AES256_GCM,data:abc]\n"
		if err := verifyKeyEncrypted(content, "password"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestGetEncryptedPathsFromConfig(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		result := getEncryptedPathsFromConfig(nil)
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("config without encrypted_paths", func(t *testing.T) {
		config := map[string]interface{}{
			"from_file": "values.yaml",
		}
		result := getEncryptedPathsFromConfig(config)
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("config with string slice encrypted_paths", func(t *testing.T) {
		config := map[string]interface{}{
			"encrypted_paths": []string{"data.password", "data.token"},
		}
		result := getEncryptedPathsFromConfig(config)
		if len(result) != 2 {
			t.Errorf("expected 2 paths, got %d", len(result))
		}
	})

	t.Run("config with interface slice encrypted_paths", func(t *testing.T) {
		config := map[string]interface{}{
			"encrypted_paths": []interface{}{"data.password", "data.token"},
		}
		result := getEncryptedPathsFromConfig(config)
		if len(result) != 2 {
			t.Errorf("expected 2 paths, got %d", len(result))
		}
	})
}

func TestDeriveSopsEncryptedPaths(t *testing.T) {
	t.Run("sops document with ciphertext leaves", func(t *testing.T) {
		content := `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
data:
  username: YWRtaW4=
  password: ENC[AES256_GCM,data:abc,iv:def,tag:ghi,type:str]
  token: ENC[AES256_GCM,data:xyz,iv:def,tag:ghi,type:str]
sops:
  age:
    - recipient: age1example
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(password|token)$
`
		paths, isSops, err := deriveSopsEncryptedPaths([]byte(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !isSops {
			t.Error("expected isSopsDocument=true")
		}
		if len(paths) != 2 || paths[0] != "password" || paths[1] != "token" {
			t.Errorf("paths = %v, want [password token]", paths)
		}
	})

	t.Run("sops metadata subtree is not treated as user data", func(t *testing.T) {
		content := `data:
  password: ENC[AES256_GCM,data:abc]
sops:
  mac: ENC[AES256_GCM,data:mac]
`
		paths, isSops, err := deriveSopsEncryptedPaths([]byte(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !isSops {
			t.Error("expected isSopsDocument=true")
		}
		for _, path := range paths {
			if path == "mac" {
				t.Errorf("sops metadata key leaked into paths: %v", paths)
			}
		}
	})

	t.Run("plain document is not sops", func(t *testing.T) {
		content := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: web\n"
		paths, isSops, err := deriveSopsEncryptedPaths([]byte(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if isSops {
			t.Error("expected isSopsDocument=false")
		}
		if len(paths) != 0 {
			t.Errorf("paths = %v, want none", paths)
		}
	})

	t.Run("multi-document detects sops in any document", func(t *testing.T) {
		content := `apiVersion: v1
kind: Namespace
metadata:
  name: web
---
data:
  password: ENC[AES256_GCM,data:abc]
sops:
  version: 3.8.1
`
		paths, isSops, err := deriveSopsEncryptedPaths([]byte(content))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !isSops {
			t.Error("expected isSopsDocument=true")
		}
		if len(paths) != 1 || paths[0] != "password" {
			t.Errorf("paths = %v, want [password]", paths)
		}
	})

	t.Run("invalid yaml errors", func(t *testing.T) {
		if _, _, err := deriveSopsEncryptedPaths([]byte(":\n  - ][")); err == nil {
			t.Error("expected a parse error")
		}
	})
}

func TestUnionEncryptedPaths(t *testing.T) {
	merged := unionEncryptedPaths([]string{"password"}, []string{"token", "password"}, []string{"apiKey"})
	if len(merged) != 3 || merged[0] != "password" || merged[1] != "token" || merged[2] != "apiKey" {
		t.Errorf("merged = %v, want [password token apiKey]", merged)
	}
	if merged := unionEncryptedPaths(nil, nil); merged != nil {
		t.Errorf("union of empties = %v, want nil", merged)
	}
}

// --- glob: key entries (PLA-798) ---

// TestEncryptKeyGlobGrammarMirrorsThePlatform pins the prefix and the
// rejection rules to the platform's encrypted_paths grammar
// (clusterengine.EncryptedPathGlobPrefix / ParseEncryptedPathGlob in the
// cluster repo's enginekit): the CLI cannot import that module, so the
// literal and the four refusals are mirrored here and held by this test.
func TestEncryptKeyGlobGrammarMirrorsThePlatform(t *testing.T) {
	if encryptKeyGlobPrefix != "glob:" {
		t.Fatalf("prefix drifted from the platform's: %q", encryptKeyGlobPrefix)
	}
	accepted := map[string][]string{
		"glob:stringData.DB_*": {"DB_PASSWORD", "DB_"},
		"glob:data.*_KEY":      {"API_KEY"},
		"glob:*password*":      {"adminpassword", "password"},
		"glob:cloud.*":         {"cloud.conf"},
		"glob:.docker*":        {".dockerconfigjson"},
		"glob:a+b(c)*":         {"a+b(c)d"},
	}
	rejectedKeys := map[string][]string{
		"glob:stringData.DB_*": {"MYDB_PASSWORD", "db_password"},
		"glob:cloud.*":         {"cloudXconf"},
		"glob:.docker*":        {"dockerconfigjson"},
		"glob:a+b(c)*":         {"aab(c)"},
	}
	for entry, keys := range accepted {
		matcher, err := parseEncryptKeyGlob(entry)
		if err != nil {
			t.Fatalf("%s: %v", entry, err)
		}
		for _, key := range keys {
			if !matcher.MatchString(key) {
				t.Errorf("%s: key %q must match", entry, key)
			}
		}
		for _, key := range rejectedKeys[entry] {
			if matcher.MatchString(key) {
				t.Errorf("%s: key %q must not match", entry, key)
			}
		}
	}
	rejected := map[string]string{
		"glob:":                       "must be followed by a key-name pattern",
		"glob:stringData.":            "names a section but no key-name pattern",
		"glob:stringData.DB_PASSWORD": "contains no * wildcard",
		"glob:*":                      "would match every key",
		"glob:stringData.*":           "would match every key",
		"glob:**":                     "would match every key",
	}
	for entry, reason := range rejected {
		_, err := parseEncryptKeyGlob(entry)
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%s: expected refusal mentioning %q, got %v", entry, reason, err)
		}
	}
}

func TestNormalizeEncryptKeyKeepsGlobEntriesVerbatim(t *testing.T) {
	for _, rawKey := range []string{"glob:stringData.DB_*", "  glob:*Password  ", "glob:DB_*_KEY"} {
		entry, err := normalizeEncryptKey(rawKey)
		if err != nil {
			t.Fatalf("normalizeEncryptKey(%q): %v", rawKey, err)
		}
		if entry != strings.TrimSpace(rawKey) {
			t.Errorf("normalizeEncryptKey(%q) = %q, want the entry verbatim (prefix included, never split on dots)", rawKey, entry)
		}
	}
	for _, rawKey := range []string{"glob:", "glob:stringData.DB_PASSWORD", "glob:*"} {
		if entry, err := normalizeEncryptKey(rawKey); err == nil {
			t.Errorf("normalizeEncryptKey(%q) accepted an invalid glob as %q", rawKey, entry)
		}
	}
	// A literal that merely looks like a pattern is not one: the prefix is
	// the opt-in, so "DB_*" stays an exact (and useless) key name.
	if entry, err := normalizeEncryptKey("stringData.DB_*"); err != nil || entry != "DB_*" {
		t.Errorf("literal with a star must stay a literal leaf key, got %q %v", entry, err)
	}
}

func TestVerifyKeyEncryptedHonoursGlobEntries(t *testing.T) {
	sealed := `apiVersion: v1
kind: Secret
stringData:
  DB_PASSWORD: ENC[AES256_GCM,data:abc,iv:def,tag:ghi,type:str]
  DB_USER: ENC[AES256_GCM,data:jkl,iv:mno,tag:pqr,type:str]
  LOG_LEVEL: debug
sops:
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(DB_.*)$
`
	t.Run("every glob-matched key sealed passes", func(t *testing.T) {
		if err := verifyKeyEncrypted(sealed, "glob:stringData.DB_*"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("glob matching no key fails", func(t *testing.T) {
		err := verifyKeyEncrypted(sealed, "glob:stringData.REDIS_*")
		if err == nil || !strings.Contains(err.Error(), "SOPS encrypted nothing") {
			t.Errorf("expected the encrypted-nothing refusal, got %v", err)
		}
	})
	t.Run("plaintext under a glob-matched key fails", func(t *testing.T) {
		partiallySealed := strings.Replace(sealed, "DB_USER: ENC[AES256_GCM,data:jkl,iv:mno,tag:pqr,type:str]", "DB_USER: trinity", 1)
		err := verifyKeyEncrypted(partiallySealed, "glob:stringData.DB_*")
		if err == nil || !strings.Contains(err.Error(), "still plaintext") {
			t.Errorf("expected the plaintext refusal, got %v", err)
		}
	})
	t.Run("sops metadata keys never count", func(t *testing.T) {
		if err := verifyKeyEncrypted(sealed, "glob:ma*"); err == nil {
			t.Error("expected refusal: 'mac' only exists inside the sops metadata block")
		}
	})
	t.Run("invalid glob is refused", func(t *testing.T) {
		if err := verifyKeyEncrypted(sealed, "glob:*"); err == nil {
			t.Error("expected refusal for a glob that would match every key")
		}
	})
}

func TestDescribeEncryptKeysRendersGlobEntries(t *testing.T) {
	cases := map[string][]string{
		`key "password"`:                                        {"password"},
		`keys "password", "token"`:                              {"password", "token"},
		`keys matching glob:stringData.DB_*`:                    {"glob:stringData.DB_*"},
		`key "password" and keys matching glob:DB_*, glob:*Key`: {"password", "glob:DB_*", "glob:*Key"},
	}
	for expected, entries := range cases {
		if actual := describeEncryptKeys(entries); actual != expected {
			t.Errorf("describeEncryptKeys(%v) = %q, want %q", entries, actual, expected)
		}
	}
}
