#!/bin/sh
# Re-record the README demo GIFs. Requires: go, asciinema (>= 3), agg, jq.
#
#   sh docs/demo/record.sh                 # every scene
#   sh docs/demo/record.sh troubleshoot    # just one
#
# Builds the CLI from this checkout, starts the offline mock API from
# docs/demo/mockapi, records the driver against it, and renders GIFs into
# docs/. Nothing outside the temp dir and docs/*.gif is touched, and nothing
# leaves the machine: no account, no token, no network.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)

# Scene -> output GIF. Add a scene in driver.sh, add its mapping here.
gif_for_scene() {
  case "$1" in
    platform)     echo "$repo/docs/demo.gif" ;;
    troubleshoot) echo "$repo/docs/demo-troubleshoot.gif" ;;
    *)            return 1 ;;
  esac
}

scenes=${*:-"platform troubleshoot"}
for scene in $scenes; do
  gif_for_scene "$scene" >/dev/null || { printf 'unknown scene: %s\n' "$scene" >&2; exit 2; }
done

for tool in go asciinema agg jq perl; do
  command -v "$tool" >/dev/null 2>&1 || {
    printf 'missing required tool: %s\n' "$tool" >&2
    printf 'install with: brew install asciinema agg jq\n' >&2
    exit 1
  }
done

work=$(mktemp -d)
mock_pid=""
cleanup() {
  [ -n "$mock_pid" ] && kill "$mock_pid" 2>/dev/null
  rm -rf "$work"
}
trap cleanup EXIT INT TERM

printf 'building ankra and the demo mock...\n'
mkdir -p "$work/bin"
go build -o "$work/bin/ankra" "$repo"
go build -o "$work/bin/mockapi" "$repo/docs/demo/mockapi"

cp "$here/platform.yaml" "$work/platform.yaml"

# start_mock runs a fresh server per scene. The executions script advances one
# step per poll, so a reused server would already be at "everything succeeded"
# on a second recording and the watch would end before it animated.
start_mock() {
  rm -f "$work/url"
  "$work/bin/mockapi" -url-file "$work/url" >"$work/mock.log" 2>&1 &
  mock_pid=$!

  waited=0
  while [ ! -s "$work/url" ]; do
    sleep 0.1
    waited=$((waited + 1))
    if [ "$waited" -gt 100 ]; then
      printf 'mock API did not start within 10s; log follows:\n' >&2
      cat "$work/mock.log" >&2
      exit 1
    fi
  done
  base_url=$(cat "$work/url")
}

stop_mock() {
  [ -n "$mock_pid" ] && kill "$mock_pid" 2>/dev/null
  mock_pid=""
}

for scene in $scenes; do
  gif=$(gif_for_scene "$scene")
  printf 'recording scene %s -> %s\n' "$scene" "${gif#"$repo"/}"

  start_mock

  # A saved ~/.ankra.yaml takes precedence over ANKRA_API_TOKEN and silently
  # ignores ANKRA_BASE_URL (cmd/root.go resolveCredentials), so a real login on
  # the recording machine would point the demo at the real platform. An empty
  # HOME is what keeps the run hermetic - do not drop it.
  rm -rf "$work/home"
  mkdir -p "$work/home"

  demo_env="HOME=$work/home \
    ANKRA_API_TOKEN=demo-token \
    ANKRA_BASE_URL=$base_url \
    FORCE_COLOR=1 \
    PATH=$work/bin:$PATH"

  # Select the active cluster off-camera. The platform scene selects it again
  # on camera (it is part of that story); the troubleshoot scene needs it
  # already set so it can open on the question instead of on setup.
  (cd "$work" && env $demo_env ankra cluster select staging-eu >/dev/null)

  (cd "$work" && env $demo_env TERM=xterm-256color COLORTERM=truecolor \
    asciinema rec --headless --window-size 134x40 \
      -c "sh $here/driver.sh $scene" \
      -q --overwrite --return "$work/$scene.cast")

  stop_mock

  agg "$work/$scene.cast" "$gif" \
    --font-size 14 \
    --theme "${DEMO_THEME:-asciinema}" \
    --fps-cap 20 \
    --last-frame-duration 4
  printf 'wrote %s (%s)\n' "${gif#"$repo"/}" "$(du -h "$gif" | cut -f1 | tr -d ' ')"
done
