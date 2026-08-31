---
name: ankra-migrate
description: Move an existing deployment - docker-compose, a bare Dockerfile, or containers on a running Docker daemon - into an Ankra-managed cluster with `ankra migrate`. Plan first, convert to a stack, deploy, dump every database and restore it through a backup vault, then cut over; or run the steps separately, clean up the imports a vault keeps, and add conversion modules for other formats. Use when the user wants to migrate or move an app, VM or server deployment onto Kubernetes, mentions `ankra migrate`, docker-compose to Kubernetes, lift-and-shift, a cutover, or carrying data into a cluster.
---

# Migrating a deployment into Ankra

`ankra migrate` moves a deployment that exists today - a docker-compose project, a bare
Dockerfile, or containers on a running Docker daemon, local or remote - into a cluster Ankra
runs. One command does the whole journey; each step is also its own command when you want to
inspect the result before the next one.

## The one command

```bash
ankra migrate up ./app --cluster shop --plan     # print the plan, change nothing
ankra migrate up ./app --cluster shop            # rehearse: convert, deploy, dump, restore
ankra migrate up ./app --cluster shop --stop-source --yes   # the cutover
```

`up` runs five steps: **plan** (what will move, how big the databases are, what stays behind),
**convert** (the deployment becomes a stack under `<out>/stack`), **deploy** (`cluster apply`
plus a wait for the database workloads), **export** (every database dumped into `<out>/data`),
**restore** (the dumps go through the backup vault into the cluster).

It is idempotent by design: run it into the same output directory as often as you like - the
stack is re-applied, the databases are dumped and restored again. **Rehearse while the source
is still serving, then run once more with `--stop-source`**: that stops the source's
non-database services first, so the final dump is the last word on the data.

- `--plan` before anything else. The plan names every item that will *not* be carried.
- `--no-data` deploys the workloads only; `--out` moves the working directory
  (default `ankra-migration`); `--timeout` bounds each waiting step.
- `--option key=value` reaches the module and applies to convert and export alike.

**Not carried:** files in the volumes of non-database workloads, and data in engines the
export does not dump (Redis, MongoDB, search indexes). Dumped engines today: PostgreSQL and
MySQL/MariaDB. Nothing is silently dropped - the plan lists each such item; move the rest by
hand after the workloads run.

## Prerequisites

- A cluster Ankra runs, selected or passed with `--cluster`.
- A **ready backup vault** in the organisation - the restore uploads travel through it.
  `ankra backup vaults provision` creates one from a provider credential; see `ankra-backups`.
  With exactly one ready vault it is picked automatically; otherwise pass `--vault`.
- The source must be up and reachable by the docker CLI - dumps are taken from the running
  containers. This machine needs neither kubectl nor a database client, and Ankra never holds
  the data: dumps upload straight to your vault with presigned URLs and the cluster's agent
  restores them in place.

## The steps as separate commands

```bash
ankra migrate detect ./app                       # which modules recognise it, most confident first
ankra migrate convert ./app --out ./app-k8s      # write cluster.yaml + manifests; read the warnings
ankra cluster apply -f ./app-k8s/cluster.yaml    # deploy the stack
ankra migrate export ./app --out ./app-data      # dump the databases (manifest.json + SHA256SUMS)
ankra migrate restore ./app-data --cluster shop --wait
ankra migrate data ./app --cluster shop --wait   # export + restore back to back
ankra migrate restore-status <import-id> --wait  # progress of a running restore
```

`convert`, `detect`, `export` and `modules` work offline; `up`, `restore`, `restore-status`
and `data` talk to the platform. **Read convert's warnings**: they list what the module could
not carry - locally built images, host directories, unresolved `${VAR}` references,
credentials written in plain text - and each names the fix.

Useful docker-module options (`--option`, repeatable, on convert/export/up):

| Option | Meaning |
|--------|---------|
| `source=compose` / `dockerfile` / `daemon` | which source to read (default: detected) |
| `profiles=app,dns` / `all-profiles=true` | compose profiles to include |
| `project=<name>` / `containers=a,b` | compose project / specific containers, for the daemon |
| `docker-host=ssh://root@203.0.113.7` | a remote Docker daemon, for daemon source and exports |
| `image.<workload>=<registry/repo:tag>` | image for a locally built workload |
| `ingress.<workload>=<host>` | expose a workload through an Ingress |
| `cluster-issuer=letsencrypt-prod` | request TLS for every Ingress |
| `volume-size=20Gi` / `storage-class=<name>` | sizing for every PersistentVolumeClaim |
| `use-environment=true` | let your shell satisfy `${VAR}` references |

## The imports a vault keeps

Every restore uploads the export's dumps into the vault under `imports/<import-id>/` and
keeps them, so the same upload can be restored again.

```bash
ankra migrate imports list --vault backups
ankra migrate imports delete <import-id> --yes
```

Those dumps are **complete copies of production data**. Once the migration is verified,
delete the imports you no longer need - a forgotten dump in an object-storage bucket is a
breach waiting for lax bucket permissions. A delete is refused while a restore is still
reading the import.

## Other formats: modules

Conversion and export are done by modules; `docker` is built in. Any executable named
`ankra-module-<name>` in `~/.ankra/modules` or on PATH becomes a module: it answers
`describe`, `detect` and `convert` (JSON on stdin/stdout, protocol 1), and optionally
`export`. `ankra migrate modules` lists what is available and documents the full contract;
a worked example lives in the CLI repository under `examples/modules/`.

```bash
ankra migrate modules
ankra migrate modules install https://example.com/ankra-module-procfile --sha256 <hex>
ankra migrate modules uninstall procfile
```

A module runs with your permissions, on your files and your Docker socket. **Install only
modules you trust and pass `--sha256`** when the author publishes a checksum; `--name` refuses
a module that calls itself something else.

## Rules

- **Plan, rehearse, then cut over.** `--plan` first; a full rehearsal while the source runs;
  `--stop-source` exactly once, when you are ready to switch.
- **Every dump is a snapshot of a live server** - only a `--stop-source` run (or exporting
  after stopping the writers yourself) is consistent with the moment of cutover.
- **Never commit an export.** `<out>/data` holds real production data; keep it out of Git and
  delete it after the restore is verified, along with the vault import.
- **The plan's "not carried" list is a checklist**, not a footnote - Redis state, uploaded
  files and search indexes need their own move.
- Review the generated stack like any change: pinned images, resources, Ingress hosts - then
  it lives in GitOps like everything else.

## Related skills

- `ankra-backups` - the vault the restore path depends on.
- `ankra-import-cluster` / `ankra-stacks-addons` - the stack YAML convert generates.
- `ankra-troubleshooting` - when the deployed workloads misbehave.
- `ankra-app-integrations` - wiring the migrated app to registries and secrets afterwards.
