#!/usr/bin/env bash
#
# flow_regression.sh
#
# The whole developer flow, from a repository to production, driven through the
# ankra CLI against a live platform, the way a person in a terminal does it. See
# README.md "Flow regression". This is the CLI lane of ankra-oznf7k.19.
#
# It proves, end to end and on every run:
#   gate blocked              the image gate stops the fixture's pinned critical CVE
#   fix landed with lockfile  Ankra AI's fix changes package.json AND the lockfile
#   HTTPS on IPv4 and IPv6    the deployed address answers on both
#   data survives a restart   a row written before the restart reads back after it
#   Slack alert delivered     the block reaches the channel, read back from Slack
#
# It creates real clusters, so it costs real money. It never runs against
# production, and it removes everything it created on every exit, then checks
# that it did: a leak fails the run even when every assertion passed.
#
# Required:
#   FLOW_REGRESSION_CONFIRM=yes       acknowledges real cost
#   ANKRA_BASE_URL                    the platform; production is refused
#   ANKRA_API_TOKEN                   a token of FLOW_ORGANISATION_ID
#   FLOW_ORGANISATION_ID              the dedicated regression organisation
#   FLOW_CREDENTIAL_ID                Ankra Cloud credential, in that organisation
#   FLOW_SSH_KEY_CREDENTIAL_ID        SSH key credential, in that organisation
#   FLOW_ZONE, FLOW_PLAN              where and what size to build
#   FLOW_FIXTURE_REPOSITORY           owner/name, must end in -fixture
#   GH_TOKEN                          reads and resets that repository
#   FLOW_ALERT_DESTINATION_ID         a Slack destination already connected
#   FLOW_SLACK_CHANNEL_ID             the channel it posts to
#   SLACK_BOT_TOKEN                   reads that channel back
# Optional:
#   FLOW_FAILURE_WEBHOOK_URL          told when a run fails (#infra-errors)
#   FLOW_PLAN_ONLY=1                  check the settings and stop; creates nothing
#   FLOW_REGRESSION_TIMEOUT           ship's bound on each wait (default 45m)

set -uo pipefail

ANKRA_BIN="${ANKRA_BIN:-ankra}"
RUN_ID="${FLOW_RUN_ID:-$(date -u +%Y%m%d%H%M%S)}"
PREFIX="flow-"
STAGING="${PREFIX}staging-${RUN_ID}"
PRODUCTION="${PREFIX}production-${RUN_ID}"
APPLICATION="${PREFIX}app-${RUN_ID}"
NAMESPACE="$APPLICATION"
ALERT_START="$(date -u +%s)"
TIMEOUT="${FLOW_REGRESSION_TIMEOUT:-45m}"

FAILURES=0
LEAKS=0
APPLICATION_ID=""
ROUTE_ID=""
WORK_DIRECTORY=""
FINISHED=0

# The rows of a list answer, whichever envelope the command prints it in.
JQ_ROWS='def rows: if type == "array" then . else (.result // .clusters // .applications // .routes // .items // []) end; rows[]?'

log() { printf '%s [flow] %s\n' "$(date +%H:%M:%S)" "$*"; }
pass() { log "PASS: $1"; }
fail() {
  log "FAIL: $1"
  FAILURES=$((FAILURES + 1))
}

# What refuses the run before anything is created. Pure, so a test can read it.
preflight_problems() {
  local name
  [ "${FLOW_REGRESSION_CONFIRM:-}" = "yes" ] || echo "FLOW_REGRESSION_CONFIRM=yes is required: this run creates real clusters"
  case "${ANKRA_BASE_URL:-}" in
    "") echo "ANKRA_BASE_URL is required: this run never falls back to a default platform" ;;
    *platform.ankra.app* | *ankra.ai*) echo "ANKRA_BASE_URL is production ($ANKRA_BASE_URL): this run never goes there" ;;
  esac
  for name in ANKRA_API_TOKEN FLOW_ORGANISATION_ID FLOW_CREDENTIAL_ID FLOW_SSH_KEY_CREDENTIAL_ID FLOW_ZONE FLOW_PLAN \
    FLOW_FIXTURE_REPOSITORY GH_TOKEN FLOW_ALERT_DESTINATION_ID FLOW_SLACK_CHANNEL_ID SLACK_BOT_TOKEN; do
    [ -n "${!name:-}" ] || echo "$name is not set"
  done
  case "${FLOW_FIXTURE_REPOSITORY:-}" in
    "" | *-fixture) ;;
    *) echo "FLOW_FIXTURE_REPOSITORY ($FLOW_FIXTURE_REPOSITORY) must end in -fixture: this run force-pushes to it" ;;
  esac
  for name in "$ANKRA_BIN" jq gh git curl; do
    command -v "$name" >/dev/null 2>&1 || echo "$name is not on PATH"
  done
}

ank() { "$ANKRA_BIN" "$@"; }

# Wait until a command prints something, every 15 seconds, up to a limit in seconds.
wait_for() {
  local limit="$1" description="$2" elapsed=0 answer
  shift 2
  while [ "$elapsed" -lt "$limit" ]; do
    answer="$("$@" 2>/dev/null)" && [ -n "$answer" ] && {
      printf '%s' "$answer"
      return 0
    }
    sleep "${FLOW_POLL_SECONDS:-15}"
    elapsed=$((elapsed + ${FLOW_POLL_SECONDS:-15}))
  done
  log "gave up waiting for $description after ${limit}s"
  return 1
}

# Everything this run made, removed in reverse. Runs on every exit, and on a run that died
# early removes what got created. Each step is independent: a failed one does not stop the next.
teardown() {
  local cluster clusters
  log "teardown: removing what run $RUN_ID created"
  if [ -n "$ROUTE_ID" ]; then
    ank alerts routes delete "$ROUTE_ID" --yes >/dev/null 2>&1 || log "teardown: could not delete route $ROUTE_ID"
  fi
  if [ -n "$APPLICATION_ID" ]; then
    ank application delete "$APPLICATION_ID" --yes >/dev/null 2>&1 || log "teardown: could not delete application $APPLICATION_ID"
  fi
  # By name as well as by id: a create that failed on its way back may still have made the cluster.
  clusters="$(ank cluster list -o json 2>/dev/null |
    jq -r --arg run "$RUN_ID" "$JQ_ROWS | select((.name // \"\") | endswith(\$run)) | .id" 2>/dev/null)"
  clusters="$(printf '%s\n%s\n' "$(cat "$WORK_DIRECTORY/created-clusters" 2>/dev/null)" "$clusters" | grep . | sort -u)"
  for cluster in $clusters; do
    # Disposable, volumes included: graceful first, then force, as lifecycle_systemtest.sh does.
    ank cluster deprovision "$cluster" --yes --accept-volume-data-loss >/dev/null 2>&1 ||
      ank cluster deprovision "$cluster" --force --yes --accept-volume-data-loss >/dev/null 2>&1 ||
      log "teardown: could not deprovision $cluster"
  done
  close_fixture_pull_requests
}

close_fixture_pull_requests() {
  [ -n "${FLOW_FIXTURE_REPOSITORY:-}" ] || return 0
  gh pr list -R "$FLOW_FIXTURE_REPOSITORY" --state open --json number --jq '.[].number' 2>/dev/null |
    while read -r number; do
      gh pr close "$number" -R "$FLOW_FIXTURE_REPOSITORY" --delete-branch >/dev/null 2>&1 || true
    done
}

# What is still there under this run's names. Empty means nothing leaked. Clusters take time to
# go, so this waits for them; anything else left after the wait is a leak.
remaining_resources() {
  local answer rows
  # A list that cannot be read proves nothing, so it counts as left behind, never as clean.
  if answer="$(ank cluster list -o json 2>/dev/null)" && rows="$(jq -r --arg run "$RUN_ID" \
    "$JQ_ROWS | select((.name // \"\") | endswith(\$run)) | \"cluster \\(.name)\"" <<<"$answer" 2>/dev/null)"; then
    [ -z "$rows" ] || printf '%s\n' "$rows"
  else
    echo "clusters: could not read the list"
  fi
  if answer="$(ank application list -o json 2>/dev/null)" && rows="$(jq -r --arg run "$RUN_ID" \
    "$JQ_ROWS | select((.name // \"\") | endswith(\$run)) | \"application \\(.name)\"" <<<"$answer" 2>/dev/null)"; then
    [ -z "$rows" ] || printf '%s\n' "$rows"
  else
    echo "applications: could not read the list"
  fi
  [ -n "$ROUTE_ID" ] || return 0
  if answer="$(ank alerts routes list -o json 2>/dev/null)" && rows="$(jq -r --arg id "$ROUTE_ID" \
    "$JQ_ROWS | select(.id == \$id) | \"route \\(.id)\"" <<<"$answer" 2>/dev/null)"; then
    [ -z "$rows" ] || printf '%s\n' "$rows"
  else
    echo "alert routes: could not read the list"
  fi
}

leak_check() {
  local elapsed=0 limit="${FLOW_LEAK_WAIT_SECONDS:-1800}" left=""
  while :; do
    left="$(remaining_resources)"
    [ -z "$left" ] && {
      pass "nothing left behind by run $RUN_ID"
      return 0
    }
    [ "$elapsed" -ge "$limit" ] && break
    sleep "${FLOW_POLL_SECONDS:-15}"
    elapsed=$((elapsed + ${FLOW_POLL_SECONDS:-15}))
  done
  log "LEAK: still present after ${limit}s:"
  printf '%s\n' "$left"
  LEAKS=$((LEAKS + 1))
  return 1
}

report_failure() {
  [ -n "${FLOW_FAILURE_WEBHOOK_URL:-}" ] || return 0
  local text="Flow regression run $RUN_ID failed on ${ANKRA_BASE_URL:-}: $FAILURES assertion(s), $LEAKS leak(s)"
  curl --silent --show-error --max-time 30 -X POST -H 'Content-Type: application/json' \
    -d "$(jq -n --arg text "$text" '{text: $text}')" "$FLOW_FAILURE_WEBHOOK_URL" >/dev/null ||
    log "could not post the failure"
}

finish() {
  local status=$?
  [ "$FINISHED" = "1" ] && return
  FINISHED=1
  trap - EXIT INT TERM
  teardown
  leak_check || true
  [ -z "$WORK_DIRECTORY" ] || rm -rf "$WORK_DIRECTORY"
  [ "$status" -eq 0 ] || FAILURES=$((FAILURES + 1))
  if [ "$FAILURES" -gt 0 ] || [ "$LEAKS" -gt 0 ]; then
    log "RESULT: FAILED, $FAILURES assertion(s), $LEAKS leak(s)"
    report_failure
    exit 1
  fi
  log "RESULT: PASSED"
  exit 0
}

create_cluster() {
  local name="$1" answer id
  answer="$(ank cluster ankracloud create --name "$name" --credential-id "$FLOW_CREDENTIAL_ID" \
    --ssh-key-credential-id "$FLOW_SSH_KEY_CREDENTIAL_ID" --zone "$FLOW_ZONE" \
    --bastion-plan "$FLOW_PLAN" --control-plane-plan "$FLOW_PLAN" --worker-plan "$FLOW_PLAN" -o json)" || return 1
  id="$(jq -r '.cluster_id // .id // empty' <<<"$answer")"
  [ -n "$id" ] || return 1
  # A file, not a variable: the caller runs this in a command substitution, whose variables die with it.
  printf '%s\n' "$id" >>"$WORK_DIRECTORY/created-clusters"
  printf '%s' "$id"
}

cluster_online() {
  ank cluster list -o json 2>/dev/null |
    jq -r --arg id "$1" "$JQ_ROWS | select(.id == \$id and ((.state // .status) == \"online\")) | \"online\""
}

# The newest run of the pull request's head, once it has concluded.
head_run() { ank pipeline get --application "$APPLICATION" --head-sha "$1" --latest --wait --timeout "$TIMEOUT" -o json; }

setup_pull_request() {
  gh pr list -R "$FLOW_FIXTURE_REPOSITORY" --state open --json number,headRefName,headRefOid --jq '.[0] // empty'
}

reset_fixture() {
  git clone --quiet "https://x-access-token:${GH_TOKEN}@github.com/${FLOW_FIXTURE_REPOSITORY}.git" "$WORK_DIRECTORY/fixture" || return 1
  close_fixture_pull_requests
  git -C "$WORK_DIRECTORY/fixture" push --quiet --force origin origin/pristine:refs/heads/main || return 1
  git -C "$WORK_DIRECTORY/fixture" fetch --quiet origin main || return 1
  git -C "$WORK_DIRECTORY/fixture" checkout --quiet -B main origin/main
}

main() {
  local problems staging_id production_id pull_request head_ref head_sha run answer action findings address title
  local changed production_address
  problems="$(preflight_problems)"
  if [ -n "$problems" ]; then
    printf 'Refused:\n%s\n' "$problems" >&2
    exit 2
  fi
  if [ "${FLOW_PLAN_ONLY:-}" = "1" ]; then
    log "plan only: settings hold, nothing created. Would build $STAGING and $PRODUCTION and ship $APPLICATION."
    exit 0
  fi
  WORK_DIRECTORY="$(mktemp -d)"
  trap finish EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  export ANKRA_ORG="$FLOW_ORGANISATION_ID"

  reset_fixture || {
    fail "could not reset $FLOW_FIXTURE_REPOSITORY to its pristine branch"
    return 1
  }
  staging_id="$(create_cluster "$STAGING")" || {
    fail "could not create $STAGING"
    return 1
  }
  production_id="$(create_cluster "$PRODUCTION")" || {
    fail "could not create $PRODUCTION"
    return 1
  }
  wait_for 2400 "$STAGING online" cluster_online "$staging_id" >/dev/null || {
    fail "$STAGING did not come online"
    return 1
  }
  wait_for 2400 "$PRODUCTION online" cluster_online "$production_id" >/dev/null || {
    fail "$PRODUCTION did not come online"
    return 1
  }
  ank org ci-settings pool add "$staging_id" >/dev/null || fail "could not add $STAGING to the CI pool"
  ank cluster node-group add "$staging_id" --name sandbox --instance-type "$FLOW_PLAN" \
    --labels ankra.cloud/sandbox-runtime=gvisor >/dev/null || fail "could not add the sandbox node group"
  ank org ai-environment set --staging-cluster "$staging_id" >/dev/null || fail "could not set the AI environment"

  # The first ship stops at the unmerged setup pull request: that is expected, whichever way it says so.
  (cd "$WORK_DIRECTORY/fixture" && ank application ship . --name "$APPLICATION" --cluster "$staging_id" \
    --set-up-database --timeout "$TIMEOUT" -o json) >"$WORK_DIRECTORY/ship-first.json" 2>"$WORK_DIRECTORY/ship-first.log" || true
  APPLICATION_ID="$(ank application get "$APPLICATION" -o json 2>/dev/null | jq -r '.id // empty')"
  [ -n "$APPLICATION_ID" ] || {
    fail "ship did not register $APPLICATION"
    return 1
  }
  answer="$(ank alerts routes create --destination-id "$FLOW_ALERT_DESTINATION_ID" \
    --kinds pipeline_run_failed,image_gate_blocked --application "$APPLICATION" -o json)" || fail "could not create the alert route"
  ROUTE_ID="$(jq -r '.id // empty' <<<"${answer:-}")"

  pull_request="$(wait_for 900 "the setup pull request" setup_pull_request)" || {
    fail "no setup pull request opened"
    return 1
  }
  head_ref="$(jq -r .headRefName <<<"$pull_request")"
  head_sha="$(jq -r .headRefOid <<<"$pull_request")"

  run="$(head_run "$head_sha" | jq -r '.id // empty')"
  findings="$(ank pipeline findings "$run" --application "$APPLICATION" -o json 2>/dev/null)"
  if jq -e '(.findings // .)[]? | select((.severity // "" | ascii_upcase) == "CRITICAL")' <<<"$findings" >/dev/null; then
    pass "gate blocked: run $run has a critical finding"
  else
    fail "gate blocked: run $run has no critical finding, so the fixture no longer proves the gate"
  fi
  slack_received "$APPLICATION" && pass "Slack alert delivered" || fail "Slack alert delivered: nothing about $APPLICATION reached the channel"

  answer="$(ank chat --mode agent -o json "The image gate blocked run $run of application $APPLICATION. Fix the finding on branch $head_ref, with the lockfile.")"
  action="$(jq -r '.pending_actions[0].action_id // empty' <<<"${answer:-}")"
  [ -z "$action" ] || ank chat actions confirm "$action" >/dev/null || fail "could not confirm the fix"
  changed="$(wait_for 1200 "the fix on $head_ref" fix_files "$head_ref")"
  if grep -qx package.json <<<"$changed" && grep -qx package-lock.json <<<"$changed"; then
    pass "fix landed with lockfile"
  else
    fail "fix landed with lockfile: $head_ref changed [${changed:-nothing}]"
  fi

  head_sha="$(gh pr view "$head_ref" -R "$FLOW_FIXTURE_REPOSITORY" --json headRefOid --jq .headRefOid)"
  head_run "$head_sha" >/dev/null || fail "the run after the fix did not pass"
  gh pr merge "$head_ref" -R "$FLOW_FIXTURE_REPOSITORY" --squash >/dev/null || {
    fail "could not merge the setup pull request"
    return 1
  }

  (cd "$WORK_DIRECTORY/fixture" && git fetch --quiet origin main && git checkout --quiet -B main origin/main &&
    ank application ship . --name "$APPLICATION" --cluster "$staging_id" --set-up-database --timeout "$TIMEOUT" -o json) \
    >"$WORK_DIRECTORY/ship-second.json" || {
    fail "ship did not reach a running deployment"
    return 1
  }
  address="$(jq -r '.url // empty' "$WORK_DIRECTORY/ship-second.json")"
  [ -n "$address" ] || {
    fail "ship printed no address"
    return 1
  }
  assert_https "$address"

  title="flow-regression-$RUN_ID"
  assert_data_survives_restart "$address" "$title" "$staging_id"

  # A person promotes the tag staging proved; here that person is this run. It proves the verb, not the judgement.
  answer="$(ank application deployments "$APPLICATION_ID" -o json 2>/dev/null)"
  local tag
  tag="$(jq -r '[(if type == "array" then . else (.deployments // .result // .items // []) end)[]? | (.image_tag // empty)][0] // empty' <<<"${answer:-}")"
  if [ -z "$tag" ]; then
    fail "promote: could not read the tag staging runs"
    return 1
  fi
  ank application deploy "$APPLICATION_ID" --cluster "$production_id" --set "image_tag=$tag" >/dev/null || {
    fail "promote: the deploy to production was refused"
    return 1
  }
  pass "promote: $tag accepted on $PRODUCTION"
  production_address="${FLOW_PRODUCTION_ADDRESS:-}"
  [ -z "$production_address" ] || assert_https "$production_address"
}

# Prints the dependency files the branch changed; prints nothing until there are both.
fix_files() {
  local files
  files="$(gh api "repos/$FLOW_FIXTURE_REPOSITORY/compare/main...$1" --jq '.files[].filename' 2>/dev/null |
    grep -E '^(package\.json|package-lock\.json)$' | sort -u)"
  [ "$(printf '%s\n' "$files" | grep -c .)" -eq 2 ] && printf '%s\n' "$files"
}

slack_received() {
  local limit=600 elapsed=0
  while [ "$elapsed" -lt "$limit" ]; do
    curl --silent --max-time 30 -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
      "https://slack.com/api/conversations.history?channel=$FLOW_SLACK_CHANNEL_ID&oldest=$ALERT_START&limit=200" |
      jq -e --arg application "$1" '.messages[]? | select(tostring | contains($application))' >/dev/null && return 0
    sleep "${FLOW_POLL_SECONDS:-15}"
    elapsed=$((elapsed + ${FLOW_POLL_SECONDS:-15}))
  done
  return 1
}

assert_https() {
  local family
  for family in 4 6; do
    if curl --silent --fail --max-time 30 "-$family" "$1/healthz" >/dev/null; then
      pass "HTTPS answers on IPv$family"
    else
      fail "HTTPS answers on IPv$family: $1 did not"
    fi
  done
}

assert_data_survives_restart() {
  local address="$1" title="$2" cluster="$3" pod
  curl --silent --fail --max-time 30 -X POST -H 'Content-Type: application/json' \
    -d "$(jq -n --arg title "$title" '{title: $title}')" "$address/api/books" >/dev/null || {
    fail "data survives a restart: could not write a row"
    return
  }
  ank cluster select "$cluster" >/dev/null || fail "could not select $cluster"
  for pod in $(ank cluster get pods -n "$NAMESPACE" -o json | jq -r 'def rows: if type == "array" then . else (.pods // .result // .items // []) end; rows[]? | (.name // .metadata.name)'); do
    ank cluster exec "$pod" -n "$NAMESPACE" -- kill 1 >/dev/null 2>&1 || true
  done
  if wait_for 600 "the row after the restart" book_listed "$address" "$title" >/dev/null; then
    pass "data survives a restart"
  else
    fail "data survives a restart: $title is gone"
  fi
}

book_listed() { curl --silent --fail --max-time 30 "$1/api/books" | grep -F "$2"; }

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
