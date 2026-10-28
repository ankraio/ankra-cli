# Re-recording the demo GIFs

`docs/demo.gif` and `docs/demo-troubleshoot.gif` are generated, not
hand-recorded. To refresh them after a feature change:

```sh
brew install asciinema agg jq   # once; perl and expect ship with macOS
sh docs/demo/record.sh          # both scenes
sh docs/demo/record.sh platform # or just one
```

That builds `ankra` from the current checkout, starts the mock API in
`mockapi/`, records the session headless with asciinema, and renders the GIFs
with agg (134x40, `asciinema` theme). Nothing outside the temp dir and
`docs/*.gif` is touched.

**The recording never talks to the Ankra platform.** It runs against a local
mock over loopback with a throwaway `HOME`, so anyone can re-record it with no
account, no token and no network - and no customer data can end up in a GIF.
Every name, cluster and answer in the demo is fabricated.

## What each file does

| File | Role |
| --- | --- |
| `record.sh` | Entry point: builds, serves, records, renders. |
| `driver.sh` | Runs *inside* the recording. Fake-types each command, then runs it for real. One shell function per scene. |
| `platform.yaml` | The ImportCluster spec the demo validates and applies. Real, and really applied. |
| `mockapi/` | The offline Ankra API. ~8 routes, written against `internal/client` structs. |

## Changing what the demo shows

- **New command in an existing scene**: add a `run '...'` line to the scene
  function in `driver.sh`. `run` types the command, executes it, and aborts the
  recording if it exits non-zero - a GIF of an error is worse than no GIF, and
  a missing mock route otherwise renders as a plausible-looking empty table.
- **New scene**: add a function and a `case` arm in `driver.sh`, then map it to
  an output path in `gif_for_scene` in `record.sh`.
- **New endpoint**: add a route in `mockapi/main.go`. Build responses from the
  `client.*` structs rather than raw JSON, so a field rename in the API
  contract breaks `go build ./...` instead of silently producing a demo that
  records output the CLI can no longer parse. Unrouted paths log `UNHANDLED`
  and return 404 on purpose.
- **Pacing**: `beat <seconds>` between commands, `TYPING_DELAY` for the typing
  speed. Keep it generous - viewers read slower than scripts type.

## Hard-won gotchas - read before debugging

These cost real time to discover; don't rediscover them.

1. **A saved `~/.ankra.yaml` silently wins over the environment.** With a login
   token on disk, `resolveCredentials` (`cmd/root.go`) ignores
   `ANKRA_API_TOKEN` *and* `ANKRA_BASE_URL` - it only prints a note on stderr.
   On a machine where someone has run `ankra login`, a demo that sets env vars
   alone would quietly record against the **real platform**. The throwaway
   `HOME` in `record.sh` is what prevents that; do not "simplify" it away.

2. **A shell typing loop types at fork speed, not at the delay you set.**
   Spawning `sleep` per character costs ~45ms on macOS whatever value you pass,
   so lowering the delay changes nothing and the scene runs ~60% longer than
   the pacing implies. `type_cmd` does the whole per-character delay inside one
   `perl` process instead. (Peeling characters with `cut` in a subshell is the
   same trap, twice over.)

3. **`--watch` only clears the screen on a real terminal.** `clearScreen`
   (`cmd/cluster_operation.go`) stats stdout for `os.ModeCharDevice` and skips
   the escape when it is not a character device. Recording through a pipe
   instead of asciinema's pty turns the animated dashboard into an
   ever-growing log.

4. **The watch ends itself, and the mock is what decides when.** It stops once
   every execution reaches a terminal status. The last entry of each
   `succeeded` slice in `demoExecutions` must equal that execution's `total`,
   or the recording runs forever.

5. **Each scene needs a fresh mock process.** The executions script advances
   one step per poll, so a server reused across scenes starts at "everything
   already succeeded" and the watch ends before it animates.

6. **Table widths come from `WidthMin`, not from the content.** `operations
   list` is 180 columns wide even when every cell is short, which is why the
   demo watches a single execution by id rather than listing them. 134x40 is
   the smallest window where `cat platform.yaml` and its validation still land
   in one frame - shrink the terminal and the top of the spec scrolls away.

7. **Consecutive chat `status` frames run together.** Before any content
   arrives the CLI prints each one as a bare `[...]` with no separator, so two
   in a row render as `[one][two]` on a single line. The script sends one.

8. **Without `--wait`, `apply` requires exactly 202.** `parseAsyncWriteResponse`
   treats every other 2xx as an error, so a mock that helpfully returns 200
   fails the command.
