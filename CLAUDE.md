# ankra-cli

Go CLI (cobra) for the Ankra Kubernetes platform. Module `ankra`, installed
binary `ankra`. Default branch is `master`; changes land via PRs using the
template in `.github/pull_request_template.md`.

## Build, test, lint

```bash
make build        # go build ./...
make test         # go test -race -count=1 ./...
make lint         # golangci-lint run
./build.sh        # dist/ankra with release-identical ldflags (--install → /usr/local/bin)
```

A pre-commit hook (`core.hooksPath` → `.githooks/`) runs `go test ./...` and
`golangci-lint run` on every commit, so commits take ~30s and a red test
blocks the commit. Do not bypass it with `--no-verify` unless explicitly
asked.

The version string is injected at build time via `-X main.version` →
`cmd.SetVersion`. The `version = "0.3.0"` constant in `cmd/root.go` is only
the un-injected fallback; never bump it for a release.

## Layout

- `main.go` → `cmd/` — flat package, one file per command using
  `<family>_<sub>.go` naming (e.g. `cluster_kubeconfig.go`), tests alongside.
- `internal/client` — typed HTTP client for the platform API, one file per
  resource family.
- `internal/kubeconfig` — kubeconfig read/merge/write.
- `internal/skills` — embedded agent skills, vendored from the sibling
  `ankra-skills` repo (`make generate` / `make verify-skills`; in the split
  repo the embedded copy is canonical).
- `tools/gendocs` — generates the Mintlify CLI reference; run on release tags
  by `.github/workflows/docs-sync.yml`, which PRs `ankraio/ankra-docs`.
- `systemtest/` — end-to-end lifecycle shell test (CI: `systemtest.yml`).

## Invariants that will not announce themselves

- **`cmd/services.go` defines the `APIClient` interface.** Adding a method to
  `internal/client` means also adding it to that interface *and* to the
  `baseMock` stub set in `cmd/e2e_test.go` (per-test mocks embed `baseMock`).
  The build breaks until all three agree — that is by design.
- **Exit codes are a scripting contract** (`cmd/exitcodes.go`): 0 success,
  1 API/runtime, 2 usage, 3 not-found, 4 confirmation declined, 5
  `--wait`/`--timeout` expiry, 6 auth, 7 RBAC permission denied (role lacks
  the permission; re-login won't help). Commands use `RunE` and return errors
  (wrapped with `withExitCode` where the class is known) — never `os.Exit`,
  never printing errors to stdout. Declined prompts return the shared
  `errCancelled`.
- **Destructive commands confirm first** (delete, uninstall, deprovision);
  declining exits 4. Follow the existing prompt helpers rather than rolling
  new ones.
- **Structured output stays clean**: commands with `-o json|yaml`
  (`registerStructuredOutputFlags`) must keep stdout parseable — human hints
  and errors go to stderr.
- **Deprecations are managed**, not ad hoc: register forwarders with
  `deprecateAndForward` (`cmd/deprecation.go`) and record them in
  `DEPRECATIONS.md`. Policy: deprecate and remove only in minor/major
  releases, with at least one minor of runtime warning in between.

## Changelog and releases

Every user-visible change gets an entry in the `## Unreleased` section of
`CHANGELOG.md`, written in the file's established prose style (bold lead-in,
explains the user-facing consequence, not the implementation).

Cutting a release: promote `## Unreleased` to `## vX.Y.Z` on `master`, tag
`vX.Y.Z`, push the tag. `.github/workflows/release.yml` then builds all six
OS/arch binaries and creates the GitHub release, extracting notes from the
CHANGELOG section whose heading matches the tag. Tags containing a hyphen
(e.g. `v0.5.0-rc1`) are marked prerelease automatically.

## Commit style

Conventional-commit subjects scoped by area — `fix(kubeconfig): ...`,
`feat(cluster): ...`, `chore: ...` — with bodies that explain the user-visible
why, matching `git log`.
