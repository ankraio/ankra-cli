---
name: ankra-cicd
description: Build CI/CD pipelines (GitHub Actions or GitLab CI) that build a container image, push it with an immutable tag, and bump that tag in the Ankra GitOps repository so the Ankra engine syncs the change - rather than running kubectl/helm against the cluster from CI. For an application repository, reach for `ankra application add` FIRST - it generates this whole pipeline (build, security scans, immutable sha- tags, managed registry login) plus the deploy contract, with push-to-deploy built in; use the manual pattern here when the repository is not an Ankra application or the pipeline must stay hand-rolled. Use when the user wires CI/CD for an Ankra-managed app, mentions GitHub Actions or GitLab CI with Ankra, or asks how to deploy on push.
---

# Ankra CI/CD

The Ankra deploy pattern is **GitOps-driven**: CI builds and pushes an image, then updates the image tag in the GitOps repo. The Ankra engine detects the commit and reconciles the cluster. CI never applies to the cluster directly.

For an application repository, `ankra application add .` generates this whole pipeline - security gates, immutable `sha-` tags, managed registry login and the deploy contract, with push-to-deploy built in. Read "When Ankra generates the pipeline for you" below and the `ankra-applications` skill before hand-rolling anything here.

## Pipeline shape (provider-agnostic)

```
1. Build the image
2. Push with an immutable tag (commit SHA or semver, never `latest`)
3. Bump the tag in the GitOps repo (commit / PR)
4. The Ankra engine syncs the change to the cluster
5. (optional) Verify rollout with the Ankra CLI / API
```

## Why bump the repo, not deploy from CI

- The repo stays the single source of truth (a commit fully describes what runs).
- Rollback is `git revert`.
- No cluster credentials in CI; CI only needs registry push + repo write.
- The same change promotes across environments by committing to the matching path/branch.

## Minimal flow

```bash
IMAGE=registry.example.com/my-app
TAG=${GIT_SHA}                      # immutable
docker build -t "$IMAGE:$TAG" .
docker push "$IMAGE:$TAG"

# update the GitOps repo so Ankra syncs the new tag
# (edit the values/manifest field that holds the image tag, then commit)
```

For full, copy-pasteable GitHub Actions and GitLab CI examples, see [reference.md](reference.md).

## Rules

- **Immutable tags only** — commit SHA or semver, never `latest` or a moving tag.
- **CI updates Git, Ankra deploys.** Do not run `kubectl apply` / `helm upgrade` against the cluster from CI.
- **Least-privilege secrets.** CI needs registry push and GitOps-repo write; it does not need cluster admin.
- **One image, many environments.** Promote by committing the same tag to the next environment's path, don't rebuild.
- **Encrypt any secret** that lands in the repo with SOPS (`ankra-sops-secrets`).

## AI pipeline-failure investigation and auto-fix PRs

When a pipeline fails, Ankra AI can investigate and fix it for you: it reads the failing run's job logs (GitHub Actions, GitLab pipelines, Bitbucket Pipelines via short-lived minted tokens), clones the repo into an ephemeral workspace pod to reproduce, and proposes the exact file changes. In **Agent** mode it opens the fix as a pull request automatically (the PR is the review gate); in **Ask** mode it stops at the proposed patch. `@ankra`-mention a failing PR from an Agent-mode SCM binding to get a fix PR. See `ankra-ai-gateway` for enabling and scoping this.

## When Ankra generates the pipeline for you

For an application registered with `ankra application add`, Ankra writes the Dockerfile, the chart
and the build workflow itself, and `ankra application auto-deploy` decides whether a build on the
tracked branch rolls itself out. Read `ankra-applications` first in that case — this skill is the
shape to aim for, and the reference below is for pipelines you own by hand.

## When there is no pipeline to run

Ankra can also build the image on its own builders, with no workflow in the repository at all:
`ankra application build start <application-id> --commit <full-sha> --wait`. Ankra clones the
commit, resolves a recipe (repository Dockerfile, else generated, else buildpacks), builds and
pushes — no Actions minutes, no runners to operate, no registry credentials in the repository.
Reach for it when the repository's own CI cannot run: a private repo on a plan whose Actions never
start, or a first image needed before anyone merges the setup PR.

It does not replace the whole generated workflow. That workflow also runs a **Semgrep scan** and,
where the application publishes one, packages and pushes a **Helm chart**; the build lane does
neither. Replacing the image build is safe; retiring the pipeline means deciding where those two go
first.

Behind the `platform_builds` organisation flag, off by default. See `ankra-applications` §5b.

## Related skills

- `ankra-applications` for Ankra-generated builds, registries, env-secrets and deploys.
- `ankra-gitops` for the repo layout CI writes into.
- `ankra-cli` for post-deploy verification (`ankra cluster operations list`).
- `ankra-troubleshooting` when the rollout does not come up.
- `ankra-security` for scanning findings and least-privilege CI credentials.
- `ankra-ai-gateway` for AI pipeline-failure investigation and auto-fix PRs, and the Ask/Agent safety modes.
