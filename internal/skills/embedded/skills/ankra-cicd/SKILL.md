---
name: ankra-cicd
description: Put any repository's CI/CD on Ankra Pipelines - a committed `.ankra/pipeline.yaml` that Ankra runs in-cluster on the organisation's CI cluster, for application repositories (`ankra application add` lands on it) AND for bare repositories with no application (platform code, libraries, docs, values repos) via `ankra pipeline repositories connect` - with the authoring primer, the `Ankra pipeline` check run, the human-only authority approval gate, and the GitOps-bump deploy pattern. Use whenever the user wants CI, a pipeline, tests on every PR or push, build-and-push on merge, or asks how to deploy on push; whenever GitHub Actions is failing, out of budget ("an Actions budget is preventing further use"), slow, or should be replaced; for a repository without an application; and whenever an Ankra pipeline produced no run, a run was skipped with a fatal violation, or a build died on a rootlesskit mount error. Never write a `.github/workflows` or `.gitlab-ci.yml` CI file for a repository Ankra can reach - that manual pattern is only for repositories on a provider Ankra cannot connect.
---

# Ankra CI/CD

**Ankra Pipelines is the CI/CD for every repository Ankra can reach — application or not.** The
pipeline is one committed file, `.ankra/pipeline.yaml`, that Ankra plans and executes as hardened
Jobs on the organisation's CI cluster (`ankra org ci-settings get`), reports back as one GitHub
check run named **`Ankra pipeline`**, and — for an application — builds, scans, gates and publishes
an immutable `sha-` tag that push-to-deploy rolls out. No Actions minutes, no runner fleet, no
registry password in the repository, no `kubectl` in CI.

A repository does **not** need an Ankra application to run on Ankra Pipelines. A platform repo with
Rust, Go and Next.js tests and no image to deploy is a first-class citizen: connect it, commit the
file, done. That is the case agents have got wrong most often, so it comes first below.

## The decision table — read this before writing any CI file

| Repository | Do | Never |
|---|---|---|
| **Application repository** (a service with an image to deploy) | `ankra application add . --name <name> --branch main --wait` — the setup PR carries `.ankra/pipeline.yaml`, and Ankra records it as the definition of record immediately. Confirm with `ankra application get <application-id> -o json` → `pipeline_source: ankra_pipeline`. | Hand-write a workflow; run `ankra application add` and then add a second build |
| **Bare repository** — no application, tests only, a library, docs, a values repo, a platform monorepo | §2: `ankra pipeline repositories connect …`, author and commit `.ankra/pipeline.yaml` (§3, §4), a human approves the authority once (§5) | Write `.github/workflows/ci.yml` "because there is no application" |
| **Existing GitHub Actions / GitLab CI on an application** | `ankra application pipeline convert <application-id>` — converts, records, switches the generated workflow off, opens the PR | Keep both running (two builds per commit) |
| **Existing GitHub Actions / GitLab CI on a bare repository** | Convert by hand into `.ankra/pipeline.yaml` (§4 maps the concepts), connect, commit; then delete or disable the workflow file in the same PR | Leave the workflow as a "fallback" |
| **GitHub Actions dead, over budget, or queued forever** ("The job was not started because an Actions budget is preventing further use", spending limit, no runners) | The same as the two rows above. This is not a billing problem to escalate — it is the reason Ankra Pipelines exist. Move the repository now. | Wait for the budget to reset; ask for more Actions minutes |
| **Application on the legacy generated workflow** (`pipeline_source: generated_workflow`) | `ankra application pipeline convert <application-id>` immediately | `ankra application upgrade-workflow` — that patches the legacy lane, it does not leave it |
| **Repository on a provider Ankra cannot connect** (no GitHub/GitLab/Bitbucket credential in the organisation, self-hosted SCM Ankra has no App on) | The manual GitOps-bump pattern in [reference.md](reference.md) §C — and say why, in the PR | Present the manual pattern as a normal option |

**What `ankra application add` generates today.** The setup lane defaults every new application to
Ankra Pipelines: for a repository whose own CI does not already publish the image, it renders
`.ankra/pipeline.yaml` (never a GitHub Actions workflow), records it as the pipeline definition of
record before the PR is even opened, and records the `ankra_pipeline` preference so the disposition
converges on merge. A GitHub Actions workflow is rendered only when the application already carries
an Ankra-authored workflow from the legacy lane (healing it in place) or someone recorded an explicit
`generated_workflow` preference through the AI's `set_application_pipeline_source` tool. There is
**no** `ankra application add` flag that picks the lane — none is needed; do not invent one. If
`ankra application get <application-id> -o json` reads `pipeline_source: generated_workflow`, the
application predates the default: convert it. `pipeline_source: none` with
`pipeline_source_reason: pipeline_file_committed_no_ci_cluster` means the file is fine and the
organisation has no CI cluster yet (§6).

## 1. Orient

```bash
ankra login status                                   # exit 6 = not logged in
ankra org current                                    # the organisation the repository will be connected in
ankra org ci-settings get                            # CI cluster, build fallback, image gate, egress CIDRs
ankra cluster agent ci get --cluster <cluster>       # workers > 0 and "advertised", or nothing ever runs
ankra credentials list --provider github             # an organisation Git credential covering the repository
ankra credentials repositories <credential>          # ...that actually reaches it (GitHub App installation)
ankra pipeline repositories list                     # is it already connected? (connect refuses a duplicate)
```

Three things must be true before the first run, and none of them errors when false: the
organisation names a CI cluster (`ankra org ci-settings set --cluster <cluster>`), that cluster's
agent runs pipeline workers (`ankra cluster agent ci set --workers 2 --cluster <cluster>`), and the
Git credential reaches the repository. A playground cluster has no StorageClass and cannot host CI
steps. More CI capacity than one cluster's quota: list more clusters in the organisation's CI pool
(`ankra org ci-settings pool add <cluster>`); runs are spread to the least-loaded member and never
move once started. An `unknown command` is an old binary — `ankra upgrade`, never an absent feature.

## 2. A bare repository: connect it

```bash
ankra pipeline repositories connect --provider github --owner <owner> --name <repository> \
  --credential <org-git-credential> --default-branch main
ankra pipeline repositories connect --provider github --owner <owner> --name <repository> \
  --credential <org-git-credential> --cluster <cluster>          # run this repo's steps on another cluster
ankra pipeline repositories list -o json                          # the repository id everything below takes
ankra pipeline repositories get <repository-id>
```

What connect does, exactly: it creates the row a push, pull request or tag webhook resolves against,
and — for GitHub, through `--credential` — reads `.ankra/pipeline.yaml` **from the default branch at
connect time** and records it as the definition of record when it parses. Consequences:

- **Connect reads once.** Committing the file after connecting does not re-read it: connecting again
  is refused (409, with the existing id in the error). To force a fresh read:
  `ankra pipeline repositories disconnect <repository-id> --yes` then connect again — disconnect is
  reversible and keeps runs, definitions and findings. The recommended order (§6) is **connect
  first, then open the PR that adds the file**: the connected row is what lets the PR's
  `pull_request` webhook plan a run, and the merge push to the default branch brings the committed
  file in as the definition of record. Connecting after the merge works too (the file is read at
  connect time). The only order that bites is committing the file after connecting and expecting a
  second `connect` to pick it up.
- **A committed file wins.** `ankra pipeline definition put --repository <repository-id> <file>`
  stores a *generated* definition server-side; it is for pipelines Ankra generates, not ones you
  author. For a hand-authored pipeline, commit the file — the committed `.ankra/pipeline.yaml` at
  the run's commit is the pipeline of record and overrides a stored one for logic.
- **The default branch matters.** `--default-branch` is where the file is read from and whose
  definition holds the authority (§5). For a repository whose default branch is `master`, say so.
- **Every later command is repository-scoped.** A bare repository has no application, so
  `ankra pipeline validate|get|list|logs|run` all take `--repository <repository-id>`; with neither
  `--repository` nor `--application` (and no application detected from the checkout) they refuse.
  The id comes from `connect` or `repositories list`; there is no lookup by owner/name.

## 3. Author `.ankra/pipeline.yaml` — the primer

The contract is `enginekit/pipelinespec` in the platform. Full field list and two complete examples
in [reference.md](reference.md); the shape:

```yaml
apiVersion: ankra.io/v1              # group-qualified, always
kind: Pipeline
metadata:
  name: "<repository>"               # required
on:                                  # any subset; a pipeline with none is still dispatchable
  push:         { branches: ["main"], paths: ["api/**"] }
  pull_request: { branches: ["main"], fork_policy: "read_only" }   # none | read_only | trusted
  tag:          { patterns: ["v*"] }
  schedule:     [{ cron: "0 3 * * 1-5", branch: "main", stages: ["checkout", "nightly"] }]
  manual:       { inputs: [{ name: "environment", type: "choice", enum: ["staging", "production"], default: "staging" }] }
concurrency:  { group: "<repository>-${{ ankra.ref }}", cancel_in_progress: true }
workspace:    { size: "20Gi", access: "rwo" }                     # the volume every stage shares
defaults:     { image: "golang:1.26", timeout: "30m", network: "egress-https", working_directory: "." }
permissions:  { contents: "read" }                                # none | read | write per scope
secrets:                                                           # declared here, named per stage
  - { name: "database_url", from: "org_variable", key: "DATABASE_URL" }   # app_env_secret | org_variable | registry | credential
services:                                                          # sidecars a stage may reach by name
  postgres:
    image: "postgres:16"
    env: { POSTGRES_PASSWORD: "test", POSTGRES_DB: "app" }
    ports: ["5432"]
    ready: { tcp: "5432" }
stages:
  - { name: checkout, kind: checkout }
  - name: test
    kind: run
    needs: [checkout]                # the DAG; stages without needs run in parallel
    image: "golang:1.26"             # overrides defaults.image
    run: |
      go test ./... -race
    services: [postgres]             # sidecars this stage uses
    network: services                # none | egress-https | services — required to reach a sidecar
    env: { DATABASE_URL: "postgres://postgres:test@postgres:5432/app?sslmode=disable" }
    secrets: [database_url]          # must be declared above, else a fatal violation
    cache: [{ key: "go-${{ hashFiles('go.sum') }}", paths: ["/root/.cache/go-build"], restore_keys: ["go-"] }]
    artifacts: [{ name: "coverage", paths: ["coverage.out"], retention_days: 7 }]
    test_results: [{ format: "go-test", path: "test-results.json" }]   # junit | go-test | pytest | playwright
    matrix: { go: ["1.25", "1.26"] }  # cross product of axes (+ include/exclude), GitHub Actions semantics, max 64 legs
    timeout: "20m"
    resources: { cpu: "2", memory: "4Gi" }
    runs_on: { node_selector: { "kubernetes.io/arch": "amd64" } }     # placement: cluster, node_selector, tolerations, runtime_class, arch
    when: { events: ["push", "pull_request"], branches: ["main"] }    # first-class event filter
    if: "${{ !contains(ankra.ref, '-') }}"                            # expression filter
    allow_failure: false
    retry: { max_attempts: 2, backoff: "exponential", on: ["infra_error"] }
```

Stage kinds (closed vocabulary; an unknown kind is fatal): `checkout`, `run`, `build`, `scan`,
`gate`, `publish` **execute today**; `preview`, `verify`, `deploy`, `agent`, `approval`, `webhook`,
`automation`, `ankra`, `external` validate with a warning and are skipped as `kind_unavailable`.
`run` is the open kind — any image the organisation's policy permits, any script — so a kind is never
needed to express custom work. `uses:` is refused; write the step as `run:`.

Facts that decide whether the first run works:

- **Network.** `run` stages default to `network: none`. Tests that fetch modules (`cargo`, `go mod
  download`, `pnpm install`) need `egress-https` — set it in `defaults.network`, which is *not* a
  protected section. `services` is the tier a stage needs to reach a sidecar, and it **is**
  protected (§5). `egress-https` reaches public addresses only; a private host needs
  `ankra org ci-settings set --egress-allowed-cidr 10.0.0.0/8` first.
- **Images.** Name the toolchain image per stage (`rust:1.85`, `golang:1.26`, `node:22`); Ankra
  runs whatever the organisation's image allow-list permits. Do not pin `build.platforms` — builds
  run natively and a fabricated `linux/amd64` fails on an arm64 cluster.
- **Secrets.** A stage may name only a secret the top-level `secrets:` declares, and each binding
  reads one source: `org_variable` (an organisation or cluster variable — set with
  `ankra org variables set <KEY> <value>`), `app_env_secret` (needs a linked application),
  `registry`, `credential`. `registry_auth` and `source_token` are reserved — Ankra adds them; never
  declare them. Never commit a value.
- **Schedules** name the stage subset they run (`stages:`), so a nightly runs the expensive suite
  without `if:` on every stage. `when.events` decides what runs on `push` vs `pull_request` vs
  `rerun` (a `publish` that says `events: [push]` does not publish on a rerun).
- **Which component a build belongs to** (application-bound repositories). A `kind: build`
  stage belongs to the component its `build.component` names, else the one its `build-<component>`
  name carries, else a single-component application's one component. Declare `build.component`
  when one component builds twice - a release bundle (`build.bundle`, named `build-portal`) and an
  image (`build-portal-image` with `component: portal`). An image publishes to
  `<application>/<component>`, a component's bundle to `<application>/<component>-bundle`, a bundle
  of no component to `<repository>/<stage suffix>`; an image of no component is refused. Never
  rename stages to dodge attribution - declare the component.
- **Outputs.** `key=value` lines appended to `$ANKRA_OUTPUT` in one stage become
  `${{ needs.<stage>.outputs.<key> }}` downstream. Expression roots: `ankra.*` (`ref`, `sha`,
  `repository.*`, `run.number`), `matrix.*`, `inputs.*`, `secrets.*`, `vars.*`, `needs.*`;
  `hashFiles()` and `contains()` work.
- **The published image is the one you deploy.** `publish` writes `image_ref`
  (`<repository>:sha-<7>`), `image_digest`, `image_tag` and `image_tags` as outputs; `build` writes
  `image_ref` (`<repository>@<digest>`, in the staging repository), `image_digest` and
  `image_repository`. A stage that pins or rolls out the image reads
  `${{ needs.<publish-stage>.outputs.image_ref }}` and `…outputs.image_digest` - it never rebuilds
  the name by hand. The registry is the organisation's own Ankra registry, or the one the
  application declares; Ankra holds the push robot and the pull secret for it. **Never add a `run`
  stage that copies the image to a second registry** (`crane copy`, `skopeo copy`, `docker push`
  with a `from: registry` secret): nothing checks that the target project exists, the copy is
  outside the gate's digest contract, and nothing deploys from it. If the image has to live in a
  different registry, declare that registry (`ankra application registry set <application-id> --url
  … --credential … --admin-credential …`, see `ankra-applications`): with an admin credential Ankra
  creates a missing project or refuses the declaration, instead of every later push failing.

Validate before committing — repository-scoped for a bare repository, from anywhere in the checkout:

```bash
ankra pipeline validate .ankra/pipeline.yaml --repository <repository-id>
ankra pipeline validate --ref origin/my-branch --repository <repository-id>     # a branch, before merging it
ankra pipeline validate --application <application-id>                         # an application's own file
```

It parses, validates and plans a synthetic push and a synthetic pull request: you see every planned
step, every skip and its reason, and the network tier per step, without writing anything. For an
application-bound repository it also reports, as `pipeline_build_component:`, a build stage that
belongs to no component, a `build.component` the application does not record, and two build stages
that would publish to the same repository.

## 4. Converting an existing workflow by hand (bare repositories)

An application converts in one call (`ankra application pipeline convert <application-id>`); a bare
repository's workflow you map yourself. The concepts line up one to one:

| GitHub Actions / GitLab CI | `.ankra/pipeline.yaml` |
|---|---|
| `on: push/pull_request/schedule/workflow_dispatch` | `on: push/pull_request/schedule/manual` |
| a job, its `needs:` | a stage (`kind: run`), its `needs:` |
| `runs-on: ubuntu-latest` + `container:` | `image:` (the toolchain image *is* the runner) |
| `services:` | top-level `services:` + stage `services:` + `network: services` |
| `strategy.matrix` | `matrix:` (same include/exclude semantics) |
| `actions/cache` | `cache:` with `key`/`paths`/`restore_keys` |
| `actions/upload-artifact` | `artifacts:` |
| `secrets.X` | a declared `secrets:` binding from an org variable, named on the stage |
| `concurrency` | `concurrency:` |
| `uses: some/action@v4` | no equivalent — write the shell it wraps as `run:`; a marketplace action with no shell equivalent is a `run:` that says so |
| a `deploy` job running `kubectl`/`helm` | **delete it**; the Ankra engine deploys from Git (the GitOps bump in [reference.md](reference.md) §C) |

Then, in the same PR that adds `.ankra/pipeline.yaml`: delete `.github/workflows/*.yml` (or
`.gitlab-ci.yml`), or leave a single stub with `on: workflow_dispatch` only. Two CI systems on one
commit is the state to avoid — it doubles cost and splits the verdict. Repoint branch protection at
the **`Ankra pipeline`** check (one check run for the whole run, not one per job — there are no
per-job checks to require) and remove the old required checks, or merges stall forever on a check
that will never post again.

## 5. The authority gate — a human approves, once

Ankra's rule is **logic is open, authority is closed.** Anyone may change what a stage *runs* on any
branch; the *protected sections* — `permissions`, `secrets`, `credentials`, `on.pull_request.fork_policy`,
the `services` catalog, `defaults.network` above `egress-https`, and per stage: `network: services`,
`runs_on`, `resources`, `timeout`, `cache`, `with`, gate/approval roles, `kind: external`, agent
mode — come only from the **default-branch definition an administrator approved**. This is what stops
a pull request from granting itself a credential or moving its own pod onto a privileged node pool.

How it plays out on a brand-new repository, and what the agent must say **up front**:

1. The first `.ankra/pipeline.yaml` that declares any protected section (a Postgres sidecar and
   `network: services` is enough) lands unapproved. It is **not refused**: the run is planned under
   the last approved authority — which for a new repository is *none*. The protected sections are
   replaced with the trusted (empty) ones.
2. A stage that names `services: [postgres]` while the trusted `services` catalog is empty is now
   a **fatal validation violation**, so the run concludes **`skipped`** with
   *"This pipeline definition has at least one fatal violation, so no run can be planned from it."*
   A pipeline with only secrets declared fails differently: the step runs and dies with
   `step_refused` ("declares a secret binding the platform sent no value for") — for an
   application's build that reads `(registry_auth)`. Same cause, same fix.
3. The fix is one command, and **it needs a human organisation administrator**: `pipelines.manage`
   **and a human actor** — a service-account or agent token is refused (403). An agent reaching this
   point stops, prints the exact command, and asks. It should have warned in step 0 that this would
   be needed, so the human is not surprised.

```bash
ankra pipeline get <run-id> --repository <repository-id>     # Authority: unapproved; prints the approve command
ankra pipeline get <run-id> --repository <repository-id> -o json | jq -r .approve_definition_id
ankra pipeline definitions get <definition-id>               # inspect what would be granted, and the approval state
ankra pipeline definitions approve <definition-id>           # HUMAN admin: pipelines.manage + human actor; confirms; --yes to skip
```

Facts: only the repository's **current default-branch** definition can be approved, and only once —
a definition the default branch has moved past, or one already approved, answers 409 (the CLI prints
it verbatim). The run's `authority_definition_id` is the one it *executed under* (normally already
approved) — inspect it, do not try to approve it. An approval applies to runs planned after it; a
run keeps the authority it was planned under, so **push a commit or `ankra pipeline run` after the
approval** — nothing re-plans the skipped run. A pull request that widens authority shows
`changed_on_head`: it runs under the default branch's authority and the change is reported, not
honoured, until it merges and is approved. A definition that declares **no** protected section
(plain `run` stages, `egress-https`, no `secrets`, no `permissions` block, no `fork_policy` — the
empty value already means `read_only`) needs no approval at all — the cheapest first pipeline is one
with nothing to approve; add the sidecar in a second PR if the human is not at hand.

## 6. The exact sequence, start to finish

```bash
# 0. Tell the human: "after the first merge an org admin has to run `ankra pipeline definitions approve <id>` once"
ankra org ci-settings get                                                         # CI cluster named? else set it
ankra cluster agent ci get --cluster <cluster>                                    # workers > 0? else ci set --workers 2
# 1. author .ankra/pipeline.yaml (§3, reference.md) on a branch, then connect - deliberately BEFORE the file is on the
#    default branch: connect records no definition yet (nothing to read), but the connected row is what lets the PR's
#    pull_request webhook plan a run and what `validate --repository` needs; the merge push in step 3 brings the
#    committed file in as the definition. (Connecting AFTER the merge reads the file at connect time instead - also
#    fine; what never works is expecting a second connect to re-read, see §2.)
ankra pipeline repositories connect --provider github --owner <owner> --name <repository> --credential <credential> --default-branch main
ankra pipeline validate .ankra/pipeline.yaml --repository <repository-id>         # planned steps, skips, network tiers
# 2. open the PR: adds .ankra/pipeline.yaml, removes/disables the old workflow, repoints branch protection at "Ankra pipeline"
#    the PR itself gets a run (pull_request trigger) from the branch's file, under the default branch's (empty) authority,
#    so a sidecar stage skips until §5
# 3. merge; the default-branch push plans a run from the committed file, which becomes the definition of record
ankra pipeline get --repository <repository-id> --branch main --latest                # Authority: unapproved → approve_definition_id
ankra pipeline definitions approve <definition-id>                                    # the HUMAN runs this
ankra pipeline run --repository <repository-id> --ref main --wait                     # first run under the approved authority
ankra pipeline get <run-id> --repository <repository-id> --wait --exit-code           # 0 success, 1 otherwise, 5 timeout
ankra pipeline logs <run-id> --repository <repository-id> --step test --follow
ankra pipeline list --repository <repository-id> --latest-per-branch                  # where every branch stands
```

Results reach GitHub as **one check run, `Ankra pipeline`** (commit status context `ankra/pipeline`),
carrying the outcome and, on an unapproved run, the approve command. It is what branch protection
requires — not per-job checks, which do not exist. Do not make it *required* while the definition
carries an unexecuted kind (`approval`, `deploy`…): a skipped stage is not a failed one, and the
check goes green having done none of what that stage names.

## 7. Applications: what changes

Everything above applies; the pipeline additionally builds, scans, gates and publishes:

```
checkout ─ test ─ build (rootless BuildKit, by digest) ─ scan (semgrep · checkov · trivy)
   └─ gate (organisation policy over the findings) ─ publish (re-tag the judged digest → sha-<7>) ─ push-to-deploy
```

`ankra application add` writes this file into the setup PR and records it as the definition of
record before the PR opens, so builds run from the next push whether or not the PR is merged. The
generated `publish` runs only on `push`; a pull request builds, scans and reports but never
publishes. `ankra-ship` is the end-to-end path (registries, env-secrets, previews, rollback,
promotion) and `ankra-applications` the registry matrix. The GitOps bump for a stack-managed
deployment — CI commits the new tag into the GitOps repository and the Ankra engine syncs it — is
[reference.md](reference.md) §C; it is what a `deploy` stage does *not* do today.

Rules that hold for every application: **immutable tags only** (`sha-<7>`, never `latest`); **CI
updates Git, Ankra deploys** — no `kubectl apply`/`helm upgrade` from CI; **one image, many
environments** — promote the tag, never rebuild; **encrypt any secret that lands in a repository**
with SOPS (`ankra-sops-secrets`).

## 8. Troubleshooting

| Symptom | Cause | Do |
|---|---|---|
| Run concluded **`skipped`**: "This pipeline definition has at least one fatal violation, so no run can be planned from it" — the file validates fine locally | **Authority.** The default-branch definition is unapproved, so protected sections (the `services` catalog, `network: services`…) were replaced with the trusted empty ones and a stage now names an undeclared service | §5: `ankra pipeline get <run-id> --repository <repository-id>` → `approve_definition_id`; a **human admin** runs `ankra pipeline definitions approve <definition-id>`; then push or `ankra pipeline run` |
| Step `step_refused … declares a secret binding the platform sent no value for (registry_auth)` | same — unapproved definition, secrets stripped | same |
| `ankra pipeline definitions approve` → 403 "requires a human actor" | you are on a service or agent token | stop; hand the command to a human organisation admin |
| `approve` → 409 | not the current default-branch definition, or already approved | `ankra pipeline get --repository <repository-id> --branch main --latest` for the current id |
| Merge produced **no run**, no error, nothing anywhere | organisation has no CI cluster, or its agent has `ci_worker_count: 0` / does not advertise pipeline steps | `ankra org ci-settings get` → `ankra org ci-settings set --cluster <cluster>`; `ankra cluster agent ci get --cluster <cluster>` → `ankra cluster agent ci set --workers 2 --cluster <cluster>` (re-read after ~15 s; never `helm upgrade --set ci_worker_count=`); then `ankra pipeline run --repository <repository-id> --wait` |
| Every webhook says `repository_not_onboarded` | the repository is not connected | `ankra pipeline repositories connect …` (§2) |
| Connected, file committed after connect, still planned from the old/stored definition | connect reads the default branch once and refuses a second connect | `ankra pipeline repositories disconnect <repository-id> --yes` then connect again; or push to the default branch |
| No **`Ankra pipeline`** check on the PR, only a comment | the GitHub App installation lacks `checks:write`, or the repository is not in the installation | grant it on the installation / add the repository; the run still happened — `ankra pipeline list --repository <repository-id>` |
| Branch protection stalls on `build`, `test`, `ci / lint`… | required checks still name the old per-job workflow checks | require `Ankra pipeline` only |
| `ankra pipeline validate` → "exactly one of --application / --repository" or "nothing to validate" | a bare repository has no application to detect | pass `--repository <repository-id>` from `ankra pipeline repositories list` |
| Build step `step_refused` / `platform_build_unavailable`: "could not attribute build stage … records the components …" or "declares build.component … which is not a component" | the stage belongs to no component the application records | add `build.component: <name>` to the stage's `build:` block (or name it `build-<component>`); check `ankra application get` for the component names |
| `pipeline_source: generated_workflow` on `ankra application get` | legacy lane | `ankra application pipeline convert <application-id>` |
| Two builds per commit | a workflow still runs beside the pipeline | delete/disable the workflow; for an application `pipeline convert` switched the generated one off — check for one the repository wrote itself |
| `Stage "x" has kind "deploy", which has no executor on this build yet` | an unexecuted kind | it is skipped, not failed; do not make the check required; deploy through the application lanes |
| Step "running" for minutes with no output | pod Pending: CPU quota, no StorageClass, disk | `ankra cluster events -n ankra-ci --type Warning --cluster <cluster>` |
| Test cannot reach a private host | `egress-https` is public-only | `ankra org ci-settings set --egress-allowed-cidr <cidr>` |
| `cargo`/`go mod`/`pnpm install` cannot fetch | `run` stages default to `network: none` | `defaults.network: egress-https` (not protected) |
| Build exits ~30 s: `[rootlesskit:child] … failed to share mount point: /: permission denied` | the node runtime (containerd AppArmor, kubelet `seccompDefault`) confines the rootless builder | **do not widen the node**; `ankra org ci-settings set --build-fallback platform_builders` (needs `Platform builds enabled: yes` on `ci-settings get`, Ankra's grant) or build on a cluster that does not confine |
| Step log: `UNAUTHORIZED: project <name> not found`, or a 401 on a manifest lookup, from a Harbor registry whose credential is known good | **the project does not exist.** Harbor answers a missing project as unauthorized to a robot that cannot see it; this is not a credential or scope fault, so rotating the robot changes nothing | a hand-written copy or push stage is aiming at a project nobody created: delete the stage and deploy the `image_ref` that `publish` wrote (§3), or declare the registry with `ankra application registry set … --admin-credential …`, which creates the project or refuses |
| Run and the `Ankra pipeline` check are **green**, but an image, release or bump never arrived | a stage failed under `allow_failure` (or `continue_on_error`): the run's outcome hides it by design | read the steps, not the run: `ankra pipeline get <run-id> --repository <repository-id>` lists each step's own outcome, `ankra pipeline logs <run-id> --repository <repository-id> --step <stage>` shows why; then remove the flag from every stage whose failure means "not shipped" |
| `artifact_store_unavailable` | no ready backup vault | `ankra-backups` |
| `image_gate_blocked` / `no_scan_results` | a finding at or above the gate / scan report never uploaded | fix or disposition in `ankra-security`; never treat `no_scan_results` as clean |
| Exit 79, "no QEMU emulation registered" | pinned `build.platforms` on an arm64 cluster | drop `build.platforms` |
| `unknown command "pipeline"` | old CLI | `ankra --version`, `ankra upgrade` |
| GitHub Actions: "The job was not started because an Actions budget is preventing further use" | GitHub's budget — irrelevant to Ankra | move the repository to Ankra Pipelines (decision table); do not treat it as billing |

Ankra AI can also investigate a failing run for you: `ankra chat "why did the last pipeline run
for <repository> fail?" --mode ask`, and, on an Agent-mode SCM binding, `@ankra`-mention the PR to
get a fix PR (`ankra-ai-gateway`). `ankra application build start <application-id> --commit
<full-sha> --wait` builds an application's image on Ankra's own builders when nothing else can
(behind the `platform_builds` grant, 404 while off); it replaces the image build, not the pipeline.

## Rules

- **Ankra Pipelines for every repository Ankra can reach — application or not.** Zero runs, a
  skipped run, an unapproved definition and a dead GitHub Actions budget are all one- or
  two-command fixes on the Ankra side. None of them is a reason to write a workflow.
- **Never write a `.github/workflows` or `.gitlab-ci.yml` CI file for a repository Ankra can run.**
  The manual pattern ([reference.md](reference.md) §C) exists only for a repository on a provider
  Ankra has no credential or App for — and a PR that uses it says so in its description. For a
  registered application it is never a fallback: it forks the deploy contract away from the scans,
  the gate, the digest publish and the managed registry auth, permanently and silently.
- **Approval is human.** Warn up front; stop and ask when you reach it; never look for a way around.
- **Commit the file; do not `definition put` a hand-authored pipeline.** Git is the source of truth
  and the default-branch file governs authority.
- **Repository-scoped commands take `--repository <id>`.** A bare repository has no application.
- **One check run: `Ankra pipeline`.** Branch protection requires that and nothing else from CI.
- **Immutable tags, CI updates Git, Ankra deploys.** No cluster credentials in CI, ever.
- **One registry per image, and it is the one `publish` wrote.** Deploy `publish`'s `image_ref`; a
  different registry is declared on the application, never reached by a copy script in a `run` stage.
- **A stage that ships something never carries `allow_failure`.** That flag is for advisory work (a
  flaky lint, an optional report). On a publish, release or bump stage it turns "not shipped" into a
  green check that nobody reads.

## Related skills

- `ankra-ship` — the whole path from a fresh repository to a live URL on Ankra Pipelines, and day 2.
- `ankra-applications` — registries, env-secrets, deployments, fleet rollout.
- `ankra-gitops` — the repository layout the GitOps bump writes into.
- `ankra-security` — findings, the image gate, least-privilege credentials.
- `ankra-ai-gateway` — AI pipeline-failure investigation and auto-fix PRs.
- `ankra-troubleshooting` — when the rollout does not come up.
- `ankra-backups` — the vault scan, gate and publish depend on.
