# Ankra CI/CD — pipeline examples

Both examples follow the same pattern: build, push an immutable tag, then commit the new tag into the GitOps repository so Ankra/ArgoCD syncs it. Neither talks to the cluster directly.

Assumptions:
- App source repo and GitOps repo may be the same or different. Below they are separate.
- The image tag lives in the GitOps repo at `stacks/my-app/values/image.yaml` under `image.tag`.
- CI has: registry push credentials, and a write token for the GitOps repo.

## GitHub Actions

`.github/workflows/deploy.yml` in the application repo:

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

      - name: Log in to registry
        uses: docker/login-action@v3
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
          IMAGE: registry.example.com/my-app
        run: |
          git clone "https://x-access-token:${GITOPS_TOKEN}@github.com/my-org/gitops-repo.git" gitops
          cd gitops
          yq -i ".image.tag = \"${TAG}\"" stacks/my-app/values/image.yaml
          git config user.name  "ci-bot"
          git config user.email "ci-bot@example.com"
          git commit -am "deploy my-app ${IMAGE}:${TAG}"
          git push
```

Ankra detects the commit on the synced branch and reconciles. Optionally verify afterward with the CLI using a scoped `ANKRA_API_TOKEN`:

```yaml
      - name: Verify rollout
        env:
          ANKRA_API_TOKEN: ${{ secrets.ANKRA_API_TOKEN }}
        run: |
          bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
          ankra cluster operations list --cluster prod
```

## GitLab CI

`.gitlab-ci.yml` in the application repo:

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
    - docker build -t "$IMAGE:$TAG" .
    - docker push "$IMAGE:$TAG"
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
    - cd gitops
    - yq -i ".image.tag = \"${TAG}\"" stacks/my-app/values/image.yaml
    - git config user.name "ci-bot" && git config user.email "ci-bot@example.com"
    - git commit -am "deploy my-app ${IMAGE}:${TAG}"
    - git push
```

## Notes

- Replace `yq` field paths with wherever your stack stores the image tag.
- Keep `GITOPS_TOKEN` scoped to write only the GitOps repo; keep `REGISTRY_TOKEN` scoped to push only this image.
- For multi-environment promotion, commit the same `TAG` into the next environment's values path (e.g. `stacks/my-app/values/image.prod.yaml`) in a separate, reviewed step.
