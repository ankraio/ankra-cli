#!/bin/sh
# Tags the release the newest CHANGELOG.md version heading names, on the
# commit a push to master built. The pipeline's release-tag stage runs it
# after every other stage passed; the tag it creates starts
# .github/workflows/release.yml, which builds the binaries, creates the GitHub
# release and publishes the Homebrew formula.
#
#   sh scripts/release-tag.sh
#
# It reads the first `## vX.Y.Z - YYYY-MM-DD` heading after `## Unreleased`
# and asks GitHub for the tag:
#   - the tag exists: the release is out, nothing to do (every push that is
#     not a release ends here);
#   - GitHub answers 404: it creates an annotated tag on RELEASE_TAG_COMMIT
#     through the API (git/tags, then git/refs), unless an existing v* tag is
#     as new or newer, which it refuses;
#   - any other answer: it fails and creates nothing.
# It never moves, deletes or recreates a tag.
#
# The token is the organisation's GitHub App installation token the pipeline
# delivers to the step. A tag created with it starts workflows; one created
# with a workflow's own GITHUB_TOKEN would not.
#
# Environment:
#   RELEASE_TAG_EVENT            the run's event; only "push" tags
#   RELEASE_TAG_REF              the run's ref; only refs/heads/master tags
#   RELEASE_TAG_COMMIT           the full sha the run built and tested
#   RELEASE_TAG_REPOSITORY       owner/name on GitHub
#   RELEASE_TAG_CREDENTIAL_FILE  the delivered GitHub login (JSON with "token")
#   RELEASE_TAG_CHANGELOG        default CHANGELOG.md
#   RELEASE_TAG_API_URL          default https://api.github.com

set -eu

fail() {
	printf 'FAILED: %s\n' "$*" >&2
	exit 1
}

event=${RELEASE_TAG_EVENT:-}
ref=${RELEASE_TAG_REF:-}
commit=${RELEASE_TAG_COMMIT:-}
repository=${RELEASE_TAG_REPOSITORY:-}
credential_file=${RELEASE_TAG_CREDENTIAL_FILE:-}
changelog=${RELEASE_TAG_CHANGELOG:-CHANGELOG.md}
api_url=${RELEASE_TAG_API_URL:-https://api.github.com}

if [ "$event" != "push" ] || [ "$ref" != "refs/heads/master" ]; then
	fail "a release is tagged only by a push to master; this run is event '$event' on '$ref'."
fi
if ! printf '%s\n' "$commit" | grep -Eq '^[0-9a-f]{40}$'; then
	fail "RELEASE_TAG_COMMIT must be the full commit sha the run built, not '$commit'."
fi
if ! printf '%s\n' "$repository" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'; then
	fail "RELEASE_TAG_REPOSITORY must be owner/name, not '$repository'."
fi
if [ ! -r "$changelog" ]; then
	fail "cannot read $changelog."
fi

heading=$(awk '
	/^## Unreleased[ \t]*$/ { next }
	/^## / { print; exit }
' "$changelog")
if [ -z "$heading" ]; then
	fail "$changelog has no version heading after ## Unreleased."
fi
if ! printf '%s\n' "$heading" | grep -Eq '^## v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*) (-|—) [0-9]{4}-[0-9]{2}-[0-9]{2}$'; then
	fail "the newest heading in $changelog is '$heading'; a release heading reads '## vX.Y.Z - YYYY-MM-DD'."
fi
tag=${heading#"## "}
tag=${tag%% *}
version=${tag#v}
echo "Newest release heading in $changelog: $tag"

if [ -z "$credential_file" ] || [ ! -r "$credential_file" ]; then
	fail "no GitHub login was delivered to this step (RELEASE_TAG_CREDENTIAL_FILE '$credential_file'), so $tag cannot be looked up or tagged."
fi
token=$(grep -o '"token": *"[^"]*"' "$credential_file" | cut -d'"' -f4 || true)
if [ -z "$token" ]; then
	fail "the GitHub login delivered to this step carries no token."
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/release-tag.XXXXXX")
trap 'rm -rf "$work"' EXIT
umask 077
printf 'Authorization: Bearer %s\n' "$token" >"$work/authorization"
unset token
response="$work/response"

# api METHOD PATH [JSON]: sets http_status and leaves the body in $response.
# A request curl could not complete reads as status 000.
api() {
	: >"$response"
	if [ $# -ge 3 ]; then
		http_status=$(curl --silent --show-error --retry 3 --max-time 60 \
			--request "$1" \
			--header "@$work/authorization" \
			--header "Accept: application/vnd.github+json" \
			--header "X-GitHub-Api-Version: 2022-11-28" \
			--header "Content-Type: application/json" \
			--data "$3" \
			--output "$response" \
			--write-out '%{http_code}' \
			"$api_url$2") || http_status=000
	else
		http_status=$(curl --silent --show-error --retry 3 --max-time 60 \
			--request "$1" \
			--header "@$work/authorization" \
			--header "Accept: application/vnd.github+json" \
			--header "X-GitHub-Api-Version: 2022-11-28" \
			--output "$response" \
			--write-out '%{http_code}' \
			"$api_url$2") || http_status=000
	fi
}

answer() {
	printf 'HTTP %s: %s' "$http_status" "$(head -c 300 "$response" 2>/dev/null | tr '\n' ' ')"
}

api GET "/repos/$repository/git/ref/tags/$tag"
case "$http_status" in
	200)
		echo "$tag already released"
		exit 0
		;;
	404) ;;
	*) fail "looking up tag $tag answered $(answer); nothing was tagged." ;;
esac

api GET "/repos/$repository/git/matching-refs/tags/v"
if [ "$http_status" != "200" ]; then
	fail "listing the existing v* tags answered $(answer); nothing was tagged."
fi
not_older=$(grep -o '"ref": *"refs/tags/v[^"]*"' "$response" | sed 's/.*"refs\/tags\///; s/"$//' | awk -v candidate="$version" '
	BEGIN { split(candidate, wanted, ".") }
	$0 ~ /^v[0-9]+\.[0-9]+\.[0-9]+(-.+)?$/ {
		core = substr($0, 2)
		prerelease = ""
		separator = index(core, "-")
		if (separator > 0) {
			prerelease = substr(core, separator + 1)
			core = substr(core, 1, separator - 1)
		}
		split(core, existing, ".")
		order = 0
		for (part = 1; part <= 3 && order == 0; part++) {
			if (wanted[part] + 0 > existing[part] + 0) order = 1
			else if (wanted[part] + 0 < existing[part] + 0) order = -1
		}
		if (order == 0 && prerelease != "") order = 1
		if (order <= 0) print $0
	}
')
if [ -n "$not_older" ]; then
	fail "refusing to tag $tag: existing tag(s) $(printf '%s\n' "$not_older" | tr '\n' ' ')are not older, so the newest heading in $changelog is not a new release."
fi

tagged_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
api POST "/repos/$repository/git/tags" "$(printf '{"tag":"%s","message":"%s\\n\\nTagged by the ankra-cli pipeline on the merge to master that carries the %s heading in CHANGELOG.md.\\n","object":"%s","type":"commit","tagger":{"name":"ankra-release-bot","email":"hello@ankra.ai","date":"%s"}}' "$tag" "$tag" "$tag" "$commit" "$tagged_at")"
if [ "$http_status" != "201" ]; then
	fail "creating the tag object for $tag answered $(answer); no tag ref was created."
fi
tag_object=$(grep -Eo '"sha": *"[0-9a-f]{40}"' "$response" | grep -Eo '[0-9a-f]{40}' | grep -v "^$commit\$" | sort -u)
if [ "$(printf '%s\n' "$tag_object" | grep -c .)" != "1" ]; then
	fail "GitHub's answer to creating the tag object for $tag names no single tag sha; no tag ref was created."
fi

api POST "/repos/$repository/git/refs" "$(printf '{"ref":"refs/tags/%s","sha":"%s"}' "$tag" "$tag_object")"
case "$http_status" in
	201)
		echo "Tagged $tag on $commit; .github/workflows/release.yml publishes it."
		;;
	422)
		api GET "/repos/$repository/git/ref/tags/$tag"
		if [ "$http_status" = "200" ]; then
			echo "$tag already released (another run tagged it while this one ran)"
			exit 0
		fi
		fail "creating tag $tag was refused as unprocessable and the tag still cannot be read ($(answer))."
		;;
	*) fail "creating tag $tag answered $(answer)." ;;
esac
