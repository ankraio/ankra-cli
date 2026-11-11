#!/bin/sh
# Tests install.sh against a fake release served from a file:// URL, with an
# isolated HOME and PATH, under every POSIX shell available (sh, and dash
# where it is installed). Nothing outside a temporary sandbox is touched:
# /usr/local/bin is replaced by ANKRA_INSTALL_SYSTEM_DIR and PATH holds only
# links to the tools the installer needs.
#
#   sh tools/test-install-sh.sh
#
# TEST_INSTALL_REQUIRE_DASH=1 makes a missing dash a failure (CI sets it).

# shellcheck disable=SC2016 # expected output and sh -c scripts hold a literal $
set -eu

repository=$(cd "$(dirname "$0")/.." && pwd)
installer=${TEST_INSTALL_SCRIPT:-"$repository/install.sh"}
temporary_root=${TMPDIR:-/tmp}
sandbox=$(mktemp -d "${temporary_root%/}/ankra-install-test.XXXXXX")
trap 'rm -rf "$sandbox"' EXIT

failures=0
checks=0

ok() {
	checks=$((checks + 1))
}

not_ok() {
	checks=$((checks + 1))
	failures=$((failures + 1))
	printf 'FAIL [%s] %s: %s\n' "$interpreter_name" "$case_name" "$1"
	if [ -f "$sandbox/out" ]; then
		sed 's/^/    | /' "$sandbox/out"
	fi
}

assert_status() {
	if [ "$status" = "$1" ]; then ok; else not_ok "exit status $status, want $1"; fi
}

assert_file() {
	if [ -f "$1" ]; then ok; else not_ok "missing file $1"; fi
}

assert_no_file() {
	if [ ! -e "$1" ] && [ ! -L "$1" ]; then ok; else not_ok "unexpected file $1"; fi
}

assert_output() {
	if grep -F -q -- "$1" "$sandbox/out"; then ok; else not_ok "output does not say: $1"; fi
}

assert_contains() {
	if grep -F -q -- "$2" "$1"; then ok; else not_ok "$1 does not contain: $2"; fi
}

assert_line_count() {
	count=$(grep -c -x -F -- "$2" "$1" || true)
	if [ "$count" = "$3" ]; then ok; else not_ok "$1 has $count lines '$2', want $3"; fi
}

assert_equal() {
	if [ "$1" = "$2" ]; then ok; else not_ok "$3: got '$1', want '$2'"; fi
}

assert_onboard_runs() {
	runs=0
	if [ -f "$onboard_log" ]; then
		runs=$(wc -l <"$onboard_log" | tr -d ' ')
	fi
	assert_equal "$runs" "$1" "ankra onboard runs"
}

sha256_line() {
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$(dirname "$1")" && sha256sum "$(basename "$1")")
	else
		(cd "$(dirname "$1")" && shasum -a 256 "$(basename "$1")")
	fi
}

# A fake ankra: answers --version, says whether it has `onboard`, and records
# each onboarding run (its own path) in FAKE_ONBOARD_LOG.
write_fake_ankra() {
	cat >"$1" <<EOF
#!/bin/sh
case "\${1:-}" in
--version)
	echo "ankra version $2"
	exit 0
	;;
onboard)
	if [ "$3" != onboard ]; then
		echo "Error: unknown command \"onboard\" for \"ankra\"" >&2
		exit 1
	fi
	if [ "\${2:-}" = --help ]; then
		echo "Sign up or in and set up"
		exit 0
	fi
	printf '%s\n' "\$0" >>"\$FAKE_ONBOARD_LOG"
	echo "fake onboard ran"
	exit "\${FAKE_ONBOARD_EXIT:-0}"
	;;
esac
exit 0
EOF
	chmod 0755 "$1"
}

# make_release DIR VERSION CHECKSUM-KIND [onboard|no-onboard]: a release with
# a binary for every OS and architecture. CHECKSUM-KIND is good, bad, missing
# or malformed.
make_release() {
	mkdir -p "$1"
	for os in darwin linux; do
		for arch in amd64 arm64; do
			asset="$1/ankra-cli-$os-$arch"
			write_fake_ankra "$asset" "$2" "${4:-onboard}"
			case "$3" in
			good) sha256_line "$asset" >"$asset.sha256" ;;
			bad) printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "ankra-cli-$os-$arch" >"$asset.sha256" ;;
			malformed) printf '<html>Not Found</html>\n' >"$asset.sha256" ;;
			missing) ;;
			esac
		done
	done
}

# The isolated PATH: links to the tools the installer uses, a uname that can
# pretend to be Windows, and a sudo that records being called.
tools="$sandbox/tools"
mkdir -p "$tools"
for tool in awk basename cat chmod cp curl dirname env ln mkdir mktemp mv pwd readlink rm sed sha256sum shasum openssl sysctl tr true xattr; do
	found=$(PATH="$PATH:/usr/sbin:/sbin" command -v "$tool" 2>/dev/null || true)
	case "$found" in
	/*) ln -s "$found" "$tools/$tool" ;;
	esac
done
real_uname=$(command -v uname)
cat >"$tools/uname" <<EOF
#!/bin/sh
if [ -n "\${FAKE_UNAME_S:-}" ] && [ "\${1:-}" = -s ]; then
	echo "\$FAKE_UNAME_S"
	exit 0
fi
exec "$real_uname" "\$@"
EOF
chmod 0755 "$tools/uname"
cat >"$tools/sudo" <<EOF
#!/bin/sh
touch "$sandbox/sudo-called"
exit 1
EOF
chmod 0755 "$tools/sudo"

make_release "$sandbox/release" "v9.9.9-test" good
make_release "$sandbox/release-bad" "v9.9.9-test" bad
make_release "$sandbox/release-missing" "v9.9.9-test" missing
make_release "$sandbox/release-malformed" "v9.9.9-test" malformed
make_release "$sandbox/release-old" "v0.1.0-old" good no-onboard
mkdir -p "$sandbox/tmp"

# Per-case state, set by new_case.
case_name=""
home=""
login_shell=""
extra_path=""
download_base=""
system_dir=""
onboard_log=""
status=0

new_case() {
	case_name=$1
	home="$sandbox/home-$interpreter_name-$(printf '%s' "$1" | tr -c 'A-Za-z0-9' '-')"
	mkdir -p "$home"
	login_shell=/bin/zsh
	extra_path=""
	download_base="file://$sandbox/release"
	system_dir="$sandbox/no-system-bin"
	onboard_log="$home/onboard.log"
	rm -f "$sandbox/sudo-called"
}

# installer_env runs "$@" with nothing from this environment but the
# case's variables. Extra NAME=value arguments may come first.
installer_env() {
	env -i HOME="$home" PATH="${extra_path:+$extra_path:}$tools" SHELL="$login_shell" \
		TMPDIR="$sandbox/tmp" ANKRA_DOWNLOAD_BASE="$download_base" \
		ANKRA_INSTALL_SYSTEM_DIR="$system_dir" FAKE_ONBOARD_LOG="$onboard_log" "$@"
}

# run_piped [NAME=value ...] [-- installer arguments]: the way people run
# it, `curl ... | sh -s -- ARGS`.
run_piped() {
	assignments=""
	while [ "$#" -gt 0 ] && [ "$1" != -- ]; do
		assignments="$assignments $1"
		shift
	done
	if [ "$#" -gt 0 ]; then
		shift
	fi
	status=0
	# shellcheck disable=SC2086 # NAME=value words without spaces
	installer_env $assignments "$interpreter" -s -- "$@" <"$installer" >"$sandbox/out" 2>&1 || status=$?
}

run_file() {
	status=0
	installer_env "$interpreter" "$installer" "$@" </dev/null >"$sandbox/out" 2>&1 || status=$?
}

assert_no_sudo() {
	if [ -e "$sandbox/sudo-called" ]; then not_ok "sudo was called"; else ok; fi
}

assert_temp_cleaned() {
	leftovers=$(find "$sandbox/tmp" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')
	assert_equal "$leftovers" 0 "files left in TMPDIR"
}

# path_after_sourcing FILE: PATH after a POSIX shell reads FILE twice.
path_after_sourcing() {
	env -i HOME="$home" PATH=/usr/bin:/bin /bin/sh -c '. "$1"; . "$1"; printf "%s" "$PATH"' sh "$1"
}

run_suite() {
	block_start="# >>> ankra installer >>>"
	block_end="# <<< ankra installer <<<"

	new_case "installs into ~/.ankra/bin without sudo, then onboards"
	printf 'export EDITOR=vi\n' >"$home/.zshrc"
	run_piped
	assert_status 0
	assert_file "$home/.ankra/bin/ankra"
	if [ -x "$home/.ankra/bin/ankra" ]; then ok; else not_ok "the binary is not executable"; fi
	assert_no_sudo
	assert_line_count "$home/.zshrc" "$block_start" 1
	assert_line_count "$home/.zshrc" "$block_end" 1
	assert_line_count "$home/.zshenv" "$block_start" 1
	assert_contains "$home/.zshrc" "export EDITOR=vi"
	assert_equal "$(sed -n 1p "$home/.zshrc")" "$block_start" "first line of .zshrc"
	if [ -L "$home/.local/bin/ankra" ]; then ok; else not_ok "no ~/.local/bin/ankra link"; fi
	assert_equal "$(readlink "$home/.local/bin/ankra" 2>/dev/null || true)" "$home/.ankra/bin/ankra" "link target of ~/.local/bin/ankra"
	assert_output 'export PATH="$HOME/.ankra/bin:$PATH"'
	assert_output "Installed ankra version v9.9.9-test to $home/.ankra/bin/ankra"
	assert_onboard_runs 1
	assert_equal "$(cat "$onboard_log" 2>/dev/null || true)" "$home/.ankra/bin/ankra" "binary that ran onboarding"
	summary_line=$(grep -n -F "Installed ankra version" "$sandbox/out" | cut -d: -f1 | head -n 1)
	onboard_line=$(grep -n -F "fake onboard ran" "$sandbox/out" | cut -d: -f1 | head -n 1)
	if [ -n "$summary_line" ] && [ -n "$onboard_line" ] && [ "$summary_line" -lt "$onboard_line" ]; then
		ok
	else
		not_ok "the install summary must come before onboarding"
	fi
	assert_temp_cleaned

	case_name="a second run writes the PATH block once and respects --no-onboard"
	run_piped -- --no-onboard
	assert_status 0
	assert_line_count "$home/.zshrc" "$block_start" 1
	assert_line_count "$home/.zshrc" "$block_end" 1
	assert_line_count "$home/.zshenv" "$block_start" 1
	assert_contains "$home/.zshrc" "export EDITOR=vi"
	assert_onboard_runs 1
	assert_output "ankra onboard"
	assert_equal "$(readlink "$home/.local/bin/ankra" 2>/dev/null || true)" "$home/.ankra/bin/ankra" "link target of ~/.local/bin/ankra"
	assert_equal "$(path_after_sourcing "$home/.zshrc")" "$home/.ankra/bin:/usr/bin:/bin" "PATH after sourcing .zshrc twice"
	assert_temp_cleaned

	new_case "ANKRA_NO_ONBOARD=1 skips onboarding; bash writes .bashrc and .bash_profile"
	login_shell=/bin/bash
	printf '# profile\n' >"$home/.bash_profile"
	run_piped ANKRA_NO_ONBOARD=1
	assert_status 0
	assert_onboard_runs 0
	assert_line_count "$home/.bashrc" "$block_start" 1
	assert_line_count "$home/.bash_profile" "$block_start" 1
	assert_contains "$home/.bash_profile" "# profile"
	assert_no_file "$home/.profile"

	new_case "other shells write .profile"
	login_shell=/bin/sh
	run_piped -- --no-onboard
	assert_status 0
	assert_line_count "$home/.profile" "$block_start" 1
	assert_no_file "$home/.zshrc"

	new_case "--no-modify-path leaves startup files alone"
	login_shell=/bin/sh
	run_piped -- --no-onboard --no-modify-path
	assert_status 0
	assert_file "$home/.ankra/bin/ankra"
	assert_no_file "$home/.profile"
	assert_output 'export PATH="$HOME/.ankra/bin:$PATH"'

	new_case "a checksum mismatch is refused"
	download_base="file://$sandbox/release-bad"
	run_piped
	assert_status 1
	assert_output "checksum mismatch"
	assert_no_file "$home/.ankra/bin/ankra"
	assert_onboard_runs 0
	assert_temp_cleaned

	new_case "a missing checksum is refused"
	download_base="file://$sandbox/release-missing"
	run_piped
	assert_status 1
	assert_output "refusing to install"
	assert_no_file "$home/.ankra/bin/ankra"
	assert_onboard_runs 0

	new_case "a checksum file that is not a checksum is refused"
	download_base="file://$sandbox/release-malformed"
	run_piped
	assert_status 1
	assert_output "is not a SHA-256 checksum"
	assert_no_file "$home/.ankra/bin/ankra"

	new_case "the exit status is onboarding's"
	run_piped FAKE_ONBOARD_EXIT=7
	assert_status 7
	assert_onboard_runs 1

	new_case "a Homebrew ankra is left alone and onboards"
	mkdir -p "$sandbox/brew-$interpreter_name/Cellar/ankra/1.0/bin" "$sandbox/brew-$interpreter_name/bin"
	brew_binary="$sandbox/brew-$interpreter_name/Cellar/ankra/1.0/bin/ankra"
	write_fake_ankra "$brew_binary" "v1.0.0-brew" onboard
	ln -sf ../Cellar/ankra/1.0/bin/ankra "$sandbox/brew-$interpreter_name/bin/ankra"
	extra_path="$sandbox/brew-$interpreter_name/bin"
	run_piped
	assert_status 0
	assert_output "ankra is managed by Homebrew; update it with: brew upgrade ankra"
	assert_contains "$brew_binary" "v1.0.0-brew"
	assert_no_file "$home/.ankra/bin/ankra"
	assert_onboard_runs 1
	assert_equal "$(cat "$onboard_log" 2>/dev/null || true)" "$sandbox/brew-$interpreter_name/bin/ankra" "binary that ran onboarding"

	new_case "an existing ankra in a writable directory is replaced in place"
	mkdir -p "$sandbox/opt-$interpreter_name/bin"
	write_fake_ankra "$sandbox/opt-$interpreter_name/bin/ankra" "v0.0.1-old" onboard
	extra_path="$sandbox/opt-$interpreter_name/bin"
	run_piped -- --no-onboard
	assert_status 0
	assert_contains "$sandbox/opt-$interpreter_name/bin/ankra" "v9.9.9-test"
	assert_no_file "$home/.ankra/bin/ankra"
	assert_no_file "$home/.zshrc"
	assert_no_sudo

	new_case "a writable /usr/local/bin is used"
	system_dir="$sandbox/usr-local-bin-$interpreter_name"
	mkdir -p "$system_dir"
	run_piped -- --no-onboard
	assert_status 0
	assert_file "$system_dir/ankra"
	assert_no_file "$home/.ankra/bin/ankra"
	assert_no_file "$home/.local/bin/ankra"

	new_case "--install-dir with a space in it"
	custom="$sandbox/custom bin $interpreter_name"
	run_piped -- --no-onboard --install-dir "$custom"
	assert_status 0
	assert_file "$custom/ankra"
	assert_equal "$(path_after_sourcing "$home/.zshrc")" "$custom:/usr/bin:/bin" "PATH after sourcing .zshrc twice"

	new_case "Windows points at install.ps1"
	run_piped FAKE_UNAME_S=MINGW64_NT-10.0
	assert_status 1
	assert_output "irm https://ankra.ai/install.ps1 | iex"
	assert_no_file "$home/.ankra/bin/ankra"

	new_case "an unknown option is refused"
	run_piped -- --bogus
	assert_status 1
	assert_output "unknown option: --bogus"

	new_case "a release without onboard installs and says how to sign in"
	download_base="file://$sandbox/release-old"
	run_piped
	assert_status 0
	assert_file "$home/.ankra/bin/ankra"
	assert_output "has no onboard command"
	assert_onboard_runs 0

	new_case "runs from a file as well as from a pipe"
	run_file --no-onboard
	assert_status 0
	assert_file "$home/.ankra/bin/ankra"
}

interpreters="/bin/sh"
dash_path=$(command -v dash 2>/dev/null || true)
if [ -n "$dash_path" ]; then
	interpreters="$interpreters $dash_path"
elif [ "${TEST_INSTALL_REQUIRE_DASH:-}" = 1 ]; then
	echo "FAIL: dash is not installed and TEST_INSTALL_REQUIRE_DASH=1"
	exit 1
else
	echo "note: dash is not installed, so only /bin/sh is tested"
fi

for interpreter in $interpreters; do
	interpreter_name=$(basename "$interpreter")
	run_suite
done

if [ "$failures" -gt 0 ]; then
	echo "install.sh: $failures of $checks checks failed"
	exit 1
fi
echo "install.sh: all $checks checks passed ($interpreters)"
