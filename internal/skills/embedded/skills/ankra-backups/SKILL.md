---
name: ankra-backups
description: Manage the organisation's backup vaults with `ankra backup vaults` - the S3-compatible buckets cluster backups and migration data move through. Let Ankra provision a bucket from a provider credential or register one you already run, watch the verification status, re-verify after rotating keys, see what a vault holds, and delete a vault with or without destroying the bucket behind it. Use when the user mentions backups, backup vaults, restore points, object storage for backups, an S3 or MinIO bucket for the platform, or asks where migration dumps are kept.
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

## Related skills

- `ankra-migrate` - the restore path that moves data through a vault.
- `ankra-security` - credential scope and the review pass that should include vault keys.
- `ankra-cloud-clusters` - the provider credentials `provision` draws on.
