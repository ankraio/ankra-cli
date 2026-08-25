# Ankra Applications — reference

Long-form detail for `ankra-applications`: the registry matrix, monorepo components, and a
complete worked greenfield run from an empty repository to production on several clusters.

## Registry matrix

An application publishes its image either into the organisation's own Ankra registry project (the
default, nothing to configure) or into a registry you already operate. Declare the second kind at
`ankra application add` time; changing it later regenerates nothing.

| Registry | `--registry-url` | Credential | Notes |
|----------|------------------|------------|-------|
| Ankra registry | *(omit)* | — | Default. Ankra owns the project and the robot. |
| Harbor | `oci://artifact.example.com/commerce` | Harbor **user** with Project Admin on the project | Ankra can mint a per-application push robot — see below. |
| Amazon ECR | `oci://<account>.dkr.ecr.<region>.amazonaws.com/commerce` | IAM key or role with `ecr:PutImage` | ECR repositories are not auto-created by default; create the repository first. |
| Google Artifact Registry | `oci://<region>-docker.pkg.dev/<project>/commerce` | Service account key with Artifact Registry Writer | |
| Azure Container Registry | `oci://<name>.azurecr.io/commerce` | Service principal or token with `AcrPush` | |
| GHCR | `oci://ghcr.io/<org>` | PAT with `write:packages` | The repository's own `GITHUB_TOKEN` works for same-org pushes. |
| Docker Hub | `oci://docker.io/<namespace>` | Access token, not the account password | Rate limits apply to pulls from the cluster. |
| Nexus / Artifactory / ChartMuseum | `oci://<host>/<repo>` | Deploy user scoped to that repository | See `ankra-helm-registries` for the chart side. |

The scheme is optional: `artifact.example.com/commerce` is accepted and normalised.

### Who mints the push robot

```bash
ankra application registry set <application-id> \
  --url oci://artifact.example.com/commerce \
  --credential example-harbor-pull \
  --admin-credential example-harbor-admin
```

- `--credential` is what Ankra uses to **read** the registry (verify builds, pull demo images).
  Without it Ankra can describe where images live but reports builds as never published.
- `--admin-credential` is a credential with **project administrator** rights. Given one, Ankra
  mints, rotates and revokes a per-application push robot and stores it in the repository's
  Actions secrets. Ankra never mints a robot for a registry it was not handed admin keys to.
- `--manage-actions-secrets` (without an admin credential) writes the *declared* credential into
  the repository's Actions secrets instead. Use it when you want to keep control of the robot.
- `--username-secret` / `--password-secret` name the Actions secrets the build workflow logs in
  with, when you populate them yourself.
- `--pull-secret` names the `dockerconfigjson` Secret the generated manifests reference, so the
  cluster can pull the private image. Without it, a private registry deploys to `ImagePullBackOff`.

### Monorepo components

```bash
ankra application registry set <application-id> \
  --url oci://artifact.example.com/commerce \
  --component-repository crm-api=commerce-api \
  --component-repository crm-frontend=commerce-web
ankra application registry set <application-id> --url ... --flat-repositories
```

By default components publish to `<project>/<app>/<component>`. `--flat-repositories` publishes to
`<project>/<component>` instead — match whatever your registry's project conventions already are.
`--component-repository` overrides one component's repository explicitly.

Demos of a monorepo run every recorded component as its own pod:

```bash
ankra application demo deploy <app-id> --branch main \
  --component crm-frontend --component crm-api \
  --component-port crm-api=8090 --component-path crm-api=/api \
  --entry-component crm-frontend
```

Selection and overrides ride the same request field, so an override may only name a component
that `--component` selects. To tune one component of a full launch, list them all.

## Worked run: greenfield service to production on three clusters

The whole path, with the decision at each step called out. Substitute names freely.

### 0. Prerequisites

```bash
ankra login
ankra org current
ankra cluster list                 # note the dev, staging and production cluster ids
ankra credentials list             # a GitHub credential must exist for the repository
```

### 1. Make the source deployable

Before registering anything, the repository needs: a container build (a Dockerfile, or a stack
Ankra can generate one for), one listening port, a health endpoint, and **all configuration read
from the environment**. A service that reads a config file baked into the image cannot be promoted
between environments without a rebuild, which defeats every step that follows.

### 2. Register

```bash
ankra application add . --name payments \
  --registry-url oci://artifact.example.com/commerce \
  --registry-credential example-harbor-pull \
  --registry-pull-secret commerce-registry
ankra application list             # note the application id
```

### 3. Review and merge the setup pull request

```bash
ankra application get <app-id>
ankra application branch-files <app-id>
```

Read the generated Dockerfile, chart and workflow in the pull request. Adjust anything wrong and
push it back:

```bash
ankra application files <app-id> --file Dockerfile=./Dockerfile --message "Pin the base image"
```

Merge when the diff is right. If setup itself failed, `ankra application retry <app-id>`.

### 4. Configuration and secrets

```bash
ankra application env-secrets list <app-id>
printf '%s' "$DATABASE_URL" | ankra application env-secrets set <app-id> DATABASE_URL
printf '%s' "$STRIPE_KEY"   | ankra application env-secrets set <app-id> STRIPE_KEY
ankra application env-secrets apply <app-id>
```

Non-secret values (the LLM gateway base URL, a feature flag, a bucket name) belong in variables,
not here:

```bash
ankra cluster variables set LLM_BASE_URL http://litellm.ai.svc.cluster.local:4000 --cluster dev
ankra org variables set SUPPORT_EMAIL support@example.com
```

### 5. Dev first

```bash
ankra application deploy <app-id> --cluster <dev-cluster-id> --namespace payments
ankra cluster operations list --cluster dev
ankra cluster get pods -n payments --cluster dev
ankra cluster logs -l app=payments -n payments --cluster dev --follow=false --tail 100
```

If it does not come up, switch to `ankra-troubleshooting` before changing anything.

### 6. Preview every pull request

```bash
ankra application demo config <app-id>
ankra application demo deploy <app-id> --pr-number 42 --ttl-hours 8
ankra application demo detail <app-id> <demo-id>       # the public preview URL
```

The reviewable artefact for a change is the preview URL, not a screenshot.

### 7. Scanning before staging

```bash
ankra application upgrade-workflow <app-id>
ankra application container-security <app-id>
ankra application code-security <app-id>
```

Fix or accept every high finding deliberately. An accepted finding should be written down.

### 8. Staging, then production

```bash
ankra application deploy <app-id> --cluster <staging-cluster-id> --namespace payments
# verify, then
ankra application deploy <app-id> --cluster <prod-cluster-id> --namespace payments \
  --mode high_availability --set replicas=3
```

The **same image tag** promotes. If the tag differs between staging and production, you did not
promote — you shipped something that was never tested.

### 9. Continuous delivery

```bash
ankra application auto-deploy set <app-id> --enabled          # dev / staging
ankra application auto-deploy get <app-id>
```

For production, leave auto-deploy off unless the tracked branch is protected and the pipeline
gates on tests. `ankra-cicd` covers the pipeline that bumps the tag in the GitOps repository.

### 10. The rest of the fleet

Two shapes, and the choice is about whether the clusters need different values.

**Same values everywhere** — publish once, install from the catalogue:

```bash
ankra application publish-addon <app-id> --version 1.2.0 \
  --display-name "Payments" --category backend --changelog "Initial catalogue release"
ankra application published-addon <app-id>
ankra application manifest-addon <app-id>          # inspect, install per cluster, withdraw
```

**Different values per cluster** — capture the deployed stack as a parameterised profile:

```bash
ankra stack-profiles drafts create --name payments \
  --source-cluster prod --source-stack payments
# turn every domain / size / replica count into a parameter, then
ankra stack-profiles drafts publish <draft-id> --changelog "Initial version"
ankra stack-profiles apply payments --cluster eu-prod --set domain=eu.example.com --dry-run
```

See `ankra-stack-profiles`. Either way you build once and promote; you never fork the YAML.

## Failure modes worth recognising

| Symptom | Usual cause |
|---------|-------------|
| Build pushes to the wrong registry | The registry was declared after `add`; the generated workflow still logs in with the old one. Re-run setup after `registry set`. |
| `ImagePullBackOff` on a private registry | No `--pull-secret`, or the `dockerconfigjson` Secret is not in the deploying namespace. |
| Builds report "never published" | A registry with no `--credential`: Ankra cannot read it to verify the push. |
| First deploy crash-loops immediately | An environment secret is unset, or was set but never `env-secrets apply`-ed. |
| `env-secrets set` had no effect | Same: `set` stores, `apply` seals into the running deployments and rolls them. |
| Deploy succeeded, service unreachable | Container port, ingress path, or health probe path does not match what the code serves. |
| Auto-deploy did not fire | Auto-deploy is off, or the build was not on the tracked branch. Check `auto-deploy get`. |
| Demo has no image | The branch has no demo-ready build — `ankra application demo build`, then `demo fix-build`. |
