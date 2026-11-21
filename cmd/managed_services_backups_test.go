package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"
)

// fakeServiceWithoutBackupsStatement is the sentence a plan carries for a
// service the organisation's several vaults leave without backups, word for
// word as the platform writes it.
const fakeServiceWithoutBackupsStatement = "This service is set up without backups. The organisation has more than one verified backup vault, " +
	"and Ankra does not choose which one holds a database's backups. To have it backed up, name the backup vault in the setup: " +
	"'--backup-vault <name>' on the command line, backup_vault in the API. " +
	"'ankra backup vaults list' shows the vaults."

// fakeServiceBackedUp is the plan block of a service backed up into vault.
func fakeServiceBackedUp(vault string) map[string]any {
	return map[string]any{"state": "backed_up", "vault": vault, "statement": "This service is backed up into the backup vault " + vault + "."}
}

// fakeServiceNotBackedUp is the plan block of a service set up without
// backups because the organisation has several vaults and the setup names
// none.
func fakeServiceNotBackedUp() map[string]any {
	return map[string]any{"state": "not_backed_up", "reason": "many_vaults", "statement": fakeServiceWithoutBackupsStatement}
}

// The review shows what the plan says the install does about the service's
// backups, in both states, on the row a named vault has always had. An
// organisation with one vault and one with several used to read the same
// review, and only the first was backed up (ankra-otrxtu.8.15.1). A plan that
// says nothing is shown nothing: the CLI never fills in an answer the
// platform did not give.
func TestServicesSetupShowsWhatThePlanSaysAboutBackups(t *testing.T) {
	names := &serviceNames{clustersLoaded: true, clusters: map[string]string{}}
	for name, testCase := range map[string]struct {
		backups map[string]any
		vault   map[string]any
		want    []string
		absent  []string
	}{
		"backed up into the organisation's one vault": {
			backups: fakeServiceBackedUp("first"),
			want:    []string{"  Backup vault:   first\n", "                  This service is backed up into the backup vault first.\n"},
			absent:  []string{"not backed up", "Backups:"},
		},
		"backed up into the vault the setup named": {
			backups: fakeServiceBackedUp("first"),
			vault:   map[string]any{"name": "first", "statement": "This service is backed up into the backup vault first."},
			want:    []string{"  Backup vault:   first\n", "                  This service is backed up into the backup vault first.\n"},
			absent:  []string{"not backed up", "Backups:"},
		},
		"not backed up": {
			backups: fakeServiceNotBackedUp(),
			want: []string{"  Backup vault:   none, this service is not backed up\n",
				"                  " + fakeServiceWithoutBackupsStatement + "\n"},
			absent: []string{"Backups:"},
		},
		"a plan that says nothing": {
			absent: []string{"Backup vault:", "Backups:", "backed up"},
		},
		"a state this CLI does not know": {
			backups: map[string]any{"state": "paused", "statement": "Backups of this service are paused."},
			want:    []string{"  Backups:        paused\n", "                  Backups of this service are paused.\n"},
			absent:  []string{"Backup vault:"},
		},
	} {
		plan := fakeServiceReview(false)["plan"].(map[string]any)
		if testCase.backups != nil {
			plan["backups"] = testCase.backups
		}
		if testCase.vault != nil {
			plan["backup_vault"] = testCase.vault
		}
		var out bytes.Buffer
		printServiceReviewPlan(&out, names, "postgresql 1.1.0",
			client.ServiceReview{ID: fakeServiceReviewID, Digest: fakeServiceReviewDigest, Plan: plan})
		shown := out.String()
		for _, expected := range testCase.want {
			if !strings.Contains(shown, expected) {
				t.Errorf("%s: the review lacks %q:\n%s", name, expected, shown)
			}
		}
		for _, unexpected := range testCase.absent {
			if strings.Contains(shown, unexpected) {
				t.Errorf("%s: the review must not show %q:\n%s", name, unexpected, shown)
			}
		}
		if rows := strings.Count(shown, "Backup vault:"); rows > 1 {
			t.Errorf("%s: the backup vault row is shown %d times:\n%s", name, rows, shown)
		}
	}
}

// A setup that is not backed up says so before it asks, and -o json carries
// the plan's block exactly as the platform sent it.
func TestServicesSetupSaysBeforeConfirmingThatTheServiceIsNotBackedUp(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.planBackups = fakeServiceNotBackedUp()
	base := []string{"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID}

	stdout, stderr, runError := runServicesCommand(t, platform, "n\n", base...)
	if runError == nil {
		t.Fatal("a setup answered no must not be confirmed")
	}
	shown := stdout + stderr
	for _, expected := range []string{"Backup vault:   none, this service is not backed up", "name the backup vault in the setup: '--backup-vault <name>'"} {
		if !strings.Contains(shown, expected) {
			t.Errorf("the review shown before the question lacks %q:\n%s", expected, shown)
		}
	}
	if confirms := platform.requests(http.MethodPost, "/confirm"); len(confirms) != 0 {
		t.Fatalf("nothing may be confirmed after a no, got %d confirms", len(confirms))
	}

	stdout, _, runError = runServicesCommand(t, platform, "", append(append([]string{}, base...), "--review-only", "-o", "json")...)
	if runError != nil {
		t.Fatal(runError)
	}
	var review client.ServiceReview
	if decodeError := json.Unmarshal([]byte(stdout), &review); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	block, isBlock := review.Plan["backups"].(map[string]any)
	if !isBlock || block["state"] != "not_backed_up" || block["reason"] != "many_vaults" || block["statement"] != fakeServiceWithoutBackupsStatement {
		t.Errorf("-o json must carry the plan's backups block as sent, got %v", review.Plan["backups"])
	}
}

// A stored review reads the same: the row in both states, the block under
// -o json, and no row for a review that says nothing.
func TestServicesReviewsShowWhatTheReviewSaysAboutBackups(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "", "reviews", "get", fakeServiceReviewID)
	if runError != nil {
		t.Fatal(runError)
	}
	if strings.Contains(stdout, "Backup vault:") || strings.Contains(stdout, "backed up") {
		t.Errorf("a review that says nothing about backups must be shown nothing:\n%s", stdout)
	}

	platform.planBackups = fakeServiceBackedUp("first")
	stdout, _, runError = runServicesCommand(t, platform, "", "reviews", "get", fakeServiceReviewID)
	if runError != nil {
		t.Fatal(runError)
	}
	if !strings.Contains(stdout, "Backup vault:   first") || !strings.Contains(stdout, "This service is backed up into the backup vault first.") {
		t.Errorf("reviews get lacks the vault the service is backed up into:\n%s", stdout)
	}

	platform.planBackups = fakeServiceNotBackedUp()
	stdout, _, runError = runServicesCommand(t, platform, "", "reviews", "get", fakeServiceReviewID)
	if runError != nil {
		t.Fatal(runError)
	}
	if !strings.Contains(stdout, "Backup vault:   none, this service is not backed up") || !strings.Contains(stdout, fakeServiceWithoutBackupsStatement) {
		t.Errorf("reviews get lacks the not-backed-up row and its sentence:\n%s", stdout)
	}
	stdout, _, runError = runServicesCommand(t, platform, "", "reviews", "get", fakeServiceReviewID, "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	var summary client.ServiceReviewSummary
	if decodeError := json.Unmarshal([]byte(stdout), &summary); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	if summary.Backups == nil || summary.Backups.State != client.ServiceBackupsOff || summary.Backups.Reason != "many_vaults" ||
		summary.Backups.Statement != fakeServiceWithoutBackupsStatement || summary.Backups.Vault != "" {
		t.Errorf("-o json must carry the review's backups block, got %+v", summary.Backups)
	}
}

// A confirm the platform refuses because the plan is no longer what would be
// installed (a backup vault added or removed since the review moves it) says
// nothing was set up and how to get the current plan, as an expired review
// does.
func TestServicesSetupSaysWhatToDoWhenTheReviewedPlanMoved(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.planBackups = fakeServiceBackedUp("first")
	platform.confirmBackupsMoved = true
	_, _, runError := runServicesCommand(t, platform, "",
		"setup", "orders-db", "--package", "postgresql", "--cluster", fakeServiceClusterID, "--consumer", fakeServiceConsumerID, "--yes")
	if runError == nil {
		t.Fatal("a confirm the platform refused must fail")
	}
	for _, expected := range []string{fakeServiceReviewMovedDetail, "nothing was set up", "run 'ankra services setup orders-db ...' again"} {
		if !strings.Contains(runError.Error(), expected) {
			t.Errorf("the refusal lacks %q: %v", expected, runError)
		}
	}
}
