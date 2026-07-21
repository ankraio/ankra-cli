# Ankra cloud lifecycle system test

`lifecycle_systemtest.sh` is a real, end-to-end system test that drives the
`ankra` CLI against a live platform and provisions **real** clusters across both
cluster families the platform supports:

- **Ankra-managed** (self-managed k3s/kubeadm on provider VMs): Hetzner, OVH,
  UpCloud and DigitalOcean, via `ankra cluster <provider> create` and the
  generic day-2 verbs.
- **Cloud-managed** (provider-native managed Kubernetes): DOKS, UKS, GKE,
  OVH MKS, AKS and EKS, via `ankra cluster managed`.

## Ankra-managed lifecycle (per provider x distribution)

1. **create** with the external cloud provider + GitOps, so the cloud-provider
   stack (CCM, CSI, Traefik, cert-manager) is installed
2. wait until the cluster is **online** and the control-plane + worker nodes are **Ready**
3. confirm the **stack addons reach `up`**
4. **scale** workers up (1 → 3) and down (3 → 1)
5. **node group** add (2 nodes) then delete
6. **Kubernetes upgrade** to a newer k3s/kubeadm version
7. **instance resize** of the default node group to a bigger plan
8. **deprovision** and confirm the cluster record is removed (`deleted_at`)

## Cloud-managed lifecycle (per managed provider)

1. **create** with an initial 1-node pool (`workers`), optional GitOps
2. wait until the cluster is **online** and the pool's nodes are **Ready**
   (the control plane is provider-hosted, so only workers appear as nodes)
3. **node pool scale** up (1 → 3) and down (3 → 1)
4. **node pool** add (`pool-b`, 2 nodes) then delete
5. **Kubernetes upgrade** — only when `MANAGED_UPGRADE_K8S_VERSION_<PROVIDER>`
   is set (the CLI has no managed version listing, so the target is explicit);
   otherwise the step is recorded as `SKIP`
6. **delete** and confirm the cluster record is removed

It is deliberately a thin wrapper over the exact CLI commands a customer runs, so
it is "as real as possible". It tolerates the real behaviours of the platform:

- transient provisioning timeouts (slow bastion/server boot) → it retries the
  reconcile instead of failing
- the platform serialises writes (HTTP 409 while a reconcile runs, or a managed
  cluster reporting "not in a state that allows ...") → it waits and retries the
  day-2 operation
- on any failure or interrupt it **deprovisions every cluster it created**
  (using the right verb per family), so it never leaks paid infrastructure

## Prerequisites

- A built `ankra` binary. By default the script uses the repo build at
  `../bin/ankra` (run `go build -o bin/ankra .` in `ankra-cli/`), or set `ANKRA_BIN`.
- A logged-in CLI (`ankra login`) or `ANKRA_API_TOKEN`, pointed at the target
  platform (`base-url` in `~/.ankra.yaml`, default `https://platform.ankra.dev`).
- Provider credentials (and, for Ankra-managed providers, an SSH-key credential)
  already stored in the Ankra org.
- A GitOps GitHub credential + repository for the generated cloud-provider stack.

## Configuration (environment variables)

Required:

| Variable | Meaning |
|---|---|
| `ANKRA_SYSTEMTEST_CONFIRM` | must be `yes` to acknowledge that real, billable infrastructure will be provisioned; the script refuses to run otherwise |
| `SSH_KEY_CREDENTIAL_ID` | SSH-key credential ID (required when any Ankra-managed provider is selected) |
| `HETZNER_CREDENTIAL_ID` / `OVH_CREDENTIAL_ID` / `UPCLOUD_CREDENTIAL_ID` / `DIGITALOCEAN_CREDENTIAL_ID` | provider API credential ID (per selected Ankra-managed provider) |
| `GKE_CREDENTIAL_ID` / `AKS_CREDENTIAL_ID` / `EKS_CREDENTIAL_ID` | cloud credential ID (per selected hyperscaler managed provider) |

Cloud-managed credential fallbacks: `DOKS_CREDENTIAL_ID` defaults to
`DIGITALOCEAN_CREDENTIAL_ID`, `UKS_CREDENTIAL_ID` to `UPCLOUD_CREDENTIAL_ID`
and `OVH_MKS_CREDENTIAL_ID` to `OVH_CREDENTIAL_ID` — the platform reuses the
same credential kind for those pairs.

Optional GitOps (commits the generated cloud-provider stack to Git; the stack still installs without it):

| Variable | Meaning |
|---|---|
| `GITOPS_CREDENTIAL_NAME` + `GITOPS_REPOSITORY` | GitOps target for the cloud-provider stack |

Common optional (defaults in parentheses):

| Variable | Default |
|---|---|
| `ANKRA_SYSTEMTEST_PROVIDERS` | `hetzner ovh upcloud digitalocean` (set to `""` to skip the Ankra-managed family) |
| `ANKRA_SYSTEMTEST_MANAGED_PROVIDERS` | `doks uks gke ovh_mks aks eks` (set to `""` to skip the cloud-managed family) |
| `ANKRA_SYSTEMTEST_DISTRIBUTIONS` | `k3s` (Ankra-managed only; set `"k3s kubeadm"` to matrix-test both) |
| `ANKRA_SYSTEMTEST_PARALLEL` | `1` (run selected targets concurrently; set `0` for one-at-a-time) |
| `ANKRA_CONFIG_FILE` | `~/.ankra.yaml` (base config parallel workers copy for auth/org) |
| `ANKRA_BIN` | `../bin/ankra` then `ankra` on PATH |
| `GITOPS_BRANCH` | `master` |
| `HETZNER_LOCATION` / `OVH_REGION` / `UPCLOUD_ZONE` / `DIGITALOCEAN_REGION` | `nbg1` / `GRA9` / `de-fra1` / `nyc3` |
| `HETZNER_CP_TYPE` / `HETZNER_WORKER_TYPE` / `HETZNER_BASTION_TYPE` / `HETZNER_BIGGER_TYPE` | `cpx32` / `cpx22` / `cpx22` / `cpx32` |
| `OVH_CP_FLAVOR` / `OVH_WORKER_FLAVOR` / `OVH_BIGGER_FLAVOR` | `b2-15` / `b2-15` / `b2-30` |
| `OVH_GATEWAY_FLAVOR` (NAT gateway instance; `b2-7` is unavailable in some regions e.g. `EU-WEST-PAR`, set a `b3-*` there) | `b2-7` |
| `UPCLOUD_CP_PLAN` / `UPCLOUD_WORKER_PLAN` / `UPCLOUD_BIGGER_PLAN` | `2xCPU-4GB` / `2xCPU-4GB` / `4xCPU-8GB` |
| `DIGITALOCEAN_BASTION_SIZE` / `DIGITALOCEAN_CP_SIZE` / `DIGITALOCEAN_WORKER_SIZE` / `DIGITALOCEAN_BIGGER_SIZE` | `s-1vcpu-1gb` / `s-2vcpu-4gb` / `s-2vcpu-4gb` / `s-4vcpu-8gb` |
| `DOKS_LOCATION` / `UKS_LOCATION` / `OVH_MKS_LOCATION` / `GKE_LOCATION` / `AKS_LOCATION` / `EKS_LOCATION` | `$DIGITALOCEAN_REGION` / `$UPCLOUD_ZONE` / `$OVH_REGION` / `europe-west1` / `westeurope` / `eu-west-1` |
| `DOKS_NODE_POOL_SIZE` / `UKS_NODE_POOL_SIZE` / `OVH_MKS_NODE_POOL_SIZE` / `GKE_NODE_POOL_SIZE` / `AKS_NODE_POOL_SIZE` / `EKS_NODE_POOL_SIZE` | `s-2vcpu-4gb` / `2xCPU-4GB` / `b2-15` / `e2-standard-2` / `Standard_D2s_v3` / `t3.medium` |
| `MANAGED_CREATE_K8S_VERSION_<PROVIDER>` / `MANAGED_UPGRADE_K8S_VERSION_<PROVIDER>` | unset (e.g. `MANAGED_UPGRADE_K8S_VERSION_DOKS`; upgrade step is skipped without a target) |
| `K8S_UPGRADE_TARGET` | highest version from `ankra cluster k3s-versions` / `kubeadm-versions` (Ankra-managed only) |
| `ETCD_TOPOLOGY` | `stacked` (kubeadm only; `stacked` or `external`) |
| `ONLINE_TIMEOUT` / `ADDONS_TIMEOUT` / `DAYTWO_TIMEOUT` / `DEPROVISION_TIMEOUT` | `1500` / `900` / `900` / `1500` (seconds) |
| `DEPROVISION_FORCE_TIMEOUT` | `600` (bounded force-deprovision fallback if a graceful deprovision stalls) |

Discover valid values with the CLI:

```bash
ankra credentials list
ankra cluster hetzner server-types --credential-id <id> --location nbg1 --available-only
ankra cluster hetzner locations --credential-id <id>
ankra cluster ovh regions --credential-id <id>
ankra cluster k3s-versions
ankra cluster kubeadm-versions
```

## Running

```bash
cd ankra-cli && go build -o bin/ankra .

export ANKRA_SYSTEMTEST_CONFIRM=yes   # acknowledge real, billable infrastructure
export SSH_KEY_CREDENTIAL_ID=...
export HETZNER_CREDENTIAL_ID=...
export OVH_CREDENTIAL_ID=...
export UPCLOUD_CREDENTIAL_ID=...
export DIGITALOCEAN_CREDENTIAL_ID=... # also used for doks
export GKE_CREDENTIAL_ID=...
export AKS_CREDENTIAL_ID=...
export EKS_CREDENTIAL_ID=...
export GITOPS_CREDENTIAL_NAME=...     # optional
export GITOPS_REPOSITORY=org/repo     # optional

# full default matrix (all Ankra-managed + all cloud-managed), in parallel
./systemtest/lifecycle_systemtest.sh

# everything, one target at a time
ANKRA_SYSTEMTEST_PARALLEL=0 ./systemtest/lifecycle_systemtest.sh

# one Ankra-managed provider, no cloud-managed
ANKRA_SYSTEMTEST_PROVIDERS=upcloud ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="" \
  ./systemtest/lifecycle_systemtest.sh

# cloud-managed only (DOKS + UKS)
ANKRA_SYSTEMTEST_PROVIDERS="" ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="doks uks" \
  ./systemtest/lifecycle_systemtest.sh

# DigitalOcean, both distributions
ANKRA_SYSTEMTEST_PROVIDERS=digitalocean ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="" \
  ANKRA_SYSTEMTEST_DISTRIBUTIONS="k3s kubeadm" ./systemtest/lifecycle_systemtest.sh
```

By default the selected targets run **concurrently** within a single invocation
(`ANKRA_SYSTEMTEST_PARALLEL=1`), so a full run takes roughly as long as the
slowest single target rather than the sum of all of them. Each parallel worker
copies the base CLI config to its own file and runs with `--config`, so
concurrent `cluster select` writes never clobber a sibling worker's selection.
Per-target logs and result files are written under a `mktemp -d` work directory
printed at the start of the run; output on the console is line-tagged
`[provider-distribution]` (cloud-managed targets use `[provider-managed]`).

## Output

The script prints a per-step `PASS`/`FAIL`/`SKIP` (tagged with the target in
parallel mode), ends with a results list and a summary line, and exits non-zero
if any step failed. Per-target logs are also saved under the run's work
directory.

## Cost & safety

This provisions real, billable cloud infrastructure: VM clusters (1
control-plane + 1 worker, briefly scaled to 3, plus a temporary node group) on
the Ankra-managed providers and provider-native managed clusters (1-node pool,
briefly scaled to 3, plus a temporary second pool) on the cloud-managed
providers. You must set `ANKRA_SYSTEMTEST_CONFIRM=yes` to run it. The run is
short-lived and the script always attempts to tear everything down on exit
(graceful, then `--force` as a fallback so nothing leaks), but verify with
`ankra cluster list` afterwards.
