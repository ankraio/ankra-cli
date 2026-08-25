---
name: ankra-stack-profiles
description: Turn a working cluster stack into a reusable, parameterised stack profile and launch it across a fleet - builder drafts, parameters and their annotations, option sets that move several inputs together, secret parameters, publishing versions, applying to a cluster as a reviewable draft, diffing versions, sharing with other organisations, and exporting as ClusterInfrastructureAsCode YAML. Use when the user mentions stack profiles, `ankra stack-profiles`, a profile catalogue, reusing a stack across clusters, or wants one definition to serve many environments.
---

# Ankra Stack Profiles

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
then capture it.

> **Opening a draft on a published profile drops `encrypted_paths`.** Re-declare them before
> publishing, or the next launch deploys a secret Ankra will not decrypt.

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
ankra stack-profiles set-current-version payments --version 3
```

`save-version` re-snapshots a source stack and makes the result the current version;
`--include-addon-configurations` (default true) carries the source add-ons' values with it.

## 5. Launch it on a cluster

```bash
ankra stack-profiles list
ankra stack-profiles get payments                     # versions and the parameters they expect
ankra stack-profiles version payments --version 3     # one version's contents

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

## 6. Operate the fleet

```bash
ankra stack-profiles deployments payments             # every stack deployed from this profile
ankra stack-profiles diff payments --from 2 --to 3    # what changed between versions
ankra stack-profiles update payments --description ... --category ...
ankra stack-profiles logo set payments ./logo.svg      # also: logo get / logo clear
ankra stack-profiles delete payments
```

`deployments` is the fleet view: which clusters are on which version. Upgrade by publishing a
version and re-applying per cluster — one cluster, verified, then the rest.

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
- **Secrets are secret parameters**, bound with `--set-file`/`--set-env`, and their
  `encrypted_paths` are re-declared after any draft reopen.
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
- `ankra-sops-secrets` — `encrypted_paths` and what happens when they are lost.
- `ankra-platform-principles` — promotion, pinning, and variables over hardcoding.
