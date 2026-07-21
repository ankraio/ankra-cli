---
name: ankra-sops-secrets
description: Encrypt Kubernetes Secrets and sensitive values stored in an Ankra GitOps repo using SOPS with AGE, and track them with encrypted_paths so Ankra decrypts at deploy time. Use when the user needs to store secrets in Git, mentions SOPS, AGE, encrypted_paths, or `ankra cluster encrypt`/`decrypt`/`sops-config`.
---

# Ankra Secrets with SOPS

Never commit plaintext Secrets. Ankra integrates SOPS (with AGE keys) so sensitive manifests and addon values are encrypted in Git and decrypted only at deploy time.

## Concepts

- **SOPS** encrypts the values of YAML keys, leaving structure readable so diffs stay reviewable.
- **AGE** is the recommended key type (simple, modern). The public key encrypts; the private key (held by Ankra / the cluster) decrypts.
- **`encrypted_paths`** — the list, on a manifest or addon, of paths that are SOPS-encrypted, so Ankra knows what to decrypt.

## Workflow with the CLI

```bash
# 1. Inspect / set the SOPS configuration for the cluster
ankra cluster sops-config

# 2. Encrypt a file before committing it
ankra cluster encrypt -f manifests/db-secret.yaml

# 3. (When needed) decrypt locally to inspect
ankra cluster decrypt -f manifests/db-secret.yaml
```

Then reference the encrypted file from your stack and declare its encrypted paths:

```yaml
manifests:
  - name: db-secret
    from_file: "manifests/db-secret.yaml"
    encrypted_paths:
      - data.password
      - data.username
addons:
  - name: my-app
    chart_name: my-app
    chart_version: 1.4.2
    repository_url: https://charts.example.com
    namespace: app
    configuration:
      from_file: "values/my-app.yaml"
      encrypted_paths:
        - secrets.apiKey
```

## Rules

- **Plaintext secrets never reach Git.** Encrypt first, commit the encrypted file.
- **Encrypt values, not whole files where possible** — keep keys/structure visible so reviews are meaningful.
- **Declare `encrypted_paths`** for every encrypted value so Ankra decrypts the right fields.
- **Rotate keys** and re-encrypt when access changes; scope decryption to the clusters that need it.
- **Do not log decrypted output** or paste it into chat, issues, or CI logs.

## Related skills

- `ankra-gitops` for the repo that stores these encrypted files.
- `ankra-platform-principles` for the broader least-privilege stance.
