---
name: ankra-sops-secrets
description: Encrypt Kubernetes Secrets and sensitive addon values with SOPS and AGE so they can live in an Ankra GitOps repo, using `ankra cluster encrypt`/`decrypt` in cluster mode or file mode, glob key patterns that keep covering keys added later, `--all-data` for a whole Secret, and `encrypted_paths` so Ankra decrypts at deploy time. Use when the user needs to store a secret in Git, mentions SOPS, AGE, `encrypted_paths`, `ankra cluster encrypt`/`decrypt`/`sops-config`, or asks how to change a secret value without committing plaintext.
---

# Ankra secrets with SOPS

Never commit a plaintext Secret. Ankra integrates SOPS (with AGE keys) so sensitive manifest and
addon values are ciphertext in Git and decrypted only at deploy time — while the surrounding YAML
stays readable, so diffs remain reviewable.

## Concepts

- **SOPS** encrypts the *values* of named YAML keys, leaving structure and key names visible.
- **AGE** is the key type. The public key encrypts; the private key, held by Ankra, decrypts.
- **`encrypted_paths`** is the list on a manifest or addon telling Ankra which values are
  encrypted. **An encrypted value with no declared path deploys as ciphertext** — this is the most
  common SOPS mistake in Ankra.

```bash
ankra cluster sops-config          # the organisation's SOPS configuration and public key
```

## Two modes, and when to use which

Both `encrypt` and `decrypt` work either against the live cluster or against a local file.

| Mode | What it does | When |
|------|--------------|------|
| **Cluster** (default) | Fetch from the live cluster, encrypt, push back via the partial-stack PATCH. The owning stack is resolved automatically. | The stack already exists on a cluster |
| **File** (`-f cluster.yaml`) | Rewrite the `from_file` the local `cluster.yaml` references, in place, and add the key to `encrypted_paths` in the file | GitOps, where the source of truth is on disk |

## Encrypting

```bash
ankra cluster encrypt manifest db-secret --key password
ankra cluster encrypt manifest db-secret --key password --cluster prod
ankra cluster encrypt manifest db-secret --key password --key api-token
ankra cluster encrypt manifest db-secret --all-data --cluster prod
ankra cluster encrypt manifest db-secret --key 'glob:stringData.DB_*' --cluster prod
ankra cluster encrypt manifest db-secret --key password -f cluster.yaml

ankra cluster encrypt addon my-app --key apiKey
ankra cluster encrypt addon my-app --key 'glob:*Password' -f cluster.yaml
```

Key semantics worth knowing before you write a `--key`:

- `--key` takes the **YAML key name**, not a dotted path: for `data.password`, that is `password`.
  A dotted `--key` is normalised to its last segment. SOPS matches key names anywhere in the
  document.
- A key whose own name starts with a dot (`.dockerconfigjson` in a `kubernetes.io/dockerconfigjson`
  Secret) is kept literally.
- Repeating `--key` encrypts several keys in one SOPS pass and one write.
- **`--key 'glob:<pattern>'`** encrypts every key matching the pattern — now *and on every later
  re-encrypt*, so keys added afterwards are covered automatically. Only `*` is a wildcard;
  everything else is literal. A pattern matching nothing fails, the same as a misspelled key.
- **`--all-data`** selects every key under `data` and `stringData` of a Secret manifest, skipping
  values already encrypted. The manifest must be a Secret; `--all-data` and `--key` are mutually
  exclusive.
- After encrypting, the CLI verifies every value really is `ENC[...]` ciphertext and fails if not.

## Changing a secret value without committing plaintext

This is the pattern that matters most, and the obvious route gets it wrong.

```bash
# Right: the new value and its encryption land in ONE commit
ankra cluster encrypt manifest db-secret --key password \
  --set 'data.password=bmV3LXNlY3JldA==' --cluster prod
```

In cluster mode `--set` applies the value edit **in memory before encrypting**, so the plaintext
never reaches Git history.

```bash
# Wrong: this commits the plaintext value first, and it stays in history forever
ankra cluster manifests upgrade db-secret --set 'data.password=...'
ankra cluster encrypt manifest db-secret --key password
```

If plaintext has already been committed, treat the value as compromised: rotate it at the source,
then encrypt the new one. Rewriting Git history is not a fix.

## Decrypting

```bash
ankra cluster decrypt manifest db-secret
ankra cluster decrypt manifest db-secret --cluster prod
ankra cluster decrypt manifest db-secret -f cluster.yaml
ankra cluster decrypt addon my-app --cluster prod
```

Decryption prints to stdout. Use it to **verify shape** — that the right keys are encrypted, that a
value is present — not to copy values around. Never paste decrypted output into a pull request, a
commit message, an issue, a chat message, or a CI log.

## Declaring it in the stack

```yaml
manifests:
  - name: db-secret
    from_file: "manifests/db-secret.yaml"
    encrypted_paths:
      - data.password
      - data.username
      - "glob:stringData.DB_*"
addons:
  - name: my-app
    chart_name: my-app
    chart_version: 1.4.2
    registry_name: example-charts
    registry_url: https://charts.example.com
    namespace: app
    configuration:
      from_file: "values/my-app.yaml"
      encrypted_paths:
        - secrets.apiKey
```

A glob entry is recorded in `encrypted_paths` **as written** (`glob:stringData.DB_*`), which is the
form the platform re-expands into the SOPS `encrypted_regex` on every push. `ankra cluster encrypt`
maintains these entries for you in file mode; if you hand-edit the YAML, keep them in sync.

Verify what the platform actually stored:

```bash
ankra cluster stacks list <stack> -o json     # echoes each resource's encrypted_paths
```

## Where a secret should live

SOPS in Git is one of three homes, and picking the wrong one is a common mistake:

| Value | Home |
|-------|------|
| A Secret or addon value a stack deploys | SOPS-encrypted in the GitOps repo, `encrypted_paths` declared |
| A secret only one application needs | `ankra application env-secrets set` (then `apply`) |
| A non-secret that varies per environment | A cluster / organisation / stack variable |

See `ankra-app-integrations` for the full split.

## Traps

- **`encrypted_paths` lost on a stack profile draft.** Opening a builder draft on a published
  profile drops them. Re-declare before publishing, or the launch deploys ciphertext.
- **A key that does not exist yet.** An exact `--key` for a key added later is not covered; use a
  `glob:` pattern when the key set will grow.
- **Cross-namespace Secrets.** A Secret is only visible to pods in its own namespace. Encrypting it
  correctly does not make it reachable from elsewhere.
- **Rotation.** Re-encrypt when access changes, and scope decryption to the clusters that need it.

## Rules

- **Plaintext never reaches Git.** Encrypt first, or use `encrypt --set` to do both at once.
- **Declare `encrypted_paths`** for every encrypted value.
- **Encrypt values, not whole files**, so reviews stay meaningful.
- **Never log or paste decrypted output.**
- **Rotate at the source** when plaintext has leaked; history rewriting is not remediation.

## Related skills

- `ankra-gitops` — the repository these encrypted files live in.
- `ankra-stacks-addons` — the manifests and addon values being encrypted.
- `ankra-app-integrations` — choosing between env-secrets, SOPS, and variables.
- `ankra-security` — the wider least-privilege posture and review pass.
