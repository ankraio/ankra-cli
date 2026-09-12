#!/usr/bin/env bash
#
# lifecycle_systemtest.sh
#
# Real, end-to-end system test for Ankra cloud clusters, driven entirely through
# the ankra CLI against a live platform (default: https://platform.ankra.dev).
#
# It exercises BOTH cluster families the platform supports:
#
# A) Ankra-managed clusters (self-managed k3s/kubeadm on provider VMs):
#    hetzner, ovh, upcloud, digitalocean. For each selected provider and each
#    selected Kubernetes distribution (k3s, kubeadm) it provisions a REAL
#    cluster and exercises the full lifecycle, asserting the outcome at every step:
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
#    `aws` (self-managed k3s/kubeadm on EC2 inside a VPC the account already
#    owns) is opt-in and runs its own, shorter lane, because the point of the
#    AWS provider is what it adopts rather than creates: it preflights, creates
#    a cluster with 1 control plane + 1 worker, waits for online + Ready,
#    checks access-info and the node list, stops and starts the cluster, then
#    deprovisions - and afterwards asserts with the AWS CLI that nothing tagged
#    ankra.cloud/cluster-id=<id> remains (instances, security groups, key
#    pairs, elastic IPs, route tables, IAM roles/instance profiles) and that
#    the customer VPC is untouched: its route tables (routes + associations),
#    subnets and DHCP options are snapshotted before the run and must diff
#    clean after deprovision. See "AWS lane" below for its variables.
#
# B) Cloud-managed clusters (provider-native managed Kubernetes via
#    `ankra cluster managed`): doks, uks, gke, ovh_mks, aks, eks. For each
#    selected managed provider it provisions a REAL managed cluster and runs
#    the managed lifecycle:
#
#   1. create (initial node pool, optional GitOps)
#   2. wait until the cluster is online and nodes are Ready
#   3. scale the initial node pool up (1 -> 3) and down (3 -> 1)
#   4. add a second node pool, then delete it
#   5. upgrade Kubernetes (only when MANAGED_UPGRADE_K8S_VERSION_<PROVIDER> is
#      set -- the CLI has no managed version listing, so the target is explicit)
#   6. delete and confirm the cluster record is removed
#
# Distributions run as an independent axis for the Ankra-managed family: with
# ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm" every selected provider gets one
# cluster per distribution (e.g. systest-digitalocean-k3s-... and
# systest-digitalocean-kubeadm-...), so a single run matrix-tests both.
# Cloud-managed providers have no distribution axis.
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
#   export SSH_KEY_CREDENTIAL_ID=...           # required for Ankra-managed providers
#   export HETZNER_CREDENTIAL_ID=...           # required per selected provider
#   export GITOPS_REPOSITORY=org/repo          # optional (GitOps commit step)
#   # AWS lane (only when "aws" is in ANKRA_SYSTEMTEST_PROVIDERS):
#   export AWS_CREDENTIAL_ID=...               # an Ankra aws credential (role scope self_managed, or keys)
#   #   or AWS_ROLE_ARN=... AWS_EXTERNAL_ID=... to register one for the run
#   export AWS_VPC_ID=vpc-... AWS_NODE_SUBNET_IDS=subnet-a,subnet-b AWS_BASTION_SUBNET_ID=subnet-c
#   export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...   # for the AWS CLI leak/VPC checks
#   ./systemtest/lifecycle_systemtest.sh                 # default matrix, in parallel
#   ANKRA_SYSTEMTEST_PARALLEL=0 ./systemtest/lifecycle_systemtest.sh   # sequential
#   ANKRA_SYSTEMTEST_PROVIDERS="upcloud" ./systemtest/lifecycle_systemtest.sh
#   # DigitalOcean, both distributions:
#   ANKRA_SYSTEMTEST_PROVIDERS="digitalocean" \
#     ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm" ./systemtest/lifecycle_systemtest.sh
#   # Cloud-managed only (DOKS + UKS):
#   ANKRA_SYSTEMTEST_PROVIDERS="" ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="doks uks" \
#     ./systemtest/lifecycle_systemtest.sh
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

# Ankra-managed (self-managed VMs) providers. Set to "" to skip the family.
ANKRA_SYSTEMTEST_PROVIDERS="${ANKRA_SYSTEMTEST_PROVIDERS-hetzner ovh upcloud digitalocean}"

# Cloud-managed (provider-native managed Kubernetes) providers, driven through
# `ankra cluster managed`. Set to "" to skip the family.
ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="${ANKRA_SYSTEMTEST_MANAGED_PROVIDERS-doks uks gke ovh_mks aks eks}"

# Kubernetes distributions to exercise per Ankra-managed provider. Each
# (provider, distribution) pair becomes its own cluster with the distribution
# in its name, so a single run can matrix-test both k3s and kubeadm side by
# side, e.g.
#   ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm"
# Cloud-managed providers ignore this axis.
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
OVH_GATEWAY_FLAVOR="${OVH_GATEWAY_FLAVOR:-b2-7}"

UPCLOUD_CP_PLAN="${UPCLOUD_CP_PLAN:-2xCPU-4GB}"
UPCLOUD_WORKER_PLAN="${UPCLOUD_WORKER_PLAN:-2xCPU-4GB}"
UPCLOUD_BIGGER_PLAN="${UPCLOUD_BIGGER_PLAN:-4xCPU-8GB}"

DIGITALOCEAN_CREDENTIAL_ID="${DIGITALOCEAN_CREDENTIAL_ID:-}"
DIGITALOCEAN_REGION="${DIGITALOCEAN_REGION:-nyc3}"
DIGITALOCEAN_BASTION_SIZE="${DIGITALOCEAN_BASTION_SIZE:-s-1vcpu-1gb}"
DIGITALOCEAN_CP_SIZE="${DIGITALOCEAN_CP_SIZE:-s-2vcpu-4gb}"
DIGITALOCEAN_WORKER_SIZE="${DIGITALOCEAN_WORKER_SIZE:-s-2vcpu-4gb}"
DIGITALOCEAN_BIGGER_SIZE="${DIGITALOCEAN_BIGGER_SIZE:-s-4vcpu-8gb}"

# AWS lane (self-managed EC2). The Ankra credential is either an existing one
# (AWS_CREDENTIAL_ID) or registered for the run from an assumable role
# (AWS_ROLE_ARN + AWS_EXTERNAL_ID, scope AWS_CREDENTIAL_SCOPE) and deleted at
# the end. The VPC, node subnets and bastion subnet are the account's own and
# are adopted, never created; the run proves they come back untouched.
# AWS_BASTION_ALLOWED_IPS defaults to this host's public IP so the bastion is
# only ever reachable from the runner.
AWS_CREDENTIAL_ID="${AWS_CREDENTIAL_ID:-}"
AWS_ROLE_ARN="${AWS_ROLE_ARN:-}"
AWS_EXTERNAL_ID="${AWS_EXTERNAL_ID:-}"
AWS_CREDENTIAL_SCOPE="${AWS_CREDENTIAL_SCOPE:-self_managed}"
AWS_REGION="${AWS_REGION:-eu-west-1}"
AWS_VPC_ID="${AWS_VPC_ID:-}"
AWS_NODE_SUBNET_IDS="${AWS_NODE_SUBNET_IDS:-}"
AWS_BASTION_SUBNET_ID="${AWS_BASTION_SUBNET_ID:-}"
AWS_BASTION_ALLOWED_IPS="${AWS_BASTION_ALLOWED_IPS:-}"
AWS_EGRESS_MODE="${AWS_EGRESS_MODE:-}"
AWS_CP_TYPE="${AWS_CP_TYPE:-t3.medium}"
AWS_WORKER_TYPE="${AWS_WORKER_TYPE:-t3.medium}"
AWS_BASTION_TYPE="${AWS_BASTION_TYPE:-t3.small}"
# Seconds to wait for terminated instances to leave describe-instances and
# for the tag sweep to come back empty after the deprovision reports done.
AWS_LEAK_TIMEOUT="${AWS_LEAK_TIMEOUT:-900}"

# Cloud-managed providers. Credentials default to the matching self-managed
# provider credential where the platform reuses the same credential kind
# (doks -> digitalocean, uks -> upcloud, ovh_mks -> ovh); gke/aks/eks need
# their own cloud credential.
DOKS_CREDENTIAL_ID="${DOKS_CREDENTIAL_ID:-$DIGITALOCEAN_CREDENTIAL_ID}"
UKS_CREDENTIAL_ID="${UKS_CREDENTIAL_ID:-$UPCLOUD_CREDENTIAL_ID}"
OVH_MKS_CREDENTIAL_ID="${OVH_MKS_CREDENTIAL_ID:-$OVH_CREDENTIAL_ID}"
GKE_CREDENTIAL_ID="${GKE_CREDENTIAL_ID:-}"
AKS_CREDENTIAL_ID="${AKS_CREDENTIAL_ID:-}"
EKS_CREDENTIAL_ID="${EKS_CREDENTIAL_ID:-}"

# Cloud-managed locations (region/zone per provider).
DOKS_LOCATION="${DOKS_LOCATION:-$DIGITALOCEAN_REGION}"
UKS_LOCATION="${UKS_LOCATION:-$UPCLOUD_ZONE}"
OVH_MKS_LOCATION="${OVH_MKS_LOCATION:-$OVH_REGION}"
GKE_LOCATION="${GKE_LOCATION:-europe-west1}"
AKS_LOCATION="${AKS_LOCATION:-westeurope}"
EKS_LOCATION="${EKS_LOCATION:-eu-west-1}"

# Cloud-managed initial node pool sizes/plans.
DOKS_NODE_POOL_SIZE="${DOKS_NODE_POOL_SIZE:-s-2vcpu-4gb}"
UKS_NODE_POOL_SIZE="${UKS_NODE_POOL_SIZE:-2xCPU-4GB}"
OVH_MKS_NODE_POOL_SIZE="${OVH_MKS_NODE_POOL_SIZE:-b2-15}"
GKE_NODE_POOL_SIZE="${GKE_NODE_POOL_SIZE:-e2-standard-2}"
AKS_NODE_POOL_SIZE="${AKS_NODE_POOL_SIZE:-Standard_D2s_v3}"
EKS_NODE_POOL_SIZE="${EKS_NODE_POOL_SIZE:-t3.medium}"

# Optional cloud-managed Kubernetes versions. The CLI has no managed version
# listing, so both the create version and the upgrade target are explicit
# per-provider env vars (e.g. MANAGED_CREATE_K8S_VERSION_DOKS=1.31.9-do.3 and
# MANAGED_UPGRADE_K8S_VERSION_DOKS=1.32.5-do.0). When the upgrade target is
# unset the managed upgrade step is skipped (recorded as SKIP, not FAIL).

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
CREATED_CREDENTIALS_FILE=""
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
skip() { log "SKIP: $1"; record "SKIP  $1"; }

# Remember a created cluster for cleanup. Entries are "name=id=managed_provider"
# where the third field is empty for Ankra-managed (self-managed) clusters and
# the managed provider slug (doks, uks, ...) for cloud-managed clusters, so the
# cleanup trap knows which delete verb to use. Append to the shared file (so
# the main-shell trap sees clusters created inside parallel worker subshells)
# and also keep the in-memory array for sequential runs.
register_cluster() {
  local name="$1" id="$2" managed_provider="${3:-}"
  CREATED_CLUSTERS+=("$name=$id=$managed_provider")
  if [ -n "${CREATED_FILE:-}" ]; then
    printf '%s=%s=%s\n' "$name" "$id" "$managed_provider" >> "$CREATED_FILE"
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
  # deprovision/delete call takes the id explicitly so no selection is required.
  local entry name id rest managed_provider
  local -a entries=()
  if [ -n "${CREATED_FILE:-}" ] && [ -f "$CREATED_FILE" ]; then
    while IFS= read -r entry; do entries+=("$entry"); done < "$CREATED_FILE"
  else
    entries=("${CREATED_CLUSTERS[@]:-}")
  fi
  for entry in "${entries[@]:-}"; do
    [ -z "$entry" ] && continue
    name="${entry%%=*}"
    rest="${entry#*=}"
    id="${rest%%=*}"
    managed_provider=""
    case "$rest" in *=*) managed_provider="${rest#*=}";; esac
    if ank cluster list | grep -q "$name"; then
      log "cleanup: deprovisioning leftover cluster $name ($id)"
      # Best-effort: try a graceful teardown, then force so we never leak
      # paid infrastructure on an aborted run.
      if [ -n "$managed_provider" ]; then
        ank cluster managed delete "$id" --provider "$managed_provider" --yes >/dev/null 2>&1 || true
        ank cluster managed delete "$id" --provider "$managed_provider" --force --yes >/dev/null 2>&1 || true
      else
        ank cluster deprovision "$id" --yes >/dev/null 2>&1 || true
        ank cluster deprovision "$id" --force --yes >/dev/null 2>&1 || true
      fi
    fi
  done
  # Credentials the run registered (the AWS role credential) go last: a
  # deprovision still needs the credential the cluster was built with.
  local credential_id
  if [ -n "${CREATED_CREDENTIALS_FILE:-}" ] && [ -f "$CREATED_CREDENTIALS_FILE" ]; then
    while IFS= read -r credential_id; do
      [ -z "$credential_id" ] && continue
      log "cleanup: deleting run-registered credential $credential_id"
      ank credentials delete "$credential_id" --yes >/dev/null 2>&1 || true
    done < "$CREATED_CREDENTIALS_FILE"
  fi
}
trap cleanup EXIT INT TERM

# ---------------------------------------------------------------------------
# Query helpers
# ---------------------------------------------------------------------------

select_cluster() { ank cluster select "$1" >/dev/null 2>&1 || true; }

# Echo the named column (header text, spaces stripped) for a cluster row, or
# empty if not present. The column index is resolved from the table header so
# added/reordered columns in `ankra cluster list` don't silently break us.
# ANSI colour escapes are stripped too: the CLI paints e.g. online green when
# it detects a tty, which the kubernetes attach runner provides.
cluster_list_field() {
  ank cluster list | awk -F'│' -v n="$1" -v h="$2" '
    function clean(s) { gsub(/\033\[[0-9;]*[a-zA-Z]/, "", s); gsub(/ /, "", s); return s }
    col == 0 {
      for (i = 1; i <= NF; i++) if (toupper(clean($i)) == h) { col = i; break }
      next
    }
    clean($2) == n { print clean($col); exit }'
}

cluster_state()   { cluster_list_field "$1" "STATE"; }
cluster_version() { cluster_list_field "$1" "KUBEVERSION"; }

cluster_in_list() { ank cluster list | awk -F'│' -v n="$1" '{gsub(/ /,"",$2); if ($2==n) f=1} END{exit f?0:1}'; }

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

# Wait until the cluster reports exactly the given state (e.g. stopped).
wait_for_state() {
  local name="$1" want="$2" timeout="$3" deadline state
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    state="$(cluster_state "$name")"
    log "  $name state=$state (want $want)"
    [ "$state" = "$want" ] && return 0
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

# Run a day-2 write that the platform may reject with 409 while a reconcile
# runs. Managed clusters reject with "not in a state that allows ..." while a
# provider-side operation is still settling; retry those the same way.
daytwo() {
  local desc="$1"; shift
  local name="$1"; shift
  local attempt out
  wait_idle "$name" "$IDLE_TIMEOUT" || log "  ($name still busy; attempting $desc anyway)"
  for attempt in $(seq 1 8); do
    out="$(ank cluster "$@" 2>&1)"
    if printf '%s' "$out" | grep -qiE "operations in progress|409|not in a state"; then
      log "  $desc rejected (ops in progress), retry $attempt"
      sleep 20
      wait_idle "$name" "$IDLE_TIMEOUT" || log "  ($name still busy before retry of $desc)"
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
        --gateway-flavor-id "$OVH_GATEWAY_FLAVOR" \
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
# AWS lane: helpers
# ---------------------------------------------------------------------------

# The AWS CLI is used for what the Ankra API cannot answer: whether anything
# tagged with the cluster id is left in the account after deprovision, and
# whether the customer VPC came back untouched. It needs its own AWS
# credentials (AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, a profile, or an
# ambient role) - these are the account's, not Ankra's. When the CLI or its
# credentials are missing those two checks are recorded as SKIP with the
# reason, never as PASS: a check that did not run proves nothing.
aws_cli_reason=""
aws_cli_available() {
  if [ -n "$aws_cli_reason" ]; then return 1; fi
  if ! command -v aws >/dev/null 2>&1; then
    aws_cli_reason="aws CLI not installed"; return 1
  fi
  if ! command -v jq >/dev/null 2>&1; then
    aws_cli_reason="jq not installed"; return 1
  fi
  if ! aws sts get-caller-identity --region "$AWS_REGION" >/dev/null 2>&1; then
    aws_cli_reason="aws CLI has no usable credentials (set AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY or a profile)"; return 1
  fi
  return 0
}

awscli() { aws --region "$AWS_REGION" --output json "$@"; }

# Snapshot the parts of the customer VPC an Ankra cluster is allowed to
# touch only transiently, normalised so a legitimate no-op diffs clean:
#   - route tables: id, routes (sorted by destination) and subnet
#     associations (sorted by subnet). Association *ids* are dropped on
#     purpose: re-associating a subnet with its original table (which
#     bastion_nat mode does on teardown) mints a new association id while
#     leaving the routing identical, and the routing is what we assert.
#   - subnets: id, cidr, zone, public-IP-on-launch, tags. AvailableIpAddressCount
#     is dropped because instances in flight change it.
#   - DHCP options: the option set the VPC points at and its configurations.
aws_vpc_snapshot() {
  local out="$1"
  {
    printf '{"route_tables":'
    awscli ec2 describe-route-tables --filters "Name=vpc-id,Values=$AWS_VPC_ID" \
      | jq -S '[.RouteTables[] | {
          id: .RouteTableId,
          tags: ((.Tags // []) | sort_by(.Key)),
          routes: ([.Routes[] | del(.State)] | sort_by(.DestinationCidrBlock // .DestinationIpv6CidrBlock // .DestinationPrefixListId // "")),
          associations: ([.Associations[] | {subnet: .SubnetId, gateway: .GatewayId, main: .Main}] | sort_by(.subnet // "", .gateway // ""))
        }] | sort_by(.id)'
    printf ',"subnets":'
    awscli ec2 describe-subnets --filters "Name=vpc-id,Values=$AWS_VPC_ID" \
      | jq -S '[.Subnets[] | {
          id: .SubnetId, cidr: .CidrBlock, zone: .AvailabilityZone,
          map_public_ip_on_launch: .MapPublicIpOnLaunch,
          assign_ipv6_on_creation: .AssignIpv6AddressOnCreation,
          tags: ((.Tags // []) | sort_by(.Key))
        }] | sort_by(.id)'
    printf ',"dhcp_options":'
    local dhcp_id
    dhcp_id="$(awscli ec2 describe-vpcs --vpc-ids "$AWS_VPC_ID" | jq -r '.Vpcs[0].DhcpOptionsId // empty')"
    if [ -n "$dhcp_id" ] && [ "$dhcp_id" != "default" ]; then
      awscli ec2 describe-dhcp-options --dhcp-options-ids "$dhcp_id" \
        | jq -S --arg id "$dhcp_id" '{id: $id, configurations: ([.DhcpOptions[0].DhcpConfigurations[] | {key: .Key, values: ([.Values[].Value] | sort)}] | sort_by(.key))}'
    else
      jq -n --arg id "${dhcp_id:-default}" '{id: $id, configurations: []}'
    fi
    printf '}'
  } | jq -S . > "$out"
}

# Diff the VPC against the pre-run snapshot. The snapshot was taken before
# the cluster existed, so the Ankra route table (bastion_nat mode) is absent
# from it by construction and its survival shows up as an extra entry.
aws_vpc_diff() {
  local before="$1" after="$2"
  diff -u "$before" "$after"
}

# Everything Ankra creates in the account carries ankra.cloud/cluster-id.
# The sweep polls because EC2 keeps a terminated instance (with its tags) in
# describe-instances for a while and security groups cannot go until the
# instances have, so "nothing left" is a condition to wait for, bounded by
# AWS_LEAK_TIMEOUT. Each resource kind is checked with its own describe call
# (the tag filter is the same everywhere), and the Resource Groups Tagging
# API sweeps every other kind (volumes, ENIs, load balancers, IAM roles and
# instance profiles) so an untracked kind cannot leak silently.
aws_leaked_resources() {
  local cluster_id="$1"
  local tag="Name=tag:ankra.cloud/cluster-id,Values=$cluster_id"
  awscli ec2 describe-instances --filters "$tag" "Name=instance-state-name,Values=pending,running,shutting-down,stopping,stopped" \
    | jq -r '.Reservations[].Instances[] | "instance " + .InstanceId + " " + .State.Name'
  awscli ec2 describe-security-groups --filters "$tag" | jq -r '.SecurityGroups[] | "security-group " + .GroupId'
  awscli ec2 describe-key-pairs --filters "$tag" | jq -r '.KeyPairs[] | "key-pair " + .KeyPairId'
  awscli ec2 describe-addresses --filters "$tag" | jq -r '.Addresses[] | "elastic-ip " + .AllocationId'
  awscli ec2 describe-route-tables --filters "$tag" | jq -r '.RouteTables[] | "route-table " + .RouteTableId'
  awscli ec2 describe-volumes --filters "$tag" | jq -r '.Volumes[] | "volume " + .VolumeId + " " + .State'
  # IAM is global: the tagging API answers for it from us-east-1.
  aws --region us-east-1 --output json resourcegroupstaggingapi get-resources \
      --resource-type-filters iam:role iam:instance-profile \
      --tag-filters "Key=ankra.cloud/cluster-id,Values=$cluster_id" \
    | jq -r '.ResourceTagMappingList[] | "iam " + .ResourceARN'
  # Any other tagged kind in the region. Terminated instances keep their tags
  # briefly and are excluded here; the instance check above already covers
  # every state that is not terminated.
  awscli resourcegroupstaggingapi get-resources --tag-filters "Key=ankra.cloud/cluster-id,Values=$cluster_id" \
    | jq -r '.ResourceTagMappingList[] | .ResourceARN | select(contains(":instance/") | not) | "tagged " + .'
}

wait_for_no_leaks() {
  local cluster_id="$1" timeout="$2" deadline leaked
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    leaked="$(aws_leaked_resources "$cluster_id" 2>&1 | sed '/^$/d')"
    if [ -z "$leaked" ]; then return 0; fi
    log "  still tagged ankra.cloud/cluster-id=$cluster_id:"
    printf '%s\n' "$leaked" | sed 's/^/    /'
    sleep "$POLL_INTERVAL"
  done
  return 1
}

# Register the run's Ankra AWS credential from the role when no credential
# id was given; remembered for cleanup.
aws_ensure_credential() {
  if [ -n "$AWS_CREDENTIAL_ID" ]; then return 0; fi
  local out
  log "registering AWS role credential for the run (scope $AWS_CREDENTIAL_SCOPE) ..."
  out="$(ank credentials aws create-role --name "${NAME_PREFIX}-aws-${RUN_ID}" --role-arn "$AWS_ROLE_ARN" \
    --external-id "$AWS_EXTERNAL_ID" --region "$AWS_REGION" --scope "$AWS_CREDENTIAL_SCOPE")"
  printf '%s\n' "$out"
  AWS_CREDENTIAL_ID="$(printf '%s' "$out" | awk -F'Credential ID:' '/Credential ID:/{gsub(/[ \t]/,"",$2); print $2; exit}')"
  if [ -z "$AWS_CREDENTIAL_ID" ]; then return 1; fi
  if [ -n "${CREATED_CREDENTIALS_FILE:-}" ]; then printf '%s\n' "$AWS_CREDENTIAL_ID" >> "$CREATED_CREDENTIALS_FILE"; fi
  return 0
}

aws_bastion_allowed_ips() {
  if [ -n "$AWS_BASTION_ALLOWED_IPS" ]; then printf '%s' "$AWS_BASTION_ALLOWED_IPS"; return; fi
  local ip
  ip="$(curl -fsS --max-time 10 https://checkip.amazonaws.com 2>/dev/null | tr -d '[:space:]')"
  [ -n "$ip" ] && printf '%s/32' "$ip"
}

# The create/preflight flag set, shared so preflight checks exactly the
# request create sends.
aws_create_args() {
  local name="$1" distribution="$2" allowed_ips="$3"
  local -a args=(--name "$name" --credential-id "$AWS_CREDENTIAL_ID" --ssh-key-credential-id "$SSH_KEY_CREDENTIAL_ID" \
    --region "$AWS_REGION" --vpc-id "$AWS_VPC_ID" --node-subnet-ids "$AWS_NODE_SUBNET_IDS" \
    --bastion-subnet-id "$AWS_BASTION_SUBNET_ID" --bastion-allowed-ips "$allowed_ips" \
    --bastion-instance-type "$AWS_BASTION_TYPE" \
    --control-plane-type "$AWS_CP_TYPE" --control-plane-count 1 \
    --worker-type "$AWS_WORKER_TYPE" --worker-count 1 \
    --distribution "$distribution")
  if [ "$distribution" = "kubeadm" ]; then args+=(--etcd-topology "$ETCD_TOPOLOGY"); fi
  if [ -n "$AWS_EGRESS_MODE" ]; then args+=(--egress-mode "$AWS_EGRESS_MODE"); fi
  if [ -n "$GITOPS_CREDENTIAL_NAME" ] && [ -n "$GITOPS_REPOSITORY" ]; then
    args+=(--gitops-credential-name "$GITOPS_CREDENTIAL_NAME" --gitops-repository "$GITOPS_REPOSITORY" --gitops-branch "$GITOPS_BRANCH")
  fi
  printf '%s\n' "${args[@]}"
}

# ---------------------------------------------------------------------------
# AWS lane: lifecycle
# ---------------------------------------------------------------------------

run_aws_provider() {
  local distribution="$1"
  local name="${NAME_PREFIX}-aws-${distribution}-${RUN_ID}"
  local label="aws/$distribution"
  local id out allowed_ips access
  local -a create_args=()

  section "$label :: $name"

  if ! aws_ensure_credential; then fail "$label credential (could not register the role credential)"; return; fi

  allowed_ips="$(aws_bastion_allowed_ips)"
  if [ -z "$allowed_ips" ]; then fail "$label bastion allowed IPs (set AWS_BASTION_ALLOWED_IPS; could not detect this host's public IP)"; return; fi
  log "bastion SSH allowed from: $allowed_ips"

  # 0. VPC snapshot before anything exists.
  local before="$WORKDIR/aws-vpc-before.$distribution.json" after="$WORKDIR/aws-vpc-after.$distribution.json"
  local vpc_snapshotted=0
  if aws_cli_available; then
    if aws_vpc_snapshot "$before"; then
      vpc_snapshotted=1; pass "$label VPC snapshot taken ($AWS_VPC_ID)"
    else
      fail "$label VPC snapshot (describe-route-tables/subnets/dhcp-options failed)"
    fi
  else
    skip "$label VPC snapshot ($aws_cli_reason)"
  fi

  while IFS= read -r line; do create_args+=("$line"); done < <(aws_create_args "$name" "$distribution" "$allowed_ips")

  # 1. Preflight must pass before anything is built.
  log "preflighting $name ..."
  if out="$(ank cluster aws preflight "${create_args[@]}")"; then
    printf '%s\n' "$out"
    pass "$label preflight"
  else
    printf '%s\n' "$out"
    fail "$label preflight (see checks above)"
    return
  fi

  # 2. Create (capture the printed "Cluster ID: <uuid>")
  log "creating $name (distribution=$distribution) ..."
  out="$(ank cluster aws create "${create_args[@]}")"
  printf '%s\n' "$out"
  id="$(printf '%s' "$out" | awk -F'Cluster ID:' '/Cluster ID:/{gsub(/[ \t]/,"",$2); print $2; exit}')"
  if [ -z "$id" ]; then fail "$label create (could not resolve cluster id)"; return; fi
  register_cluster "$name" "$id"
  pass "$label create submitted (id=$id)"

  # 3. Online + nodes
  if wait_for_online "$name" "$ONLINE_TIMEOUT"; then pass "$label online"; else fail "$label did not reach online"; return; fi
  if wait_for_nodes "$name" 2 "$DAYTWO_TIMEOUT"; then pass "$label nodes Ready (cp+worker)"; else fail "$label nodes not Ready"; fi

  # 4. Access info: the bastion has its elastic IP and the control plane a
  # private address. "-" is the CLI's rendering of a null, so it is a fail.
  access="$(ank cluster aws access-info "$id")"
  printf '%s\n' "$access"
  if printf '%s' "$access" | grep -qE '^Bastion IP: [0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' \
     && printf '%s' "$access" | grep -qE '^Control Plane IP: [0-9]+\.[0-9]+\.[0-9]+\.[0-9]+'; then
    pass "$label access-info (bastion + control plane addresses)"
  else
    fail "$label access-info (missing bastion or control plane address)"
  fi

  # 5. Node list through the provider node route: both nodes present.
  select_cluster "$name"
  out="$(ank cluster nodes list "$id")"
  printf '%s\n' "$out"
  if [ "$(printf '%s\n' "$out" | grep -cE 'control[-_ ]?plane|worker')" -ge 2 ]; then
    pass "$label node list (control plane + worker)"
  else
    fail "$label node list (expected a control plane and a worker row)"
  fi

  # 6. Stop -> stopped, start -> online again with both nodes Ready.
  if daytwo "stop" "$name" aws stop "$id" && wait_for_state "$name" stopped "$DAYTWO_TIMEOUT"; then
    pass "$label stop -> stopped"
  else
    fail "$label stop"
  fi
  if daytwo "start" "$name" aws start "$id" && wait_for_online "$name" "$ONLINE_TIMEOUT" && wait_for_nodes "$name" 2 "$DAYTWO_TIMEOUT"; then
    pass "$label start -> online (cp+worker Ready)"
  else
    fail "$label start"
  fi

  # 7. Deprovision -> removed (with a bounded force fallback on stall)
  wait_idle "$name" "$IDLE_TIMEOUT" || log "  ($name still busy; deprovisioning anyway)"
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

  # 8. Leak check: nothing tagged with the cluster id may remain.
  if aws_cli_available; then
    if wait_for_no_leaks "$id" "$AWS_LEAK_TIMEOUT"; then
      pass "$label no resources tagged ankra.cloud/cluster-id=$id remain"
    else
      fail "$label leaked resources still tagged ankra.cloud/cluster-id=$id after ${AWS_LEAK_TIMEOUT}s (see list above)"
    fi
  else
    skip "$label leak check ($aws_cli_reason)"
  fi

  # 9. Customer VPC untouched: route tables, subnets and DHCP options match
  # the pre-run snapshot exactly.
  if [ "$vpc_snapshotted" = "1" ]; then
    if aws_vpc_snapshot "$after"; then
      if aws_vpc_diff "$before" "$after"; then
        pass "$label VPC untouched (route tables, subnets, DHCP options identical)"
      else
        fail "$label VPC changed by the run (see diff above)"
      fi
    else
      fail "$label VPC snapshot after deprovision failed"
    fi
  elif aws_cli_available; then
    skip "$label VPC diff (no pre-run snapshot)"
  else
    skip "$label VPC diff ($aws_cli_reason)"
  fi
}

# ---------------------------------------------------------------------------
# Cloud-managed provider helpers
# ---------------------------------------------------------------------------

managed_credential_id() {
  case "$1" in
    doks)    echo "$DOKS_CREDENTIAL_ID" ;;
    uks)     echo "$UKS_CREDENTIAL_ID" ;;
    gke)     echo "$GKE_CREDENTIAL_ID" ;;
    ovh_mks) echo "$OVH_MKS_CREDENTIAL_ID" ;;
    aks)     echo "$AKS_CREDENTIAL_ID" ;;
    eks)     echo "$EKS_CREDENTIAL_ID" ;;
  esac
}

managed_location() {
  case "$1" in
    doks)    echo "$DOKS_LOCATION" ;;
    uks)     echo "$UKS_LOCATION" ;;
    gke)     echo "$GKE_LOCATION" ;;
    ovh_mks) echo "$OVH_MKS_LOCATION" ;;
    aks)     echo "$AKS_LOCATION" ;;
    eks)     echo "$EKS_LOCATION" ;;
  esac
}

managed_node_pool_size() {
  case "$1" in
    doks)    echo "$DOKS_NODE_POOL_SIZE" ;;
    uks)     echo "$UKS_NODE_POOL_SIZE" ;;
    gke)     echo "$GKE_NODE_POOL_SIZE" ;;
    ovh_mks) echo "$OVH_MKS_NODE_POOL_SIZE" ;;
    aks)     echo "$AKS_NODE_POOL_SIZE" ;;
    eks)     echo "$EKS_NODE_POOL_SIZE" ;;
  esac
}

managed_provider_upper() { printf '%s' "$1" | tr '[:lower:]' '[:upper:]'; }

# Read an optional per-provider env var like MANAGED_UPGRADE_K8S_VERSION_DOKS
# (bash-3.2-safe indirection, so the script still runs on stock macOS bash).
managed_env() {
  local variable_name
  variable_name="${1}_$(managed_provider_upper "$2")"
  eval "printf '%s' \"\${$variable_name:-}\""
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
  # Let running reconciles (e.g. the resize's server replacement) settle first:
  # a deprovision submitted mid-reconcile computes its teardown plan against an
  # incomplete resource set and can strand the late-registered server after the
  # credential is already deleted.
  wait_idle "$name" "$IDLE_TIMEOUT" || log "  ($name still busy; deprovisioning anyway)"
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

# ---------------------------------------------------------------------------
# Full lifecycle for one cloud-managed provider
# ---------------------------------------------------------------------------

run_managed_provider() {
  local provider="$1"
  local slug; slug="$(printf '%s' "$provider" | tr '_' '-')"
  local name="${NAME_PREFIX}-${slug}-${RUN_ID}"
  local label="managed/$provider"
  local id out size target create_version
  size="$(managed_node_pool_size "$provider")"

  section "$label :: $name"

  # 1. Create (capture the printed "Cluster ID: <uuid>")
  local gitops_args=()
  if [ -n "$GITOPS_CREDENTIAL_NAME" ] && [ -n "$GITOPS_REPOSITORY" ]; then
    gitops_args=(--gitops-credential-name "$GITOPS_CREDENTIAL_NAME" --gitops-repository "$GITOPS_REPOSITORY" --gitops-branch "$GITOPS_BRANCH")
  fi
  local version_args=()
  create_version="$(managed_env MANAGED_CREATE_K8S_VERSION "$provider")"
  if [ -n "$create_version" ]; then
    version_args=(--kubernetes-version "$create_version")
  fi
  log "creating managed $name ..."
  out="$(ank cluster managed create --provider "$provider" --name "$name" \
    --credential-id "$(managed_credential_id "$provider")" \
    --location "$(managed_location "$provider")" \
    --node-pool-name workers --node-pool-size "$size" --node-pool-count 1 \
    "${version_args[@]}" "${gitops_args[@]}")"
  printf '%s\n' "$out"
  id="$(printf '%s' "$out" | awk -F'Cluster ID:' '/Cluster ID:/{gsub(/[ \t]/,"",$2); print $2; exit}')"
  if [ -z "$id" ]; then fail "$label create (could not resolve cluster id)"; return; fi
  register_cluster "$name" "$id" "$provider"
  pass "$label create submitted (id=$id)"

  # 2. Online + nodes (managed control planes are provider-hosted, so only the
  # node pool's workers appear as nodes).
  if wait_for_online "$name" "$ONLINE_TIMEOUT"; then pass "$label online"; else fail "$label did not reach online"; return; fi
  if wait_for_nodes "$name" 1 "$DAYTWO_TIMEOUT"; then pass "$label nodes Ready (worker pool)"; else fail "$label nodes not Ready"; fi

  # 3. Node pool scale up / down
  if daytwo "node-pool scale up" "$name" managed node-pool scale "$id" workers --provider "$provider" --count 3 \
     && wait_for_nodes "$name" 3 "$DAYTWO_TIMEOUT"; then
    pass "$label node-pool scale up 1->3"; else fail "$label node-pool scale up"; fi
  if daytwo "node-pool scale down" "$name" managed node-pool scale "$id" workers --provider "$provider" --count 1 \
     && wait_for_nodes "$name" 1 "$DAYTWO_TIMEOUT"; then
    pass "$label node-pool scale down 3->1"; else fail "$label node-pool scale down"; fi

  # 4. Node pool add / delete
  if daytwo "node-pool add" "$name" managed node-pool add "$id" --provider "$provider" --name pool-b --size "$size" --count 2 \
     && wait_for_nodes "$name" 3 "$DAYTWO_TIMEOUT"; then
    pass "$label node-pool add"; else fail "$label node-pool add"; fi
  if daytwo "node-pool delete" "$name" managed node-pool delete "$id" pool-b --provider "$provider" --yes \
     && wait_for_nodes "$name" 1 "$DAYTWO_TIMEOUT"; then
    pass "$label node-pool delete"; else fail "$label node-pool delete"; fi

  # 5. K8s upgrade (explicit target only -- the CLI has no managed version
  # listing to pick one from, so without a target the step is a SKIP).
  target="$(managed_env MANAGED_UPGRADE_K8S_VERSION "$provider")"
  if [ -n "$target" ]; then
    local want_ver="${target#v}"; want_ver="${want_ver%%[-+]*}"
    if daytwo "k8s upgrade" "$name" managed upgrade "$id" --provider "$provider" --version "$target" --yes \
       && wait_until_version "$name" "$want_ver" "$DAYTWO_TIMEOUT"; then
      pass "$label k8s upgrade -> $target"; else fail "$label k8s upgrade did not reach $target"; fi
  else
    skip "$label k8s upgrade (MANAGED_UPGRADE_K8S_VERSION_$(managed_provider_upper "$provider") not set)"
  fi

  # 6. Delete -> removed (with a bounded force fallback on stall)
  log "deleting managed $name ..."
  ank cluster managed delete "$id" --provider "$provider" --yes | tail -2
  if wait_for_removed "$name" "$DEPROVISION_TIMEOUT"; then
    pass "$label delete -> removed"
  else
    log "  $name delete stalled after ${DEPROVISION_TIMEOUT}s; attempting bounded force-delete fallback"
    ank cluster managed delete "$id" --provider "$provider" --force --yes | tail -2 || true
    if wait_for_removed "$name" "$DEPROVISION_FORCE_TIMEOUT"; then
      pass "$label delete -> removed (after force fallback)"
    else
      fail "$label delete did not complete (even after force fallback)"
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
  #  and volumes on Hetzner / OVH / UpCloud / DigitalOcean (and EC2 in your #
  #  own VPC when aws is selected) plus provider-native managed clusters    #
  #  (DOKS / UKS / GKE / OVH MKS / AKS / EKS) and                            #
  #  runs a multi-step lifecycle (create, scale, node-groups/pools, k8s      #
  #  upgrade, resize, deprovision). A full run can take ~2 hours and WILL    #
  #  incur charges. Clusters are deprovisioned at the end and on abort, but  #
  #  a crash of this script can still leave paid resources running --        #
  #  verify afterwards.                                                      #
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
  if [ -z "$ANKRA_SYSTEMTEST_PROVIDERS" ] && [ -z "$ANKRA_SYSTEMTEST_MANAGED_PROVIDERS" ]; then
    die "nothing selected: both ANKRA_SYSTEMTEST_PROVIDERS and ANKRA_SYSTEMTEST_MANAGED_PROVIDERS are empty"
  fi
  if [ -n "$ANKRA_SYSTEMTEST_PROVIDERS" ]; then
    [ -n "$SSH_KEY_CREDENTIAL_ID" ] || die "SSH_KEY_CREDENTIAL_ID is required for Ankra-managed providers"
  fi
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
      aws)
        if [ -z "$AWS_CREDENTIAL_ID" ] && { [ -z "$AWS_ROLE_ARN" ] || [ -z "$AWS_EXTERNAL_ID" ]; }; then
          die "AWS_CREDENTIAL_ID (or AWS_ROLE_ARN + AWS_EXTERNAL_ID to register one) required for aws"
        fi
        [ -n "$AWS_VPC_ID" ] || die "AWS_VPC_ID required for aws"
        [ -n "$AWS_NODE_SUBNET_IDS" ] || die "AWS_NODE_SUBNET_IDS required for aws"
        [ -n "$AWS_BASTION_SUBNET_ID" ] || die "AWS_BASTION_SUBNET_ID required for aws"
        if ! aws_cli_available; then
          log "WARNING: $aws_cli_reason -> the AWS leak check and VPC diff will be recorded as SKIP"
        fi
        ;;
      *) die "unknown provider in ANKRA_SYSTEMTEST_PROVIDERS: $p" ;;
    esac
  done
  local m
  for m in $ANKRA_SYSTEMTEST_MANAGED_PROVIDERS; do
    case "$m" in
      doks|uks|gke|ovh_mks|aks|eks)
        [ -n "$(managed_credential_id "$m")" ] || die "$(managed_provider_upper "$m")_CREDENTIAL_ID required for managed provider $m" ;;
      *) die "unknown provider in ANKRA_SYSTEMTEST_MANAGED_PROVIDERS: $m (want doks, uks, gke, ovh_mks, aks or eks)" ;;
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

# Dispatch a target to the right lifecycle: the "managed" distribution marks a
# cloud-managed provider, everything else is an Ankra-managed (self-managed)
# provider + distribution pair.
run_target() {
  local provider="$1" distribution="$2"
  if [ "$distribution" = "managed" ]; then
    run_managed_provider "$provider"
  elif [ "$provider" = "aws" ]; then
    run_aws_provider "$distribution"
  else
    run_provider "$provider" "$distribution"
  fi
}

# Run one target's full lifecycle as an isolated worker: its own config copy
# (so `cluster select` is not shared), its own results file, and every line of
# output tagged + tee'd to a per-target log. Intended to be backgrounded.
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
  run_target "$provider" "$distribution" 2>&1 | sed -u "s/^/[$slug] /" | tee "$WORKDIR/log.$slug"
}

main() {
  preflight
  section "Ankra cloud lifecycle system test (run $RUN_ID)"

  WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/ankra-systest-${RUN_ID}.XXXXXX")"
  CREATED_FILE="$WORKDIR/created_clusters"
  : > "$CREATED_FILE"
  CREATED_CREDENTIALS_FILE="$WORKDIR/created_credentials"
  : > "$CREATED_CREDENTIALS_FILE"

  # Build the target list: the Ankra-managed provider x distribution matrix
  # ("provider:distribution") plus one "provider:managed" target per selected
  # cloud-managed provider (managed clusters have no distribution axis).
  local -a targets=()
  local p d m
  for p in $ANKRA_SYSTEMTEST_PROVIDERS; do
    for d in $ANKRA_SYSTEMTEST_DISTRIBUTIONS; do
      targets+=("$p:$d")
    done
  done
  for m in $ANKRA_SYSTEMTEST_MANAGED_PROVIDERS; do
    targets+=("$m:managed")
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
      RESULT_FILE="$WORKDIR/results.$p-$d" WORKER_INDEX="$worker_index" run_target "$p" "$d"
      worker_index=$((worker_index + 1))
    done
  fi

  # Aggregate results from every worker's file (works for both modes).
  local -a all_results=()
  local total_failures=0 total_skips=0 line slug
  for t in "${targets[@]}"; do
    slug="${t%%:*}-${t#*:}"
    [ -f "$WORKDIR/results.$slug" ] || continue
    while IFS= read -r line; do
      [ -z "$line" ] && continue
      all_results+=("$line")
      case "$line" in
        FAIL*) total_failures=$((total_failures + 1));;
        SKIP*) total_skips=$((total_skips + 1));;
      esac
    done < "$WORKDIR/results.$slug"
  done

  section "RESULTS"
  if [ "${#all_results[@]}" -gt 0 ]; then printf '%s\n' "${all_results[@]}"; fi
  section "SUMMARY"
  log "$(( ${#all_results[@]} - total_failures - total_skips )) passed, $total_failures failed, $total_skips skipped"
  [ "$total_failures" -eq 0 ]
}

main "$@"
