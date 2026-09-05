#!/usr/bin/env bash
# Installs the GitHub CLI (from GitHub's own apt repository, so the version does
# not depend on the distribution's package) plus any extra apt packages named
# as arguments, on whichever host the job landed on: as root inside a job
# container (golang:1.26) or through sudo on the bare self-hosted runner image.
# Depot's images ship gh preinstalled; there this is a no-op.
set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
    SUDO=""
else
    SUDO="sudo"
fi

if ! command -v gh >/dev/null 2>&1; then
    ${SUDO} mkdir -p /etc/apt/keyrings
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | ${SUDO} tee /etc/apt/keyrings/githubcli-archive-keyring.gpg >/dev/null
    ${SUDO} chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        | ${SUDO} tee /etc/apt/sources.list.d/github-cli.list >/dev/null
    set -- gh "$@"
fi

if [ "$#" -gt 0 ]; then
    ${SUDO} apt-get update -qq >/dev/null
    DEBIAN_FRONTEND=noninteractive ${SUDO} apt-get install -y -qq --no-install-recommends "$@" >/dev/null
fi
gh --version | head -n 1
