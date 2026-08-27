---
name: ankra-applications
description: Take source code from a Git repository to a running deployment on one or many Ankra clusters - registering an application, the generated Dockerfile/chart/build workflow, the image registry it publishes to, environment secrets, deployments, auto-deploy, PR demos, and publishing the result as a catalogue add-on. Use when the user wants to deploy their own code with Ankra, mentions `ankra application`, connects a repository, asks how to get a service live, or wants the same service on several clusters.
---

# Ankra Applications

An **Application** is Ankra's link between a Git repository of source code and the clusters that
run it. Ankra reads the repository, generates the container build and the Kubernetes packaging,
publishes the image to a registry, and deploys it — so a service goes from source to production
without anyone hand-writing a Deployment.

Use this skill for *your own code*. For third-party software (a Helm chart someone else
maintains) use `ankra-stacks-addons`; the two meet when an application publishes its manifests as
a catalogue add-on.

## The lifecycle

```
repository → add → setup PR (Dockerfile, chart, build workflow) → build & push image
          → env-secrets → deploy to cluster → verify → auto-deploy / promote → many clusters
```

Every step has a CLI command; all of them are also visible in the portal's Applications tab.

## 1. Register the application

```bash
ankra application add .                      # detect repo, credential and branch from the checkout
ankra application add ./services/payments --name payments
ankra application add . --credential github-acme --branch main
```

`add` reads the local Git checkout: it detects the GitHub repository from the remote (`--remote`,
default `origin`), uses the remote's default branch, and picks the GitHub credential when the
choice is unambiguous.

**Declare a registry you already operate in the same command** — not afterwards:

```bash
ankra application add . \
  --registry-url oci://artifact.example.com/commerce \
  --registry-credential example-harbor \
  --registry-pull-secret commerce-registry
```

The setup job generates the build workflow *from the declaration the application is created
with*. A registry added later leaves a workflow that logs in with the wrong one, and you will be
debugging a push failure that has nothing to do with the code. With no declaration the
application publishes into the organisation's own Ankra registry project, which is the right
default when you do not already run a registry.

Related flags: `--registry-api-url`, `--registry-username-secret`, `--registry-password-secret`
(the repository Actions secrets the workflow logs in with) and `--registry-manage-actions-secrets`
to let Ankra write the named credential into those secrets for you. See
[reference.md](reference.md) for the registry matrix (Harbor, ECR, GAR, ACR, GHCR, Docker Hub).

## 2. Review what Ankra generated

Ankra opens a **setup pull request** carrying the Dockerfile, the Helm chart, and the build
workflow. Read it — this is the contract for everything that follows.

```bash
ankra application list
ankra application get <application-id>
ankra application branches <application-id>
ankra application branch-files <application-id>      # what is tracked on the setup branch
ankra application files <application-id> \
  --file Dockerfile=./Dockerfile --message "Pin the base image"
ankra application retry <application-id>             # re-run a failed setup
```

Things worth checking before merging: the base image and its tag, the exposed port, the health
probe paths, resource requests, and that nothing secret was baked into the image.

## 3. Supply configuration and secrets

The generated manifests declare which environment values they need. Fill them in — a missing one
is the single most common reason a first deploy crash-loops.

```bash
ankra application env-secrets list <application-id>          # keys and their state
printf '%s' "$DATABASE_URL" | ankra application env-secrets set <application-id> DATABASE_URL
ankra application env-secrets set <application-id> API_TOKEN  # prompts without echo
ankra application env-secrets apply <application-id>          # seal into the deployments and roll
ankra application env-secrets delete <application-id> OLD_KEY
```

**Never pass `--value` on the command line.** It lands in shell history and, on a shared host, in
the process table. Pipe on stdin or let it prompt.

Storing a value does nothing until `env-secrets apply` seals it into the running deployments.
Non-secret configuration (base URLs, model names, feature toggles) belongs in cluster or
organisation variables instead — see `ankra-app-integrations`.

## 4. Deploy to a cluster

```bash
ankra cluster list                                    # find the target cluster id
ankra application deploy <application-id> --cluster <cluster-id> --namespace prod
ankra application deploy <application-id> --cluster <cluster-id> --mode high_availability --set replicas=3
ankra application deployments <application-id>        # where it is running
ankra application installations <application-id>      # installation intents and their state
ankra application jobs <application-id>               # the platform jobs behind those intents
```

`--mode quick` is the single-replica default; `--mode high_availability` asks for the resilient
shape. `--set key=value` binds the deploy inputs the chart declares.

Then verify, before you call it done:

```bash
ankra cluster operations list --cluster <cluster>     # did the platform execution succeed
ankra cluster get pods -n prod --cluster <cluster>
ankra cluster logs -l app=<name> -n prod --follow=false --tail 100
```

## 5. Continuous delivery

```bash
ankra application auto-deploy get <application-id>
ankra application auto-deploy set <application-id> --enabled        # or --enabled=false
ankra application settings get                        # org-wide CI settings
ankra application settings set --ci-runner-label self-hosted
ankra application workflow-runs <application-id>
ankra application workflow-run-jobs <application-id> <run-id>
ankra application rerun-workflow <application-id> <run-id>
```

With auto-deploy on, a build Ankra observes on the tracked branch rolls itself out unattended.
With it off, a push still builds and waits for an explicit `ankra application deploy`. Choose
deliberately: auto-deploy is right for dev and staging, and for production only when the branch
is protected and the pipeline gates on tests.

The organisation's CI runner label decides which GitHub Actions runner the generated pipelines
request — change it when GitHub-hosted runners are unavailable to you.

## 5b. Building without the repository's CI

Everything above routes the build through the workflow Ankra generated into the repository, which
means the first image cannot exist until a human merges the setup PR. Ankra can also build the
image itself, on its own builders:

```bash
ankra application build start <application-id> --commit <full-sha> --ref main
ankra application build start <application-id> --commit <full-sha> --wait   # follow it; non-zero if it failed
ankra application build list <application-id>
ankra application build get <application-id> <build-id>
ankra application build request <application-id> <request-id>   # a queued ask, before it has a build
```

Ankra clones the commit, resolves a recipe (the repository's own Dockerfile, else a generated one,
else buildpacks), builds it and pushes the image — no Actions minutes, no runners to operate, and
no registry credentials in the repository.

Use it when the repository's CI cannot do the job: a private repository on a plan whose Actions
never run, a first image needed before anyone merges a PR, an unattended caller with no browser,
or a build the repository's own pipeline is failing to produce.

Two things to know. `--commit` takes a **full** 40- or 64-character sha — the queue deduplicates on
the string it is given, so an abbreviation would be a second key for the same commit. And a request
is not yet a build: `start` answers with a request id, and the build row appears when the scheduler
claims it, which is what `build request` reads and what `--wait` polls across.

A failed build carries an `error_class` that says whose failure it is. `build_failed` is the
repository's — read `error_message`. `clone_auth` and `recipe_missing` are the application's
configuration. `push_failed`, `timeout` and `capacity` are Ankra's, and Ankra can already see them.

The routes answer **404 while the `platform_builds` flag is off**, which is the default — the lane
runs untrusted code on a builder pool that has to exist first. Ask Ankra to enable it for your
organisation. A 404 on an application you can otherwise read means the flag, not the application.

## 6. Security scanning

```bash
ankra application upgrade-workflow <application-id>   # add scanning steps to the build workflow
ankra application code-security <application-id>      # source findings
ankra application container-security <application-id> # image CVEs
ankra application pull-request-reviews <application-id>
```

Treat an application whose build workflow has no scanning step as unfinished. See
`ankra-security` for how these findings fit the wider posture review.

## 7. Preview a branch before it merges

```bash
ankra application demo build <application-id> --branch <branch>   # is there a demo-ready image
ankra application demo deploy <application-id> --branch <branch> --ttl-hours 8
ankra application demo deploy <application-id> --pr-number 42
ankra application demo list <application-id>
ankra application demo detail <application-id> <demo-id>
ankra application demo logs <application-id> <demo-id>
ankra application demo stop <application-id> <demo-id>
ankra application demo fix <application-id> <demo-id>             # AI pre-setup mission on failure
ankra application demo fix-build <application-id> --branch <branch>
ankra application demo config <application-id>                    # saved demo defaults
```

Demos are ephemeral workspaces on the organisation's AI/staging cluster with their own TTL, and
they are reaped automatically. They are the reviewable artefact for a pull request — a public
preview URL, not a port-forward. Configure the staging cluster in **Organisation settings → AI →
Environment** (`ankra-ai-gateway`).

## 8. One service, many clusters

Do **not** repeat `application deploy` with hand-copied values per cluster. Two supported shapes:

**Publish the manifests as a catalogue add-on** — the application becomes installable software
other clusters pick up:

```bash
ankra application publish-addon <application-id> --version 1.2.0 \
  --display-name "Payments" --category backend --changelog "TLS defaults"
ankra application published-addon <application-id>
ankra application chart-versions <application-id>
ankra application manifest-addon <application-id>            # inspect, install, withdraw
```

**Or capture the deployed stack as a stack profile** — the right choice when each cluster needs
different values (domains, sizes, replica counts). Every per-cluster difference becomes a
parameter instead of a fork. See `ankra-stack-profiles`.

Either way the artefact is built once and promoted, never rebuilt per environment.

## 9. Ankra's own analysis of the application

```bash
ankra application platform <application-id> --cluster <cluster-id>   # operators already present
ankra application publish-readiness <application-id>
ankra application ai-config <application-id>                          # the AI lane configuration
ankra application reconcile <application-id>                          # request a refresh
```

`platform` detects which operators (ingress, cert-manager, a database operator) a target cluster
already runs, so the generated manifests reuse them rather than duplicating them.

## Rules

- **Declare the registry at `add` time.** A late `--registry-url` leaves a wrong build workflow.
- **Immutable image tags.** Commit SHA or semver; never `latest`. This is what makes promotion and
  rollback meaningful.
- **Secrets by stdin or prompt**, never `--value`, and never printed back into a transcript.
- **`env-secrets set` then `env-secrets apply`.** Setting alone does not reach a running workload.
- **Verify before promoting.** Operations, pods, logs — in that order. A green `deploy` command is
  not a working service.
- **Nothing is finished until it is reproducible.** If a deploy only works because someone edited a
  live resource, it is not deployed.
- **`ankra application delete` is destructive**; confirm the application id and what it is running
  before you run it.

## Related skills

- `ankra-cicd` — the pipeline shape and the GitOps bump that drives deploys.
- `ankra-app-integrations` — wiring the application to LiteLLM, Harbor, a database, an internal API.
- `ankra-stack-profiles` — one definition, many clusters, per-cluster parameters.
- `ankra-troubleshooting` — when the first deploy does not come up.
- `ankra-security` — tokens, scanning findings, and least-privilege registry credentials.
