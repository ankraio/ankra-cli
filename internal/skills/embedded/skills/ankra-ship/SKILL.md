---
name: ankra-ship
description: Ship code with Ankra - take a fresh (vibe-coded) project or any repository to a live URL through Ankra Pipelines, then run it on day 2 - the generated `.ankra/pipeline.yaml` that tests, builds rootless in your own cluster, scans with Semgrep, Checkov and Trivy, gates on findings and publishes an immutable `sha-` tag; push-to-deploy and the `Live:` URL; pull-request previews, environment secrets, rollback, promotion, alerts; and Ankra AI reviewing every pull request, watching runs and opening fix PRs. Use when the user says ship it, deploy this, get it live, go to production, wants CI/CD for a new or AI-generated project, asks how to test, build, scan, package or release code with Ankra, or asks what to do once it is live. This is the default skill for shipping; `ankra-applications` and `ankra-cicd` are its detail.
---

# Ship with Ankra

One repository, one contract, one command to a live URL — and everything that comes after. Ankra
Pipelines run **inside your own cluster**: every push is checked out, tested, built rootless by
digest, scanned, judged against the organisation's image gate and, only if it passes, published
under an immutable `sha-<7>` tag that push-to-deploy rolls out and a preview can be cut from. Ankra
AI reads every pull request, watches every run, and opens the fix when a build breaks. Nothing here
needs GitHub Actions minutes, a runner fleet, `kubectl` in CI, or a registry password in the repo.

Use this skill first whenever the job is *get this code running and keep it running*. Reach for
`ankra-applications` for the registry matrix and fleet-wide rollout, `ankra-cicd` for the
GitOps-bump pattern and hand-rolled workflows, `ankra-troubleshooting` when the workload is up but
unhealthy, and `ankra-ai-gateway` for the Ask/Agent binding modes the AI acts under.

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
  sites; anything it cannot classify needs yours.
- **A `.dockerignore`**, and migrations that run on start or on one documented command (§8).

Run the tests locally once: a project that fails its own tests fails the `test` stage, and the
first run is the one everyone watches.

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
(0 running pods) and exits non-zero instead: a URL that answers 503 is the one lie it never tells.

Without `--ankra-build` it prints the setup PR URL and waits for a human to merge it, because the
first image comes from the pipeline the PR adds; with it, Ankra's own builders make the first image
and the merge wait is skipped (404 while the organisation's `platform_builds` capability is off —
`ankra org ci-settings get` shows `Platform builds enabled`). A repository with several
applications is refused without `--name`: ship once adopted the wrong one and redeployed it onto
the caller's hostname.

Progress goes to stderr; `-o json` puts one document on stdout (`application_id`, `setup_pr_url`,
`url`, `state` …). Exit `0` live, `2` usage or ambiguity, `5` timeout, `6` not logged in, `7` forbidden.

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

`--branch` is trusted from the *local* branch when omitted — pass the remote branch. Declare a
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
default branch and stays editable in Git.

### 3c. What the generated pipeline says

```yaml
apiVersion: ankra.io/v1
kind: Pipeline
metadata: { name: "shop" }
on:
  push: { branches: ["main"] }
  pull_request: { branches: ["main"], fork_policy: "read_only" }
  manual: {}
concurrency: { group: "${{ ankra.repository }}-${{ ankra.ref }}", cancel_in_progress: true }
workspace: { size: "10Gi", access: "rwo" }
defaults: { image: "node:22", timeout: "30m", network: "egress-https" }
permissions: { contents: "read" }
stages:
  - { name: checkout, kind: checkout }
  - name: test
    kind: run
    needs: [checkout]
    run: "npm ci && npm test"
    test_results: [{ format: junit, path: "test-results.xml" }]
  - name: build
    kind: build
    needs: [test]
    build: { dockerfile: "Dockerfile", provenance: true, sbom: true }
  - name: scan
    kind: scan
    needs: [build]
    with: { image_gate: "app", semgrep_config: "p/default p/docker p/javascript p/typescript" }
    scan:
      scanners: [semgrep, checkov, trivy]
      fail_on: { semgrep: "error", checkov: "none", trivy: "app" }
  - { name: gate, kind: gate, needs: [scan], gate: { require_stages: [scan] } }
  - name: publish
    kind: publish
    needs: [gate]
    when: { events: [push] }
    with: { image: "harbor.ankra.cloud/<project>/shop" }
```

Stages without `needs` run in parallel; `publish` refuses without a successful `gate` upstream and
only runs on `push`, so a pull request builds, scans and reports but never publishes. The
`registry_auth` and `source_token` bindings are reserved and added by Ankra from the application's
push robot — never declare them. Do not pin `build.platforms`: the build runs natively on the CI
cluster's architecture, and a fabricated `linux/amd64` fails on an arm64 cluster with "no QEMU
emulation registered". [reference.md](reference.md) has the full schema with services, matrix,
caches, schedules and manual inputs.

### 3d. Approve the authority — a human does this

```bash
ankra pipeline validate --application <application-id>           # dry run: planned steps, skips, network per step
ankra pipeline definitions get <definition-id>
ankra pipeline definitions approve <definition-id>                # pipelines.manage + a human actor
```

An unapproved definition is **not refused**: the run executes with none of its protected sections,
so the build has no `registry_auth` and dies with `step_refused` ("This step declares a secret
binding the platform sent no value for (registry_auth)"). A service-account or agent token is
refused at `approve`, so an agent stops here and asks. Only the current default-branch definition
can be approved, once; `ankra pipeline get` on an unapproved run prints the exact approve command.

### 3e. Run, gate on it, read it

```bash
ankra pipeline run --application <application-id> --wait                 # --sha defaults to HEAD in a checkout
ankra pipeline run --application <application-id> --sha <full-sha> --ref main --wait --timeout 45m
ankra pipeline get --application <application-id> --head-sha "$(git rev-parse HEAD)" --trigger push --latest --wait --exit-code
ankra pipeline get <run-id> --application <application-id> --watch -o json   # one JSON object per state change
ankra pipeline logs <run-id> --application <application-id> --step build --follow
ankra pipeline findings <run-id> --application <application-id>           # worst severity first, by tool
ankra pipeline artifacts <run-id> --application <application-id>          # step logs, scan reports, SBOM
ankra pipeline rerun <run-id> --application <application-id> --failed-only --wait
```

`get --wait --exit-code` is the primitive to gate anything external on: `0` succeeded, `1` concluded
otherwise, `5` not concluded by `--timeout`, `3` no run matched. `status` is the lifecycle
(`queued` → `running` → `concluded`); `outcome` is the verdict (`success`, `failure`, `cancelled`,
`timed_out`, `skipped`, `infra_error`). A failed run names its error class: `step_failed` is your
build, `image_gate_blocked` is a finding, `registry_push_failed`, `build_runtime_confined` and
`platform_build_infra` are placement or Ankra. `rerun` skips publish (`when.events` is push/manual)
— push a commit to publish. Logs attach once a step has an execution; a concluded step replays for
24 h and from the archived artifact after that.

### 3f. Already on GitHub Actions? Convert in one call

```bash
ankra application pipeline convert <application-id>                  # stores the converted definition, opens the PR
ankra application pipeline convert <application-id> --keep-workflows
```

Ankra converts the workflow to `.ankra/pipeline.yaml`, records it as the definition of record,
**switches the generated workflow off on GitHub** so no commit builds twice, and opens a PR on
`ankra/pipeline-migration` that commits the file and removes Ankra's generated workflow (a workflow
the repository wrote itself is never touched). Review the result: marketplace actions it cannot map
become `run` stages that exit 1 with an explanation, never a silent drop.

## 4. Tests

The `test` stage is a `run` stage: a script in an image, on the shared workspace, with no network
by default (`run` stages get `network: none`; set `egress-https` if the tests fetch). Give it what a
real test needs:

```yaml
services:
  postgres:
    image: "postgres:16"
    env: { POSTGRES_PASSWORD: "test", POSTGRES_DB: "shop" }
    ports: ["5432"]
    ready: { tcp: "5432" }
stages:
  - name: test
    kind: run
    needs: [checkout]
    image: "golang:${{ matrix.go }}"
    network: services
    services: [postgres]
    env: { DATABASE_URL: "postgres://postgres:test@postgres:5432/shop?sslmode=disable" }
    cache:
      - key: "go-${{ hashFiles('go.sum') }}"
        paths: ["/root/go/pkg/mod"]
        restore_keys: ["go-"]
    run: |
      go test ./... -json > test-results.json
    test_results: [{ format: go-test, path: "test-results.json" }]
    matrix:
      go: ["1.25", "1.26"]
```

A generated `test` stage follows the committed lockfile (`uv sync --locked && uv run pytest`,
`pnpm install --frozen-lockfile`, `go test ./...`). `test_results` formats are `junit`, `go-test`,
`pytest`, `playwright` — declared and carried, not yet ingested; the exit code is what fails the
run. A matrix fans out per axis value (GitHub Actions semantics, at most 64 legs). Caches are keyed
per repository, cluster and trust scope; a missing StorageClass degrades to `disabled`, never a
failed step. Every step's output is archived as a `step_log` artifact, and `key=value` lines
appended to `$ANKRA_OUTPUT` become `${{ needs.test.outputs.<name> }}` downstream.

## 5. Build

`kind: build` is a rootless BuildKit pod in the `ankra-ci-build` namespace (Pod Security
*baseline*, never privileged). It pushes the image **by digest** into a CI staging repository; the
digest is what scan reads and gate judges. `build.dockerfile`, `context`, `target`, `build_args`
(names resolved from variables), `provenance`, `sbom`, `cache_to`. Monorepos get one file with
`build-<component>` / `scan-<component>` / `publish-<component>` stages filtered by `when.paths`.

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
tag is the identity every deploy, promotion, rollback and scan history is keyed on; `latest` does
not exist here.

## 7. Ship: from a published tag to a running service

```bash
ankra application auto-deploy get <application-id>    # on by default; shows the newest build seen on the branch
ankra application auto-deploy set <application-id> --enabled=false   # gate production explicitly
ankra application deploy <application-id> --cluster <cluster> --namespace shop --mode high_availability --set replicas=3
ankra application deployments <application-id>        # where it runs, the ingress host, publication state
ankra application env-secrets list <application-id>
printf '%s' "$DATABASE_URL" | ankra application env-secrets set <application-id> DATABASE_URL
ankra application env-secrets apply <application-id>  # set stores; apply seals into the workloads and rolls
ankra org variables set SUPPORT_EMAIL support@example.com          # non-secret config: variables, not secrets
```

Push-to-deploy watches the tracked branch for a published tag and rolls it out; a pull request never
deploys anywhere but a preview. Deploy inputs come from `--set`; secrets from env-secrets (**never
`--value`** on the command line); non-secret configuration from organisation, cluster or stack
variables (resolution: stack > cluster > organisation).

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
cert-manager certificate. A green `deploy` is not a working service; the curl is. On a shared
cluster a Pending pod is usually CPU quota: read the events before touching anything.

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

The staging cluster is chosen in the portal (Organisation settings → AI → Environment). Ankra mints
the preview hostname but publishes **no DNS** under the demo base domain — point a wildcard at the
staging cluster's ingress yourself, or the URL resolves nowhere and gets no certificate.

**Rollback.** Every deployment is keyed on a `sha-<7>` tag, so rolling back is putting an earlier
published tag back on the running workload — one thing moves, and the rollback holds until a
genuinely newer build arrives. The application rollback lane does exactly that: `GET
…/rollback-targets` lists the tags still in the registry, `POST …/rollbacks` with `cluster_id` and
`image_tag` performs it, and the answer spells out what it does **not** revert (a migrated schema,
data written since, changed secrets, stack edits). It is a portal-session and AI surface, not a CLI
verb: `ankra chat "roll shop back to sha-1a2b3c4 on <cluster>" --mode agent`, then confirm the
action. A stack Ankra deployed is `git revert` in the GitOps repository; a Helm release you manage
yourself is `ankra cluster helm rollback <release> -n <ns> --revision <n>` after `helm history`
(refused for addon-managed releases — change the stack). Roll back first, fix on a branch.

**Promotion.** The tag, and only the tag, moves between environments; configuration is deliberately
not promoted, and the preview names every config difference so you decide. Promote from the
application's environments page in the portal, from chat (`promote_application`, Agent mode,
confirmed action), or over the API (`GET …/promotions/preview`, `POST …/promotions`). A source whose
deploy has not settled is `source_not_settled`; a target that follows pushes is
`target_follows_pushes` and refused rather than re-pointed, so turn auto-deploy off for production
first; missing env-secrets on the target block it by name. The same `sha-<7>` in staging and
production is the proof you shipped what you tested. Fleets of clusters use a stack profile
(`ankra-stack-profiles`) or a published add-on (`ankra-applications` §8), never copied YAML.

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
  Agent-mode bindings only, 24 h expiry, a new push invalidates it. It is built but **switched off
  for every organisation today**; the request is refused with that reason until Ankra enables it.
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

Say this out loud rather than promising it. The spec accepts fifteen stage kinds; **six execute**:
`checkout`, `run`, `build`, `scan`, `gate`, `publish`. `preview`, `verify`, `deploy`, `agent`,
`approval`, `webhook`, `automation`, `ankra` and `external` validate with a warning and are skipped
as `kind_unavailable`; `uses:` is refused outright — write the step as `run:`; `on_failure:`,
`finally:`, `retry:` and `test_results` ingestion are accepted but not yet honoured. So **an
`approval` stage blocks nothing — everything downstream runs**, and a skipped stage is not a failed
one: do not make the `Ankra pipeline` check *required* while the definition carries an unexecuted
kind, or it goes green having done none of what that stage names. Previews, deploys, rollback and
promotion happen through the application lanes above, not inside a pipeline. GitLab and Bitbucket
plan from the stored definition (the head-sha file is read on GitHub only) and have no setup-PR
writer yet. `egress-https` reaches **public addresses only**; name a private range with
`ankra org ci-settings set --egress-allowed-cidr <cidr>` before the step runs.

## When nothing happens, or the wrong thing does

| Symptom | Cause | Do |
|---|---|---|
| Merge produced no run, no error | CI cluster unset, or `ci_worker_count` 0 | §1; then `ankra pipeline run … --wait` |
| No run and every webhook says `repository_not_onboarded` | repository not connected | `ankra pipeline repositories connect --application <application-id> --cluster <cluster>` |
| Build: `step_refused … (registry_auth)` | definition not approved | §3d, a human approves |
| `artifact_store_unavailable` | no ready backup vault | `ankra-backups`, or wait for the platform default |
| Build exits ~30 s, rootlesskit mount permission denied | confined runtime | §5, fallback to platform builders |
| `build_fallback_none` | fallback needs `--build-fallback platform_builders` **and** the grant | `ankra org ci-settings get` |
| Exit 79, "no QEMU emulation registered" | pinned `platforms` on an arm64 cluster | drop `build.platforms` |
| `image_gate_blocked` | a finding at or above the gate | fix it, or a written disposition in `ankra-security` |
| `no_scan_results` | scan report never uploaded | vault (above) or an older platform; do not treat as clean |
| Step "running" for minutes with no output | pod Pending: CPU quota, no StorageClass, disk | `ankra cluster events -n ankra-ci --type Warning` |
| `publish` skipped on a rerun | `when.events` excludes `rerun` | push a commit |
| Test or build times out reaching a private host | `egress-https` is public-only | `ankra org ci-settings set --egress-allowed-cidr 10.0.0.0/8` |
| Two builds per commit | generated GitHub workflow still present | §3f `pipeline convert` |
| No "Ankra pipeline" check on the PR, only a comment | GitHub App lacks `checks:write` | grant it on the installation |
| Setup PR 403 "Resource not accessible by integration" | repo not in the App installation | add it (owner-only) or choose "All repositories" |
| Deploy healthy, URL does not load | preview/demo domain has no DNS, or probe paths | wildcard record; §7 curl ladder |
| First deploy crash-loops | env-secret set but never applied | `ankra application env-secrets apply <application-id>` |

## Rules

- **Ankra Pipelines first, always.** For a registered application, zero runs is a one-command
  capacity or approval fix. A hand-rolled GitHub Actions workflow forks the contract away from the
  scans, the gate, the digest publish and the managed registry auth — permanently, and silently.
- **Immutable tags only.** `sha-<7>` is the identity of every deploy, preview, rollback and
  promotion. Nothing is tagged `latest` and nothing is rebuilt to promote.
- **Approval is human.** Authority (`definitions approve`) and promotion approval are refused to
  service and agent tokens. An agent reaches that point, stops, and says so.
- **Set, then apply.** `env-secrets set` stores; `env-secrets apply` seals and rolls. Secrets by
  stdin or prompt, never `--value`, never echoed into a transcript.
- **Verify, then claim.** Operations → pods → events → logs → curl. A green command is not a
  working service, and a listing can be minutes stale on a busy platform: the curl is primary.
- **Confirm the organisation before writing** (`ankra org current`, `--org`); a wrong-org
  registration is a tear-down, not a rename.
- **Re-run is the recovery, and §10 is the truth.** Every wait resumes from platform state; a
  timeout is a pause. Do not emit `deploy`, `approval` or `agent` stages and call them wired.

## Related skills

- `ankra-applications` — the registry matrix, monorepo components, fleet rollout, the worked run.
- `ankra-cicd` — the GitOps-bump pattern, and the full zero-runs / confined-build branches.
- `ankra-ai-gateway` — Ask vs Agent per binding, the staging cluster, workspaces and PR demos.
- `ankra-security` — findings, KEV/EPSS, dispositions, tokens and least-privilege credentials.
- `ankra-troubleshooting` — up but wrong; `ankra-domains-dns` — the public and preview domains, TLS.
- `ankra-stack-profiles` — one definition across many clusters; `ankra-backups` — the vault scan,
  gate and publish depend on; `ankra-alerts-webhooks` — routing the notifications.
