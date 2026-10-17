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
# the outputs file. Any failure exits non-zero having written nothing for the
# keys it did not reach, and a key with no line runs: the caller never has to
# tell a broken analysis from an affected stage.
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
awk -v mode=analyze -v out="$outputs" -f "$data/graph.awk" \
	"$data/files" "$data/modules" "$data/edges" "$data/changed" "$data/targets"
