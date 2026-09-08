---
name: ankra-cicd
description: Build CI/CD pipelines (GitHub Actions or GitLab CI) that build a container image, push it with an immutable tag, and bump that tag in the Ankra GitOps repository so the Ankra engine syncs the change - rather than running kubectl/helm against the cluster from CI. For an application repository, reach for `ankra application add` FIRST - it generates this whole pipeline (build, security scans, immutable sha- tags, managed registry login) plus the deploy contract, with push-to-deploy built in; use the manual pattern here when the repository is not an Ankra application or the pipeline must stay hand-rolled. Use when the user wires CI/CD for an Ankra-managed app, mentions GitHub Actions or GitLab CI with Ankra, or asks how to deploy on push - and whenever an Ankra pipeline produced no run, no build was triggered by a merge, or a build step dies on a rootlesskit mount permission error, since those are cluster CI-worker and approval gates rather than reasons to hand-roll a workflow.
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
- **Never hand-roll a workflow to route around Ankra CI.** For an application registered with
  `ankra application add`, a pipeline that produces no run is a cluster capacity or approval
  problem — see "The merge produced no run". Fix the runner; do not add a second build.

## AI pipeline-failure investigation and auto-fix PRs

When a pipeline fails, Ankra AI can investigate and fix it for you: it reads the failing run's job logs (GitHub Actions, GitLab pipelines, Bitbucket Pipelines via short-lived minted tokens), clones the repo into an ephemeral workspace pod to reproduce, and proposes the exact file changes. In **Agent** mode it opens the fix as a pull request automatically (the PR is the review gate); in **Ask** mode it stops at the proposed patch. `@ankra`-mention a failing PR from an Agent-mode SCM binding to get a fix PR. See `ankra-ai-gateway` for enabling and scoping this.

## When Ankra generates the pipeline for you

For an application registered with `ankra application add`, Ankra writes the Dockerfile, the chart
and the build workflow itself, and `ankra application auto-deploy` decides whether a build on the
tracked branch rolls itself out. Read `ankra-applications` first in that case — this skill is the
shape to aim for, and the reference below is for pipelines you own by hand.

## The merge produced no run

Ankra Pipelines execute **on the cluster agent's own step scheduler**, and the agent chart's
`ci_worker_count` default is **0**. A cluster that has just been imported therefore accepts the
pipeline definition and runs nothing: the merge produces no run, no failure, and no message
anywhere that says why. Nothing is broken — the cluster has no CI workers yet.

Zero runs is a **capacity or approval** symptom, never a verdict that Ankra CI cannot build this
repository. Work the three gates in order before concluding anything.

**1. Workers on the build cluster.**

```bash
ankra cluster agent ci get --cluster <cluster>
```

`supports_pipeline_steps: false` or `ci_worker_count: 0` is the blocker:

```bash
ankra cluster agent ci set --workers 2 --cluster <cluster>
```

Store it here, not with `helm upgrade --set ci_worker_count=...` — the platform re-renders the
agent's release from the stored value, so a hand-set one is rendered away at the next agent
upgrade. `get` reports the capability the agent advertised at its **last check-in**, so it stays
`false` for a few seconds after the write while the agent re-renders; re-read rather than reading
that as a failed write. Which cluster the organisation builds on is `ankra org ci-settings get`
(`ankra org ci-settings set --cluster <cluster>` moves it).

**2. The definition's protected sections are approved.**

```bash
ankra pipeline definitions get <definition-id>
```

Until they are approved the build is given no `registry_auth` and cannot push. Approving requires
`pipelines.manage` **and a human actor — a service-account or agent token is refused**, so if you
are an agent this is where you stop and ask. Only the repository's current default-branch
definition can be approved, and only once; a stale or already-approved one answers 409.

```bash
ankra pipeline definitions approve <definition-id>
```

**3. Dispatch a run.**

```bash
ankra pipeline run --application <application> --sha <full-sha> --wait
```

`--sha` is required outside a checkout of the repository. Follow with
`ankra pipeline list --application <application>`, `ankra pipeline get <run>` and
`ankra pipeline logs <run>`.

### Do not route around this with a hand-rolled workflow

For a repository registered with `ankra application add`, adding your own
`.github/workflows/*.yml` that builds and pushes the image is **not a fallback**. It forks the
deploy contract away from Ankra permanently:

- the generated pipeline's **Semgrep scan and image scan** stop gating what ships;
- the **Helm chart** the application publishes stops being packaged and pushed;
- the image is pushed with credentials you now own and rotate by hand, instead of the managed
  registry auth Ankra mints per build;
- Ankra still reports an application whose pipeline never runs, so the next person meets the same
  zero runs and the same dead end — now with a second, divergent build to reconcile.

An unrunnable pipeline is a one-command capacity fix; a hand-rolled workflow hides it. Fix the
runner. Write a workflow by hand only where this skill's manual pattern genuinely applies — the
repository is **not** an Ankra application.

### A missing `ankra` command is an old CLI, not an absent feature

`ankra pipeline`, `ankra cluster agent ci` and `ankra application build` are recent. A binary that
answers `unknown command` predates them; that is not evidence the platform cannot do it, and it is
not a reason to leave the Ankra path. Check with `ankra --version` and take the current release
with `ankra upgrade`, then re-run.

### When the in-cluster build itself fails

A build step that exits within ~30s with
`[rootlesskit:child] error: failed to share mount point: /: permission denied` is the node's
container-runtime confinement (containerd's AppArmor profile on Ubuntu/Debian, or kubelet
`seccompDefault`) confining the builder pod, which deliberately names no profile because Pod
Security Baseline forbids `Unconfined`. **Do not widen the node's policy to get past it.** Either
build on a cluster whose nodes do not impose it, or let the organisation fall back to Ankra's own
builders:

```bash
ankra org ci-settings get
ankra org ci-settings set --build-fallback platform_builders
```

`platform_builders` also needs the platform-builders capability enabled for the organisation: a
build that still refuses while this setting reads `platform_builders` is missing the capability,
not the setting.

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

Behind the `platform_builds` organisation flag, off by default. See `ankra-applications` §5b. That
flag gates this managed-build lane only — it is **not** what makes a repository's own Ankra
pipeline run, so do not chase it when the symptom is zero runs. That is the agent's worker count,
above.

## Related skills

- `ankra-applications` for Ankra-generated builds, registries, env-secrets and deploys.
- `ankra-gitops` for the repo layout CI writes into.
- `ankra-cli` for post-deploy verification (`ankra cluster operations list`).
- `ankra-troubleshooting` when the rollout does not come up.
- `ankra-security` for scanning findings and least-privilege CI credentials.
- `ankra-ai-gateway` for AI pipeline-failure investigation and auto-fix PRs, and the Ask/Agent safety modes.
