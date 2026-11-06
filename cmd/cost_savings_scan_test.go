package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// savingsWithWaste is a priced, quiet savings read carrying only the waste
// block under test, so the waste lines are the part of the output that moves.
func savingsWithWaste(waste client.CloudSavingsWaste) *client.CloudSavings {
	return &client.CloudSavings{
		Currency: "eur", PricedClusterCount: 1, AnalysedClusterCount: 1,
		Recommendations: []client.CloudSavingsRecommendation{}, Breakdowns: []client.CloudSavingsBreakdown{},
		Namespaces: []client.CloudSavingsNamespace{}, UnpricedClusters: []client.CloudSavingsCluster{},
		StaleClusters: []client.CloudSavingsCluster{}, UnreadableClusters: []client.CloudSavingsCluster{},
		Waste:      waste,
		Thresholds: client.CloudSavingsThresholds{MinimumSavingsCents: 500, AnalysedClusterLimit: 8},
	}
}

func scanFlag(value bool) *bool { return &value }

// runCostCommandKeepingStderr is runCostCommand with stderr returned too.
func runCostCommandKeepingStderr(t *testing.T, mock APIClient, args ...string) (string, string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(args)
	t.Cleanup(func() { resetTreeFlags(t, costCommandTree()...) })
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

// failedScansFixture is one failed scan of each shape the waste listing
// gives: a named credential, a scan no organisation credential stands behind,
// and the one entry standing for the scans outside the reader's projects.
func failedScansFixture() []client.CloudWasteCredentialError {
	scannedAt := "2026-11-05T22:00:00Z"
	return []client.CloudWasteCredentialError{
		{Provider: "hetzner", CredentialName: "prod-hetzner", Error: "401 Unauthorized", ScannedAt: &scannedAt},
		{Provider: "ovh", CredentialName: "", Error: "token expired", ScannedAt: &scannedAt},
		{CredentialOutsideProjects: true, Error: "A waste scan by a credential outside your projects did not finish."},
	}
}

func TestCostSavingsWithAnIncompleteScanAndNoFindingsSaysUnknownAndNamesTheFailedScans(t *testing.T) {
	for _, hasData := range []bool{true, false} {
		mock := &costMock{
			savings:      savingsWithWaste(client.CloudSavingsWaste{Available: true, HasData: hasData, ScanIncomplete: scanFlag(true)}),
			scanFailures: failedScansFixture(),
		}
		output, executeError := runCostCommand(t, mock, "cost", "savings")
		if executeError != nil {
			t.Fatalf("cost savings failed: %v", executeError)
		}
		want := "\nWaste: none listed, but the last scan did not finish, so that is unknown, not no waste.\n" +
			"  Did not finish: prod-hetzner (Hetzner Cloud): 401 Unauthorized\n" +
			"  Did not finish: a credential Ankra has no name for (OVHcloud): token expired\n" +
			"  Did not finish: a scan by a credential outside your projects.\n"
		if !strings.HasSuffix(output, want) {
			t.Fatalf("has_data %v: the waste block must say the scan did not finish and name each failed scan, want suffix\n%q\ngot\n%q", hasData, want, output)
		}
		// A failed scan as the only data is the case this guards: it must never
		// read as a clean scan, nor as no scan at all.
		for _, absent := range []string{"no open findings", "no scan yet"} {
			if strings.Contains(output, absent) {
				t.Fatalf("has_data %v: an incomplete scan printed %q:\n%s", hasData, absent, output)
			}
		}
		if mock.scanFailureReads != 1 {
			t.Fatalf("has_data %v: the failed scans are read once, got %d reads", hasData, mock.scanFailureReads)
		}
	}
}

func TestCostSavingsWithAnIncompleteScanAndFindingsSaysTheCountMayBeShort(t *testing.T) {
	savings := cloudSavingsFixture()
	savings.Waste.ScanIncomplete = scanFlag(true)
	mock := &costMock{savings: savings, scanFailures: failedScansFixture()[:1]}
	output, executeError := runCostCommand(t, mock, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings failed: %v", executeError)
	}
	want := "\nWaste: 7 findings open, €120.00/mo (2 unpriced, not in that figure) · scanned 2026-09-17T22:00:00\n" +
		"The last scan did not finish, so findings may be missing from that count.\n" +
		"  Did not finish: prod-hetzner (Hetzner Cloud): 401 Unauthorized\n"
	if !strings.HasSuffix(output, want) {
		t.Fatalf("want suffix\n%q\ngot\n%q", want, output)
	}
}

func TestCostSavingsWhenTheFailedScansCannotBeReadStillSucceeds(t *testing.T) {
	mock := &costMock{
		savings:           savingsWithWaste(client.CloudSavingsWaste{Available: true, HasData: true, ScanIncomplete: scanFlag(true)}),
		scanFailuresError: errors.New("503 Service Unavailable"),
	}
	output, stderr, executeError := runCostCommandKeepingStderr(t, mock, "cost", "savings")
	if executeError != nil {
		t.Fatalf("the savings read already said the scan is incomplete, so an unreadable listing must not fail the command: %v", executeError)
	}
	want := "\nWaste: none listed, but the last scan did not finish, so that is unknown, not no waste.\n" +
		"  Which scan did not finish could not be read.\n"
	if !strings.HasSuffix(output, want) {
		t.Fatalf("want suffix\n%q\ngot\n%q", want, output)
	}
	if !strings.Contains(stderr, "Reading which waste scan did not finish failed: 503 Service Unavailable") {
		t.Fatalf("the reason goes to stderr, got %q", stderr)
	}
	if strings.Contains(output, "503") {
		t.Fatalf("the read error belongs on stderr, not stdout:\n%s", output)
	}
}

func TestCostSavingsFailedScanTextIsFlattenedToOneLine(t *testing.T) {
	mock := &costMock{
		savings: savingsWithWaste(client.CloudSavingsWaste{Available: true, HasData: true, ScanIncomplete: scanFlag(true)}),
		scanFailures: []client.CloudWasteCredentialError{
			{Provider: "hetzner", CredentialName: "prod-\u202ehetzner\nWaste: no open findings.", Error: "denied\r\nretry"},
		},
	}
	output, executeError := runCostCommand(t, mock, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings failed: %v", executeError)
	}
	if !strings.Contains(output, "  Did not finish: prod-hetzner Waste: no open findings. (Hetzner Cloud): denied retry\n") {
		t.Fatalf("a credential name and an error are one line with hidden runes removed:\n%q", output)
	}
	if strings.Contains(output, "\nWaste: no open findings.") || strings.Contains(output, "\u202e") {
		t.Fatalf("provider text forged a line of its own:\n%q", output)
	}
}

// TestCostSavingsWasteLineWithoutScanIncompleteIsByteForByteUnchanged pins the
// waste line a platform that predates scan_incomplete gets, and one that says
// false, to the strings this command printed before the field existed, and
// that neither costs a read of the waste listing.
func TestCostSavingsWasteLineWithoutScanIncompleteIsByteForByteUnchanged(t *testing.T) {
	scannedAt := "2026-09-17T22:00:00"
	cases := []struct {
		name  string
		waste client.CloudSavingsWaste
		want  string
	}{
		{"unreadable", client.CloudSavingsWaste{Available: false},
			"\nWaste: the scan could not be read (this is not the same as no waste).\n"},
		{"no scan yet", client.CloudSavingsWaste{Available: true, HasData: false},
			"\nWaste: no scan yet.\n"},
		{"no findings", client.CloudSavingsWaste{Available: true, HasData: true},
			"\nWaste: no open findings.\n"},
		{"findings", client.CloudSavingsWaste{Available: true, HasData: true, FindingCount: 7, UnpricedFindingCount: 2,
			TotalMonthlyCostCents: 12000, ScannedAt: &scannedAt},
			"\nWaste: 7 findings open, €120.00/mo (2 unpriced, not in that figure) · scanned 2026-09-17T22:00:00\n"},
	}
	for _, testCase := range cases {
		for _, scanIncomplete := range []*bool{nil, scanFlag(false)} {
			waste := testCase.waste
			waste.ScanIncomplete = scanIncomplete
			rendered := new(bytes.Buffer)
			renderCloudSavingsWaste(rendered, waste, "eur", &cloudSavingsScanFailures{failures: failedScansFixture()})
			if rendered.String() != testCase.want {
				t.Fatalf("%s, scan_incomplete %v: want %q, got %q", testCase.name, scanIncomplete, testCase.want, rendered.String())
			}
		}
	}

	// An unreadable waste block says nothing of a scan, even when the flag is
	// set: the read failed before any scan was looked at.
	unreadable := new(bytes.Buffer)
	renderCloudSavingsWaste(unreadable, client.CloudSavingsWaste{Available: false, ScanIncomplete: scanFlag(true)}, "eur", nil)
	if unreadable.String() != cases[0].want {
		t.Fatalf("unreadable waste with the flag set: want %q, got %q", cases[0].want, unreadable.String())
	}

	var outputs []string
	for _, scanIncomplete := range []*bool{nil, scanFlag(false)} {
		savings := cloudSavingsFixture()
		savings.Waste.ScanIncomplete = scanIncomplete
		mock := &costMock{savings: savings, scanFailures: failedScansFixture()}
		output, executeError := runCostCommand(t, mock, "cost", "savings")
		if executeError != nil {
			t.Fatalf("cost savings failed: %v", executeError)
		}
		if mock.scanFailureReads != 0 {
			t.Fatalf("scan_incomplete %v read the waste listing %d times; a complete scan costs no extra request", scanIncomplete, mock.scanFailureReads)
		}
		outputs = append(outputs, output)
	}
	if outputs[0] != outputs[1] {
		t.Fatalf("an absent scan_incomplete and false must render the same bytes:\n%q\n%q", outputs[0], outputs[1])
	}
}

func TestCostSavingsStructuredOutputMakesNoFailedScanRead(t *testing.T) {
	mock := &costMock{
		savings:      savingsWithWaste(client.CloudSavingsWaste{Available: true, HasData: true, ScanIncomplete: scanFlag(true)}),
		scanFailures: failedScansFixture(),
	}
	output, executeError := runCostCommand(t, mock, "cost", "savings", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost savings -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	if mock.scanFailureReads != 0 {
		t.Fatalf("structured output is the savings document alone, got %d waste reads", mock.scanFailureReads)
	}
}
