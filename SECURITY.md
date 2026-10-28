# Security

## Supported Releases

We support the latest tagged release of `ankra-cli`. Pin to a tagged
version (e.g. `v1.0.0`) rather than `latest` for reproducible installs.

## Reporting a Vulnerability

Please email security@ankra.io with a description of the issue, steps to
reproduce, and any proof-of-concept. Do not open a public GitHub issue.

## Verifying Releases

Every binary published to GitHub Releases ships with a `.sha256` checksum
sibling file. Verify before installing:

```bash
curl -fsSL -O https://github.com/ankraio/ankra-cli/releases/download/<TAG>/ankra-cli-linux-amd64
curl -fsSL -O https://github.com/ankraio/ankra-cli/releases/download/<TAG>/ankra-cli-linux-amd64.sha256
sha256sum -c ankra-cli-linux-amd64.sha256
```

The recommended installer at
<https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh>
performs this verification automatically and refuses to install on
mismatch.

## macOS Signing

Until a Developer ID Application certificate is in place, macOS binaries
are ad-hoc signed in CI. This reduces Gatekeeper friction but does not
provide notarization-grade attestation. Verifying the SHA256 checksum
remains the primary integrity check.

## Manual Installation with Verification

```bash
ASSET="ankra-cli-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')"
BASE="https://github.com/ankraio/ankra-cli/releases/download/<TAG>"

curl -fsSL -o "${ASSET}" "${BASE}/${ASSET}"
curl -fsSL -o "${ASSET}.sha256" "${BASE}/${ASSET}.sha256"
sha256sum -c "${ASSET}.sha256"

chmod +x "${ASSET}"
sudo install -m 0755 "${ASSET}" /usr/local/bin/ankra
```

On macOS you can strip the quarantine attribute:

```bash
xattr -d com.apple.quarantine /usr/local/bin/ankra
```

## Sensitive Data on Disk

`ankra login` writes a bearer token to `~/.ankra.yaml`. The file is
created with `0600` permissions. If you ever observe a `~/.ankra.yaml`
with weaker permissions, regenerate it via `ankra logout && ankra login`.
