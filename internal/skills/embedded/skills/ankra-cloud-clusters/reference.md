# Cloud clusters — reference

Choosing the instance family, sizing the roles, and the create-flag map per provider.

**The discovery command is always authoritative.** Families and SKU names change, and a credential
can only deploy what its project is entitled to. Everything below is how to *choose*; run the
listing to find out what exists:

```bash
ankra cluster hetzner server-types --credential-id <id> --location <loc> --available-only
ankra cluster digitalocean sizes   --credential-id <id> --region <r> --available-only
ankra cluster ovh regions          --credential-id <id> --with-zones
ankra cluster proxmox sizes                        # px-small, px-medium, ... presets
ankra cluster morpheus plans       --credential-id <id>
ankra cluster morpheus layouts     --credential-id <id>
```

## Instance families, by provider

| Provider | Family prefix | Shape | Reach for it when |
|----------|---------------|-------|-------------------|
| **Hetzner** | `cpx` | Shared vCPU, AMD | Default workers, control planes, anything not latency-critical (`cpx12` bastion, `cpx22` and up for nodes) |
| | `cx` | Shared vCPU, Intel | Cheaper than `cpx`, but sold in limited quantities and often unavailable per location - check `server-types` first |
| | `cax` | Shared vCPU, Ampere **ARM64** | Cheapest per core — only if every image is multi-arch |
| | `ccx` | **Dedicated** vCPU | Steady CPU-bound work, databases, CI runners, noisy-neighbour sensitivity |
| **DigitalOcean** | `s-` | Basic, shared | Dev clusters, small workers |
| | `g-` / `gd-` | General purpose (dedicated) | Production workers |
| | `c-` | CPU-optimised | Build farms, encoding, CPU-bound services |
| | `m-` | Memory-optimised | In-memory caches, JVM heaps, analytics |
| | `so-` | Storage-optimised | Local-NVMe databases |
| | GPU sizes | GPU | Inference and training — see the GPU section |
| **OVH** | `d2-` | Discovery, small | Labs only |
| | `b2-` / `b3-` | Balanced (general purpose) | Default control planes and workers |
| | `c2-` / `c3-` | CPU-optimised | CPU-bound services |
| | `r2-` / `r3-` | RAM-optimised | Memory-heavy workloads |
| | `t1-` / `t2-` | GPU | Inference and training |
| **UpCloud** | `<N>xCPU-<M>GB` | General purpose plans | Default; the number pair *is* the shape |
| | Developer plans | Small, capped | Labs only — too small for a production control plane |
| | High-CPU / High-Memory plans | Skewed ratios | When the general-purpose ratio wastes one dimension |
| **Proxmox VE** | `px-small`, `px-medium`, … presets | Your own hardware | Sized by what the host node actually has free |
| **Morpheus** | service plans + layouts | Whatever the Morpheus admin published | Ask the Morpheus admin which plan is intended for Kubernetes |

ARM (`cax`, and ARM sizes elsewhere) is the single biggest cost lever and the single most common
way to get a cluster that will not schedule: **every image in every stack** must have an `arm64`
variant, including the platform add-ons. Verify before committing, or stay on x86.

## Sizing the roles

| Role | Guidance |
|------|----------|
| **Control plane** | 2 vCPU / 4 GB is the floor; go to 4 vCPU / 8 GB above ~50 nodes or heavy API traffic. `--control-plane-count 3` for anything that must survive a node loss (1 or 3 only — 2 has no quorum benefit). |
| **etcd** (`--etcd-topology external`) | Latency-sensitive: prefer dedicated or NVMe-backed instances. `--etcd-node-count 3` (5 only for very large clusters). Stacked etcd is fine up to a few dozen nodes. |
| **Workers** | Fewer, larger nodes pack better and cost less per usable core; smaller nodes give finer failure granularity and cheaper scale steps. Two or three mid-size beats one huge node — losing it should not lose the cluster. |
| **Bastion / gateway** | The smallest thing the provider sells. It routes; it does not work. |
| **GPU** | Its own node group, tainted, with the GPU operator installed. |

Leave headroom: kubelet, the CNI, the CSI, ingress, metrics and logging take real CPU and memory
before your workload starts. A 2 vCPU worker has appreciably less than 2 vCPU for pods.

## GPU node groups

```bash
ankra cluster node-group add <cluster_id> --name gpu \
  --instance-type <gpu-type> --count 1 --wait
ankra cluster node-group taints <cluster_id> gpu --taints nvidia.com/gpu=true:NoSchedule
ankra cluster node-group labels <cluster_id> gpu --labels workload=gpu
```

Then install the GPU operator as an addon so the driver, container runtime hook and device plugin
are present, and confirm nodes advertise `nvidia.com/gpu` before deploying anything that requests
one.

Two traps that cost a rebuild:

- **The OS disk.** Provider defaults are often 25 GB. GPU driver layers plus a model-serving image
  plus downloaded weights overrun that and the node wedges on disk pressure. Size the disk, or mount
  a volume for the model cache, before the first deploy.
- **Quota and stock are separate refusals.** Providers commonly check stock *before* quota, so an
  "out of capacity" error can really be a quota of zero. Check both with the provider before
  concluding the region is full.

## Create-flag map

Same concepts, different names. `--name`, `--credential-id`, `--ssh-key-credential-id`,
`--distribution`, `--kubernetes-version`, `--control-plane-count`, `--worker-count`,
`--etcd-topology`, `--etcd-node-count`, `--include-networking` and `--include-dns` are common to
every provider. `--gitops-repository`, `--gitops-credential-name` and `--gitops-branch` exist on
Hetzner, DigitalOcean, OVH, UpCloud, AWS and Ankra Cloud (not Scaleway, Proxmox or Morpheus);
`--external-cloud-provider` only on Hetzner, DigitalOcean, OVH and UpCloud.

| Concept | Hetzner | DigitalOcean | OVH | UpCloud |
|---------|---------|--------------|-----|---------|
| Where | `--location` | `--region` | `--region` | `--zone` |
| Control plane compute | `--control-plane-server-type` | `--control-plane-size` | `--control-plane-flavor-id` | `--control-plane-plan` |
| Worker compute | `--worker-server-type` | `--worker-size` | `--worker-flavor-id` | `--worker-plan` |
| Bastion compute | `--bastion-server-type` | `--bastion-size` | `--gateway-flavor-id` | `--bastion-plan` |
| External etcd compute | `--etcd-server-type` | `--etcd-size` | `--etcd-flavor-id` | `--etcd-plan` |
| Private network | `--network-ip-range`, `--subnet-range` | `--network-ip-range` | `--network-vlan-id`, `--subnet-cidr`, `--dhcp-start/--dhcp-end` | `--network-ip-range` (auto-picked if omitted) |
| Zones | — | — | `--availability-zones` | `--zones` (+ `--network-mode`, `ankra cluster upcloud zones` to grow; node-group `--zone`) |
| CNI | — | — | — | `--cni flannel\|calico\|cilium` |

Scaleway (`--zone`/`--region`, `--*-type`, `--gateway-type`, `--private-network-id`) and AWS
(`--region`, `--*-type`, `--vpc-id`, `--node-subnet-ids`, `--bastion-subnet-id`, `--egress-mode`,
`--architecture`, `--root-volume-gib`) take `--retention-policy retain|delete` for what a teardown
leaves behind. Proxmox VE (`--node`, `--storage`, `--template`, `--bridge`,
`--*-instance-type`) and Morpheus (`--group-id`, `--cloud-id`, `--network-id`, `--layout-id`,
`--*-plan-id`) have their own placement flags - run `create --help`; the discovery commands above
list the valid values.

Ankra Cloud (`ankra cluster ankracloud create`) uses `--zone`, the `*-plan` compute flags,
`--network-ip-range` or `--private-network-id`, and `--network-mode ipv4_nat|ipv6_only`
(omit it for the server default, `ipv4_nat`). `ipv6_only` is for a zone with no public IPv4:
no NAT router, the bastion is reached over IPv6 through the Cloud's port forward, and the nodes
reach IPv4 through the zone's NAT64. When `ankra cluster ankracloud preflight` answers that the
zone has no public IPv4 left for the bastion, rerun it with `--network-mode ipv6_only` and create
with the same flag; the flag is accepted by both commands.

## Cost, without surprises

- **Right-size from measurement, not from the default.** Create, run the real workload, then read
  `ankra cluster top nodes` and `ankra cluster top pods` and resize.
- **Scale the pool, not the instance**, for elastic load: `ankra cluster node-group autoscaling set`
  where the provider supports it.
- **Stop what is not in use.** `ankra cluster <provider> stop` and `ankra cluster power-schedules`
  turn a dev cluster off nightly. Note a plain stop can still bill for parked networking and load
  balancers — check the provider's rules before assuming a stopped cluster is free.
- **Know what a teardown keeps.** `deprovision --force` also releases leftover load balancers on
  UpCloud, Hetzner, OVH, DigitalOcean and AWS; volumes go only with `--accept-volume-data-loss`
  (or a yes at the prompt), and a `retain` cluster (AWS, Scaleway, Ankra Cloud default) keeps them.
  Kept volumes bill indefinitely and belong to no cluster, so nothing surfaces them later.
- **Dedicated vCPU costs more per core and less per unit of predictability.** Use it where jitter
  actually hurts, not by default.

## Failure modes worth recognising

| Symptom | Usual cause |
|---------|-------------|
| Reconcile fails at private-network setup | The region is not enabled on the account. List regions with the credential first. |
| `LoadBalancer` Service stays `<pending>` | `--external-cloud-provider=false`, so there is no CCM to provision it. |
| Ingress resolves nowhere / no certificate | `--include-dns=false`, or the hostname is on your own domain — the generated external-dns only manages the generated subdomain. See `ankra-domains-dns`. |
| PVCs stay `Pending` | No CSI: same root cause as the LoadBalancer one. |
| Pods `Pending` after adding a node group | Taints without matching tolerations, or the instance type is smaller than the requests. |
| Everything `Pending` on an ARM cluster | An image with no `arm64` variant. |
| A one-node group landed in the wrong zone | Unpinned group on a zone-spread cluster: it takes the thinnest zone. Pin with `--availability-zone`. |
| Upgrade aborted partway | A drain blocked by a PodDisruptionBudget. Fix the PDB or re-run with `--force`. |
| Cluster gone but the bill is not | Load balancers left by a deprovision without `--force`, or volumes kept by `retain` or by declining the volume prompt. |
| A node-group change "succeeded" but nothing changed | The write was refused later (stock, quota). `ankra cluster operations list --failed --cluster <c>` names the reason. |
