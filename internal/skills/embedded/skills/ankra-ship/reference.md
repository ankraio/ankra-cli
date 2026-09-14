# Ship with Ankra — reference

Long-form detail for `ankra-ship`: the `.ankra/pipeline.yaml` schema, a worked pipeline that uses
most of it, the scripting contract, the lanes that are API-only today, and the vocabulary a run
speaks in. Read `ankra-applications` reference.md for the registry matrix and monorepo components.

## The document

```yaml
apiVersion: ankra.io/v1        # required, exactly this
kind: Pipeline                 # required, exactly this
metadata: { name: "<name>" }   # required
on: {…}                        # triggers; a pipeline with none is still dispatchable by API
concurrency: { group: "…", cancel_in_progress: true }
workspace: { size: "10Gi", access: "rwo" }     # one volume per run, every step shares it; rwx refused today
defaults: { image, timeout, resources, cache, working_directory, network, env, secrets }
permissions: { contents: read, pipelines: read, runs: read }   # none|read|write; can only narrow. Protected.
secrets: [ { name, from: app_env_secret|org_variable|registry|credential, key } ]   # Protected.
credentials: [ { name, from: cloud|git|registry|object_storage, as } ]              # whole run. Protected.
services: { <name>: { image, env, ports, ready: { tcp | http } } }                  # Protected.
stages: […]                    # the DAG
on_failure: […]                # accepted, not yet honoured
finally: […]                   # accepted, not yet honoured
environments: { <name>: { data, verify, rollout, gate } }   # Protected; no executor acts on it yet
```

Parsing is lenient where it is safe: unknown keys are **warned and ignored** (an older Ankra can
read a newer file), a scalar stands in for a one-element list, quoted numbers read as numbers,
anchors resolve. It is strict where it must be: a wrong `apiVersion`/`kind`, unreadable YAML,
`stages` not a list, a missing `metadata.name` or an empty `stages` is fatal. A file committed under
`.ankra/manifests/` is refused by the manifest gate because it carries `kind`.

**Which file governs.** On GitHub the run plans its *logic* from the file at the run's head sha; on
GitLab and Bitbucket from the definition stored with `ankra pipeline definition put`. `ankra
pipeline run --spec-file` carries a one-off definition on the run. *Authority* — every protected
section — always comes from the default-branch definition an administrator approved, whichever
source the logic came from.

## Triggers

| Key | Fields | Notes |
|---|---|---|
| `on.push` | `branches`, `paths` | empty `branches` = every branch; a path filter never excludes a run whose diff could not be read |
| `on.pull_request` | `branches` (base), `paths`, `fork_policy` | `fork_policy`: `none` (refuse), `read_only` (default: no secrets, no credentials, no push target, clamped budgets), `trusted`. Protected. |
| `on.tag` | `patterns` | globs |
| `on.schedule` | list of `cron`, `branch`, `stages` | five **numeric** fields; `stages` restricts the run to a subset (the nightly that runs only the slow suite) |
| `on.manual` | `inputs` | `name` (`[A-Za-z0-9_]`), `type` (`string`, `boolean`, `number`, `choice` + `enum`), `default`, `required`, `description` |
| `on.webhook` | none | declaration only; tokens not available yet |

Run triggers on the wire: `push`, `pull_request`, `tag`, `schedule`, `manual`, `api`, `agent`,
`rerun`. Dispatches (`manual`, `api`, `agent`, `rerun`) are never deduplicated; provider events are
(`already_queued`, `already_recorded`).

## Stages

Every stage: `name` (`^[a-z0-9][a-z0-9_-]*$`, unique across `stages`/`on_failure`/`finally`) and a
`kind` from the closed vocabulary. Six execute today — `checkout`, `run`, `build`, `scan`, `gate`,
`publish`; `gate` and `publish` are settled by the platform, never a Job. The other nine
(`preview`, `verify`, `deploy`, `agent`, `approval`, `webhook`, `automation`, `ankra`, `external`)
warn at validation and are skipped by the planner (`kind_unavailable`).

Common fields: `image` (required for `run`; ignored for `build` and `checkout`, whose images are
Ankra's), `run`, `uses` (**fatal today**), `with`, `needs`, `if` (CEL), `when.branches|paths|events`,
`matrix` (axes + `include`/`exclude`, ≤ 64 legs), `services`, `env` (names starting `ANKRA_`, `PATH`,
`BUILDKIT*`, `DOCKER_CONFIG` refused), `secrets`, `cache` (`key`, `paths`, `size`, `restore_keys`),
`artifacts` (`name`, `paths`, `retention_days`), `test_results` (`junit`|`go-test`|`pytest`|`playwright`;
carried, not ingested yet), `outputs`, `timeout` (default 30m, max 6h), `resources.cpu|memory|gpu`
(500m/1Gi default; ≤ 8 cores, 32Gi, 4 GPUs; requests = limits), `runs_on.cluster|node_selector|
tolerations|runtime_class|arch` (all protected), `network`, `shm_size` (64Mi), `working_directory`,
`allow_failure`, `continue_on_error`, `retry` (accepted, not applied), `environment`.

Network tiers: `none` (default for `run` and every non-executing kind), `egress-https` (default for
`checkout`, `build`, `scan`: DNS to cluster DNS, 443 to **public** addresses only; private ranges need
`ci_egress_allowed_cidrs`), `services` (deny-all plus the run's own sidecars; needs at least one
service). Anything above `egress-https` is protected.

Kind blocks:

| Block | Fields |
|---|---|
| `build` | `dockerfile` (path from the repo root), `context`, `target`, `platforms` (do not pin), `build_args` (names, resolved from variables), `provenance`, `sbom`, `cache_to`. `dockerfile` and `context` resolve independently: `context: backend` + `dockerfile: Dockerfile` reads the **root** Dockerfile; write `dockerfile: backend/Dockerfile`. |
| `scan` | `scanners` (`semgrep`, `checkov`, `trivy`), `fail_on` (scanner → its own severity word, folded into the gate tighten-only; a value outside that scanner's vocabulary — the generated `trivy: "app"`, `checkov: "none"` — carries no floor and leaves the organisation gate to decide), `with.semgrep_config` (space-separated packs; else `--config=auto`), `with.image_gate` (`app`/`all`/`off`, tighten-only) |
| `gate` | `require_stages` (the scan stages whose findings it judges; a required scanner that recorded nothing fails it by name), `approval_roles` |
| `publish` | `with.image` is accepted and **not read** — the target is the application's own registry, resolved like the build's push target. Needs a successful `gate` upstream; re-tags the judged digest as `sha-<7>`. |

What every step carries: the workspace at `/workspace`; `ANKRA_RUN_ID`, `ANKRA_STEP_ID`,
`ANKRA_STEP_KEY`, `ANKRA_STEP_KIND`, `ANKRA_ORGANISATION_ID`, `ANKRA_CLUSTER_ID`, `ANKRA_REPOSITORY_ID`,
`ANKRA_APPLICATION_ID`, `ANKRA_HEAD_SHA`, `ANKRA_REF`, `ANKRA_IS_FORK`, `ANKRA_WORKSPACE`;
`$ANKRA_OUTPUT` (append `key=value`, ≤ 64 KiB); declared secrets and credentials as files under
`/run/agent-secrets`, revealed once at step start by the agent, never in the payload or a log line.
`registry_auth` and `source_token` are reserved names Ankra adds to `build` (and the Trivy leg of
`scan`) from the application's push robot.

## Expressions

`${{ … }}` with **CEL** inside, evaluated with no I/O. Contexts: `ankra.*` (`organisation`,
`repository.owner|name|provider`, `application`, `ref`, `sha`, `sha_short`, `run.id|number`,
`environment.name|url`, `urls.run|portal`), `event.*` (`kind`, `provider`, `pull_request.number|
is_fork|head_repo`, `ref`, `before_sha`, `changed_files`), `inputs.<name>`, `matrix.<name>`,
`vars.<NAME>` (organisation, cluster and environment variables), `env.<NAME>`,
`steps.<key>.outputs.<name>` / `.outcome`, `needs.<stage>.outputs.<name>` / `.result`,
`secrets.<NAME>` (`true` when declared, never the value). A bare object such as `${{ ankra.repository }}`
(what the generated pipeline's concurrency group uses) renders as compact JSON, so it is a unique
per-repository key; `ankra.repository.owner`/`.name` is the readable form. Functions: `hashFiles`, `contains`,
`startsWith`, `endsWith`, `fromJSON`, `toJSON`, `format`, `join`, `always()`, `success()`,
`failure()`, `cancelled()`, `skipped()`, plus the CEL standard library. Two departures from GitHub:
`toJSON` is compact, and comparisons are typed (`'1' == 1` is an error). A reference to an
undeclared step key interpolates to the empty string. `has()` is refused on the defaulting roots.

## A pipeline that uses most of it

```yaml
apiVersion: ankra.io/v1
kind: Pipeline
metadata: { name: "shop" }
on:
  push: { branches: ["main"] }
  pull_request: { branches: ["main"], fork_policy: "read_only" }
  tag: { patterns: ["v*"] }
  schedule:
    - { cron: "0 3 * * 1-5", branch: "main", stages: ["checkout", "install", "e2e"] }
  manual:
    inputs:
      - { name: "suite", type: "choice", enum: ["unit", "e2e", "all"], default: "unit" }
concurrency:
  group: "${{ ankra.repository.owner }}-${{ ankra.repository.name }}-${{ ankra.ref }}"
  cancel_in_progress: true
workspace: { size: "10Gi", access: "rwo" }
defaults: { image: "node:22-bookworm-slim", timeout: "20m" }
permissions: { contents: "read" }
secrets:
  - { name: "NPM_TOKEN", from: "org_variable" }
services:
  postgres:
    image: "postgres:16"
    env: { POSTGRES_PASSWORD: "test", POSTGRES_DB: "shop" }
    ports: ["5432"]
    ready: { tcp: "5432" }
stages:
  - { name: checkout, kind: checkout, with: { fetch_depth: "1" } }
  - name: install
    kind: run
    needs: [checkout]
    network: egress-https
    secrets: [NPM_TOKEN]
    cache:
      - { key: "pnpm-${{ hashFiles('pnpm-lock.yaml') }}", paths: [".pnpm-store"], restore_keys: ["pnpm-"] }
    run: |
      corepack enable && pnpm install --frozen-lockfile
  - name: unit
    kind: run
    needs: [install]
    if: "${{ inputs.suite != 'e2e' }}"
    matrix: { node: ["20", "22"] }
    image: "node:${{ matrix.node }}-bookworm-slim"
    run: pnpm test -- --reporter=junit --outputFile=junit-${{ matrix.node }}.xml
    test_results: [{ format: junit, path: "junit-${{ matrix.node }}.xml" }]
  - name: e2e
    kind: run
    needs: [install]
    if: "${{ inputs.suite != 'unit' || event.kind == 'schedule' }}"
    network: services
    services: [postgres]
    env: { DATABASE_URL: "postgres://postgres:test@postgres:5432/shop" }
    run: pnpm run migrate && pnpm run e2e
    artifacts: [{ name: "playwright-report", paths: ["playwright-report"], retention_days: 14 }]
    allow_failure: true
  - name: build
    kind: build
    needs: [unit]
    build: { dockerfile: "Dockerfile", build_args: ["VERSION"], provenance: true, sbom: true }
  - name: scan
    kind: scan
    needs: [build]
    with: { image_gate: "app", semgrep_config: "p/default p/docker p/javascript p/typescript" }
    scan: { scanners: [semgrep, checkov, trivy], fail_on: { semgrep: "error", checkov: "none", trivy: "app" } }
  - { name: gate, kind: gate, needs: [scan], gate: { require_stages: [scan] } }
  - name: publish
    kind: publish
    needs: [gate]
    when: { events: [push, tag] }
```

Read against what executes today: everything above runs. `test_results` is carried and not read;
the run's verdict is the stages' exit codes plus the gate. A `verify`, `agent` or `webhook` stage
added here would validate as `warn` and be skipped — say so in the PR that adds it.

## Scripting contract

Exit codes, every command: `0` ok · `1` API/runtime failure or a run that concluded without success ·
`2` usage, bad flag, ambiguous selection · `3` target does not exist / no run matched · `4`
confirmation declined · `5` `--wait`/`--timeout` expired, or `--exit-code` on an unconcluded run ·
`6` not logged in / token rejected · `7` authenticated but forbidden. `-o json` is per command
(`json`|`yaml`); the Kubernetes reads under `ankra cluster get|logs|events|describe` use `-o
table|json|yaml`. Progress goes to stderr; stdout stays parseable.

Gate an external job on the in-cluster run (the one place another CI belongs — as a consumer):

```bash
# GitHub Actions: a pull_request job's checkout is a merge commit, so pass the head sha explicitly
ankra pipeline get --application shop --head-sha "${{ github.event.pull_request.head.sha }}" \
  --trigger pull_request --latest --wait --timeout 45m
# Any shell: dispatch a named commit and follow it
ankra pipeline run --application shop --sha "$(git rev-parse HEAD)" --ref main --wait --timeout 45m -o json
# Stream state changes to something else
ankra pipeline get <run-id> --application shop --watch -o json | jq -c 'select(.event=="step") | {step_key,status,outcome}'
```

`ankra pipeline get` on a run: `status` (`queued`|`running`|`concluded`), `outcome`
(`success`|`failure`|`cancelled`|`timed_out`|`skipped`|`infra_error`), `authority_state`
(`approved`|`unapproved`|`changed_on_head`|null), `definition_source` (`head_file`|`stored`|
`default`), per step `status` (`blocked`|`pending`|`running`|`concluded`), `outcome`, `error_class`,
`executor` (`in_cluster`|`platform_builders`|`platform`), `cache_result` (`hit`|`miss`|`restore`|
`disabled`), `outputs`. The `run_id` field is the umbrella run across lifecycles — correlate it with
`ankra cluster operations list`; address pipeline commands by the run's own id.

`ankra application ship -o json`: `application_id`, `application_name`, `repository`, `branch`,
`registered`, `setup_pr_url`, `build_ref`, `organisation_name`, `cluster_id`, `cluster_name`,
`namespace`, `installation_id`, `url`, `state` (`healthy`|`parked`).

## Organisation CI settings

`ankra org ci-settings set` writes only the flags you pass (admin only). `--cluster` (the CI cluster;
empty clears; unset falls back to the AI staging cluster), `--build-fallback platform_builders|none`
(default `platform_builders`, also needs the `Platform builds enabled` grant), `--image-gate
app|all|off`, `--ignore-unfixed` (default true; `=false` makes every unfixed finding block),
`--allowed-image-prefix` (replaces the list; organisation registry and platform images are always
allowed), `--egress-allowed-cidr` (replaces the list, ≤ 32), `--max-parallel-runs` (default 4, ≤ 64,
per organisation), `--max-parallel-steps` (default 8, ≤ 256, per organisation),
`--run-retention-days` (90; 7–365), `--artifact-retention-days` (30), `--cache-retention-days` (14).

Per cluster: `ankra cluster agent ci set --workers N --storage-class <sc> --cluster <cluster>`;
`--workers 0` disables the scheduler. Stored on the platform and re-rendered into the agent's own
release; an agent older than 2.1.1108 cannot take the values and 2.1.1115+ is the CI-lane floor.

## Lanes that are API-only today

All under `/api/v1/org/applications/{application_id}` with a bearer token (the same paths without
`/api/v1` for a browser session). Names accept ids only here.

| Lane | Call | Body / answer |
|---|---|---|
| Auto-fix builds (default on) | `GET` / `PUT …/auto-fix-builds` | `{"enabled": false}` |
| Rollback targets | `GET …/rollback-targets` | `targets[]` of `image_tag`, `pushed_at`, `is_current`; `not_reverted[]`; `auto_deploy_enabled`; browser session only |
| Roll back | `POST …/rollbacks` | `{"cluster_id": "…", "image_tag": "sha-1a2b3c4"}`; holds until a newer build is observed |
| Promotion preview | `GET …/promotions/preview?source_environment_id=…&target_environment_id=…` | `state` (`ready`\|`already_current`\|`blocked`), `blocker` (`source_not_settled`, `target_follows_pushes`, `secrets_missing`, …), config differences, `fingerprint` |
| Promote | `POST …/promotions` | `{"source_environment_id","target_environment_id","expected_fingerprint"}` — the fingerprint from the preview; the tag and only the tag moves |
| Deploy runs | `GET …/deploy-runs`, `GET …/deploy-runs/{run_id}` | the journey phases `analyse` → `build` → `stacks` → `dns` → `certificate` → `serving` |
| Validate a definition | `POST …/pipeline/validate` | `{"spec_yaml": "…"}` (empty = the stored one); answers planned steps per event, skips and diagnostics |
| Stored definition | `GET` / `PUT …/pipeline` | `{"spec_yaml": "…"}`; `pipelines.manage` |
| Approve authority | `POST /api/v1/org/pipelines/definitions/{id}/approve` | no body; human actor with `pipelines.manage` |
| Cluster CI workers | `PUT /api/v1/org/clusters/{cluster_id}/agent/ci-settings` | `{"ci_worker_count": 2}` |
| Org CI settings | `GET` / `PUT /api/v1/org/ci-settings` | the `ci_*` fields above |
| Step log stream | `GET …/pipeline-runs/{run_id}/steps/{step_id}/logs` | SSE; frames are `{"stream","line"}` plus `seq`; `410` past 24 h — read the `step_log` artifact |

## Error classes and frozen sentences

| Class | Whose | Meaning |
|---|---|---|
| `step_failed` | yours | the step exited non-zero; routes to auto-fix on the tracked branch |
| `timeout` | yours | exceeded `timeout` |
| `image_gate_blocked` | yours | a finding at or above the gate; `ankra pipeline findings` |
| `no_scan_results` | platform/vault | a required scanner recorded nothing: "This is not a clean scan" |
| `report_unreadable` | platform | the tool wrote a report Ankra could not read (pre-vault-default platforms) |
| `artifact_store_unavailable` | organisation | "This step's reports need a ready backup vault in the organisation, and there is none." |
| `step_refused` | authority | a binding with no grant: "This step declares a secret binding the platform sent no value for (registry_auth)" |
| `policy_refused` | authority | image, network, placement or resources outside the organisation's policy |
| `secret_unavailable` | configuration | a declared registry with no resource name |
| `recipe_missing` | yours | no Dockerfile at that commit |
| `build_emulation_missing` | yours | a pinned platform the node cannot emulate |
| `build_runtime_confined` | node | the runtime refused the rootless builder; not charged to the retry budget; fall back |
| `build_fallback_none` / `build_fallback_unsupported` | settings | fallback off or missing the grant; bare `build_args` on the fallback |
| `platform_build_infra` | Ankra | the builders lane failed; retried once |
| `registry_push_failed` | registry | the push was refused (401, gone, unreachable) — a revoked robot looks like this |
| `publish_failed` | registry | re-tag refused: no gate, digest mismatch, host mismatch, cannot mount blobs |
| `stranded` | Ankra | the executor was lost; never retried |
| `kind_unavailable` | planner skip | the stage's kind has no executor on this build |

Other vocabulary: `pipeline_source` ∈ `ankra_pipeline` · `own_ci` · `generated_workflow` · `none`
(the disposition ladder; a stored definition is first-rung evidence). Trigger answers:
`enqueued`, `already_queued`, `already_recorded`, `skipped`, `refused`, `repository_not_onboarded`,
`no_ci_cluster`, `no_definition`, `trigger_not_declared`. Planner skips: `event_filter`,
`branch_filter`, `path_filter`, `schedule_subset`, `condition_false`, `condition_unreadable`,
`fork_policy`, `dependency_skipped`, `kind_unavailable`. Notification kinds: `pipeline_run_failed`,
`pipeline_run_succeeded`, `image_gate_blocked`, `preview_ready`, `preview_failed`. Comment markers:
`<!-- ankra-ai:review -->` (sections pipeline · preview · review · promotion), fallback
`<!-- ankra-ai:pipeline -->` when the GitHub App lacks `checks:write`; check run `Ankra pipeline`;
setup branch `ankra/setup-<app>`; migration branch `ankra/pipeline-migration`. Permissions:
`pipelines.read|operate|manage`, `runs.read|operate`, `promotions.read|request|approve` (approve is
human-only), `credentials.read|write|delete`.

## Limits

Step timeout 30m default, 6h max · one automatic retry for Ankra's own failure classes only ·
matrix ≤ 64 legs (8 on a restricted fork) · step compute 500m/1Gi default, ≤ 8 cores, 32Gi, 4 GPUs
(2 cores/4Gi on a fork) · caches 5Gi per path · artifacts 512 MiB per object, 2 GiB per run, 20
uploads per step, links valid 5 minutes · parallelism 4 runs / 8 steps per organisation by default ·
retention: artifacts and logs 30 days, caches 14, runs 90 · grants ≤ 32 per step, alive for the
step's timeout plus 15 minutes · live log queues 1,024 lines and keeps the last 8 MiB on upload ·
expressions ≤ 8,192 bytes per template, 64 per template, 2,048 bytes and 24 levels each.
