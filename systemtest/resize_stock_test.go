package systemtest

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestHetznerResizeIsSkippedWhenTheTargetTypeHasNoStock pins ankra-mw9qji: the
// lane picks its location by stock only at create time, cpx32 left fsn1 on
// 2026-10-14, and the resize to it was refused by the provider hours later.
// A stock gap is Hetzner's, not the platform's, so the resize step re-reads
// the location's stock first and records a SKIP naming it; a type that is
// available, or a stock read that failed, still runs the step so a genuine
// refusal or timeout stays a FAIL.
func TestHetznerResizeIsSkippedWhenTheTargetTypeHasNoStock(t *testing.T) {
	bash, lookError := exec.LookPath("bash")
	if lookError != nil {
		t.Skip("bash not available")
	}
	script, readError := os.ReadFile("lifecycle_systemtest.sh")
	if readError != nil {
		t.Fatal(readError)
	}
	definition := regexp.MustCompile(`(?ms)^hetzner_resize_stock_gap\(\) \{.*?^\}`).Find(script)
	if definition == nil {
		t.Fatal("hetzner_resize_stock_gap is not defined in lifecycle_systemtest.sh")
	}

	stub := `hetzner_available_types() { case "$1" in fsn1) printf 'cpx22\ncpx12\n';; nbg1) printf 'cpx22\ncpx32\n';; broken) return 7;; empty) return 0;; esac; }`
	cases := []struct {
		name     string
		location string
		wantGap  bool
		want     string
	}{
		{"the type left the location", "fsn1", true, "server type cpx32 not available in fsn1 right now"},
		{"the type is still offered", "nbg1", false, ""},
		{"a stock read that failed is not a stock gap", "broken", false, ""},
		{"an empty stock read is not a stock gap", "empty", false, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			program := "set -euo pipefail\n" + stub + "\n" + string(definition) + "\nhetzner_resize_stock_gap cpx32 \"$1\""
			output, runError := exec.Command(bash, "-c", program, "bash", testCase.location).Output()
			if gap := runError == nil; gap != testCase.wantGap {
				t.Fatalf("hetzner_resize_stock_gap cpx32 %s: gap=%v, want %v (output %q)", testCase.location, gap, testCase.wantGap, output)
			}
			if got := strings.TrimSpace(string(output)); got != testCase.want {
				t.Errorf("reason = %q, want %q", got, testCase.want)
			}
		})
	}

	// The resize step must use the check, and a gap must record a SKIP, never
	// a PASS; the refusal and timeout path keeps its FAIL.
	step := regexp.MustCompile(`(?ms)# 7\. Instance resize.*?# 7b\.`).Find(script)
	if step == nil {
		t.Fatal("the instance resize step (# 7.) was not found")
	}
	body := string(step)
	if !strings.Contains(body, "hetzner_resize_stock_gap") {
		t.Error("the resize step does not re-check stock before resizing")
	}
	if !regexp.MustCompile(`skip "[^"]*instance resize[^"]*\$\{?reason`).MatchString(body) {
		t.Error("a stock gap must be recorded with skip(), carrying the reason")
	}
	if !strings.Contains(body, `fail "$label instance resize"`) {
		t.Error("a genuine refusal or timeout must stay a FAIL")
	}
}
