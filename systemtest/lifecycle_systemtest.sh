#!/usr/bin/env bash
#
# lifecycle_systemtest.sh
#
# Real, end-to-end system test for Ankra cloud clusters, driven entirely through
# the ankra CLI against a live platform (default: https://platform.ankra.dev).
#
# For each selected provider (hetzner, ovh, upcloud, digitalocean) and each
# selected Kubernetes distribution (k3s, kubeadm) it provisions a REAL cluster
# and exercises the full lifecycle, asserting the outcome at every step:
#
#   1. create (with external cloud provider + GitOps -> CCM/CSI/Traefik/cert-manager)
#   2. wait until the cluster is online and nodes are Ready
#   3. confirm the cloud-provider stack addons reach "up"
#   4. scale workers up (1 -> 3) and down (3 -> 1)
#   5. add a node group, then delete it
#   6. upgrade Kubernetes (k3s or kubeadm) to a newer version
#   7. resize the default node group to a bigger instance plan
#   8. deprovision and confirm the cluster record is removed (deleted_at)
#
# Distributions run as an independent axis: with
# ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm" every selected provider gets one
# cluster per distribution (e.g. systest-digitalocean-k3s-... and
# systest-digitalocean-kubeadm-...), so a single run matrix-tests both.
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
# By default the selected providers run CONCURRENTLY (ANKRA_SYSTEMTEST_PARALLEL=1)
# so a full three-provider run finishes in roughly the time of the slowest single
# provider instead of the sum of all three. Each parallel worker uses an isolated
# copy of the ankra CLI config so concurrent `cluster select` calls do not clobber
# each other. Set ANKRA_SYSTEMTEST_PARALLEL=0 to run providers one at a time.
#
# Usage:
#   export ANKRA_SYSTEMTEST_CONFIRM=yes        # required (acknowledges real cost)
#   export SSH_KEY_CREDENTIAL_ID=...           # required
#   export HETZNER_CREDENTIAL_ID=...           # required per selected provider
#   export GITOPS_REPOSITORY=org/repo          # optional (GitOps commit step)
#   ./systemtest/lifecycle_systemtest.sh                 # all three, in parallel
#   ANKRA_SYSTEMTEST_PARALLEL=0 ./systemtest/lifecycle_systemtest.sh   # sequential
#   ANKRA_SYSTEMTEST_PROVIDERS="upcloud" ./systemtest/lifecycle_systemtest.sh
#   # DigitalOcean, both distributions:
#   ANKRA_SYSTEMTEST_PROVIDERS="digitalocean" \
#     ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm" ./systemtest/lifecycle_systemtest.sh
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

# Kubernetes distributions to exercise per provider. Each (provider,
# distribution) pair becomes its own cluster with the distribution in its name,
# so a single run can matrix-test both k3s and kubeadm side by side, e.g.
#   ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm"
ANKRA_SYSTEMTEST_DISTRIBUTIONS="${ANKRA_SYSTEMTEST_DISTRIBUTIONS:-k3s}"

NAME_PREFIX="${NAME_PREFIX:-systest}"
RUN_ID="${RUN_ID:-$(date +%y%m%d%H%M%S)}"

# Async execution: run the selected providers concurrently (1) or one at a time
# (0). Each parallel worker gets an isolated copy of the ankra CLI config so that
# per-worker `cluster select` writes never clobber a sibling worker's selection.
ANKRA_SYSTEMTEST_PARALLEL="${ANKRA_SYSTEMTEST_PARALLEL:-1}"

# Base ankra CLI config (holds the login token + selected org). Parallel workers
# copy this so they share auth but isolate the per-cluster selection.
BASE_CONFIG="${ANKRA_CONFIG_FILE:-$HOME/.ankra.yaml}"

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

DIGITALOCEAN_CREDENTIAL_ID="${DIGITALOCEAN_CREDENTIAL_ID:-}"
DIGITALOCEAN_REGION="${DIGITALOCEAN_REGION:-nyc3}"
DIGITALOCEAN_BASTION_SIZE="${DIGITALOCEAN_BASTION_SIZE:-s-1vcpu-1gb}"
DIGITALOCEAN_CP_SIZE="${DIGITALOCEAN_CP_SIZE:-s-2vcpu-4gb}"
DIGITALOCEAN_WORKER_SIZE="${DIGITALOCEAN_WORKER_SIZE:-s-2vcpu-4gb}"
DIGITALOCEAN_BIGGER_SIZE="${DIGITALOCEAN_BIGGER_SIZE:-s-4vcpu-8gb}"

# Timeouts / polling (seconds).
ONLINE_TIMEOUT="${ONLINE_TIMEOUT:-1500}"     # cluster create -> online
ADDONS_TIMEOUT="${ADDONS_TIMEOUT:-900}"      # addons -> up
DAYTWO_TIMEOUT="${DAYTWO_TIMEOUT:-900}"      # each day-2 op
DEPROVISION_TIMEOUT="${DEPROVISION_TIMEOUT:-1500}"
DEPROVISION_FORCE_TIMEOUT="${DEPROVISION_FORCE_TIMEOUT:-600}"  # bounded force fallback on stall
POLL_INTERVAL="${POLL_INTERVAL:-15}"
IDLE_TIMEOUT="${IDLE_TIMEOUT:-600}"          # wait for no running ops before a write

# k8s upgrade target. If empty, the highest version from the distribution's
# version listing (`cluster k3s-versions` or `cluster kubeadm-versions`).
K8S_UPGRADE_TARGET="${K8S_UPGRADE_TARGET:-}"

# etcd topology for kubeadm clusters (stacked | external). k3s ignores it.
ETCD_TOPOLOGY="${ETCD_TOPOLOGY:-stacked}"

# ---------------------------------------------------------------------------
# Internal state
# ---------------------------------------------------------------------------

CREATED_CLUSTERS=()        # "name=id" entries for cleanup (sequential mode)
FAILURES=0
declare -a RESULTS         # human-readable per-step results (sequential mode)

# Shared run artifacts (populated in main). In parallel mode each worker writes
# its results to its own file and appends created clusters to a shared file so
# the EXIT/INT/TERM cleanup can tear down everything even on abort.
WORKDIR=""
CREATED_FILE=""
WORKER_PIDS=()

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------

log()     { printf '%s [systest] %s\n' "$(date +%H:%M:%S)" "$*"; }
section() { printf '\n========== %s ==========\n' "$*"; }

# Record a result line. A worker (parallel or sequential) sets RESULT_FILE so its
# results survive the subshell; otherwise fall back to the in-memory array.
record() {
  if [ -n "${RESULT_FILE:-}" ]; then
    printf '%s\n' "$1" >> "$RESULT_FILE"
  else
    RESULTS+=("$1")
  fi
}

pass() { log "PASS: $1"; record "PASS  $1"; }
fail() { log "FAIL: $1"; record "FAIL  $1"; FAILURES=$((FAILURES + 1)); }

# Remember a created cluster for cleanup. Append to the shared file (so the
# main-shell trap sees clusters created inside parallel worker subshells) and
# also keep the in-memory array for sequential runs.
register_cluster() {
  CREATED_CLUSTERS+=("$1=$2")
  if [ -n "${CREATED_FILE:-}" ]; then
    printf '%s=%s\n' "$1" "$2" >> "$CREATED_FILE"
  fi
}

# Run the CLI, stripping the noisy login/env preamble lines. A parallel worker
# sets ANK_CONFIG to an isolated config copy; the CLI keys both the saved
# credentials and the active-cluster selection off the --config file, so workers
# never clobber each other's `cluster select`.
ank() {
  if [ -n "${ANK_CONFIG:-}" ]; then
    "$ANKRA_BIN" --config "$ANK_CONFIG" "$@" 2>&1 | grep -vE "ANKRA_API_TOKEN env var is set|To use the env var instead|Using login token"
  else
    "$ANKRA_BIN" "$@" 2>&1 | grep -vE "ANKRA_API_TOKEN env var is set|To use the env var instead|Using login token"
  fi
}

die() {
  log "FATAL: $*"
  exit 2
}

# ---------------------------------------------------------------------------
# Cleanup: deprovision anything we created, on any exit.
# ---------------------------------------------------------------------------

cleanup() {
  # On a signalled abort, stop the parallel workers before we tear down so they
  # do not keep issuing writes against clusters we are deleting.
  local pid
  for pid in "${WORKER_PIDS[@]:-}"; do
    [ -z "$pid" ] && continue
    kill "$pid" >/dev/null 2>&1 || true
  done

  # Cleanup runs in the main shell with the default config (auth intact); the
  # deprovision call takes the id explicitly so no selection is required.
  local entry name id
  local -a entries=()
  if [ -n "${CREATED_FILE:-}" ] && [ -f "$CREATED_FILE" ]; then
    while IFS= read -r entry; do entries+=("$entry"); done < "$CREATED_FILE"
  else
    entries=("${CREATED_CLUSTERS[@]:-}")
  fi
  for entry in "${entries[@]:-}"; do
    [ -z "$entry" ] && continue
    name="${entry%%=*}"
    id="${entry#*=}"
    if ank cluster list | grep -q "$name"; then
      log "cleanup: deprovisioning leftover cluster $name ($id)"
      # Best-effort: try a graceful deprovision, then force so we never leak
      # paid infrastructure on an aborted run.
      ank cluster deprovision "$id" --yes >/dev/null 2>&1 || true
      ank cluster deprovision "$id" --force --yes >/dev/null 2>&1 || true
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
  local name="$1" distribution="$2" versions_cmd="k3s-versions"
  if [ -n "$K8S_UPGRADE_TARGET" ]; then echo "$K8S_UPGRADE_TARGET"; return; fi
  if [ "$distribution" = "kubeadm" ]; then versions_cmd="kubeadm-versions"; fi
  ank cluster "$versions_cmd" | awk '/Available versions:/{f=1;next} f&&NF{print $1; exit}'
}

# ---------------------------------------------------------------------------
# Per-provider create
# ---------------------------------------------------------------------------

# UpCloud uses a shared SDN address space and DigitalOcean rejects overlapping
# VPC ranges account-wide, so each cluster needs a unique /16. $RANDOM alone is
# unsafe for this: parallel workers fork from the same shell and would draw the
# same value, so combine a per-run random base (drawn once, before forking)
# with the worker's index. Hetzner and OVH networks are isolated per cluster,
# so their defaults are safe.
CIDR_BASE=$(( RANDOM % 200 ))
worker_cidr() { echo "10.$(( ((CIDR_BASE + ${WORKER_INDEX:-0} * 11) % 200) + 30 )).0.0/16"; }

create_cluster() {
  local provider="$1" name="$2" distribution="$3"
  local gitops_args=()
  if [ -n "$GITOPS_CREDENTIAL_NAME" ] && [ -n "$GITOPS_REPOSITORY" ]; then
    gitops_args=(--gitops-credential-name "$GITOPS_CREDENTIAL_NAME" --gitops-repository "$GITOPS_REPOSITORY" --gitops-branch "$GITOPS_BRANCH")
  fi
  # Distribution selection applies to every self-managed provider; the etcd
  # topology flag only matters for kubeadm (k3s ignores it).
  local dist_args=(--distribution "$distribution")
  if [ "$distribution" = "kubeadm" ]; then
    dist_args+=(--etcd-topology "$ETCD_TOPOLOGY")
  fi
  case "$provider" in
    hetzner)
      ank cluster hetzner create --name "$name" --credential-id "$HETZNER_CREDENTIAL_ID" \
        --ssh-key-credential-id "$SSH_KEY_CREDENTIAL_ID" --location "$HETZNER_LOCATION" \
        --bastion-server-type "$HETZNER_BASTION_TYPE" \
        --control-plane-server-type "$HETZNER_CP_TYPE" --control-plane-count 1 \
        --worker-server-type "$HETZNER_WORKER_TYPE" --worker-count 1 \
        --external-cloud-provider "${dist_args[@]}" "${gitops_args[@]}"
      ;;
    ovh)
      ank cluster ovh create --name "$name" --credential-id "$OVH_CREDENTIAL_ID" \
        --ssh-key-credential-id "$SSH_KEY_CREDENTIAL_ID" --region "$OVH_REGION" \
        --control-plane-flavor-id "$OVH_CP_FLAVOR" --control-plane-count 1 \
        --worker-flavor-id "$OVH_WORKER_FLAVOR" --worker-count 1 \
        --external-cloud-provider "${dist_args[@]}" "${gitops_args[@]}"
      ;;
    upcloud)
      ank cluster upcloud create --name "$name" --credential-id "$UPCLOUD_CREDENTIAL_ID" \
        --ssh-key-credential-id "$SSH_KEY_CREDENTIAL_ID" --zone "$UPCLOUD_ZONE" \
        --network-ip-range "$(worker_cidr)" \
        --control-plane-plan "$UPCLOUD_CP_PLAN" --control-plane-count 1 \
        --worker-plan "$UPCLOUD_WORKER_PLAN" --worker-count 1 \
        --external-cloud-provider "${dist_args[@]}" "${gitops_args[@]}"
      ;;
    digitalocean)
      ank cluster digitalocean create --name "$name" --credential-id "$DIGITALOCEAN_CREDENTIAL_ID" \
        --ssh-key-credential-id "$SSH_KEY_CREDENTIAL_ID" --region "$DIGITALOCEAN_REGION" \
        --network-ip-range "$(worker_cidr)" \
        --bastion-size "$DIGITALOCEAN_BASTION_SIZE" \
        --control-plane-size "$DIGITALOCEAN_CP_SIZE" --control-plane-count 1 \
        --worker-size "$DIGITALOCEAN_WORKER_SIZE" --worker-count 1 \
        --external-cloud-provider "${dist_args[@]}" "${gitops_args[@]}"
      ;;
    *) die "unknown provider $provider" ;;
  esac
}

bigger_plan() {
  case "$1" in
    hetzner) echo "$HETZNER_BIGGER_TYPE" ;;
    ovh)     echo "$OVH_BIGGER_FLAVOR" ;;
    upcloud) echo "$UPCLOUD_BIGGER_PLAN" ;;
    digitalocean) echo "$DIGITALOCEAN_BIGGER_SIZE" ;;
  esac
}

ng_instance_type() {
  case "$1" in
    hetzner) echo "$HETZNER_WORKER_TYPE" ;;
    ovh)     echo "$OVH_WORKER_FLAVOR" ;;
    upcloud) echo "$UPCLOUD_WORKER_PLAN" ;;
    digitalocean) echo "$DIGITALOCEAN_WORKER_SIZE" ;;
  esac
}

# ---------------------------------------------------------------------------
# Full lifecycle for one provider
# ---------------------------------------------------------------------------

run_provider() {
  local provider="$1" distribution="$2"
  local name="${NAME_PREFIX}-${provider}-${distribution}-${RUN_ID}"
  local label="$provider/$distribution"
  local id target plan out

  section "$label :: $name"

  # 1. Create (capture the printed "Cluster ID: <uuid>")
  log "creating $name (distribution=$distribution) ..."
  out="$(create_cluster "$provider" "$name" "$distribution")"
  printf '%s\n' "$out"
  id="$(printf '%s' "$out" | awk -F'Cluster ID:' '/Cluster ID:/{gsub(/[ \t]/,"",$2); print $2; exit}')"
  if [ -z "$id" ]; then fail "$label create (could not resolve cluster id)"; return; fi
  register_cluster "$name" "$id"
  pass "$label create submitted (id=$id)"

  # 2. Online + nodes
  if wait_for_online "$name" "$ONLINE_TIMEOUT"; then pass "$label online"; else fail "$label did not reach online"; return; fi
  if wait_for_nodes "$name" 2 "$DAYTWO_TIMEOUT"; then pass "$label nodes Ready (cp+worker)"; else fail "$label nodes not Ready"; fi

  # 3. Stacks
  if wait_for_addons "$name" 4 "$ADDONS_TIMEOUT"; then pass "$label stacks up (CCM/CSI/Traefik/cert-manager)"; else fail "$label stacks did not reach up"; fi

  # 4. Scale up / down (generic, provider-auto-detecting verbs)
  if daytwo "scale up" "$name" scale "$id" 3 && wait_for_nodes "$name" 4 "$DAYTWO_TIMEOUT"; then
    pass "$label scale up 1->3"; else fail "$label scale up"; fi
  if daytwo "scale down" "$name" scale "$id" 1 && wait_for_nodes "$name" 2 "$DAYTWO_TIMEOUT"; then
    pass "$label scale down 3->1"; else fail "$label scale down"; fi

  # 5. Node group add / delete
  if daytwo "ng add" "$name" node-group add "$id" --name pool-b --instance-type "$(ng_instance_type "$provider")" --count 2 \
     && wait_for_nodes "$name" 4 "$DAYTWO_TIMEOUT" && node_group_present "$name" "$id" pool-b; then
    pass "$label node-group add"; else fail "$label node-group add"; fi
  if daytwo "ng delete" "$name" node-group delete "$id" pool-b --yes \
     && wait_for_nodes "$name" 2 "$DAYTWO_TIMEOUT"; then
    pass "$label node-group delete"; else fail "$label node-group delete"; fi

  # 6. K8s upgrade (uses the matching k3s/kubeadm version listing)
  target="$(pick_upgrade_target "$name" "$distribution")"
  local want_ver="${target#v}"; want_ver="${want_ver%%+*}"
  if [ -n "$target" ] && daytwo "k8s upgrade" "$name" upgrade "$id" "$target"; then
    if wait_until_version "$name" "$want_ver" "$DAYTWO_TIMEOUT"; then pass "$label k8s upgrade -> $target"; else fail "$label k8s upgrade did not reach $target"; fi
  else fail "$label k8s upgrade (submit)"; fi

  # 7. Instance resize
  plan="$(bigger_plan "$provider")"
  if daytwo "resize" "$name" node-group upgrade "$id" default "$plan" \
     && wait_until_ng_plan "$name" "$id" default "$plan" "$DAYTWO_TIMEOUT"; then
    pass "$label instance resize default -> $plan"; else fail "$label instance resize"; fi

  # 8. Deprovision -> removed (with a bounded force-deprovision fallback on stall)
  log "deprovisioning $name ..."
  ank cluster deprovision "$id" --yes | tail -2
  if wait_for_removed "$name" "$DEPROVISION_TIMEOUT"; then
    pass "$label deprovision -> deleted_at"
  else
    log "  $name deprovision stalled after ${DEPROVISION_TIMEOUT}s; attempting bounded force-deprovision fallback"
    ank cluster deprovision "$id" --force --yes | tail -2 || true
    if wait_for_removed "$name" "$DEPROVISION_FORCE_TIMEOUT"; then
      pass "$label deprovision -> deleted_at (after force fallback)"
    else
      fail "$label deprovision did not complete (even after force fallback)"
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
      digitalocean) [ -n "$DIGITALOCEAN_CREDENTIAL_ID" ] || die "DIGITALOCEAN_CREDENTIAL_ID required for digitalocean" ;;
      *) die "unknown provider in ANKRA_SYSTEMTEST_PROVIDERS: $p" ;;
    esac
  done
  local d
  for d in $ANKRA_SYSTEMTEST_DISTRIBUTIONS; do
    case "$d" in
      k3s|kubeadm) ;;
      *) die "unknown distribution in ANKRA_SYSTEMTEST_DISTRIBUTIONS: $d (want k3s or kubeadm)" ;;
    esac
  done
}

# Run one provider's full lifecycle as an isolated worker: its own config copy
# (so `cluster select` is not shared), its own results file, and every line of
# output tagged + tee'd to a per-provider log. Intended to be backgrounded.
run_provider_bg() {
  local provider="$1" distribution="$2" WORKER_INDEX="${3:-0}"
  local slug="$provider-$distribution"
  # Isolated config copy so the CLI's saved credentials and active-cluster
  # selection are private to this worker (see the ank() wrapper). The .yaml
  # suffix keeps viper's format detection happy.
  local ANK_CONFIG="$WORKDIR/config.$slug.yaml"
  local RESULT_FILE="$WORKDIR/results.$slug"
  : > "$RESULT_FILE"
  if [ -f "$BASE_CONFIG" ]; then
    cp "$BASE_CONFIG" "$ANK_CONFIG" 2>/dev/null || true
  fi
  run_provider "$provider" "$distribution" 2>&1 | sed -u "s/^/[$slug] /" | tee "$WORKDIR/log.$slug"
}

main() {
  preflight
  section "Ankra cloud lifecycle system test (run $RUN_ID)"

  WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/ankra-systest-${RUN_ID}.XXXXXX")"
  CREATED_FILE="$WORKDIR/created_clusters"
  : > "$CREATED_FILE"

  # Build the provider x distribution matrix ("provider:distribution" targets).
  local -a targets=()
  local p d
  for p in $ANKRA_SYSTEMTEST_PROVIDERS; do
    for d in $ANKRA_SYSTEMTEST_DISTRIBUTIONS; do
      targets+=("$p:$d")
    done
  done

  local t
  local worker_index=0
  if [ "$ANKRA_SYSTEMTEST_PARALLEL" = "1" ]; then
    log "targets: ${targets[*]} (parallel)"
    log "run artifacts + per-target logs: $WORKDIR"
    local -a worker_targets=()
    for t in "${targets[@]}"; do
      run_provider_bg "${t%%:*}" "${t#*:}" "$worker_index" &
      WORKER_PIDS+=("$!")
      worker_targets+=("$t")
      worker_index=$((worker_index + 1))
    done
    local i
    for i in "${!WORKER_PIDS[@]}"; do
      wait "${WORKER_PIDS[$i]}" || true
      log "worker finished: ${worker_targets[$i]}"
    done
  else
    log "targets: ${targets[*]} (sequential)"
    log "run artifacts: $WORKDIR"
    for t in "${targets[@]}"; do
      p="${t%%:*}"; d="${t#*:}"
      RESULT_FILE="$WORKDIR/results.$p-$d" WORKER_INDEX="$worker_index" run_provider "$p" "$d"
      worker_index=$((worker_index + 1))
    done
  fi

  # Aggregate results from every worker's file (works for both modes).
  local -a all_results=()
  local total_failures=0 line slug
  for t in "${targets[@]}"; do
    slug="${t%%:*}-${t#*:}"
    [ -f "$WORKDIR/results.$slug" ] || continue
    while IFS= read -r line; do
      [ -z "$line" ] && continue
      all_results+=("$line")
      case "$line" in FAIL*) total_failures=$((total_failures + 1));; esac
    done < "$WORKDIR/results.$slug"
  done

  section "RESULTS"
  if [ "${#all_results[@]}" -gt 0 ]; then printf '%s\n' "${all_results[@]}"; fi
  section "SUMMARY"
  log "$(( ${#all_results[@]} - total_failures )) passed, $total_failures failed"
  [ "$total_failures" -eq 0 ]
}

main "$@"
