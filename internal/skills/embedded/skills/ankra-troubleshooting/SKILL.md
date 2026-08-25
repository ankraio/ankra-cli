---
name: ankra-troubleshooting
description: Diagnose a failing workload, deployment or Ankra operation on a managed cluster - reading logs (including the previous container of a CrashLoopBackOff), events scoped to one object, describe output, live CPU/memory from the metrics API, PromQL against the cluster's Prometheus, and the platform execution history that explains most deploy failures. Use when something is broken, a pod is crashlooping or Pending, a deploy failed, a stack will not come up, or the user asks to check logs, events, metrics or operations.
---

# Troubleshooting an Ankra cluster

Diagnose read-only first, propose the fix second, apply it only when asked. Almost everything here
is a bounded read that needs no cluster credentials of your own — the Ankra agent proxies it.

## Order of investigation

The order matters more than the commands. Working bottom-up from pod logs wastes time on failures
whose cause is one layer up.

```
1. operations   did the platform execution succeed?      ankra cluster operations list
2. object       what does the object itself say?         ankra cluster describe <kind> <name>
3. events       what happened to it?                     ankra cluster events --for <kind>/<name>
4. logs         what did the process say?                ankra cluster logs ... --previous
5. resources    was it starved or evicted?               ankra cluster top pods / metrics query
6. dependency   is the thing it calls actually up?       get services / logs of the provider
```

## 1. Platform executions

A stack, addon or application change that "did not happen" almost always has a failed execution
behind it, and its step output names the cause directly.

```bash
ankra cluster info                                   # confirm which cluster you are on
ankra cluster operations list
ankra cluster operations list <execution-id>         # detail for one execution
ankra cluster operations steps <execution-id>        # per-step status and output
ankra cluster operations retry <execution-id>        # terminal (failed/cancelled/timeout) only
ankra cluster operations cancel <execution-id>
ankra cluster operations cancel-step <execution-id> <step-id>
```

Retry is the right move for a transient failure (a registry timeout, a node that came back). It is
the wrong move for a bad manifest: fix the definition, then re-apply.

## 2. The object itself

```bash
ankra cluster get pods -n <namespace>
ankra cluster get pods --all-namespaces
ankra cluster describe pod <name> -n <namespace>
ankra cluster describe deployment <name> -n <namespace>
ankra cluster describe node <name>
ankra cluster describe pvc <name> -n <namespace> -o json
```

`describe` answers "why is it not ready?" in one call: conditions, per-container state (probe
failures, image-pull errors, OOMKills, exit codes) and the events whose `involvedObject` is this
resource. Kinds outside the built-in set need `--group` (and sometimes `--api-version`), the same
as `ankra cluster get resources`.

## 3. Events, scoped

```bash
ankra cluster events -n <namespace>
ankra cluster events --for pod/<name> -n <namespace>
ankra cluster events --for deployment/<name> -n <namespace> --type Warning
ankra cluster events --all-namespaces --type Warning -o json
```

`--for` is a server-side field selector on `involvedObject`, not a substring match — it is what
turns "the pod is Pending" into "no node matches the nodeSelector". Prefer it over reading a
namespace-wide list by eye.

## 4. Logs

```bash
ankra cluster logs <pod> -n <namespace> --tail 200 --follow=false
ankra cluster logs <pod> -n <namespace> --previous            # the CrashLoopBackOff log
ankra cluster logs <pod> -n <namespace> -c <container>
ankra cluster logs -l app=web -n <namespace> --follow=false --tail 50
ankra cluster logs <pod> -n <namespace> --all-containers --previous
ankra cluster logs -l app=web -n <namespace> --follow=false -o json
```

- `--namespace` is required.
- **`--previous` is the only log with the failure in it** for a pod in CrashLoopBackOff: the
  running container is the *next* attempt, which has not failed yet. A previous log is closed, so
  this is always a bounded read.
- `--follow` defaults to true. Pass `--follow=false` for anything you are piping into `grep`, or a
  script will hang.
- `-l/--selector` reads every matching pod; `--all-containers` expands each pod to all of its
  containers, init and ephemeral included. With more than one target, lines are prefixed with
  `[pod]` or `[pod/container]` so interleaved output stays attributable.

## 5. Resources

```bash
ankra cluster top pods -n <namespace>
ankra cluster top nodes
ankra cluster metrics query 'sum by (pod) (rate(container_cpu_usage_seconds_total[5m]))'
ankra cluster metrics query-range 'rate(node_cpu_seconds_total[5m])' --range 6h --step 5m
```

`top` reads the aggregated metrics API directly, so it works on clusters where Prometheus was
never installed — it is the "which container just got OOMKilled" view. `metrics` proxies PromQL to
the Prometheus source configured in **Cluster Settings → Metrics**, and is the view for trends over
time. See `ankra-observability` for installing and wiring those sources.

## 6. Cluster and agent health

```bash
ankra cluster list
ankra cluster info
ankra cluster agent status                            # an offline agent explains "nothing works"
ankra cluster get nodes
ankra cluster reconcile                               # ask Ankra to re-converge
ankra cluster helm releases                           # what Helm thinks is installed
ankra cluster stacks history <stack>                  # what changed on this stack, and when
```

An offline agent is the first thing to rule out when every command is failing rather than one
workload. `ankra cluster stacks history` is the cheapest way to answer "what changed?" when
something that worked yesterday does not today.

## 7. Ask Ankra AI

```bash
ankra chat health                                     # AI-analysed cluster health
ankra chat --mode ask "why is the payments pod crashlooping in prod?"
ankra tickets list --needs-human                      # incidents the agents have filed
```

Ask mode is read-only. It is a good first pass on an unfamiliar cluster, and it will tell you what
it looked at — verify the evidence rather than acting on the conclusion alone. See
`ankra-ai-agents`.

## Classifying what you found

| Evidence | Class | Where the fix belongs |
|----------|-------|-----------------------|
| `CreateContainerConfigError`, missing key | Configuration / secret | Application env-secrets, or the Secret the stack deploys |
| `ImagePullBackOff` | Registry / pull secret | `--pull-secret`, credential scope, image tag exists |
| `CrashLoopBackOff` with a stack trace | Application code | The repository, as a pull request |
| `CrashLoopBackOff` immediately, no output | Config read at startup, or the wrong command | Chart values / Dockerfile |
| Pending, `no nodes available` | Scheduling / capacity | Node group size, requests, tolerations |
| OOMKilled | Limits | Chart values; check `top pods` first |
| Ready but unreachable | Port, ingress path, probe path | Chart values / ingress manifest |
| Deployed too early, dependency missing | Stack ordering | `parents` edges — see `ankra-stacks-addons` |
| Execution failed before any pod existed | Platform / manifest | The stack or addon definition |

## Rules

- **Read before you write.** Diagnose fully, then propose. Do not re-apply or reconcile as a
  probe — it changes the state you are diagnosing.
- **`--follow=false` for anything scripted.** The default streams forever.
- **`--previous` for CrashLoopBackOff**, every time.
- **Name the layer.** "Platform" and "application code" have different owners and different fixes;
  say which one it is.
- **Never paste secret values** from `get secrets` or `cluster decrypt` into an issue, a PR, or
  chat. Report the key name and its state.
- **Retry a transient failure, fix a deterministic one.** Repeatedly retrying a bad manifest just
  moves the failure later.
- **Do not reach for `kubectl` to mutate.** Read-only kubectl against a context from
  `ankra cluster kubeconfig add --use` is fine; mutations belong in the GitOps repo or
  `ankra cluster apply`.

## Related skills

- `ankra-observability` — installing Prometheus/Grafana/Loki and wiring external metrics sources.
- `ankra-alerts-webhooks` — routing what you just found to Slack, Teams, or PagerDuty.
- `ankra-stacks-addons` — dependency ordering, the cause behind most "deployed too early" failures.
- `ankra-applications` — application deploys, env-secrets, and their failure modes.
- `ankra-ai-agents` — Ankra AI's own investigation, agent runs, and the incident board.
