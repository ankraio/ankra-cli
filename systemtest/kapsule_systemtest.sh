#!/usr/bin/env bash
#
# Opt-in live Kapsule create + separate provider-created import lifecycle.
# Never scheduled: it creates billable resources and requires explicit input.

set -euo pipefail

ANKRA_BIN="${ANKRA_BIN:-ankra}"
CREATE_FILE="${KAPSULE_CREATE_FILE:-}"
CREATE_CREDENTIAL_ID="${KAPSULE_CREATE_CREDENTIAL_ID:-}"
IMPORT_CREDENTIAL_ID="${KAPSULE_IMPORT_CREDENTIAL_ID:-}"
IMPORT_PROVIDER_CLUSTER_ID="${KAPSULE_IMPORT_PROVIDER_CLUSTER_ID:-}"
IMPORT_NAME="${KAPSULE_IMPORT_NAME:-systest-kapsule-imported}"
POOL_SIZE="${KAPSULE_POOL_SIZE:-DEV1-M}"
UPGRADE_VERSION="${KAPSULE_UPGRADE_VERSION:-}"
TIMEOUT="${KAPSULE_TIMEOUT:-10m}"
DELETE_TIMEOUT_SECONDS="${KAPSULE_DELETE_TIMEOUT_SECONDS:-1800}"
POLL_INTERVAL_SECONDS="${KAPSULE_POLL_INTERVAL_SECONDS:-15}"
ORPHAN_VERIFY_SCRIPT="${KAPSULE_ORPHAN_VERIFY_SCRIPT:-}"

CREATED_CLUSTER_ID=""
CREATED_PROVIDER_CLUSTER_ID=""
IMPORTED_CLUSTER_ID=""

ank() { "$ANKRA_BIN" "$@"; }
die() { printf 'kapsule systemtest: %s\n' "$*" >&2; exit 2; }

cleanup() {
  if [ -n "$IMPORTED_CLUSTER_ID" ]; then
    ank cluster managed kapsule disconnect "$IMPORTED_CLUSTER_ID" --force --yes --timeout "$TIMEOUT" -o json >/dev/null 2>&1 || true
  fi
  if [ -n "$CREATED_CLUSTER_ID" ]; then
    ank cluster managed kapsule delete-provider-cluster "$CREATED_CLUSTER_ID" \
      --force --yes --retention-policy delete --timeout "$TIMEOUT" -o json >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

preflight() {
  [ "${ANKRA_SYSTEMTEST_CONFIRM:-}" = "yes" ] || die "set ANKRA_SYSTEMTEST_CONFIRM=yes to acknowledge billable resources"
  command -v "$ANKRA_BIN" >/dev/null 2>&1 || die "ankra binary not found"
  command -v jq >/dev/null 2>&1 || die "jq is required for structured assertions"
  [ -f "$CREATE_FILE" ] || die "KAPSULE_CREATE_FILE must reference a populated copy of kapsule-create.yaml.example"
  [ -n "$CREATE_CREDENTIAL_ID" ] || die "KAPSULE_CREATE_CREDENTIAL_ID is required for provider-absence polling"
  [ -n "$IMPORT_CREDENTIAL_ID" ] || die "KAPSULE_IMPORT_CREDENTIAL_ID is required"
  [ -n "$IMPORT_PROVIDER_CLUSTER_ID" ] || die "KAPSULE_IMPORT_PROVIDER_CLUSTER_ID is required"
  [ -x "$ORPHAN_VERIFY_SCRIPT" ] || die "KAPSULE_ORPHAN_VERIFY_SCRIPT must be an executable tagged/orphan verification script"
}

run_created_fixture() {
  local preflight_json create_json status_json pool_json operation_json
  preflight_json="$(ank cluster managed kapsule preflight --file "$CREATE_FILE" --timeout "$TIMEOUT" -o json)" || return 1
  jq -e '.can_proceed == true' <<<"$preflight_json" >/dev/null || die "create fixture preflight cannot proceed"

  create_json="$(ank cluster managed kapsule create --file "$CREATE_FILE" --timeout "$TIMEOUT" -o json)" || return 1
  CREATED_CLUSTER_ID="$(jq -r '.cluster_id' <<<"$create_json")"
  jq -e '.provenance == "created"' <<<"$create_json" >/dev/null || die "created fixture lost provenance"

  status_json="$(ank cluster managed kapsule status "$CREATED_CLUSTER_ID" --timeout "$TIMEOUT" -o json)" || return 1
  jq -e '.ownership == "created" and (.cni | type == "string")' <<<"$status_json" >/dev/null || die "created status missing ownership/CNI"
  CREATED_PROVIDER_CLUSTER_ID="$(jq -er '.provider_cluster_id | select(type == "string" and length > 0)' <<<"$status_json")"

  ank cluster managed kapsule pool catalog "$CREATED_CLUSTER_ID" --timeout "$TIMEOUT" -o json |
    jq -e '.incomplete == false and (.sizes | length > 0)' >/dev/null || die "pool catalog incomplete"

  pool_json="$(ank cluster managed kapsule pool add "$CREATED_CLUSTER_ID" \
    --name systest-extra --size "$POOL_SIZE" --count 1 --autohealing \
    --timeout "$TIMEOUT" -o json)" || return 1
  jq -e '.node_pool_name == "systest-extra"' <<<"$pool_json" >/dev/null || die "pool add response mismatch"

  ank cluster managed kapsule pool scale "$CREATED_CLUSTER_ID" systest-extra 2 --timeout "$TIMEOUT" -o json |
    jq -e '.count == 2' >/dev/null || die "pool scale response mismatch"
  ank cluster managed kapsule pool update "$CREATED_CLUSTER_ID" systest-extra \
    --autoscaling-enabled=true --autoscaling-min 1 --autoscaling-max 3 \
    --autohealing=true --upgrade-policy surge --timeout "$TIMEOUT" -o json |
    jq -e '.autoscaling_enabled == true and .autohealing == true' >/dev/null || die "pool update response mismatch"
  ank cluster managed kapsule pool delete "$CREATED_CLUSTER_ID" systest-extra --yes --timeout "$TIMEOUT" -o json |
    jq -e '.node_pool_name == "systest-extra"' >/dev/null || die "pool delete response mismatch"

  ank cluster managed kapsule upgrades "$CREATED_CLUSTER_ID" --timeout "$TIMEOUT" -o json |
    jq -e '.cluster_id == $id and (.upgrades | type == "array")' --arg id "$CREATED_CLUSTER_ID" >/dev/null ||
    die "upgrades response mismatch"
  if [ -n "$UPGRADE_VERSION" ]; then
    operation_json="$(ank cluster managed kapsule upgrade "$CREATED_CLUSTER_ID" "$UPGRADE_VERSION" --yes --timeout "$TIMEOUT" -o json)" || return 1
    jq -e '.operation_id | type == "string" and length > 0' <<<"$operation_json" >/dev/null || die "upgrade operation ID missing"
  fi
}

run_imported_fixture() {
  local discovery_json import_json retained_json
  [ "$IMPORT_PROVIDER_CLUSTER_ID" != "$CREATED_PROVIDER_CLUSTER_ID" ] ||
    die "import fixture must be a separate provider-created Kapsule cluster"
  discovery_json="$(ank cluster managed kapsule discover --credential-id "$IMPORT_CREDENTIAL_ID" --timeout "$TIMEOUT" -o json)" || return 1
  jq -e '.incomplete == false' <<<"$discovery_json" >/dev/null || die "discovery incomplete; import intentionally blocked"
  jq -e '.clusters[] | select(.provider_cluster_id == $id and .already_imported == false)' \
    --arg id "$IMPORT_PROVIDER_CLUSTER_ID" <<<"$discovery_json" >/dev/null ||
    die "separate import fixture not discoverable"

  import_json="$(ank cluster managed kapsule import --credential-id "$IMPORT_CREDENTIAL_ID" \
    --provider-cluster-id "$IMPORT_PROVIDER_CLUSTER_ID" --name "$IMPORT_NAME" \
    --timeout "$TIMEOUT" -o json)" || return 1
  IMPORTED_CLUSTER_ID="$(jq -r '.cluster_id' <<<"$import_json")"
  jq -e '.provenance == "imported"' <<<"$import_json" >/dev/null || die "imported fixture lost provenance"

  ank cluster managed kapsule status "$IMPORTED_CLUSTER_ID" --timeout "$TIMEOUT" -o json |
    jq -e '.ownership == "imported"' >/dev/null || die "imported status ownership mismatch"
  ank cluster managed kapsule disconnect "$IMPORTED_CLUSTER_ID" --yes --timeout "$TIMEOUT" -o json |
    jq -e '.success == true' >/dev/null || die "import disconnect failed"
  retained_json="$(ank cluster managed kapsule discover --credential-id "$IMPORT_CREDENTIAL_ID" --timeout "$TIMEOUT" -o json)" || return 1
  jq -e '.incomplete == false' <<<"$retained_json" >/dev/null ||
    die "post-disconnect discovery incomplete"
  jq -e '.clusters[] | select(.provider_cluster_id == $id)' \
    --arg id "$IMPORT_PROVIDER_CLUSTER_ID" <<<"$retained_json" >/dev/null ||
    die "disconnect unexpectedly removed the provider-created import fixture"
  IMPORTED_CLUSTER_ID=""
}

wait_for_operation() {
  local cluster_id="$1" operation_id="$2"
  local deadline status_json status
  deadline=$(( $(date +%s) + DELETE_TIMEOUT_SECONDS ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    status_json="$(ank cluster operations list "$operation_id" --cluster "$cluster_id" -o json 2>/dev/null)" || {
      sleep "$POLL_INTERVAL_SECONDS"
      continue
    }
    status="$(jq -r '.execution.status // .status // empty' <<<"$status_json")"
    case "$status" in
      success|succeeded) return 0 ;;
      failed|critical|error|cancelled|canceled|timeout)
        printf 'provider-delete operation %s failed with status %s\n' "$operation_id" "$status" >&2
        return 1
        ;;
    esac
    sleep "$POLL_INTERVAL_SECONDS"
  done
  printf 'timed out waiting for provider-delete operation %s\n' "$operation_id" >&2
  return 1
}

wait_for_provider_absence() {
  local deadline discovery_json
  deadline=$(( $(date +%s) + DELETE_TIMEOUT_SECONDS ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    discovery_json="$(ank cluster managed kapsule discover --credential-id "$CREATE_CREDENTIAL_ID" --timeout "$TIMEOUT" -o json)" || {
      sleep "$POLL_INTERVAL_SECONDS"
      continue
    }
    if jq -e '.incomplete == false' <<<"$discovery_json" >/dev/null &&
       ! jq -e '.clusters[] | select(.provider_cluster_id == $id)' \
         --arg id "$CREATED_PROVIDER_CLUSTER_ID" <<<"$discovery_json" >/dev/null; then
      return 0
    fi
    sleep "$POLL_INTERVAL_SECONDS"
  done
  printf 'timed out waiting for provider cluster %s to disappear\n' "$CREATED_PROVIDER_CLUSTER_ID" >&2
  return 1
}

verify_no_tagged_orphans() {
  "$ORPHAN_VERIFY_SCRIPT" "$CREATE_CREDENTIAL_ID" "$CREATED_PROVIDER_CLUSTER_ID"
}

main() {
  local delete_json operation_id
  preflight
  run_created_fixture
  run_imported_fixture

  delete_json="$(ank cluster managed kapsule delete-provider-cluster "$CREATED_CLUSTER_ID" \
    --yes --retention-policy delete --timeout "$TIMEOUT" -o json)"
  jq -e '.success == true' <<<"$delete_json" >/dev/null || die "created fixture provider deletion failed"
  operation_id="$(jq -er '.operation_id | select(type == "string" and length > 0)' <<<"$delete_json")" ||
    die "provider deletion response did not include an operation_id"

  # Keep CREATED_CLUSTER_ID populated through every verification. If operation
  # polling, provider absence, or orphan scanning fails, the EXIT trap retains
  # enough state to retry deletion rather than silently abandoning resources.
  wait_for_operation "$CREATED_CLUSTER_ID" "$operation_id"
  wait_for_provider_absence
  verify_no_tagged_orphans
  CREATED_CLUSTER_ID=""
  CREATED_PROVIDER_CLUSTER_ID=""
  printf 'Kapsule systemtest submitted all lifecycle operations successfully.\n'
  printf 'Provider deletion, provider absence, and tagged-orphan verification passed.\n'
}

main "$@"
