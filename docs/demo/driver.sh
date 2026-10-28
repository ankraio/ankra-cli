#!/bin/sh
# Runs inside the asciinema recording session. Fake-types each command, then
# runs it for real against the mock API from docs/demo/mockapi.
#
# Expects: `ankra` on PATH, cwd containing platform.yaml, and the ANKRA_*
# environment already pointed at the mock (record.sh does all of that).
#
# $1 selects the scene: platform | troubleshoot.
set -u

# type_cmd fake-types a command at a green prompt, one character at a time, so
# the recording reads like someone using the CLI rather than a script dumping
# output. It only draws the line; the caller runs the real thing.
#
# The per-character delay runs inside one perl process rather than a shell loop
# calling sleep(1). Spawning a process per character costs ~45ms on macOS
# whatever delay you ask for, so a shell loop types at a speed set by fork
# overhead and TYPING_DELAY below would be decorative - lower it and nothing
# changes. One process makes the number mean what it says.
TYPING_DELAY=${TYPING_DELAY:-0.026}

type_cmd() {
  printf '\033[1;32m❯\033[0m '
  printf '%s' "$1" | perl -se '
    $| = 1;
    while (defined(my $char = getc(STDIN))) {
      print $char;
      select(undef, undef, undef, $delay);
    }
  ' -- -delay="$TYPING_DELAY"
  sleep 0.35
  printf '\n'
}

# run type-and-execute in one step, and abort the recording if the command
# fails. A demo that records an error is worse than no demo, and without this
# a broken mock route renders as a plausible-looking empty table.
run() {
  type_cmd "$*"
  if ! eval "$*"; then
    printf '\n\033[1;31mdemo aborted: command failed: %s\033[0m\n' "$*" >&2
    exit 1
  fi
}

beat() { sleep "$1"; }

scene_platform() {
  beat 0.6
  run 'ankra cluster list'
  beat 2.4

  run 'ankra cluster select staging-eu'
  beat 1.4

  run 'cat platform.yaml'
  beat 3.4

  run 'ankra cluster validate -f platform.yaml'
  beat 2.0

  run 'ankra cluster apply -f platform.yaml --wait'
  beat 2.6

  # Sources the execution id the next command watches, and shows the
  # structured-output contract: stdout stays parseable, hints go to stderr.
  run "ankra cluster operations list -o json | jq -r '.[] | \"\\(.id)  \\(.status|ascii_upcase)  \\(.display_name)\"'"
  beat 2.2

  # Self-terminating: the watch stops on its own once every execution reaches
  # a terminal state, so the scene needs no Ctrl+C.
  run 'ankra cluster operations list op-8f21c4 --watch --interval 1s'
  beat 2.2

  # Closes the loop back to platform.yaml, and gives the scene a final frame
  # worth freezing on: the watch clears the screen, so ending there would
  # leave the GIF resting on an almost empty terminal.
  run 'ankra cluster stacks list'
  beat 3.0
}

scene_troubleshoot() {
  # The active cluster is selected off-camera by record.sh so the scene opens
  # on the question rather than on setup.
  beat 0.6
  run 'ankra cluster get pods -n observability'
  beat 3.0

  run 'ankra chat "why is loki-0 crashlooping?"'
  beat 3.0
}

case "${1:-platform}" in
  platform)     scene_platform ;;
  troubleshoot) scene_troubleshoot ;;
  *)            printf 'unknown scene: %s\n' "$1" >&2; exit 2 ;;
esac
