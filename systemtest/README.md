# Ankra cloud lifecycle system test

`lifecycle_systemtest.sh` is a real, end-to-end system test that drives the
`ankra` CLI against a live platform and provisions **real** clusters across both
cluster families the platform supports:

- **Ankra-managed** (self-managed k3s/kubeadm on provider VMs): Hetzner, OVH,
  UpCloud and DigitalOcean, via `ankra cluster <provider> create` and the
  generic day-2 verbs - plus, opt-in, **AWS** (self-managed k3s on EC2 in a
  network Ankra creates), which runs its own shorter lane focused on the
  network the provider owns and must remove again (see below).
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

## AWS lane (opt-in, per distribution)

`aws` in `ANKRA_SYSTEMTEST_PROVIDERS` runs a different lane, because the AWS
provider owns its network: by default Ankra **creates** the whole thing (VPC,
subnets, internet gateway, route tables, NAT) and the thing to prove is that
it removes every piece of it with the cluster. The lane therefore needs only
credentials - no pre-made VPC. Setting `AWS_VPC_ID` (with
`AWS_NODE_SUBNET_IDS` and `AWS_BASTION_SUBNET_ID`) switches it to **adopting**
that VPC, where the thing to prove is instead that the VPC comes back exactly
as it was found.

1. **VPC snapshot** (adopted mode only) with the AWS CLI: the VPC's route
   tables (ids, routes, subnet associations), subnets and DHCP options,
   normalised so a legitimate no-op diffs clean (association *ids* are
   dropped: re-associating a subnet with its original table mints a new id
   while the routing is identical)
2. **preflight** with exactly the flags `create` will send; a failed preflight
   ends the lane before anything is built, and so does a preflight that does
   not report the expected `Network ownership` (`created`, or `adopted` when
   a VPC was named) or, for a created network, the zones it resolved
3. **create** with 1 control plane + 1 worker (`t3.medium`, `t3.small` bastion,
   bastion SSH allowed only from `AWS_BASTION_ALLOWED_IPS`, default: the
   runner's public IP `/32`). A created network uses `--egress-mode
   bastion_nat` by default - the bastion is the NAT, so CI pays for no NAT
   gateway; `AWS_EGRESS_MODE=nat_gateway` exercises the gateways instead
   (`AWS_NAT_GATEWAY_SINGLE_ZONE=1` for one gateway rather than one per
   zone). The server picks one zone for this 1-control-plane cluster; three
   zones are built only when `AWS_AVAILABILITY_ZONES` asks for them, and
   `AWS_NETWORK_IP_RANGE` overrides the server's `10.0.0.0/16`
4. wait until the cluster is **online** and both nodes are **Ready**
5. **access-info** shows a bastion IP and a control plane IP (the CLI renders
   a null as `-`, which fails the step)
6. **node list** (`ankra cluster nodes list`) shows the control plane and the
   worker
7. **stop** → state `stopped`, then **start** → online with both nodes Ready
8. **deprovision** and confirm the cluster record is removed
9. **leak check**: poll (bounded by `AWS_LEAK_TIMEOUT`, default 900s) until
   nothing tagged `ankra.cloud/cluster-id=<id>` remains - instances (any
   state but terminated), security groups, key pairs, volumes, IAM
   roles/instance profiles, **and the created network: the VPC, subnets,
   internet gateway, NAT gateways (any state but deleted), route tables and
   elastic IPs** - plus a Resource Groups Tagging API sweep for any other
   kind. The created network is Ankra's and must be gone
10. **VPC untouched** (adopted mode only): a second snapshot must be
    identical to the first (the Ankra route table of `bastion_nat` mode is
    absent from the first by construction, so its survival shows up as an
    extra entry)

The leak check and the VPC diff use the AWS CLI with the *account's* own
credentials (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`, a profile, or an
ambient role - read-only EC2/IAM/tagging permissions suffice), not Ankra's.
They are **required**: the leak check is the lane's proof that the network
Ankra created is gone, so the preflight refuses to run the lane without the
CLI, `jq` and usable credentials rather than recording a `SKIP` that proves
nothing. The Ankra credential is either an existing one (`AWS_CREDENTIAL_ID`)
or registered for the run with `ankra credentials aws create-role` from
`AWS_ROLE_ARN` + `AWS_EXTERNAL_ID` (scope `AWS_CREDENTIAL_SCOPE`, default
`self_managed`) and deleted at the end.

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
| `AWS_CREDENTIAL_ID`, or `AWS_ROLE_ARN` + `AWS_EXTERNAL_ID` | (aws only) an Ankra aws credential id - a role onboarded with scope `self_managed`, or keys - or the role to register one from for the run |
| `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` (or any AWS CLI credential source) | (aws only) the account's own read-only keys for the leak check that proves the created network is gone; the lane refuses to run without a working AWS CLI |
| `AWS_REGION` | (aws only) the region to build in; the script defaults to `eu-west-1`, the CI workflow requires `SYSTEMTEST_AWS_REGION` explicitly |

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
| `ANKRA_SYSTEMTEST_PROVIDERS` | `hetzner ovh upcloud digitalocean` (set to `""` to skip the Ankra-managed family; add `aws` for the opt-in AWS lane) |
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
| `AWS_CP_TYPE` / `AWS_WORKER_TYPE` / `AWS_BASTION_TYPE` | `t3.medium` / `t3.medium` / `t3.small` |
| `AWS_BASTION_ALLOWED_IPS` (comma-separated CIDRs) / `AWS_CREDENTIAL_SCOPE` / `AWS_LEAK_TIMEOUT` | runner public IP `/32` / `self_managed` / `900` |
| `AWS_EGRESS_MODE` (created network: `bastion_nat` or `nat_gateway`; adopted VPC: `existing` or `bastion_nat`) | `bastion_nat` for a created network / resolved by preflight for an adopted VPC |
| `AWS_NETWORK_IP_RANGE` / `AWS_AVAILABILITY_ZONES` (comma-separated) / `AWS_NAT_GATEWAY_SINGLE_ZONE` (`1`) | created network only: server's `10.0.0.0/16` / server's choice (one zone for this cluster; three only when asked) / `0` |
| `AWS_VPC_ID` + `AWS_NODE_SUBNET_IDS` (comma-separated private subnets) + `AWS_BASTION_SUBNET_ID` (a public subnet) | unset (Ankra creates the network); set all three to adopt a VPC you own instead |
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
ankra cluster aws availability-zones --credential-id <id> --region eu-west-1   # for AWS_AVAILABILITY_ZONES
ankra cluster aws vpcs --credential-id <id> --region eu-west-1                 # adopted mode only
ankra cluster aws subnets --credential-id <id> --region eu-west-1 --vpc-id vpc-...   # egress kind per subnet
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

# AWS lane only: k3s in a network Ankra creates (needs the AWS CLI for the leak check)
export AWS_CREDENTIAL_ID=... AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_REGION=eu-west-1
ANKRA_SYSTEMTEST_PROVIDERS=aws ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="" ./systemtest/lifecycle_systemtest.sh

# ... with NAT gateways across three zones instead of the bastion NAT in one
AWS_EGRESS_MODE=nat_gateway AWS_AVAILABILITY_ZONES=eu-west-1a,eu-west-1b,eu-west-1c \
  ANKRA_SYSTEMTEST_PROVIDERS=aws ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="" ./systemtest/lifecycle_systemtest.sh

# ... or adopting a VPC you own (adds the VPC-untouched diff)
AWS_VPC_ID=vpc-... AWS_NODE_SUBNET_IDS=subnet-a,subnet-b AWS_BASTION_SUBNET_ID=subnet-c \
  ANKRA_SYSTEMTEST_PROVIDERS=aws ANKRA_SYSTEMTEST_MANAGED_PROVIDERS="" ./systemtest/lifecycle_systemtest.sh
```

By default the selected targets run **concurrently** within a single invocation
(`ANKRA_SYSTEMTEST_PARALLEL=1`), so a full run takes roughly as long as the
slowest single target rather than the sum of all of them. Each parallel worker
copies the base CLI config to its own file and runs with `--config`, so
concurrent `cluster select` writes never clobber a sibling worker's selection.
Per-target logs and result files are written under a `mktemp -d` work directory
printed at the start of the run; output on the console is line-tagged
`[provider-distribution]` (cloud-managed targets use `[provider-managed]`).

## Scheduled runs in CI

`.github/workflows/systemtest.yml` runs this suite at 03:00 UTC every Monday,
Wednesday and Friday, and on manual dispatch. Every value in its `env:` block
comes from a repository secret or variable named `SYSTEMTEST_*` — the same
names as the environment variables above, prefixed:

| Kind | Names |
|---|---|
| Secrets | `SYSTEMTEST_ANKRA_API_TOKEN`, `SYSTEMTEST_SSH_KEY_CREDENTIAL_ID`, `SYSTEMTEST_HETZNER_CREDENTIAL_ID`, `SYSTEMTEST_OVH_CREDENTIAL_ID`, `SYSTEMTEST_UPCLOUD_CREDENTIAL_ID`, `SYSTEMTEST_DIGITALOCEAN_CREDENTIAL_ID`, `SYSTEMTEST_GKE_CREDENTIAL_ID`, `SYSTEMTEST_AKS_CREDENTIAL_ID`, `SYSTEMTEST_EKS_CREDENTIAL_ID` |
| Variables | `SYSTEMTEST_ANKRA_BASE_URL`, `SYSTEMTEST_ANKRA_ORG`, `SYSTEMTEST_GITOPS_CREDENTIAL_NAME`, `SYSTEMTEST_GITOPS_REPOSITORY` |
| Secrets (AWS lane, opt-in) | `SYSTEMTEST_AWS_CREDENTIAL_ID` *or* `SYSTEMTEST_AWS_ROLE_ARN` + `SYSTEMTEST_AWS_EXTERNAL_ID`; `SYSTEMTEST_AWS_ACCESS_KEY_ID` + `SYSTEMTEST_AWS_SECRET_ACCESS_KEY` (the account's read-only keys for the leak check - required, it is the proof the created network is gone) |
| Variables (AWS lane, opt-in) | `SYSTEMTEST_AWS_REGION` (required), `SYSTEMTEST_AWS_BASTION_ALLOWED_IPS` (the script falls back to the runner's public IP `/32`); optional `SYSTEMTEST_AWS_EGRESS_MODE`, `SYSTEMTEST_AWS_AVAILABILITY_ZONES`, `SYSTEMTEST_AWS_NETWORK_IP_RANGE`; and, to adopt a VPC instead of creating one, `SYSTEMTEST_AWS_VPC_ID` + `SYSTEMTEST_AWS_NODE_SUBNET_IDS` + `SYSTEMTEST_AWS_BASTION_SUBNET_ID` |

The AWS lane joins the scheduled matrix only when its credential (or role),
region and leak-check keys exist - no VPC is needed, Ankra creates the
network; a manual dispatch that fills in `providers` has to name `aws`
itself.

None of them are defaulted, because a run provisions real, billable
infrastructure and the target org must be a deliberate choice. A repository
without `SYSTEMTEST_ANKRA_API_TOKEN` therefore skips the job with a notice
rather than failing: an unconfigured repository is not a broken build. If the
token is present but a provider credential is missing, the preflight still
fails loudly — that repository *is* misconfigured.

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
