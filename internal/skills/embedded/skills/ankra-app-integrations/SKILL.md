---
name: ankra-app-integrations
description: Connect an application to services and secrets that already exist in an Ankra organisation - an LLM gateway such as LiteLLM or vLLM, a container registry such as Harbor, a database, an object store, or an internal API - by reusing stored credentials rather than minting new ones, and choosing correctly between application env-secrets, SOPS-encrypted manifests, and cluster/organisation variables. Use when the user wants to wire an app to LiteLLM, Harbor, Postgres, Redis, S3 or an internal service, mentions connecting to an existing secret or app, or asks where a configuration value should live.
---

# Connecting an application to what already runs

Most integration work in an Ankra organisation is not "provision a new thing" — it is "point this
application at the thing that already runs, with the credential that already exists". Doing that
wrongly is how admin tokens end up in application environments and how a value gets configured in
a live pod and lost at the next roll.

## The four questions, in order

1. **Where does the service run?** In this cluster, in another cluster, or outside entirely.
2. **Which credential already exists?** Reuse or scope down; never copy an admin token.
3. **Where does the value belong?** Environment secret, encrypted manifest, or plain variable.
4. **How will you prove it works?** A real call, in the logs — not a green deploy.

## 1. Find the service

```bash
ankra cluster stacks list                          # what is deployed, by stack
ankra cluster addons list                          # the Helm releases
ankra cluster get services -n <namespace>          # in-cluster DNS name and port
ankra cluster get ingresses -n <namespace>         # the public name, if any
ankra cluster addons values <addon>                # how the service itself is configured
```

For a consumer in the **same cluster**, use the in-cluster address —
`http://<service>.<namespace>.svc.cluster.local:<port>` — not the public URL. It skips the
ingress, stays inside the cluster network, and keeps working when the public name changes.

For a consumer in **another cluster**, you need the public name plus authentication; say so
explicitly rather than assuming cluster-local DNS will resolve.

## 2. Find the credential

```bash
ankra credentials list                             # cloud, Git, registry credentials
ankra credentials get <name>
ankra credentials repositories <name>              # what a Git credential can actually reach
ankra helm credentials list                        # Helm registry credentials
ankra cluster decrypt addon <addon>                # SOPS-encrypted addon values, when you must
```

Rules that matter more than convenience:

- **Reuse an existing scoped credential, or issue a new scoped one from the service itself.**
  An application gets its own key with its own permissions.
- **Never hand an application an admin credential.** Admin credentials exist so Ankra can mint
  the scoped one (see the Harbor pattern below).
- **Never print a decrypted secret** into the terminal transcript, a pull request, a commit
  message, or chat. `decrypt` is for verifying shape, not for copying values around.

## 3. Decide where the value lives

| Kind of value | Where it goes | Command |
|---------------|---------------|---------|
| Secret only this application needs | Application environment secret | `ankra application env-secrets set` |
| Secret a stack deploys (a Secret manifest, an addon value) | SOPS-encrypted in the GitOps repo, with `encrypted_paths` | `ankra cluster encrypt manifest` / `... addon` |
| Non-secret, differs per cluster (base URLs, sizes, model names) | Cluster variable | `ankra cluster variables set` |
| Non-secret, same across the organisation | Organisation variable | `ankra org variables set` |
| Non-secret, differs per stack instance | Stack variable | `ankra cluster stacks variables set` |
| A value other clusters must supply when they install this | Stack profile parameter | `ankra-stack-profiles` |

```bash
printf '%s' "$LITELLM_KEY" | ankra application env-secrets set <app-id> LLM_API_KEY
ankra application env-secrets apply <app-id>

ankra cluster variables set LLM_BASE_URL http://litellm.ai.svc.cluster.local:4000 --cluster prod
ankra org variables set DEFAULT_MODEL gpt-4o-mini
```

Getting this split right is the whole skill: a base URL in an encrypted secret is unreviewable,
and an API key in a plain variable is a leak.

## 4. Wire the consumer by reference

Inject the secret **by reference**, never as a literal in the manifest:

```yaml
env:
  - name: LLM_BASE_URL
    value: "${LLM_BASE_URL}"          # a variable, resolved at deploy
  - name: LLM_API_KEY
    valueFrom:
      secretKeyRef:
        name: payments-llm
        key: api-key
```

A Secret the stack deploys must live in the consumer's namespace — a Secret in another namespace
is invisible to the pod, and this is the most common cause of "the value is set but the app sees
nothing".

## Pattern: an LLM gateway (LiteLLM, vLLM, Ollama, OpenRouter)

Two different connections are often confused. Be explicit about which one is being asked for.

**A. Your application calls the gateway.**

```bash
ankra cluster get services -n ai                   # e.g. litellm ClusterIP :4000
ankra cluster variables set LLM_BASE_URL http://litellm.ai.svc.cluster.local:4000 --cluster prod
printf '%s' "$VIRTUAL_KEY" | ankra application env-secrets set <app-id> LLM_API_KEY
ankra application env-secrets apply <app-id>
```

Issue the application a **virtual key** from the gateway with its own budget and model allow-list,
rather than sharing the gateway's master key. Pin the model id in a variable so a model change is
a reviewable commit. Verify with one real request and confirm the gateway logged it:

```bash
ankra cluster logs -l app=litellm -n ai --follow=false --tail 50
```

**B. Ankra's own AI calls the gateway** — a different, organisation-level setting:

```bash
ankra ai status
ankra ai endpoints create --name litellm --base-url https://llm.example.com/v1 --api-key <key>
ankra ai endpoints discover litellm                # the model ids it advertises
ankra ai models create --name <model-id> ...       # what the chat picker offers
ankra ai provider set openai_compatible
```

Ankra probes `GET <base-url>/models` on save; a wrong model id only surfaces at the first call.
See `ankra-ai-agents` for the provider and catalogue surface.

## Pattern: a container registry (Harbor and friends)

An application publishing to a registry you operate needs three distinct things, and they are
frequently conflated:

1. **A push credential for CI** — the build workflow logs in with it.
2. **A read credential for Ankra** — so Ankra can verify builds and pull demo images
   (`--credential`). Without it, builds report as never published.
3. **A pull secret for the cluster** — a `dockerconfigjson` Secret the generated manifests
   reference (`--pull-secret`). Without it, deploys land in `ImagePullBackOff`.

```bash
ankra application registry set <app-id> \
  --url oci://artifact.example.com/commerce \
  --credential example-harbor-pull \
  --admin-credential example-harbor-admin \
  --pull-secret commerce-registry
```

Given `--admin-credential` (a credential with **project administrator** rights — for Harbor, a
Harbor *user* with Project Admin on that project, not a robot), Ankra mints, rotates and revokes a
per-application push robot and stores it in the repository's Actions secrets. That is the pattern
to prefer: the admin credential stays with the platform, the application gets a narrow robot.

Declare the registry at `ankra application add` time. Adding it afterwards leaves a build workflow
that logs in with the wrong registry — see `ankra-applications`.

For **charts** rather than images, the equivalent surface is `ankra helm registries` /
`ankra helm credentials` — see `ankra-helm-registries`.

## Pattern: a database or cache in the cluster

```bash
ankra cluster get services -n data                  # the operator's service, often -rw / -ro pairs
ankra cluster get secrets -n data                   # the operator-generated credential Secret
```

Database operators (CloudNativePG, the Redis and MySQL operators) already generate a credential
Secret. Consume **that** Secret rather than copying its value into an application env-secret:
rotation then happens without you. Point the application at the read-write service for writes and
the read-only service for reads where the operator provides both.

Cross-namespace: replicate the Secret into the consumer's namespace with a stack manifest (and a
`parents` edge on the namespace), or deploy the consumer into the data namespace. Do not assume a
pod can read a Secret from elsewhere.

## Pattern: an internal API or third-party SaaS

- In-cluster: `http://<service>.<namespace>.svc.cluster.local:<port>`, no credential if the API
  trusts the cluster network, a scoped token if it does not.
- Outside: the public URL in a variable, the token in an env-secret. Check egress actually works
  from the cluster before blaming the credential.
- Webhooks pointing *back* at Ankra belong in `ankra alerts destinations` — see
  `ankra-alerts-webhooks`.

## Verification checklist

```bash
ankra cluster operations list                       # the change actually deployed
ankra cluster get pods -n <ns>                      # the consumer rolled and is Ready
ankra cluster logs -l app=<name> -n <ns> --follow=false --tail 100
ankra cluster logs -l app=<provider> -n <ns> --follow=false --tail 50   # the provider saw the call
```

An integration is done when the **provider's** logs show the consumer's call succeeding. A
consumer that starts without crashing has proved nothing.

## Rules

- **Reuse, scope down, never copy an admin token into an application.**
- **In-cluster consumers use in-cluster DNS.** Public URLs are for consumers outside the cluster.
- **Secrets by reference**, in the consumer's namespace, never a literal in a manifest.
- **Non-secret configuration is a variable**, so it stays reviewable in a diff.
- **`env-secrets set` then `env-secrets apply`** — setting alone never reaches a running workload.
- **Pin what you depend on**: chart version, image tag, model id.
- **Never echo a decrypted secret** anywhere it will be recorded.
- **Nothing configured only in a live pod.** If it is not in committed YAML or Ankra's stored
  state, the next roll loses it.

## Related skills

- `ankra-applications` — registering the application and its registry.
- `ankra-sops-secrets` — encrypting anything that lands in Git.
- `ankra-helm-registries` — private chart sources and their credentials.
- `ankra-ai-agents` — Ankra's own AI provider, model catalogue and MCP tool servers.
- `ankra-security` — credential scope, tokens, and who can reach what.
- `ankra-troubleshooting` — when the call fails and you need the evidence.
