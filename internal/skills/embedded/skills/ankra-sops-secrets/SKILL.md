---
name: ankra-sops-secrets
description: Encrypt Kubernetes Secrets and sensitive addon values with SOPS and AGE so they can live in an Ankra GitOps repo, using `ankra cluster encrypt`/`decrypt` in cluster mode or file mode, glob key patterns that keep covering keys added later, `--all-data` for a whole Secret, and `encrypted_paths` so Ankra decrypts at deploy time. Use when the user needs to store a secret in Git, mentions SOPS, AGE, `encrypted_paths`, `ankra cluster encrypt`/`decrypt`/`sops-config`, or asks how to change a secret value without committing plaintext. Also covers `ankra secrets` (organisation secret slots), the home for a value that must never pass through a terminal, a committed file or a model: generated or piped in, referenced by a `${SECRET_SLOT:<id>}` sentinel, rotated in place.
---

# Ankra secrets with SOPS

## Recommended path

- Golden path 2 (GitOps as the source of truth; see `ankra-platform-principles`): the Secret or
  values file lives in the GitOps repository sealed with SOPS, `encrypted_paths` declares it, and
  `ankra cluster apply` plus the Ankra engine deploy it.
- First run `ankra cluster sops-config`; then encrypt in file mode (`-f cluster.yaml`) when the repo
  is the source of truth, or in cluster mode for a stack that already runs.
- To change a value, use `encrypt --set` so the plaintext never reaches Git. Repository layout is
  `ankra-gitops`; a secret only one application reads belongs in `ankra application env-secrets`.
- A value nobody needs to see or type (a database password, an internal token, anything an agent
  sets up): `ankra secrets create <name> --generate`, then put the printed `${SECRET_SLOT:<id>}`
  sentinel where the value belongs, at a key `encrypted_paths` declares. The value never passes
  through a terminal, a file you commit, or a model. See "Secret slots" below.
- Anti-patterns: a plaintext commit "to encrypt later", a structural key name (`key`, `name`,
  `host`) in `encrypted_paths`, decrypted output in a transcript, and a secret value typed into a
  command line or a chat.

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

ankra cluster encrypt addon --name my-app --key apiKey
ankra cluster encrypt addon --name my-app --key apiKey --cluster prod --stack apps   # --stack when the name is in several stacks
ankra cluster encrypt addon --name my-app --key 'glob:*Password' -f cluster.yaml
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
- Every key the resource already declares in `encrypted_paths` is sealed again together with
  `--key`, in both modes, so a file decrypted for inspection is sealed whole on the next encrypt.

### What an entry actually matches

An entry names a **key**, not a location. Ankra compiles the entries into a SOPS `encrypted_regex`
of key names, and SOPS applies it at every depth: `auth.password` seals **every** `password` key in
the file. That is why a key added under a new section is covered, and why a structural name is a
trap: **never declare `key`, `name`, `path`, `value`, `type`, `host`, `port` or `operator`**, bare
or as the last segment (`tls.key`, `metadata.name`). It seals the chart's own fields too, and the
render or sync then fails. Give the secret a distinctive name (`smtpPassword`) or put it in its own
Secret manifest. `ankra cluster validate` reports over-broad entries as
`encrypted_paths_over_match`.

## Changing a secret value without committing plaintext

This is the pattern that matters most, and the obvious route gets it wrong.

```bash
# Right: the new value and its encryption land in ONE commit
ankra cluster encrypt manifest db-secret --key password \
  --set 'data.password=bmV3LXNlY3JldA==' --cluster prod
```

In cluster mode `--set` applies the value edit **in memory before encrypting**, so the plaintext
never reaches Git history. It also replaces a value that is already sealed, which is how a sealed
credential is rotated in place (escape a key that starts with a dot:
`--set 'data.\.dockerconfigjson=<base64>'`).

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
ankra cluster decrypt addon --name my-app --cluster prod
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

**Empty and absent are different.** Over the API, omitting `encrypted_paths` on an update keeps
the stored declaration, while `encrypted_paths: []` clears it and the next platform write renders
the file as plain content. `ankra cluster apply` drops an empty list, so `[]` in a cluster YAML
keeps what is stored; a sealed `from_file` with no stored declaration is refused. In a repository
Ankra syncs, the declaration is derived from the sealed file's `sops` metadata.

**Who decrypts.** On the native engine (the default) the Ankra agent fetches the organisation's
private key at deploy time and decrypts inside the cluster; nothing is installed beforehand.

## Secret slots: values that never pass through you

`ankra secrets` manages the organisation's secret slots. A slot holds one value in Ankra's vault;
the stack carries only its sentinel, `${SECRET_SLOT:<id>}`, and Ankra substitutes the value only
when it SOPS-seals the file for delivery. The cluster YAML you write, review and commit holds the
sentinel, never the value, so this is the home for a value that must not live in Git even
encrypted by hand, and for any secret an AI agent creates for you.

```bash
ankra secrets create db-password --generate            # Ankra creates the value; prints the sentinel
SENTINEL="$(ankra secrets create db-password --generate)"   # stdout is the sentinel alone
printf '%s' "$STRIPE_KEY" | ankra secrets create stripe-key  # a value you have, on stdin
ankra secrets create tls-key --from-file ./tls.key     # a file, byte for byte
ankra secrets list                                     # names, ids, who uses each, last read; never values
ankra secrets set stripe-key --from-file ./new.key     # replace in place; the sentinel stays valid
ankra secrets rotate db-password --generate            # a new generated value, in place
ankra secrets delete stripe-key                        # refused while anything references it
```

Use the sentinel at a key the resource seals:

```yaml
addons:
  - name: grafana
    configuration:
      from_file: "values/grafana.yaml"     # adminPassword: ${SECRET_SLOT:0b6f2c1e-...}
      encrypted_paths:
        - adminPassword
```

- **The value is never a command-line argument.** `ankra secrets` refuses one: shell history and
  the process list keep argv. Use `--generate`, stdin or `--from-file`.
- **The sentinel only resolves inside a sealed key.** A sentinel at a key `encrypted_paths` does
  not declare is refused at push time, and an organisation without SOPS configured refuses the
  save. Run `ankra cluster sops-config` first.
- **A generated slot expires** (after a day by default, `--ttl` up to a week) unless a deployed
  resource references its sentinel, which keeps it for as long as it is referenced. Reference it
  promptly, and check `ankra secrets list` (EXPIRES `never (in use)`).
- **Rotation keeps the id.** `set` and `rotate` replace the value in place, so nothing that
  references the sentinel changes; Ankra re-delivers the resources that use it.
- **Delete is guarded.** A slot that deployed resources reference is refused and the resources are
  named; remove the sentinel from them first. There is no force.
- `--org <id>` scopes every command to one organisation; `-o json` gives sentinels and references.

## Where a secret should live

SOPS in Git is one of three homes, and picking the wrong one is a common mistake:

| Value | Home |
|-------|------|
| A Secret or addon value a stack deploys | SOPS-encrypted in the GitOps repo, `encrypted_paths` declared |
| A value nobody should see, type or commit (generated passwords, tokens an agent sets up) | `ankra secrets create --generate`; the stack carries the sentinel at a declared `encrypted_paths` key |
| A secret only one application needs | `ankra application env-secrets set` (then `apply`) |
| A non-secret that varies per environment | A cluster / organisation / stack variable |

See `ankra-app-integrations` for the full split.

## Traps

- **Stack profiles carry no secret values.** Capture redacts them. Before publishing a profile
  built from a sealed stack, check each sealed resource still declares `encrypted_paths`, and bind
  secrets at launch with `--set-file`/`--set-env` (see `ankra-stack-profiles`).
- **A key that does not exist yet.** An exact `--key` for a key added later is not covered; use a
  `glob:` pattern when the key set will grow.
- **Cross-namespace Secrets.** A Secret is only visible to pods in its own namespace. Encrypting it
  correctly does not make it reachable from elsewhere.
- **Rotation.** Rotate a leaked value at its source, then re-seal it with `encrypt --set`.
  Organisation key rotation is not self-service: every sealed file must be re-encrypted first.

## Rules

- **Plaintext never reaches Git.** Encrypt first, or use `encrypt --set` to do both at once.
- **A value that should never pass through a person or a model goes in `ankra secrets`**, and
  only its sentinel goes in the stack.
- **Declare `encrypted_paths`** for every encrypted value.
- **Encrypt values, not whole files**, so reviews stay meaningful.
- **Never log or paste decrypted output.**
- **Rotate at the source** when plaintext has leaked; history rewriting is not remediation.

## Related skills

- `ankra-gitops` — the repository these encrypted files live in.
- `ankra-stacks-addons` — the manifests and addon values being encrypted.
- `ankra-app-integrations` — choosing between env-secrets, SOPS, and variables.
- `ankra-security` — the wider least-privilege posture and review pass.
