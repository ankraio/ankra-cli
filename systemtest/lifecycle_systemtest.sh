#!/usr/bin/env bash
#
# lifecycle_systemtest.sh
#
# Real, end-to-end system test for Ankra cloud clusters, driven entirely through
# the ankra CLI against a live platform (default: https://platform.ankra.dev).
#
# For each selected provider (hetzner, ovh, upcloud) it provisions a REAL cluster
# and exercises the full lifecycle, asserting the outcome at every step:
#
#   1. create (with external cloud provider + GitOps -> CCM/CSI/Traefik/cert-manager)
#   2. wait until the cluster is online and nodes are Ready
#   3. confirm the cloud-provider stack addons reach "up"
#   4. scale workers up (1 -> 3) and down (3 -> 1)
#   5. add a node group, then delete it
#   6. upgrade Kubernetes (k3s) to a newer version
#   7. resize the default node group to a bigger instance plan
#   8. deprovision and confirm the cluster record is removed (deleted_at)
#
# It tolerates the two real-world behaviours observed on UpCloud and the others:
#   - transient provisioning timeouts (slow bastion/server boot) -> reconcile retry
#   - the platform serialises writes (HTTP 409 while a reconcile runs) -> wait + retry
#
# On any failure (or Ctrl-C) it attempts to deprovision every cluster it created
# so the test never leaks paid cloud infrastructure.
#
# This is intentionally a thin, faithful wrapper around the same CLI commands an
# operator (or customer) runs by hand -- "as real as possible".
#
# Day-2 operations use the generic, provider-auto-detecting CLI verbs
# (`ankra cluster scale|node-group|upgrade|deprovision`); only `create` is
# provider-specific because the flags differ per provider.
#
# Usage:
#   export ANKRA_SYSTEMTEST_CONFIRM=yes        # required (acknowledges real cost)
#   export SSH_KEY_CREDENTIAL_ID=...           # required
#   export HETZNER_CREDENTIAL_ID=...           # required per selected provider
#   export GITOPS_REPOSITORY=org/repo          # optional (GitOps commit step)
#   ./systemtest/lifecycle_systemtest.sh                 # all three providers
#   ANKRA_SYSTEMTEST_PROVIDERS="upcloud" ./systemtest/lifecycle_systemtest.sh
#
# See systemtest/README.md for the full list of configuration variables.

set -u -o pipefail

# ---------------------------------------------------------------------------
# Configuration (override any of these via environment variables)
# ---------------------------------------------------------------------------

# CLI binary: default to the repo-local build, fall back to whatever is on PATH.
_repo_bin="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/bin/ankra"
ANKRA_BIN="${ANKRA_BIN:-$_repo_bin}"
if [ ! -x "$ANKRA_BIN" ]; then
  ANKRA_BIN="ankra"
fi

ANKRA_SYSTEMTEST_PROVIDERS="${ANKRA_SYSTEMTEST_PROVIDERS:-hetzner ovh upcloud}"
NAME_PREFIX="${NAME_PREFIX:-systest}"
RUN_ID="${RUN_ID:-$(date +%y%m%d%H%M%S)}"

# Credentials (IDs/names already stored in the Ankra org). Override per environment.
SSH_KEY_CREDENTIAL_ID="${SSH_KEY_CREDENTIAL_ID:-}"
HETZNER_CREDENTIAL_ID="${HETZNER_CREDENTIAL_ID:-}"
OVH_CREDENTIAL_ID="${OVH_CREDENTIAL_ID:-}"
UPCLOUD_CREDENTIAL_ID="${UPCLOUD_CREDENTIAL_ID:-}"

# GitOps target for the generated cloud-provider stack (required for stacks).
GITOPS_CREDENTIAL_NAME="${GITOPS_CREDENTIAL_NAME:-}"
GITOPS_REPOSITORY="${GITOPS_REPOSITORY:-}"
GITOPS_BRANCH="${GITOPS_BRANCH:-master}"

# Regions / zones / locations.
HETZNER_LOCATION="${HETZNER_LOCATION:-nbg1}"
OVH_REGION="${OVH_REGION:-GRA9}"
UPCLOUD_ZONE="${UPCLOUD_ZONE:-de-fra1}"

# Instance plans (create) and the bigger plan used by the resize step.
HETZNER_CP_TYPE="${HETZNER_CP_TYPE:-cpx32}"
HETZNER_WORKER_TYPE="${HETZNER_WORKER_TYPE:-cpx22}"
HETZNER_BASTION_TYPE="${HETZNER_BASTION_TYPE:-cpx22}"
HETZNER_BIGGER_TYPE="${HETZNER_BIGGER_TYPE:-cpx32}"

OVH_CP_FLAVOR="${OVH_CP_FLAVOR:-b2-15}"
OVH_WORKER_FLAVOR="${OVH_WORKER_FLAVOR:-b2-15}"
OVH_BIGGER_FLAVOR="${OVH_BIGGER_FLAVOR:-b2-30}"

UPCLOUD_CP_PLAN="${UPCLOUD_CP_PLAN:-2xCPU-4GB}"
UPCLOUD_WORKER_PLAN="${UPCLOUD_WORKER_PLAN:-2xCPU-4GB}"
UPCLOUD_BIGGER_PLAN="${UPCLOUD_BIGGER_PLAN:-4xCPU-8GB}"

# Timeouts / polling (seconds).
ONLINE_TIMEOUT="${ONLINE_TIMEOUT:-1500}"     # cluster create -> online
ADDONS_TIMEOUT="${ADDONS_TIMEOUT:-900}"      # addons -> up
DAYTWO_TIMEOUT="${DAYTWO_TIMEOUT:-900}"      # each day-2 op
DEPROVISION_TIMEOUT="${DEPROVISION_TIMEOUT:-1500}"
DEPROVISION_FORCE_TIMEOUT="${DEPROVISION_FORCE_TIMEOUT:-600}"  # bounded force fallback on stall
POLL_INTERVAL="${POLL_INTERVAL:-15}"
IDLE_TIMEOUT="${IDLE_TIMEOUT:-600}"          # wait for no running ops before a write

# k8s upgrade target. If empty, the highest version from `cluster k3s-versions`.
K8S_UPGRADE_TARGET="${K8S_UPGRADE_TARGET:-}"

# ---------------------------------------------------------------------------
# Internal state
# ---------------------------------------------------------------------------

CREATED_CLUSTERS=()        # "name=id" entries for cleanup
FAILURES=0
declare -a RESULTS         # human-readable per-step results

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------

log()     { printf '%s [systest] %s\n' "$(date +%H:%M:%S)" "$*"; }
section() { printf '\n========== %s ==========\n' "$*"; }
record()  { RESULTS+=("$1"); }

pass() { log "PASS: $1"; record "PASS  $1"; }
fail() { log "FAIL: $1"; record "FAIL  $1"; FAILURES=$((FAILURES + 1)); }

# Run the CLI, stripping the noisy login/env preamble lines.
ank() {
  "$ANKRA_BIN" "$@" 2>&1 | grep -vE "ANKRA_API_TOKEN env var is set|To use the env var instead|Using login token"
}

die() {
  log "FATAL: $*"
  exit 2
}

# ---------------------------------------------------------------------------
# Cleanup: deprovision anything we created, on any exit.
# ---------------------------------------------------------------------------

cleanup() {
  local entry name id
  for entry in "${CREATED_CLUSTERS[@]:-}"; do
    [ -z "$entry" ] && continue
    name="${entry%%=*}"
    id="${entry#*=}"
    if ank cluster list | grep -q "$name"; then
      log "cleanup: deprovisioning leftover cluster $name ($id)"
      # Best-effort: try a graceful deprovision, then force so we never leak
      # paid infrastructure on an aborted run.
      ank cluster deprovision "$id" >/dev/null 2>&1 || true
      ank cluster deprovision "$id" --force >/dev/null 2>&1 || true
    fi
  done
}
trap cleanup EXIT INT TERM

# ---------------------------------------------------------------------------
# Query helpers
# ---------------------------------------------------------------------------

select_cluster() { ank cluster select "$1" >/dev/null 2>&1 || true; }

# Echo the STATE column for a cluster name, or empty if not present.
cluster_state() {
  ank cluster list | awk -F'│' -v n="$1" '
    { gsub(/ /,"",$2); if ($2==n) { gsub(/ /,"",$6); print $6; exit } }'
}

cluster_in_list() { ank cluster list | awk -F'│' -v n="$1" '{gsub(/ /,"",$2); if ($2==n) f=1} END{exit f?0:1}'; }

cluster_version() {
  ank cluster list | awk -F'│' -v n="$1" '
    { gsub(/ /,"",$2); if ($2==n) { gsub(/ /,"",$3); print $3; exit } }'
}

ready_nodes() { select_cluster "$1"; ank cluster get nodes | grep -cE "Ready"; }

# Count of running cluster operations.
running_ops() { select_cluster "$1"; ank cluster operations list | grep -cE "running"; }

# Has any recent reconcile failed (transient timeout) that a retry could clear?
has_failed_reconcile() {
  select_cluster "$1"
  ank cluster operations list | grep -iE "Reconcile" | grep -qiE "failed|timed out"
}

nudge_reconcile() { select_cluster "$1"; ank cluster reconcile >/dev/null 2>&1 || true; }

# Count addons in state "up"; also report whether traefik+cert-manager are up.
addons_up_count() {
  select_cluster "$1"
  ank cluster addons list | awk -F'│' 'NF>=10 { gsub(/ /,"",$10); if ($10=="up") c++ } END{print c+0}'
}
addon_state() {
  select_cluster "$1"
  ank cluster addons list | awk -F'│' -v a="$2" 'NF>=10 { gsub(/ /,"",$2); if ($2==a) { gsub(/ /,"",$10); print $10; exit } }'
}

node_group_plan() {
  select_cluster "$1"
  ank cluster node-group list "$2" | awk -v g="$3" '$1==g { for(i=1;i<=NF;i++){ if($i ~ /^type=/){ sub(/type=/,"",$i); print $i; exit } } }'
}

node_group_present() {
  select_cluster "$1"
  ank cluster node-group list "$2" | awk -v g="$3" '$1==g{f=1} END{exit f?0:1}'
}

# ---------------------------------------------------------------------------
# Wait helpers
# ---------------------------------------------------------------------------

# Wait until cluster reaches a state, nudging reconcile when ops fail transiently.
wait_for_online() {
  local name="$1" timeout="$2" deadline state
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    state="$(cluster_state "$name")"
    case "$state" in
      online) return 0 ;;
      "") log "  ($name not yet in list)";;
      *) log "  $name state=$state";;
    esac
    if has_failed_reconcile "$name"; then
      log "  $name has a failed reconcile (likely transient) -> retrying"
      nudge_reconcile "$name"
    fi
    sleep "$POLL_INTERVAL"
  done
  return 1
}

wait_for_nodes() {
  local name="$1" want="$2" timeout="$3" deadline got
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    got="$(ready_nodes "$name")"
    log "  $name ready nodes=$got (want $want)"
    [ "$got" = "$want" ] && return 0
    if has_failed_reconcile "$name"; then
      log "  $name failed reconcile during node wait -> retrying"
      nudge_reconcile "$name"
    fi
    sleep "$POLL_INTERVAL"
  done
  return 1
}

wait_for_addons() {
  local name="$1" mincount="$2" timeout="$3" deadline up traefik cert
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    up="$(addons_up_count "$name")"
    traefik="$(addon_state "$name" traefik)"
    cert="$(addon_state "$name" cert-manager)"
    log "  $name addons up=$up traefik=$traefik cert-manager=$cert"
    if [ "${up:-0}" -ge "$mincount" ] && [ "$traefik" = "up" ] && [ "$cert" = "up" ]; then
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  return 1
}

# Wait until there are no running operations (write serialisation gate).
wait_idle() {
  local name="$1" timeout="$2" deadline n
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    n="$(running_ops "$name")"
    [ "${n:-0}" = "0" ] && return 0
    sleep "$POLL_INTERVAL"
  done
  return 1
}

wait_for_removed() {
  local name="$1" timeout="$2" deadline
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if ! cluster_in_list "$name"; then return 0; fi
    log "  $name still present (state=$(cluster_state "$name"))"
    if has_failed_reconcile "$name"; then
      log "  $name has a failed teardown reconcile -> retrying"
      nudge_reconcile "$name"
    fi
    sleep "$POLL_INTERVAL"
  done
  return 1
}

# Run a day-2 write that the platform may reject with 409 while a reconcile runs.
daytwo() {
  local desc="$1"; shift
  local name="$1"; shift
  local attempt out
  wait_idle "$name" "$IDLE_TIMEOUT" || log "  ($name still busy; attempting $desc anyway)"
  for attempt in $(seq 1 8); do
    out="$(ank cluster "$@" 2>&1)"
    if printf '%s' "$out" | grep -qiE "operations in progress|409"; then
      log "  $desc rejected (ops in progress), retry $attempt"
      sleep 20
      continue
    fi
    printf '%s\n' "$out" | tail -2
    return 0
  done
  return 1
}

pick_upgrade_target() {
  local name="$1" current target
  if [ -n "$K8S_UPGRADE_TARGET" ]; then echo "$K8S_UPGRADE_TARGET"; return; fi
  current="$(cluster_version "$name")"
  target="$(ank cluster k3s-versions | awk '/Available versions:/{f=1;next} f&&NF{print $1; exit}')"
  echo "$target"
}

# ---------------------------------------------------------------------------
# Per-provider create
# ---------------------------------------------------------------------------

# UpCloud uses a shared SDN address space, so each cluster needs a unique
# /16 to avoid "network overlaps with an existing private network" (409).
# Hetzner and OVH networks are isolated per cluster, so their defaults are safe.
gen_upcloud_cidr() { echo "10.$(( (RANDOM % 200) + 30 )).0.0/16"; }

create_cluster() {
  local provider="$1" name="$2"
  local gitops_args=()
  if [ -n "$GITOPS_CREDENTIAL_NAME" ] && [ -n "$GITOPS_REPOSITORY" ]; then
    gitops_args=(--gitops-credential-name "$GITOPS_CREDENTIAL_NAME" --gitops-repository "$GITOPS_REPOSITORY" --gitops-branch "$GITOPS_BRANCH")
  fi
  case "$provider" in
    hetzner)
      ank cluster hetzner create --name "$name" --credential-id "$HETZNER_CREDENTIAL_ID" \
        --ssh-key-credential-id "$SSH_KEY_CREDENTIAL_ID" --location "$HETZNER_LOCATION" \
        --bastion-server-type "$HETZNER_BASTION_TYPE" \
        --control-plane-server-type "$HETZNER_CP_TYPE" --control-plane-count 1 \
        --worker-server-type "$HETZNER_WORKER_TYPE" --worker-count 1 \
        --external-cloud-provider "${gitops_args[@]}"
      ;;
    ovh)
      ank cluster ovh create --name "$name" --credential-id "$OVH_CREDENTIAL_ID" \
        --ssh-key-credential-id "$SSH_KEY_CREDENTIAL_ID" --region "$OVH_REGION" \
        --control-plane-flavor-id "$OVH_CP_FLAVOR" --control-plane-count 1 \
        --worker-flavor-id "$OVH_WORKER_FLAVOR" --worker-count 1 \
        --external-cloud-provider "${gitops_args[@]}"
      ;;
    upcloud)
      ank cluster upcloud create --name "$name" --credential-id "$UPCLOUD_CREDENTIAL_ID" \
        --ssh-key-credential-id "$SSH_KEY_CREDENTIAL_ID" --zone "$UPCLOUD_ZONE" \
        --network-ip-range "$(gen_upcloud_cidr)" \
        --control-plane-plan "$UPCLOUD_CP_PLAN" --control-plane-count 1 \
        --worker-plan "$UPCLOUD_WORKER_PLAN" --worker-count 1 \
        --external-cloud-provider "${gitops_args[@]}"
      ;;
    *) die "unknown provider $provider" ;;
  esac
}

bigger_plan() {
  case "$1" in
    hetzner) echo "$HETZNER_BIGGER_TYPE" ;;
    ovh)     echo "$OVH_BIGGER_FLAVOR" ;;
    upcloud) echo "$UPCLOUD_BIGGER_PLAN" ;;
  esac
}

ng_instance_type() {
  case "$1" in
    hetzner) echo "$HETZNER_WORKER_TYPE" ;;
    ovh)     echo "$OVH_WORKER_FLAVOR" ;;
    upcloud) echo "$UPCLOUD_WORKER_PLAN" ;;
  esac
}

# ---------------------------------------------------------------------------
# Full lifecycle for one provider
# ---------------------------------------------------------------------------

run_provider() {
  local provider="$1"
  local name="${NAME_PREFIX}-${provider}-${RUN_ID}"
  local id target plan out

  section "$provider :: $name"

  # 1. Create (capture the printed "Cluster ID: <uuid>")
  log "creating $name ..."
  out="$(create_cluster "$provider" "$name")"
  printf '%s\n' "$out"
  id="$(printf '%s' "$out" | awk -F'Cluster ID:' '/Cluster ID:/{gsub(/[ \t]/,"",$2); print $2; exit}')"
  if [ -z "$id" ]; then fail "$provider create (could not resolve cluster id)"; return; fi
  CREATED_CLUSTERS+=("$name=$id")
  pass "$provider create submitted (id=$id)"

  # 2. Online + nodes
  if wait_for_online "$name" "$ONLINE_TIMEOUT"; then pass "$provider online"; else fail "$provider did not reach online"; return; fi
  if wait_for_nodes "$name" 2 "$DAYTWO_TIMEOUT"; then pass "$provider nodes Ready (cp+worker)"; else fail "$provider nodes not Ready"; fi

  # 3. Stacks
  if wait_for_addons "$name" 4 "$ADDONS_TIMEOUT"; then pass "$provider stacks up (CCM/CSI/Traefik/cert-manager)"; else fail "$provider stacks did not reach up"; fi

  # 4. Scale up / down (generic, provider-auto-detecting verbs)
  if daytwo "scale up" "$name" scale "$id" 3 && wait_for_nodes "$name" 4 "$DAYTWO_TIMEOUT"; then
    pass "$provider scale up 1->3"; else fail "$provider scale up"; fi
  if daytwo "scale down" "$name" scale "$id" 1 && wait_for_nodes "$name" 2 "$DAYTWO_TIMEOUT"; then
    pass "$provider scale down 3->1"; else fail "$provider scale down"; fi

  # 5. Node group add / delete
  if daytwo "ng add" "$name" node-group add "$id" --name pool-b --instance-type "$(ng_instance_type "$provider")" --count 2 \
     && wait_for_nodes "$name" 4 "$DAYTWO_TIMEOUT" && node_group_present "$name" "$id" pool-b; then
    pass "$provider node-group add"; else fail "$provider node-group add"; fi
  if daytwo "ng delete" "$name" node-group delete "$id" pool-b \
     && wait_for_nodes "$name" 2 "$DAYTWO_TIMEOUT"; then
    pass "$provider node-group delete"; else fail "$provider node-group delete"; fi

  # 6. K8s upgrade
  target="$(pick_upgrade_target "$name")"
  local want_ver="${target#v}"; want_ver="${want_ver%%+*}"
  if [ -n "$target" ] && daytwo "k8s upgrade" "$name" upgrade "$id" "$target"; then
    if wait_until_version "$name" "$want_ver" "$DAYTWO_TIMEOUT"; then pass "$provider k8s upgrade -> $target"; else fail "$provider k8s upgrade did not reach $target"; fi
  else fail "$provider k8s upgrade (submit)"; fi

  # 7. Instance resize
  plan="$(bigger_plan "$provider")"
  if daytwo "resize" "$name" node-group upgrade "$id" default "$plan" \
     && wait_until_ng_plan "$name" "$id" default "$plan" "$DAYTWO_TIMEOUT"; then
    pass "$provider instance resize default -> $plan"; else fail "$provider instance resize"; fi

  # 8. Deprovision -> removed (with a bounded force-deprovision fallback on stall)
  log "deprovisioning $name ..."
  ank cluster deprovision "$id" | tail -2
  if wait_for_removed "$name" "$DEPROVISION_TIMEOUT"; then
    pass "$provider deprovision -> deleted_at"
  else
    log "  $name deprovision stalled after ${DEPROVISION_TIMEOUT}s; attempting bounded force-deprovision fallback"
    ank cluster deprovision "$id" --force | tail -2 || true
    if wait_for_removed "$name" "$DEPROVISION_FORCE_TIMEOUT"; then
      pass "$provider deprovision -> deleted_at (after force fallback)"
    else
      fail "$provider deprovision did not complete (even after force fallback)"
    fi
  fi
}

wait_until_version() {
  local name="$1" want="$2" timeout="$3" deadline cur
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    cur="$(cluster_version "$name")"
    log "  $name version=$cur (want ~$want)"
    case "$cur" in *"$want"*) return 0;; esac
    sleep "$POLL_INTERVAL"
  done
  return 1
}

wait_until_ng_plan() {
  local name="$1" id="$2" group="$3" want="$4" timeout="$5" deadline cur
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    cur="$(node_group_plan "$name" "$id" "$group")"
    log "  $name $group plan=$cur (want $want)"
    [ "$cur" = "$want" ] && return 0
    sleep "$POLL_INTERVAL"
  done
  return 1
}

# ---------------------------------------------------------------------------
# Preflight + main
# ---------------------------------------------------------------------------

# Cost gate: this test provisions REAL, billable cloud infrastructure across up
# to three providers and only tears it down at the end (or on cleanup). Require
# an explicit opt-in so it is never run by accident in CI or by a stray invocation.
confirm_cost() {
  cat >&2 <<'WARNING'

  ############################################################################
  #  WARNING: REAL, BILLABLE CLOUD INFRASTRUCTURE                            #
  #                                                                          #
  #  This system test provisions actual servers, load balancers, networks   #
  #  and volumes on Hetzner / OVH / UpCloud and runs a multi-step lifecycle  #
  #  (create, scale, node-groups, k8s upgrade, resize, deprovision).         #
  #  A full three-provider run can take ~2 hours and WILL incur charges.     #
  #  Clusters are deprovisioned at the end and on abort, but a crash of this #
  #  script can still leave paid resources running -- verify afterwards.     #
  #                                                                          #
  #  Set ANKRA_SYSTEMTEST_CONFIRM=yes to acknowledge and proceed.           #
  ############################################################################

WARNING
  if [ "${ANKRA_SYSTEMTEST_CONFIRM:-}" != "yes" ]; then
    die "refusing to run without confirmation: export ANKRA_SYSTEMTEST_CONFIRM=yes to proceed"
  fi
}

preflight() {
  command -v "$ANKRA_BIN" >/dev/null 2>&1 || [ -x "$ANKRA_BIN" ] || die "ankra binary not found ($ANKRA_BIN)"
  log "using ankra: $ANKRA_BIN ($($ANKRA_BIN --version 2>/dev/null | head -1))"
  confirm_cost
  [ -n "$SSH_KEY_CREDENTIAL_ID" ] || die "SSH_KEY_CREDENTIAL_ID is required"
  if [ -z "$GITOPS_CREDENTIAL_NAME" ] || [ -z "$GITOPS_REPOSITORY" ]; then
    log "WARNING: GITOPS_CREDENTIAL_NAME/GITOPS_REPOSITORY not set -> the GitOps commit step is skipped (stacks still install)"
  fi
  local p
  for p in $ANKRA_SYSTEMTEST_PROVIDERS; do
    case "$p" in
      hetzner) [ -n "$HETZNER_CREDENTIAL_ID" ] || die "HETZNER_CREDENTIAL_ID required for hetzner" ;;
      ovh)     [ -n "$OVH_CREDENTIAL_ID" ] || die "OVH_CREDENTIAL_ID required for ovh" ;;
      upcloud) [ -n "$UPCLOUD_CREDENTIAL_ID" ] || die "UPCLOUD_CREDENTIAL_ID required for upcloud" ;;
      *) die "unknown provider in ANKRA_SYSTEMTEST_PROVIDERS: $p" ;;
    esac
  done
}

main() {
  preflight
  section "Ankra cloud lifecycle system test (run $RUN_ID)"
  log "providers: $ANKRA_SYSTEMTEST_PROVIDERS"
  local p
  for p in $ANKRA_SYSTEMTEST_PROVIDERS; do
    run_provider "$p"
  done

  section "RESULTS"
  printf '%s\n' "${RESULTS[@]}"
  section "SUMMARY"
  log "$(( ${#RESULTS[@]} - FAILURES )) passed, $FAILURES failed"
  [ "$FAILURES" -eq 0 ]
}

main "$@"
