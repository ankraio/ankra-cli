---
name: ankra-cli
description: Drive the Ankra CLI end to end - log in, switch organisation and cluster, apply cluster/stack YAML, manage applications and stack profiles, inspect Kubernetes resources, logs, events and metrics, triage operations, work the AI board, and script it all with `-o json` and stable exit codes. Use when the user mentions the `ankra` CLI, `ankra login`, `ankra cluster`, `ankra application`, `ankra stack-profiles`, `ankra tickets`, applying an ImportCluster, or managing an Ankra-managed cluster from the terminal.
---

# Ankra CLI

`ankra` is the terminal client for the Ankra Platform. Everything the portal does, it does - and
unlike the portal it is scriptable, which is why agent work should go through it.

## Recommended path

The CLI is the instrument of every golden path in `ankra-platform-principles`: it is how an
assistant touches a cluster, reads included, instead of `kubectl` or `helm`. Orient first
(`ankra org current`, `ankra cluster info`), then hand off to the skill that owns the job via the
command map below - `ankra-ship` for code, `ankra-cicd` for CI, `ankra-gitops` for cluster state.
Declarative change goes through Git and `ankra cluster apply`; the imperative commands here are for
reads and operational fixes. If the CLI has no command for something, report the gap and stop.

## Install and verify

```bash
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
ankra --version
ankra completion install            # once per machine
ankra upgrade                       # later, to update
ankra config beta enable            # opt in to pre-release builds (disable to leave)
```

Docs: https://docs.ankra.ai

## Orient before you act

```bash
ankra login                 # browser SSO; stores credentials in ~/.ankra.yaml
ankra org list
ankra org switch <slug|name|id>
ankra org current
ankra project list                  # projects: the team-sized scope beneath an organisation
ankra project switch <slug|name|id>
ankra project current               # says whether a flag, a saved selection or the platform decides
ankra cluster list
ankra cluster select <name>         # persisted; omit the name for a picker
ankra cluster info                  # confirm what you are about to change
```

Most `ankra cluster ...` subcommands act on the **selected** cluster. Override per command with
`--cluster <name|id>`, the organisation with the global `--org <name|id>`, and the project with the
global `--project <slug|name|id>` (or `ANKRA_PROJECT`). In a shared script, never rely on the
selection - pass all three explicitly. While an organisation has projects off, `ankra project`
exits 3 saying so and everything else works as before.

## The command map

| Area | Entry point | Skill |
|------|-------------|-------|
| Clusters, stacks, addons, manifests, variables | `ankra cluster apply\|draft\|stacks\|addons\|manifests\|variables ...` | `ankra-import-cluster`, `ankra-stacks-addons` |
| GitOps sync state and merge conflicts | `ankra cluster gitops status\|conflicts list\|resolve` | `ankra-gitops` |
| Shipping code end to end | `ankra application ship\|add\|deploy\|deployments\|auto-deploy\|env-secrets\|demo ...` | `ankra-ship`, `ankra-applications` |
| Ankra Pipelines (CI for any repository) | `ankra pipeline list\|get\|logs\|run\|rerun\|cancel\|validate\|tests\|findings\|artifacts\|time-saved`, `ankra pipeline repositories\|definitions\|schedules\|train ...`, `ankra org ci-settings`, `ankra cluster agent ci` | `ankra-cicd` |
| AI review of pull requests | `ankra org ai-review repos\|related-repos ...` | `ankra-ai-gateway` |
| Managed services, image registry | `ankra services ...`, `ankra registry projects\|robots\|storage ...` | `ankra-app-integrations`, `ankra-applications` |
| Reusable parameterised stacks | `ankra stack-profiles ...` | `ankra-stack-profiles` |
| Cloud clusters Ankra provisions | `ankra cluster hetzner\|ovh\|upcloud\|digitalocean\|scaleway\|ankracloud\|aws\|proxmox\|morpheus ...`, `ankra cluster scale\|upgrade\|node-group\|power-schedules\|mesh ...` | `ankra-cloud-clusters` |
| Provider-managed Kubernetes | `ankra cluster managed ...` | `ankra-managed-kubernetes` |
| A playground cluster to try things | `ankra cluster playground create\|status\|destroy` | `ankra-getting-started` |
| Domains and DNS | `ankra cluster domain`, `ankra org domain\|dns\|custom-dns-zones\|cloudflare ...`, `ankra cluster custom-dns-zones` | `ankra-domains-dns` |
| Helm chart sources | `ankra helm registries\|credentials ...`, `ankra charts` | `ankra-helm-registries` |
| Secrets in Git | `ankra cluster encrypt\|decrypt\|sops-config` | `ankra-sops-secrets` |
| Organisation secrets referenced by sentinel (values never in Git, argv or a model) | `ankra secrets list\|create\|set\|rotate\|delete` | `ankra-sops-secrets`, `ankra-security` |
| Alerts and routing | `ankra alerts destinations\|routes\|ingest-credentials ...` | `ankra-alerts-webhooks` |
| Metrics and logs | `ankra cluster metrics\|top\|logs\|events\|logs-ship` | `ankra-observability`, `ankra-troubleshooting` |
| Debugging inside a cluster | `ankra cluster exec\|terminal\|debug ...` | `ankra-troubleshooting` |
| AI provider, tools, runs, board, memories | `ankra ai ...`, `ankra org mcp-servers ...`, `ankra agents ...`, `ankra tickets ...`, `ankra chat` | `ankra-ai-agents` |
| Tokens, roles, cluster access | `ankra tokens create\|list\|rotate\|revoke\|delete` (`--service` for service accounts, `--project` to pin one to a project), `ankra org members\|roles\|invite`, `ankra cluster access grant\|list\|revoke\|elevate`, `ankra org access-policy get`, `ankra org oidc-trust` | `ankra-security` |
| Projects (scope beneath the organisation) | `ankra project list\|current\|switch\|create\|get\|rename\|delete\|members\|move`, global `--project`, `ankra cost ... --all-projects`, `ankra cost object project`, `ankra org usage --group-by project` | `ankra-security`, `ankra-platform-principles` |
| Credentials | `ankra credentials ...` | `ankra-security` |
| Vulnerabilities, CVEs, compliance | `ankra security overview\|findings\|clusters\|compliance\|dispositions ...` | `ankra-security` |
| Backup vaults, object storage | `ankra backup vaults ...`, `ankra bucket ...` | `ankra-backups` |
| Protecting and restoring a stack's data | `ankra cluster stacks protect\|unprotect\|restore-points\|data ...`, `ankra backup restore-points ...`, `ankra runs ...` | `ankra-backups` |
| Migrating existing deployments | `ankra migrate ...` | `ankra-migrate` |
| Host deploy targets (machines, not clusters) | `ankra targets ...`, `ankra deployments ...` | `ankra-applications` |
| Cloud cost and Ankra usage | `ankra cost ...`, `ankra org usage\|limits` | below |
| Heavy local commands in a workspace pod | `ankra exec`, `ankra workspace up\|status\|down`, `ankra dev install\|mode\|status` | below |
| Sharing a page at a public URL | `ankra canvas publish\|list\|unpublish` | below |
| Agent skills for assistants | `ankra skills install\|list\|clients\|uninstall` | below |
| Support requests | `ankra support create\|list\|get\|comment\|attach\|close` | below |

## Applying configuration

```bash
ankra cluster validate -f cluster.yaml      # server-side validation, changes nothing
ankra cluster apply -f cluster.yaml --dry-run
ankra cluster apply -f cluster.yaml
ankra cluster draft -f cluster.yaml         # stage every stack as a reviewable draft instead
ankra cluster reconcile                     # ask Ankra to re-converge
ankra cluster clone existing.yaml new.yaml --stack monitoring
```

`ankra cluster draft` is the safe default on an environment you are not ready to change: the local
checks run first, then each stack is saved as a resource draft to review, edit and deploy from the
stack builder. **`apply` replaces a stack's contents declaratively** - what is not in the file is
removed from that stack.

## Reading a cluster

```bash
ankra cluster get pods -n <ns>              # also deployments, services, nodes, ingresses,
                                            # configmaps, secrets, statefulsets, daemonsets,
                                            # cronjobs, k8s-jobs, namespaces, storageclasses
ankra cluster get resources <kind> --group <api-group>
ankra cluster describe <kind> <name> -n <ns>
ankra cluster events --for pod/<name> -n <ns> --type Warning
ankra cluster logs <pod> -n <ns> --previous --follow=false
ankra cluster top pods -n <ns>
ankra cluster metrics query '<promql>'
```

`--follow` defaults to true on `logs`; pass `--follow=false` for anything scripted or it will hang.

## Changing a cluster

These act through the cluster's Ankra agent - no kubeconfig - and prompt before changing anything
(`--yes` skips the prompt, `--dry-run` reports without changing). They are for operational fixes;
anything declarative (a new workload, a changed addon) belongs in the stack YAML and `ankra cluster apply`.

```bash
ankra cluster delete pod <pod> -n <ns>                 # any kind: deployment, cm, secret, namespace, node, ...
ankra cluster delete Certificate web-tls -n <ns> --group cert-manager.io --api-version v1
ankra cluster restart deployment <name> -n <ns>        # kubectl rollout restart; also statefulset, daemonset
ankra cluster cordon <node> / uncordon <node>
ankra cluster drain <node> --dry-run                   # prints the plan; without --dry-run cordons + deletes pods
ankra cluster helm get <release> -n <ns> -o values > values.yaml
ankra cluster helm history <release> -n <ns>
ankra cluster helm rollback <release> -n <ns> --revision <n>
ankra cluster helm upgrade <release> -n <ns> --chart <repo/name> --values values.yaml
```

`drain` deletes pods rather than evicting them (the agent has no eviction relay yet), so
PodDisruptionBudgets are not consulted - check them first on a node that carries a quorum member.
A pod owned by a controller comes straight back after `delete pod` - that is the single-replica
restart. `delete` exits 3 when an object did not exist and 1 when the cluster refused, so scripts
can tell the two apart. A Helm release an Ankra addon manages is refused by `rollback`/`upgrade`:
change the addon in the stack instead.

## Cluster access

```bash
ankra cluster access list --cluster prod          # role, namespace, Expires (or standing) and Reason per grant
ankra cluster access grant <email> --cluster prod --role view|edit|admin|cluster-admin \
  [--namespace <ns>] [--expires 30m|4h|7d|<RFC3339>] [--reason "..."]
ankra cluster access revoke <grant-id|email> --cluster prod
ankra cluster access elevate --cluster prod --role edit --expires 4h --reason "..."   # break-glass, for yourself only
ankra org access-policy get                        # creator role, ceiling, elevated lifetime, reason rule, pending proposal
ankra org access-policy set --max-role edit --creator-role view --reason "..."   # PROPOSES; prints the apply link
ankra org access-policy proposals [--status pending|all] ; ankra org access-policy proposal <id>
```

A grant without `--expires` is standing. `elevate` needs only `kube_access.elevate`, always grants
the caller, and requires `--expires` and `--reason`; run it again to extend, `revoke <grant-id>` to
end it early. A grant the organisation access policy refuses names the limit and what to pass. The
CLI cannot change the policy: `set` only proposes (needs `kube_access.manage`), prints the before
and after, the proposal id, its 7-day expiry and an apply link, and an organisation owner or admin
opens that link signed in and applies it. `--wait` blocks until it is decided (exit 0 applied, 4
dismissed, 1 expired or superseded, 5 on `--timeout`). See `ankra-security`.

## Triage

```bash
ankra cluster operations list
ankra cluster operations steps <execution-id>
ankra cluster operations retry <execution-id>
ankra cluster stacks history <stack>
ankra cluster agent status
```

A failed platform execution explains more deploy failures than pod logs do - start there. See
`ankra-troubleshooting`.

## Recovering from a failed Kubernetes version hop (Ankra staff only)

After a failed `ankra cluster upgrade` whose nodes were rolled back by hand, the platform still
records the new version and re-runs the hop as soon as it may act again. Ankra staff hold the
cluster still and put the record back with:

```bash
export ANKRA_STAFF_SESSION='<staff admin-portal session>'   # an API token is refused here
ankra cluster operations hold set <cluster-id> --for 4h --reason "hop failed, manual rollback"
ankra cluster upgrade rollback-record <cluster-id> v1.34.5+k3s1 --reason "control plane rolled back"
ankra cluster operations hold get <cluster-id>    # wait until the record check says confirmed
ankra cluster operations hold lift <cluster-id> --reason "record confirmed"
```

These commands take the cluster by id, not name, and never use the saved login: a customer
login, a service token or an MCP client cannot run them, so do not offer them to a customer.
While a hold is in force every write to the cluster through Ankra (a deploy, a scale) is refused
with 409. `rollback-record` is refused without a hold, while a write is running, or when the
platform's last read of the control planes does not show the version; the lift is refused until
the platform has read every node at the recorded version (`--force` overrides, audited).

## Agent upgrades

```bash
ankra cluster agent status                       # version, check-in, whether auto-upgrade is on
ankra cluster agent auto-upgrade disable --reason "freeze window"   # fence this agent off from the fleet rollout
ankra cluster agent auto-upgrade enable --reason "freeze over"      # put it back in
ankra cluster agent upgrade                      # apply a release now, whichever way the switch sits
ankra cluster agent upgrade --version 2.1.1250   # install exactly that published release
ankra cluster agent upgrade --version 2.1.1247 --allow-downgrade   # roll back on purpose
```

The fleet rollout upgrades every online agent that has not opted out, as soon as the cluster has
no write execution running; it knows nothing about your freeze windows, so disable it ahead of
one and enable it after. Each switch is recorded in the audit log (`set_agent_auto_upgrade`, with the
previous value and your `--reason`), so say why. The MCP twins are `get_cluster_agent_status`, `set_cluster_agent_auto_upgrade`
and `upgrade_cluster_agent` (which takes the same `version` / `allow_downgrade`).

`agent upgrade` without `--version` installs the newer of the release the agent runs and the latest
stable one, so it never moves an agent backwards (a release candidate is re-rendered where it is).
A `--version` older than the running release is refused (exit 2) unless `--allow-downgrade` is also
given, and a release that is neither published nor running is refused the same way: a downgrade
only happens when both flags say so.

## Agent sizing

```bash
ankra cluster agent resources get                          # memory/CPU limits and requests; "chart default" when unset
ankra cluster agent resources set --memory-limit 2Gi       # only the flags passed are sent
ankra cluster agent resources set --memory-limit ""        # clear one member back to the chart default
```

An agent being OOM-killed needs a bigger memory limit stored here, not a hand-run
`helm upgrade --set resources.limits.memory=...`: the next agent upgrade renders the hand-set value
away, while the stored one rides every install and upgrade Ankra generates. The command prints the
platform's sentence saying whether the agent took the change now, at its next upgrade, or when it
reconnects.

## Hosted logs

```bash
ankra cluster logs-ship status                   # on/off, platform availability, whether the agent follows it
ankra cluster logs-ship enable --yes             # opt in: ship every running container's log lines to Ankra (kept 7 days)
ankra cluster logs-ship disable                  # opt out; no confirmation needed
```

Ankra restarts a NotReady control plane node through the cloud provider when the other control
planes are Ready and etcd keeps quorum without it. The switch is per cluster and on by default:

```bash
ankra cluster control-plane-heal get                    # switch + latest NotReady episode (shadow = recorded only)
ankra cluster control-plane-heal set --enabled=false    # stop the automatic restart (needs clusters.write)
```

Ankra backs up each running cluster's etcd state every six hours and keeps the 4 newest backups plus
one a day for the cluster's retention window (7 days by default, 1 to 90). A shorter window deletes
the daily backups outside it, so the command asks first; confirm with the user before passing `--yes`:

```bash
ankra cluster state-snapshots retention                 # the window in effect (and whether it is the default)
ankra cluster state-snapshots retention --days 30       # keep 30 days (needs clusters.write)
```

Control-plane changes on clusters Ankra provisions are `ankra cluster <provider> control-plane
get|set-count|set-instance-type` (details in `ankra-cloud-clusters`). Resizing the ONLY controller
of a running Hetzner cluster takes the Kubernetes API down for a few minutes and needs
`--accept-api-downtime`; get the user's explicit go-ahead first, or grow to three controllers for a
resize without downtime. Other providers resize a single controller only on a stopped cluster.

Shipping logs to Ankra's hosted log store is opt-in per cluster and off by default; turning it on
needs `clusters.write` and sends customer log content out of the cluster, so confirm with the user
before passing `--yes`. `status` names an agent that does not follow the switch yet (too old: fix
with `ankra cluster agent upgrade`; or opted out locally with `logs_ship.enabled: false` in its Helm
values) and a platform where hosted logging is not live yet (the switch is stored, nothing ships
until it is).

## Migrating a Docker deployment

```bash
ankra migrate up ./app --cluster shop --plan       # what will move, how big, what stays behind; changes nothing
ankra migrate up ./app --cluster shop              # convert + deploy + dump + restore, one command (rehearsal)
ankra migrate up ./app --cluster shop --stop-source --yes   # the cutover: stop the source's services, final sync
ankra migrate convert ./app --out ./app-k8s        # the steps separately: compose / Dockerfile / daemon -> stack
ankra cluster apply -f ./app-k8s/cluster.yaml      # the workloads, with empty databases
ankra migrate data ./app --cluster shop --wait     # dump every database and restore it in the cluster
ankra migrate export ./app --out ./app-data        # the two halves of `data`, separately:
ankra migrate restore ./app-data --cluster shop --wait
ankra migrate restore-status <import-id> --wait    # follow a restore started without --wait
ankra migrate imports list                         # the dumps a vault still holds (restore again, or clean up)
ankra migrate imports delete <import-id> --yes     # remove an import's dumps from the vault
ankra migrate modules install <https-url-or-file>  # add an external module into ~/.ankra/modules
```

The CLI also dispatches unknown commands to plugins, kubectl style: an executable named
`ankra-<command>` in `~/.ankra/plugins` or on PATH runs as `ankra <command>` (built-ins always
win; `ankra plugins` lists what is installed).

`up` plans before it touches anything (databases and their sizes from the running containers, free
disk, cluster, stack, vault; `--plan` stops there), applies the stack under the cluster's name, waits
for the database pods, then dumps and restores. It is safe to re-run. `export` dumps PostgreSQL and
MySQL/MariaDB through the docker CLI with the credentials the container actually runs with
(`--option docker-host=ssh://root@host` for a remote daemon, `--option project=<name>` when the compose
project runs under another name); `restore` uploads the dumps to the organisation's backup vault with
presigned URLs and the cluster's agent restores them inside the cluster - no kubectl, no database
client, Ankra never holds the data. Needs a ready backup vault (`ankra backup vaults provision`; picked
automatically when there is one) and an agent that supports data restores. Not carried, and named in
the plan: files in the volumes of non-database workloads, Redis/MongoDB/search data.

## AI from the terminal

```bash
ankra chat --mode ask "why is my ingress pod crashlooping?"
ankra chat --mode agent "open a PR fixing the failing lint job"
ankra chat health
ankra chat actions list <conversation-id>   # writes awaiting confirmation
ankra tickets list --needs-human
ankra agents runs --status running
```

- `--mode ask` - read-only, plus curated safe creations (a workspace pod to search an
  application's repositories, a throwaway PR demo, a brand-new stack). It never changes existing
  infrastructure and never opens a pull request.
- `--mode agent` - can act. Opening a pull request is automatic (the PR is the review gate); other
  destructive changes stay confirmation-gated via `ankra chat actions`.

Omit `--mode` to use the server default. MCP token scopes mirror the modes:
`ankra tokens create <name> --scopes mcp:read` for the Ask surface, `--scopes mcp:read,mcp:write`
for the Agent surface. See `ankra-ai-agents` and `ankra-ai-gateway`.

## Support requests

```bash
ankra support create --subject "Nodes NotReady" --description "..." --cluster prod
ankra support list
ankra support get <ticket-id>
ankra support comment <ticket-id> --message "Any update?"
ankra support attach <ticket-id> ./screenshot.png
ankra support close <ticket-id>
```

Every request is reviewed by Ankra AI before it reaches the team; `--force` on `create` submits a
flagged request anyway. Attach the evidence (operations output, logs) rather than describing it.

## Cloud cost

```bash
ankra cost summary                       # fleet rollup: projected month end, month to date, run rate, by provider, costliest clusters
ankra cost savings                       # savings levers per cluster (right-size idle, reduce unallocated, off-hours schedule) with their monthly saving, the total counting each cluster once at its best; unpriced and stale clusters; waste summary
ankra cost cluster prod-eu               # one cluster: breakdown by component, namespace allocation, daily trend
ankra cost settings get                  # display currency, effective discount, network egress estimate
ankra cost settings set --currency eur --discount 12.5   # admins only; only the flags you pass change
ankra cost namespaces prod-eu            # cost per namespace; `cost object` for one stack, application or credential
ankra cost trend                         # how the run rate moved, with `cost events` for why
ankra cost budgets list                  # monthly budgets (set, delete)
ankra cost decisions list                # proposals and their ledger (`cost ledger`: what decisions actually saved)
ankra cost autopilot get                 # how much Ankra may do about cost on its own
ankra org usage                          # what the organisation used of Ankra itself, per meter
```

Figures are list-price estimates in the organisation's display currency, priced hourly from each
cluster's node inventory and allocated to namespaces by CPU and memory share. A cluster with no
estimate prints its readiness reason (no cloud credential, unsupported provider, nodes still
syncing) - never zeros. Add `-o json` to script against the same document the portal reads.

## Workspaces, pages and skills

```bash
ankra exec -- go test ./...              # a heavy command for this checkout, run in its Ankra Workspace pod
ankra workspace up --wait                # bring the workspace up ahead of time (status, down)
ankra dev install                        # shims so plain go test / golangci-lint / pnpm typecheck go to the workspace
ankra canvas publish report.html         # an HTML, SVG or Markdown page at a public, unguessable URL (list, unpublish)
ankra skills install                     # these skills plus the /ankra-* workflow commands, into every assistant found
ankra skills install --with-hooks        # also ask before an assistant runs a cluster-mutating kubectl or helm
ankra skills list
```

## Scripting

```bash
export ANKRA_API_TOKEN=<token>              # never hardcode in a repository
ankra cluster info --cluster prod -o json
ankra cluster logs -l app=web -n prod --follow=false -o json | jq '.[].message'
ankra login --github-oidc                   # a GitHub Actions job that must call Ankra, with no stored token (needs an `ankra org oidc-trust` policy); CI itself belongs on Ankra Pipelines
ankra tokens rotate <token-id>              # renew before expiry; the old value keeps working for --overlap-hours
```

- **Structured output**: most read commands take `-o json|yaml`. stdout stays parseable; hints and
  errors go to stderr.
- **Token resolution order**: explicit `--token`, then the saved login from `ankra login`, then
  `ANKRA_API_TOKEN`. A saved login **beats** the environment variable - run `ankra logout` first if
  you want the variable to win.
- **Exit codes are a contract**: `0` success, `1` API/runtime, `2` usage, `3` not found,
  `4` confirmation declined, `5` `--wait`/`--timeout` expiry, `6` auth, `7` RBAC permission denied
  (the role lacks the permission; re-logging in will not help).
- **Destructive commands confirm first** (`delete`, `uninstall`, `deprovision`); declining exits 4.
  In automation, decide deliberately whether to pass the command's non-interactive flag.

## Conventions to follow

- **Confirm the target before mutating.** `ankra org current` and `ankra cluster info`, every time.
- **Prefer versioned YAML** (`ankra cluster apply -f`) over ad-hoc edits, so the change is
  reproducible and reviewable.
- **Validate, then draft, then apply.** Each step is cheap; a wrong apply is not.
- **Do not reach for `kubectl` or `helm`, for reads either.** Use `ankra cluster get`, `describe`,
  `events`, `logs`, `top` and `metrics query` to look, and
  `ankra cluster exec <pod> -n <namespace> -- <command>` to run one command in a container.
  Changes belong in the GitOps repo or `ankra cluster apply`. If the CLI has no command for
  something, report that gap and stop; do not suggest `kubectl` in its place.
- **Never paste secrets** into a command line - use stdin, a prompt, `--set-file` or `--set-env`,
  or `ankra secrets create <name> --generate` so nobody handles the value at all.

## Related skills

- Authoring the YAML you apply: `ankra-import-cluster`, `ankra-stacks-addons`.
- Deploying your own code: `ankra-ship` first, then `ankra-applications` and `ankra-cicd`.
- Reusing a stack across clusters: `ankra-stack-profiles`.
- When it breaks: `ankra-troubleshooting`.
- Everything above, safely: `ankra-security`, `ankra-sops-secrets`, `ankra-platform-principles`.
