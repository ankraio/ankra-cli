---
name: ankra-sops-secrets
description: Encrypt Kubernetes Secrets and sensitive values stored in Git using SOPS with AGE, tracked with encrypted_paths so Ankra decrypts at deploy time. Use when the user needs to put secrets, credentials, or API keys into Kubernetes configuration stored in a repo, or mentions SOPS, AGE, encrypted_paths, or `ankra cluster encrypt`/`decrypt`/`sops-config`.
---

# Ankra Secrets with SOPS

Never commit plaintext Secrets. Ankra integrates SOPS (with AGE keys) so sensitive manifests and addon values are encrypted in Git and decrypted only at deploy time.

## Concepts

- **SOPS** encrypts the values of YAML keys, leaving structure readable so diffs stay reviewable.
- **AGE** is the recommended key type (simple, modern). The public key encrypts; the private key (held by Ankra / the cluster) decrypts.
- **`encrypted_paths`** - the list, on a manifest or addon, of YAML key names that are SOPS-encrypted, so Ankra knows what to decrypt.

## Workflow with the CLI

`encrypt` / `decrypt` take an `addon` or `manifest` subcommand and a `--key`. They run in two modes: **cluster mode** (default - fetch the resource from a live cluster, encrypt, and push the result back) and **file mode** (`-f cluster.yaml` - rewrite the referenced `from_file` on disk and add the key to `encrypted_paths`, for GitOps).

`--key` takes the **YAML key name**, not a dotted path: SOPS matches key names anywhere in the document, so the `password` under a Secret's `data:` is `--key password`. A dotted `--key` like `data.password` is normalised to its last segment, and the CLI verifies the value is real `ENC[...]` ciphertext after encrypting.

```bash
# 1. Inspect the SOPS configuration (the public key used to encrypt)
ankra cluster sops-config

# 2a. Encrypt a key on the selected cluster (cluster mode)
ankra cluster encrypt manifest db-secret --key password
ankra cluster encrypt addon --name grafana --key adminPassword

# 2b. Encrypt in a local cluster.yaml before committing (file mode)
ankra cluster encrypt manifest db-secret --key password -f cluster.yaml

# 3. (When needed) decrypt to stdout to inspect
ankra cluster decrypt manifest db-secret              # cluster mode
ankra cluster decrypt manifest db-secret -f cluster.yaml
```

Reference the encrypted file from your stack and declare its encrypted paths (file mode adds these for you):

```yaml
manifests:
  - name: db-secret
    from_file: "manifests/db-secret.yaml"
    encrypted_paths:
      - password
      - username
addons:
  - name: my-app
    chart_name: my-app
    chart_version: 1.4.2
    repository_url: https://charts.example.com
    namespace: app
    configuration:
      from_file: "values/my-app.yaml"
      encrypted_paths:
        - apiKey
```

## Rules

- **Plaintext secrets never reach Git.** Encrypt first, commit the encrypted file.
- **Encrypt values, not whole files where possible** - keep keys/structure visible so reviews are meaningful.
- **Declare `encrypted_paths`** for every encrypted value so Ankra decrypts the right fields.
- **Rotate keys** and re-encrypt when access changes; scope decryption to the clusters that need it.
- **Do not log decrypted output** or paste it into chat, issues, or CI logs.

## Related skills

- `ankra-gitops` for the repo that stores these encrypted files.
- `ankra-platform-principles` for the broader least-privilege stance.
