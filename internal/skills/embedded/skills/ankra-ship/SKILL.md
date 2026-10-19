---
name: ankra-ship
description: Ship code with Ankra - take a fresh (vibe-coded) project or any repository to a live URL through Ankra Pipelines, then run it on day 2 - the generated `.ankra/pipeline.yaml` that tests, builds rootless in your own cluster, scans with Semgrep, Checkov and Trivy, gates on findings and publishes an immutable `sha-` tag; development following every merge and a person promoting the same tag to production; pull-request previews, environment secrets, rollback, alerts; and Ankra AI reviewing every pull request, watching runs and opening fix PRs. Use when the user says ship it, deploy this, get it live, go to production, wants CI/CD for a new or AI-generated project, asks how to test, build, scan, package or release code with Ankra, or asks what to do once it is live. This is the default skill for shipping; `ankra-applications` and `ankra-cicd` are its detail.
---

# Ship with Ankra

One repository, one contract, one command to a live URL — and everything that comes after. Ankra
Pipelines run **inside your own cluster**: every push is checked out, tested, built rootless by
digest, scanned, judged against the organisation's image gate and, only if it passes, published
under an immutable `sha-<7>` tag that push-to-deploy rolls out and a preview can be cut from. Ankra
AI reads every pull request, watches every run, and opens the fix when a build breaks. Nothing here
needs GitHub Actions minutes, a runner fleet, `kubectl` in CI, or a registry password in the repo.

Use this skill first whenever the job is *get this code running and keep it running*. Reach for
`ankra-applications` for the registry matrix and fleet-wide rollout, `ankra-cicd` for a repository with
no application (connect it, author `.ankra/pipeline.yaml`), the authority gate, fast CI and the GitOps bump,
`ankra-troubleshooting` when the workload is up but unhealthy, and `ankra-ai-gateway` for the Ask/Agent binding modes the AI acts under.

## Recommended path

Golden path 1, **Ship code** (and the deploy stage of golden path 5, agent-driven work; the canonical
list is in `ankra-platform-principles`). Make the source shippable (§0), then `ankra application ship
. --cluster <dev-cluster>`: registration, the setup pull request (Dockerfile, manifests,
`.ankra/ankra.yaml`, `.ankra/pipeline.yaml`), a pipeline that tests, builds, scans and gates in your
own cluster, an immutable `sha-` tag. **Development follows every merge; a person promotes the same
tag development proved to production** (§7). Done means evidence, not a green command (§7). Never
hand-write a GitHub Actions workflow, Kubernetes manifests, `kubectl` or `helm` for this.

## The contract

```
push / pull request / tag / schedule / manual
  └─ checkout ─ test ─ build (rootless BuildKit, by digest) ─ scan (semgrep · checkov · trivy)
       └─ gate (organisation policy over the findings) ─ publish (re-tag the judged digest → sha-<7>)
            └─ push-to-deploy ─ Live: https://…        pull request → preview URL + AI status comment
```

Three facts decide everything else. **The gate is preventive, not advisory** — publish is a re-tag
of the exact digest the gate judged, so nothing unscanned can reach a cluster. **Logic is open,
authority is closed** — anyone can change what a stage runs on a branch, but credentials,
permissions, network tier, placement and approval roles come from the default-branch definition an
administrator approved. **Git is the source of truth** — the file at the run's commit is the
pipeline of record; a committed file that fails validation refuses the run rather than falling back.

## 0. Make the source shippable (do this to a vibe-coded project first)

An AI-generated project usually runs on a laptop and nowhere else. Before registering anything,
check and fix, in the repository, in one commit:

- **One process, one port, from the environment.** The server listens on `PORT` (or the framework's
  equivalent) and binds `0.0.0.0`, not `localhost`.
- **Health endpoint.** `GET /healthz` (or `/health`) answering 200 without touching a database.
- **Configuration from environment variables only.** No `config.local.json`, no `.env` read at
  runtime that is not also declared. Every secret becomes an env-secret (§7); every non-secret
  becomes a variable.
- **Nothing secret committed.** Keys in the tree are published with the image. Rotate anything
  already committed; do not just delete it from the next commit.
- **A lockfile and a single test command.** `package-lock.json`/`pnpm-lock.yaml`, `go.sum`,
  `poetry.lock`/`requirements.txt`. The test command Ankra detects becomes the `test` stage.
- **A `Dockerfile` if the framework is unusual.** Ankra generates one for Node (Next.js, React,
  Express, NestJS), Python (FastAPI, Django, Flask), Go, Rust, Java, Ruby, PHP, Elixir and static
  sites - a Vite single-page app (Lovable's shape: `build` script, no `start`) gets an unprivileged
  nginx on 8080 with SPA fallback and `/healthz`. A Dockerfile you commit is never overwritten.
- **A `.dockerignore`**, and migrations that run on start or on one documented command (§8).
- **Tell the coding agent what is Ankra's job**: no Kubernetes manifests, no CI workflows. When the
  analysis finds the app needs Postgres, the setup PR adds a CloudNativePG database with
  `DATABASE_URL` wired, one per environment.

Run the tests locally once: a project that fails its own tests fails the `test` stage, and the
first run is the one everyone watches. A repository with no test command gets no `test` stage.
A Lovable app that calls Supabase keeps calling it - Ankra does not rewrite the data layer.

## 1. Orient (two minutes, every time)

```bash
ankra login status                       # exit 6 = not logged in
ankra org current                        # the organisation everything below lands in
ankra cluster list                       # the target cluster must be here, agent online
ankra org ci-settings get                # CI cluster, build fallback, image gate, platform builds
ankra cluster agent ci get --cluster <cluster>   # workers > 0 and "advertised", or nothing runs
ankra credentials list --provider github         # a usable GitHub credential covering the repo
ankra backup vaults list                          # scan/gate/publish need a ready vault (or the platform default)
```

Four things must be true before the first run can succeed, and none of them errors when false:

| Must be true | Read it with | Fix |
|---|---|---|
| The organisation names a CI cluster (else its AI staging cluster serves) | `ankra org ci-settings get` | `ankra org ci-settings set --cluster <cluster>` |
| That cluster's agent runs pipeline workers | `ankra cluster agent ci get --cluster <cluster>` | `ankra cluster agent ci set --workers 2 --cluster <cluster>` |
| The GitHub credential reaches the repository | `ankra credentials repositories <credential>` | add the repo to the GitHub App installation |
| A backup vault is ready (or Ankra's default serves) | `ankra backup vaults list` | `ankra-backups` |

`ci set` stores the worker count on the platform and re-renders the agent's own release in ~15 s;
never `helm upgrade --set ci_worker_count=` (the next agent upgrade renders it away). `ci get`
reports the capability the agent advertised at its **last check-in**, so re-read after a write
instead of reading a stale `false` as failure. A playground cluster (`ankra cluster playground
create`) has no StorageClass and cannot host CI steps — a deploy target, not the CI cluster. A
command below that is "unknown" is an old binary: `ankra upgrade`, never an absent feature.

## 2. The fast path: one command to a live URL

```bash
ankra application ship . --cluster <cluster>                       # register → setup → build → deploy → verify
ankra application ship . --cluster <cluster> --ankra-build          # first image on Ankra's builders, no PR merge wait
ankra application ship . --name api --cluster <cluster> -o json    # monorepo: --name is identity
```

`ship` is a state machine over platform reads, not a script: it registers the application (or
adopts the one already tracking this repository), waits for the setup pull request, waits for the
first image, deploys, waits for the installation to be healthy, counts running pods, and prints
`Live: https://…` from the deployments surface's published hostname. Every wait is bounded by one
`--timeout` (default 1h across all steps). If it expires, **re-run the same command** — it continues
from the current state; nothing is lost. It refuses to print a `Live:` line for a parked deployment
(0 running pods), or for an Ingress whose backend Service, port or endpoints do not resolve (state
`unroutable`, with `url_problem`), and exits non-zero instead.

On Ankra Pipelines (the default) it waits for the run of the tracked branch's head commit; when no
run appears within a minute it starts that branch's push run itself (needs `applications.deploy`).
A run whose trigger the branch does not satisfy yet - typically the setup PR is not merged - stops
ship with the PR to merge. Only the legacy generated-workflow lane waits on the merge. With
`--ankra-build` Ankra's own builders make the first image (404 while the organisation's
`platform_builds` capability is off — `ankra org ci-settings get` shows `Platform builds
enabled`). An existing application is reused only when it tracks the same repository **and** the
requested branch; otherwise ship stops and names them - pass `--name` (and `--branch`).
`--namespace` and `--set` (for example `ingress_enabled=true`, `ingress_host=app.example.com`)
pass through to the deploy.

Progress goes to stderr; `-o json` puts one document on stdout (`application_id`, `setup_pr_url`,
`url`, `state` …). Exit `0` live, `2` usage or ambiguity, `5` timeout, `6` not logged in, `7` forbidden.
The installed `/ankra-ship-service` command drives this from Claude Code.

## 3. The full path, step by step

Use this when you want to read what Ankra generated before it runs, or when `ship` stopped and you
need to know where.

### 3a. Register

```bash
ankra application add . --name <name> --branch main --wait
ankra application add . --name <name> \
  --registry-url oci://harbor.example.com/shop --registry-credential harbor-pull \
  --registry-pull-secret shop-registry --registry-admin-credential harbor-admin
```

`--branch` defaults to the remote's default branch, else the current local branch — pass it when
the tracked branch is neither. Declare a
registry you already operate **at `add` time** (a registry added later leaves a pipeline that logs
in to the wrong one); with no declaration the image goes to the organisation's own Ankra registry
project and Ankra mints the push robot. `--wait` follows the analysis and prints the setup PR URL or
the failure reason. Auto-deploy on push is **on from registration**.

### 3b. Read the setup pull request

```bash
ankra application get <application-id>
ankra application branch-files <application-id>
ankra application files <application-id> --file Dockerfile=./Dockerfile --message "Fix the entrypoint"
```

It carries `.ankra/ankra.yaml`, `.ankra/pipeline.yaml`, `Dockerfile` (when generated) and
`.ankra/manifests/`. Read the Dockerfile's `CMD` against the real entrypoint, the exposed port, the
probe paths and the resource requests; push corrections back with `files`. **The setup PR is a
proposal, not a gate** — the generated pipeline is the definition of record before the PR opens, so
builds run from the next push whether or not it is merged; merging is how the file reaches the
default branch and stays editable in Git. When the setup PR fails its own checks (usually a
vulnerable dependency the repository already had), Ankra AI commits a fix to that same PR and says
so in its status comment.

### 3c. What the generated pipeline says

A generated file has `checkout`, a `test` stage when the repository declares a test command,
`build`, `scan` (Semgrep, Checkov, Trivy), `gate`, and a push-only `publish`: a pull request builds,
scans and reports but never publishes. Never declare `registry_auth`/`source_token`, never pin
`build.platforms`. The annotated file is in [reference.md](reference.md#the-generated-pipeline-annotated).

### 3d. Approve the authority — a human does this

```bash
ankra pipeline validate --application <application-id>           # dry run: planned steps, skips, network per step
ankra pipeline definitions get <definition-id>
ankra pipeline definitions approve <definition-id>                # pipelines.manage + a human actor
```

An unapproved definition is **not refused**: the run executes with none of its protected sections,
so the build has no `registry_auth` and dies with `step_refused` ("This step declares a secret
binding the platform sent no value for (registry_auth)"). A service-account or agent token is
refused at `approve`, so an agent stops here and asks. Creating the application in the portal
approves the generated pipeline in that person's name; one added from the CLI, a token or an agent
waits for an administrator. Only the current default-branch definition can be approved, once;
`ankra pipeline get --application <application> --latest` prints the exact approve command.

### 3e. Run, gate on it, read it

```bash
ankra pipeline run --application <application-id> --wait                 # --sha defaults to HEAD in the application's own checkout, else the default branch tip
ankra pipeline run --application <application-id> --sha <full-sha> --ref main --wait --timeout 45m
ankra pipeline get --application <application-id> --head-sha "$(git rev-parse HEAD)" --trigger push --latest --wait --exit-code
ankra pipeline get <run-id> --application <application-id> --watch -o json   # one JSON object per state change
ankra pipeline logs <run-id> --application <application-id> --step build --follow
ankra pipeline findings <run-id> --application <application-id>           # worst severity first, by tool
ankra pipeline artifacts <run-id> --application <application-id>          # step logs, scan reports, SBOM
ankra pipeline rerun <run-id> --application <application-id> --failed-only --wait
```

`get --wait --exit-code` is the primitive to gate anything external on: `0` succeeded, `1`
concluded otherwise, `5` not concluded by `--timeout`, `3` no run matched. A re-run of a push
publishes again but never rolls a deployment; `--full` sets path filters aside. Outcomes, error
classes and re-run planning: [reference.md](reference.md#run-states-error-classes-and-re-runs).

### 3f. Already on GitHub Actions? Convert in one call

`ankra application pipeline convert <application-id>` converts the workflow, switches Ankra's
generated one off and opens a PR; details in [reference.md](reference.md#converting-a-github-actions-application).

## 4. Tests

The `test` stage is a `run` stage: a script in an image, on the shared workspace, with no network
by default (`run` stages get `network: none`; set `egress-https` if the tests fetch). Give it what a
real test needs:

Sidecars, matrix and caches: [reference.md](reference.md#a-test-stage-with-a-database-matrix-and-cache).

A generated `test` stage follows the committed lockfile (`uv sync --locked && uv run pytest`,
`pnpm install --frozen-lockfile`, `go test ./...`). `test_results` formats are `junit`, `go-test`,
`pytest`, `playwright`; Ankra records every test's outcome and duration, read with `ankra pipeline
tests <run-id> --application <application-id>` (failed, flaky, slowest; `history`, `timings`,
`quarantine` below it). The exit code still fails the step, unless every failed test is
quarantined. A matrix fans out per axis value (GitHub Actions semantics, at most 64 legs).

Generated test stages keep package caches on the workspace, keyed on the lock file; rules in
[reference.md](reference.md#caches-and-outputs).

## 5. Build

`kind: build` is a rootless BuildKit pod in the `ankra-ci-build` namespace (Pod Security
*baseline*, never privileged). It pushes the image **by digest** into a CI staging repository; the
digest is what scan reads and gate judges. `build.dockerfile`, `context`, `target`, `build_args`
(names resolved from variables), `provenance`, `sbom`, `cache_to`. Monorepos get one file with
`build-<component>` / `scan-<component>` / `publish-<component>` stages filtered by `when.paths`.

A slow pipeline is golden path 6, **Fast CI**, in `ankra-cicd` §7b: `affected:` and path filters
so a change runs only the stages it touches, `promote_from: pull_request` so a merge re-tags the
image its pull request already built and tested instead of building again, the merge train, and
`--full` / the `ankra:full-run` label / `[skip ci]` to set the filters aside or skip a push.
Generated pipelines already declare `promote_from` when their shape allows.

If the build dies within ~30 s on `rootlesskit … failed to share mount point: /: permission denied`,
the node's runtime confines the builder (containerd AppArmor, kubelet `seccompDefault`; DigitalOcean
Ubuntu 24.04 is the reference case). **Do not widen the node.** Fall back to Ankra's builders with
`ankra org ci-settings set --build-fallback platform_builders` (also needs the `Platform builds
enabled: yes` grant), or build on a cluster that does not confine.

## 6. Scan, gate, publish — where security lives

`kind: scan` expands to one step per tool on pinned vendor images: **Semgrep** over the source
(packs from `with.semgrep_config`), **Checkov** over the chart and manifests, **Trivy** over the
filesystem and the built digest, with the SBOM ingested as information. Scanners exit 0 on findings;
**the gate decides**, from the persisted findings, against the organisation's policy:

```bash
ankra org ci-settings set --image-gate app            # app | all | off — what blocks a publish
ankra org ci-settings set --ignore-unfixed=false      # every unfixed finding blocks, whatever a pipeline asks
ankra pipeline findings <run-id> --application <application-id>
ankra application security-versions <application-id> # every published tag: SBOM, findings, where it runs
ankra security findings --known-exploited             # fleet-wide, CISA KEV first, then EPSS
```

A stage's `with.image_gate` / `scan.fail_on` can only **tighten** the organisation gate; a declared
report that never uploads is `no_scan_results` — "This is not a clean scan" — and blocks. `publish`
copies the judged digest into the application's real repository as `sha-<7>` (the commit's first
seven characters), verifies it, and refuses a registry host other than the one Ankra resolved. That
tag is the identity every deploy, promotion, rollback and scan history is keyed on. `publish.tags`
can add extra tags to the same digest (a moving `latest`, `"0.2.${{ ankra.run.number }}"`) for
consumers outside Ankra; never deploy or promote by them.

## 7. Ship: from a published tag to a running service

```bash
ankra application auto-deploy get <application-id>    # on by default; shows the newest build seen on the branch
ankra application deploy <application-id> --cluster <dev-cluster> --namespace shop-dev \
  --set ingress_enabled=true --set ingress_host=shop.example.dev
ankra application deployments <application-id>        # where it runs, the ingress host, the tag on each
ankra application env-secrets list <application-id>
printf '%s' "${DATABASE_URL:?}" | ankra application env-secrets set <application-id> DATABASE_URL   # :? refuses an unset value
ankra application env-secrets apply <application-id>  # set stores; apply seals into the workloads and rolls
ankra org variables set SUPPORT_EMAIL support@example.com          # non-secret config: variables, not secrets
```

Push-to-deploy watches the tracked branch for a published tag and rolls it out to every environment
that follows pushes; a pull request never deploys anywhere but a preview. Deploy inputs come from
`--set`; secrets from env-secrets (**never `--value`** on the command line); non-secret
configuration from organisation, cluster or stack variables (resolution: stack > cluster >
organisation). Env-secrets belong to the application and are sealed into **every** environment; a
value that must differ per environment (a test and a live payment key) is a SOPS-encrypted Secret
in that cluster's Stack (`ankra-sops-secrets`).

**Two environments: development follows merges, a person promotes to production.**

```bash
# 1. Portal: application Settings → Push to deploy → "What follows pushes": development on, production off.
#    (`auto-deploy set --enabled=false` turns push-to-deploy off for EVERY environment - not this.)
# 2. Promote the exact tag development proved - a re-tag, never a rebuild:
ankra application deploy <application-id> --cluster <prod-cluster> --namespace shop \
  --set image_tag=sha-<the-tag-that-passed> --mode high_availability --set replicas=3 \
  --set ingress_enabled=true --set ingress_host=shop.example.com
# Rollback is the same command with the previous tag from `ankra application deployments`.
```

Production is not brought online until development is online and tested, and production is a
decision a person makes: an agent prepares the command and stops for confirmation (the installed
`/ankra-promote` command does exactly that). The portal's **Environments** page and chat
(`promote_application`, Agent mode, confirmed) promote the same way (§8).

Then verify, in this order, before saying it is live:

```bash
ankra cluster operations list --cluster <cluster> --failed          # the platform execution
ankra cluster get pods -n shop --cluster <cluster>                  # Running, every container ready
ankra cluster events -n shop --type Warning --cluster <cluster>     # quota, image pull, probes
ankra cluster logs -l app=<name> -n shop --cluster <cluster> --follow=false --tail 100
ankra cluster domain <cluster>                                      # the public domain the hostname derives from
curl -fsS https://<host>/healthz
```

The hostname derives on the first deploy from the cluster's public domain (the organisation's
preview domain when a DNS zone covers it, else the generated `*.ankra.cc` subdomain) with a
cert-manager certificate; your own domain is `--set ingress_host=` plus a DNS zone the cluster
serves (`ankra-domains-dns`). A green `deploy` is not a working service; the curl is, and then one
real journey (sign up, sign in) in a browser. On a shared cluster a Pending pod is usually CPU
quota: read the events before touching anything.

**Done means evidence.** Before saying "live", attach: the test runner output (not "tests pass"),
the `sha-` tag and its digest, the scan summary (`ankra pipeline findings`, or an explicit zero),
the public URL answering 200 with a valid certificate, a browser walk of the main journeys when a
browser is available, and the exact rollback command. Anything missing means not done.

## 8. Day 2

**Previews on every pull request.** Ankra's AI gateway deploys the pull request's published digest
into a TTL namespace on the organisation's staging cluster and posts the URL in one status comment
(marker `<!-- ankra-ai:review -->`, sections pipeline · preview · review). By hand:

```bash
ankra application demo deploy <application-id> --pr-number 42 --ttl-hours 8
ankra application demo deploy <application-id> --branch feature/x
ankra application demo config set <application-id> --database --migrate-command "npm run migrate"
ankra application demo list <application-id> ; ankra application demo detail <application-id> <workspace-id>
ankra application demo logs <application-id> <workspace-id> ; ankra application demo stop <application-id> <workspace-id>
ankra org ai-environment get                          # preview domain, TLS issuer, publication verdict
ankra org ai-environment set --demo-base-domain preview.example.com
```

The staging cluster is chosen in the portal (**AI → Settings → Workspaces**); the development
cluster works, so no third cluster is needed. Ankra mints
the preview hostname but publishes **no DNS** under the demo base domain — point a wildcard at the
staging cluster's ingress yourself, or the URL resolves nowhere and gets no certificate.

**Rollback.** Every deployment is keyed on a `sha-<7>` tag, so rolling back is putting an earlier
published tag back: `ankra application deploy <application-id> --cluster <cluster> --set
image_tag=sha-<previous>` (§7), or the portal's Environments page / `ankra chat "roll shop back to
sha-1a2b3c4 on <cluster>" --mode agent` (the rollback lane, which also lists what it does **not**
revert: a migrated schema, data written since, changed secrets, stack edits). A stack Ankra
deployed is `git revert` in the GitOps repository; a Helm release you manage yourself is `ankra
cluster helm rollback <release> -n <ns> --revision <n>` after `ankra cluster helm history` (refused
for addon-managed releases — change the stack). Roll back first, fix on a branch.

**Promotion.** The tag, and only the tag, moves between environments; configuration stays in each
environment's deploy options and cluster configuration. Besides §7's `deploy --set image_tag=`, the
portal's Environments page, chat and the API (`GET …/promotions/preview`, `POST …/promotions`)
preview every config difference first: a source whose deploy has not settled is
`source_not_settled`, a target that follows pushes is `target_follows_pushes` (switch production
off under **What follows pushes**), and missing env-secrets block by name. The same `sha-<7>` in
development and production is the proof you shipped what you tested. Fleets of clusters use a stack
profile (`ankra-stack-profiles`) or a published add-on (`ankra-applications` §8), never copied YAML.

**Schedules, manual runs, alerts.** A nightly is `ankra pipeline schedules create --application
<application-id> --cron "0 3 * * 1-5" --ref main --timezone Europe/London`; a dispatch with inputs is
`ankra pipeline run --application <application-id> --ref main --input environment=staging`. Route
`pipeline_run_failed`, `pipeline_run_succeeded`, `image_gate_blocked`, `preview_ready` and
`preview_failed` to Slack, Teams, Discord, PagerDuty or a webhook with `ankra alerts destinations
create` and `ankra alerts routes create --destination-id <id> --kinds pipeline_run_failed,image_gate_blocked`;
`ankra alerts routes preview --kind pipeline_run_failed` shows where one would land.

**Posture over time.** `ankra application security-versions <application-id>` is the per-tag
history; `ankra security findings --known-exploited` is what is exploited in the wild across the
fleet. Fix or accept every high finding deliberately, and write the acceptance down.

## 9. AI at the core

Ankra AI is in the loop at four points, and it never holds authority it was not given.

- **Every pull request is reviewed.** A read-only turn over the diff and the pipeline state
  (failing step, findings by tool and severity, gate verdict, digest), with no tools at all — a
  hostile diff cannot escalate past text. Verdicts `approve` / `comment` / `request_changes` land in
  the status comment. `ankra application pull-request-reviews <application-id> --limit 5` lists
  them; `ankra ai lanes set pr_review <model>` picks the model.
- **Every run is watched.** `ankra chat "watch pipeline run <run-id> and tell me why it fails"`
  registers a watch; when the run concludes, the AI wakes in the same conversation with the outcome
  and the failing step's log tail. On a pull request, `@ankraai merge when green` records a request
  to squash-merge once the run, the gate, the review verdict and human approvals all agree —
  Agent-mode bindings only, 24 h expiry, a new push invalidates it. It is **off by default**: an
  organisation admin turns **Merge when green** on per GitHub installation, beside its AI mode.
- **A failed build on the tracked branch is fixed.** Auto-fix is **on by default per application**
  — its output is a pull request a human merges, and closing the PR undoes all of it. Ankra tries
  the deterministic rungs first (registry login, rerun, setup retry), then dispatches a bounded
  mission (80 tool calls, 12 turns, 45 min, one per application per UTC day) that reads the
  repository and the logs, commits a fix to a branch, watches CI and opens the PR. By hand:
  `ankra application demo fix-build <application-id> --branch main`; follow with
  `ankra agents runs --status running` and `ankra agents transcript <run-id>`. Off per application
  at `PUT …/auto-fix-builds {"enabled": false}`.
- **You can ask.** `ankra chat "why did the last pipeline run for shop fail?" --mode ask` reads
  runs, logs, findings, pods and events; in `--mode agent` every write it proposes waits for
  `ankra chat actions confirm <action-id>`. `ankra tickets list --needs-human` is the board's queue
  of decisions waiting on you.

What the AI **never** does, by construction: approve a pipeline definition's authority, approve a
promotion (`promotions.approve` is human-only), merge without a green gate, push to a default
branch, or act on an automatic webhook review. Ask mode reads and proposes; Agent mode acts under
the binding's identity and still arrives as a pull request.

## 10. What is not there yet

Say this out loud rather than promising it. Seven stage kinds execute: `checkout`, `run`, `build`,
`scan`, `gate`, `publish`, `deploy` (host targets only, never a cluster). `preview`, `verify`,
`agent`, `approval`, `webhook`, `automation`, `ankra`, `external` are skipped as
`kind_unavailable`, so **an `approval` stage blocks nothing**; `uses:` is refused; `retry:` is not
applied. Previews, cluster deploys, rollback and promotion happen through the application lanes,
not inside a pipeline. Full list: [reference.md](reference.md#what-is-not-there-yet-in-full).

## When nothing happens, or the wrong thing does

| Symptom | Cause | Do |
|---|---|---|
| Merge produced no run, no error | CI cluster unset, or `ci_worker_count` 0 | §1; then `ankra pipeline run … --wait` |
| No run and every webhook says `repository_not_onboarded` | repository not connected | `ankra pipeline repositories connect --application <application-id> --cluster <cluster>` |
| Build: `step_refused … (registry_auth)` | definition not approved | §3d, a human approves |
| `artifact_store_unavailable` | no ready backup vault | `ankra-backups`, or wait for the platform default |
| Build exits ~30 s, rootlesskit mount permission denied | confined runtime | §5, fallback to platform builders |
| `image_gate_blocked` | a finding at or above the gate | fix it, or a written disposition in `ankra-security` |
| Step "running" for minutes with no output | pod Pending: CPU quota, no StorageClass, disk | `ankra pipeline get <run-id>` Queueing says where the wait went ("Pending 5m in the cluster (scheduling 4m30s, volumes 8s, image pull 19s)"); then `ankra cluster events -n ankra-ci --type Warning` |
| `publish` skipped on a rerun, or a rerun published but nothing rolled | the rerun repeats a pull request, tag, manual, API or agent run; a rerun's image never becomes the push-to-deploy candidate | push a commit, or `ankra application deploy --set image_tag=sha-<7>` |
| Production changed on a merge | production still follows pushes | switch it off under **What follows pushes**; promote by tag (§7) |
| Run green, release never arrived | a shipping stage failed under `allow_failure` | read each step's outcome with `ankra pipeline get <run-id> --application <application-id>`; drop the flag |
| Two builds per commit | generated GitHub workflow still present | `ankra application pipeline convert` |
| Deploy healthy, URL does not load | preview/demo domain has no DNS, or probe paths | wildcard record; §7 curl ladder |
| First deploy crash-loops | env-secret set but never applied | `ankra application env-secrets apply <application-id>` |

Rarer symptoms (build fallback, QEMU, missing scan results, Harbor project, private egress, App permissions): [reference.md](reference.md#rarer-symptoms).

## Rules

- **Ankra Pipelines first, always.** For a registered application, zero runs is a one-command
  capacity or approval fix. A hand-rolled GitHub Actions workflow forks the contract away from the
  scans, the gate, the digest publish and the managed registry auth — permanently, and silently.
- **Immutable tags only.** `sha-<7>` is the identity of every deploy, preview, rollback and
  promotion. Never deploy by `latest` or any extra `publish.tags`, and never rebuild to promote.
- **Development follows merges; a person promotes to production.** Production never follows
  pushes, and no agent promotes on its own.
- **Approval is human.** Authority (`definitions approve`) and promotion approval are refused to
  service and agent tokens. An agent reaches that point, stops, and says so.
- **Set, then apply.** `env-secrets set` stores; `env-secrets apply` seals and rolls. Secrets by
  stdin or prompt, never `--value`, never echoed into a transcript.
- **Verify, then claim, with evidence.** Operations → pods → events → logs → curl → a real journey.
  A green command is not a working service; report the evidence list in §7.
- **Confirm the organisation before writing** (`ankra org current`, `--org`); a wrong-org
  registration is a tear-down, not a rename.
- **Re-run is the recovery, and §10 is the truth.** Every wait resumes from platform state; a
  timeout is a pause. Do not emit `approval` or `agent` stages and call them wired, and do not emit
  `deploy` to reach a cluster: it releases only to host targets.

## Related skills

- `ankra-applications` — the registry matrix, monorepo components, fleet rollout, the worked run.
- `ankra-cicd` — repositories without an application, the pipeline authoring primer, the authority gate, fast CI (golden path 6), the GitOps bump.
- `ankra-app-integrations` — wiring the app to a database, registry, LLM gateway or managed service already in the organisation.
- `ankra-ai-gateway` — Ask vs Agent per binding, the staging cluster, workspaces and PR demos.
- `ankra-security` — findings, KEV/EPSS, dispositions, tokens and least-privilege credentials.
- `ankra-troubleshooting` — up but wrong; `ankra-domains-dns` — the public and preview domains, TLS.
- `ankra-stack-profiles` — one definition across many clusters; `ankra-backups` — the vault scan,
  gate and publish depend on; `ankra-alerts-webhooks` — routing the notifications.
