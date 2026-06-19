# Ankra cloud lifecycle system test

`lifecycle_systemtest.sh` is a real, end-to-end system test that drives the
`ankra` CLI against a live platform and provisions **real** clusters on Hetzner,
OVH and UpCloud, validating the full lifecycle the same way an operator would by
hand:

1. **create** with the external cloud provider + GitOps, so the cloud-provider
   stack (CCM, CSI, Traefik, cert-manager) is installed
2. wait until the cluster is **online** and the control-plane + worker nodes are **Ready**
3. confirm the **stack addons reach `up`**
4. **scale** workers up (1 → 3) and down (3 → 1)
5. **node group** add (2 nodes) then delete
6. **Kubernetes upgrade** to a newer k3s version
7. **instance resize** of the default node group to a bigger plan
8. **deprovision** and confirm the cluster record is removed (`deleted_at`)

It is deliberately a thin wrapper over the exact CLI commands a customer runs, so
it is "as real as possible". It tolerates the real behaviours of the platform:

- transient provisioning timeouts (slow bastion/server boot) → it retries the
  reconcile instead of failing
- the platform serialises writes (HTTP 409 while a reconcile runs) → it waits and
  retries the day-2 operation
- on any failure or interrupt it **deprovisions every cluster it created**, so it
  never leaks paid infrastructure

## Prerequisites

- A built `ankra` binary. By default the script uses the repo build at
  `../bin/ankra` (run `go build -o bin/ankra .` in `ankra-cli/`), or set `ANKRA_BIN`.
- A logged-in CLI (`ankra login`) or `ANKRA_API_TOKEN`, pointed at the target
  platform (`base-url` in `~/.ankra.yaml`, default `https://platform.ankra.dev`).
- Provider credentials and an SSH-key credential already stored in the Ankra org.
- A GitOps GitHub credential + repository for the generated cloud-provider stack.

## Configuration (environment variables)

Required:

| Variable | Meaning |
|---|---|
| `ANKRA_SYSTEMTEST_CONFIRM` | must be `yes` to acknowledge that real, billable infrastructure will be provisioned; the script refuses to run otherwise |
| `SSH_KEY_CREDENTIAL_ID` | SSH-key credential ID |
| `HETZNER_CREDENTIAL_ID` / `OVH_CREDENTIAL_ID` / `UPCLOUD_CREDENTIAL_ID` | provider API credential ID (per selected provider) |

Optional GitOps (commits the generated cloud-provider stack to Git; the stack still installs without it):

| Variable | Meaning |
|---|---|
| `GITOPS_CREDENTIAL_NAME` + `GITOPS_REPOSITORY` | GitOps target for the cloud-provider stack |

Common optional (defaults in parentheses):

| Variable | Default |
|---|---|
| `ANKRA_SYSTEMTEST_PROVIDERS` | `hetzner ovh upcloud` |
| `ANKRA_SYSTEMTEST_PARALLEL` | `1` (run selected providers concurrently; set `0` for one-at-a-time) |
| `ANKRA_CONFIG_FILE` | `~/.ankra.yaml` (base config parallel workers copy for auth/org) |
| `ANKRA_BIN` | `../bin/ankra` then `ankra` on PATH |
| `GITOPS_BRANCH` | `master` |
| `HETZNER_LOCATION` / `OVH_REGION` / `UPCLOUD_ZONE` | `nbg1` / `GRA9` / `de-fra1` |
| `HETZNER_CP_TYPE` / `HETZNER_WORKER_TYPE` / `HETZNER_BASTION_TYPE` / `HETZNER_BIGGER_TYPE` | `cpx32` / `cpx22` / `cpx22` / `cpx32` |
| `OVH_CP_FLAVOR` / `OVH_WORKER_FLAVOR` / `OVH_BIGGER_FLAVOR` | `b2-15` / `b2-15` / `b2-30` |
| `UPCLOUD_CP_PLAN` / `UPCLOUD_WORKER_PLAN` / `UPCLOUD_BIGGER_PLAN` | `2xCPU-4GB` / `2xCPU-4GB` / `4xCPU-8GB` |
| `K8S_UPGRADE_TARGET` | highest version from `ankra cluster k3s-versions` |
| `ONLINE_TIMEOUT` / `ADDONS_TIMEOUT` / `DAYTWO_TIMEOUT` / `DEPROVISION_TIMEOUT` | `1500` / `900` / `900` / `1500` (seconds) |
| `DEPROVISION_FORCE_TIMEOUT` | `600` (bounded force-deprovision fallback if a graceful deprovision stalls) |

Discover valid values with the CLI:

```bash
ankra credentials list
ankra cluster hetzner server-types --credential-id <id> --location nbg1 --available-only
ankra cluster hetzner locations --credential-id <id>
ankra cluster ovh regions --credential-id <id>
ankra cluster k3s-versions
```

## Running

```bash
cd ankra-cli && go build -o bin/ankra .

export ANKRA_SYSTEMTEST_CONFIRM=yes   # acknowledge real, billable infrastructure
export SSH_KEY_CREDENTIAL_ID=...
export HETZNER_CREDENTIAL_ID=...
export OVH_CREDENTIAL_ID=...
export UPCLOUD_CREDENTIAL_ID=...
export GITOPS_CREDENTIAL_NAME=...     # optional
export GITOPS_REPOSITORY=org/repo     # optional

# all three providers, in parallel (default)
./systemtest/lifecycle_systemtest.sh

# all three providers, one at a time
ANKRA_SYSTEMTEST_PARALLEL=0 ./systemtest/lifecycle_systemtest.sh

# one provider
ANKRA_SYSTEMTEST_PROVIDERS=upcloud ./systemtest/lifecycle_systemtest.sh
```

By default the selected providers run **concurrently** within a single invocation
(`ANKRA_SYSTEMTEST_PARALLEL=1`), so a full three-provider run takes roughly as long
as the slowest single provider rather than the sum of all three. Each parallel
worker copies the base CLI config to its own file and runs with `--config`, so
concurrent `cluster select` writes never clobber a sibling worker's selection.
Per-provider logs and result files are written under a `mktemp -d` work directory
printed at the start of the run; output on the console is line-tagged `[provider]`.

## Output

The script prints a per-step `PASS`/`FAIL` (tagged with the provider in parallel
mode), ends with a results list and a summary line, and exits non-zero if any step
failed. Per-provider logs are also saved under the run's work directory.

## Cost & safety

This provisions real, billable cloud servers (1 control-plane + 1 worker, briefly
scaled to 3, plus a temporary node group). You must set `ANKRA_SYSTEMTEST_CONFIRM=yes`
to run it. The run is short-lived and the script always attempts to deprovision on
exit (graceful, then `--force` as a fallback so nothing leaks), but verify with
`ankra cluster list` afterwards.
