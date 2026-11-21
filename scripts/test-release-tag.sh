#!/bin/sh
# Tests scripts/release-tag.sh against a fake curl that answers from per-case
# response files and records every request, so nothing reaches GitHub.
#
#   sh scripts/test-release-tag.sh

# shellcheck disable=SC2016 # the fake curl's script holds a literal $
set -eu

repository=$(cd "$(dirname "$0")/.." && pwd)
script="$repository/scripts/release-tag.sh"
temporary_root=${TMPDIR:-/tmp}
sandbox=$(mktemp -d "${temporary_root%/}/release-tag-test.XXXXXX")
trap 'rm -rf "$sandbox"' EXIT

commit=0123456789abcdef0123456789abcdef01234567
tag_object=fedcba9876543210fedcba9876543210fedcba98
api=https://api.test
repo_path=/repos/ankraio/ankra-cli

failures=0
checks=0

ok() {
	checks=$((checks + 1))
}

not_ok() {
	checks=$((checks + 1))
	failures=$((failures + 1))
	printf 'FAIL %s: %s\n' "$case_name" "$1"
	sed 's/^/    | /' "$sandbox/out"
	if [ -f "$case_dir/requests" ]; then
		sed 's/^/    > /' "$case_dir/requests"
	fi
}

assert_status() {
	if [ "$status" = "$1" ]; then ok; else not_ok "exit status $status, want $1"; fi
}

assert_output() {
	if grep -F -q -- "$1" "$sandbox/out"; then ok; else not_ok "output does not say: $1"; fi
}

assert_requested() {
	if grep -F -q -- "$1" "$case_dir/requests"; then ok; else not_ok "no request: $1"; fi
}

assert_not_requested() {
	if [ -f "$case_dir/requests" ] && grep -F -q -- "$1" "$case_dir/requests"; then
		not_ok "unexpected request: $1"
	else
		ok
	fi
}

mkdir -p "$sandbox/bin"
cat >"$sandbox/bin/curl" <<'EOF'
#!/bin/sh
# Answers from $FAKE_CURL_DIR/<METHOD><path with / as _>.status and .body; a
# second call to the same request reads .2.status/.2.body when they exist. A
# status of "unreachable" fails as curl does when it cannot connect.
method=GET
output=/dev/null
data=
authorization=
url=
while [ $# -gt 0 ]; do
	case "$1" in
	--request) method=$2; shift 2 ;;
	--output) output=$2; shift 2 ;;
	--data) data=$2; shift 2 ;;
	--header)
		case "$2" in @*) authorization=$(cat "${2#@}") ;; esac
		shift 2
		;;
	--write-out | --retry | --max-time) shift 2 ;;
	--*) shift ;;
	*) url=$1; shift ;;
	esac
done
path=${url#"$FAKE_API_URL"}
key="$method$(printf '%s' "$path" | tr '/' '_')"
count_file="$FAKE_CURL_DIR/$key.count"
count=$(($(cat "$count_file" 2>/dev/null || echo 0) + 1))
echo "$count" >"$count_file"
printf '%s %s %s [%s]\n' "$method" "$path" "$data" "$authorization" >>"$FAKE_CURL_DIR/requests"
answer="$FAKE_CURL_DIR/$key"
if [ "$count" -gt 1 ] && [ -f "$answer.$count.status" ]; then
	answer="$answer.$count"
fi
if [ ! -f "$answer.status" ]; then
	echo "curl: (7) no fake answer for $method $path" >&2
	printf '000'
	exit 7
fi
answer_status=$(cat "$answer.status")
if [ "$answer_status" = unreachable ]; then
	echo "curl: (7) Failed to connect" >&2
	printf '000'
	exit 7
fi
if [ -f "$answer.body" ]; then cp "$answer.body" "$output"; else : >"$output"; fi
printf '%s' "$answer_status"
EOF
chmod +x "$sandbox/bin/curl"

printf '{"provider":"github","username":"x-access-token","token":"installation-token","expires_at":"2099-01-01T00:00:00Z"}\n' >"$sandbox/credential"

case_number=0
new_case() {
	case_name=$1
	case_number=$((case_number + 1))
	case_dir="$sandbox/case-$case_number"
	mkdir -p "$case_dir"
	event=push
	ref=refs/heads/master
	cat >"$case_dir/CHANGELOG.md" <<'EOF'
# Ankra CLI Changelog

## Unreleased

### Added

- **Something not released yet.**

## v1.3.0 - 2026-11-22

The release.

## v1.2.0 - 2026-11-20

The one before.
EOF
}

# respond METHOD PATH STATUS [BODY] [CALL]
respond() {
	file="$case_dir/$1$(printf '%s' "$2" | tr '/' '_')"
	if [ -n "${5:-}" ]; then file="$file.$5"; fi
	printf '%s' "$3" >"$file.status"
	if [ -n "${4:-}" ]; then printf '%s' "$4" >"$file.body"; fi
}

existing_tags() {
	body="["
	separator=""
	for existing in "$@"; do
		body="$body$separator{\"ref\":\"refs/tags/$existing\",\"object\":{\"sha\":\"1111111111111111111111111111111111111111\",\"type\":\"tag\"}}"
		separator=","
	done
	respond GET "$repo_path/git/matching-refs/tags/v" 200 "$body]"
}

tag_created() {
	respond POST "$repo_path/git/tags" 201 "{\"node_id\":\"TAG\",\"tag\":\"v1.3.0\",\"sha\":\"$tag_object\",\"message\":\"v1.3.0\",\"object\":{\"sha\":\"$commit\",\"type\":\"commit\"}}"
}

run() {
	status=0
	(
		cd "$case_dir"
		PATH="$sandbox/bin:$PATH" \
			FAKE_CURL_DIR="$case_dir" FAKE_API_URL="$api" \
			RELEASE_TAG_API_URL="$api" \
			RELEASE_TAG_EVENT="$event" RELEASE_TAG_REF="$ref" \
			RELEASE_TAG_COMMIT="$commit" RELEASE_TAG_REPOSITORY=ankraio/ankra-cli \
			RELEASE_TAG_CREDENTIAL_FILE="$sandbox/credential" \
			TMPDIR="$sandbox" \
			sh "$script"
	) >"$sandbox/out" 2>&1 || status=$?
}

new_case "the newest heading after Unreleased is tagged when GitHub answers 404"
respond GET "$repo_path/git/ref/tags/v1.3.0" 404 '{"message":"Not Found"}'
existing_tags v1.2.0 v1.1.0 v1.3.0-rc1 v0.9.0-alpha
tag_created
respond POST "$repo_path/git/refs" 201 '{"ref":"refs/tags/v1.3.0"}'
run
assert_status 0
assert_output "Newest release heading in CHANGELOG.md: v1.3.0"
assert_output "Tagged v1.3.0 on $commit"
assert_requested "GET $repo_path/git/ref/tags/v1.3.0  [Authorization: Bearer installation-token]"
assert_requested "POST $repo_path/git/tags {\"tag\":\"v1.3.0\","
assert_requested "\"object\":\"$commit\",\"type\":\"commit\""
assert_requested "POST $repo_path/git/refs {\"ref\":\"refs/tags/v1.3.0\",\"sha\":\"$tag_object\"}"
assert_not_requested "PATCH"
assert_not_requested "DELETE"

new_case "an existing tag is a release already out, and nothing is written"
respond GET "$repo_path/git/ref/tags/v1.3.0" 200 '{"ref":"refs/tags/v1.3.0"}'
run
assert_status 0
assert_output "v1.3.0 already released"
assert_not_requested "POST"
assert_not_requested "matching-refs"

for failing in 401 403 500 502; do
	new_case "a lookup answering $failing fails and tags nothing"
	respond GET "$repo_path/git/ref/tags/v1.3.0" "$failing" '{"message":"no"}'
	run
	assert_status 1
	assert_output "looking up tag v1.3.0 answered HTTP $failing"
	assert_not_requested "POST"
done

new_case "a lookup curl cannot complete fails and tags nothing"
respond GET "$repo_path/git/ref/tags/v1.3.0" unreachable
run
assert_status 1
assert_output "looking up tag v1.3.0 answered HTTP 000"
assert_not_requested "POST"

new_case "a tag list GitHub will not give fails and tags nothing"
respond GET "$repo_path/git/ref/tags/v1.3.0" 404
respond GET "$repo_path/git/matching-refs/tags/v" 503
run
assert_status 1
assert_output "listing the existing v* tags answered HTTP 503"
assert_not_requested "POST"

new_case "a heading not newer than an existing tag is refused"
respond GET "$repo_path/git/ref/tags/v1.3.0" 404
existing_tags v1.2.0 v1.4.0 v1.10.0-rc1
run
assert_status 1
assert_output "refusing to tag v1.3.0: existing tag(s) v1.4.0 v1.10.0-rc1 are not older"
assert_not_requested "POST"

new_case "versions compare as numbers, not text"
cat >"$case_dir/CHANGELOG.md" <<'EOF'
## Unreleased

## v0.10.0 - 2026-11-22
EOF
respond GET "$repo_path/git/ref/tags/v0.10.0" 404
existing_tags v0.9.0 v0.9.10 v0.10.0-rc2
respond POST "$repo_path/git/tags" 201 "{\"sha\":\"$tag_object\",\"object\":{\"sha\":\"$commit\"}}"
respond POST "$repo_path/git/refs" 201
run
assert_status 0
assert_output "Tagged v0.10.0 on $commit"

new_case "the older em dash heading style is read"
cat >"$case_dir/CHANGELOG.md" <<'EOF'
## Unreleased

## v1.3.0 — 2026-10-20
EOF
respond GET "$repo_path/git/ref/tags/v1.3.0" 200
run
assert_status 0
assert_output "v1.3.0 already released"

new_case "a tag another run created first is accepted, not recreated"
respond GET "$repo_path/git/ref/tags/v1.3.0" 404
existing_tags v1.2.0
tag_created
respond POST "$repo_path/git/refs" 422 '{"message":"Reference already exists"}'
respond GET "$repo_path/git/ref/tags/v1.3.0" 200 '{"ref":"refs/tags/v1.3.0"}' 2
run
assert_status 0
assert_output "v1.3.0 already released (another run tagged it while this one ran)"

new_case "a refused tag ref fails"
respond GET "$repo_path/git/ref/tags/v1.3.0" 404
existing_tags v1.2.0
tag_created
respond POST "$repo_path/git/refs" 403 '{"message":"Resource not accessible by integration"}'
run
assert_status 1
assert_output "creating tag v1.3.0 answered HTTP 403"

for heading in "## v1.3 - 2026-11-22" "## v1.3.0" "## 1.3.0 - 2026-11-22" "## v01.3.0 - 2026-11-22" "## v1.3.0-rc1 - 2026-11-22" "## Notes"; do
	new_case "the malformed heading '$heading' fails"
	printf '## Unreleased\n\n%s\n\n## v1.2.0 - 2026-11-20\n' "$heading" >"$case_dir/CHANGELOG.md"
	run
	assert_status 1
	assert_output "a release heading reads '## vX.Y.Z - YYYY-MM-DD'"
	assert_not_requested "GET"
done

new_case "a changelog with only Unreleased fails"
printf '# Ankra CLI Changelog\n\n## Unreleased\n\n- **Pending.**\n' >"$case_dir/CHANGELOG.md"
run
assert_status 1
assert_output "has no version heading after ## Unreleased"

new_case "a pull request run never tags"
event=pull_request
run
assert_status 1
assert_output "a release is tagged only by a push to master"
assert_not_requested "GET"

new_case "a push to another branch never tags"
ref=refs/heads/feature
run
assert_status 1
assert_output "a release is tagged only by a push to master"
assert_not_requested "GET"

if [ "$failures" -gt 0 ]; then
	echo "release-tag.sh: $failures of $checks checks failed"
	exit 1
fi
echo "release-tag.sh: all $checks checks passed"
