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
#   7b. (opt-in, ANKRA_SYSTEMTEST_AUTOSCALING=1) node-group autoscaling end to
#      end: enable min 1 / max 2, the Cluster Autoscaler is Ready and
#      registers every worker, pending pods grow the group to 2, it shrinks
#      back to 1 once they are gone, disable - see "Opt-in: node-group
#      autoscaling" below; the AWS lane runs it too
#   8. deprovision and confirm the cluster record is removed (deleted_at)
#
#    `aws` (self-managed k3s/kubeadm on EC2) is opt-in and runs its own,
#    shorter lane, because the point of the AWS provider is the network it
#    owns: by default Ankra creates the whole network (VPC, subnets, internet
#    gateway, route tables, NAT) and must remove it with the cluster. The lane
#    needs only credentials - no pre-made VPC: it preflights, creates a cluster
#    with 1 control plane + 1 worker in a network Ankra creates (egress
#    bastion_nat by default, the cheapest for CI), waits for online + Ready,
#    checks access-info and the node list, stops and starts the cluster,
#    upgrades Kubernetes to the newest listed version (the create pins the
#    second-newest so there is a step to take), runs the opt-in autoscaling
#    step when ANKRA_SYSTEMTEST_AUTOSCALING=1, then
#    deprovisions - and afterwards asserts with the AWS CLI that nothing tagged
#    ankra.cloud/cluster-id=<id> remains: instances, security groups, key
#    pairs, IAM roles/instance profiles AND the created network itself (VPC,
#    subnets, internet gateway, NAT gateways, route tables, elastic IPs).
#    Setting AWS_VPC_ID (+ AWS_NODE_SUBNET_IDS + AWS_BASTION_SUBNET_ID) switches
#    the lane to adopting that VPC instead, in which case the VPC's route
#    tables (routes + associations), subnets and DHCP options are snapshotted
#    before the run and must diff clean after deprovision. See "AWS lane"
#    below for its variables.
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
#   export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...   # the account's keys for the AWS CLI leak check (required)
#   export AWS_REGION=eu-west-1 AWS_BASTION_ALLOWED_IPS=203.0.113.0/24   # allowed IPs default to this host's /32
#   #   optional: AWS_EGRESS_MODE=nat_gateway, AWS_AVAILABILITY_ZONES=a,b,c (3 zones only when asked),
#   #   or AWS_VPC_ID=vpc-... AWS_NODE_SUBNET_IDS=subnet-a,subnet-b AWS_BASTION_SUBNET_ID=subnet-c to adopt a VPC
#   ./systemtest/lifecycle_systemtest.sh                 # default matrix, in parallel
#   ANKRA_SYSTEMTEST_PARALLEL=0 ./systemtest/lifecycle_systemtest.sh   # sequential
#   ANKRA_SYSTEMTEST_PROVIDERS="upcloud" ./systemtest/lifecycle_systemtest.sh
#   # DigitalOcean, both distributions:
#   ANKRA_SYSTEMTEST_PROVIDERS="digitalocean" \
#     ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm" ./systemtest/lifecycle_systemtest.sh
#   # Cloud-managed only (DOKS + UKS):
#   ANKRA_SYSTEMTEST_PROVIDERS="" ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="doks uks" \
#     ./systemtest/lifecycle_systemtest.sh
#   # Hetzner with the opt-in node-group autoscaling step (needs jq):
#   ANKRA_SYSTEMTEST_AUTOSCALING=1 ANKRA_SYSTEMTEST_PROVIDERS=hetzner \
#     ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="" ./systemtest/lifecycle_systemtest.sh
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
# the end.
#
# The network is Ankra's by default: the lane names no VPC and the platform
# creates one (AWS_NETWORK_IP_RANGE, server default 10.0.0.0/16) in the
# zones AWS_AVAILABILITY_ZONES names - unset, the server picks ONE zone for
# this 1-control-plane cluster; three zones are built only when asked for.
# Egress is AWS_EGRESS_MODE, default bastion_nat (no NAT gateway to pay for
# in CI; set nat_gateway to exercise the gateways, and
# AWS_NAT_GATEWAY_SINGLE_ZONE=1 for one gateway instead of one per zone).
# The run proves the created network is gone after deprovision.
#
# Setting AWS_VPC_ID (with AWS_NODE_SUBNET_IDS and AWS_BASTION_SUBNET_ID)
# switches the lane to adopting that VPC: nothing is created there, egress
# is resolved by preflight unless AWS_EGRESS_MODE pins existing|bastion_nat,
# and the run proves the VPC comes back untouched instead.
#
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
AWS_NETWORK_IP_RANGE="${AWS_NETWORK_IP_RANGE:-}"
AWS_AVAILABILITY_ZONES="${AWS_AVAILABILITY_ZONES:-}"
AWS_NAT_GATEWAY_SINGLE_ZONE="${AWS_NAT_GATEWAY_SINGLE_ZONE:-0}"
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

# Opt-in node-group autoscaling step (every Ankra-managed lane, AWS
# included; cloud-managed lanes have provider-native autoscalers and do not
# run it). 1 runs it just before the deprovision: enable autoscaling min 1 /
# max 2 on AUTOSCALING_NODE_GROUP, wait for the Cluster Autoscaler, assert it
# registers every worker, grow the group with pods that need a second node,
# shrink it again once they are gone, disable. Off by default because it
# adds roughly 25-50 minutes and one extra worker for ~15-25 of them to every
# lane it runs on, so the scheduled cost must not change silently. Needs jq.
ANKRA_SYSTEMTEST_AUTOSCALING="${ANKRA_SYSTEMTEST_AUTOSCALING:-0}"
AUTOSCALING_NODE_GROUP="${AUTOSCALING_NODE_GROUP:-default}"
# Timeouts (seconds), sized from the cluster-autoscaler platform profile
# (ankraio/cluster alembic/platform_profiles/cluster-autoscaler.json):
# max-node-provision-time 15m, scale-down-delay-after-add 10m,
# scale-down-unneeded-time 10m.
#  - CA_READY: enabling installs the autoscaler stack (a Helm release).
#  - IDENTITY_GRACE: how long the autoscaler may still report registered
#    workers as unregistered once every worker is Ready.
#  - SCALE_UP: 15m provision cap + 5m for the autoscaler to see the pending
#    pod and the scheduler to bind it once the node is Ready.
#  - SCALE_DOWN: the node must be unneeded for 10m (and 10m past the
#    scale-up, which has mostly elapsed by then), then drained and its
#    server deleted: ~15m in practice, so 30m bounds it at twice that.
AUTOSCALING_CA_READY_TIMEOUT="${AUTOSCALING_CA_READY_TIMEOUT:-900}"
AUTOSCALING_IDENTITY_GRACE="${AUTOSCALING_IDENTITY_GRACE:-300}"
AUTOSCALING_SCALE_UP_TIMEOUT="${AUTOSCALING_SCALE_UP_TIMEOUT:-1200}"
AUTOSCALING_SCALE_DOWN_TIMEOUT="${AUTOSCALING_SCALE_DOWN_TIMEOUT:-1800}"
# CPU request (millicores) of each of the two load pods. Empty = sized from
# the group's worker: 90% of the CPU it has not already promised to pods.
AUTOSCALING_LOAD_CPU_MILLICORES="${AUTOSCALING_LOAD_CPU_MILLICORES:-}"

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

# Run the CLI keeping only stdout, with the CLI's own exit status: for the
# -o json reads a step parses. ank() merges stderr into the stream (a hint
# there would corrupt the document) and pipes it through grep, whose status
# - 1 when every line is filtered or there are none - pipefail would report
# in place of the CLI's.
ank_out() {
  if [ -n "${ANK_CONFIG:-}" ]; then
    "$ANKRA_BIN" --config "$ANK_CONFIG" "$@" 2>/dev/null
  else
    "$ANKRA_BIN" "$@" 2>/dev/null
  fi
}

# Like ank() - stderr merged, login preamble filtered - but returning the
# CLI's own exit status, so a refused write is a failure the caller sees.
ank_rc() {
  local out rc
  if [ -n "${ANK_CONFIG:-}" ]; then
    out="$("$ANKRA_BIN" --config "$ANK_CONFIG" "$@" 2>&1)"; rc=$?
  else
    out="$("$ANKRA_BIN" "$@" 2>&1)"; rc=$?
  fi
  printf '%s\n' "$out" | grep -vE "ANKRA_API_TOKEN env var is set|To use the env var instead|Using login token" || true
  return "$rc"
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
# tagged with the cluster id is left in the account after deprovision - the
# created network above all - and, when a VPC was adopted, whether it came
# back untouched. It needs its own AWS credentials
# (AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, a profile, or an ambient role) -
# these are the account's, not Ankra's. The preflight refuses to run the
# lane without them: a created-network run whose leak check cannot run
# proves nothing about the network being gone, and that is the lane's
# point. (The helpers still record SKIP, never PASS, should the CLI go away
# mid-run.)
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

# The lane adopts a VPC only when one was named; otherwise Ankra creates
# the network and the created-network checks apply.
aws_adopts_vpc() { [ -n "$AWS_VPC_ID" ]; }

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
# describe-instances for a while, security groups cannot go until the
# instances have, and a VPC cannot go until everything in it has, so
# "nothing left" is a condition to wait for, bounded by AWS_LEAK_TIMEOUT.
# Each resource kind is checked with its own describe call (the tag filter
# is the same everywhere) - the network kinds explicitly, because a created
# network is Ankra's and every piece of it must be gone: VPC, subnets,
# internet gateway, NAT gateways, route tables and elastic IPs - and the
# Resource Groups Tagging API sweeps every other kind (volumes, ENIs, load
# balancers, IAM roles and instance profiles) so an untracked kind cannot
# leak silently.
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
  # The created network. A deleted NAT gateway lingers in describe-nat-gateways
  # (state "deleted") for about an hour, so only the live states count.
  awscli ec2 describe-vpcs --filters "$tag" | jq -r '.Vpcs[] | "vpc " + .VpcId + " " + .State'
  awscli ec2 describe-subnets --filters "$tag" | jq -r '.Subnets[] | "subnet " + .SubnetId'
  awscli ec2 describe-internet-gateways --filters "$tag" | jq -r '.InternetGateways[] | "internet-gateway " + .InternetGatewayId'
  awscli ec2 describe-nat-gateways --filter "$tag" "Name=state,Values=pending,failed,available,deleting" \
    | jq -r '.NatGateways[] | "nat-gateway " + .NatGatewayId + " " + .State'
  awscli ec2 describe-network-interfaces --filters "$tag" | jq -r '.NetworkInterfaces[] | "network-interface " + .NetworkInterfaceId'
  # IAM is global and the two roles and instance profiles of a cluster have
  # deterministic names (clusterengine.AwsIAMName: ankra-k3s-<id>-cp and
  # -node), so they are looked up by name with the iam:GetRole /
  # iam:GetInstanceProfile the provisioning keys already hold. NoSuchEntity
  # is the only answer that means gone: any other failure is an unknown
  # answer and is listed as such, so it keeps the sweep red rather than
  # passing for a role nobody could see.
  local suffix iam_name iam_answer
  for suffix in cp node; do
    iam_name="ankra-k3s-${cluster_id}-${suffix}"
    if iam_answer="$(aws --region us-east-1 --output json iam get-role --role-name "$iam_name" 2>&1)"; then
      echo "iam role $iam_name"
    elif ! printf '%s' "$iam_answer" | grep -q NoSuchEntity; then
      echo "iam role $iam_name (cannot check: $(printf '%s' "$iam_answer" | head -n1))"
    fi
    if iam_answer="$(aws --region us-east-1 --output json iam get-instance-profile --instance-profile-name "$iam_name" 2>&1)"; then
      echo "iam instance-profile $iam_name"
    elif ! printf '%s' "$iam_answer" | grep -q NoSuchEntity; then
      echo "iam instance-profile $iam_name (cannot check: $(printf '%s' "$iam_answer" | head -n1))"
    fi
  done
  # Any other tagged kind in the region, when the keys may ask (see
  # aws_tag_sweep_permitted). Terminated instances and deleted NAT gateways
  # keep their tags for a while and are excluded here; the checks above
  # already cover every state of theirs that is not gone.
  if [ "$AWS_TAG_SWEEP" = yes ]; then
    awscli resourcegroupstaggingapi get-resources --tag-filters "Key=ankra.cloud/cluster-id,Values=$cluster_id" \
      | jq -r '.ResourceTagMappingList[] | .ResourceARN | select((contains(":instance/") or contains(":natgateway/")) | not) | "tagged " + .'
  fi
}

# The Resource Groups Tagging API sweep needs tag:GetResources, which the
# provisioning template does not grant (nothing Ankra does needs it), so
# keys scoped exactly like a customer's role cannot ask. Probed once, before
# the sweep: a refusal is logged and the sweep covers the explicitly listed
# kinds; an unexpected failure is logged too and treated the same way. The
# answer is cached in AWS_TAG_SWEEP because aws_leaked_resources runs inside
# a command substitution, where a log line would be read as a leaked
# resource. The run that shipped this (ankra-cli nightly 34781352847) failed
# its leak check on 900 seconds of AccessDenied while the account was clean.
AWS_TAG_SWEEP=""
aws_tag_sweep_permitted() {
  local probe
  if [ -n "$AWS_TAG_SWEEP" ]; then [ "$AWS_TAG_SWEEP" = yes ]; return; fi
  if probe="$(awscli resourcegroupstaggingapi get-resources --resources-per-page 1 2>&1)"; then
    AWS_TAG_SWEEP=yes
  elif printf '%s' "$probe" | grep -q AccessDenied; then
    AWS_TAG_SWEEP=no
    log "  tag:GetResources is not granted to these keys: the leak sweep covers the explicitly listed kinds only (grant tag:GetResources to sweep every tagged kind)"
  else
    AWS_TAG_SWEEP=no
    log "  tagging API probe failed, the leak sweep covers the explicitly listed kinds only: $(printf '%s' "$probe" | head -n1)"
  fi
  [ "$AWS_TAG_SWEEP" = yes ]
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

# The egress mode the lane asks for. A created network defaults to
# bastion_nat (the bastion is the NAT: no NAT gateway hourly charge, the
# cheapest run for CI); an adopted VPC leaves it to preflight unless pinned.
aws_egress_mode() {
  if [ -n "$AWS_EGRESS_MODE" ]; then printf '%s' "$AWS_EGRESS_MODE"; return; fi
  if aws_adopts_vpc; then return; fi
  printf 'bastion_nat'
}

# The create/preflight flag set, shared so preflight checks exactly the
# request create sends. Without AWS_VPC_ID it names no network at all beyond
# what was explicitly asked for, so the platform creates one with its own
# defaults (one zone for this 1-control-plane cluster).
# The AWS lane creates on the second-newest listed version so its upgrade
# step has somewhere to go (AWS_CREATE_K8S_VERSION overrides; empty when the
# listing has fewer than two entries, in which case the upgrade is skipped).
aws_pick_create_version() {
  local distribution="$1" versions_cmd="k3s-versions"
  if [ -n "${AWS_CREATE_K8S_VERSION:-}" ]; then echo "$AWS_CREATE_K8S_VERSION"; return; fi
  if [ "$distribution" = "kubeadm" ]; then versions_cmd="kubeadm-versions"; fi
  ank cluster "$versions_cmd" | awk '/Available versions:/{f=1;next} f&&NF{n++; if(n==2){print $1; exit}}'
}

aws_create_args() {
  local name="$1" distribution="$2" allowed_ips="$3" egress_mode create_version
  local -a args=(--name "$name" --credential-id "$AWS_CREDENTIAL_ID" --ssh-key-credential-id "$SSH_KEY_CREDENTIAL_ID" \
    --region "$AWS_REGION" --bastion-allowed-ips "$allowed_ips" \
    --bastion-instance-type "$AWS_BASTION_TYPE" \
    --control-plane-type "$AWS_CP_TYPE" --control-plane-count 1 \
    --worker-type "$AWS_WORKER_TYPE" --worker-count 1 \
    --distribution "$distribution")
  create_version="$(aws_pick_create_version "$distribution")"
  if [ -n "$create_version" ]; then args+=(--kubernetes-version "$create_version"); fi
  if aws_adopts_vpc; then
    args+=(--vpc-id "$AWS_VPC_ID" --node-subnet-ids "$AWS_NODE_SUBNET_IDS" --bastion-subnet-id "$AWS_BASTION_SUBNET_ID")
  else
    if [ -n "$AWS_NETWORK_IP_RANGE" ]; then args+=(--network-ip-range "$AWS_NETWORK_IP_RANGE"); fi
    if [ -n "$AWS_AVAILABILITY_ZONES" ]; then args+=(--availability-zones "$AWS_AVAILABILITY_ZONES"); fi
    if [ "$AWS_NAT_GATEWAY_SINGLE_ZONE" = "1" ]; then args+=(--nat-gateway-single-zone); fi
  fi
  if [ "$distribution" = "kubeadm" ]; then args+=(--etcd-topology "$ETCD_TOPOLOGY"); fi
  egress_mode="$(aws_egress_mode)"
  if [ -n "$egress_mode" ]; then args+=(--egress-mode "$egress_mode"); fi
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

  local network_ownership="created" egress_mode
  if aws_adopts_vpc; then network_ownership="adopted"; fi
  egress_mode="$(aws_egress_mode)"
  log "network: $network_ownership (egress ${egress_mode:-resolved by preflight})"

  # 0. Adopted VPC only: snapshot it before anything exists. A created
  # network has nothing to snapshot - its proof is the leak check.
  local before="$WORKDIR/aws-vpc-before.$distribution.json" after="$WORKDIR/aws-vpc-after.$distribution.json"
  local vpc_snapshotted=0
  if aws_adopts_vpc; then
    if aws_cli_available; then
      if aws_vpc_snapshot "$before"; then
        vpc_snapshotted=1; pass "$label VPC snapshot taken ($AWS_VPC_ID)"
      else
        fail "$label VPC snapshot (describe-route-tables/subnets/dhcp-options failed)"
      fi
    else
      skip "$label VPC snapshot ($aws_cli_reason)"
    fi
  fi

  while IFS= read -r line; do create_args+=("$line"); done < <(aws_create_args "$name" "$distribution" "$allowed_ips")

  # 1. Preflight must pass before anything is built, and it must agree on
  # who owns the network: "Network ownership: created" for the default
  # lane, "adopted" when a VPC was named. An ownership the preflight did not
  # report is not the right one.
  log "preflighting $name ..."
  if out="$(ank cluster aws preflight "${create_args[@]}")"; then
    printf '%s\n' "$out"
    pass "$label preflight"
  else
    printf '%s\n' "$out"
    fail "$label preflight (see checks above)"
    return
  fi
  if printf '%s\n' "$out" | grep -qx "Network ownership: $network_ownership"; then
    pass "$label preflight reports network ownership $network_ownership"
  else
    fail "$label preflight did not report network ownership $network_ownership (got: $(printf '%s\n' "$out" | grep '^Network ownership:' || echo 'no ownership line'))"
    return
  fi
  if ! aws_adopts_vpc; then
    if printf '%s\n' "$out" | grep -qE '^Resolved availability zones: [a-z0-9-]+'; then
      pass "$label preflight resolved the zones for the created network ($(printf '%s\n' "$out" | grep '^Resolved availability zones:' | sed 's/^Resolved availability zones: //'))"
    else
      fail "$label preflight did not resolve the created network's zones"
      return
    fi
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
  out="$(ank cluster aws nodes list "$id")"
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

  # 6b. Kubernetes upgrade to the newest listed version (the create pinned
  # the second-newest so there is a step to take): cordon, drain, upgrade,
  # Ready at the target - control plane first, then the worker.
  local target want_ver
  target="$(pick_upgrade_target "$name" "$distribution")"
  want_ver="${target#v}"; want_ver="${want_ver%%+*}"
  if [ -z "$target" ]; then
    skip "$label k8s upgrade (no target version listed)"
  elif daytwo "k8s upgrade" "$name" upgrade "$id" "$target"; then
    if wait_until_version "$name" "$want_ver" "$DAYTWO_TIMEOUT" && wait_for_nodes "$name" 2 "$DAYTWO_TIMEOUT"; then
      pass "$label k8s upgrade -> $target (cp+worker Ready)"
    else
      fail "$label k8s upgrade did not reach $target with both nodes Ready"
    fi
  else
    fail "$label k8s upgrade (submit)"
  fi

  # 6c. Node-group autoscaling end to end (opt-in: ANKRA_SYSTEMTEST_AUTOSCALING=1;
  # records a SKIP otherwise). The leak check after the deprovision (step 8)
  # is the proof that no EC2 instance the autoscaler created outlived the
  # cluster.
  run_autoscaling_step "$name" "$id" "$label"

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

  # 8. Leak check: nothing tagged with the cluster id may remain - for a
  # created network that includes the VPC, subnets, internet gateway, NAT
  # gateways, route tables and elastic IPs, which the sweep lists by kind.
  local leak_scope="instances, security groups, key pairs, volumes, network interfaces, IAM roles and instance profiles"
  if ! aws_adopts_vpc; then leak_scope="$leak_scope and the created network (VPC, subnets, IGW, NAT, route tables, EIPs)"; fi
  if aws_cli_available; then
    if aws_tag_sweep_permitted; then leak_scope="$leak_scope, plus every other tagged kind"; else leak_scope="$leak_scope; tagging-API sweep not permitted"; fi
    if wait_for_no_leaks "$id" "$AWS_LEAK_TIMEOUT"; then
      pass "$label no resources tagged ankra.cloud/cluster-id=$id remain ($leak_scope)"
    else
      fail "$label leaked resources still tagged ankra.cloud/cluster-id=$id after ${AWS_LEAK_TIMEOUT}s (see list above)"
    fi
  else
    skip "$label leak check ($aws_cli_reason)"
  fi

  # 9. Adopted VPC only: route tables, subnets and DHCP options match the
  # pre-run snapshot exactly. A created network has no "untouched" to prove
  # - it must be gone, which step 8 covered.
  if ! aws_adopts_vpc; then
    return
  fi
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
# Opt-in: node-group autoscaling end to end (ANKRA_SYSTEMTEST_AUTOSCALING=1)
# ---------------------------------------------------------------------------
#
# Nothing else exercises node-group autoscaling end to end, which is how
# three production defects shipped unseen (fixed in ankraio/cluster#3557,
# plus #3555 for AWS scale-to-zero): AWS served the Cluster Autoscaler a
# target size of zero and refused every scale-down; the autoscaler saw every
# worker as an unregistered instance on every provider ("N unregistered nodes
# present", "Nodegroup is nil for ..."); and a worker whose Kubernetes join
# was held let each scale-up spawn another billed server. The step drives the
# CLI verbs a customer uses and asserts each of those:
#
#   1. enable autoscaling min 1 / max 2 on the group, from exactly 1 worker
#   2. the cluster-autoscaler Deployment in the ankra namespace is Ready
#   3. the autoscaler's newest main loop reports no registered worker as
#      unregistered (checked at 1 worker, and again at 2)
#   4. two pods that cannot share a node grow the group to 2 workers and
#      both schedule; the group never goes past max_count
#   5. with the pods gone the group shrinks back to 1, never below it
#   6. autoscaling is disabled and the group is left at 1 worker
#
# Any failure after the enable runs as_cleanup: remove the load, disable
# autoscaling, scale the group back to 1 by hand. What that cannot fix, the
# lane's deprovision removes with every other server (the EXIT/INT/TERM trap
# deprovisions on an abort mid-step too), and on AWS the leak check after it
# proves no instance the autoscaler created outlived the cluster.

AS_LOAD_NAME="systest-ca-load"   # the load's stack, manifest and Deployment
AS_LOAD_NAMESPACE="default"
AS_CA_NAMESPACE="ankra"
AS_CA_NAME="cluster-autoscaler"  # fullnameOverride of the platform's CA release
AS_NODE_GROUP_LABEL="ankra.cloud/node-group"
AS_LOG_WINDOW=60                 # seconds of CA log per read: ~6 loops at the 10s scan interval

# jq definitions for the step's reads of `ankra cluster get ... -o json`:
#   cpu_m               a CPU quantity ("2", "1.5", "250m") in millicores
#   labelled            whether any node carries the ankra.cloud/node-group label
#   group_nodes($g)     the group's nodes as {name, ready, provider_id, cpu_m}:
#                       matched by that label, which every Ankra worker
#                       carries - or, when no node carries it, every node
#                       without a control-plane role
#   node_requests($n)   "<millicores requested on node $n> <listing truncated>"
#                       over its non-terminated pods (containers only; pod
#                       overhead and init containers are left out)
# shellcheck disable=SC2016 # a jq program: its $names are jq's, not the shell's
AS_JQ_LIB='
def cpu_m: if . == null then 0
  elif type == "number" then . * 1000
  elif endswith("m") then (.[0:-1] | tonumber)
  else tonumber * 1000 end;
def control_plane: (.metadata.labels // {}) | has("node-role.kubernetes.io/control-plane") or has("node-role.kubernetes.io/master");
def labelled: [.resource_responses[0].items[]? | select(.metadata.labels["ankra.cloud/node-group"] != null)] | length > 0;
def group_nodes($g):
  labelled as $labelled
  | .resource_responses[0].items[]?
  | select(if $labelled then .metadata.labels["ankra.cloud/node-group"] == $g else (control_plane | not) end)
  | {name: .metadata.name,
     ready: ([.status.conditions[]? | select(.type == "Ready") | .status] | first == "True"),
     provider_id: (.spec.providerID // ""),
     cpu_m: (.status.allocatable.cpu | cpu_m | floor)};
def node_requests($n):
  .resource_responses[0] as $r
  | [$r.items[]?
     | select(.spec.nodeName == $n)
     | select((.status.phase // "") as $p | $p != "Succeeded" and $p != "Failed")
     | [.spec.containers[]? | (.resources.requests.cpu // null) | cpu_m] | add // 0]
  | "\(add // 0 | floor) \(($r.total_count // ($r.items | length)) > ($r.items | length))";
'

as_nodes_json() { ank_out cluster get nodes --cluster "$1" -o json; }

# The group's platform worker count ("count" in node-group list: its worker
# records, i.e. what is billed), or empty when unreadable.
as_group_count() {
  ank_out cluster node-group list "$1" -o json \
    | jq -r --arg g "$2" '[.node_groups[]? | select(.name == $g) | .count] | first // empty' 2>/dev/null
}

# "<count> <ready> <nodes>": the platform's worker count, and the group's
# Kubernetes nodes Ready and in total. "?" where a read failed - and for the
# nodes when the listing came back empty, since a self-managed cluster
# always has its control plane: an empty answer is a failed read, not a
# group scaled to zero.
as_group_state() {
  local id="$1" group="$2" count ready="" total=""
  count="$(as_group_count "$id" "$group")"
  read -r ready total <<<"$(as_nodes_json "$id" | jq -r --arg g "$group" "$AS_JQ_LIB
    if ([.resource_responses[0].items[]?] | length) == 0 then \"? ?\"
    else \"\([group_nodes(\$g) | select(.ready)] | length) \([group_nodes(\$g)] | length)\" end" 2>/dev/null)"
  printf '%s %s %s\n' "${count:-?}" "${ready:-?}" "${total:-?}"
}

as_group_names() {
  ank_out cluster node-group list "$1" -o json | jq -r '[.node_groups[]?.name] | join(", ")' 2>/dev/null
}

# Wait until the group's worker count, Ready nodes and nodes all equal want.
as_wait_group() {
  local id="$1" group="$2" want="$3" timeout="$4" deadline count ready total
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    read -r count ready total <<<"$(as_group_state "$id" "$group")"
    log "  $group count=$count nodes ready=$ready/$total (want $want)"
    if [ "$count" = "$want" ] && [ "$ready" = "$want" ] && [ "$total" = "$want" ]; then return 0; fi
    sleep "$POLL_INTERVAL"
  done
  return 1
}

# Load pods in a phase ("" = any phase), or empty when unreadable.
as_load_pods() {
  ank_out cluster get pods -n "$AS_LOAD_NAMESPACE" --name "$AS_LOAD_NAME" --cluster "$1" -o json \
    | jq -r --arg p "$AS_LOAD_NAME-" --arg phase "$2" \
        '[.pods[]? | select(.name | startswith($p)) | select($phase == "" or .phase == $phase)] | length' 2>/dev/null
}

# The newest Running autoscaler pod, or empty.
as_ca_pod() {
  ank_out cluster get pods -n "$AS_CA_NAMESPACE" --name "$AS_CA_NAME" --cluster "$1" -o json \
    | jq -r '[.pods[]? | select(.phase == "Running")] | sort_by(.start_time // "") | last | .name // empty' 2>/dev/null
}

# A write the step depends on: daytwo()'s wait-for-idle and 409 retry, but
# returning the CLI's own exit status - daytwo() reports success for any
# answer that is not a 409. Unlike daytwo(), the whole write shares one
# budget of 2 x IDLE_TIMEOUT: every retry waits for the cluster to go idle
# first, so per-attempt waits alone could spend 8 x IDLE_TIMEOUT inside a
# single write on a cluster that never settles.
as_write() {
  local desc="$1" name="$2"; shift 2
  local attempt out rc=1 remaining
  local budget=$(( 2 * IDLE_TIMEOUT ))
  local deadline=$(( $(date +%s) + budget ))
  for attempt in 1 2 3 4 5 6 7 8; do
    remaining=$(( deadline - $(date +%s) ))
    if [ "$remaining" -le 0 ]; then
      log "  $desc gave up: $name stayed busy past the write's ${budget}s budget"
      break
    fi
    if [ "$remaining" -gt "$IDLE_TIMEOUT" ]; then remaining="$IDLE_TIMEOUT"; fi
    wait_idle "$name" "$remaining" || log "  ($name still busy; attempting $desc anyway)"
    out="$(ank_rc "$@")"; rc=$?
    if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -qiE "operations in progress|409|not in a state"; then
      log "  $desc rejected (ops in progress), retry $attempt"
      sleep 20
      continue
    fi
    break
  done
  printf '%s\n' "$out" | tail -n 4
  return "$rc"
}

# Poll the group's autoscaling settings until they read as wanted:
# "true <min> <max>" when enabled, "false" when disabled. The write is
# asynchronous, so the platform's own read is the confirmation.
as_wait_settings() {
  local id="$1" group="$2" want="$3" deadline got
  deadline=$(( $(date +%s) + DAYTWO_TIMEOUT ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    got="$(ank_out cluster node-group autoscaling get "$id" "$group" -o json \
      | jq -r 'if .enabled then "true \(.min_count) \(.max_count)" else "false" end' 2>/dev/null)"
    if [ "$got" = "$want" ]; then return 0; fi
    log "  $group autoscaling=${got:-unreadable} (want $want)"
    sleep "$POLL_INTERVAL"
  done
  return 1
}

as_wait_ca_ready() {
  local id="$1" deadline state ready want
  deadline=$(( $(date +%s) + AUTOSCALING_CA_READY_TIMEOUT ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    state="$(ank_out cluster get deployments -n "$AS_CA_NAMESPACE" --cluster "$id" -o json \
      | jq -r --arg d "$AS_CA_NAME" '[.resource_responses[0].items[]? | select(.metadata.name == $d)] | first
          | if . == null then "absent" else "\(.status.readyReplicas // 0)/\(.spec.replicas // 1)" end' 2>/dev/null)"
    log "  $AS_CA_NAMESPACE/$AS_CA_NAME ready=${state:-unreadable}"
    case "$state" in
      [0-9]*/[0-9]*)
        ready="${state%%/*}"; want="${state#*/}"
        if [ "$want" -gt 0 ] && [ "$ready" -ge "$want" ]; then return 0; fi
        ;;
    esac
    sleep "$POLL_INTERVAL"
  done
  return 1
}

# Step 3. The autoscaler's newest complete main loop - the lines between the
# last two "Starting main loop" markers (klog v4, the chart default) - must
# not report a registered worker as unregistered. It logs "<N> unregistered
# nodes present" only when N > 0. Tolerated, and named rather than failed:
# nodes of the group with no spec.providerID, which the platform serves
# under a fallback instance id the autoscaler cannot match to a node (the
# documented fallback of cluster#3557). Polled for AUTOSCALING_IDENTITY_GRACE,
# since a node that has just joined can read as unregistered until the next
# snapshot. A log the CLI cannot read skips this assertion only.
as_assert_ca_identity() {
  local id="$1" group="$2" tag="$3"
  local deadline pod logs="" rc loop unregistered nil_count noid="" noid_count=0 state evidence
  deadline=$(( $(date +%s) + AUTOSCALING_IDENTITY_GRACE ))
  while :; do
    pod="$(as_ca_pod "$id")"
    if [ -z "$pod" ]; then
      state="nopod"; evidence="no Running $AS_CA_NAME pod in $AS_CA_NAMESPACE"
    else
      logs="$(ank_rc cluster logs "$pod" -n "$AS_CA_NAMESPACE" --all-containers --since "$AS_LOG_WINDOW" --follow=false --cluster "$id")"; rc=$?
      if [ "$rc" -ne 0 ]; then
        skip "$tag: autoscaler registers every worker NOT ASSERTED - the CLI could not read the $AS_CA_NAME log (ankra cluster logs exit $rc: $(printf '%s\n' "$logs" | tail -n 1))"
        return 0
      fi
      noid="$(as_nodes_json "$id" | jq -r --arg g "$group" "$AS_JQ_LIB [group_nodes(\$g) | select(.provider_id == \"\") | .name] | join(\" \")" 2>/dev/null)"
      noid_count="$(printf '%s\n' "$noid" | wc -w | tr -d ' ')"
      if loop="$(printf '%s\n' "$logs" | awk '/Starting main loop/ { prev = cur; cur = ""; n++; next } { cur = cur $0 "\n" } END { if (n < 2) exit 1; printf "%s", prev }')"; then
        unregistered="$(printf '%s\n' "$loop" | grep -oE '[0-9]+ unregistered nodes present' | tail -n 1 | awk '{print $1}')"
        unregistered="${unregistered:-0}"
        if [ "$unregistered" -le "$noid_count" ]; then
          if [ "$unregistered" -gt 0 ]; then
            pass "$tag: autoscaler registers every worker ($unregistered unregistered = the nodes with no spec.providerID, the documented fallback: $noid)"
          else
            pass "$tag: autoscaler registers every worker (newest loop: none unregistered)"
          fi
          return 0
        fi
        nil_count="$(printf '%s\n' "$loop" | grep -c 'Nodegroup is nil')"
        state="unregistered"
        evidence="newest loop: $unregistered unregistered nodes present, $nil_count 'Nodegroup is nil' lines; nodes without a providerID: ${noid:-none}"
      else
        state="noloop"; evidence="no complete main loop in the last ${AS_LOG_WINDOW}s of $pod"
      fi
    fi
    log "  $tag: $evidence"
    if [ "$(date +%s)" -ge "$deadline" ]; then break; fi
    sleep "$POLL_INTERVAL"
  done

  case "$state" in
    unregistered)
      fail "$tag: autoscaler still reports registered workers as unregistered ${AUTOSCALING_IDENTITY_GRACE}s after every worker was Ready ($evidence)"
      printf '%s\n' "$logs" | grep -E 'unregistered nodes present|Nodegroup is nil' | tail -n 10 | sed 's/^/    /'
      ;;
    noloop)
      # No loop markers (log verbosity below 4?): fall back to the newest
      # count anywhere in the window. Only a count past the tolerance is
      # evidence; its absence proves nothing without a loop to bound it.
      unregistered="$(printf '%s\n' "$logs" | grep -oE '[0-9]+ unregistered nodes present' | tail -n 1 | awk '{print $1}')"
      if [ -n "$unregistered" ] && [ "$unregistered" -gt "$noid_count" ]; then
        fail "$tag: autoscaler reports $unregistered unregistered nodes with ${noid_count} node(s) lacking a providerID (${noid:-none})"
      else
        skip "$tag: autoscaler registers every worker NOT ASSERTED - $evidence (log verbosity below 4?)"
      fi
      ;;
    *)
      fail "$tag: autoscaler identity ($evidence)"
      ;;
  esac
  return 0
}

# CPU request (millicores) for each of the two load pods: 90% of what the
# group's worker has not already promised to its pods. One pod then fits
# beside what the worker runs; the second cannot (two of them need 180% of
# that), so it stays Pending until the autoscaler adds a node - where it
# fits, a fresh node carrying only DaemonSet pods, which the worker already
# counts in its requests. Required pod anti-affinity keeps one pod per node
# on top, so even a mis-sized request leaves exactly one pod Pending, not
# two (two would need a third node, past max 2). Logs go to stderr: stdout
# is the value.
as_load_cpu() {
  local id="$1" group="$2" node alloc used truncated pods_json free cpu
  if [ -n "$AUTOSCALING_LOAD_CPU_MILLICORES" ]; then
    log "  load sizing: AUTOSCALING_LOAD_CPU_MILLICORES=${AUTOSCALING_LOAD_CPU_MILLICORES}m per pod" >&2
    printf '%s\n' "$AUTOSCALING_LOAD_CPU_MILLICORES"
    return 0
  fi
  read -r node alloc <<<"$(as_nodes_json "$id" | jq -r --arg g "$group" "$AS_JQ_LIB [group_nodes(\$g) | select(.ready)] | first | \"\(.name) \(.cpu_m)\"" 2>/dev/null)"
  case "$alloc" in ''|*[!0-9]*) log "  load sizing: no Ready node of $group to size against" >&2; return 1 ;; esac
  if ! pods_json="$(ank_out cluster get resources Pod -A --cluster "$id" -o json)"; then
    log "  load sizing: could not list the cluster's pods" >&2; return 1
  fi
  read -r used truncated <<<"$(printf '%s' "$pods_json" | jq -r --arg n "$node" "$AS_JQ_LIB node_requests(\$n)" 2>/dev/null)"
  case "$used" in ''|*[!0-9]*) log "  load sizing: could not read the CPU requested on $node" >&2; return 1 ;; esac
  if [ "$truncated" = "true" ]; then
    cpu=$(( alloc / 10 ))
    log "  load sizing: the pod listing is truncated, so $node's free CPU is unknown: ${cpu}m per pod (10% of ${alloc}m); anti-affinity keeps the second pod off the first one's node" >&2
  else
    free=$(( alloc - used )); cpu=$(( free * 9 / 10 ))
    log "  load sizing: $node allocatable ${alloc}m, requested ${used}m, free ${free}m -> ${cpu}m per pod" >&2
  fi
  if [ "$cpu" -lt 50 ]; then
    log "  load sizing: ${cpu}m leaves no room for a load pod on $node" >&2; return 1
  fi
  printf '%s\n' "$cpu"
}

# The ImportCluster document the load is staged from: one stack holding one
# Deployment of two pause pods, each requesting $cpu millicores, pinned to
# the group's nodes (by its label, or off the control plane when no node
# carries it) and kept one per node by required anti-affinity.
as_load_import() {
  local cluster_name="$1" group="$2" cpu="$3" labelled="$4" node_terms
  if [ "$labelled" = "true" ]; then
    node_terms="                              - key: $AS_NODE_GROUP_LABEL
                                operator: In
                                values: [\"$group\"]"
  else
    node_terms="                              - key: node-role.kubernetes.io/control-plane
                                operator: DoesNotExist
                              - key: node-role.kubernetes.io/master
                                operator: DoesNotExist"
  fi
  cat <<EOF
apiVersion: v1
kind: ImportCluster
metadata:
  name: $cluster_name
spec:
  stacks:
    - name: $AS_LOAD_NAME
      manifests:
        - name: $AS_LOAD_NAME
          namespace: $AS_LOAD_NAMESPACE
          parents: []
          manifest: |
            apiVersion: apps/v1
            kind: Deployment
            metadata:
              name: $AS_LOAD_NAME
              namespace: $AS_LOAD_NAMESPACE
              labels:
                app.kubernetes.io/name: $AS_LOAD_NAME
                app.kubernetes.io/part-of: ankra-systemtest
            spec:
              replicas: 2
              selector:
                matchLabels:
                  app.kubernetes.io/name: $AS_LOAD_NAME
              template:
                metadata:
                  labels:
                    app.kubernetes.io/name: $AS_LOAD_NAME
                spec:
                  terminationGracePeriodSeconds: 0
                  affinity:
                    nodeAffinity:
                      requiredDuringSchedulingIgnoredDuringExecution:
                        nodeSelectorTerms:
                          - matchExpressions:
$node_terms
                    podAntiAffinity:
                      requiredDuringSchedulingIgnoredDuringExecution:
                        - labelSelector:
                            matchLabels:
                              app.kubernetes.io/name: $AS_LOAD_NAME
                          topologyKey: kubernetes.io/hostname
                  containers:
                    - name: pause
                      image: registry.k8s.io/pause:3.10
                      resources:
                        requests:
                          cpu: ${cpu}m
                          memory: 16Mi
EOF
}

# Remove the load: the stack (which removes its Deployment), then the
# Deployment directly in case the stack delete did not take. Idempotent:
# a stack or Deployment that is already gone is not an error here.
as_delete_load() {
  local name="$1" id="$2"
  as_write "load stack delete" "$name" cluster stacks delete "$AS_LOAD_NAME" --yes --cluster "$id" \
    || log "  (deleting stack $AS_LOAD_NAME did not succeed; deleting the Deployment directly)"
  ank_rc cluster delete deployment "$AS_LOAD_NAME" -n "$AS_LOAD_NAMESPACE" --yes --cluster "$id" >/dev/null || true
}

as_diagnostics() {
  local id="$1" pod
  log "  diagnostics: node groups"
  ank cluster node-group list "$id" | sed 's/^/    /'
  log "  diagnostics: load pods"
  ank cluster get pods -n "$AS_LOAD_NAMESPACE" --name "$AS_LOAD_NAME" --cluster "$id" | sed 's/^/    /'
  pod="$(as_ca_pod "$id")"
  if [ -n "$pod" ]; then
    log "  diagnostics: newest scaling and node-identity lines of $pod"
    ank cluster logs "$pod" -n "$AS_CA_NAMESPACE" --all-containers --tail 400 --follow=false --cluster "$id" \
      | grep -iE 'scale.?up|scale.?down|unregistered|nodegroup is nil|unneeded|increase|delete|error|fail' \
      | tail -n 25 | sed 's/^/    /'
  fi
}

# Step 4: stage and deploy the load, then wait for the group to reach 2
# Ready workers with both load pods Running. The worker count never passing
# max 2 is asserted on every poll (the held-join defect let each scale-up
# spawn another billed server).
as_scale_up() {
  local name="$1" id="$2" group="$3" tag="$4"
  local cpu file labelled start deadline count="?" ready="?" total="?" running="?" pending="?" max_seen=1 value
  if ! cpu="$(as_load_cpu "$id" "$group")"; then
    fail "$tag load sizing (see the load sizing line above)"; return 1
  fi
  labelled="$(as_nodes_json "$id" | jq -r "$AS_JQ_LIB labelled" 2>/dev/null)"
  file="$WORKDIR/autoscaling-load.$id.yaml"
  as_load_import "$name" "$group" "$cpu" "$labelled" > "$file"
  log "  load: $AS_LOAD_NAMESPACE/$AS_LOAD_NAME, 2 pods x ${cpu}m CPU, one per node (stack $AS_LOAD_NAME, from $file)"
  if ! as_write "load draft" "$name" cluster draft -f "$file"; then
    fail "$tag load (ankra cluster draft refused the $AS_LOAD_NAME stack)"; return 1
  fi
  if ! as_write "load deploy" "$name" cluster stacks deploy-draft "$AS_LOAD_NAME" --cluster "$id"; then
    fail "$tag load (ankra cluster stacks deploy-draft refused the $AS_LOAD_NAME stack)"; return 1
  fi
  start=$(date +%s); deadline=$(( start + AUTOSCALING_SCALE_UP_TIMEOUT ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    read -r count ready total <<<"$(as_group_state "$id" "$group")"
    running="$(as_load_pods "$id" Running)"; pending="$(as_load_pods "$id" Pending)"
    log "  $group count=$count nodes ready=$ready/$total load running=${running:-?} pending=${pending:-?} (want 2, 2/2, 2)"
    for value in "$count" "$total"; do
      case "$value" in ''|*[!0-9]*) ;; *) if [ "$value" -gt "$max_seen" ]; then max_seen="$value"; fi ;; esac
    done
    if [ "$max_seen" -gt 2 ]; then
      fail "$tag max_count did not bind: $group reached $max_seen workers/nodes with max 2"
      as_diagnostics "$id"
      return 1
    fi
    if [ "$count" = 2 ] && [ "$ready" = 2 ] && [ "$total" = 2 ] && [ "$running" = 2 ]; then
      pass "$tag scale-up 1->2 under pending pods, both scheduled ($(( $(date +%s) - start ))s)"
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  fail "$tag scale-up did not finish in ${AUTOSCALING_SCALE_UP_TIMEOUT}s (count=$count nodes ready=$ready/$total load running=${running:-?} pending=${pending:-?})"
  as_diagnostics "$id"
  return 1
}

# Step 5: remove the load and wait for the autoscaler to shrink the group
# back to 1 - asserting on every poll that it never goes below min 1.
as_scale_down() {
  local name="$1" id="$2" group="$3" tag="$4"
  local start deadline count="?" ready="?" total="?" pods="?" reissued=0
  as_delete_load "$name" "$id"
  start=$(date +%s); deadline=$(( start + AUTOSCALING_SCALE_DOWN_TIMEOUT ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    read -r count ready total <<<"$(as_group_state "$id" "$group")"
    pods="$(as_load_pods "$id" "")"
    log "  $group count=$count nodes ready=$ready/$total load pods=${pods:-?} (want 1, 1/1, 0)"
    if [ "$count" = 0 ] || [ "$total" = 0 ]; then
      fail "$tag min_count did not bind: $group reached count=$count nodes=$total with min 1"
      as_diagnostics "$id"
      return 1
    fi
    if [ "${pods:-?}" != 0 ] && [ "$reissued" = 0 ] && [ $(( $(date +%s) - start )) -ge 300 ]; then
      log "  load pods still present after 300s; deleting the Deployment directly"
      ank_rc cluster delete deployment "$AS_LOAD_NAME" -n "$AS_LOAD_NAMESPACE" --yes --cluster "$id" >/dev/null || true
      reissued=1
    fi
    if [ "$count" = 1 ] && [ "$ready" = 1 ] && [ "$total" = 1 ] && [ "$pods" = 0 ]; then
      pass "$tag scale-down 2->1 after the load left ($(( $(date +%s) - start ))s)"
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  fail "$tag scale-down did not finish in ${AUTOSCALING_SCALE_DOWN_TIMEOUT}s (count=$count nodes ready=$ready/$total load pods=${pods:-?})"
  as_diagnostics "$id"
  return 1
}

# Undo whatever the step left: the load, autoscaling, a second worker.
as_cleanup() {
  local name="$1" id="$2" group="$3" tag="$4" count
  log "  $tag cleanup: removing the load, disabling autoscaling, returning $group to 1 worker"
  as_delete_load "$name" "$id"
  as_write "autoscaling disable (cleanup)" "$name" cluster node-group autoscaling set "$id" "$group" --enabled=false \
    || log "  (cleanup: disabling autoscaling on $group was refused)"
  count="$(as_group_count "$id" "$group")"
  if [ "$count" = 1 ]; then return 0; fi
  as_write "scale $group to 1 (cleanup)" "$name" cluster node-group scale "$id" "$group" 1 \
    || log "  (cleanup: scaling $group to 1 was refused)"
  if as_wait_group "$id" "$group" 1 "$DAYTWO_TIMEOUT"; then
    log "  cleanup: $group is back at 1 worker"
  else
    fail "$tag cleanup could not return $group to 1 worker (count/ready/nodes: $(as_group_state "$id" "$group")); the deprovision removes it with the cluster"
  fi
}

# The opt-in step itself, called by each Ankra-managed lane just before its
# deprovision. Records a SKIP when not opted in, so a run's results say that
# autoscaling was not exercised.
run_autoscaling_step() {
  local name="$1" id="$2" label="$3"
  local tag="$label autoscaling" group="$AUTOSCALING_NODE_GROUP" count
  if [ "$ANKRA_SYSTEMTEST_AUTOSCALING" != "1" ]; then
    skip "$tag (opt-in: ANKRA_SYSTEMTEST_AUTOSCALING=1)"
    return 0
  fi
  log "autoscaling step on $name: node group $group, min 1 / max 2"

  # Baseline: exactly one Ready worker, so max 2 leaves room for exactly one
  # scale-up and the end state can be compared with the start.
  if ! as_wait_group "$id" "$group" 1 "$DAYTWO_TIMEOUT"; then
    fail "$tag baseline ($group is not at exactly 1 Ready worker; count/ready/nodes: $(as_group_state "$id" "$group"); node groups: $(as_group_names "$id"); set AUTOSCALING_NODE_GROUP to pick another)"
    return 0
  fi

  # 1. Enable.
  if ! as_write "autoscaling enable" "$name" cluster node-group autoscaling set "$id" "$group" --enabled=true --min 1 --max 2; then
    fail "$tag enable (the write was refused)"
    as_cleanup "$name" "$id" "$group" "$tag"; return 0
  fi
  if ! as_wait_settings "$id" "$group" "true 1 2"; then
    fail "$tag enable (the settings did not read back as enabled, min 1, max 2)"
    as_cleanup "$name" "$id" "$group" "$tag"; return 0
  fi
  pass "$tag enabled on $group (min 1, max 2)"

  # 2. The autoscaler the first enable installs is Ready.
  if ! as_wait_ca_ready "$id"; then
    fail "$tag $AS_CA_NAMESPACE/$AS_CA_NAME not Ready within ${AUTOSCALING_CA_READY_TIMEOUT}s"
    as_cleanup "$name" "$id" "$group" "$tag"; return 0
  fi
  pass "$tag $AS_CA_NAMESPACE/$AS_CA_NAME Ready"

  # 3. It registers the worker it has.
  as_assert_ca_identity "$id" "$group" "$tag at 1 worker"

  # 4. Scale-up under pending pods, and the new worker is registered too.
  if ! as_scale_up "$name" "$id" "$group" "$tag"; then
    as_cleanup "$name" "$id" "$group" "$tag"; return 0
  fi
  as_assert_ca_identity "$id" "$group" "$tag at 2 workers"

  # 5. Scale-down once the load is gone.
  if ! as_scale_down "$name" "$id" "$group" "$tag"; then
    as_cleanup "$name" "$id" "$group" "$tag"; return 0
  fi

  # 6. Disable, and nothing extra is left running.
  if ! as_write "autoscaling disable" "$name" cluster node-group autoscaling set "$id" "$group" --enabled=false \
     || ! as_wait_settings "$id" "$group" "false"; then
    fail "$tag disable"
    as_cleanup "$name" "$id" "$group" "$tag"; return 0
  fi
  pass "$tag disabled on $group"
  count="$(as_group_count "$id" "$group")"
  if [ "$count" = 1 ]; then
    pass "$tag left $group at 1 worker (no extra billed server)"
  else
    fail "$tag left $group at count=${count:-unreadable}, want 1"
    as_cleanup "$name" "$id" "$group" "$tag"
  fi
  return 0
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

  # 7b. Node-group autoscaling end to end (opt-in: ANKRA_SYSTEMTEST_AUTOSCALING=1;
  # records a SKIP otherwise). Last before the deprovision, so a failure in it
  # cannot leave a second worker under the steps above.
  run_autoscaling_step "$name" "$id" "$label"

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
  #  and volumes on Hetzner / OVH / UpCloud / DigitalOcean (and EC2 plus a  #
  #  VPC/NAT in your AWS account when aws is selected) plus provider-native #
  #  managed clusters                                                        #
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
        [ -n "$AWS_REGION" ] || die "AWS_REGION required for aws"
        # The network is created by default; a VPC is adopted only when all
        # three of its variables are set, and a partial set is a mistake
        # rather than a created-network run with stray subnets.
        if aws_adopts_vpc; then
          [ -n "$AWS_NODE_SUBNET_IDS" ] || die "AWS_NODE_SUBNET_IDS required with AWS_VPC_ID (adopting a VPC)"
          [ -n "$AWS_BASTION_SUBNET_ID" ] || die "AWS_BASTION_SUBNET_ID required with AWS_VPC_ID (adopting a VPC)"
          case "$AWS_EGRESS_MODE" in
            ""|existing|bastion_nat) ;;
            *) die "AWS_EGRESS_MODE=$AWS_EGRESS_MODE is not valid for an adopted VPC (want existing or bastion_nat)" ;;
          esac
        else
          if [ -n "$AWS_NODE_SUBNET_IDS" ] || [ -n "$AWS_BASTION_SUBNET_ID" ]; then
            die "AWS_NODE_SUBNET_IDS/AWS_BASTION_SUBNET_ID only apply with AWS_VPC_ID (set all three to adopt a VPC, or none to let Ankra create the network)"
          fi
          case "$AWS_EGRESS_MODE" in
            ""|nat_gateway|bastion_nat) ;;
            *) die "AWS_EGRESS_MODE=$AWS_EGRESS_MODE is not valid for a created network (want nat_gateway or bastion_nat)" ;;
          esac
        fi
        # The leak check is the lane's proof that the network Ankra created
        # is gone (or, for an adopted VPC, that it came back untouched), so
        # the AWS CLI and the account's own credentials are not optional.
        if ! aws_cli_available; then
          die "aws: $aws_cli_reason -> set AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY (the account's own keys) so the leak check can run; the lane proves nothing without it"
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
  case "$ANKRA_SYSTEMTEST_AUTOSCALING" in
    0) ;;
    1)
      command -v jq >/dev/null 2>&1 || die "ANKRA_SYSTEMTEST_AUTOSCALING=1 needs jq (the autoscaling step parses the CLI's -o json output)"
      case "$AUTOSCALING_LOAD_CPU_MILLICORES" in
        *[!0-9]*) die "AUTOSCALING_LOAD_CPU_MILLICORES=$AUTOSCALING_LOAD_CPU_MILLICORES (want whole millicores, e.g. 1500)" ;;
      esac
      if [ -z "$ANKRA_SYSTEMTEST_PROVIDERS" ]; then
        log "WARNING: ANKRA_SYSTEMTEST_AUTOSCALING=1 but no Ankra-managed provider is selected; the autoscaling step runs on Ankra-managed lanes only"
      else
        log "autoscaling step ON (node group $AUTOSCALING_NODE_GROUP, min 1 / max 2): adds roughly 25-50 minutes and one extra worker for ~15-25 of them per Ankra-managed lane"
      fi
      ;;
    *) die "ANKRA_SYSTEMTEST_AUTOSCALING=$ANKRA_SYSTEMTEST_AUTOSCALING (want 0 or 1)" ;;
  esac
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
