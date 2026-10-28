# Contributing to Ankra CLI

Thanks for your interest in improving the Ankra CLI! This document covers how
to report problems, propose changes, and get a pull request merged.

By participating in this project you agree to follow our
[Code of Conduct](CODE_OF_CONDUCT.md). Contributions are accepted under the
project's [Apache 2.0 license](LICENSE).

## Reporting issues

- **Bugs and feature requests**: use the
  [issue templates](https://github.com/ankraio/ankra-cli/issues/new/choose).
- **Security vulnerabilities**: do **not** open a public issue. Follow
  [SECURITY.md](SECURITY.md) and email
  [security@ankra.io](mailto:security@ankra.io).
- **Questions**: check the [documentation](https://docs.ankra.ai) first; for
  anything else open an issue or reach us at
  [hello@ankra.io](mailto:hello@ankra.io).

## Development setup

You need Go (the version pinned by the `toolchain` directive in `go.mod` —
`go` downloads it automatically), `make`, and
[golangci-lint](https://golangci-lint.run) for the lint gate.

```bash
git clone https://github.com/ankraio/ankra-cli.git
cd ankra-cli

make hooks        # once per clone: git config core.hooksPath .githooks
make build        # go build ./...
make test         # go test -race -count=1 ./...
make lint         # golangci-lint run
./build.sh        # dist/ankra with release-identical ldflags (--install → /usr/local/bin)
```

`make hooks` installs the pre-commit gate, and it is the step people miss:
git does not read `.githooks/` unless `core.hooksPath` points at it, so until
you run it your commits go through with no gate and no warning. Run it once
per clone (it is idempotent, and `git config core.hooksPath` tells you whether
a clone already has it). Keep the value relative, as `make hooks` writes it:
git resolves a relative `core.hooksPath` per worktree, so each worktree runs
its own branch's hook, while an absolute path makes every worktree run one
checkout's copy.

The hook then runs `go test` for the packages the commit changes (every
package when `go.mod` or `go.sum` changes) and lints those same packages,
and a red test blocks the commit. Please don't bypass it with
`--no-verify`. `ankra/cmd` is the exception: building it (for its tests or for
the linter) needs more than 12 GiB, so the hook leaves it to CI's
`lint-test-build`, which tests and lints the whole module on every push.
`ANKRA_PRECOMMIT_FULL=1 git commit` runs `go test ./...` and
`golangci-lint run ./...` locally instead, on a machine with the memory for it.

## Making changes

The layout in brief:

- `main.go` → `cmd/` — flat package, one file per command using
  `<family>_<sub>.go` naming (e.g. `cluster_kubeconfig.go`), with tests
  alongside.
- `internal/client` — typed HTTP client for the platform API, one file per
  resource family.
- `internal/kubeconfig` — kubeconfig read/merge/write.
- `tools/gendocs` — generates the CLI reference published to
  [docs.ankra.ai](https://docs.ankra.ai); it runs automatically on release
  tags, so write `Short`/`Long`/`Example` on new commands for docs readers.

A few invariants the codebase relies on:

- **`cmd/services.go` defines the `APIClient` interface.** Adding a method to
  `internal/client` means also adding it to that interface *and* to the
  `baseMock` stub set in `cmd/e2e_test.go`. The build breaks until all three
  agree — that is by design.
- **Exit codes are a scripting contract** (`cmd/exitcodes.go`): 0 success,
  1 API/runtime, 2 usage, 3 not-found, 4 confirmation declined, 5
  `--wait`/`--timeout` expiry, 6 auth, 7 RBAC permission denied. Commands use
  `RunE` and return errors (wrapped with `withExitCode` where the class is
  known) — never `os.Exit`, and errors never go to stdout.
- **Destructive commands confirm first** (delete, uninstall, deprovision);
  declining exits 4. Use the existing prompt helpers rather than rolling new
  ones.
- **Structured output stays clean**: commands with `-o json|yaml` must keep
  stdout parseable — human hints and errors go to stderr.
- **Deprecations are managed, not ad hoc**: register forwarders with
  `deprecateAndForward` (`cmd/deprecation.go`) and record them in
  [DEPRECATIONS.md](DEPRECATIONS.md).

## Changelog

Every user-visible change gets an entry in the `## Unreleased` section of
[CHANGELOG.md](CHANGELOG.md), written in the file's established prose style:
bold lead-in, explaining the user-facing consequence rather than the
implementation.

## Commit style

Conventional-commit subjects scoped by area — `fix(kubeconfig): ...`,
`feat(cluster): ...`, `chore: ...` — with bodies that explain the user-visible
why. Recent `git log` entries are the best reference.

## Pull requests

The default branch is `master` and all changes land via pull requests.

1. Fork the repository (or branch, if you have write access) and make your
   change on a topic branch.
2. Make sure `make test` and `make lint` pass locally — CI runs the same
   gates.
3. Open a PR; the
   [template](.github/pull_request_template.md) asks for a summary, a test
   plan, and a docs checklist. Please fill in all three.
4. A maintainer will review your PR. Small, focused PRs with a clear "what &
   why" get reviewed fastest.

Thanks for contributing!
