# Validation test fixtures

ImportCluster YAML examples for exercising the client-side validation in
`ankra cluster apply` (the `buildImportRequest` / `buildStack` / `buildManifest` /
`buildAddon` functions in `cmd/apply.go`). Each invalid file violates exactly
one rule so the resulting error message is unambiguous.

Validation runs locally **before** the API call. The simplest way to exercise it
is `--dry-run`, which runs validation and returns without contacting the API (and
without requiring a token).

## Running

```bash
# from the ankra-cli/ directory, after `go build -o ankra .`
./ankra cluster apply -f testing/validation/<file>.yaml --dry-run
```

A valid file prints `Validation succeeded for "..."; no changes applied (--dry-run).`
and an invalid file prints the error from the table below.

## Files and expected output

Validation failures are printed as `Invalid ImportCluster in "<file>":` followed by
an indented, human-readable path to the offending field. Stacks, manifests, and
addons are identified by name when present, otherwise by 1-based position
(`stack #1`, `manifest #2`, ...).

| File | Expected result |
|------|-----------------|
| `valid-cluster.yaml` | Passes validation. |
| `invalid-yaml.yaml` | `the file is not valid YAML: ...` |
| `wrong-kind.yaml` | `the 'kind' field must be "ImportCluster", but found "NotAnImportCluster"` |
| `missing-metadata.yaml` | `the 'metadata' section is missing (it must contain at least 'name')` |
| `missing-name.yaml` | `'metadata.name' is required (this is the cluster name)` |
| `missing-spec.yaml` | `the 'spec' section is missing (it must contain 'stacks' and optionally 'git_repository')` |
| `stack-missing-name.yaml` | `stack #1: every stack needs a 'name'` |
| `manifest-missing-name.yaml` | `stack "logging": manifest #1: every manifest needs a 'name'` |
| `manifest-no-source.yaml` | `stack "logging": manifest "namespace-fluent-bit": a manifest must set either 'manifest' (inline YAML) or 'from_file' (path to a YAML file)` |
| `manifest-missing-file.yaml` | `stack "logging": manifest "namespace-fluent-bit": could not read the file referenced by 'from_file' ("..."): ... no such file or directory` |
| `addon-missing-name.yaml` | `stack "logging": addon #1: every addon needs a 'name'` |
| `stack-without-manifests-key.yaml` | Passes validation. A stack that omits the `manifests` key (and likewise `addons`) is handled gracefully — `cmd/apply.go` guards the map access, so no panic. |

`manifests/namespace.yaml` is the Kubernetes manifest referenced by
`valid-cluster.yaml` via `from_file`.

## Dependency-tree validation

After the structural checks above, `--dry-run` also validates the parent
(`parents:`) graph across the whole document, offline. This catches the
dependency errors the backend would otherwise only reject at apply time
(HTTP 422). Three rules are enforced:

1. **Parent kind** — each parent `kind` must be `manifest` or `addon`:
   `addon "cert-manager" in stack "apps": parent #1 has invalid kind "stack" (must be "manifest" or "addon")`
2. **Parent existence** — each parent must name a manifest or addon declared
   anywhere in the same document (cross-stack references are allowed):
   `addon "cert-manager" in stack "apps": parent manifest "namespace" is not defined anywhere in this file`
3. **Acyclic graph** — the parent edges must not form a cycle (a resource
   depending on itself counts):
   `dependency cycle detected: addon "a" -> addon "b" -> addon "a"`

These checks run for both `ankra cluster apply` and `ankra cluster apply --dry-run`.

## Referenced-file validation

**Every file reference in the document is resolved and checked**, regardless of
whether its content is ultimately used:

- `manifest.from_file` and inline `manifest` — resolved and parsed as YAML
  (multi-document `---` files supported).
- `addon.configuration.from_file` and inline `configuration.values` — resolved
  and parsed as YAML.
- `stack.description_from_file` — resolved and read (a plain-text description,
  so existence/readability only). This is validated even when an inline
  `description` is also set and takes precedence for the value.

Errors name the resolved file and the problem:

```
stack "logging": manifest "broken": the file referenced by 'from_file' ("/abs/path/broken.yaml") is not valid YAML: yaml: line 2: found a tab character that violates indentation
```
