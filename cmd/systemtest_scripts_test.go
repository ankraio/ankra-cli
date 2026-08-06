package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestScalewaySystemtestParsesJSONBeforeCleanupRegistration(t *testing.T) {
	script := readSystemtestScript(t, "lifecycle_systemtest.sh")
	parse := strings.Index(script, `jq -er '.cluster_id | select(type == "string" and length > 0)'`)
	register := strings.Index(script, `register_cluster "$name" "$id"`)
	firstLaterStep := strings.Index(script, `pass "$label create submitted`)
	if parse < 0 || register < 0 || firstLaterStep < 0 {
		t.Fatalf("expected jq parse, cleanup registration, and later lifecycle step")
	}
	if parse >= register || register >= firstLaterStep {
		t.Fatalf("unsafe order: parse=%d register=%d later=%d", parse, register, firstLaterStep)
	}
}

func TestScalewaySystemtestRequiresTaggedOrphanVerification(t *testing.T) {
	script := readSystemtestScript(t, "lifecycle_systemtest.sh")
	required := []string{
		`SCALEWAY_ORPHAN_VERIFY_SCRIPT="${SCALEWAY_ORPHAN_VERIFY_SCRIPT:-}"`,
		`[ -x "$SCALEWAY_ORPHAN_VERIFY_SCRIPT" ]`,
		`"$SCALEWAY_ORPHAN_VERIFY_SCRIPT" "$SCALEWAY_CREDENTIAL_ID" "$cluster_id"`,
		`pass "$label provider tagged-orphan verification"`,
		`fail "$label provider tagged-orphan verification"`,
	}
	for _, value := range required {
		if !strings.Contains(script, value) {
			t.Fatalf("missing Scaleway orphan-verification contract %q", value)
		}
	}
	deprovisioned := strings.LastIndex(script, `pass "$label deprovision -> deleted_at`)
	verified := strings.LastIndex(script, `verify_scaleway_orphans "$id"`)
	if deprovisioned < 0 || verified < deprovisioned {
		t.Fatalf("orphan verification must follow provider deprovision: deprovision=%d verify=%d",
			deprovisioned, verified)
	}
}

func TestKapsuleSystemtestVerifiesDeletionBeforeClearingCleanupState(t *testing.T) {
	script := readSystemtestScript(t, "kapsule_systemtest.sh")
	required := []string{
		"set -euo pipefail",
		`trap cleanup EXIT INT TERM`,
		`wait_for_operation "$CREATED_CLUSTER_ID" "$operation_id"`,
		"wait_for_provider_absence",
		"verify_no_tagged_orphans",
		`"$ORPHAN_VERIFY_SCRIPT" "$CREATE_CREDENTIAL_ID" "$CREATED_PROVIDER_CLUSTER_ID"`,
		`[ "$IMPORT_PROVIDER_CLUSTER_ID" != "$CREATED_PROVIDER_CLUSTER_ID" ]`,
		`die "disconnect unexpectedly removed the provider-created import fixture"`,
	}
	for _, value := range required {
		if !strings.Contains(script, value) {
			t.Fatalf("missing deletion safety contract %q", value)
		}
	}
	operation := strings.LastIndex(script, `wait_for_operation "$CREATED_CLUSTER_ID" "$operation_id"`)
	absence := strings.LastIndex(script, "wait_for_provider_absence")
	orphans := strings.LastIndex(script, "verify_no_tagged_orphans")
	clearID := strings.LastIndex(script, `CREATED_CLUSTER_ID=""`)
	if operation >= absence || absence >= orphans || orphans >= clearID {
		t.Fatalf("unsafe deletion verification order: operation=%d absence=%d orphans=%d clear=%d",
			operation, absence, orphans, clearID)
	}
}

func TestSystemtestWorkflowKeepsScalewayManualAndProtected(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/systemtest.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	required := []string{
		"run_scaleway_instances:",
		"run_scaleway_kapsule:",
		"default: false",
		"environment: scaleway-systemtest",
		"github.event_name == 'workflow_dispatch' && inputs.run_scaleway_instances",
		"github.event_name == 'workflow_dispatch' && inputs.run_scaleway_kapsule",
		"ANKRA_SYSTEMTEST_PROVIDERS: 'scaleway'",
		"KAPSULE_IMPORT_PROVIDER_CLUSTER_ID: ${{ inputs.kapsule_import_provider_cluster_id }}",
		"SYSTEMTEST_SCALEWAY_ORPHAN_VERIFY_SCRIPT",
	}
	for _, value := range required {
		if !strings.Contains(workflow, value) {
			t.Fatalf("missing protected manual workflow contract %q", value)
		}
	}
	if strings.Contains(workflow, `github.event.inputs.providers || 'hetzner ovh upcloud scaleway'`) {
		t.Fatal("Scaleway must never be added to scheduled provider defaults")
	}
}

func readSystemtestScript(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../systemtest/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
