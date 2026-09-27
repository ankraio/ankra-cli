# Ankra CI/CD — `.ankra/pipeline.yaml` reference and examples

Two complete pipelines Ankra runs in-cluster (§A a test-only repository with no application, §B an
application that builds and publishes), the field reference (§D), and — last, because it applies
only to a repository Ankra cannot connect — the manual GitOps-bump pattern (§C). SKILL.md holds the
decision table, the connect/approve sequence and the troubleshooting table.

Both Ankra examples are modelled on the platform's own fixtures (`enginekit/pipelinespec/testdata/`).
Everything marked **protected** needs a one-time human `ankra pipeline definitions approve` after it
first lands on the default branch (SKILL.md §5).

## A. A test-only repository with a Postgres sidecar (no application)

A platform monorepo: a Rust agent, a Go API with Postgres integration tests, a Next.js portal. No
image to deploy. Connected with `ankra pipeline repositories connect --provider github --owner <owner>
--name <repository> --credential <credential> --default-branch main`; every command is
`--repository <repository-id>`.

```yaml
# .ankra/pipeline.yaml
apiVersion: ankra.io/v1
kind: Pipeline
metadata:
  name: "ankra-cloud"
on:
  push:
    branches: ["main"]
  pull_request:
    branches: ["main"]
    fork_policy: "read_only"            # forks build and test with no secrets, no sidecar credentials
  schedule:
    - cron: "0 3 * * 1-5"
      branch: "main"
      stages: ["checkout", "api-test", "agent-test"]   # the nightly runs only these
concurrency:
  group: "ankra-cloud-${{ ankra.ref }}"
  cancel_in_progress: true
workspace:
  size: "20Gi"
  access: "rwo"
defaults:
  timeout: "30m"
  network: "egress-https"               # run stages default to none; toolchains fetch, so widen here (not protected)
permissions:
  contents: "read"                      # protected
services:                               # protected: the sidecar catalog
  postgres:
    image: "postgres:17-alpine"
    env:
      POSTGRES_PASSWORD: "test"
      POSTGRES_DB: "ankra_cloud_test"
    ports: ["5432"]
    ready:
      tcp: "5432"                       # without a probe the stage starts before the database listens
stages:
  - name: "checkout"
    kind: "checkout"

  - name: "api-test"
    kind: "run"
    needs: ["checkout"]
    image: "golang:1.26"
    run: |
      set -eu
      cd api
      go vet ./...
      go test -race -count=1 ./... -json > test-results.json
    services: ["postgres"]
    network: "services"                 # protected: the tier that reaches a sidecar
    env:
      ANKRA_CLOUD_TEST_DATABASE_URL: "postgres://postgres:test@postgres:5432/ankra_cloud_test?sslmode=disable"
    cache:                              # protected
      - key: "go-${{ hashFiles('api/go.sum') }}"
        paths: ["/root/.cache/go-build", "/root/go/pkg/mod"]
        restore_keys: ["go-"]
    test_results:
      - format: "go-test"
        path: "api/test-results.json"
    timeout: "20m"                      # protected
    resources:                          # protected
      cpu: "2"
      memory: "4Gi"

  - name: "api-lint"
    kind: "run"
    needs: ["checkout"]
    image: "golangci/golangci-lint:v2.12.2"
    run: |
      set -eu
      cd api && golangci-lint run --timeout 10m ./...

  - name: "agent-test"
    kind: "run"
    needs: ["checkout"]
    image: "rust:1.85"
    run: |
      set -eu
      cd agent
      cargo fmt --all -- --check
      cargo clippy --workspace --all-targets -- -D warnings
      cargo test --workspace
    cache:
      - key: "cargo-${{ hashFiles('agent/Cargo.lock') }}"
        paths: ["/usr/local/cargo/registry", "agent/target"]
        restore_keys: ["cargo-"]
    timeout: "30m"

  - name: "portal-test"
    kind: "run"
    needs: ["checkout"]
    image: "node:22"
    working_directory: "portal"
    run: |
      set -eu
      corepack enable
      pnpm install --frozen-lockfile
      pnpm test:ci
    cache:
      - key: "pnpm-${{ hashFiles('portal/pnpm-lock.yaml') }}"
        paths: ["/root/.local/share/pnpm/store"]
    artifacts:
      - name: "portal-test-report"
        paths: ["portal/test-results/"]
        retention_days: 7
    test_results:
      - format: "junit"
        path: "portal/test-results/junit.xml"

  - name: "shellcheck"
    kind: "run"
    needs: ["checkout"]
    image: "koalaman/shellcheck-alpine:stable"
    network: "none"                     # nothing to fetch
    run: |
      set -eu
      shellcheck -x -e SC1091 sim/*.sh sim/node/*.sh
```

What to tell the human before the first merge: `api-test` declares a sidecar, `network: services`,
a cache, a timeout and resources — all protected — so the first default-branch run will conclude
**skipped** with the fatal-violation sentence until an organisation admin runs
`ankra pipeline definitions approve <definition-id>` once. If nobody with `pipelines.manage` is at
hand, land `agent-test`, `portal-test`, `api-lint` and `shellcheck` first (nothing to approve) and
add `api-test` with its sidecar in a second PR. Then repoint branch protection at the single
**`Ankra pipeline`** check and delete `.github/workflows/`.

## B. An application: test → build → scan → gate → publish

This is the shape `ankra application add` generates into the setup PR (and records as the definition
of record before the PR opens). Shown expanded so you can read it against your own.

```yaml
apiVersion: ankra.io/v1
kind: Pipeline
metadata:
  name: "orders-api"
on:
  push:
    branches: ["main"]
  pull_request:
    branches: ["main"]
    fork_policy: "read_only"
  tag:
    patterns: ["v*"]
  manual: {}
concurrency:
  group: "orders-api-${{ ankra.ref }}"
  cancel_in_progress: true
workspace:
  size: "10Gi"
  access: "rwo"
defaults:
  image: "golang:1.26"
  timeout: "30m"
  network: "egress-https"
permissions:
  contents: "read"
services:
  postgres:
    image: "postgres:16"
    env: { POSTGRES_PASSWORD: "test", POSTGRES_DB: "orders" }
    ports: ["5432"]
    ready: { tcp: "5432" }
stages:
  - name: "checkout"
    kind: "checkout"
  - name: "test"
    kind: "run"
    needs: ["checkout"]
    image: "golang:${{ matrix.go }}"      # one leg per matrix value
    run: |
      go test ./... -race -json > test-results.json
    services: ["postgres"]
    network: "services"
    env:
      DATABASE_URL: "postgres://postgres:test@postgres:5432/orders?sslmode=disable"
    test_results:
      - format: "go-test"
        path: "test-results.json"
    matrix:
      go: ["1.25", "1.26"]
  - name: "build"
    kind: "build"
    needs: ["test"]
    build:
      dockerfile: "Dockerfile"
      context: "."
      build_args: ["VERSION"]           # names resolved from variables
      provenance: true
      sbom: true
      # never pin platforms: builds run natively; a fabricated linux/amd64 fails on an arm64 cluster
  - name: "scan"
    kind: "scan"
    needs: ["build"]
    with:
      image_gate: "app"                 # tightens the organisation gate; can never loosen it
      semgrep_config: "p/default p/docker p/golang"
    scan:
      scanners: ["semgrep", "checkov", "trivy"]
      fail_on:
        semgrep: "error"
        checkov: "none"
        trivy: "app"
  - name: "gate"
    kind: "gate"
    needs: ["scan"]
    gate:
      require_stages: ["scan"]          # the organisation policy judges the persisted findings
  - name: "publish"
    kind: "publish"
    needs: ["gate"]
    when:
      events: ["push"]                  # a pull request builds, scans and reports; it never publishes
    with:
      image: "harbor.ankra.cloud/<project>/orders-api"   # recorded, not read: the target is the application's registry
```

`registry_auth` and `source_token` are reserved bindings Ankra adds from the application's push
robot — never declare them. `publish` copies the exact digest the gate judged into the application's
registry as `sha-<7>`; push-to-deploy rolls that tag out (`ankra-ship` §7). A `rerun` does not
publish (`when.events` excludes it) — push a commit.

## C. Manual GitOps bump — only for a repository Ankra cannot connect

Use this **only** when the repository lives on a provider the organisation has no Git credential or
App for (a self-hosted SCM Ankra cannot reach). For every GitHub, GitLab or Bitbucket repository
Ankra can connect, §A/§B on Ankra Pipelines is the answer and a hand-written workflow is a
regression — say in the PR description why this pattern was the only option.

The pattern that stays valid wherever CI runs: build, push an **immutable tag** (commit SHA or
semver, never `latest`), commit the new tag into the GitOps repository, and let the **Ankra engine**
sync it. CI never runs `kubectl`/`helm` against the cluster and holds no cluster credential — only a
registry push credential and a write token scoped to the GitOps repository. Rollback is
`git revert`; promotion is committing the same tag to the next environment's values path in a
separate, reviewed step.

Assumptions: the image tag lives in the GitOps repo at `stacks/my-app/values/image.yaml` under
`image.tag`; `GITOPS_TOKEN` writes only that repo; `REGISTRY_TOKEN` pushes only this image.

GitHub Actions (`.github/workflows/deploy.yml`):

```yaml
name: build-and-deploy
on:
  push:
    branches: [main]
permissions:
  contents: read
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: docker/login-action@v3
        with:
          registry: registry.example.com
          username: ${{ secrets.REGISTRY_USER }}
          password: ${{ secrets.REGISTRY_TOKEN }}
      - name: Build and push (immutable tag)
        env:
          IMAGE: registry.example.com/my-app
        run: |
          TAG="${GITHUB_SHA::12}"
          docker build -t "$IMAGE:$TAG" .
          docker push "$IMAGE:$TAG"
          echo "TAG=$TAG" >> "$GITHUB_ENV"
      - name: Bump tag in GitOps repo
        env:
          GITOPS_TOKEN: ${{ secrets.GITOPS_TOKEN }}
        run: |
          git clone "https://x-access-token:${GITOPS_TOKEN}@github.com/my-org/gitops-repo.git" gitops
          cd gitops
          yq -i ".image.tag = \"${TAG}\"" stacks/my-app/values/image.yaml
          git config user.name "ci-bot" && git config user.email "ci-bot@example.com"
          git commit -am "deploy my-app ${TAG}" && git push
```

GitLab CI (`.gitlab-ci.yml`):

```yaml
stages: [build, deploy]
variables:
  IMAGE: registry.example.com/my-app
build:
  stage: build
  image: docker:27
  services: [docker:27-dind]
  script:
    - export TAG="${CI_COMMIT_SHORT_SHA}"
    - echo "$REGISTRY_TOKEN" | docker login registry.example.com -u "$REGISTRY_USER" --password-stdin
    - docker build -t "$IMAGE:$TAG" . && docker push "$IMAGE:$TAG"
    - echo "TAG=$TAG" > build.env
  artifacts:
    reports:
      dotenv: build.env
deploy:
  stage: deploy
  image: alpine:3.20
  script:
    - apk add --no-cache git yq
    - git clone "https://gitlab-ci-token:${GITOPS_TOKEN}@gitlab.com/my-org/gitops-repo.git" gitops
    - cd gitops && yq -i ".image.tag = \"${TAG}\"" stacks/my-app/values/image.yaml
    - git config user.name "ci-bot" && git config user.email "ci-bot@example.com"
    - git commit -am "deploy my-app ${TAG}" && git push
```

Optional verification afterwards with a scoped token: install the CLI on the runner and run
`ankra cluster operations list --cluster prod` with `ANKRA_API_TOKEN` set. Encrypt any secret that
lands in the GitOps repo with SOPS (`ankra-sops-secrets`).

## D. Field reference (`enginekit/pipelinespec`)

Top level: `apiVersion: ankra.io/v1` · `kind: Pipeline` · `metadata.name` (required) · `on` ·
`concurrency {group, cancel_in_progress}` · `workspace {size, access: rwo|rwx}` ·
`defaults {image, timeout, resources, cache, working_directory, network, env, secrets}` ·
`permissions {<scope>: none|read|write}` · `secrets [{name, from, key}]` ·
`credentials [{name, from: cloud|git|registry|object_storage, as}]` ·
`services {<name>: {image, env, ports, ready {tcp|http}}}` · `stages` · `on_failure` · `finally`
(accepted, not yet honoured) · `environments` (pipeline half of a binding; deploy kinds do not
execute yet).

Triggers: `push {branches, paths}` · `pull_request {branches, paths, fork_policy: none|read_only|trusted}`
· `tag {patterns}` (glob) · `schedule [{cron, branch, stages}]` · `manual {inputs [{name, type:
string|boolean|number|choice, default, enum, required, description}]}` · `webhook {}`.

Stage (common): `name` · `kind` · `image` · `run` · `with` · `needs` · `if` · `when {branches,
paths, events}` · `matrix {<axis>: [...], include, exclude}` (max 64 legs) · `services` · `env` ·
`secrets` · `cache [{key, paths, size, restore_keys}]` · `artifacts [{name, paths,
retention_days}]` · `test_results [{format: junit|go-test|pytest|playwright, path}]` · `outputs`
· `timeout` · `resources {cpu, memory, gpu}` · `runs_on {cluster, node_selector, tolerations,
runtime_class, arch}` · `network: none|egress-https|services` · `shm_size` · `working_directory`
· `allow_failure` · `continue_on_error` · `retry {max_attempts ≤ 5, backoff: fixed|exponential,
on: [failure, timeout, infra_error]}`.

Kind blocks: `build {dockerfile, context, target, platforms, build_args, provenance, sbom,
cache_to}` · `scan {scanners, fail_on}` · `gate {approval_roles, require_stages}` · `preview`,
`verify`, `agent`, `approval`, `webhook`, `external` (validate, not yet executed).

Secret sources: `app_env_secret` (one key of the linked application's env-secret, or all) ·
`org_variable` (organisation or cluster variable) · `registry` · `credential`.

Network defaults by kind: `checkout`, `build`, `scan` → `egress-https`; everything else → `none`.
Resolution order: stage `network` → `defaults.network` → kind default.

Protected sections (the hash a human approves): `permissions`, `secrets`, `credentials`,
`on.pull_request.fork_policy`, `defaults.network` above `egress-https`, the `services` catalog, and
per stage: `network` above `egress-https`, `runs_on.*`, `resources.*`, `timeout`, `cache`, `with`,
`gate.approval_roles`/`require_stages`, `approval.roles`/`min_approvals`, `agent.mode: agent` and
its tool profile, `kind: external`. Everything else is logic and runs as the head says.

Expression roots in `${{ … }}`: `ankra.ref`, `ankra.sha`, `ankra.repository.{provider,owner,name}`,
`ankra.run.number`, `matrix.<axis>`, `inputs.<name>`, `secrets.<name>`, `vars.<NAME>`,
`needs.<stage>.outputs.<key>`; functions `hashFiles(...)`, `contains(...)`.
