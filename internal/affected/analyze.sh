# analyze.sh - affected detection over a checked-out repository
# (enginekit/pipelineaffected, ankra-efrnzf.19).
#
# Usage: sh analyze.sh <repository root> <data directory> <outputs file>
#
# The data directory and the outputs file are absolute paths: the analysis
# runs from the repository root, because graph.awk opens the repository's
# own go.mod and package.json files by their repository-relative names.
#
# The data directory holds graph.awk, "changed" (one repository-relative path
# per line) and "targets" (one "<key><TAB><go|pnpm><TAB><pattern>" line per
# target). One "ankra.affected.<key>=true|false" line per key is appended to
# the outputs file, all of them at the end and only on success: any failure
# exits non-zero having written none, and a key with no line runs, so the
# caller never has to tell a broken analysis from an affected stage.
#
# POSIX sh, git, awk and grep only: it runs in the checkout image (alpine/git,
# busybox) and on a laptop. Every git command takes its configuration from
# here and not from the repository, for the reason the checkout's own git
# does (enginekit/pipelinesteps/checkout.go, ankra-gff55z).

root=$1
data=$2
outputs=$3

cd "$root" || exit 2

affected_git() {
	GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git \
		-c core.quotePath=false -c core.fsmonitor=false -c core.hooksPath=/dev/null \
		-c safe.directory='*' "$@"
}

# The root must be the top of a work tree: every path the analysis reads and
# every changed file is relative to it, so a directory inside some other
# repository (or no repository at all) is refused rather than half-read.
prefix=$(affected_git rev-parse --show-prefix) || exit 2
[ -z "$prefix" ] || exit 2
affected_git ls-files > "$data/files" || exit 2
awk -v mode=modules -f "$data/graph.awk" "$data/files" > "$data/modules" || exit 2
: > "$data/edges"
if grep -q "$(printf '\tgo\t')" "$data/targets" && [ -s "$data/modules" ]; then
	expression=$(awk -v mode=regex -f "$data/graph.awk" "$data/modules") || exit 2
	if [ -n "$expression" ]; then
		affected_git grep -I -o -E \
			-e "\"($expression)(/[^\"]*)?\"" \
			-e "\`($expression)(/[^\`]*)?\`" \
			-- '*.go' > "$data/edges"
		status=$?
		# git grep answers 1 for no match at all, which is a repository
		# whose packages import nothing of each other.
		[ "$status" -le 1 ] || exit 2
	fi
fi
# The answers go to a private file first and reach the outputs file only once
# awk has exited cleanly, so an analysis killed part way (the time limit,
# memory) never leaves some answers, or half of one, behind.
: > "$data/answers"
awk -v mode=analyze -v out="$data/answers" -f "$data/graph.awk" \
	"$data/files" "$data/modules" "$data/edges" "$data/changed" "$data/targets" || exit 2
cat "$data/answers" >> "$outputs"
