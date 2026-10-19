---
name: ankra-stack-profiles
description: Turn a working cluster stack into a reusable, parameterised stack profile and launch it across a fleet - builder drafts, parameters and their annotations, option sets that move several inputs together, secret parameters, publishing versions, applying to a cluster as a reviewable draft, rolling a version out in place across the fleet, adopting stacks already running, reading a version's decoded contents, deprecating versions, diffing versions, sharing with other organisations, and exporting as ClusterInfrastructureAsCode YAML. Use when the user mentions stack profiles, `ankra stack-profiles`, a profile catalogue, reusing a stack across clusters, or wants one definition to serve many environments.
---

# Ankra Stack Profiles

## Recommended path

- Golden path 2 (GitOps as the source of truth) carried across a fleet; see
  `ankra-platform-principles`. Build and verify the stack on one real cluster first
  (`ankra-stacks-addons`), with pinned charts and secrets sealed (`ankra-sops-secrets`).
- Capture it, turn every cluster-specific value into an annotated parameter, prove it with a
  `demo launch`, publish, then `apply` per cluster as a reviewable draft.
- Upgrade with `rollout` (in place, one cluster first, then `--all --outdated`), never by applying
  the profile a second time; withdraw a bad version with `deprecate`.
- Anti-patterns: copy-pasting a stack to a second cluster, a cluster-specific literal left in the
  profile, a secret passed with `--set`, and editing an exported IaC file and re-importing it.

A **stack profile** is a published, versioned, parameterised snapshot of a stack. It is how one
definition serves many clusters: the parts that differ per cluster become **parameters** the
launcher fills in, and the rest is fixed and pinned.

Reach for a profile the moment a stack would otherwise be copy-pasted to a second cluster and
edited. The copy is the bug: two stacks that started identical drift silently, and nothing tells
you which one is right.

## Where a profile comes from

```
deployed stack → builder draft → parameters + annotations + options → publish → version
                                                                          ↓
                                                     apply to cluster (draft → deploy)
```

Everything below is also available in the portal's stack builder; the CLI is what makes it
scriptable and reviewable.

## 1. Open a builder draft

```bash
ankra stack-profiles drafts create --name payments \
  --source-cluster prod --source-stack payments      # seed from a deployed stack
ankra stack-profiles drafts create --profile payments  # edit an existing profile (next version)
ankra stack-profiles drafts create --name blank-profile
ankra stack-profiles drafts list
ankra stack-profiles drafts get <draft-id>            # parameters and annotations
ankra stack-profiles drafts delete <draft-id>
```

Seeding from a deployed stack is the normal route: build it once on a real cluster, verify it,
then capture it. `ankra stack-profiles create --name payments --stack payments --cluster prod`
captures and publishes version 1 in one step, when there is nothing to annotate.

To change a published profile, always open a draft on it (`drafts create --profile`): it keeps the
input annotations and publishes the next version of the same profile. Exporting the IaC, editing it
and re-importing drops the annotations and creates a separate profile.

> **A profile never stores secret values** - capture redacts them. Bind secrets at launch, and
> before publishing a profile built from a sealed stack, check each sealed resource still declares
> its `encrypted_paths` (`stack-profiles contents <profile> <version>` after publishing).

## 2. Turn cluster-specific values into parameters

Every domain, size, replica count, storage class, node selector and credential that came from the
source cluster must become a parameter. Anything you leave as a literal is the source cluster's
value silently baked into everyone else's launch.

```bash
ankra stack-profiles drafts annotate <draft-id> --parameter domain \
  --title "Public domain" \
  --description "The host the ingress serves, e.g. payments.example.com" \
  --required
ankra stack-profiles drafts annotate <draft-id> --parameter replicas \
  --type number --default 2
ankra stack-profiles drafts annotate <draft-id> --parameter storage_class \
  --type enum --enum "gp3,standard,local-path" --default gp3
```

The `--description` is the guidance the launch form shows under the field. Write it for someone
who has never seen this stack — that is the whole point of publishing it.

`--add` declares an input the draft does not have, which you need for a choice input that no
manifest references (see below).

## 3. Group the choices that move together

Where one decision drives several values, declare it once as an **option set**. A single "Model
size" input can move the model id, the context length and the volume size together, so whoever
launches the profile picks one thing instead of keeping four consistent.

```bash
ankra stack-profiles drafts annotate <draft-id> --parameter model_size --add --type enum
ankra stack-profiles drafts options set <draft-id> --parameter model_size \
  --value 8b --title "8B (single GPU)" \
  --set model_id=meta-llama/Llama-3.1-8B-Instruct --set context_length=8192 --set volume_size=40Gi
ankra stack-profiles drafts options set <draft-id> --parameter model_size \
  --value 32b --title "32B (two GPUs)" \
  --set model_id=Qwen/Qwen2.5-32B-Instruct --set context_length=32768 --set volume_size=120Gi
ankra stack-profiles drafts options remove <draft-id> --parameter model_size --value 8b
```

Resolution order at apply time is fixed: **an explicit `--set` wins, then what the selected choice
sets, then the input's own default.** A choice may set any non-secret input that does not offer
choices of its own.

## 4. Validate and publish

```bash
ankra stack-profiles drafts validate <draft-id>       # publish checks, without publishing
ankra stack-profiles drafts publish <draft-id> --changelog "Initial version"
ankra stack-profiles drafts rebase <draft-id>         # move a stale draft onto the latest version
```

Validate first. It catches the omissions that only bite the person launching it: an unannotated
required input, a secret parameter with a default, a dangling reference.

Later versions from a deployed stack, without reopening the builder:

```bash
ankra stack-profiles save-version payments --stack payments --cluster staging \
  --changelog "Bump CloudNativePG to 1.30" --channel stable
ankra stack-profiles set-current-version payments v3
```

`save-version` re-snapshots a source stack and makes the result the current version;
`--include-addon-configurations` (default true) carries the source add-ons' values with it.

## 5. Launch it on a cluster

```bash
ankra stack-profiles list
ankra stack-profiles get payments                     # versions and the parameters they expect
ankra stack-profiles version payments v3              # one version's metadata
ankra stack-profiles contents payments 3              # the decoded manifests and add-on values
ankra stack-profiles contents payments 3 --all        # one YAML stream, to audit or grep

ankra stack-profiles apply payments --cluster eu-prod --dry-run
ankra stack-profiles apply payments --cluster eu-prod \
  --set domain=payments.eu.example.com --set replicas=3 \
  --set-file tls_key=./tls.key --set-env api_token=PAYMENTS_TOKEN \
  --stack-name payments-eu
ankra stack-profiles apply payments --cluster eu-prod --deploy
```

- **`--dry-run` shows the value every input would deploy with** — your `--set`, the selected
  choice, or the default. Run it before every production apply; it is the only place the
  three-way resolution is visible.
- **Without `--deploy` you get a reviewable stack draft**, not a deployment. That is the default
  and it is the right one: review in the stack builder, then deploy.
- **Secret parameters take `--set-file` or `--set-env`**, never `--set`. A `--set` secret lands in
  shell history and the process table.
- **Backup protection travels with the profile.** A profile captured from a protected stack keeps
  its policy; the vault is resolved by name in the target organisation (`ankra_protection_vault`
  binds it per cluster). With no matching ready vault the stack is created unprotected and a
  `--deploy` stops at the draft - see `ankra-backups`.

## 6. Operate the fleet

```bash
ankra stack-profiles deployments payments             # every stack deployed from this profile
ankra stack-profiles deployments payments --outdated  # the ones behind the current version
ankra stack-profiles diff payments --from 2 --to 3    # what changed between versions

ankra stack-profiles rollout payments --cluster staging --version 3 --dry-run
ankra stack-profiles rollout payments --cluster staging --version 3
ankra stack-profiles rollout payments --all --outdated          # then the rest of the fleet
ankra cluster operations list --cluster staging                 # the deploys run in the background

ankra stack-profiles adopt payments --stack payments --cluster prod --version 2
ankra stack-profiles update payments --description ... --category ...
ankra stack-profiles logo set payments ./logo.svg      # also: logo get / logo clear
ankra stack-profiles delete payments
```

`deployments` is the fleet view: which clusters are on which version.

- **`rollout` upgrades in place.** It replaces the contents of the stack each deployment already
  runs, so Kubernetes performs a rolling update and nothing is created twice, and records the new
  version. Non-secret inputs are carried forward; bind new or secret inputs with `--set`,
  `--set-file`, `--set-env`. Pick targets with `--cluster` (repeatable) or `--all`. Roll one
  cluster, verify it, then the rest. Applying the profile again instead creates a second stack
  beside the first.
- **`adopt` tracks a stack the profile was captured from** (or any matching stack already running).
  Capture does not register the source as a deployment, so `rollout` cannot reach it until adopted.
  Adopt writes only the record and reports drift; a later `rollout` removes anything listed as
  "only on the cluster", so read that report first.

### Withdraw a version that must not be deployed

```bash
ankra stack-profiles deprecate payments 3 --reason cve \
  --note "Fixed in v4." --reference CVE-2026-12345      # reasons: incompatibility | critical-bug | cve
ankra stack-profiles undeprecate payments 3
```

A deprecated version stays in the history but nothing deploys it: `apply --version 3`, `rollout`,
a demo launch and an in-place update all answer `409` with the reason and the note, so the person
who tried learns what to do instead. `deployments` marks the stacks still on it as `deprecated`.
The current version cannot be deprecated — `set-current-version` another one first — and a
deprecated version cannot be made current.

## 7. Share and move profiles between organisations

```bash
ankra stack-profiles share add payments <organisation-slug>
ankra stack-profiles share list payments
ankra stack-profiles share remove payments <organisation-slug>

ankra stack-profiles export-iac payments --version 3 -o payments.yaml
ankra stack-profiles import payments.yaml --name payments --category general

ankra stack-profiles suggestions list payments        # suggestions from other organisations
ankra stack-profiles suggestions get payments <suggestion-id>
ankra stack-profiles suggestions approve payments <suggestion-id>   # publishes it as the next version
ankra stack-profiles suggestions reject payments <suggestion-id> --note "..."
ankra stack-profiles drafts submit-suggestion <draft-id>            # propose to another org's profile
```

Shared organisations can view and deploy every version but cannot change the profile. Keep the
exported `ClusterInfrastructureAsCode` YAML in Git so the definition has a home outside the
platform.

## 8. Throwaway launches

```bash
ankra stack-profiles demo launch payments      # on the org's staging cluster, no cluster of your own
ankra stack-profiles demo list payments
ankra stack-profiles demo detail payments <demo-id>
ankra stack-profiles demo logs payments <demo-id>
ankra stack-profiles demo stop payments <demo-id>
```

The fastest way to prove a profile launches clean from nothing — run it before publishing a
version anyone else will depend on.

## Rules

- **No cluster-specific literals.** If it came from the source cluster and differs elsewhere, it is
  a parameter.
- **Annotate every parameter.** An input with no description is a support ticket.
- **Secrets are secret parameters**, bound with `--set-file`/`--set-env`, and every sealed
  resource keeps its `encrypted_paths`.
- **Upgrade with `rollout`, not a second apply.** Adopt stacks the profile was captured from first.
- **`--dry-run` before a production apply.** It is the only view of the resolution order.
- **Draft, review, deploy.** `--deploy` is for environments you are willing to change unreviewed.
- **Pin chart and image versions inside the profile.** A profile with a floating tag makes every
  launch a different stack.
- **Version, do not fork.** Publish a new version rather than a second profile that differs
  slightly.
- **One concern per profile**, matching the one-concern-per-stack rule in `ankra-stacks-addons`.

## Related skills

- `ankra-stacks-addons` — the stack a profile is captured from, and dependency ordering.
- `ankra-applications` — publishing your own code as an add-on rather than a profile.
- `ankra-import-cluster` — the ImportCluster document and `ankra cluster draft`.
- `ankra-sops-secrets` — `encrypted_paths` and sealing the stack before capture.
- `ankra-backups` — the vault a protected profile launch writes restore points to.
- `ankra-platform-principles` — promotion, pinning, and variables over hardcoding.
