package cmd

import "testing"

func TestReleaseAssetName(t *testing.T) {
	cases := []struct {
		goos    string
		goarch  string
		want    string
		wantErr bool
	}{
		{"darwin", "arm64", "ankra-cli-darwin-arm64", false},
		{"darwin", "amd64", "ankra-cli-darwin-amd64", false},
		{"linux", "amd64", "ankra-cli-linux-amd64", false},
		{"linux", "arm64", "ankra-cli-linux-arm64", false},
		{"windows", "amd64", "", true},
		{"linux", "386", "", true},
	}
	for _, tc := range cases {
		got, err := releaseAssetName(tc.goos, tc.goarch)
		if tc.wantErr {
			if err == nil {
				t.Errorf("releaseAssetName(%q, %q) expected error, got %q", tc.goos, tc.goarch, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("releaseAssetName(%q, %q) unexpected error: %v", tc.goos, tc.goarch, err)
			continue
		}
		if got != tc.want {
			t.Errorf("releaseAssetName(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestReleaseDownloadURLs(t *testing.T) {
	binaryURL, checksumURL := releaseDownloadURLs("v0.2.5", "ankra-cli-linux-amd64")
	wantBinary := "https://github.com/ankraio/ankra-cli/releases/download/v0.2.5/ankra-cli-linux-amd64"
	if binaryURL != wantBinary {
		t.Errorf("binary URL = %q, want %q", binaryURL, wantBinary)
	}
	if checksumURL != wantBinary+".sha256" {
		t.Errorf("checksum URL = %q, want %q", checksumURL, wantBinary+".sha256")
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"v0.2.4":   "0.2.4",
		"0.2.4":    "0.2.4",
		" v1.0.0 ": "1.0.0",
		"V2.3.4":   "2.3.4",
	}
	for input, want := range cases {
		if got := normalizeVersion(input); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEnsureTagPrefix(t *testing.T) {
	cases := map[string]string{
		"v0.2.5":     "v0.2.5",
		"0.2.5":      "v0.2.5",
		" 0.2.5 ":    "v0.2.5",
		"V1.0.0":     "v1.0.0",
		"0.3.0-rc.1": "v0.3.0-rc.1",
		"":           "",
		"   ":        "",
	}
	for input, want := range cases {
		if got := ensureTagPrefix(input); got != want {
			t.Errorf("ensureTagPrefix(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left  string
		right string
		want  int
	}{
		{"0.2.5", "0.2.4", 1},
		{"0.2.4", "0.2.5", -1},
		{"0.2.4", "0.2.4", 0},
		{"1.0.0", "0.9.9", 1},
		{"0.2.10", "0.2.9", 1},
		{"0.2", "0.2.0", 0},
		{"1.2.0", "1.2.0-rc.1", 1},
		{"1.2.0-rc.1", "1.2.0", -1},
		{"1.2.0-rc.2", "1.2.0-rc.1", 1},
		{"1.2.0-rc.1", "1.2.0-rc.1", 0},
		{"0.3.0-rc.1", "0.2.9", 1},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.left, tc.right); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}
