---
name: ankra-backups
description: Manage the organisation's backup vaults with `ankra backup vaults` - the S3-compatible buckets cluster backups and migration data move through - and, where the closed-beta `backups` feature is on, the restore points over them: protect a stack on a schedule, take a restore point now, inspect what one carries and what it does not, restore a stack in place, and follow the runs that move the data. Use when the user mentions backups, backup vaults, restore points, protecting or restoring a stack, object storage for backups, an S3 or MinIO bucket for the platform, or asks where migration dumps are kept.
---

# Backup vaults

A backup vault is an organisation-level target for data that must survive a cluster:
an S3-compatible bucket plus the access keys to reach it, registered with Ankra. Cluster
backups are written to it, and `ankra migrate restore` uploads database dumps through it.
The platform verifies the keys against the bucket and the outcome is the vault's status -
a vault that is not **ready** is a vault nothing can restore from.

```bash
ankra backup vaults list
ankra backup vaults get offsite          # endpoint, bucket, status, and the failure excerpt when a check failed
```

## Adding a vault: provision or create

**Provision** - Ankra creates the bucket for you from one of the organisation's provider
credentials (Hetzner, UpCloud, DigitalOcean or Scaleway), mints or stores the keys, verifies
and registers the vault:

```bash
ankra backup vaults provision                    # everything defaulted; prints what it chose first
ankra backup vaults provision offsite --credential upcloud-main --region europe-1 --wait
```

Defaults are picked for you: the name (`backups`, then `backups-2`), the credential (the only
one Ankra can provision from) and the region (the provider's usual one). The vault shows
`provisioning` until the bucket exists and verifies. **Hetzner alone needs its Object Storage
key pair passed in or prompted for** - Hetzner issues those in the Cloud Console and its API
cannot mint them; the other providers need nothing beyond the credential.

**Create** - register a bucket you already run (MinIO, AWS S3, any S3-compatible store):

```bash
ankra backup vaults create offsite --endpoint https://s3.example.com --bucket cluster-backups
```

Leave `--access-key-id` and `--secret-access-key` off and let the command prompt - the keys
then never touch your shell history. `--region` only when the endpoint does not imply it;
`--path-style` is the default addressing and fits MinIO, pass `--path-style=false` for stores
that want virtual-hosted-style.

Prefer **provision** when a supported provider credential exists - the keys are minted for
the one bucket and never pass through your hands. Prefer **create** when backups must land in
storage you already govern (retention policies, object locks, your own encryption).

## Verification

The credential check runs when the vault is registered and its result sticks as the status.
After rotating or fixing the keys, re-run it:

```bash
ankra backup vaults verify offsite
```

`get` shows the failure excerpt when the last check failed - an endpoint typo, a dead key and
a missing bucket permission each read differently there. Migrations and restores pick "the
organisation's only **ready** vault" automatically; a vault stuck unverified breaks that
default silently, so `verify` after every key rotation.

## What a vault holds

Migration restores keep their uploads in the vault under `imports/<import-id>/` so they can
be restored again. List and remove them with `ankra migrate imports list` and
`ankra migrate imports delete <import-id>` (see `ankra-migrate`) - those dumps are complete
copies of production databases and should not outlive their purpose.

## Deleting a vault

```bash
ankra backup vaults delete offsite
ankra backup vaults delete offsite --destroy-provider-resources --yes
```

By default only Ankra's record and its stored keys go; **the bucket and everything in it stay
in your cloud account** - and keep billing. `--destroy-provider-resources` also empties and
deletes the bucket and removes what was minted for it (an UpCloud object-storage service, a
DigitalOcean Spaces key); every restore point in it is gone for good. It is refused for a
vault registering a bucket you created yourself - your bucket, your teardown.

## Rules

- **A vault is part of the recovery path - treat its bucket like production.** Scope the keys
  to that one bucket, keep it private, and rotate keys like any other credential
  (`verify` afterwards).
- **Prompt for keys, don't pass them as flags** - flags land in shell history and process
  lists.
- **One ready vault keeps the defaults working.** Restores and migrations resolve "the only
  ready vault"; with several, name one explicitly with `--vault` everywhere.
- **Deleting the vault does not delete the data** - and `--destroy-provider-resources` very
  much does. Know which of the two you mean before `--yes`.
- **Clean up imports after a verified migration** - dumps left in object storage are the
  quiet kind of data leak.
- **Read `Not carried` before you rely on a restore point.** Every listing and detail prints
  it; an omission nobody read is the same as an omission nobody was told about.

## Restore points (closed beta)

Everything below is gated by the organisation's `backups` feature. While it is off every
command answers `Backups are not enabled for this organisation.` - ask Ankra to switch it on
rather than looking for a permission or a typo.

A **restore point** is an immutable copy of a stack's data in a vault, self-describing enough
to be read without the cluster it came from. Backing up creates one; restoring applies one.

### Protect a stack

```bash
ankra cluster stacks data list shop                       # what a backup would have to carry
ankra cluster stacks protect shop --vault production-backups \
  --schedule daily --retention daily=7,weekly=4,monthly=6
ankra cluster stacks unprotect shop                        # retype the stack name to confirm
```

Protection is a property of the stack, not a separate object: `protect` writes a backup block
onto the stack's definition and the platform converges on it by installing the backup data
plane. The command prints `Backup stack: installing` until that plane is actually on the
cluster, so nothing is called protected while the Velero that would do the protecting is still
arriving. `--vault` is required - a protected stack with nowhere to write to is not protected.
`--schedule` takes `hourly`, `daily`, `weekly` or a five-field cron expression. Databases are
captured by default and volumes only where named with `--include-pvc namespace/name`;
`--exclude-databases` needs `--confirm-exclude-databases` beside it. Unprotecting stops future
backups and **keeps** every restore point already taken.

### Take one, read one, restore one

```bash
ankra cluster stacks restore-points create shop --note "before the 3.2 upgrade" --wait
ankra cluster stacks restore-points list shop --status complete
ankra cluster stacks restore-points get shop 0b2f          # an unambiguous id prefix is enough
ankra cluster stacks restore-points restore shop 0b2f1c3d --wait   # destructive: 8+ characters or the full id
ankra cluster stacks restore-points delete shop 0b2f1c3d
ankra backup restore-points list --cluster production      # across every cluster
```

`create` dispatches a capture and answers with the run that will seal it; `--wait` follows
that run and prints the sealed restore point. `restore` prints the four-step sequence the
platform will follow - scale down, remove the volumes it replaces, restore, scale back up -
and asks you to retype the stack's name before any of it starts. A restore point carrying a
CloudNativePG or Percona database is refused with the platform's own reason: the ordinary
restore path would report success over unchanged data, which is worse than refusing. A stack
whose data has changed since the restore point was taken is refused unless `--force`.

**Every read shows what the restore point does not carry**, next to what it does. Read that
list before you rely on a restore point, not after.

### Watch the runs

```bash
ankra runs list --kind backup --cluster production
ankra runs get <run-id>            # the plan, and every attempt of every step
ankra runs cancel <run-id>
ankra runs retry <run-id>          # opens a NEW run from the step that failed
```

`retry` answers with the new run, not the old one: the failed row will never move again.

## Related skills

- `ankra-migrate` - the restore path that moves data through a vault.
- `ankra-troubleshooting` - reading a failed backup or restore run on the cluster.
- `ankra-security` - credential scope and the review pass that should include vault keys.
- `ankra-cloud-clusters` - the provider credentials `provision` draws on.
