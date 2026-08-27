# Writing a module for `ankra migrate`

`ankra migrate convert` turns an existing deployment description into an
ImportCluster manifest plus the Kubernetes manifests its stack refers to. The
`docker` module is built in; every other format is a module you can write in
any language, because a module is just an executable on `PATH`.

## The contract

Name the executable `ankra-module-<name>` and put it in `~/.ankra/modules` or
anywhere on `PATH`. It must answer three verbs. Each one gets a JSON request
on stdin and must write a JSON reply on stdout; exit non-zero with the reason
on stderr to fail a verb.

### `describe`

No input. Reply with what the module is:

```json
{"name": "procfile", "version": "1.0", "protocol": 1,
 "summary": "Heroku-style Procfile", "file_patterns": ["Procfile"]}
```

`protocol` is the wire protocol version this document describes (`1`); the
CLI refuses a module that speaks one it does not know. `file_patterns` are
shown to users so an empty detection is explainable.

### `detect`

Input `{"dir": "/absolute/path"}`. Reply with a confidence from 0 (not
mine) to 1 (certain), the files found, and a one-line reason - including
for a zero score:

```json
{"confidence": 1, "files": ["Procfile"], "reason": "Procfile present"}
```

Reserve 1 for an unambiguous marker file. A module working from a heuristic
should score itself lower so a more specific module wins the directory.

### `convert`

Input:

```json
{"dir": "/absolute/path", "cluster_name": "shop", "namespace": "shop",
 "options": {"image": "ghcr.io/org/shop:1.2"}}
```

`options` carries every `--option key=value` the user passed, untouched, so
a module can take input the CLI knows nothing about. Reply with the
resources:

```json
{"cluster": { ...an ImportCluster manifest... },
 "files": {"manifests/web.yaml": "apiVersion: apps/v1\n..."},
 "warnings": ["web listens on 8080; put an Ingress in front of it"]}
```

- `cluster` is written to `cluster.yaml`. Its stack's `manifests[].from_file`
  entries must name keys of `files`.
- `files` keys are paths relative to the output directory; absolute paths
  and `..` are rejected before anything is written.
- `warnings` are for everything the module could not translate faithfully -
  an unmappable construct, a value the user has to supply, a credential
  written in plain text. A partial conversion a person can finish beats
  none, so warn rather than fail wherever you can.

Time limits: 15s for `describe`, 30s for `detect`, 5 minutes for `convert`.

## Rules worth knowing

- **Never write to the source directory.** Convert reads; the CLI writes.
- **Be deterministic.** Sort everything. Output that reorders itself on each
  run cannot be reviewed in a pull request.
- **Keep secrets out of ConfigMaps.** Put credentials in a `Secret` and warn
  that it needs encrypting (`ankra cluster encrypt`) before it is committed.
- **Encode dependency order as `parents`.** A workload that must start after
  another lists it under `parents: [{name, kind: manifest}]`; that is how a
  source format's start order survives the conversion.
- **Set `enableServiceLinks: false`** on pods. Kubernetes injects
  `<SERVICE>_PORT=tcp://...` for every Service in the namespace, and
  applications that read a variable of the same name break on it.

## Reference implementations

- `ankra-module-procfile` in this directory: a complete module in Python,
  about a hundred lines.
- The built-in `docker` module (`internal/migrate/docker` in the CLI
  repository) is the same contract implemented in-process, and shows the
  full treatment: profiles, variable interpolation, bind mounts, healthchecks,
  resource limits, and reading the live daemon.

Try the example:

```sh
cp ankra-module-procfile ~/.ankra/modules/
printf 'web: bundle exec puma\nworker: bundle exec sidekiq\n' > /tmp/demo/Procfile
ankra migrate detect /tmp/demo
ankra migrate convert /tmp/demo --option image=ghcr.io/org/app:1.0 --dry-run
```
