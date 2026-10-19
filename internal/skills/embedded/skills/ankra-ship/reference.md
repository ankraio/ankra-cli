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
on_failure: […]                # runs only if a main stage failed, timed out or hit an infra error; skips are not failures
finally: […]                   # runs after everything else, whatever the outcome; also after a cancel
full_run: { events: [push], branches: [main] }   # these runs set every path filter aside (logic, not protected)
path_sets: { <name>: [patterns | {set: <name>}] }  # named path lists, below
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
`rerun`, `merge_train`. A `rerun` is planned as the event the run it repeats was planned as: a
re-run of a push plans the push's `publish` again (the image never becomes the push-to-deploy
candidate, so a re-run rolls nothing), a re-run of a pull request or a merge train car
plans as a pull request, a re-run of a schedule repeats that entry's `stages`, and it reuses the
original's change list. A re-run of a tag, manual, API or agent run is planned as `rerun`. A
`merge_train` car is planned as a pull request. Dispatches (`manual`, `api`, `agent`, `rerun`) are never deduplicated; provider events are
(`already_queued`, `already_recorded`).

## Stages

Every stage: `name` (`^[a-z0-9][a-z0-9_-]*$`, unique across `stages`/`on_failure`/`finally`) and a
`kind` from the closed vocabulary. Seven execute today: `checkout`, `run`, `build`, `scan`, `gate`,
`publish`, `deploy`; `gate`, `publish` and `deploy` are settled by the platform, never a Job. The
other eight (`preview`, `verify`, `agent`, `approval`, `webhook`, `automation`, `ankra`,
`external`) warn at validation and are skipped by the planner (`kind_unavailable`). `deploy` rolls a
signed release bundle out to **host targets** (machines outside Kubernetes running
`ankra-host-agent`, `ankra targets`), never to a cluster: an application's push-to-deploy does that.

Common fields: `image` (required for `run`; ignored for `build` and `checkout`, whose images are
Ankra's), `run`, `uses` (**fatal today**), `with`, `needs`, `skippable_needs` (needs this stage still
runs without when their own path filter left them out), `paths` (affected-only: below), `affected`
(`go`/`pnpm` packages; affected-only from the dependency graph: below), `if` (CEL),
`when.branches|paths|events`, `when.push|pull_request` (event-scoped `branches`/`paths`), `when.tag`,
`matrix` (axes + `include`/`exclude`, ≤ 64 legs), `services`, `env` (names starting `ANKRA_`, `PATH`,
`BUILDKIT*`, `DOCKER_CONFIG` refused), `secrets`, `cache` (`key`, `paths` relative to the workspace root, `size`, `restore_keys`, `fallback` `volume`|`none`),
`artifacts` (`name`, `paths`, `retention_days`), `test_results` (`junit`|`go-test`|`pytest`|`playwright`;
every test's outcome recorded, `ankra pipeline tests <run>`), `outputs`, `timeout` (default 30m, max 6h), `resources.cpu|memory|gpu`
(500m/1Gi default; ≤ 8 cores, 32Gi, 4 GPUs; requests = limits), `runs_on.cluster|node_selector|
tolerations|runtime_class|arch` (all protected), `network`, `shm_size` (64Mi), `working_directory`,
`allow_failure`, `continue_on_error`, `retry` (accepted, not applied), `environment`.

Path filters, three shapes:

- `paths:` makes a stage **affected-only**: it runs only when a changed file matches, and when it is
  left out it **counts as satisfied**, so the stages behind it still run (gate and publish included).
  It applies on every event. Refused (`pipeline_paths:`) on a stage whose outputs another reads, a
  `build` another stage needs, a `checkout`, `gate` or `approval`, a scan a gate requires, and beside
  a `when` path filter.
- `affected: { go: [...], pnpm: [...] }` makes a stage affected-only from the **code's dependency
  graph**: `go` takes `./...`/`./pkg` relative to `working_directory` or import paths (only a
  trailing `/...` wildcard), `pnpm` takes package names or `./dirs`. The planner leaves the answer to
  the run's checkout step, which reads the graph of the exact commit (every `go.mod`, local
  `replace`, `_test.go` imports of the stage's own packages, `package.json` deps incl. `workspace:`)
  and writes `ankra.affected.<stage>`; the dispatcher skips the step as `not_affected` (satisfied)
  only on `false`, at once, without waiting for a CI slot, and records the time saved
  (`not_affected`). No answer runs it. Promotion (`promote_from`) counts a source run's
  `not_affected` skip when the push plans the stage for the graph too, asks it the same patterns,
  and changes only files the source's analysis read; anything else builds the push in full. `paths:` beside it is an OR (runtime-read files). An
  incomplete change list, a `.ankra/pipeline.yaml` change or a matching `paths:` runs it without the
  graph. Same refusals as `paths:`, beside a `when` path filter included; no `${{ }}` in
  patterns; no checkout stage is a warning.
- `when.paths` (or the event block's `paths`) leaves the stage out and **skips what needs it**
  (`dependency_skipped`), unless the dependent lists it in `skippable_needs`.
- `path_sets:` (top level) names lists of patterns once; any of the lists above, and
  `on.push|pull_request.paths`, takes `{set: <name>}` in place of a pattern (or as the whole list),
  and a set may include sets. Expanded in place when the file is read, so the plan, the stored
  definition and its hash are the written-out lists; paths are not protected, so a set change needs
  no admin approval. `pipeline_path_sets:` refuses an undeclared set (with a did-you-mean), a cycle,
  `{set: }` in a non-path list (`needs`, `branches`, cache/artifact paths) and malformed entries;
  warns about a set nothing references. `ankra pipeline validate` prints each set, its expansion and
  the lists that use it (`path_sets` in `-o json`).
- A change list the planner could not read in full never leaves a stage out: a Bitbucket Cloud push
  (it carries none), a list over 300 paths, a push that created its branch, or one that rewrote
  history (`paths_unverified`: `unsupported_provider`, `truncated`, `branch_created`, `forced_push`,
  `provider_error`). A GitHub push's list is the change since the branch's last successful push run,
  so a cancelled or red push's files are not lost.

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
| `publish` | `with.image` is accepted and **not read** — the target is the application's own registry, resolved like the build's push target. Needs a successful `gate` upstream; re-tags the judged digest as `sha-<7>`. `publish.tags` adds extra tags to the same digest (never deploy by them); `publish.promote_from: pull_request` (protected) re-tags a passed pull request run's digest on a push with the identical tree instead of rebuilding - see `ankra-cicd` §7b. Outputs `image_ref` (`<repository>:sha-<7>`), `image_digest`, `image_tag`, `image_tags`: a later stage pins those, never a hand-built name or a copy to another registry. |
| `deploy` | `environment` (the stage's own field) plus `deploy.artifact` (the `publish` stage inside `needs`), `deploy.release` (`metadata.name` of the bundle's `ankra-release.yaml`), `deploy.targets.match_labels`. The artifact must be a signed release bundle (`build.bundle.path` + `build.bundle.manifest`) this run published. Protected. Skipped with no `deploy` block or an unknown environment; never runs for a pull request or fork. Outputs `deployment_id`, `digest`, `environment`, `targets_succeeded`, `targets_failed`. |

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
      - { key: "pnpm-${{ hashFiles('pnpm-lock.yaml') }}", paths: [".ankra-node-cache/pnpm-store"], restore_keys: ["pnpm-"], fallback: none }
    env: { COREPACK_HOME: "/workspace/.ankra-node-cache/corepack" }   # read-only root: no `corepack enable`
    run: |
      corepack pnpm install --frozen-lockfile --store-dir /workspace/.ankra-node-cache/pnpm-store
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

Read against what executes today: everything above runs. `test_results` is recorded per test
(`ankra pipeline tests`); the run's verdict is still the stages' exit codes plus the gate. A `verify`, `agent` or `webhook` stage
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
`disabled`), `outputs`, and the in-cluster start of each step from its pod (`pod_created_at`,
`pod_scheduled_at`, `workspace_attached_at`, `image_pull_started_at`, `image_pulled_at`,
`container_started_at`; null from an agent older than 2.1.1213, and no `image_pull_started_at` when
the node already had the image). The human view turns them into a Queueing line such as
`test: Pending 5m9s in the cluster before it ran (scheduling 4m30s, volumes 8s, image pull 19s)`.
The `run_id` field is the umbrella run across lifecycles — correlate it with
`ankra cluster operations list`; address pipeline commands by the run's own id.

`ankra application ship -o json`: `application_id`, `application_name`, `repository`, `branch`,
`registered`, `setup_pr_url`, `build_ref`, `organisation_name`, `cluster_id`, `cluster_name`,
`namespace`, `installation_id`, `url`, `url_problem`, `state` (`healthy`|`parked`|`unroutable`).

## Organisation CI settings

`ankra org ci-settings set` writes only the flags you pass (admin only). `--cluster` (the CI cluster;
empty clears; unset falls back to the AI staging cluster), `--build-fallback platform_builders|none`
(default `platform_builders`, also needs the `Platform builds enabled` grant), `--image-gate
app|all|off`, `--ignore-unfixed` (default true; `=false` makes every unfixed finding block),
`--allowed-image-prefix` (replaces the list; organisation registry and platform images are always
allowed), `--egress-allowed-cidr` (replaces the list, ≤ 32), `--max-parallel-runs` (default 16, ≤ 64,
per organisation), `--max-parallel-steps` (default 8, ≤ 256, per run),
`--run-retention-days` (90; 7–365), `--artifact-retention-days` (30), `--cache-retention-days` (14).
Portal and API only (no CLI flag yet): `ci_always_run_stages` (stage names no path filter can skip;
a pull request cannot change it), `ci_full_run_on_default_branch`, and `ci_skip_markers`
(`except_default_branch` default, `everywhere`, `never` - where `[skip ci]` skips a branch push).

CI cluster pool: `ankra org ci-settings pool list|add <cluster> [--weight N]|remove <cluster>` spreads
runs over more clusters than `--cluster` (the primary, always a member). Each run is pinned at its
first step to the least-loaded member whose agent runs steps (in-flight steps over workers, scaled by
weight 1–1000, default 100), preferring the member where the repository ran last; a run never moves.
No listed members = everything on `--cluster`, as before. Move one repository without
disconnecting: `ankra pipeline repositories set-cluster <repository-id> --cluster <cluster>` (`""`
clears); queued and running runs keep their cluster.

Per cluster: `ankra cluster agent ci set --workers N --storage-class <sc> --cluster <cluster>`;
`--workers 0` disables the scheduler. Stored on the platform and re-rendered into the agent's own
release; an agent older than 2.1.1108 cannot take the values and 2.1.1115+ is the CI-lane floor.
`--node-group <group>` (or `--node-selector k=v`, `--toleration k[=v]:Effect`, `--placement
required|preferred`, `--clear-placement`) puts every pipeline pod on chosen nodes, and
`--run-reservation off|shadow|on` (default `off`) makes each run reserve the room its later steps
need on its node first; `shadow` sizes and records the reservation without holding anything, so
start there. `on` needs `Reservation support: advertised` on `ankra cluster agent ci get` and can
add or hold a node. Both apply to the next dispatched step with no agent restart.

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
| Org CI pool | `GET` / `PUT /api/v1/org/ci-settings/pool`, `PUT` / `DELETE …/pool/{cluster_id}` | `{"members": [{"cluster_id", "weight"}]}`; `{"weight": 150}`; admin |
| Repository CI cluster | `PATCH /api/v1/org/pipelines/repositories/{repository_id}` | `{"cluster_id": "…"}` or `null` to clear; `pipelines.manage` |
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
| `deploy_untrusted_event` / `deploy_authority_unapproved` / `deploy_artifact_unpublished` / `deploy_artifact_not_release_bundle` / `deploy_artifact_unsigned` / `deploy_no_targets` | deploy | a `deploy` step refused: pull request or fork run, definition not approved, an artifact this run did not publish, not a release bundle, no signature, no matching host target |

Other vocabulary: `pipeline_source` ∈ `ankra_pipeline` · `own_ci` · `generated_workflow` · `none`
(the disposition ladder; a stored definition is first-rung evidence). Trigger answers:
`enqueued`, `already_queued`, `already_recorded`, `skipped`, `refused`, `repository_not_onboarded`,
`no_ci_cluster`, `no_definition`, `trigger_not_declared`. Planner skips: `event_filter`,
`branch_filter`, `path_filter`, `schedule_subset`, `condition_false`, `condition_unreadable`,
`fork_policy`, `dependency_skipped`, `kind_unavailable`, `not_affected`, `skip_marker` (`[skip ci]`), `deploy_untrusted_event` (a `deploy` stage
in a pull request or fork run), `promoted` (a push publishes the digest a pull request or merge train
run already passed for the same tree, so the stages it needs are not run again). Notification kinds: `pipeline_run_failed`,
`pipeline_run_succeeded`, `image_gate_blocked`, `preview_ready`, `preview_failed`. Comment markers:
`<!-- ankra-ai:review -->` (sections pipeline · preview · review · promotion), fallback
`<!-- ankra-ai:pipeline -->` when the GitHub App lacks `checks:write`; check run `Ankra pipeline`;
setup branch `ankra/setup-<app>`; migration branch `ankra/pipeline-migration`. Permissions:
`pipelines.read|operate|manage`, `runs.read|operate`, `promotions.read|request|approve` (approve is
human-only), `credentials.read|write|delete`.

## Limits

Step timeout 30m default, 6h max · one automatic retry for Ankra's own failure classes only ·
matrix ≤ 64 legs (8 on a restricted fork) · step compute 500m/1Gi default, ≤ 8 cores, 32Gi, 4 GPUs
(2 cores/4Gi on a fork) · caches 4 GiB per saved path, 10 GiB per repository (a 5Gi volume per path on an agent without cache archives) · artifacts 512 MiB per object, 2 GiB per run, 20
uploads per step, links valid 5 minutes · parallelism 16 runs per organisation, 8 steps per run by default ·
retention: artifacts and logs 30 days, caches 14, runs 90 · grants ≤ 32 per step, alive for the
step's timeout plus 15 minutes · live log queues 1,024 lines and keeps the last 8 MiB on upload ·
expressions ≤ 8,192 bytes per template, 64 per template, 2,048 bytes and 24 levels each.

## The generated pipeline, annotated

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
    with: { image: "harbor.ankra.cloud/<project>/shop" }   # recorded, not read: the target is the app's registry
```

Stages without `needs` run in parallel; `publish` refuses without a successful `gate` upstream and
only runs on `push`, so a pull request builds, scans and reports but never publishes. `fail_on`
values outside a scanner's own severity words (`trivy: "app"`, `checkov: "none"`) carry no floor
and leave the organisation gate to decide. `registry_auth` and `source_token` are reserved bindings
Ankra adds from the push robot — never declare them. Do not pin `build.platforms`: builds run
natively, and a fabricated `linux/amd64` fails on an arm64 cluster ("no QEMU emulation
registered"). [reference.md](reference.md) has the full schema.


## Run states, error classes and re-runs

`get --wait --exit-code` is the primitive to gate anything external on: `0` succeeded, `1` concluded
otherwise, `5` not concluded by `--timeout`, `3` no run matched. `status` is the lifecycle
(`queued` → `running` → `concluded`); `outcome` is the verdict (`success`, `failure`, `cancelled`,
`timed_out`, `skipped`, `infra_error`). A failed run names its error class: `step_failed` is your
build, `image_gate_blocked` is a finding, `registry_push_failed`, `build_runtime_confined` and
`platform_build_infra` are placement or Ankra. A `rerun` is planned as the event it repeats: a
re-run of a push publishes the `sha-` image again but **does not become the push-to-deploy
candidate**, so it never rolls a deployment; a re-run of a pull request does not publish, and a
re-run of a tag, manual, API or agent run is planned as `rerun`, which a `when.events` list must
name. `--full` (on `run` and `rerun`) sets path filters aside for that run. `--failed-only` also re-runs the checkout and every step the failed ones
build on (the new run's workspace starts empty), and is refused when a step that failed could not
run in a re-run. Logs attach once a step has an execution; a concluded step replays for
24 h and from the archived artifact after that.


## Converting a GitHub Actions application

```bash
ankra application pipeline convert <application-id>                  # stores the converted definition, opens the PR
ankra application pipeline convert <application-id> --keep-workflows
```

Ankra converts the workflow to `.ankra/pipeline.yaml`, records it as the definition of record,
**switches the generated workflow off on GitHub** so no commit builds twice, and opens a PR on
`ankra/pipeline-migration` that commits the file and removes Ankra's generated workflow (a workflow
the repository wrote itself is never touched). Review the result: marketplace actions it cannot map
become `run` stages that exit 1 with an explanation, never a silent drop.
A job with no `container:` runs in an Ubuntu image matching its `runs-on` (`buildpack-deps:noble`
for `ubuntu-latest`/`ubuntu-24.04`, `buildpack-deps:jammy` for `ubuntu-22.04`, pinned by digest),
set as `defaults.image`, and a `setup-node`/`setup-go`/`setup-python`/`setup-java` step with a
version becomes the language image of the stages after it. The runner image has git, curl, make
and gcc but not GitHub's full tool set, so check the notes for stages that need another image.


## A test stage with a database, matrix and cache

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
    env:
      DATABASE_URL: "postgres://postgres:test@postgres:5432/shop?sslmode=disable"
      GOMODCACHE: "/workspace/.ankra-go/mod"     # the root filesystem is read-only: keep caches on the workspace
      GOCACHE: "/workspace/.ankra-go/cache"
    cache:
      - key: "go-${{ hashFiles('go.mod', 'go.sum') }}"
        paths: [".ankra-go/mod", ".ankra-go/cache"]  # relative to the workspace root
        restore_keys: ["go-"]
        fallback: none
    run: |
      go test ./... -json > test-results.json
    test_results: [{ format: go-test, path: "test-results.json" }]
    matrix:
      go: ["1.25", "1.26"]
```


## Caches and outputs

Caches: `hashFiles` is evaluated after checkout in the stage's `working_directory`; the exact key
restores first, then each `restore_keys` prefix in order. A default-branch run reads and saves only
its own scope; a branch or PR run reads its own scope then the default branch's and saves only its
own; fork and tag runs get none. A save happens only after the step passes (a `hashFiles` key once,
a static key every time). 14 days unused, 10 GiB per repository, 4 GiB per path. On an agent
without cache archives a cache is a shared 5Gi volume per path - `fallback: none` makes it an
empty directory instead, which never wedges overlapping runs. The generated `test` stage declares
its package caches (pnpm/Yarn/npm, Go, pip/uv/Poetry/Pipenv, cargo) this way under
`/workspace/.ankra-*`, keyed on the lock file, with `fallback: none`; a committed file is never
rewritten, so copy the block into an older one. A cache path outside the workspace is refused. Every step's output is archived as a `step_log` artifact, and `key=value` lines
appended to `$ANKRA_OUTPUT` become `${{ needs.test.outputs.<name> }}` downstream.


## What is not there yet, in full

Say this out loud rather than promising it. The spec accepts fifteen stage kinds; **seven execute**:
`checkout`, `run`, `build`, `scan`, `gate`, `publish`, `deploy`. `deploy` releases a signed bundle
to host targets outside Kubernetes (`ankra targets`), never to a cluster. `preview`, `verify`, `agent`,
`approval`, `webhook`, `automation`, `ankra` and `external` validate with a warning and are skipped
as `kind_unavailable`; `uses:` is refused outright (write the step as `run:`); `retry:` is
accepted but not yet applied. `on_failure:` stages run only after a
main stage failed, timed out or hit an infrastructure error (a skipped stage is not a failure; after
a green run they are skipped with that reason), and `finally:` stages run after everything else
whatever the outcome. Cancelling a run that started (a person, a superseding push, a closed pull
request, the merge train) stops its main and `on_failure` stages, runs its `finally:` stages
(15-minute budget; `ankra pipeline cancel --force` skips them), then concludes it `cancelled`. So **an
`approval` stage blocks nothing — everything downstream runs**, and a skipped stage is not a failed
one: do not make the `Ankra pipeline` check *required* while the definition carries an unexecuted
kind, or it goes green having done none of what that stage names. Previews, deploys to a cluster, rollback and
promotion happen through the application lanes above, not inside a pipeline. GitLab and Bitbucket
plan from the stored definition (the head-sha file is read on GitHub only) and have no setup-PR
writer yet. `egress-https` reaches **public addresses only**; name a private range with
`ankra org ci-settings set --egress-allowed-cidr <cidr>` before the step runs.


## Rarer symptoms

| Symptom | Cause | Do |
|---|---|---|
| `build_fallback_none` | fallback needs `--build-fallback platform_builders` **and** the grant | `ankra org ci-settings get` |
| Exit 79, "no QEMU emulation registered" | pinned `platforms` on an arm64 cluster | drop `build.platforms` |
| `no_scan_results` | scan report never uploaded | a ready backup vault (`ankra-backups`) or an older platform; do not treat as clean |
| A stage's log says `UNAUTHORIZED: project <name> not found` from a Harbor registry | the project does not exist; it is not a credential fault | a hand-written copy to a second registry: remove it and deploy `publish`'s `image_ref` (`ankra-cicd` §3), or declare the registry on the application |
| Test or build times out reaching a private host | `egress-https` is public-only | `ankra org ci-settings set --egress-allowed-cidr 10.0.0.0/8` |
| No "Ankra pipeline" check on the PR, only a comment | GitHub App lacks `checks:write` | grant it on the installation |
| Setup PR 403 "Resource not accessible by integration" | repo not in the App installation | add it (owner-only) or choose "All repositories" |
