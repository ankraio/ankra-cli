package cmd

import (
	"errors"
	"strings"
	"testing"
)

// A rollout to several stacks asks first; a declined (or unanswerable)
// question writes nothing and exits with the cancellation code (ankra-sgqurr).
func TestStackProfilesRolloutAsksBeforeAMultiStackWrite(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	output, executeError := runStackProfilesCommand(t, mock, "n\n", "rollout", "hello-fleet", "--all")
	if !errors.Is(executeError, errCancelled) {
		t.Fatalf("a declined rollout must cancel, got %v\n%s", executeError, output)
	}
	if len(mock.upgraded) != 0 {
		t.Fatalf("a declined rollout wrote: %+v", mock.upgraded)
	}
	if !strings.Contains(output, "to 2 stacks on 2 clusters?") {
		t.Errorf("the question must name the scope:\n%s", output)
	}

	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all"); !errors.Is(executeError, errCancelled) {
		t.Fatalf("with no answer (no terminal) the rollout must cancel, got %v", executeError)
	}
	if len(mock.upgraded) != 0 {
		t.Fatalf("an unanswered rollout wrote: %+v", mock.upgraded)
	}

	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "y\n", "rollout", "hello-fleet", "--all"); executeError != nil {
		t.Fatalf("a confirmed rollout failed: %v", executeError)
	}
	if len(mock.upgraded) != 2 {
		t.Fatalf("a confirmed rollout should write both stacks: %+v", mock.upgraded)
	}
}

// One target is the operator's direct instruction and is not asked about.
func TestStackProfilesRolloutDoesNotAskForOneStack(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu"); executeError != nil {
		t.Fatalf("rollout failed: %v", executeError)
	}
	if len(mock.upgraded) != 1 {
		t.Fatalf("upgraded = %+v", mock.upgraded)
	}
}

// The dry run lists, per stack, the members the version would remove, and
// says so when it could not read them rather than reporting nothing.
func TestStackProfilesRolloutDryRunListsWhatEachStackWouldLose(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.extraLiveMembers = map[string][]string{"11111111-1111-1111-1111-111111111111": {"addon/team-exporter"}}
	mock.unlistableCluster = "22222222-2222-2222-2222-222222222222"
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--dry-run")
	if executeError != nil {
		t.Fatalf("dry run failed: %v", executeError)
	}
	if len(mock.upgraded) != 0 || len(mock.applied) != 0 {
		t.Fatalf("a dry run wrote: upgraded=%+v applied=%+v", mock.upgraded, mock.applied)
	}
	for _, want := range []string{"WOULD REMOVE", "addon/team-exporter", "unknown (listing the stacks of prod-us", "--confirm-removal"} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}

	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	output, executeError = runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--dry-run", "-o", "json")
	if executeError != nil {
		t.Fatalf("dry run failed: %v", executeError)
	}
	if !strings.Contains(output, `"removes": [`) || !strings.Contains(output, `"removes_unknown": "listing the stacks of prod-us`) {
		t.Errorf("structured dry run lacks the plan:\n%s", output)
	}
}

// The platform's refusal of a removal it cannot attribute to the profile is
// reported with the flag that would confirm it, and stops the loop; the
// confirmation is sent on the next attempt.
func TestStackProfilesRolloutInPlaceReportsAndConfirmsARefusedRemoval(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.removalRefusedCluster = "prod-eu"
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--yes")
	if executeError == nil {
		t.Fatal("a refused removal must fail the rollout")
	}
	if !strings.Contains(output, "refused") || !strings.Contains(output, "--confirm-removal prod-eu:addon/team-exporter") {
		t.Errorf("output = %s", output)
	}
	if len(mock.upgraded) != 1 {
		t.Fatalf("the loop must stop at the refusal: %+v", mock.upgraded)
	}

	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock.upgraded = nil
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu",
		"--confirm-removal", "addon/team-exporter"); executeError != nil {
		t.Fatalf("a confirmed removal failed: %v", executeError)
	}
	if len(mock.upgraded) != 1 || strings.Join(mock.upgraded[0].ConfirmedMemberRemovals, ",") != "addon/team-exporter" {
		t.Fatalf("the confirmation was not sent: %+v", mock.upgraded)
	}
}

// --via-apply cannot tell whose a member is, so it applies nothing that would
// remove a member nobody named, nor anything whose members it cannot read.
func TestStackProfilesRolloutViaApplyRefusesUnconfirmedRemovals(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	mock.extraLiveMembers = map[string][]string{"11111111-1111-1111-1111-111111111111": {"manifest/team-config"}}
	output, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--via-apply")
	if executeError == nil {
		t.Fatal("an unconfirmed removal must fail the via-apply rollout")
	}
	if len(mock.applied) != 0 {
		t.Fatalf("nothing may be applied over an unconfirmed removal: %+v", mock.applied)
	}
	if !strings.Contains(output, "--confirm-removal prod-eu:manifest/team-config") {
		t.Errorf("output = %s", output)
	}

	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--via-apply",
		"--confirm-removal", "manifest/team-config"); executeError != nil {
		t.Fatalf("a confirmed via-apply rollout failed: %v", executeError)
	}
	if len(mock.applied) != 1 {
		t.Fatalf("the confirmed rollout should apply: %+v", mock.applied)
	}

	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock.applied = nil
	mock.unlistableCluster = "11111111-1111-1111-1111-111111111111"
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--via-apply"); executeError == nil {
		t.Fatal("unreadable members must fail the via-apply rollout")
	}
	if len(mock.applied) != 0 {
		t.Fatalf("nothing may be applied when the members cannot be read: %+v", mock.applied)
	}
}

func TestStackProfilesRolloutRefusesAMalformedRemovalConfirmation(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	_, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--cluster", "prod-eu", "--confirm-removal", "team-exporter")
	if executeError == nil || !strings.Contains(executeError.Error(), "kind/name") {
		t.Fatalf("expected a usage error, got %v", executeError)
	}
	if len(mock.upgraded) != 0 {
		t.Fatalf("nothing should be sent: %+v", mock.upgraded)
	}
}

// A confirmation is for one stack: in a multi-stack rollout it must name the
// cluster, and it reaches only that cluster's request.
func TestStackProfilesRolloutScopesRemovalConfirmationsToTheirCluster(t *testing.T) {
	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	mock := newRolloutMock()
	_, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--yes",
		"--confirm-removal", "addon/team-exporter")
	if executeError == nil || !strings.Contains(executeError.Error(), "name the cluster the removal is for") {
		t.Fatalf("an unscoped confirmation in a multi-stack rollout must be a usage error, got %v", executeError)
	}
	if len(mock.upgraded) != 0 {
		t.Fatalf("nothing should be sent: %+v", mock.upgraded)
	}

	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--yes",
		"--confirm-removal", "staging:addon/team-exporter"); executeError == nil || !strings.Contains(executeError.Error(), "not one of this rollout's targets") {
		t.Fatalf("a confirmation for a cluster outside the rollout must be refused, got %v", executeError)
	}

	resetStackProfileCommandFlags(t, stackProfilesRolloutCmd)
	if _, executeError := runStackProfilesCommand(t, mock, "", "rollout", "hello-fleet", "--all", "--yes",
		"--confirm-removal", "prod-eu:addon/team-exporter"); executeError != nil {
		t.Fatalf("rollout failed: %v", executeError)
	}
	if len(mock.upgraded) != 2 {
		t.Fatalf("upgraded = %+v", mock.upgraded)
	}
	for index, clusterID := range mock.upgradeClusters {
		confirmed := strings.Join(mock.upgraded[index].ConfirmedMemberRemovals, ",")
		if clusterID == "11111111-1111-1111-1111-111111111111" && confirmed != "addon/team-exporter" {
			t.Errorf("prod-eu's request carries %q, want its confirmation", confirmed)
		}
		if clusterID == "22222222-2222-2222-2222-222222222222" && confirmed != "" {
			t.Errorf("prod-us's request carries %q, want none", confirmed)
		}
	}
}
