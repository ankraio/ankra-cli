# GitHub Actions without a stored Ankra token

An organisation admin trusts a repository once. Its workflows then sign in with
the job's own OIDC identity, and the Ankra token they receive lasts minutes.

## 1. Create the trust policy (once, as an org admin)

```sh
gh api repos/acme/deploy --jq '.id, .owner.id'      # repository id, owner id

ankra org oidc-trust create deploy-main \
  --repository acme/deploy \
  --repository-id 123456789 --repository-owner-id 987654 \
  --ref refs/heads/main \
  --environment production \
  --role operator --ttl 15m
```

No value takes a wildcard. Pull request runs are refused unless you pass
`--allow-pull-request`; `pull_request_target` is never trusted. Prefer pinning an
`--environment` that requires reviewers in GitHub.

## 2. Use it in the workflow

```yaml
permissions:
  id-token: write      # lets the job request its OIDC ID token
  contents: read

jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: production
    steps:
      - uses: actions/checkout@v4
      - name: Install ankra
        run: curl -fsSL https://get.ankra.io | sh      # or your usual install step
      - name: Sign in to Ankra
        env:
          ANKRA_ORG: 3b7dccca-0788-4470-9910-19478ae345ae   # your organisation id
        run: ankra login --github-oidc
      - run: ankra cluster list
```

`--audience` overrides the audience requested for the ID token (the default is
`https://ankra.ai/oidc/github`; self-hosted installs set `GITHUB_OIDC_AUDIENCE` on the
platform to match).

## Managing policies

```sh
ankra org oidc-trust list
ankra org oidc-trust delete <policy-id>     # also revokes the tokens it minted
```

A refused sign-in tells you only that no policy matched. The reason (wrong
ref, wrong environment, a pull request, an unknown repository id) is in the
organisation's audit log under `github_oidc_exchange_refused`.
