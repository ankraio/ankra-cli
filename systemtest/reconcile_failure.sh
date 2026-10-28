# shellcheck shell=bash
#
# reconcile_failure.sh - sourced by lifecycle_systemtest.sh (and by its Go
# tests). Decides whether a failed reconcile is worth retrying.
#
# The wait helpers used to nudge a new reconcile whenever the cluster's
# operations listed a failed one, on the theory that it was a slow boot or a
# timeout. A step the provider refused outright fails the same way every time,
# and the systest runs against PRODUCTION: every nudged retry of such a step is
# another production ERROR. On 2026-10-02 the uks:managed lane retried a
# deterministic UpCloud 422 "Cluster network is required." 17 times in 25
# minutes (ankra-rvpwz). So before nudging, the newest failed reconcile's
# failed steps are read and classified: a provider 4xx or validation refusal
# fails the wait on the first attempt; anything else (5xx, timeouts, agent
# offline, an error we cannot classify) keeps the retry.
#
# Expects the caller to define ank, ank_out and select_cluster (see
# lifecycle_systemtest.sh). Reading the execution JSON needs jq; without it
# the helpers fall back to the old behaviour (every failure retried).

# 4xx statuses a retry can legitimately clear: request timeout, conflict
# (the platform or provider is busy with another write), locked, too early,
# rate limited. Every other 4xx is the provider refusing the request itself.
RECONCILE_RETRYABLE_4XX="${RECONCILE_RETRYABLE_4XX:-408 409 423 425 429}"

# classify_step_error <error text>
# Prints "terminal" when the text is a refusal a retry repeats unchanged - a
# provider/API 4xx outside RECONCILE_RETRYABLE_4XX, or a validation refusal -
# and "transient" otherwise. Timeouts and 5xx win over everything else, so an
# ambiguous excerpt keeps the retry.
classify_step_error() {
  local lower code
  lower="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"
  # A status in the forms the platform's errors carry it: "uks API 422: ...",
  # "HTTP 403", "status 404", "status code 400", "status_code=401",
  # "\"status\":422". Bash ERE has no \b, so the trailing boundary is spelled out.
  local status_re='(api|http|status([ _]?code)?)"?[ :=]*([0-9][0-9][0-9])([^0-9]|$)'
  local transient_re='timed? ?out|timeout|deadline exceeded|connection (refused|reset)|temporarily unavailable|service unavailable|agent (is )?(offline|disconnected|not connected)|no such host|i/o timeout'
  local validation_re='validation (failed|error)|invalid_input|invalid_request|unprocessable entity'

  if [[ $lower =~ $transient_re ]]; then
    echo transient; return 0
  fi
  local rest="$lower" saw_terminal=0
  while [[ $rest =~ $status_re ]]; do
    code="${BASH_REMATCH[3]}"
    case "$code" in
      5??) echo transient; return 0 ;;
      4??)
        case " $RECONCILE_RETRYABLE_4XX " in
          *" $code "*) echo transient; return 0 ;;
          *) saw_terminal=1 ;;
        esac
        ;;
    esac
    rest="${rest#*"${BASH_REMATCH[0]}"}"
  done
  if [ "$saw_terminal" = 1 ] || [[ $lower =~ $validation_re ]]; then
    echo terminal; return 0
  fi
  echo transient
}

# jq: from `cluster operations list -o json` (newest first), the newest
# failed reconcile and whether any reconcile has started since it.
# shellcheck disable=SC2016 # a jq program
RECONCILE_LIST_JQ='
  [ .[]? | select(((.display_name // "") + " " + (.name // "")) | test("reconcile"; "i")) ] as $reconciles
  | ([ $reconciles[] | select((.status // "") | test("failed|critical|timeout|timed"; "i")) ] | first) as $failed
  | if $failed == null then "none"
    elif $reconciles[0].id == $failed.id then "latest \($failed.id)"
    else "superseded \($failed.id)" end'

# jq: from `cluster operations steps <id> -o json`, one "<step>\t<error>"
# line per failed step (the execution-level excerpt when no step carries one).
# shellcheck disable=SC2016 # a jq program
RECONCILE_STEPS_JQ='
  def flat: gsub("[\t\r\n]+"; " ");
  [ .steps[]? | select((.status // "") | test("failed|critical|timeout|timed"; "i"))
    | select((.error_excerpt // "") != "")
    | "\(.name // "step")\t\(.error_excerpt | flat)" ] as $lines
  | if ($lines | length) > 0 then $lines[]
    elif ((.execution.error_excerpt // "") != "") then "execution\t\(.execution.error_excerpt | flat)"
    else empty end'

# reconcile_failure_verdict <cluster name>
# Prints one line:
#   none                         no reconcile in the recent operations failed
#   transient                    one failed and a retry may clear it
#   terminal<TAB><step><TAB><error>
#                                the newest reconcile failed on a refusal a
#                                retry repeats (nothing newer has started)
reconcile_failure_verdict() {
  local name="$1" listing found kind execution_id steps step error
  select_cluster "$name"
  if ! command -v jq >/dev/null 2>&1; then
    if ank cluster operations list | grep -iE "Reconcile" | grep -iE "failed|timed out" >/dev/null; then
      echo transient
    else
      echo none
    fi
    return 0
  fi
  listing="$(ank_out cluster operations list -o json)" || { echo none; return 0; }
  found="$(printf '%s' "$listing" | jq -r "$RECONCILE_LIST_JQ" 2>/dev/null)" || { echo none; return 0; }
  kind="${found%% *}"
  execution_id="${found#* }"
  case "$kind" in
    latest) ;;
    superseded) echo transient; return 0 ;;
    *) echo none; return 0 ;;
  esac
  # A failure a later reconcile already superseded keeps the old retry; only
  # the newest reconcile decides whether the next one would fail the same way.
  steps="$(ank_out cluster operations steps "$execution_id" -o json \
    | jq -r "$RECONCILE_STEPS_JQ" 2>/dev/null)" || { echo transient; return 0; }
  while IFS=$'\t' read -r step error; do
    [ -z "$error" ] && continue
    if [ "$(classify_step_error "$error")" = terminal ]; then
      printf 'terminal\t%s\t%s\n' "$step" "$error"
      return 0
    fi
  done <<<"$steps"
  echo transient
}

nudge_reconcile() { select_cluster "$1"; ank cluster reconcile >/dev/null 2>&1 || true; }

# retry_failed_reconcile <cluster name> <log text for a retry>
# Called from the wait loops on every poll. Nudges a new reconcile when the
# last one failed transiently and returns 0 (keep waiting). Returns 1 without
# nudging when the newest reconcile failed on a terminal refusal, so the
# caller fails the step now instead of re-running it until its timeout.
retry_failed_reconcile() {
  local name="$1" retry_text="$2" verdict step error
  if [ -z "${RECONCILE_JQ_NOTED:-}" ] && ! command -v jq >/dev/null 2>&1; then
    RECONCILE_JQ_NOTED=1
    log "  note: jq is not installed, so a failed reconcile cannot be classified and every failure is retried"
  fi
  verdict="$(reconcile_failure_verdict "$name")"
  case "$verdict" in
    terminal*)
      IFS=$'\t' read -r _ step error <<<"$verdict"
      log "  $name reconcile failed at step $step on a refusal a retry repeats -> not retrying: $error"
      return 1
      ;;
    transient)
      log "  $name $retry_text -> retrying"
      nudge_reconcile "$name"
      ;;
  esac
  return 0
}
